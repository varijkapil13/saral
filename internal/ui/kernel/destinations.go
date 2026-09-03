package kernel

import (
	"strconv"
	"strings"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"
)

const destZone = "dest:"

// destMarker is the width of the cursor's gutter, in columns.
const destMarker = 2

const (
	destHere = "In this view"
	destNone = "no view in this build sits on a digit"
	destOn   = "on screen"
)

func (m Model) destTitle() string { return "Where " + m.keys.Go.Help().Key + " goes" }

// The keys the overlay adds to a latched prefix. They are not in GlobalKeys
// because they answer in one place only.
var (
	destUp     = Bind([]string{"up", "k"}, "up", "up")
	destDown   = Bind([]string{"down", "j"}, "up/down", "choose")
	destChoose = Bind([]string{"enter"}, "enter", "go there")
)

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

// latchPrefixMsg latches the go-to prefix without a keypress. It carries no
// stroke of its own, so a rebound prefix cannot leave the palette latching a key
// that no longer means this.
type latchPrefixMsg struct{}

func (m Model) latchPrefix() (tea.Model, tea.Cmd) {
	press, ok := Stroke(m.keys.Go)
	if !ok {
		return m, nil
	}
	// A view with the keyboard would be handed the keys the box advertises, so
	// there is nothing to draw here — the same answer pressing the prefix into a
	// field gets, said out loud because the palette entry was chosen on purpose.
	if m.capturing() {
		m.status, m.statusLevel = "this screen is taking typing; leave the field, then press "+
			m.keys.Go.Help().Key, LevelInfo
		return m, nil
	}
	return m.latch(press)
}

func (m Model) latch(press tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	m.prefix, m.prefixSet = press, true
	m.dest = m.destStart()
	return m, nil
}

type destination struct {
	slot      int
	title     string
	note      string
	here      bool
	reachable bool
}

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

// destKey leaves every key it does not answer to resolve the way it did before
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

// chooseDest spends the gesture on one row, through openSlot so that a row
// nothing can reach answers with the reason its digit gives.
func (m Model) chooseDest(at int) (tea.Model, tea.Cmd) {
	m.prefix, m.prefixSet = tea.KeyPressMsg{}, false
	dests := m.destinations()
	if at < 0 || at >= len(dests) {
		return m, nil
	}
	return m.openSlot(dests[at].slot)
}

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

func (m Model) destFooterActs() []Binding {
	return []Binding{
		Bind(m.keys.Slot.Keys(), "1-9", "switch view"),
		destDown,
		destChoose,
		Bind(m.keys.Back.Keys(), "esc", "cancel"),
	}
}

// viewGestures is what the focused view spends this same prefix on, taken from
// the keys it says work right now. A view spells a two-stroke gesture as the
// label of the binding it lands on — "g g" on a binding whose stroke is home —
// so the label is where the pair is recorded.
func (m Model) viewGestures() []Binding {
	set, _ := m.viewKeys()
	lead := m.keys.Go.Help().Key + " "
	out := make([]Binding, 0, 2)
	each := func(b Binding) {
		gesture, ok := gestureIn(b, lead)
		if !ok {
			return
		}
		for _, held := range out {
			if held.Help().Key == gesture {
				return
			}
		}
		if gesture == b.Help().Key {
			out = append(out, b)
			return
		}
		out = append(out, Bind(b.Keys(), gesture, b.Help().Desc))
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

// gestureIn is the gesture on this prefix a binding's label spells, if any. A
// binding answering to more than one thing lists them — "↑/k", "G / g e" — so
// the label is a list before it is a name.
func gestureIn(b Binding, lead string) (string, bool) {
	if !b.Enabled() {
		return "", false
	}
	label := b.Help().Key
	if strings.HasPrefix(label, lead) {
		return label, true
	}
	if !strings.Contains(label, "/") {
		return "", false
	}
	for alt := range strings.SplitSeq(label, "/") {
		if alt = strings.TrimSpace(alt); strings.HasPrefix(alt, lead) {
			return alt, true
		}
	}
	return "", false
}

type destRow struct {
	key, name, note, zone string
	// head marks a line that names the block under it rather than a row in it,
	// so it starts where the box does.
	head    bool
	dim, on bool
}

// destRows is the box's content in order. Nine slots and a title fit in the
// smallest terminal the program draws in, so the block that folds when the
// terminal is short is the one naming the view's own gestures, and no
// destination is ever dropped.
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
	// The five rows destView adds around this block: the title, the blank row and
	// the heading, and a line of border at each end of the box.
	left := height - len(rows) - 5
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
// It takes the whole body rather than floating over the view at the cursor,
// because a box spliced into the view's own lines would have to cut the strings
// carrying the zone markers a click is resolved through.
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
	// The border takes two columns and the style's padding two more.
	rowW := min(widest, max(w-4, lipgloss.Width(title)))

	lines := make([]string, 0, len(rows)+1)
	lines = append(lines, t.Muted.Render(title))
	for _, r := range rows {
		lines = append(lines, m.destLine(r, keyW, nameW, rowW))
	}
	return t.Overlay.Render(strings.Join(lines, "\n"))
}

func (m Model) destGutter(on bool) string {
	t := m.deps.Theme
	if !on {
		return strings.Repeat(" ", destMarker)
	}
	return t.HintKey.Render(t.Glyphs.Collapsed) +
		strings.Repeat(" ", max(destMarker-ansi.StringWidth(t.Glyphs.Collapsed), 0))
}

// destLine draws one row. It is cut rather than wrapped, because the box is
// sized by its widest row and a wrapped row's second line is outside the zone a
// click resolves through.
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
