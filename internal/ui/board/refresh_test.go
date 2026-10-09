package board

import (
	"context"
	"encoding/json"
	"errors"
	"slices"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"

	appcache "github.com/varijkapil13/saral/internal/app/cache"
	"github.com/varijkapil13/saral/internal/ui/kernel"
	"github.com/varijkapil13/saral/pkg/jira"
	"github.com/varijkapil13/saral/pkg/jira/jiratest"
)

const longBoard = 250

// snapshotOf is what a session that read this site in full would have stored,
// its sprint included, so the chrome drawn from it is the chrome drawn after.
func snapshotOf(t *testing.T, site *jiratest.Fake) appcache.BoardSnapshot {
	t.Helper()
	dr := newDriver(t, testDeps(site), 120, 20)
	return appcache.BoardSnapshot{
		Config: dr.m.rawConfig, QuickFilters: dr.m.quickFilters, Issues: slices.Clone(dr.m.issues),
		Sprints: dr.m.sprints, Sprint: dr.m.sprint.ID,
	}
}

func newLongFake() *jiratest.Fake { return newFake(longBoard, jiratest.WithPageSize(pageSize)) }

func isFirstPage(msg tea.Msg) bool {
	page, ok := msg.(issuesMsg)
	return ok && page.first
}

func (d *driver) releaseOne() {
	d.t.Helper()
	if len(d.heldPages) == 0 {
		d.t.Fatal("no page is held")
	}
	msg := d.heldPages[0]
	d.heldPages = d.heldPages[1:]
	d.send(msg)
}

func (d *driver) onScreen() int {
	n := 0
	for c := range d.m.cols {
		n += len(d.m.cols[c]) + d.m.foldedIn(c)
	}
	return n
}

func (d *driver) tops() []string {
	out := make([]string, len(d.m.cols))
	for c := range d.m.cols {
		if iss := d.m.issueAt(c, d.m.rowTopAt(c)); iss != nil {
			out[c] = iss.Key
		}
	}
	return out
}

// readDeep puts the cursor on a card from the last page and scrolls every
// other long column part of the way down, and says which card that is.
func (d *driver) readDeep() string {
	d.t.Helper()
	if len(d.m.issues) != longBoard {
		d.t.Fatalf("setup: the board holds %d cards, want %d", len(d.m.issues), longBoard)
	}
	key := d.m.issues[longBoard-10].Key
	col, row := -1, -1
	for c := range d.m.cols {
		if r := d.m.rowOf(c, key); r >= 0 {
			col, row = c, r
		}
	}
	if col < 0 {
		d.t.Fatalf("setup: %s is in no column", key)
	}
	h := d.m.rowsHeight()
	scrolled := 0
	for c := range d.m.cols {
		if c != col && d.m.columnLen(c) > 2*h {
			d.moveTo(c, d.m.columnLen(c)/2+h)
			scrolled++
		}
	}
	if scrolled == 0 {
		d.t.Fatal("setup: no other column is long enough to scroll")
	}
	d.moveTo(col, row)
	if d.m.rowTopAt(col) == 0 {
		d.t.Fatal("setup: the cursor's column did not scroll")
	}
	return key
}

// walkHeld runs a refresh whose pages are held, checking between every page
// that the board never shows fewer cards than it did and the cursor never
// leaves its card.
func (d *driver) walkHeld(key string) {
	d.t.Helper()
	want := d.onScreen()
	check := func(when string) {
		d.t.Helper()
		if got := d.onScreen(); got < want {
			d.t.Fatalf("%s the board shows %d cards, fewer than the %d it showed", when, got, want)
		}
		if !strings.Contains(d.view(), strconv.Itoa(want)+" cards") {
			d.t.Fatalf("%s the count no longer says %d cards:\n%s", when, want, d.view())
		}
		if got := d.m.selectedKey(); got != key {
			d.t.Fatalf("%s the cursor sits on %s, want %s", when, got, key)
		}
	}
	d.unpark()
	check("after the first page")
	pages := 1
	for len(d.heldPages) > 0 {
		d.releaseOne()
		pages++
		check("after page " + strconv.Itoa(pages))
	}
	if pages < 3 {
		d.t.Fatalf("the walk took %d pages, want at least 3", pages)
	}
	d.park, d.holdPages = nil, false
}

func assertKeptPlace(t *testing.T, dr *driver, key string, tops []string) {
	t.Helper()
	if dr.m.next != nil || dr.m.loading {
		t.Fatal("the walk never finished")
	}
	if got := len(dr.m.issues); got != longBoard {
		t.Errorf("the board holds %d cards after the walk, want %d", got, longBoard)
	}
	if got := dr.m.selectedKey(); got != key {
		t.Errorf("the cursor sits on %s after the walk, want %s", got, key)
	}
	if got := dr.tops(); !slices.Equal(got, tops) {
		t.Errorf("the columns open on %v after the walk, want %v", got, tops)
	}
}

func TestRefresh_AnRKeepsTheWholeBoardAndTheReadersPlace(t *testing.T) {
	t.Parallel()
	dr := newDriver(t, testDeps(newLongFake()), 160, 16)
	key := dr.readDeep()
	tops := dr.tops()

	dr.park, dr.holdPages = isFirstPage, true
	dr.send(kernel.RefreshMsg{})
	dr.walkHeld(key)

	assertKeptPlace(t, dr, key, tops)
}

func TestRefresh_ComingBackAfterTheTTLKeepsTheWholeBoardAndTheReadersPlace(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, time.March, 5, 9, 0, 0, 0, time.UTC)
	var mu sync.Mutex
	d := testDeps(newLongFake())
	d.Now = func() time.Time {
		mu.Lock()
		defer mu.Unlock()
		return now
	}
	dr := newDriver(t, d, 160, 16)
	key := dr.readDeep()
	tops := dr.tops()

	dr.send(kernel.FocusMsg{Focused: false})
	mu.Lock()
	now = now.Add(appcache.KindBoard.TTL() + time.Minute)
	mu.Unlock()
	dr.park, dr.holdPages = isFirstPage, true
	dr.send(kernel.FocusMsg{Focused: true})
	if len(dr.parked) == 0 {
		t.Fatal("coming back to an aged board did not read it again")
	}
	dr.walkHeld(key)

	assertKeptPlace(t, dr, key, tops)
	mustNotContain(t, dr.view(), staleLabel)
}

func TestRefresh_AStaleSnapshotRevalidatesWithoutMovingTheReader(t *testing.T) {
	t.Parallel()
	cache := newFakeCache()
	snap := snapshotOf(t, newLongFake())
	cache.hold("PROJ", snap.Config.BoardID, snap, true)

	view, ok := New(withCache(testDeps(newLongFake()), cache)).(*Model)
	if !ok {
		t.Fatal("New did not return a *Model")
	}
	dr := &driver{t: t, m: view, park: isFirstPage, holdPages: true}
	dr.send(kernel.SizeMsg{Width: 160, Height: 16})
	dr.send(kernel.FocusMsg{Focused: true})
	key := dr.readDeep()
	tops := dr.tops()
	dr.run(dr.m.Init())
	if len(dr.parked) == 0 {
		t.Fatal("a stale snapshot was not revalidated")
	}
	mustContain(t, dr.view(), staleLabel)
	dr.walkHeld(key)

	assertKeptPlace(t, dr, key, tops)
	mustNotContain(t, dr.view(), staleLabel)
}

// A board that fits on one page swaps the same way, so cards shifting under a
// scrolled column leave it opening on the card it opened on.
func TestRefresh_OnePageKeepsEachColumnOnItsTopCard(t *testing.T) {
	t.Parallel()
	issues := append(genIssues("PROJ-B", "10201", "Backlog", 40), genIssues("PROJ-D", "10202", "Done", 20)...)
	_, dr := stocked(t, twoColumnBoard(), issues, 40, 10)
	dr.moveTo(1, 19)
	dr.moveTo(0, 30)
	tops := dr.tops()
	if tops[0] == "PROJ-B1" || tops[1] == "PROJ-D1" {
		t.Fatalf("setup: the columns did not scroll: %v", tops)
	}

	dr.send(firstPage(dr.m.gen, slices.Concat(issues[5:40], issues[43:])))

	if got := dr.tops(); !slices.Equal(got, tops) {
		t.Errorf("the columns open on %v, want %v", got, tops)
	}
	if got := dr.m.selectedKey(); got != "PROJ-B31" {
		t.Errorf("the cursor sits on %s, want PROJ-B31", got)
	}
}

func TestRefresh_ACardGoneFromTheBoardLeavesTheCursorOnItsNeighbour(t *testing.T) {
	t.Parallel()
	fake := newLongFake()
	dr := newDriver(t, testDeps(fake), 160, 16)
	key := dr.readDeep()
	col, row := dr.m.curCol, dr.m.curRow
	below := dr.column(col)[row+1]

	if err := fake.MoveToBacklog(context.Background(), []string{key}); err != nil {
		t.Fatal(err)
	}
	dr.send(kernel.RefreshMsg{})

	if dr.m.byKey(key) != nil {
		t.Fatalf("setup: %s is still on the board", key)
	}
	if dr.m.curCol != col || dr.m.selectedKey() != below {
		t.Errorf("the cursor sits on %s in column %d, want %s, the card under the one that left, in column %d",
			dr.m.selectedKey(), dr.m.curCol, below, col)
	}
}

func TestRefresh_AWalkThatBreaksOffKeepsTheBoardItWasReplacing(t *testing.T) {
	t.Parallel()
	for name, failure := range map[string]error{
		"a 403":               &jira.CapabilityError{Reason: "you need Browse Projects in this project"},
		"a rate limit":        &jira.RateLimitError{RetryAfter: 30 * time.Second},
		"a transport failure": &jira.TransportError{Op: "GET /board", Err: errors.New("connection reset")},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			fake := newLongFake()
			dr := newDriver(t, testDeps(fake), 160, 16)
			key := dr.readDeep()
			tops := dr.tops()
			was := slices.Clone(dr.m.issues)

			dr.park = isFirstPage
			dr.send(kernel.RefreshMsg{})
			fake.FailNext(failure)
			dr.unpark()

			if dr.m.next != nil || dr.m.loading {
				t.Error("the walk that broke off is still held as in flight")
			}
			if !slices.EqualFunc(dr.m.issues, was, func(a, b jira.Issue) bool { return a.Key == b.Key }) {
				t.Errorf("the board holds %d cards after the walk broke off, want the %d it had", len(dr.m.issues), len(was))
			}
			if got := dr.m.selectedKey(); got != key {
				t.Errorf("the cursor sits on %s, want %s", got, key)
			}
			if got := dr.tops(); !slices.Equal(got, tops) {
				t.Errorf("the columns open on %v, want %v", got, tops)
			}
			mustContain(t, dr.view(), staleLabel)
			if got := dr.lastStatus(); got.Level != kernel.LevelWarn || !strings.Contains(got.Text, failure.Error()) {
				t.Errorf("status = %+v, want a warning naming %q", got, failure.Error())
			}
		})
	}
}

func TestRefresh_ASecondRDropsTheFirstWalk(t *testing.T) {
	t.Parallel()
	dr := newDriver(t, testDeps(newLongFake()), 160, 16)
	key := dr.readDeep()
	tops := dr.tops()

	dr.park, dr.holdPages = isFirstPage, true
	dr.send(kernel.RefreshMsg{})
	dr.unpark()
	if dr.m.next == nil {
		t.Fatal("setup: the first walk holds nothing")
	}
	dr.send(kernel.RefreshMsg{})
	if dr.m.next != nil {
		t.Error("a second r kept the first walk's pages")
	}
	old := dr.heldPages
	dr.heldPages = nil
	for _, msg := range old {
		dr.send(msg)
	}
	if dr.m.next != nil || len(dr.heldPages) != 0 {
		t.Error("a page of the first walk was taken into the second")
	}
	dr.walkHeld(key)

	assertKeptPlace(t, dr, key, tops)
}

func TestRefresh_APickOnTheLastPageSurvives(t *testing.T) {
	t.Parallel()
	dr := newDriver(t, testDeps(newLongFake()), 160, 16)
	key := dr.readDeep()
	dr.key("space")
	if !dr.m.picked[key] {
		t.Fatalf("setup: %s was not picked", key)
	}

	dr.park, dr.holdPages = isFirstPage, true
	dr.send(kernel.RefreshMsg{})
	dr.walkHeld(dr.m.selectedKey())

	if !dr.m.picked[key] {
		t.Errorf("%s was let go of by a refresh that still returns it", key)
	}
}

type recording struct {
	*jiratest.Fake
	mu    sync.Mutex
	asked [][]string
}

func (r *recording) SprintIssues(ctx context.Context, boardID, sprintID int64, q jira.BoardQuery) (jira.Page[jira.Issue], error) {
	r.mu.Lock()
	r.asked = append(r.asked, slices.Clone(q.QuickFilters))
	r.mu.Unlock()
	return r.Fake.SprintIssues(ctx, boardID, sprintID, q)
}

func (r *recording) reads() [][]string {
	r.mu.Lock()
	defer r.mu.Unlock()
	return slices.Clone(r.asked)
}

func recalledFilter(t *testing.T, fake *jiratest.Fake) (*fakeMemory, jira.QuickFilter) {
	t.Helper()
	boards, err := fake.Boards(context.Background(), "PROJ")
	if err != nil || len(boards) == 0 {
		t.Fatalf("setup: no board: %v", err)
	}
	qfs, err := fake.QuickFilters(context.Background(), boards[0].ID)
	if err != nil || len(qfs) == 0 {
		t.Fatalf("setup: no quick filter: %v", err)
	}
	setupCalls.Store(fake, len(fake.Calls()))
	ids, err := json.Marshal([]int64{qfs[0].ID})
	if err != nil {
		t.Fatal(err)
	}
	mem := newFakeMemory()
	mem.state[ViewID+"."+quickFiltersMemoryKey] = string(ids)
	return mem, qfs[0]
}

func assertEveryReadFiltered(t *testing.T, site *recording, qf jira.QuickFilter) {
	t.Helper()
	reads := site.reads()
	if len(reads) == 0 {
		t.Fatal("the cards were never read")
	}
	for i, got := range reads {
		if !slices.Equal(got, []string{qf.JQL}) {
			t.Errorf("card read %d asked with quick filters %q, want only %q", i, got, qf.JQL)
		}
	}
}

func TestRefresh_ARecalledQuickFilterIsInTheFirstCardRead(t *testing.T) {
	t.Parallel()
	fake := newFake(12)
	mem, qf := recalledFilter(t, fake)
	site := &recording{Fake: fake}

	dr := newDriver(t, withMemory(testDeps(site), mem), 120, 20)

	assertEveryReadFiltered(t, site, qf)
	if !dr.m.qfOn[qf.ID] {
		t.Errorf("%s is not on", qf.Name)
	}
	mustContain(t, dr.view(), "filters: "+qf.Name)
}

func TestRefresh_ARecalledQuickFilterStaysOnThroughARevalidation(t *testing.T) {
	t.Parallel()
	cache := newFakeCache()
	snap := snapshotOf(t, newFake(12))
	cache.hold("PROJ", snap.Config.BoardID, snap, true)
	fake := newFake(12)
	mem, qf := recalledFilter(t, fake)
	site := &recording{Fake: fake}

	view, ok := New(withMemory(withCache(testDeps(site), cache), mem)).(*Model)
	if !ok {
		t.Fatal("New did not return a *Model")
	}
	dr := &driver{t: t, m: view, park: isFirstPage}
	dr.send(kernel.SizeMsg{Width: 120, Height: 20})
	dr.send(kernel.FocusMsg{Focused: true})
	mustContain(t, dr.view(), "filters: "+qf.Name)
	dr.run(dr.m.Init())
	if len(dr.parked) == 0 {
		t.Fatal("a stale snapshot was not revalidated")
	}
	mustContain(t, dr.view(), "filters: "+qf.Name)
	dr.park = nil
	dr.unpark()

	assertEveryReadFiltered(t, site, qf)
	if got := len(site.reads()); got != 1 {
		t.Errorf("the cards were read %d times, want once: the quick filters answered what was already on", got)
	}
	mustContain(t, dr.view(), "filters: "+qf.Name)
}

// A move that lands while the walk is out is newer than the page that read the
// card before it, so the swap keeps the moved copy.
func TestRefresh_AMoveThatLandsDuringTheWalkIsKept(t *testing.T) {
	t.Parallel()
	dr := newDriver(t, testDeps(newLongFake()), 160, 16)
	key := dr.readDeep()
	to := (dr.m.curCol + 1) % len(dr.m.plan.columns)
	var status jira.Status
	for id, col := range dr.m.plan.byStatus {
		if col == to {
			status.ID = id
		}
	}

	dr.park, dr.holdPages = isFirstPage, true
	dr.send(kernel.RefreshMsg{})
	dr.unpark()
	dr.send(movedMsg{gen: dr.m.moveGen, key: key, status: status})
	dr.park = nil
	dr.release()

	if dr.m.next != nil {
		t.Fatal("the walk never finished")
	}
	if got := dr.m.byKey(key); got == nil || got.Status.ID != status.ID {
		t.Errorf("%s shows %+v after the walk, want the status it moved to, %s", key, got, status.ID)
	}
	if dr.m.curCol != to || dr.m.selectedKey() != key {
		t.Errorf("the cursor sits on %s in column %d, want %s in column %d", dr.m.selectedKey(), dr.m.curCol, key, to)
	}
}
