package onboarding

import (
	"fmt"
	"strings"
	"testing"

	"github.com/varijkapil13/saral/internal/ui/kernel"
)

// TestLiveKeys_EveryStateGolden holds every state the footer and the help
// overlay can be asked about. A state nothing covers is a state whose keys can
// change without anybody noticing.
func TestLiveKeys_EveryStateGolden(t *testing.T) {
	t.Parallel()
	named := []struct {
		name  string
		state keyState
	}{
		{"the first step", keysFirstStep},
		{"the first step, after it failed", keysFirstStepFailed},
		{"a step with something to type", keysTyping},
		{"a step with something to choose", keysChoosing},
		{"the review", keysReview},
		{"the summary", keysDone},
		{"after a step failed", keysFailed},
		{"waiting on the site", keysBusy},
	}
	if len(named) != int(keyStates) {
		t.Fatalf("the flow has %d key states and this test names %d", keyStates, len(named))
	}
	var b strings.Builder
	for _, s := range named {
		fmt.Fprintf(&b, "%s\n", s.name)
		writeKeySet(&b, liveSets[s.state])
	}
	golden(t, "keys.golden", b.String())
}

func TestLiveKeys_FollowTheStepTheUserIsOn(t *testing.T) {
	t.Parallel()
	base, ok := NewWith(testDeps(), nil).(Model)
	if !ok {
		t.Fatal("NewWith no longer builds a Model")
	}
	seen := map[int]keyState{}
	for _, tc := range []struct {
		name  string
		enter func(Model) Model
		state keyState
	}{
		{"the site", func(m Model) Model { return m }, keysFirstStep},
		{"the site, unreachable", func(m Model) Model { m.problem = "no such host"; return m }, keysFirstStepFailed},
		{"the email", func(m Model) Model { m.step = stepEmail; return m }, keysTyping},
		{"the token store", func(m Model) Model { m.step = stepStorage; return m }, keysChoosing},
		{"a project with suggestions", func(m Model) Model {
			m.step, m.suggested = stepProject, []string{"PROJ"}
			return m
		}, keysChoosing},
		{"a project with none", func(m Model) Model { m.step = stepProject; return m }, keysTyping},
		{"the review", func(m Model) Model { m.step = stepReview; return m }, keysReview},
		{"the summary", func(m Model) Model { m.step = stepDone; return m }, keysDone},
		{"the token refused", func(m Model) Model {
			m.step, m.problem = stepToken, "that token is not for this account"
			return m
		}, keysFailed},
		{"asking the site", func(m Model) Model { m.step, m.busy = stepToken, busyConnect; return m }, keysBusy},
	} {
		set, gen := tc.enter(base).LiveKeys()
		if gen != int(tc.state) {
			t.Errorf("%s: generation %d, want %d", tc.name, gen, tc.state)
		}
		if other, clash := seen[gen]; clash && other != tc.state {
			t.Errorf("%s reports generation %d, which belongs to %d", tc.name, gen, other)
		}
		seen[gen] = tc.state
		if tc.state == keysBusy && !set.IsZero() {
			t.Errorf("a step waiting on the site advertises %s, none of which answers", actsOf(set))
		}
	}
}

// The first step has nowhere to go back to — back() returns nothing there — so
// naming the key would be advertising a stroke that does nothing at the one
// moment a first-time user is most likely to try it.
func TestLiveKeys_TheFirstStepDoesNotOfferAWayBack(t *testing.T) {
	t.Parallel()
	for _, state := range []keyState{keysFirstStep, keysFirstStepFailed} {
		if got := actsOf(liveSets[state]); strings.Contains(got, "back") {
			t.Errorf("the first step offers a way back: %s", got)
		}
	}
	if got := actsOf(liveSets[keysTyping]); !strings.Contains(got, "back") {
		t.Errorf("a later step lost its way back: %s", got)
	}
}

// enter does three different things across the flow, and the last two are the
// ones a user has never seen before.
func TestLiveKeys_EnterIsNamedForWhatItDoesOnThisStep(t *testing.T) {
	t.Parallel()
	for state, want := range map[keyState]string{
		keysTyping: "continue",
		keysReview: "write the profile",
		keysDone:   "start using it",
	} {
		if got := actsOf(liveSets[state]); !strings.Contains(got, want) {
			t.Errorf("state %d does not say enter %q: %s", state, want, got)
		}
	}
}

// AllocsPerRun measures the whole process, so this one cannot run beside
// anything else.
func TestLiveKeys_CostNothingToAskFor(t *testing.T) {
	m, ok := NewWith(testDeps(), nil).(Model)
	if !ok {
		t.Fatal("NewWith no longer builds a Model")
	}
	if got := testing.AllocsPerRun(100, func() { _, _ = m.LiveKeys() }); got != 0 {
		t.Errorf("asking for the live keys allocates %.0f times; chromeFor asks on every frame, so the sets must be stored", got)
	}
}

func actsOf(set kernel.KeySet) string {
	return strings.Join(labels(set.Acts), " · ")
}

func labels(bindings []kernel.Binding) []string {
	out := make([]string, 0, len(bindings))
	for _, b := range bindings {
		out = append(out, b.Help().Key+" "+b.Help().Desc)
	}
	return out
}

func writeKeySet(b *strings.Builder, set kernel.KeySet) {
	if set.IsZero() {
		b.WriteString("  nothing of its own; the globals are all that answer\n")
		return
	}
	fmt.Fprintf(b, "  acts   %s\n", actsOf(set))
	for _, column := range set.Full {
		fmt.Fprintf(b, "  full   [%s]\n", strings.Join(labels(column), ", "))
	}
}
