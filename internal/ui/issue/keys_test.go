package issue

import (
	"fmt"
	"strings"
	"testing"

	"github.com/varijkapil13/saral/internal/ui/kernel"
	"github.com/varijkapil13/saral/pkg/jira"
)

// TestLiveKeys_EveryStateGolden holds every state the sidebar answers for. A state nothing covers is a state whose keys
// can change without anybody noticing.
func TestLiveKeys_EveryStateGolden(t *testing.T) {
	t.Parallel()
	sideStates := []struct {
		name string
		idx  int
	}{
		{"browsing, the description focused", lkBrowseDesc},
		{"browsing, the description focused, dirty", lkBrowseDescDirty},
		{"browsing, the fields focused", lkBrowseDetails},
		{"browsing, the fields focused, dirty", lkBrowseDetailsDirty},
		{"browsing, the thread focused", lkBrowseComments},
		{"browsing, the thread focused, dirty", lkBrowseCommentsDirty},
		{"a row taking typing", lkTyping},
		{"the description textarea open", lkDocEdit},
		{"an inline list open", lkPicking},
		{"a chosen status move's required fields", lkPickFields},
		{"waiting for the go-ahead to move, inline", lkPickConfirm},
		{"saving", lkSaving},
		{"the leave prompt", lkLeaving},
	}
	if len(sideStates) != lkCount {
		t.Fatalf("the pane has %d states; this test names %d", lkCount, len(sideStates))
	}

	var b strings.Builder
	b.WriteString("editing an issue's fields\n")
	for _, s := range sideStates {
		fmt.Fprintf(&b, "  %s\n", s.name)
		writeKeySet(&b, sideLiveSets[s.idx])
	}
	golden(t, "keys.golden", b.String())
}

func TestLiveKeys_FollowTheSidebarsOwnState(t *testing.T) {
	t.Parallel()
	m, ok := New(testDeps(t, nil), jira.Issue{Key: "PROJ-1"}).(*Model)
	if !ok {
		t.Fatal("New no longer builds a *Model")
	}
	seen := map[int]string{}
	for _, tc := range []struct {
		name    string
		prepare func()
	}{
		{"browsing, description", func() { m.stage, m.leaving, m.focus = sideBrowse, false, regionDesc }},
		{"browsing, details", func() { m.stage, m.leaving, m.focus = sideBrowse, false, regionDetails }},
		{"typing", func() { m.stage, m.leaving = sideTyping, false }},
		{"doc editing", func() { m.stage, m.leaving = sideDocEdit, false }},
		{"saving", func() { m.stage, m.leaving = sideSaving, false }},
		{"leaving", func() { m.stage, m.leaving = sideBrowse, true }},
	} {
		tc.prepare()
		set, gen := m.LiveKeys()
		if other, clash := seen[gen]; clash {
			t.Errorf("%s and %s share generation %d, so the footer will not repaint between them",
				tc.name, other, gen)
		}
		seen[gen] = tc.name
		if tc.name == "saving" && !set.IsZero() {
			t.Errorf("a save in flight advertises %s, none of which answers", actsOf(set))
		}
	}
}

// AllocsPerRun measures the whole process, so this one cannot run beside
// anything else.
func TestLiveKeys_CostNothingToAskFor(t *testing.T) {
	side, ok := New(testDeps(t, nil), jira.Issue{Key: "PROJ-1"}).(*Model)
	if !ok {
		t.Fatal("New no longer builds a *Model")
	}
	if got := testing.AllocsPerRun(100, func() { _, _ = side.LiveKeys() }); got != 0 {
		t.Errorf("the issue pane allocates %.0f times to report its keys; chromeFor asks on every frame, so the sets must be stored",
			got)
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
	if set.IsZero() {
		b.WriteString("    nothing of its own; the globals are all that answer\n")
		return
	}
	fmt.Fprintf(b, "    acts   %s\n", actsOf(set))
	for _, column := range set.Full {
		fmt.Fprintf(b, "    full   [%s]\n", strings.Join(labels(column), ", "))
	}
}
