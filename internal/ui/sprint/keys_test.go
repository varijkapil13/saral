package sprint

import (
	"fmt"
	"strings"
	"testing"
	"time"

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
		{"nothing to act on yet", keysWaiting},
		{"a board with no sprint on it", keysEmpty},
		{"a planned sprint", keysFuture},
		{"a running sprint", keysActive},
		{"a closed sprint", keysClosed},
		{"filling a sprint in", keysForm},
		{"answering the confirm", keysConfirm},
		{"a write in flight", keysWorking},
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

// Which lifecycle move is advertised comes from the sprint under the cursor,
// because future to active to closed is the whole of what the port allows: a
// footer naming both would name one that comes back refused.
func TestLiveKeys_FollowTheSprintUnderTheCursor(t *testing.T) {
	t.Parallel()

	dr := newDriver(t, testDeps(newFake()), 120, 20)
	dr.key("o")
	seen := map[int]string{}
	for _, tc := range []struct {
		name  string
		enter func()
		state keyState
		acts  string
	}{
		{"a running sprint", func() { dr.onSprint("Sprint 2") }, keysActive, "complete"},
		{"a planned sprint", func() { dr.onSprint("Sprint 3") }, keysFuture, "start"},
		{"a closed sprint", func() { dr.onSprint("Sprint 1") }, keysClosed, "edit"},
		{"filling a sprint in", func() { dr.key("n") }, keysForm, "ctrl+s"},
		{"answering the confirm", func() {
			dr.key("esc")
			dr.onSprint("Sprint 2")
			dr.key("c")
		}, keysConfirm, "y"},
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
			t.Fatalf("%s advertises nothing at all", tc.name)
		}
		if got := actsOf(set); !strings.Contains(got, tc.acts) {
			t.Errorf("%s advertises %q, want %q in it", tc.name, got, tc.acts)
		}
	}
}

// The two moves are never advertised together, and neither is advertised on a
// sprint it would be refused on.
func TestLiveKeys_OfferOnlyTheMoveTheStateMachineAllows(t *testing.T) {
	t.Parallel()

	k := defaultKeys()
	start, done := k.Start.Help().Key, k.Complete.Help().Key
	for state, set := range map[keyState]kernel.KeySet{
		keysFuture:  liveSets[keysFuture],
		keysActive:  liveSets[keysActive],
		keysClosed:  liveSets[keysClosed],
		keysEmpty:   liveSets[keysEmpty],
		keysWaiting: liveSets[keysWaiting],
	} {
		acts := actsOf(set)
		offersStart := strings.Contains(acts, start+" ")
		offersDone := strings.Contains(acts, done+" ")
		if offersStart && offersDone {
			t.Errorf("state %d offers both moves at once: %s", state, acts)
		}
		switch state {
		case keysFuture:
			if !offersStart || offersDone {
				t.Errorf("a planned sprint advertises %q", acts)
			}
		case keysActive:
			if offersStart || !offersDone {
				t.Errorf("a running sprint advertises %q", acts)
			}
		default:
			if offersStart || offersDone {
				t.Errorf("state %d advertises a lifecycle move on nothing that can take one: %s", state, acts)
			}
		}
	}
}

// A write in flight answers nothing of its own: every key is refused until the
// site says what happened, and the footer says so by naming none.
func TestLiveKeys_AWriteInFlightAdvertisesNothingAndAnswersNothing(t *testing.T) {
	t.Parallel()

	dr := newDriver(t, testDeps(newFake()), 120, 20)
	dr.onSprint("Sprint 2")
	dr.m.inflight = opComplete
	set, gen := dr.m.LiveKeys()
	if len(set.Acts) != 0 {
		t.Errorf("a write in flight advertises %s", actsOf(set))
	}
	if gen != int(keysWorking) {
		t.Errorf("a write in flight is in key state %d, want %d", gen, keysWorking)
	}
	at := dr.m.cursor
	dr.key("j", "n", "e", "s", "c")
	if dr.m.cursor != at || dr.m.state != browsing {
		t.Error("a key was answered while a write was out with the site")
	}
}

// g reaches nothing here: the kernel buffers it as the view-switch prefix and
// never forwards it, so a binding on it would name a stroke that cannot arrive.
func TestKeys_NothingIsBoundToTheViewSwitchPrefix(t *testing.T) {
	t.Parallel()

	rows, form, confirm := defaultKeys().tables()
	for name, table := range map[string]map[string]action{
		"the list": rows, "the form": form, "the confirm": confirm,
	} {
		if _, bound := table["g"]; bound {
			t.Errorf("%s binds g, which the kernel buffers and never delivers", name)
		}
	}
}

// The registered resting set is what a palette entry's key is held against, so
// every key this view teaches has to be in it.
func TestKeys_TheRestingSetNamesEveryKeyACommandTeaches(t *testing.T) {
	t.Parallel()

	set := defaultKeys().keySet()
	shown := map[string]bool{}
	for _, b := range set.Acts {
		shown[b.Help().Key] = true
	}
	for _, column := range set.Full {
		for _, b := range column {
			shown[b.Help().Key] = true
		}
	}
	k := defaultKeys()
	for _, b := range []kernel.Binding{k.New, k.Edit, k.Start, k.Complete, k.Closed} {
		if !shown[b.Help().Key] {
			t.Errorf("the resting set does not name %q, so the palette entry that teaches it has nothing to be held against",
				b.Help().Key)
		}
	}
}

// AllocsPerRun measures the whole process, so this one cannot run beside
// anything else.
func TestLiveKeys_CostNothingToAskFor(t *testing.T) {
	m, ok := New(kernel.Deps{
		Caps:  fullCaps(),
		Theme: kernel.NewTheme(kernel.ThemeNoColor, true, kernel.ASCIIGlyphs()),
		Now:   func() time.Time { return time.Date(2026, time.March, 5, 9, 0, 0, 0, time.UTC) },
	}).(*Model)
	if !ok {
		t.Fatal("New no longer builds a *Model")
	}
	m.sprints = []jira.Sprint{{ID: 1, Name: "Sprint 1", State: jira.SprintActive}}
	m.boards = []jira.Board{{ID: 1, Name: "board"}}
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
