// Package issue is the read-only issue detail: the fields worth reading, the
// description rendered out of ADF, and the comment thread.
package issue

import (
	"context"
	"time"

	"charm.land/bubbles/v2/viewport"
	tea "charm.land/bubbletea/v2"

	"github.com/varijkapil13/saral/internal/app"
	"github.com/varijkapil13/saral/internal/ui/kernel"
	"github.com/varijkapil13/saral/pkg/jira"
)

// ViewID is the scope this view's keys are registered under. The view is never
// a footer slot: it is pushed onto the stack with the issue it is about, so
// there is nothing for a registry constructor to build it from.
const ViewID = "issue"

// headerHeight is the two identity lines and the rule below them.
const headerHeight = 3

var _ kernel.View = (*Model)(nil)

// Model is the issue detail pane.
type Model struct {
	deps   kernel.Deps
	keys   keyMap
	styles *styles

	issue          jira.Issue
	comments       []jira.Comment
	loadedIssue    bool
	loadedComments bool

	pager     viewport.Model
	built     bool
	builtAt   int
	builtGen  int
	pendingGo bool

	width, height int

	search *app.Search
	gen    int
	cancel context.CancelFunc
}

// New builds the detail pane around the row the user opened.
//
// The row is drawn immediately and the full issue replaces it when it arrives:
// docs/UX.md asks for a first paint that never waits, and the list already has
// the key, the summary and the status.
func New(d kernel.Deps, seed jira.Issue) kernel.View {
	m := &Model{
		deps:   d,
		keys:   defaultKeys(),
		styles: newStyles(d.Theme),
		issue:  seed,
		pager:  newPager(),
	}
	if m.deps.Theme == nil {
		m.deps.Theme = kernel.NewTheme(kernel.ThemeAuto, true, kernel.UnicodeGlyphs())
		m.styles = newStyles(m.deps.Theme)
	}
	if d.Jira != nil {
		m.search = app.NewSearch(d.Jira)
	}
	return m
}

func newPager() viewport.Model {
	vp := viewport.New()
	vp.SoftWrap = true
	vp.MouseWheelEnabled = true
	// Horizontal scrolling has nothing to reach once the text soft-wraps, and
	// leaving h and l bound would take two keys away from anything that does.
	vp.KeyMap.Left.SetEnabled(false)
	vp.KeyMap.Right.SetEnabled(false)
	return vp
}

// Init reads the issue and its thread.
func (m *Model) Init() tea.Cmd { return m.fetch() }

// Update handles one message.
func (m *Model) Update(msg tea.Msg) (kernel.View, tea.Cmd) {
	var cmd tea.Cmd
	switch msg := msg.(type) {
	case kernel.SizeMsg:
		m.resize(msg.Width, msg.Height)

	case kernel.FocusMsg:
		switch {
		case !msg.Focused:
			// A pushed view that loses focus has either been popped or been
			// covered, and either way nobody is waiting for this issue.
			m.stop()
		case !m.loadedIssue, !m.loadedComments:
			cmd = m.fetch()
		}

	case kernel.ThemeMsg:
		m.deps.Theme = msg.Theme
		m.styles = newStyles(msg.Theme)
		m.built = false

	case kernel.CapabilitiesMsg:
		m.deps.Caps = msg.Caps
		m.built = false

	case kernel.RefreshMsg:
		if msg.Purge && m.search != nil {
			m.search.Invalidate()
		}
		cmd = m.fetch()

	case loadedMsg:
		if m.current(msg.gen) {
			m.issue, m.loadedIssue, m.built = msg.issue, true, false
		}

	case commentsMsg:
		if m.current(msg.gen) {
			m.comments, m.loadedComments, m.built = msg.comments, true, false
		}

	case failedMsg:
		if m.current(msg.gen) {
			cmd = kernel.Fail(msg.err)
		}

	case tea.KeyPressMsg:
		cmd = m.key(msg)

	case tea.MouseWheelMsg:
		m.pager, _ = m.pager.Update(msg)

	default:
		cmd = m.editMsg(msg)
	}
	// The pager is filled here rather than in View so that a key pressed before
	// the first frame scrolls the content that is already in hand.
	m.build()
	return m, cmd
}

func (m *Model) current(gen int) bool { return gen == m.gen }

func (m *Model) fetch() tea.Cmd {
	if m.search == nil || m.deps.Jira == nil || m.issue.Key == "" {
		return nil
	}
	m.stop()
	m.gen++
	gen := m.gen
	ctx, cancel := context.WithCancel(context.Background())
	m.cancel = cancel
	key, search, client := m.issue.Key, m.search, m.deps.Jira

	// One context covers both requests, and stop cancels it: on a refetch, and
	// when the pane is closed or covered.
	return tea.Batch(load(ctx, search, key, gen), comments(ctx, client, key, gen))
}

func (m *Model) stop() {
	if m.cancel != nil {
		m.cancel()
		m.cancel = nil
	}
}

func (m *Model) resize(w, h int) {
	if w == m.width && h == m.height {
		return
	}
	m.width, m.height = w, h
	m.pager.SetWidth(w)
	m.pager.SetHeight(max(h-headerHeight, 1))
	m.built = false
}

func (m *Model) location() *time.Location { return m.deps.Caps.Location() }

func (m *Model) key(msg tea.KeyPressMsg) tea.Cmd {
	if m.pendingGo {
		m.pendingGo = false
		switch msg.String() {
		case "g":
			m.pager.GotoTop()
			return nil
		case "e":
			m.pager.GotoBottom()
			return nil
		}
	}
	if cmd, took := m.editKey(msg); took {
		return cmd
	}
	switch {
	case kernel.Matches(msg, m.keys.Go):
		m.pendingGo = true
		return nil
	case kernel.Matches(msg, m.keys.Top):
		m.pager.GotoTop()
		return nil
	case kernel.Matches(msg, m.keys.Bottom):
		m.pager.GotoBottom()
		return nil
	}
	m.pager, _ = m.pager.Update(msg)
	return nil
}

// View draws the identity lines and the pager below them.
func (m *Model) View() string {
	if m.width <= 0 || m.height <= 0 {
		return ""
	}
	m.build()
	return m.header() + "\n" + m.pager.View()
}

// build refreshes the pager's content when the data, the width or the theme has
// changed, and leaves the scroll position alone when none of them has.
func (m *Model) build() {
	if m.width <= 0 || (m.built && m.builtAt == m.width && m.builtGen == m.styles.gen) {
		return
	}
	at := m.pager.YOffset()
	m.pager.SetContent(m.body(m.width))
	m.pager.SetYOffset(at)
	m.built, m.builtAt, m.builtGen = true, m.width, m.styles.gen
}
