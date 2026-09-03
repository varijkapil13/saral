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

	found := false
	for _, at := range m.shown[:min(m.rowsHeight(), len(m.shown))] {
		if at.selectable() && !at.issue && m.rows[at.at].cmd.Kind == kernel.KindGoTo {
			found = true
		}
	}
	if !found {
		t.Errorf("no KindGoTo row is on the unfiltered 80x24 screen:\n%s", frame)
	}
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
