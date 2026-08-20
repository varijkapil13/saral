// Package list is the issue list: a virtualized table over a JQL search that
// pages as the cursor approaches the end of what it has.
package list

import (
	"context"
	"slices"
	"strconv"
	"strings"
	"time"

	"charm.land/bubbles/v2/textinput"
	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/varijkapil13/saral/internal/app"
	"github.com/varijkapil13/saral/internal/ui/issue"
	"github.com/varijkapil13/saral/internal/ui/kernel"
	"github.com/varijkapil13/saral/pkg/jira"
)

// ViewID is the name this view registers itself under and the scope its keys
// are registered in.
const ViewID = "list"

// lookahead is how close to the end of the loaded rows the cursor gets before
// the next page is asked for. One screen ahead means the fetch has landed by
// the time the user reaches the rows it brought.
const lookahead = 12

// autoFillCap bounds the paging done to fill a screen the local filter has
// emptied. Without it a filter matching nothing walks the whole result set.
const autoFillCap = 2000

// rowCacheLimit is how many rendered rows are kept. A window plus its overscan,
// in both selected and unselected forms, several relayouts deep.
const rowCacheLimit = 1024

var _ kernel.View = (*Model)(nil)

// Model is the issue list.
type Model struct {
	deps     kernel.Deps
	search   *app.Search
	normal   map[string]action
	inFilter map[string]action
	styles   *styles
	rows     *rowCache

	jql   string
	title string

	issues  []jira.Issue
	page    jira.Page[jira.Issue]
	missing []string
	view    []int

	// needles holds one lowercased haystack per issue, built while a filter is
	// open so that a keystroke costs a substring search rather than a fresh
	// lowercasing of every row. It is dropped when the filter is.
	needles []string

	cursor int
	top    int

	width, height int
	lay           layout
	head          string

	// lines is the frame under construction, kept between frames so that
	// drawing a screen does not allocate one slice per frame.
	lines   []string
	summary string
	sumKey  summaryKey

	filtering bool
	filter    textinput.Model
	query     string

	pendingGo bool

	loading bool
	loaded  bool
	gen     int
	cancel  context.CancelFunc

	zonePrefix string
}

// New builds the issue list. The query it opens on is the user's own work,
// narrowed to the session's project when there is one; both halves are resolved
// at runtime, so nothing about the site is written down here.
func New(d kernel.Deps) kernel.View {
	m := &Model{
		deps:   d,
		styles: newStyles(d.Theme),
		rows:   newRowCache(rowCacheLimit),
		filter: newFilterInput(),
	}
	if m.deps.Theme == nil {
		m.deps.Theme = kernel.NewTheme(kernel.ThemeAuto, true, kernel.UnicodeGlyphs())
		m.styles = newStyles(m.deps.Theme)
	}
	if d.Jira != nil {
		m.search = app.NewSearch(d.Jira)
	}
	if d.Zones != nil {
		m.zonePrefix = d.Zones.NewPrefix()
	}
	m.normal, m.inFilter = defaultKeys().tables()
	m.jql, m.title = defaultQuery(d.Project)
	m.relayout()
	return m
}

func defaultQuery(project string) (jql, title string) {
	if strings.TrimSpace(project) == "" {
		return "assignee = currentUser() ORDER BY updated DESC", "My issues"
	}
	return "project = " + quote(project) + " AND assignee = currentUser() ORDER BY updated DESC",
		"My issues in " + project
}

func quote(s string) string {
	return strconv.Quote(strings.ReplaceAll(s, `"`, ""))
}

func newFilterInput() textinput.Model {
	ti := textinput.New()
	ti.Prompt = "/"
	ti.Placeholder = "filter these rows"
	return ti
}

// QueryMsg retargets the list at another search. It is exported so that the
// command palette, a saved query or a JQL prompt can drive this view without
// holding a pointer to it.
type QueryMsg struct {
	JQL   string
	Title string
}

// Init runs the opening search.
func (m *Model) Init() tea.Cmd { return m.load() }

// Update handles one message.
func (m *Model) Update(msg tea.Msg) (kernel.View, tea.Cmd) {
	var cmd tea.Cmd
	switch msg := msg.(type) {
	case kernel.SizeMsg:
		cmd = m.resize(msg.Width, msg.Height)

	case kernel.FocusMsg:
		m.setFocus(msg.Focused)

	case kernel.ThemeMsg:
		m.styles = newStyles(msg.Theme)
		m.deps.Theme = msg.Theme
		m.rows.reset()
		m.relayout()

	case kernel.CapabilitiesMsg:
		m.deps.Caps = msg.Caps
		m.rows.reset()

	case kernel.RefreshMsg:
		cmd = m.refresh(msg.Purge)

	case QueryMsg:
		cmd = m.retarget(msg)

	case loadedMsg:
		cmd = m.loadedPage(msg)

	case pagedMsg:
		cmd = m.nextPage(msg)

	case patchedMsg:
		cmd = m.patch(msg)

	case failedMsg:
		cmd = m.failed(msg)

	case tea.KeyPressMsg:
		cmd = m.key(msg)

	case tea.MouseClickMsg:
		cmd = m.click(msg)

	case tea.MouseWheelMsg:
		cmd = m.wheel(msg)
	}
	return m, cmd
}

// setFocus keeps the filter's cursor out of a pane nobody is looking at.
func (m *Model) setFocus(on bool) {
	if !m.filtering {
		return
	}
	if on {
		_ = m.filter.Focus()
		return
	}
	m.filter.Blur()
}

func (m *Model) resize(w, h int) tea.Cmd {
	if w == m.width && h == m.height {
		return nil
	}
	m.width, m.height = w, h
	m.relayout()
	m.scrollToCursor()
	return m.pageAheadIfNeeded()
}

// relayout recomputes the column plan and the caption row. Both are memoized
// here rather than in View because they change on a resize and on nothing else.
func (m *Model) relayout() {
	lay := planLayout(m.width, m.widestKey())
	if lay == m.lay && m.head != "" {
		return
	}
	m.lay = lay
	m.head = lay.header(m.deps.Theme)
}

func (m *Model) widestKey() int {
	widest := minKeyWidth
	for i := range m.issues {
		if n := ansi.StringWidth(m.issues[i].Key); n > widest {
			widest = n
		}
		if widest >= maxKeyWidth {
			return maxKeyWidth
		}
	}
	return widest
}

// rowsHeight is how many issue rows fit: the box, less the summary line, the
// column captions and the filter prompt when one is open.
func (m *Model) rowsHeight() int {
	h := m.height - 2
	if m.filtering {
		h--
	}
	return max(h, 1)
}

// --- fetching ---------------------------------------------------------------

// begin cancels whatever is in flight and opens a context for its replacement.
// The generation it returns is what a landing result is checked against, so an
// answer to a question the user has already changed is dropped rather than
// drawn.
func (m *Model) begin() (ctx context.Context, gen int) {
	m.stop()
	m.gen++
	ctx, cancel := context.WithCancel(context.Background())
	m.cancel = cancel
	m.loading = true
	return ctx, m.gen
}

func (m *Model) stop() {
	if m.cancel != nil {
		m.cancel()
		m.cancel = nil
	}
	m.loading = false
}

func (m *Model) current(gen int) bool { return gen == m.gen }

func (m *Model) load() tea.Cmd {
	if m.search == nil {
		return nil
	}
	ctx, gen := m.begin()
	return withCancel(m.cancel, load(ctx, m.search, m.jql, gen))
}

func (m *Model) refresh(purge bool) tea.Cmd {
	if m.search == nil {
		return nil
	}
	if purge {
		m.search.Invalidate()
	}
	if !m.loaded {
		return m.load()
	}
	ctx, gen := m.begin()
	return withCancel(m.cancel, reload(ctx, m.search, m.jql, len(m.issues), gen))
}

func (m *Model) retarget(msg QueryMsg) tea.Cmd {
	jql := strings.TrimSpace(msg.JQL)
	if jql == "" {
		return nil
	}
	m.jql = jql
	if msg.Title != "" {
		m.title = msg.Title
	}
	m.issues, m.page, m.missing, m.view, m.needles = nil, jira.Page[jira.Issue]{}, nil, nil, nil
	m.cursor, m.top, m.loaded = 0, 0, false
	m.rows.reset()
	m.refilter()
	return m.load()
}

// withCancel makes a command release its context however it ends. The cancel is
// also held on the model so that the next request can cut this one short.
func withCancel(cancel context.CancelFunc, cmd tea.Cmd) tea.Cmd {
	return func() tea.Msg {
		defer cancel()
		return cmd()
	}
}

func (m *Model) loadedPage(msg loadedMsg) tea.Cmd {
	if !m.current(msg.gen) {
		return nil
	}
	m.loading, m.loaded = false, true
	m.issues, m.needles = slices.Clone(msg.page.Items), nil
	m.page, m.missing = msg.page, msg.missing
	m.rows.reset()
	m.relayout()
	m.refilter()
	m.cursor, m.top = 0, 0
	return tea.Batch(m.missingFields(), m.pageAheadIfNeeded())
}

func (m *Model) nextPage(msg pagedMsg) tea.Cmd {
	if !m.current(msg.gen) {
		return nil
	}
	m.loading = false
	m.issues, m.needles = append(m.issues, msg.page.Items...), nil
	m.page = msg.page
	m.relayout()
	m.refilter()
	return m.pageAheadIfNeeded()
}

// patch replaces the rows with a re-read of themselves, leaving the cursor on
// the issue it was on, the scroll where it was and the filter as it was typed.
func (m *Model) patch(msg patchedMsg) tea.Cmd {
	if !m.current(msg.gen) {
		return nil
	}
	m.loading, m.loaded = false, true
	under := m.selectedKey()
	m.issues, m.page, m.needles = msg.issues, msg.page, nil
	m.rows.reset()
	m.relayout()
	m.refilter()
	m.restore(under)
	return m.pageAheadIfNeeded()
}

func (m *Model) failed(msg failedMsg) tea.Cmd {
	if !m.current(msg.gen) {
		return nil
	}
	m.loading = false
	return kernel.Fail(msg.err)
}

// missingFields reports the projection fields this site has no field for, which
// is the only honest thing to do with a column that would otherwise be empty.
func (m *Model) missingFields() tea.Cmd {
	if len(m.missing) == 0 {
		return nil
	}
	return kernel.Warn("this site has no field called " + strings.Join(m.missing, ", "))
}

// pageAheadIfNeeded asks for the next page when the cursor is near the end of
// what is loaded, or when the local filter has left too few rows to fill the
// screen. One request is in flight at a time, whatever the cursor does.
func (m *Model) pageAheadIfNeeded() tea.Cmd {
	if m.loading || !m.page.HasMore() {
		return nil
	}
	near := m.cursor >= len(m.view)-lookahead
	starved := m.query != "" && len(m.view) < m.rowsHeight() && len(m.issues) < autoFillCap
	if !near && !starved {
		return nil
	}
	ctx, gen := m.begin()
	return withCancel(m.cancel, more(ctx, m.page, gen))
}

// --- selection and filtering ------------------------------------------------

func (m *Model) selectedKey() string {
	if m.cursor < 0 || m.cursor >= len(m.view) {
		return ""
	}
	if at := m.view[m.cursor]; at >= 0 && at < len(m.issues) {
		return m.issues[at].Key
	}
	return ""
}

// restore puts the cursor back on an issue by key, keeping the scroll offset so
// that the row does not jump even when its index moved.
func (m *Model) restore(key string) {
	if key != "" {
		for i, at := range m.view {
			if m.issues[at].Key == key {
				m.cursor = i
				m.clampScroll()
				return
			}
		}
	}
	m.cursor = min(m.cursor, max(len(m.view)-1, 0))
	m.clampScroll()
}

// refilter recomputes which rows the local filter leaves visible. It reuses the
// index slice so that typing in the filter does not allocate one per keystroke.
func (m *Model) refilter() {
	under := m.selectedKey()
	m.view = m.view[:0]
	needle := strings.ToLower(m.query)
	if needle == "" {
		for i := range m.issues {
			m.view = append(m.view, i)
		}
		m.restore(under)
		return
	}
	m.buildNeedles()
	for i := range m.issues {
		if strings.Contains(m.needles[i], needle) {
			m.view = append(m.view, i)
		}
	}
	m.restore(under)
}

// buildNeedles lowercases what a row can be found by, once per row rather than
// once per row per keystroke.
func (m *Model) buildNeedles() {
	if len(m.needles) == len(m.issues) {
		return
	}
	m.needles = slices.Grow(m.needles[:0], len(m.issues))
	var b strings.Builder
	for i := range m.issues {
		iss := &m.issues[i]
		b.Reset()
		b.Grow(len(iss.Key) + len(iss.Summary) + 48)
		for _, field := range [...]string{iss.Key, iss.Summary, iss.Status.Name, iss.Type.Name, assigneeName(iss, "")} {
			b.WriteString(field)
			b.WriteByte(' ')
		}
		for _, label := range iss.Labels {
			b.WriteString(label)
			b.WriteByte(' ')
		}
		m.needles = append(m.needles, strings.ToLower(b.String()))
	}
}

func (m *Model) moveTo(at int) tea.Cmd {
	if len(m.view) == 0 {
		m.cursor, m.top = 0, 0
		return nil
	}
	m.cursor = min(max(at, 0), len(m.view)-1)
	m.scrollToCursor()
	return m.pageAheadIfNeeded()
}

func (m *Model) scrollToCursor() {
	h := m.rowsHeight()
	if m.cursor < m.top {
		m.top = m.cursor
	}
	if m.cursor >= m.top+h {
		m.top = m.cursor - h + 1
	}
	m.clampScroll()
}

func (m *Model) clampScroll() {
	m.top = min(max(m.top, 0), max(len(m.view)-m.rowsHeight(), 0))
}

// --- input ------------------------------------------------------------------

func (m *Model) key(msg tea.KeyPressMsg) tea.Cmd {
	stroke := msg.String()
	if m.filtering {
		return m.filterKey(msg, stroke)
	}
	if m.pendingGo {
		m.pendingGo = false
		switch stroke {
		case "g":
			return m.moveTo(0)
		case "e":
			return m.moveTo(len(m.view) - 1)
		}
	}
	switch m.normal[stroke] {
	case actDown:
		return m.moveTo(m.cursor + 1)
	case actUp:
		return m.moveTo(m.cursor - 1)
	case actPageDown:
		return m.moveTo(m.cursor + m.rowsHeight())
	case actPageUp:
		return m.moveTo(m.cursor - m.rowsHeight())
	case actHalfDown:
		return m.moveTo(m.cursor + m.rowsHeight()/2)
	case actHalfUp:
		return m.moveTo(m.cursor - m.rowsHeight()/2)
	case actTop:
		return m.moveTo(0)
	case actBottom:
		return m.moveTo(len(m.view) - 1)
	case actGo:
		m.pendingGo = true
	case actOpen:
		return m.open()
	case actFilter:
		return m.startFilter()
	case actNone, actAccept, actClear:
	}
	return nil
}

func (m *Model) filterKey(msg tea.KeyPressMsg, stroke string) tea.Cmd {
	switch m.inFilter[stroke] {
	case actAccept:
		m.filtering = false
		m.filter.Blur()
		m.clampScroll()
		return nil
	case actClear:
		m.filtering = false
		m.filter.Blur()
		m.filter.Reset()
		m.query, m.needles = "", nil
		m.refilter()
		m.scrollToCursor()
		return nil
	default:
	}
	// The text input's own command is a cursor blink, which is a timer this
	// view would then own for as long as the filter is open. Dropping it costs
	// a blinking block and keeps every frame reproducible.
	m.filter, _ = m.filter.Update(msg)
	if q := m.filter.Value(); q != m.query {
		m.query = q
		m.refilter()
		m.scrollToCursor()
		return m.pageAheadIfNeeded()
	}
	return nil
}

func (m *Model) startFilter() tea.Cmd {
	m.filtering = true
	m.filter.SetWidth(max(m.width-2, 8))
	_ = m.filter.Focus()
	m.clampScroll()
	return nil
}

func (m *Model) open() tea.Cmd {
	if m.selectedKey() == "" {
		return nil
	}
	iss := m.issues[m.view[m.cursor]]
	return kernel.Push(issue.ViewID, iss.Key, issue.New(m.deps, iss))
}

// click selects the row under the pointer, and opens it when it was already the
// selected one. There is no double-click message in Bubble Tea v2, and a second
// click on the row you just picked is the gesture that means "this one".
func (m *Model) click(msg tea.MouseClickMsg) tea.Cmd {
	if msg.Button != tea.MouseLeft || m.deps.Zones == nil {
		return nil
	}
	for i := m.top; i < min(m.top+m.rowsHeight(), len(m.view)); i++ {
		iss := &m.issues[m.view[i]]
		if !m.deps.Zones.Get(m.zonePrefix + "row:" + iss.Key).InBounds(msg) {
			continue
		}
		if i == m.cursor {
			return m.open()
		}
		return m.moveTo(i)
	}
	return nil
}

// wheel scrolls the rows without moving the selection, which is what a wheel
// does everywhere else.
func (m *Model) wheel(msg tea.MouseWheelMsg) tea.Cmd {
	switch msg.Button {
	case tea.MouseWheelUp:
		m.top -= 3
	case tea.MouseWheelDown:
		m.top += 3
	default:
		return nil
	}
	m.clampScroll()
	return nil
}

// --- rendering --------------------------------------------------------------

// View draws the visible window and nothing else. A ten-thousand row list and a
// twenty row list do the same work here.
func (m *Model) View() string {
	if m.width <= 0 || m.height <= 0 {
		return ""
	}
	h := m.rowsHeight()
	lines := m.lines[:0]
	lines = append(lines, m.summaryLine(), m.head)

	switch {
	case len(m.view) == 0:
		lines = m.appendEmpty(lines, h)
	default:
		end := min(m.top+h, len(m.view))
		for i := m.top; i < end; i++ {
			lines = append(lines, m.row(m.view[i], i == m.cursor))
		}
		for i := end - m.top; i < h; i++ {
			lines = append(lines, "")
		}
		m.warm(end)
	}
	if m.filtering {
		lines = append(lines, m.filter.View())
	}
	m.lines = lines
	return strings.Join(lines, "\n")
}

// warm renders the overscan into the memo so that the next scroll step is a
// cache hit rather than a row build. It draws nothing.
func (m *Model) warm(end int) {
	const overscan = 4
	for i := max(m.top-overscan, 0); i < min(end+overscan, len(m.view)); i++ {
		if i < m.top || i >= end {
			m.row(m.view[i], false)
		}
	}
}

func (m *Model) row(at int, selected bool) string {
	iss := &m.issues[at]
	k := rowKey{key: iss.Key, updated: iss.Updated.UnixNano(), lay: m.lay, selected: selected, gen: m.styles.gen}
	if s, ok := m.rows.get(k); ok {
		return s
	}
	s := renderRow(iss, m.lay, selected, m.styles, m.deps.Theme, m.deps.Caps.Location(), m.now())
	if m.deps.Zones != nil {
		s = m.deps.Zones.Mark(m.zonePrefix+"row:"+iss.Key, s)
	}
	m.rows.put(k, s)
	return s
}

func (m *Model) now() time.Time {
	if m.deps.Now == nil {
		return time.Time{}
	}
	return m.deps.Now()
}

// summaryKey is everything the summary line is built from, so that the line is
// rebuilt when one of them moves and never otherwise.
type summaryKey struct {
	title           string
	width, gen      int
	issues, visible int
	more            bool
	loading, loaded bool
	filtered        bool
}

func (m *Model) summaryKey() summaryKey {
	return summaryKey{
		title: m.title, width: m.width, gen: m.styles.gen,
		issues: len(m.issues), visible: len(m.view), more: m.page.HasMore(),
		loading: m.loading, loaded: m.loaded, filtered: m.query != "",
	}
}

// summaryLine names the query and says how much of it is in hand. The count is
// "142+" while another page exists, because /search/jql reports no total and
// pretending otherwise would be a number the user could not trust.
func (m *Model) summaryLine() string {
	key := m.summaryKey()
	if m.summary != "" && key == m.sumKey {
		return m.summary
	}
	ell := m.deps.Theme.Glyphs.Ellipsis
	count := m.countLabel()
	title := ansi.Truncate(m.title, max(m.width-ansi.StringWidth(count)-1, 1), ell)
	pad := max(m.width-ansi.StringWidth(title)-ansi.StringWidth(count), 1)
	m.summary = m.styles.title.Render(title) + strings.Repeat(" ", pad) + m.styles.count.Render(count)
	m.sumKey = key
	return m.summary
}

func (m *Model) countLabel() string {
	var b strings.Builder
	switch {
	case !m.loaded && m.loading:
		b.WriteString("searching")
	case m.query != "":
		b.WriteString(strconv.Itoa(len(m.view)))
		b.WriteString(" of ")
		b.WriteString(m.total())
	default:
		b.WriteString(m.total())
		b.WriteString(" issues")
	}
	if m.loaded && m.loading {
		b.WriteString(" ")
		b.WriteString(m.deps.Theme.Glyphs.Stale)
	}
	return b.String()
}

func (m *Model) total() string {
	n := strconv.Itoa(len(m.issues))
	if m.page.HasMore() {
		return n + "+"
	}
	return n
}

func (m *Model) appendEmpty(lines []string, h int) []string {
	at := len(lines)
	switch {
	case m.search == nil:
		lines = append(lines, m.styles.muted.Render("  No Jira connection in this session yet."))
	case !m.loaded:
		lines = append(lines, m.styles.muted.Render("  Searching"+m.deps.Theme.Glyphs.Ellipsis))
	case m.query != "":
		lines = append(lines, m.styles.muted.Render("  No loaded row matches "+strconv.Quote(m.query)+"."))
	default:
		lines = append(lines, m.styles.muted.Render("  Nothing matches this search."),
			m.styles.muted.Render("  "+m.jql))
	}
	for len(lines)-at < h {
		lines = append(lines, "")
	}
	return lines[:at+h]
}
