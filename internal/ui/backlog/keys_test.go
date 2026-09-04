package backlog

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
		{"browsing with nothing picked", keysBrowsing},
		{"something picked", keysPicked},
		{"narrowed by a term", keysNarrowed},
		{"something picked and narrowed by a term", keysPickedNarrowed},
		{"choosing where they go", keysChoosing},
		{"confirming the move", keysConfirming},
		{"a move in flight", keysMoving},
		{"choosing the order", keysSorting},
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
	dr := newDriver(t, testDeps(newFake(12)), 120, 24)
	dr.cursorTo("row:PROJ-1")

	seen := make(map[int]string)
	for _, step := range []struct {
		name string
		do   func()
		want keyState
	}{
		{"browsing", func() {}, keysBrowsing},
		{"picked", func() { dr.cursorTo("row:PROJ-1"); dr.key("space") }, keysPicked},
		{"choosing", func() { dr.key("m") }, keysChoosing},
		{"confirming", func() { dr.key("enter") }, keysConfirming},
	} {
		step.do()
		set, gen := dr.m.LiveKeys()
		if gen != int(step.want) {
			t.Errorf("%s reports generation %d, want %d", step.name, gen, step.want)
		}
		if len(set.Acts) == 0 {
			t.Errorf("%s advertises nothing", step.name)
		}
		if other, clash := seen[gen]; clash {
			t.Errorf("%s and %s report the same generation, so the footer cannot tell them apart", step.name, other)
		}
		seen[gen] = step.name
	}

	cmd := dr.hold(keyPress("y"))
	if set, gen := dr.m.LiveKeys(); gen != int(keysMoving) || !set.IsZero() {
		t.Errorf("a move in flight reports generation %d and %v", gen, set.Acts)
	}
	dr.run(cmd)
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

// The registered set is the resting record, and the sweep in internal/ui holds a
// palette entry's key against it.
func TestKeys_TheRestingSetNamesWhatTheViewCanDo(t *testing.T) {
	t.Parallel()
	set := defaultKeys().keySet()
	if len(set.Acts) == 0 {
		t.Fatal("the resting set names no action")
	}
	labels := make(map[string]string, len(set.Acts))
	for _, b := range set.Acts {
		label := b.Help().Key
		if other, clash := labels[label]; clash {
			t.Errorf("%q is advertised for both %q and %q", label, other, b.Help().Desc)
		}
		labels[label] = b.Help().Desc
		if _, ok := kernel.Stroke(b); !ok {
			t.Errorf("%q cannot be spelt back into a keypress, so clicking it does nothing", label)
		}
	}
	// The palette teaches m for the move, and the footer has to be where a user
	// reads it.
	if _, shown := labels[defaultKeys().Move.Help().Key]; !shown {
		t.Error("the move is registered as a command carrying m and the resting footer does not show it")
	}
}

// Raw keys are claimed only while this view is asking something. A root view
// otherwise never sees esc, and one that claimed them all the time would swallow
// the digits that run a saved query.
func TestKeys_ClaimsRawKeysOnlyWhileItIsAskingSomething(t *testing.T) {
	t.Parallel()
	dr := newDriver(t, testDeps(newFake(12)), 120, 24)
	if dr.m.WantsRawKeys() {
		t.Error("the browsing state claims every key")
	}
	dr.cursorTo("row:PROJ-1")
	dr.key("space", "m")
	if !dr.m.WantsRawKeys() {
		t.Error("the chooser does not claim its keys, so esc never reaches it")
	}
	dr.key("enter")
	if !dr.m.WantsRawKeys() {
		t.Error("the confirm does not claim its keys, so esc never reaches it")
	}
	dr.key("esc")
	if dr.m.WantsRawKeys() {
		t.Error("the view still claims every key after the question was answered")
	}
}

func TestKeys_GoThenGoIsTheFirstRow(t *testing.T) {
	t.Parallel()
	dr := newDriver(t, testDeps(newFake(40)), 100, 20)
	dr.key("end")
	if dr.m.cursor == 0 {
		t.Fatal("end left the cursor on the first row, so gg cannot be shown to move it")
	}
	dr.key("g", "g")
	if dr.m.cursor != 0 {
		t.Errorf("gg left the cursor on row %d", dr.m.cursor)
	}
}

func TestKeys_TheChooserWalksThroughEveryDestination(t *testing.T) {
	t.Parallel()
	dr := newDriver(t, testDeps(newFake(12)), 120, 24)
	dr.cursorTo("row:PROJ-1")
	dr.key("space", "m")

	dr.key("up", "up", "up", "up")
	if dr.m.destAt != 0 {
		t.Errorf("walking up past the first destination left it on %d", dr.m.destAt)
	}
	for range len(dr.m.groups) + 2 {
		dr.key("down")
	}
	if want := len(dr.m.groups) - 1; dr.m.destAt != want {
		t.Errorf("walking down past the last destination left it on %d, want %d", dr.m.destAt, want)
	}
	mustContain(t, dr.view(), "["+backlogName+"]")
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
