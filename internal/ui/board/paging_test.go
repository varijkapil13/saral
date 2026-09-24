package board

import (
	"sync"
	"testing"
	"time"

	"github.com/varijkapil13/saral/internal/app"
	"github.com/varijkapil13/saral/internal/ui/kernel"
	"github.com/varijkapil13/saral/pkg/jira"
	"github.com/varijkapil13/saral/pkg/jira/jiratest"
)

// pageCache is fakeCache that also keeps a board a page at a time, and
// records what each page write carried.
type pageCache struct {
	*fakeCache
	mu    sync.Mutex
	pages []pageWrite
}

type pageWrite struct {
	first bool
	cards int
	more  bool
}

var _ app.BoardPageCache = (*pageCache)(nil)

func (c *pageCache) PutBoardPage(boardID int64, page app.BoardSnapshot, first bool) error {
	c.mu.Lock()
	c.pages = append(c.pages, pageWrite{first: first, cards: len(page.Issues), more: page.More})
	c.mu.Unlock()
	held, _ := c.Board(boardID)
	if first {
		held = page
	} else {
		held.Config, held.QuickFilters, held.More = page.Config, page.QuickFilters, page.More
		held.Sprints, held.Sprint, held.NoSprints = page.Sprints, page.Sprint, page.NoSprints
		held.Issues = append(held.Issues, page.Issues...)
	}
	return c.PutBoard(boardID, held)
}

func (c *pageCache) writes() []pageWrite {
	c.mu.Lock()
	defer c.mu.Unlock()
	return append([]pageWrite(nil), c.pages...)
}

func TestBoard_EachPageIsStoredOnItsOwnAndOffTheUpdateLoop(t *testing.T) {
	t.Parallel()
	cache := &pageCache{fakeCache: newFakeCache()}
	fake := newFake(24, jiratest.WithPageSize(5))
	dr := newDriver(t, withCache(testDeps(fake), cache), 120, 20)

	got := cache.writes()
	cardWrites := got[:0:0]
	for _, w := range got {
		if w.cards > 0 {
			cardWrites = append(cardWrites, w)
		}
	}
	if len(cardWrites) != 5 {
		t.Fatalf("%d page writes carried cards, want one per page of five: %+v", len(cardWrites), got)
	}
	for i, w := range cardWrites {
		if w.first != (i == 0) {
			t.Errorf("write %d says first = %v; only the first page replaces what was stored", i, w.first)
		}
		if w.cards > 5 {
			t.Errorf("write %d carried %d cards, more than its own page", i, w.cards)
		}
	}
	if last := cardWrites[len(cardWrites)-1]; last.more {
		t.Error("the last page was stored as though there were more to come")
	}
	snap, ok := cache.Board(dr.m.plan.boardID)
	if !ok || len(snap.Issues) != 24 || snap.Sprint != dr.m.sprint.ID {
		t.Errorf("the stored board holds %d cards on sprint %d, want 24 on %d", len(snap.Issues), snap.Sprint, dr.m.sprint.ID)
	}

	before := len(cache.writes())
	_, cmd := dr.m.Update(issuesMsg{gen: dr.m.gen, first: true, page: jira.Page[jira.Issue]{Items: dr.m.issues[:3]}})
	if n := len(cache.writes()); n != before {
		t.Error("a page was written to the cache inside Update, which is the frame loop")
	}
	dr.run(cmd)
	if n := len(cache.writes()); n != before+1 {
		t.Errorf("the page's command wrote %d times, want once", n-before)
	}
}

func TestBoard_AStoredBoardCutShortIsReadAgain(t *testing.T) {
	t.Parallel()
	boardID, cfg, qf, issues := primed(t, testDeps(newFake(6)))
	cache := newFakeCache()
	cache.hold("PROJ", boardID, app.BoardSnapshot{Config: cfg, QuickFilters: qf, Issues: issues[:3], More: true}, false)

	fake := newFake(6)
	dr := newDriver(t, withCache(testDeps(fake), cache), 120, 20)

	if got := countCalls(fake, "Boards"); got == 0 {
		t.Error("a stored board that had more to read was taken as the whole board")
	}
	if len(dr.m.issues) != 6 {
		t.Errorf("the board holds %d cards, want all 6 once it was read again", len(dr.m.issues))
	}
}

// A board left open goes on being drawn from what it read, so once that is
// older than a stored board is trusted for it says so, and coming back to it
// reads it again.
func TestBoard_AnOpenBoardGoesStaleAndIsReadAgainWhenItComesBack(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, time.March, 5, 9, 0, 0, 0, time.UTC)
	var mu sync.Mutex
	clock := func() time.Time {
		mu.Lock()
		defer mu.Unlock()
		return now
	}
	fake := newFake(6)
	d := testDeps(fake)
	d.Now = clock
	dr := newDriver(t, d, 120, 20)
	mustNotContain(t, dr.view(), staleLabel)

	mu.Lock()
	now = now.Add(app.KindBoard.TTL() + time.Minute)
	mu.Unlock()

	mustContain(t, dr.view(), staleLabel)
	reads := countCalls(fake, "SprintIssues")

	dr.send(kernel.FocusMsg{Focused: false})
	if n := countCalls(fake, "SprintIssues"); n != reads {
		t.Error("losing the keyboard read the board again")
	}
	dr.send(kernel.FocusMsg{Focused: true})

	if n := countCalls(fake, "SprintIssues"); n == reads {
		t.Error("coming back to a stale board did not read it again")
	}
	mustNotContain(t, dr.view(), staleLabel)
}
