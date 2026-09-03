package jiratest_test

import (
	"errors"
	"slices"
	"strconv"
	"testing"

	"github.com/varijkapil13/saral/pkg/jira"
	"github.com/varijkapil13/saral/pkg/jira/jiratest"
)

// fakeMapped is the status ids the board's own columns map, read off the
// configuration rather than written down.
func fakeMapped(t *testing.T, c *jiratest.Fake, boardID int64) map[string]bool {
	t.Helper()
	cfg, err := c.BoardConfig(t.Context(), boardID)
	if err != nil {
		t.Fatalf("reading the configuration of board %d: %v", boardID, err)
	}
	out := make(map[string]bool)
	for _, col := range cfg.Columns {
		for _, id := range col.StatusIDs {
			out[id] = true
		}
	}
	if len(out) == 0 {
		t.Fatal("the board maps no status, so every issue falls outside every column")
	}
	return out
}

func fakeKeysOf(issues []jira.Issue) []string {
	out := make([]string, 0, len(issues))
	for i := range issues {
		out = append(out, issues[i].Key)
	}
	return out
}

// fakeBoardFields is fakeNarrow plus what a sub-query about resolved work turns
// on, which a read has to name to be able to see it. The narrowing itself is
// applied to the stored issue and not to the masked copy, so a read that leaves
// these out is narrowed all the same.
var fakeBoardFields = append(slices.Clone(fakeNarrow), "resolution", "resolutiondate")

func fakeWholeBoard(t *testing.T, c *jiratest.Fake, boardID int64) []jira.Issue {
	t.Helper()
	page, err := c.BoardIssues(t.Context(), boardID, jira.BoardQuery{Fields: fakeBoardFields})
	if err != nil {
		t.Fatalf("reading board %d: %v", boardID, err)
	}
	all, err := jira.Collect(t.Context(), page, 0)
	if err != nil {
		t.Fatalf("walking board %d: %v", boardID, err)
	}
	return all
}

// A board answers for its own project and for the statuses its columns map, and
// for nothing else. An issue in a status mapped to no column is an issue the
// board does not show, which is what leaves rows off a real board.
func TestBoardIssues_AnswerOneProjectAndOnlyTheStatusesTheColumnsMap(t *testing.T) {
	t.Parallel()
	unmapped := jira.Status{ID: "40404", Name: "Parked", Category: jira.CategoryToDo}
	c := jiratest.New(
		jiratest.WithProject("PROJ", jiratest.Scrum),
		jiratest.WithProject("OTHER", jiratest.Scrum),
		jiratest.WithIssues(slices.Concat(jiratest.Gen(9), jiratest.GenFor("OTHER", 4), []jira.Issue{{
			ID: "29999", Key: "PROJ-900", Project: jira.ProjectRef{Key: "PROJ"},
			Summary: "Parked until the quarter turns", Status: unmapped,
		}})),
	)
	board := fakeBoard(t, c)
	mapped := fakeMapped(t, c, board.ID)
	if mapped[unmapped.ID] {
		t.Fatalf("status %s is mapped after all, so this test proves nothing", unmapped.ID)
	}

	got := fakeWholeBoard(t, c, board.ID)
	if len(got) == 0 {
		t.Fatal("the board answered with nothing")
	}
	for i := range got {
		if got[i].Project.Key != board.ProjectKey {
			t.Errorf("%s is in %s and on %s's board", got[i].Key, got[i].Project.Key, board.ProjectKey)
		}
		if !mapped[got[i].Status.ID] {
			t.Errorf("%s is in status %s, which no column maps", got[i].Key, got[i].Status.ID)
		}
	}
	if slices.Contains(fakeKeysOf(got), "PROJ-900") {
		t.Error("PROJ-900 is in a status mapped to no column and is on the board anyway")
	}
}

// The sub-query the configuration hands out is applied rather than ignored.
// Ignoring one is the whole bug this read exists to fix: without it the done
// column is every issue the project ever finished.
func TestBoardIssues_ApplyTheSubQueryTheConfigurationHandsOut(t *testing.T) {
	t.Parallel()
	c := jiratest.New(
		jiratest.WithProject("PROJ", jiratest.Kanban),
		jiratest.WithIssues(jiratest.Gen(12)),
	)
	board := fakeBoard(t, c)
	cfg, err := c.BoardConfig(t.Context(), board.ID)
	if err != nil {
		t.Fatalf("reading the configuration: %v", err)
	}
	if cfg.SubQuery == "" {
		t.Fatal("a Kanban board here reports a sub-query, and this one reports none")
	}

	wide := fakeWholeBoard(t, c, board.ID)
	resolved := 0
	for i := range wide {
		if wide[i].Resolved != nil {
			resolved++
		}
	}
	if resolved == 0 {
		t.Fatal("no issue on this board was ever resolved, so the sub-query has nothing to hide")
	}

	page, err := c.BoardIssues(t.Context(), board.ID, jira.BoardQuery{Fields: fakeBoardFields, SubQuery: cfg.SubQuery})
	if err != nil {
		t.Fatalf("reading the board with its sub-query: %v", err)
	}
	narrowed, err := jira.Collect(t.Context(), page, 0)
	if err != nil {
		t.Fatalf("walking the board: %v", err)
	}
	if len(narrowed) == 0 {
		t.Fatal("the sub-query hid every issue")
	}
	for i := range narrowed {
		if narrowed[i].Resolved != nil {
			t.Errorf("%s was resolved long ago and the sub-query kept it", narrowed[i].Key)
		}
	}
	if len(narrowed) >= len(wide) {
		t.Errorf("the sub-query narrowed %d issues to %d", len(wide), len(narrowed))
	}
}

// The one thing this fake cannot express is a sub-query of its own choosing: a
// real board's is arbitrary JQL. It says so rather than dropping one, because a
// dropped sub-query is exactly the bug being fixed.
func TestBoardIssues_RefuseASubQueryThisFakeCannotEvaluate(t *testing.T) {
	t.Parallel()
	for name, tc := range map[string]struct {
		kind jiratest.BoardKind
		sub  string
	}{
		"a sub-query no board here reports":    {kind: jiratest.Kanban, sub: "assignee = currentUser()"},
		"a sub-query on a board that has none": {kind: jiratest.Scrum, sub: "resolved is EMPTY"},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			c := jiratest.New(
				jiratest.WithProject("PROJ", tc.kind),
				jiratest.WithIssues(jiratest.Gen(4)),
			)
			board := fakeBoard(t, c)
			_, err := c.BoardIssues(t.Context(), board.ID, jira.BoardQuery{Fields: fakeNarrow, SubQuery: tc.sub})
			var invalid *jira.ValidationError
			if !errors.As(err, &invalid) {
				t.Fatalf("got %T (%v), want a *jira.ValidationError", err, err)
			}
			if _, named := invalid.For("subQuery"); !named {
				t.Errorf("the refusal says %v and does not name subQuery", invalid.Fields)
			}
		})
	}
}

// The backlog is what no active or future sprint holds and what is not finished.
// A closed sprint holds nothing back: the work in it is over, and an issue whose
// only sprint is closed is waiting to be scheduled again.
func TestBoardBacklog_LeavesOutWhatAnOpenSprintHoldsAndWhatIsFinished(t *testing.T) {
	t.Parallel()
	c := fakeNewWithIssues(t, 12)
	board := fakeBoard(t, c)
	sprints := fakeSprintsOf(t, c, board.ID)

	byState := make(map[jira.SprintState]int64, len(sprints))
	for _, sp := range sprints {
		byState[sp.State] = sp.ID
	}
	for _, state := range []jira.SprintState{jira.SprintActive, jira.SprintFuture, jira.SprintClosed} {
		if byState[state] == 0 {
			t.Fatalf("the board seeds no %s sprint", state)
		}
	}

	onBoard := fakeKeysOf(fakeWholeBoard(t, c, board.ID))
	if len(onBoard) < 6 {
		t.Fatalf("the board holds %d issues, and this test needs a handful", len(onBoard))
	}
	held := map[jira.SprintState]string{
		jira.SprintActive: onBoard[0],
		jira.SprintFuture: onBoard[1],
		jira.SprintClosed: onBoard[2],
	}
	for state, key := range held {
		if err := c.MoveToSprint(t.Context(), byState[state], []string{key}); err != nil {
			t.Fatalf("putting %s in the %s sprint: %v", key, state, err)
		}
	}

	page, err := c.BoardBacklog(t.Context(), board.ID, jira.BoardQuery{Fields: fakeNarrow})
	if err != nil {
		t.Fatalf("reading the backlog: %v", err)
	}
	backlog, err := jira.Collect(t.Context(), page, 0)
	if err != nil {
		t.Fatalf("walking the backlog: %v", err)
	}
	keys := fakeKeysOf(backlog)
	if len(keys) == 0 {
		t.Fatal("the backlog came back empty")
	}
	for _, state := range []jira.SprintState{jira.SprintActive, jira.SprintFuture} {
		if slices.Contains(keys, held[state]) {
			t.Errorf("%s is in the %s sprint and in the backlog", held[state], state)
		}
	}
	if !slices.Contains(keys, held[jira.SprintClosed]) {
		t.Errorf("%s is in a closed sprint only and is not in the backlog", held[jira.SprintClosed])
	}
	for i := range backlog {
		if backlog[i].Status.Category == jira.CategoryDone {
			t.Errorf("%s is finished and in the backlog", backlog[i].Key)
		}
	}
	// The board still holds everything the backlog leaves out.
	after := fakeKeysOf(fakeWholeBoard(t, c, board.ID))
	for _, key := range []string{held[jira.SprintActive], held[jira.SprintFuture]} {
		if !slices.Contains(after, key) {
			t.Errorf("%s left the board when it went into a sprint", key)
		}
	}
}

// Both reads narrow to the fields asked for and report the list they asked with,
// so a field nobody asked about is not read as one the site had nothing for.
func TestBoardIssues_NarrowTheFieldSetAndReportWhatWasAsked(t *testing.T) {
	t.Parallel()
	c := fakeNewWithIssues(t, 6)
	board := fakeBoard(t, c)
	wanted := []string{"summary", "status"}

	for name, read := range map[string]func(jira.BoardQuery) (jira.Page[jira.Issue], error){
		"the board": func(q jira.BoardQuery) (jira.Page[jira.Issue], error) { return c.BoardIssues(t.Context(), board.ID, q) },
		"its backlog": func(q jira.BoardQuery) (jira.Page[jira.Issue], error) {
			return c.BoardBacklog(t.Context(), board.ID, q)
		},
	} {
		t.Run(name, func(t *testing.T) {
			page, err := read(jira.BoardQuery{Fields: wanted})
			if err != nil {
				t.Fatalf("reading %s: %v", name, err)
			}
			if len(page.Items) == 0 {
				t.Fatalf("%s came back empty", name)
			}
			for _, iss := range page.Items {
				if iss.Summary == "" || iss.Status.ID == "" {
					t.Errorf("%s came back without what was asked for: %+v", iss.Key, iss)
				}
				if iss.Assignee != nil || len(iss.Labels) > 0 {
					t.Errorf("%s carries a field nobody asked for", iss.Key)
				}
				if iss.Requested.Wide() || !slices.Equal(iss.Requested.IDs(), slices.Sorted(slices.Values(wanted))) {
					t.Errorf("%s reports the mask %v, want %v", iss.Key, iss.Requested.IDs(), wanted)
				}
			}
		})
	}
}

// The Agile envelope pages by offset and reports a total, which is the model the
// platform API's cursor is not.
func TestBoardIssues_PageByOffsetAndReportATotal(t *testing.T) {
	t.Parallel()
	c := fakeNewWithIssues(t, 7, jiratest.WithPageSize(3))
	board := fakeBoard(t, c)

	page, err := c.BoardIssues(t.Context(), board.ID, jira.BoardQuery{Fields: fakeNarrow})
	if err != nil {
		t.Fatalf("reading the board: %v", err)
	}
	total, counted := page.Count()
	if !counted {
		t.Fatal("the page reports no total, and this envelope carries one")
	}
	if len(page.Items) != 3 || !page.HasMore() {
		t.Fatalf("the first page holds %d issues (more=%v), want 3 with more to come", len(page.Items), page.HasMore())
	}
	all, err := jira.Collect(t.Context(), page, 0)
	if err != nil {
		t.Fatalf("walking the board: %v", err)
	}
	if len(all) != total {
		t.Errorf("the walk gathered %d issues against a reported total of %d", len(all), total)
	}
	if seen := len(slices.Compact(slices.Sorted(slices.Values(fakeKeysOf(all))))); seen != len(all) {
		t.Errorf("the walk handed back %d issues and %d distinct keys, so a page repeated", len(all), seen)
	}
}

// A board that ranks answers in rank order, which is the order the endpoint
// answers in and the reason nothing above the port sorts a board.
func TestBoardIssues_AnswerInTheBoardsRankOrder(t *testing.T) {
	t.Parallel()
	c := fakeNewWithIssues(t, 8)
	board := fakeBoard(t, c)
	cfg, err := c.BoardConfig(t.Context(), board.ID)
	if err != nil {
		t.Fatalf("reading the configuration: %v", err)
	}
	if cfg.RankFieldID == "" {
		t.Fatal("this board exposes no rank field, so there is no order to assert")
	}
	ref := jira.FieldRef{ID: cfg.RankFieldID}

	page, err := c.BoardIssues(t.Context(), board.ID, jira.BoardQuery{
		Fields: append(slices.Clone(fakeNarrow), cfg.RankFieldID),
	})
	if err != nil {
		t.Fatalf("reading the board: %v", err)
	}
	got := page.Items
	if len(got) < 2 {
		t.Fatalf("the board answered %d issues, and an order needs two", len(got))
	}
	last := ""
	for i := range got {
		rank, ok := got[i].Fields.Text(ref)
		if !ok {
			t.Fatalf("%s came back without the rank that was asked for", got[i].Key)
		}
		if last != "" && rank < last {
			t.Errorf("%s ranks %q, which comes before %q on the row above it", got[i].Key, rank, last)
		}
		last = rank
	}
}

func TestBoardIssues_RefuseAReadThatNamesNoFieldAndABoardNobodyHas(t *testing.T) {
	t.Parallel()
	c := fakeNewWithIssues(t, 4)
	board := fakeBoard(t, c)

	for _, fields := range [][]string{nil, {"  "}} {
		_, err := c.BoardIssues(t.Context(), board.ID, jira.BoardQuery{Fields: fields})
		var invalid *jira.ValidationError
		if !errors.As(err, &invalid) {
			t.Fatalf("fields %v: got %T (%v), want a *jira.ValidationError", fields, err, err)
		}
		if _, named := invalid.For("fields"); !named {
			t.Errorf("the refusal says %v and does not name fields", invalid.Fields)
		}
	}
	for _, id := range []int64{0, board.ID + 9000} {
		if _, err := c.BoardBacklog(t.Context(), id, jira.BoardQuery{Fields: fakeNarrow}); err == nil {
			t.Errorf("board %s was accepted", strconv.FormatInt(id, 10))
		}
	}
	var missing *jira.NotFoundError
	_, err := c.BoardIssues(t.Context(), board.ID+9000, jira.BoardQuery{Fields: fakeNarrow})
	if !errors.As(err, &missing) {
		t.Fatalf("got %T (%v) for a board nobody has, want a *jira.NotFoundError", err, err)
	}
	if missing.Kind != "board" {
		t.Errorf("the 404 names %s %s, want a board", missing.Kind, missing.ID)
	}
}

// A Scrum board here reports the two quick filters this fake can actually
// evaluate; a Kanban board reports none, which is a board's ordinary answer
// and not a refusal — see Fake.QuickFilters.
func TestQuickFilters_AScrumBoardHasThemAndAKanbanBoardHasNone(t *testing.T) {
	t.Parallel()
	c := fakeNewWithIssues(t, 4)
	scrum := fakeBoard(t, c)
	got, err := c.QuickFilters(t.Context(), scrum.ID)
	if err != nil {
		t.Fatalf("QuickFilters: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("a Scrum board reports %d quick filters, want 2: %+v", len(got), got)
	}
	for i, qf := range got {
		if qf.Name == "" || qf.JQL == "" {
			t.Errorf("quick filter %d is %+v, missing a name or JQL", i, qf)
		}
		if qf.Position != i {
			t.Errorf("quick filter %d is %+v, want position %d", i, qf, i)
		}
	}

	kanban := jiratest.New(jiratest.WithProject("OTHER", jiratest.Kanban), jiratest.WithIssues(jiratest.GenFor("OTHER", 4)))
	boards, err := kanban.Boards(t.Context(), "OTHER")
	if err != nil || len(boards) != 1 {
		t.Fatalf("Boards on OTHER: %v, %+v", err, boards)
	}
	none, err := kanban.QuickFilters(t.Context(), boards[0].ID)
	if err != nil {
		t.Fatalf("QuickFilters on a Kanban board: %v", err)
	}
	if len(none) != 0 {
		t.Errorf("a Kanban board reports %+v, want none", none)
	}
}

func TestQuickFilters_ABoardNobodyHasIsA404(t *testing.T) {
	t.Parallel()
	c := fakeNewWithIssues(t, 4)
	board := fakeBoard(t, c)
	_, err := c.QuickFilters(t.Context(), board.ID+9000)
	var missing *jira.NotFoundError
	if !errors.As(err, &missing) {
		t.Fatalf("got %T (%v), want a *jira.NotFoundError", err, err)
	}
}

// The whole reason QuickFilters exists: what it reads back has to actually
// narrow a board read when passed back through BoardQuery.QuickFilters.
func TestBoardIssues_ApplyTheQuickFiltersTheCallerToggledOn(t *testing.T) {
	t.Parallel()
	me := jira.User{AccountID: "acct-quickfilter-me", DisplayName: "Quick Filter Tester", Kind: jira.AccountPerson}
	base := jiratest.Gen(6)
	mine := base[0]
	mine.ID, mine.Key, mine.Assignee = "29901", "PROJ-901", &me
	unassigned := base[1]
	unassigned.ID, unassigned.Key, unassigned.Assignee = "29902", "PROJ-902", nil
	c := fakeNewWithIssues(t, 0,
		jiratest.WithMe(me),
		jiratest.WithIssues(append(base, mine, unassigned)),
	)
	board := fakeBoard(t, c)
	qfs, err := c.QuickFilters(t.Context(), board.ID)
	if err != nil {
		t.Fatalf("QuickFilters: %v", err)
	}
	mineQF, unassignedQF := fakeQuickFilterNamed(t, qfs, "Only My Issues"), fakeQuickFilterNamed(t, qfs, "Unassigned")

	page, err := c.BoardIssues(t.Context(), board.ID, jira.BoardQuery{Fields: fakeNarrow, QuickFilters: []string{mineQF.JQL}})
	if err != nil {
		t.Fatalf("reading the board with Only My Issues toggled on: %v", err)
	}
	got, err := jira.Collect(t.Context(), page, 0)
	if err != nil {
		t.Fatalf("walking the board: %v", err)
	}
	if len(got) != 1 || got[0].Key != "PROJ-901" {
		t.Fatalf("Only My Issues answered %v, want just PROJ-901", fakeKeysOf(got))
	}

	page, err = c.BoardIssues(t.Context(), board.ID, jira.BoardQuery{Fields: fakeNarrow, QuickFilters: []string{unassignedQF.JQL}})
	if err != nil {
		t.Fatalf("reading the board with Unassigned toggled on: %v", err)
	}
	got, err = jira.Collect(t.Context(), page, 0)
	if err != nil {
		t.Fatalf("walking the board: %v", err)
	}
	if !slices.Contains(fakeKeysOf(got), "PROJ-902") {
		t.Errorf("Unassigned answered %v, want it to include PROJ-902", fakeKeysOf(got))
	}
	for i := range got {
		if got[i].Assignee != nil {
			t.Errorf("%s has an assignee, and Unassigned is supposed to mean nobody does", got[i].Key)
		}
	}

	// Both together is an AND, not an OR: nobody is both PROJ-901's assignee and
	// unassigned, so toggling both on together leaves nothing.
	page, err = c.BoardIssues(t.Context(), board.ID, jira.BoardQuery{
		Fields: fakeNarrow, QuickFilters: []string{mineQF.JQL, unassignedQF.JQL},
	})
	if err != nil {
		t.Fatalf("reading the board with both toggled on: %v", err)
	}
	got, err = jira.Collect(t.Context(), page, 0)
	if err != nil {
		t.Fatalf("walking the board: %v", err)
	}
	if len(got) != 0 {
		t.Errorf("Only My Issues AND Unassigned together answered %v, want none", fakeKeysOf(got))
	}
}

// A quick filter's JQL is opaque and board-native; passing something this
// fake's JQL subset cannot read is refused rather than silently ignored, the
// same way an unrecognised sub-query is.
func TestBoardIssues_RefuseAQuickFilterThisFakeCannotEvaluate(t *testing.T) {
	t.Parallel()
	c := fakeNewWithIssues(t, 4)
	board := fakeBoard(t, c)
	_, err := c.BoardIssues(t.Context(), board.ID, jira.BoardQuery{
		Fields: fakeNarrow, QuickFilters: []string{"text ~ \"urgent\""},
	})
	var invalid *jira.ValidationError
	if !errors.As(err, &invalid) {
		t.Fatalf("got %T (%v), want a *jira.ValidationError", err, err)
	}
	if _, named := invalid.For("quickFilters"); !named {
		t.Errorf("the refusal says %v and does not name quickFilters", invalid.Fields)
	}
}

func fakeQuickFilterNamed(t *testing.T, qfs []jira.QuickFilter, name string) jira.QuickFilter {
	t.Helper()
	for _, qf := range qfs {
		if qf.Name == name {
			return qf
		}
	}
	t.Fatalf("no quick filter named %q among %+v", name, qfs)
	return jira.QuickFilter{}
}
