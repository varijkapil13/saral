package board

import (
	"fmt"
	"strings"
	"testing"

	"github.com/varijkapil13/saral/internal/ui/kernel"
	"github.com/varijkapil13/saral/pkg/jira"
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
		{"looking at the board", keysBrowsing},
		{"a card in hand", keysHolding},
		{"a move out with the site", keysMoving},
		{"F waiting for its digit", keysPickingFilter},
	}
	if len(named) != int(keyStates) {
		t.Fatalf("the board has %d key states and this test names %d", keyStates, len(named))
	}
	var b strings.Builder
	for _, s := range named {
		fmt.Fprintf(&b, "%s\n", s.name)
		writeKeySet(&b, liveSets[s.state])
	}
	golden(t, "keys.golden", b.String())
}

// The keys follow what the board is doing. Every state is reached by real keys,
// the generations are distinct so the footer repaints, and the two that offer
// something name it.
func TestLiveKeys_FollowWhatTheBoardIsDoing(t *testing.T) {
	t.Parallel()
	dr := newDriver(t, testDeps(newFake(9)), 120, 20)

	set, browsing := dr.m.LiveKeys()
	if len(set.Acts) == 0 {
		t.Fatal("a board being looked at advertises nothing")
	}

	dr.key("m")
	held, holding := dr.m.LiveKeys()
	if holding == browsing {
		t.Error("picking a card up did not change the generation, so the footer keeps the row it had")
	}
	if len(held.Acts) == 0 {
		t.Fatal("a card in hand advertises nothing")
	}
	if labelsOf(held.Acts) == labelsOf(set.Acts) {
		t.Errorf("a card in hand advertises the same actions as a board being looked at: %s", labelsOf(held.Acts))
	}

	dr.m.moving = true
	moving, gen := dr.m.LiveKeys()
	if gen == holding {
		t.Error("a move in flight reports the generation a card in hand does")
	}
	if len(moving.Acts) != 0 {
		t.Errorf("a move out with the site advertises %s; every key is refused until it answers", labelsOf(moving.Acts))
	}
}

// The keys a state answers to are the keys it advertises. A stroke the footer
// names and the dispatcher drops is the drift the registry exists to prevent.
func TestKeys_EveryAdvertisedActionIsOneTheStateAnswers(t *testing.T) {
	t.Parallel()
	browsing, holding := defaultKeys().tables()
	for name, tc := range map[string]struct {
		set   kernel.KeySet
		table map[string]action
	}{
		"looking at the board": {set: liveSets[keysBrowsing], table: browsing},
		"a card in hand":       {set: liveSets[keysHolding], table: holding},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			for _, column := range append([][]kernel.Binding{tc.set.Acts}, tc.set.Full...) {
				for _, b := range column {
					for _, stroke := range b.Keys() {
						if tc.table[stroke] == actNone {
							t.Errorf("%q is advertised as %q and the dispatcher does nothing with it",
								stroke, b.Help().Desc)
						}
					}
				}
			}
		})
	}
}

// g is bound as the first half of this view's own gg and ge gestures, and the
// kernel buffers it for exactly that reason. Nothing else the kernel keeps for
// itself may be bound here, because a binding on one would advertise a stroke
// that cannot arrive.
func TestKeys_NothingTheKernelKeepsIsBoundHere(t *testing.T) {
	t.Parallel()
	globals := kernel.DefaultGlobalKeys()
	reserved := map[string]string{}
	for _, b := range []kernel.Binding{globals.Quit, globals.Back, globals.Help, globals.Palette, globals.Refresh, globals.Purge} {
		for _, stroke := range b.Keys() {
			reserved[stroke] = b.Help().Desc
		}
	}
	browsing, holding := defaultKeys().tables()
	for name, table := range map[string]map[string]action{"looking at the board": browsing, "a card in hand": holding} {
		for stroke := range table {
			if why, taken := reserved[stroke]; taken {
				t.Errorf("%s binds %q, which the kernel keeps for %s and never forwards", name, stroke, why)
			}
		}
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

// The command the palette carries for a move teaches the key the footer shows,
// which is the half of the binding a user is told to press.
func TestKeys_ThePaletteTeachesTheKeyTheFooterShows(t *testing.T) {
	t.Parallel()
	keys := defaultKeys()
	shown := map[string]bool{}
	for _, b := range liveSets[keysBrowsing].Acts {
		shown[b.Help().Key] = true
	}
	for _, want := range []string{keys.Pick.Help().Key, keys.Board.Help().Key} {
		if !shown[want] {
			t.Errorf("the resting footer does not show %q, which a palette entry teaches", want)
		}
	}
	if got, ok := kernel.LookupView(ViewID); !ok || got.Requires != jira.CapBoards {
		t.Errorf("the board registers Requires = %q, want the boards capability", got.Requires)
	}
}

func labelsOf(bindings []kernel.Binding) string {
	return strings.Join(labels(bindings), " · ")
}

func labels(bindings []kernel.Binding) []string {
	out := make([]string, 0, len(bindings))
	for _, b := range bindings {
		out = append(out, b.Help().Key+" "+b.Help().Desc)
	}
	return out
}

func writeKeySet(b *strings.Builder, set kernel.KeySet) {
	fmt.Fprintf(b, "  acts   %s\n", labelsOf(set.Acts))
	for _, column := range set.Full {
		fmt.Fprintf(b, "  full   [%s]\n", strings.Join(labels(column), ", "))
	}
}
