// Package timeline draws a project as horizontal bars over a calendar axis,
// each bar carrying which rule of the date cascade produced it.
package timeline

import (
	"context"
	"slices"
	"strconv"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"

	appterm "github.com/varijkapil13/saral/internal/app/term"
	apptimeline "github.com/varijkapil13/saral/internal/app/timeline"
	"github.com/varijkapil13/saral/internal/ui/filter"
	"github.com/varijkapil13/saral/internal/ui/issue"
	"github.com/varijkapil13/saral/internal/ui/kernel"
	"github.com/varijkapil13/saral/internal/ui/widget"
	"github.com/varijkapil13/saral/internal/ui/widget/filterbar"
	"github.com/varijkapil13/saral/pkg/jira"
)

// ViewID is the name this view registers itself under and the scope its keys are
// registered in.
const ViewID = "timeline"

var (
	_ kernel.View      = (*Model)(nil)
	_ kernel.Addressed = (*Model)(nil)
)

// ZoomMsg sets the zoom level exactly. It is exported so the palette reaches the
// same code the keys do rather than a second implementation of it.
type ZoomMsg struct{ Zoom Zoom }

// ZoomStepMsg moves one level finer or coarser, which is what + and - do.
type ZoomStepMsg struct{ Finer bool }

// TodayMsg centres the chart on today.
type TodayMsg struct{}

// NotesMsg opens the pane naming what could not be worked out.
type NotesMsg struct{}

// barRow is one issue's line: what it is called, where it sits, and where in the
// issues slice to find the whole of it when it is opened.
type barRow struct {
	key     string
	summary string
	rng     apptimeline.Range
	at      int
}

// versionMark is a release date drawn above the bars.
type versionMark struct {
	name string
	on   jira.Date
}

// sprintMark is a sprint's two boundaries drawn above the bars.
type sprintMark struct {
	name string
	from jira.Date
	to   jira.Date
}

// Model is the timeline.
type Model struct {
	deps      kernel.Deps
	loader    *apptimeline.Loader
	inChart   map[string]action
	pendingGo bool
	inNotes   map[string]action
	styles    *styles
	memo      *widget.RowCache[rowKey, string]

	jql   string
	title string

	fields apptimeline.DateFields
	res    apptimeline.Resolution
	issues []jira.Issue
	rows   []barRow

	// terms is this program's own narrowing — a person, a status, a type, a
	// priority or a label — applied locally against what is already loaded, the
	// way board.terms and backlog.terms are and for the same reason: a chart's
	// own read is already whole in memory.
	terms appterm.Terms
	// termsGen counts the changes to them, because a slice cannot be part of the
	// comparable key the bar is memoized on.
	termsGen int
	// bar draws the chip line naming the terms in force.
	bar *filterbar.Bar
	// filteredOut counts an issue take left off the chart because a term did,
	// as distinct from one no rule could date.
	filteredOut int
	// resolvedShown is how many of the rows actually drawn carry a date, as
	// opposed to Resolution.Resolved, which counts the whole read before a term
	// took any of it off screen — "3 of 3000 dated" would be what a term left
	// the summary saying otherwise.
	resolvedShown int

	versionMarks []versionMark
	sprintMarks  []sprintMark
	markerNotes  []string
	// marksGen counts the changes to the two marker sets, because a slice cannot
	// be part of the comparable key the lines drawing them are memoized on.
	marksGen int

	notes     bool
	noteLines []string
	noteTop   int

	zoom Zoom
	ax   axis
	left int

	cursor int
	top    int

	width, height int
	lay           layout

	// lines is the frame under construction, kept between frames so that drawing
	// a screen does not allocate one slice per frame.
	lines []string

	summary string
	sumKey  summaryKey
	heading string
	axisAt  axisKey
	ruler   string
	rulerAt axisKey

	versions     string
	sprints      string
	hasVersions  bool
	hasSprints   bool
	marksAt      markerKey
	marksBuilt   bool
	detail       string
	detailAt     detailKey
	noteCount    string
	noteCountAt  noteCountKey
	noteCountSet bool

	truncated bool
	missing   []string

	// badge says why what is on screen may not be what the site holds: rows off
	// disk were dated by a cascade that had not read the site's fields yet, and a
	// refresh that failed leaves the last true answer behind.
	badge   string
	checked time.Time

	cfgStart []string
	cfgEnd   []string

	loading bool
	loaded  bool
	gen     int
	// The two reads a fetch makes each hold their own cancel. One shared between
	// them would be released by whichever finished first, which cancelled the
	// markers read the moment the issues landed.
	cancel      context.CancelFunc
	cancelMarks context.CancelFunc
	addr        kernel.Addr
	failure     error

	zones  widget.Zoner
	clicks *widget.Clicks
}

// New builds the timeline. It opens on the whole of the session's project in
// ascending date order, which is the only search a chart of a project can mean.
func New(d kernel.Deps) kernel.View {
	m := &Model{
		deps:   d,
		addr:   kernel.NewAddr(),
		loader: apptimeline.NewLoader(d.Jira, d.Cache),
		styles: newStyles(themeOf(d)),
		memo:   widget.NewRowCache[rowKey, string](rowCacheLimit),
		zoom:   ZoomWeek,
	}
	m.deps.Theme = themeOf(d)
	m.zones = widget.NewZoner(d.Zones)
	m.clicks = widget.NewClicks(d.Now)
	m.bar = filterbar.New(m.zones)
	m.inChart, m.inNotes = defaultKeys().tables()
	m.cfgStart, m.cfgEnd = configuredFields(d.Site)
	m.jql, m.title = defaultQuery(d.Project)
	if terms, ok := m.recallTerms(); ok {
		m.terms = terms
	}
	m.relayout()
	m.fromCache()
	return m
}

func themeOf(d kernel.Deps) *kernel.Theme {
	if d.Theme != nil {
		return d.Theme
	}
	return kernel.NewTheme(kernel.ThemeAuto, true, kernel.UnicodeGlyphs())
}

// defaultQuery is every issue in the session's project, oldest first. Nothing
// about the project is written down: the key is whatever the session opened on.
func defaultQuery(project string) (jql, title string) {
	key := strings.TrimSpace(project)
	if key == "" {
		return "ORDER BY created ASC", "Timeline"
	}
	return "project = " + strconv.Quote(key) + " ORDER BY created ASC", "Timeline of " + key
}

// fromCache draws the rows the last session left on disk before anything is
// asked of the site (docs/UX.md principle 1). A bar it draws can move a rule
// when the real read lands, which is what the badge says.
func (m *Model) fromCache() {
	snap, ok := m.loader.Stored(m.jql, m.cascade())
	if !ok {
		return
	}
	m.take(snap.Resolution, snap.Issues, true)
	m.loaded, m.badge, m.checked = true, "stored", snap.StoredAt
}

// take replaces the chart with a resolved pass. recentre is for a chart the user
// has not looked at yet: a refresh must leave the window where it was.
func (m *Model) take(res apptimeline.Resolution, issues []jira.Issue, recentre bool) {
	under := m.selectedKey()
	m.res, m.issues = res, issues
	m.rows, m.filteredOut, m.resolvedShown = m.rows[:0], 0, 0
	seen := make(map[string]bool, len(issues))
	for i := range issues {
		key := issues[i].Key
		if key == "" || seen[key] {
			continue
		}
		seen[key] = true
		if !m.terms.Match(&issues[i]) {
			m.filteredOut++
			continue
		}
		rng, _ := res.Range(key)
		if rng.OK() {
			m.resolvedShown++
		}
		m.rows = append(m.rows, barRow{key: widget.Sanitize(key), summary: widget.Sanitize(issues[i].Summary), rng: rng, at: i})
	}
	slices.SortFunc(m.rows, byStartThenKey)
	m.memo.Reset()
	m.relayout()
	m.reaxis(recentre)
	m.restore(under)
	m.buildNotes()
}

// byStartThenKey puts the chart in the order a reader scans it: earliest first,
// and everything with no date at the bottom rather than at the top.
func byStartThenKey(a, b barRow) int {
	if c := apptimeline.CompareRanges(a.rng, b.rng); c != 0 {
		return c
	}
	return strings.Compare(a.key, b.key)
}

func (m *Model) buildNotes() {
	m.noteLines = m.noteLines[:0]
	for _, problem := range m.fields.Problems() {
		m.noteLines = append(m.noteLines, problem.String())
	}
	if len(m.missing) > 0 {
		m.noteLines = append(m.noteLines, "this site has no field called "+strings.Join(m.missing, ", "))
	}
	m.noteLines = append(m.noteLines, m.markerNotes...)
	m.noteLines = append(m.noteLines, m.res.Warnings()...)
	if m.truncated {
		m.noteLines = append(m.noteLines,
			"this search has more than "+strconv.Itoa(apptimeline.MaxIssues)+" issues in it and the chart holds the first "+
				strconv.Itoa(apptimeline.MaxIssues)+" by creation date")
	}
	m.noteTop = 0
	m.noteCountSet = false
}

// Addr is where the kernel delivers what this chart asked for, whatever has
// since been pushed over it and whichever root is on screen.
func (m *Model) Addr() kernel.Addr { return m.addr }

func (m *Model) reply(cancel context.CancelFunc, cmd tea.Cmd) tea.Cmd {
	if cmd == nil {
		return nil
	}
	return kernel.Reply(withCancel(cancel, cmd), m.addr)
}

// Init asks the site for the fields the cached pass could not have.
func (m *Model) Init() tea.Cmd { return m.load() }

// Update handles one message.
func (m *Model) Update(msg tea.Msg) (kernel.View, tea.Cmd) {
	var cmd tea.Cmd
	switch msg := msg.(type) {
	case kernel.SizeMsg:
		m.resize(msg.Width, msg.Height)

	case kernel.FocusMsg:

	case kernel.ThemeMsg:
		m.deps.Theme = msg.Theme
		m.styles = newStyles(msg.Theme)
		m.memo.Reset()
		m.marksBuilt, m.noteCountSet = false, false
		m.summary, m.heading, m.ruler, m.detail = "", "", "", ""

	case kernel.SetMouseMsg:
		m.termsGen++
		m.memo.Reset()
		m.marksBuilt = false
		m.summary, m.heading, m.ruler, m.detail = "", "", "", ""

	case kernel.CapabilitiesMsg:
		m.deps.Caps = msg.Caps
		m.memo.Reset()

	case kernel.ProjectMsg:
		cmd = m.reproject(msg.Project)

	case kernel.RefreshMsg:
		cmd = m.refresh(msg.Purge)

	case ZoomMsg:
		cmd = m.setZoom(msg.Zoom)

	case ZoomStepMsg:
		cmd = m.step(msg.Finer)

	case TodayMsg:
		m.centreOn(m.today())

	case NotesMsg:
		m.toggleNotes()

	case filter.ChosenMsg:
		cmd = m.applyFilterTerm(msg.Term)

	case OpenFilterMsg:
		cmd = m.openFilterPicker()

	case ClearFilterMsg:
		cmd = m.clearFilter()

	case loadedMsg:
		cmd = m.landed(msg)

	case markersMsg:
		m.marked(msg)

	case failedMsg:
		cmd = m.failed(msg)

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
	m.relayout()
	m.reaxis(false)
	m.scrollToCursor()
}

func (m *Model) relayout() {
	lay := planLayout(m.width, m.widestKey())
	if lay == m.lay {
		return
	}
	m.lay = lay
	m.memo.Reset()
}

func (m *Model) widestKey() int {
	widest := keyMin
	for i := range m.rows {
		if n := ansi.StringWidth(m.rows[i].key); n > widest {
			widest = n
		}
		if widest >= keyMax {
			return keyMax
		}
	}
	return widest
}

// rowsHeight is how many bars fit once the lines around them have taken theirs.
func (m *Model) rowsHeight() int { return m.chrome().rows }

func (m *Model) hasNoteCount() bool {
	return (m.loaded && len(m.rows) > 0 && m.resolvedShown == 0) || len(m.noteLines) > 0
}

// --- fetching ---------------------------------------------------------------

func (m *Model) begin() (issues, marks context.Context, gen int) {
	m.stop()
	m.gen++
	issues, cancel := context.WithCancel(context.Background())
	m.cancel = cancel
	marks, cancelMarks := context.WithCancel(context.Background())
	m.cancelMarks = cancelMarks
	m.loading, m.failure = true, nil
	return issues, marks, m.gen
}

func (m *Model) stop() {
	for _, cancel := range [...]*context.CancelFunc{&m.cancel, &m.cancelMarks} {
		if *cancel != nil {
			(*cancel)()
			*cancel = nil
		}
	}
	m.loading = false
}

func (m *Model) current(gen int) bool { return gen == m.gen }

func (m *Model) cascade() apptimeline.Config {
	return apptimeline.Config{Start: m.cfgStart, End: m.cfgEnd, Caps: m.deps.Caps, Now: m.now}
}

func (m *Model) load() tea.Cmd {
	if !m.loader.Live() {
		return nil
	}
	ctx, marks, gen := m.begin()
	return tea.Batch(
		m.reply(m.cancel, load(ctx, m.loader, m.cascade(), m.jql, gen)),
		m.reply(m.cancelMarks, markers(marks, m.loader, m.deps.Project, m.deps.Caps, gen)),
	)
}

func (m *Model) refresh(purge bool) tea.Cmd {
	if !m.loader.Live() {
		return nil
	}
	var said tea.Cmd
	if purge {
		if err := m.loader.Purge(m.jql); err != nil {
			said = kernel.Warn("the stored copy of this timeline could not be dropped: " + err.Error())
		}
	}
	return tea.Batch(said, m.load())
}

func (m *Model) reproject(project string) tea.Cmd {
	if project == m.deps.Project {
		return nil
	}
	was := m.deps.Project
	m.deps.Project = project
	var said tea.Cmd
	m.terms, said = filterbar.Reproject(m.deps, ViewID, was, m.terms)
	m.termsGen++
	m.jql, m.title = defaultQuery(project)
	m.issues, m.rows, m.loaded, m.badge = nil, m.rows[:0], false, ""
	m.res, m.fields = apptimeline.Resolution{}, apptimeline.DateFields{}
	m.versionMarks, m.sprintMarks, m.markerNotes = nil, nil, nil
	m.marksGen, m.marksBuilt = m.marksGen+1, false
	m.cursor, m.top, m.checked = 0, 0, time.Time{}
	m.memo.Reset()
	m.buildNotes()
	m.reaxis(true)
	m.fromCache()
	return tea.Batch(said, m.load())
}

func (m *Model) landed(msg loadedMsg) tea.Cmd {
	if !m.current(msg.gen) {
		return nil
	}
	first := !m.loaded || m.badge != ""
	m.loading, m.loaded = false, true
	m.fields, m.missing, m.truncated = msg.Fields, msg.Missing, msg.Truncated
	m.badge, m.checked = "", m.now()
	m.take(msg.Resolution, msg.Issues, first)
	return notStored(msg.Stored)
}

func (m *Model) marked(msg markersMsg) {
	if !m.current(msg.gen) {
		return
	}
	m.versionMarks = m.versionMarks[:0]
	for _, r := range msg.Releases() {
		m.versionMarks = append(m.versionMarks, versionMark{name: r.Name, on: r.On})
	}
	m.sprintMarks = m.sprintMarks[:0]
	zone, _ := m.deps.Caps.Zone()
	for _, s := range msg.SprintSpans(zone) {
		m.sprintMarks = append(m.sprintMarks, sprintMark{name: s.Name, from: s.From, to: s.To})
	}
	m.markerNotes = msg.Notes
	m.marksGen, m.marksBuilt = m.marksGen+1, false
	m.reaxis(false)
	m.buildNotes()
}

// failed keeps whatever is on screen. Bars that are already drawn are the last
// true answer this session had, so a refusal badges them rather than clearing
// them; with nothing to badge, the refusal is all there is and it is kept.
func (m *Model) failed(msg failedMsg) tea.Cmd {
	if !m.current(msg.gen) {
		return nil
	}
	m.loading = false
	if len(m.rows) > 0 {
		m.badge = "stale"
	} else {
		m.failure = msg.err
	}
	m.summary, m.noteCountSet = "", false
	return kernel.Fail(msg.err)
}

// --- the axis ---------------------------------------------------------------

// reaxis rebuilds the calendar the chart is drawn over. The span covers every
// bar, every marker and today, so the today line is always somewhere reachable.
func (m *Model) reaxis(recentre bool) {
	from, to := m.res.Span()
	today := m.today()
	from, to = earliest(from, today), latest(to, today)
	for _, v := range m.versionMarks {
		from, to = earliest(from, v.on), latest(to, v.on)
	}
	for _, s := range m.sprintMarks {
		from, to = earliest(from, s.from), latest(to, s.to)
	}
	was := m.ax
	m.ax = newAxis(m.zoom, from, to)
	if was != m.ax {
		m.marksBuilt = false
	}
	if recentre {
		m.centreOn(today)
		return
	}
	m.clampLeft()
}

// step moves one zoom level and says so when there is none left, because a key
// that answers with nothing and a key that is not bound look the same.
func (m *Model) step(finer bool) tea.Cmd {
	want := m.zoom.out()
	if finer {
		want = m.zoom.in()
	}
	if want == m.zoom {
		if finer {
			return kernel.Status("one column is already one " + m.zoom.String() + ", which is the finest there is")
		}
		return kernel.Status("one column is already one " + m.zoom.String() + ", which is the coarsest there is")
	}
	return m.setZoom(want)
}

func (m *Model) centreOn(d jira.Date) {
	if m.ax.empty() || d.IsZero() {
		return
	}
	m.left = m.ax.col(d) - m.lay.chart/2
	m.clampLeft()
}

func (m *Model) clampLeft() {
	m.left = min(max(m.left, 0), max(m.ax.cols-m.lay.chart, 0))
}

// setZoom keeps the day in the middle of the chart in the middle of the chart, so
// that zooming is a change of detail rather than a change of place.
func (m *Model) setZoom(z Zoom) tea.Cmd {
	if z < ZoomDay || z >= zoomCount {
		return nil
	}
	if z == m.zoom {
		return kernel.Status("one column is already one " + z.String())
	}
	// The middle of the chart only names a day while there is more calendar than
	// chart. Where the whole span fits, there is no middle to keep and today is
	// the day worth being on.
	centre := m.today()
	if m.ax.cols > m.lay.chart {
		centre = m.ax.start(m.left + m.lay.chart/2)
	}
	m.zoom = z
	m.memo.Reset()
	m.reaxis(false)
	m.centreOn(centre)
	return nil
}

func earliest(a, b jira.Date) jira.Date {
	switch {
	case b.IsZero():
		return a
	case a.IsZero() || b.Before(a):
		return b
	default:
		return a
	}
}

func latest(a, b jira.Date) jira.Date {
	switch {
	case b.IsZero():
		return a
	case a.IsZero() || a.Before(b):
		return b
	default:
		return a
	}
}

// --- selection --------------------------------------------------------------

func (m *Model) selected() *barRow {
	if m.notes || m.cursor < 0 || m.cursor >= len(m.rows) {
		return nil
	}
	return &m.rows[m.cursor]
}

func (m *Model) selectedKey() string {
	if m.cursor < 0 || m.cursor >= len(m.rows) {
		return ""
	}
	return m.rows[m.cursor].key
}

// restore puts the cursor back on an issue by key, so a refresh that reordered
// the chart does not move the reader's place.
func (m *Model) restore(key string) {
	if key != "" {
		for i := range m.rows {
			if m.rows[i].key == key {
				m.cursor = i
				m.scrollToCursor()
				return
			}
		}
	}
	m.cursor = min(max(m.cursor, 0), max(len(m.rows)-1, 0))
	m.scrollToCursor()
}

func (m *Model) moveTo(at int) {
	if len(m.rows) == 0 {
		m.cursor, m.top = 0, 0
		return
	}
	m.cursor = min(max(at, 0), len(m.rows)-1)
	m.scrollToCursor()
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
	m.top = min(max(m.top, 0), max(len(m.rows)-m.rowsHeight(), 0))
}

func (m *Model) toggleNotes() {
	m.notes = !m.notes
	m.noteTop = 0
	m.clampScroll()
}

// --- input ------------------------------------------------------------------

func (m *Model) key(msg tea.KeyPressMsg) tea.Cmd {
	stroke := msg.String()
	if m.notes {
		return m.notesKey(stroke)
	}
	if m.pendingGo {
		m.pendingGo = false
		switch stroke {
		case "g":
			m.moveTo(0)
			return nil
		case "e":
			m.moveTo(len(m.rows) - 1)
			return nil
		}
	}
	switch m.inChart[stroke] {
	case actGo:
		m.pendingGo = true
	case actDown:
		m.moveTo(m.cursor + 1)
	case actUp:
		m.moveTo(m.cursor - 1)
	case actPageDown:
		m.moveTo(m.cursor + m.rowsHeight())
	case actPageUp:
		m.moveTo(m.cursor - m.rowsHeight())
	case actTop:
		m.moveTo(0)
	case actBottom:
		m.moveTo(len(m.rows) - 1)
	case actEarlier:
		m.pan(-m.panStep())
	case actLater:
		m.pan(m.panStep())
	case actOpen:
		return m.open()
	case actZoomIn:
		return m.step(true)
	case actZoomOut:
		return m.step(false)
	case actToday:
		m.centreOn(m.today())
	case actNotes:
		m.toggleNotes()
	case actFilterBy:
		return m.openFilterPicker()
	case actUnfilter:
		return m.clearFilter()
	case actNone:
	}
	return nil
}

func (m *Model) notesKey(stroke string) tea.Cmd {
	switch m.inNotes[stroke] {
	case actDown:
		m.noteTop++
	case actUp:
		m.noteTop = max(m.noteTop-1, 0)
	case actPageDown:
		m.noteTop += m.height - 1
	case actPageUp:
		m.noteTop = max(m.noteTop-(m.height-1), 0)
	case actNotes:
		m.toggleNotes()
	default:
	}
	return nil
}

// panStep is a quarter of the chart, so that one press of h or l moves a
// readable distance whether a column is a day or a quarter.
func (m *Model) panStep() int { return max(m.lay.chart/4, 1) }

func (m *Model) pan(by int) {
	m.left += by
	m.clampLeft()
}

func (m *Model) open() tea.Cmd {
	r := m.selected()
	if r == nil || r.at < 0 || r.at >= len(m.issues) {
		return nil
	}
	iss := m.issues[r.at]
	return kernel.Push(issue.ViewID, iss.Key, issue.New(m.deps, iss))
}

// click selects the bar under the pointer, and opens it on a real double-click.
// Bubble Tea reports neither a click count nor an instant, so the pair is timed
// against this session's clock rather than inferred from the row being selected.
func (m *Model) click(msg tea.MouseClickMsg) tea.Cmd {
	if msg.Button != tea.MouseLeft || m.notes {
		return nil
	}
	if cmd, dropped := m.clickTerm(msg); dropped {
		return cmd
	}
	for i := m.top; i < min(m.top+m.rowsHeight(), len(m.rows)); i++ {
		if !m.zones.Hit(rowZone(m.rows[i].key), msg) {
			continue
		}
		if m.clicks.Double(rowZone(m.rows[i].key)) {
			m.cursor = i
			return m.open()
		}
		m.moveTo(i)
		return nil
	}
	return nil
}

// wheel scrolls without moving the selection, and pans the calendar on the two
// horizontal buttons a trackpad sends.
func (m *Model) wheel(msg tea.MouseWheelMsg) {
	switch msg.Button {
	case tea.MouseWheelUp:
		if m.notes {
			m.noteTop = max(m.noteTop-widget.WheelStep, 0)
			return
		}
		m.top -= widget.WheelStep
	case tea.MouseWheelDown:
		if m.notes {
			m.noteTop += widget.WheelStep
			return
		}
		m.top += widget.WheelStep
	case tea.MouseWheelLeft:
		m.pan(-widget.WheelStep)
		return
	case tea.MouseWheelRight:
		m.pan(widget.WheelStep)
		return
	default:
		return
	}
	m.clampScroll()
}
