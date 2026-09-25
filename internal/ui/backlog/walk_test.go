package backlog

import (
	"sync"
	"testing"

	"github.com/varijkapil13/saral/internal/app"
	"github.com/varijkapil13/saral/pkg/jira"
)

// pageCache is fakeCache that also keeps a backlog a page at a time, and
// records what each write carried.
type pageCache struct {
	*fakeCache
	mu     sync.Mutex
	writes []pageWrite
}

type pageWrite struct {
	first bool
	keys  []string
}

var _ app.BacklogPageCache = (*pageCache)(nil)

func (c *pageCache) PutBacklogPage(boardID int64, page app.BacklogSnapshot, first bool) error {
	w := pageWrite{first: first}
	for i := range page.Issues {
		w.keys = append(w.keys, page.Issues[i].Key)
	}
	c.mu.Lock()
	c.writes = append(c.writes, w)
	c.mu.Unlock()
	held, _ := c.Backlog(boardID)
	if first {
		held.Issues = nil
	}
	at := make(map[string]int, len(held.Issues))
	for i := range held.Issues {
		at[held.Issues[i].Key] = i
	}
	for i := range page.Issues {
		if j, ok := at[page.Issues[i].Key]; ok {
			held.Issues[j] = page.Issues[i]
			continue
		}
		held.Issues = append(held.Issues, page.Issues[i])
	}
	page.Issues = held.Issues
	return c.PutBacklog(boardID, page)
}

func (c *pageCache) all() []pageWrite {
	c.mu.Lock()
	defer c.mu.Unlock()
	return append([]pageWrite(nil), c.writes...)
}

// A board's done is its last column with a status mapped to it. A status in
// the done category sitting in an earlier column is work the board still shows
// as not finished, and a status of another category in that last column is
// work it counts as done.
func TestBacklog_DoneIsTheBoardsLastMappedColumnNotTheStatusCategory(t *testing.T) {
	t.Parallel()
	dr := newDriver(t, testDeps(nil), 120, 20)
	field := jira.FieldRef{ID: "customfield_20001", Name: "Sprint"}
	cfg := jira.BoardConfig{BoardID: 7, Name: "Ledger", Columns: []jira.Column{
		{Name: "Waiting", StatusIDs: []string{"s-wait"}},
		{Name: "Verified", StatusIDs: []string{"s-verified"}},
		{Name: "Shipped", StatusIDs: []string{"s-shipped"}},
		{Name: "Unused"},
	}}
	issues := []jira.Issue{
		{Key: "PROJ-1", Summary: "waiting", Status: jira.Status{ID: "s-wait", Category: jira.CategoryToDo}},
		{Key: "PROJ-2", Summary: "verified", Status: jira.Status{ID: "s-verified", Category: jira.CategoryDone}},
		{Key: "PROJ-3", Summary: "shipped", Status: jira.Status{ID: "s-shipped", Category: jira.CategoryInProgress}},
	}
	dr.send(loadedMsg{
		gen: dr.m.gen, boards: []jira.Board{{ID: 7, Name: "Ledger"}}, config: cfg, field: field,
		page: jira.Page[jira.Issue]{Items: issues},
	})

	if got := dr.groupOf("PROJ-2"); got == "" {
		t.Error("PROJ-2 is in a done-category status but not in the board's done column, and was hidden")
	}
	if got := dr.groupOf("PROJ-3"); got != "" {
		t.Errorf("PROJ-3 is in the board's last mapped column and was drawn in %q", got)
	}
	if got := dr.groupOf("PROJ-1"); got == "" {
		t.Error("PROJ-1 is waiting and was hidden")
	}
}

func TestBacklog_AStoredBacklogCutShortIsReadAgain(t *testing.T) {
	t.Parallel()
	boardID, snap := primed(t, testDeps(newFake(20)))
	snap.Issues, snap.More = snap.Issues[:3], true
	cache := newFakeCache()
	cache.hold("PROJ", boardID, snap, false)

	fake := newFake(20)
	dr := newDriver(t, withCache(testDeps(fake), cache), 120, 20)

	if got := countCalls(fake, "Boards"); got == 0 {
		t.Error("a stored backlog that had more to read was taken as the whole backlog")
	}
	if len(dr.m.issues) <= 3 {
		t.Errorf("the backlog holds %d issues, want it read again", len(dr.m.issues))
	}
}

// The pages are written one at a time, and a move writes the issues it moved,
// so a backlog opened from disk after a move does not draw them where they were.
func TestBacklog_StoresEachPageAndTheIssuesAMoveMoved(t *testing.T) {
	t.Parallel()
	cache := &pageCache{fakeCache: newFakeCache()}
	dr := newDriver(t, withCache(testDeps(newFake(140)), cache), 120, 24)
	dr.loadAll()

	writes := cache.all()
	if len(writes) < 2 || !writes[0].first {
		t.Fatalf("the walk was written as %d writes; want the first page first and one per page after", len(writes))
	}
	for i, w := range writes[1:] {
		if w.first {
			t.Errorf("write %d replaced the stored backlog part way through the walk", i+1)
		}
	}

	key := dr.m.issues[dr.m.groups[len(dr.m.groups)-1].issues[0]].Key
	dr.cursorTo("row:" + key)
	before := len(cache.all())
	dr.key("m")
	dr.m.destAt = 0
	dr.key("enter", "y")

	after := cache.all()
	if len(after) == before {
		t.Fatal("the move was never written to the cache")
	}
	last := after[len(after)-1]
	if last.first || len(last.keys) != 1 || last.keys[0] != key {
		t.Errorf("the move wrote %+v, want just %s merged into what was stored", last, key)
	}
	snap, _ := cache.Backlog(dr.m.config.BoardID)
	for i := range snap.Issues {
		if snap.Issues[i].Key != key {
			continue
		}
		if ids := dr.m.sprintsOn(&snap.Issues[i]); len(ids) == 0 || ids[0] != dr.m.groups[0].id {
			t.Errorf("the stored %s is in sprints %v, want the %d it was moved into", key, ids, dr.m.groups[0].id)
		}
	}
}

// Picking a whole section means all of it, so the rest of the backlog is read
// before the pick lands, and until it has been every section's count says it
// may be short.
func TestBacklog_PickingAWholeSectionReadsTheRestFirst(t *testing.T) {
	t.Parallel()
	fake := newFake(140)
	dr := newDriver(t, testDeps(fake), 120, 24)
	if !dr.m.page.HasMore() {
		t.Fatal("the whole backlog arrived at once, so there is no rest to read")
	}
	mustContain(t, dr.view(), "+ issues")

	dr.pickWholeBacklog()

	if dr.m.page.HasMore() {
		t.Fatal("picking a whole section left the rest of the backlog unread")
	}
	backlog := dr.m.groups[len(dr.m.groups)-1]
	if len(dr.m.picked) != len(backlog.issues) {
		t.Errorf("%d issues were picked, want every one of the %d in the section", len(dr.m.picked), len(backlog.issues))
	}
	mustNotContain(t, dr.view(), "+ issues")
}

func TestBacklog_APickThatCannotReadTheRestSaysSoAndPicksNothing(t *testing.T) {
	t.Parallel()
	fake := newFake(140)
	dr := newDriver(t, testDeps(fake), 120, 24)
	fake.FailNext(&jira.CapabilityError{Reason: "you need Browse Projects on PROJ"})

	dr.pickWholeBacklog()

	if len(dr.m.picked) != 0 {
		t.Errorf("%d issues were picked from a section that could not be read whole", len(dr.m.picked))
	}
	mustContain(t, dr.lastStatus().Text, "Browse Projects")
	if dr.m.pickAfterRead || dr.m.reading {
		t.Error("the pick is still waiting on a walk that failed")
	}
}

func TestBacklog_AConfigMappingNoColumnFallsBackToTheStatusCategory(t *testing.T) {
	t.Parallel()
	dr := newDriver(t, testDeps(nil), 120, 20)
	dr.send(loadedMsg{
		gen: dr.m.gen, boards: []jira.Board{{ID: 7, Name: "Ledger"}}, config: jira.BoardConfig{BoardID: 7},
		field: jira.FieldRef{ID: "customfield_20001", Name: "Sprint"},
		page: jira.Page[jira.Issue]{Items: []jira.Issue{
			{Key: "PROJ-1", Summary: "open", Status: jira.Status{ID: "s1", Category: jira.CategoryToDo}},
			{Key: "PROJ-2", Summary: "finished", Status: jira.Status{ID: "s2", Category: jira.CategoryDone}},
		}},
	})

	if dr.groupOf("PROJ-2") != "" {
		t.Error("a finished issue was drawn on a board whose config maps no column")
	}
	if dr.groupOf("PROJ-1") == "" {
		t.Error("an open issue was hidden")
	}
}
