package move

import (
	"fmt"
	"slices"
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
		name string
		step step
	}{
		{"choosing the target project", stepTarget},
		{"typing a project key", stepTyping},
		{"choosing the issue type", stepType},
		{"saying what each status becomes", stepStatus},
		{"answering what the target insists on", stepFields},
		{"the confirm screen", stepConfirm},
		{"a move the queue has taken", stepRunning},
		{"the outcome", stepDone},
	}
	if len(named) != int(steps) {
		t.Fatalf("the wizard has %d key states and this test names %d", steps, len(named))
	}
	var b strings.Builder
	for _, s := range named {
		fmt.Fprintf(&b, "%s\n", s.name)
		writeKeySet(&b, liveSets[s.step])
	}
	golden(t, "keys.golden", b.String())
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

// The footer must follow the wizard rather than the registry: the registered set
// is the resting one, and half of what a user meets here is some other step.
func TestLiveKeys_FollowWhatTheWizardIsDoing(t *testing.T) {
	t.Parallel()
	f, iss := twoProjects(t)
	w := &immediate{}
	dr := newDriver(t, testDeps(f), 100, 24, WithIssues(iss), withWaiter(w.wait))

	seen := make(map[int]string, int(steps))
	record := func(where string) {
		t.Helper()
		set, gen := dr.m.LiveKeys()
		if other, clash := seen[gen]; clash {
			t.Errorf("%s reports the same generation as %s, so the footer never repaints between them", where, other)
		}
		seen[gen] = where
		if len(set.Acts) == 0 && dr.m.step != stepRunning {
			t.Errorf("%s names no action, so its footer is a row of globals", where)
		}
	}

	record("the target project")
	dr.key("i")
	record("typing a key")
	dr.typeText("OTHER")
	dr.key("enter")
	record("the issue type")
	dr.key("enter")
	record("the status remap")
	dr.key("enter")
	record("the confirm screen")
	dr.running()
	record("a move the queue has taken")
	dr.send(taskMsg{gen: dr.m.gen, status: jira.TaskStatus{State: jira.TaskComplete, Progress: 100}})
	record("the outcome")

	if len(seen) < 7 {
		t.Errorf("only %d of the wizard's steps were reached by real keys: %v", len(seen), seen)
	}
}

// Entering a step has to change what is advertised, or the footer is telling the
// user about the step they have just left.
func TestLiveKeys_TypingAKeyAdvertisesSomethingElse(t *testing.T) {
	t.Parallel()
	f, iss := twoProjects(t)
	dr := newDriver(t, testDeps(f), 100, 20, WithIssues(iss))
	before, beforeGen := dr.m.LiveKeys()
	dr.key("i")
	after, afterGen := dr.m.LiveKeys()

	if beforeGen == afterGen {
		t.Fatal("typing a key reports the generation the project list does")
	}
	if actsOf(before) == actsOf(after) {
		t.Errorf("both steps advertise %q", actsOf(before))
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

// g is the kernel's view-switch prefix and is never forwarded, so a binding on it
// would advertise a stroke that cannot arrive.
func TestKeys_NothingIsBoundToTheViewSwitchPrefix(t *testing.T) {
	t.Parallel()
	prefix := kernel.DefaultGlobalKeys().Go.Keys()
	for stroke := range defaultKeys().table() {
		if slices.Contains(prefix, stroke) {
			t.Errorf("%q is bound here and the kernel buffers it as the view-switch prefix", stroke)
		}
	}
}

// The registered set is what the palette holds a command's key against and what
// the footer shows before a view has moved, so it may not be empty.
func TestKeys_TheRestingSetNamesWhatCanBeDone(t *testing.T) {
	t.Parallel()
	set := defaultKeys().keySet()
	if len(set.Acts) == 0 {
		t.Fatal("the resting set names no action")
	}
	seen := make(map[string]string, len(set.Acts))
	for _, b := range set.Acts {
		label := b.Help().Key
		if other, clash := seen[label]; clash {
			t.Errorf("%q is advertised for both %q and %q; the footer mints one zone per label", label, other, b.Help().Desc)
		}
		seen[label] = b.Help().Desc
		if _, ok := kernel.Stroke(b); !ok {
			t.Errorf("%q cannot be spelt back into a keypress, so clicking it does nothing", label)
		}
	}
}

// Every state's advertised actions owe the same two things, because the footer is
// drawn from whichever one is on screen.
func TestLiveKeys_EveryStateCanBeClicked(t *testing.T) {
	t.Parallel()
	for at, set := range liveSets {
		seen := make(map[string]string, len(set.Acts))
		for _, b := range set.Acts {
			label := b.Help().Key
			if other, clash := seen[label]; clash {
				t.Errorf("step %d advertises %q for both %q and %q", at, label, other, b.Help().Desc)
			}
			seen[label] = b.Help().Desc
			if _, ok := kernel.Stroke(b); !ok {
				t.Errorf("step %d advertises %q on %v, which cannot be spelt back into a keypress", at, label, b.Keys())
			}
		}
	}
}
