package sprint

import (
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/charmbracelet/x/ansi"

	"github.com/varijkapil13/saral/internal/app"
	"github.com/varijkapil13/saral/internal/ui/kernel"
	"github.com/varijkapil13/saral/pkg/jira"
	"github.com/varijkapil13/saral/pkg/jira/jiratest"
)

func stripped(frame string) string { return ansi.Strip(frame) }

func contains(s, sub string) bool { return strings.Contains(s, sub) }

// storedFrom is what a read of the fake would have stored, so a snapshot names
// boards and sprints the site does have.
func storedFrom(t *testing.T, f *jiratest.Fake) app.SprintsSnapshot {
	t.Helper()
	boards, err := f.Boards(t.Context(), "PROJ")
	if err != nil {
		t.Fatalf("Boards: %v", err)
	}
	var sprints []jira.Sprint
	for _, b := range boards {
		page, err := f.Sprints(t.Context(), b.ID, jira.SprintActive, jira.SprintFuture)
		if err != nil {
			t.Fatalf("Sprints: %v", err)
		}
		sprints = append(sprints, page.Items...)
	}
	return app.SprintsSnapshot{Boards: boards, Sprints: sprints}
}

// memCache is the sprint half of the cache and nothing else. The embedded
// app.Cache is nil: this view never reaches for rows, and a call that did would
// panic rather than pass.
type memCache struct {
	app.Cache
	mu      sync.Mutex
	held    map[string]app.SprintsSnapshot
	puts    int
	forgets int
	fail    error
}

func newMemCache() *memCache { return &memCache{held: make(map[string]app.SprintsSnapshot)} }

func (c *memCache) Sprints(project string) (app.SprintsSnapshot, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	snap, ok := c.held[project]
	return snap, ok
}

func (c *memCache) PutSprints(project string, snap app.SprintsSnapshot) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.puts++
	if c.fail != nil {
		return c.fail
	}
	c.held[project] = snap
	return nil
}

func (c *memCache) ForgetSprints(project string) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.forgets++
	delete(c.held, project)
	return nil
}

func storedSprints(stale bool) app.SprintsSnapshot {
	return app.SprintsSnapshot{
		Boards:  []jira.Board{{ID: 1, Name: "PROJ board", Type: jira.BoardScrum}},
		Sprints: many(4),
		Closed:  true,
		Stale:   stale,
	}
}

func TestSprints_TheFirstFrameIsDrawnFromTheCacheBeforeAnythingIsAsked(t *testing.T) {
	t.Parallel()

	f := newFake()
	cache := newMemCache()
	cache.held["PROJ"] = storedSprints(false)
	d := testDeps(f)
	d.Cache = cache

	view, _ := New(d).(*Model)
	next, _ := view.Update(kernel.SizeMsg{Width: 120, Height: 20})
	m, _ := next.(*Model)
	mustContain(t, stripped(m.View()), "Sprint 4", "Sprint 3")
	mustNotContain(t, stripped(m.View()), "Sprint 1", "Asking the site")
	if calls := f.Calls(); len(calls) != 0 {
		t.Errorf("the first frame asked the site %v", calls)
	}
}

func TestSprints_AFreshSnapshotSkipsTheListReadAndAStaleOneIsBadgedAndRead(t *testing.T) {
	t.Parallel()

	t.Run("fresh", func(t *testing.T) {
		t.Parallel()
		f := newFake()
		cache := newMemCache()
		cache.held["PROJ"] = storedFrom(t, f)
		before := len(f.Calls())
		d := testDeps(f)
		d.Cache = cache
		dr := newDriver(t, d, 120, 20)
		if n := countCalls(f, "Boards") + countCalls(f, "Sprints") - before; n != 0 {
			t.Errorf("a snapshot inside its TTL was read again %d times", n)
		}
		mustNotContain(t, dr.view(), staleLabel)
		if n := countCalls(f, "SprintIssues"); n == 0 {
			t.Error("the running sprint's progress was not read; nothing stores it")
		}
	})

	t.Run("stale", func(t *testing.T) {
		t.Parallel()
		f := newFake()
		cache := newMemCache()
		cache.held["PROJ"] = storedSprints(true)
		d := testDeps(f)
		d.Cache = cache
		view, _ := New(d).(*Model)
		next, _ := view.Update(kernel.SizeMsg{Width: 120, Height: 20})
		m, _ := next.(*Model)
		mustContain(t, stripped(m.View()), staleLabel)

		dr := newDriver(t, d, 120, 20)
		if countCalls(f, "Sprints") == 0 {
			t.Error("a stale snapshot was not revalidated")
		}
		mustNotContain(t, dr.view(), staleLabel, "Sprint 4")
		mustContain(t, dr.view(), "Sprint 2")
		if cache.puts == 0 {
			t.Error("the answer was not stored for the next session")
		}
		if got := cache.held["PROJ"]; len(got.Sprints) != len(dr.m.sprints) {
			t.Errorf("the store holds %d sprints, the screen %d", len(got.Sprints), len(dr.m.sprints))
		}
	})
}

// A read that fails over rows drawn from the cache keeps the rows and badges
// them, rather than clearing yesterday's answer for no answer.
func TestSprints_AFailedReadOverTheCacheKeepsTheRowsAndBadgesThem(t *testing.T) {
	t.Parallel()

	for name, err := range map[string]error{
		"a 403":               &jira.CapabilityError{Capability: jira.CapBoards, Reason: "needs Browse Projects"},
		"a 429":               &jira.RateLimitError{RetryAfter: time.Minute},
		"a transport failure": &jira.TransportError{Op: "GET", Err: errors.New("connection reset")},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			f := newFake()
			f.FailNext(err)
			cache := newMemCache()
			cache.held["PROJ"] = storedSprints(true)
			d := testDeps(f)
			d.Cache = cache
			dr := newDriver(t, d, 120, 20)
			frame := dr.view()
			mustContain(t, frame, "Sprint 4", staleLabel)
			if !dr.m.stale {
				t.Error("the rows are not marked stale")
			}
		})
	}
}

func TestSprints_APurgingRefreshDropsTheStoredCopyAndAWriteKeepsIt(t *testing.T) {
	t.Parallel()

	f := newFake()
	cache := newMemCache()
	d := testDeps(f)
	d.Cache = cache
	dr := newDriver(t, d, 120, 20)
	dr.send(kernel.RefreshMsg{Purge: true})
	if cache.forgets != 1 {
		t.Errorf("a purging refresh dropped the stored copy %d times, want once", cache.forgets)
	}

	before := cache.puts
	dr.onSprint("Sprint 2")
	dr.key("c", "y")
	if cache.puts == before {
		t.Error("a completion was not stored, so the next launch draws the sprint as running")
	}
	if got := cache.held["PROJ"]; !containsState(got.Sprints, "Sprint 2", jira.SprintClosed) {
		t.Errorf("the store holds %+v, want Sprint 2 closed", got.Sprints)
	}
}

func TestSprints_AStoreThatRefusesAWriteIsSaidAndTheListStays(t *testing.T) {
	t.Parallel()

	cache := newMemCache()
	cache.fail = errors.New("disk full")
	d := testDeps(newFake())
	d.Cache = cache
	dr := newDriver(t, d, 120, 20)
	if len(dr.m.sprints) == 0 {
		t.Fatal("the list emptied over a store that refused a write")
	}
	found := false
	for _, st := range dr.statuses {
		if st.Level == kernel.LevelWarn && contains(st.Text, "disk full") {
			found = true
		}
	}
	if !found {
		t.Errorf("the refused write was not said; statuses were %+v", dr.statuses)
	}
}

func containsState(sprints []jira.Sprint, name string, state jira.SprintState) bool {
	for _, sp := range sprints {
		if sp.Name == name {
			return sp.State == state
		}
	}
	return false
}
