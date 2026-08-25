package form

import (
	"fmt"
	"strings"
	"testing"
	"time"

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
		{"choosing an issue type", keysTypes},
		{"the field list", keysFields},
		{"a one-line field open", keysText},
		{"the long-text pane open", keysDoc},
		{"the value chooser open", keysChoosing},
	}
	if len(named) != int(keyStates) {
		t.Fatalf("the form has %d key states and this test names %d", keyStates, len(named))
	}
	var b strings.Builder
	for _, s := range named {
		fmt.Fprintf(&b, "%s\n", s.name)
		writeKeySet(&b, liveSets[s.state])
	}
	golden(t, "keys.golden", b.String())
}

func TestLiveKeys_FollowWhatTheFormIsShowing(t *testing.T) {
	t.Parallel()
	m := newWith(testDeps(nil), newSchemaCache(schemaTTL, time.Now), newDraftStore())
	seen := map[int]string{}
	for _, tc := range []struct {
		name  string
		enter func()
		state keyState
	}{
		{"issue types", func() { m.stage, m.edit = stageTypes, editNone }, keysTypes},
		{"field list", func() { m.stage, m.edit = stageFields, editNone }, keysFields},
		{"one-line field", func() { m.edit = editText }, keysText},
		{"long text", func() { m.edit = editDoc }, keysDoc},
		{"value chooser", func() { m.edit = editChoose }, keysChoosing},
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
		if len(set.Short) == 0 {
			t.Errorf("%s advertises nothing at all", tc.name)
		}
	}
}

// ctrl+d empties a field in the list and finishes the text in the long-text
// pane. Whichever is on screen has to be the only one advertised, or the footer
// is telling the user the stroke does something it does not.
func TestLiveKeys_CtrlDIsNamedForWhatItDoesInThisState(t *testing.T) {
	t.Parallel()
	fields := shortOf(liveSets[keysFields]) + " " + strings.Join(labels(liveSets[keysFields].Full[1]), ", ")
	doc := shortOf(liveSets[keysDoc])
	switch {
	case !strings.Contains(fields, "ctrl+d empty this field"):
		t.Errorf("the field list stopped naming ctrl+d: %s", fields)
	case !strings.Contains(doc, "ctrl+d finish this text"):
		t.Errorf("the long-text pane does not name the key that closes it: %s", doc)
	case strings.Contains(doc, "empty this field"):
		t.Errorf("the long-text pane advertises the field list's ctrl+d: %s", doc)
	}
}

// AllocsPerRun measures the whole process, so this one cannot run beside
// anything else.
func TestLiveKeys_CostNothingToAskFor(t *testing.T) {
	m := newWith(testDeps(nil), newSchemaCache(schemaTTL, time.Now), newDraftStore())
	if got := testing.AllocsPerRun(100, func() { _, _ = m.LiveKeys() }); got != 0 {
		t.Errorf("asking for the live keys allocates %.0f times; chromeFor asks on every frame, so the sets must be stored", got)
	}
}

func shortOf(set kernel.KeySet) string {
	return strings.Join(labels(set.Short), " · ")
}

func labels(bindings []kernel.Binding) []string {
	out := make([]string, 0, len(bindings))
	for _, b := range bindings {
		out = append(out, b.Help().Key+" "+b.Help().Desc)
	}
	return out
}

func writeKeySet(b *strings.Builder, set kernel.KeySet) {
	fmt.Fprintf(b, "  short  %s\n", shortOf(set))
	for _, column := range set.Full {
		fmt.Fprintf(b, "  full   [%s]\n", strings.Join(labels(column), ", "))
	}
}
