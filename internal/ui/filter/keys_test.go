package filter

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
		{"choosing what to filter by", keysFacets},
		{"typing for a value", keysValues},
		{"nothing on offer", keysNothing},
	}
	if len(named) != int(keyStates) {
		t.Fatalf("the picker has %d key states and this test names %d", keyStates, len(named))
	}
	var b strings.Builder
	for _, s := range named {
		fmt.Fprintf(&b, "%s\n", s.name)
		writeKeySet(&b, liveSets[s.state])
	}
	golden(t, "keys.golden", b.String())
}

func TestLiveKeys_FollowWhatThePickerIsDoing(t *testing.T) {
	t.Parallel()

	dr := newDriver(t, testDeps(newFake(20)), 120, 30)
	seen := map[int]string{}
	for _, tc := range []struct {
		name  string
		enter func()
		state keyState
	}{
		{"choosing what to filter by", func() {}, keysFacets},
		{"typing for a value", func() { dr.pick(FacetPriority) }, keysValues},
		{"nothing on offer", func() { dr.typeText("nothing like this") }, keysNothing},
	} {
		tc.enter()
		set, gen := dr.m.LiveKeys()
		if gen != int(tc.state) {
			t.Errorf("%s: generation %d, want %d", tc.name, gen, tc.state)
		}
		if other, clash := seen[gen]; clash {
			t.Errorf("%s and %s share generation %d, so the footer will not repaint between them",
				tc.name, other, gen)
		}
		seen[gen] = tc.name
		if len(set.Acts) == 0 {
			t.Errorf("%s advertises nothing at all", tc.name)
		}
	}
}

// The facets are opened with the key rather than by setting the field, so that
// what the footer says is held against what the dispatcher actually does.
func TestLiveKeys_OpeningAFacetChangesWhatIsAdvertised(t *testing.T) {
	t.Parallel()

	dr := newDriver(t, testDeps(newFake(20)), 120, 30)
	before, _ := dr.m.LiveKeys()
	dr.pick(FacetPriority)
	after, gen := dr.m.LiveKeys()

	if gen != int(keysValues) {
		t.Fatalf("opening a facet left the keys in state %d", gen)
	}
	if actsOf(before) == actsOf(after) {
		t.Errorf("the same keys are advertised on both screens: %s", actsOf(after))
	}
	if !strings.Contains(actsOf(after), "esc") {
		t.Errorf("typing for a value does not advertise the way back, and it swallows esc: %s", actsOf(after))
	}
}

// g reaches nothing here: the kernel buffers it as the view-switch prefix and
// never forwards it, so a binding on it would name a stroke that cannot arrive.
func TestKeys_NothingIsBoundToTheViewSwitchPrefix(t *testing.T) {
	t.Parallel()

	facets, values := defaultKeys().tables()
	for name, table := range map[string]map[string]action{"the facets": facets, "the values": values} {
		if _, bound := table["g"]; bound {
			t.Errorf("%s bind g, which the kernel buffers and never delivers", name)
		}
	}
}

// AllocsPerRun measures the whole process, so this one cannot run beside
// anything else.
func TestLiveKeys_CostNothingToAskFor(t *testing.T) {
	m, ok := New(testDeps(nil)).(*Model)
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
