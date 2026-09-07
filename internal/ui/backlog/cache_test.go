package backlog

import (
	"errors"
	"maps"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/varijkapil13/saral/internal/app"
	"github.com/varijkapil13/saral/internal/ui/kernel"
	"github.com/varijkapil13/saral/pkg/jira"
	"github.com/varijkapil13/saral/pkg/jira/jiratest"
)

// fakeCache is an app.Cache and an app.BacklogCache in a map. The real one is
// bbolt-backed and lives below internal/app, which a view may not import —
// which is the whole point of the interface being where it is.
type fakeCache struct {
	mu        sync.Mutex
	backlogs  map[int64]app.BacklogSnapshot
	lastBoard map[string]int64
	issues    map[string]jira.Issue
	gen       uint64
	forgot    []int64
	putFail   error
}

var (
	_ app.Cache        = (*fakeCache)(nil)
	_ app.BacklogCache = (*fakeCache)(nil)
)

func newFakeCache() *fakeCache {
	return &fakeCache{
		backlogs: map[int64]app.BacklogSnapshot{}, lastBoard: map[string]int64{}, issues: map[string]jira.Issue{},
	}
}

// hold puts a backlog in as though a previous session had left it there.
func (c *fakeCache) hold(project string, boardID int64, snap app.BacklogSnapshot, stale bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	snap.StoredAt, snap.Stale = cacheStoredAt, stale
	c.backlogs[boardID] = snap
	c.lastBoard[project] = boardID
	for i := range snap.Issues {
		c.issues[snap.Issues[i].Key] = snap.Issues[i]
	}
}

func (c *fakeCache) Backlog(boardID int64) (app.BacklogSnapshot, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	snap, ok := c.backlogs[boardID]
	return snap, ok
}

func (c *fakeCache) PutBacklog(boardID int64, snap app.BacklogSnapshot) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.putFail != nil {
		return c.putFail
	}
	c.gen++
	snap.StoredAt = cacheStoredAt
	c.backlogs[boardID] = snap
	for i := range snap.Issues {
		c.issues[snap.Issues[i].Key] = app.MergeIssue(c.issues[snap.Issues[i].Key], snap.Issues[i])
	}
	return nil
}

func (c *fakeCache) ForgetBacklog(boardID int64) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.gen++
	c.forgot = append(c.forgot, boardID)
	delete(c.backlogs, boardID)
	return nil
}

func (c *fakeCache) LastBacklogBoard(project string) (int64, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	id, ok := c.lastBoard[project]
	return id, ok
}

func (c *fakeCache) PutLastBacklogBoard(project string, boardID int64) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.gen++
	c.lastBoard[project] = boardID
	return nil
}

// The rest of app.Cache is unused by this package: a backlog's issues are read
// by id, never by JQL.
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
func refusing(issues int) *jiratest.Fake {
	f := newFake(issues)
	f.FailNextN(200, &jira.TransportError{Op: "board", Err: errors.New("dial tcp: no such host")})
	return f
}

// primed runs a fresh backlog against a fake to get an authentic snapshot to
// seed a cache with, rather than a hand-rolled approximation of a real read.
func primed(t *testing.T, d kernel.Deps) (boardID int64, snap app.BacklogSnapshot) {
	t.Helper()
	dr := newDriver(t, d, 120, 20)
	return dr.m.config.BoardID, app.BacklogSnapshot{
		Config: dr.m.config, Sprints: slices.Clone(dr.m.sprints), Field: dr.m.field, NoSprints: dr.m.noSprints,
		Issues: slices.Clone(dr.m.issues),
	}
}

// TestBacklog_DrawsTheStoredBacklogBeforeAnythingIsAskedOfTheSite is the gate
// on this packet. It builds the view and renders one frame without ever
// calling Init, which is exactly what kernel.FirstPaint does, so a cache read
// moved into Init fails here.
func TestBacklog_DrawsTheStoredBacklogBeforeAnythingIsAskedOfTheSite(t *testing.T) {
	t.Parallel()

	boardID, snap := primed(t, testDeps(newFake(6)))
	if len(snap.Issues) == 0 {
		t.Fatal("nothing was primed, so this test proves nothing")
	}
	cache := newFakeCache()
	cache.hold("PROJ", boardID, snap, false)

	fake := refusing(6)
	deps := withCache(testDeps(fake), cache)

	view, ok := New(deps).(*Model)
	if !ok {
		t.Fatal("New did not return a *Model")
	}
	next, _ := view.Update(kernel.SizeMsg{Width: 120, Height: 20})
	m, ok := next.(*Model)
	if !ok {
		t.Fatal("Update did not return a *Model")
	}
	frame := m.View()

	// Finished work is never drawn here (regroup leaves it out), so only the
	// issues a real frame would show are asserted.
	for _, iss := range snap.Issues {
		if iss.Status.Category == jira.CategoryDone {
			continue
		}
		mustContain(t, frame, iss.Key)
	}
	if calls := fake.Calls(); len(calls) != 0 {
		t.Errorf("the first frame made %v; nothing may be asked of the site before it is drawn", calls)
	}
}

func TestBacklog_DoesNotAskTheSiteAgainWhileTheStoredBacklogIsStillFresh(t *testing.T) {
	t.Parallel()

	boardID, snap := primed(t, testDeps(newFake(6)))
	cache := newFakeCache()
	cache.hold("PROJ", boardID, snap, false)

	fake := newFake(6)
	dr := newDriver(t, withCache(testDeps(fake), cache), 120, 20)

	if got := countCalls(fake, "Boards"); got != 0 {
		t.Errorf("a backlog opened on a snapshot written seconds ago asked the site %d times", got)
	}
	if len(dr.m.issues) != len(snap.Issues) {
		t.Errorf("the backlog holds %d issues, want the %d that were stored", len(dr.m.issues), len(snap.Issues))
	}
	mustNotContain(t, dr.view(), staleLabel)
}

func TestBacklog_RevalidatesAStoredBacklogThatIsPastItsTTL(t *testing.T) {
	t.Parallel()

	boardID, snap := primed(t, testDeps(newFake(6)))
	cache := newFakeCache()
	cache.hold("PROJ", boardID, snap, true)

	fake := newFake(6)
	dr := newDriver(t, withCache(testDeps(fake), cache), 120, 20)

	if got := countCalls(fake, "Boards"); got == 0 {
		t.Error("a backlog past its TTL was drawn and never checked")
	}
	if dr.m.stale {
		t.Error("the badge stayed up after the revalidation landed")
	}
	mustNotContain(t, dr.view(), staleLabel)
}

// TestBacklog_RevalidationLandsOnTheSameBoardTheSnapshotNamed pins that a
// revalidating load does not reset to whichever board the site lists first: a
// stored snapshot's own board id is what took resolves the index from.
func TestBacklog_RevalidationLandsOnTheSameBoardTheSnapshotNamed(t *testing.T) {
	t.Parallel()

	fake := jiratest.New(
		jiratest.WithProject("PROJ", jiratest.Scrum),
		jiratest.WithIssues(jiratest.Gen(4)),
	)
	boardID, snap := primed(t, testDeps(fake))
	if len(snap.Issues) == 0 {
		t.Fatal("nothing was primed, so this test proves nothing")
	}
	cache := newFakeCache()
	cache.hold("PROJ", boardID, snap, true)

	dr := newDriver(t, withCache(testDeps(fake), cache), 120, 20)

	if dr.m.config.BoardID != boardID {
		t.Errorf("the revalidated board is %d, want the stored board %d", dr.m.config.BoardID, boardID)
	}
}

func TestBacklog_KeepsTheStoredBacklogOnScreenWhenTheSiteRefuses(t *testing.T) {
	t.Parallel()

	tests := map[string]struct {
		err  error
		says string
	}{
		"a permission the token does not have": {
			err:  &jira.CapabilityError{Capability: jira.CapBoards, Reason: "needs Browse Projects permission"},
			says: "needs Browse Projects permission",
		},
		"the rate limiter": {
			err:  &jira.RateLimitError{RetryAfter: 30 * time.Second},
			says: "retry in 30s",
		},
		"a transport failure": {
			err:  &jira.TransportError{Op: "GET /board", Err: errors.New("dial tcp: no such host")},
			says: "no such host",
		},
	}

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			boardID, snap := primed(t, testDeps(newFake(6)))
			cache := newFakeCache()
			cache.hold("PROJ", boardID, snap, true)

			fake := newFake(6)
			fake.FailNextN(20, tc.err)
			dr := newDriver(t, withCache(testDeps(fake), cache), 120, 20)

			if len(dr.m.issues) != len(snap.Issues) {
				t.Fatalf("%d issues survived the refusal, want the %d off disk", len(dr.m.issues), len(snap.Issues))
			}
			view := dr.view()
			mustContain(t, view, staleLabel)
			if status := dr.lastStatus(); !strings.Contains(status.Text, tc.says) {
				t.Errorf("the status line reads %q, want the error's own words %q", status.Text, tc.says)
			}
		})
	}
}

func TestBacklog_StoresWhatItFetchedSoTheNextSessionDrawsItFirst(t *testing.T) {
	t.Parallel()

	cache := newFakeCache()
	dr := newDriver(t, withCache(testDeps(newFake(9)), cache), 120, 20)

	boardID := dr.m.config.BoardID
	snap, ok := cache.Backlog(boardID)
	if !ok {
		t.Fatal("a backlog that loaded stored nothing")
	}
	if len(snap.Issues) != len(dr.m.issues) {
		t.Errorf("%d issues were stored against the %d on screen", len(snap.Issues), len(dr.m.issues))
	}
	if got, ok := cache.LastBacklogBoard("PROJ"); !ok || got != boardID {
		t.Errorf("LastBacklogBoard returned %d, %t; want %d, true", got, ok, boardID)
	}
}

func TestBacklog_APurgingRefreshDropsTheStoredCopyToo(t *testing.T) {
	t.Parallel()

	cache := newFakeCache()
	dr := newDriver(t, withCache(testDeps(newFake(9)), cache), 120, 20)
	boardID := dr.m.config.BoardID

	dr.send(kernel.RefreshMsg{Purge: true})

	if !slices.Contains(cache.forgot, boardID) {
		t.Errorf("a purging refresh forgot %v, want it to drop %d", cache.forgot, boardID)
	}
}

func TestBacklog_SaysSoWhenTheBacklogCannotBeStored(t *testing.T) {
	t.Parallel()

	cache := newFakeCache()
	cache.putFail = errors.New("the cache file is read-only")
	dr := newDriver(t, withCache(testDeps(newFake(6)), cache), 120, 20)

	if len(dr.m.issues) == 0 {
		t.Fatal("a cache that could not be written dropped issues that had already arrived")
	}
	var said bool
	for _, status := range dr.statuses {
		if strings.Contains(status.Text, "read-only") {
			said = true
		}
	}
	if !said {
		t.Errorf("nothing was said about a backlog that could not be stored: %+v", dr.statuses)
	}
}

func TestBacklog_WithNoCacheDrawsExactlyWhatItDrewBefore(t *testing.T) {
	t.Parallel()

	withNone := newDriver(t, testDeps(newFake(9)), 120, 30)
	withEmpty := newDriver(t, withCache(testDeps(newFake(9)), newFakeCache()), 120, 30)

	if withNone.view() != withEmpty.view() {
		t.Errorf("a session with nowhere to cache draws a different frame\n--- no cache ---\n%s\n--- empty cache ---\n%s",
			withNone.view(), withEmpty.view())
	}
}

func TestStaleBacklogBadge_Golden(t *testing.T) {
	t.Parallel()

	boardID, snap := primed(t, testDeps(newFake(6)))
	cache := newFakeCache()
	cache.hold("PROJ", boardID, snap, true)

	fake := newFake(6)
	fake.FailNextN(20, &jira.TransportError{Op: "GET /board", Err: errors.New("dial tcp: no such host")})
	dr := newDriver(t, withCache(testDeps(fake), cache), 120, 30)

	golden(t, "backlog_stale_120x30.golden", dr.view())
}
