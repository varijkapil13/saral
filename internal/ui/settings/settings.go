// Package settings is the settings screen: every registered kernel.Setting,
// grouped into the sections it registered under and drawn as the row shape
// its Kind calls for. docs/SETTINGS.md is the design; the palette is for
// verbs and this is for state.
package settings

import (
	"strings"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/varijkapil13/saral/internal/ui/kernel"
)

var (
	_ kernel.View = (*Model)(nil)
)

func init() {
	kernel.RegisterView(kernel.ViewSpec{
		ID:    kernel.SettingsViewID,
		Title: "Settings",
		New:   New,
	})
	kernel.RegisterKeys(kernel.SettingsViewID, keySet())
	keys := kernel.DefaultGlobalKeys()
	kernel.RegisterCommand(kernel.Command{
		ID:    "settings.open",
		Title: "Settings",
		Group: "Session",
		Kind:  kernel.KindSession,
		Keys:  []string{keys.Settings.Help().Key},
		Run:   func(kernel.Deps) tea.Cmd { return kernel.Open(kernel.SettingsViewID) },
	})
}

// Model is the settings screen.
type Model struct {
	deps kernel.Deps
	keys settingsKeys
	acts map[string]action

	all      []kernel.Setting
	sections []string
	rows     []kernel.Setting
	// lineStart is, per entry in rows, the line its control row starts on in
	// the full rendered output — a header contributes two lines above it and a
	// setting three, so this is computed once whenever rows changes rather
	// than walked on every scroll.
	lineStart []int
	total     int

	cursor int

	width, height int
	top           int

	styles  *styles
	memo    *rowCache
	lay     layout
	profile profileState

	zonePrefix string
}

// New builds the settings screen over everything registered, the registry's
// own constructor exactly as palette.New is.
func New(d kernel.Deps) kernel.View { return build(d, kernel.Settings(), kernel.SettingSections()) }

func build(d kernel.Deps, all []kernel.Setting, sections []string) *Model {
	m := &Model{
		deps:     d,
		keys:     defaultKeys(),
		all:      all,
		sections: sections,
		memo:     newRowCache(rowMemoLimit),
		profile:  readProfile(),
	}
	if m.deps.Theme == nil {
		m.deps.Theme = kernel.NewTheme(kernel.ThemeAuto, true, kernel.UnicodeGlyphs())
	}
	if d.Zones != nil {
		m.zonePrefix = d.Zones.NewPrefix()
	}
	m.acts = m.keys.table()
	m.styles = newStyles(m.deps.Theme)
	m.lay = planLayout(m.width)
	m.rebuildRows()
	return m
}

// Init has nothing to fetch: every setting answers from the live session, and
// the active profile is read once, synchronously, the same way the kernel's
// own restoreCaps reads its cache — a local file, not a request in flight.
func (m *Model) Init() tea.Cmd { return nil }

func (m *Model) rebuildRows() {
	under := m.selectedID()
	m.rows = m.rows[:0]
	for i := range m.all {
		if refusal(m.deps.Caps, m.all[i].Requires, m.all[i].Title) != "" {
			continue
		}
		m.rows = append(m.rows, m.all[i])
	}
	m.layoutLines()
	if at := m.indexOf(under); at >= 0 {
		m.cursor = at
	} else if m.cursor >= len(m.rows) {
		m.cursor = max(len(m.rows)-1, 0)
	}
	m.scrollToCursor()
}

func (m *Model) layoutLines() {
	m.lineStart = make([]int, len(m.rows))
	line, last := 0, ""
	for i := range m.rows {
		if m.rows[i].Section != last {
			line += 2
			last = m.rows[i].Section
		}
		m.lineStart[i] = line
		line += 3
	}
	m.total = line
}

func (m *Model) selectedID() string {
	if m.cursor < 0 || m.cursor >= len(m.rows) {
		return ""
	}
	return m.rows[m.cursor].ID
}

func (m *Model) indexOf(id string) int {
	if id == "" {
		return -1
	}
	for i := range m.rows {
		if m.rows[i].ID == id {
			return i
		}
	}
	return -1
}

// Update handles one message.
func (m *Model) Update(msg tea.Msg) (kernel.View, tea.Cmd) {
	var cmd tea.Cmd
	switch msg := msg.(type) {
	case kernel.SizeMsg:
		m.resize(msg.Width, msg.Height)

	case kernel.ThemeMsg:
		m.deps.Theme = msg.Theme
		m.styles = newStyles(msg.Theme)
		m.memo.reset()

	case kernel.CapabilitiesMsg:
		m.deps.Caps = msg.Caps
		m.rebuildRows()
		m.memo.reset()

	case kernel.ProjectMsg:
		m.deps.Project = msg.Project
		m.memo.reset()

	case kernel.RefreshMsg:
		m.profile = readProfile()
		m.memo.reset()

	case tea.KeyPressMsg:
		cmd = m.key(msg)

	case tea.MouseClickMsg:
		cmd = m.click(msg)

	case tea.MouseWheelMsg:
		m.wheel(msg)
	}
	return m, cmd
}

func (m *Model) resize(w, h int) {
	if w == m.width && h == m.height {
		return
	}
	m.width, m.height = w, h
	m.lay = planLayout(w)
	m.memo.reset()
	m.clampScroll()
}

func (m *Model) key(msg tea.KeyPressMsg) tea.Cmd {
	switch m.acts[msg.String()] {
	case actUp:
		m.moveTo(m.cursor - 1)
	case actDown:
		m.moveTo(m.cursor + 1)
	case actLeft:
		return m.adjust(-1)
	case actRight:
		return m.adjust(1)
	case actChoose:
		return m.activate()
	}
	return nil
}

func (m *Model) current() (kernel.Setting, bool) {
	if m.cursor < 0 || m.cursor >= len(m.rows) {
		return kernel.Setting{}, false
	}
	return m.rows[m.cursor], true
}

// adjust is ←/→ on the row under the cursor: it moves a radio to its neighbour
// and applies at once, flips a toggle either direction, and opens a picker on
// → only — ← has nothing to do to a control that is not a set of values in a
// row.
func (m *Model) adjust(dir int) tea.Cmd {
	s, ok := m.current()
	if !ok {
		return nil
	}
	switch shapeOf(s, m.deps) {
	case shapeRadios:
		opts := s.Options(m.deps)
		at := indexOfOption(opts, s.Value(m.deps))
		next := clampInt(at+dir, 0, len(opts)-1)
		if next == at || at < 0 {
			return nil
		}
		return s.Set(m.deps, opts[next].ID)
	case shapeToggle:
		return s.Set(m.deps, otherToggle(s.Value(m.deps)))
	case shapePicker:
		if dir > 0 {
			return m.openPicker(s)
		}
	}
	return nil
}

// activate is enter: apply the radio under the cursor (idempotent, since an
// arrow already applies as it moves), flip the toggle, open the picker or run
// the action — and for an info row, say why it does not move, in Unavailable's
// words when it has one.
func (m *Model) activate() tea.Cmd {
	s, ok := m.current()
	if !ok {
		return nil
	}
	switch shapeOf(s, m.deps) {
	case shapeRadios:
		return s.Set(m.deps, s.Value(m.deps))
	case shapeToggle:
		return s.Set(m.deps, otherToggle(s.Value(m.deps)))
	case shapePicker:
		return m.openPicker(s)
	case shapeAction:
		return s.Run(m.deps)
	case shapeInfo:
		if s.Run != nil {
			return s.Run(m.deps)
		}
		return m.explain(s)
	}
	return nil
}

func (m *Model) explain(s kernel.Setting) tea.Cmd {
	if s.Unavailable != nil {
		if why := s.Unavailable(m.deps); why != "" {
			return kernel.Status(why)
		}
	}
	return kernel.Status(s.Summary)
}

func (m *Model) openPicker(s kernel.Setting) tea.Cmd {
	if s.OpenPicker != nil {
		return s.OpenPicker(m.deps)
	}
	return openOptionsPicker(m.deps, s.Title, s.Options(m.deps), s.Value(m.deps), s.Set)
}

func indexOfOption(opts []kernel.SettingOption, id string) int {
	for i, o := range opts {
		if o.ID == id {
			return i
		}
	}
	return -1
}

func otherToggle(value string) string {
	if value == "on" {
		return "off"
	}
	return "on"
}

func clampInt(v, lo, hi int) int {
	if hi < lo {
		return lo
	}
	return min(max(v, lo), hi)
}

func (m *Model) moveTo(at int) {
	if len(m.rows) == 0 {
		m.cursor, m.top = 0, 0
		return
	}
	m.cursor = clampInt(at, 0, len(m.rows)-1)
	m.scrollToCursor()
}

func (m *Model) scrollToCursor() {
	if m.cursor < 0 || m.cursor >= len(m.lineStart) {
		return
	}
	start := m.lineStart[m.cursor]
	end := start + 1
	if start < m.top {
		m.top = start
	}
	h := max(m.height, 1)
	if end >= m.top+h {
		m.top = end - h + 1
	}
	m.clampScroll()
}

func (m *Model) clampScroll() {
	h := max(m.height, 1)
	maxTop := max(m.total-h, 0)
	m.top = clampInt(m.top, 0, maxTop)
}

func (m *Model) wheel(msg tea.MouseWheelMsg) {
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

const (
	zoneRow = "row:"
	zoneOpt = "opt:"
)

func (m *Model) rowZone(id string) string { return m.zonePrefix + zoneRow + id }

func (m *Model) optZone(id, optID string) string { return m.zonePrefix + zoneOpt + id + ":" + optID }

// click resolves a click to the setting row it landed on. A radio option has
// its own zone, so a click on one both selects the row and applies that exact
// value in one gesture; anywhere else on an already-selected row does what
// enter does, and a click that only reaches an unselected row selects it,
// mirroring how the palette and the project picker treat a click.
func (m *Model) click(msg tea.MouseClickMsg) tea.Cmd {
	if msg.Button != tea.MouseLeft || m.deps.Zones == nil {
		return nil
	}
	for i := range m.rows {
		s := &m.rows[i]
		if shapeOf(*s, m.deps) == shapeRadios {
			for _, o := range s.Options(m.deps) {
				if m.deps.Zones.Get(m.optZone(s.ID, o.ID)).InBounds(msg) {
					m.cursor = i
					m.scrollToCursor()
					return s.Set(m.deps, o.ID)
				}
			}
		}
		if !m.deps.Zones.Get(m.rowZone(s.ID)).InBounds(msg) {
			continue
		}
		if i == m.cursor {
			return m.activate()
		}
		m.moveTo(i)
		return nil
	}
	return nil
}

// View draws the screen: every section in registered order, each of its
// settings as its row shape calls for.
func (m *Model) View() string {
	if m.width <= 0 || m.height <= 0 {
		return ""
	}
	full := m.renderAll()
	end := min(m.top+m.height, len(full))
	var lines []string
	if m.top < end {
		lines = full[m.top:end]
	}
	out := make([]string, 0, m.height)
	out = append(out, lines...)
	for len(out) < m.height {
		out = append(out, "")
	}
	return strings.Join(out, "\n")
}

func (m *Model) renderAll() []string {
	if len(m.rows) == 0 {
		text := "  Nothing is registered on this screen yet."
		if len(m.all) > 0 {
			text = "  Nothing on this screen is available on this site."
		}
		return []string{m.styles.muted.Render(text)}
	}
	out := make([]string, 0, m.total)
	last := ""
	for i := range m.rows {
		if m.rows[i].Section != last {
			out = append(out, m.renderHeader(m.rows[i].Section), "")
			last = m.rows[i].Section
		}
		ctrl, detail := m.renderRow(i, m.rows[i])
		out = append(out, ctrl, detail, "")
	}
	return out
}

func (m *Model) renderRow(i int, s kernel.Setting) (ctrl, detail string) {
	sel := i == m.cursor
	value := ""
	if s.Value != nil {
		value = s.Value(m.deps)
	}
	unavailable := ""
	if s.Unavailable != nil {
		unavailable = s.Unavailable(m.deps)
	}
	key := rowKey{id: s.ID, value: value, unavailable: unavailable, sel: sel, gen: m.styles.gen, width: m.lay.width}
	r, ok := m.memo.get(key)
	if !ok {
		sp := shapeOf(s, m.deps)
		ctrlLine := renderControl(s, sp, m.deps, value, sel, m.lay, m.styles)
		text := s.Summary
		warn := false
		if unavailable != "" {
			text, warn = unavailable, true
		} else if note := m.scopeSuffix(s); note != "" {
			text += "  (" + note + ")"
		}
		detailLine := renderDetail(text, warn, sel, m.lay.width, m.deps.Theme.Glyphs.Ellipsis, m.styles)
		r = renderedRow{ctrl: ctrlLine, detail: detailLine, shape: sp}
		if sp == shapeRadios {
			r.radioOpts = s.Options(m.deps)
		}
		m.memo.put(key, r)
	}
	return m.markRow(s, r)
}

// markRow applies this frame's click zones to an otherwise-cached row. Zones
// are reapplied on every call rather than baked into the cached string: Mark
// is idempotent under the same id and this keeps the cache oblivious to the
// zone manager entirely, the same separation renderControl already keeps from
// Deps.Zones by never being handed it. The shape and, for a radio row, the
// options it marks zones over both come from the cache entry rather than a
// fresh shapeOf/Options call, so a cache hit costs nothing to reclassify.
func (m *Model) markRow(s kernel.Setting, r renderedRow) (ctrl, detail string) {
	if m.deps.Zones == nil {
		return r.ctrl, r.detail
	}
	ctrl = r.ctrl
	if r.shape == shapeRadios {
		ctrl = markRadioZones(ctrl, s, m.deps, r.radioOpts, m.zonePrefix)
	}
	ctrl = m.deps.Zones.Mark(m.rowZone(s.ID), ctrl)
	return ctrl, r.detail
}

// markRadioZones finds each option's already-rendered "(x) label" span in the
// finished control line and wraps just that span in its own zone. It runs
// after padTruncate rather than before, so a zone marker — an inert ANSI
// escape width libraries already skip — is never itself a candidate for
// truncation; an option pushed out of a narrow column is simply not found and
// draws with no zone of its own, same as it would with no mouse at all.
func markRadioZones(line string, s kernel.Setting, d kernel.Deps, opts []kernel.SettingOption, prefix string) string {
	value := ""
	if s.Value != nil {
		value = s.Value(d)
	}
	for _, o := range opts {
		span := "( ) " + o.Label
		if o.ID == value {
			span = "(" + d.Theme.Glyphs.Bullet + ") " + o.Label
		}
		if at := strings.Index(line, span); at >= 0 {
			line = line[:at] + d.Zones.Mark(prefix+zoneOpt+s.ID+":"+o.ID, span) + line[at+len(span):]
		}
	}
	return line
}

func (m *Model) renderHeader(section string) string {
	note := m.sectionScope(section)
	left := "  " + section
	if note == "" {
		return m.styles.header.Render(padTruncate(left, m.lay.width, m.deps.Theme.Glyphs.Ellipsis))
	}
	right := m.styles.note.Render(note)
	pad := m.lay.width - ansi.StringWidth(left) - ansi.StringWidth(note)
	if pad < 1 {
		pad = 1
	}
	return m.styles.header.Render(left) + strings.Repeat(" ", pad) + right
}

// sectionScope is the scope shared by most of a section's settings, the note
// docs/SETTINGS.md draws once beside the section rather than on every row. A
// setting whose own scope disagrees gets its own note on its detail line
// instead — a fourth section, or a section that splits down the middle, is
// answered per row rather than by a header that would be wrong for half of it.
func (m *Model) sectionScope(section string) string {
	scope, ok := m.dominantScope(section)
	if !ok {
		return ""
	}
	return scopeText(scope, m.profile)
}

func (m *Model) scopeSuffix(s kernel.Setting) string {
	if s.Kind == kernel.KindAction {
		return ""
	}
	dom, ok := m.dominantScope(s.Section)
	if !ok || s.Scope == dom {
		return ""
	}
	return scopeText(s.Scope, m.profile)
}

func (m *Model) dominantScope(section string) (kernel.SettingScope, bool) {
	counts := map[kernel.SettingScope]int{}
	var order []kernel.SettingScope
	for i := range m.rows {
		s := &m.rows[i]
		if s.Section != section || s.Kind == kernel.KindAction {
			continue
		}
		if counts[s.Scope] == 0 {
			order = append(order, s.Scope)
		}
		counts[s.Scope]++
	}
	if len(order) == 0 {
		return 0, false
	}
	best := order[0]
	for _, sc := range order[1:] {
		if counts[sc] > counts[best] {
			best = sc
		}
	}
	return best, true
}
