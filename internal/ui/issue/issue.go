// Package issue is the read-only issue detail: the description rendered out of
// ADF beside the fields it belongs to and the thread that belongs to the same
// issue.
package issue

import (
	"context"
	"strings"
	"time"

	"charm.land/bubbles/v2/textarea"
	"charm.land/bubbles/v2/textinput"
	tea "charm.land/bubbletea/v2"

	"github.com/varijkapil13/saral/internal/app"
	"github.com/varijkapil13/saral/internal/config"
	"github.com/varijkapil13/saral/internal/ui/comment"
	"github.com/varijkapil13/saral/internal/ui/kernel"
	"github.com/varijkapil13/saral/internal/ui/richtext"
	"github.com/varijkapil13/saral/internal/ui/widget"
	"github.com/varijkapil13/saral/pkg/jira"
)

// ViewID is the scope this view's keys are registered under. The view is never a
// footer slot: it is pushed onto the stack with the issue it is about, so there
// is nothing for a registry constructor to build it from.
const ViewID = "issue"

// headerHeight is the two identity lines and the rule below them.
const headerHeight = 3

// zoneNames are the click targets, one per region, so that a wheel scrolls the
// region under the pointer and a click moves the keyboard to it.
var zoneNames = [regionCount]string{
	regionDesc:     "region:description",
	regionDetails:  "region:details",
	regionComments: "region:comments",
}

var (
	_ kernel.View      = (*Model)(nil)
	_ kernel.Closer    = (*Model)(nil)
	_ kernel.Addressed = (*Model)(nil)
)

// Model is the issue detail pane.
type Model struct {
	deps   kernel.Deps
	keys   keyMap
	styles *styles

	issue       jira.Issue
	labels      app.FieldLabels
	loadedIssue bool

	// edit is the site's own answer to which fields belong on this issue's
	// screen right now. A read that never arrives — it failed, or this build
	// has no site to ask — leaves it at its zero value, which fields.go reads
	// as "no ordering signal" rather than as an empty screen.
	edit jira.EditMeta

	// thread is the comment view itself rather than a second rendering of one.
	// The full-screen gesture hands this same instance to the kernel, so the
	// footer and the ? overlay are the thread's own keys and coming back lands
	// on the comment it was left on with the draft still in it.
	thread   kernel.View
	pushed   bool
	threadAt struct{ w, h int }

	focus     region
	lay       layout
	panes     [regionCount]content
	tops      [regionCount]int
	pans      [regionCount]int
	rails     [regionCount]railRun
	marks     [regionCount]string
	pendingGo bool

	// split is the share of the pane the sidebar takes, and the drag is the
	// gesture that moves it. dragFrom and dragSide are what the press found, so
	// that a resize, a key or a view switch can put the boundary back where it
	// was rather than leaving it wherever the pointer had reached.
	split       split
	drag        widget.Drag
	dragFrom    split
	dragSide    int
	dividerMark string
	splitFailed bool

	// open holds the expands the reader has opened, by the index the renderer
	// gave them; folded counts the times that set has changed, because a memo
	// keyed on a map would never see one.
	open    map[int]bool
	folds   []richtext.Fold
	folded  int
	dataGen int

	head         string
	headAt       contentKey
	threadLines  []string
	threadWidths []int
	threadRaw    string
	blank        string
	buf          []byte

	width, height int
	zones         widget.Zoner

	// rows is the persistent state of the fields this build knows how to edit —
	// summary, description, labels, due — rebuilt around every fresh read of the
	// issue and never otherwise, so an edit survives a redraw. sideRows is every
	// row the sidebar cursor can land on, cursorable and read-only alike,
	// rebuilt on every frame the details region actually redraws — see fields.go.
	rows     []fieldRow
	sideRows []cursorRow
	cursor   int
	clicks   *widget.Clicks

	// stage is what the sidebar is doing right now; leaving is the leave prompt,
	// tracked apart from it because a save started from that prompt still passes
	// through stage sideSaving and the prompt has to survive the save failing.
	stage   sideStage
	leaving bool
	input   textinput.Model
	docArea textarea.Model

	// saveFail is the last save's own refusal, in the site's words; draftRestored
	// says the dirty set on screen is one this pane picked back up rather than
	// one the user is in the middle of typing.
	saveFail      string
	draftRestored bool
	// editGen counts every keystroke a typing row or the description textarea
	// takes, which is what tells the sidebar's own memo a frame has to be
	// rebuilt when neither the cursor nor the stage has moved.
	editGen int

	drafts     draftStore
	launch     editorLauncher
	saveGen    int
	saveCancel context.CancelFunc
	docGen     int

	// pick is the inline list open beneath a choice, person or status row —
	// nil whenever stage is not sidePicking. pickGen and pickCancel are its own
	// read's cancellation, apart from the issue read's and the save's, because
	// a picker outlives neither of those and starts and cancels reads far more
	// often than either.
	pick       *picker
	pickGen    int
	pickCancel context.CancelFunc

	// me is this session's own account, asked for once and kept for as long as
	// this pane is open — the person picker's "me" row and "Assign to me" both
	// use it rather than asking the site again. pendingAssignSelf is set while
	// "Assign to me" is waiting on a first read of it.
	me                *jira.User
	meAsked           bool
	meCancel          context.CancelFunc
	pendingAssignSelf bool

	search *app.Search
	cache  app.Cache
	gen    int
	cancel context.CancelFunc

	// addr is where this pane's own answers come back to, and what the thread it
	// holds names as its holder: the sidebar is not a stack entry, so the kernel
	// reaches it through this pane.
	addr kernel.Addr
}

// Addr is where the kernel delivers what this pane asked the site for, whatever
// has been pushed over it since.
func (m *Model) Addr() kernel.Addr { return m.addr }

// modelOption configures the pane at construction. Nothing outside the package
// builds one; the tests use it to stand in for the user's editor and for a
// draft store nowhere near the person running them.
type modelOption func(*Model)

// withLauncher replaces the handoff to the user's editor.
func withLauncher(l editorLauncher) modelOption {
	return func(m *Model) { m.launch = l }
}

// withDrafts replaces where drafts are kept.
func withDrafts(s draftStore) modelOption {
	return func(m *Model) { m.drafts = s }
}

// New builds the detail pane around the row the user opened.
//
// The row is drawn immediately and the full issue replaces it when it arrives:
// docs/UX.md asks for a first paint that never waits, and the list already has
// the key, the summary and the status. The split the reader last chose is read
// here for the same reason: a constructor runs before the first frame and Init
// does not.
func New(d kernel.Deps, seed jira.Issue, opts ...modelOption) kernel.View {
	m := &Model{
		deps:   d,
		keys:   defaultKeys(),
		issue:  seed,
		cache:  d.Cache,
		open:   map[int]bool{},
		addr:   kernel.NewAddr(),
		input:  newSideInput(),
		launch: launchEditor,
	}
	if share, chosen := config.LoadUIState().Split(ViewID); chosen {
		m.split = split(share)
	}
	if m.deps.Theme == nil {
		m.deps.Theme = kernel.NewTheme(kernel.ThemeAuto, true, kernel.UnicodeGlyphs())
	}
	m.styles = newStyles(m.deps.Theme)
	m.zones = widget.NewZoner(d.Zones)
	m.clicks = widget.NewClicks(d.Now)
	for r := range regionCount {
		m.marks[r] = marker(m.zones, zoneNames[r])
	}
	m.dividerMark = marker(m.zones, dividerZone)
	if d.Jira != nil {
		m.search = app.NewSearch(d.Jira)
	}
	if store, err := newDraftStore(); err == nil {
		m.drafts = store
	}
	for _, o := range opts {
		if o != nil {
			o(m)
		}
	}
	m.thread = comment.Thread(m.deps, seed.Key, m.addr)
	m.fromCache()
	m.rebaseRows()
	return m
}

func newSideInput() textinput.Model {
	ti := widget.NewInput()
	ti.Prompt = ""
	// The widget's own cursor blink is a timer this pane would then own for as
	// long as a row is open, the same reason the description textarea drops it.
	ti.SetVirtualCursor(false)
	return ti
}

// fromCache enriches a seed that was not read wide — no seed at all, or the
// few fields a list row or a card carries — with whatever this issue's key
// already has on disk, before anything is asked of the site (docs/UX.md
// principle 1).
//
// It runs here rather than in Init because this is where a first paint
// happens: kernel.FirstPaint builds the view and renders one frame without
// ever calling Init, which is the thing docs/PERFORMANCE.md budgets.
//
// It never sets loadedIssue: a cached copy answers for the fields it was
// itself read with and says nothing about the rest, exactly as a freshly
// seeded row does, and fields.go's read reads loadedIssue to tell "known
// empty" from "not asked for" — flipping it here on a copy that might still be
// narrow would draw a field this cache never held as confidently blank.
func (m *Model) fromCache() {
	if m.issue.Key == "" || m.issue.Requested.Wide() {
		return
	}
	held, ok := m.cache.(app.IssueCache)
	if !ok || held == nil {
		return
	}
	snap, ok := held.Issue(m.issue.Key)
	if !ok {
		return
	}
	m.issue = app.MergeIssue(snap.Issue, m.issue)
}

// keepIssue stores a freshly read issue so this pane's next open draws it
// immediately. It merges into the same shared issue records a search's own
// rows do, so a field this pane never asked about — one a list row or a board
// card happened to carry — is left as it was.
func (m *Model) keepIssue(iss jira.Issue) tea.Cmd {
	held, ok := m.cache.(app.IssueCache)
	if !ok || held == nil {
		return nil
	}
	if err := held.PutIssue(iss); err != nil {
		return kernel.Warn("this issue could not be stored for next time: " + err.Error())
	}
	return nil
}

// Init reads the issue, and lets the thread read its own.
func (m *Model) Init() tea.Cmd { return tea.Batch(m.fetch(), m.thread.Init()) }

// Update handles one message.
func (m *Model) Update(msg tea.Msg) (kernel.View, tea.Cmd) {
	var cmd tea.Cmd
	switch msg := msg.(type) {
	case kernel.SizeMsg:
		m.resize(msg.Width, msg.Height)

	case kernel.FocusMsg:
		cmd = m.focused(msg.Focused)

	case kernel.ThemeMsg:
		m.deps.Theme = msg.Theme
		m.styles = newStyles(msg.Theme)
		cmd = m.tell(msg)

	case kernel.CapabilitiesMsg:
		m.deps.Caps = msg.Caps
		m.dataGen++
		cmd = m.tell(msg)

	case kernel.ProjectMsg:
		m.deps.Project = msg.Project
		m.dataGen++
		cmd = m.tell(msg)

	case kernel.RefreshMsg:
		if msg.Purge && m.search != nil {
			m.search.Invalidate()
		}
		cmd = join(m.fetch(), m.tell(msg))

	case loadedMsg:
		if m.current(msg.gen) {
			m.issue, m.labels, m.loadedIssue = msg.issue, msg.labels, true
			m.dataGen++
			m.rebaseRows()
			cmd = m.keepIssue(msg.issue)
		}

	case editMetaMsg:
		if m.current(msg.gen) {
			m.edit = msg.meta
			m.relist()
			m.dataGen++
		}

	case failedMsg:
		if m.current(msg.gen) {
			cmd = kernel.Fail(msg.err)
		}

	case savedMsg:
		cmd = m.saveResult(msg)

	case editedMsg:
		cmd = m.editedResult(msg)

	case peopleFoundMsg:
		m.peopleFound(msg)

	case meLoadedMsg:
		cmd = m.meLoaded(msg)

	case movesLoadedMsg:
		m.movesLoaded(msg)

	case moveDoneMsg:
		cmd = m.moveDone(msg)

	case editFailedMsg:
		cmd = m.pickFailed(msg)

	case CommentsMsg:
		cmd = m.openComments()

	case comment.WriteMsg, comment.EditMsg, comment.DeleteMsg:
		cmd = m.commentAction(msg)

	case splitFailedMsg:
		// Said once: the split works either way, and a warning on every stroke
		// would bury whatever came before it.
		m.splitFailed = true
		cmd = kernel.Warn("this split is not being remembered: " + msg.err.Error())

	case tea.KeyPressMsg:
		// Any key ends a gesture the pointer is in the middle of, which is what
		// keeps the boundary from following a pointer nobody is watching.
		m.cancelDrag()
		cmd = m.key(msg)

	case tea.MouseClickMsg:
		cmd = m.clicked(msg)

	case tea.MouseMotionMsg:
		m.dragDivider(msg)
		cmd = m.tell(msg)

	case tea.MouseReleaseMsg:
		cmd = join(m.dropDivider(msg), m.tell(msg))

	case tea.MouseWheelMsg:
		cmd = m.wheel(msg)

	default:
		cmd = join(m.splitMsg(msg), join(m.moveMsg(msg), join(m.assignMsg(msg), join(m.dirtyMsg(msg), m.tell(msg)))))
	}
	// The regions are laid out here rather than only in View so that a key
	// pressed before the first frame moves the content that is already in hand,
	// and so that the box the thread is given can be handed over as a command.
	m.build()
	return m, join(cmd, m.sizeThread())
}

// join is tea.Batch for two commands, without the variadic slice a keystroke
// that returns nothing would otherwise pay for on every frame.
func join(a, b tea.Cmd) tea.Cmd {
	switch {
	case a == nil:
		return b
	case b == nil:
		return a
	default:
		return tea.Batch(a, b)
	}
}

func (m *Model) current(gen int) bool { return gen == m.gen }

// fetch reads the issue and, alongside it rather than after it, asks the site
// which fields belong on its screen right now. Both share this pane's
// cancellation and its generation, and neither waits on the other: the second
// request is the whole cost of the ordering signal fields.go draws with, and
// starting it only once the first has answered would double what opening an
// issue costs to draw.
func (m *Model) fetch() tea.Cmd {
	if m.search == nil || m.deps.Jira == nil || m.issue.Key == "" {
		return nil
	}
	m.stop()
	m.gen++
	ctx, cancel := context.WithCancel(context.Background())
	m.cancel = cancel
	return join(
		kernel.Reply(load(ctx, m.search, m.issue.Key, m.gen), m.addr),
		kernel.Reply(loadEditMeta(ctx, m.deps.Jira, m.issue.Key, m.gen), m.addr),
	)
}

func (m *Model) stop() {
	if m.cancel != nil {
		m.cancel()
		m.cancel = nil
	}
}

// Close cuts the read short, and the thread's with it: the sidebar holds that
// model and nothing else does, so a pane thrown away takes it along. A save in
// flight is left to finish: it is a write already sent, and cutting it off here
// would leave the pane unsure whether it landed.
func (m *Model) Close() {
	m.stop()
	if m.pickCancel != nil {
		m.pickCancel()
	}
	if m.meCancel != nil {
		m.meCancel()
	}
	if m.thread != nil {
		kernel.CloseView(m.thread)
	}
}

// focused answers the kernel telling this pane whether it is the one taking
// keys. Coming back from the full-screen thread is where the thread's box has to
// be put back: the kernel gave it the whole screen on the way there.
func (m *Model) focused(on bool) tea.Cmd {
	if !on {
		// The read carries on: a palette opened over a loading pane must not
		// cancel what it is loading. Nobody is holding the divider, though.
		m.cancelDrag()
		return m.tell(kernel.FocusMsg{})
	}
	if m.pushed {
		m.pushed = false
		m.threadAt.w, m.threadAt.h = 0, 0
	}
	return m.tell(kernel.FocusMsg{Focused: true})
}

func (m *Model) resize(w, h int) {
	if w == m.width && h == m.height {
		return
	}
	// The boundary was grabbed at a width that has gone, so the delta measured
	// from the press means nothing now.
	m.cancelDrag()
	m.width, m.height = w, h
	if len(m.blank) < w {
		m.blank = strings.Repeat(" ", w)
	}
}

func (m *Model) location() *time.Location { return m.deps.Caps.Location() }

// build lays the regions out and re-renders whatever has gone stale. Every memo
// is held under a key carrying the width, the theme generation, the read the
// data came from and the expands that are open, so a resize, a theme switch, a
// fold, a project switch or a fresh read cannot leave a stale one behind.
func (m *Model) build() {
	if m.width <= 0 || m.height <= 0 {
		return
	}
	descW, sideW := m.contentWidths()
	m.refresh(regionDetails, sideW, m.detailContent)
	m.cursor = min(m.cursor, max(len(m.sideRows)-1, 0))
	m.lay = newLayout(m.width, m.height, len(m.panes[regionDetails].lines), m.focus, m.split)
	if m.stage == sideDocEdit {
		m.panes[regionDesc] = m.docEditContent(descW, m.lay.boxes[regionDesc].h)
	} else {
		m.refresh(regionDesc, descW, m.descLines)
	}
	m.buildHeader()
	for r := range regionCount {
		b := m.lay.boxes[r]
		total := len(m.panes[r].lines)
		if r == regionComments {
			// The thread scrolls itself and says how far along it is in its own
			// count line, so this gutter is the focus half only.
			total = 0
		}
		m.tops[r] = min(m.tops[r], max(total-b.h, 0))
		m.pans[r] = min(m.pans[r], max(m.panes[r].widest-b.content(), 0))
		m.rails[r] = railFor(b.h, total, m.tops[r], r == m.focus)
	}
	if cr := m.currentCursorRow(); cr != nil {
		if m.cursor == 0 {
			// The first row is worth showing from the very top of the region,
			// heading and all — the same way Home does for every other one.
			m.tops[regionDetails] = 0
		} else {
			m.tops[regionDetails] = followTop(
				m.tops[regionDetails], cr.lineAt, m.lay.boxes[regionDetails].h, len(m.panes[regionDetails].lines),
			)
		}
	}
}

// contentWidths is how wide each region's content is once its gutter has its
// column. It does not depend on how the sidebar splits vertically, which is what
// lets the fields be rendered before the layout that places them.
func (m *Model) contentWidths() (desc, side int) {
	if m.width < wideAt {
		w := max(m.width-gutter, 1)
		return w, w
	}
	side = sideWidth(m.width, m.split)
	return max(m.width-side-divider-gutter, 1), max(side-gutter, 1)
}

// refresh re-renders one region when anything its lines depend on has moved. The
// key is read again after the render rather than reused, because rendering the
// description is what discovers the expands in it.
func (m *Model) refresh(r region, w int, render func(int) content) {
	if m.panes[r].built && m.panes[r].key == m.contentKey(w, r) {
		return
	}
	c := render(w)
	c.key, c.built = m.contentKey(w, r), true
	m.panes[r] = c
}

func (m *Model) contentKey(w int, r region) contentKey {
	k := contentKey{
		width: w, theme: m.styles.gen, data: m.dataGen, folds: m.folded,
		bookkeeping: showBookkeeping.Load(),
	}
	if r == regionDetails {
		k.cursor, k.stage, k.edit = m.cursor, int(m.stage), m.editGen
	}
	return k
}

func (m *Model) buildHeader() {
	key := contentKey{
		width: m.width, theme: m.styles.gen, data: m.dataGen,
		dirty: m.dirtyCount(), leaving: m.leaving,
	}
	if m.head != "" && key == m.headAt {
		return
	}
	m.head, m.headAt = m.header(), key
}

func (m *Model) rendered(r region) (lines []string, widths []int) {
	if r == regionComments {
		return m.threadLines, m.threadWidths
	}
	return m.panes[r].lines, m.panes[r].widths
}

// key answers one keypress. Every key belongs to this pane, whichever region has
// the keyboard: the footer holds one set for the whole view, so a stroke cannot
// mean one thing in the description and something else beside it. What a stroke
// means is decided here first by the sidebar's own editing state — the leave
// prompt, a row being typed into, the description textarea, a save in flight —
// because every one of those is a state nothing else on this pane may act
// underneath.
func (m *Model) key(msg tea.KeyPressMsg) tea.Cmd {
	if m.leaving && m.stage != sideSaving {
		return m.leavingKey(msg)
	}
	switch m.stage {
	case sideSaving:
		return nil
	case sideTyping:
		return m.typingKey(msg)
	case sideDocEdit:
		return m.docEditKey(msg)
	case sidePicking:
		return m.pickKey(msg)
	case sideBrowse:
	}

	stroke := msg.String()
	if m.pendingGo {
		m.pendingGo = false
		switch stroke {
		case "g":
			return m.move(m.focus, stepTop, 1)
		case "e":
			return m.move(m.focus, stepBottom, 1)
		}
	}
	switch at := strokes[stroke]; at {
	case actNone:
		return nil
	case actGo:
		m.pendingGo = true
		return nil
	case actPane:
		m.focus = m.focus.next(1)
		return nil
	case actPrevPane:
		m.focus = m.focus.next(-1)
		return nil
	case actExpands:
		m.foldAll()
		return nil
	case actLeft:
		return m.pan(m.focus, -1)
	case actRight:
		return m.pan(m.focus, 1)
	case actSidebar:
		return m.moveDivider(-splitStep)
	case actDescribe:
		return m.moveDivider(splitStep)
	case actReset:
		return m.resetSplit()
	case actComments:
		return m.openComments()
	case actEdit:
		if m.focus == regionDesc {
			return m.startDescriptionEdit()
		}
		return m.actOnCursor()
	case actEditor:
		return m.handOffDescription()
	case actSave:
		return m.saveDirty()
	case actUndoRow:
		return m.undoRow()
	case actUndoAll:
		return m.undoAll()
	case actAssign:
		return m.openAssigneePicker()
	case actMove:
		cmd, _ := m.moveKey(msg)
		return cmd
	default:
		return m.move(m.focus, steps[at], 1)
	}
}

// startDescriptionEdit is e or enter while the description region has the
// keyboard: the same gesture the sidebar's own Description row answers to,
// reached from where the prose actually is rather than from a row naming it.
func (m *Model) startDescriptionEdit() tea.Cmd {
	row := m.rowByID("description")
	if row == nil || !row.editable() {
		return kernel.Warn("read-only")
	}
	m.saveFail, row.problem = "", ""
	return m.startDocEdit(row)
}

// move takes one region up or down. The comments region is a view rather than a
// list of lines, so it is handed the stroke that means the same motion in its
// own keymap, and the details region has a row cursor rather than a scroll
// offset of its own — see moveCursor.
func (m *Model) move(r region, at step, times int) tea.Cmd {
	b := m.lay.boxes[r]
	if !b.drawn() {
		return nil
	}
	if r == regionComments {
		press := threadSteps[at]
		cmds := make([]tea.Cmd, 0, times)
		for range times {
			cmds = append(cmds, m.tell(press))
		}
		return tea.Batch(cmds...)
	}
	if r == regionDetails {
		return m.moveCursor(at, times, b.h)
	}
	for range times {
		m.tops[r] = scroll(at, m.tops[r], len(m.panes[r].lines), b.h)
	}
	if at == stepTop {
		m.pans[r] = 0
	}
	return nil
}

// moveCursor walks the sidebar's row cursor. Page and half-page reuse the
// box's own height as one page of rows, since every row here is one line.
func (m *Model) moveCursor(at step, times, boxH int) tea.Cmd {
	if len(m.sideRows) == 0 {
		return nil
	}
	last := len(m.sideRows) - 1
	page := max(boxH, 1)
	switch at {
	case stepUp:
		m.cursor = max(m.cursor-times, 0)
	case stepDown:
		m.cursor = min(m.cursor+times, last)
	case stepPageUp:
		m.cursor = max(m.cursor-page*times, 0)
	case stepPageDown:
		m.cursor = min(m.cursor+page*times, last)
	case stepHalfUp:
		m.cursor = max(m.cursor-max(page/2, 1)*times, 0)
	case stepHalfDown:
		m.cursor = min(m.cursor+max(page/2, 1)*times, last)
	case stepTop:
		m.cursor = 0
	case stepBottom:
		m.cursor = last
	case stepCount:
	}
	return nil
}

// followTop keeps a line visible in a scrolled box, moving the top only as far
// as it has to.
func followTop(top, line, boxH, total int) int {
	if boxH <= 0 {
		return top
	}
	if line < top {
		top = line
	}
	if line >= top+boxH {
		top = line - boxH + 1
	}
	return min(max(top, 0), max(total-boxH, 0))
}

// pan moves a region sideways, which is what reaches a code line or a table
// wider than the box. The fields never need it — the sidebar clips its own lines
// to the box — and the thread pans itself, so it is handed the stroke that means
// the same thing in its own keymap.
func (m *Model) pan(r region, by int) tea.Cmd {
	b := m.lay.boxes[r]
	if !b.drawn() {
		return nil
	}
	if r == regionComments {
		if by > 0 {
			return m.tell(threadPanRight)
		}
		return m.tell(threadPanLeft)
	}
	room := max(m.panes[r].widest-b.content(), 0)
	m.pans[r] = min(max(m.pans[r]+by*panStep, 0), room)
	return nil
}

// foldAll opens every expand in the description, or closes them all again. There
// is no cursor in a document nobody can select inside, so the key is the whole
// set; a click is how one of them is reached on its own.
func (m *Model) foldAll() {
	if len(m.folds) == 0 {
		return
	}
	if len(m.open) > 0 {
		m.open = map[int]bool{}
		m.folded++
		return
	}
	for _, f := range m.folds {
		m.open[f.Index] = true
	}
	m.folded++
}

// foldAt opens or closes the one expand that was clicked.
func (m *Model) foldAt(msg tea.MouseMsg) bool {
	for _, f := range m.folds {
		if !m.zones.Hit(foldZone(f.Index), msg) {
			continue
		}
		if m.open[f.Index] {
			delete(m.open, f.Index)
		} else {
			m.open[f.Index] = true
		}
		m.folded++
		return true
	}
	return false
}

func (m *Model) clicked(msg tea.MouseClickMsg) tea.Cmd {
	if m.leaving {
		return m.clickLeavePrompt(msg)
	}
	if m.stage == sidePicking {
		return m.clickPicker(msg)
	}
	if cmd, hit := m.clickDirtyLine(msg); hit {
		return cmd
	}
	if m.grabDivider(msg) {
		return nil
	}
	// A press anywhere else ends a gesture whose release never arrived. The help
	// overlay swallows everything from the mouse while it is up, so a boundary
	// grabbed before ? was pressed is still held after it, and the next release
	// would otherwise apply a delta measured from a press two gestures ago.
	m.cancelDrag()
	r, ok := m.regionAt(msg)
	if !ok {
		return nil
	}
	m.focus = r
	switch r {
	case regionDetails:
		if m.leaving || m.stage != sideBrowse {
			return nil
		}
		return m.clickRow(msg)
	case regionDesc:
		m.foldAt(msg)
	}
	return nil
}

// clickLeavePrompt answers a click on one of the three words the leave prompt
// offers, the same as pressing the key it names.
func (m *Model) clickLeavePrompt(msg tea.MouseClickMsg) tea.Cmd {
	switch {
	case m.zones.Hit(zoneLeaveYes, msg):
		return m.leavingKey(press("y"))
	case m.zones.Hit(zoneLeaveNo, msg):
		return m.leavingKey(press("n"))
	case m.zones.Hit(zoneLeaveStay, msg):
		return m.leavingKey(press("esc"))
	}
	return nil
}

// clickDirtyLine answers a click on save or undo in the header's dirty-set
// line, wherever the pointer happens to be — the line sits above every
// region, so this is checked before any of them claims the click.
func (m *Model) clickDirtyLine(msg tea.MouseClickMsg) (tea.Cmd, bool) {
	switch {
	case m.zones.Hit(zoneDirtySave, msg):
		return m.saveDirty(), true
	case m.zones.Hit(zoneDirtyUndo, msg):
		return m.undoRow(), true
	case m.zones.Hit(zoneDirtyUndoAll, msg):
		return m.undoAll(), true
	}
	return nil, false
}

// clickRow puts the cursor on the row under the pointer, and opens it on a
// double-click.
func (m *Model) clickRow(msg tea.MouseClickMsg) tea.Cmd {
	for i := range m.sideRows {
		zone := fieldRowZone(m.sideRows[i].id)
		if !m.zones.Hit(zone, msg) {
			continue
		}
		if m.clicks.Double(zone) {
			m.cursor = i
			return m.actOnCursor()
		}
		m.cursor = i
		return nil
	}
	return nil
}

func (m *Model) wheel(msg tea.MouseWheelMsg) tea.Cmd {
	if m.stage == sidePicking && m.wheelPicker(msg) {
		return nil
	}
	r, ok := m.regionAt(msg)
	if !ok {
		r = m.focus
	}
	switch msg.Button {
	case tea.MouseWheelUp:
		return m.move(r, stepUp, widget.WheelStep)
	case tea.MouseWheelDown:
		return m.move(r, stepDown, widget.WheelStep)
	default:
		return nil
	}
}

// regionAt is which region the pointer is over, by zone lookup: bubblezone
// records where each region was drawn, and arithmetic on coordinates cannot
// work here at all — a mouse position is where it is on the terminal, and a view
// is never told where its own frame begins.
func (m *Model) regionAt(msg tea.MouseMsg) (region, bool) {
	for r := range regionCount {
		if m.lay.shows(r) && m.lay.boxes[r].drawn() && m.zones.Hit(zoneNames[r], msg) {
			return r, true
		}
	}
	return regionDesc, false
}
