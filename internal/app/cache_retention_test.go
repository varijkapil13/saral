package app

import (
	"fmt"
	"slices"
	"testing"
	"time"

	"github.com/varijkapil13/saral/internal/store"
	"github.com/varijkapil13/saral/pkg/jira"
	"github.com/varijkapil13/saral/pkg/jira/jiratest"
)

func heldKeys(t testing.TB, cache *DiskCache) []string {
	t.Helper()
	var held []string
	if _, err := cache.EachIssue(func(iss jira.Issue, _ time.Time) bool {
		held = append(held, iss.Key)
		return true
	}); err != nil {
		t.Fatalf("EachIssue: %v", err)
	}
	return held
}

func TestPutBoardPage_TheFirstPageReplacesTheOrderAndLaterOnesAppend(t *testing.T) {
	t.Parallel()

	cache, _ := newTestCache(t)
	all := boardIssues(7)
	if err := cache.PutBoard(7, BoardSnapshot{Config: testBoardConfig(7), Issues: boardIssues(9)}); err != nil {
		t.Fatalf("PutBoard: %v", err)
	}

	pages := [][]jira.Issue{all[:3], all[3:6], append([]jira.Issue{all[1]}, all[6:]...)}
	for i, page := range pages {
		snap := BoardSnapshot{
			Config: testBoardConfig(7), QuickFilters: testQuickFilters(7), Issues: page, More: i < len(pages)-1,
		}
		if err := cache.PutBoardPage(7, snap, i == 0); err != nil {
			t.Fatalf("PutBoardPage %d: %v", i, err)
		}
	}

	got, ok := cache.Board(7)
	if !ok {
		t.Fatal("the board stored page by page is a miss")
	}
	if want := keysOf(all); !slices.Equal(keysOf(got.Issues), want) {
		t.Errorf("the board holds %v, want %v: the first page replaces the old order and a repeated card stays where it was first seen",
			keysOf(got.Issues), want)
	}
	if got.More {
		t.Error("More is the first page's rather than the last one's")
	}
	if len(got.QuickFilters) != 1 {
		t.Errorf("quick filters %v, want the page's", got.QuickFilters)
	}
}

func TestPutBacklogPage_TheFirstPageReplacesTheOrderAndLaterOnesAppend(t *testing.T) {
	t.Parallel()

	cache, _ := newTestCache(t)
	all := boardIssues(5)
	sprints := []jira.Sprint{{ID: 3, Name: "Sprint 3", State: jira.SprintActive}}
	for i, page := range [][]jira.Issue{all[:2], all[2:]} {
		snap := BacklogSnapshot{Config: testBoardConfig(7), Sprints: sprints, Issues: page, More: i == 0}
		if err := cache.PutBacklogPage(7, snap, i == 0); err != nil {
			t.Fatalf("PutBacklogPage %d: %v", i, err)
		}
	}
	got, ok := cache.Backlog(7)
	if !ok {
		t.Fatal("the backlog stored page by page is a miss")
	}
	if !slices.Equal(keysOf(got.Issues), keysOf(all)) {
		t.Errorf("the backlog holds %v, want %v", keysOf(got.Issues), keysOf(all))
	}
	if len(got.Sprints) != 1 || got.More {
		t.Errorf("the shape is %+v / more=%t, want the last page's", got.Sprints, got.More)
	}

	if err := cache.PutBacklogPage(7, BacklogSnapshot{Config: testBoardConfig(7), Issues: all[4:]}, true); err != nil {
		t.Fatalf("PutBacklogPage: %v", err)
	}
	got, _ = cache.Backlog(7)
	if want := keysOf(all[4:]); !slices.Equal(keysOf(got.Issues), want) {
		t.Errorf("a fresh walk's first page left %v, want %v", keysOf(got.Issues), want)
	}
}

// What makes a page cheap is that the issues of earlier pages are not read,
// decoded and written again. Their write time is the witness: the old path
// restamped every card held on every page.
func TestPutBoardPage_LeavesTheEarlierPagesIssuesAlone(t *testing.T) {
	t.Parallel()

	cache, clk := newTestCache(t)
	all := boardIssues(4)
	if err := cache.PutBoardPage(7, BoardSnapshot{Config: testBoardConfig(7), Issues: all[:2]}, true); err != nil {
		t.Fatal(err)
	}
	clk.at = testNow.Add(time.Minute)
	if err := cache.PutBoardPage(7, BoardSnapshot{Config: testBoardConfig(7), Issues: all[2:]}, false); err != nil {
		t.Fatal(err)
	}
	first, ok := cache.Issue(all[0].Key)
	if !ok {
		t.Fatal("the first page's issue went missing")
	}
	if !first.StoredAt.Equal(testNow) {
		t.Errorf("%s was rewritten at %s by a later page", all[0].Key, first.StoredAt)
	}
	later, _ := cache.Issue(all[3].Key)
	if !later.StoredAt.Equal(testNow.Add(time.Minute)) {
		t.Errorf("%s stored at %s, want the second page's time", all[3].Key, later.StoredAt)
	}
}

func TestPutSearch_KeepsTheSearchCountInBounds(t *testing.T) {
	t.Parallel()

	cache, clk := newTestCache(t)
	keep := KindSearch.Retention().Keep
	row := listRows(1)
	for i := range keep + 1 {
		clk.at = testNow.Add(time.Duration(i) * time.Second)
		if err := cache.PutRows(fmt.Sprintf(`project = "P%d"`, i), row, false); err != nil {
			t.Fatalf("PutRows %d: %v", i, err)
		}
	}
	n, err := cache.db.Len(testScope, string(KindSearch))
	if err != nil {
		t.Fatal(err)
	}
	if n > keep || n < keep-keep/10 {
		t.Errorf("%d searches held after %d written, want between %d and %d", n, keep+1, keep-keep/10, keep)
	}
	if _, ok := cache.Rows(`project = "P0"`); ok {
		t.Error("the search stored longest ago survived the bound")
	}
	if _, ok := cache.Rows(fmt.Sprintf(`project = "P%d"`, keep)); !ok {
		t.Error("the newest search was dropped")
	}
}

func TestSweep_DropsWhatIsOlderThanItsRetention(t *testing.T) {
	t.Parallel()

	cache, clk := newTestCache(t)
	old := jiratest.GenFor("OLD", 2)
	if err := cache.PutRows(`project = "OLD"`, old, false); err != nil {
		t.Fatal(err)
	}
	if err := cache.PutBoard(1, BoardSnapshot{Config: testBoardConfig(1), Issues: old}); err != nil {
		t.Fatal(err)
	}
	if err := cache.PutCaps("OLD", jira.Capabilities{}); err != nil {
		t.Fatal(err)
	}

	clk.at = testNow.Add(KindSearch.Retention().MaxAge + time.Hour)
	fresh := listRows(2)
	if err := cache.PutRows(cacheJQL, fresh, false); err != nil {
		t.Fatal(err)
	}
	before := cache.Generation()

	removed, err := cache.Sweep()
	if err != nil {
		t.Fatalf("Sweep: %v", err)
	}
	if removed != 5 {
		t.Errorf("Sweep removed %d entries, want the old search, board, caps and its two issues", removed)
	}
	if _, ok := cache.Rows(`project = "OLD"`); ok {
		t.Error("a month-old search survived the sweep")
	}
	if _, ok := cache.Board(1); ok {
		t.Error("a month-old board survived the sweep")
	}
	if _, ok := cache.Caps("OLD"); ok {
		t.Error("a month-old probe answer survived the sweep")
	}
	if got := heldKeys(t, cache); !slices.Equal(got, keysOf(fresh)) {
		t.Errorf("issues held %v, want only the fresh %v", got, keysOf(fresh))
	}
	if _, ok := cache.Rows(cacheJQL); !ok {
		t.Error("the fresh search went with the old")
	}
	if cache.Generation() == before {
		t.Error("a sweep that removed entries left the generation where it was")
	}

	if n, err := cache.Sweep(); err != nil || n != 0 {
		t.Errorf("a second sweep removed %d (err %v), want nothing", n, err)
	}
}

func TestClear_EmptiesThisProfileAndLeavesAnotherAlone(t *testing.T) {
	t.Parallel()

	db := openDB(t)
	clk := &clock{at: testNow}
	mine := NewCache(db, testScope, WithClock(clk.now))
	theirs := NewCache(db, store.Scope{Site: testScope.Site, Account: "someone.else@example.com"}, WithClock(clk.now))
	for _, c := range []*DiskCache{mine, theirs} {
		if err := c.PutRows(cacheJQL, listRows(3), false); err != nil {
			t.Fatal(err)
		}
		if err := c.PutBacklog(7, BacklogSnapshot{Config: testBoardConfig(7), Issues: boardIssues(2)}); err != nil {
			t.Fatal(err)
		}
		if err := c.PutLastBoard("PROJ", 7); err != nil {
			t.Fatal(err)
		}
	}
	before := mine.Generation()

	if err := mine.Clear(); err != nil {
		t.Fatalf("Clear: %v", err)
	}
	if _, ok := mine.Rows(cacheJQL); ok {
		t.Error("the search survived a clear")
	}
	if _, ok := mine.Backlog(7); ok {
		t.Error("the backlog survived a clear")
	}
	if _, ok := mine.LastBoard("PROJ"); ok {
		t.Error("the last board survived a clear")
	}
	if got := heldKeys(t, mine); len(got) != 0 {
		t.Errorf("issues %v survived a clear", got)
	}
	if mine.Generation() == before {
		t.Error("a clear left the generation where it was, so an index over the cache still answers from it")
	}
	if _, ok := theirs.Rows(cacheJQL); !ok {
		t.Error("clearing one profile emptied another's")
	}

	if err := mine.PutRows(cacheJQL, listRows(1), false); err != nil {
		t.Fatalf("writing after a clear: %v", err)
	}
	if _, ok := mine.Rows(cacheJQL); !ok {
		t.Error("the cache cannot be written again after a clear")
	}
}

func TestPutRows_ASessionAtTheBoundTrimsOnceAndNotOnEveryWrite(t *testing.T) {
	t.Parallel()

	cache, clk := newTestCache(t, WithIssueBound(20))
	for i := range 21 {
		clk.at = testNow.Add(time.Duration(i) * time.Second)
		if err := cache.PutIssue(jiratest.GenFor("PROJ", 21)[i]); err != nil {
			t.Fatal(err)
		}
	}
	if n := len(heldKeys(t, cache)); n != 18 {
		t.Fatalf("%d issues held after going one over a bound of 20, want it trimmed a tenth below to 18", n)
	}
	gen := cache.Generation()
	if err := cache.PutIssue(jiratest.GenFor("MORE", 1)[0]); err != nil {
		t.Fatal(err)
	}
	if n := len(heldKeys(t, cache)); n != 19 {
		t.Errorf("%d issues held, want a write under the bound to trim nothing", n)
	}
	if cache.Generation() != gen+1 {
		t.Errorf("the generation moved by %d for one write under the bound", cache.Generation()-gen)
	}
}

// At 5,000 cards the old path read, decoded, merged and rewrote every one of
// them for each page of fifty; a page now costs its own fifty and the key list.
func benchmarkBoardAt5k(b *testing.B, perPage bool) {
	cache, _ := newTestCache(b)
	const total, size = 5000, 50
	all := boardIssues(total)
	cfg := testBoardConfig(7)
	for start := 0; start < total; start += size {
		if err := cache.PutBoardPage(7, BoardSnapshot{Config: cfg, Issues: all[start : start+size], More: true}, start == 0); err != nil {
			b.Fatal(err)
		}
	}
	last := all[total-size:]
	b.ReportAllocs()
	b.ResetTimer()
	for range b.N {
		var err error
		if perPage {
			err = cache.PutBoardPage(7, BoardSnapshot{Config: cfg, Issues: last}, false)
		} else {
			err = cache.PutBoard(7, BoardSnapshot{Config: cfg, Issues: all})
		}
		if err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkPutBoardPage_At5kIssues(b *testing.B) { benchmarkBoardAt5k(b, true) }

func BenchmarkPutBoard_At5kIssues(b *testing.B) { benchmarkBoardAt5k(b, false) }
