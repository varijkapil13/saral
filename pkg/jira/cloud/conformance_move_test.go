package cloud

import (
	"errors"
	"net/http"
	"slices"
	"strings"
	"testing"

	"github.com/varijkapil13/saral/pkg/jira"
	"github.com/varijkapil13/saral/pkg/jira/jiratest"
)

// One table of assertions about moving issues, run against both adapters through
// the role that names the methods. The two describe different sites on purpose,
// so what is asserted here is the property and never the answer: which task id
// comes back is the site's business, and that the ref can be polled at all is
// this client's.

type moveBuilder func(*testing.T) jira.Relocator

// conformMoveKeys are issues both adapters hold: the fixture site's project is
// EX and the fake is given generated issues under the same key.
var conformMoveKeys = []string{conformProject + "-1", conformProject + "-2"}

// conformMoveType is an issue type id the fake's catalogue has. Nothing about it
// is site knowledge — a target type is resolved from createmeta — but a fake
// that validates the target needs one that exists.
const conformMoveType = "10301"

func conformMove() jira.MoveRequest {
	return jira.MoveRequest{
		Keys:              slices.Clone(conformMoveKeys),
		TargetProjectKey:  conformProject,
		TargetIssueTypeID: conformMoveType,
	}
}

func moveFromSite(t *testing.T, opts ...jiratest.ServerOption) jira.Relocator {
	t.Helper()

	s := jiratest.NewServer(opts...)
	t.Cleanup(s.Close)
	c, _ := testClient(t, s.URL(), WithRetry(RetryPolicy{Attempts: 1}))
	return c
}

func moveFromFake(t *testing.T, opts ...jiratest.Option) jira.Relocator {
	t.Helper()

	return fakeThatMoves(t, opts...)
}

func fakeThatMoves(t *testing.T, opts ...jiratest.Option) *jiratest.Fake {
	t.Helper()

	return conformFake(t, append([]jiratest.Option{
		jiratest.WithIssues(jiratest.GenFor(conformProject, len(conformMoveKeys))),
	}, opts...)...)
}

func TestBulkMove_BothAdaptersAnswerTheSameWay(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name   string
		cloud  moveBuilder
		fake   moveBuilder
		in     jira.MoveRequest
		assert func(*testing.T, jira.Relocator, jira.TaskRef, error)
	}{
		{
			name:   "a move that can be followed afterwards",
			cloud:  func(t *testing.T) jira.Relocator { return moveFromSite(t) },
			fake:   func(t *testing.T) jira.Relocator { return moveFromFake(t) },
			in:     conformMove(),
			assert: assertTheMoveCanBeFollowed,
		},
		{
			name:  "a move with nothing in it",
			cloud: func(t *testing.T) jira.Relocator { return moveFromSite(t) },
			fake:  func(t *testing.T) jira.Relocator { return moveFromFake(t) },
			in:    jira.MoveRequest{TargetProjectKey: conformProject, TargetIssueTypeID: conformMoveType},
			assert: func(t *testing.T, _ jira.Relocator, ref jira.TaskRef, err error) {
				assertNothingWasSubmitted(t, ref, err)
			},
		},
		{
			name:  "more issues than the endpoint takes",
			cloud: func(t *testing.T) jira.Relocator { return moveFromSite(t) },
			fake:  func(t *testing.T) jira.Relocator { return moveFromFake(t) },
			in:    tooManyToMove(),
			assert: func(t *testing.T, _ jira.Relocator, ref jira.TaskRef, err error) {
				assertNothingWasSubmitted(t, ref, err)
			},
		},
		{
			name:  "a move with no target issue type",
			cloud: func(t *testing.T) jira.Relocator { return moveFromSite(t) },
			fake:  func(t *testing.T) jira.Relocator { return moveFromFake(t) },
			in: jira.MoveRequest{
				Keys:             slices.Clone(conformMoveKeys),
				TargetProjectKey: conformProject,
			},
			assert: func(t *testing.T, _ jira.Relocator, ref jira.TaskRef, err error) {
				assertNothingWasSubmitted(t, ref, err)
			},
		},
		{
			name: "a token that may not move issues between projects",
			cloud: func(t *testing.T) jira.Relocator {
				return moveFromSite(t, jiratest.WithStatus(http.MethodPost, movePattern, http.StatusForbidden, "plans_403.json"))
			},
			fake: func(t *testing.T) jira.Relocator {
				return moveFromFake(t, jiratest.WithCapabilities(jiratest.NoBulkMove))
			},
			in: conformMove(),
			assert: func(t *testing.T, _ jira.Relocator, ref jira.TaskRef, err error) {
				assertRefusalNamesBulkMove(t, ref, err)
			},
		},
		{
			name:   "a task polled by the id alone, with the endpoint dropped",
			cloud:  func(t *testing.T) jira.Relocator { return moveFromSite(t) },
			fake:   func(t *testing.T) jira.Relocator { return moveFromFake(t) },
			in:     conformMove(),
			assert: assertARefWithoutItsEndpointIsRefused,
		},
		{
			name: "a move that leaves issues behind",
			cloud: func(t *testing.T) jira.Relocator {
				return moveFromSite(t, jiratest.WithFixture(http.MethodGet, bulkQueuePattern, "bulkmove_task_failed.json"))
			},
			fake: func(t *testing.T) jira.Relocator {
				f := fakeThatMoves(t)
				f.FailNextTask()
				return f
			},
			in:     conformMove(),
			assert: assertTheFailuresAreNamedTheSameWay,
		},
	}

	for _, tt := range cases {
		for _, adapter := range []struct {
			name string
			open moveBuilder
		}{
			{name: "cloud", open: tt.cloud},
			{name: "fake", open: tt.fake},
		} {
			t.Run(tt.name+"/"+adapter.name, func(t *testing.T) {
				t.Parallel()

				under := adapter.open(t)
				ref, err := under.BulkMove(t.Context(), tt.in)
				tt.assert(t, under, ref, err)
			})
		}
	}
}

// tooManyToMove names 1001 issues, spelt out rather than built from
// bulkMoveMax: a cap compared against its own constant is satisfied whatever the
// constant says, and the fake holds its own literal 1000 that this has to agree
// with. Widening bulkMoveMax has to fail here.
func tooManyToMove() jira.MoveRequest {
	in := conformMove()
	in.Keys = make([]string, 1001)
	for i := range in.Keys {
		in.Keys[i] = conformMoveKeys[0]
	}
	return in
}

// assertTheMoveCanBeFollowed is the property the whole packet exists for: a
// submit hands back something that can be polled, and it polls the queue the
// operation is actually in rather than the generic task registry, whose body
// reads as an empty version of this one.
func assertTheMoveCanBeFollowed(t *testing.T, under jira.Relocator, ref jira.TaskRef, err error) {
	t.Helper()

	if err != nil {
		t.Fatalf("submitting the move: %v", err)
	}
	if ref.ID == "" {
		t.Error("the move came back with no task id")
	}
	if !strings.Contains(ref.URL, "/bulk/queue/") {
		t.Errorf("the task is polled at %q, which is not the bulk queue", ref.URL)
	}
	if strings.Contains(ref.URL, "/rest/api/3/task/") {
		t.Errorf("the task is polled at %q, which answers the other progress shape", ref.URL)
	}

	got, err := under.Task(t.Context(), ref)
	if err != nil {
		t.Fatalf("polling the ref the move returned: %v", err)
	}
	if got.Ref.ID != ref.ID {
		t.Errorf("the snapshot names task %q, want the %q that was submitted", got.Ref.ID, ref.ID)
	}
	if !slices.Contains(conformTaskStates, got.State) {
		t.Errorf("the task reads as %q, which is not one of Jira's seven task states", got.State)
	}
	if got.Progress < 0 || got.Progress > 100 {
		t.Errorf("progress is %d, and the port promises a percentage", got.Progress)
	}
}

// conformTaskStates is the whole enum, CANCEL_REQUESTED included: a state a
// poller has no case for reads as a stopped task on the one hand or as an
// unknown string on the other.
var conformTaskStates = []jira.TaskState{
	jira.TaskEnqueued, jira.TaskRunning, jira.TaskComplete, jira.TaskFailed,
	jira.TaskCancelRequested, jira.TaskCancelled, jira.TaskDead,
}

// assertNothingWasSubmitted names the error type and not merely its presence.
// Any non-nil error passes a table that checks only err != nil, so the two
// adapters can refuse the same request in two different ways — and a view
// branching on the type then behaves differently against each.
//
// A request either adapter can refuse without asking a site is a validation
// failure: it is the values in the request that are wrong, and nothing was
// looked up to find that out.
func assertNothingWasSubmitted(t *testing.T, ref jira.TaskRef, err error) {
	t.Helper()

	if err == nil {
		t.Fatalf("the move was accepted as task %+v, and a caller now polls a move of nothing", ref)
	}
	var invalid *jira.ValidationError
	if !errors.As(err, &invalid) {
		t.Errorf("got %T (%v), want a *jira.ValidationError: a request neither adapter had to ask a site about "+
			"is refused for what it says, not for something that could not be found", err, err)
	}
	if ref != (jira.TaskRef{}) {
		t.Errorf("the refusal came back with %+v attached, want no ref at all", ref)
	}
}

// assertARefWithoutItsEndpointIsRefused is the divergence a view walks into by
// keeping a task's id and dropping its URL, which is the natural thing to do
// with a two-field struct.
//
// A submit answers an id and no link, so the ref's URL is the only record of
// which of the two progress registries the task is in — they answer bodies that
// read as empty versions of each other. An adapter that polls on the id alone is
// guessing, so both must refuse.
func assertARefWithoutItsEndpointIsRefused(t *testing.T, under jira.Relocator, ref jira.TaskRef, err error) {
	t.Helper()

	if err != nil {
		t.Fatalf("submitting the move: %v", err)
	}
	got, err := under.Task(t.Context(), jira.TaskRef{ID: ref.ID})
	if err == nil {
		t.Fatalf("polling %q with no progress endpoint answered %+v; the id alone does not say which registry the task is in",
			ref.ID, got)
	}
	var invalid *jira.ValidationError
	if !errors.As(err, &invalid) {
		t.Errorf("got %T (%v), want a *jira.ValidationError: a ref with no endpoint is a ref this client cannot use, "+
			"not a task that is missing", err, err)
	}
}

// assertTheFailuresAreNamedTheSameWay is the other half of the same problem: a
// view renders TaskStatus.Failed as a list of issues, so the two adapters have
// to agree on what kind of identifier is in it.
//
// The queue body keys failedAccessibleIssues by numeric issue id and carries
// nothing that turns one into a key, so ids are what the port can hold. Anything
// that renders as EX-1 against one adapter and 10002 against the other is a list
// no caller can click.
func assertTheFailuresAreNamedTheSameWay(t *testing.T, under jira.Relocator, ref jira.TaskRef, err error) {
	t.Helper()

	if err != nil {
		t.Fatalf("submitting the move: %v", err)
	}
	got := pollUntilStopped(t, under, ref)
	if got.State != jira.TaskFailed {
		t.Fatalf("the task stopped as %q, want %q: this case exists to see a failure list", got.State, jira.TaskFailed)
	}
	if len(got.Failed) == 0 {
		t.Fatalf("a failed move named no issue at all, and the only per-issue diagnostic the endpoint gives is that list")
	}
	for _, named := range got.Failed {
		if !isIssueID(named) {
			t.Errorf("the failures name %v; the queue keys them by numeric issue id and nothing on that body turns "+
				"one into a key, so %q is an identifier one adapter invented", got.Failed, named)
		}
	}
}

// isIssueID reports whether an identifier is an issue id rather than an issue
// key. Jira ids are digits and every key carries a project prefix and a hyphen.
func isIssueID(in string) bool {
	if in == "" {
		return false
	}
	return strings.IndexFunc(in, func(r rune) bool { return r < '0' || r > '9' }) < 0
}

// pollUntilStopped drives a task to its end state. The fake walks one step per
// poll and the replay server answers the same body every time, so a fixed bound
// covers both without either waiting on a clock.
func pollUntilStopped(t *testing.T, under jira.Relocator, ref jira.TaskRef) jira.TaskStatus {
	t.Helper()

	const polls = 6
	var got jira.TaskStatus
	for range polls {
		var err error
		got, err = under.Task(t.Context(), ref)
		if err != nil {
			t.Fatalf("polling the move: %v", err)
		}
		if got.State.Done() {
			return got
		}
	}
	t.Fatalf("the task still reads as %q after %d polls", got.State, polls)
	return got
}

func assertRefusalNamesBulkMove(t *testing.T, ref jira.TaskRef, err error) {
	t.Helper()

	var refused *jira.CapabilityError
	if !errors.As(err, &refused) {
		t.Fatalf("got %T (%v), want a *jira.CapabilityError: a permission this token lacks is an answer", err, err)
	}
	if refused.Capability != jira.CapBulkMove {
		t.Errorf("the refusal names %q, want %q, so a view cannot tell which action to hide",
			refused.Capability, jira.CapBulkMove)
	}
	if refused.Reason == "" {
		t.Error("the refusal carries no reason, and an action hidden without one reads as a bug")
	}
	if ref != (jira.TaskRef{}) {
		t.Errorf("the refusal came back with %+v attached, want no ref at all", ref)
	}
}
