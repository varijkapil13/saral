package timeline

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
		{"reading the chart", keysBrowsing},
		{"nothing to chart", keysNothing},
		{"the notes pane", keysNotes},
	}
	if len(named) != int(keyStates) {
		t.Fatalf("the chart has %d key states and this test names %d", keyStates, len(named))
	}
	var b strings.Builder
	for _, s := range named {
		fmt.Fprintf(&b, "%s\n", s.name)
		writeKeySet(&b, liveSets[s.state])
	}
	golden(t, "keys.golden", b.String())
}

// The set the footer shows has to follow what the chart is doing, and the
// generation has to move with it or the memoized chrome is right on the first
// frame and stale forever.
func TestLiveKeys_FollowWhatTheChartIsDoing(t *testing.T) {
	t.Parallel()

	dr := newDriver(t, testDeps(newFake(10)), 120, 20)
	browsing, browsingGen := dr.m.LiveKeys()
	if len(browsing.Acts) == 0 {
		t.Error("the chart advertises nothing while it is being read")
	}

	dr.key("n")
	notes, notesGen := dr.m.LiveKeys()
	if notesGen == browsingGen {
		t.Errorf("opening the notes left the generation at %d, so the footer cannot know to repaint", notesGen)
	}
	if len(notes.Acts) == 0 {
		t.Error("the notes pane advertises nothing")
	}
	if actsOf(notes) == actsOf(browsing) {
		t.Errorf("the notes pane advertises the same actions as the chart: %s", actsOf(notes))
	}

	dr.key("n")
	if _, gen := dr.m.LiveKeys(); gen != browsingGen {
		t.Errorf("closing the notes left the generation at %d, want %d", gen, browsingGen)
	}

	empty := newDriver(t, testDeps(customFake(nil)), 120, 20)
	nothing, nothingGen := empty.m.LiveKeys()
	if nothingGen == browsingGen {
		t.Error("an empty chart reports the same generation as one with bars in it")
	}
	if len(nothing.Acts) == 0 {
		t.Error("an empty chart advertises nothing, which is a footer of globals and no way to ask why")
	}
}

// AllocsPerRun measures the whole process, so this one cannot run beside
// anything else.
func TestLiveKeys_CostNothingToAskFor(t *testing.T) {
	m, ok := New(testDeps(nil)).(*Model)
	if !ok {
		t.Fatal("New did not return a *Model")
	}
	if got := testing.AllocsPerRun(100, func() { _, _ = m.LiveKeys() }); got != 0 {
		t.Errorf("asking for the live keys allocates %.0f times; the chrome asks on every frame, "+
			"so the sets must be stored", got)
	}
}

// The kernel buffers g as the view-switch prefix and never forwards it, so a
// binding on it would advertise a stroke that cannot arrive.
func TestKeys_NothingIsBoundToTheViewSwitchPrefix(t *testing.T) {
	t.Parallel()

	prefix := kernel.DefaultGlobalKeys().Go.Keys()
	chart, notes := defaultKeys().tables()
	for _, table := range []map[string]action{chart, notes} {
		for _, stroke := range prefix {
			if got, bound := table[stroke]; bound {
				t.Errorf("%q is bound to action %d and the kernel never forwards it", stroke, got)
			}
		}
	}
}

// Two things every advertised action owes the footer's zone map and the mouse: a
// first stroke the kernel can spell back into a keypress, and a label no other
// action in the same state shares.
func TestLiveKeys_EveryAdvertisedActionCanBeClicked(t *testing.T) {
	t.Parallel()

	for state := keyState(0); state < keyStates; state++ {
		seen := make(map[string]string)
		for _, b := range liveSets[state].Acts {
			label := b.Help().Key
			if other, clash := seen[label]; clash {
				t.Errorf("state %d advertises %q for both %q and %q; the footer mints one zone per label",
					state, label, other, b.Help().Desc)
			}
			seen[label] = b.Help().Desc
			if _, ok := kernel.Stroke(b); !ok {
				t.Errorf("state %d advertises %q on %v, which the kernel cannot spell back into a keypress",
					state, label, b.Keys())
			}
		}
	}
}

// Every command this package registers with a key teaches a key this view's own
// footer shows. The sweep in internal/ui holds the whole build to this; it cannot
// see a view it does not yet import, so the package holds itself to it too.
func TestCommands_TeachOnlyKeysThisViewShows(t *testing.T) {
	t.Parallel()

	shown := map[string]bool{kernel.SlotGesture(6): true}
	for _, b := range kernel.KeysFor(ViewID).Acts {
		shown[b.Help().Key] = true
	}
	for _, column := range kernel.KeysFor(ViewID).Full {
		for _, b := range column {
			shown[b.Help().Key] = true
		}
	}
	found := 0
	for _, cmd := range kernel.Commands() {
		if !strings.HasPrefix(cmd.ID, "timeline.") || len(cmd.Keys) == 0 {
			continue
		}
		found++
		for _, k := range cmd.Keys {
			if !shown[k] {
				t.Errorf("command %q teaches %q, which this view never shows", cmd.ID, k)
			}
		}
	}
	if found == 0 {
		t.Fatal("no command carries a key, so this test is checking nothing")
	}
}

// The registered set is the resting record, and the command sweep holds a palette
// entry's key against it.
func TestKeys_TheRegisteredSetNamesWhatCanBeDone(t *testing.T) {
	t.Parallel()

	set := kernel.KeysFor(ViewID)
	if set.IsZero() {
		t.Fatal("nothing was registered for this view")
	}
	if len(set.Acts) == 0 {
		t.Error("the registered set names no action, so the footer is a row of globals")
	}
}

func actsOf(set kernel.KeySet) string { return strings.Join(labels(set.Acts), " · ") }

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

// A bad registration is recorded rather than raised, because init() runs before
// anything can handle an error — so something has to read the record.
func TestRegister_TheRegistryAcceptedThisView(t *testing.T) {
	t.Parallel()

	if errs := kernel.RegistrationErrors(); len(errs) > 0 {
		t.Fatalf("the registry recorded %d bad registration(s): %v", len(errs), errs)
	}
	spec, ok := kernel.LookupView(ViewID)
	if !ok {
		t.Fatal("the view is not in the registry")
	}
	if spec.Slot != 6 {
		t.Errorf("the view took slot %d; docs/UX.md allocates 6 to the timeline", spec.Slot)
	}
	if spec.Requires != "" {
		t.Errorf("the view requires %q, and a chart of a search needs no capability beyond the search", spec.Requires)
	}
	if spec.RunsQueries {
		t.Error("the view claims saved queries, which open into the issue list")
	}
	if spec.New == nil {
		t.Error("the view registered no constructor")
	}
}
