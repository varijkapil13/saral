package release

import (
	"context"
	"errors"
	"slices"
	"strconv"
	"strings"

	"charm.land/bubbles/v2/textinput"
	tea "charm.land/bubbletea/v2"

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

const (
	// bulkCap is the most issues one assignment reads and writes. A query
	// matching more is refused rather than cut short: the preview has to be the
	// whole of what will change.
	bulkCap = 1000
	// bulkChunk is how many issues one command edits before the screen hears how
	// it went. There is no bulk edit in the port, so each is a write of its own.
	bulkChunk = 25
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
	next    int
	done    int
	failed  []bulkFailure
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

// bulkFailure is one issue the site refused, in its own words.
type bulkFailure struct {
	key    string
	reason string
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

func (b *Bulk) begin() (context.Context, int) {
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

// errTooMany is a query matching more than one assignment will write.
var errTooMany = &jira.ValidationError{Messages: []string{
	"the query matches more than " + strconv.Itoa(bulkCap) + " issues; narrow it and ask again",
}}

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

// readMatches runs the query for the one field the write turns on and splits
// what it matched into what will change and what already is the way it would
// be left.
func readMatches(ctx context.Context, s jira.Searcher, jql, versionID string, remove bool, gen int) tea.Cmd {
	return func() tea.Msg {
		page, err := s.Search(ctx, jira.Query{JQL: jql, Fields: []string{"summary", "fixVersions"}})
		if err != nil {
			return failedMsg{gen: gen, what: whatQuery, err: err}
		}
		var todo []jira.Issue
		skipped, seen := 0, 0
		for {
			for i := range page.Items {
				if seen++; seen > bulkCap {
					return failedMsg{gen: gen, what: whatQuery, err: errTooMany}
				}
				iss := page.Items[i]
				carries := slices.ContainsFunc(iss.FixVersions, func(v jira.Version) bool { return v.ID == versionID })
				if carries != remove {
					skipped++
					continue
				}
				todo = append(todo, iss)
			}
			if !page.HasMore() {
				return bulkReadMsg{gen: gen, todo: todo, skipped: skipped}
			}
			if page, err = page.Next(ctx); err != nil {
				return failedMsg{gen: gen, what: whatQuery, err: err}
			}
		}
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
	gen    int
	done   int
	failed []bulkFailure
	// stopped is a chunk that ended because its context did, with the keys it
	// never sent.
	stopped []string
}

func (b *Bulk) patch() jira.IssuePatch {
	if b.remove {
		return jira.IssuePatch{RemoveFixVersions: []string{b.version.ID}}
	}
	return jira.IssuePatch{AddFixVersions: []string{b.version.ID}}
}

func (b *Bulk) apply() tea.Cmd {
	if b.state != bulkPreview || len(b.todo) == 0 {
		return nil
	}
	if b.deps.Jira == nil {
		return kernel.Warn("there is no Jira connection in this session")
	}
	b.state, b.next, b.done = bulkWorking, 0, 0
	b.failed, b.pending = nil, nil
	return b.sendChunk()
}

func (b *Bulk) sendChunk() tea.Cmd {
	end := min(b.next+bulkChunk, len(b.todo))
	keys := make([]string, 0, end-b.next)
	for i := b.next; i < end; i++ {
		keys = append(keys, b.todo[i].Key)
	}
	b.next = end
	ctx, gen := b.begin()
	return b.reply(editChunk(ctx, b.deps.Jira, keys, b.patch(), gen))
}

// editChunk writes one chunk issue by issue. A refusal is that issue's and the
// rest of the chunk still goes; a context that ends stops the chunk where it is.
func editChunk(ctx context.Context, w jira.IssueWriter, keys []string, patch jira.IssuePatch, gen int) tea.Cmd {
	return func() tea.Msg {
		out := bulkChunkMsg{gen: gen}
		for i, key := range keys {
			if ctx.Err() != nil {
				out.stopped = slices.Clone(keys[i:])
				return out
			}
			err := w.UpdateIssue(ctx, key, patch)
			switch {
			case err == nil:
				out.done++
			case errors.Is(err, context.Canceled):
				out.stopped = slices.Clone(keys[i:])
				return out
			default:
				reason, _ := jira.Reason(err)
				out.failed = append(out.failed, bulkFailure{key: key, reason: reason})
			}
		}
		return out
	}
}

// tookChunk counts one chunk and sends the next. A chunk in which nothing at
// all landed stops the run: whatever refused every issue in it — a permission,
// a rate limit that outlasted the retries, a connection — will refuse the next
// chunk too, and the rest are reported as not sent rather than as refused.
func (b *Bulk) tookChunk(msg bulkChunkMsg) tea.Cmd {
	if msg.gen != b.gen || b.state != bulkWorking {
		return nil
	}
	b.done += msg.done
	b.failed = append(b.failed, msg.failed...)
	stuck := msg.done == 0 && len(msg.failed) > 0
	if len(msg.stopped) > 0 || stuck || b.next >= len(b.todo) {
		b.pending = append(b.pending, msg.stopped...)
		if b.next < len(b.todo) {
			for i := b.next; i < len(b.todo); i++ {
				b.pending = append(b.pending, b.todo[i].Key)
			}
			b.next = len(b.todo)
		}
		return b.finish()
	}
	return b.sendChunk()
}

func (b *Bulk) finish() tea.Cmd {
	b.stop()
	b.state = bulkDone
	b.items = b.items[:0]
	for _, f := range b.failed {
		b.items = append(b.items, bulkItem{key: f.key, text: widget.Sanitize(f.reason)})
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
