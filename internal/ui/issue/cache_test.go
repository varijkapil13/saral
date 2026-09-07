package issue

import (
	"errors"
	"maps"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/charmbracelet/x/ansi"

	"github.com/varijkapil13/saral/internal/app"
	"github.com/varijkapil13/saral/internal/ui/kernel"
	"github.com/varijkapil13/saral/pkg/adf"
	"github.com/varijkapil13/saral/pkg/jira"
	"github.com/varijkapil13/saral/pkg/jira/jiratest"
)

// fakeCache is an app.Cache and an app.IssueCache in a map. The real one is
// bbolt-backed and lives below internal/app, which a view may not import —
// which is the whole point of the interface being where it is.
type fakeCache struct {
	mu      sync.Mutex
	issues  map[string]jira.Issue
	gen     uint64
	putFail error
}

var (
	_ app.Cache      = (*fakeCache)(nil)
	_ app.IssueCache = (*fakeCache)(nil)
)

func newFakeCache() *fakeCache {
	return &fakeCache{issues: map[string]jira.Issue{}}
}

// hold puts an issue in as though a previous session had read it.
func (c *fakeCache) hold(iss jira.Issue) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.issues[iss.Key] = iss
}

func (c *fakeCache) Issue(key string) (app.IssueSnapshot, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	iss, ok := c.issues[key]
	if !ok {
		return app.IssueSnapshot{}, false
	}
	return app.IssueSnapshot{Issue: iss, StoredAt: cacheStoredAt}, true
}

func (c *fakeCache) PutIssue(iss jira.Issue) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.putFail != nil {
		return c.putFail
	}
	c.gen++
	c.issues[iss.Key] = app.MergeIssue(c.issues[iss.Key], iss)
	return nil
}

// The rest of app.Cache is unused by this package: the detail pane reads and
// writes one issue by key, never a search's rows.
func (c *fakeCache) Rows(string) (app.Snapshot, bool)         { return app.Snapshot{}, false }
func (c *fakeCache) PutRows(string, []jira.Issue, bool) error { return nil }
func (c *fakeCache) Forget(string) error                      { return nil }

func (c *fakeCache) EachIssue(fn func(jira.Issue, time.Time) bool) (int, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	for _, key := range slices.Sorted(maps.Keys(c.issues)) {
		if !fn(c.issues[key], cacheStoredAt) {
			return 0, nil
		}
	}
	return 0, nil
}

func (c *fakeCache) Generation() uint64 {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.gen
}

var cacheStoredAt = time.Date(2025, time.March, 5, 8, 30, 0, 0, time.UTC)

func withCache(d kernel.Deps, c app.Cache) kernel.Deps {
	d.Cache = c
	return d
}

// refusing is a site that answers nothing at all, which is what a first paint
// has to work through.
func refusing(t *testing.T, issues int) *jiratest.Fake {
	t.Helper()
	f := newFake(issues)
	f.FailNextN(200, &jira.TransportError{Op: "issue", Err: errors.New("dial tcp: no such host")})
	return f
}

// TestIssue_DrawsTheCachedIssueBeforeAnythingIsAskedOfTheSite is the gate on
// this packet. It builds the view and renders one frame without ever calling
// Init, which is exactly what kernel.FirstPaint does, so a cache read moved
// into Init fails here.
func TestIssue_DrawsTheCachedIssueBeforeAnythingIsAskedOfTheSite(t *testing.T) {
	t.Parallel()

	fake := newFake(3)
	full := readIssue(t, fake, "PROJ-1")
	full.Description = adf.NewDoc(adf.NewNode("paragraph", adf.NewText("a description read once and cached")))

	cache := newFakeCache()
	cache.hold(full)

	seed := seedOf(t, fake, "PROJ-1")
	deps := withCache(testDeps(refusing(t, 3)), cache)

	view, ok := New(deps, seed).(*Model)
	if !ok {
		t.Fatal("New did not return a *Model")
	}
	next, _ := view.Update(kernel.SizeMsg{Width: 100, Height: 30})
	m, ok := next.(*Model)
	if !ok {
		t.Fatal("Update did not return a *Model")
	}
	frame := ansi.Strip(m.View())

	if !strings.Contains(frame, "a description read once and cached") {
		t.Errorf("the first frame does not carry the cached description:\n%s", frame)
	}
}

func TestIssue_ANarrowSeedWithNothingCachedStillReadsAsNotYetKnown(t *testing.T) {
	t.Parallel()

	fake := refusing(t, 3)
	seed := seedOf(t, newFake(3), "PROJ-1")
	deps := withCache(testDeps(fake), newFakeCache())

	view, ok := New(deps, seed).(*Model)
	if !ok {
		t.Fatal("New did not return a *Model")
	}
	next, _ := view.Update(kernel.SizeMsg{Width: 100, Height: 30})
	m, ok := next.(*Model)
	if !ok {
		t.Fatal("Update did not return a *Model")
	}
	frame := ansi.Strip(m.View())

	if strings.Contains(frame, "No description.") {
		t.Error("an empty cache made a description that was simply never fetched look like an issue with none")
	}
	if !strings.Contains(frame, "Reading the issue") {
		t.Errorf("a narrow seed with nothing cached should still say it is reading:\n%s", frame)
	}
}

func TestIssue_TheLandingFetchReplacesTheCachedCopyCleanly(t *testing.T) {
	t.Parallel()

	fake := newFake(3)
	stale := readIssue(t, fake, "PROJ-1")
	stale.Summary = "an old summary from a previous session"
	cache := newFakeCache()
	cache.hold(stale)

	seed := seedOf(t, fake, "PROJ-1")
	dr := newDriver(t, withCache(testDeps(fake), cache), seed, 100, 30)

	if dr.m.issue.Summary == stale.Summary {
		t.Error("the landing fetch did not replace the cached summary")
	}
	if !dr.m.loadedIssue {
		t.Error("a landed fetch did not mark the issue loaded")
	}
}

func TestIssue_StoresTheFetchedIssueSoTheNextOpenDrawsItFirst(t *testing.T) {
	t.Parallel()

	fake := newFake(3)
	cache := newFakeCache()
	seed := seedOf(t, fake, "PROJ-1")
	newDriver(t, withCache(testDeps(fake), cache), seed, 100, 30)

	snap, ok := cache.Issue("PROJ-1")
	if !ok {
		t.Fatal("opening an issue stored nothing for next time")
	}
	if snap.Issue.Summary == "" {
		t.Error("the stored issue carries no summary")
	}
}

func TestIssue_SaysSoWhenTheIssueCannotBeStored(t *testing.T) {
	t.Parallel()

	fake := newFake(3)
	cache := newFakeCache()
	cache.putFail = errors.New("the cache file is read-only")
	seed := seedOf(t, fake, "PROJ-1")
	dr := newDriver(t, withCache(testDeps(fake), cache), seed, 100, 30)

	if !dr.m.loadedIssue {
		t.Fatal("a cache that could not be written dropped the issue that had already arrived")
	}
	var said bool
	for _, status := range dr.statuses {
		if strings.Contains(status.Text, "read-only") {
			said = true
		}
	}
	if !said {
		t.Errorf("nothing was said about an issue that could not be stored: %+v", dr.statuses)
	}
}

func TestIssue_WithNoCacheDrawsExactlyWhatItDrewBefore(t *testing.T) {
	t.Parallel()

	fakeA, fakeB := newFake(3), newFake(3)
	seedA, seedB := seedOf(t, fakeA, "PROJ-1"), seedOf(t, fakeB, "PROJ-1")

	withNone := newDriver(t, testDeps(fakeA), seedA, 100, 30)
	withEmpty := newDriver(t, withCache(testDeps(fakeB), newFakeCache()), seedB, 100, 30)

	if withNone.view() != withEmpty.view() {
		t.Errorf("a session with nowhere to cache draws a different frame\n--- no cache ---\n%s\n--- empty cache ---\n%s",
			withNone.view(), withEmpty.view())
	}
}
