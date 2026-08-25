package palette

import (
	"errors"
	"maps"
	"runtime"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/varijkapil13/saral/internal/app"
	"github.com/varijkapil13/saral/internal/ui/kernel"
	"github.com/varijkapil13/saral/pkg/jira"
)

// fakeCache is an app.Cache in a map. The real one is bbolt-backed and lives
// under internal/app, which is why the palette takes the interface.
type fakeCache struct {
	mu     sync.Mutex
	issues map[string]jira.Issue
	stored map[string]time.Time
	gen    uint64
	walks  int
	fail   error
}

var _ app.Cache = (*fakeCache)(nil)

func newFakeCache() *fakeCache {
	return &fakeCache{issues: map[string]jira.Issue{}, stored: map[string]time.Time{}}
}

// hold puts an issue in as though a previous session had read it, with the title
// it was read with.
func (c *fakeCache) hold(key, summary string, storedAt time.Time) *fakeCache {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.issues[key] = jira.Issue{
		Key: key, Summary: summary,
		Requested: jira.NewFieldMask(app.ListProjection().IDs),
	}
	c.stored[key] = storedAt
	c.gen++
	return c
}

// holdUntitled puts in an issue stored by a read too narrow to have asked for a
// title, which is not the same as an issue whose title is empty.
func (c *fakeCache) holdUntitled(key string, storedAt time.Time) *fakeCache {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.issues[key] = jira.Issue{Key: key, Requested: jira.NewFieldMask([]string{"status"})}
	c.stored[key] = storedAt
	c.gen++
	return c
}

func (c *fakeCache) Rows(string) (app.Snapshot, bool) { return app.Snapshot{}, false }

func (c *fakeCache) PutRows(string, []jira.Issue, bool) error { return nil }

func (c *fakeCache) Forget(string) error { return nil }

func (c *fakeCache) EachIssue(fn func(jira.Issue, time.Time) bool) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.walks++
	for _, key := range slices.Sorted(maps.Keys(c.issues)) {
		if !fn(c.issues[key], c.stored[key]) {
			break
		}
	}
	return c.fail
}

func (c *fakeCache) Generation() uint64 {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.gen
}

func (c *fakeCache) walked() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.walks
}

// cachedDeps is a session that has read a few issues on an earlier run: one of
// them stored a minute ago, one of them yesterday, one of them by a read that
// never asked for a title.
func cachedDeps() (kernel.Deps, *fakeCache) {
	cache := newFakeCache().
		hold("PROJ-142", "Fix the login flow", clockAt.Add(-30*time.Second)).
		hold("PROJ-143", "Speed up the nightly export", clockAt.Add(-26*time.Hour)).
		hold("OTHER-7", "Rework webhook retries", clockAt.Add(-9*time.Minute)).
		holdUntitled("PROJ-9", clockAt.Add(-3*time.Hour))
	d := paletteDeps()
	d.Cache = cache
	return d, cache
}

func TestPalette_TypingFindsAnIssueTheCacheHoldsAndNotOnlyACommand(t *testing.T) {
	t.Parallel()

	d, _ := cachedDeps()
	p := fly(t, d, sample(), memoryTable(), 120, 24)
	p.typeText("login")

	if got := p.keys(); len(got) != 1 || got[0] != "PROJ-142" {
		t.Errorf("%q offers the issues %v, and the cache holds one with that in its title", "login", got)
	}
	mustContain(t, p.frame(), "PROJ-142", "Fix the login flow")
}

// The whole point of the index: a key nothing on screen has and no search asked
// for still finds the issue, with no site to ask.
func TestPalette_FindsACachedIssueByItsKeyWithNothingToAsk(t *testing.T) {
	t.Parallel()

	d, _ := cachedDeps()
	if d.Jira != nil {
		t.Fatal("this test proves there is no round trip by having nothing to make one with")
	}
	p := fly(t, d, sample(), memoryTable(), 120, 24)
	p.typeText("other-7")

	if got := p.keys(); len(got) == 0 || got[0] != "OTHER-7" {
		t.Errorf("%q offers the issues %v", "other-7", got)
	}
}

// Both halves of the list are offered at once — docs/UX.md asks the palette to be
// everything, fuzzy — with the commands first, because they are what the filter
// was registered against.
func TestPalette_OffersCommandsAndIssuesTogetherWithTheCommandsFirst(t *testing.T) {
	t.Parallel()

	d, _ := cachedDeps()
	p := fly(t, d, sample(), memoryTable(), 120, 24)
	p.typeText("e")

	if len(p.titles()) == 0 || len(p.keys()) == 0 {
		t.Fatalf("%q offers %d commands and %d issues; both halves should answer it",
			"e", len(p.titles()), len(p.keys()))
	}
	issues := false
	for i, at := range p.m.shown {
		if at.issue {
			issues = true
			continue
		}
		if issues {
			t.Fatalf("row %d is a command drawn below an issue", i)
		}
	}
	mustContain(t, p.frame(), "My issues", "PROJ-142")
}

func TestPalette_OpeningACachedIssuePutsTheDetailPaneOverWhatThePaletteCovered(t *testing.T) {
	t.Parallel()

	d, _ := cachedDeps()
	p := fly(t, d, sample(), memoryTable(), 120, 24)
	p.typeText("login")
	p.press("down")
	for !p.m.onIssue() {
		p.press("down")
	}
	p.press("enter")

	if got := p.pushed(); len(got) != 1 || got[0] != "issue:PROJ-142" {
		t.Fatalf("enter over an issue pushed %v, want the detail pane for PROJ-142", got)
	}
	if !p.popped() {
		t.Error("the palette stayed on the stack under the issue, so esc goes back to a finished filter")
	}
	if got := p.ran(); len(got) != 0 {
		t.Errorf("enter over an issue ran the commands %v", got)
	}
}

// The row is the detail pane's first paint, so what the cache holds is handed
// over rather than fetched again.
func TestPalette_SeedsTheDetailPaneWithTheTitleTheCacheHeld(t *testing.T) {
	t.Parallel()

	d, _ := cachedDeps()
	p := fly(t, d, sample(), memoryTable(), 120, 24)
	p.typeText("login")

	at := slices.IndexFunc(p.m.shown, func(e entry) bool { return e.issue })
	if at < 0 {
		t.Fatal("no issue was offered at all")
	}
	got := p.m.hits[p.m.shown[at].at]
	if got.summary != "Fix the login flow" {
		t.Errorf("the row carries the summary %q into the detail pane", got.summary)
	}
}

func TestPalette_EnterOverAnIssueIsAdvertisedAsOpeningIt(t *testing.T) {
	t.Parallel()

	d, _ := cachedDeps()
	p := fly(t, d, sample(), memoryTable(), 120, 24)
	p.typeText("login")
	for !p.m.onIssue() {
		p.press("down")
	}

	set, gen := p.m.LiveKeys()
	if gen != int(keysIssue) {
		t.Fatalf("the cursor is on an issue and the keys are in state %d", gen)
	}
	if labels := shortOf(set); !strings.Contains(labels, "open it") || strings.Contains(labels, "run it") {
		t.Errorf("the footer says %q over an issue", labels)
	}
}

func TestPalette_SaysSoWhereATitleWasNeverStoredRatherThanDrawingABlank(t *testing.T) {
	t.Parallel()

	d, _ := cachedDeps()
	p := fly(t, d, sample(), memoryTable(), 120, 24)
	p.typeText("proj-9")

	line := lineWith(t, p.frame(), "PROJ-9")
	if !strings.Contains(line, noTitle) {
		t.Errorf("the row for an issue read without its title reads %q", line)
	}
}

func TestPalette_SaysHowOldEachCopyIs(t *testing.T) {
	t.Parallel()

	d, _ := cachedDeps()
	p := fly(t, d, sample(), memoryTable(), 120, 24)
	p.typeText("r")

	for _, want := range []struct{ key, age string }{
		{key: "PROJ-142", age: "just now"},
		{key: "OTHER-7", age: "9m old"},
		{key: "PROJ-9", age: "3h old"},
		{key: "PROJ-143", age: "1d old"},
	} {
		line := lineWith(t, p.frame(), want.key)
		if !strings.Contains(line, want.age) {
			t.Errorf("the row for %s reads %q, want it to say %q", want.key, line, want.age)
		}
	}
}

// The badge is the theme's, so a copy past the cache's own idea of current is
// drawn differently from one inside it rather than merely described differently.
func TestPalette_BadgesACopyOlderThanTheCacheCallsCurrent(t *testing.T) {
	t.Parallel()

	fresh := newHit(app.Hit{Key: "PROJ-1", Summary: "x", HasSummary: true,
		StoredAt: clockAt.Add(-app.KindIssue.TTL() / 2)}, clockAt)
	old := newHit(app.Hit{Key: "PROJ-2", Summary: "x", HasSummary: true,
		StoredAt: clockAt.Add(-2 * app.KindIssue.TTL())}, clockAt)
	if fresh.stale {
		t.Errorf("a copy written %s ago is badged stale", app.KindIssue.TTL()/2)
	}
	if !old.stale {
		t.Errorf("a copy written %s ago is not badged", 2*app.KindIssue.TTL())
	}

	theme := kernel.NewTheme(kernel.ThemeDark, true, kernel.UnicodeGlyphs())
	st := newStyles(theme)
	lay := planLayout(120, 4)
	if renderHit(&old, lay, false, st, theme) == renderHit(&fresh, lay, false, st, theme) {
		t.Error("the two rows are drawn identically, so the badge is not on either of them")
	}
}

// A copy written in the future is a clock that moved, not an age to draw a
// negative number for.
func TestPalette_ReadsACopyStoredInTheFutureAsJustNow(t *testing.T) {
	t.Parallel()

	got := newHit(app.Hit{Key: "PROJ-1", StoredAt: clockAt.Add(time.Hour)}, clockAt)
	if got.age != "just now" || got.stale {
		t.Errorf("a copy stored an hour from now reads %q (stale=%t)", got.age, got.stale)
	}
}

// A session with nowhere to cache is a first run, another copy of Saral holding
// the file, or an unwritable home. The palette says so and keeps working.
func TestPalette_WithNowhereToCacheOffersNoIssuesAndSaysWhy(t *testing.T) {
	t.Parallel()

	d := paletteDeps()
	d.Cache = nil
	p := fly(t, d, sample(), memoryTable(), 120, 24)
	p.typeText("zzzz")

	if got := p.keys(); len(got) != 0 {
		t.Errorf("a session with no cache offered the issues %v", got)
	}
	mustContain(t, p.frame(), "nowhere to cache issues")
	mustNotContain(t, p.frame(), "which issue?")
}

func TestPalette_WithAnEmptyCacheSaysNothingHasBeenStoredYet(t *testing.T) {
	t.Parallel()

	d := paletteDeps()
	d.Cache = newFakeCache()
	p := fly(t, d, sample(), memoryTable(), 120, 24)
	mustContain(t, p.frame(), "which issue?")

	p.typeText("zzzz")
	mustContain(t, p.frame(), "No issue has been cached")
}

// An issue that cannot be decoded is reported rather than swallowed, and the
// issues that could be read are still offered.
func TestPalette_ReportsACachedIssueItCouldNotRead(t *testing.T) {
	t.Parallel()

	d, cache := cachedDeps()
	cache.fail = errors.New("the cached copy of PROJ-9 cannot be read")
	p := fly(t, d, sample(), memoryTable(), 120, 24)
	p.typeText("login")

	if got := p.statuses(); len(got) != 1 || !strings.Contains(got[0], "PROJ-9") {
		t.Fatalf("the palette said %v about a cached issue it could not read", got)
	}
	if got := p.keys(); len(got) == 0 {
		t.Error("one unreadable issue took the rest of the cache with it")
	}
}

// docs/PERFORMANCE.md: the index rebuilds only when the cache has moved, so a
// filter being typed re-ranks what it walked once.
func TestPalette_WalksTheCacheOnceWhileAFilterIsTyped(t *testing.T) {
	t.Parallel()

	d, cache := cachedDeps()
	p := fly(t, d, sample(), memoryTable(), 120, 24)
	p.typeText("login")
	if got := cache.walked(); got != 1 {
		t.Errorf("five keystrokes walked the cache %d times", got)
	}

	cache.hold("PROJ-200", "Another login problem", clockAt)
	p.press("backspace")
	if got := cache.walked(); got != 2 {
		t.Errorf("the cache moved and was walked %d times in total", got)
	}
	if !slices.Contains(p.keys(), "PROJ-200") {
		t.Errorf("the issue stored while the palette was open is not offered: %v", p.keys())
	}
}

func TestPalette_ClickingACachedIssueSelectsItAndClickingItAgainOpensIt(t *testing.T) {
	t.Parallel()

	d, _ := cachedDeps()
	p := fly(t, d, sample(), memoryTable(), 120, 24)
	p.typeText("r")
	if p.m.onIssue() {
		t.Fatal("the cursor is already on the row this clicks, so the first click cannot be the selecting one")
	}
	at := p.hitZone("PROJ-142")
	click := tea.MouseClickMsg{X: at.StartX + 2, Y: at.StartY, Button: tea.MouseLeft}

	p.send(click)
	if got := p.m.selection(); !got.issue || got.id != "PROJ-142" {
		t.Fatalf("the click selected %+v", got)
	}
	if got := p.pushed(); len(got) != 0 {
		t.Fatalf("the first click opened %v; docs/UX.md gives one click the selection", got)
	}

	p.send(click)
	if got := p.pushed(); len(got) != 1 || got[0] != "issue:PROJ-142" {
		t.Errorf("a second click on the selected issue pushed %v", got)
	}
}

// The cursor is on a row, not on an index: a probe landing while an issue is
// selected must not move the selection onto a command.
func TestPalette_KeepsTheSelectedIssueWhenTheCapabilitiesChange(t *testing.T) {
	t.Parallel()

	d, _ := cachedDeps()
	p := fly(t, d, sample(), memoryTable(), 120, 24)
	p.typeText("login")
	for !p.m.onIssue() {
		p.press("down")
	}
	before := p.m.selection()

	p.send(kernel.CapabilitiesMsg{Caps: fullCaps()})
	if got := p.m.selection(); got != before {
		t.Errorf("the selection moved from %+v to %+v when the probe answered", before, got)
	}
}

// hitZone resolves the click target the palette marked a cached issue with.
func (p *pilot) hitZone(key string) zoneBounds {
	p.t.Helper()
	_ = p.m.deps.Zones.Scan(p.m.View())
	deadline := time.Now().Add(5 * time.Second)
	for {
		if at := p.m.deps.Zones.Get(p.m.zonePrefix + zoneHit + key); !at.IsZero() {
			return zoneBounds{StartX: at.StartX, StartY: at.StartY}
		}
		if time.Now().After(deadline) {
			p.t.Fatalf("the palette never marked a click target for %q", key)
		}
		runtime.Gosched()
	}
}
