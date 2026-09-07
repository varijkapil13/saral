package app

import (
	"slices"
	"testing"
	"time"

	"github.com/varijkapil13/saral/internal/store"
	"github.com/varijkapil13/saral/pkg/jira"
	"github.com/varijkapil13/saral/pkg/jira/jiratest"
)

func testBoardConfig(id int64) jira.BoardConfig {
	return jira.BoardConfig{
		BoardID: id, Name: "Sprint board", Type: jira.BoardScrum,
		Columns: []jira.Column{
			{Name: "To Do", StatusIDs: []string{"10000"}},
			{Name: "Done", StatusIDs: []string{"10001"}},
		},
		RankFieldID: "customfield_10019",
	}
}

func testQuickFilters(id int64) []jira.QuickFilter {
	return []jira.QuickFilter{{ID: id*10 + 1, Name: "Only My Issues", JQL: "assignee = currentUser()"}}
}

// boardIssues is what a board's own read stores: the six fields of
// ListProjection plus reporter and labels, and a mask saying so — the same
// shape board.plan.projection asks for.
func boardIssues(n int) []jira.Issue {
	mask := jira.NewFieldMask(ListProjection().With("reporter", "labels").IDs)
	out := jiratest.Gen(n)
	for i := range out {
		out[i].Requested = mask
	}
	return out
}

func TestBoard_ComesBackAsItWasStored(t *testing.T) {
	t.Parallel()

	cache, _ := newTestCache(t)
	stored := boardIssues(3)
	snap := BoardSnapshot{Config: testBoardConfig(7), QuickFilters: testQuickFilters(7), Issues: stored, More: true}
	if err := cache.PutBoard(7, snap); err != nil {
		t.Fatalf("PutBoard: %v", err)
	}

	got, ok := cache.Board(7)
	if !ok {
		t.Fatal("the board just stored is not there")
	}
	if got.Config.BoardID != 7 || len(got.Config.Columns) != 2 {
		t.Errorf("the config came back as %+v", got.Config)
	}
	if len(got.QuickFilters) != 1 || got.QuickFilters[0].Name != "Only My Issues" {
		t.Errorf("the quick filters came back as %+v", got.QuickFilters)
	}
	if !slices.Equal(keysOf(got.Issues), keysOf(stored)) {
		t.Errorf("the cards came back as %v, want %v in that order", keysOf(got.Issues), keysOf(stored))
	}
	if !got.More {
		t.Error("a board stored with another page behind it came back looking complete")
	}
	if got.Stale {
		t.Error("a board stored this instant came back stale")
	}
	if !got.StoredAt.Equal(testNow) {
		t.Errorf("the board says it was stored at %s, want %s", got.StoredAt, testNow)
	}
}

func TestBoard_IsAMissWhenNothingWasStoredForThatID(t *testing.T) {
	t.Parallel()

	cache, _ := newTestCache(t)
	if err := cache.PutBoard(7, BoardSnapshot{Config: testBoardConfig(7), Issues: boardIssues(1)}); err != nil {
		t.Fatalf("PutBoard: %v", err)
	}
	if _, ok := cache.Board(9); ok {
		t.Error("a board id nobody stored found one")
	}
}

func TestBoard_TurnsStaleOnceTheBoardTTLHasPassed(t *testing.T) {
	t.Parallel()

	cache, clk := newTestCache(t)
	if err := cache.PutBoard(7, BoardSnapshot{Config: testBoardConfig(7), Issues: boardIssues(2)}); err != nil {
		t.Fatalf("PutBoard: %v", err)
	}

	clk.at = testNow.Add(KindBoard.TTL())
	if got, _ := cache.Board(7); got.Stale {
		t.Error("a board exactly at its TTL is still current")
	}
	clk.at = testNow.Add(KindBoard.TTL() + time.Second)
	got, ok := cache.Board(7)
	if !ok {
		t.Fatal("a stale board was dropped rather than badged; seeing yesterday's board beats seeing none")
	}
	if !got.Stale {
		t.Error("a board past its TTL did not come back stale")
	}
	if len(got.Issues) != 2 {
		t.Errorf("a stale board came back with %d cards, want the 2 that were stored", len(got.Issues))
	}
}

func TestForgetBoard_DropsTheBoardAndKeepsTheIssuesItHeld(t *testing.T) {
	t.Parallel()

	cache, _ := newTestCache(t)
	stored := boardIssues(2)
	if err := cache.PutBoard(7, BoardSnapshot{Config: testBoardConfig(7), Issues: stored}); err != nil {
		t.Fatalf("PutBoard: %v", err)
	}
	if err := cache.ForgetBoard(7); err != nil {
		t.Fatalf("ForgetBoard: %v", err)
	}
	if _, ok := cache.Board(7); ok {
		t.Error("the board survived being forgotten")
	}
	seen := 0
	if _, err := cache.EachIssue(func(jira.Issue, time.Time) bool { seen++; return true }); err != nil {
		t.Fatalf("EachIssue: %v", err)
	}
	if seen != len(stored) {
		t.Errorf("forgetting the board dropped %d of its issues; they are shared with every other read", len(stored)-seen)
	}
}

func TestBoard_SkipsARecordItCannotDecode(t *testing.T) {
	t.Parallel()

	cache, _ := newTestCache(t)
	if err := cache.PutBoard(7, BoardSnapshot{Config: testBoardConfig(7), Issues: boardIssues(1)}); err != nil {
		t.Fatalf("PutBoard: %v", err)
	}
	corrupt(t, cache, KindBoard, "7")

	if _, ok := cache.Board(7); ok {
		t.Error("a record that cannot be decoded was served anyway")
	}
}

func TestLastBoard_RoundTrips(t *testing.T) {
	t.Parallel()

	cache, _ := newTestCache(t)
	if _, ok := cache.LastBoard("PROJ"); ok {
		t.Fatal("a project nobody stored a board for already has one")
	}
	if err := cache.PutLastBoard("PROJ", 42); err != nil {
		t.Fatalf("PutLastBoard: %v", err)
	}
	got, ok := cache.LastBoard("PROJ")
	if !ok || got != 42 {
		t.Errorf("LastBoard returned %d, %t; want 42, true", got, ok)
	}
}

// The board view and the backlog view flip through a project's boards
// independently, so a session that left one on a board the other never opened
// has two different true answers to "which board".
func TestLastBoard_AndLastBacklogBoard_DoNotShareAnAnswer(t *testing.T) {
	t.Parallel()

	cache, _ := newTestCache(t)
	if err := cache.PutLastBoard("PROJ", 1); err != nil {
		t.Fatalf("PutLastBoard: %v", err)
	}
	if err := cache.PutLastBacklogBoard("PROJ", 2); err != nil {
		t.Fatalf("PutLastBacklogBoard: %v", err)
	}
	if got, _ := cache.LastBoard("PROJ"); got != 1 {
		t.Errorf("LastBoard returned %d, want 1", got)
	}
	if got, _ := cache.LastBacklogBoard("PROJ"); got != 2 {
		t.Errorf("LastBacklogBoard returned %d, want 2", got)
	}
}

func testSprint(id int64, name string, state jira.SprintState) jira.Sprint {
	return jira.Sprint{ID: id, BoardID: 7, Name: name, State: state}
}

func TestBacklog_ComesBackAsItWasStored(t *testing.T) {
	t.Parallel()

	cache, _ := newTestCache(t)
	stored := boardIssues(3)
	field := jira.FieldRef{ID: "customfield_10020", Name: "Sprint"}
	snap := BacklogSnapshot{
		Config: testBoardConfig(7), Sprints: []jira.Sprint{testSprint(1, "Sprint 1", jira.SprintActive)},
		Field: field, Issues: stored, More: true,
	}
	if err := cache.PutBacklog(7, snap); err != nil {
		t.Fatalf("PutBacklog: %v", err)
	}

	got, ok := cache.Backlog(7)
	if !ok {
		t.Fatal("the backlog just stored is not there")
	}
	if len(got.Sprints) != 1 || got.Sprints[0].Name != "Sprint 1" {
		t.Errorf("the sprints came back as %+v", got.Sprints)
	}
	if got.Field.ID != field.ID {
		t.Errorf("the sprint field came back as %+v, want %+v", got.Field, field)
	}
	if !slices.Equal(keysOf(got.Issues), keysOf(stored)) {
		t.Errorf("the issues came back as %v, want %v in that order", keysOf(got.Issues), keysOf(stored))
	}
	if !got.More || got.Stale {
		t.Errorf("More=%t Stale=%t, want More=true Stale=false", got.More, got.Stale)
	}
}

func TestBacklog_ReportsTheSitesOwnSentenceForABoardWithNoSprints(t *testing.T) {
	t.Parallel()

	cache, _ := newTestCache(t)
	snap := BacklogSnapshot{Config: testBoardConfig(7), NoSprints: "The board does not support sprints"}
	if err := cache.PutBacklog(7, snap); err != nil {
		t.Fatalf("PutBacklog: %v", err)
	}
	got, ok := cache.Backlog(7)
	if !ok {
		t.Fatal("the backlog just stored is not there")
	}
	if got.NoSprints != snap.NoSprints {
		t.Errorf("NoSprints came back as %q, want %q", got.NoSprints, snap.NoSprints)
	}
}

func TestBacklog_TurnsStaleOnceTheBacklogTTLHasPassed(t *testing.T) {
	t.Parallel()

	cache, clk := newTestCache(t)
	if err := cache.PutBacklog(7, BacklogSnapshot{Config: testBoardConfig(7), Issues: boardIssues(1)}); err != nil {
		t.Fatalf("PutBacklog: %v", err)
	}
	clk.at = testNow.Add(KindBacklog.TTL() + time.Second)
	got, ok := cache.Backlog(7)
	if !ok || !got.Stale {
		t.Errorf("Backlog past its TTL returned ok=%t stale=%t, want true, true", ok, got.Stale)
	}
}

func TestForgetBacklog_DropsTheBacklogAndKeepsTheIssuesItHeld(t *testing.T) {
	t.Parallel()

	cache, _ := newTestCache(t)
	stored := boardIssues(2)
	if err := cache.PutBacklog(7, BacklogSnapshot{Config: testBoardConfig(7), Issues: stored}); err != nil {
		t.Fatalf("PutBacklog: %v", err)
	}
	if err := cache.ForgetBacklog(7); err != nil {
		t.Fatalf("ForgetBacklog: %v", err)
	}
	if _, ok := cache.Backlog(7); ok {
		t.Error("the backlog survived being forgotten")
	}
	seen := 0
	if _, err := cache.EachIssue(func(jira.Issue, time.Time) bool { seen++; return true }); err != nil {
		t.Fatalf("EachIssue: %v", err)
	}
	if seen != len(stored) {
		t.Errorf("forgetting the backlog dropped %d of its issues; they are shared with every other read", len(stored)-seen)
	}
}

func TestBacklog_SkipsARecordItCannotDecode(t *testing.T) {
	t.Parallel()

	cache, _ := newTestCache(t)
	if err := cache.PutBacklog(7, BacklogSnapshot{Config: testBoardConfig(7), Issues: boardIssues(1)}); err != nil {
		t.Fatalf("PutBacklog: %v", err)
	}
	corrupt(t, cache, KindBacklog, "7")

	if _, ok := cache.Backlog(7); ok {
		t.Error("a record that cannot be decoded was served anyway")
	}
}

func TestIssue_ComesBackAsItWasStored(t *testing.T) {
	t.Parallel()

	cache, _ := newTestCache(t)
	stored := boardIssues(1)[0]
	if err := cache.PutIssue(stored); err != nil {
		t.Fatalf("PutIssue: %v", err)
	}
	got, ok := cache.Issue(stored.Key)
	if !ok {
		t.Fatal("the issue just stored is not there")
	}
	if got.Issue.Key != stored.Key || got.Issue.Summary != stored.Summary {
		t.Errorf("the issue came back as %+v", got.Issue)
	}
	if !got.StoredAt.Equal(testNow) {
		t.Errorf("the issue says it was stored at %s, want %s", got.StoredAt, testNow)
	}
}

func TestIssue_IsAMissWhenNothingWasStoredForThatKey(t *testing.T) {
	t.Parallel()

	cache, _ := newTestCache(t)
	if _, ok := cache.Issue("PROJ-404"); ok {
		t.Error("a key nobody stored found an issue")
	}
	if _, ok := cache.Issue(""); ok {
		t.Error("an empty key found an issue")
	}
}

// PutIssue merges into the same shared issue records PutRows, PutBoard and
// PutBacklog all write through, the way MergeIssue promises: a field this read
// did not ask for is left as it was.
func TestPutIssue_MergesRatherThanBlankingWhatANarrowerReadLeftAlone(t *testing.T) {
	t.Parallel()

	cache, _ := newTestCache(t)
	wide := jiratest.Gen(1)[0]
	wide.Requested = jira.AllFields()
	if err := cache.PutIssue(wide); err != nil {
		t.Fatalf("PutIssue (wide): %v", err)
	}

	narrow := wide
	narrow.Summary = "renamed"
	narrow.Requested = jira.NewFieldMask([]string{"summary"})
	if err := cache.PutIssue(narrow); err != nil {
		t.Fatalf("PutIssue (narrow): %v", err)
	}

	got, ok := cache.Issue(wide.Key)
	if !ok {
		t.Fatal("the issue is gone after a narrower read merged into it")
	}
	if got.Issue.Summary != "renamed" {
		t.Errorf("the summary is %q, want the narrow read's own renamed value", got.Issue.Summary)
	}
	if got.Issue.Status.ID != wide.Status.ID {
		t.Errorf("the status is %+v, want the wide read's own value the narrow one never asked about", got.Issue.Status)
	}
}

// A board's cards land in the same shared issue records a list's own search
// writes to, so a list row opened after a board read draws the fields the
// board asked for and never fetched itself.
func TestPutBoard_SharesItsIssuesWithEveryOtherRead(t *testing.T) {
	t.Parallel()

	cache, _ := newTestCache(t)
	wide := boardIssues(1)
	wide[0].Fields = wide[0].Fields.With(jira.FieldRef{ID: "reporter"}, jira.FieldValue{})
	if err := cache.PutBoard(7, BoardSnapshot{Config: testBoardConfig(7), Issues: wide}); err != nil {
		t.Fatalf("PutBoard: %v", err)
	}

	if err := cache.PutRows(cacheJQL, listRows(1), false); err != nil {
		t.Fatalf("PutRows: %v", err)
	}

	got, ok := cache.Issue(wide[0].Key)
	if !ok {
		t.Fatal("the board's own issue is gone")
	}
	if !got.Issue.Requested.Has("reporter") {
		t.Error("a list search that never asked for reporter unasked it from a board's own read")
	}
}

func TestKindTTL_CoversBoardAndBacklog(t *testing.T) {
	t.Parallel()

	if got := KindBoard.TTL(); got != 30*time.Second {
		t.Errorf("KindBoard.TTL() = %s, want 30s", got)
	}
	if got := KindBacklog.TTL(); got != 30*time.Second {
		t.Errorf("KindBacklog.TTL() = %s, want 30s", got)
	}
}

// corrupt overwrites a record with bytes that cannot decode as the shape its
// kind promises, the way a value truncated under a crash would.
func corrupt(t *testing.T, cache *DiskCache, kind Kind, key string) {
	t.Helper()
	if err := cache.db.Put(testScope, string(kind), store.Record{
		Key: key, Value: []byte("not json"), StoredAt: testNow,
	}); err != nil {
		t.Fatalf("writing an undecodable record: %v", err)
	}
}
