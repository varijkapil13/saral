package list

import (
	"errors"
	"maps"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/varijkapil13/saral/internal/app"
	"github.com/varijkapil13/saral/internal/ui/kernel"
	"github.com/varijkapil13/saral/pkg/jira"
	"github.com/varijkapil13/saral/pkg/jira/jiratest"
)

// fakeCache is an app.Cache in a map. The real one is bbolt-backed and lives
// below internal/app, which a view may not import — which is the whole point of
// the interface being where it is.
type fakeCache struct {
	mu      sync.Mutex
	rows    map[string]app.Snapshot
	issues  map[string]jira.Issue
	gen     uint64
	forgot  []string
	putFail error
}

var _ app.Cache = (*fakeCache)(nil)

func newFakeCache() *fakeCache {
	return &fakeCache{rows: map[string]app.Snapshot{}, issues: map[string]jira.Issue{}}
}

// hold puts rows in as though a previous session had left them there.
func (c *fakeCache) hold(jql string, issues []jira.Issue, stale, more bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.rows[jql] = app.Snapshot{Issues: slices.Clone(issues), StoredAt: cacheStoredAt, Stale: stale, More: more}
	for i := range issues {
		c.issues[issues[i].Key] = issues[i]
	}
}

func (c *fakeCache) Rows(jql string) (app.Snapshot, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	snap, ok := c.rows[strings.TrimSpace(jql)]
	return snap, ok
}

func (c *fakeCache) PutRows(jql string, issues []jira.Issue, more bool) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.putFail != nil {
		return c.putFail
	}
	c.gen++
	c.rows[strings.TrimSpace(jql)] = app.Snapshot{Issues: slices.Clone(issues), StoredAt: cacheStoredAt, More: more}
	for i := range issues {
		c.issues[issues[i].Key] = app.MergeIssue(c.issues[issues[i].Key], issues[i])
	}
	return nil
}

func (c *fakeCache) Forget(jql string) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.gen++
	c.forgot = append(c.forgot, jql)
	delete(c.rows, strings.TrimSpace(jql))
	return nil
}

func (c *fakeCache) EachIssue(fn func(jira.Issue, time.Time) bool) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	for _, key := range slices.Sorted(maps.Keys(c.issues)) {
		if !fn(c.issues[key], cacheStoredAt) {
			return nil
		}
	}
	return nil
}

func (c *fakeCache) Generation() uint64 {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.gen
}

var cacheStoredAt = time.Date(2025, time.March, 5, 8, 30, 0, 0, time.UTC)

// storedRows are what a previous session would have written: the six fields of
// the list projection, and a mask that says so.
func storedRows(n int) []jira.Issue {
	mask := jira.NewFieldMask(app.ListProjection().IDs)
	out := jiratest.Gen(n)
	for i := range out {
		out[i].Requested = mask
	}
	return out
}

func withCache(d kernel.Deps, c app.Cache) kernel.Deps {
	d.Cache = c
	return d
}

// refusing is a site that answers nothing at all, which is what a first paint
// has to work through.
func refusing(t *testing.T, issues int) *jiratest.Fake {
	t.Helper()
	f := newFake(issues)
	f.FailNextN(200, &jira.TransportError{Op: "search", Err: errors.New("dial tcp: no such host")})
	return f
}

// TestNew_DrawsTheStoredRowsBeforeAnythingIsAskedOfTheSite is the gate on this
// packet. It builds the view and renders one frame without ever calling Init,
// which is exactly what kernel.FirstPaint does, so a cache read moved into Init
// fails here.
func TestNew_DrawsTheStoredRowsBeforeAnythingIsAskedOfTheSite(t *testing.T) {
	t.Parallel()

	cache := newFakeCache()
	f := refusing(t, 0)
	deps := withCache(testDeps(f), cache)
	jql, _ := defaultQuery(deps.Project)
	rows := storedRows(4)
	cache.hold(jql, rows, false, false)

	view, ok := New(deps).(*Model)
	if !ok {
		t.Fatal("New did not return a *Model")
	}
	next, _ := view.Update(kernel.SizeMsg{Width: 120, Height: 20})
	m, _ := next.(*Model)
	frame := m.View()

	for _, iss := range rows {
		mustContain(t, frame, iss.Key, iss.Summary)
	}
	if calls := f.Calls(); len(calls) != 0 {
		t.Errorf("the first frame made %v; nothing may be asked of the site before it is drawn", calls)
	}
}

func TestFirstPaint_DrawsStoredRowsWithNothingReachable(t *testing.T) {
	t.Parallel()

	cache := newFakeCache()
	f := refusing(t, 0)
	deps := withCache(testDeps(f), cache)
	jql, _ := defaultQuery(deps.Project)
	rows := storedRows(6)
	cache.hold(jql, rows, false, false)

	took, frame, err := kernel.FirstPaint(deps, 120, 40)
	if err != nil {
		t.Fatalf("FirstPaint: %v", err)
	}
	for _, iss := range rows {
		mustContain(t, frame, iss.Key)
	}
	if calls := f.Calls(); len(calls) != 0 {
		t.Errorf("the first paint made %v", calls)
	}
	if took > 60*time.Millisecond {
		t.Errorf("the first paint from the cache took %s, want under the 60ms in docs/PERFORMANCE.md", took)
	}
}

func TestList_DoesNotAskTheSiteAgainWhileTheStoredRowsAreStillFresh(t *testing.T) {
	t.Parallel()

	cache := newFakeCache()
	f := newFake(20)
	deps := withCache(testDeps(f), cache)
	jql, _ := defaultQuery(deps.Project)
	cache.hold(jql, storedRows(4), false, false)

	dr := newDriver(t, deps, 120, 20)

	if got := countCalls(f, "Search"); got != 0 {
		t.Errorf("a list opened on rows written seconds ago searched %d times; the cache exists to spare that", got)
	}
	if len(dr.m.issues) != 4 {
		t.Errorf("the list holds %d rows, want the 4 that were stored", len(dr.m.issues))
	}
	mustNotContain(t, dr.view(), staleLabel)
}

func TestList_RevalidatesStoredRowsThatArePastTheirTTL(t *testing.T) {
	t.Parallel()

	cache := newFakeCache()
	f := newFake(20)
	deps := withCache(testDeps(f), cache)
	jql, _ := defaultQuery(deps.Project)
	cache.hold(jql, storedRows(4), true, false)

	dr := newDriver(t, deps, 120, 20)

	if got := countCalls(f, "Search"); got == 0 {
		t.Error("rows past their TTL were drawn and never checked")
	}
	if dr.m.stale {
		t.Error("the badge stayed up after the refresh landed")
	}
	mustNotContain(t, dr.view(), staleLabel)
}

// TestList_RevalidationArrivesAsAPatchRatherThanAFreshLoad pins that the rows
// off disk are fed into the cursor-preserving patch this view already had, and
// not into the load that starts again at row one.
func TestList_RevalidationArrivesAsAPatchRatherThanAFreshLoad(t *testing.T) {
	t.Parallel()

	cache := newFakeCache()
	deps := withCache(testDeps(newFake(20)), cache)
	jql, _ := defaultQuery(deps.Project)
	cache.hold(jql, storedRows(4), true, false)

	stored, _ := New(deps).(*Model)
	switch msg := firstMsg(t, stored.Init()).(type) {
	case patchedMsg:
	default:
		t.Errorf("revalidating stored rows produced a %T, want the patchedMsg that keeps the cursor", msg)
	}

	cold, _ := New(withCache(testDeps(newFake(20)), newFakeCache())).(*Model)
	switch msg := firstMsg(t, cold.Init()).(type) {
	case loadedMsg:
	default:
		t.Errorf("a list with nothing stored produced a %T, want a loadedMsg", msg)
	}
}

func TestList_ARefreshAfterAStoredPaintKeepsThePlace(t *testing.T) {
	t.Parallel()

	cache := newFakeCache()
	deps := withCache(testDeps(newFake(60)), cache)
	cache.hold(allJQL, storedRows(40), false, false)

	dr := newDriver(t, deps, 120, 20)
	dr.send(QueryMsg{JQL: allJQL, Title: "All issues"})
	if len(dr.m.issues) != 40 {
		t.Fatalf("the retargeted list holds %d rows, want the 40 that were stored", len(dr.m.issues))
	}
	for range 25 {
		dr.key("j")
	}
	under, cursor, top := dr.m.selectedKey(), dr.m.cursor, dr.m.top
	if top == 0 {
		t.Fatal("the list never scrolled, so this proves nothing about the offset")
	}

	dr.send(kernel.RefreshMsg{})

	if dr.m.selectedKey() != under || dr.m.cursor != cursor || dr.m.top != top {
		t.Errorf("the refresh moved to %s at %d/%d, want %s at %d/%d",
			dr.m.selectedKey(), dr.m.cursor, dr.m.top, under, cursor, top)
	}
}

func TestList_KeepsTheStoredRowsOnScreenWhenTheSiteRefuses(t *testing.T) {
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
			err:  &jira.TransportError{Op: "search", Err: errors.New("dial tcp: no such host")},
			says: "no such host",
		},
	}

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			cache := newFakeCache()
			f := newFake(20)
			deps := withCache(testDeps(f), cache)
			jql, _ := defaultQuery(deps.Project)
			rows := storedRows(4)
			cache.hold(jql, rows, true, false)
			f.FailNextN(20, tc.err)

			dr := newDriver(t, deps, 120, 20)

			if len(dr.m.issues) != len(rows) {
				t.Fatalf("%d rows survived the refusal, want the %d off disk", len(dr.m.issues), len(rows))
			}
			view := dr.view()
			mustContain(t, view, rows[0].Key, staleLabel)
			if status := dr.lastStatus(); !strings.Contains(status.Text, tc.says) {
				t.Errorf("the status line reads %q, want the error's own words %q", status.Text, tc.says)
			}
		})
	}
}

func TestList_BadgesRowsItCouldNotRefreshEvenWhenTheyWereFreshOnDisk(t *testing.T) {
	t.Parallel()

	cache := newFakeCache()
	f := newFake(20)
	deps := withCache(testDeps(f), cache)
	jql, _ := defaultQuery(deps.Project)
	cache.hold(jql, storedRows(4), false, false)

	dr := newDriver(t, deps, 120, 20)
	mustNotContain(t, dr.view(), staleLabel)

	f.FailNext(&jira.TransportError{Op: "search", Err: errors.New("dial tcp: no such host")})
	dr.send(kernel.RefreshMsg{})

	if !dr.m.stale {
		t.Error("rows the site refused to confirm are not badged")
	}
	mustContain(t, dr.view(), staleLabel)
}

func TestList_StoresWhatItFetchedSoTheNextSessionDrawsItFirst(t *testing.T) {
	t.Parallel()

	cache := newFakeCache()
	deps := withCache(testDeps(newFake(60, jiratest.WithPageSize(20))), cache)
	dr := openAll(t, deps, 120, 20)

	snap, ok := cache.Rows(allJQL)
	if !ok {
		t.Fatal("a search that worked stored nothing")
	}
	if len(snap.Issues) != len(dr.m.issues) {
		t.Errorf("%d rows were stored against the %d on screen", len(snap.Issues), len(dr.m.issues))
	}
	if snap.Issues[0].Key != dr.m.issues[0].Key {
		t.Errorf("the stored rows start at %s and the screen at %s", snap.Issues[0].Key, dr.m.issues[0].Key)
	}
	if !snap.More {
		t.Error("a search with another page behind it was stored as complete")
	}
}

func TestList_StoresEveryPageTheUserScrolledThroughAndNotJustTheLast(t *testing.T) {
	t.Parallel()

	cache := newFakeCache()
	deps := withCache(testDeps(newFake(60, jiratest.WithPageSize(20))), cache)
	dr := openAll(t, deps, 120, 30)
	for range 32 {
		dr.key("j")
	}
	if len(dr.m.issues) <= 20 {
		t.Fatalf("the list only ever loaded %d rows, so nothing was paged", len(dr.m.issues))
	}

	snap, ok := cache.Rows(allJQL)
	if !ok {
		t.Fatal("nothing was stored")
	}
	if len(snap.Issues) != len(dr.m.issues) {
		t.Errorf("%d rows were stored against the %d the user has scrolled through", len(snap.Issues), len(dr.m.issues))
	}
}

func TestList_SaysSoWhenRowsCannotBeStored(t *testing.T) {
	t.Parallel()

	cache := newFakeCache()
	cache.putFail = errors.New("the cache file is read-only")
	deps := withCache(testDeps(newFake(10)), cache)

	dr := openAll(t, deps, 120, 20)

	if len(dr.m.issues) == 0 {
		t.Fatal("a cache that could not be written dropped rows that had already arrived")
	}
	var said bool
	for _, status := range dr.statuses {
		if strings.Contains(status.Text, "read-only") {
			said = true
		}
	}
	if !said {
		t.Errorf("nothing was said about a cache that refused the rows: %+v", dr.statuses)
	}
}

func TestList_APurgingRefreshDropsTheStoredCopyToo(t *testing.T) {
	t.Parallel()

	cache := newFakeCache()
	deps := withCache(testDeps(newFake(10)), cache)
	dr := openAll(t, deps, 120, 20)

	dr.send(kernel.RefreshMsg{Purge: true})

	if !slices.Contains(cache.forgot, allJQL) {
		t.Errorf("a purging refresh forgot %v, want it to drop %q", cache.forgot, allJQL)
	}
}

func TestList_AsksTheSiteAgainToPageOnFromStoredRowsThatWerePartial(t *testing.T) {
	t.Parallel()

	cache := newFakeCache()
	f := newFake(60, jiratest.WithPageSize(20))
	deps := withCache(testDeps(f), cache)
	cache.hold(allJQL, storedRows(4), false, true)

	dr := newDriver(t, deps, 120, 20)
	before := countCalls(f, "Search")
	dr.send(QueryMsg{JQL: allJQL, Title: "All issues"})

	if countCalls(f, "Search") == before {
		t.Fatal("rows that were only part of an answer left the rest unreachable")
	}
	if len(dr.m.issues) <= 4 {
		t.Errorf("the list still holds %d rows; stored rows carry no cursor, so the search is asked again",
			len(dr.m.issues))
	}
}

func TestList_ShowsAnOpenEndedCountForStoredRowsThatWerePartial(t *testing.T) {
	t.Parallel()

	cache := newFakeCache()
	deps := withCache(testDeps(nil), cache)
	jql, _ := defaultQuery(deps.Project)
	cache.hold(jql, storedRows(4), false, true)

	view, _ := New(deps).(*Model)
	next, _ := view.Update(kernel.SizeMsg{Width: 120, Height: 20})
	m, _ := next.(*Model)

	mustContain(t, m.View(), "4+ issues")
}

func TestList_WithNoCacheDrawsExactlyWhatItDrewBefore(t *testing.T) {
	t.Parallel()

	withNone := start(t, testDeps(newFake(12)), 120, 30)
	withEmpty := start(t, withCache(testDeps(newFake(12)), newFakeCache()), 120, 30)

	if frame(withNone) != frame(withEmpty) {
		t.Errorf("a session with nowhere to cache draws a different frame\n--- no cache ---\n%s\n--- empty cache ---\n%s",
			frame(withNone), frame(withEmpty))
	}
}

func TestList_WithNoCacheDrawsAFrameWhenTheSiteRefuses(t *testing.T) {
	t.Parallel()

	deps := testDeps(refusing(t, 10))
	if deps.Cache != nil {
		t.Fatal("this test is about a session with no cache at all")
	}
	dr := newDriver(t, deps, 120, 20)

	if len(dr.m.issues) != 0 {
		t.Errorf("a session with nothing stored and nothing reachable found %d rows", len(dr.m.issues))
	}
	if status := dr.lastStatus(); !strings.Contains(status.Text, "no such host") {
		t.Errorf("the status line reads %q, want the transport failure", status.Text)
	}
	mustNotContain(t, dr.view(), staleLabel)
	if view := dr.view(); strings.TrimSpace(view) == "" {
		t.Error("nothing was drawn at all")
	}
}

func TestStaleBadge_Golden(t *testing.T) {
	t.Parallel()

	cache := newFakeCache()
	f := newFake(20)
	deps := withCache(testDeps(f), cache)
	jql, _ := defaultQuery(deps.Project)
	cache.hold(jql, storedRows(6), true, false)
	f.FailNextN(20, &jira.TransportError{Op: "search", Err: errors.New("dial tcp: no such host")})

	m := start(t, deps, 120, 30)
	golden(t, "list_stale_120x30.golden", frame(m))
}

// firstMsg runs a command, unwrapping a batch, and returns the first message it
// produced. It is how the outcome a command reports is asserted without letting
// the model act on it.
func firstMsg(t *testing.T, cmd tea.Cmd) tea.Msg {
	t.Helper()

	queue := []tea.Cmd{cmd}
	for len(queue) > 0 {
		next := queue[0]
		queue = queue[1:]
		if next == nil {
			continue
		}
		msg := next()
		if msg == nil {
			continue
		}
		if cmds, ok := unwrapCmds(msg); ok {
			queue = append(queue, cmds...)
			continue
		}
		// The kernel takes the envelope off a view's own answer before it hands
		// the message inside to the view the address names.
		if reply, addressed := msg.(kernel.ReplyMsg); addressed {
			return reply.Msg
		}
		return msg
	}
	t.Fatal("the command produced no message")
	return nil
}

func BenchmarkFirstPaintFromCache(b *testing.B) {
	cache := newFakeCache()
	deps := kernel.Deps{
		Caps:    fullCaps(),
		Project: "PROJ",
		Theme:   kernel.NewTheme(kernel.ThemeDark, true, kernel.UnicodeGlyphs()),
		Now:     func() time.Time { return time.Date(2025, time.March, 5, 9, 0, 0, 0, time.UTC) },
		Cache:   cache,
	}
	jql, _ := defaultQuery(deps.Project)
	cache.hold(jql, storedRows(50), false, true)

	b.ReportAllocs()
	b.ResetTimer()
	for range b.N {
		view, _ := New(deps).(*Model)
		next, _ := view.Update(kernel.SizeMsg{Width: 120, Height: 40})
		m, _ := next.(*Model)
		_ = m.View()
	}
}
