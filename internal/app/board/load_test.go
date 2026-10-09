package board

import (
	"context"
	"errors"
	"testing"
	"time"

	appquery "github.com/varijkapil13/saral/internal/app/query"
	"github.com/varijkapil13/saral/pkg/jira"
	"github.com/varijkapil13/saral/pkg/jira/jiratest"
)

func refusals() map[string]error {
	return map[string]error{
		"a 403":               &jira.CapabilityError{Reason: "you may not read this board"},
		"a rate limit":        &jira.RateLimitError{RetryAfter: 30 * time.Second},
		"a transport failure": &jira.TransportError{Op: "GET /board", Err: errors.New("connection reset")},
	}
}

func fakeOf(kind jiratest.BoardKind, issues int) *jiratest.Fake {
	return jiratest.New(jiratest.WithProject("PROJ", kind), jiratest.WithIssues(jiratest.Gen(issues)))
}

func boardOf(t *testing.T, f *jiratest.Fake) (jira.Board, Plan) {
	t.Helper()
	ctx := context.Background()
	boards, err := ListBoards(ctx, f, "PROJ")
	if err != nil || len(boards) != 1 {
		t.Fatalf("ListBoards = %v, %v; want the project's one board", boards, err)
	}
	cfg, err := Config(ctx, f, boards[0].ID)
	if err != nil {
		t.Fatalf("Config: %v", err)
	}
	return boards[0], NewPlan(cfg)
}

// stubSprints answers a sprint read with one error, so the decision in
// OpenSprints can be held to each kind the site can give.
type stubSprints struct{ err error }

func (s stubSprints) Sprints(context.Context, int64, ...jira.SprintState) (jira.Page[jira.Sprint], error) {
	return jira.Page[jira.Sprint]{}, s.err
}

func (s stubSprints) Sprint(context.Context, int64) (jira.Sprint, error) { return jira.Sprint{}, s.err }

// Only a 400 means "this board has no sprints". Every other way the read can
// fail means the board could not be read, and has to stay a failure — reading a
// refusal or a rate limit as "no sprints" would draw a backlog that is not
// this board's.
func TestOpenSprints_OnlyAValidationErrorMeansNoSprints(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name      string
		err       error
		noSprints bool
	}{
		{"the site's own 400", &jira.ValidationError{Messages: []string{"The board does not support sprints"}}, true},
		{"a refusal", &jira.CapabilityError{Reason: "no"}, false},
		{"a rate limit", &jira.RateLimitError{}, false},
		{"a board that is not there", &jira.NotFoundError{Kind: "board", ID: "9"}, false},
		{"a transport failure", &jira.TransportError{Op: "GET /board/9/sprint", Status: 502, Err: errors.New("bad gateway")}, false},
	} {
		got, err := OpenSprints(context.Background(), stubSprints{tc.err}, 9, 50, jira.SprintActive, jira.SprintFuture)
		switch {
		case tc.noSprints && (err != nil || !got.None || got.Reason == "" || got.Open != nil):
			t.Errorf("%s: got (%+v, %v), want no sprints with the site's reason and no error", tc.name, got, err)
		case !tc.noSprints && (err == nil || got.None || got.Reason != ""):
			t.Errorf("%s: got (%+v, %v), want the error kept and no reason", tc.name, got, err)
		}
	}
}

// The states are checked again and put in the order they were asked for.
func TestOpenSprints_KeepsTheStatesAskedForInTheirOrder(t *testing.T) {
	t.Parallel()
	f := fakeOf(jiratest.Scrum, 3)
	b, _ := boardOf(t, f)
	got, err := OpenSprints(context.Background(), f, b.ID, 50, jira.SprintActive, jira.SprintFuture)
	if err != nil || got.None {
		t.Fatalf("OpenSprints = %+v, %v", got, err)
	}
	if len(got.Open) == 0 || got.Open[0].State != jira.SprintActive {
		t.Fatalf("the open sprints %+v do not start with the active one", got.Open)
	}
	for _, sp := range got.Open {
		if sp.State != jira.SprintActive && sp.State != jira.SprintFuture {
			t.Errorf("a %s sprint came back", sp.State)
		}
	}
	active, err := OpenSprints(context.Background(), f, b.ID, 50, jira.SprintActive)
	if err != nil || len(active.Open) != 1 {
		t.Errorf("asking for the active sprint alone gave %+v, %v", active, err)
	}
}

func TestReadCards_AKanbanBoardIsReadWholeWithItsSubQuery(t *testing.T) {
	t.Parallel()
	f := fakeOf(jiratest.Kanban, 5)
	_, p := boardOf(t, f)
	got, _, err := ReadCards(context.Background(), f, appquery.NewSearch(f), CardsQuery{
		Plan: p, Projection: p.Projection(), PageSize: 100, SprintLimit: 50, Probe: true,
	})
	if err != nil {
		t.Fatalf("ReadCards: %v", err)
	}
	if !got.NoSprints || got.Sprint.ID != 0 {
		t.Errorf("a Kanban board read as one with sprints: %+v", got)
	}
	if len(got.Page.Items) == 0 || len(got.Fields) == 0 {
		t.Errorf("the board read %d cards with fields %v", len(got.Page.Items), got.Fields)
	}
}

func TestReadCards_AScrumBoardIsReadThroughItsActiveSprint(t *testing.T) {
	t.Parallel()
	f := fakeOf(jiratest.Scrum, 3)
	_, p := boardOf(t, f)
	got, _, err := ReadCards(context.Background(), f, appquery.NewSearch(f), CardsQuery{
		Plan: p, Projection: p.Projection(), PageSize: 100, SprintLimit: 50, Probe: true,
	})
	if err != nil {
		t.Fatalf("ReadCards: %v", err)
	}
	if got.NoSprints || got.Sprint.State != jira.SprintActive {
		t.Errorf("the board was read through %+v, want its active sprint", got.Sprint)
	}
	if calls := f.Calls(); calls[len(calls)-1] != "SprintIssues" {
		t.Errorf("the last read was %s, want SprintIssues", calls[len(calls)-1])
	}
	empty, _, err := ReadCards(context.Background(), f, appquery.NewSearch(f), CardsQuery{Plan: p, Projection: p.Projection()})
	if err != nil || empty.Page.Items != nil {
		t.Errorf("a board with no active sprint known read %+v, %v; want nothing read", empty, err)
	}
}

// A failure says which read went unanswered and keeps the site's error whole.
func TestReadCards_AFailureNamesTheStepAndKeepsTheError(t *testing.T) {
	t.Parallel()
	for name, want := range refusals() {
		for _, tc := range []struct {
			step  Step
			skip  int
			probe bool
		}{
			{step: StepSprints, skip: 1, probe: true},
			{step: StepIssues, skip: 2, probe: true},
		} {
			f := fakeOf(jiratest.Scrum, 3)
			_, p := boardOf(t, f)
			search := appquery.NewSearch(f)
			if _, err := search.Resolve(context.Background(), p.Projection()); err != nil {
				t.Fatal(err)
			}
			if tc.skip == 2 {
				f.FailNextN(1, nil)
			}
			f.FailNext(want)
			_, failed, err := ReadCards(context.Background(), f, search, CardsQuery{
				Plan: p, Projection: p.Projection(), PageSize: 100, SprintLimit: 50, Probe: tc.probe,
			})
			if failed != tc.step || !errors.Is(err, want) {
				t.Errorf("%s at step %d: got step %d, %v", name, tc.step, failed, err)
			}
		}
	}
}

func TestLoadBacklog_ReadsTheBoardItsSprintsAndItsIssues(t *testing.T) {
	t.Parallel()
	f := fakeOf(jiratest.Scrum, 4)
	got, err := LoadBacklog(context.Background(), f, appquery.NewSearch(f), BacklogQuery{
		Project: "PROJ", PageSize: 50, SprintLimit: 200,
		Projection: func(sprint jira.FieldRef, _ jira.BoardConfig) appquery.Projection {
			return appquery.ListProjection().With(sprint.ID)
		},
	})
	if err != nil {
		t.Fatalf("LoadBacklog: %v", err)
	}
	if len(got.Boards) != 1 || got.Config.BoardID != got.Boards[0].ID || got.Field.ID == "" {
		t.Errorf("the load answered %+v", got)
	}
	if len(got.Sprints) == 0 || len(got.Page.Items) == 0 || got.NoSprints != "" {
		t.Errorf("the load read %d sprints and %d issues, noSprints %q", len(got.Sprints), len(got.Page.Items), got.NoSprints)
	}
}

func TestLoadBacklog_AKanbanBoardIsReadWithTheSitesReason(t *testing.T) {
	t.Parallel()
	f := fakeOf(jiratest.Kanban, 2)
	got, err := LoadBacklog(context.Background(), f, appquery.NewSearch(f), BacklogQuery{
		Project: "PROJ", PageSize: 50, SprintLimit: 200, WantID: 999,
		Projection: func(jira.FieldRef, jira.BoardConfig) appquery.Projection { return appquery.ListProjection() },
	})
	if err != nil {
		t.Fatalf("LoadBacklog: %v", err)
	}
	if got.NoSprints == "" || got.Sprints != nil || len(got.Page.Items) == 0 {
		t.Errorf("a Kanban backlog answered %+v", got)
	}
}

func TestLoadBacklog_EachFailureIsKeptWhole(t *testing.T) {
	t.Parallel()
	for name, want := range refusals() {
		for skip := range 4 {
			f := fakeOf(jiratest.Scrum, 2)
			search := appquery.NewSearch(f)
			if skip > 0 {
				f.FailNextN(skip, nil)
			}
			f.FailNext(want)
			_, err := LoadBacklog(context.Background(), f, search, BacklogQuery{
				Project: "PROJ", PageSize: 50, SprintLimit: 200,
				Projection: func(jira.FieldRef, jira.BoardConfig) appquery.Projection { return appquery.ListProjection() },
			})
			if !errors.Is(err, want) {
				t.Errorf("%s after %d reads: got %v", name, skip, err)
			}
		}
	}
}

func TestNextPage_WritesThePageInHandBeforeReadingTheNext(t *testing.T) {
	t.Parallel()
	f := jiratest.New(jiratest.WithProject("PROJ", jiratest.Kanban), jiratest.WithIssues(jiratest.Gen(5)), jiratest.WithPageSize(2))
	b, _ := boardOf(t, f)
	page, err := f.BoardIssues(context.Background(), b.ID, jira.BoardQuery{Fields: []string{"summary"}, MaxResults: 2})
	if err != nil || !page.HasMore() {
		t.Fatalf("the first page = %+v, %v", page, err)
	}
	var order []string
	put := func() error { order = append(order, "put"); return errors.New("full") }
	next, stored, err := NextPage(context.Background(), page, put)
	if err != nil || len(next.Items) == 0 {
		t.Fatalf("NextPage = %+v, %v", next, err)
	}
	if stored == nil || len(order) != 1 {
		t.Errorf("the write said %v after %v", stored, order)
	}
	for name, want := range refusals() {
		f.FailNext(want)
		if _, _, err := NextPage(context.Background(), page, nil); !errors.Is(err, want) {
			t.Errorf("%s: NextPage = %v", name, err)
		}
	}
}

func TestPortReads_KeepEachFailureWhole(t *testing.T) {
	t.Parallel()
	for name, want := range refusals() {
		f := fakeOf(jiratest.Scrum, 2)
		b, _ := boardOf(t, f)
		ctx := context.Background()
		for read, run := range map[string]func() error{
			"ListBoards": func() error { _, err := ListBoards(ctx, f, "PROJ"); return err },
			"Config":     func() error { _, err := Config(ctx, f, b.ID); return err },
			"Reread":     func() error { _, err := Reread(ctx, f, "PROJ-1", []string{"summary"}); return err },
			"Account":    func() error { _, err := Account(ctx, f); return err },
			"FindAssignees": func() error {
				_, err := FindAssignees(ctx, f, "a", "PROJ", 20)
				return err
			},
		} {
			f.FailNext(want)
			if err := run(); !errors.Is(err, want) {
				t.Errorf("%s from %s: got %v", name, read, err)
			}
		}
		f.FailNext(want)
		if got := ReadQuickFilters(ctx, f, b.ID); got != nil {
			t.Errorf("%s: quick filters that could not be read answered %v, want none", name, got)
		}
	}
}

func TestPortReads_Succeed(t *testing.T) {
	t.Parallel()
	f := fakeOf(jiratest.Scrum, 2)
	b, _ := boardOf(t, f)
	ctx := context.Background()
	if got := ReadQuickFilters(ctx, f, b.ID); len(got) == 0 {
		t.Error("a Scrum board's quick filters read as none")
	}
	if iss, err := Reread(ctx, f, "PROJ-1", []string{"summary"}); err != nil || iss.Key != "PROJ-1" {
		t.Errorf("Reread = %+v, %v", iss, err)
	}
	if _, err := Account(ctx, f); err != nil {
		t.Errorf("Account: %v", err)
	}
	if _, err := FindAssignees(ctx, f, "a", "PROJ", 20); err != nil {
		t.Errorf("FindAssignees: %v", err)
	}
	if at, ok := IndexOfBoard([]jira.Board{{ID: 4}, {ID: 9}}, 9); !ok || at != 1 {
		t.Errorf("IndexOfBoard = %d, %v", at, ok)
	}
	if got := PickSprint([]jira.Sprint{{ID: 1}, {ID: 2}}, 3); got.ID != 1 {
		t.Errorf("a sprint not among them picked %d, want the first", got.ID)
	}
}
