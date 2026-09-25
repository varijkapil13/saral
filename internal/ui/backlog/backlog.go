// Package backlog is a board's backlog: the issues waiting to be scheduled, the
// open sprints they can go into, and the moves between the two.
package backlog

import (
	"context"
	"slices"
	"strconv"
	"strings"

	"charm.land/bubbles/v2/textinput"
	tea "charm.land/bubbletea/v2"

	"github.com/varijkapil13/saral/internal/app"
	"github.com/varijkapil13/saral/internal/ui/filter"
	"github.com/varijkapil13/saral/internal/ui/form"
	"github.com/varijkapil13/saral/internal/ui/issue"
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
	_ kernel.BackClaimer = (*Model)(nil)
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
	sorting
	finding
)

// group is one section: an open sprint, or the backlog itself as the last one.
type group struct {
	// id is the sprint's, and zero for the backlog.
	id     int64
	name   string
	state  jira.SprintState
	issues []int
	// points is the board's estimate summed over issues, and pointed says
	// whether any of them carries one.
	points  float64
	pointed bool
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
	cache  app.Cache
	addr   kernel.Addr

	styles *styles
	memo   *widget.RowCache[rowKey, string]
	zones  widget.Zoner
	clicks *widget.Clicks
	drag   widget.Drag

	acts      map[string]action
	inChooser map[string]action
	inConfirm map[string]action
	inSort    map[string]action
	inFind    map[string]action

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
	// done is the statuses of the config's last mapped column, nil when it maps
	// none, in which case the status category decides.
	done    map[string]bool
	sprints []jira.Sprint
	// noSprints is the site's own sentence for a board that has none — a Kanban
	// board — and "" otherwise. It is what the header says in place of a count,
	// and what a move into a sprint is refused with.
	noSprints string
	field     jira.FieldRef
	// estimate is the field the board estimates in, zero when it does not.
	estimate jira.FieldRef
	issues   []jira.Issue
	byKey    map[string]int
	page     jira.Page[jira.Issue]
	missing  []string
	// fieldIDs is what the last read of the board asked for, nil when the
	// backlog on screen came off disk.
	fieldIDs []string

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

	// sort is the order this view reads each section's own issues in, over and
	// above a board's own rank. A zero value is no choice made, and sorting is
	// the mode that changes it.
	sort           sortChoice
	sortCursor     int
	sortSaveFailed bool
	// pendingSort is the order chosen while the rest of the backlog was still
	// being read, and reading marks that walk. A sort has to have every issue
	// before it means anything — see applySortChoice.
	pendingSort   sortChoice
	reading       bool
	sortAfterRead bool
	pickAfterRead bool
	pendingGroup  int64

	writes *writer
	// pendingPut rides on the next page's read, so pages are written in order.
	pendingPut func() error

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
	// stale marks the backlog on screen as older than it should be: it came off
	// disk past its TTL, or a revalidation that would have replaced it failed.
	stale bool
	// boardIDHint is a board id this view already believes it is drawing, from a
	// stored snapshot or a project switch, before the site has said which
	// boards this project has. took resolves it into an index and clears it, so
	// a revalidating load lands on the same board rather than always the first.
	boardIDHint int64

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

	inFlight *ranking
	rankGen  int
	rankStop context.CancelFunc

	me       *jira.User
	askingMe bool

	find     textinput.Model
	needle   string
	findMiss bool
	findFrom int

	// createOn is the board the last create was started on, so a report that
	// lands after the board changed is not drawn on the wrong one.
	createOn int64
	// made is the issues this view created and drew itself, which a re-read
	// the search index has not caught up with yet would otherwise drop.
	made []string

	focused bool
}

// New builds the backlog. Which board, which sprints and which issues are all
// answers, but a stored one may already have them: fromCache draws it before
// anything at all is asked of the site (docs/UX.md principle 1), and a session
// with nothing stored draws from nothing but the size it is given, the way it
// always has.
func New(d kernel.Deps) kernel.View {
	m := &Model{
		deps:   d,
		addr:   kernel.NewAddr(),
		cache:  d.Cache,
		picked: make(map[string]bool),
		byKey:  make(map[string]int),
		writes: &writer{},
	}
	if m.deps.Theme == nil {
		m.deps.Theme = kernel.NewTheme(kernel.ThemeAuto, true, kernel.UnicodeGlyphs())
	}
	m.styles = newStyles(m.deps.Theme)
	m.memo = widget.NewRowCache[rowKey, string](rowCacheLimit)
	m.zones = widget.NewZoner(d.Zones)
	m.clicks = widget.NewClicks(d.Now)
	m.bar = filterbar.New(m.zones)
	m.acts, m.inChooser, m.inConfirm, m.inSort, m.inFind = defaultKeys().tables()
	m.find = newFindInput()
	m.sort = loadSort(ViewID)
	if d.Jira != nil {
		m.search = app.NewSearch(d.Jira)
		m.site = d.Jira
		m.mover = d.Jira
	}
	if terms, ok := m.recallTerms(); ok {
		m.terms = terms
	}
	m.relayout()
	m.fromCache()
	return m
}

// backlogCache is the cache's optional backlog-shaped half, absent whenever
// the session has nowhere to keep one or the cache in force is only rows and
// issues — the same additive-interface pattern kernel.restoreCaps uses for
// app.CapsCache.
func (m *Model) backlogCache() (app.BacklogCache, bool) {
	held, ok := m.cache.(app.BacklogCache)
	return held, ok && held != nil
}

// fromCache draws the backlog this project was last showing, before anything
// is asked of the site. Which boards a project has is itself an answer nobody
// has yet, so the one board a snapshot names stands in for the whole list
// until Boards replaces it.
//
// It runs here rather than in Init because this is where a first paint
// happens: kernel.FirstPaint builds the view and renders one frame without
// ever calling Init, which is the thing docs/PERFORMANCE.md budgets.
func (m *Model) fromCache() {
	held, ok := m.backlogCache()
	if !ok {
		return
	}
	boardID, ok := held.LastBacklogBoard(m.deps.Project)
	if !ok {
		return
	}
	snap, ok := held.Backlog(boardID)
	if !ok {
		return
	}
	m.applyBacklogSnapshot(snap)
	m.boards = []jira.Board{{ID: boardID, Name: snap.Config.Name, Type: snap.Config.Type}}
	m.boardAt = 0
	m.boardIDHint = boardID
}

// applyBacklogSnapshot puts a stored backlog in force, without touching which
// boards this project has or which of them is selected: New and a project
// switch know only the one board a snapshot names, while nextBoard already
// holds the site's own list and must not collapse it down to one.
func (m *Model) applyBacklogSnapshot(snap app.BacklogSnapshot) {
	m.config, m.done, m.estimate = snap.Config, doneStatuses(snap.Config), estimateOf(snap.Config)
	m.sprints, m.field, m.noSprints = snap.Sprints, snap.Field, snap.NoSprints
	m.issues, m.page, m.missing = snap.Issues, jira.Page[jira.Issue]{}, nil
	// A snapshot stored part way through a walk carries no cursor to page on
	// from, so the rest of the backlog is only reached by reading it again.
	m.loaded, m.stale, m.absent = true, snap.Stale || snap.More, ""
	m.reindex()
	m.relayout()
	m.regroup()
}

// WantsRawKeys is true while a destination is being chosen and while a move is
// waiting on its y. Without it the kernel keeps esc for going back and the
// digits for the saved queries, so the two questions this view asks could be
// answered by nobody.
func (m *Model) WantsRawKeys() bool {
	return m.mode == choosing || m.mode == confirming || m.mode == sorting || m.mode == finding
}

// backKeys are the kernel's own back strokes, which reach the backlog only while
// WantsBack claims them.
var backKeys = kernel.DefaultGlobalKeys().Back.Keys()

// WantsBack claims esc while terms narrow the backlog, so esc clears them.
func (m *Model) WantsBack() bool { return len(m.terms) > 0 && m.mode == browsing }

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

// Init reads the board the backlog belongs to — unless a stored backlog
// already answered every question it asks and is still within its TTL, in
// which case nothing here is asked of the site at all (docs/UX.md principle
// 1).
func (m *Model) Init() tea.Cmd {
	if m.loaded && !m.stale {
		return nil
	}
	return m.load()
}

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

	case issue.ShareMsg:
		if m.focused {
			cmd = issue.Share(m.deps, msg.Act, m.underKey())
		}

	case kernel.SetMouseMsg:
		m.termsGen++
		m.memo.Reset()
		m.head = ""

	case kernel.ThemeMsg:
		m.deps.Theme = msg.Theme
		m.styles = newStyles(msg.Theme)
		m.memo.Reset()
		m.head = ""

	case kernel.CapabilitiesMsg:
		m.deps.Caps = msg.Caps
		m.memo.Reset()
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

	case SortMsg:
		cmd = m.startSort()

	case RankMsg:
		cmd = m.reorder(msg.Where)

	case FindMsg:
		cmd = m.startFind()

	case CreateMsg:
		cmd = m.startCreate()

	case form.CreatedMsg:
		cmd = m.created(msg)

	case createdMsg:
		cmd = m.settled(msg)

	case MineMsg:
		cmd = m.toggleMine()

	case rankMsg:
		cmd = m.ranked(msg)

	case meMsg:
		cmd = m.tookMe(msg)

	case sortSaveFailedMsg:
		cmd = m.reportSortSaveFailed(msg)

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

// SortMsg opens the picker that chooses the order this view reads a section's
// issues in. It is exported for the same reason NextBoardMsg is.
type SortMsg struct{}

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
	m.memo.Reset()
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
	return m.reply(read(ctx, m.site, m.search, m.deps.Project, m.boardAt, m.boardIDHint, gen))
}

// refresh re-reads the board this backlog belongs to. Purging also drops the
// stored snapshot, the way a purging refresh of the list drops its stored
// rows.
func (m *Model) refresh(purge bool) tea.Cmd {
	if m.busy() {
		return kernel.Warn("this move is still going; the board is re-read once it has finished")
	}
	var said tea.Cmd
	if purge {
		if m.search != nil {
			m.search.Invalidate()
		}
		said = m.forgetBacklog()
	}
	m.said = ""
	return tea.Batch(said, m.load())
}

// forgetBacklog drops the stored snapshot of the backlog on screen, if there is
// one and if there is a board on screen to name. The issues themselves stay:
// they are shared with every other read that named them.
func (m *Model) forgetBacklog() tea.Cmd {
	held, ok := m.backlogCache()
	if !ok || m.config.BoardID == 0 {
		return nil
	}
	if err := held.ForgetBacklog(m.config.BoardID); err != nil {
		return kernel.Warn("the stored copy of this backlog could not be dropped: " + err.Error())
	}
	return nil
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
	was := m.deps.Project
	m.deps.Project = project
	var said tea.Cmd
	m.terms, said = filterbar.Reproject(m.deps, ViewID, was, m.terms)
	m.termsGen++
	m.boardAt = 0
	m.forget()
	m.said = abandoned
	m.fromCache()
	if m.loaded && !m.stale {
		return said
	}
	return tea.Batch(said, m.load())
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
	m.config, m.field, m.done, m.estimate = jira.BoardConfig{}, jira.FieldRef{}, nil, jira.FieldRef{}
	m.fieldIDs, m.made = nil, nil
	m.dropRank()
	m.needle, m.findMiss = "", false
	m.cursor, m.top = 0, 0
	m.loaded, m.stale, m.failure, m.absent, m.said = false, false, nil, "", ""
	m.mode = browsing
	m.endMove()
	m.memo.Reset()
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
	boards, boardID := m.boards, m.boards[at].ID
	m.forget()
	m.boards, m.boardAt = boards, at
	if held, ok := m.backlogCache(); ok {
		if snap, ok := held.Backlog(boardID); ok {
			m.applyBacklogSnapshot(snap)
			if !m.stale {
				return nil
			}
		}
	}
	return m.load()
}

func (m *Model) took(msg loadedMsg) tea.Cmd {
	if !m.current(msg.gen) {
		return nil
	}
	m.loading, m.loaded, m.stale, m.boardIDHint = false, true, false, 0
	under, issues := m.under(), m.carryMade(msg.config.BoardID, msg.page.Items)
	m.boards, m.boardAt, m.config = msg.boards, msg.boardAt, msg.config
	m.done, m.estimate = doneStatuses(msg.config), estimateOf(msg.config)
	m.sprints, m.field, m.noSprints = msg.sprints, msg.field, msg.noSprints
	m.issues, m.page, m.missing, m.fieldIDs = issues, msg.page, msg.missing, msg.fields
	// The rows still index the issues just replaced.
	m.rows = m.rows[:0]
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
	m.restore(under)
	return tea.Batch(m.rememberLastBacklogBoard(), m.storeThen(m.pagePut(msg.page.Items, true), m.pageAheadIfNeeded))
}

// rememberLastBacklogBoard writes which board this project's backlog is
// drawing, so a session opening cold knows which board's snapshot to read
// before the site has said which boards this project has.
func (m *Model) rememberLastBacklogBoard() tea.Cmd {
	held, ok := m.backlogCache()
	if !ok || len(m.boards) == 0 {
		return nil
	}
	if err := held.PutLastBacklogBoard(m.deps.Project, m.config.BoardID); err != nil {
		return kernel.Warn("this backlog could not be remembered for next time: " + err.Error())
	}
	return nil
}

// pagePut is run off the update loop. A cache that keeps no pages is written
// whole, on the first page and at the end of the walk only.
func (m *Model) pagePut(items []jira.Issue, first bool) func() error {
	if len(m.boards) == 0 {
		return nil
	}
	if paged, ok := m.cache.(app.BacklogPageCache); ok && paged != nil {
		snap := m.snapshot(slices.Clone(items))
		boardID := m.config.BoardID
		return m.writes.put(m.gen, first, func() error { return paged.PutBacklogPage(boardID, snap, first) })
	}
	if !first && m.page.HasMore() {
		return nil
	}
	return m.wholePut()
}

func (m *Model) movedPut(keys []string) func() error {
	if len(m.boards) == 0 {
		return nil
	}
	paged, ok := m.cache.(app.BacklogPageCache)
	if !ok || paged == nil {
		return m.wholePut()
	}
	moved := make([]jira.Issue, 0, len(keys))
	for _, key := range keys {
		if at, held := m.byKey[key]; held {
			moved = append(moved, m.issues[at])
		}
	}
	snap, boardID := m.snapshot(moved), m.config.BoardID
	return m.writes.put(m.gen, false, func() error { return paged.PutBacklogPage(boardID, snap, false) })
}

func (m *Model) wholePut() func() error {
	held, ok := m.backlogCache()
	if !ok {
		return nil
	}
	snap, boardID := m.snapshot(slices.Clone(m.issues)), m.config.BoardID
	return m.writes.put(m.gen, true, func() error { return held.PutBacklog(boardID, snap) })
}

func (m *Model) snapshot(issues []jira.Issue) app.BacklogSnapshot {
	return app.BacklogSnapshot{
		Config: m.config, Sprints: slices.Clone(m.sprints), Field: m.field, NoSprints: m.noSprints,
		Issues: issues, More: m.page.HasMore(),
	}
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
	m.loading, m.stale = false, false
	under := m.under()
	m.issues = append(m.dropMade(msg.page.Items), msg.page.Items...)
	m.rows = m.rows[:0]
	m.page = msg.page
	m.reindex()
	m.relayout()
	m.regroup()
	m.restore(under)
	var said tea.Cmd
	if msg.stored != nil {
		said = kernel.Warn("this backlog could not be stored for next time: " + msg.stored.Error())
	}
	next := m.pageAheadIfNeeded
	if m.reading {
		next = m.readRest
	}
	return tea.Batch(said, m.storeThen(m.pagePut(msg.page.Items, false), next))
}

// readRest walks what is left of the backlog for a sort that has been chosen
// over it, and puts the order in force once there is nothing left to read. It
// asks for the next page whatever the cursor is near, which is what separates
// it from pageAheadIfNeeded.
func (m *Model) readRest() tea.Cmd {
	if m.page.HasMore() {
		if m.busy() || m.loading || m.search == nil {
			return nil
		}
		ctx, gen := m.begin()
		return m.reply(nextPage(ctx, m.page, gen, m.takePut()))
	}
	m.reading = false
	var cmd tea.Cmd
	if m.sortAfterRead {
		m.sortAfterRead = false
		cmd = m.setSort(m.pendingSort)
	}
	if m.pickAfterRead {
		m.pickAfterRead = false
		m.pickSection(m.pendingGroup)
	}
	return cmd
}

// failed keeps a backlog that is already drawn on screen, badged stale rather
// than replaced with a refusal (docs/UX.md — stale data is badged, not
// hidden): the rows already in hand are the last true answer this session had.
// A backlog with no rows to badge has no cards on screen either way, so the
// refusal is what appendEmpty draws and it is kept for the pane the way it
// always was.
func (m *Model) failed(msg failedMsg) tea.Cmd {
	if !m.current(msg.gen) {
		return nil
	}
	m.loading = false
	m.reading, m.sortAfterRead, m.pickAfterRead = false, false, false
	if len(m.rows) > 0 {
		m.stale = true
	} else {
		m.failure = msg.err
	}
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
	return m.reply(nextPage(ctx, m.page, gen, m.takePut()))
}

func (m *Model) storeThen(put func() error, next func() tea.Cmd) tea.Cmd {
	m.pendingPut = put
	cmd := next()
	if put := m.takePut(); put != nil {
		return tea.Batch(stored(put), cmd)
	}
	return cmd
}

func (m *Model) takePut() func() error {
	put := m.pendingPut
	m.pendingPut = nil
	return put
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
// it lands in is the first of them this board has open. Issues the board counts
// as done are left out altogether: finished work is neither in a sprint you can
// plan nor waiting to be scheduled. Done is the board's last column with a
// status mapped to it, never the status category, which a board whose last
// column is not the done-category one disagrees with (docs/API-NOTES.md).
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
		if m.finished(&m.issues[i]) {
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
		if m.estimate.ID != "" {
			if n, ok := m.issues[i].Fields.Number(m.estimate); ok {
				m.groups[at].points += n
				m.groups[at].pointed = true
			}
		}
	}
	m.orderIssues()
	m.rebuildRows()
	m.restore(under)
}

func (m *Model) finished(iss *jira.Issue) bool {
	if m.done == nil {
		return iss.Status.Category == jira.CategoryDone
	}
	return m.done[iss.Status.ID]
}

func estimateOf(cfg jira.BoardConfig) jira.FieldRef {
	if !cfg.Estimates() {
		return jira.FieldRef{}
	}
	return cfg.Estimation.Field
}

func doneStatuses(cfg jira.BoardConfig) map[string]bool {
	for c := len(cfg.Columns) - 1; c >= 0; c-- {
		ids := cfg.Columns[c].StatusIDs
		if len(ids) == 0 {
			continue
		}
		out := make(map[string]bool, len(ids))
		for _, id := range ids {
			out[strings.TrimSpace(id)] = true
		}
		return out
	}
	return nil
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

func (m *Model) underKey() string {
	if m.cursor < 0 || m.cursor >= len(m.rows) || m.rows[m.cursor].head {
		return ""
	}
	return m.issues[m.rows[m.cursor].issue].Key
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

// pickGroup reads the rest of the backlog first: a section is only whole once
// the walk is.
func (m *Model) pickGroup() tea.Cmd {
	if m.cursor < 0 || m.cursor >= len(m.rows) {
		return nil
	}
	g := m.groups[m.rows[m.cursor].group]
	if !m.page.HasMore() {
		m.pickSection(g.id)
		return nil
	}
	m.pendingGroup, m.pickAfterRead, m.reading = g.id, true, true
	return tea.Batch(
		kernel.Status("reading the rest of the backlog first, so every issue in "+g.name+" is picked"),
		m.readRest())
}

// pickSection names the section by sprint id, not index: a regroup moves it.
func (m *Model) pickSection(id int64) {
	at := slices.IndexFunc(m.groups, func(g group) bool { return g.id == id })
	if at < 0 {
		return
	}
	g := &m.groups[at]
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
	chunk := m.mv.chunk(m.mv.done)
	m.applyMoved(chunk)
	kept := stored(m.movedPut(chunk))
	m.mv.done++
	m.mv.moved += msg.moved
	if m.mv.done < m.mv.chunks {
		return tea.Batch(kept, m.nextChunk())
	}
	said := "moved " + count(m.mv.moved, "issue") + " into " + m.mv.name
	m.said, m.mode = said, browsing
	for _, key := range m.mv.keys {
		delete(m.picked, key)
	}
	m.endMove()
	return tea.Batch(kept, kernel.Status(said))
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
	case sorting:
		return m.sortKey(stroke)
	case finding:
		return m.findKey(msg)
	case movingIssues:
		return nil
	case browsing:
	}
	if m.WantsBack() && slices.Contains(backKeys, stroke) {
		return m.clearFilter()
	}
	if m.pendingGo {
		m.pendingGo = false
		if stroke == "g" {
			return m.moveTo(0)
		}
	}
	if act := issue.ShareStroke(stroke); act != issue.ShareNone {
		return issue.Share(m.deps, act, m.underKey())
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
		return m.pickGroup()
	case actClear:
		m.clearPicks()
		return nil
	case actMove:
		return m.startMove()
	case actFilterBy:
		return m.openFilterPicker()
	case actClearFilter:
		return m.clearFilter()
	case actSort:
		return m.startSort()
	case actRankUp:
		return m.reorder(rankUp)
	case actRankDown:
		return m.reorder(rankDown)
	case actRankTop:
		return m.reorder(rankTop)
	case actRankBottom:
		return m.reorder(rankBottom)
	case actMine:
		return m.toggleMine()
	case actFind:
		return m.startFind()
	case actFindNext:
		return m.findAgain(1)
	case actFindPrev:
		return m.findAgain(-1)
	case actCreate:
		return m.startCreate()
	case actNone, actChoose, actBack, actConfirm,
		actSortPrev, actSortNext, actSortChoose, actSortCancel, actFindKeep, actFindCancel:
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
	case sorting, finding:
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
	if m.sort.chosen() && m.zones.Hit(sortZone, msg) {
		return m.startSort()
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
	if cmd := m.dropWithin(from, msg); cmd != nil {
		return cmd
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
// program chose and the pane says which.
//
// A sort chosen here takes over from both: it is this program's own local
// reorder, described in its own words rather than the board's.
func (m *Model) ordering() string {
	if !m.loaded || len(m.boards) == 0 {
		return ""
	}
	if m.sort.chosen() {
		return "Sorted within each section by " + m.sort.plain(m.deps.Theme.Glyphs) + "."
	}
	if m.config.Ordering() == jira.OrderRank {
		return rankNote
	}
	return "No rank field on this board; oldest first, not its filter's order."
}
