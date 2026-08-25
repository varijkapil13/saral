package palette

import (
	"fmt"
	"strings"
	"testing"

	"github.com/varijkapil13/saral/internal/ui/kernel"
)

// TestLiveKeys_EveryStateGolden holds every state the footer and the ? overlay
// can be asked about. A state nothing covers is a state whose keys can change
// without anybody noticing.
func TestLiveKeys_EveryStateGolden(t *testing.T) {
	t.Parallel()

	named := []struct {
		name  string
		state keyState
	}{
		{"something to run", keysOffering},
		{"a cached issue under the cursor", keysIssue},
		{"nothing matches", keysNothing},
	}
	if len(named) != int(keyStates) {
		t.Fatalf("the palette has %d key states and this test names %d", keyStates, len(named))
	}
	var b strings.Builder
	for _, s := range named {
		fmt.Fprintf(&b, "%s\n", s.name)
		writeKeySet(&b, liveSets[s.state])
	}
	golden(t, "keys.golden", b.String())
}

func TestLiveKeys_FollowWhetherThereIsAnythingToRun(t *testing.T) {
	t.Parallel()

	p := fly(t, paletteDeps(), sample(), memoryTable(), 120, 24)
	set, gen := p.m.LiveKeys()
	if gen != int(keysOffering) {
		t.Fatalf("a freshly opened palette is in key state %d", gen)
	}
	if set.IsZero() {
		t.Fatal("a freshly opened palette advertises nothing")
	}

	p.typeText("zzzz")
	empty, emptyGen := p.m.LiveKeys()
	if emptyGen == gen {
		t.Errorf("both states report generation %d, so the footer will not repaint between them", gen)
	}
	if actsOf(empty) == actsOf(set) {
		t.Errorf("the same keys are advertised with and without a match: %s", actsOf(empty))
	}
	if !strings.Contains(actsOf(empty), "close") {
		t.Errorf("nothing matching leaves no key advertised at all: %s", actsOf(empty))
	}
}

// AllocsPerRun measures the whole process, so this one cannot run beside
// anything else.
func TestLiveKeys_CostNothingToAskFor(t *testing.T) {
	p := fly(t, paletteDeps(), sample(), memoryTable(), 120, 24)
	if got := testing.AllocsPerRun(100, func() { _, _ = p.m.LiveKeys() }); got != 0 {
		t.Errorf("asking for the live keys allocates %.0f times; the chrome asks on every frame, so the sets must be stored", got)
	}
}

// Every key the palette answers to has to be one of its bindings, or the footer
// is advertising one thing and the dispatcher doing another.
func TestKeys_TheDispatcherAnswersExactlyWhatTheBindingsSay(t *testing.T) {
	t.Parallel()

	keys := defaultKeys()
	acts := keys.table()
	for _, b := range []kernel.Binding{keys.Up, keys.Down, keys.PageUp, keys.PageDown, keys.Run, keys.Open, keys.Close} {
		for _, stroke := range b.Keys() {
			if acts[stroke] == actNone {
				t.Errorf("%q is bound to %q and the dispatcher does nothing with it", stroke, b.Help().Desc)
			}
		}
	}
	for _, letter := range []string{"j", "k", "q", "r", "1", "g"} {
		if acts[letter] != actNone {
			t.Errorf("%q is a keystroke the palette takes for itself, so nobody can type it into the filter", letter)
		}
	}
}

func writeKeySet(b *strings.Builder, set kernel.KeySet) {
	fmt.Fprintf(b, "  acts   %s\n", actsOf(set))
	for _, column := range set.Full {
		labels := make([]string, 0, len(column))
		for _, binding := range column {
			labels = append(labels, binding.Help().Key+" "+binding.Help().Desc)
		}
		fmt.Fprintf(b, "  full   [%s]\n", strings.Join(labels, ", "))
	}
}
