package plan

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
		{"a plan under the cursor", keysClosed},
		{"the plan under the cursor already open", keysOpen},
		{"no plan to be on", keysNothing},
	}
	if len(named) != int(keyStates) {
		t.Fatalf("the view has %d key states and this test names %d", keyStates, len(named))
	}
	var b strings.Builder
	for _, s := range named {
		fmt.Fprintf(&b, "%s\n", s.name)
		writeKeySet(&b, liveSets[s.state])
	}
	golden(t, "keys.golden", b.String())
}

func TestLiveKeys_FollowWhatTheViewIsDoing(t *testing.T) {
	t.Parallel()

	empty := newDriver(t, testDeps(nil), 120, 20, WithDefined(nil))
	held := newDriver(t, refusedDeps(newFake(5)), 120, 20, WithDefined(defined()))

	seen := map[int]string{}
	for _, tc := range []struct {
		name  string
		enter func() *driver
		state keyState
		acts  bool
	}{
		{"no plan to be on", func() *driver { return empty }, keysNothing, false},
		{"a plan under the cursor", func() *driver { return held }, keysClosed, true},
		{"the plan under the cursor already open", func() *driver {
			held.key("enter")
			return held
		}, keysOpen, true},
	} {
		set, gen := tc.enter().m.LiveKeys()
		if gen != int(tc.state) {
			t.Errorf("%s: generation %d, want %d", tc.name, gen, tc.state)
		}
		if other, clash := seen[gen]; clash {
			t.Errorf("%s and %s share generation %d, so the footer will not repaint between them",
				tc.name, other, gen)
		}
		seen[gen] = tc.name
		if got := len(set.Acts) > 0; got != tc.acts {
			t.Errorf("%s advertises %d actions, want acts=%v", tc.name, len(set.Acts), tc.acts)
		}
	}
}

// Opening a plan changes what enter is called, because it is the same stroke
// doing the opposite thing.
func TestLiveKeys_OpeningAPlanChangesWhatIsAdvertised(t *testing.T) {
	t.Parallel()

	dr := newDriver(t, refusedDeps(newFake(5)), 120, 20, WithDefined(defined()))
	before, _ := dr.m.LiveKeys()
	dr.key("enter")
	after, gen := dr.m.LiveKeys()

	if gen != int(keysOpen) {
		t.Fatalf("opening a plan left the keys in state %d", gen)
	}
	if actsOf(before) == actsOf(after) {
		t.Errorf("the same action is advertised open and closed: %s", actsOf(after))
	}
}

// The resting set is what the footer falls back on, and a set naming no action
// leaves the row as a line of globals.
func TestKeys_TheRegisteredRestingSetNamesAnAction(t *testing.T) {
	t.Parallel()

	if got := kernel.KeysFor(ViewID).Acts; len(got) == 0 {
		t.Fatal("the registered keys name nothing that can be done")
	}
	for _, act := range kernel.KeysFor(ViewID).Acts {
		if _, spellable := kernel.Stroke(act); !spellable {
			t.Errorf("%q cannot be spelt back into a keypress, so clicking it in the footer does nothing",
				act.Help().Key)
		}
	}
}

// g reaches nothing here: the kernel buffers it as the view-switch prefix and
// never forwards it, so a binding on it would name a stroke that cannot arrive.
func TestKeys_NothingIsBoundToTheViewSwitchPrefix(t *testing.T) {
	t.Parallel()

	if _, bound := defaultKeys().table()["g"]; bound {
		t.Error("the view binds g, which the kernel buffers and never delivers")
	}
}

// AllocsPerRun measures the whole process, so this one cannot run beside
// anything else.
func TestLiveKeys_CostNothingToAskFor(t *testing.T) {
	m, ok := New(testDeps(nil), WithDefined(defined())).(*Model)
	if !ok {
		t.Fatal("New no longer builds a *Model")
	}
	if got := testing.AllocsPerRun(100, func() { _, _ = m.LiveKeys() }); got != 0 {
		t.Errorf("asking for the live keys allocates %.0f times; the chrome asks on every frame, "+
			"so the sets must be stored", got)
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
	fmt.Fprintf(b, "  acts   %s\n", actsOf(set))
	for _, column := range set.Full {
		fmt.Fprintf(b, "  full   [%s]\n", strings.Join(labels(column), ", "))
	}
}
