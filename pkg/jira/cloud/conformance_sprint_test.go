package cloud

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/varijkapil13/saral/pkg/jira"
	"github.com/varijkapil13/saral/pkg/jira/jiratest"
)

// One set of assertions, run against both adapters, for the eight methods that
// run a board's sprints. The state machine is enforced in the adapters and
// nowhere above them, so a rule the cloud adapter holds and the fake does not is
// a rule the suite never meets: every view is tested against the fake, and the
// first thing to notice would be a localised 400 from a real site.
//
// The cases are properties, not answers. The two adapters describe different
// boards and cannot agree on which sprints are on them.

// sprintWorld is a board with one sprint in each state a write can find, in
// whichever adapter's terms.
type sprintWorld struct {
	mgr     jira.SprintManager
	board   int64
	closed  int64
	active  int64
	undated int64
}

type sprintBuilder func(*testing.T) sprintWorld

// conformRogueSprint is an id neither adapter's board has.
const conformRogueSprint = int64(9999999)

func sprintFromSite(t *testing.T, opts ...jiratest.ServerOption) sprintWorld {
	t.Helper()

	s := jiratest.NewServer(append([]jiratest.ServerOption{sprintMemberRoute(t)}, opts...)...)
	t.Cleanup(s.Close)
	c, _ := testClient(t, s.URL())
	return sprintWorld{
		mgr:     c,
		board:   testBoard,
		closed:  testClosedSprint,
		active:  testActiveSprint,
		undated: testBlankSprint,
	}
}

func sprintFromFake(t *testing.T, opts ...jiratest.Option) sprintWorld {
	t.Helper()

	fake := conformFake(t, opts...)
	boards, err := fake.Boards(t.Context(), conformProject)
	if err != nil || len(boards) == 0 {
		t.Fatalf("the fake's %s has no board to run sprints on: %v", conformProject, err)
	}
	board := boards[0].ID
	world := sprintWorld{mgr: fake, board: board}
	page, err := fake.Sprints(t.Context(), board)
	if err != nil {
		t.Fatalf("reading the fake's sprints: %v", err)
	}
	all, err := jira.Collect(t.Context(), page, 0)
	if err != nil {
		t.Fatalf("walking the fake's sprints: %v", err)
	}
	for _, sp := range all {
		switch {
		case sp.State == jira.SprintClosed:
			world.closed = sp.ID
		case sp.State == jira.SprintActive:
			world.active = sp.ID
		case sp.State == jira.SprintFuture && sp.Start == nil && sp.End == nil:
			world.undated = sp.ID
		}
	}
	if world.closed == 0 || world.active == 0 || world.undated == 0 {
		t.Fatalf("the fake's board holds no sprint in some state a write can find: %+v", world)
	}
	return world
}

// sprintsNarrowedRoute answers the list route the way the endpoint does, by
// narrowing on the state parameter, so the cloud arm has to have sent it.
func sprintsNarrowedRoute(t *testing.T) jiratest.ServerOption {
	t.Helper()

	var page struct {
		MaxResults int               `json:"maxResults"`
		StartAt    int               `json:"startAt"`
		Total      int               `json:"total"`
		IsLast     bool              `json:"isLast"`
		Values     []json.RawMessage `json:"values"`
	}
	if err := json.Unmarshal(fixture(t, "sprint_page.json"), &page); err != nil {
		t.Fatalf("reading sprint_page.json: %v", err)
	}
	return jiratest.WithHandler(http.MethodGet, testSprintsRoute, func(w http.ResponseWriter, r *http.Request) {
		wanted := strings.Split(r.URL.Query().Get("state"), ",")
		kept := make([]json.RawMessage, 0, len(page.Values))
		for _, raw := range page.Values {
			var entry struct {
				State string `json:"state"`
			}
			if err := json.Unmarshal(raw, &entry); err != nil {
				continue
			}
			if r.URL.Query().Get("state") == "" || slices.Contains(wanted, entry.State) {
				kept = append(kept, raw)
			}
		}
		out, err := json.Marshal(map[string]any{
			"maxResults": page.MaxResults,
			"startAt":    0,
			"total":      len(kept),
			"isLast":     true,
			"values":     kept,
		})
		if err != nil {
			jsonHandler(http.StatusInternalServerError, `{"errorMessages":["the stand-in board broke"]}`)(w, r)
			return
		}
		jsonHandler(http.StatusOK, string(out))(w, r)
	})
}

// sprintConformCase is one property asserted of both adapters. fakeToday is
// filled in only where the fake is known not to hold the rule yet, and names
// what it answers instead so the failing arm says what to change.
type sprintConformCase struct {
	name      string
	cloud     sprintBuilder
	fake      sprintBuilder
	run       func(context.Context, sprintWorld) (jira.Sprint, error)
	assert    func(*testing.T, jira.Sprint, error)
	fakeToday string
}

func runSprintConform(t *testing.T, cases []sprintConformCase) {
	t.Helper()

	for _, tt := range cases {
		for _, adapter := range []struct {
			name string
			open sprintBuilder
		}{
			{name: "cloud", open: tt.cloud},
			{name: "fake", open: tt.fake},
		} {
			t.Run(tt.name+"/"+adapter.name, func(t *testing.T) {
				t.Parallel()

				if adapter.name == "fake" && tt.fakeToday != "" {
					t.Logf("the fake answers %s instead; only pkg/jira/jiratest/fake.go can make this row green", tt.fakeToday)
				}
				got, err := tt.run(t.Context(), adapter.open(t))
				tt.assert(t, got, err)
			})
		}
	}
}

func TestSprintLifecycle_BothAdaptersAnswerTheSameWay(t *testing.T) {
	t.Parallel()

	cases := []sprintConformCase{
		{
			name:  "only a future sprint starts",
			cloud: func(t *testing.T) sprintWorld { return sprintFromSite(t) },
			fake:  func(t *testing.T) sprintWorld { return sprintFromFake(t) },
			run: func(ctx context.Context, w sprintWorld) (jira.Sprint, error) {
				return w.mgr.StartSprint(ctx, w.closed)
			},
			assert: conformRefusesField("state"),
		},
		{
			name:  "an active sprint does not start again",
			cloud: func(t *testing.T) sprintWorld { return sprintFromSite(t) },
			fake:  func(t *testing.T) sprintWorld { return sprintFromFake(t) },
			run: func(ctx context.Context, w sprintWorld) (jira.Sprint, error) {
				return w.mgr.StartSprint(ctx, w.active)
			},
			assert: conformRefusesField("state"),
		},
		{
			name:  "a sprint with no dates on it cannot be started, and both are named",
			cloud: func(t *testing.T) sprintWorld { return sprintFromSite(t) },
			fake:  func(t *testing.T) sprintWorld { return sprintFromFake(t) },
			run: func(ctx context.Context, w sprintWorld) (jira.Sprint, error) {
				return w.mgr.StartSprint(ctx, w.undated)
			},
			assert: conformRefusesField("startDate", "endDate"),
		},
		{
			name:  "only an active sprint closes",
			cloud: func(t *testing.T) sprintWorld { return sprintFromSite(t) },
			fake:  func(t *testing.T) sprintWorld { return sprintFromFake(t) },
			run: func(ctx context.Context, w sprintWorld) (jira.Sprint, error) {
				return w.mgr.CompleteSprint(ctx, w.closed)
			},
			assert: conformRefusesField("state"),
		},
		{
			name:  "a sprint that has not started cannot be closed",
			cloud: func(t *testing.T) sprintWorld { return sprintFromSite(t) },
			fake:  func(t *testing.T) sprintWorld { return sprintFromFake(t) },
			run: func(ctx context.Context, w sprintWorld) (jira.Sprint, error) {
				return w.mgr.CompleteSprint(ctx, w.undated)
			},
			assert: conformRefusesField("state"),
		},
		{
			name:  "a patch that names one field leaves the others alone",
			cloud: func(t *testing.T) sprintWorld { return sprintFromSite(t) },
			fake:  func(t *testing.T) sprintWorld { return sprintFromFake(t) },
			run: func(ctx context.Context, w sprintWorld) (jira.Sprint, error) {
				goal := "Ship the field cache."
				return w.mgr.UpdateSprint(ctx, w.active, jira.SprintPatch{Goal: &goal})
			},
			assert: func(t *testing.T, got jira.Sprint, err error) {
				t.Helper()
				if err != nil {
					t.Fatalf("changing a sprint's goal: %v", err)
				}
				if got.Name == "" {
					t.Error("the sprint came back with no name; a patch naming the goal must not empty the rest")
				}
				if got.Start == nil || got.End == nil {
					t.Errorf("the active sprint came back with dates %v/%v; a patch naming the goal must not empty them", got.Start, got.End)
				}
			},
		},
		{
			name:  "a closed sprint takes only its name and its goal",
			cloud: func(t *testing.T) sprintWorld { return sprintFromSite(t) },
			fake:  func(t *testing.T) sprintWorld { return sprintFromFake(t) },
			run: func(ctx context.Context, w sprintWorld) (jira.Sprint, error) {
				when := time.Date(2026, 3, 1, 8, 0, 0, 0, time.UTC)
				return w.mgr.UpdateSprint(ctx, w.closed, jira.SprintPatch{Start: &when})
			},
			assert: conformRefusesField("startDate"),
		},
		{
			name:  "a closed sprint can still be renamed",
			cloud: func(t *testing.T) sprintWorld { return sprintFromSite(t) },
			fake:  func(t *testing.T) sprintWorld { return sprintFromFake(t) },
			run: func(ctx context.Context, w sprintWorld) (jira.Sprint, error) {
				name := "EX Sprint 7, as it was"
				return w.mgr.UpdateSprint(ctx, w.closed, jira.SprintPatch{Name: &name})
			},
			assert: func(t *testing.T, got jira.Sprint, err error) {
				t.Helper()
				if err != nil {
					t.Fatalf("renaming a closed sprint: %v", err)
				}
				if got.ID == 0 {
					t.Error("the rename came back with no sprint on it")
				}
			},
		},
		{
			name:  "a closed sprint says when it ended",
			cloud: func(t *testing.T) sprintWorld { return sprintFromSite(t) },
			fake:  func(t *testing.T) sprintWorld { return sprintFromFake(t) },
			run: func(ctx context.Context, w sprintWorld) (jira.Sprint, error) {
				return w.mgr.Sprint(ctx, w.closed)
			},
			assert: func(t *testing.T, got jira.Sprint, err error) {
				t.Helper()
				if err != nil {
					t.Fatalf("reading a closed sprint: %v", err)
				}
				if got.State != jira.SprintClosed {
					t.Fatalf("got state %q, want closed", got.State)
				}
				if got.Complete == nil {
					t.Error("a closed sprint carries no completion date, and it is the only reliable sign the sprint is over")
				}
				if got.Start == nil || got.End == nil {
					t.Errorf("a closed sprint came back with dates %v/%v", got.Start, got.End)
				}
			},
		},
		{
			name:  "a sprint answers with no dates until they are set",
			cloud: func(t *testing.T) sprintWorld { return sprintFromSite(t) },
			fake:  func(t *testing.T) sprintWorld { return sprintFromFake(t) },
			run: func(ctx context.Context, w sprintWorld) (jira.Sprint, error) {
				return w.mgr.Sprint(ctx, w.undated)
			},
			assert: func(t *testing.T, got jira.Sprint, err error) {
				t.Helper()
				if err != nil {
					t.Fatalf("reading an undated sprint: %v", err)
				}
				if got.Start != nil || got.End != nil || got.Complete != nil {
					t.Errorf("got %v/%v/%v; a missing date is nothing to draw, not a failed read", got.Start, got.End, got.Complete)
				}
			},
		},
		{
			name:  "a sprint the board does not have is named as a sprint",
			cloud: func(t *testing.T) sprintWorld { return sprintFromSite(t) },
			fake:  func(t *testing.T) sprintWorld { return sprintFromFake(t) },
			run: func(ctx context.Context, w sprintWorld) (jira.Sprint, error) {
				return w.mgr.Sprint(ctx, conformRogueSprint)
			},
			assert: func(t *testing.T, _ jira.Sprint, err error) {
				t.Helper()
				var missing *jira.NotFoundError
				if !errors.As(err, &missing) {
					t.Fatalf("got %T (%v), want a *jira.NotFoundError", err, err)
				}
				if missing.Kind != "sprint" || missing.ID != strconv.FormatInt(conformRogueSprint, 10) {
					t.Errorf("the failure names %s %s rather than the sprint asked for", missing.Kind, missing.ID)
				}
			},
		},
		{
			name:  "a sprint needs a name",
			cloud: func(t *testing.T) sprintWorld { return sprintFromSite(t) },
			fake:  func(t *testing.T) sprintWorld { return sprintFromFake(t) },
			run: func(ctx context.Context, w sprintWorld) (jira.Sprint, error) {
				return w.mgr.CreateSprint(ctx, jira.SprintInput{BoardID: w.board, Name: "   "})
			},
			assert: conformRefusesField("name"),
		},
		{
			name:  "a created sprint is a future sprint",
			cloud: func(t *testing.T) sprintWorld { return sprintFromSite(t) },
			fake:  func(t *testing.T) sprintWorld { return sprintFromFake(t) },
			run: func(ctx context.Context, w sprintWorld) (jira.Sprint, error) {
				return w.mgr.CreateSprint(ctx, jira.SprintInput{BoardID: w.board, Name: "EX Sprint 10"})
			},
			assert: func(t *testing.T, got jira.Sprint, err error) {
				t.Helper()
				if err != nil {
					t.Fatalf("creating a sprint: %v", err)
				}
				if got.State != jira.SprintFuture {
					t.Errorf("a new sprint came back %q; the only state a create can leave is future", got.State)
				}
				if got.ID == 0 {
					t.Error("a new sprint came back with no id, so nothing can start it")
				}
			},
		},
		{
			name:  "moving no issues is a move of nothing rather than a failure",
			cloud: func(t *testing.T) sprintWorld { return sprintFromSite(t) },
			fake:  func(t *testing.T) sprintWorld { return sprintFromFake(t) },
			run: func(ctx context.Context, w sprintWorld) (jira.Sprint, error) {
				return jira.Sprint{}, w.mgr.MoveToBacklog(ctx, nil)
			},
			assert: func(t *testing.T, _ jira.Sprint, err error) {
				t.Helper()
				if err != nil {
					t.Fatalf("moving nothing to the backlog: %v", err)
				}
			},
		},
	}

	runSprintConform(t, cases)
}

// Six rules the cloud adapter holds and the fake does not, so every fake arm
// below fails. That is the honest state, and it goes green when
// pkg/jira/jiratest/fake.go holds the same rules: skipping a row, or asserting
// the fake's present answer instead, hides the divergence rather than closing
// it. The worst of the six runs the other way — the fake refuses a move of more
// than fifty issues that the adapter chunks and the port accepts, so a view
// written against the fake learns a cap that is not the port's.
func TestSprintLifecycle_RulesTheFakeDoesNotHold(t *testing.T) {
	t.Parallel()

	late := time.Date(2026, 3, 1, 8, 0, 0, 0, time.UTC)
	early := late.AddDate(0, 0, -14)
	blank := "   "
	overTheCap := issueKeys(sprintMoveChunk + 1)

	cases := []sprintConformCase{
		{
			name:      "more than fifty issues is chunked rather than refused",
			fakeToday: `a *jira.ValidationError naming "issues", from fakeCheckBatch's len(keys) > 50`,
			cloud:     func(t *testing.T) sprintWorld { return sprintFromSite(t) },
			fake: func(t *testing.T) sprintWorld {
				return sprintFromFake(t, jiratest.WithIssues(conformIssues(overTheCap)))
			},
			run: func(ctx context.Context, w sprintWorld) (jira.Sprint, error) {
				return jira.Sprint{}, w.mgr.MoveToBacklog(ctx, overTheCap)
			},
			assert: conformGoesThrough,
		},
		{
			name:      "an update that names no field is refused rather than sent",
			fakeToday: "no error, and the sprint unchanged",
			cloud:     func(t *testing.T) sprintWorld { return sprintFromSite(t) },
			fake:      func(t *testing.T) sprintWorld { return sprintFromFake(t) },
			run: func(ctx context.Context, w sprintWorld) (jira.Sprint, error) {
				return w.mgr.UpdateSprint(ctx, w.active, jira.SprintPatch{})
			},
			assert: conformRefusesField(),
		},
		{
			name:      "a sprint cannot end before it starts",
			fakeToday: "no error, and a sprint whose end precedes its start",
			cloud:     func(t *testing.T) sprintWorld { return sprintFromSite(t) },
			fake:      func(t *testing.T) sprintWorld { return sprintFromFake(t) },
			run: func(ctx context.Context, w sprintWorld) (jira.Sprint, error) {
				return w.mgr.CreateSprint(ctx, jira.SprintInput{BoardID: w.board, Name: "EX Sprint 10", Start: &late, End: &early})
			},
			assert: conformRefusesField("endDate"),
		},
		{
			name:      "a rename to nothing but spaces is refused",
			fakeToday: "no error, and a sprint left with a name of spaces",
			cloud:     func(t *testing.T) sprintWorld { return sprintFromSite(t) },
			fake:      func(t *testing.T) sprintWorld { return sprintFromFake(t) },
			run: func(ctx context.Context, w sprintWorld) (jira.Sprint, error) {
				return w.mgr.UpdateSprint(ctx, w.active, jira.SprintPatch{Name: &blank})
			},
			assert: conformRefusesField("name"),
		},
		{
			name:      "a blank issue key is a bad request rather than a missing issue",
			fakeToday: "a *jira.NotFoundError naming the blank string as an issue",
			cloud:     func(t *testing.T) sprintWorld { return sprintFromSite(t) },
			fake:      func(t *testing.T) sprintWorld { return sprintFromFake(t) },
			run: func(ctx context.Context, w sprintWorld) (jira.Sprint, error) {
				return jira.Sprint{}, w.mgr.MoveToBacklog(ctx, []string{blank})
			},
			assert: conformRefusesField("issues"),
		},
		{
			name:      "an id that identifies nothing is refused without asking",
			fakeToday: "a *jira.NotFoundError naming sprint 0",
			cloud:     func(t *testing.T) sprintWorld { return sprintFromSite(t) },
			fake:      func(t *testing.T) sprintWorld { return sprintFromFake(t) },
			run: func(ctx context.Context, w sprintWorld) (jira.Sprint, error) {
				return w.mgr.Sprint(ctx, 0)
			},
			assert: conformRefusesField("sprintId"),
		},
	}

	runSprintConform(t, cases)
}

// conformIssues is the issues a move needs to exist, so that a case about a cap
// cannot pass or fail over a key the fake has never heard of.
func conformIssues(keys []string) []jira.Issue {
	out := make([]jira.Issue, 0, len(keys))
	for _, key := range keys {
		out = append(out, jira.Issue{Key: key, Project: jira.ProjectRef{Key: conformProject}})
	}
	return out
}

func conformGoesThrough(t *testing.T, _ jira.Sprint, err error) {
	t.Helper()

	if err != nil {
		t.Fatalf("got %T (%v), want it to go through", err, err)
	}
}

func TestSprints_BothAdaptersNarrowToTheStatesAsked(t *testing.T) {
	t.Parallel()

	for _, adapter := range []struct {
		name string
		open sprintBuilder
	}{
		{name: "cloud", open: func(t *testing.T) sprintWorld { return sprintFromSite(t, sprintsNarrowedRoute(t)) }},
		{name: "fake", open: func(t *testing.T) sprintWorld { return sprintFromFake(t) }},
	} {
		t.Run(adapter.name, func(t *testing.T) {
			t.Parallel()

			world := adapter.open(t)
			page, err := world.mgr.Sprints(t.Context(), world.board, jira.SprintActive)
			if err != nil {
				t.Fatalf("listing the active sprints: %v", err)
			}
			got, err := jira.Collect(t.Context(), page, 0)
			if err != nil {
				t.Fatalf("walking the active sprints: %v", err)
			}
			if len(got) == 0 {
				t.Fatal("narrowing to the active sprints answered with none, and there is one")
			}
			for _, sp := range got {
				if sp.State != jira.SprintActive {
					t.Errorf("sprint %d is %s, and only the active ones were asked for", sp.ID, sp.State)
				}
				if sp.BoardID != world.board {
					t.Errorf("sprint %d says it is on board %d rather than %d", sp.ID, sp.BoardID, world.board)
				}
			}
		})
	}
}

// conformRefusesField asserts a refusal that a form can act on: the typed
// validation error, naming every field it is about.
func conformRefusesField(fields ...string) func(*testing.T, jira.Sprint, error) {
	return func(t *testing.T, _ jira.Sprint, err error) {
		t.Helper()

		var rejected *jira.ValidationError
		if !errors.As(err, &rejected) {
			t.Fatalf("got %T (%v), want a *jira.ValidationError", err, err)
		}
		for _, field := range fields {
			if _, named := rejected.For(field); !named {
				t.Errorf("the refusal says %q and never names %s, so a form has nothing to annotate", rejected.Error(), field)
			}
		}
	}
}
