package backlog

import (
	"errors"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/varijkapil13/saral/internal/ui/kernel"
	"github.com/varijkapil13/saral/pkg/jira"
	"github.com/varijkapil13/saral/pkg/jira/jiratest"
)

// activeSprint is the id the fake gives the active sprint on PROJ's board, and
// futureSprint the one it gives the future one. Both are read off the fake
// rather than written down, because the seed is the fake's business.
func sprintIDs(t *testing.T, f *jiratest.Fake) (active, future int64) {
	t.Helper()
	boards, err := f.Boards(t.Context(), "PROJ")
	if err != nil || len(boards) == 0 {
		t.Fatalf("the fake has no board on PROJ: %v", err)
	}
	page, err := f.Sprints(t.Context(), boards[0].ID)
	if err != nil {
		t.Fatalf("reading the sprints: %v", err)
	}
	for _, sp := range page.Items {
		switch sp.State {
		case jira.SprintActive:
			active = sp.ID
		case jira.SprintFuture:
			future = sp.ID
		case jira.SprintClosed:
		}
	}
	if active == 0 || future == 0 {
		t.Fatalf("the fake seeded no active and future sprint: %+v", page.Items)
	}
	return active, future
}

func TestBacklog_SortsTheProjectIntoTheOpenSprintsAndWhatIsLeft(t *testing.T) {
	t.Parallel()
	f := newFake(12)
	active, _ := sprintIDs(t, f)
	if err := f.MoveToSprint(t.Context(), active, []string{"PROJ-3", "PROJ-4"}); err != nil {
		t.Fatalf("seeding the sprint: %v", err)
	}

	dr := newDriver(t, testDeps(f), 120, 24)
	for _, key := range []string{"PROJ-3", "PROJ-4"} {
		if got := dr.groupOf(key); got != "Sprint 2" {
			t.Errorf("%s is drawn under %q, want the active sprint it is in", key, got)
		}
	}
	if got := dr.groupOf("PROJ-1"); got != backlogName {
		t.Errorf("PROJ-1 is in no sprint and is drawn under %q", got)
	}
}

// Finished work is neither in a sprint anybody is planning nor waiting to be
// scheduled, and the difference is drawn rather than left to be guessed at.
func TestBacklog_LeavesFinishedWorkOut(t *testing.T) {
	t.Parallel()
	f := newFake(12)
	dr := newDriver(t, testDeps(f), 120, 24)

	var done []string
	for i := range dr.m.issues {
		if dr.m.issues[i].Status.Category == jira.CategoryDone {
			done = append(done, dr.m.issues[i].Key)
		}
	}
	if len(done) == 0 {
		t.Fatal("the fixture has no issue in the done category, so this test proves nothing")
	}
	for _, key := range done {
		if got := dr.groupOf(key); got != "" {
			t.Errorf("%s is finished and is drawn under %q", key, got)
		}
	}
	mustContain(t, dr.view(), " of "+count(len(dr.m.issues), "issue"))
}

// The board's own order is its rank field, and a rank the site did not send is
// not a position at the top of the list.
func TestBacklog_PutsEachSectionInTheBoardsRankOrder(t *testing.T) {
	t.Parallel()
	f := newFake(6)
	dr := newDriver(t, testDeps(f), 120, 24)
	if dr.m.config.RankFieldID == "" {
		t.Fatal("the fixture board exposes no rank field, so this test proves nothing")
	}
	ref := jira.FieldRef{ID: dr.m.config.RankFieldID}

	// The rows arrive oldest first; reversing every rank has to reverse them.
	reversed := make([]string, 0, len(dr.m.issues))
	for i := range dr.m.issues {
		reversed = append(reversed, dr.m.issues[i].Key)
	}
	for i := range dr.m.issues {
		at := len(dr.m.issues) - 1 - i
		dr.m.issues[i].Fields = dr.m.issues[i].Fields.With(ref, jira.FieldValue{
			Kind: jira.KindText, Text: reversed[at],
		})
	}
	dr.m.regroup()

	last := ""
	for _, at := range dr.m.groups[len(dr.m.groups)-1].issues {
		rank, ok := dr.m.issues[at].Fields.Text(ref)
		if !ok {
			t.Fatalf("%s lost its rank", dr.m.issues[at].Key)
		}
		if last != "" && rank < last {
			t.Errorf("the section is not in rank order: %q comes after %q", rank, last)
		}
		last = rank
	}

	// An issue with no rank at all goes last, not first.
	first := dr.m.groups[len(dr.m.groups)-1].issues[0]
	blank := dr.m.issues[first].Key
	dr.m.issues[first].Fields = dr.m.issues[first].Fields.Without(ref)
	dr.m.regroup()
	tail := dr.m.groups[len(dr.m.groups)-1].issues
	if got := dr.m.issues[tail[len(tail)-1]].Key; got != blank {
		t.Errorf("the issue with no rank is drawn at %q, want it last", got)
	}
}

// A board that does not rank is ordered by its saved filter, which nothing here
// can read, so it says which order the rows are actually in rather than offering
// a gesture that would silently do nothing.
func TestBacklog_ABoardWithNoRankFieldSaysWhatTheOrderIs(t *testing.T) {
	t.Parallel()
	kanban := jiratest.New(
		jiratest.WithProject("PROJ", jiratest.Kanban),
		jiratest.WithIssues(jiratest.Gen(8)),
	)
	dr := newDriver(t, testDeps(kanban), 120, 24)
	if dr.m.config.RankFieldID != "" {
		t.Fatal("the kanban fixture exposes a rank field, so this test proves nothing")
	}
	mustContain(t, dr.view(), "No rank field on this board")
	mustNotContain(t, dr.view(), "Rank order")

	scrum := newDriver(t, testDeps(newFake(8)), 120, 24)
	mustContain(t, scrum.view(), "Rank order", "cannot be reordered")
}

func TestBacklog_SaysWhichKindOfEmptyItIs(t *testing.T) {
	t.Parallel()
	for name, tc := range map[string]struct {
		build func(t *testing.T) *driver
		want  []string
	}{
		"no connection at all": {
			build: func(t *testing.T) *driver {
				d := testDeps(nil)
				d.Jira = nil
				return newDriver(t, d, 100, 20)
			},
			want: []string{"There is no Jira connection in this session."},
		},
		"nothing has been asked of the site yet": {
			build: func(t *testing.T) *driver {
				view, ok := New(testDeps(newFake(4))).(*Model)
				if !ok {
					t.Fatal("New did not return a *Model")
				}
				dr := &driver{t: t, m: view}
				dr.send(kernel.SizeMsg{Width: 100, Height: 20})
				return dr
			},
			want: []string{"Nothing has been asked of Jira yet."},
		},
		"a project with no board": {
			build: func(t *testing.T) *driver {
				f := jiratest.New(
					jiratest.WithProject("PROJ", jiratest.NoBoard),
					jiratest.WithIssues(jiratest.Gen(4)),
				)
				return newDriver(t, testDeps(f), 100, 20)
			},
			want: []string{"This project has no board", "the site listed none for it"},
		},
		"a session with no project": {
			build: func(t *testing.T) *driver {
				d := testDeps(newFake(4))
				d.Project = ""
				return newDriver(t, d, 100, 20)
			},
			want: []string{"not scoped to a project"},
		},
		"a site with no sprint field": {
			build: func(t *testing.T) *driver {
				f := jiratest.New(
					jiratest.WithProject("PROJ", jiratest.Scrum),
					jiratest.WithIssues(jiratest.Gen(4)),
					jiratest.WithFields(noSprintField()),
				)
				return newDriver(t, d(f), 100, 20)
			},
			want: []string{"no sprint field this session could resolve"},
		},
		"a board whose issues are all finished": {
			build: func(t *testing.T) *driver {
				f := jiratest.New(
					jiratest.WithProject("PROJ", jiratest.Scrum),
					jiratest.WithIssues(allDone(t)),
				)
				return newDriver(t, d(f), 100, 20)
			},
			want: []string{"Nothing on this board is waiting to be scheduled."},
		},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			got := tc.build(t).view()
			mustContain(t, got, tc.want...)
		})
	}
}

func d(client jira.SessionClient) kernel.Deps { return testDeps(client) }

// noSprintField is this site's catalogue with the sprint field taken out of it,
// which is what a Jira without Jira Software looks like.
func noSprintField() []jira.Field {
	return []jira.Field{
		{ID: "summary", Key: "summary", Name: "Summary", Navigable: true,
			Schema: jira.FieldSchema{Type: "string", System: "summary"}},
		{ID: "status", Key: "status", Name: "Status", Navigable: true,
			Schema: jira.FieldSchema{Type: "status", System: "status"}},
	}
}

// allDone is the fixture with every issue finished. The status is taken off an
// issue the generator already put in the done category rather than written down:
// a board shows an issue whose status one of its columns maps, so an id invented
// here is an issue on no board at all.
func allDone(tb testing.TB) []jira.Issue {
	tb.Helper()
	issues := jiratest.Gen(6)
	done := jira.Status{}
	for i := range issues {
		if issues[i].Status.Category == jira.CategoryDone {
			done = issues[i].Status
			break
		}
	}
	if done.ID == "" {
		tb.Fatal("the generator made no finished issue, so there is no done status to give the rest")
	}
	for i := range issues {
		issues[i].Status = done
	}
	return issues
}

// A German site translates every field name, and the sprint field is resolved
// through the name that does not move with the locale.
func TestBacklog_ResolvesTheSprintFieldOnATranslatedSite(t *testing.T) {
	t.Parallel()
	f := jiratest.New(
		jiratest.WithProject("PROJ", jiratest.Scrum),
		jiratest.WithIssues(jiratest.Gen(6)),
		jiratest.WithFields(germanFields()),
	)
	dr := newDriver(t, testDeps(f), 120, 24)
	if dr.m.field.ID != "customfield_10020" {
		t.Fatalf("the sprint field resolved to %q on a site that calls it Sprints; the untranslated "+
			"name is the only spelling that does not move with the locale", dr.m.field.ID)
	}
	if dr.m.absent != "" {
		t.Errorf("the view refused a site it could resolve: %s", dr.m.absent)
	}
}

func germanFields() []jira.Field {
	return []jira.Field{
		{ID: "summary", Key: "summary", Name: "Zusammenfassung", Navigable: true,
			ClauseNames: []string{"summary"},
			Schema:      jira.FieldSchema{Type: "string", System: "summary"}},
		{ID: "status", Key: "status", Name: "Status", Navigable: true,
			ClauseNames: []string{"status"},
			Schema:      jira.FieldSchema{Type: "status", System: "status"}},
		{ID: "assignee", Key: "assignee", Name: "Bearbeiter", Navigable: true,
			ClauseNames: []string{"assignee"},
			Schema:      jira.FieldSchema{Type: "user", System: "assignee"}},
		{ID: "priority", Key: "priority", Name: "Priorität", Navigable: true,
			ClauseNames: []string{"priority"},
			Schema:      jira.FieldSchema{Type: "priority", System: "priority"}},
		{ID: "updated", Key: "updated", Name: "Aktualisiert", Navigable: true,
			ClauseNames: []string{"updated"},
			Schema:      jira.FieldSchema{Type: "datetime", System: "updated"}},
		{ID: "issuetype", Key: "issuetype", Name: "Vorgangstyp", Navigable: true,
			ClauseNames: []string{"issuetype"},
			Schema:      jira.FieldSchema{Type: "issuetype", System: "issuetype"}},
		{ID: "customfield_10020", Key: "customfield_10020", Name: "Sprints",
			UntranslatedName: "Sprint", Custom: true, Navigable: true,
			ClauseNames: []string{"cf[10020]", "Sprint"},
			Schema:      jira.FieldSchema{Type: "array", Items: "json", CustomID: 10020}},
	}
}

func TestBacklog_SaysWhatTheSiteSaidWhenTheReadFails(t *testing.T) {
	t.Parallel()
	for name, tc := range map[string]struct {
		err  error
		want string
	}{
		"a refusal": {
			err:  &jira.CapabilityError{Capability: jira.CapBoards, Reason: "you need Browse Projects on PROJ"},
			want: "you need Browse Projects on PROJ",
		},
		"a rate limit": {
			err:  &jira.RateLimitError{RetryAfter: 30 * time.Second, Endpoint: "/board"},
			want: "retry in 30s",
		},
		"a transport failure": {
			err:  &jira.TransportError{Op: "GET /board", Status: 502, Err: errors.New("bad gateway")},
			want: "502",
		},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			f := newFake(6)
			f.FailNext(tc.err)
			dr := newDriver(t, testDeps(f), 100, 20)

			got := dr.view()
			mustContain(t, got, "The board could not be read.", tc.want, retryHint)
			if dr.m.failure == nil {
				t.Error("the failure was not kept, so the pane stops saying so as soon as the status line clears")
			}
			// A status line is transient; a keypress clears it and the pane has
			// to go on saying why it is empty.
			dr.key("j")
			mustContain(t, dr.view(), tc.want)
		})
	}
}

// Losing the keyboard is not being closed: the palette opening over a board
// still being read must not cancel the read.
func TestBacklog_KeepsItsReadWhenItLosesTheKeyboard(t *testing.T) {
	t.Parallel()
	f := newFake(8)
	view, ok := New(testDeps(f)).(*Model)
	if !ok {
		t.Fatal("New did not return a *Model")
	}
	dr := &driver{t: t, m: view}
	dr.send(kernel.SizeMsg{Width: 100, Height: 20})
	cmd := dr.m.Init()
	dr.send(kernel.FocusMsg{Focused: false})
	dr.run(cmd)

	if len(dr.m.rows) == 0 {
		t.Error("the board was read into a view that had lost the keyboard and the answer was dropped")
	}
}

// An answer to a question the user has already changed is dropped rather than
// drawn.
func TestBacklog_DropsAnAnswerToAQuestionItHasSinceChanged(t *testing.T) {
	t.Parallel()
	dr := newDriver(t, testDeps(newFake(8)), 100, 20)
	before := len(dr.m.rows)

	dr.send(loadedMsg{gen: dr.m.gen - 1, boards: []jira.Board{{ID: 99, Name: "Somewhere else"}}})
	if got := dr.m.board().Name; got == "Somewhere else" {
		t.Error("a stale answer was drawn")
	}
	if len(dr.m.rows) != before {
		t.Errorf("a stale answer changed the rows from %d to %d", before, len(dr.m.rows))
	}
}

func TestBacklog_ShowingAnotherBoardForgetsWhatBelongedToTheFirst(t *testing.T) {
	t.Parallel()
	f := jiratest.New(
		jiratest.WithProject("PROJ", jiratest.Scrum),
		jiratest.WithIssues(jiratest.Gen(8)),
	)
	dr := newDriver(t, testDeps(f), 100, 20)
	if len(dr.m.boards) < 2 {
		// One board is the fixture, so the gesture is a no-op and says so by
		// doing nothing rather than by reloading.
		before := countCalls(f, "Boards")
		dr.send(NextBoardMsg{})
		if got := countCalls(f, "Boards"); got != before {
			t.Errorf("the next-board gesture re-read the board list on a project with one board")
		}
		return
	}
	dr.m.picked["PROJ-1"] = true
	dr.send(NextBoardMsg{})
	if len(dr.m.picked) != 0 {
		t.Error("a selection made on one board survived onto another")
	}
}

func TestBacklog_PagesAsTheCursorApproachesTheEnd(t *testing.T) {
	t.Parallel()
	f := newFake(140)
	dr := newDriver(t, testDeps(f), 100, 20)
	first := len(dr.m.issues)
	if first == 0 || !dr.m.page.HasMore() {
		t.Fatalf("the first page brought %d issues and says more=%v; this test needs a paged fixture",
			first, dr.m.page.HasMore())
	}
	for range 40 {
		dr.key("end")
	}
	if len(dr.m.issues) <= first {
		t.Errorf("walking to the end of %d loaded issues never asked for the next page", first)
	}
}

func TestBacklog_FitsTheBoxItIsGiven(t *testing.T) {
	t.Parallel()
	for _, size := range [...]struct{ w, h int }{{80, 20}, {100, 28}, {120, 30}, {200, 60}} {
		dr := newDriver(t, testDeps(newFake(40)), size.w, size.h)
		for _, stage := range []func(){
			func() {},
			func() { dr.key("space") },
			func() { dr.key("m") },
			func() { dr.key("enter") },
		} {
			stage()
			got := dr.m.View()
			if lines := strings.Count(got, "\n") + 1; lines != size.h {
				t.Errorf("at %dx%d the frame is %d lines", size.w, size.h, lines)
			}
			for _, line := range strings.Split(dr.view(), "\n") {
				if len(line) > size.w {
					t.Errorf("at %dx%d a line is %d cells wide: %q", size.w, size.h, len(line), line)
				}
			}
		}
	}
}

func TestBacklog_EveryRowFillsTheWidth(t *testing.T) {
	t.Parallel()
	const width = 100
	dr := newDriver(t, testDeps(newFake(20)), width, 24)
	dr.key("space")
	lines := strings.Split(dr.view(), "\n")
	for i := 1; i <= len(dr.m.rows) && i < len(lines); i++ {
		if got := len(lines[i]); got != width {
			t.Errorf("row %d is %d cells wide, want %d: %q", i, got, width, lines[i])
		}
	}
}

func TestBacklog_DrawsNothingBeforeItHasASize(t *testing.T) {
	t.Parallel()
	view, ok := New(testDeps(newFake(4))).(*Model)
	if !ok {
		t.Fatal("New did not return a *Model")
	}
	if got := view.View(); got != "" {
		t.Errorf("a view with no size drew %q", got)
	}
}

// An issue's sprint value reaches a caller in two shapes, decided by whether the
// read expanded the field schema, and neither of them is the field's declared
// type. A view that reads only one of them works against one kind of search and
// silently draws every issue as unscheduled against the other.
func TestBacklog_ReadsBothShapesOfASprintValue(t *testing.T) {
	t.Parallel()
	dr := newDriver(t, testDeps(newFake(4)), 120, 24)
	ref := dr.m.field
	if ref.ID == "" {
		t.Fatal("the sprint field did not resolve, so this test proves nothing")
	}
	sprint := dr.m.sprints[0]
	id := strconv.FormatInt(sprint.ID, 10)

	for name, value := range map[string]jira.FieldValue{
		"the options a read with no schema infers": {
			Kind:    jira.KindOptions,
			Options: []jira.Option{{ID: id, Label: sprint.Name}},
		},
		"the json a read that sent a schema keeps verbatim": {
			Kind: jira.KindUnknown,
			Text: `[{"id":` + id + `,"name":"` + sprint.Name + `","state":"active"}]`,
		},
	} {
		t.Run(name, func(t *testing.T) {
			at := 0
			key := dr.m.issues[at].Key
			before := dr.m.issues[at].Fields
			dr.m.issues[at].Fields = before.With(ref, value)
			dr.m.regroup()
			got := dr.groupOf(key)
			dr.m.issues[at].Fields = before
			dr.m.regroup()
			if got != sprint.Name {
				t.Errorf("%s is drawn under %q, want %q", key, got, sprint.Name)
			}
		})
	}
}

// The fifty-issue cap is the endpoint's and not the port's: pkg/jira/cloud
// chunks to stay inside it, so MoveToSprint takes as many keys as a caller has
// and moves all of them. The view chunks as well, because a chunk is the only
// unit it can report progress in — so both halves are asserted here, the port
// taking the whole list and the view still sending it in calls the endpoint
// would accept.
func TestPort_TakesEveryKeyItIsGivenAndTheViewStillChunks(t *testing.T) {
	t.Parallel()
	c := newChunker(newFake(140))
	dr := newDriver(t, testDeps(c), 120, 24)
	dr.loadAll()
	dr.pickWholeBacklog()

	whole := len(dr.m.picked)
	if whole <= endpointCap {
		t.Fatalf("the fixture leaves %d issues to schedule, and this test is about more than the %d the endpoint takes",
			whole, endpointCap)
	}
	keys := make([]string, 0, whole)
	for i := range dr.m.rows {
		if !dr.m.rows[i].head {
			if key := dr.m.issues[dr.m.rows[i].issue].Key; dr.m.picked[key] {
				keys = append(keys, key)
			}
		}
	}

	// The port, handed the whole selection at once.
	active, _ := sprintIDs(t, c.Fake)
	if err := c.Fake.MoveToSprint(t.Context(), active, keys); err != nil {
		t.Fatalf("the port refused a move of %d issues; the fifty-issue cap is the endpoint's, and the "+
			"adapter chunks for it: %v", len(keys), err)
	}
	for _, key := range keys {
		iss, err := c.Fake.Issue(t.Context(), key)
		if err != nil {
			t.Fatalf("reading %s back: %v", key, err)
		}
		if _, held := iss.Fields.Get(dr.m.field); !held {
			t.Fatalf("%s was in a move of %d and is in no sprint, so only part of the list moved",
				key, len(keys))
		}
	}

	// The view, moving the same selection through the same port.
	dr.key("m")
	dr.m.destAt = 0
	dr.key("enter", "y")
	sprint, _ := c.calls()
	if len(sprint) != (whole+endpointCap-1)/endpointCap {
		t.Errorf("the view moved %d issues in %d calls, want %d", whole, len(sprint),
			(whole+endpointCap-1)/endpointCap)
	}
	for i, n := range sprint {
		if n > endpointCap || n == 0 {
			t.Errorf("call %d carried %d issues; the endpoint takes 1 to %d", i, n, endpointCap)
		}
	}
}
