package onboarding

import "github.com/varijkapil13/saral/internal/ui/kernel"

var _ kernel.KeyReporter = Model{}

type keyMap struct {
	Continue kernel.Binding
	Back     kernel.Binding
	Choose   kernel.Binding
	Write    kernel.Binding
	Finish   kernel.Binding
	Retry    kernel.Binding
}

// defaultKeys is the flow's keymap. enter is spelt three ways because it does
// three different things — take this step, write the file, leave — and a screen
// that called all three "continue" would be telling the user the least at the
// two moments it matters most.
func defaultKeys() keyMap {
	return keyMap{
		Continue: kernel.Bind([]string{"enter", "tab"}, "enter", "continue"),
		Back:     kernel.Bind([]string{"shift+tab"}, "shift+tab", "back a step"),
		Choose:   kernel.Bind([]string{"up", "down"}, "↑/↓", "choose"),
		Write:    kernel.Bind([]string{"enter", "tab"}, "enter", "write the profile"),
		Finish:   kernel.Bind([]string{"enter", "tab"}, "enter", "start using it"),
		Retry:    kernel.Bind([]string{"ctrl+r"}, "ctrl+r", "try that again"),
	}
}

// keySet is the resting state: a step with a field in it, part way through.
func (k keyMap) keySet() kernel.KeySet {
	return kernel.KeySet{
		Acts: []kernel.Binding{k.Continue, k.Back},
		Full: [][]kernel.Binding{{k.Continue, k.Back, k.Retry}},
	}
}

// keyState is which of the flow's states the keys belong to. It doubles as the
// generation the chrome memoizes on, so a state that is added has to be added
// here to be drawn.
type keyState int

const (
	keysFirstStep keyState = iota
	keysFirstStepFailed
	keysTyping
	keysChoosing
	keysReview
	keysDone
	keysFailed
	keysBusy
	keyStates
)

// liveSets is one set per state, built once at start-up. LiveKeys is called on
// every frame, so it hands back a stored value rather than assembling one.
var liveSets = func() [keyStates]kernel.KeySet {
	k := defaultKeys()
	var sets [keyStates]kernel.KeySet
	// The first step has nowhere to go back to, and back() knows it, so the
	// footer says so by leaving the key out.
	sets[keysFirstStep] = kernel.KeySet{
		Acts: []kernel.Binding{k.Continue},
		Full: [][]kernel.Binding{{k.Continue}},
	}
	// A site that could not be reached is the commonest way this flow fails, and
	// it fails on the one step with nothing behind it.
	sets[keysFirstStepFailed] = kernel.KeySet{
		Acts: []kernel.Binding{k.Retry, k.Continue},
		Full: [][]kernel.Binding{{k.Retry, k.Continue}},
	}
	sets[keysTyping] = k.keySet()
	// The arrows are the action on this step rather than a way of moving around
	// it: they are how the option is picked, on the first screen anybody sees.
	sets[keysChoosing] = kernel.KeySet{
		Acts: []kernel.Binding{k.Choose, k.Continue, k.Back},
		Full: [][]kernel.Binding{{k.Choose, k.Continue, k.Back, k.Retry}},
	}
	sets[keysReview] = kernel.KeySet{
		Acts: []kernel.Binding{k.Write, k.Back},
		Full: [][]kernel.Binding{{k.Write, k.Back}},
	}
	sets[keysDone] = kernel.KeySet{
		Acts: []kernel.Binding{k.Finish},
		Full: [][]kernel.Binding{{k.Finish}},
	}
	sets[keysFailed] = kernel.KeySet{
		Acts: []kernel.Binding{k.Retry, k.Back},
		Full: [][]kernel.Binding{{k.Retry, k.Back, k.Continue}},
	}
	// Nothing this view offers works while it is waiting on a site: enter and
	// shift+tab are both refused until the answer comes back, so the footer
	// falls back to the globals rather than naming a key that does nothing.
	sets[keysBusy] = kernel.KeySet{}
	return sets
}()

// LiveKeys reports the keys that work on the step the user is actually on. The
// steps differ in more than their prompt: the first has no way back, two of them
// have something to choose with the arrows, the last two spend enter on writing
// the file and leaving, and none of them answers anything while a site is being
// asked.
func (m Model) LiveKeys() (set kernel.KeySet, gen int) {
	state := m.keyState()
	return liveSets[state], int(state)
}

func (m Model) keyState() keyState {
	switch {
	case m.busy != busyNone:
		return keysBusy
	case m.problem != "" && m.step == stepSite:
		return keysFirstStepFailed
	case m.problem != "":
		return keysFailed
	case m.step == stepStorage, m.step == stepProject && len(m.suggested) > 0:
		return keysChoosing
	case m.step == stepReview:
		return keysReview
	case m.step >= stepDone:
		return keysDone
	case m.step == stepSite:
		return keysFirstStep
	default:
		return keysTyping
	}
}
