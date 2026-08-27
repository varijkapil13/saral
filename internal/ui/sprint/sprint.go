// Package sprint is the sprints view: a board's sprints by state, and the moves
// the port allows on one.
//
// The lifecycle belongs to the port — future to active to closed and nothing
// else — so there is no state to set here and one key per move. Both moves are
// irreversible, so neither is reachable without a confirm that says what it
// does, and a refusal arrives as a typed error rather than as a round trip:
// the reason is drawn on the field it names.
//
// The list is asked for by state. A board that has been running for years has
// hundreds of closed sprints, and nothing on a first paint walks them.
package sprint

import (
	"context"
	"slices"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/varijkapil13/saral/internal/ui/kernel"
	"github.com/varijkapil13/saral/internal/ui/widget"
	"github.com/varijkapil13/saral/pkg/jira"
)

// ViewID is the name this view is registered and its keys are scoped under.
const ViewID = "sprints"

// slot is the digit g reaches this view with. docs/UX.md allocates it.
const slot = 4

const (
	// boardCap is how many of a project's boards are asked for sprints. A
	// project with more is named as truncated rather than walked: a board is a
	// read of its own, and a project can hold dozens.
	boardCap = 5
	// sprintCap bounds the walk over one board's sprints. The closed ones reach
	// back to its first day.
	sprintCap = 200
)

var (
	_ kernel.View        = (*Model)(nil)
	_ kernel.KeyCapturer = (*Model)(nil)
	_ kernel.Blocker     = (*Model)(nil)
	_ kernel.Addressed   = (*Model)(nil)
)

// state is which of the view's three screens is up.
type state uint8

const (
	browsing state = iota
	filling
	confirming
)

// NewMsg, EditMsg, StartMsg, CompleteMsg and ClosedMsg are what this view's
// palette entries carry. They are broadcasts because the palette knows which
// command was run and never which sprint is on screen.
// The two that name a lifecycle move still go through the confirm: the palette
// is a way to reach an action and never a way round the question it asks.
type (
	// NewMsg opens the form on a sprint that does not exist yet.
	NewMsg struct{}
	// EditMsg opens the form on the sprint under the cursor.
	EditMsg struct{}
	// StartMsg asks to start the sprint under the cursor.
	StartMsg struct{}
	// CompleteMsg asks to complete the sprint under the cursor.
	CompleteMsg struct{}
	// ClosedMsg shows or hides the closed sprints.
	ClosedMsg struct{}
)

// pending is the move a confirm is standing in front of.
type pending struct {
	op     op
	sprint jira.Sprint
	board  string
}

// Model is the sprints view.
type Model struct {
	deps    kernel.Deps
	acts    map[string]action
	inForm  map[string]action
	inConf  map[string]action
	keys    keyMap
	showAll bool

	state   state
	boards  []jira.Board
	more    int
	sprints []jira.Sprint

	form    form
	pending pending

	cursor, top   int
	width, height int

	loading, loaded bool
	inflight        op
	failure         error
	failedOp        op

	gen    int
	cancel context.CancelFunc
	addr   kernel.Addr

	styles *styles
	memo   *rowCache
	lay    layout
	lines  []string

	chrome   [2]string
	chromeAt chromeKey

	zones  widget.Zoner
	clicks *widget.Clicks
}

// New builds the view. It draws its first frame without an answer from the
// site, which is the empty state that says the site has not answered yet.
func New(d kernel.Deps) kernel.View {
	m := &Model{deps: d, addr: kernel.NewAddr(), keys: defaultKeys()}
	if m.deps.Theme == nil {
		m.deps.Theme = kernel.NewTheme(kernel.ThemeAuto, true, kernel.UnicodeGlyphs())
	}
	m.acts, m.inForm, m.inConf = m.keys.tables()
	m.styles = newStyles(m.deps.Theme)
	m.memo = newRowCache(rowMemoLimit)
	m.zones = widget.NewZoner(d.Zones)
	m.clicks = widget.NewClicks(d.Now)
	m.form = newForm()
	m.lay = planLayout(m.width, 1)
	return m
}

// WantsRawKeys is true while the form is taking typing and while the confirm is
// waiting for an answer. Without it the kernel matches its own bindings first,
// so a sprint name loses every digit, q quits the program out from under the
// typing, and esc never reaches the confirm to take it back.
func (m *Model) WantsRawKeys() bool { return m.state != browsing }

// BlocksClose refuses to let a filled-in form go with the program. The kernel
// asks before anything that would discard the view, and what is typed here is
// several fields' worth.
func (m *Model) BlocksClose() (string, bool) {
	if m.state == filling && m.form.dirty() {
		return "this sprint has not been sent — " + m.keys.Save.Help().Key + " sends it, " +
			m.keys.Discard.Help().Key + " discards it", true
	}
	return "", false
}

// Addr is where the kernel delivers this view's own answers, whatever has since
// been pushed over it.
func (m *Model) Addr() kernel.Addr { return m.addr }

// Init reads the boards this project has and the sprints on them.
func (m *Model) Init() tea.Cmd { return m.load() }

// Update handles one message.
func (m *Model) Update(msg tea.Msg) (kernel.View, tea.Cmd) {
	var cmd tea.Cmd
	switch msg := msg.(type) {
	case kernel.SizeMsg:
		m.resize(msg.Width, msg.Height)

	case kernel.FocusMsg:
		m.focus(msg.Focused)

	case kernel.ThemeMsg:
		m.deps.Theme = msg.Theme
		m.styles = newStyles(msg.Theme)
		m.memo.reset()
		m.chrome = [2]string{}

	case kernel.CapabilitiesMsg:
		m.deps.Caps = msg.Caps
		m.memo.reset()
		m.chrome = [2]string{}

	case kernel.ProjectMsg:
		m.deps.Project = msg.Project
		m.forget()
		cmd = m.load()

	case kernel.RefreshMsg:
		cmd = m.load()

	case loadedMsg:
		m.took(msg)

	case wroteMsg:
		cmd = m.wrote(msg)

	case failedMsg:
		cmd = m.failed(msg)

	case NewMsg:
		cmd = m.openCreate()

	case EditMsg:
		cmd = m.openEdit()

	case StartMsg:
		cmd = m.ask(opStart)

	case CompleteMsg:
		cmd = m.ask(opComplete)

	case ClosedMsg:
		cmd = m.toggleClosed()

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
	m.lay = planLayout(w, len(m.boards))
	m.form.resize(w)
	m.memo.reset()
	m.chrome = [2]string{}
	m.clampScroll()
}

// focus keeps the cursor out of a form nobody is typing into. It does not let
// go of a read: losing the keys is not being closed, and the kernel blurs a
// view it is switching away from as well as one it is covering.
func (m *Model) focus(on bool) {
	if on && m.state == filling {
		m.form.focus()
		return
	}
	m.form.blur()
}

// forget drops what was read for another project.
func (m *Model) forget() {
	m.boards, m.sprints, m.more = nil, nil, 0
	m.cursor, m.top = 0, 0
	m.loaded = false
	m.state = browsing
	m.memo.reset()
	m.chrome = [2]string{}
}

// --- reading ----------------------------------------------------------------

// states is what the list is narrowed to. The closed ones are asked for only
// when they are wanted, because they are the ones there are hundreds of.
func (m *Model) states() []jira.SprintState {
	if m.showAll {
		return []jira.SprintState{jira.SprintActive, jira.SprintFuture, jira.SprintClosed}
	}
	return []jira.SprintState{jira.SprintActive, jira.SprintFuture}
}

func (m *Model) load() tea.Cmd {
	if m.deps.Jira == nil {
		return nil
	}
	ctx, gen := m.begin()
	return m.reply(load(ctx, m.deps.Jira, m.deps.Project, m.states(), boardCap, sprintCap, gen))
}

// begin cancels whatever is in flight and opens a context for its replacement.
// The generation it returns is what a landing answer is checked against, so an
// answer to a question that has since changed is dropped rather than drawn.
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

// withCancel makes a command release its context however it ends. The cancel is
// also held on the model so the next request can cut this one short.
func withCancel(cancel context.CancelFunc, cmd tea.Cmd) tea.Cmd {
	return func() tea.Msg {
		defer cancel()
		return cmd()
	}
}

func (m *Model) took(msg loadedMsg) {
	if msg.gen != m.gen {
		return
	}
	m.loading, m.loaded, m.failure = false, true, nil
	under := m.underCursor()
	m.boards, m.more = msg.boards, msg.more
	m.sprints = sortSprints(msg.sprints)
	m.lay = planLayout(m.width, len(m.boards))
	m.memo.reset()
	m.chrome = [2]string{}
	m.restore(under)
}

// wrote puts the site's answer in place of what was held. A sprint that has
// moved state stays on the list whether or not its new state is one being asked
// for: it is what the user just did, and a row that vanished would be the only
// report of it.
func (m *Model) wrote(msg wroteMsg) tea.Cmd {
	if msg.gen != m.gen {
		return nil
	}
	m.inflight, m.loading, m.failure = opNone, false, nil
	at := slices.IndexFunc(m.sprints, func(sp jira.Sprint) bool { return sp.ID == msg.sprint.ID })
	if at < 0 {
		m.sprints = append(m.sprints, msg.sprint)
	} else {
		m.sprints[at] = msg.sprint
	}
	m.sprints = sortSprints(m.sprints)
	m.memo.reset()
	m.chrome = [2]string{}
	m.state = browsing
	m.form.close()
	m.restore(msg.sprint.ID)
	return kernel.Status(said(msg.op, msg.sprint))
}

// said is what the status line reports. A write whose answer did not move
// anything on screen is indistinguishable from one that never ran, so the
// sprint is named and so is what happened to it.
func said(o op, sp jira.Sprint) string {
	name := strings.TrimSpace(sp.Name)
	if name == "" {
		name = "the sprint"
	}
	switch o {
	case opCreate:
		return name + " is planned, and is not running yet"
	case opUpdate:
		return name + " is saved"
	case opStart:
		return name + " has started"
	case opComplete:
		return name + " is closed"
	case opNone, opRead:
	}
	return name
}

// failed keeps the refusal in the pane as well as on the status line: a status
// line is overwritten by the next thing that happens, and a list that is empty
// because the site said no has to keep saying so.
//
// A refusal about the values in a form goes back on the fields it names, which
// is the whole reason the port validates locally rather than at the site.
func (m *Model) failed(msg failedMsg) tea.Cmd {
	if msg.gen != m.gen {
		return nil
	}
	m.loading, m.inflight = false, opNone
	m.failure, m.failedOp = msg.err, msg.op
	if msg.op != opRead && m.form.open {
		m.state = filling
		m.form.annotate(msg.err)
		m.form.focus()
	}
	if m.state == confirming {
		m.state = browsing
	}
	m.memo.reset()
	m.chrome = [2]string{}
	return kernel.Fail(msg.err)
}

// --- the list ---------------------------------------------------------------

// sortSprints puts the sprint a team is in first, then the ones it is going to
// be in, then the ones it has finished, newest first. It sorts by state and by
// date and never by name: a name is whatever anybody typed.
func sortSprints(in []jira.Sprint) []jira.Sprint {
	slices.SortStableFunc(in, func(a, b jira.Sprint) int {
		if r := rankState(a.State) - rankState(b.State); r != 0 {
			return r
		}
		if rankState(a.State) == rankClosed {
			return compareTimes(b.End, a.End)
		}
		return compareTimes(a.Start, b.Start)
	})
	return in
}

const (
	rankActive = 0
	rankFuture = 1
	rankClosed = 2
	rankOther  = 3
)

// rankState orders the states without switching exhaustively on them: the type
// is an open string and a site can report a value none of the three constants
// covers.
func rankState(s jira.SprintState) int {
	switch s {
	case jira.SprintActive:
		return rankActive
	case jira.SprintFuture:
		return rankFuture
	case jira.SprintClosed:
		return rankClosed
	}
	return rankOther
}

// compareTimes sorts a sprint with no date after one that has one: a date that
// is not set is not a date at the beginning of time.
func compareTimes(a, b *time.Time) int {
	switch {
	case a == nil && b == nil:
		return 0
	case a == nil:
		return 1
	case b == nil:
		return -1
	case a.Before(*b):
		return -1
	case b.Before(*a):
		return 1
	}
	return 0
}

func (m *Model) rowCount() int { return len(m.sprints) }

// selected is the sprint under the cursor, or the zero sprint when there is
// none. The zero value is a sprint in no state, which is the answer every
// caller wants: nothing can be done to it.
func (m *Model) selected() jira.Sprint {
	if m.cursor < 0 || m.cursor >= len(m.sprints) {
		return jira.Sprint{}
	}
	return m.sprints[m.cursor]
}

func (m *Model) underCursor() int64 { return m.selected().ID }

// restore puts the cursor back on a sprint by id rather than by row number, so
// that a read or a write that reorders the list does not move the reader.
func (m *Model) restore(id int64) {
	m.cursor = 0
	if id != 0 {
		if at := slices.IndexFunc(m.sprints, func(sp jira.Sprint) bool { return sp.ID == id }); at >= 0 {
			m.cursor = at
		}
	}
	m.scrollToCursor()
}

// boardOf names the board a sprint is on. A sprint whose board this session did
// not read is named by nothing rather than by a guess.
func (m *Model) boardOf(sp jira.Sprint) string {
	for i := range m.boards {
		if m.boards[i].ID == sp.BoardID {
			return m.boards[i].Name
		}
	}
	return ""
}

func (m *Model) moveTo(at int) {
	n := m.rowCount()
	if n == 0 {
		m.cursor, m.top = 0, 0
		return
	}
	m.cursor = min(max(at, 0), n-1)
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
	m.top = min(max(m.top, 0), max(m.rowCount()-m.rowsHeight(), 0))
}

// --- keys -------------------------------------------------------------------

func (m *Model) key(msg tea.KeyPressMsg) tea.Cmd {
	// A write in flight answers nothing: the site has the question and the
	// answer is what decides what is on screen next.
	if m.inflight != opNone {
		return nil
	}
	switch m.state {
	case filling:
		return m.formKey(msg)
	case confirming:
		return m.confirmKey(msg)
	case browsing:
	}
	switch m.acts[msg.String()] {
	case actUp:
		m.moveTo(m.cursor - 1)
	case actDown:
		m.moveTo(m.cursor + 1)
	case actPageUp:
		m.moveTo(m.cursor - m.rowsHeight())
	case actPageDown:
		m.moveTo(m.cursor + m.rowsHeight())
	case actTop:
		m.moveTo(0)
	case actBottom:
		m.moveTo(m.rowCount() - 1)
	case actNew:
		return m.openCreate()
	case actEdit:
		return m.openEdit()
	case actStart:
		return m.ask(opStart)
	case actComplete:
		return m.ask(opComplete)
	case actClosed:
		return m.toggleClosed()
	case actNone, actNextField, actPrevField, actSave, actDiscard, actYes, actNo:
	}
	return nil
}

func (m *Model) confirmKey(msg tea.KeyPressMsg) tea.Cmd {
	switch m.inConf[msg.String()] {
	case actYes:
		return m.goAhead()
	case actNo:
		return m.refuse()
	default:
		return nil
	}
}

func (m *Model) toggleClosed() tea.Cmd {
	m.showAll = !m.showAll
	m.chrome = [2]string{}
	if !m.showAll {
		// The closed ones are dropped rather than kept and hidden, so that what
		// is on screen is what was asked for.
		under := m.underCursor()
		m.sprints = slices.DeleteFunc(m.sprints, func(sp jira.Sprint) bool {
			return rankState(sp.State) == rankClosed
		})
		m.memo.reset()
		m.restore(under)
		return kernel.Status("showing the active and planned sprints")
	}
	return m.load()
}

// --- the two moves nothing can undo -----------------------------------------

// ask puts the confirm in front of a move. Nothing else calls the write: both
// moves are irreversible, so the question is the only route to them and a
// palette entry reaches it rather than going round it.
func (m *Model) ask(o op) tea.Cmd {
	sp := m.selected()
	if sp.ID == 0 {
		return nil
	}
	if reason := refusal(o, sp); reason != "" {
		return kernel.Warn(reason)
	}
	m.state, m.pending = confirming, pending{op: o, sprint: sp, board: m.boardOf(sp)}
	m.chrome = [2]string{}
	m.clicks.Forget()
	return nil
}

// refusal is why a move cannot be made, in the words the port would use, worked
// out before anything is asked of the site. The port refuses the same thing;
// this is what keeps the key off the row it would be refused on.
func refusal(o op, sp jira.Sprint) string {
	switch o {
	case opStart:
		if sp.State != jira.SprintFuture {
			return "only a planned sprint can be started, and " + named(sp) + " is " + stateWord(sp.State)
		}
		switch {
		case sp.Start == nil && sp.End == nil:
			return named(sp) + " has no dates yet, and a sprint cannot start without both"
		case sp.Start == nil:
			return named(sp) + " has no start date yet, and a sprint cannot start without one"
		case sp.End == nil:
			return named(sp) + " has no end date yet, and a sprint cannot start without one"
		}
	case opComplete:
		if sp.State != jira.SprintActive {
			return "only a running sprint can be completed, and " + named(sp) + " is " + stateWord(sp.State)
		}
	case opNone, opRead, opCreate, opUpdate:
	}
	return ""
}

func named(sp jira.Sprint) string {
	if name := strings.TrimSpace(sp.Name); name != "" {
		return name
	}
	return "this sprint"
}

// stateWord is a state to put in a sentence, including the one this build does
// not know: the type is an open string and the site's own word is the honest
// thing to print.
func stateWord(s jira.SprintState) string {
	switch s {
	case jira.SprintFuture:
		return "planned"
	case jira.SprintActive:
		return "running"
	case jira.SprintClosed:
		return "closed"
	case "":
		return "in no state the site reported"
	}
	return string(s)
}

func (m *Model) refuse() tea.Cmd {
	m.state, m.pending = browsing, pending{}
	m.chrome = [2]string{}
	return kernel.Status("left alone")
}

// goAhead is the only caller of the two lifecycle writes, and it can only be
// reached from the confirm.
func (m *Model) goAhead() tea.Cmd {
	if m.state != confirming || m.deps.Jira == nil {
		return nil
	}
	sp, o := m.pending.sprint, m.pending.op
	if reason := refusal(o, sp); reason != "" {
		m.state, m.pending = browsing, pending{}
		return kernel.Warn(reason)
	}
	ctx, gen := m.begin()
	m.state, m.pending = browsing, pending{}
	m.inflight = o
	m.chrome = [2]string{}
	switch o {
	case opStart:
		return m.reply(startSprint(ctx, m.deps.Jira, sp.ID, gen))
	case opComplete:
		return m.reply(completeSprint(ctx, m.deps.Jira, sp.ID, gen))
	case opNone, opRead, opCreate, opUpdate:
	}
	m.inflight = opNone
	return nil
}

// --- mouse ------------------------------------------------------------------

// click resolves a click against what was actually drawn. A single click on a
// row selects it and a double-click does what the edit key does, which is the
// gesture every other list here answers to.
func (m *Model) click(msg tea.MouseClickMsg) tea.Cmd {
	if msg.Button != tea.MouseLeft {
		return nil
	}
	switch m.state {
	case confirming:
		switch {
		case m.zones.Hit(zoneConfirm, msg):
			return m.goAhead()
		case m.zones.Hit(zoneRefuse, msg):
			return m.refuse()
		}
		return nil
	case filling:
		return m.formClick(msg)
	case browsing:
	}
	for i := m.top; i < min(m.top+m.rowsHeight(), m.rowCount()); i++ {
		zone := m.zoneOf(i)
		if !m.zones.Hit(zone, msg) {
			continue
		}
		m.moveTo(i)
		if m.clicks.Double(zone) {
			return m.openEdit()
		}
		return nil
	}
	return nil
}

// wheel scrolls the rows without moving the selection, which is what a wheel
// does everywhere else.
func (m *Model) wheel(msg tea.MouseWheelMsg) {
	if m.state != browsing {
		return
	}
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
