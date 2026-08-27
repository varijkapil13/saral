package release

import (
	"fmt"
	"strings"
	"testing"

	"github.com/varijkapil13/saral/internal/ui/kernel"
)

func TestFlowKeys_EveryStateGolden(t *testing.T) {
	t.Parallel()
	named := []struct {
		name  string
		state flowState
	}{
		{"choosing what happens to the open issues", flowChoosing},
		{"choosing where they move to", flowPicking},
		{"the confirm", flowConfirming},
		{"a release in flight", flowWorking},
		{"a release that did not happen", flowStuck},
	}
	if len(named) != int(flowKeyStates) {
		t.Fatalf("the flow has %d key states and this test names %d", flowKeyStates, len(named))
	}
	var b strings.Builder
	for _, s := range named {
		fmt.Fprintf(&b, "%s\n", s.name)
		writeKeySet(&b, flowSets[s.state])
	}
	golden(t, "flow_keys.golden", b.String())
}

// Every state is reached with real keys, so what the footer says is held against
// what the flow actually does.
func TestFlowKeys_FollowWhatTheScreenIsDoing(t *testing.T) {
	t.Parallel()

	dr := openFlow(t, newFake(4), 6, threeOhVersion())
	seen := map[int]string{}
	for _, tc := range []struct {
		name  string
		enter func()
		state flowState
		acts  bool
	}{
		{name: "choosing what happens", enter: func() {}, state: flowChoosing, acts: true},
		{name: "choosing where they move to", enter: func() { dr.key("j", "enter") }, state: flowPicking, acts: true},
		{name: "the confirm", enter: func() { dr.key("enter") }, state: flowConfirming, acts: true},
		{name: "a release in flight", enter: func() { dr.flow().state = flowWorking }, state: flowWorking},
		{name: "a release that did not happen", enter: func() { dr.flow().state = flowStuck }, state: flowStuck, acts: true},
	} {
		tc.enter()
		set, gen := dr.flow().LiveKeys()
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

// The confirm is the one screen that names y, because it is the one screen where
// y does anything.
func TestFlowKeys_OnlyTheConfirmAdvertisesTheRelease(t *testing.T) {
	t.Parallel()

	confirm := defaultFlowKeys().Confirm.Help().Key
	for state := flowState(0); state < flowKeyStates; state++ {
		named := strings.Contains(actsOf(flowSets[state]), confirm)
		if want := state == flowConfirming; named != want {
			t.Errorf("state %d advertises %q = %v, want %v: %s",
				state, confirm, named, want, actsOf(flowSets[state]))
		}
	}
}

// The flow binds no esc: it is pushed, so the kernel's own esc pops it, and a
// binding here would be a second answer to one question.
func TestFlowKeys_LeavesTheWayBackToTheKernel(t *testing.T) {
	t.Parallel()

	if _, bound := defaultFlowKeys().table()["esc"]; bound {
		t.Error("the flow binds esc, which the kernel already spends on popping it")
	}
	for state := flowState(0); state < flowKeyStates; state++ {
		for _, b := range flowSets[state].Acts {
			if b.Help().Key == kernel.DefaultGlobalKeys().Back.Help().Key {
				t.Errorf("state %d advertises the kernel's own way back as one of its actions", state)
			}
		}
	}
}

// A view whose keys move with its state has to say something in the state it
// opens in, or the footer of a freshly pushed screen is the globals and nothing
// else.
func TestFlowKeys_TheScreenItOpensInAdvertisesSomething(t *testing.T) {
	t.Parallel()

	for name, open := range map[string]int{"with issues still open": 4, "with nothing open": 0} {
		f, ok := NewFlow(testDeps(newFake(4)), twoOhVersion(), open, nil).(*Flow)
		if !ok {
			t.Fatal("NewFlow no longer builds a *Flow")
		}
		set, _ := f.LiveKeys()
		if len(set.Acts) == 0 {
			t.Errorf("a flow opened %s advertises nothing at all", name)
		}
	}
}

// Every advertised action needs a first stroke the kernel can spell back into a
// keypress, and a label no other action in the same state shares: the footer
// mints one zone per label and a click on it is delivered as that stroke.
func TestFlowKeys_EveryAdvertisedActionCanBeClicked(t *testing.T) {
	t.Parallel()

	for _, sets := range []struct {
		name  string
		count int
		at    func(int) kernel.KeySet
	}{
		{"the list", int(keyStates), func(i int) kernel.KeySet { return liveSets[i] }},
		{"the flow", int(flowKeyStates), func(i int) kernel.KeySet { return flowSets[i] }},
	} {
		for state := range sets.count {
			seen := map[string]string{}
			for _, b := range sets.at(state).Acts {
				label := b.Help().Key
				if other, clash := seen[label]; clash {
					t.Errorf("%s state %d advertises %q for both %q and %q",
						sets.name, state, label, other, b.Help().Desc)
				}
				seen[label] = b.Help().Desc
				if _, ok := kernel.Stroke(b); !ok {
					t.Errorf("%s state %d advertises %q on %v, which the kernel cannot spell back "+
						"into a keypress, so clicking it does nothing", sets.name, state, label, b.Keys())
				}
			}
		}
	}
}
