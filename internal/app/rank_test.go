package app

import (
	"context"
	"errors"
	"slices"
	"testing"

	"github.com/varijkapil13/saral/pkg/jira"
)

type rankerFunc func(ctx context.Context, keys []string, at jira.RankPosition) error

func (f rankerFunc) RankIssues(ctx context.Context, keys []string, at jira.RankPosition) error {
	return f(ctx, keys, at)
}

func answering(err error) jira.Ranker {
	return rankerFunc(func(context.Context, []string, jira.RankPosition) error { return err })
}

func TestRanking_StepIsFreshThenQueuedAndRefusesAnotherKey(t *testing.T) {
	t.Parallel()
	var r Ranking[int]
	if got := r.Key(); got != "" {
		t.Fatalf("a new ranking has %q in flight", got)
	}
	if got := r.Step("PROJ-1", "PROJ-2", 1); got != RankFresh {
		t.Fatalf("the first step is %v, want fresh", got)
	}
	if got := r.Step("PROJ-1", "PROJ-9", 9); got != RankQueued {
		t.Errorf("a second step for the same key is %v, want queued", got)
	}
	if got := r.Step("PROJ-3", "", 3); got != RankBusy {
		t.Errorf("a step for another key while one is out is %v, want busy", got)
	}
	if got := r.Key(); got != "PROJ-1" {
		t.Errorf("in flight is %q, want PROJ-1", got)
	}
}

func TestRanking_DoneNamesTheAnchorAndClears(t *testing.T) {
	t.Parallel()
	var r Ranking[int]
	var asked []string
	var pos jira.RankPosition
	ranker := rankerFunc(func(_ context.Context, keys []string, at jira.RankPosition) error {
		asked, pos = keys, at
		return nil
	})
	r.Step("PROJ-1", "PROJ-2", 1)
	run, cancel := r.Send(ranker, jira.RankAfter("PROJ-5"), "PROJ-6", 2)
	defer cancel()
	res := r.Answer(run())
	if !slices.Equal(asked, []string{"PROJ-1"}) || pos.After != "PROJ-5" {
		t.Errorf("the site was asked to rank %v at %+v", asked, pos)
	}
	want := RankResult[int]{Outcome: RankDone, Key: "PROJ-1", Anchor: "PROJ-5", After: true}
	if res != want {
		t.Errorf("answer is %+v, want %+v", res, want)
	}
	if r.Key() != "" {
		t.Error("a finished rank is still in flight")
	}
}

func TestRanking_ADirtyStepResendsAndPromotesWhatWasSent(t *testing.T) {
	t.Parallel()
	var r Ranking[int]
	r.Step("PROJ-1", "PROJ-2", 1)
	run, _ := r.Send(answering(nil), jira.RankBefore("PROJ-4"), "PROJ-4", 2)
	r.Step("PROJ-1", "", 0)
	if res := r.Answer(run()); res.Outcome != RankResend || res.Key != "PROJ-1" {
		t.Fatalf("a dirty step answered %+v, want a resend", res)
	}
	run, _ = r.Send(answering(errors.New("refused")), jira.RankBefore("PROJ-7"), "PROJ-7", 3)
	res := r.Answer(run())
	want := RankResult[int]{Outcome: RankRefused, Key: "PROJ-1", Next: "PROJ-4", Snap: 2}
	if res != want {
		t.Errorf("a refusal after an accepted step is %+v, want it to restore what that step sent: %+v", res, want)
	}
}

func TestRanking_ARefusalRestoresTheReadAndClears(t *testing.T) {
	t.Parallel()
	var r Ranking[string]
	r.Step("PROJ-1", "PROJ-2", "was")
	run, _ := r.Send(answering(&jira.TransportError{Op: "PUT", Err: errors.New("reset")}), jira.RankBefore("PROJ-9"), "PROJ-9", "now")
	res := r.Answer(run())
	want := RankResult[string]{Outcome: RankRefused, Key: "PROJ-1", Next: "PROJ-2", Snap: "was"}
	if res != want {
		t.Errorf("answer is %+v, want %+v", res, want)
	}
	if r.Key() != "" {
		t.Error("a refused rank is still in flight")
	}
}

func TestRanking_APartialAnswerNamingTheKeyIsSuccess(t *testing.T) {
	t.Parallel()
	for name, tc := range map[string]struct {
		ranked []string
		want   RankOutcome
	}{
		"named among the ranked": {ranked: []string{"PROJ-1"}, want: RankDone},
		"not named":              {ranked: []string{"PROJ-8"}, want: RankRefused},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			var r Ranking[int]
			r.Step("PROJ-1", "", 0)
			run, _ := r.Send(answering(&jira.PartialRankError{Ranked: tc.ranked}), jira.RankBefore("PROJ-2"), "PROJ-2", 0)
			if got := r.Answer(run()).Outcome; got != tc.want {
				t.Errorf("outcome %v, want %v", got, tc.want)
			}
		})
	}
}

func TestRanking_AStaleAnswerIsIgnored(t *testing.T) {
	t.Parallel()
	var r Ranking[int]
	r.Step("PROJ-1", "PROJ-2", 1)
	first, _ := r.Send(answering(errors.New("refused")), jira.RankBefore("PROJ-3"), "PROJ-3", 2)
	second, _ := r.Send(answering(nil), jira.RankBefore("PROJ-4"), "PROJ-4", 3)
	if res := r.Answer(first()); res.Outcome != RankStale {
		t.Errorf("an answer to a superseded send is %+v, want stale", res)
	}
	if r.Key() != "PROJ-1" {
		t.Fatal("a stale answer cleared the rank in flight")
	}
	if res := r.Answer(RankAnswer{Gen: 99, Key: "PROJ-1"}); res.Outcome != RankStale {
		t.Errorf("an answer with an unknown generation is %+v, want stale", res)
	}
	if res := r.Answer(second()); res.Outcome != RankDone {
		t.Errorf("the current answer is %+v, want done", res)
	}
}

func TestRanking_ASendIsCancelledByTheNextAndByDrop(t *testing.T) {
	t.Parallel()
	var r Ranking[int]
	var ctxs []context.Context
	ranker := rankerFunc(func(ctx context.Context, _ []string, _ jira.RankPosition) error {
		ctxs = append(ctxs, ctx)
		return nil
	})
	r.Step("PROJ-1", "", 0)
	first, _ := r.Send(ranker, jira.RankBefore("PROJ-2"), "", 0)
	second, _ := r.Send(ranker, jira.RankBefore("PROJ-2"), "", 0)
	first()
	if ctxs[0].Err() == nil {
		t.Error("a second send left the first one running")
	}
	r.Drop()
	answer := second()
	if ctxs[1].Err() == nil {
		t.Error("drop left the send running")
	}
	if r.Key() != "" {
		t.Error("drop kept the rank in flight")
	}
	if res := r.Answer(answer); res.Outcome != RankStale {
		t.Errorf("an answer after drop is %+v, want stale", res)
	}
	if run, cancel := r.Send(ranker, jira.RankBefore("PROJ-2"), "", 0); run != nil || cancel != nil {
		t.Error("a send with nothing in flight made a request")
	}
}

func TestRankBeside_PrefersTheNextThenThePrevious(t *testing.T) {
	t.Parallel()
	if at, ok := RankBeside("PROJ-1", "PROJ-3", "rank"); !ok || at != (jira.RankPosition{Before: "PROJ-3", FieldID: "rank"}) {
		t.Errorf("between two is %+v %v", at, ok)
	}
	if at, ok := RankBeside("PROJ-1", "", "rank"); !ok || at != (jira.RankPosition{After: "PROJ-1", FieldID: "rank"}) {
		t.Errorf("last is %+v %v", at, ok)
	}
	if _, ok := RankBeside("", "", "rank"); ok {
		t.Error("an issue alone in its column has a position to send")
	}
}

func issuesOf(keys ...string) []jira.Issue {
	out := make([]jira.Issue, len(keys))
	for i, k := range keys {
		out[i] = jira.Issue{Key: k}
	}
	return out
}

func TestShiftIssueAndPutBack(t *testing.T) {
	t.Parallel()
	for name, tc := range map[string]struct {
		got  []jira.Issue
		want []string
	}{
		"before an anchor":       {ShiftIssue(issuesOf("A", "B", "C", "D"), 3, "B", false), []string{"A", "D", "B", "C"}},
		"after an anchor":        {ShiftIssue(issuesOf("A", "B", "C", "D"), 0, "C", true), []string{"B", "C", "A", "D"}},
		"an anchor that is gone": {ShiftIssue(issuesOf("A", "B", "C"), 1, "Z", true), []string{"A", "B", "C"}},
		"back before next":       {PutBack(issuesOf("A", "B", "C", "D"), 0, "D"), []string{"B", "C", "A", "D"}},
		"back with next gone":    {PutBack(issuesOf("A", "B", "C"), 0, "Z"), []string{"B", "C", "A"}},
		"back with no next":      {PutBack(issuesOf("A", "B", "C"), 1, ""), []string{"A", "C", "B"}},
	} {
		if got := keysOf(tc.got); !slices.Equal(got, tc.want) {
			t.Errorf("%s: %v, want %v", name, got, tc.want)
		}
	}
}
