// Package board is the board: the columns a board's own configuration defines,
// the issues that fall into them, and moving one from one column to another.
package board

import (
	"context"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/varijkapil13/saral/internal/app"
	"github.com/varijkapil13/saral/internal/ui/issue"
	"github.com/varijkapil13/saral/internal/ui/kernel"
	"github.com/varijkapil13/saral/internal/ui/widget"
	"github.com/varijkapil13/saral/pkg/jira"
)

// ViewID is the name this view registers itself under and the scope its keys
// are registered in.
const ViewID = "board"

// cardCacheLimit is how many rendered cards are kept: a screenful of columns in
// both selected and unselected forms, a few relayouts deep.
const cardCacheLimit = 1024

// lineCacheLimit is how many composed grid lines are kept. A line is one row
// across every visible column, so a screen is at most a few dozen of them and
// this holds several screens of scrolling.
const lineCacheLimit = 512

var (
	_ kernel.View      = (*Model)(nil)
	_ kernel.Addressed = (*Model)(nil)
)

// held is the card that has been taken off the board and not yet landed. It is
// what the keyboard gesture and the pointer drag both write, so that the two
// cannot come to mean different things.
type held struct {
	key    string
	status string
	from   int
	row    int
	target int
}

// Model is the board.
type Model struct {
	deps   kernel.Deps
	search *app.Search
	styles *styles
	cards  *cardCache
	rows   *lineCache

	browsing map[string]action
	holding  map[string]action

	// all are the boards that draw on this project and at is the one being
	// drawn. A project with several is ordinary, and so is a project with none.
	all []jira.Board
	at  int

	plan  plan
	ready bool

	issues []jira.Issue
	// cols holds, per column, the indexes into issues that landed in it. An
	// issue whose status the board does not map lands in none of them and is
	// counted in unmapped instead, because a board does not show it either.
	cols     [][]int
	unmapped int
	more     bool
	// dataGen counts the rebuilds of cols, because a slice cannot be part of the
	// comparable key the chrome is memoized on.
	dataGen int

	curCol, curRow int
	colTop, rowTop int
	pendingGo      bool

	width, height int
	lay           layout
	blank         string

	// lines is the frame under construction, kept between frames so that drawing
	// a screen does not allocate one slice per frame.
	lines    []string
	summary  string
	sumKey   summaryKey
	head     string
	rule     string
	chromeAt chromeKey

	card   *held
	moving bool

	step     step
	loading  bool
	loaded   bool
	failure  error
	failStep step
	missing  []string
	checked  time.Time

	gen    int
	cancel context.CancelFunc
	addr   kernel.Addr

	zones   widget.Zoner
	clicks  *widget.Clicks
	drag    widget.Drag
	focused bool
}

// New builds the board. It draws nothing of the site in its first frame: which
// columns a board has is an answer, and the frame before that answer says which
// question is outstanding rather than a spinner.
func New(d kernel.Deps) kernel.View {
	m := &Model{deps: d, addr: kernel.NewAddr()}
	if m.deps.Theme == nil {
		m.deps.Theme = kernel.NewTheme(kernel.ThemeAuto, true, kernel.UnicodeGlyphs())
	}
	m.styles = newStyles(m.deps.Theme)
	m.cards = newCardCache(cardCacheLimit)
	m.rows = newLineCache(lineCacheLimit)
	m.browsing, m.holding = defaultKeys().tables()
	m.zones = widget.NewZoner(d.Zones)
	m.clicks = widget.NewClicks(d.Now)
	if d.Jira != nil {
		m.search = app.NewSearch(d.Jira)
	}
	return m
}

// Addr is where the kernel delivers the boards, the configuration, the cards and
// the move this view asked for, whatever has since been pushed over it and
// whichever root is on screen.
func (m *Model) Addr() kernel.Addr { return m.addr }

// Init asks which boards this project has, which is the first of the three
// questions a board is.
func (m *Model) Init() tea.Cmd { return m.load() }

// Update handles one message.
func (m *Model) Update(msg tea.Msg) (kernel.View, tea.Cmd) {
	var cmd tea.Cmd
	switch msg := msg.(type) {
	case kernel.SizeMsg:
		m.resize(msg.Width, msg.Height)

	case kernel.FocusMsg:
		m.focused = msg.Focused

	case kernel.ThemeMsg:
		m.deps.Theme = msg.Theme
		m.styles = newStyles(msg.Theme)
		m.forget()

	case kernel.CapabilitiesMsg:
		m.deps.Caps = msg.Caps
		m.forget()
		if !m.loaded && !m.loading {
			cmd = m.load()
		}

	case kernel.ProjectMsg:
		cmd = m.reproject(msg.Project)

	case kernel.RefreshMsg:
		cmd = m.refresh(msg.Purge)

	case MoveIssueMsg:
		cmd = m.pickUp()

	case NextBoardMsg:
		cmd = m.nextBoard()

	case boardsMsg:
		cmd = m.tookBoards(msg)

	case configMsg:
		cmd = m.tookConfig(msg)

	case issuesMsg:
		cmd = m.tookIssues(msg)

	case movesMsg:
		cmd = m.tookMoves(msg)

	case movedMsg:
		cmd = m.moved(msg)

	case failedMsg:
		cmd = m.failed(msg)

	case tea.KeyPressMsg:
		// Any key ends a gesture the pointer is in the middle of, so a card is
		// never left following a pointer nobody is watching.
		m.drag.Cancel()
		cmd = m.key(msg)

	case tea.MouseClickMsg:
		cmd = m.click(msg)

	case tea.MouseMotionMsg:
		m.dragging(msg)

	case tea.MouseReleaseMsg:
		cmd = m.released(msg)

	case tea.MouseWheelMsg:
		m.wheel(msg)
	}
	return m, cmd
}

// Close lets go of whichever of the three reads is in flight, and of a move
// still out with the site. A board that has been thrown away has nothing to
// draw.
func (m *Model) Close() { m.stop() }

// --- fetching ---------------------------------------------------------------

// begin cancels whatever is in flight and opens a context for its replacement.
// The generation it returns is what a landing answer is checked against, so a
// reply to a question this view has already changed is dropped rather than
// drawn.
func (m *Model) begin(s step) (ctx context.Context, gen int) {
	m.stop()
	m.gen++
	ctx, cancel := context.WithCancel(context.Background())
	m.cancel = cancel
	m.step, m.loading, m.failure = s, true, nil
	return ctx, m.gen
}

func (m *Model) stop() {
	if m.cancel != nil {
		m.cancel()
		m.cancel = nil
	}
	m.loading, m.moving = false, false
	m.step = stepIdle
}

// reply puts this board's address on a command, so what it asked for comes back
// here rather than to whatever the stack has on top by then. The board is a
// root: the detail pane is pushed over it and the palette over that, and it is
// parked off screen whenever another root is shown.
func (m *Model) reply(cmd tea.Cmd) tea.Cmd {
	return kernel.Reply(withCancel(m.cancel, cmd), m.addr)
}

func (m *Model) current(gen int) bool { return gen == m.gen }

// load starts again from the first question: which boards this project has.
func (m *Model) load() tea.Cmd {
	if m.deps.Jira == nil || !m.deps.Caps.Allows(jira.CapBoards) {
		return nil
	}
	ctx, gen := m.begin(stepBoards)
	return m.reply(boards(ctx, m.deps.Jira, m.deps.Project, gen))
}

func (m *Model) loadConfig() tea.Cmd {
	if m.deps.Jira == nil || m.at < 0 || m.at >= len(m.all) {
		return nil
	}
	ctx, gen := m.begin(stepConfig)
	return m.reply(config(ctx, m.deps.Jira, m.all[m.at].ID, gen))
}

func (m *Model) loadCards() tea.Cmd {
	if m.search == nil || !m.ready {
		return nil
	}
	ctx, gen := m.begin(stepIssues)
	return m.reply(cards(ctx, m.search, m.plan, m.deps.Project, gen))
}

// refresh re-reads what is on screen. Purging re-reads the board's shape as
// well, because a column an administrator added is not a change to the cards.
func (m *Model) refresh(purge bool) tea.Cmd {
	switch {
	case purge:
		if m.search != nil {
			m.search.Invalidate()
		}
		return m.load()
	case m.ready:
		return m.loadCards()
	default:
		return m.load()
	}
}

func (m *Model) reproject(project string) tea.Cmd {
	if project == m.deps.Project {
		return nil
	}
	m.deps.Project = project
	m.all, m.at, m.ready = nil, 0, false
	m.issues, m.cols, m.unmapped = nil, nil, 0
	m.curCol, m.curRow, m.colTop, m.rowTop = 0, 0, 0, 0
	m.card, m.loaded, m.checked = nil, false, time.Time{}
	m.forget()
	return m.load()
}

func (m *Model) tookBoards(msg boardsMsg) tea.Cmd {
	if !m.current(msg.gen) {
		return nil
	}
	m.loading, m.loaded, m.step = false, true, stepIdle
	m.all, m.at = msg.boards, 0
	m.ready = false
	m.forget()
	if len(m.all) == 0 {
		return nil
	}
	return m.loadConfig()
}

func (m *Model) tookConfig(msg configMsg) tea.Cmd {
	if !m.current(msg.gen) {
		return nil
	}
	m.loading, m.step = false, stepIdle
	m.plan, m.ready = newPlan(msg.cfg), true
	m.card = nil
	m.place()
	m.forget()
	return m.loadCards()
}

func (m *Model) tookIssues(msg issuesMsg) tea.Cmd {
	if !m.current(msg.gen) {
		return nil
	}
	under := m.selectedKey()
	m.loading, m.loaded, m.step = false, true, stepIdle
	m.issues, m.more, m.missing = msg.issues, msg.more, msg.missing
	m.checked = m.now()
	m.place()
	m.forget()
	m.restore(under)
	return m.saidMissing()
}

// saidMissing reports the field the board estimates in when this site has no
// such field, which is the only honest thing to do with a number that would
// otherwise be blank on every card.
func (m *Model) saidMissing() tea.Cmd {
	if len(m.missing) == 0 {
		return nil
	}
	return kernel.Warn("this site has no field called " + strings.Join(m.missing, ", "))
}

// failed keeps the refusal in the pane as well as on the status line: a status
// line is overwritten by the next thing that happens, and a board that is empty
// because the site said no has to keep saying so.
func (m *Model) failed(msg failedMsg) tea.Cmd {
	if !m.current(msg.gen) {
		return nil
	}
	m.loading, m.moving, m.step = false, false, stepIdle
	m.failure, m.failStep = msg.err, msg.step
	m.card = nil
	m.forget()
	return kernel.Fail(msg.err)
}

func (m *Model) nextBoard() tea.Cmd {
	if len(m.all) < 2 {
		return nil
	}
	m.at = (m.at + 1) % len(m.all)
	m.ready, m.issues, m.cols, m.unmapped = false, nil, nil, 0
	m.curCol, m.curRow, m.colTop, m.rowTop = 0, 0, 0, 0
	m.card = nil
	m.forget()
	return m.loadConfig()
}

// --- columns ----------------------------------------------------------------

// place puts every issue in the column its status belongs to. It is the whole of
// what decides a board's shape: a status id, matched against the ids the
// board's own configuration mapped into each column.
func (m *Model) place() {
	m.dataGen++
	m.unmapped = 0
	if !m.ready {
		m.cols = nil
		return
	}
	if len(m.cols) != len(m.plan.columns) {
		m.cols = make([][]int, len(m.plan.columns))
	}
	for i := range m.cols {
		m.cols[i] = m.cols[i][:0]
	}
	for i := range m.issues {
		at, mapped := m.plan.columnOf(m.issues[i].Status.ID)
		if !mapped {
			m.unmapped++
			continue
		}
		m.cols[at] = append(m.cols[at], i)
	}
	m.relayout()
	m.clamp()
}

// gridRows is how many rows the tallest column needs, which is how far the grid
// scrolls.
func (m *Model) gridRows() int {
	n := 0
	for i := range m.cols {
		n = max(n, len(m.cols[i]))
	}
	return n
}

func (m *Model) columnLen(col int) int {
	if col < 0 || col >= len(m.cols) {
		return 0
	}
	return len(m.cols[col])
}

func (m *Model) issueAt(col, row int) *jira.Issue {
	if col < 0 || col >= len(m.cols) || row < 0 || row >= len(m.cols[col]) {
		return nil
	}
	return &m.issues[m.cols[col][row]]
}

func (m *Model) selectedKey() string {
	if iss := m.issueAt(m.curCol, m.curRow); iss != nil {
		return iss.Key
	}
	return ""
}

// restore puts the cursor back on an issue by key, wherever a refetch moved it
// to — including into another column, which is exactly what a move that landed
// did to it.
func (m *Model) restore(key string) {
	if key != "" {
		for col := range m.cols {
			for row, at := range m.cols[col] {
				if m.issues[at].Key == key {
					m.curCol, m.curRow = col, row
					m.follow()
					return
				}
			}
		}
	}
	m.clamp()
}

func (m *Model) clamp() {
	m.curCol = min(max(m.curCol, 0), max(len(m.cols)-1, 0))
	m.curRow = min(max(m.curRow, 0), max(m.columnLen(m.curCol)-1, 0))
	m.follow()
}

// follow moves the two offsets as little as it takes to keep the cursor on
// screen, which is how a wheel and a keypress agree about where the board is.
func (m *Model) follow() {
	if m.lay.cols > 0 {
		switch {
		case m.curCol < m.colTop:
			m.colTop = m.curCol
		case m.curCol >= m.colTop+m.lay.cols:
			m.colTop = m.curCol - m.lay.cols + 1
		}
		m.colTop = min(max(m.colTop, 0), max(len(m.cols)-m.lay.cols, 0))
	}
	h := m.rowsHeight()
	switch {
	case m.curRow < m.rowTop:
		m.rowTop = m.curRow
	case m.curRow >= m.rowTop+h:
		m.rowTop = m.curRow - h + 1
	}
	m.clampScroll()
}

func (m *Model) clampScroll() {
	m.rowTop = min(max(m.rowTop, 0), max(m.gridRows()-m.rowsHeight(), 0))
}

func (m *Model) moveTo(col, row int) {
	if len(m.cols) == 0 {
		m.curCol, m.curRow = 0, 0
		return
	}
	m.curCol = min(max(col, 0), len(m.cols)-1)
	m.curRow = min(max(row, 0), max(m.columnLen(m.curCol)-1, 0))
	m.follow()
}

// --- moving a card ----------------------------------------------------------

// pickUp takes the card under the cursor off the board. It is the keyboard half
// of a drag and writes the same state the pointer does.
func (m *Model) pickUp() tea.Cmd {
	if m.moving || m.card != nil {
		return nil
	}
	iss := m.issueAt(m.curCol, m.curRow)
	if iss == nil {
		return nil
	}
	if len(m.plan.columns) < 2 {
		return kernel.Warn("this board has one column, so there is nowhere to move " + iss.Key + " to")
	}
	m.card = &held{key: iss.Key, status: iss.Status.Name, from: m.curCol, row: m.curRow, target: m.curCol}
	m.forget()
	return nil
}

// aim points the card in hand at a column. Both the two keys and the pointer
// come through here, so what the prompt says is what either gesture will do.
func (m *Model) aim(col int) {
	if m.card == nil || len(m.plan.columns) == 0 {
		return
	}
	col = min(max(col, 0), len(m.plan.columns)-1)
	if col == m.card.target {
		return
	}
	m.card.target = col
	m.moveTo(col, m.curRow)
	m.forget()
}

// putBack ends the gesture without asking the site for anything.
func (m *Model) putBack() {
	if m.card == nil {
		return
	}
	m.moveTo(m.card.from, m.card.row)
	m.card = nil
	m.drag.Cancel()
	m.forget()
}

// drop asks the site which of this issue's moves lands it in the column it is
// aimed at. The transitions are read here rather than kept: what an issue can do
// is per issue and per token and expires, so a list read when the board loaded
// would be a list of moves that may already be refused.
func (m *Model) drop() tea.Cmd {
	if m.card == nil {
		return nil
	}
	if m.card.target == m.card.from {
		name := m.plan.columns[m.card.from].name
		key := m.card.key
		m.putBack()
		return kernel.Status(key + " is already in " + name)
	}
	if m.deps.Jira == nil {
		m.putBack()
		return kernel.Warn("there is no Jira connection in this session")
	}
	key, target := m.card.key, m.card.target
	ctx, gen := m.begin(stepIssues)
	m.moving = true
	return m.reply(moves(ctx, m.deps.Jira, key, target, gen))
}

// tookMoves picks the transition that lands the issue in the column it was
// dropped on, by the id the site gave it. A status is not writable and a status
// name is not an identity, so the target column's status ids are what a
// transition is matched against.
func (m *Model) tookMoves(msg movesMsg) tea.Cmd {
	if !m.current(msg.gen) {
		return nil
	}
	m.moving, m.step = false, stepIdle
	if m.card == nil || m.card.key != msg.key {
		return nil
	}
	col := msg.column
	if col < 0 || col >= len(m.plan.columns) {
		m.putBack()
		return nil
	}
	tr, found := m.moveInto(msg.moves, col)
	name := m.plan.columns[col].name
	if !found {
		from := m.card.status
		m.putBack()
		return kernel.Warn("no workflow move takes " + msg.key + " from " + from + " into " + name +
			"; the columns a board draws and the moves a workflow allows are two different things")
	}
	// A transition insisting on a field cannot be made blind, so the pane that
	// fills a transition screen is handed the issue rather than a value being
	// guessed for it.
	if needsScreen(tr) {
		iss := m.byKey(msg.key)
		m.putBack()
		if iss == nil {
			return nil
		}
		return tea.Batch(
			kernel.Status(tr.Name+" needs more than a column, so it is being asked for"),
			kernel.Push(issue.MoveViewID, iss.Key, issue.NewMove(m.deps, *iss)),
		)
	}
	if m.deps.Jira == nil {
		m.putBack()
		return kernel.Warn("there is no Jira connection in this session")
	}
	from := m.plan.columns[m.card.from].name
	ctx, gen := m.begin(stepIssues)
	m.moving = true
	return m.reply(apply(ctx, m.deps.Jira, msg.key, tr.ID, name, from, gen))
}

// moveInto is the first transition landing in a column. First rather than best:
// the site decides the order it offers them in, and a column with two statuses
// in it has no way of preferring one of them that is not a guess.
func (m *Model) moveInto(list []jira.Transition, col int) (jira.Transition, bool) {
	for _, tr := range list {
		if at, mapped := m.plan.columnOf(tr.To.ID); mapped && at == col {
			return tr, true
		}
	}
	return jira.Transition{}, false
}

func (m *Model) byKey(key string) *jira.Issue {
	for i := range m.issues {
		if m.issues[i].Key == key {
			return &m.issues[i]
		}
	}
	return nil
}

func (m *Model) moved(msg movedMsg) tea.Cmd {
	if !m.current(msg.gen) {
		return nil
	}
	m.moving, m.step, m.card = false, stepIdle, nil
	m.drag.Cancel()
	m.forget()
	return tea.Batch(
		kernel.Status(msg.key+" moved from "+msg.from+" to "+msg.to),
		m.loadCards(),
	)
}

// --- input ------------------------------------------------------------------

func (m *Model) key(msg tea.KeyPressMsg) tea.Cmd {
	stroke := msg.String()
	if m.moving {
		return nil
	}
	if m.card != nil {
		switch m.holding[stroke] {
		case actLeft:
			m.aim(m.card.target - 1)
		case actRight:
			m.aim(m.card.target + 1)
		case actDrop:
			return m.drop()
		case actCancel:
			m.putBack()
		default:
		}
		return nil
	}
	if m.pendingGo {
		m.pendingGo = false
		switch stroke {
		case "g":
			m.moveTo(m.curCol, 0)
			return nil
		case "e":
			m.moveTo(m.curCol, m.columnLen(m.curCol)-1)
			return nil
		}
	}
	switch m.browsing[stroke] {
	case actUp:
		m.moveTo(m.curCol, m.curRow-1)
	case actDown:
		m.moveTo(m.curCol, m.curRow+1)
	case actLeft:
		m.moveTo(m.curCol-1, m.curRow)
	case actRight:
		m.moveTo(m.curCol+1, m.curRow)
	case actPageUp:
		m.moveTo(m.curCol, m.curRow-m.rowsHeight())
	case actPageDown:
		m.moveTo(m.curCol, m.curRow+m.rowsHeight())
	case actGo:
		m.pendingGo = true
	case actTop:
		m.moveTo(m.curCol, 0)
	case actBottom:
		m.moveTo(m.curCol, m.columnLen(m.curCol)-1)
	case actOpen:
		return m.open()
	case actPick:
		return m.pickUp()
	case actBoard:
		return m.nextBoard()
	case actNone, actDrop, actCancel:
	}
	return nil
}

func (m *Model) open() tea.Cmd {
	iss := m.issueAt(m.curCol, m.curRow)
	if iss == nil {
		return nil
	}
	return kernel.Push(issue.ViewID, iss.Key, issue.New(m.deps, *iss))
}

// click selects the card under the pointer, opens it on a real double-click, and
// grabs it so that a drag out of its column becomes the same move the keys make.
func (m *Model) click(msg tea.MouseClickMsg) tea.Cmd {
	if msg.Button != tea.MouseLeft {
		return nil
	}
	if m.card != nil {
		// A press while a card is in hand aims it, so a keyboard pick-up can be
		// landed with the pointer and the two gestures stay one thing.
		if col, over := m.columnUnder(msg); over {
			m.aim(col)
			return m.drop()
		}
		return nil
	}
	m.drag.Cancel()
	if col, row, on := m.cardUnder(msg); on {
		m.moveTo(col, row)
		zone := cardZone(m.selectedKey())
		if m.clicks.Double(zone) {
			return m.open()
		}
		m.drag.Start(zone, msg)
		return nil
	}
	if col, over := m.columnUnder(msg); over {
		m.clicks.Forget()
		m.moveTo(col, m.curRow)
	}
	return nil
}

// dragging turns a press that has left the card's own column into the pick-up
// the m key makes, and follows the pointer from column to column after that.
func (m *Model) dragging(msg tea.MouseMotionMsg) {
	if !m.drag.Active() {
		return
	}
	if _, _, ok := m.drag.Move(msg); !ok {
		return
	}
	col, over := m.columnUnder(msg)
	if !over {
		return
	}
	if m.card == nil {
		if col == m.curCol {
			return
		}
		_ = m.pickUp()
		if m.card == nil {
			// A board with one column has nowhere to drag to, which pickUp
			// refuses; the gesture ends with it rather than following the
			// pointer for nothing.
			m.drag.Cancel()
			return
		}
	}
	m.aim(col)
}

func (m *Model) released(msg tea.MouseReleaseMsg) tea.Cmd {
	if _, _, ok := m.drag.Release(msg); !ok {
		return nil
	}
	if m.card == nil {
		return nil
	}
	if col, over := m.columnUnder(msg); over {
		m.aim(col)
	}
	return m.drop()
}

// wheel scrolls the grid without moving the selection, which is what a wheel
// does everywhere else.
func (m *Model) wheel(msg tea.MouseWheelMsg) {
	switch msg.Button {
	case tea.MouseWheelUp:
		m.rowTop -= widget.WheelStep
	case tea.MouseWheelDown:
		m.rowTop += widget.WheelStep
	default:
		return
	}
	m.clampScroll()
}

// cardUnder is the card the pointer is over, by zone lookup over what is
// actually drawn.
func (m *Model) cardUnder(msg tea.MouseMsg) (col, row int, ok bool) {
	h := m.rowsHeight()
	for c := m.colTop; c < min(m.colTop+m.lay.cols, len(m.cols)); c++ {
		for r := m.rowTop; r < min(m.rowTop+h, m.columnLen(c)); r++ {
			iss := m.issueAt(c, r)
			if iss != nil && m.zones.Hit(cardZone(iss.Key), msg) {
				return c, r, true
			}
		}
	}
	return 0, 0, false
}

// columnUnder is the column the pointer is over. The strip is one zone from its
// caption to the rule under the grid, so the whole of a column answers — an
// empty one included, which is the column a card is most often dropped on.
func (m *Model) columnUnder(msg tea.MouseMsg) (col int, ok bool) {
	for c := m.colTop; c < min(m.colTop+m.lay.cols, len(m.plan.columns)); c++ {
		if m.zones.Hit(colZone(c), msg) {
			return c, true
		}
	}
	return 0, false
}

func (m *Model) resize(w, h int) {
	if w == m.width && h == m.height {
		return
	}
	m.width, m.height = w, h
	m.drag.Cancel()
	m.relayout()
	m.follow()
}

func (m *Model) relayout() {
	lay := planLayout(m.width, m.rowsHeight(), len(m.plan.columns))
	if lay == m.lay {
		return
	}
	m.lay = lay
	m.blank = strings.Repeat(" ", max(lay.cell, 0))
	m.forget()
}

// forget drops the memos. It is called whenever something they are not keyed on
// moves — the theme, the data, the gesture in progress — so that a stale line
// can never be redrawn.
func (m *Model) forget() {
	m.cards.reset()
	m.rows.reset()
	m.summary, m.head, m.rule = "", "", ""
}

func (m *Model) now() time.Time {
	if m.deps.Now == nil {
		return time.Time{}
	}
	return m.deps.Now()
}

// boardName is what the board on screen is called, which is the board's own name
// and never a word of this program's.
func (m *Model) boardName() string {
	if m.at < 0 || m.at >= len(m.all) {
		return ""
	}
	if name := strings.TrimSpace(m.all[m.at].Name); name != "" {
		return name
	}
	return m.plan.name
}
