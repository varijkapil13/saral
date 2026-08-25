package comment

import (
	"fmt"
	"strings"
	"testing"

	"github.com/varijkapil13/saral/internal/ui/kernel"
)

// TestLiveKeys_EveryStateGolden holds every mode the footer and the help overlay
// can be asked about. A mode nothing covers is a mode whose keys can change
// without anybody noticing.
func TestLiveKeys_EveryStateGolden(t *testing.T) {
	t.Parallel()
	named := []struct {
		name string
		mode mode
	}{
		{"reading the thread", browsing},
		{"writing a comment", writing},
		{"confirming a deletion", confirming},
	}
	if len(named) != len(liveSets) {
		t.Fatalf("the thread has %d key states and this test names %d", len(liveSets), len(named))
	}
	var b strings.Builder
	for _, s := range named {
		fmt.Fprintf(&b, "%s\n", s.name)
		writeKeySet(&b, liveSets[s.mode])
	}
	golden(t, "keys.golden", b.String())
}

func TestLiveKeys_FollowTheModeTheThreadIsIn(t *testing.T) {
	t.Parallel()
	m := build(testDeps(t, nil), "PROJ-1")
	seen := map[int]string{}
	for _, tc := range []struct {
		name string
		mode mode
	}{
		{"reading", browsing},
		{"writing", writing},
		{"confirming", confirming},
	} {
		m.mode = tc.mode
		set, gen := m.LiveKeys()
		if gen != int(tc.mode) {
			t.Errorf("%s: generation %d, want %d", tc.name, gen, tc.mode)
		}
		if other, clash := seen[gen]; clash {
			t.Errorf("%s and %s share generation %d, so the footer will not repaint between them",
				tc.name, other, gen)
		}
		seen[gen] = tc.name
		if len(set.Acts) == 0 {
			t.Errorf("%s advertises nothing at all", tc.name)
		}
	}
}

// The three modes have no key in common, so nothing an editor advertises may
// appear while the thread is being read, and the other way round.
func TestLiveKeys_TheModesShareNoAdvertisedKey(t *testing.T) {
	t.Parallel()
	read := actsOf(liveSets[browsing])
	write := actsOf(liveSets[writing])
	confirm := actsOf(liveSets[confirming])
	for _, pair := range [][2]string{{read, write}, {read, confirm}, {write, confirm}} {
		for _, label := range strings.Split(pair[0], " · ") {
			if strings.Contains(pair[1], label) {
				t.Errorf("%q is advertised in two modes at once: %q and %q", label, pair[0], pair[1])
			}
		}
	}
}

// AllocsPerRun measures the whole process, so this one cannot run beside
// anything else.
func TestLiveKeys_CostNothingToAskFor(t *testing.T) {
	m := build(testDeps(t, nil), "PROJ-1")
	if got := testing.AllocsPerRun(100, func() { _, _ = m.LiveKeys() }); got != 0 {
		t.Errorf("asking for the live keys allocates %.0f times; chromeFor asks on every frame, so the sets must be stored", got)
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
