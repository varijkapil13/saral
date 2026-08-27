// Package move is the wizard that moves issues to another project: the target
// project and issue type, what each status becomes over there, whatever the
// target insists on, and then a confirm screen naming the whole resolved mapping
// before anything is submitted.
//
// Jira only moves an issue between projects through its asynchronous bulk
// endpoint, so what a submit hands back is a task and not an outcome. The task
// is followed on the queue the port built for it, with a backoff, and the poll
// is given up when the view is thrown away — the move itself carries on, which
// the confirm screen says out loud.
//
// Nothing here is matched by display name. A status is remapped by id and an
// issue type is chosen by id, because a name is translated on a site that is not
// in English and is not unique even on one that is.
package move

import (
	"context"
	"strconv"
	"strings"
	"time"

	"charm.land/bubbles/v2/textinput"
	tea "charm.land/bubbletea/v2"

	"github.com/varijkapil13/saral/internal/ui/kernel"
	"github.com/varijkapil13/saral/internal/ui/widget"
	"github.com/varijkapil13/saral/pkg/jira"
)

// ViewID is the name this view's keys are registered under, and the id it has to
// be pushed with for the kernel to find them.
const ViewID = "move"

// Requires is the capability a cross-project move needs: global Bulk Change,
// plus Move in the source project and Create in the target. It is exported so
// that the view holding the issues can hide the way in here with the probe's own
// reason rather than offering an action that answers 403.
const Requires = jira.CapBulkMove

var (
	_ kernel.View        = (*Model)(nil)
	_ kernel.KeyCapturer = (*Model)(nil)
	_ kernel.Addressed   = (*Model)(nil)
	_ kernel.Closer      = (*Model)(nil)
)

// step is where the wizard has got to. It doubles as the generation the footer
// repaints on, so a step that is added has to be added here to be drawn.
type step uint8

const (
	stepTarget step = iota
	stepTyping
	stepType
	stepStatus
	stepFields
	stepConfirm
	stepRunning
	stepDone
	steps
)

// Option configures a wizard at construction.
type Option func(*Model)

// WithIssues names the issues to move. It is how the view holding them opens
// this: there is no port method that lists projects and none that lists a
// selection, so both halves arrive from the view that already has them.
func WithIssues(issues []jira.Issue) Option {
	return func(m *Model) { m.issues = append([]jira.Issue(nil), issues...) }
}

// withWaiter replaces the pause between two questions about a task, so that a
// test can hold the backoff to account without spending it.
func withWaiter(w waiter) Option {
	return func(m *Model) {
		if w != nil {
			m.wait = w
		}
	}
}

// Model is the move wizard.
type Model struct {
	deps   kernel.Deps
	keys   keyMap
	acts   map[string]action
	typing map[string]action

	issues []jira.Issue
	step   step

	found  []string
	looked bool
	input  textinput.Model

	target string
	vocab  []jira.IssueTypeStatuses
	typeAt int

	remaps []remap
	fields []pending
	// schema records that the target has answered what it insists on. Nothing
	// asks a user to confirm a move whose mandatory fields are still unknown.
	schema bool
	notify bool
	// planGen counts the changes to the resolved mapping, which is what the
	// memoized head repaints on: the mapping is built from slices and a key
	// holding one is not comparable.
	planGen int

	ref     jira.TaskRef
	state   jira.TaskState
	percent int
	attempt int
	failed  []string
	// paused is what a rate limit asked for, kept so the pane can say why
	// nothing is happening rather than looking wedged.
	paused time.Duration

	cursor, top   int
	width, height int

	loading bool
	failure error
	reason  string
	// warned is the refusal the wizard is keeping on screen — a step that cannot
	// be left, a selection too big to submit. The status line that said it first
	// is gone by the next keypress.
	warned string

	gen    int
	ctx    context.Context
	cancel context.CancelFunc
	addr   kernel.Addr
	wait   waiter

	styles *styles
	memo   *rowCache
	lay    layout
	head   []string
	headAt headKey
	tail   []string
	tailAt tailKey
	lines  []string

	zones  widget.Zoner
	clicks *widget.Clicks
}

// New builds the wizard. It opens on the target project, which is the one
// question that has to be answered before anything else can be asked of the
// site.
func New(d kernel.Deps, opts ...Option) kernel.View {
	m := &Model{deps: d, keys: defaultKeys(), input: newInput(), addr: kernel.NewAddr(), wait: sleep}
	for _, opt := range opts {
		opt(m)
	}
	if m.deps.Theme == nil {
		m.deps.Theme = kernel.NewTheme(kernel.ThemeAuto, true, kernel.UnicodeGlyphs())
	}
	m.acts, m.typing = m.keys.table(), m.keys.typingTable()
	m.styles = newStyles(m.deps.Theme)
	m.memo = newRowCache(rowMemoLimit)
	m.zones = widget.NewZoner(d.Zones)
	m.clicks = widget.NewClicks(d.Now)
	m.lay = planLayout(m.width)
	m.reason = m.refusal()
	return m
}

// refusal is why this session cannot move issues between projects, in the
// probe's own words, and "" when it can. A negative is the normal answer here —
// Bulk Change is a global permission most tokens do not hold — so it is drawn
// rather than raised.
func (m *Model) refusal() string {
	switch {
	case m.deps.Jira == nil:
		return "there is no Jira connection in this session"
	case m.deps.Caps.Allows(Requires):
		return ""
	}
	if said := m.deps.Caps.Capability(Requires).Reason; said != "" {
		return said
	}
	return "this token may not move issues between projects on this site"
}

// WantsRawKeys is true only while a project key is being typed. Without it the
// kernel matches its own bindings first, so the key loses every digit and q
// quits the program out from under the typing.
func (m *Model) WantsRawKeys() bool { return m.step == stepTyping }

// Init asks which projects are worth offering as a target. There is no port
// method that lists projects, so this is a page of the account's own recent
// issues and the keys behind them.
func (m *Model) Init() tea.Cmd {
	if m.reason != "" || m.looked {
		return nil
	}
	ctx, gen := m.begin()
	return m.reply(candidates(ctx, m.deps.Jira, gen))
}

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
		m.forget()

	case kernel.CapabilitiesMsg:
		m.deps.Caps = msg.Caps
		m.reason = m.refusal()
		m.forget()

	case kernel.RefreshMsg:
		cmd = m.reread()

	case candidatesMsg:
		cmd = m.tookCandidates(msg)

	case vocabularyMsg:
		cmd = m.tookVocabulary(msg)

	case schemaMsg:
		cmd = m.tookSchema(msg)

	case submittedMsg:
		cmd = m.tookRef(msg)

	case taskMsg:
		cmd = m.tookTask(msg)

	case failedMsg:
		cmd = m.tookFailure(msg)

	case tea.KeyPressMsg:
		cmd = m.key(msg)

	case tea.MouseClickMsg:
		cmd = m.click(msg)

	case tea.MouseWheelMsg:
		m.wheel(msg)
	}
	return m, cmd
}

func newInput() textinput.Model {
	ti := textinput.New()
	ti.Prompt = "> "
	ti.Placeholder = "project key"
	return ti
}

func (m *Model) resize(w, h int) {
	if w == m.width && h == m.height {
		return
	}
	m.width, m.height = w, h
	m.lay = planLayout(w)
	m.input.SetWidth(max(w-inputChrome, 8))
	m.forget()
	m.clampScroll()
}

// focus keeps the cursor out of a field nobody is typing into. It does not let
// go of a request: losing the keys is not being closed, and the kernel blurs a
// view it is pushing over as well as one it is discarding.
func (m *Model) focus(on bool) {
	m.head, m.tail = nil, nil
	if on && m.step == stepTyping {
		_ = m.input.Focus()
		return
	}
	m.input.Blur()
}

// forget drops every memoized line. It is what a resize, a theme and a new
// capability answer all do: each of them changes what a row looks like.
func (m *Model) forget() {
	m.memo.reset()
	m.head, m.tail = nil, nil
}

// reread asks again for whatever the step on screen is drawn from. A move that
// has been submitted is not re-read: asking the queue again is what the poll is
// already doing.
func (m *Model) reread() tea.Cmd {
	if m.reason != "" {
		return nil
	}
	switch m.step {
	case stepTarget, stepTyping:
		m.looked = false
		return m.Init()
	case stepType, stepStatus, stepFields, stepConfirm:
		return m.lookUp(m.target)
	case stepRunning, stepDone:
	}
	return nil
}

// --- addressing and cancellation --------------------------------------------

// Addr is where the kernel delivers what this wizard asked for, whatever has
// since been pushed over it.
func (m *Model) Addr() kernel.Addr { return m.addr }

// reply puts this wizard's address on a command, so its answers come back here
// rather than to whatever the stack has on top by then.
func (m *Model) reply(cmd tea.Cmd) tea.Cmd { return kernel.Reply(cmd, m.addr) }

// begin cancels whatever is in flight and opens a context for its replacement.
// The generation it returns is what a landing answer is checked against, so a
// reply to a question the user has already moved past is dropped.
func (m *Model) begin() (ctx context.Context, gen int) {
	m.stop()
	m.gen++
	ctx, cancel := context.WithCancel(context.Background())
	m.ctx, m.cancel = ctx, cancel
	m.loading, m.failure = true, nil
	m.head, m.tail = nil, nil
	return ctx, m.gen
}

func (m *Model) stop() {
	if m.cancel != nil {
		m.cancel()
		m.cancel = nil
	}
	m.loading = false
}

// Close lets go of a read still out with the site and of the poll following a
// task. The move itself is not stopped by this and cannot be: it belongs to
// Jira's queue once it has been submitted, which is what the confirm screen says
// before anybody agrees to it.
func (m *Model) Close() { m.stop() }

func (m *Model) current(gen int) bool { return gen == m.gen }

// --- answers ----------------------------------------------------------------

func (m *Model) tookCandidates(msg candidatesMsg) tea.Cmd {
	if !m.current(msg.gen) {
		return nil
	}
	m.stop()
	m.failure, m.looked = nil, true
	m.found = m.without(msg.keys)
	m.cursor, m.top = 0, 0
	m.forget()
	return nil
}

// without drops the projects the issues are already in. A move to the project
// they are in is not a move, and offering it is offering a no-op.
func (m *Model) without(keys []string) []string {
	held := make(map[string]bool, 2)
	for i := range m.issues {
		held[m.issues[i].Project.Key] = true
	}
	out := make([]string, 0, len(keys))
	for _, key := range keys {
		if !held[key] {
			out = append(out, key)
		}
	}
	return out
}

func (m *Model) tookVocabulary(msg vocabularyMsg) tea.Cmd {
	if !m.current(msg.gen) || msg.project != m.target {
		return nil
	}
	m.stop()
	m.failure = nil
	m.vocab, m.schema = msg.types, false
	m.remaps, m.fields = nil, nil
	m.cursor, m.top = 0, 0
	m.planGen++
	m.forget()
	m.step = stepType
	m.input.Blur()
	if len(m.vocab) == 0 {
		m.warned = m.target + " offers no issue type this token can create in"
		return kernel.Warn(m.warned)
	}
	return nil
}

func (m *Model) tookSchema(msg schemaMsg) tea.Cmd {
	if !m.current(msg.gen) {
		return nil
	}
	m.stop()
	m.failure = nil
	m.fields, m.schema = mandatory(msg.schema), true
	m.planGen++
	m.forget()
	return nil
}

func (m *Model) tookRef(msg submittedMsg) tea.Cmd {
	if !m.current(msg.gen) {
		return nil
	}
	m.ref, m.attempt, m.paused = msg.ref, 0, 0
	m.state, m.percent = jira.TaskEnqueued, 0
	m.loading = true
	m.head, m.tail = nil, nil
	return m.reply(poll(m.pollContext(), m.deps.Jira, m.ref, m.wait, 0, m.gen))
}

// pollContext is the context the poll runs under, which is the one begin opened
// for the submit: following a task is the same piece of work as handing it over,
// so closing the view gives up both.
func (m *Model) pollContext() context.Context {
	if m.ctx == nil {
		return context.Background()
	}
	return m.ctx
}

// tookTask draws one answer from the queue and decides whether to ask again.
//
// Whether the task has stopped is State.Done and never a switch written here:
// CANCEL_REQUESTED is a task still running, and a poller with no case for it
// reports a move in progress as finished.
func (m *Model) tookTask(msg taskMsg) tea.Cmd {
	if !m.current(msg.gen) {
		return nil
	}
	m.state, m.percent, m.paused = msg.status.State, msg.status.Progress, 0
	m.failed = msg.status.Failed
	m.head, m.tail = nil, nil
	if msg.status.State.Done() {
		m.stop()
		m.step = stepDone
		return m.finished()
	}
	m.attempt++
	return m.reply(poll(m.pollContext(), m.deps.Jira, m.ref, m.wait, backoff(m.attempt), m.gen))
}

// finished says on the status line what the queue reported, in the words of what
// the task actually did. A complete task with issues in Failed is a partial
// outcome and is reported as one rather than as a success.
func (m *Model) finished() tea.Cmd {
	moved := len(m.issues) - len(m.failed)
	switch {
	case len(m.failed) > 0:
		return kernel.Warn(plural(moved, "issue") + " moved to " + m.target + ", " +
			plural(len(m.failed), "issue") + " did not")
	case m.state == jira.TaskComplete:
		return kernel.Status(plural(moved, "issue") + " moved to " + m.target)
	}
	return kernel.Warn("the move of " + plural(len(m.issues), "issue") + " to " + m.target +
		" ended " + strings.ToLower(strings.ReplaceAll(string(m.state), "_", " ")))
}

// tookFailure keeps the refusal in the pane as well as on the status line, and a
// rate limit is a pause rather than a failure: the queue is still working and
// the wizard is being told to ask less often.
func (m *Model) tookFailure(msg failedMsg) tea.Cmd {
	if !m.current(msg.gen) {
		return nil
	}
	m.head, m.tail = nil, nil
	if msg.at == stepRunning && m.step == stepRunning {
		if wait, limited := held(msg.err, m.attempt); limited {
			m.attempt, m.paused = m.attempt+1, wait
			return m.reply(poll(m.pollContext(), m.deps.Jira, m.ref, m.wait, wait, m.gen))
		}
		m.stop()
		m.failure = msg.err
		m.step = stepDone
		return kernel.Fail(msg.err)
	}
	m.stop()
	m.failure = msg.err
	m.forget()
	return kernel.Fail(msg.err)
}

func plural(n int, what string) string {
	if n == 1 {
		return "1 " + what
	}
	return strconv.Itoa(n) + " " + what + "s"
}
