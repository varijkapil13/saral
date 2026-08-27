package plan

import (
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	zone "github.com/lrstanley/bubblezone/v2"

	"github.com/varijkapil13/saral/internal/ui/kernel"
	"github.com/varijkapil13/saral/internal/ui/widget"
	"github.com/varijkapil13/saral/pkg/jira"
)

// pressOn scans the frame the view would draw and presses the left button in
// the first cell of one of its zones. The manager records a zone on its own
// goroutine, so the zone is waited for rather than assumed.
func pressOn(t *testing.T, d kernel.Deps, dr *driver, name string) {
	t.Helper()

	_ = d.Zones.Scan(dr.m.View())
	id := dr.m.zones.ID(name)
	eventually(t, func() bool { return !d.Zones.Get(id).IsZero() })
	at := d.Zones.Get(id)
	dr.send(tea.MouseClickMsg{X: at.StartX, Y: at.StartY, Button: tea.MouseLeft})
}

// Clicking a plan opens it, which is what clicking a fold does in the issue
// pane, and it does the whole of what enter does — including the read.
func TestPlans_ClickingAPlanOpensItAndReadsItsReleases(t *testing.T) {
	t.Parallel()

	f := newFake(5)
	d := refusedDeps(f)
	dr := newDriver(t, d, 120, 30, WithDefined(defined()))

	pressOn(t, d, dr, "plan:"+dr.m.plans[1].plan.ID)
	if dr.m.planUnderCursor() != 1 {
		t.Fatalf("the click left the cursor on plan %d", dr.m.planUnderCursor())
	}
	if !dr.m.open[dr.m.plans[1].plan.ID] {
		t.Fatal("the click did not open the plan")
	}

	pressOn(t, d, dr, "plan:"+dr.m.plans[0].plan.ID)
	if !dr.m.open[dr.m.plans[0].plan.ID] {
		t.Error("clicking the first plan did not open it")
	}
	if n := countCalls(f, "Versions"); n != 1 {
		t.Errorf("the clicks read versions %d times; only the plan with a project has any to read", n)
	}
}

// Nothing here strips ANSI, and nothing may: a marker left in a frame with the
// mouse off is what terminal text selection picks up.
func TestPlans_WithTheMouseOffTheFrameCarriesNoMarker(t *testing.T) {
	t.Parallel()

	off := zone.New()
	t.Cleanup(off.Close)
	off.SetEnabled(false)

	d := plainDeps(newFake(5))
	d.Zones = off
	dr := newDriver(t, d, 120, 30, WithDefined(defined()))

	frame := dr.m.View()
	if strings.ContainsRune(frame, '\x1b') {
		t.Errorf("an escape survived a frame drawn with the mouse off:\n%q", frame)
	}
	if !strings.Contains(frame, "Q3 delivery") {
		t.Fatalf("the plans did not draw at all:\n%q", frame)
	}
}

func TestPlans_WithTheMouseOffNoRowIsHit(t *testing.T) {
	t.Parallel()

	off := zone.New()
	t.Cleanup(off.Close)
	off.SetEnabled(false)

	d := refusedDeps(newFake(5))
	d.Zones = off
	dr := newDriver(t, d, 120, 30, WithDefined(defined()))

	_ = off.Scan(dr.m.View())
	dr.send(tea.MouseClickMsg{X: 10, Y: 2, Button: tea.MouseLeft})
	if len(dr.m.open) != 0 {
		t.Error("a click opened a plan with the mouse off")
	}
}

// plainDeps draws with a theme that writes no escape sequence of its own, so
// that an escape left in a frame can only be a zone marker.
func plainDeps(client jira.SessionClient) kernel.Deps {
	d := refusedDeps(client)
	th := kernel.NewTheme(kernel.ThemeNoColor, true, kernel.ASCIIGlyphs())
	plain := lipgloss.NewStyle()
	for _, style := range []*lipgloss.Style{
		&th.Base, &th.Muted, &th.Accent, &th.Danger, &th.Warning, &th.Success, &th.Title,
		&th.Selected, &th.Badge, &th.StaleBadge,
	} {
		*style = plain
	}
	d.Theme = th
	return d
}

// The wheel scrolls the rows and leaves the selection alone, which is what a
// wheel does everywhere else.
func TestPlans_TheWheelScrollsWithoutMovingTheSelection(t *testing.T) {
	t.Parallel()

	many := make([]Defined, 0, 40)
	for i := range 40 {
		many = append(many, Defined{Name: "plan-" + string(rune('a'+i%26)), Projects: []string{"PROJ"}})
	}
	dr := newDriver(t, refusedDeps(newFake(5)), 120, 8, WithDefined(many))
	under := dr.m.cursor

	dr.send(tea.MouseWheelMsg{Button: tea.MouseWheelDown})
	if dr.m.top != widget.WheelStep {
		t.Errorf("the wheel scrolled to %d, want %d", dr.m.top, widget.WheelStep)
	}
	if dr.m.cursor != under {
		t.Errorf("the wheel moved the selection to %d, want it left on %d", dr.m.cursor, under)
	}

	dr.send(tea.MouseWheelMsg{Button: tea.MouseWheelUp})
	if dr.m.top != 0 {
		t.Errorf("the wheel is at %d after going down and back up, want 0", dr.m.top)
	}
}

// The lines under an open plan are prose. Pointing at one must not toggle the
// plan it belongs to, or reading a plan's sources would close them.
func TestPlans_ClickingADetailLineDoesNothing(t *testing.T) {
	t.Parallel()

	d := refusedDeps(newFake(5))
	dr := newDriver(t, d, 120, 30, WithDefined(defined()))
	dr.key("enter")

	frame := dr.m.View()
	_ = d.Zones.Scan(frame)
	at := 0
	for i := range dr.m.rows {
		if dr.m.rows[i].kind != rowPlan {
			at = i
			break
		}
	}
	if at == 0 {
		t.Fatal("the open plan has no detail lines under it")
	}
	dr.send(tea.MouseClickMsg{X: 6, Y: headHeight + at, Button: tea.MouseLeft})

	if !dr.m.open[dr.m.plans[0].plan.ID] {
		t.Error("clicking a line under the plan closed it")
	}
}
