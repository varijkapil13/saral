package kernel

import (
	"strconv"
	"strings"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"
)

// destZone is the zone one destination is marked with, under the kernel's own
// prefix. A click there spends the gesture on that slot, which is exactly what
// pressing its digit does.
const destZone = "dest:"

// destMarker is the gutter the cursor's arrow sits in, so that which row enter
// would take is legible with no colour at all — the palette's own list has the
// same one for the same reason.
const destMarker = 2

// What the box says about itself. The list is not self-evident from a column of
// keys, and neither is the block under it: those are the strokes the focused
// view spends this same prefix on, named here so that a gesture this overlay
// covers is taught by it rather than hidden behind it.
const (
	destHere = "In this view"
	destNone = "no view in this build sits on a digit"
	destOn   = "on screen"
)

// destTitle says what the box is and which key it belongs to, spelt from the
// keymap the kernel is running rather than written down here.
func (m Model) destTitle() string { return "Where " + m.keys.Go.Help().Key + " goes" }

// The keys the overlay adds to a latched prefix. They are not in GlobalKeys
// because they work in exactly one place and mean something else everywhere
// else, which is why the right-click menu's are not either.
var (
	destUp     = Bind([]string{"up", "k"}, "up", "up")
	destDown   = Bind([]string{"down", "j"}, "up/down", "choose")
	destChoose = Bind([]string{"enter"}, "enter", "go there")
)

// The overlay has a palette entry as well as a key, because docs/UX.md asks for
// an action to be reachable three ways and because the palette is where somebody
// who has not learnt the prefix is already looking.
func init() { registerDestinationCommand() }

func registerDestinationCommand() {
	RegisterCommand(Command{
		ID:    "views.switch",
		Title: "Switch view",
		Group: "Go to",
		Keys:  []string{DefaultGlobalKeys().Slot.Help().Key},
		Run:   func(Deps) tea.Cmd { return func() tea.Msg { return latchPrefixMsg{} } },
	})
}

// latchPrefixMsg latches the go-to prefix without a keypress, which is how the
// palette entry opens the overlay the key opens. It carries no stroke of its own:
// the kernel spells the prefix from the keymap it is running, so a rebound prefix
// cannot leave the palette latching a key that no longer means this.
type latchPrefixMsg struct{}

// latchPrefix buffers the prefix the keymap binds. The stroke is spelt back
// rather than assumed, so a key handed to the view on the way out of the gesture
// is the one the user would have pressed to start it.
func (m Model) latchPrefix() (tea.Model, tea.Cmd) {
	press, ok := Stroke(m.keys.Go)
	if !ok {
		return m, nil
	}
	return m.latch(press)
}

// latch holds the prefix and opens the overlay on the view this session is in.
func (m Model) latch(press tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	m.prefix, m.prefixSet = press, true
	m.dest = m.destStart()
	return m, nil
}

// destination is one row: the slot's gesture, the view it reaches, and the note
// saying why it is not somewhere to go — the probe's own words for a view this
// token cannot reach, and that it is already up for the one on screen.
type destination struct {
	slot      int
	title     string
	note      string
	here      bool
	reachable bool
}

// destinations is every slot a view has claimed, in slot order, read off the
// registry rather than written down: RegisterView refuses a duplicate slot at
// startup, so what it holds is the whole allocation.
func (m Model) destinations() []destination {
	root := ""
	if len(m.stack) > 0 {
		root = m.stack[0].spec.ID
	}
	out := make([]destination, 0, len(m.roots))
	for _, spec := range m.roots {
		if spec.Slot <= 0 {
			continue
		}
		d := destination{slot: spec.Slot, title: spec.Title, reachable: true}
		if d.title == "" {
			d.title = spec.ID
		}
		switch {
		case !m.available(spec):
			d.note, d.reachable = m.unavailable(spec), false
		case spec.ID == root:
			d.note, d.here = destOn, true
		}
		out = append(out, d)
	}
	return out
}

// destStart is where the cursor opens: on the view this session is in, so that
// the overlay says which row you are on by putting the cursor there, and on the
// first reachable row when the root holds no digit at all.
func (m Model) destStart() int {
	dests := m.destinations()
	for i, d := range dests {
		if d.here {
			return i
		}
	}
	for i, d := range dests {
		if d.reachable {
			return i
		}
	}
	return -1
}

// destAt is the row a motion lands on, skipping the ones nothing can reach: a
// view whose capability is absent is an answer with a reason attached rather
// than a place the cursor may rest.
func (m Model) destAt(step int) int {
	dests := m.destinations()
	n := len(dests)
	if n == 0 {
		return -1
	}
	at := m.dest
	for range n {
		at = (at + step + n) % n
		if dests[at].reachable {
			return at
		}
	}
	return m.dest
}

// destKey answers the three keys the overlay adds to a latched prefix: the two
// that move its cursor, which leave the gesture latched, and the one that spends
// it on the row under the cursor. Everything else resolves the way it did before
// this overlay existed, so the prefix stays a pass-through rather than a mode.
func (m Model) destKey(msg tea.KeyPressMsg) (tea.Model, tea.Cmd, bool) {
	switch {
	case Matches(msg, destUp):
		m.dest = m.destAt(-1)
	case Matches(msg, destDown):
		m.dest = m.destAt(1)
	case Matches(msg, destChoose):
		return withHit(m.chooseDest(m.dest))
	default:
		return m, nil, false
	}
	return m, nil, true
}

// chooseDest spends the gesture on one row. It goes through openSlot, so a row
// nothing can reach answers with the reason rather than with nothing — the same
// answer its digit gives.
func (m Model) chooseDest(at int) (tea.Model, tea.Cmd) {
	m.prefix, m.prefixSet = tea.KeyPressMsg{}, false
	dests := m.destinations()
	if at < 0 || at >= len(dests) {
		return m, nil
	}
	return m.openSlot(dests[at].slot)
}

// destMouse handles a click while the overlay is up. A click on a row spends the
// gesture on it, a click anywhere else throws the gesture away, and a wheel or a
// drag does nothing: what is under this is not being looked at.
func (m Model) destMouse(msg tea.MouseMsg) (tea.Model, tea.Cmd) {
	click, ok := msg.(tea.MouseClickMsg)
	if !ok || !m.mouse || click.Button != tea.MouseLeft {
		return m, nil
	}
	for i, d := range m.destinations() {
		if m.deps.Zones.Get(m.zonePrefix + destZone + strconv.Itoa(d.slot)).InBounds(click) {
			return m.chooseDest(i)
		}
	}
	m.prefix, m.prefixSet = tea.KeyPressMsg{}, false
	return m, nil
}

// destFooterActs is the row while the overlay is up. The kernel answers for
// itself here for the reason it does under the ? overlay and the menu: it is
// holding the keys, so the view's own inventory is not what works right now.
func (m Model) destFooterActs() []Binding {
	return []Binding{
		Bind(m.keys.Slot.Keys(), "1-9", "switch view"),
		destDown,
		destChoose,
		Bind(m.keys.Back.Keys(), "esc", "cancel"),
	}
}

// viewGestures is what the focused view spends this same prefix on, taken from
// the keys it says work right now rather than written down here. A view spells a
// two-stroke gesture as the label of the binding it lands on — "g g" on a
// binding whose stroke is home — so the label is where the pair is recorded.
func (m Model) viewGestures() []Binding {
	set, _ := m.viewKeys()
	lead := m.keys.Go.Help().Key + " "
	out := make([]Binding, 0, 2)
	each := func(b Binding) {
		if !b.Enabled() || !strings.HasPrefix(b.Help().Key, lead) {
			return
		}
		for _, held := range out {
			if held.Help().Key == b.Help().Key {
				return
			}
		}
		out = append(out, b)
	}
	for _, b := range set.Acts {
		each(b)
	}
	for _, b := range set.Short {
		each(b)
	}
	for _, column := range set.Full {
		for _, b := range column {
			each(b)
		}
	}
	return out
}

// destRow is one line of the box before it is measured: what to press, what that
// reaches, the note beside it, and the zone a click on it resolves through.
type destRow struct {
	key, name, note, zone string
	// head marks the line that names the block under the destinations. It is a
	// heading rather than a row, so it starts where the box does.
	head    bool
	dim, on bool
}

// destRows is the box's content in order. The nine slots and the title always
// fit — there are nine digits and the body is at least fifteen rows at the
// documented minimum — so the block that folds when the terminal is short is the
// one naming the view's own gestures, and no destination is ever dropped.
func (m Model) destRows(height int) []destRow {
	dests := m.destinations()
	rows := make([]destRow, 0, len(dests)+4)
	for i, d := range dests {
		rows = append(rows, destRow{
			key:  slotGesture(m.keys, d.slot),
			name: d.title,
			note: d.note,
			zone: destZone + strconv.Itoa(d.slot),
			dim:  !d.reachable,
			on:   i == m.dest,
		})
	}
	gestures := m.viewGestures()
	// Two rows for the blank one and the heading, and one line of border at each
	// end of the box.
	left := height - 2 - len(rows) - 2
	if len(gestures) == 0 || left <= 0 {
		return rows
	}
	shown := len(gestures)
	if shown > left {
		shown = max(left-1, 0)
	}
	rows = append(rows, destRow{}, destRow{name: destHere, head: true, dim: true})
	for _, b := range gestures[:shown] {
		rows = append(rows, destRow{key: b.Help().Key, name: b.Help().Desc, dim: true})
	}
	if rest := len(gestures) - shown; rest > 0 {
		rows = append(rows, destRow{name: "+" + strconv.Itoa(rest) + " more", head: true, dim: true})
	}
	return rows
}

// destView draws the destinations into the body while the prefix is latched.
//
// It takes the whole body, the way the ? overlay and the right-click menu do,
// rather than floating over the view at the cursor: a box spliced into the
// view's own lines would have to cut the strings carrying the zone markers a
// click is resolved through, and half the frame's mouse targets would stop
// answering.
func (m Model) destView() string {
	t := m.deps.Theme
	w, h := m.bodySize()
	rows := m.destRows(h)
	title := m.destTitle()
	if len(rows) == 0 {
		return t.Overlay.Render(t.Muted.Render(title) + "\n" + t.Muted.Render(destNone))
	}

	keyW, nameW, widest := 0, 0, lipgloss.Width(title)
	for _, r := range rows {
		if r.head {
			continue
		}
		keyW = max(keyW, lipgloss.Width(r.key))
		nameW = max(nameW, lipgloss.Width(r.name))
	}
	for _, r := range rows {
		width := lipgloss.Width(r.name)
		switch {
		case r.head:
		case r.note != "":
			width = destMarker + keyW + 2 + nameW + 2 + lipgloss.Width(r.note)
		default:
			width = destMarker + keyW + 2 + width
		}
		widest = max(widest, width)
	}
	// The border takes two columns and the style's padding two more. A row longer
	// than what is left is cut rather than wrapped, because the box is sized by
	// its widest row and a wrapped row's second line is outside the zone a click
	// resolves through.
	rowW := min(widest, max(w-4, lipgloss.Width(title)))

	lines := make([]string, 0, len(rows)+1)
	lines = append(lines, t.Muted.Render(title))
	for _, r := range rows {
		lines = append(lines, m.destLine(r, keyW, nameW, rowW))
	}
	return t.Overlay.Render(strings.Join(lines, "\n"))
}

// destGutter is the cursor's own column: the glyph on the row enter would spend
// the gesture on, and spaces on every other.
func (m Model) destGutter(on bool) string {
	t := m.deps.Theme
	if !on {
		return strings.Repeat(" ", destMarker)
	}
	return t.HintKey.Render(t.Glyphs.Collapsed) +
		strings.Repeat(" ", max(destMarker-ansi.StringWidth(t.Glyphs.Collapsed), 0))
}

func (m Model) destLine(r destRow, keyW, nameW, rowW int) string {
	t := m.deps.Theme
	if r.key == "" && r.name == "" {
		return ""
	}
	name := r.name
	if r.dim {
		name = t.Muted.Render(r.name)
	}
	if r.head {
		return ansi.Truncate(name, rowW, t.Glyphs.Ellipsis)
	}
	line := m.destGutter(r.on) + cell(t.HintKey.Render(r.key), keyW+2) + name
	if r.note != "" {
		line = cell(line, destMarker+keyW+2+nameW+2) + t.Muted.Render(r.note)
	}
	line = ansi.Truncate(line, rowW, t.Glyphs.Ellipsis)
	if r.on {
		line = t.Selected.Render(cell(line, rowW))
	}
	if r.zone == "" {
		return line
	}
	return m.deps.Zones.Mark(m.zonePrefix+r.zone, line)
}
