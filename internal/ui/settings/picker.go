package settings

import (
	"slices"
	"strconv"
	"strings"

	"charm.land/bubbles/v2/textinput"
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/varijkapil13/saral/internal/app"
	"github.com/varijkapil13/saral/internal/ui/kernel"
	"github.com/varijkapil13/saral/internal/ui/widget"
)

// pickerViewID scopes the generic picker's click zones. It registers no keys
// for the reason palette.projectModel registers none: it takes typing from
// the moment it opens.
const pickerViewID = "settings.picker"

const zoneOption = "opt:"

var (
	_ kernel.View        = (*pickerModel)(nil)
	_ kernel.KeyCapturer = (*pickerModel)(nil)
	_ kernel.KeyReporter = (*pickerModel)(nil)
)

// openOptionsPicker opens a small, local list over a KindChoice setting's own
// options — everything a scheme, or any future setting whose values do not
// come from the site, needs. It is not palette.projectModel's shape borrowed
// wholesale: there is no site to read and nothing to rank by frecency, so a
// plain fuzzy filter over Options is the whole of it, and app.Pattern is the
// one piece of that machinery worth reusing rather than rewriting.
func openOptionsPicker(d kernel.Deps, title string, opts []kernel.SettingOption, current string, apply func(kernel.Deps, string) tea.Cmd) tea.Cmd {
	return kernel.Push(pickerViewID, title, newPicker(d, opts, current, apply))
}

type pickerModel struct {
	deps kernel.Deps
	keys pickerKeys
	acts map[string]pickerAction

	input   textinput.Model
	opts    []kernel.SettingOption
	current string
	apply   func(kernel.Deps, string) tea.Cmd

	query string
	shown []int

	cursor, top   int
	width, height int

	styles     *pickerStyles
	memo       *rowCache
	lay        layout
	lines      []string
	head       string
	headAt     pickerHeadKey
	zonePrefix string
}

func newPicker(d kernel.Deps, opts []kernel.SettingOption, current string, apply func(kernel.Deps, string) tea.Cmd) *pickerModel {
	m := &pickerModel{
		deps:    d,
		keys:    defaultPickerKeys(),
		input:   newPickerInput(),
		opts:    opts,
		current: current,
		apply:   apply,
		memo:    newRowCache(rowMemoLimit),
	}
	if m.deps.Theme == nil {
		m.deps.Theme = kernel.NewTheme(kernel.ThemeAuto, true, kernel.UnicodeGlyphs())
	}
	if d.Zones != nil {
		m.zonePrefix = d.Zones.NewPrefix()
	}
	m.acts = m.keys.table()
	m.styles = newPickerStyles(m.deps.Theme)
	_ = m.input.Focus()
	m.refilter()
	return m
}

func newPickerInput() textinput.Model {
	ti := widget.NewInput()
	ti.Prompt = "> "
	ti.Placeholder = "which one?"
	return ti
}

func (m *pickerModel) WantsRawKeys() bool { return true }

func (m *pickerModel) Init() tea.Cmd { return nil }

func (m *pickerModel) Update(msg tea.Msg) (kernel.View, tea.Cmd) {
	var cmd tea.Cmd
	switch msg := msg.(type) {
	case kernel.SizeMsg:
		m.resize(msg.Width, msg.Height)
	case kernel.FocusMsg:
		if msg.Focused {
			_ = m.input.Focus()
		} else {
			m.input.Blur()
		}
	case kernel.ThemeMsg:
		m.deps.Theme = msg.Theme
		m.styles = newPickerStyles(msg.Theme)
		m.memo.reset()
		m.head = ""
	case tea.KeyPressMsg:
		cmd = m.key(msg)
	case tea.MouseClickMsg:
		cmd = m.click(msg)
	case tea.MouseWheelMsg:
		m.wheel(msg)
	}
	return m, cmd
}

func (m *pickerModel) resize(w, h int) {
	if w == m.width && h == m.height {
		return
	}
	m.width, m.height = w, h
	m.lay = planLayout(w)
	m.input.SetWidth(max(w-2, 8))
	m.memo.reset()
	m.head = ""
	m.clampScroll()
}

func (m *pickerModel) key(msg tea.KeyPressMsg) tea.Cmd {
	switch m.acts[msg.String()] {
	case pickUp:
		m.moveTo(m.cursor - 1)
		return nil
	case pickDown:
		m.moveTo(m.cursor + 1)
		return nil
	case pickPageUp:
		m.moveTo(m.cursor - m.rowsHeight())
		return nil
	case pickPageDown:
		m.moveTo(m.cursor + m.rowsHeight())
		return nil
	case pickChoose:
		return m.choose()
	case pickClose:
		return kernel.Pop()
	}
	m.input, _ = m.input.Update(msg)
	if q := m.input.Value(); q != m.query {
		m.query = q
		m.refilter()
	}
	return nil
}

func (m *pickerModel) choose() tea.Cmd {
	if len(m.shown) == 0 || m.apply == nil {
		return nil
	}
	o := m.opts[m.shown[m.cursor]]
	return tea.Sequence(kernel.Pop(), m.apply(m.deps, o.ID))
}

func (m *pickerModel) click(msg tea.MouseClickMsg) tea.Cmd {
	if msg.Button != tea.MouseLeft || m.deps.Zones == nil {
		return nil
	}
	for i := m.top; i < min(m.top+m.rowsHeight(), len(m.shown)); i++ {
		if !m.deps.Zones.Get(m.zone(m.shown[i])).InBounds(msg) {
			continue
		}
		if i == m.cursor {
			return m.choose()
		}
		m.moveTo(i)
		return nil
	}
	return nil
}

func (m *pickerModel) zone(at int) string {
	return m.zonePrefix + zoneOption + strconv.Itoa(at)
}

func (m *pickerModel) wheel(msg tea.MouseWheelMsg) {
	switch msg.Button {
	case tea.MouseWheelUp:
		m.top -= 3
	case tea.MouseWheelDown:
		m.top += 3
	default:
		return
	}
	m.clampScroll()
}

// refilter recomputes what the filter leaves, ranked by app.Pattern's score
// alone: there is no habit to weigh a tie by, since these lists are short and
// switched between rarely, so the registered order breaks one.
func (m *pickerModel) refilter() {
	m.shown = m.shown[:0]
	pattern := app.NewPattern(strings.TrimSpace(m.query))
	type ranked struct {
		at    int
		score int
	}
	ranks := make([]ranked, 0, len(m.opts))
	for i, o := range m.opts {
		score, ok := pattern.Score(o.Label)
		if !ok {
			continue
		}
		ranks = append(ranks, ranked{at: i, score: score})
	}
	slices.SortFunc(ranks, func(a, b ranked) int {
		if a.score != b.score {
			return b.score - a.score
		}
		return a.at - b.at
	})
	for _, r := range ranks {
		m.shown = append(m.shown, r.at)
	}
	m.cursor = 0
	m.scrollToCursor()
}

func (m *pickerModel) moveTo(at int) {
	if len(m.shown) == 0 {
		m.cursor, m.top = 0, 0
		return
	}
	m.cursor = clampInt(at, 0, len(m.shown)-1)
	m.scrollToCursor()
}

func (m *pickerModel) scrollToCursor() {
	h := m.rowsHeight()
	if m.cursor < m.top {
		m.top = m.cursor
	}
	if m.cursor >= m.top+h {
		m.top = m.cursor - h + 1
	}
	m.clampScroll()
}

func (m *pickerModel) clampScroll() {
	h := m.rowsHeight()
	m.top = clampInt(m.top, 0, max(len(m.shown)-h, 0))
}

func (m *pickerModel) rowsHeight() int { return max(m.height-headHeight, 1) }

const headHeight = 2

type pickerHeadKey struct {
	width, gen, shown, total int
	filtered                 bool
}

func (m *pickerModel) rule() string {
	key := pickerHeadKey{width: m.width, gen: m.styles.gen, shown: len(m.shown), total: len(m.opts), filtered: m.query != ""}
	if m.head != "" && key == m.headAt {
		return m.head
	}
	count := strconv.Itoa(key.total) + " options"
	if key.filtered {
		count = strconv.Itoa(key.shown) + " of " + strconv.Itoa(key.total)
	}
	dashes := max(m.width-ansi.StringWidth(count)-1, 0)
	m.head = m.styles.rule.Render(strings.Repeat(m.deps.Theme.Glyphs.HLine, dashes)) + " " + m.styles.muted.Render(count)
	m.headAt = key
	return m.head
}

func (m *pickerModel) row(at int) string {
	sel := at == m.cursor
	o := &m.opts[m.shown[at]]
	key := rowKey{id: o.ID, value: o.Label, sel: sel, gen: m.styles.gen, width: m.lay.width}
	s, ok := m.memo.get(key)
	var text string
	if ok {
		text = s.ctrl
	} else {
		text = renderOption(o, o.ID == m.current, m.lay, sel, m.styles, m.deps.Theme)
		m.memo.put(key, renderedRow{ctrl: text})
	}
	if m.deps.Zones != nil {
		text = m.deps.Zones.Mark(m.zone(m.shown[at]), text)
	}
	return text
}

func renderOption(o *kernel.SettingOption, current bool, lay layout, sel bool, st *pickerStyles, t *kernel.Theme) string {
	var b strings.Builder
	writeMarker(&b, sel, t)
	mark := "  "
	if current {
		mark = t.Glyphs.Check + " "
	}
	style := st.title
	if o.Style != nil {
		style = o.Style(t)
	}
	label := padTruncate(mark+o.Label, lay.width-marker-8, t.Glyphs.Ellipsis)
	if sel {
		b.WriteString(label)
	} else {
		b.WriteString(style.Render(label))
	}
	if o.Note != "" {
		b.WriteString("  ")
		note := padTruncate(o.Note, 8, t.Glyphs.Ellipsis)
		if sel {
			b.WriteString(note)
		} else {
			b.WriteString(st.muted.Render(note))
		}
	}
	if sel {
		return st.selected.Render(b.String())
	}
	return b.String()
}

func (m *pickerModel) View() string {
	if m.width <= 0 || m.height <= 0 {
		return ""
	}
	lines := m.lines[:0]
	lines = append(lines, m.input.View(), m.rule())
	h := m.rowsHeight()
	if len(m.shown) == 0 {
		ell := m.deps.Theme.Glyphs.Ellipsis
		lines = append(lines, m.styles.muted.Render(
			ansi.Truncate("  Nothing matches "+strconv.Quote(m.query)+".", m.width, ell)))
		for len(lines)-2 < h {
			lines = append(lines, "")
		}
	} else {
		end := min(m.top+h, len(m.shown))
		for i := m.top; i < end; i++ {
			lines = append(lines, m.row(i))
		}
		for i := end - m.top; i < h; i++ {
			lines = append(lines, "")
		}
	}
	m.lines = lines
	return strings.Join(lines, "\n")
}

type pickerStyles struct {
	gen                          int
	title, muted, rule, selected lipgloss.Style
}

func newPickerStyles(t *kernel.Theme) *pickerStyles {
	return &pickerStyles{
		gen:      t.Gen,
		title:    t.Base,
		muted:    t.Muted,
		rule:     t.Muted,
		selected: t.Selected,
	}
}

// pickerKeys is what the generic picker answers to — the same shape
// palette.projectModel's own keys are, since it takes typing from the moment
// it opens the same way.
type pickerKeys struct {
	Up, Down, PageUp, PageDown, Choose, Close kernel.Binding
}

func defaultPickerKeys() pickerKeys {
	return pickerKeys{
		Up:       kernel.Bind([]string{"up", "ctrl+p"}, "↑", "up"),
		Down:     kernel.Bind([]string{"down", "ctrl+n"}, "↓", "down"),
		PageUp:   kernel.Bind([]string{"pgup"}, "pgup", "page up"),
		PageDown: kernel.Bind([]string{"pgdown"}, "pgdn", "page down"),
		Choose:   kernel.Bind([]string{"enter"}, "enter", "choose it"),
		Close:    kernel.Bind([]string{"esc"}, "esc", "cancel"),
	}
}

type pickerAction uint8

const (
	pickNone pickerAction = iota
	pickUp
	pickDown
	pickPageUp
	pickPageDown
	pickChoose
	pickClose
)

func (k pickerKeys) table() map[string]pickerAction {
	entries := []struct {
		b kernel.Binding
		a pickerAction
	}{
		{k.Up, pickUp}, {k.Down, pickDown},
		{k.PageUp, pickPageUp}, {k.PageDown, pickPageDown},
		{k.Choose, pickChoose}, {k.Close, pickClose},
	}
	out := make(map[string]pickerAction, len(entries)*2)
	for _, e := range entries {
		if !e.b.Enabled() {
			continue
		}
		for _, stroke := range e.b.Keys() {
			out[stroke] = e.a
		}
	}
	return out
}

// pickerState is which of the picker's two states the keys belong to, and
// doubles as the generation the footer repaints on — the same shape
// palette.projectModel's own projectState is.
type pickerState int

const (
	pickerPicking pickerState = iota
	pickerNothing
	pickerStates
)

var pickerSets = func() [pickerStates]kernel.KeySet {
	k := defaultPickerKeys()
	var sets [pickerStates]kernel.KeySet
	sets[pickerPicking] = kernel.KeySet{
		Acts: []kernel.Binding{k.Choose, k.Close},
		Full: [][]kernel.Binding{
			{k.Down, k.Up, k.PageDown, k.PageUp},
			{k.Choose, k.Close},
			{widget.KillLine},
		},
	}
	sets[pickerNothing] = kernel.KeySet{
		Acts: []kernel.Binding{k.Close},
		Full: [][]kernel.Binding{{k.Close}, {widget.KillLine}},
	}
	return sets
}()

// LiveKeys reports the keys that work right now. A filter matching nothing
// has nothing to choose, and advertising enter there names a key that is
// refused.
func (m *pickerModel) LiveKeys() (set kernel.KeySet, gen int) {
	state := pickerPicking
	if len(m.shown) == 0 {
		state = pickerNothing
	}
	return pickerSets[state], int(state)
}
