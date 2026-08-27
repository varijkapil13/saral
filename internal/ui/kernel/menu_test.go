package kernel

import (
	"strconv"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"
)

func rightClick(x, y int) tea.MouseClickMsg {
	return tea.MouseClickMsg{X: x, Y: y, Button: tea.MouseRight}
}

// menuBoard is a view with an inventory whose footer labels are terse and whose
// full descriptions are not, which is what the menu is for.
func menuBoard() *actingView {
	terse := []Binding{
		Bind([]string{"enter"}, "enter", "open"),
		Bind([]string{"e"}, "e", "edit"),
		Bind([]string{"t"}, "t", "status"),
	}
	full := []Binding{
		Bind([]string{"enter"}, "enter", "open the row under the cursor"),
		Bind([]string{"e"}, "e", "edit the fields of this issue"),
		Bind([]string{"t"}, "t", "change the status of this issue"),
	}
	return &actingView{set: KeySet{Acts: terse, Full: [][]Binding{full}}}
}

func openedMenu(t *testing.T, view *actingView) Model {
	t.Helper()

	resetRegistry()
	t.Cleanup(resetRegistry)
	RegisterView(actingSpec(view, 1))

	m := newAt(t, testDeps(), 120, 30, WithMouse(true))
	next, _ := m.Update(rightClick(10, 5))
	m = next.(Model)
	if !m.menu.open {
		t.Fatalf("a right-click did not open the menu:\n%s", ansi.Strip(m.Frame()))
	}
	return m
}

// TestMenu_ARightClickOffersWhatTheFocusedViewSaysApplies is the gesture the
// mouse table promised and P3.3 cut. The entries are the view's own inventory,
// with the sentences the footer row had no space for.
func TestMenu_ARightClickOffersWhatTheFocusedViewSaysApplies(t *testing.T) {
	m := openedMenu(t, menuBoard())

	frame := ansi.Strip(m.Frame())
	for _, want := range []string{
		"What can be done here",
		"open the row under the cursor",
		"edit the fields of this issue",
		"change the status of this issue",
	} {
		if !strings.Contains(frame, want) {
			t.Errorf("the menu does not offer %q:\n%s", want, frame)
		}
	}
}

// TestMenu_ChoosingAnEntryArrivesAtTheViewAsItsKey is the rule that keeps the
// key, the palette and the pointer one implementation of an action.
func TestMenu_ChoosingAnEntryArrivesAtTheViewAsItsKey(t *testing.T) {
	view := menuBoard()
	m := openedMenu(t, view)

	m, _ = press(m, "j")
	next, _ := m.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	m = next.(Model)

	if !view.took("e") {
		t.Errorf("choosing the second entry did not reach the view as e: %v", view.keys)
	}
	if m.menu.open {
		t.Error("the menu is still up after running something")
	}
}

// TestMenu_ClickingAnEntryArrivesAtTheViewAsItsKey is the same action by the
// route that opened the menu in the first place.
func TestMenu_ClickingAnEntryArrivesAtTheViewAsItsKey(t *testing.T) {
	view := menuBoard()
	m := openedMenu(t, view)
	_ = m.Frame()

	prefix := m.zonePrefix
	eventually(t, func() bool { return !m.deps.Zones.Get(prefix + menuZone + "t").IsZero() })
	at := m.deps.Zones.Get(prefix + menuZone + "t")

	next, _ := m.Update(tea.MouseClickMsg{X: at.StartX, Y: at.StartY, Button: tea.MouseLeft})
	m = next.(Model)
	if !view.took("t") {
		t.Errorf("clicking an entry did not reach the view as its key: %v", view.keys)
	}
	if m.menu.open {
		t.Error("the menu is still up after a click ran something")
	}
}

func TestMenu_AClickOffTheEntriesPutsItAway(t *testing.T) {
	view := menuBoard()
	m := openedMenu(t, view)

	next, _ := m.Update(tea.MouseClickMsg{X: 100, Y: 25, Button: tea.MouseLeft})
	m = next.(Model)
	if m.menu.open {
		t.Error("a click off the menu left it up")
	}
	if len(view.keys) != 0 {
		t.Errorf("dismissing the menu sent the view a key: %v", view.keys)
	}
}

func TestMenu_EscClosesItAndTheViewKeepsItsPlace(t *testing.T) {
	view := menuBoard()
	m := openedMenu(t, view)

	m, _ = press(m, "esc")
	if m.menu.open {
		t.Fatal("esc did not close the menu")
	}
	if len(m.stack) != 1 {
		t.Errorf("esc reached the kernel's own back key and popped %d entries", 1-len(m.stack))
	}
	if got := ansi.Strip(m.Frame()); !strings.Contains(got, "board body") {
		t.Errorf("the view did not come back:\n%s", got)
	}
}

// TestMenu_NoKeyReachesTheViewWhileItIsUp is the same rule the help overlay
// follows: what is covered is not being looked at, so nothing may act on it.
func TestMenu_NoKeyReachesTheViewWhileItIsUp(t *testing.T) {
	view := menuBoard()
	m := openedMenu(t, view)

	for _, key := range []string{"x", "/", "?", "r"} {
		m, _ = press(m, key)
	}
	if len(view.keys) != 0 {
		t.Errorf("keys reached the view behind the menu: %v", view.keys)
	}
	if m.showHelp {
		t.Error("? opened the help overlay from under the menu")
	}
}

// TestMenu_NoMouseEventReachesTheViewWhileItIsUp covers the kind the help
// overlay had to learn about too: a wheel scrolls something nobody can see.
func TestMenu_NoMouseEventReachesTheViewWhileItIsUp(t *testing.T) {
	for name, msg := range map[string]tea.Msg{
		"wheel":   tea.MouseWheelMsg{Button: tea.MouseWheelDown, X: 4, Y: 4},
		"motion":  tea.MouseMotionMsg{Button: tea.MouseLeft, X: 5, Y: 5},
		"release": tea.MouseReleaseMsg{Button: tea.MouseLeft, X: 4, Y: 4},
	} {
		t.Run(name, func(t *testing.T) {
			view := menuBoard()
			m := openedMenu(t, view)
			view.keys = nil
			next, _ := m.Update(msg)
			if got := next.(Model); !got.menu.open {
				t.Errorf("a %s put the menu away", name)
			}
			if len(view.keys) != 0 {
				t.Errorf("a %s reached the view behind the menu: %v", name, view.keys)
			}
		})
	}
}

// TestMenu_TheRowSaysWhatTheMenuAnswersTo holds docs/UX.md principle 2 over the
// one screen where the view's own keys do nothing.
func TestMenu_TheRowSaysWhatTheMenuAnswersTo(t *testing.T) {
	m := openedMenu(t, menuBoard())

	footer := lastLine(ansi.Strip(m.Frame()))
	for _, want := range []string{"up/down", "enter", "esc"} {
		if !strings.Contains(footer, want) {
			t.Errorf("the row does not name %q while the menu is up:\n%q", want, footer)
		}
	}
	if strings.Contains(footer, "edit") {
		t.Errorf("the row still advertises the view's own keys while the menu has them:\n%q", footer)
	}
}

// TestMenu_AViewWithNothingToOfferSaysSoRatherThanFlickering keeps a gesture
// that does nothing from reading as a broken program.
func TestMenu_AViewWithNothingToOfferSaysSoRatherThanFlickering(t *testing.T) {
	resetRegistry()
	t.Cleanup(resetRegistry)
	RegisterView(actingSpec(&actingView{}, 1))

	m := newAt(t, testDeps(), 120, 30, WithMouse(true))
	next, _ := m.Update(rightClick(10, 5))
	m = next.(Model)

	if m.menu.open {
		t.Fatal("a view with no inventory opened an empty menu")
	}
	if got := ansi.Strip(m.Frame()); !strings.Contains(got, "nothing to do to what is on this screen") {
		t.Errorf("the gesture did nothing and said nothing:\n%s", got)
	}
}

// TestMenu_AViewTakingTypingKeepsTheKeyboard is the token field: the menu spends
// the arrows and enter, and a view mid-token must not lose them.
func TestMenu_AViewTakingTypingKeepsTheKeyboard(t *testing.T) {
	resetRegistry()
	t.Cleanup(resetRegistry)
	typing := &stubView{id: "board", capturing: true}
	RegisterView(ViewSpec{ID: "board", Title: "Board", Slot: 1, New: func(Deps) View { return typing }})
	RegisterKeys("board", KeySet{Acts: []Binding{Bind([]string{"esc"}, "esc", "cancel")}})

	m := newAt(t, testDeps(), 120, 30, WithMouse(true))
	click := rightClick(10, 5)
	next, _ := m.Update(click)
	m = next.(Model)

	if m.menu.open {
		t.Fatal("the menu opened over a view that is taking typing")
	}
	if !saw(typing, msgName(click)) {
		t.Errorf("the click did not reach the view either: %v", typing.seen)
	}
}

// TestMenu_TheClickIsForwardedBeforeTheMenuIsBuilt is the seam a view closes
// when it maps a right-click to selecting the row under the pointer: the view
// hears the click first, so what it selects is what the menu is built from.
//
// The view here is a value rather than a pointer — onboarding is one, and the
// kernel copies it on every Update — so building the menu from the model before
// the forward really would read the view as it was.
func TestMenu_TheClickIsForwardedBeforeTheMenuIsBuilt(t *testing.T) {
	resetRegistry()
	t.Cleanup(resetRegistry)
	RegisterView(ViewSpec{ID: "board", Title: "Board", Slot: 1, New: func(Deps) View {
		return selectingView{acts: []Binding{Bind([]string{"enter"}, "enter", "open the selected row")}}
	}})

	m := newAt(t, testDeps(), 120, 30, WithMouse(true))
	next, _ := m.Update(rightClick(10, 5))
	m = next.(Model)

	held, ok := m.stack[0].view.(selectingView)
	if !ok {
		t.Fatalf("the stack holds a %T", m.stack[0].view)
	}
	if !held.selected {
		t.Fatal("the view never heard the right-click, so it cannot select what the menu is about")
	}
	if !m.menu.open {
		t.Fatal("the menu did not open")
	}
	if got := m.menu.acts[0].Help().Desc; got != "open PROJ-1" {
		t.Errorf("the menu was built from %q, so it was built before the view selected", got)
	}
}

// selectingView is a view that takes a right-click as "select the row under the
// pointer" and names the row in what it then says can be done to it.
type selectingView struct {
	acts     []Binding
	selected bool
}

func (v selectingView) Init() tea.Cmd { return nil }

func (v selectingView) Update(msg tea.Msg) (View, tea.Cmd) {
	if click, ok := msg.(tea.MouseClickMsg); ok && click.Button == tea.MouseRight {
		v.selected = true
		v.acts = []Binding{Bind([]string{"enter"}, "enter", "open PROJ-1")}
	}
	return v, nil
}

func (v selectingView) View() string { return "board body" }

func (v selectingView) LiveKeys() (KeySet, int) {
	gen := 0
	if v.selected {
		gen = 1
	}
	return KeySet{Acts: v.acts, Full: [][]Binding{v.acts}}, gen
}

func TestMenu_Golden(t *testing.T) {
	for _, size := range [][2]int{{120, 30}, {80, 20}} {
		t.Run(strconv.Itoa(size[0])+"x"+strconv.Itoa(size[1]), func(t *testing.T) {
			resetRegistry()
			t.Cleanup(resetRegistry)
			RegisterView(actingSpec(menuBoard(), 1))

			m := newAt(t, testDeps(), size[0], size[1], WithMouse(true))
			next, _ := m.Update(rightClick(4, 4))
			m = next.(Model)
			golden(t, "menu_"+strconv.Itoa(size[0])+"x"+strconv.Itoa(size[1])+".golden", ansi.Strip(m.Frame()))
		})
	}
}

// TestMenu_FitsTheBoxItIsGiven is why the rows are cut rather than wrapped: a
// frame one column too wide wraps, and a wrapped chrome row pushes the footer off
// the bottom of the screen.
func TestMenu_FitsTheBoxItIsGiven(t *testing.T) {
	long := "change the status of this issue to something else entirely, at length"
	for width := MinWidth; width <= 140; width += 7 {
		resetRegistry()
		view := &actingView{set: KeySet{
			Acts: []Binding{Bind([]string{"t"}, "t", "status")},
			Full: [][]Binding{{Bind([]string{"t"}, "t", long)}},
		}}
		RegisterView(actingSpec(view, 1))

		m := newAt(t, testDeps(), width, 24, WithMouse(true))
		next, _ := m.Update(rightClick(4, 4))
		m = next.(Model)

		frame := ansi.Strip(m.Frame())
		lines := strings.Split(frame, "\n")
		if len(lines) > 24 {
			t.Fatalf("at %d columns the menu made a frame of %d rows:\n%s", width, len(lines), frame)
		}
		for i, line := range lines {
			if got := ansi.StringWidth(line); got > width {
				t.Fatalf("at %d columns row %d of the menu is %d wide:\n%s", width, i, got, frame)
			}
		}
		resetRegistry()
	}
}

// TestMenu_MouseOffLeavesTheGestureWithTheTerminal keeps the one configuration
// where none of this exists: mouse = false is off all the way down, so a
// right-click that somehow arrives is the view's business and not a menu.
func TestMenu_MouseOffLeavesTheGestureWithTheTerminal(t *testing.T) {
	resetRegistry()
	t.Cleanup(resetRegistry)
	board := &stubView{id: "board"}
	RegisterView(spec("board", 1, "", board))
	RegisterKeys("board", KeySet{Acts: []Binding{Bind([]string{"e"}, "e", "edit")}})

	m := newAt(t, testDeps(), 120, 30, WithMouse(false))
	click := rightClick(10, 5)
	next, _ := m.Update(click)
	m = next.(Model)

	if m.menu.open {
		t.Error("a session with the mouse off opened a menu")
	}
	if !saw(board, msgName(click)) {
		t.Errorf("the click did not reach the view either: %v", board.seen)
	}
}
