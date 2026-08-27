// Package list is the issue list: a virtualized table over a JQL search that
// pages as the cursor approaches the end of what it has.
package list

import (
	"context"
	"errors"
	"slices"
	"strconv"
	"strings"
	"time"

	"charm.land/bubbles/v2/textinput"
	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/varijkapil13/saral/internal/app"
	"github.com/varijkapil13/saral/internal/ui/filter"
	"github.com/varijkapil13/saral/internal/ui/issue"
	"github.com/varijkapil13/saral/internal/ui/kernel"
	"github.com/varijkapil13/saral/internal/ui/widget"
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

var _ kernel.KeyCapturer = (*Model)(nil)

var _ kernel.Addressed = (*Model)(nil)

// Model is the issue list.
type Model struct {
	deps     kernel.Deps
	search   *app.Search
	cache    app.Cache
	normal   map[string]action
	inFilter map[string]action
	inAsk    map[string]action
	styles   *styles
	rows     *rowCache

	jql   string
	title string

	issues  []jira.Issue
	page    jira.Page[jira.Issue]
	missing []string
	view    []int

	// ranks is the scored run the filter is ordered from, kept between keystrokes
	// so that ranking ten thousand rows costs no allocation of its own.
	ranks []ranked

	cursor int
	top    int

	width, height int
	lay           layout
	head          string

	// lines is the frame under construction, kept between frames so that
	// drawing a screen does not allocate one slice per frame.
	lines      []string
	summary    string
	sumKey     summaryKey
	chip       string
	chipAt     chipKey
	filterLine string
	filterAt   chipKey

	filtering bool
	filter    textinput.Model
	query     string

	// asking is the prompt that shows the search on screen and takes an edited
	// one, so that what is being run is readable and changeable from the view
	// rather than only from the palette.
	asking bool
	ask    textinput.Model

	// terms are what the search on screen is narrowed by: a person, a status, a
	// type, a priority or a label, each held by the id the site gave it. They
	// compose into the query rather than into a pass over the rows already
	// loaded, so a term reaches an issue this session has never fetched.
	//
	// termsGen counts the changes to them, because a slice cannot be part of the
	// comparable key the line naming them is memoized on.
	terms    filter.Terms
	termsGen int

	// defaulted records that the search on screen is still the one this view
	// chose from the session's project. A project switch retargets that search
	// and leaves alone one the user asked for.
	defaulted bool

	// widened records that the default has already fallen back to the whole
	// project for want of anything assigned to this account. It stops an empty
	// project being asked the same question twice, and a project switch clears
	// it because that derives a fresh default.
	widened bool

	// asked and answered track the one question a session asks about the
	// credential: whether the site has anything assigned to this account
	// anywhere. It is asked once — the answer is about the token, which does not
	// change while the program runs — and a project switch does not ask again.
	asked    bool
	answered bool
	// assignedNowhere is that answer. It is why the search on screen is not the
	// one this view opens on, and the pane says so for as long as the list is
	// empty.
	assignedNowhere bool

	// saved is the kernel's set of saved queries, as this view was built and as
	// the kernel last changed it, kept so that binding a key can name what that
	// key already runs.
	saved    app.SavedQueries
	bind     bindStep
	bindSlot int

	pendingGo bool

	loading bool
	loaded  bool
	gen     int
	cancel  context.CancelFunc
	addr    kernel.Addr

	// failure is why the search on screen brought back no rows. The status line
	// kernel.Fail writes is replaced by the next keypress, so the pane keeps its
	// own copy of the reason for as long as it is empty.
	failure error

	// checked is when what is on screen last came from the site, and the zero
	// time until anything has. The summary line's stamp is drawn from it: the
	// status line a refresh writes goes away, and this does not.
	checked time.Time

	// stale marks the rows on screen as older than they should be: they came off
	// disk past their TTL, or the refresh that would have replaced them failed.
	// It is what the badge in the summary line is drawn from.
	stale bool
	// cachedMore records that the stored rows were only part of the answer. Rows
	// off disk carry no cursor, so paging on from them means asking the search
	// again rather than following one.
	cachedMore bool

	focused bool

	poll       time.Duration
	pollArmed  bool
	pollPaused bool

	zones  widget.Zoner
	clicks *widget.Clicks
}

// bindStep is how far the gesture that binds this query to a number key has
// got.
type bindStep uint8

const (
	bindNone bindStep = iota
	bindPick
	bindConfirm
)

// WantsRawKeys is true while either prompt is open, and while a number key is
// being picked. Without it the kernel matches its own bindings first, so a
// query loses every digit, r triggers a refetch, esc cannot cancel, and q quits
// the program out from under the typing — and the digit that was meant to bind
// a key would run whatever is already on it instead.
func (m *Model) WantsRawKeys() bool { return m.filtering || m.asking || m.bind != bindNone }

// New builds the issue list. The query it opens on is the user's own work,
// narrowed to the session's project when there is one — and the project itself
// where that comes back empty, which widen does once the site has answered.
// Every half of it is resolved at runtime, so nothing about the site is written
// down here.
func New(d kernel.Deps) kernel.View {
	m := &Model{
		deps:   d,
		addr:   kernel.NewAddr(),
		cache:  d.Cache,
		styles: newStyles(d.Theme),
		rows:   newRowCache(rowCacheLimit),
		filter: newFilterInput(),
		ask:    newAskInput(),
		saved:  d.Saved,
		poll:   PollInterval(),
	}
	if m.deps.Theme == nil {
		m.deps.Theme = kernel.NewTheme(kernel.ThemeAuto, true, kernel.UnicodeGlyphs())
		m.styles = newStyles(m.deps.Theme)
	}
	if d.Jira != nil {
		m.search = app.NewSearch(d.Jira)
	}
	m.zones = widget.NewZoner(d.Zones)
	m.clicks = widget.NewClicks(d.Now)
	m.normal, m.inFilter, m.inAsk = defaultKeys().tables()
	m.jql, m.title = defaultQuery(d.Project)
	m.defaulted = true
	m.relayout()
	m.fromCache()
	return m
}

// fromCache puts the rows the last session left on disk on screen, before
// anything at all is asked of the site (docs/UX.md principle 1).
//
// It runs here rather than in Init because this is where a first paint happens:
// kernel.FirstPaint builds the view and renders one frame without ever calling
// Init, which is the thing docs/PERFORMANCE.md budgets at 60ms.
func (m *Model) fromCache() {
	if m.cache == nil {
		return
	}
	snap, ok := m.cache.Rows(m.jql)
	if !ok || len(snap.Issues) == 0 {
		return
	}
	m.issues = snap.Issues
	m.page, m.missing = jira.Page[jira.Issue]{}, nil
	m.cachedMore, m.stale = snap.More, snap.Stale
	m.loaded, m.cursor, m.top = true, 0, 0
	m.rows.reset()
	m.relayout()
	m.refilter()
}

func newFilterInput() textinput.Model {
	ti := widget.NewInput()
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

// SaveQueryMsg starts the gesture that binds the query on screen to a number
// key. It is exported so that the palette reaches the same gesture the key
// does, rather than a second implementation of it.
type SaveQueryMsg struct{}

// ClearFilterMsg drops the filter narrowing the rows. It is exported for the
// same reason FacetMsg is: the palette has to reach the gesture the key does.
type ClearFilterMsg struct{}

// Init asks the site for what the first frame could not draw from disk.
func (m *Model) Init() tea.Cmd {
	if m.loaded {
		return tea.Batch(m.revalidate(), m.pageAheadIfNeeded(), m.probeAssignment())
	}
	return tea.Batch(m.load(), m.probeAssignment())
}

// probeAssignment asks whether this credential belongs to an account anybody
// assigns work to. The search a session opens on narrows by currentUser(), which
// resolves for a service token and matches nothing at all, so without an answer
// the opening frame is empty and the reason for it is a guess.
//
// It costs a second round trip, and it is skipped wherever the answer is already
// in hand: rows for the default off disk are proof of work, and an unscoped
// session's own default asks the same question.
func (m *Model) probeAssignment() tea.Cmd {
	if m.search == nil || m.asked || m.answered || !m.defaulted || len(m.issues) > 0 {
		return nil
	}
	jql := probeQuery()
	if jql == m.jql {
		return nil
	}
	m.asked = true
	ctx, cancel := context.WithCancel(context.Background())
	return kernel.Reply(withCancel(cancel, probeAssigned(ctx, m.search, jql)), m.addr)
}

// revalidate re-reads rows that came off disk, and only once they are past their
// TTL: rows written seconds ago are what the last frame of the last session
// showed, and asking for them again is the round trip the cache exists to spare.
func (m *Model) revalidate() tea.Cmd {
	if !m.stale {
		return nil
	}
	return m.refetch(whyBackground)
}

// Update handles one message.
func (m *Model) Update(msg tea.Msg) (kernel.View, tea.Cmd) {
	var cmd tea.Cmd
	switch msg := msg.(type) {
	case kernel.SizeMsg:
		cmd = m.resize(msg.Width, msg.Height)

	case kernel.FocusMsg:
		cmd = m.setFocus(msg.Focused)

	case kernel.ThemeMsg:
		m.styles = newStyles(msg.Theme)
		m.deps.Theme = msg.Theme
		m.rows.reset()
		m.relayout()

	case kernel.CapabilitiesMsg:
		m.deps.Caps = msg.Caps
		m.rows.reset()

	case kernel.ProjectMsg:
		cmd = m.reproject(msg.Project)

	case kernel.RefreshMsg:
		cmd = m.refresh(msg.Purge)

	case QueryMsg:
		cmd = m.retarget(msg)

	case kernel.RunQueryMsg:
		cmd = m.retarget(QueryMsg{JQL: msg.JQL, Title: msg.Title})

	case kernel.SavedQueriesMsg:
		m.saved = msg.Queries

	case SaveQueryMsg:
		m.startBind()

	case EditQueryMsg:
		cmd = m.startAsk()

	case FacetMsg:
		cmd = m.facetMsg(msg)

	case OpenFilterMsg:
		cmd = m.openFilter()

	case filter.ChosenMsg:
		cmd = m.applyTerm(msg.Term)

	case ClearFilterMsg:
		cmd = m.clearFilter()

	case loadedMsg:
		cmd = m.loadedPage(msg)

	case pagedMsg:
		cmd = m.nextPage(msg)

	case patchedMsg:
		cmd = m.patch(msg)

	case failedMsg:
		cmd = m.failed(msg)

	case assignedMsg:
		cmd = m.assigned(msg)

	case pollMsg:
		cmd = m.polled(msg)

	case tea.KeyPressMsg:
		cmd = m.key(msg)

	case tea.MouseClickMsg:
		cmd = m.click(msg)

	case tea.MouseWheelMsg:
		cmd = m.wheel(msg)
	}
	return m, cmd
}

// setFocus keeps the filter's cursor out of a pane nobody is looking at, and the
// poller scoped to the view somebody is.
func (m *Model) setFocus(on bool) tea.Cmd {
	m.focused = on
	switch {
	case m.filtering && on:
		_ = m.filter.Focus()
	case m.filtering:
		m.filter.Blur()
	case m.asking && on:
		_ = m.ask.Focus()
	case m.asking:
		m.ask.Blur()
	}
	if on {
		return m.pollTick()
	}
	return nil
}

func (m *Model) resize(w, h int) tea.Cmd {
	if w == m.width && h == m.height {
		return nil
	}
	m.width, m.height = w, h
	if m.asking {
		m.ask.SetWidth(m.askWidth())
	}
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
// column captions and whichever of the four lines below the rows are drawn.
func (m *Model) rowsHeight() int {
	h := m.height - 2
	if len(m.terms) > 0 {
		h--
	}
	if m.keptFilter() {
		h--
	}
	if m.filtering || m.asking || m.bind != bindNone {
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
	m.loading, m.failure = true, nil
	return ctx, m.gen
}

func (m *Model) stop() {
	if m.cancel != nil {
		m.cancel()
		m.cancel = nil
	}
	m.loading = false
}

// Addr is where the kernel delivers the pages and the poll this list asked for,
// whatever has since been pushed over it and whichever root is on screen.
func (m *Model) Addr() kernel.Addr { return m.addr }

// reply puts this list's address on a command, so what it asked for comes back
// here rather than to whatever the stack has on top by then. The list is a root:
// the detail pane is pushed over it and the palette over that, and it is parked
// off screen whenever another root is shown.
func (m *Model) reply(cmd tea.Cmd) tea.Cmd {
	return kernel.Reply(withCancel(m.cancel, cmd), m.addr)
}

func (m *Model) current(gen int) bool { return gen == m.gen }

func (m *Model) load() tea.Cmd { return m.loadFor(whyOpen) }

func (m *Model) loadFor(w why) tea.Cmd {
	if m.search == nil {
		return nil
	}
	ctx, gen := m.begin()
	return m.reply(load(ctx, m.search, m.cache, m.jql, gen, w))
}

func (m *Model) refresh(purge bool) tea.Cmd {
	if purge {
		return m.refetch(whyPurge)
	}
	return m.refetch(whyRefresh)
}

// refetch re-reads the search on screen. What it was asked for travels with the
// request, because a poll, a revalidation and somebody pressing r all arrive
// here and only one of them has anybody waiting to be told what came back.
func (m *Model) refetch(w why) tea.Cmd {
	if m.search == nil {
		return nil
	}
	var said tea.Cmd
	if w == whyPurge {
		m.search.Invalidate()
		if m.cache != nil {
			if err := m.cache.Forget(m.jql); err != nil {
				said = kernel.Warn("the stored copy of this search could not be dropped: " + err.Error())
			}
		}
	}
	if !m.loaded {
		return tea.Batch(said, m.loadFor(w))
	}
	ctx, gen := m.begin()
	return tea.Batch(said, m.reply(reload(ctx, m.search, m.cache, m.jql, len(m.issues), gen, w)))
}

func (m *Model) retarget(msg QueryMsg) tea.Cmd {
	return m.setQuery(msg.JQL, msg.Title, false)
}

// reproject follows a mid-session project switch. The key is taken whatever the
// search is, because the detail pane is built from these deps; the search moves
// only while it is the one this view chose, never one the user ran.
func (m *Model) reproject(project string) tea.Cmd {
	if project == m.deps.Project {
		return nil
	}
	was := m.deps.Project
	m.deps.Project = project
	// Terms cannot follow a project switch: a status and an issue type are
	// minted per project, so the ids in force name values the new project has
	// never heard of. They go, and the switch says so rather than leaving a
	// search about somewhere else under a header naming here.
	if len(m.terms) > 0 {
		m.widened = false
		jql, title := defaultQuery(project)
		return tea.Batch(
			kernel.Status("the filters were about "+was+", so they came off with it"),
			m.setQuery(jql, title, true))
	}
	if !m.defaulted {
		return nil
	}
	m.widened = false
	jql, title := defaultQuery(project)
	return m.setQuery(jql, title, true)
}

func (m *Model) setQuery(jql, title string, byDefault bool) tea.Cmd {
	jql = strings.TrimSpace(jql)
	if jql == "" {
		return nil
	}
	m.jql, m.defaulted = jql, byDefault
	if title != "" {
		m.title = title
	}
	m.issues, m.page, m.missing, m.view = nil, jira.Page[jira.Issue]{}, nil, nil
	m.cursor, m.top, m.loaded = 0, 0, false
	m.cachedMore, m.stale, m.failure = false, false, nil
	m.checked = time.Time{}
	m.terms, m.termsGen = nil, m.termsGen+1
	m.rows.reset()
	m.refilter()
	m.fromCache()
	if m.loaded {
		return tea.Batch(m.revalidate(), m.pageAheadIfNeeded())
	}
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
	before := m.issues
	m.loading, m.loaded = false, true
	m.issues = slices.Clone(msg.page.Items)
	m.page, m.missing = msg.page, msg.missing
	m.cachedMore, m.stale, m.checked = false, false, m.now()
	m.rows.reset()
	m.relayout()
	m.refilter()
	m.cursor, m.top = 0, 0
	if wider := m.widen(); wider != nil {
		return tea.Batch(notStored(msg.stored), wider)
	}
	return tea.Batch(m.missingFields(), notStored(msg.stored),
		refreshed(msg.why, before, m.issues), m.pageAheadIfNeeded(), m.pollTick())
}

func (m *Model) nextPage(msg pagedMsg) tea.Cmd {
	if !m.current(msg.gen) {
		return nil
	}
	m.loading = false
	m.issues = append(m.issues, msg.page.Items...)
	m.page = msg.page
	m.cachedMore, m.stale, m.checked = false, false, m.now()
	m.relayout()
	m.refilter()
	return tea.Batch(notStored(msg.stored), m.pageAheadIfNeeded(), m.pollTick())
}

// patch replaces the rows with a re-read of themselves, leaving the cursor on
// the issue it was on, the scroll where it was and the filter as it was typed.
func (m *Model) patch(msg patchedMsg) tea.Cmd {
	if !m.current(msg.gen) {
		return nil
	}
	m.loading, m.loaded = false, true
	under, before := m.selectedKey(), m.issues
	m.issues, m.page = msg.issues, msg.page
	m.cachedMore, m.stale, m.checked = false, false, m.now()
	m.rows.reset()
	m.relayout()
	m.refilter()
	m.restore(under)
	return tea.Batch(notStored(msg.stored), refreshed(msg.why, before, m.issues),
		m.pageAheadIfNeeded(), m.pollTick())
}

// failed keeps whatever is on screen. Rows that are already drawn are the last
// true answer this session had, so a refusal badges them rather than clearing
// them (docs/UX.md — stale data is badged, not hidden).
//
// With no rows to badge the refusal is all there is, so it is kept. A retarget
// reaches that state as well as a first load: it drops the rows it had before
// the search that replaces them is issued.
func (m *Model) failed(msg failedMsg) tea.Cmd {
	if !m.current(msg.gen) {
		return nil
	}
	m.loading = false
	if len(m.issues) > 0 {
		m.stale = true
	} else {
		m.failure = msg.err
	}
	var limit *jira.RateLimitError
	if errors.As(msg.err, &limit) {
		m.pollPaused = true
	}
	return tea.Batch(failure(msg.why, msg.err), m.pollTick())
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
	if m.loading || m.search == nil || !m.hasMore() {
		return nil
	}
	near := m.cursor >= len(m.view)-lookahead
	starved := m.filtered() && len(m.view) < m.rowsHeight() && len(m.issues) < autoFillCap
	if !near && !starved {
		return nil
	}
	ctx, gen := m.begin()
	// Rows that came off disk carry no cursor to follow, so the page after them
	// is reached by asking the search again and walking to where they end.
	if !m.page.HasMore() {
		return m.reply(reload(ctx, m.search, m.cache, m.jql, len(m.issues)+pageSize, gen, whyPage))
	}
	return m.reply(more(ctx, m.cache, m.jql, m.issues, m.page, gen))
}

// hasMore reports whether anything is left to fetch, from the live page or from
// what the stored rows said about the answer they were part of.
func (m *Model) hasMore() bool { return m.page.HasMore() || m.cachedMore }

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

func (m *Model) selectedIssue() *jira.Issue {
	if m.cursor < 0 || m.cursor >= len(m.view) {
		return nil
	}
	at := m.view[m.cursor]
	if at < 0 || at >= len(m.issues) {
		return nil
	}
	return &m.issues[at]
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

// refilter recomputes which rows the local filter leaves visible, keeping the
// cursor on the row it was on. Everything but typing — a page landing, a
// refresh, a retarget — rebuilds the view for a reason nobody asked for.
func (m *Model) refilter() {
	under := m.selectedKey()
	m.rankRows()
	m.restore(under)
}

// refilterTyped is the keystroke path: typing lands the cursor on the best
// match. Deleting back to nothing has no best match, so that keeps the place.
func (m *Model) refilterTyped() {
	if strings.TrimSpace(m.query) == "" {
		m.refilter()
		return
	}
	m.rankRows()
	m.cursor = 0
}

// rankRows orders the rows the filter leaves best first, and leaves them in the
// order the search returned them when nothing is typed. Both slices it works in
// are the model's own, so a keystroke over ten thousand rows allocates nothing.
func (m *Model) rankRows() {
	m.view, m.ranks = m.view[:0], m.ranks[:0]
	pattern := app.NewPattern(strings.TrimSpace(m.query))
	if pattern.Empty() {
		for i := range m.issues {
			m.view = append(m.view, i)
		}
		return
	}
	for i := range m.issues {
		if score, ok := score(&m.issues[i], pattern); ok {
			m.ranks = append(m.ranks, ranked{at: i, score: score})
		}
	}
	// The pattern decides which rows and the search's own order settles the
	// equals, so the site's sort is never re-ordered into a ranking of ours.
	slices.SortFunc(m.ranks, func(a, b ranked) int {
		if a.score != b.score {
			return b.score - a.score
		}
		return a.at - b.at
	})
	for _, r := range m.ranks {
		m.view = append(m.view, r.at)
	}
}

// ranked is one row's place in the order.
type ranked struct {
	at    int
	score int
}

// fieldPenalty is what finding a row by something other than its key or its
// summary costs: app.Pattern's ranking step nine times over, the calibration the
// palette and the value picker already use.
const fieldPenalty = 9 * 256

// score is the best of the ways a row can be found: the key and the summary
// answer for themselves and the rest of the row pays the penalty. Each field is
// scored on its own because one concatenated haystack matches across the
// boundaries between them, so "flowdone" would find a login flow that is Done.
func score(iss *jira.Issue, p app.Pattern) (int, bool) {
	best, ok := p.Score(iss.Key)
	if other, hit := p.Score(iss.Summary); hit && (!ok || other > best) {
		best, ok = other, true
	}
	for _, field := range [...]string{iss.Status.Name, iss.Type.Name, assigneeName(iss, "")} {
		if other, hit := p.Score(field); hit && (!ok || other-fieldPenalty > best) {
			best, ok = other-fieldPenalty, true
		}
	}
	for _, label := range iss.Labels {
		if other, hit := p.Score(label); hit && (!ok || other-fieldPenalty > best) {
			best, ok = other-fieldPenalty, true
		}
	}
	return best, ok
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
	if m.asking {
		return m.askKey(msg, stroke)
	}
	if m.bind != bindNone {
		return m.bindKey(stroke)
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
	case actClear:
		return m.clearFilter()
	case actAll:
		return m.showEverything()
	case actFilterBy:
		return m.openFilter()
	case actEdit:
		return m.startAsk()
	case actSave:
		m.startBind()
	case actNone, actAccept, actRun, actKeep:
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
		m.dropFilter()
		return nil
	default:
	}
	// The text input's own command is a cursor blink, which is a timer this
	// view would then own for as long as the filter is open. Dropping it costs
	// a blinking block and keeps every frame reproducible.
	m.filter, _ = m.filter.Update(msg)
	if q := m.filter.Value(); q != m.query {
		m.query = q
		m.refilterTyped()
		m.scrollToCursor()
		return m.pageAheadIfNeeded()
	}
	return nil
}

// clearFilter drops a filter that has already been accepted. The kernel takes
// esc in a root view and never forwards it, so without this a filter put on with
// / and enter comes off only by opening it again.
func (m *Model) clearFilter() tea.Cmd {
	if m.query == "" {
		return nil
	}
	m.dropFilter()
	return nil
}

// dropFilter forgets the query and the haystacks that were built to match it.
func (m *Model) dropFilter() {
	m.filter.Reset()
	m.query = ""
	m.refilter()
	m.scrollToCursor()
}

func (m *Model) startFilter() tea.Cmd {
	m.filtering = true
	m.filter.SetWidth(max(m.width-2, 8))
	_ = m.filter.Focus()
	m.clampScroll()
	return nil
}

func (m *Model) startBind() {
	m.bind, m.bindSlot = bindPick, 0
	m.clampScroll()
}

// bindKey takes the digit the gesture is waiting for. Anything else ends it,
// so there is no mode to guess a way out of, and a key another query holds is
// named in a confirmation before it changes hands.
func (m *Model) bindKey(stroke string) tea.Cmd {
	step := m.bind
	m.bind = bindNone
	switch step {
	case bindPick:
		slot, err := strconv.Atoi(stroke)
		if err != nil || slot < 1 || slot > app.MaxSavedSlot {
			m.clampScroll()
			return nil
		}
		held, taken := m.saved.BySlot(slot)
		if taken && !strings.EqualFold(held.Name, m.title) {
			m.bind, m.bindSlot = bindConfirm, slot
			return nil
		}
		return m.commitBind(slot)
	case bindConfirm:
		slot := m.bindSlot
		m.bindSlot = 0
		if stroke != "y" {
			m.clampScroll()
			return nil
		}
		return m.commitBind(slot)
	case bindNone:
	}
	return nil
}

func (m *Model) commitBind(slot int) tea.Cmd {
	m.bind, m.bindSlot = bindNone, 0
	m.clampScroll()
	return kernel.BindQuery(m.title, m.jql, slot)
}

// bindPrompt is the line the gesture puts under the rows: what is being bound,
// and what will be lost if it goes ahead. The name is what gives way on a
// narrow terminal, never the keys that answer.
func (m *Model) bindPrompt() string {
	label := "bind " + strconv.Quote(m.title) + " to a key"
	hint := "  1-" + strconv.Itoa(app.MaxSavedSlot) + ", any other key cancels"
	if m.bind == bindConfirm {
		held, _ := m.saved.BySlot(m.bindSlot)
		label = strconv.Itoa(m.bindSlot) + " runs " + strconv.Quote(held.Name)
		hint = "  y replaces it, any other key cancels"
	}
	room := max(m.width-ansi.StringWidth(hint), 8)
	return m.styles.prompt.Render(ansi.Truncate(label, room, m.deps.Theme.Glyphs.Ellipsis)) +
		m.styles.muted.Render(hint)
}

func (m *Model) open() tea.Cmd {
	if m.selectedKey() == "" {
		return nil
	}
	iss := m.issues[m.view[m.cursor]]
	return kernel.Push(issue.ViewID, iss.Key, issue.New(m.deps, iss))
}

// click narrows the rows to the cell under the pointer, or selects the row that
// cell is on and opens it on a double-click. Bubble Tea v2 reports neither a
// click count nor an instant, so the second click is timed against this
// session's clock rather than inferred from the row already being selected.
func (m *Model) click(msg tea.MouseClickMsg) tea.Cmd {
	if msg.Button != tea.MouseLeft {
		return nil
	}
	if m.zones.Hit(titleZone, msg) {
		m.clicks.Forget()
		return m.startAsk()
	}
	if cmd, dropped := m.clickTerm(msg); dropped {
		m.clicks.Forget()
		return cmd
	}
	for i := m.top; i < min(m.top+m.rowsHeight(), len(m.view)); i++ {
		iss := &m.issues[m.view[i]]
		if cmd, narrowed := m.clickFacet(msg, iss); narrowed {
			m.clicks.Forget()
			return cmd
		}
		if !m.zones.Hit(rowZone(iss.Key), msg) {
			continue
		}
		if m.clicks.Double(rowZone(iss.Key)) {
			m.cursor = i
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
	if len(m.terms) > 0 {
		lines = append(lines, m.termsLine())
	}
	if m.keptFilter() {
		lines = append(lines, m.filterChip())
	}
	if m.filtering {
		lines = append(lines, m.filter.View())
	}
	if m.asking {
		lines = append(lines, m.askPrompt())
	}
	if m.bind != bindNone {
		lines = append(lines, m.bindPrompt())
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
	s := renderRow(iss, m.lay, selected, m.styles, m.deps.Theme, m.deps.Caps.Location(), m.now(), m.zones)
	s = m.zones.Mark(rowZone(iss.Key), s)
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
	stale           bool
	failed          bool
	checked         int64
}

func (m *Model) summaryKey() summaryKey {
	return summaryKey{
		title: m.title, width: m.width, gen: m.styles.gen,
		issues: len(m.issues), visible: len(m.view), more: m.hasMore(),
		loading: m.loading, loaded: m.loaded, filtered: m.filtered(),
		stale: m.stale, failed: m.failure != nil, checked: m.checked.UnixNano(),
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
	badge := ""
	if m.stale {
		badge = m.deps.Theme.StaleBadge.Render(staleLabel)
	}
	stamp := m.checkedLabel()
	right := ansi.StringWidth(stamp) + ansi.StringWidth(badge) + ansi.StringWidth(count)
	title := ansi.Truncate(m.title, max(m.width-right-1, 1), ell)
	pad := max(m.width-ansi.StringWidth(title)-right, 1)
	m.summary = m.zones.Mark(titleZone, m.styles.title.Render(title)) +
		strings.Repeat(" ", pad) + stamp + badge + m.styles.count.Render(count)
	m.sumKey = key
	return m.summary
}

// checkedLabel says when what is on screen last came from the site, in the
// account's own timezone like every other instant this view draws. It is the
// half of an observable refresh that stays: pressing r twice a minute apart
// leaves two different times behind, whatever the answer was both times.
func (m *Model) checkedLabel() string {
	if m.checked.IsZero() {
		return ""
	}
	return m.styles.muted.Render("checked " + m.checked.In(m.deps.Caps.Location()).Format("15:04") + "  ")
}

// staleLabel is a word and not a glyph: the glyph beside the count already means
// a refresh is in flight, which is the opposite state.
const staleLabel = "stale"

func (m *Model) countLabel() string {
	var b strings.Builder
	switch {
	case !m.loaded && m.loading:
		b.WriteString("searching")
	case m.failure != nil:
		// "0 issues" here would be a count of an answer nobody got.
		b.WriteString("no answer")
	case m.filtered():
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
	if m.hasMore() {
		return n + "+"
	}
	return n
}

// appendEmpty says which kind of empty this is. Five of them are told apart
// here, and the one that failed is the only one that also has to say what to do
// about it.
func (m *Model) appendEmpty(lines []string, h int) []string {
	at := len(lines)
	switch {
	case m.search == nil:
		lines = append(lines, m.styles.muted.Render("  No Jira connection in this session yet."))
	case m.loading && !m.loaded:
		lines = append(lines, m.styles.muted.Render("  Searching"+m.deps.Theme.Glyphs.Ellipsis))
	case m.failure != nil:
		lines = m.appendFailure(lines, h)
	case !m.loaded:
		lines = append(lines, m.styles.muted.Render("  Nothing has been asked of Jira yet."))
	case m.filtered():
		lines = append(lines, m.styles.muted.Render("  No loaded row matches "+strconv.Quote(m.query)+"."))
	default:
		lines = append(lines, m.styles.muted.Render("  Nothing matches this search."),
			m.styles.muted.Render("  "+m.jql))
		if m.defaulted && m.assignedNowhere {
			lines = append(lines, "", m.styles.muted.Render("  "+nothingAssignedPane))
		}
	}
	for len(lines)-at < h {
		lines = append(lines, "")
	}
	return lines[:at+h]
}

// appendFailure is what the pane says instead of rows: the reason in the error's
// own words, the query it was asked about, and the key that runs it again. The
// reason is wrapped rather than cut, since a transport failure names a host and a
// port before it says what is wrong with them.
func (m *Model) appendFailure(lines []string, h int) []string {
	reason, _ := jira.Reason(m.failure)
	lines = append(lines, m.styles.danger.Render("  The search failed."))
	room := max(m.width-2, 8)
	said := strings.Split(ansi.Wrap(reason, room, ""), "\n")
	for _, line := range said[:min(len(said), max(h-3, 1))] {
		lines = append(lines, m.styles.muted.Render("  "+line))
	}
	return append(lines,
		m.styles.muted.Render("  "+ansi.Truncate(m.jql, room, m.deps.Theme.Glyphs.Ellipsis)),
		"",
		m.styles.muted.Render("  "+retryHint))
}

// keptFilter reports that a filter is narrowing the rows while nothing is being
// typed into it, which is the state with no prompt of its own to show it.
func (m *Model) keptFilter() bool { return m.query != "" && !m.filtering }

// The two sentences that name a key, spelt from the binding rather than written
// out. The retry names the kernel's own refresh, which this view registers
// nothing for.
var (
	retryHint = kernel.DefaultGlobalKeys().Refresh.Help().Key + " tries the search again."
	clearHint = "  " + defaultKeys().Unfilter.Help().Key + " clears it"
)
