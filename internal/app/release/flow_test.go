package release

import (
	"context"
	"errors"
	"slices"
	"testing"

	"github.com/varijkapil13/saral/pkg/jira"
	"github.com/varijkapil13/saral/pkg/jira/jiratest"
)

// openOn is an issue set where exactly n issues carry a version and none of
// them is done.
func openOn(id string, n int) []jira.Issue {
	issues := jiratest.Gen(24)
	put := 0
	for i := range issues {
		issues[i].FixVersions = nil
		if put == n || issues[i].Status.Category == jira.CategoryDone {
			continue
		}
		issues[i].FixVersions = []jira.Version{{ID: id}}
		put++
	}
	return issues
}

func TestRelease_AMoveWithNowhereToGoIsRefusedBeforeTheSite(t *testing.T) {
	t.Parallel()
	f := newFake(nil)
	id := jiratest.VersionsFor("PROJ")[1].ID
	if _, err := Release(context.Background(), f, id, jira.MoveUnresolved, "", 2); !errors.Is(err, ErrNoTarget) {
		t.Fatalf("got %v, want ErrNoTarget", err)
	}
	if slices.Contains(f.Calls(), "ReleaseVersion") {
		t.Error("a move with no target reached the site")
	}
}

func TestRelease_ReadsTheAnswerAgainstTheDecision(t *testing.T) {
	t.Parallel()
	seeded := jiratest.VersionsFor("PROJ")
	from, to := seeded[1].ID, seeded[2].ID
	for name, tc := range map[string]struct {
		policy     jira.UnresolvedPolicy
		target     string
		left       int
		unfinished bool
	}{
		"anyway leaves them and is finished": {policy: jira.ReleaseAnyway, left: 3},
		"a move takes them along":            {policy: jira.MoveUnresolved, target: to},
		"a strip takes the version off":      {policy: jira.StripUnresolved},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			f := newFake(openOn(from, 3))
			out, err := Release(context.Background(), f, from, tc.policy, tc.target, 3)
			if err != nil {
				t.Fatal(err)
			}
			if !out.Released() || out.Policy != tc.policy || out.Asked != 3 {
				t.Errorf("outcome %+v", out)
			}
			if out.Left() != tc.left || out.Unfinished() != tc.unfinished {
				t.Errorf("left %d unfinished %v, want %d %v", out.Left(), out.Unfinished(), tc.left, tc.unfinished)
			}
		})
	}
}

func TestOutcome_ASweepThatStoppedPartWayIsUnfinished(t *testing.T) {
	t.Parallel()
	two := 2
	out := Outcome{Version: jira.Version{Released: true, Unresolved: &two}, Policy: jira.StripUnresolved, Asked: 5}
	if !out.Unfinished() || out.Left() != 2 {
		t.Errorf("left %d unfinished %v, want a strip that left two", out.Left(), out.Unfinished())
	}
	if (Outcome{}).Released() {
		t.Error("an empty answer says released")
	}
}
