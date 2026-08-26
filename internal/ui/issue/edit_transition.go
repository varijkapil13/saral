package issue

import (
	"context"
	"strings"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/varijkapil13/saral/internal/ui/kernel"
	"github.com/varijkapil13/saral/internal/ui/widget"
	"github.com/varijkapil13/saral/pkg/jira"
)

var (
	_ kernel.View        = (*moveModel)(nil)
	_ kernel.KeyCapturer = (*moveModel)(nil)
)

type moveStage uint8

const (
	moveList moveStage = iota
	moveScreen
	moveConfirm
	moveDoing
)

// moveModel is the transition picker.
//
// What it offers is read from the issue at the moment it is asked, never from a
// workflow held somewhere: which moves exist depends on the status the issue is
// in right now and on conditions the workflow evaluates against this issue. A
// move is made by its id, because the names are translated on a site that is
// not in English and the ids are not.
type moveModel struct {
	deps   kernel.Deps
	keys   moveKeyMap
	styles *editStyles

	issue  jira.Issue
	moves  []jira.Transition
	cursor int
	loaded bool

	stage  moveStage
	chosen int
	fields []moveField
	field  int

	fail string

	width, height int
	// top is the first move on screen: an issue with more transitions than the
	// terminal has rows is what the wheel is for. follow is set while the window
	// still has to catch up with a cursor that moved.
	top    int
	follow bool
	// reach is how far the last frame could scroll, which is what the wheel
	// clamps against so that a notch back always moves.
	reach int

	zones  widget.Zoner
	clicks *widget.Clicks

	gen    int
	cancel context.CancelFunc
}

// moveField is one required field of a transition screen. A screen field with
// no allowed values is one this pane cannot fill: there is nothing to choose
// from and no way to know what the site would accept.
type moveField struct {
	meta    jira.FieldMeta
	options []jira.Option
	chosen  int
}

func (f *moveField) fillable() bool { return len(f.options) > 0 }

func (f *moveField) value() jira.Option {
	if !f.fillable() {
		return jira.Option{}
	}
	return f.options[f.chosen]
}

func (f *moveField) name() string {
	if strings.TrimSpace(f.meta.Name) != "" {
		return f.meta.Name
	}
	return f.meta.Field.ID
}

// NewMove builds the transition picker for one issue.
func NewMove(d kernel.Deps, iss jira.Issue) kernel.View {
	m := &moveModel{deps: d, keys: defaultMoveKeys(), issue: iss}
	if m.deps.Theme == nil {
		m.deps.Theme = kernel.NewTheme(kernel.ThemeAuto, true, kernel.UnicodeGlyphs())
	}
	m.styles = newEditStyles(m.deps.Theme)
	m.zones = widget.NewZoner(d.Zones)
	m.clicks = widget.NewClicks(d.Now)
	return m
}

// WantsRawKeys is true once a move has been chosen: esc then means "back to the
// list of moves" rather than "close the pane", and the answer to a confirmation
// must not be read as a global.
func (m *moveModel) WantsRawKeys() bool {
	return m.stage == moveScreen || m.stage == moveConfirm
}

// Init reads what this issue can do right now.
func (m *moveModel) Init() tea.Cmd { return m.fetch() }

// Update handles one message.
func (m *moveModel) Update(msg tea.Msg) (kernel.View, tea.Cmd) {
	var cmd tea.Cmd
	switch msg := msg.(type) {
	case kernel.SizeMsg:
		m.width, m.height = msg.Width, msg.Height

	case kernel.ThemeMsg:
		m.deps.Theme = msg.Theme
		m.styles = newEditStyles(msg.Theme)

	case kernel.RefreshMsg:
		cmd = m.fetch()

	case movesLoadedMsg:
		if m.current(msg.gen) {
			m.moves, m.loaded, m.cursor = msg.moves, true, 0
			m.top, m.follow = 0, true
		}

	case moveDoneMsg:
		if m.current(msg.gen) {
			cmd = m.done()
		}

	case editFailedMsg:
		if m.current(msg.gen) {
			m.stage = moveList
			m.fail, _ = jira.Reason(msg.err)
			cmd = kernel.Fail(msg.err)
		}

	case tea.KeyPressMsg:
		cmd = m.key(msg)

	case tea.MouseClickMsg:
		cmd = m.click(msg)

	case tea.MouseWheelMsg:
		m.wheel(msg)
	}
	return m, cmd
}

func (m *moveModel) current(gen int) bool { return gen == m.gen }

func (m *moveModel) stop() {
	if m.cancel != nil {
		m.cancel()
		m.cancel = nil
	}
}

// Close lets go of the transitions read, and of a move that is still being
// applied. The picker is only ever pushed, so this is the whole of its life
// ending.
func (m *moveModel) Close() { m.stop() }

func (m *moveModel) fetch() tea.Cmd {
	if m.deps.Jira == nil || m.issue.Key == "" {
		return nil
	}
	m.stop()
	m.gen++
	ctx, cancel := context.WithCancel(context.Background())
	m.cancel = cancel
	return loadMoves(ctx, m.deps.Jira, m.issue.Key, m.gen)
}

func (m *moveModel) key(msg tea.KeyPressMsg) tea.Cmd {
	switch m.stage {
	case moveScreen:
		return m.screenKey(msg)
	case moveConfirm:
		return m.confirmKey(msg)
	case moveDoing:
		return nil
	case moveList:
	}
	switch {
	case kernel.Matches(msg, m.keys.Down):
		m.moveTo(m.cursor + 1)
	case kernel.Matches(msg, m.keys.Up):
		m.moveTo(m.cursor - 1)
	case kernel.Matches(msg, m.keys.Act):
		return m.choose()
	}
	return nil
}

func (m *moveModel) moveTo(at int) {
	if len(m.moves) == 0 {
		return
	}
	m.cursor = min(max(at, 0), len(m.moves)-1)
	m.follow = true
	m.fail = ""
}

// choose takes the move under the cursor and works out whether it can be made
// from here at all.
func (m *moveModel) choose() tea.Cmd {
	if m.cursor < 0 || m.cursor >= len(m.moves) {
		return nil
	}
	m.chosen, m.field, m.fail = m.cursor, 0, ""
	m.fields = requiredFields(m.moves[m.chosen])
	if len(m.fields) == 0 {
		m.stage = moveConfirm
		return nil
	}
	m.stage = moveScreen
	return nil
}

// requiredFields is what the transition screen insists on. An optional field is
// left alone: not filling one is a legitimate answer, and guessing a value for
// it is not.
func requiredFields(tr jira.Transition) []moveField {
	out := make([]moveField, 0, len(tr.Fields))
	for i := range tr.Fields {
		meta := tr.Fields[i]
		if !meta.Required {
			continue
		}
		out = append(out, moveField{meta: meta, options: meta.AllowedValues})
	}
	return out
}

func (m *moveModel) screenKey(msg tea.KeyPressMsg) tea.Cmd {
	switch {
	case kernel.Matches(msg, m.keys.Cancel):
		m.stage, m.fail = moveList, ""
		return nil
	case kernel.Matches(msg, m.keys.Down):
		m.field = min(m.field+1, len(m.fields)-1)
	case kernel.Matches(msg, m.keys.Up):
		m.field = max(m.field-1, 0)
	case kernel.Matches(msg, m.keys.Next):
		m.cycleField(1)
	case kernel.Matches(msg, m.keys.Prev):
		m.cycleField(-1)
	case kernel.Matches(msg, m.keys.Act):
		if reason, blocked := m.unfillable(); blocked {
			m.fail = reason
			return kernel.Warn(reason)
		}
		m.stage = moveConfirm
	}
	return nil
}

func (m *moveModel) cycleField(by int) {
	if m.field < 0 || m.field >= len(m.fields) {
		return
	}
	field := &m.fields[m.field]
	if !field.fillable() {
		return
	}
	field.chosen = (field.chosen + by + len(field.options)) % len(field.options)
}

// unfillable reports the required field this pane has no way to answer, which
// is the honest end of a transition that cannot be made from a terminal.
func (m *moveModel) unfillable() (string, bool) {
	for i := range m.fields {
		if m.fields[i].fillable() {
			continue
		}
		return "this move needs " + m.fields[i].name() +
			", and the site offered no values for it; make this one in the browser", true
	}
	return "", false
}

func (m *moveModel) confirmKey(msg tea.KeyPressMsg) tea.Cmd {
	if !kernel.Matches(msg, m.keys.Yes) {
		m.stage = moveList
		if len(m.fields) > 0 {
			m.stage = moveScreen
		}
		return nil
	}
	return m.apply()
}

func (m *moveModel) apply() tea.Cmd {
	if m.deps.Jira == nil || m.chosen < 0 || m.chosen >= len(m.moves) {
		return nil
	}
	move := m.moves[m.chosen]
	m.stage, m.fail = moveDoing, ""
	m.stop()
	m.gen++
	ctx, cancel := context.WithCancel(context.Background())
	m.cancel = cancel
	return applyMove(ctx, m.deps.Jira, m.issue.Key, move.ID, m.screenPatch(), m.gen)
}

// screenPatch carries the screen's answers by field id and option id, which is
// the only pair that means the same thing on a site in another language.
func (m *moveModel) screenPatch() jira.IssuePatch {
	values := make(map[string]jira.FieldValue, len(m.fields))
	for i := range m.fields {
		field := &m.fields[i]
		if !field.fillable() {
			continue
		}
		values[field.meta.Field.ID] = jira.FieldValue{Kind: jira.KindOption, Options: []jira.Option{field.value()}}
	}
	if len(values) == 0 {
		return jira.IssuePatch{}
	}
	return jira.IssuePatch{Fields: jira.NewFieldSet(values)}
}

func (m *moveModel) done() tea.Cmd {
	to := m.moves[m.chosen].To.Name
	return tea.Sequence(
		kernel.Pop(),
		kernel.Broadcast(kernel.RefreshMsg{}),
		kernel.Status(m.issue.Key+" is now "+to),
	)
}

func (m *moveModel) click(msg tea.MouseClickMsg) tea.Cmd {
	if msg.Button != tea.MouseLeft || m.stage != moveList {
		return nil
	}
	for i := range m.moves {
		zone := moveZone(m.moves[i].ID)
		if !m.zones.Hit(zone, msg) {
			continue
		}
		if m.clicks.Double(zone) {
			m.moveTo(i)
			return m.choose()
		}
		m.moveTo(i)
		return nil
	}
	return nil
}

// wheel scrolls the list of moves. There is nothing to scroll on a transition
// screen: it holds the fields that move needs and they all fit.
func (m *moveModel) wheel(msg tea.MouseWheelMsg) {
	if m.stage != moveList {
		return
	}
	switch msg.Button {
	case tea.MouseWheelUp:
		m.top -= widget.WheelStep
	case tea.MouseWheelDown:
		m.top += widget.WheelStep
	default:
	}
	m.top = min(max(m.top, 0), m.reach)
}

func moveZone(id string) string { return "move:" + id }

// View draws the moves, or the screen one of them needs.
func (m *moveModel) View() string {
	if m.width <= 0 || m.height <= 0 {
		return ""
	}
	t := m.deps.Theme
	lines := []string{m.styles.title.Render(ansi.Truncate("Move "+m.issue.Key+" from "+m.issue.Status.Name, max(m.width, 1), t.Glyphs.Ellipsis))}
	switch m.stage {
	case moveScreen, moveConfirm, moveDoing:
		lines = append(lines, m.screenLines()...)
	case moveList:
		tail := flatten(m.listTail())
		moves, under := m.moveLinesAndCursor()
		if !m.follow {
			under = -1
		}
		height := max(m.height-1-len(tail), 1)
		window, top := widget.Window(moves, m.top, height, under)
		m.top, m.follow, m.reach = top, false, max(len(moves)-height, 0)
		lines = append(lines, window...)
		lines = append(lines, tail...)
	}
	return strings.Join(fit(lines, m.height), "\n")
}

// moveLinesAndCursor draws the moves and says which line the one under the
// cursor is on, so that a cursor moved by a key comes back into view.
func (m *moveModel) moveLinesAndCursor() (lines []string, under int) {
	t := m.deps.Theme
	lines = make([]string, 0, len(m.moves)+1)
	switch {
	case !m.loaded:
		lines = append(lines, m.styles.muted.Render(indentWrap("reading what this issue can do"+t.Glyphs.Ellipsis, m.width)))
	case len(m.moves) == 0:
		lines = append(lines, m.styles.muted.Render(indentWrap("this issue has no move available to you right now", m.width)))
	}
	under = -1
	for i := range m.moves {
		if i == m.cursor {
			under = len(lines)
		}
		lines = append(lines, m.moveLine(i))
	}
	return lines, under
}

// listTail is what stays below the moves however far they are scrolled.
func (m *moveModel) listTail() []string {
	out := make([]string, 0, 3)
	out = append(out, "")
	if m.fail != "" {
		out = append(out, m.styles.fail.Render(wrapped(m.fail, m.width)))
	}
	return append(out, m.styles.muted.Render("enter chooses a move"))
}

func (m *moveModel) moveLine(at int) string {
	t := m.deps.Theme
	move := &m.moves[at]
	prefix := strings.Repeat(" ", editMarker)
	if at == m.cursor {
		prefix = m.styles.selected.Render(t.Glyphs.Arrow) + strings.Repeat(" ", editMarker-ansi.StringWidth(t.Glyphs.Arrow))
	}
	trailing := ""
	if move.HasScreen {
		trailing = "  " + t.Glyphs.Bullet + " has a screen"
	}
	room := max(m.width-editMarker, 8)
	body := ansi.Truncate(padTo(move.Name, editLabelWidth+8)+move.To.Name+trailing, room, t.Glyphs.Ellipsis)
	line := prefix + m.styles.value.Render(body)
	return m.zones.Mark(moveZone(move.ID), line)
}

func (m *moveModel) screenLines() []string {
	t := m.deps.Theme
	move := m.moves[m.chosen]
	out := []string{m.styles.muted.Render(indentWrap(move.Name+" "+t.Glyphs.Arrow+" "+move.To.Name, m.width))}
	for i := range m.fields {
		out = append(out, m.fieldLines(i)...)
	}
	out = append(out, "")
	if m.fail != "" {
		out = append(out, m.styles.fail.Render(wrapped(m.fail, m.width)))
	}
	switch m.stage {
	case moveDoing:
		return append(out, m.styles.muted.Render("moving "+m.issue.Key+t.Glyphs.Ellipsis))
	case moveConfirm:
		return append(out, m.styles.title.Render(wrapped(m.confirmQuestion(), m.width)),
			m.styles.muted.Render("y moves it, any other key goes back"))
	case moveList, moveScreen:
	}
	return append(out, m.styles.muted.Render("left and right choose a value, enter continues, esc goes back"))
}

func (m *moveModel) confirmQuestion() string {
	move := m.moves[m.chosen]
	question := "Move " + m.issue.Key + " from " + m.issue.Status.Name + " to " + move.To.Name + "?"
	for i := range m.fields {
		field := &m.fields[i]
		if !field.fillable() {
			continue
		}
		question += " " + field.name() + " will be " + field.value().Label + "."
	}
	return question
}

func (m *moveModel) fieldLines(at int) []string {
	t := m.deps.Theme
	field := &m.fields[at]
	prefix := strings.Repeat(" ", editMarker)
	if at == m.field && m.stage == moveScreen {
		prefix = m.styles.selected.Render(t.Glyphs.Arrow) + strings.Repeat(" ", editMarker-ansi.StringWidth(t.Glyphs.Arrow))
	}
	label := m.styles.label.Render(padTo(field.name()+" *", editLabelWidth))
	if !field.fillable() {
		return []string{
			prefix + label + m.styles.warn.Render("nothing to choose from"),
			m.styles.warn.Render(indentWrap("this move cannot be completed here: the site offered no values for "+field.name(), m.width)),
		}
	}
	room := max(m.width-editMarker-editLabelWidth, 8)
	return []string{prefix + label + m.styles.value.Render(ansi.Truncate(pickerLine(field.value().Label, t), room, t.Glyphs.Ellipsis))}
}
