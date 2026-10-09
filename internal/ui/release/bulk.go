package release

import (
	"context"
	"strconv"
	"strings"

	"charm.land/bubbles/v2/textinput"
	tea "charm.land/bubbletea/v2"

	apprelease "github.com/varijkapil13/saral/internal/app/release"
	"github.com/varijkapil13/saral/internal/ui/kernel"
	"github.com/varijkapil13/saral/internal/ui/widget"
	"github.com/varijkapil13/saral/pkg/jira"
)

var (
	_ kernel.View        = (*Bulk)(nil)
	_ kernel.KeyCapturer = (*Bulk)(nil)
	_ kernel.Blocker     = (*Bulk)(nil)
	_ kernel.Closer      = (*Bulk)(nil)
	_ kernel.Addressed   = (*Bulk)(nil)
)

// bulkState is which of the screen's four steps is up. It doubles as the
// generation the footer repaints on.
type bulkState int

const (
	bulkQuery bulkState = iota
	bulkReading
	bulkPreview
	bulkWorking
	bulkDone
	bulkStates
)

// Bulk puts a version on the issues a query matches, or takes it off them.
//
// It is pushed over the versions list with the version under the cursor. The
// write is an add or a remove of that one version on each issue, never the
// issue's whole list, so a version somebody else put on an issue between the
// preview and the write survives it.
type Bulk struct {
	deps    kernel.Deps
	version jira.Version
	remove  bool
	input   textinput.Model

	state   bulkState
	failure error
	// todo are the issues the write will change, and skipped how many the query
	// matched that already are the way the write would leave them.
	todo    []jira.Issue
	skipped int
	run     *apprelease.Assignment
	done    int
	failed  []apprelease.Failure
	pending []string
	items   []bulkItem

	cursor, top   int
	width, height int

	acts   map[string]bulkAction
	gen    int
	cancel context.CancelFunc
	addr   kernel.Addr

	styles   *styles
	rows     *widget.RowCache[bulkRowKey, string]
	lines    []string
	chrome   [4]string
	chromeAt bulkChromeKey
	zones    widget.Zoner
}

// NewBulk builds the screen over one version.
func NewBulk(d kernel.Deps, v jira.Version) kernel.View {
	b := &Bulk{deps: d, version: v, addr: kernel.NewAddr()}
	if b.deps.Theme == nil {
		b.deps.Theme = kernel.NewTheme(kernel.ThemeAuto, true, kernel.UnicodeGlyphs())
	}
	b.input = widget.NewInput()
	b.input.Prompt = "> "
	b.input.SetValue(b.defaultQuery())
	b.input.CursorEnd()
	_ = b.input.Focus()
	b.acts = defaultBulkKeys().table()
	b.styles = newStyles(b.deps.Theme)
	b.rows = widget.NewRowCache[bulkRowKey, string](rowCacheLimit)
	b.zones = widget.NewZoner(d.Zones)
	return b
}

// defaultQuery is where an assignment starts: the session's project, or for a
// removal the issues that carry the version. Both are built from what the
// session knows, never from a word the site can rename.
func (b *Bulk) defaultQuery() string {
	if b.remove {
		return "fixVersion = " + b.version.ID
	}
	if p := strings.TrimSpace(b.deps.Project); p != "" {
		return "project = " + strconv.Quote(p)
	}
	return ""
}

// Init has nothing to read: the query is the reader's to write first.
func (b *Bulk) Init() tea.Cmd { return nil }

// Addr is where the reads and the chunks come back to.
func (b *Bulk) Addr() kernel.Addr { return b.addr }

// WantsRawKeys is true while the query is being typed, so a digit reaches the
// query and esc reaches the screen rather than the kernel.
func (b *Bulk) WantsRawKeys() bool { return b.state == bulkQuery }

// BlocksClose refuses to leave while chunks are still going to the site: the
// ones already sent have changed and the rest have not.
func (b *Bulk) BlocksClose() (string, bool) {
	if b.state != bulkWorking {
		return "", false
	}
	return strconv.Itoa(b.done+len(b.failed)) + " of " + strconv.Itoa(len(b.todo)) +
		" issues have been sent and the rest are still going", true
}

// Close lets go of whatever is in flight.
func (b *Bulk) Close() { b.stop() }

func (b *Bulk) stop() {
	if b.cancel != nil {
		b.cancel()
		b.cancel = nil
	}
}

func (b *Bulk) begin() (ctx context.Context, gen int) {
	b.stop()
	b.gen++
	ctx, cancel := context.WithCancel(context.Background())
	b.cancel = cancel
	return ctx, b.gen
}

func (b *Bulk) reply(cmd tea.Cmd) tea.Cmd { return kernel.Reply(withCancel(b.cancel, cmd), b.addr) }

// Update handles one message.
func (b *Bulk) Update(msg tea.Msg) (kernel.View, tea.Cmd) {
	var cmd tea.Cmd
	switch msg := msg.(type) {
	case kernel.SizeMsg:
		b.width, b.height = msg.Width, msg.Height
		b.input.SetWidth(max(msg.Width-inputChrome-2, 8))
		b.rows.Reset()
		b.clampScroll()
	case kernel.SetMouseMsg:
		b.rows.Reset()
		b.chrome = [4]string{}
	case kernel.ThemeMsg:
		b.deps.Theme = msg.Theme
		b.styles = newStyles(msg.Theme)
		b.rows.Reset()
		b.chrome = [4]string{}
	case kernel.RefreshMsg:
		if b.state == bulkPreview {
			cmd = b.read()
		}
	case bulkReadMsg:
		b.tookRead(msg)
	case bulkChunkMsg:
		cmd = b.tookChunk(msg)
	case failedMsg:
		b.failedRead(msg)
	case tea.KeyPressMsg:
		cmd = b.key(msg)
	case tea.MouseClickMsg:
		cmd = b.click(msg)
	case tea.MouseWheelMsg:
		b.wheel(msg)
	}
	return b, cmd
}

// --- reading ---------------------------------------------------------------

type bulkReadMsg struct {
	gen     int
	todo    []jira.Issue
	skipped int
}

const whatQuery = "The query could not be run."

func (b *Bulk) read() tea.Cmd {
	jql := strings.TrimSpace(b.input.Value())
	switch {
	case b.deps.Jira == nil:
		return kernel.Warn("there is no Jira connection in this session")
	case jql == "":
		return kernel.Warn("an assignment needs a query to say which issues")
	}
	ctx, gen := b.begin()
	b.state, b.failure = bulkReading, nil
	return b.reply(readMatches(ctx, b.deps.Jira, jql, b.version.ID, b.remove, gen))
}

func readMatches(ctx context.Context, s jira.Searcher, jql, versionID string, remove bool, gen int) tea.Cmd {
	return func() tea.Msg {
		got, err := apprelease.ReadMatches(ctx, s, jql, versionID, remove)
		if err != nil {
			return failedMsg{gen: gen, what: whatQuery, err: err}
		}
		return bulkReadMsg{gen: gen, todo: got.Todo, skipped: got.Skipped}
	}
}

func (b *Bulk) tookRead(msg bulkReadMsg) {
	if msg.gen != b.gen || b.state != bulkReading {
		return
	}
	b.stop()
	b.state, b.todo, b.skipped = bulkPreview, msg.todo, msg.skipped
	b.items = b.items[:0]
	for i := range msg.todo {
		b.items = append(b.items, bulkItem{key: msg.todo[i].Key, text: widget.Sanitize(msg.todo[i].Summary)})
	}
	b.cursor, b.top = 0, 0
	b.rows.Reset()
}

func (b *Bulk) failedRead(msg failedMsg) {
	if msg.gen != b.gen || b.state != bulkReading {
		return
	}
	b.stop()
	b.state, b.failure = bulkQuery, msg.err
	_ = b.input.Focus()
}

// --- writing ---------------------------------------------------------------

type bulkChunkMsg struct {
	gen      int
	progress apprelease.Progress
}

func (b *Bulk) apply() tea.Cmd {
	if b.state != bulkPreview || len(b.todo) == 0 {
		return nil
	}
	if b.deps.Jira == nil {
		return kernel.Warn("there is no Jira connection in this session")
	}
	keys := make([]string, len(b.todo))
	for i := range b.todo {
		keys[i] = b.todo[i].Key
	}
	b.run = apprelease.NewAssignment(b.deps.Jira, keys, apprelease.Patch(b.version.ID, b.remove))
	b.state, b.done = bulkWorking, 0
	b.failed, b.pending = nil, nil
	return b.sendChunk()
}

func (b *Bulk) sendChunk() tea.Cmd {
	ctx, gen := b.begin()
	run := b.run
	return b.reply(func() tea.Msg {
		return bulkChunkMsg{gen: gen, progress: run.Next(ctx)}
	})
}

func (b *Bulk) tookChunk(msg bulkChunkMsg) tea.Cmd {
	if msg.gen != b.gen || b.state != bulkWorking {
		return nil
	}
	p := msg.progress
	b.done, b.failed, b.pending = p.Done, p.Failed, p.Pending
	if p.Finished {
		return b.finish()
	}
	return b.sendChunk()
}

func (b *Bulk) finish() tea.Cmd {
	b.stop()
	b.state = bulkDone
	b.items = b.items[:0]
	for _, f := range b.failed {
		b.items = append(b.items, bulkItem{key: f.Key, text: widget.Sanitize(f.Reason)})
	}
	for _, key := range b.pending {
		b.items = append(b.items, bulkItem{key: key, text: "not sent"})
	}
	b.cursor, b.top = 0, 0
	b.rows.Reset()
	words := b.outcome()
	if len(b.failed) > 0 || len(b.pending) > 0 {
		return kernel.Warn(words)
	}
	return kernel.Status(words)
}

// outcome is the one sentence the run ends with, on the status line and at the
// head of the screen.
func (b *Bulk) outcome() string {
	name := widget.Sanitize(b.version.Name)
	var s strings.Builder
	if b.remove {
		s.WriteString(name + " came off " + strconv.Itoa(b.done) + " of " + plural(len(b.todo), "issue", "issues"))
	} else {
		s.WriteString(name + " is on " + strconv.Itoa(b.done) + " of " + plural(len(b.todo), "issue", "issues"))
	}
	if len(b.failed) > 0 {
		s.WriteString("; the site refused " + strconv.Itoa(len(b.failed)))
	}
	if len(b.pending) > 0 {
		s.WriteString("; " + strconv.Itoa(len(b.pending)) + " were not sent")
	}
	s.WriteString(".")
	return s.String()
}

// --- keys ------------------------------------------------------------------

func (b *Bulk) key(msg tea.KeyPressMsg) tea.Cmd {
	act := b.acts[msg.String()]
	switch b.state {
	case bulkQuery:
		switch act {
		case bulkRun:
			return b.read()
		case bulkToggle:
			b.toggle()
			return nil
		case bulkLeave:
			return kernel.Pop()
		case bulkNone, bulkUp, bulkDown, bulkPageUp, bulkPageDown, bulkApply, bulkEdit:
		}
		b.input, _ = b.input.Update(msg)
		b.failure = nil
		return nil
	case bulkPreview, bulkDone:
		switch act {
		case bulkApply:
			return b.apply()
		case bulkEdit:
			b.state, b.failure = bulkQuery, nil
			_ = b.input.Focus()
		case bulkUp:
			b.moveTo(b.cursor - 1)
		case bulkDown:
			b.moveTo(b.cursor + 1)
		case bulkPageUp:
			b.moveTo(b.cursor - b.rowsHeight())
		case bulkPageDown:
			b.moveTo(b.cursor + b.rowsHeight())
		case bulkNone, bulkRun, bulkToggle, bulkLeave:
		}
	case bulkReading, bulkWorking, bulkStates:
	}
	return nil
}

// toggle switches between putting the version on and taking it off. A query
// still as it was offered follows the switch; one the reader wrote is kept.
func (b *Bulk) toggle() {
	was := b.defaultQuery()
	b.remove = !b.remove
	if strings.TrimSpace(b.input.Value()) == strings.TrimSpace(was) {
		b.input.SetValue(b.defaultQuery())
		b.input.CursorEnd()
	}
}

// --- selection -------------------------------------------------------------

func (b *Bulk) moveTo(at int) {
	n := len(b.items)
	if n == 0 {
		b.cursor, b.top = 0, 0
		return
	}
	b.cursor = min(max(at, 0), n-1)
	h := b.rowsHeight()
	if b.cursor < b.top {
		b.top = b.cursor
	}
	if b.cursor >= b.top+h {
		b.top = b.cursor - h + 1
	}
	b.clampScroll()
}

func (b *Bulk) clampScroll() {
	b.top = min(max(b.top, 0), max(len(b.items)-b.rowsHeight(), 0))
}

func (b *Bulk) click(msg tea.MouseClickMsg) tea.Cmd {
	if msg.Button != tea.MouseLeft {
		return nil
	}
	switch {
	case b.state == bulkPreview && b.zones.Hit(zoneConfirm, msg):
		return b.apply()
	case b.state == bulkQuery && b.zones.Hit(bulkZoneToggle, msg):
		b.toggle()
	case b.state == bulkQuery && b.zones.Hit(bulkZoneRun, msg):
		return b.read()
	case (b.state == bulkPreview || b.state == bulkDone) && b.zones.Hit(bulkZoneEdit, msg):
		b.state, b.failure = bulkQuery, nil
		_ = b.input.Focus()
	}
	return nil
}

func (b *Bulk) wheel(msg tea.MouseWheelMsg) {
	switch msg.Button {
	case tea.MouseWheelUp:
		b.top -= widget.WheelStep
	case tea.MouseWheelDown:
		b.top += widget.WheelStep
	default:
		return
	}
	b.clampScroll()
}
