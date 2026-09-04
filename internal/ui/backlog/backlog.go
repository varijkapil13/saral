// Package backlog is a board's backlog: the issues waiting to be scheduled, the
// open sprints they can go into, and the moves between the two.
package backlog

import (
	"context"
	"slices"
	"strconv"
	"strings"

	tea "charm.land/bubbletea/v2"

	"github.com/varijkapil13/saral/internal/app"
	"github.com/varijkapil13/saral/internal/ui/filter"
	"github.com/varijkapil13/saral/internal/ui/kernel"
	"github.com/varijkapil13/saral/internal/ui/widget"
	"github.com/varijkapil13/saral/internal/ui/widget/filterbar"
	"github.com/varijkapil13/saral/pkg/jira"
)

// ViewID is the name this view registers itself under and the scope its keys
// are registered in.
const ViewID = "backlog"

const (
	pageSize      = 50
	lookahead     = 12
	rowCacheLimit = 1024
	// sprintLimit bounds the walk over a board's open sprints. Two active and a
	// handful of future ones is the shape; a board with two hundred of them is
	// one nothing can draw anyway.
	sprintLimit = 200
	// moveChunk is the most issues either move endpoint takes in one call.
	//
	// The view chunks rather than handing a whole selection to the port because a
	// chunk is the only unit it can report progress in, and because the adapter's
	// own partial-failure type lives in pkg/jira/cloud, which nothing under
	// internal/ui may name.
	moveChunk = 50
)

// backlogName is what the last section is called. It is this program's word for
// "in no open sprint on this board" and never read off the site.
const backlogName = "Backlog"

var (
	_ kernel.View        = (*Model)(nil)
	_ kernel.KeyCapturer = (*Model)(nil)
	_ kernel.Addressed   = (*Model)(nil)
	_ kernel.Blocker     = (*Model)(nil)
)

// site is the narrow slice of the port one read of the backlog needs.
type site interface {
	jira.BoardReader
	jira.SprintReader
}

type mode uint8

const (
	browsing mode = iota
	choosing
	confirming
	movingIssues
)

// group is one section: an open sprint, or the backlog itself as the last one.
type group struct {
	// id is the sprint's, and zero for the backlog.
	id     int64
	name   string
	state  jira.SprintState
	issues []int
}

// row is one drawn line: a section head, or an issue inside one.
type row struct {
	head  bool
	group int
	issue int
}

// move is a chunked move in flight. Keys is the whole selection in row order,
// and done counts the chunks the site has confirmed.
type move struct {
	dest   int
	name   string
	id     int64
	keys   []string
	chunks int
	done   int
	moved  int
}

func (mv *move) chunk(at int) []string {
	start := at * moveChunk
	if start >= len(mv.keys) {
		return nil
	}
	return mv.keys[start:min(start+moveChunk, len(mv.keys))]
}

// Model is the board's backlog.
type Model struct {
	deps   kernel.Deps
	search *app.Search
	site   site
	mover  jira.SprintManager
	addr   kernel.Addr

	styles *styles
	memo   *rowCache
	zones  widget.Zoner
	clicks *widget.Clicks
	drag   widget.Drag

	acts      map[string]action
	inChooser map[string]action
	inConfirm map[string]action

	width, height int
	lay           layout
	lines         []string
	head          string
	headOf        headKey
	noted         lineCache
	picks         lineCache
	outcome       lineCache

	boards  []jira.Board
	boardAt int
	config  jira.BoardConfig
	sprints []jira.Sprint
	field   jira.FieldRef
	issues  []jira.Issue
	byKey   map[string]int
	page    jira.Page[jira.Issue]
	missing []string

	groups []group
	rows   []row
	picked map[string]bool

	// terms is this program's own narrowing — a person, a status, a type, a
	// priority or a label — applied locally against what is already loaded, the
	// way board.terms is and for the same reason: a backlog's own read is
	// already whole in memory.
	terms filter.Terms
	// termsGen counts the changes to them, because a slice cannot be part of the
	// comparable key the bar is memoized on.
	termsGen int
	// bar draws the chip line naming the terms in force.
	bar *filterbar.Bar
	// filteredOut counts an issue regroup placed in no section because a term
	// left it out, as distinct from one the done category excluded.
	filteredOut int

	cursor    int
	top       int
	pendingGo bool

	gen     int
	cancel  context.CancelFunc
	loading bool
	loaded  bool
	failure error
	// absent is why there is nothing to draw that is neither a failure nor a
	// load still running: no project, no board on it, or no sprint field to tell
	// a scheduled issue from an unscheduled one.
	absent string

	mode     mode
	destAt   int
	mv       *move
	moveCtx  context.Context
	moveStop context.CancelFunc
	// wanted is the selection a move was started on, taken once so that the
	// confirm names the same issues it will move and a frame costs no walk.
	wanted []string
	// said is the outcome of the last move. The status line is overwritten by
	// the next thing that happens and a half-finished move has to keep saying
	// which half finished.
	said string

	focused bool
}

// New builds the backlog. It draws its first frame from nothing but the size it
// is given: which board, which sprints and which issues are all answers, and it
// has not asked for them yet.
func New(d kernel.Deps) kernel.View {
	m := &Model{
		deps:   d,
		addr:   kernel.NewAddr(),
		picked: make(map[string]bool),
		byKey:  make(map[string]int),
	}
	if m.deps.Theme == nil {
		m.deps.Theme = kernel.NewTheme(kernel.ThemeAuto, true, kernel.UnicodeGlyphs())
	}
	m.styles = newStyles(m.deps.Theme)
	m.memo = newRowCache(rowCacheLimit)
	m.zones = widget.NewZoner(d.Zones)
	m.clicks = widget.NewClicks(d.Now)
	m.bar = filterbar.New(m.zones)
	m.acts, m.inChooser, m.inConfirm = defaultKeys().tables()
	if d.Jira != nil {
		m.search = app.NewSearch(d.Jira)
		m.site = d.Jira
		m.mover = d.Jira
	}
	m.relayout()
	return m
}

// WantsRawKeys is true while a destination is being chosen and while a move is
// waiting on its y. Without it the kernel keeps esc for going back and the
// digits for the saved queries, so the two questions this view asks could be
// answered by nobody.
func (m *Model) WantsRawKeys() bool { return m.mode == choosing || m.mode == confirming }

// BlocksClose refuses to throw away a move that is part way through. The chunks
// already accepted have moved and the rest have not, and a program that exits
// here leaves nobody able to say which was which.
func (m *Model) BlocksClose() (string, bool) {
	if m.mode != movingIssues || m.mv == nil {
		return "", false
	}
	return "a move is still going: " + strconv.Itoa(m.mv.moved) + " of " +
		strconv.Itoa(len(m.mv.keys)) + " issues have moved and the rest are still with Jira", true
}

// Addr is where the kernel delivers the board, the sprints, the pages and the
// move chunks this view asked for, whatever has since been pushed over it and
// whichever root is on screen.
func (m *Model) Addr() kernel.Addr { return m.addr }

// Init reads the board the backlog belongs to. There is nothing on disk to draw
// first: the cache holds rows against a JQL, and which JQL this is depends on
// the board's rank field, which is itself an answer.
func (m *Model) Init() tea.Cmd { return m.load() }

// Update handles one message.
func (m *Model) Update(msg tea.Msg) (kernel.View, tea.Cmd) {
	var cmd tea.Cmd
	switch msg := msg.(type) {
	case kernel.SizeMsg:
		m.resize(msg.Width, msg.Height)

	case kernel.FocusMsg:
		// Losing the keyboard is not being closed: the palette opening over a
		// board still being read must not cancel the read.
		m.focused = msg.Focused

	case kernel.ThemeMsg:
		m.deps.Theme = msg.Theme
		m.styles = newStyles(msg.Theme)
		m.memo.reset()
		m.head = ""

	case kernel.CapabilitiesMsg:
		m.deps.Caps = msg.Caps
		m.memo.reset()
		m.head = ""

	case kernel.ProjectMsg:
		cmd = m.reproject(msg.Project)

	case kernel.RefreshMsg:
		cmd = m.refresh(msg.Purge)

	case NextBoardMsg:
		cmd = m.nextBoard()

	case MoveMsg:
		cmd = m.startMove()

	case filter.ChosenMsg:
		cmd = m.applyFilterTerm(msg.Term)

	case OpenFilterMsg:
		cmd = m.openFilterPicker()

	case ClearFilterMsg:
		cmd = m.clearFilter()

	case loadedMsg:
		cmd = m.took(msg)

	case pagedMsg:
		cmd = m.tookPage(msg)

	case movedMsg:
		cmd = m.chunkMoved(msg)

	case moveFailedMsg:
		cmd = m.moveFailed(msg)

	case failedMsg:
		cmd = m.failed(msg)

	case tea.KeyPressMsg:
		cmd = m.key(msg)

	case tea.MouseClickMsg:
		cmd = m.click(msg)

	case tea.MouseMotionMsg:
		m.drag.Move(msg)

	case tea.MouseReleaseMsg:
		cmd = m.release(msg)

	case tea.MouseWheelMsg:
		m.wheel(msg)
	}
	return m, cmd
}

// NextBoardMsg puts the next of the project's boards on screen. It is exported
// so that the palette reaches the gesture the pointer does rather than a second
// implementation of it.
type NextBoardMsg struct{}

// MoveMsg starts the gesture that moves the picked issues. It is exported for
// the same reason NextBoardMsg is.
type MoveMsg struct{}

func (m *Model) resize(w, h int) {
	if w == m.width && h == m.height {
		return
	}
	m.width, m.height = w, h
	m.drag.Cancel()
	m.relayout()
	m.scrollToCursor()
}

func (m *Model) relayout() {
	lay := planLayout(m.width, m.widestKey())
	if lay == m.lay && m.head != "" {
		return
	}
	m.lay = lay
	m.memo.reset()
	m.head = ""
}

func (m *Model) widestKey() int {
	widest := minKeyWidth
	for i := range m.issues {
		if n := len(m.issues[i].Key); n > widest {
			widest = n
		}
		if widest >= maxKeyWidth {
			return maxKeyWidth
		}
	}
	return widest
}

func (m *Model) rowsHeight() int {
	h := m.height - 1
	if m.note() != "" {
		h--
	}
	if len(m.picked) > 0 {
		h--
	}
	if len(m.terms) > 0 {
		h--
	}
	if m.said != "" {
		h--
	}
	if m.mode != browsing {
		h--
	}
	return max(h, 1)
}

// --- fetching ---------------------------------------------------------------

// begin cancels whatever is in flight and opens a context for its replacement.
// The generation it returns is what a landing answer is checked against, so a
// reply to a question the user has already changed is dropped rather than drawn.
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

// reply puts this view's address on a command, so what it asked for comes back
// here rather than to whatever the stack has on top by then.
func (m *Model) reply(cmd tea.Cmd) tea.Cmd {
	return kernel.Reply(withCancel(m.cancel, cmd), m.addr)
}

func (m *Model) current(gen int) bool { return gen == m.gen }

// busy reports a move part way through, which is the one time this view refuses
// to start another request: a second one would cancel the context the chunks
// still out with the site are travelling on.
func (m *Model) busy() bool { return m.mode == movingIssues }

func (m *Model) load() tea.Cmd {
	if m.busy() {
		return nil
	}
	if m.site == nil || m.search == nil {
		return nil
	}
	if strings.TrimSpace(m.deps.Project) == "" {
		m.absent = "This session is not scoped to a project, so there is no board to draw a backlog from."
		return nil
	}
	m.absent = ""
	ctx, gen := m.begin()
	return m.reply(read(ctx, m.site, m.search, m.deps.Project, m.boardAt, gen))
}

func (m *Model) refresh(purge bool) tea.Cmd {
	if m.busy() {
		return kernel.Warn("this move is still going; the board is re-read once it has finished")
	}
	if purge && m.search != nil {
		m.search.Invalidate()
	}
	m.said = ""
	return m.load()
}

func (m *Model) reproject(project string) tea.Cmd {
	if project == m.deps.Project {
		return nil
	}
	abandoned := ""
	if m.mv != nil {
		abandoned = "the move into " + m.mv.name + " was left after " + strconv.Itoa(m.mv.moved) +
			" of " + count(len(m.mv.keys), "issue") + ": this session moved to another project"
	}
	m.deps.Project = project
	m.boardAt = 0
	m.forget()
	m.said = abandoned
	return m.load()
}

// forget drops everything that belonged to the board on screen. A project or a
// board that has changed shares nothing with the one before it, not even which
// issues were picked.
func (m *Model) forget() {
	m.boards, m.sprints, m.issues = nil, nil, nil
	m.groups, m.rows = m.groups[:0], m.rows[:0]
	m.byKey = make(map[string]int)
	m.picked = make(map[string]bool)
	m.page, m.missing = jira.Page[jira.Issue]{}, nil
	m.config, m.field = jira.BoardConfig{}, jira.FieldRef{}
	m.cursor, m.top = 0, 0
	m.loaded, m.failure, m.absent, m.said = false, nil, "", ""
	m.mode = browsing
	m.endMove()
	m.memo.reset()
	m.head = ""
}

func (m *Model) nextBoard() tea.Cmd {
	if m.busy() {
		return kernel.Warn("this move is still going; the board can be changed once it has finished")
	}
	if len(m.boards) < 2 {
		return nil
	}
	at := (m.boardAt + 1) % len(m.boards)
	m.forget()
	m.boardAt = at
	return m.load()
}

func (m *Model) took(msg loadedMsg) tea.Cmd {
	if !m.current(msg.gen) {
		return nil
	}
	m.loading, m.loaded = false, true
	m.boards, m.boardAt, m.config = msg.boards, msg.boardAt, msg.config
	m.sprints, m.field = msg.sprints, msg.field
	m.issues, m.page, m.missing = msg.page.Items, msg.page, msg.missing
	m.head = ""
	switch {
	case len(msg.boards) == 0:
		m.absent = "This project has no board, so it has no backlog: " + m.boardsReason()
	case msg.field.ID == "":
		m.absent = "This site has no sprint field this session could resolve, so nothing here can " +
			"tell an issue in a sprint from one waiting to be scheduled."
	default:
		m.absent = ""
	}
	m.reindex()
	m.relayout()
	m.regroup()
	return m.pageAheadIfNeeded()
}

func (m *Model) boardsReason() string {
	if reason := m.deps.Caps.Capability(jira.CapBoards).Reason; reason != "" {
		return reason
	}
	return "the site listed none for it"
}

func (m *Model) tookPage(msg pagedMsg) tea.Cmd {
	if !m.current(msg.gen) {
		return nil
	}
	m.loading = false
	m.issues = append(m.issues, msg.page.Items...)
	m.page = msg.page
	m.reindex()
	m.relayout()
	m.regroup()
	return m.pageAheadIfNeeded()
}

func (m *Model) failed(msg failedMsg) tea.Cmd {
	if !m.current(msg.gen) {
		return nil
	}
	m.loading = false
	m.failure = msg.err
	m.head = ""
	return kernel.Fail(msg.err)
}

// pageAheadIfNeeded asks for the next page as the cursor approaches the end of
// what is in hand, one screen ahead rather than one row at a time.
func (m *Model) pageAheadIfNeeded() tea.Cmd {
	if m.busy() || m.loading || m.search == nil || !m.page.HasMore() {
		return nil
	}
	if len(m.rows)-m.cursor > lookahead {
		return nil
	}
	ctx, gen := m.begin()
	return m.reply(nextPage(ctx, m.page, gen))
}

// --- grouping ---------------------------------------------------------------

func (m *Model) reindex() {
	m.byKey = make(map[string]int, len(m.issues))
	for i := range m.issues {
		m.byKey[m.issues[i].Key] = i
	}
}

// regroup sorts the issues in hand into one section per open sprint and the
// backlog for everything else.
//
// An issue is placed by the sprint ids on its own sprint value, and the section
// it lands in is the first of them this board has open. Issues whose status is
// in the done category are left out altogether: finished work is neither in a
// sprint you can plan nor waiting to be scheduled. The category is the one on
// the status, which the port resolved from statusCategory rather than from a
// name anybody can translate.
func (m *Model) regroup() {
	under := m.under()
	m.groups = m.groups[:0]
	m.filteredOut = 0
	open := make(map[int64]int, len(m.sprints))
	for i := range m.sprints {
		open[m.sprints[i].ID] = i
		m.groups = append(m.groups, group{
			id: m.sprints[i].ID, name: m.sprints[i].Name, state: m.sprints[i].State,
		})
	}
	m.groups = append(m.groups, group{name: backlogName})
	last := len(m.groups) - 1
	for i := range m.issues {
		if m.issues[i].Status.Category == jira.CategoryDone {
			continue
		}
		if !matchesTerms(&m.issues[i], m.terms) {
			m.filteredOut++
			continue
		}
		at := last
		for _, id := range m.sprintsOn(&m.issues[i]) {
			if g, ok := open[id]; ok {
				at = g
				break
			}
		}
		m.groups[at].issues = append(m.groups[at].issues, i)
	}
	m.rank()
	m.rebuildRows()
	m.restore(under)
}

// rank puts each section in the board's own order, where the board has one.
//
// The order is the value of the rank field the board configuration named, which
// is a lexicographic string, so a section is sorted by comparing them. An issue
// the site sent no rank for sorts last rather than first: a missing rank is not
// a position at the top.
func (m *Model) rank() {
	if m.config.RankFieldID == "" {
		return
	}
	ref := jira.FieldRef{ID: m.config.RankFieldID}
	for g := range m.groups {
		slices.SortStableFunc(m.groups[g].issues, func(a, b int) int {
			left, hasLeft := m.issues[a].Fields.Text(ref)
			right, hasRight := m.issues[b].Fields.Text(ref)
			switch {
			case hasLeft && hasRight:
				return strings.Compare(left, right)
			case hasLeft:
				return -1
			case hasRight:
				return 1
			default:
				return 0
			}
		})
	}
}

// rebuildRows lays the sections out as lines. A board where no section holds an
// issue draws no rows at all: three heads over nothing is a screen that looks
// like a list and says nothing, where the empty pane says which kind of empty it
// is.
func (m *Model) rebuildRows() {
	m.rows = m.rows[:0]
	held := 0
	for g := range m.groups {
		held += len(m.groups[g].issues)
	}
	if held == 0 {
		return
	}
	for g := range m.groups {
		m.rows = append(m.rows, row{head: true, group: g, issue: -1})
		for _, at := range m.groups[g].issues {
			m.rows = append(m.rows, row{group: g, issue: at})
		}
	}
}

// under names what the cursor is on, so that a regroup can put it back on the
// same thing rather than on the same index.
func (m *Model) under() string {
	if m.cursor < 0 || m.cursor >= len(m.rows) {
		return ""
	}
	r := m.rows[m.cursor]
	if r.head {
		return "head:" + strconv.Itoa(r.group)
	}
	return m.issues[r.issue].Key
}

func (m *Model) restore(what string) {
	if what != "" {
		for i := range m.rows {
			if m.rowName(i) == what {
				m.cursor = i
				m.keepVisible()
				return
			}
		}
	}
	m.cursor = min(max(m.cursor, 0), max(len(m.rows)-1, 0))
	m.keepVisible()
}

// keepVisible holds the scroll offset where it was, and moves it only when the
// row the cursor is on has gone off screen. A move that empties a section shifts
// every row under it, and a place that is kept off screen is not kept.
func (m *Model) keepVisible() {
	m.clampScroll()
	if m.cursor < m.top || m.cursor >= m.top+m.rowsHeight() {
		m.scrollToCursor()
	}
}

func (m *Model) rowName(at int) string {
	r := m.rows[at]
	if r.head {
		return "head:" + strconv.Itoa(r.group)
	}
	return m.issues[r.issue].Key
}

// --- selection --------------------------------------------------------------

func (m *Model) issueAt(at int) *jira.Issue {
	if at < 0 || at >= len(m.rows) || m.rows[at].head {
		return nil
	}
	return &m.issues[m.rows[at].issue]
}

func (m *Model) pick() {
	iss := m.issueAt(m.cursor)
	if iss == nil {
		return
	}
	if m.picked[iss.Key] {
		delete(m.picked, iss.Key)
	} else {
		m.picked[iss.Key] = true
	}
}

func (m *Model) pickGroup() {
	if m.cursor < 0 || m.cursor >= len(m.rows) {
		return
	}
	g := &m.groups[m.rows[m.cursor].group]
	all := len(g.issues) > 0
	for _, at := range g.issues {
		if !m.picked[m.issues[at].Key] {
			all = false
			break
		}
	}
	for _, at := range g.issues {
		if all {
			delete(m.picked, m.issues[at].Key)
			continue
		}
		m.picked[m.issues[at].Key] = true
	}
}

func (m *Model) clearPicks() {
	if len(m.picked) == 0 {
		return
	}
	m.picked = make(map[string]bool)
}

func (m *Model) selection() []string {
	if len(m.picked) == 0 {
		if iss := m.issueAt(m.cursor); iss != nil {
			return []string{iss.Key}
		}
		return nil
	}
	out := make([]string, 0, len(m.picked))
	for i := range m.rows {
		if m.rows[i].head {
			continue
		}
		if key := m.issues[m.rows[i].issue].Key; m.picked[key] {
			out = append(out, key)
		}
	}
	return out
}

// --- moving -----------------------------------------------------------------

func (m *Model) startMove() tea.Cmd {
	if m.busy() {
		return nil
	}
	if m.mover == nil {
		return kernel.Warn("there is no Jira connection in this session to move anything with")
	}
	wanted := m.selection()
	if len(wanted) == 0 {
		return kernel.Warn("nothing is picked and the cursor is not on an issue, so there is nothing to move")
	}
	m.wanted = wanted
	m.mode, m.destAt = choosing, m.firstOtherGroup()
	m.said = ""
	return nil
}

// firstOtherGroup opens the chooser on somewhere other than where the selection
// already is, which is the only destination that changes anything.
func (m *Model) firstOtherGroup() int {
	from := -1
	if m.cursor >= 0 && m.cursor < len(m.rows) {
		from = m.rows[m.cursor].group
	}
	for g := range m.groups {
		if g != from {
			return g
		}
	}
	return 0
}

func (m *Model) moveDest(by int) {
	if len(m.groups) == 0 {
		return
	}
	m.destAt = min(max(m.destAt+by, 0), len(m.groups)-1)
}

func (m *Model) chooseDest() tea.Cmd {
	if m.destAt < 0 || m.destAt >= len(m.groups) {
		return nil
	}
	m.mode = confirming
	return nil
}

func (m *Model) leave() tea.Cmd {
	m.mode = browsing
	m.mv, m.wanted = nil, nil
	return nil
}

// confirmMove is the only way a move starts. Nothing reaches it but the y the
// confirm line names, so no single stroke and no single click moves an issue.
func (m *Model) confirmMove() tea.Cmd {
	if m.mode != confirming || m.mover == nil {
		return nil
	}
	keys := m.wanted
	if len(keys) == 0 || m.destAt >= len(m.groups) {
		return m.leave()
	}
	dest := m.groups[m.destAt]
	m.mv = &move{
		dest: m.destAt, name: dest.name, id: dest.id, keys: keys,
		chunks: batches(len(keys)),
	}
	m.mode, m.said = movingIssues, ""
	// A move holds a context of its own rather than the one a read travels on,
	// because the chunks after the first are issued from a message: a read
	// starting beside them would otherwise cancel a call already with the site.
	m.stop()
	m.gen++
	ctx, cancel := context.WithCancel(context.Background())
	m.moveCtx, m.moveStop = ctx, cancel
	return kernel.Reply(moveInto(ctx, m.mover, m.mv.id, m.mv.chunk(0), 0, m.gen), m.addr)
}

func (m *Model) nextChunk() tea.Cmd {
	if m.moveCtx == nil || m.mv == nil {
		return nil
	}
	return kernel.Reply(moveInto(m.moveCtx, m.mover, m.mv.id, m.mv.chunk(m.mv.done), m.mv.done, m.gen), m.addr)
}

func (m *Model) endMove() {
	if m.moveStop != nil {
		m.moveStop()
		m.moveStop = nil
	}
	m.moveCtx, m.mv, m.wanted = nil, nil, nil
}

func (m *Model) chunkMoved(msg movedMsg) tea.Cmd {
	if !m.current(msg.gen) || m.mv == nil || msg.at != m.mv.done {
		return nil
	}
	m.applyMoved(m.mv.chunk(m.mv.done))
	m.mv.done++
	m.mv.moved += msg.moved
	if m.mv.done < m.mv.chunks {
		return m.nextChunk()
	}
	said := "moved " + count(m.mv.moved, "issue") + " into " + m.mv.name
	m.said, m.mode = said, browsing
	for _, key := range m.mv.keys {
		delete(m.picked, key)
	}
	m.endMove()
	return kernel.Status(said)
}

// moveFailed reports the half that moved and the half that did not, in the
// site's own words, and leaves the issues that did not move picked so that the
// same gesture tries them again.
func (m *Model) moveFailed(msg moveFailedMsg) tea.Cmd {
	if !m.current(msg.gen) || m.mv == nil {
		return nil
	}
	pending := m.mv.keys[msg.at*moveChunk:]
	reason, _ := jira.Reason(msg.err)
	m.said = "moved " + strconv.Itoa(m.mv.moved) + " of " + count(len(m.mv.keys), "issue") +
		" into " + m.mv.name + "; the other " + strconv.Itoa(len(pending)) + " did not move: " + reason
	m.picked = make(map[string]bool, len(pending))
	for _, key := range pending {
		m.picked[key] = true
	}
	m.mode = browsing
	m.endMove()
	return kernel.Fail(msg.err)
}

// applyMoved moves the rows the site has accepted, without reading them back:
// the order a board reports lags a write, so a confirming read hands back the
// section the issue was dragged out of.
func (m *Model) applyMoved(keys []string) {
	if m.field.ID == "" || m.mv == nil {
		return
	}
	dest := m.groups[m.mv.dest]
	for _, key := range keys {
		at, ok := m.byKey[key]
		if !ok {
			continue
		}
		if dest.id == 0 {
			m.issues[at].Fields = m.issues[at].Fields.Without(m.field)
			continue
		}
		m.issues[at].Fields = m.issues[at].Fields.With(m.field, jira.FieldValue{
			Kind: jira.KindOptions,
			Options: []jira.Option{{
				ID: strconv.FormatInt(dest.id, 10), Label: dest.name,
			}},
		})
	}
	m.regroup()
}

// filterBar draws the chip line naming the terms in force.
func (m *Model) filterBar() string {
	return m.bar.Render(m.terms, m.width, m.deps.Theme, clearFilterKey, m.termsGen)
}

// clearFilterKey is built once rather than read off a fresh defaultKeys() on
// every frame.
var clearFilterKey = defaultKeys().Unfilter.Help().Key

func count(n int, what string) string {
	if n == 1 {
		return "1 " + what
	}
	return strconv.Itoa(n) + " " + what + "s"
}

// --- motion -----------------------------------------------------------------

func (m *Model) moveTo(at int) tea.Cmd {
	if len(m.rows) == 0 {
		m.cursor, m.top = 0, 0
		return nil
	}
	m.cursor = min(max(at, 0), len(m.rows)-1)
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
	m.top = min(max(m.top, 0), max(len(m.rows)-m.rowsHeight(), 0))
}

// --- keys -------------------------------------------------------------------

func (m *Model) key(msg tea.KeyPressMsg) tea.Cmd {
	stroke := msg.String()
	m.drag.Cancel()
	switch m.mode {
	case choosing:
		return m.chooserKey(stroke)
	case confirming:
		return m.confirmKey(stroke)
	case movingIssues:
		return nil
	case browsing:
	}
	if m.pendingGo {
		m.pendingGo = false
		if stroke == "g" {
			return m.moveTo(0)
		}
	}
	switch m.acts[stroke] {
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
		return m.moveTo(len(m.rows) - 1)
	case actGo:
		m.pendingGo = true
		return nil
	case actPick:
		m.pick()
		return m.moveTo(m.cursor + 1)
	case actPickGroup:
		m.pickGroup()
		return nil
	case actClear:
		m.clearPicks()
		return nil
	case actMove:
		return m.startMove()
	case actFilterBy:
		return m.openFilterPicker()
	case actClearFilter:
		return m.clearFilter()
	case actNone, actChoose, actBack, actConfirm:
	}
	return nil
}

func (m *Model) chooserKey(stroke string) tea.Cmd {
	switch m.inChooser[stroke] {
	case actUp:
		m.moveDest(-1)
	case actDown:
		m.moveDest(1)
	case actChoose:
		return m.chooseDest()
	case actBack:
		return m.leave()
	default:
	}
	return nil
}

func (m *Model) confirmKey(stroke string) tea.Cmd {
	switch m.inConfirm[stroke] {
	case actConfirm:
		return m.confirmMove()
	case actBack:
		return m.leave()
	default:
	}
	return nil
}

// --- mouse ------------------------------------------------------------------

func (m *Model) click(msg tea.MouseClickMsg) tea.Cmd {
	if msg.Button != tea.MouseLeft {
		return nil
	}
	switch m.mode {
	case confirming:
		if m.zones.Hit(zoneConfirm, msg) {
			return m.confirmMove()
		}
		if m.zones.Hit(zoneCancel, msg) {
			return m.leave()
		}
		return nil
	case choosing:
		for g := range m.groups {
			if !m.zones.Hit(destZone(g), msg) {
				continue
			}
			if g == m.destAt {
				return m.chooseDest()
			}
			m.destAt = g
			return nil
		}
		return nil
	case movingIssues:
		return nil
	case browsing:
	}
	if cmd, dropped := m.clickTerm(msg); dropped {
		return cmd
	}
	if m.zones.Hit(zoneBoard, msg) {
		return m.nextBoard()
	}
	for i := m.top; i < min(m.top+m.rowsHeight(), len(m.rows)); i++ {
		if !m.zones.Hit(m.zoneOf(i), msg) {
			continue
		}
		m.cursor = i
		m.scrollToCursor()
		if !m.rows[i].head {
			m.drag.Start(m.zoneOf(i), msg)
		}
		// A double-click picks, which is what the space this view advertises
		// does. Nothing here opens an issue, so there is no second meaning for
		// the gesture to take.
		if m.clicks.Double(m.zoneOf(i)) {
			m.drag.Cancel()
			m.pick()
		}
		return m.pageAheadIfNeeded()
	}
	return nil
}

// release ends a drag over a section, which asks the same question m asks: the
// confirm line, and nothing moves until it is answered.
func (m *Model) release(msg tea.MouseReleaseMsg) tea.Cmd {
	if !m.drag.Active() {
		return nil
	}
	from := m.drag.ID()
	m.drag.Cancel()
	if m.mode != browsing || m.mover == nil {
		return nil
	}
	for i := m.top; i < min(m.top+m.rowsHeight(), len(m.rows)); i++ {
		if !m.rows[i].head || !m.zones.Hit(m.zoneOf(i), msg) {
			continue
		}
		g := m.rows[i].group
		wanted, ok := m.draggedKeys(from)
		if !ok {
			return nil
		}
		m.wanted, m.destAt, m.mode = wanted, g, confirming
		m.said = ""
		return nil
	}
	return nil
}

// draggedKeys is what a drag would move: the selection when the row that was
// grabbed is part of it, and that row alone when it is not. Dragging a row
// nobody picked is about that row, and dragging one of the picked ones is about
// all of them.
func (m *Model) draggedKeys(zone string) ([]string, bool) {
	for i := range m.rows {
		if m.zoneOf(i) != zone || m.rows[i].head {
			continue
		}
		m.cursor = i
		key := m.issues[m.rows[i].issue].Key
		if m.picked[key] {
			return m.selection(), true
		}
		return []string{key}, true
	}
	return nil, false
}

func (m *Model) wheel(msg tea.MouseWheelMsg) {
	switch msg.Button {
	case tea.MouseWheelUp:
		m.top -= widget.WheelStep
	case tea.MouseWheelDown:
		m.top += widget.WheelStep
	default:
		return
	}
	m.clampScroll()
}

// The value arrives in one of two shapes and the field's own type is neither: a
// read that sent no schema decodes the array as options, and a read that did
// finds the field declared as an array of json, which nothing here has a slot
// for, so the bytes are kept as text.
func (m *Model) sprintsOn(iss *jira.Issue) []int64 {
	if m.field.ID == "" {
		return nil
	}
	if options, ok := iss.Fields.Options(m.field); ok {
		out := make([]int64, 0, len(options))
		for _, option := range options {
			if id, err := strconv.ParseInt(strings.TrimSpace(option.ID), 10, 64); err == nil {
				out = append(out, id)
			}
		}
		return out
	}
	text, ok := iss.Fields.Text(m.field)
	if !ok {
		return nil
	}
	return sprintIDsIn(text)
}

func (m *Model) board() jira.Board {
	if m.boardAt < 0 || m.boardAt >= len(m.boards) {
		return jira.Board{}
	}
	return m.boards[m.boardAt]
}

// A board with no rank field is ordered by its saved filter, and reading that
// filter is not something this session can do — so the rows are in an order this
// program chose and the pane says which. A board that does rank still offers no
// reorder: the port has no way to write a rank, and a gesture that silently did
// nothing would be worse than the sentence.
func (m *Model) ordering() string {
	if !m.loaded || len(m.boards) == 0 {
		return ""
	}
	if m.config.Ordering() == jira.OrderRank {
		return "Rank order. Rows cannot be reordered here: nothing can write a rank."
	}
	return "No rank field on this board; oldest first, not its filter's order."
}
