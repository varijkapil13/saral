package release

import (
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/charmbracelet/x/ansi"

	"github.com/varijkapil13/saral/internal/app"
	"github.com/varijkapil13/saral/internal/ui/kernel"
	"github.com/varijkapil13/saral/pkg/jira"
)

// memCache is the versions half of the cache and nothing else. The embedded
// app.Cache is nil: this view never reaches for rows, and a call that did would
// panic rather than pass.
type memCache struct {
	app.Cache
	mu      sync.Mutex
	held    map[string]app.VersionsSnapshot
	puts    int
	forgets int
	fail    error
}

func newMemCache() *memCache { return &memCache{held: make(map[string]app.VersionsSnapshot)} }

func (c *memCache) Versions(project string) (app.VersionsSnapshot, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	snap, ok := c.held[project]
	return snap, ok
}

func (c *memCache) PutVersions(project string, versions []jira.Version) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.puts++
	if c.fail != nil {
		return c.fail
	}
	c.held[project] = app.VersionsSnapshot{Versions: append([]jira.Version(nil), versions...)}
	return nil
}

func (c *memCache) ForgetVersions(project string) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.forgets++
	delete(c.held, project)
	return nil
}

func storedVersions(stale bool) app.VersionsSnapshot {
	return app.VersionsSnapshot{
		Versions: []jira.Version{{ID: "70001", Name: "stored-1"}, {ID: "70002", Name: "stored-2"}},
		StoredAt: time.Date(2026, time.March, 5, 8, 30, 0, 0, time.UTC),
		Stale:    stale,
	}
}

func TestReleases_TheFirstFrameIsDrawnFromTheCacheBeforeAnythingIsAsked(t *testing.T) {
	t.Parallel()

	f := newFake(4)
	cache := newMemCache()
	cache.held["PROJ"] = storedVersions(false)
	d := testDeps(f)
	d.Cache = cache
	view, _ := New(d).(*Model)
	next, _ := view.Update(kernel.SizeMsg{Width: 100, Height: 16})
	frame := ansi.Strip(next.View())
	mustContain(t, frame, "stored-1", "stored-2", "read 08:30")
	if calls := f.Calls(); len(calls) != 0 {
		t.Errorf("the first frame asked the site %v", calls)
	}
}

func TestReleases_AFreshSnapshotIsNotReadAgainAndAStaleOneIsBadgedAndRead(t *testing.T) {
	t.Parallel()

	t.Run("fresh", func(t *testing.T) {
		t.Parallel()
		f := newFake(4)
		cache := newMemCache()
		cache.held["PROJ"] = storedVersions(false)
		d := testDeps(f)
		d.Cache = cache
		dr := listOf(t, d, 100, 16)
		if n := countCalls(f, "Versions"); n != 0 {
			t.Errorf("a snapshot inside its TTL was read again %d times", n)
		}
		mustNotContain(t, dr.view(), staleLabel)
	})

	t.Run("stale", func(t *testing.T) {
		t.Parallel()
		f := newFake(4)
		cache := newMemCache()
		cache.held["PROJ"] = storedVersions(true)
		d := testDeps(f)
		d.Cache = cache
		view, _ := New(d).(*Model)
		next, _ := view.Update(kernel.SizeMsg{Width: 100, Height: 16})
		mustContain(t, ansi.Strip(next.View()), staleLabel)

		dr := listOf(t, d, 100, 16)
		if countCalls(f, "Versions") == 0 {
			t.Fatal("a stale snapshot was not revalidated")
		}
		frame := dr.view()
		mustNotContain(t, frame, staleLabel, "stored-1")
		mustContain(t, frame, "2.0")
		if got := cache.held["PROJ"]; len(got.Versions) != len(dr.list().versions) {
			t.Errorf("the store holds %d versions, the screen %d", len(got.Versions), len(dr.list().versions))
		}
	})
}

func TestReleases_AFailedReadOverTheCacheKeepsTheRowsAndBadgesThem(t *testing.T) {
	t.Parallel()

	for name, err := range map[string]error{
		"a 403":               &jira.CapabilityError{Capability: jira.CapBoards, Reason: "needs Browse Projects"},
		"a 429":               &jira.RateLimitError{RetryAfter: time.Minute},
		"a transport failure": &jira.TransportError{Op: "GET", Err: errors.New("connection reset")},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			f := newFake(4)
			f.FailNext(err)
			cache := newMemCache()
			cache.held["PROJ"] = storedVersions(true)
			d := testDeps(f)
			d.Cache = cache
			dr := listOf(t, d, 100, 16)
			mustContain(t, dr.view(), "stored-1", staleLabel)
		})
	}
}

func TestReleases_APurgingRefreshDropsTheStoredCopy(t *testing.T) {
	t.Parallel()

	cache := newMemCache()
	d := testDeps(newFake(4))
	d.Cache = cache
	dr := listOf(t, d, 100, 16)
	dr.send(kernel.RefreshMsg{Purge: true})
	if cache.forgets != 1 {
		t.Errorf("a purging refresh dropped the stored copy %d times, want once", cache.forgets)
	}
	if len(cache.held["PROJ"].Versions) == 0 {
		t.Error("the read after the purge was not stored")
	}
}

func TestReleases_AStoreThatRefusesAWriteIsSaid(t *testing.T) {
	t.Parallel()

	cache := newMemCache()
	cache.fail = errors.New("disk full")
	d := testDeps(newFake(4))
	d.Cache = cache
	dr := listOf(t, d, 100, 16)
	if len(dr.list().versions) == 0 {
		t.Fatal("the list emptied over a store that refused a write")
	}
	if st := dr.lastStatus(); st.Level != kernel.LevelWarn {
		t.Errorf("the refused write ended on %q at level %v, want a warning", st.Text, st.Level)
	}
}
