package palette

import (
	"strings"
	"testing"

	"github.com/varijkapil13/saral/internal/ui/kernel"
)

// Deliberately no blank import of another view package here: this package's
// test binary already links list, issue and comment (via kernel_test.go and
// palette.go itself), and adding more would grow their golden files' registry
// along with this one's.
func TestGate_UnfilteredFirstScreenHoldsADestination(t *testing.T) {
	cmds := kernel.Commands()
	goTo := 0
	for _, cmd := range cmds {
		if cmd.Kind == kernel.KindGoTo {
			goTo++
		}
	}
	if goTo == 0 {
		t.Fatal("the registry holds no KindGoTo command; issues.open or views.switch stopped claiming it")
	}

	m := build(paletteDeps(), cmds, memoryTable())
	next, _ := m.Update(kernel.SizeMsg{Width: 80, Height: 24})
	m, _ = next.(*Model)
	frame := m.View()

	// A destination merely being on the screen is not what this packet did:
	// deleting the nine appearance commands already put one there, and the test
	// stayed green with Commands() sorting by group name. What the Kind rank
	// decides is that every destination comes before every verb, so that is what
	// is asserted — the whole of Go to above the first thing you can do to a row.
	seenVerb := ""
	first := ""
	for _, at := range m.shown {
		if !at.selectable() || at.issue {
			continue
		}
		cmd := m.rows[at.at].cmd
		if first == "" {
			first = cmd.ID
		}
		switch {
		case cmd.Kind != kernel.KindGoTo:
			if seenVerb == "" {
				seenVerb = cmd.ID
			}
		case seenVerb != "":
			t.Errorf("the destination %q is listed below %q, so the unfiltered order is not ranked by Kind:\n%s",
				cmd.ID, seenVerb, frame)
		}
	}
	if first == "" {
		t.Fatal("the unfiltered list holds no command at all, so this test proved nothing")
	}
	if kind := commandKind(m, first); kind != kernel.KindGoTo {
		t.Errorf("the unfiltered list opens on %q, which is kind %d and not a destination:\n%s",
			first, kind, frame)
	}
}

func commandKind(m *Model, id string) kernel.CommandKind {
	for i := range m.rows {
		if m.rows[i].cmd.ID == id {
			return m.rows[i].cmd.Kind
		}
	}
	return kernel.KindVerb
}

func TestGate_NoThemeOrSchemeCommandIsRegistered(t *testing.T) {
	cmds := kernel.Commands()
	if len(cmds) == 0 {
		t.Fatal("the registry holds nothing; nothing in this test binary registered a command at all")
	}
	for _, cmd := range cmds {
		if strings.HasPrefix(cmd.ID, "theme.") || strings.HasPrefix(cmd.ID, "scheme.") {
			t.Errorf("%s is still a command; appearance.theme and appearance.scheme replaced it", cmd.ID)
		}
	}
}
