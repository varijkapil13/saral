package settings

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strconv"
	"strings"

	"charm.land/bubbles/v2/textinput"
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/varijkapil13/saral/internal/app"
	"github.com/varijkapil13/saral/internal/config"
	"github.com/varijkapil13/saral/internal/ui/kernel"
	"github.com/varijkapil13/saral/internal/ui/widget"
	"github.com/varijkapil13/saral/pkg/jira"
)

const issueSection = "Issue"

func init() { kernel.RegisterSetting(pinnedFieldsSetting()) }

// pinnedFieldsSetting is issue.pinned: which of this site's fields the sidebar
// draws first, in the order they were pinned. Its Options and Set exist only
// to satisfy RegisterSetting's KindChoice contract — the row always opens
// fieldPickerModel rather than applying a value picked from Options — the same
// shape session.project already answers a picker-only row in.
func pinnedFieldsSetting() kernel.Setting {
	return kernel.Setting{
		ID:      "issue.pinned",
		Section: issueSection,
		Title:   "Pinned fields",
		Summary: "which of this site's fields draw first in the issue sidebar, and in the order they were pinned",
		Kind:    kernel.KindChoice,
		Scope:   kernel.ScopeProfile,
		Options: func(kernel.Deps) []kernel.SettingOption {
			v := pinnedSummary()
			return []kernel.SettingOption{{ID: v, Label: v}}
		},
		Value: func(kernel.Deps) string { return pinnedSummary() },
		Set:   func(kernel.Deps, string) tea.Cmd { return nil },
		OpenPicker: func(d kernel.Deps) tea.Cmd {
			return kernel.Push(fieldPickerViewID, "Pinned fields", newFieldPicker(d))
		},
	}
}

func pinnedSummary() string {
	switch n := len(readProfile().pinned); n {
	case 0:
		return "none pinned"
	case 1:
		return "1 field"
	default:
		return strconv.Itoa(n) + " fields"
	}
}

// savePinned writes the pinned list into the profile it came from, the same
// shape saveTheme already answers a scoped write in.
func savePinned(site string, ids []string) tea.Cmd {
	return func() tea.Msg {
		switch err := writePinned(site, ids); {
		case err == nil:
			return nil
		case errors.Is(err, config.ErrNoConfig), errors.Is(err, config.ErrNoProfile):
			return kernel.StatusMsg{
				Text:  "the pinned fields changed for this session; there is no profile to save them to",
				Level: kernel.LevelInfo,
			}
		default:
			return kernel.StatusMsg{
				Text:  "the pinned fields changed for this session, but saving them failed: " + err.Error(),
				Level: kernel.LevelWarn,
			}
		}
	}
}

// writePinned reads the whole file and writes it back with one field changed,
// for the reason writeTheme already does: Save writes the profile it is
// handed and nothing else, so a fresh Profile built from what is on screen
// would drop the timeline field names and the saved queries.
func writePinned(site string, ids []string) error {
	path, err := config.Path()
	if err != nil {
		return err
	}
	cfg, err := config.LoadFile(path)
	if err != nil {
		return err
	}
	profile, err := cfg.Current()
	if err != nil {
		return err
	}
	// The kernel is told which site it is talking to and never which profile
	// was named on the command line, so a session started with --profile would
	// otherwise write the choice onto whichever profile is active instead.
	if site != "" && profile.Site != site {
		return fmt.Errorf("this session is on %s and the active profile %q is on %s, so nothing was written",
			site, profile.Name, profile.Site)
	}
	if slices.Equal(profile.Pinned, ids) {
		return nil
	}
	profile.Pinned = ids
	cfg.Profiles[profile.Name] = profile
	return cfg.Save(path)
}

// fieldPickerViewID scopes the picker's click zones.
const fieldPickerViewID = "settings.fields"

const zoneField = "fld:"

var (
	_ kernel.View        = (*fieldPickerModel)(nil)
	_ kernel.KeyCapturer = (*fieldPickerModel)(nil)
	_ kernel.KeyReporter = (*fieldPickerModel)(nil)
	_ kernel.Addressed   = (*fieldPickerModel)(nil)
	_ kernel.Closer      = (*fieldPickerModel)(nil)
)

// fieldRow is one field on offer: the id pinning writes and the name to draw.
type fieldRow struct{ id, label string }

// fieldsFoundMsg carries the site's field catalogue.
type fieldsFoundMsg struct{ fields []jira.Field }

// fieldsFailedMsg is a read that brought nothing back.
type fieldsFailedMsg struct{ err error }

// fieldPickerModel is the multi-select picker behind the "Pinned fields" row:
// a fuzzy filter over the site's field catalogue, enter toggles the field
// under the cursor on or off and the picker stays open — the same "pick any
// number, then leave" shape filter.Model's own value screen answers to since
// it went multi-select — and esc writes the accumulated list to the profile
// in one save rather than one per toggle, because nothing here has rows of
// its own to narrow live the way a search does.
//
// Reusing filter.Model itself was the first thing tried: its picker is wired
// to filter.Facet — a fixed enum of assignee/reporter/status/type/priority/
// label, each fetched as JQL vocabulary — and a field catalogue is neither a
// facet nor a vocabulary of values for one; offering it would mean adding a
// seventh facet and a new fetch to a package this packet does not own. This
// is the smaller thing that works instead.
type fieldPickerModel struct {
	deps kernel.Deps
	addr kernel.Addr
	keys fieldKeys
	acts map[string]fieldAction

	input textinput.Model
	query string

	rows  []fieldRow
	shown []int

	// pinned is the working copy, in pin order. It starts as the profile's own
	// list and is only ever written back on close, so a picker opened and left
	// with esc's cousin — closing the whole session — never leaves a half
	// finished edit on disk.
	pinned []string

	loading bool
	problem string
	cancel  context.CancelFunc

	cursor, top   int
	width, height int

	styles     *fieldPickerStyles
	lines      []string
	head       string
	headAt     fieldHeadKey
	zonePrefix string
}

func newFieldPicker(d kernel.Deps) kernel.View {
	m := &fieldPickerModel{
		deps:   d,
		addr:   kernel.NewAddr(),
		keys:   defaultFieldKeys(),
		input:  newFieldInput(),
		pinned: append([]string(nil), readProfile().pinned...),
	}
	if m.deps.Theme == nil {
		m.deps.Theme = kernel.NewTheme(kernel.ThemeAuto, true, kernel.UnicodeGlyphs())
	}
	if d.Zones != nil {
		m.zonePrefix = d.Zones.NewPrefix()
	}
	m.acts = m.keys.table()
	m.styles = newFieldPickerStyles(m.deps.Theme)
	_ = m.input.Focus()
	return m
}

func newFieldInput() textinput.Model {
	ti := widget.NewInput()
	ti.Prompt = "> "
	ti.Placeholder = "which field?"
	return ti
}

func (m *fieldPickerModel) Addr() kernel.Addr { return m.addr }

func (m *fieldPickerModel) WantsRawKeys() bool { return true }

func (m *fieldPickerModel) Init() tea.Cmd { return m.fetch() }

func (m *fieldPickerModel) Close() { m.stop() }

func (m *fieldPickerModel) stop() {
	if m.cancel != nil {
		m.cancel()
		m.cancel = nil
	}
	m.loading = false
}

func (m *fieldPickerModel) fetch() tea.Cmd {
	if m.deps.Jira == nil {
		m.problem = "there is no Jira connection in this session"
		return nil
	}
	m.stop()
	search := app.NewSearch(m.deps.Jira)
	ctx, cancel := context.WithCancel(context.Background())
	m.cancel, m.loading, m.problem = cancel, true, ""
	return kernel.Reply(func() tea.Msg {
		defer cancel()
		fields, err := search.Fields(ctx)
		if err != nil {
			return fieldsFailedMsg{err: err}
		}
		return fieldsFoundMsg{fields: fields}
	}, m.addr)
}

func (m *fieldPickerModel) Update(msg tea.Msg) (kernel.View, tea.Cmd) {
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
		m.styles = newFieldPickerStyles(msg.Theme)
		m.head = ""

	case fieldsFoundMsg:
		m.landed(msg.fields)

	case fieldsFailedMsg:
		m.stop()
		text, _ := jira.Reason(msg.err)
		m.problem = text
		cmd = kernel.Warn(text)

	case tea.KeyPressMsg:
		cmd = m.key(msg)

	case tea.MouseClickMsg:
		cmd = m.click(msg)

	case tea.MouseWheelMsg:
		m.wheel(msg)
	}
	return m, cmd
}

// landed keeps every field with an id, named the way this site spells it, and
// sorted by that name — the only order there is before anything is pinned.
func (m *fieldPickerModel) landed(fields []jira.Field) {
	m.stop()
	m.rows = make([]fieldRow, 0, len(fields))
	for i := range fields {
		f := &fields[i]
		if f.ID == "" {
			continue
		}
		label := f.Name
		if strings.TrimSpace(label) == "" {
			label = f.ID
		}
		m.rows = append(m.rows, fieldRow{id: f.ID, label: label})
	}
	slices.SortFunc(m.rows, func(a, b fieldRow) int { return strings.Compare(a.label, b.label) })
	m.head = ""
	m.refilter()
}

func (m *fieldPickerModel) resize(w, h int) {
	if w == m.width && h == m.height {
		return
	}
	m.width, m.height = w, h
	m.input.SetWidth(max(w-2, 8))
	m.head = ""
	m.clampScroll()
}

func (m *fieldPickerModel) key(msg tea.KeyPressMsg) tea.Cmd {
	switch m.acts[msg.String()] {
	case fieldUp:
		m.moveTo(m.cursor - 1)
		return nil
	case fieldDown:
		m.moveTo(m.cursor + 1)
		return nil
	case fieldPageUp:
		m.moveTo(m.cursor - m.rowsHeight())
		return nil
	case fieldPageDown:
		m.moveTo(m.cursor + m.rowsHeight())
		return nil
	case fieldToggle:
		m.toggle()
		return nil
	case fieldClose:
		return m.close()
	case fieldNone:
	}
	m.input, _ = m.input.Update(msg)
	if q := m.input.Value(); q != m.query {
		m.query = q
		m.refilter()
	}
	return nil
}

// toggle pins the field under the cursor, at the end of the working list, or
// unpins it if it was already there — the picker stays open either way, so a
// second field costs another enter rather than a fresh trip through settings.
func (m *fieldPickerModel) toggle() {
	if m.cursor < 0 || m.cursor >= len(m.shown) {
		return
	}
	id := m.rows[m.shown[m.cursor]].id
	if at := slices.Index(m.pinned, id); at >= 0 {
		m.pinned = slices.Delete(m.pinned, at, at+1)
	} else {
		m.pinned = append(m.pinned, id)
	}
	m.head = ""
}

// close writes the working list to the profile it opened over and pops, in
// that order so the status line ends up naming what actually happened rather
// than being overwritten by the pop.
func (m *fieldPickerModel) close() tea.Cmd {
	return tea.Sequence(kernel.Pop(), savePinned(m.deps.Site, m.pinned))
}

func (m *fieldPickerModel) click(msg tea.MouseClickMsg) tea.Cmd {
	if msg.Button != tea.MouseLeft || m.deps.Zones == nil {
		return nil
	}
	for i := m.top; i < min(m.top+m.rowsHeight(), len(m.shown)); i++ {
		if !m.deps.Zones.Get(m.zone(m.shown[i])).InBounds(msg) {
			continue
		}
		if i == m.cursor {
			m.toggle()
			return nil
		}
		m.moveTo(i)
		return nil
	}
	return nil
}

func (m *fieldPickerModel) zone(at int) string { return m.zonePrefix + zoneField + strconv.Itoa(at) }

func (m *fieldPickerModel) wheel(msg tea.MouseWheelMsg) {
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

// refilter recomputes what the typed pattern leaves, ranked by app.Pattern's
// score alone the way the generic options picker already is: these lists are
// switched between rarely, so there is no habit worth weighing a tie by.
func (m *fieldPickerModel) refilter() {
	under := m.underCursor()
	m.shown = m.shown[:0]
	pattern := app.NewPattern(strings.TrimSpace(m.query))
	type ranked struct{ at, score int }
	ranks := make([]ranked, 0, len(m.rows))
	for i, r := range m.rows {
		score, ok := pattern.Score(r.label)
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
	if under != "" {
		for i, at := range m.shown {
			if m.rows[at].id == under {
				m.cursor = i
				break
			}
		}
	}
	m.scrollToCursor()
}

func (m *fieldPickerModel) underCursor() string {
	if m.cursor < 0 || m.cursor >= len(m.shown) {
		return ""
	}
	return m.rows[m.shown[m.cursor]].id
}

func (m *fieldPickerModel) moveTo(at int) {
	if len(m.shown) == 0 {
		m.cursor, m.top = 0, 0
		return
	}
	m.cursor = clampInt(at, 0, len(m.shown)-1)
	m.scrollToCursor()
}

func (m *fieldPickerModel) scrollToCursor() {
	h := m.rowsHeight()
	if m.cursor < m.top {
		m.top = m.cursor
	}
	if m.cursor >= m.top+h {
		m.top = m.cursor - h + 1
	}
	m.clampScroll()
}

func (m *fieldPickerModel) clampScroll() {
	h := m.rowsHeight()
	m.top = clampInt(m.top, 0, max(len(m.shown)-h, 0))
}

func (m *fieldPickerModel) rowsHeight() int { return max(m.height-headHeight, 1) }

func (m *fieldPickerModel) row(at int) string {
	sel := at == m.cursor
	r := &m.rows[m.shown[at]]
	mark := "[ ] "
	if pin := slices.Index(m.pinned, r.id); pin >= 0 {
		mark = "[" + strconv.Itoa(pin+1) + "] "
	}
	var b strings.Builder
	writeMarker(&b, sel, m.deps.Theme)
	text := padTruncate(mark+r.label, m.width-marker, m.deps.Theme.Glyphs.Ellipsis)
	if sel {
		b.WriteString(text)
	} else {
		b.WriteString(m.styles.title.Render(text))
	}
	s := b.String()
	if sel {
		s = m.styles.selected.Render(s)
	}
	if m.deps.Zones != nil {
		s = m.deps.Zones.Mark(m.zone(m.shown[at]), s)
	}
	return s
}

// fieldHeadKey is everything the rule line is built from, so it repaints only
// when one of them moves.
type fieldHeadKey struct {
	width, gen, shown, total, pinned int
	filtered                         bool
}

func (m *fieldPickerModel) rule() string {
	key := fieldHeadKey{
		width: m.width, gen: m.styles.gen, shown: len(m.shown), total: len(m.rows),
		filtered: m.query != "", pinned: len(m.pinned),
	}
	if m.head != "" && key == m.headAt {
		return m.head
	}
	count := strconv.Itoa(key.total) + " fields"
	if key.filtered {
		count = strconv.Itoa(key.shown) + " of " + strconv.Itoa(key.total)
	}
	count += ", " + strconv.Itoa(key.pinned) + " pinned"
	dashes := max(m.width-ansi.StringWidth(count)-1, 0)
	m.head = m.styles.rule.Render(strings.Repeat(m.deps.Theme.Glyphs.HLine, dashes)) + " " + m.styles.muted.Render(count)
	m.headAt = key
	return m.head
}

func (m *fieldPickerModel) View() string {
	if m.width <= 0 || m.height <= 0 {
		return ""
	}
	lines := m.lines[:0]
	lines = append(lines, m.input.View(), m.rule())
	h := m.rowsHeight()
	switch {
	case m.loading && len(m.rows) == 0:
		lines = append(lines, m.styles.muted.Render("  Reading this site's fields"+m.deps.Theme.Glyphs.Ellipsis))
		for len(lines)-2 < h {
			lines = append(lines, "")
		}
	case len(m.shown) == 0:
		lines = m.appendEmpty(lines, h)
	default:
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

func (m *fieldPickerModel) appendEmpty(lines []string, h int) []string {
	at := len(lines)
	ell := m.deps.Theme.Glyphs.Ellipsis
	text := "  Nothing matches " + strconv.Quote(m.query) + "."
	if len(m.rows) == 0 && !m.loading {
		text = "  This site has no fields to pin."
	}
	lines = append(lines, m.styles.muted.Render(ansi.Truncate(text, m.width, ell)))
	if m.problem != "" {
		lines = append(lines, "  "+m.styles.muted.Render(ansi.Truncate(m.problem, max(m.width-marker, 8), ell)))
	}
	for len(lines)-at < h {
		lines = append(lines, "")
	}
	return lines[:at+h]
}

type fieldPickerStyles struct {
	gen                          int
	title, muted, rule, selected lipgloss.Style
}

func newFieldPickerStyles(t *kernel.Theme) *fieldPickerStyles {
	return &fieldPickerStyles{gen: t.Gen, title: t.Base, muted: t.Muted, rule: t.Muted, selected: t.Selected}
}

// fieldKeys is what the picker answers to. Every letter goes into the filter,
// so moving the selection is arrows and their control-key twins and nothing
// else, the same shape the generic options picker's own keys are.
type fieldKeys struct {
	Up, Down, PageUp, PageDown, Toggle, Close kernel.Binding
}

func defaultFieldKeys() fieldKeys {
	return fieldKeys{
		Up:       kernel.Bind([]string{"up", "ctrl+p"}, "↑", "up"),
		Down:     kernel.Bind([]string{"down", "ctrl+n"}, "↓", "down"),
		PageUp:   kernel.Bind([]string{"pgup"}, "pgup", "page up"),
		PageDown: kernel.Bind([]string{"pgdown"}, "pgdn", "page down"),
		Toggle:   kernel.Bind([]string{"enter"}, "enter", "pin or unpin it"),
		Close:    kernel.Bind([]string{"esc"}, "esc", "done"),
	}
}

type fieldAction uint8

const (
	fieldNone fieldAction = iota
	fieldUp
	fieldDown
	fieldPageUp
	fieldPageDown
	fieldToggle
	fieldClose
)

func (k fieldKeys) table() map[string]fieldAction {
	entries := []struct {
		b kernel.Binding
		a fieldAction
	}{
		{k.Up, fieldUp}, {k.Down, fieldDown},
		{k.PageUp, fieldPageUp}, {k.PageDown, fieldPageDown},
		{k.Toggle, fieldToggle}, {k.Close, fieldClose},
	}
	out := make(map[string]fieldAction, len(entries)*2)
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

// fieldPickerState is which of the picker's states the keys belong to, and
// doubles as the generation the footer repaints on.
type fieldPickerState int

const (
	fieldPicking fieldPickerState = iota
	fieldNothing
	fieldStates
)

var fieldSets = func() [fieldStates]kernel.KeySet {
	k := defaultFieldKeys()
	var sets [fieldStates]kernel.KeySet
	sets[fieldPicking] = kernel.KeySet{
		Acts: []kernel.Binding{k.Toggle, k.Close},
		Full: [][]kernel.Binding{
			{k.Down, k.Up, k.PageDown, k.PageUp},
			{k.Toggle, k.Close},
		},
	}
	sets[fieldNothing] = kernel.KeySet{
		Acts: []kernel.Binding{k.Close},
		Full: [][]kernel.Binding{{k.Close}},
	}
	return sets
}()

// LiveKeys reports the keys that work right now. A filter matching nothing
// has nothing to toggle, and advertising enter there names a key that is
// refused.
func (m *fieldPickerModel) LiveKeys() (set kernel.KeySet, gen int) {
	state := fieldPicking
	if len(m.shown) == 0 {
		state = fieldNothing
	}
	return fieldSets[state], int(state)
}
