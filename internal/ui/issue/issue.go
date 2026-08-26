// Package issue is the read-only issue detail: the description rendered out of
// ADF beside the fields it belongs to and the thread that belongs to the same
// issue.
package issue

import (
	"context"
	"strings"
	"time"

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
	_ kernel.View   = (*Model)(nil)
	_ kernel.Closer = (*Model)(nil)
)

// Model is the issue detail pane.
type Model struct {
	deps   kernel.Deps
	keys   keyMap
	styles *styles

	issue        jira.Issue
	labels       app.FieldLabels
	loadedIssue  bool
	loadingIssue bool

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

	head      string
	headAt    contentKey
	rows      []string
	rowWidths []int
	threadRaw string
	blank     string
	buf       []byte

	width, height int
	zones         widget.Zoner

	search *app.Search
	gen    int
	cancel context.CancelFunc
}

// New builds the detail pane around the row the user opened.
//
// The row is drawn immediately and the full issue replaces it when it arrives:
// docs/UX.md asks for a first paint that never waits, and the list already has
// the key, the summary and the status. The split the reader last chose is read
// here for the same reason: a constructor runs before the first frame and Init
// does not.
func New(d kernel.Deps, seed jira.Issue) kernel.View {
	m := &Model{
		deps:  d,
		keys:  defaultKeys(),
		issue: seed,
		open:  map[int]bool{},
	}
	if share, chosen := config.LoadUIState().Split(ViewID); chosen {
		m.split = split(share)
	}
	if m.deps.Theme == nil {
		m.deps.Theme = kernel.NewTheme(kernel.ThemeAuto, true, kernel.UnicodeGlyphs())
	}
	m.styles = newStyles(m.deps.Theme)
	m.zones = widget.NewZoner(d.Zones)
	for r := range regionCount {
		m.marks[r] = marker(m.zones, zoneNames[r])
	}
	m.dividerMark = marker(m.zones, dividerZone)
	if d.Jira != nil {
		m.search = app.NewSearch(d.Jira)
	}
	m.thread = comment.Thread(m.deps, seed.Key)
	return m
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
			m.loadingIssue = false
			m.dataGen++
		}

	case failedMsg:
		if m.current(msg.gen) {
			m.loadingIssue = false
			cmd = kernel.Fail(msg.err)
		}

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
		m.clicked(msg)

	case tea.MouseMotionMsg:
		m.dragDivider(msg)
		cmd = m.tell(msg)

	case tea.MouseReleaseMsg:
		cmd = join(m.dropDivider(msg), m.tell(msg))

	case tea.MouseWheelMsg:
		cmd = m.wheel(msg)

	default:
		cmd = join(m.splitMsg(msg), join(m.editMsg(msg), m.tell(msg)))
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

func (m *Model) fetch() tea.Cmd {
	if m.search == nil || m.deps.Jira == nil || m.issue.Key == "" {
		return nil
	}
	m.stop()
	m.gen++
	ctx, cancel := context.WithCancel(context.Background())
	m.cancel = cancel
	m.loadingIssue = true
	return load(ctx, m.search, m.issue.Key, m.gen)
}

func (m *Model) stop() {
	if m.cancel != nil {
		m.cancel()
		m.cancel = nil
	}
	m.loadingIssue = false
}

// Close cuts the read short, and the thread's with it: the sidebar holds that
// model and nothing else does, so a pane thrown away takes it along.
func (m *Model) Close() {
	m.stop()
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
	cmds := []tea.Cmd{m.tell(kernel.FocusMsg{Focused: true})}
	// An answer that landed while this pane was covered went to whatever was on
	// top, so a pane with nothing and nothing coming asks again.
	if !m.loadedIssue && !m.loadingIssue {
		cmds = append(cmds, m.fetch())
	}
	return tea.Batch(cmds...)
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
	m.lay = newLayout(m.width, m.height, len(m.panes[regionDetails].lines), m.focus, m.split)
	m.refresh(regionDesc, descW, m.descLines)
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
	if m.panes[r].built && m.panes[r].key == m.contentKey(w) {
		return
	}
	c := render(w)
	c.key, c.built = m.contentKey(w), true
	m.panes[r] = c
}

func (m *Model) contentKey(w int) contentKey {
	return contentKey{width: w, theme: m.styles.gen, data: m.dataGen, folds: m.folded}
}

func (m *Model) buildHeader() {
	key := contentKey{width: m.width, theme: m.styles.gen, data: m.dataGen}
	if m.head != "" && key == m.headAt {
		return
	}
	m.head, m.headAt = m.header(), key
}

func (m *Model) rendered(r region) (lines []string, widths []int) {
	if r == regionComments {
		return m.rows, m.rowWidths
	}
	return m.panes[r].lines, m.panes[r].widths
}

// key answers one keypress. Every key belongs to this pane, whichever region has
// the keyboard: the footer holds one set for the whole view, so a stroke cannot
// mean one thing in the description and something else beside it.
func (m *Model) key(msg tea.KeyPressMsg) tea.Cmd {
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
	case actEdit, actMove:
		cmd, _ := m.editKey(msg)
		return cmd
	default:
		return m.move(m.focus, steps[at], 1)
	}
}

// move takes one region up or down. The comments region is a view rather than a
// list of lines, so it is handed the stroke that means the same motion in its own
// keymap.
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
	for range times {
		m.tops[r] = scroll(at, m.tops[r], len(m.panes[r].lines), b.h)
	}
	if at == stepTop {
		m.pans[r] = 0
	}
	return nil
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

func (m *Model) clicked(msg tea.MouseClickMsg) {
	if m.grabDivider(msg) {
		return
	}
	// A press anywhere else ends a gesture whose release never arrived. The help
	// overlay swallows everything from the mouse while it is up, so a boundary
	// grabbed before ? was pressed is still held after it, and the next release
	// would otherwise apply a delta measured from a press two gestures ago.
	m.cancelDrag()
	r, ok := m.regionAt(msg)
	if !ok {
		return
	}
	m.focus = r
	if r == regionDesc {
		m.foldAt(msg)
	}
}

func (m *Model) wheel(msg tea.MouseWheelMsg) tea.Cmd {
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
