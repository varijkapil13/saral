package release

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
		{"reading the versions", keysBrowsing},
		{"counting what is open on one", keysCounting},
		{"typing a version", keysEditing},
		{"a save in flight", keysSaving},
	}
	if len(named) != int(keyStates) {
		t.Fatalf("the list has %d key states and this test names %d", keyStates, len(named))
	}
	var b strings.Builder
	for _, s := range named {
		fmt.Fprintf(&b, "%s\n", s.name)
		writeKeySet(&b, liveSets[s.state])
	}
	golden(t, "list_keys.golden", b.String())
}

// The states are reached with real keys, so what the footer says is held against
// what the dispatcher actually does.
func TestLiveKeys_FollowWhatTheListIsDoing(t *testing.T) {
	t.Parallel()

	dr := listOf(t, testDeps(newFake(8)), 120, 24)
	seen := map[int]string{}
	for _, tc := range []struct {
		name  string
		enter func()
		state keyState
		acts  bool
	}{
		{name: "reading the versions", enter: func() {}, state: keysBrowsing, acts: true},
		{name: "typing a version", enter: func() { dr.key("n") }, state: keysEditing, acts: true},
		{
			name:  "a save in flight",
			enter: func() { dr.list().saving = true },
			state: keysSaving,
		},
		{
			name: "counting what is open on one",
			enter: func() {
				m := dr.list()
				m.saving, m.mode, m.counting = false, browsing, twoOh
			},
			state: keysCounting,
			acts:  true,
		},
	} {
		tc.enter()
		set, gen := dr.list().LiveKeys()
		if gen != int(tc.state) {
			t.Errorf("%s: generation %d, want %d", tc.name, gen, tc.state)
		}
		if other, clash := seen[gen]; clash {
			t.Errorf("%s and %s share generation %d, so the footer will not repaint between them",
				tc.name, other, gen)
		}
		seen[gen] = tc.name
		if got := len(set.Acts) > 0; got != tc.acts {
			t.Errorf("%s advertises %d actions, want any = %v", tc.name, len(set.Acts), tc.acts)
		}
	}
}

// While the count is being read, releasing is the one thing that cannot be
// done — the count is what the decision is made against — and everything else
// still can.
func TestLiveKeys_CountingDropsTheReleaseAndKeepsTheRest(t *testing.T) {
	t.Parallel()

	browsing := actsOf(liveSets[keysBrowsing])
	counting := actsOf(liveSets[keysCounting])
	if !strings.Contains(browsing, "enter") {
		t.Fatalf("the resting row does not offer a release at all: %s", browsing)
	}
	if strings.Contains(counting, "enter") {
		t.Errorf("the row offers a release while the count is still being read: %s", counting)
	}
	for _, want := range []string{"n", "e", "A"} {
		if !strings.Contains(counting, want) {
			t.Errorf("the row gave up %q while counting, which still works: %s", want, counting)
		}
	}
}

func TestLiveKeys_OpeningTheEditorChangesWhatIsAdvertised(t *testing.T) {
	t.Parallel()

	dr := listOf(t, testDeps(newFake(8)), 120, 24)
	before, _ := dr.list().LiveKeys()
	dr.key("n")
	after, gen := dr.list().LiveKeys()

	if gen != int(keysEditing) {
		t.Fatalf("opening the editor left the keys in state %d", gen)
	}
	if actsOf(before) == actsOf(after) {
		t.Errorf("the same keys are advertised on both screens: %s", actsOf(after))
	}
	if !strings.Contains(actsOf(after), "esc") {
		t.Errorf("the editor does not advertise the way out, and it swallows esc: %s", actsOf(after))
	}
}

// g reaches only this view's own g g. Everything else on it would advertise a
// stroke the kernel buffers and never delivers on its own.
func TestKeys_TheOnlyThingBoundToTheViewSwitchPrefixIsItsOwnGesture(t *testing.T) {
	t.Parallel()

	browsing, editor := defaultKeys().tables()
	if got := browsing["g"]; got != actGo {
		t.Errorf("g in the list dispatches %d, want the go-to prefix", got)
	}
	if _, bound := editor["g"]; bound {
		t.Error("the editor binds g, where it is a letter somebody is typing")
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
		t.Errorf("asking the list for its live keys allocates %.0f times; the chrome asks on every "+
			"frame, so the sets must be stored", got)
	}
	f, ok := NewFlow(testDeps(nil), twoOhVersion(), 3, nil).(*Flow)
	if !ok {
		t.Fatal("NewFlow no longer builds a *Flow")
	}
	if got := testing.AllocsPerRun(100, func() { _, _ = f.LiveKeys() }); got != 0 {
		t.Errorf("asking the flow for its live keys allocates %.0f times", got)
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
