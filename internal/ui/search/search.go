// Package search finds issues by what their summary, description and comments say.
package search

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"

	"charm.land/bubbles/v2/textinput"
	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"

	appissueref "github.com/varijkapil13/saral/internal/app/issueref"
	appquery "github.com/varijkapil13/saral/internal/app/query"
	appsearch "github.com/varijkapil13/saral/internal/app/search"
	"github.com/varijkapil13/saral/internal/config"
	"github.com/varijkapil13/saral/internal/ui/issue"
	"github.com/varijkapil13/saral/internal/ui/kernel"
	"github.com/varijkapil13/saral/internal/ui/list"
	"github.com/varijkapil13/saral/internal/ui/widget"
	"github.com/varijkapil13/saral/pkg/jira"
)

// ViewID is the name this view registers itself under.
const ViewID = kernel.SearchViewID

const (
	settle     = 250 * time.Millisecond
	maxRows    = 200
	lookahead  = 10
	rowMemo    = 256
	fallbackRL = 30 * time.Second
	memoQuery  = "query"
	memoScope  = "scope"
)

var (
	_ kernel.View        = (*Model)(nil)
	_ kernel.KeyCapturer = (*Model)(nil)
	_ kernel.Addressed   = (*Model)(nil)
	_ kernel.Closer      = (*Model)(nil)
)

// Seed is what a search opens with.
type Seed struct {
	Query    string
	Scope    Scope
	HasScope bool
}

// QueryMsg puts a query in the box of a search that is already open and runs it.
type QueryMsg struct{ Text string }

// Option configures a search at construction.
type Option func(*Model)

func withAfter(after func(time.Duration, func() tea.Msg) tea.Cmd) Option {
	return func(m *Model) { m.after = after }
}

func tickAfter(d time.Duration, fn func() tea.Msg) tea.Cmd {
	return tea.Tick(d, func(time.Time) tea.Msg { return fn() })
}

// Model is the search view.
type Model struct {
	deps   kernel.Deps
	search *appsearch.TextSearch
	addr   kernel.Addr
	keys   keyMap
	table  map[string]action
	after  func(time.Duration, func() tea.Msg) tea.Cmd

	input         textinput.Model
	typed         string
	valueGen      int
	typedGen      int
	replaceOnType bool
	browsing      bool
	focused       bool
	scope         Scope
	pendingGo     bool
	browseOnLand  bool

	ctx         context.Context
	cancel      context.CancelFunc
	gen         int
	loading     bool
	paging      bool
	textPending bool
	failure     error
	retryAt     time.Time

	jql        string
	tq         jira.TextQuery
	ranText    string
	words      []string
	short      bool
	restoreKey string

	found []*row
	pin   *row
	rows  []*row
	page  jira.Page[jira.Issue]

	cursor, top   int
	width, height int
	keyW          int
	lay           layout
	termsGen      int

	styles *styles
	memo   *widget.RowCache[rowKey, string]
	zones  widget.Zoner
	clicks *widget.Clicks

	head   [3]string
	headAt headKey
	body   []string
	lines  []string
}

// New builds a search opened with seed.
func New(d kernel.Deps, seed Seed, opts ...Option) *Model {
	m := &Model{
		deps:     d,
		addr:     kernel.NewAddr(),
		keys:     defaultKeys(),
		after:    tickAfter,
		input:    widget.NewInput(),
		memo:     widget.NewRowCache[rowKey, string](rowMemo),
		zones:    widget.NewZoner(d.Zones),
		clicks:   widget.NewClicks(d.Now),
		keyW:     minKeyWidth,
		termsGen: 1,
	}
	for _, o := range opts {
		o(m)
	}
	m.table = m.keys.browseTable()
	if m.deps.Theme == nil {
		m.deps.Theme = kernel.NewTheme(kernel.ThemeAuto, true, kernel.UnicodeGlyphs())
	}
	m.styles = newStyles(m.deps.Theme)
	if d.Jira != nil {
		m.search = appsearch.NewTextSearch(appquery.NewSearch(d.Jira), d.Jira)
	}
	m.input.Prompt = "search "
	m.input.Placeholder = "words from a summary, description or comment"
	m.scope = seed.Scope
	if !seed.HasScope {
		m.scope = recalledScope(d)
	}
	if !m.canScope() {
		m.scope = ScopeSite
	}
	if seed.Query != "" {
		m.input.SetValue(seed.Query)
		m.input.CursorEnd()
		m.typed = seed.Query
		m.replaceOnType = true
	}
	_ = m.input.Focus()
	m.lay = planLayout(m.width, m.keyW)
	return m
}

// NewView builds a search that opens on the query and scope the last one ended on.
func NewView(d kernel.Deps) kernel.View {
	q, _ := kernel.Recall(d, ViewID, memoQuery)
	return New(d, Seed{Query: q})
}

// Open pushes a search with query in the box, running when it is not empty.
func Open(d kernel.Deps, query string) tea.Cmd {
	return kernel.Push(ViewID, "Search", New(d, Seed{Query: query}))
}

func recalledScope(d kernel.Deps) Scope {
	if v, ok := kernel.Recall(d, ViewID, memoScope); ok && v == "project" {
		return ScopeProject
	}
	return ScopeSite
}

// Addr is where the kernel delivers this view's answers.
func (m *Model) Addr() kernel.Addr { return m.addr }

// WantsRawKeys is true while the box is taking typing.
func (m *Model) WantsRawKeys() bool { return !m.browsing }

// Close stops whatever is out.
func (m *Model) Close() { m.stop() }

func (m *Model) canScope() bool { return m.deps.Project != "" }

func (m *Model) count() int {
	if m.hasMoreRow() {
		return len(m.rows) + 1
	}
	return len(m.rows)
}

func (m *Model) hasMoreRow() bool { return len(m.rows) >= maxRows && m.page.HasMore() }

func (m *Model) hasMore() bool { return m.page.HasMore() }

func (m *Model) now() time.Time {
	if m.deps.Now == nil {
		return time.Time{}
	}
	return m.deps.Now()
}

func (m *Model) title() string {
	t := "Search"
	if m.ranText != "" {
		t += " " + quote(m.deps.Theme.Glyphs.IsASCII(), m.ranText)
	}
	if m.scope == ScopeProject && m.deps.Project != "" {
		t += " in " + m.deps.Project
	}
	return t
}

// Init runs the seeded query, if there is one.
func (m *Model) Init() tea.Cmd {
	if strings.TrimSpace(m.typed) == "" {
		return nil
	}
	return m.run(false)
}

// Update handles one message.
func (m *Model) Update(msg tea.Msg) (kernel.View, tea.Cmd) {
	var cmd tea.Cmd
	switch msg := msg.(type) {
	case kernel.SizeMsg:
		m.resize(msg.Width, msg.Height)
	case kernel.FocusMsg:
		m.focused = msg.Focused
		m.focusInput()
	case kernel.ThemeMsg:
		m.deps.Theme = msg.Theme
		m.styles = newStyles(msg.Theme)
		m.memo.Reset()
	case kernel.CapabilitiesMsg:
		m.deps.Caps = msg.Caps
		m.memo.Reset()
	case kernel.ProjectMsg:
		cmd = m.reproject(msg.Project)
	case kernel.RefreshMsg:
		cmd = m.refresh(msg.Purge)
	case QueryMsg:
		m.input.SetValue(msg.Text)
		m.input.CursorEnd()
		m.typed = msg.Text
		m.valueGen++
		m.typedGen++
		cmd = m.run(false)
	case issue.ShareMsg:
		if m.focused {
			cmd = issue.Share(m.deps, msg.Act, m.selectedKey())
		}
	case settledMsg:
		if msg.gen == m.typedGen {
			cmd = m.run(false)
		}
	case searchMsg:
		cmd = m.landed(msg)
	case keyMsg:
		m.pinned(msg)
	case pagedMsg:
		cmd = m.nextPage(msg)
	case tea.KeyPressMsg:
		cmd = m.key(msg)
	case tea.MouseClickMsg:
		cmd = m.click(msg)
	case tea.MouseWheelMsg:
		cmd = m.wheel(msg)
	}
	return m, cmd
}

func (m *Model) resize(w, h int) {
	if w == m.width && h == m.height {
		return
	}
	m.width, m.height = w, h
	m.lay = planLayout(w, m.keyW)
	m.memo.Reset()
	m.clampScroll()
}

func (m *Model) focusInput() {
	if m.focused && !m.browsing {
		_ = m.input.Focus()
		return
	}
	m.input.Blur()
}

func (m *Model) reply(cmd tea.Cmd) tea.Cmd { return kernel.Reply(cmd, m.addr) }

func (m *Model) begin() (ctx context.Context, gen int) {
	m.stop()
	m.gen++
	ctx, m.cancel = context.WithCancel(context.Background())
	m.ctx = ctx
	m.loading, m.failure, m.paging = true, nil, false
	return ctx, m.gen
}

func (m *Model) stop() {
	if m.cancel != nil {
		m.cancel()
		m.cancel = nil
	}
	m.loading, m.paging = false, false
}

func (m *Model) clear() {
	m.stop()
	m.gen++
	m.jql, m.failure = "", nil
	m.found, m.pin, m.rows = nil, nil, nil
	m.page = jira.Page[jira.Issue]{}
	m.textPending = false
	m.cursor, m.top = 0, 0
	m.browsing = false
	m.focusInput()
}

func (m *Model) keyOf(text string) (string, bool) {
	if key, ok := appissueref.ParseKey(text); ok {
		return key, true
	}
	key, host, ok := appissueref.ParseIssueURL(text)
	if !ok {
		return "", false
	}
	here, err := config.NormalizeSite(m.deps.Site)
	if err != nil || !strings.EqualFold(here, host) {
		return "", false
	}
	return key, true
}

func (m *Model) rateLimited() bool {
	return !m.retryAt.IsZero() && m.now().Before(m.retryAt)
}

func (m *Model) run(rerun bool) tea.Cmd {
	text := strings.TrimSpace(m.typed)
	tq := jira.ParseText(text)
	key, keyed := m.keyOf(text)
	if tq.Empty() || (tq.Longest() < 2 && !keyed) {
		m.clear()
		m.short = !tq.Empty()
		m.ranText = ""
		return nil
	}
	jql, _ := appsearch.Compose(tq, m.scope == ScopeProject, m.deps.Project)
	if jql == m.jql && m.failure == nil && !rerun {
		return nil
	}
	if m.search == nil || m.rateLimited() {
		return nil
	}
	m.short = false
	kernel.Keep(m.deps, ViewID, memoQuery, text)
	kernel.Keep(m.deps, ViewID, memoScope, m.scopeName())

	ctx, gen := m.begin()
	m.jql, m.tq, m.ranText, m.words = jql, tq, text, tq.Words()
	m.textPending = true
	if !keyed || (m.pin != nil && m.pin.iss.Key != key) {
		m.pin = nil
	}
	cmds := []tea.Cmd{m.reply(searchCmd(ctx, m.search, jql, gen))}
	if keyed && m.deps.Jira != nil {
		cmds = append(cmds, m.reply(keyCmd(ctx, m.search, key, gen)))
	}
	return tea.Batch(cmds...)
}

func (m *Model) scopeName() string {
	if m.scope == ScopeProject {
		return "project"
	}
	return "site"
}

func (m *Model) newRow(iss jira.Issue, pinned bool) *row {
	summary := widget.Sanitize(iss.Summary)
	return &row{iss: iss, summary: summary, spans: highlights(summary, m.words), pinned: pinned}
}

func (m *Model) landed(msg searchMsg) tea.Cmd {
	if msg.gen != m.gen {
		return nil
	}
	m.loading, m.textPending = false, false
	if msg.err != nil {
		return m.fail(msg.err)
	}
	m.page, m.retryAt = msg.res.Page, time.Time{}
	m.found = m.found[:0]
	for i := range msg.res.Page.Items {
		m.found = append(m.found, m.newRow(msg.res.Page.Items[i], false))
	}
	m.termsGen++
	m.rebuild()
	if k := m.restoreKey; k != "" {
		m.restoreKey = ""
		m.moveToKey(k)
	} else {
		m.cursor, m.top = 0, 0
	}
	if m.browseOnLand {
		m.startBrowsing()
	}
	return m.pageAhead(m.cursor)
}

func (m *Model) pinned(msg keyMsg) {
	if msg.gen != m.gen || msg.err != nil || m.failure != nil {
		return
	}
	m.pin = m.newRow(msg.issue, true)
	if m.textPending {
		if len(m.rows) == 0 {
			m.rows = []*row{m.pin}
			m.cursor, m.top = 0, 0
		}
		return
	}
	m.rebuild()
}

func (m *Model) nextPage(msg pagedMsg) tea.Cmd {
	if msg.gen != m.gen {
		return nil
	}
	m.paging = false
	if msg.err != nil {
		return kernel.Fail(msg.err)
	}
	m.page = msg.page
	for i := range msg.page.Items {
		m.found = append(m.found, m.newRow(msg.page.Items[i], false))
	}
	m.rebuild()
	return m.pageAhead(m.cursor)
}

func (m *Model) rebuild() {
	under := m.selectedKey()
	rows := make([]*row, 0, len(m.found)+1)
	if m.pin != nil {
		rows = append(rows, m.pin)
	}
	for _, r := range m.found {
		if m.pin != nil && r.iss.Key == m.pin.iss.Key {
			continue
		}
		rows = append(rows, r)
	}
	m.rows = rows
	widest := minKeyWidth
	for _, r := range rows {
		widest = max(widest, ansi.StringWidth(r.iss.Key))
	}
	if widest != m.keyW {
		m.keyW = widest
		m.lay = planLayout(m.width, m.keyW)
	}
	if under != "" {
		m.moveToKey(under)
	}
	m.cursor = min(m.cursor, max(m.count()-1, 0))
	m.clampScroll()
}

func (m *Model) moveToKey(key string) {
	if at := slices.IndexFunc(m.rows, func(r *row) bool { return r.iss.Key == key }); at >= 0 {
		m.cursor = at
		m.scrollToCursor()
	}
}

func (m *Model) fail(err error) tea.Cmd {
	m.failure = err
	m.found, m.pin, m.rows = nil, nil, nil
	m.page = jira.Page[jira.Issue]{}
	m.cursor, m.top = 0, 0
	m.paging = false
	var limit *jira.RateLimitError
	if errors.As(err, &limit) {
		wait := limit.RetryAfter
		if wait <= 0 {
			wait = fallbackRL
		}
		m.retryAt = m.now().Add(wait)
	}
	return kernel.Fail(err)
}

func (m *Model) pageAhead(at int) tea.Cmd {
	if m.paging || m.loading || m.failure != nil || !m.page.HasMore() || len(m.found) >= maxRows || m.ctx == nil {
		return nil
	}
	if at < len(m.rows)-lookahead {
		return nil
	}
	m.paging = true
	return m.reply(pageCmd(m.ctx, m.search, m.page, m.gen))
}

func (m *Model) refresh(purge bool) tea.Cmd {
	if m.search == nil || (m.jql == "" && strings.TrimSpace(m.typed) == "") {
		return nil
	}
	if purge {
		m.search.Invalidate()
	}
	m.restoreKey = m.selectedKey()
	return m.run(true)
}

func (m *Model) reproject(project string) tea.Cmd {
	if project == m.deps.Project {
		return nil
	}
	m.deps.Project = project
	if !m.canScope() && m.scope == ScopeProject {
		m.scope = ScopeSite
	}
	if m.scope == ScopeProject && strings.TrimSpace(m.typed) != "" {
		return m.run(false)
	}
	return nil
}

func (m *Model) toggleScope() tea.Cmd {
	if !m.canScope() {
		return nil
	}
	if m.scope == ScopeSite {
		m.scope = ScopeProject
	} else {
		m.scope = ScopeSite
	}
	kernel.Keep(m.deps, ViewID, memoScope, m.scopeName())
	m.memo.Reset()
	if strings.TrimSpace(m.typed) == "" {
		return nil
	}
	return m.run(false)
}

func (m *Model) selected() *row {
	if m.cursor < 0 || m.cursor >= len(m.rows) {
		return nil
	}
	return m.rows[m.cursor]
}

func (m *Model) selectedKey() string {
	if r := m.selected(); r != nil {
		return r.iss.Key
	}
	return ""
}

func (m *Model) moveTo(at int) tea.Cmd {
	if m.count() == 0 {
		m.cursor, m.top = 0, 0
		return nil
	}
	m.cursor = min(max(at, 0), m.count()-1)
	m.scrollToCursor()
	return m.pageAhead(m.cursor)
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
	m.top = min(max(m.top, 0), max(m.count()-m.rowsHeight(), 0))
}

func (m *Model) open() tea.Cmd {
	if m.cursor >= len(m.rows) {
		return m.openInList()
	}
	r := m.selected()
	if r == nil {
		return nil
	}
	return kernel.Push(issue.ViewID, r.iss.Key, issue.New(m.deps, r.iss))
}

func (m *Model) openInList() tea.Cmd {
	if m.jql == "" {
		return nil
	}
	return kernel.OpenThen(list.ViewID, list.QueryMsg{JQL: m.jql, Title: m.title()})
}

func (m *Model) startTyping() {
	m.browsing, m.browseOnLand = false, false
	m.replaceOnType = false
	m.pendingGo = false
	m.input.CursorEnd()
	m.focusInput()
}

func (m *Model) startBrowsing() {
	if len(m.rows) == 0 {
		return
	}
	m.browseOnLand = false
	m.browsing = true
	m.pendingGo = false
	m.focusInput()
}

func (m *Model) key(msg tea.KeyPressMsg) tea.Cmd {
	if !m.browsing {
		return m.typingKey(msg)
	}
	return m.browseKey(msg.String())
}

func (m *Model) typingKey(msg tea.KeyPressMsg) tea.Cmd {
	switch msg.String() {
	case "esc":
		return kernel.Pop()
	case "enter":
		cmd := m.run(false)
		m.browseOnLand = true
		m.startBrowsing()
		return cmd
	case "down", "ctrl+n":
		m.startBrowsing()
		m.cursor, m.top = 0, 0
		return nil
	case "tab":
		return m.toggleScope()
	}
	if m.replaceOnType && msg.Text != "" {
		m.input.SetValue("")
	}
	m.replaceOnType = false
	m.input, _ = m.input.Update(msg)
	if v := m.input.Value(); v != m.typed {
		return m.typedChanged(v)
	}
	return nil
}

func (m *Model) typedChanged(v string) tea.Cmd {
	m.typed, m.browseOnLand = v, false
	m.valueGen++
	m.typedGen++
	tq := jira.ParseText(strings.TrimSpace(v))
	_, keyed := m.keyOf(strings.TrimSpace(v))
	if tq.Empty() || (tq.Longest() < 2 && !keyed) {
		m.clear()
		m.short = !tq.Empty()
		return nil
	}
	gen := m.typedGen
	return m.reply(m.after(settle, func() tea.Msg { return settledMsg{gen: gen} }))
}

func (m *Model) browseKey(stroke string) tea.Cmd {
	if m.pendingGo {
		m.pendingGo = false
		switch stroke {
		case "g":
			return m.moveTo(0)
		case "e":
			return m.moveTo(m.count() - 1)
		}
	}
	if act := issue.ShareStroke(stroke); act != issue.ShareNone {
		return issue.Share(m.deps, act, m.selectedKey())
	}
	h := m.rowsHeight()
	switch m.table[stroke] {
	case actDown:
		return m.moveTo(m.cursor + 1)
	case actUp:
		return m.moveTo(m.cursor - 1)
	case actPageDown:
		return m.moveTo(m.cursor + h)
	case actPageUp:
		return m.moveTo(m.cursor - h)
	case actHalfDown:
		return m.moveTo(m.cursor + max(h/2, 1))
	case actHalfUp:
		return m.moveTo(m.cursor - max(h/2, 1))
	case actTop:
		return m.moveTo(0)
	case actBottom:
		return m.moveTo(m.count() - 1)
	case actGo:
		m.pendingGo = true
	case actOpen:
		return m.open()
	case actTyping:
		m.startTyping()
	case actScope:
		return m.toggleScope()
	case actInList:
		return m.openInList()
	case actNone:
	}
	return nil
}

func (m *Model) click(msg tea.MouseClickMsg) tea.Cmd {
	if msg.Button == tea.MouseRight {
		return m.pointAt(msg)
	}
	if msg.Button != tea.MouseLeft {
		return nil
	}
	switch {
	case m.canScope() && m.zones.Hit(scopeZone, msg):
		m.clicks.Forget()
		return m.toggleScope()
	case m.zones.Hit(queryZone, msg):
		m.clicks.Forget()
		m.startTyping()
		return nil
	case m.hasMoreRow() && m.zones.Hit(moreZone, msg):
		m.clicks.Forget()
		return m.openInList()
	}
	for i := m.top; i < min(m.top+m.rowsHeight(), len(m.rows)); i++ {
		key := m.rows[i].iss.Key
		if !m.zones.Hit(rowZone(key), msg) {
			continue
		}
		if !m.browsing {
			m.browsing = true
			m.focusInput()
		}
		if m.clicks.Double(rowZone(key)) {
			m.cursor = i
			return m.open()
		}
		return m.moveTo(i)
	}
	return nil
}

func (m *Model) pointAt(msg tea.MouseClickMsg) tea.Cmd {
	for i := m.top; i < min(m.top+m.rowsHeight(), len(m.rows)); i++ {
		if m.zones.Hit(rowZone(m.rows[i].iss.Key), msg) {
			m.clicks.Forget()
			if !m.browsing {
				m.browsing = true
				m.focusInput()
			}
			return m.moveTo(i)
		}
	}
	return nil
}

func (m *Model) wheel(msg tea.MouseWheelMsg) tea.Cmd {
	switch msg.Button {
	case tea.MouseWheelUp:
		m.top -= widget.WheelStep
	case tea.MouseWheelDown:
		m.top += widget.WheelStep
	default:
		return nil
	}
	m.clampScroll()
	return m.pageAhead(m.top + m.rowsHeight())
}

func (m *Model) stateLine() (string, bool) {
	t := m.deps.Theme
	switch m.mode() {
	case modeShort:
		return "type a little more", false
	case modeLoading:
		return "searching" + t.Glyphs.Ellipsis, false
	case modeResults:
		n := len(m.rows)
		plus := ""
		if m.hasMore() {
			plus = "+"
		}
		noun := "issues"
		if n == 1 && plus == "" {
			noun = "issue"
		}
		return fmt.Sprintf("%d%s %s, newest first", n, plus, noun), false
	case modeEmpty:
		return "0 issues", false
	case modeFailed:
		return "the search did not run", true
	default:
		dot := " " + t.Glyphs.Dot + " "
		switch {
		case !m.canScope():
			return "Type to search every project", false
		case m.scope == ScopeProject:
			return "Type to search " + m.deps.Project + dot + "tab: every project", false
		default:
			return "Type to search every project" + dot + "tab: only " + m.deps.Project, false
		}
	}
}

func (m *Model) where() string {
	if m.scope == ScopeProject && m.deps.Project != "" {
		return m.deps.Project
	}
	return "all projects"
}

func (m *Model) bodyLines() []string {
	t := m.deps.Theme
	switch m.mode() {
	case modeLoading:
		return []string{"Searching " + m.where() + t.Glyphs.Ellipsis}
	case modeEmpty:
		return []string{"Jira found no issues for " + quote(t.Glyphs.IsASCII(), m.ranText) + " in " + m.where() + "."}
	case modeFailed:
		return []string{failureBody(m.failure, m.retryWait())}
	default:
		return nil
	}
}

func (m *Model) retryWait() time.Duration {
	var limit *jira.RateLimitError
	if errors.As(m.failure, &limit) && limit.RetryAfter > 0 {
		return limit.RetryAfter
	}
	return fallbackRL
}

func failureBody(err error, wait time.Duration) string {
	var (
		limit *jira.RateLimitError
		trans *jira.TransportError
		valid *jira.ValidationError
	)
	switch {
	case errors.As(err, &limit):
		return fmt.Sprintf("Jira asked to wait %s before searching again.", wait.Round(time.Second))
	case errors.As(err, &trans):
		reason := trans.Error()
		if trans.Err != nil {
			reason = trans.Err.Error()
		}
		return "The site could not be reached: " + reason
	case errors.As(err, &valid):
		return "Jira refused this search: " + valid.Error()
	default:
		reason, _ := jira.Reason(err)
		return reason
	}
}
