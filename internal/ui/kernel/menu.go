package kernel

import (
	"strings"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"
)

// menuZone is the zone one entry of the menu is marked with, under the kernel's
// own prefix. A click there is delivered as the key that entry advertises, which
// is the rule the footer's actions already follow.
const menuZone = "menu:"

// The keys the menu answers to while it is up. They are not in GlobalKeys
// because they work in exactly one place and mean something else everywhere
// else: j and k are a view's own motions, and enter is whatever the view under
// this says it is.
var (
	menuUp     = Bind([]string{"up", "k"}, "up", "up")
	menuDown   = Bind([]string{"down", "j"}, "up/down", "choose")
	menuChoose = Bind([]string{"enter"}, "enter", "do")
	menuClose  = Bind([]string{"esc", "q"}, "esc", "close")
)

// menuState is the right-click menu: what could be done when it opened, and
// which entry the cursor is on.
//
// The entries are held rather than re-read every frame because the menu is a
// snapshot of one moment. The view under it hears nothing while it is up, so
// nothing can move — but a list rebuilt per frame would let an answer landing
// behind the menu slide a different action under the cursor between the frame a
// user read and the enter they pressed.
type menuState struct {
	open bool
	at   int
	acts []Binding
}

// menuActs is what the focused view says can be done to what it is showing,
// spelt out the way the ? overlay spells it rather than the way the footer's row
// has room for.
//
// It is the view's Acts and deliberately not the command registry:
// Command.Requires answers whether this token may do a thing on this site, and
// nothing on a Command says whether it applies to what is on screen, so a menu
// built from the registry would offer "set up a Jira profile" over an issue row.
func (m Model) menuActs() []Binding {
	set, _ := m.viewKeys()
	acts := set.Acts
	if len(acts) == 0 {
		acts = set.Short
	}
	if len(acts) == 0 {
		return nil
	}
	return spellOut(acts, set.Full)
}

// openMenu answers a right-click.
//
// The click is forwarded to the focused view first. Only the view can turn a
// coordinate into a row — it owns the zones, and the kernel has a frame — so a
// view that maps a right-click to selecting the row under the pointer makes the
// pointer and this menu agree about what it is for. No view does that yet, and
// until one does the menu is about the row the view draws highlighted, which
// docs/UX.md says in as many words.
func (m Model) openMenu(click tea.MouseClickMsg) (tea.Model, tea.Cmd) {
	// A view taking typing owns the keyboard, and this menu spends the arrows and
	// enter: opening it over a half-typed API token would eat the rest of it.
	if m.capturing() {
		return m.forwardTop(click)
	}
	if len(m.menuActs()) == 0 {
		m.status, m.statusLevel = "there is nothing to do to what is on this screen yet", LevelInfo
		return m, nil
	}
	told, cmd := m.forwardTop(click)
	next, ok := told.(Model)
	if !ok {
		return told, cmd
	}
	acts := next.menuActs()
	if len(acts) == 0 {
		return next, cmd
	}
	next.menu = menuState{open: true, acts: acts}
	return next, cmd
}

func (m Model) closeMenu() (tea.Model, tea.Cmd) {
	m.menu = menuState{}
	return m, nil
}

// chooseMenu runs one entry by handing the view the first stroke of the key that
// entry names, which is what a click on a footer action already does. One
// implementation of an action, reachable three ways.
func (m Model) chooseMenu(at int) (tea.Model, tea.Cmd) {
	if at < 0 || at >= len(m.menu.acts) {
		return m.closeMenu()
	}
	press, ok := Stroke(m.menu.acts[at])
	m.menu = menuState{}
	if !ok {
		// A stroke this cannot spell would arrive as its first rune, which is
		// some other key entirely.
		m.status, m.statusLevel = m.menuUnreachable(at), LevelWarn
		return m, nil
	}
	return m.handleKey(press)
}

func (m Model) menuUnreachable(at int) string {
	return m.menu.acts[at].Help().Desc + " has no key this can send, so it is only in the palette"
}

// menuKey handles a key while the menu is up, and swallows the rest: a key that
// reached the view under it would act on the very row this menu is about, from a
// screen the user cannot see.
func (m Model) menuKey(msg tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	n := len(m.menu.acts)
	if n == 0 {
		return m.closeMenu()
	}
	switch {
	case Matches(msg, menuUp):
		m.menu.at = (m.menu.at - 1 + n) % n
	case Matches(msg, menuDown):
		m.menu.at = (m.menu.at + 1) % n
	case Matches(msg, menuChoose):
		return m.chooseMenu(m.menu.at)
	case Matches(msg, menuClose):
		return m.closeMenu()
	}
	return m, nil
}

// menuMouse handles a click while the menu is up. A click on an entry runs it, a
// click anywhere else puts the menu away, and a wheel or a drag does nothing:
// what is under this is not being looked at.
func (m Model) menuMouse(msg tea.MouseMsg) (tea.Model, tea.Cmd) {
	click, ok := msg.(tea.MouseClickMsg)
	if !ok || !m.mouse || click.Button != tea.MouseLeft {
		return m, nil
	}
	for i, b := range m.menu.acts {
		if m.deps.Zones.Get(m.zonePrefix + menuZone + b.Help().Key).InBounds(click) {
			return m.chooseMenu(i)
		}
	}
	return m.closeMenu()
}

// menuFooterActs is the row while the menu is up. The kernel answers for itself
// here for the same reason it does under the ? overlay: it has taken the view's
// keys away, so the view's own inventory is not what works right now.
func menuFooterActs() []Binding {
	return []Binding{menuDown, menuChoose, menuClose}
}

// menuView draws the menu into the body.
//
// It takes the whole body, the way the ? overlay does, rather than floating over
// the view at the pointer. A box spliced into the view's own lines would have to
// cut strings that carry the zone markers a click is resolved through, and half
// the frame's mouse targets would stop answering — which is a worse trade than a
// menu that is not where the pointer is.
func (m Model) menuView() string {
	t := m.deps.Theme
	w, _ := m.bodySize()

	keyWidth := 0
	for _, b := range m.menu.acts {
		keyWidth = max(keyWidth, lipgloss.Width(b.Help().Key))
	}
	// The border takes two columns and the style's padding two more. A row longer
	// than what is left is cut rather than wrapped: the box is sized by its widest
	// row, and a wrapped description would make the box taller than the entries in
	// it and leave the second line unclickable.
	rowW := min(menuRowWidth(m.menu.acts, keyWidth), max(w-4, len(menuTitle)))

	rows := make([]string, 0, len(m.menu.acts)+1)
	rows = append(rows, t.Muted.Render(menuTitle))
	for i, b := range m.menu.acts {
		row := ansi.Truncate(cell(t.HintKey.Render(b.Help().Key), keyWidth+2)+b.Help().Desc,
			rowW, t.Glyphs.Ellipsis)
		if i == m.menu.at {
			row = t.Selected.Render(cell(row, rowW))
		}
		rows = append(rows, m.deps.Zones.Mark(m.zonePrefix+menuZone+b.Help().Key, row))
	}
	return t.Overlay.Render(strings.Join(rows, "\n"))
}

// menuTitle says what the list is, because a bare list of keys in a box is not
// self-evident and the entries themselves are the actions rather than a heading.
const menuTitle = "What can be done here"

func menuRowWidth(acts []Binding, keyWidth int) int {
	width := len(menuTitle)
	for _, b := range acts {
		width = max(width, keyWidth+2+lipgloss.Width(b.Help().Desc))
	}
	return width
}

// cell widens a string to exactly width columns, measuring what will be on the
// screen rather than the styling around it.
func cell(s string, width int) string {
	if gap := width - lipgloss.Width(s); gap > 0 {
		return s + strings.Repeat(" ", gap)
	}
	return s
}
