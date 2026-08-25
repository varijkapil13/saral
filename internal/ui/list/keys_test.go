package list

import (
	"fmt"
	"strings"
	"testing"

	"github.com/varijkapil13/saral/internal/ui/kernel"
)

// TestLiveKeys_EveryStateGolden holds every state the footer and the help
// overlay can be asked about. A state nothing covers is a state whose keys can
// change without anybody noticing, which is the drift this file exists to stop.
func TestLiveKeys_EveryStateGolden(t *testing.T) {
	t.Parallel()
	named := []struct {
		name  string
		state keyState
	}{
		{"browsing", keysBrowsing},
		{"a filter kept on the rows", keysNarrowed},
		{"filter open", keysFiltering},
		{"picking the number key", keysPickingSlot},
		{"confirming a number key that is taken", keysConfirmingSlot},
		{"editing the search on screen", keysAsking},
	}
	if len(named) != int(keyStates) {
		t.Fatalf("the list has %d key states and this test names %d", keyStates, len(named))
	}
	var b strings.Builder
	for _, s := range named {
		fmt.Fprintf(&b, "%s\n", s.name)
		writeKeySet(&b, liveSets[s.state])
	}
	golden(t, "keys.golden", b.String())
}

func TestLiveKeys_FollowWhatTheListIsDoing(t *testing.T) {
	t.Parallel()
	m := newModel(t)
	seen := map[int]string{}
	for _, tc := range []struct {
		name  string
		enter func()
		state keyState
	}{
		{"browsing", func() { m.filtering, m.bind, m.query = false, bindNone, "" }, keysBrowsing},
		{"a filter kept on the rows", func() { m.query = "login" }, keysNarrowed},
		{"filter open", func() { m.filtering = true }, keysFiltering},
		{"picking a key", func() { m.filtering, m.bind = false, bindPick }, keysPickingSlot},
		{"confirming a key", func() { m.bind = bindConfirm }, keysConfirmingSlot},
		{"editing the search", func() { m.bind, m.asking = bindNone, true }, keysAsking},
	} {
		tc.enter()
		set, gen := m.LiveKeys()
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

// The filter is opened with the key rather than by setting the field, so that
// what the footer says is held against what the dispatcher actually does.
func TestLiveKeys_TheFilterKeyChangesWhatIsAdvertised(t *testing.T) {
	t.Parallel()
	m := newModel(t)
	before, _ := m.LiveKeys()
	m.key(keyPress("/"))
	after, gen := m.LiveKeys()
	if gen != int(keysFiltering) {
		t.Fatalf("pressing / left the keys in state %d", gen)
	}
	if actsOf(before) == actsOf(after) {
		t.Errorf("the same keys are advertised with the filter open and closed: %s", actsOf(after))
	}
	if !strings.Contains(actsOf(after), "clear filter") {
		t.Errorf("an open filter does not advertise the key that closes it: %s", actsOf(after))
	}
}

// AllocsPerRun measures the whole process, so this one cannot run beside
// anything else.
func TestLiveKeys_CostNothingToAskFor(t *testing.T) {
	m := newModel(t)
	if got := testing.AllocsPerRun(100, func() { _, _ = m.LiveKeys() }); got != 0 {
		t.Errorf("asking for the live keys allocates %.0f times; chromeFor asks on every frame, so the sets must be stored", got)
	}
}

func newModel(t *testing.T) *Model {
	t.Helper()
	m, ok := New(testDeps(nil)).(*Model)
	if !ok {
		t.Fatal("New no longer builds a *Model")
	}
	return m
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
