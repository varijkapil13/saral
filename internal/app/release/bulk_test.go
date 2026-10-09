package release

import (
	"context"
	"errors"
	"slices"
	"strconv"
	"sync"
	"testing"
	"time"

	"github.com/varijkapil13/saral/pkg/jira"
	"github.com/varijkapil13/saral/pkg/jira/jiratest"
)

func TestReadMatches_SplitsWhatWillChangeFromWhatAlreadyIs(t *testing.T) {
	t.Parallel()
	id := jiratest.VersionsFor("PROJ")[1].ID
	f := jiratest.New(jiratest.WithProject("PROJ", jiratest.Scrum), jiratest.WithIssues(openOn(id, 4)), jiratest.WithPageSize(5))
	add, err := ReadMatches(context.Background(), f, `project = "PROJ"`, id, false)
	if err != nil {
		t.Fatal(err)
	}
	if add.Skipped != 4 || len(add.Todo) != 20 {
		t.Errorf("an add matched %d to do and %d skipped, want 20 and 4", len(add.Todo), add.Skipped)
	}
	remove, err := ReadMatches(context.Background(), f, `project = "PROJ"`, id, true)
	if err != nil {
		t.Fatal(err)
	}
	if remove.Skipped != 20 || len(remove.Todo) != 4 {
		t.Errorf("a removal matched %d to do and %d skipped, want 4 and 20", len(remove.Todo), remove.Skipped)
	}
}

func TestReadMatches_RefusesAQueryWiderThanTheCap(t *testing.T) {
	t.Parallel()
	f := newFake(jiratest.Gen(Cap + 1))
	if _, err := ReadMatches(context.Background(), f, `project = "PROJ"`, "x", false); !errors.Is(err, ErrTooMany) {
		t.Errorf("got %v, want ErrTooMany", err)
	}
}

func TestReadMatches_PassesARefusalThrough(t *testing.T) {
	t.Parallel()
	for name, want := range refusals() {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			f := newFake(jiratest.Gen(4))
			f.FailNext(want)
			if _, err := ReadMatches(context.Background(), f, `project = "PROJ"`, "x", false); !errors.Is(err, want) {
				t.Errorf("got %v, want %v", err, want)
			}
		})
	}
}

func TestPatch_IsAnAddOrARemoveOfTheOneVersion(t *testing.T) {
	t.Parallel()
	if p := Patch("7", false); !slices.Equal(p.AddFixVersions, []string{"7"}) || len(p.RemoveFixVersions) != 0 {
		t.Errorf("an add is %+v", p)
	}
	if p := Patch("7", true); !slices.Equal(p.RemoveFixVersions, []string{"7"}) || len(p.AddFixVersions) != 0 {
		t.Errorf("a removal is %+v", p)
	}
}

type writes struct {
	mu     sync.Mutex
	sent   []string
	refuse func(key string) error
}

func (w *writes) CreateIssue(context.Context, jira.IssueInput) (jira.Issue, error) {
	return jira.Issue{}, errors.New("not here")
}

func (w *writes) UpdateIssue(_ context.Context, key string, _ jira.IssuePatch) error {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.sent = append(w.sent, key)
	if w.refuse != nil {
		return w.refuse(key)
	}
	return nil
}

func keys(n int) []string {
	out := make([]string, n)
	for i := range out {
		out[i] = "PROJ-" + strconv.Itoa(i+1)
	}
	return out
}

func drain(t *testing.T, ctx context.Context, a *Assignment) (last Progress, steps int) {
	t.Helper()
	for steps = 1; steps < 100; steps++ {
		if p := a.Next(ctx); p.Finished {
			return p, steps
		}
	}
	t.Fatal("the assignment never finished")
	return Progress{}, 0
}

func TestAssignment_WritesEveryIssueAChunkAtATime(t *testing.T) {
	t.Parallel()
	w := &writes{}
	p, steps := drain(t, context.Background(), NewAssignment(w, keys(2*Chunk+1), Patch("7", false)))
	if p.Done != 2*Chunk+1 || len(p.Failed) != 0 || len(p.Pending) != 0 {
		t.Errorf("progress %+v", p)
	}
	if steps != 3 {
		t.Errorf("took %d steps, want 3 chunks", steps)
	}
}

func TestAssignment_ARefusalIsThatIssuesAndTheRestStillGo(t *testing.T) {
	t.Parallel()
	w := &writes{refuse: func(key string) error {
		if key == "PROJ-2" {
			return &jira.CapabilityError{Reason: "you may not"}
		}
		return nil
	}}
	p, _ := drain(t, context.Background(), NewAssignment(w, keys(5), Patch("7", false)))
	if p.Done != 4 || len(p.Failed) != 1 || p.Failed[0].Key != "PROJ-2" || p.Failed[0].Reason == "" {
		t.Errorf("progress %+v", p)
	}
}

func TestAssignment_AChunkThatLandsNothingStopsTheRun(t *testing.T) {
	t.Parallel()
	for name, refusal := range refusals() {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			w := &writes{refuse: func(string) error { return refusal }}
			p, steps := drain(t, context.Background(), NewAssignment(w, keys(3*Chunk), Patch("7", false)))
			if steps != 1 || len(p.Failed) != Chunk || len(p.Pending) != 2*Chunk {
				t.Errorf("steps %d failed %d pending %d, want one chunk refused and the rest not sent",
					steps, len(p.Failed), len(p.Pending))
			}
			if len(w.sent) != Chunk {
				t.Errorf("%d writes reached the site, want %d", len(w.sent), Chunk)
			}
		})
	}
}

func TestAssignment_AnEndedContextStopsWhereItIs(t *testing.T) {
	t.Parallel()
	ctx, cancel := context.WithCancel(context.Background())
	w := &writes{refuse: func(key string) error {
		if key == "PROJ-3" {
			cancel()
			return context.Canceled
		}
		return nil
	}}
	p := NewAssignment(w, keys(Chunk+5), Patch("7", false)).Next(ctx)
	if !p.Finished || p.Done != 2 || len(p.Pending) != Chunk+3 || p.Pending[0] != "PROJ-3" {
		t.Errorf("progress done %d pending %d finished %v", p.Done, len(p.Pending), p.Finished)
	}
}

func TestAssignment_AgainstTheFakeAddsTheVersion(t *testing.T) {
	t.Parallel()
	id := jiratest.VersionsFor("PROJ")[2].ID
	f := newFake(jiratest.Gen(3))
	m, err := ReadMatches(context.Background(), f, `project = "PROJ"`, id, false)
	if err != nil {
		t.Fatal(err)
	}
	ks := make([]string, 0, len(m.Todo))
	for _, iss := range m.Todo {
		ks = append(ks, iss.Key)
	}
	p, _ := drain(t, context.Background(), NewAssignment(f, ks, Patch(id, false)))
	if p.Done != len(ks) {
		t.Fatalf("progress %+v", p)
	}
	again, err := ReadMatches(context.Background(), f, `project = "PROJ"`, id, false)
	if err != nil || len(again.Todo) != 0 {
		t.Errorf("after the run %d still lack the version, %v", len(again.Todo), err)
	}
}

func TestAssignment_ARateLimitedIssueIsReportedInItsOwnWords(t *testing.T) {
	t.Parallel()
	w := &writes{refuse: func(key string) error {
		if key == "PROJ-1" {
			return &jira.RateLimitError{RetryAfter: time.Minute}
		}
		return nil
	}}
	p, _ := drain(t, context.Background(), NewAssignment(w, keys(2), Patch("7", true)))
	if len(p.Failed) != 1 || p.Failed[0].Reason == "" {
		t.Errorf("failed %+v", p.Failed)
	}
}
