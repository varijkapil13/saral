package sprint

import (
	"context"
	"errors"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/varijkapil13/saral/internal/ui/kernel"
	"github.com/varijkapil13/saral/pkg/jira"
	"github.com/varijkapil13/saral/pkg/jira/jiratest"
)

// refusing is the fake with one method made to fail, whatever else is called
// before it. FailNext cannot aim at a method: a completion makes four kinds of
// call and the one under test is the third.
type refusing struct {
	*jiratest.Fake
	mu     sync.Mutex
	method string
	err    error
}

func (r *refusing) refuses(method string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.method == method {
		return r.err
	}
	return nil
}

func (r *refusing) MoveToSprint(ctx context.Context, sprintID int64, keys []string) error {
	if err := r.refuses("MoveToSprint"); err != nil {
		return err
	}
	return r.Fake.MoveToSprint(ctx, sprintID, keys)
}

func (r *refusing) CompleteSprint(ctx context.Context, id int64) (jira.Sprint, error) {
	if err := r.refuses("CompleteSprint"); err != nil {
		return jira.Sprint{}, err
	}
	return r.Fake.CompleteSprint(ctx, id)
}

func (r *refusing) CreateSprint(ctx context.Context, in jira.SprintInput) (jira.Sprint, error) {
	if err := r.refuses("CreateSprint"); err != nil {
		return jira.Sprint{}, err
	}
	return r.Fake.CreateSprint(ctx, in)
}

func (r *refusing) SprintIssues(ctx context.Context, boardID, sprintID int64, q jira.BoardQuery) (jira.Page[jira.Issue], error) {
	if err := r.refuses("SprintIssues"); err != nil {
		return jira.Page[jira.Issue]{}, err
	}
	return r.Fake.SprintIssues(ctx, boardID, sprintID, q)
}

// seeded is a view over the fake with issues in the running sprint, read after
// they were put there so its progress counts them.
func seeded(t *testing.T, client jira.SessionClient, f *jiratest.Fake, keys ...string) *driver {
	t.Helper()
	return seededWith(t, testDeps(client), f, keys...)
}

func seededWith(t *testing.T, d kernel.Deps, f *jiratest.Fake, keys ...string) *driver {
	t.Helper()
	dr := newDriver(t, d, 120, 24)
	dr.onSprint("Sprint 2")
	if err := f.MoveToSprint(t.Context(), dr.m.selected().ID, keys); err != nil {
		t.Fatalf("seeding the sprint: %v", err)
	}
	dr.send(kernel.RefreshMsg{})
	dr.onSprint("Sprint 2")
	return dr
}

func sprintNamed(t *testing.T, dr *driver, name string) jira.Sprint {
	t.Helper()
	for _, sp := range dr.m.sprints {
		if sp.Name == name {
			return sp
		}
	}
	t.Fatalf("no sprint called %q is on the list; it holds %v", name, dr.names())
	return jira.Sprint{}
}

func inSprint(t *testing.T, f *jiratest.Fake, sp jira.Sprint) []string {
	t.Helper()
	page, err := f.SprintIssues(t.Context(), sp.BoardID, sp.ID, jira.BoardQuery{Fields: []string{"status"}})
	if err != nil {
		t.Fatalf("reading %s back: %v", sp.Name, err)
	}
	out := make([]string, 0, len(page.Items))
	for _, iss := range page.Items {
		out = append(out, iss.Key)
	}
	return out
}

func callOrder(f *jiratest.Fake, names ...string) []string {
	var out []string
	for _, call := range f.Calls() {
		if slices.Contains(names, call) {
			out = append(out, call)
		}
	}
	return out
}

func TestSprints_CompletingSendsTheOpenIssuesWhereverTheReaderChose(t *testing.T) {
	t.Parallel()

	t.Run("the backlog is the default and moves nothing itself", func(t *testing.T) {
		t.Parallel()
		f := newFake()
		dr := seeded(t, f, f, "PROJ-1", "PROJ-3")
		dr.key("c", "y")
		if n := countCalls(f, "MoveToSprint"); n != 1 {
			t.Errorf("MoveToSprint ran %d times beyond the seeding; the close itself sends them to the backlog", n-1)
		}
		if n := countCalls(f, "CompleteSprint"); n != 1 {
			t.Errorf("CompleteSprint ran %d times, want once", n)
		}
	})

	t.Run("the next planned sprint gets them before the close", func(t *testing.T) {
		t.Parallel()
		f := newFake()
		dr := seeded(t, f, f, "PROJ-1", "PROJ-3")
		open := slices.Clone(dr.m.progress[dr.m.selected().ID].open)
		if len(open) == 0 {
			t.Fatal("the seeded sprint has nothing open, so this proves nothing")
		}
		dr.key("c", "tab")
		if d := dr.m.pending.dest(); d.kind != destNext || d.sprint.Name != "Sprint 3" {
			t.Fatalf("tab chose %+v, want the next planned sprint", d)
		}
		dr.key("y")

		if got := callOrder(f, "MoveToSprint", "CompleteSprint"); !slices.Equal(got, []string{"MoveToSprint", "MoveToSprint", "CompleteSprint"}) {
			t.Errorf("the calls ran %v, want the move before the close", got)
		}
		if got := inSprint(t, f, sprintNamed(t, dr, "Sprint 3")); !slices.Equal(got, open) {
			t.Errorf("Sprint 3 holds %v, want the open issues %v", got, open)
		}
		if sp := sprintNamed(t, dr, "Sprint 2"); sp.State != jira.SprintClosed {
			t.Errorf("Sprint 2 is %s after the completion", sp.State)
		}
		mustContain(t, dr.lastStatus().Text, "Sprint 2 is closed", "moved into Sprint 3")
	})

	t.Run("a new sprint is created, named on from this one, and gets them", func(t *testing.T) {
		t.Parallel()
		f := newFake()
		dr := seeded(t, f, f, "PROJ-1", "PROJ-3")
		open := slices.Clone(dr.m.progress[dr.m.selected().ID].open)
		dr.key("c", "tab", "tab")
		if d := dr.m.pending.dest(); d.kind != destNew || d.name != "Sprint 4" {
			t.Fatalf("two tabs chose %+v, want a new sprint called Sprint 4", d)
		}
		dr.key("y")

		made := sprintNamed(t, dr, "Sprint 4")
		if made.State != jira.SprintFuture || made.BoardID != dr.m.boards[0].ID {
			t.Errorf("the new sprint is %+v, want a planned sprint on the same board", made)
		}
		if got := inSprint(t, f, made); !slices.Equal(got, open) {
			t.Errorf("the new sprint holds %v, want %v", got, open)
		}
		mustContain(t, dr.lastStatus().Text, "moved into Sprint 4", "new and planned")
	})

	t.Run("shift+tab goes round the other way", func(t *testing.T) {
		t.Parallel()
		f := newFake()
		dr := seeded(t, f, f, "PROJ-1")
		dr.key("c", "shift+tab")
		if d := dr.m.pending.dest(); d.kind != destNew {
			t.Errorf("shift+tab from the first choice chose %+v, want the last", d)
		}
	})
}

// A completion that stops part way says how far it got, keeps what it made,
// and never closes a sprint whose open issues did not all reach their new one.
func TestSprints_ACompletionThatStopsPartWaySaysHowFarItGot(t *testing.T) {
	t.Parallel()

	for name, tc := range map[string]struct {
		method string
		err    error
		pick   []string
		want   []string
		closed bool
	}{
		"the move is refused": {
			method: "MoveToSprint", pick: []string{"tab"},
			err:  &jira.CapabilityError{Capability: jira.CapBoards, Reason: "needs the Schedule Issues permission"},
			want: []string{"moved 0 of", "still running and was not closed", "Schedule Issues"},
		},
		"the move stops part way": {
			method: "MoveToSprint", pick: []string{"tab"},
			err: &jira.PartialMoveError{
				Op: "moving to the sprint", Moved: []string{"PROJ-1"}, Pending: []string{"PROJ-3"},
				Err: &jira.RateLimitError{RetryAfter: time.Minute},
			},
			want: []string{"moved 1 of", "still running and was not closed"},
		},
		"the new sprint cannot be created": {
			method: "CreateSprint", pick: []string{"tab", "tab"},
			err:  &jira.TransportError{Op: "POST /sprint", Err: errors.New("connection reset")},
			want: []string{"nothing was moved", "still running", "could not be created", "connection reset"},
		},
		"the issues cannot be read": {
			method: "SprintIssues", pick: []string{"tab"},
			err:  &jira.RateLimitError{RetryAfter: time.Minute},
			want: []string{"nothing was moved", "still running", "could not be read"},
		},
		"the close is refused after everything moved": {
			method: "CompleteSprint", pick: []string{"tab"},
			err:  &jira.RateLimitError{RetryAfter: time.Minute},
			want: []string{"open issues moved into Sprint 3", "was not closed"},
		},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			f := newFake()
			r := &refusing{Fake: f}
			dr := seeded(t, r, f, "PROJ-1", "PROJ-3")
			r.mu.Lock()
			r.method, r.err = tc.method, tc.err
			r.mu.Unlock()
			dr.key("c")
			dr.key(tc.pick...)
			dr.key("y")

			if sp := sprintNamed(t, dr, "Sprint 2"); sp.State != jira.SprintActive {
				t.Errorf("Sprint 2 is %s, want it still running", sp.State)
			}
			if tc.method != "CompleteSprint" && countCalls(f, "CompleteSprint") != 0 {
				t.Error("the sprint was closed although its open issues did not all move")
			}
			var stopped *completionError
			if !errors.As(dr.m.failure, &stopped) {
				t.Fatalf("the failure is %T (%v), want the completion's own account of it", dr.m.failure, dr.m.failure)
			}
			mustContain(t, dr.m.failure.Error(), tc.want...)
			mustContain(t, dr.view(), tc.want[0])
			if st := dr.lastStatus(); st.Level != kernel.LevelError {
				t.Errorf("the status line said %q at level %v, want the failure", st.Text, st.Level)
			}
		})
	}
}

func TestSprints_ANewSprintAFailedMoveMadeStaysOnTheList(t *testing.T) {
	t.Parallel()

	f := newFake()
	r := &refusing{Fake: f, method: "MoveToSprint", err: &jira.TransportError{Op: "POST", Err: errors.New("reset")}}
	dr := newDriver(t, testDeps(r), 120, 24)
	dr.onSprint("Sprint 2")
	r.mu.Lock()
	r.method = ""
	r.mu.Unlock()
	if err := f.MoveToSprint(t.Context(), dr.m.selected().ID, []string{"PROJ-1"}); err != nil {
		t.Fatalf("seeding: %v", err)
	}
	r.mu.Lock()
	r.method = "MoveToSprint"
	r.mu.Unlock()
	dr.send(kernel.RefreshMsg{})
	dr.onSprint("Sprint 2")
	dr.key("c", "tab", "tab", "y")
	if made := sprintNamed(t, dr, "Sprint 4"); made.State != jira.SprintFuture {
		t.Errorf("the sprint the completion created is %s", made.State)
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
	} {
		m := &Model{}
		for i, name := range append([]string{tc.name}, tc.board...) {
			m.sprints = append(m.sprints, jira.Sprint{ID: int64(i + 1), BoardID: 1, Name: name})
		}
		m.sprints = append(m.sprints, jira.Sprint{ID: 99, BoardID: 2, Name: tc.want})
		if got := m.successorName(m.sprints[0]); got != tc.want {
			t.Errorf("after %q with %v on the board: %q, want %q", tc.name, tc.board, got, tc.want)
		}
	}
}

func TestDaysLeft_CountsCalendarDaysInTheAccountsZone(t *testing.T) {
	t.Parallel()

	berlin, err := time.LoadLocation("Europe/Berlin")
	if err != nil {
		t.Skip("no zone database on this machine")
	}
	now := time.Date(2026, time.March, 5, 23, 30, 0, 0, time.UTC)
	for _, tc := range []struct {
		end  time.Time
		loc  *time.Location
		want string
	}{
		{time.Date(2026, time.March, 9, 12, 0, 0, 0, time.UTC), time.UTC, "4 days left"},
		{time.Date(2026, time.March, 6, 12, 0, 0, 0, time.UTC), time.UTC, "1 day left"},
		{time.Date(2026, time.March, 5, 23, 45, 0, 0, time.UTC), time.UTC, "ends today"},
		{time.Date(2026, time.March, 6, 12, 0, 0, 0, time.UTC), berlin, "ends today"},
		{time.Date(2026, time.March, 4, 12, 0, 0, 0, time.UTC), time.UTC, "ended yesterday and is still running"},
		{time.Date(2026, time.March, 1, 12, 0, 0, 0, time.UTC), time.UTC, "ended 4 days ago and is still running"},
	} {
		if got := daysLeft(tc.end, now, tc.loc); got != tc.want {
			t.Errorf("ending %s in %s: %q, want %q", tc.end, tc.loc, got, tc.want)
		}
	}
}

// Done is the board's last column with a status in it, and points are the
// board's own estimation field — neither is a status category or a field name.
func TestSprints_ProgressCountsByTheBoardsLastColumnAndItsEstimationField(t *testing.T) {
	t.Parallel()

	f := newFake()
	dr := seeded(t, f, f, "PROJ-1", "PROJ-2", "PROJ-3", "PROJ-4", "PROJ-5", "PROJ-6")
	sp := dr.m.selected()
	cfg, err := f.BoardConfig(t.Context(), sp.BoardID)
	if err != nil {
		t.Fatalf("BoardConfig: %v", err)
	}
	done := doneStatuses(cfg)
	page, err := f.SprintIssues(t.Context(), sp.BoardID, sp.ID, jira.BoardQuery{Fields: []string{"status", cfg.Estimation.Field.ID}})
	if err != nil {
		t.Fatalf("SprintIssues: %v", err)
	}
	var want progress
	want.estimated = true
	for i := range page.Items {
		want.count(&page.Items[i], done, cfg.Estimation.Field)
	}
	got := dr.m.progress[sp.ID]
	if got.total != 6 || got.done != want.done || got.points != want.points || got.donePoints != want.donePoints {
		t.Errorf("progress is %+v, want %+v", got, want)
	}
	if want.done == 0 || want.done == want.total {
		t.Fatalf("the seed has %d of %d done, so this proves nothing about which column counts", want.done, want.total)
	}
	mustContain(t, dr.view(), want.words())
}

func TestSprints_ProgressThatCannotBeReadSaysSoAndLeavesTheListAlone(t *testing.T) {
	t.Parallel()

	for name, err := range map[string]error{
		"a 403":               &jira.CapabilityError{Capability: jira.CapBoards, Reason: "needs Browse Projects"},
		"a 429":               &jira.RateLimitError{RetryAfter: time.Minute},
		"a transport failure": &jira.TransportError{Op: "GET", Err: errors.New("connection reset")},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			f := newFake()
			r := &refusing{Fake: f, method: "SprintIssues", err: err}
			dr := newDriver(t, testDeps(r), 120, 20)
			dr.onSprint("Sprint 2")
			frame := dr.view()
			mustContain(t, frame, "could not be counted")
			if dr.m.failure != nil {
				t.Errorf("a progress read put %v on the whole pane", dr.m.failure)
			}
			if len(dr.m.sprints) == 0 {
				t.Error("the list emptied over a progress read")
			}
		})
	}
}

func TestSprints_TheDetailNamesTheGoalTheZoneAndTheDaysLeft(t *testing.T) {
	t.Parallel()

	f := newFake()
	d := testDeps(f)
	loc, err := time.LoadLocation("Asia/Tokyo")
	if err != nil {
		t.Skip("no zone database on this machine")
	}
	d.Caps.TimeZone = loc
	dr := newDriver(t, d, 120, 20)
	dr.onSprint("Sprint 2")
	frame := dr.view()
	mustContain(t, frame, "(Asia/Tokyo)", "Goal: Make it usable", "days left")
	if !strings.Contains(frame, "issues done") && !strings.Contains(frame, "0 of 0") {
		t.Errorf("the running sprint's progress is missing:\n%s", frame)
	}
}
