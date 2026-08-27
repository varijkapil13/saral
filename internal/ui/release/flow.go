package release

import (
	"context"
	"errors"
	"strconv"

	tea "charm.land/bubbletea/v2"

	"github.com/varijkapil13/saral/internal/ui/kernel"
	"github.com/varijkapil13/saral/internal/ui/widget"
	"github.com/varijkapil13/saral/pkg/jira"
)

var (
	_ kernel.View      = (*Flow)(nil)
	_ kernel.Closer    = (*Flow)(nil)
	_ kernel.Addressed = (*Flow)(nil)
)

// flowState is which of the flow's screens is up. It doubles as the generation
// the memoized chrome repaints on, so a state that is added has to be added here
// to be drawn.
type flowState int

const (
	// flowChoosing offers the three things that can happen to the issues still
	// open on a version. It is skipped only when there are none.
	flowChoosing flowState = iota
	flowPicking
	flowConfirming
	flowWorking
	flowStuck
	flowKeyStates
)

// Flow is the screen that releases one version.
//
// It is pushed with the count already read, because the count is the whole
// decision: Jira's own API releases a version over the top of whatever is open
// on it and says nothing, and this is the screen that refuses to do that. The
// only path from here to the write is the confirm, and the confirm cannot be
// skipped — not by a version with nothing open on it, which still gets one, and
// not by a policy, which cannot be chosen without passing through it.
type Flow struct {
	deps    kernel.Deps
	version jira.Version
	open    int
	targets []jira.Version

	state      flowState
	choices    []choice
	targetRows []choice
	policy     jira.UnresolvedPolicy
	target     jira.Version

	cursor, top   int
	width, height int

	acts    map[string]flowAction
	failure error
	gen     int
	cancel  context.CancelFunc
	addr    kernel.Addr

	styles *styles
	rows   *memo[flowRowKey]
	head   [3]string
	headAt chromeKey
	lines  []string
	zones  widget.Zoner
	clicks *widget.Clicks
}

// NewFlow builds the release screen over one version. open is how many issues
// are still open on it, already counted, and targets are the versions its open
// issues could be moved to.
func NewFlow(d kernel.Deps, v jira.Version, open int, targets []jira.Version) kernel.View {
	f := &Flow{deps: d, version: v, open: max(open, 0), targets: targets, addr: kernel.NewAddr()}
	if f.deps.Theme == nil {
		f.deps.Theme = kernel.NewTheme(kernel.ThemeAuto, true, kernel.UnicodeGlyphs())
	}
	f.version.Unresolved = &f.open
	f.choices, f.targetRows = f.buildChoices(), f.buildTargets()
	f.acts = defaultFlowKeys().table()
	f.styles = newStyles(f.deps.Theme)
	f.rows = newMemo[flowRowKey](flowMemoLimit)
	f.zones = widget.NewZoner(d.Zones)
	f.clicks = widget.NewClicks(d.Now)
	// Nothing open is nothing to decide, and it is still not nothing to
	// confirm: a release is a release whether or not it swept anything up.
	if f.open == 0 {
		f.state = flowConfirming
	}
	return f
}

// Init has nothing to fetch. The count it turns on was read on the way in, so
// the first frame is the decision rather than a wait.
func (f *Flow) Init() tea.Cmd { return nil }

// Addr is where the released version comes back to, whatever has since been
// pushed over this screen.
func (f *Flow) Addr() kernel.Addr { return f.addr }

// Close lets go of a release still out with the site. The screen has been
// discarded, so there is nothing here to draw the answer into — but the write
// itself is the site's now, which is why the flow says so on its way out rather
// than pretending the version was left alone.
func (f *Flow) Close() { f.stop() }

func (f *Flow) stop() {
	if f.cancel != nil {
		f.cancel()
		f.cancel = nil
	}
}

// Update handles one message.
func (f *Flow) Update(msg tea.Msg) (kernel.View, tea.Cmd) {
	var cmd tea.Cmd
	switch msg := msg.(type) {
	case kernel.SizeMsg:
		if msg.Width != f.width {
			f.rows.reset()
			f.head[0] = ""
		}
		f.width, f.height = msg.Width, msg.Height
		f.clampScroll()

	case kernel.ThemeMsg:
		f.deps.Theme = msg.Theme
		f.styles = newStyles(msg.Theme)
		f.rows.reset()
		f.head[0] = ""

	// r and R are the kernel's, and this screen has nothing of its own to
	// refetch: the count came in with it. Saying so beats a key that looks
	// answered and does nothing.
	case kernel.RefreshMsg:
		cmd = kernel.Status("what is open on " + f.version.Name +
			" was counted when this screen opened; go back and release it again to count it afresh")

	case releasedMsg:
		cmd = f.tookRelease(msg)

	case failedMsg:
		cmd = f.failed(msg)

	case tea.KeyPressMsg:
		cmd = f.key(msg)

	case tea.MouseClickMsg:
		cmd = f.click(msg)

	case tea.MouseWheelMsg:
		f.wheel(msg)
	}
	return f, cmd
}

// --- the rows -------------------------------------------------------------

// choice is one of the three things that can happen to the issues still open on
// a version.
type choice struct {
	policy jira.UnresolvedPolicy
	label  string
	note   string
	// refusal is why this session cannot offer the choice. It stays on the list
	// with the reason beside it, because a row that disappeared would be one
	// nobody could find out about.
	refusal string
	zone    string
}

// buildChoices draws the three decisions out once. A frame asks for a row at a
// time and every sentence in one of them is a string to build, so building them
// per frame would put that under every keystroke.
func (f *Flow) buildChoices() []choice {
	moving := choice{
		policy: jira.MoveUnresolved,
		label:  "Move them to another version",
		note:   plural(len(f.targets), "version to move them to", "versions to move them to"),
	}
	if len(f.targets) == 0 {
		moving.refusal = "no other unreleased version to move them to"
	}
	out := []choice{
		{
			policy: jira.ReleaseAnyway,
			label:  "Release " + f.version.Name + " anyway",
			note:   "the " + plural(f.open, "open issue", "open issues") + " stay on it",
		},
		moving,
		{
			policy: jira.StripUnresolved,
			label:  "Take " + f.version.Name + " off the open issues",
			note:   "they end up with no fix version",
		},
	}
	for i := range out {
		out[i].zone = "choice:" + strconv.Itoa(int(out[i].policy))
	}
	return out
}

func (f *Flow) rowCount() int {
	switch f.state {
	case flowChoosing:
		return len(f.choices)
	case flowPicking:
		return len(f.targets)
	case flowConfirming, flowWorking, flowStuck:
		return 0
	}
	return 0
}

// --- keys -------------------------------------------------------------------

func (f *Flow) key(msg tea.KeyPressMsg) tea.Cmd {
	switch f.acts[msg.String()] {
	case flowUp:
		f.moveTo(f.cursor - 1)
	case flowDown:
		f.moveTo(f.cursor + 1)
	case flowChoose:
		return f.choose()
	case flowConfirm:
		return f.release()
	case flowNone:
	}
	return nil
}

// choose takes the row under the cursor. Neither screen writes anything: the
// choices lead to the confirm and the confirm is the only thing that releases.
func (f *Flow) choose() tea.Cmd {
	switch f.state {
	case flowChoosing:
		if f.cursor < 0 || f.cursor >= len(f.choices) {
			return nil
		}
		row := f.choices[f.cursor]
		if row.refusal != "" {
			return kernel.Warn(row.refusal)
		}
		f.policy = row.policy
		if row.policy == jira.MoveUnresolved {
			f.state, f.cursor, f.top = flowPicking, 0, 0
			return nil
		}
		f.state = flowConfirming
		return nil
	case flowPicking:
		if f.cursor < 0 || f.cursor >= len(f.targets) {
			return nil
		}
		f.target = f.targets[f.cursor]
		f.state = flowConfirming
		return nil
	case flowStuck:
		// Starting again goes back to the decision and writes nothing. What the
		// site did with the last attempt is what the port re-reads when the
		// confirm is answered again.
		f.state, f.cursor, f.top = flowChoosing, 0, 0
		f.policy, f.target, f.failure = jira.ReleaseAnyway, jira.Version{}, nil
		if f.open == 0 {
			f.state = flowConfirming
		}
		return nil
	case flowConfirming, flowWorking:
	}
	return nil
}

// release is the write, and the only thing that reaches it is the confirm. A
// move with nowhere to move to is refused here as well as on the choice, because
// the port refuses it too and a refusal from the site would name a field rather
// than a screen.
func (f *Flow) release() tea.Cmd {
	if f.state != flowConfirming {
		return nil
	}
	if f.deps.Jira == nil {
		return kernel.Warn("there is no Jira connection in this session")
	}
	in := jira.ReleaseInput{Unresolved: f.policy}
	if f.policy == jira.MoveUnresolved {
		if f.target.ID == "" {
			return kernel.Warn("nothing has been chosen to move the open issues to")
		}
		in.MoveToVersionID = f.target.ID
	}
	f.stop()
	f.gen++
	ctx, cancel := context.WithCancel(context.Background())
	f.cancel = cancel
	f.state, f.failure = flowWorking, nil
	return kernel.Reply(
		withCancel(cancel, releaseOne(ctx, f.deps.Jira, f.version.ID, in, f.open, f.gen)),
		f.addr)
}

// tookRelease reads the answer rather than assuming it. The port hands back the
// version as the write answered it, with Unresolved holding what the release
// left open on it, and a move or a strip that did not reach every issue comes
// back released with a count still on it — which is a sweep that stopped part
// way and is reported as one.
func (f *Flow) tookRelease(msg releasedMsg) tea.Cmd {
	if msg.gen != f.gen || f.state != flowWorking {
		return nil
	}
	if !msg.version.Released {
		f.state = flowStuck
		f.failure = errors.New("the site answered without saying " + f.version.Name +
			" is released, so it may not be")
		return kernel.Fail(f.failure)
	}
	f.stop()
	left := 0
	if msg.version.Unresolved != nil {
		left = *msg.version.Unresolved
	}
	report := kernel.Status(f.version.Name + " released. " + f.outcome(msg.policy, msg.asked))
	if msg.policy != jira.ReleaseAnyway && left > 0 {
		report = kernel.Warn(f.version.Name + " was released, but " + strconv.Itoa(left) +
			" of the " + plural(msg.asked, "open issue", "open issues") +
			" still carry it: the " + sweepWord(msg.policy) + " did not finish")
	}
	return tea.Sequence(kernel.Pop(), kernel.Broadcast(msg), report)
}

// outcome is what the release did about the open issues, in the words the
// confirm used.
func (f *Flow) outcome(policy jira.UnresolvedPolicy, asked int) string {
	switch {
	case asked == 0:
		return "Nothing was open on it."
	case policy == jira.MoveUnresolved:
		return plural(asked, "open issue", "open issues") + " moved to " + f.target.Name + "."
	case policy == jira.StripUnresolved:
		return f.version.Name + " came off " + plural(asked, "open issue", "open issues") + "."
	default:
		return plural(asked, "open issue", "open issues") + " left on it."
	}
}

func sweepWord(policy jira.UnresolvedPolicy) string {
	if policy == jira.MoveUnresolved {
		return "move"
	}
	return "strip"
}

// failed keeps the refusal on the screen as well as on the status line, because
// a status line is gone by the next keypress and a release that did not happen
// has to keep saying so.
func (f *Flow) failed(msg failedMsg) tea.Cmd {
	if msg.gen != f.gen || f.state != flowWorking {
		return nil
	}
	f.stop()
	f.state, f.failure = flowStuck, msg.err
	return kernel.Fail(msg.err)
}

// --- selection --------------------------------------------------------------

func (f *Flow) moveTo(at int) {
	n := f.rowCount()
	if n == 0 {
		f.cursor, f.top = 0, 0
		return
	}
	f.cursor = min(max(at, 0), n-1)
	h := f.rowsHeight()
	if f.cursor < f.top {
		f.top = f.cursor
	}
	if f.cursor >= f.top+h {
		f.top = f.cursor - h + 1
	}
	f.clampScroll()
}

func (f *Flow) clampScroll() {
	f.top = min(max(f.top, 0), max(f.rowCount()-f.rowsHeight(), 0))
}

// --- mouse ------------------------------------------------------------------

// click selects a row, opens it on a second click of the same gesture, and
// answers the confirm where the confirm is what is on screen. The confirm takes
// one click because it is already the second screen: the row that led here was
// the first.
func (f *Flow) click(msg tea.MouseClickMsg) tea.Cmd {
	if msg.Button != tea.MouseLeft {
		return nil
	}
	if f.state == flowConfirming && f.zones.Hit(zoneConfirm, msg) {
		return f.release()
	}
	for i := f.top; i < min(f.top+f.rowsHeight(), f.rowCount()); i++ {
		id := f.zoneOf(i)
		if !f.zones.Hit(id, msg) {
			continue
		}
		f.moveTo(i)
		if f.clicks.Double(id) {
			return f.choose()
		}
		return nil
	}
	return nil
}

func (f *Flow) wheel(msg tea.MouseWheelMsg) {
	switch msg.Button {
	case tea.MouseWheelUp:
		f.top -= widget.WheelStep
	case tea.MouseWheelDown:
		f.top += widget.WheelStep
	default:
		return
	}
	f.clicks.Forget()
	f.clampScroll()
}
