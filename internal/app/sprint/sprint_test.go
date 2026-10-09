package sprint

import (
	"context"
	"errors"
	"slices"
	"testing"
	"time"

	"github.com/varijkapil13/saral/pkg/jira"
	"github.com/varijkapil13/saral/pkg/jira/jiratest"
)

func failures() map[string]error {
	return map[string]error{
		"a 403":               &jira.CapabilityError{Capability: jira.CapBoards, Reason: "needs Browse Projects"},
		"a 429":               &jira.RateLimitError{RetryAfter: time.Minute},
		"a transport failure": &jira.TransportError{Op: "GET", Err: errors.New("connection reset")},
	}
}

// unchanged is err passed through as the port gave it: a prefix would change
// what a view prints.
func unchanged(err, want error) bool {
	return errors.Is(err, want) && err.Error() == want.Error()
}

func newFake() *jiratest.Fake {
	return jiratest.New(jiratest.WithProject("PROJ", jiratest.Scrum), jiratest.WithIssues(jiratest.Gen(6)))
}

// refusing fails one named method with err and passes everything else through.
type refusing struct {
	*jiratest.Fake
	method string
	err    error
}

func (r *refusing) refuse(name string) error {
	if name == r.method {
		return r.err
	}
	return nil
}

func (r *refusing) Boards(ctx context.Context, project string) ([]jira.Board, error) {
	if err := r.refuse("Boards"); err != nil {
		return nil, err
	}
	return r.Fake.Boards(ctx, project)
}

func (r *refusing) Sprints(ctx context.Context, boardID int64, states ...jira.SprintState) (jira.Page[jira.Sprint], error) {
	if err := r.refuse("Sprints"); err != nil {
		return jira.Page[jira.Sprint]{}, err
	}
	return r.Fake.Sprints(ctx, boardID, states...)
}

func (r *refusing) BoardConfig(ctx context.Context, boardID int64) (jira.BoardConfig, error) {
	if err := r.refuse("BoardConfig"); err != nil {
		return jira.BoardConfig{}, err
	}
	return r.Fake.BoardConfig(ctx, boardID)
}

func (r *refusing) SprintIssues(ctx context.Context, boardID, sprintID int64, q jira.BoardQuery) (jira.Page[jira.Issue], error) {
	if err := r.refuse("SprintIssues"); err != nil {
		return jira.Page[jira.Issue]{}, err
	}
	return r.Fake.SprintIssues(ctx, boardID, sprintID, q)
}

func (r *refusing) CreateSprint(ctx context.Context, in jira.SprintInput) (jira.Sprint, error) {
	if err := r.refuse("CreateSprint"); err != nil {
		return jira.Sprint{}, err
	}
	return r.Fake.CreateSprint(ctx, in)
}

func (r *refusing) MoveToSprint(ctx context.Context, id int64, keys []string) error {
	if err := r.refuse("MoveToSprint"); err != nil {
		return err
	}
	return r.Fake.MoveToSprint(ctx, id, keys)
}

func (r *refusing) CompleteSprint(ctx context.Context, id int64) (jira.Sprint, error) {
	if err := r.refuse("CompleteSprint"); err != nil {
		return jira.Sprint{}, err
	}
	return r.Fake.CompleteSprint(ctx, id)
}

func listed(t *testing.T, f *jiratest.Fake, closed bool) Listing {
	t.Helper()
	l, err := List(t.Context(), f, "PROJ", States(closed), 5, 200)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	return l
}

func named(t *testing.T, l Listing, name string) jira.Sprint {
	t.Helper()
	for _, sp := range l.Sprints {
		if sp.Name == name {
			return sp
		}
	}
	t.Fatalf("no sprint called %q in %v", name, l.Sprints)
	return jira.Sprint{}
}

// running is the seeded active sprint holding keys.
func running(t *testing.T, f *jiratest.Fake, keys ...string) jira.Sprint {
	t.Helper()
	sp := named(t, listed(t, f, false), "Sprint 2")
	if err := f.MoveToSprint(t.Context(), sp.ID, keys); err != nil {
		t.Fatalf("seeding: %v", err)
	}
	return sp
}

func TestList_AsksForTheStatesWantedAndOnlyThose(t *testing.T) {
	t.Parallel()

	f := newFake()
	open := listed(t, f, false)
	if len(open.Boards) == 0 || len(open.Sprints) == 0 {
		t.Fatalf("the listing is empty: %+v", open)
	}
	for _, sp := range open.Sprints {
		if sp.State == jira.SprintClosed {
			t.Errorf("%s is closed and closed sprints were not asked for", sp.Name)
		}
	}
	all := listed(t, f, true)
	if !slices.ContainsFunc(all.Sprints, func(sp jira.Sprint) bool { return sp.State == jira.SprintClosed }) {
		t.Error("closed sprints were asked for and none came back")
	}
}

func TestList_CapsTheSprintsABoardContributes(t *testing.T) {
	t.Parallel()

	l, err := List(t.Context(), newFake(), "PROJ", States(true), 5, 1)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(l.Sprints) != len(l.Boards) {
		t.Errorf("%d sprints over %d boards with a cap of one each", len(l.Sprints), len(l.Boards))
	}
}

func TestList_AKanbanBoardContributesNothingRatherThanFailing(t *testing.T) {
	t.Parallel()

	f := jiratest.New(jiratest.WithProject("KAN", jiratest.Kanban))
	l, err := List(t.Context(), f, "KAN", States(false), 5, 200)
	if err != nil {
		t.Fatalf("a board without sprints failed the listing: %v", err)
	}
	if len(l.Boards) == 0 || len(l.Sprints) != 0 {
		t.Errorf("listing is %+v, want the board and no sprints", l)
	}
}

func TestList_PassesARefusalThroughUnwrapped(t *testing.T) {
	t.Parallel()

	for name, want := range failures() {
		for _, method := range []string{"Boards", "Sprints"} {
			t.Run(name+" on "+method, func(t *testing.T) {
				t.Parallel()
				r := &refusing{Fake: newFake(), method: method, err: want}
				_, err := List(t.Context(), r, "PROJ", States(false), 5, 200)
				if !unchanged(err, want) {
					t.Errorf("List returned %v, want %v as it was", err, want)
				}
			})
		}
	}
}

func TestWrites_ReturnTheSprintAsTheSiteHasIt(t *testing.T) {
	t.Parallel()

	f := newFake()
	board := listed(t, f, false).Boards[0].ID
	start := time.Date(2026, time.March, 9, 0, 0, 0, 0, time.UTC)
	end := start.AddDate(0, 0, 14)
	made, err := Create(t.Context(), f, jira.SprintInput{BoardID: board, Name: "Sprint 9"})
	if err != nil || made.State != jira.SprintFuture {
		t.Fatalf("Create: %+v, %v", made, err)
	}
	goal := "ship it"
	saved, err := Update(t.Context(), f, made.ID, jira.SprintPatch{Goal: &goal, Start: &start, End: &end})
	if err != nil || saved.Goal != goal || saved.Name != "Sprint 9" {
		t.Fatalf("Update: %+v, %v", saved, err)
	}
	if _, err := Complete(t.Context(), f, named(t, listed(t, f, false), "Sprint 2").ID); err != nil {
		t.Fatalf("Complete: %v", err)
	}
	started, err := Start(t.Context(), f, made.ID)
	if err != nil || started.State != jira.SprintActive {
		t.Fatalf("Start: %+v, %v", started, err)
	}
}

func TestWrites_PassARefusalThroughUnwrapped(t *testing.T) {
	t.Parallel()

	for name, want := range failures() {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			f := newFake()
			sp := named(t, listed(t, f, false), "Sprint 3")
			goal := "x"
			calls := map[string]func() error{
				"Create": func() error {
					_, err := Create(t.Context(), f, jira.SprintInput{BoardID: sp.BoardID, Name: "n"})
					return err
				},
				"Update": func() error {
					_, err := Update(t.Context(), f, sp.ID, jira.SprintPatch{Goal: &goal})
					return err
				},
				"Start":    func() error { _, err := Start(t.Context(), f, sp.ID); return err },
				"Complete": func() error { _, err := Complete(t.Context(), f, sp.ID); return err },
			}
			for call, run := range calls {
				f.FailNext(want)
				if err := run(); !unchanged(err, want) {
					t.Errorf("%s returned %v, want %v as it was", call, err, want)
				}
			}
		})
	}
}

func TestReadProgress_CountsByTheBoardsLastColumnAndItsEstimationField(t *testing.T) {
	t.Parallel()

	f := newFake()
	sp := running(t, f, "PROJ-1", "PROJ-2", "PROJ-3", "PROJ-4", "PROJ-5", "PROJ-6")
	cfg, err := f.BoardConfig(t.Context(), sp.BoardID)
	if err != nil {
		t.Fatalf("BoardConfig: %v", err)
	}
	if cfg.Estimation == nil {
		t.Fatal("the seeded board has no estimation field, so this proves nothing about points")
	}
	last := cfg.Columns[len(cfg.Columns)-1].StatusIDs
	page, err := f.SprintIssues(t.Context(), sp.BoardID, sp.ID, jira.BoardQuery{Fields: []string{"status", cfg.Estimation.Field.ID}})
	if err != nil {
		t.Fatalf("SprintIssues: %v", err)
	}
	var done int
	var pts, donePts float64
	var open []string
	for i := range page.Items {
		iss := &page.Items[i]
		n, _ := iss.Fields.Number(cfg.Estimation.Field)
		pts += n
		if slices.Contains(last, iss.Status.ID) {
			done++
			donePts += n
		} else {
			open = append(open, iss.Key)
		}
	}
	if done == 0 || done == len(page.Items) {
		t.Fatalf("the seed has %d of %d done, so this proves nothing about which column counts", done, len(page.Items))
	}

	got, err := ReadProgress(t.Context(), f, sp.BoardID, sp.ID)
	if err != nil {
		t.Fatalf("ReadProgress: %v", err)
	}
	if got.Total != 6 || got.Done != done || got.Points != pts || got.DonePoints != donePts || !got.Estimated || got.Capped {
		t.Errorf("progress is %+v, want %d of 6 done, %v of %v points", got, done, donePts, pts)
	}
	if !slices.Equal(got.Open, open) {
		t.Errorf("open is %v, want %v", got.Open, open)
	}
}

func TestDoneStatuses_SkipsATrailingColumnWithNothingMapped(t *testing.T) {
	t.Parallel()

	cfg := jira.BoardConfig{Columns: []jira.Column{
		{Name: "To do", StatusIDs: []string{"1"}},
		{Name: "Done", StatusIDs: []string{"3"}},
		{Name: "Archive"},
	}}
	if got := doneStatuses(cfg); len(got) != 1 || !got["3"] {
		t.Errorf("done is %v, want only the last column with a status", got)
	}
}

func TestReadAllProgress_KeepsARefusalOnItsSprint(t *testing.T) {
	t.Parallel()

	for name, want := range failures() {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			f := newFake()
			sp := running(t, f, "PROJ-1")
			r := &refusing{Fake: f, method: "SprintIssues", err: want}
			got, err := ReadAllProgress(t.Context(), r, []jira.Sprint{sp})
			if err != nil {
				t.Fatalf("a refusal on one sprint failed the pass: %v", err)
			}
			if !unchanged(got[sp.ID].Err, want) {
				t.Errorf("the sprint carries %v, want %v", got[sp.ID].Err, want)
			}
		})
	}
}

func TestReadAllProgress_StopsOnCancellation(t *testing.T) {
	t.Parallel()

	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if _, err := ReadAllProgress(ctx, newFake(), []jira.Sprint{{ID: 1, BoardID: 1}}); !errors.Is(err, context.Canceled) {
		t.Errorf("a cancelled pass returned %v", err)
	}
}

func TestCompleteInto_MovesTheOpenIssuesBeforeClosing(t *testing.T) {
	t.Parallel()

	for _, kind := range []Dest{DestNext, DestNew} {
		f := newFake()
		sp := running(t, f, "PROJ-1", "PROJ-2", "PROJ-3")
		l := listed(t, f, false)
		var d Destination
		for _, dd := range Destinations(l.Sprints, sp) {
			if dd.Kind == kind {
				d = dd
			}
		}
		p, err := ReadProgress(t.Context(), f, sp.BoardID, sp.ID)
		if err != nil {
			t.Fatalf("ReadProgress: %v", err)
		}
		if len(p.Open) == 0 || d.Kind != kind {
			t.Fatalf("nothing is open or %v is not offered, so this proves nothing", kind)
		}
		done, err := CompleteInto(t.Context(), f, sp, d)
		if err != nil {
			t.Fatalf("CompleteInto %v: %v", kind, err)
		}
		if done.Sprint.State != jira.SprintClosed || done.Moved != len(p.Open) || done.Created != (kind == DestNew) {
			t.Errorf("completion into %v is %+v", kind, done)
		}
		moved, err := ReadProgress(t.Context(), f, done.Target.BoardID, done.Target.ID)
		if err != nil {
			t.Fatalf("ReadProgress: %v", err)
		}
		if moved.Total != len(p.Open) {
			t.Errorf("the target holds %d issues, want the %d that were open", moved.Total, len(p.Open))
		}
	}
}

func TestCompleteInto_TheBacklogIsTheCloseAlone(t *testing.T) {
	t.Parallel()

	f := newFake()
	sp := running(t, f, "PROJ-1")
	before := len(f.Calls())
	done, err := CompleteInto(t.Context(), f, sp, Destination{Kind: DestBacklog})
	if err != nil || done.Sprint.State != jira.SprintClosed {
		t.Fatalf("CompleteInto the backlog: %+v, %v", done, err)
	}
	if calls := f.Calls()[before:]; !slices.Equal(calls, []string{"CompleteSprint"}) {
		t.Errorf("completing into the backlog called %v", calls)
	}
}

func TestCompleteInto_SaysHowFarItGotWhenItStops(t *testing.T) {
	t.Parallel()

	for name, want := range failures() {
		for _, tc := range []struct {
			method  string
			stage   Stage
			created bool
		}{
			{"BoardConfig", StageRead, false},
			{"SprintIssues", StageRead, false},
			{"CreateSprint", StageCreate, false},
			{"MoveToSprint", StageMove, true},
			{"CompleteSprint", StageClose, true},
		} {
			t.Run(name+" on "+tc.method, func(t *testing.T) {
				t.Parallel()
				f := newFake()
				sp := running(t, f, "PROJ-1", "PROJ-2")
				d := Destination{Kind: DestNew, Name: SuccessorName(listed(t, f, false).Sprints, sp)}
				r := &refusing{Fake: f, method: tc.method, err: want}
				_, err := CompleteInto(t.Context(), r, sp, d)
				var stopped *CompletionError
				if !errors.As(err, &stopped) {
					t.Fatalf("the failure is %T, want a *CompletionError", err)
				}
				if stopped.Stage != tc.stage || stopped.Created != tc.created || stopped.Sprint.ID != sp.ID {
					t.Errorf("stopped at %+v, want stage %v created %v", stopped, tc.stage, tc.created)
				}
				if !errors.Is(err, want) {
					t.Errorf("the cause %v does not unwrap to %v", stopped.Err, want)
				}
				if tc.stage != StageClose && slices.Contains(f.Calls(), "CompleteSprint") {
					t.Error("the sprint was closed although its open issues did not all move")
				}
			})
		}
	}
}

func TestSuccessorName_CountsOnPastWhatTheBoardAlreadyHas(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name  string
		board []string
		want  string
	}{
		{"Sprint 7", nil, "Sprint 8"},
		{"Sprint 7", []string{"Sprint 8", "Sprint 9"}, "Sprint 10"},
		{"Team 12", []string{"Team 13"}, "Team 14"},
		{"Autumn push", nil, "Autumn push 2"},
		{"Sprint 9", []string{"Sprint 10"}, "Sprint 11"},
		{"", nil, "Sprint 2"},
	} {
		sprints := make([]jira.Sprint, 0, len(tc.board)+2)
		for i, name := range append([]string{tc.name}, tc.board...) {
			sprints = append(sprints, jira.Sprint{ID: int64(i + 1), BoardID: 1, Name: name})
		}
		sprints = append(sprints, jira.Sprint{ID: 99, BoardID: 2, Name: tc.want})
		if got := SuccessorName(sprints, sprints[0]); got != tc.want {
			t.Errorf("after %q with %v on the board: %q, want %q", tc.name, tc.board, got, tc.want)
		}
	}
}

func TestDestinations_LeaveOutTheNextSprintWhenTheBoardHasNone(t *testing.T) {
	t.Parallel()

	sp := jira.Sprint{ID: 1, BoardID: 1, Name: "Sprint 1", State: jira.SprintActive}
	other := jira.Sprint{ID: 2, BoardID: 2, Name: "Sprint 2", State: jira.SprintFuture}
	got := Destinations([]jira.Sprint{sp, other}, sp)
	if len(got) != 2 || got[0].Kind != DestBacklog || got[1].Kind != DestNew {
		t.Errorf("destinations are %+v, want the backlog and a new sprint", got)
	}
	next := jira.Sprint{ID: 3, BoardID: 1, Name: "Sprint 3", State: jira.SprintFuture}
	got = Destinations([]jira.Sprint{sp, other, next}, sp)
	if len(got) != 3 || got[1].Kind != DestNext || got[1].Sprint.ID != next.ID {
		t.Errorf("destinations are %+v, want the board's own next sprint", got)
	}
}

func TestSort_StateThenDateAndANilDateLast(t *testing.T) {
	t.Parallel()

	day := func(d int) *time.Time { at := time.Date(2026, time.March, d, 0, 0, 0, 0, time.UTC); return &at }
	in := []jira.Sprint{
		{ID: 1, State: jira.SprintClosed, End: day(1)},
		{ID: 2, State: jira.SprintFuture},
		{ID: 3, State: "weird"},
		{ID: 4, State: jira.SprintFuture, Start: day(9)},
		{ID: 5, State: jira.SprintClosed, End: day(5)},
		{ID: 6, State: jira.SprintActive, Start: day(2)},
	}
	got := make([]int64, 0, len(in))
	for _, sp := range Sort(in) {
		got = append(got, sp.ID)
	}
	if want := []int64{6, 4, 2, 5, 1, 3}; !slices.Equal(got, want) {
		t.Errorf("order is %v, want %v", got, want)
	}
}

func TestCanStart_NeedsAPlannedSprintWithBothDates(t *testing.T) {
	t.Parallel()

	at := time.Now()
	for _, tc := range []struct {
		sp   jira.Sprint
		want Block
	}{
		{jira.Sprint{State: jira.SprintActive, Start: &at, End: &at}, NotPlanned},
		{jira.Sprint{State: jira.SprintFuture}, NoDates},
		{jira.Sprint{State: jira.SprintFuture, End: &at}, NoStartDate},
		{jira.Sprint{State: jira.SprintFuture, Start: &at}, NoEndDate},
		{jira.Sprint{State: jira.SprintFuture, Start: &at, End: &at}, Clear},
	} {
		if got := CanStart(tc.sp); got != tc.want {
			t.Errorf("CanStart(%+v) = %v, want %v", tc.sp, got, tc.want)
		}
	}
	if CanComplete(jira.Sprint{State: jira.SprintFuture}) != NotRunning || CanComplete(jira.Sprint{State: jira.SprintActive}) != Clear {
		t.Error("only a running sprint can be completed")
	}
}

func TestValidate_FindsWhatThePortWouldRefuse(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name       string
		typed, was Draft
		want       Problems
	}{
		{"fine", Draft{"S", "", "2026-03-01", "2026-03-14"}, Draft{}, Problems{}},
		{"no name", Draft{"  ", "", "", ""}, Draft{}, Problems{FieldName: NoName}},
		{"bad dates", Draft{"S", "", "1 March", "14/3"}, Draft{}, Problems{FieldStart: BadDate, FieldEnd: BadDate}},
		{"backwards", Draft{"S", "", "2026-03-14", "2026-03-01"}, Draft{}, Problems{FieldEnd: EndsBeforeStart}},
		{"cleared", Draft{"S", "", "", "2026-03-14"}, Draft{"S", "", "2026-03-01", "2026-03-14"}, Problems{FieldStart: Cleared}},
	} {
		if got := Validate(tc.typed, tc.was, time.UTC); got != tc.want {
			t.Errorf("%s: %v, want %v", tc.name, got, tc.want)
		}
	}
}

func TestPatch_NamesOnlyWhatChanged(t *testing.T) {
	t.Parallel()

	was := Draft{"S", "old", "2026-03-01", "2026-03-14"}
	if _, named := Patch(was, was, false, time.UTC); named {
		t.Error("an untouched draft named a field")
	}
	p, named := Patch(Draft{"S", "new", "2026-03-01", "2026-03-20"}, was, false, time.UTC)
	if !named || p.Name != nil || p.Start != nil || p.Goal == nil || *p.Goal != "new" || p.End == nil {
		t.Errorf("patch is %+v", p)
	}
	p, _ = Patch(Draft{"S", "old", "2026-03-02", "2026-03-20"}, was, true, time.UTC)
	if p.Start != nil || p.End != nil {
		t.Errorf("a closed sprint's patch carried dates: %+v", p)
	}
}

func TestInput_TrimsAndReadsTheDatesInTheZone(t *testing.T) {
	t.Parallel()

	loc := time.FixedZone("plus9", 9*3600)
	in := Input(7, Draft{" S ", " g ", "2026-03-01", ""}, loc)
	if in.BoardID != 7 || in.Name != "S" || in.Goal != "g" || in.End != nil || in.Start == nil || in.Start.Location() != loc {
		t.Errorf("input is %+v", in)
	}
}

func TestFieldOf_MapsTheAPIsNames(t *testing.T) {
	t.Parallel()

	for name, want := range map[string]Field{"name": FieldName, "goal": FieldGoal, "startDate": FieldStart, "endDate": FieldEnd} {
		if got, ok := FieldOf(name); !ok || got != want {
			t.Errorf("FieldOf(%q) = %v, %v", name, got, ok)
		}
	}
	if _, ok := FieldOf("originBoardId"); ok {
		t.Error("a field not on the form was mapped onto one")
	}
}

func TestDaysLeft_IsNegativeOnceTheEndHasPassed(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, time.March, 5, 23, 30, 0, 0, time.UTC)
	if got := DaysLeft(time.Date(2026, time.March, 1, 12, 0, 0, 0, time.UTC), now, time.UTC); got != -4 {
		t.Errorf("DaysLeft = %d, want -4", got)
	}
}
