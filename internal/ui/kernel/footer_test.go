package kernel

import (
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"
)

// actingView offers an inventory and remembers the keys it was handed, which is
// what a click on a footer action has to arrive as.
type actingView struct {
	set  KeySet
	keys []string
}

func (v *actingView) Init() tea.Cmd { return nil }

func (v *actingView) Update(msg tea.Msg) (View, tea.Cmd) {
	if key, ok := msg.(tea.KeyPressMsg); ok {
		v.keys = append(v.keys, key.String())
	}
	return v, nil
}

func (v *actingView) View() string                    { return "board body" }
func (v *actingView) LiveKeys() (set KeySet, gen int) { return v.set, 0 }
func (v *actingView) took(stroke string) bool {
	return strings.Contains(strings.Join(v.keys, " "), stroke)
}
func act(key, desc string) Binding { return Bind([]string{key}, key, desc) }
func acting(acts ...Binding) *actingView {
	return &actingView{set: KeySet{Acts: acts, Full: [][]Binding{acts}}}
}
func actingSpec(v *actingView, slot int) ViewSpec {
	return ViewSpec{ID: "board", Title: "Board", Slot: slot, New: func(Deps) View { return v }}
}

// The regression, stated as a property: whatever else the row gives up, the way
// out survives. This is the row an eighty-column terminal used to throw away
// whole, which is how the program ran for a week without ever saying the command
// palette existed.
func TestFooter_TheGlobalsSurviveEveryWidth(t *testing.T) {
	resetRegistry()
	t.Cleanup(resetRegistry)
	RegisterView(actingSpec(acting(
		act("enter", "open the row under the cursor"),
		act("e", "edit the fields of this issue"),
		act("t", "change the status of this issue"),
		act("c", "read and write the comments"),
		act("/", "filter these rows down"),
		act("a", "widen to every issue in the project"),
		act("s", "save this search to a number key"),
	), 1))
	RegisterView(spec(PaletteViewID, 0, "", &stubView{id: PaletteViewID}))

	for width := MinWidth; width <= 132; width++ {
		m := newAt(t, testDeps(), width, 24)
		footer := lastLine(ansi.Strip(m.Frame()))
		if !strings.HasSuffix(footer, "? ctrl+k q") {
			t.Fatalf("at %d columns the row does not end in the globals:\n%q", width, footer)
		}
		if got := ansi.StringWidth(footer); got > width {
			t.Fatalf("at %d columns the row is %d wide, so it wraps and pushes itself off the screen:\n%q",
				width, got, footer)
		}
	}
}

// Each rung of the ladder, at the width that reaches it. The order is the design:
// actions fold into a +N from the right, then the root cell goes, then the
// actions lose their descriptions, and the globals never go at all.
func TestFooter_GivesThingsUpInOrder(t *testing.T) {
	long := "a description far too long for one row to hold beside anything"
	for name, tc := range map[string]struct {
		acts []Binding
		want string
	}{
		"everything fits": {
			acts: []Binding{act("e", "edit"), act("t", "status")},
			want: " Board  e edit  t status                                              ? ctrl+k q",
		},
		"what is left over folds into a count": {
			acts: []Binding{
				act("enter", "open"), act("e", "edit"), act("t", "status"),
				act("c", "comment"), act("/", "filter"), act("a", "all"), act("s", "save"),
			},
			want: " Board  enter open  e edit  t status  c comment  / filter  a all  +1  ? ctrl+k q",
		},
		"the root cell goes before the last action does": {
			acts: []Binding{act("e", long)},
			want: "e " + long + "      ? ctrl+k q",
		},
		"the descriptions go last": {
			acts: []Binding{act("e", long+" whatsoever")},
			want: "e                                                                     ? ctrl+k q",
		},
	} {
		t.Run(name, func(t *testing.T) {
			resetRegistry()
			t.Cleanup(resetRegistry)
			RegisterView(actingSpec(acting(tc.acts...), 1))
			RegisterView(spec(PaletteViewID, 0, "", &stubView{id: PaletteViewID}))

			m := newAt(t, testDeps(), MinWidth, 24)
			if got := lastLine(ansi.Strip(m.Frame())); got != tc.want {
				t.Errorf("at %d columns\n got %q\nwant %q", MinWidth, got, tc.want)
			}
		})
	}
}

// docs/UX.md principle 3: every action reachable three ways. A click on one is
// delivered as the key it advertises, so the pointer and the keyboard are one
// implementation and cannot drift.
func TestFooter_ClickingAnActionArrivesAtTheViewAsItsKey(t *testing.T) {
	view := &actingView{set: KeySet{
		Acts: []Binding{Bind([]string{"ctrl+s", "s"}, "ctrl+s", "save")},
		Full: [][]Binding{{Bind([]string{"ctrl+s", "s"}, "ctrl+s", "save")}},
	}}
	resetRegistry()
	t.Cleanup(resetRegistry)
	RegisterView(actingSpec(view, 1))

	m := newAt(t, testDeps(), 120, 30, WithMouse(true))
	_ = m.Frame()

	prefix := m.zonePrefix
	eventually(t, func() bool { return !m.deps.Zones.Get(prefix + actZone + "ctrl+s").IsZero() })

	info := m.deps.Zones.Get(prefix + actZone + "ctrl+s")
	click := tea.MouseClickMsg{X: info.StartX, Y: info.StartY, Button: tea.MouseLeft}
	if _, _ = m.Update(click); !view.took("ctrl+s") {
		t.Errorf("clicking the action did not reach the view as its first stroke: %v", view.keys)
	}
}

// The row's zones are minted from a label rather than a position, and the click
// handler walks the whole inventory rather than only what was drawn — so what
// stops an action that folded into a +N from answering where it used to be is
// bubblezone rebuilding its positions from each scanned frame. That is worth
// holding: without it, shrinking a terminal would leave every dropped action
// clickable in the gap it used to occupy.
func TestFooter_AnActionThatLeftTheRowStopsAnsweringToWhereItWas(t *testing.T) {
	view := acting(
		act("enter", "open"), act("e", "edit"), act("t", "status"),
		act("c", "comment"), act("/", "filter"), act("a", "all"), act("s", "save"),
	)
	resetRegistry()
	t.Cleanup(resetRegistry)
	RegisterView(actingSpec(view, 1))
	RegisterView(spec(PaletteViewID, 0, "", &stubView{id: PaletteViewID}))

	m := newAt(t, testDeps(), 120, 24, WithMouse(true))
	_ = m.Frame()
	prefix := m.zonePrefix
	eventually(t, func() bool { return !m.deps.Zones.Get(prefix + actZone + "s").IsZero() })
	was := m.deps.Zones.Get(prefix + actZone + "s")

	next, _ := m.Update(tea.WindowSizeMsg{Width: MinWidth, Height: 24})
	m = next.(Model)
	row := lastLine(ansi.Strip(m.Frame()))
	if strings.Contains(row, "s save") {
		t.Fatalf("s save still fits at %d columns, so this proves nothing:\n%s", MinWidth, row)
	}

	// bubblezone purges the zones a frame no longer carries on its own
	// goroutine, so the absence arrives after the frame does.
	eventually(t, func() bool { return m.deps.Zones.Get(prefix + actZone + "s").IsZero() })

	view.keys = nil
	click := tea.MouseClickMsg{X: was.StartX, Y: was.StartY, Button: tea.MouseLeft}
	if _, _ = m.Update(click); view.took("s") {
		t.Errorf("clicking where s save used to be ran it: %v", view.keys)
	}
}

// The +N is the only trace of what was left out, so it has to lead somewhere.
func TestFooter_ClickingTheCountOpensTheOverlayThatListsWhatItHides(t *testing.T) {
	resetRegistry()
	t.Cleanup(resetRegistry)
	RegisterView(actingSpec(acting(
		act("e", "edit the fields of this issue"),
		act("t", "change the status of this issue"),
		act("c", "read and write the comments"),
	), 1))

	m := newAt(t, testDeps(), MinWidth, 24, WithMouse(true))
	if got := lastLine(ansi.Strip(m.Frame())); !strings.Contains(got, "+") {
		t.Fatalf("nothing was left out at %d columns, so this proves nothing:\n%s", MinWidth, got)
	}
	prefix := m.zonePrefix
	eventually(t, func() bool { return !m.deps.Zones.Get(prefix + overflowZone).IsZero() })

	info := m.deps.Zones.Get(prefix + overflowZone)
	click := tea.MouseClickMsg{X: info.StartX, Y: info.StartY, Button: tea.MouseLeft}
	next, _ := m.Update(click)
	if got := ansi.Strip(next.(Model).Frame()); !strings.Contains(got, "read and write the comments") {
		t.Errorf("clicking the count did not open the overlay that lists what it stands for:\n%s", got)
	}
}

// The overlay leads with what somebody came to do. It used to lead with how to
// scroll, because that is the order the bindings happened to be written in.
func TestHelpOverlay_LeadsWithTheActionsRatherThanTheMotions(t *testing.T) {
	resetRegistry()
	t.Cleanup(resetRegistry)
	view := acting(act("enter", "open"), act("/", "filter"))
	view.set.Full = [][]Binding{
		{Bind([]string{"j"}, "↓/j", "down"), Bind([]string{"k"}, "↑/k", "up")},
		{act("enter", "open the row under the cursor"), act("/", "filter these rows")},
	}
	RegisterView(actingSpec(view, 1))

	m := newAt(t, testDeps(), 120, 30)
	m, _ = press(m, "?")
	frame := ansi.Strip(m.Frame())
	first := strings.SplitN(frame, "\n", 3)[1]
	if !strings.HasPrefix(strings.TrimLeft(first, " "), "enter open") {
		t.Errorf("the overlay does not lead with the actions:\n%s", first)
	}
	if !strings.Contains(frame, "open the row under the cursor") {
		t.Errorf("the overlay dropped the sentence the row had no space for:\n%s", frame)
	}
	if !strings.Contains(frame, "down") {
		t.Errorf("the overlay dropped the motions the row no longer carries:\n%s", frame)
	}
}

// The row says "? close help" while the overlay is up, so pointing at it has to
// close the overlay. Nothing else on the chrome answers there.
func TestFooter_ClickingCloseHelpClosesTheOverlay(t *testing.T) {
	resetRegistry()
	t.Cleanup(resetRegistry)
	RegisterView(actingSpec(acting(act("e", "edit")), 1))

	m := newAt(t, testDeps(), 120, 30, WithMouse(true))
	m, _ = press(m, "?")
	_ = m.Frame()
	if !m.showHelp {
		t.Fatal("the overlay did not open")
	}

	prefix := m.zonePrefix
	eventually(t, func() bool { return !m.deps.Zones.Get(prefix + actZone + "?").IsZero() })
	info := m.deps.Zones.Get(prefix + actZone + "?")

	next, _ := m.Update(tea.MouseClickMsg{X: info.StartX, Y: info.StartY, Button: tea.MouseLeft})
	if next.(Model).showHelp {
		t.Error("clicking the entry that says how to close the overlay left it up")
	}

	m, _ = press(m, "?", "?")
	root := m.deps.Zones.Get(prefix + rootZone)
	m.showHelp = true
	after, _ := m.Update(tea.MouseClickMsg{X: root.StartX + 1, Y: root.StartY, Button: tea.MouseLeft})
	if !after.(Model).showHelp {
		t.Error("clicking the root cell under an overlay switched view and left the overlay covering it")
	}
}

func TestStroke_IsTheInverseOfHowAKeyIsSpelt(t *testing.T) {
	for _, stroke := range []string{
		"enter", "esc", "tab", "shift+tab", "space", "delete", "up", "down", "left", "right",
		"home", "end", "pgup", "pgdown", "ctrl+b", "ctrl+d", "ctrl+f", "ctrl+g", "ctrl+k",
		"ctrl+n", "ctrl+p", "ctrl+r", "ctrl+s", "ctrl+t", "ctrl+u",
		"a", "b", "c", "d", "e", "f", "j", "k", "s", "t", "u", "y", "G", "R", "X", "/", "?", "1", "9",
	} {
		key, ok := Stroke(Bind([]string{stroke}, stroke, "does something"))
		if !ok {
			t.Errorf("%q cannot be turned back into a keypress, so a click on it does nothing", stroke)
			continue
		}
		if got := key.String(); got != stroke {
			t.Errorf("%q comes back as %q, so a click on it would arrive as a different key", stroke, got)
		}
	}
	if _, ok := Stroke(Bind(nil, "", "nothing")); ok {
		t.Error("a binding with no strokes reported one")
	}
	if _, ok := Stroke(Bind([]string{"f13"}, "f13", "unknown")); ok {
		t.Error("a stroke this package does not spell was turned into a keypress anyway")
	}
}

// The three cells at the three sizes the goldens cover elsewhere, drawn by the
// kernel alone so that a change here is visible without a view to blame.
func TestFooter_Golden(t *testing.T) {
	for _, size := range []struct {
		label string
		w, h  int
	}{{"80x20", 80, 20}, {"100x28", 100, 28}, {"120x38", 120, 38}} {
		t.Run(size.label, func(t *testing.T) {
			resetRegistry()
			t.Cleanup(resetRegistry)
			RegisterView(actingSpec(acting(
				act("enter", "open"), act("e", "edit"), act("t", "status"),
				act("c", "comment"), act("/", "filter"), act("a", "all"), act("s", "save"),
			), 1))
			RegisterView(spec(PaletteViewID, 0, "", &stubView{id: PaletteViewID}))

			m := newAt(t, testDeps(), size.w, size.h)
			golden(t, "footer_"+size.label+".golden", lastLine(ansi.Strip(m.Frame()))+"\n")
		})
	}
}
