package kernel

import (
	"runtime"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/varijkapil13/saral/internal/ui/uitest"
)

// Below the minimum the frame is a sentence, so the footer a click lands on is
// the last full frame's and no longer on screen.
func TestMouse_AClickBelowTheMinimumSizeResolvesNothing(t *testing.T) {
	board, _ := twoRoots(t)
	m := newAt(t, testDeps(), 120, 30)
	m = pushed(t, m, PushMsg{View: &stubView{id: "pane"}, ID: "pane"})
	at := uitest.ZoneDrawn(t, m.deps.Zones, func() { _ = m.Frame() }, m.zonePrefix+rootZone)

	next, _ := m.Update(tea.WindowSizeMsg{Width: MinWidth - 1, Height: MinHeight - 1})
	m = next.(Model)
	board.seen = nil
	next, _ = m.Update(tea.MouseClickMsg{X: at.StartX, Y: at.StartY, Button: tea.MouseLeft})
	m = next.(Model)
	if len(m.stack) != 2 {
		t.Errorf("a click on a footer no longer drawn went back to the root: %d deep", len(m.stack))
	}
	if saw(board, "click") {
		t.Error("the click reached a view that is not drawn")
	}
}

func TestMouse_ATooSmallFrameStillPurgesTheZonesOfTheLastOne(t *testing.T) {
	twoRoots(t)
	m := newAt(t, testDeps(), 120, 30)
	id := m.zonePrefix + rootZone
	uitest.ZoneDrawn(t, m.deps.Zones, func() { _ = m.Frame() }, id)

	next, _ := m.Update(tea.WindowSizeMsg{Width: MinWidth - 1, Height: MinHeight})
	m = next.(Model)
	_ = m.Frame()
	eventuallyTrue(t, func() bool { return m.deps.Zones.Get(id).IsZero() })
}

// Every view hears the switch, parked roots included, because a view that
// memoizes marked rows has to know to draw them again.
func TestMouse_TurningItOffOrOnIsToldToEveryView(t *testing.T) {
	board, backlog := twoRoots(t)
	m := newAt(t, testDeps(), 120, 30)
	m, _ = press(m, "g", "2")

	next, _ := m.Update(SetMouseMsg{Enabled: false})
	m = next.(Model)
	for _, v := range []*stubView{board, backlog} {
		if !saw(v, "mouse:false") {
			t.Errorf("%s never heard the mouse go off: %v", v.id, v.seen)
		}
	}
	if m.deps.Zones.Enabled() {
		t.Error("the zone manager is still enabled")
	}
}

// A row memoized while the mouse was on carries markers. Scanning with the
// manager disabled is what takes them out of the frame.
func TestMouse_AFrameDrawnAfterTurningItOffCarriesNoMarker(t *testing.T) {
	resetRegistry()
	t.Cleanup(resetRegistry)
	var memo string
	RegisterView(ViewSpec{ID: "board", Title: "Board", Slot: 1, New: func(d Deps) View {
		memo = d.Zones.Mark(d.Zones.NewPrefix()+"row", "PROJ-1 Fix the thing")
		return &stubView{id: "board", content: memo}
	}})

	m := newAt(t, unstyledDeps(), 120, 30)
	next, _ := m.Update(SetMouseMsg{Enabled: false})
	frame := next.(Model).Frame()
	if !strings.ContainsRune(memo, '\x1b') {
		t.Fatal("the memoized row carries no marker, so this proves nothing")
	}
	if strings.ContainsRune(frame, '\x1b') {
		t.Errorf("a marker drawn while the mouse was on survived into a frame drawn with it off:\n%q", frame)
	}
}

// The footer is memoized, and one drawn with the mouse off has no zones; turning
// it back on has to redraw it or the root cell stops answering clicks.
func TestMouse_TurningItBackOnRedrawsTheFootersZones(t *testing.T) {
	twoRoots(t)
	m := newAt(t, testDeps(), 120, 30, WithMouse(false))
	_ = m.Frame()

	next, _ := m.Update(SetMouseMsg{Enabled: true})
	m = next.(Model)
	uitest.ZoneDrawn(t, m.deps.Zones, func() { _ = m.Frame() }, m.zonePrefix+rootZone)
}

// NO_COLOR draws every level in the same bold, so the glyph is what tells a
// warning from a failure.
func TestStatus_EachLevelIsToldApartWithoutColour(t *testing.T) {
	for _, tier := range []Glyphs{ASCIIGlyphs(), UnicodeGlyphs(), NerdGlyphs()} {
		t.Run(tier.Tier(), func(t *testing.T) {
			twoRoots(t)
			d := testDeps()
			d.Theme = NewTheme(ThemeNoColor, true, tier)
			m := newAt(t, d, 120, 30)
			lines := map[StatusLevel]string{}
			for _, level := range []StatusLevel{LevelInfo, LevelWarn, LevelError} {
				next, _ := m.Update(StatusMsg{Text: "said", Level: level})
				lines[level] = strings.TrimSpace(ansi.Strip(next.(Model).statusLine()))
			}
			if lines[LevelInfo] != "said" {
				t.Errorf("an info line reads %q, want it bare", lines[LevelInfo])
			}
			if lines[LevelWarn] != tier.Warn+" said" || lines[LevelError] != tier.Cross+" said" {
				t.Errorf("warn reads %q and error %q", lines[LevelWarn], lines[LevelError])
			}
			if lines[LevelWarn] == lines[LevelError] {
				t.Error("a warning and a failure read the same")
			}
		})
	}
}

func TestStatus_GoldenPerLevel(t *testing.T) {
	for name, level := range map[string]StatusLevel{"warn": LevelWarn, "error": LevelError} {
		t.Run(name, func(t *testing.T) {
			twoRoots(t)
			m := newAt(t, testDeps(), 80, 20)
			next, _ := m.Update(StatusMsg{Text: "the token cannot move issues on this board", Level: level})
			golden(t, "status_"+name+"_80x20.golden", ansi.Strip(next.(Model).Frame()))
		})
	}
}

// eventuallyTrue waits on the zone manager's worker, which stores and purges
// zones off the event loop.
func eventuallyTrue(t *testing.T, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for !cond() {
		if time.Now().After(deadline) {
			t.Fatal("the condition never held")
		}
		runtime.Gosched()
	}
}

func BenchmarkFrameMouseOff(b *testing.B) {
	resetRegistry()
	b.Cleanup(resetRegistry)
	RegisterView(ViewSpec{ID: "board", Title: "Board", Slot: 1,
		New: func(d Deps) View {
			return &stubView{id: "board", content: strings.Repeat("row\n", 40)}
		}})

	m, err := New(testDeps(), WithSize(200, 60), WithMouse(false))
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
