package kernel

import (
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	zone "github.com/lrstanley/bubblezone/v2"
)

// markingView wraps what it draws in a zone, which is what every clickable view
// in the tree does.
type markingView struct {
	zones  *zone.Manager
	prefix string
}

func (v *markingView) Init() tea.Cmd                  { return nil }
func (v *markingView) Update(tea.Msg) (View, tea.Cmd) { return v, nil }
func (v *markingView) View() string {
	return v.zones.Mark(v.prefix+"row:1", "PROJ-1 Fix the thing")
}

func registerMarkingView() {
	RegisterView(ViewSpec{ID: "board", Title: "Board", Slot: 1, New: func(d Deps) View {
		return &markingView{zones: d.Zones, prefix: d.Zones.NewPrefix()}
	}})
}

// unstyledDeps draws with a theme that writes no escape sequence of its own —
// not even bold — so that an escape left in a frame can only be a zone marker.
func unstyledDeps() Deps {
	d := testDeps()
	t := NewTheme(ThemeNoColor, true, ASCIIGlyphs())
	plain := lipgloss.NewStyle()
	for _, style := range []*lipgloss.Style{
		&t.Base, &t.Muted, &t.Accent, &t.Danger, &t.Warning, &t.Success, &t.Title,
		&t.Header, &t.StatusBar, &t.StatusWarn, &t.StatusFail, &t.Footer,
		&t.SlotOn, &t.SlotOff, &t.SlotGone, &t.HintKey, &t.HintDesc,
		&t.Selected, &t.Badge, &t.StaleBadge, &t.Overlay,
		&t.Help.Ellipsis, &t.Help.ShortKey, &t.Help.ShortDesc, &t.Help.ShortSeparator,
		&t.Help.FullKey, &t.Help.FullDesc, &t.Help.FullSeparator,
	} {
		*style = plain
	}
	t.HelpModel.Styles = t.Help
	d.Theme = t
	return d
}

// Nothing here strips ANSI, and nothing may: every other frame assertion in the
// package does, and stripping is precisely what hid these markers.
func TestFrame_MouseOffLeavesNoZoneMarkerBehind(t *testing.T) {
	resetRegistry()
	t.Cleanup(resetRegistry)
	registerMarkingView()

	m := newAt(t, unstyledDeps(), 120, 30, WithMouse(false))
	frame := m.Frame()
	if strings.ContainsRune(frame, '\x1b') {
		t.Errorf("an escape survived a frame drawn with the mouse off, so terminal text selection picks it up:\n%q", frame)
	}
	if !strings.Contains(frame, "PROJ-1 Fix the thing") {
		t.Errorf("the view did not draw at all:\n%q", frame)
	}
}

func TestFrame_MouseOnScansTheMarkersBackOut(t *testing.T) {
	resetRegistry()
	t.Cleanup(resetRegistry)
	registerMarkingView()

	m := newAt(t, unstyledDeps(), 120, 30)
	frame := m.Frame()
	if strings.ContainsRune(frame, '\x1b') {
		t.Errorf("a marker survived the scan:\n%q", frame)
	}
	if !strings.Contains(frame, "PROJ-1 Fix the thing") {
		t.Errorf("the view did not draw at all:\n%q", frame)
	}
}

func TestMouse_TheZoneManagerIsToldWhichWayTheSessionIs(t *testing.T) {
	for name, on := range map[string]bool{"reporting mouse": true, "leaving the mouse to the terminal": false} {
		t.Run(name, func(t *testing.T) {
			resetRegistry()
			t.Cleanup(resetRegistry)
			registerMarkingView()

			m := newAt(t, testDeps(), 120, 30, WithMouse(on))
			if got := m.deps.Zones.Enabled(); got != on {
				t.Errorf("the zone manager is enabled=%v for a session with mouse=%v", got, on)
			}
		})
	}
}

func TestClick_DoesNothingBehindTheHelpOverlay(t *testing.T) {
	resetRegistry()
	t.Cleanup(resetRegistry)
	board := &stubView{id: "board"}
	RegisterView(spec("board", 1, "", board))

	m := newAt(t, testDeps(), 120, 30)
	next, _ := m.Update(tea.MouseClickMsg{Button: tea.MouseLeft, X: 4, Y: 4})
	m = next.(Model)
	if !saw(board, "click") {
		t.Fatalf("a click never reached the view at all: %v", board.seen)
	}

	m, _ = press(m, "?")
	board.seen = nil
	if _, _ = m.Update(tea.MouseClickMsg{Button: tea.MouseLeft, X: 4, Y: 4}); saw(board, "click") {
		t.Errorf("a click reached the view the help overlay is covering: %v", board.seen)
	}
}

func BenchmarkFrameMouseOn(b *testing.B) {
	resetRegistry()
	b.Cleanup(resetRegistry)
	RegisterView(ViewSpec{ID: "board", Title: "Board", Slot: 1,
		New: func(d Deps) View {
			return &stubView{id: "board", content: d.Zones.Mark(d.Zones.NewPrefix()+"rows", strings.Repeat("row\n", 40))}
		}})
	RegisterKeys("board", KeySet{Short: []Binding{Bind([]string{"enter"}, "enter", "open")}})

	m, err := New(testDeps(), WithSize(200, 60), WithMouse(true))
	if err != nil {
		b.Fatal(err)
	}
	next, _ := m.Update(tea.WindowSizeMsg{Width: 200, Height: 60})
	m = next.(Model)

	b.ReportAllocs()
	b.ResetTimer()
	for range b.N {
		_ = m.Frame()
	}
}
