package attach

import (
	"fmt"
	"strings"
	"testing"

	"github.com/varijkapil13/saral/internal/ui/kernel"
)

// TestLiveKeys_EveryStateGolden holds every state the footer and the help overlay
// can be asked about. A state nothing covers is a state whose keys can change
// without anybody noticing.
func TestLiveKeys_EveryStateGolden(t *testing.T) {
	t.Parallel()
	named := []struct {
		name  string
		state keyState
	}{
		{"files, and a token that may only read them", keysReading},
		{"files, and a token that may add and remove them", keysReadingWrite},
		{"nothing attached, and nothing this token may attach", keysEmpty},
		{"nothing attached, and a token that may attach one", keysEmptyWrite},
		{"typing a path", keysTyping},
		{"a deletion waiting for an answer", keysConfirming},
	}
	if len(named) != int(keyStates) {
		t.Fatalf("the pane has %d key states and this test names %d", keyStates, len(named))
	}
	var b strings.Builder
	for _, s := range named {
		fmt.Fprintf(&b, "%s\n", s.name)
		writeKeySet(&b, liveSets[s.state])
	}
	golden(t, "keys.golden", b.String())
}

func TestLiveKeys_FollowWhatThePaneIsDoing(t *testing.T) {
	t.Parallel()

	dr, _ := loadedPane(t)
	seen := map[int]string{}
	for _, tc := range []struct {
		name  string
		enter func()
		state keyState
		acts  bool
	}{
		{"reading and writing", func() {}, keysReadingWrite, true},
		{"typing a path", func() { dr.key("u") }, keysTyping, true},
		{"a deletion waiting", func() { dr.key("esc"); dr.key("d") }, keysConfirming, true},
		{"nothing attached", func() {
			dr.key("esc")
			dr.m.files, dr.m.canWrite = nil, false
		}, keysEmpty, false},
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
		if got := len(set.Acts) > 0; got != tc.acts {
			t.Errorf("%s advertises %d actions, want some=%v", tc.name, len(set.Acts), tc.acts)
		}
	}
}

// The one state that offers nothing still hands back a set, because a zero set is
// how a view says it never got round to naming its keys.
func TestLiveKeys_TheStateWithNothingToOfferStillAnswers(t *testing.T) {
	t.Parallel()

	if liveSets[keysEmpty].IsZero() {
		t.Error("the empty state hands back a zero set, which reads as a view that named no keys at all")
	}
	if got := len(liveSets[keysEmpty].Acts); got != 0 {
		t.Errorf("the empty state names %d actions; nothing is attached and nothing may be", got)
	}
}

// g reaches nothing here: the kernel buffers it as the view-switch prefix and
// never forwards it, so a binding on it would name a stroke that cannot arrive.
func TestKeys_NothingIsBoundToTheViewSwitchPrefix(t *testing.T) {
	t.Parallel()

	browse, prompt, confirm := defaultKeys().tables()
	for name, table := range map[string]map[string]action{
		"the list": browse, "the path prompt": prompt, "the confirmation": confirm,
	} {
		if _, bound := table["g"]; bound {
			t.Errorf("%s binds g, which the kernel buffers and never delivers", name)
		}
	}
}

// The confirmation answers two strokes and no others. A table that also carried
// the key which opened it would delete a file on a held-down d.
func TestKeys_TheConfirmationAnswersOnlyItsTwoKeys(t *testing.T) {
	t.Parallel()

	_, _, confirm := defaultKeys().tables()
	if len(confirm) != 2 {
		t.Errorf("the confirmation answers %d strokes: %v", len(confirm), confirm)
	}
	for _, stroke := range []string{"d", "enter", "j", "u", "o", "z"} {
		if got, bound := confirm[stroke]; bound {
			t.Errorf("the confirmation answers %q with action %d", stroke, got)
		}
	}
}

// AllocsPerRun measures the whole process, so this one cannot run beside anything
// else.
func TestLiveKeys_CostNothingToAskFor(t *testing.T) {
	m, ok := New(testDeps(nil)).(*Model)
	if !ok {
		t.Fatal("New no longer builds a *Model")
	}
	if got := testing.AllocsPerRun(100, func() { _, _ = m.LiveKeys() }); got != 0 {
		t.Errorf("asking for the live keys allocates %.0f times; the chrome asks on every frame, "+
			"so the sets must be stored", got)
	}
}

// The resting record is what the command sweep in internal/ui holds a palette
// entry's key against, so every key a command teaches has to be a label it shows.
func TestKeys_TheRestingRecordShowsEveryKeyACommandTeaches(t *testing.T) {
	t.Parallel()

	shown := map[string]bool{}
	for _, b := range defaultKeys().keySet().Acts {
		shown[b.Help().Key] = true
	}
	k := defaultKeys()
	for _, want := range []kernel.Binding{k.Show, k.Open, k.Upload, k.Delete} {
		if !shown[want.Help().Key] {
			t.Errorf("a command teaches %q and the resting footer does not show it", want.Help().Key)
		}
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
