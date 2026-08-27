// Package comment is the comment thread on one issue: reading it, writing a
// comment, editing one and deleting one.
package comment

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"

	"charm.land/bubbles/v2/textarea"
	tea "charm.land/bubbletea/v2"

	"github.com/varijkapil13/saral/internal/ui/kernel"
	"github.com/varijkapil13/saral/internal/ui/widget"
	"github.com/varijkapil13/saral/pkg/adf"
	"github.com/varijkapil13/saral/pkg/jira"
)

// ViewID is the name this view registers itself under and the scope its keys
// are registered in.
const ViewID = "comment"

// lookahead is how close to the end of the loaded thread the cursor gets before
// the next page is asked for.
const lookahead = 5

// blockCacheLimit is how many rendered comments are kept: a screenful and its
// overscan, in both selected and unselected forms, several relayouts deep.
const blockCacheLimit = 256

// editorLines is the most rows of text the editor accepts. It is the widget's
// own ceiling; a comment longer than this is not something a terminal editor is
// the right place for.
const editorLines = 10000

// editorOptions is how a document is rendered for somebody to edit, and how the
// edited markdown is read back. The two must be the same value or reconciling
// the edit against the original matches nothing: a block that renders one way
// and parses another reads as a block the author rewrote.
//
// TableWidth is deliberately zero. A width-bounded render truncates a table's
// cells with an ellipsis, and an edit anywhere in that table would write the
// truncation back.
var editorOptions = adf.Options{}

// zones are the click targets this view marks. Each is prefixed per instance so
// that two of these views on one screen cannot answer for each other.
const (
	zoneWrite   = "write"
	zoneEdit    = "edit"
	zoneDelete  = "delete"
	zoneConfirm = "confirm"
	zoneRefuse  = "refuse"
	zoneSend    = "send"
	zoneCancel  = "cancel"
	zoneComment = "comment:"
)

// mode is what the view is doing. Only browsing lets the kernel have the keys.
type mode uint8

const (
	browsing mode = iota
	writing
	confirming
)

var (
	_ kernel.View        = (*Model)(nil)
	_ kernel.KeyCapturer = (*Model)(nil)
	_ kernel.Blocker     = (*Model)(nil)
	_ kernel.Addressed   = (*Model)(nil)
)

// ThreadMsg points the view at an issue's thread. It is exported so that a view
// holding an issue can retarget this one without holding a pointer to it.
type ThreadMsg struct{ Key string }

// WriteMsg opens the editor on a new comment.
type WriteMsg struct{}

// EditMsg opens the editor on the comment under the cursor.
type EditMsg struct{}

// DeleteMsg starts the confirmation for the comment under the cursor. It starts
// the confirmation and never the deletion: there is one path to removing a
// comment and it goes through the sentence naming what will go.
type DeleteMsg struct{}

// Model is the comment thread.
type Model struct {
	deps    kernel.Deps
	keys    keyMap
	browse  map[string]action
	confirm map[string]action
	styles  *styles
	blocks  *blocks
	drafts  *drafts

	issue    string
	comments []jira.Comment
	page     jira.Page[jira.Comment]
	loaded   bool
	loading  bool

	// cursor is the comment under the selection; top and skip are the first
	// comment drawn and how many of its lines are above the window. Scrolling
	// is tracked this way so that drawing a screen never needs the height of a
	// comment above it — a thread of a thousand costs the same as a thread of
	// ten.
	cursor int
	top    int
	skip   int

	mode      mode
	editor    textarea.Model
	editing   string
	original  adf.Doc
	pending   jira.Comment
	prompt    []string
	sending   bool
	pendingGo bool

	// composerLines is how many lines the draft occupies at the width the
	// composer has, which is what its height is derived from. It is recomputed
	// when the text or the width moves rather than per frame.
	composerLines int

	// pan is how far right the thread is scrolled. It is only ever non-zero
	// because a line is wider than the box: code is never wrapped and a grid is
	// never cut, so panning is how the rest of one is reached.
	pan int

	// draft is the text last written to disk, so that a keystroke that changes
	// nothing does not rewrite the file.
	draft       string
	draftFailed bool

	lines    []string
	head     []string
	headAt   headKey
	chrome   string
	chromeAt chromeKey

	width, height int
	gen           int
	cancel        context.CancelFunc

	// addr is where this thread's own answers come back to, and holder is the
	// view drawing it when that view is a pane rather than the kernel. A thread
	// in a sidebar is not on the stack, so its answer reaches it through the
	// pane, which forwards everything it does not know.
	addr   kernel.Addr
	holder kernel.Addr

	zones  widget.Zoner
	clicks *widget.Clicks
}

// New builds the thread with no issue yet. It is the registry's constructor:
// the view is reachable by name and from the palette, and is told which issue
// it is about by a ThreadMsg.
func New(d kernel.Deps) kernel.View { return build(d, "") }

// Addr is where the kernel delivers what this thread asked the site for,
// whatever has since been pushed over it.
func (m *Model) Addr() kernel.Addr { return m.addr }

// Thread builds the thread for one issue, which is how a view holding an issue
// opens it — pushed over that view, or held as a child model inside it.
//
// A pane that embeds one owes it four things: the kernel.SizeMsg for the box it
// has been given, every message the pane is handed, a kernel.ThreadMsg when the
// issue changes, and its own answers to WantsRawKeys, BlocksClose and LiveKeys.
// Everything else — one fetch, one draft, one cursor, one composer with the text
// still in it — follows from there being one of these however many boxes it is
// drawn in.
// held names the view drawing this one when a pane embeds it instead of
// pushing it. The kernel cannot see a model inside another, so an answer is
// addressed to this thread first and to its holder after, and the holder hands
// it on.
func Thread(d kernel.Deps, key string, held ...kernel.Addr) *Model {
	m := build(d, key)
	if len(held) > 0 {
		m.holder = held[0]
	}
	return m
}

// Push returns the command that opens the thread for one issue on top of
// whatever is on screen.
func Push(d kernel.Deps, key string) tea.Cmd {
	return kernel.Push(ViewID, key, Thread(d, key))
}

func build(d kernel.Deps, key string) *Model {
	m := &Model{
		deps:   d,
		keys:   defaultKeys(),
		blocks: newBlocks(blockCacheLimit),
		drafts: openDrafts(),
		issue:  strings.TrimSpace(key),
		editor: newEditor(),
		addr:   kernel.NewAddr(),
	}
	if m.deps.Theme == nil {
		m.deps.Theme = kernel.NewTheme(kernel.ThemeAuto, true, kernel.UnicodeGlyphs())
	}
	m.styles = newStyles(m.deps.Theme)
	m.zones = widget.NewZoner(d.Zones)
	m.clicks = widget.NewClicks(d.Now)
	m.browse, m.confirm = m.keys.tables()
	return m
}

func newEditor() textarea.Model {
	ta := widget.NewArea()
	ta.Prompt = ""
	ta.ShowLineNumbers = false
	ta.Placeholder = "Markdown. Tables, lists, quotes and code fences all survive the round trip."
	// The widget sizes itself to the rows the draft actually occupies, which is
	// what the composer's height is derived from: it soft-wraps on words, so
	// counting cells and dividing by the width is a line out at every width but
	// one. MaxHeight is then the display ceiling and MaxContentHeight the
	// document's, which is why the two are not the same number.
	ta.DynamicHeight = true
	ta.MinHeight = 1
	ta.MaxContentHeight = editorLines
	// The widget's own cursor blink is a timer this view would then own for as
	// long as the editor is open; dropping it costs a blinking block and keeps
	// every frame reproducible.
	ta.SetVirtualCursor(false)
	return ta
}

// WantsRawKeys is true while the editor is open and while a delete is waiting
// to be confirmed. Without it the kernel matches its own bindings first, so a
// comment loses every q and esc it is typed with, and the key that answers a
// confirmation would quit the program instead.
func (m *Model) WantsRawKeys() bool { return m.mode != browsing }

// BlocksClose refuses to let the view be discarded while it holds text nobody
// has sent. The draft is on disk either way; the sentence is what stops a
// click on the footer from looking like the text was thrown away.
func (m *Model) BlocksClose() (string, bool) {
	if m.mode == writing && strings.TrimSpace(m.editor.Value()) != "" {
		return "this comment has not been sent — ctrl+s sends it, esc keeps it as a draft", true
	}
	return "", false
}

// Init reads the thread, and only when there is one to read. A pane that embeds
// this builds it before an issue may be known, and asking again for a thread
// already in hand would throw the reader's place away.
func (m *Model) Init() tea.Cmd {
	if m.issue == "" || m.loaded || m.loading {
		return nil
	}
	return m.load()
}

// Update handles one message.
func (m *Model) Update(msg tea.Msg) (kernel.View, tea.Cmd) {
	var cmd tea.Cmd
	switch msg := msg.(type) {
	case kernel.SizeMsg:
		cmd = m.resize(msg.Width, msg.Height)

	case kernel.FocusMsg:
		m.focus(msg.Focused)

	case kernel.ThemeMsg:
		m.deps.Theme = msg.Theme
		m.styles = newStyles(msg.Theme)
		m.blocks.reset()
		m.reprompt()

	case kernel.CapabilitiesMsg:
		m.deps.Caps = msg.Caps
		m.blocks.reset()
		m.reprompt()

	case kernel.RefreshMsg:
		cmd = m.load()

	case ThreadMsg:
		cmd = m.retarget(msg.Key)

	case WriteMsg:
		cmd = m.openEditor("", jira.Comment{})

	case EditMsg:
		cmd = m.editSelected()

	case DeleteMsg:
		cmd = m.askToDelete()

	case loadedMsg:
		cmd = m.loadedPage(msg)

	case pagedMsg:
		cmd = m.nextPage(msg)

	case savedMsg:
		cmd = m.saved(msg)

	case deletedMsg:
		cmd = m.deleted(msg)

	case failedMsg:
		cmd = m.failed(msg)

	case tea.KeyPressMsg:
		cmd = m.key(msg)

	case tea.MouseClickMsg:
		cmd = m.click(msg)

	case tea.MouseWheelMsg:
		cmd = m.wheel(msg)
	}
	return m, cmd
}

func (m *Model) location() *time.Location { return m.deps.Caps.Location() }

// resize takes the box the pane has been given. The width is what every memo is
// keyed on, so a divider moving, a terminal resizing and this same thread going
// from a sidebar to the whole screen all arrive here and all invalidate the same
// things; the height only moves where the lines go.
func (m *Model) resize(w, h int) tea.Cmd {
	if w == m.width && h == m.height {
		return nil
	}
	if w != m.width {
		m.blocks.reset()
	}
	m.width, m.height = w, h
	m.relayout()
	m.reprompt()
	return m.pageAheadIfNeeded()
}

// focus keeps the cursor out of a composer nobody is typing into. It does not
// let go of a request, because losing the keys is not being closed: a thread in
// a sidebar loses them whenever the pane beside it takes them, and the kernel
// blurs a view it is pushing over or switching away from as well as one it is
// discarding. Each request cancels its own context when it finishes, so the one
// case that is a discard costs a read that lands nowhere rather than a goroutine
// that stays.
func (m *Model) focus(on bool) {
	if on && m.mode == writing {
		_ = m.editor.Focus()
		return
	}
	if !on {
		m.editor.Blur()
	}
}

func (m *Model) retarget(key string) tea.Cmd {
	key = strings.TrimSpace(key)
	switch key {
	case "":
		return nil
	case m.issue:
		// A pane that embeds this names the issue it was built with, and a
		// thread that has read nothing yet has to take that as the cue.
		return m.Init()
	}
	m.issue = key
	m.comments, m.page = nil, jira.Page[jira.Comment]{}
	m.cursor, m.top, m.skip = 0, 0, 0
	m.loaded = false
	m.mode, m.editing, m.sending = browsing, "", false
	m.editor.Reset()
	m.blocks.reset()
	return m.load()
}

// --- fetching ---------------------------------------------------------------

func (m *Model) begin() (ctx context.Context, gen int) {
	m.stop()
	m.gen++
	ctx, cancel := context.WithCancel(context.Background())
	m.cancel = cancel
	return ctx, m.gen
}

func (m *Model) stop() {
	if m.cancel != nil {
		m.cancel()
		m.cancel = nil
	}
	m.loading = false
}

// reply puts this thread's address on a command, and its holder's after, so the
// answer comes back here rather than to whatever the stack has on top by then.
func (m *Model) reply(cmd tea.Cmd) tea.Cmd {
	return kernel.Reply(withCancel(m.cancel, cmd), m.addr, m.holder)
}

// Close lets go of the read, the paging and the send. A thread the kernel has
// discarded is one nothing can draw the answer into; a pane that embeds one is
// lending it instead, so this is not reached by esc coming back from the whole
// screen.
func (m *Model) Close() { m.stop() }

func (m *Model) current(gen int) bool { return gen == m.gen }

func (m *Model) load() tea.Cmd {
	if m.deps.Jira == nil || m.issue == "" {
		return nil
	}
	ctx, gen := m.begin()
	m.loading = true
	return m.reply(load(ctx, m.deps.Jira, m.issue, gen))
}

func (m *Model) loadedPage(msg loadedMsg) tea.Cmd {
	if !m.current(msg.gen) {
		return nil
	}
	// A refresh must not throw the reader's place away: docs/UX.md principle 5.
	// A first read has nothing under the cursor, so it lands on the newest.
	under := ""
	if c := m.selected(); c != nil {
		under = c.ID
	}
	m.loading, m.loaded = false, true
	m.comments = slices.Clone(msg.page.Items)
	m.page = msg.page
	m.blocks.reset()
	m.cursor = max(len(m.comments)-1, 0)
	if under != "" {
		if at := slices.IndexFunc(m.comments, func(c jira.Comment) bool { return c.ID == under }); at >= 0 {
			m.cursor = at
		}
	}
	if m.cursor == len(m.comments)-1 {
		m.scrollToBottom()
	} else {
		m.top, m.skip = m.cursor, 0
	}
	return m.pageAheadIfNeeded()
}

func (m *Model) nextPage(msg pagedMsg) tea.Cmd {
	if !m.current(msg.gen) {
		return nil
	}
	m.loading = false
	m.comments = append(m.comments, msg.page.Items...)
	m.page = msg.page
	return m.pageAheadIfNeeded()
}

// pageAheadIfNeeded asks for the page after the one in hand when the cursor is
// near the end of what is loaded. One request is in flight at a time, whatever
// the cursor does.
func (m *Model) pageAheadIfNeeded() tea.Cmd {
	if m.loading || m.sending || !m.page.HasMore() {
		return nil
	}
	if m.cursor < len(m.comments)-lookahead {
		return nil
	}
	ctx, gen := m.begin()
	m.loading = true
	return m.reply(more(ctx, m.page, gen))
}

func (m *Model) failed(msg failedMsg) tea.Cmd {
	if !m.current(msg.gen) {
		return nil
	}
	m.loading, m.sending = false, false
	return kernel.Fail(msg.err)
}

// --- writing ----------------------------------------------------------------

func (m *Model) selected() *jira.Comment {
	if m.cursor < 0 || m.cursor >= len(m.comments) {
		return nil
	}
	return &m.comments[m.cursor]
}

func (m *Model) draftKey() draftKey {
	return draftKey{site: m.deps.Site, issue: m.issue, comment: m.editing}
}

// openEditor puts the editor in front of the user, seeded with whatever is
// already theirs: a draft they left, or the comment they are editing rendered
// back into markdown.
func (m *Model) openEditor(id string, c jira.Comment) tea.Cmd {
	if m.issue == "" {
		return kernel.Warn("open an issue first: a comment needs something to be about")
	}
	m.mode, m.editing, m.pending = writing, id, c
	m.original = c.Body
	m.sending = false

	seeded := adf.MarkdownWith(c.Body, editorOptions)
	restored := m.drafts.read(m.draftKey())
	text := seeded
	if restored != "" {
		text = restored
	}
	m.draft = text
	m.editor.SetValue(text)
	m.editor.MoveToEnd()
	m.relayout()
	_ = m.editor.Focus()

	switch {
	case restored != "" && restored != seeded:
		return kernel.Warn("this is the draft you left, not what the site holds")
	case id != "":
		if lost := oneWay(c.Body); len(lost) > 0 {
			return kernel.Warn("the " + list(lost) + " in this comment survive only in the parts you leave alone")
		}
	}
	return nil
}

func (m *Model) editSelected() tea.Cmd {
	c := m.selected()
	if c == nil {
		return kernel.Warn("there is no comment here to edit")
	}
	return m.openEditor(c.ID, *c)
}

// closeEditor puts the editor away and keeps what was typed, which is the whole
// point of a draft: esc is not a way to lose an afternoon's wording.
func (m *Model) closeEditor() tea.Cmd {
	cmd := m.keepDraft(m.editor.Value())
	m.mode, m.editing, m.sending = browsing, "", false
	m.editor.Blur()
	m.editor.Reset()
	m.draft = ""
	m.relayout()
	return cmd
}

// keepDraft writes the text to disk when it has changed. It runs on the
// keystroke rather than on the way out, because docs/UX.md principle 6 counts a
// crash among the things text has to survive.
func (m *Model) keepDraft(text string) tea.Cmd {
	if text == m.draft {
		return nil
	}
	m.draft = text
	var err error
	if strings.TrimSpace(text) == "" {
		m.drafts.discard(m.draftKey())
	} else {
		err = m.drafts.write(m.draftKey(), text)
	}
	if err == nil || m.draftFailed {
		return nil
	}
	// Said once: a warning on every keystroke would bury whatever came before it.
	m.draftFailed = true
	return kernel.Warn("this draft is not being kept on disk: " + err.Error())
}

// send turns the markdown back into a document and writes it.
//
// A new comment has no original to reconcile against, so it is parsed on its
// own. An edit goes through ParseMarkdownInto, which reuses the original node
// for every block the author did not touch — the only way an account id behind
// a mention, a lozenge's colour or a node type this client has never heard of
// survives being edited.
func (m *Model) send() tea.Cmd {
	if m.deps.Jira == nil || m.sending {
		return nil
	}
	text := m.editor.Value()
	if strings.TrimSpace(text) == "" {
		return kernel.Warn("there is nothing here to send yet")
	}
	var (
		body adf.Doc
		err  error
	)
	if m.editing == "" {
		body, err = adf.ParseMarkdown(text)
	} else {
		body, err = adf.ParseMarkdownInto(m.original, text, editorOptions)
	}
	if err != nil {
		return kernel.Fail(unreadable(err))
	}
	m.sending = true
	ctx, gen := m.begin()
	if m.editing == "" {
		return m.reply(add(ctx, m.deps.Jira, m.issue, body, gen))
	}
	return m.reply(edit(ctx, m.deps.Jira, m.issue, m.editing, body, gen))
}

// unreadable turns a parse failure into the sentence a user reads: which line
// stopped it, and what about that line.
func unreadable(err error) error {
	var pe *adf.ParseError
	if errors.As(err, &pe) {
		return fmt.Errorf("line %d: %w", pe.Line, pe.Err)
	}
	return err
}

func (m *Model) saved(msg savedMsg) tea.Cmd {
	if !m.current(msg.gen) {
		return nil
	}
	m.sending = false
	m.drafts.discard(m.draftKey())
	m.draft = ""
	m.mode, m.editing = browsing, ""
	m.editor.Blur()
	m.editor.Reset()
	m.relayout()

	if msg.edited {
		at := slices.IndexFunc(m.comments, func(c jira.Comment) bool { return c.ID == msg.comment.ID })
		if at >= 0 {
			m.comments[at] = msg.comment
			m.cursor = at
		}
	} else {
		m.comments = append(m.comments, msg.comment)
		m.cursor = len(m.comments) - 1
	}
	m.blocks.reset()
	m.ensureVisible()
	if msg.edited {
		return kernel.Status("the comment has been changed")
	}
	return kernel.Status("the comment has been added")
}

// --- deleting ---------------------------------------------------------------

// askToDelete puts the named confirmation up. Nothing else in this view calls
// remove, so there is no path from a keystroke to a deleted comment that skips
// the sentence saying which comment it is.
func (m *Model) askToDelete() tea.Cmd {
	c := m.selected()
	if c == nil {
		return kernel.Warn("there is no comment here to delete")
	}
	m.mode, m.pending = confirming, *c
	m.prompt = m.buildPrompt()
	m.ensureVisible()
	return nil
}

func (m *Model) confirmDelete() tea.Cmd {
	id := m.pending.ID
	m.mode, m.prompt = browsing, nil
	if m.deps.Jira == nil || id == "" {
		return nil
	}
	ctx, gen := m.begin()
	m.loading = true
	return m.reply(remove(ctx, m.deps.Jira, m.issue, id, gen))
}

func (m *Model) deleted(msg deletedMsg) tea.Cmd {
	if !m.current(msg.gen) {
		return nil
	}
	m.loading = false
	at := slices.IndexFunc(m.comments, func(c jira.Comment) bool { return c.ID == msg.id })
	if at >= 0 {
		m.comments = slices.Delete(m.comments, at, at+1)
	}
	m.blocks.reset()
	m.cursor = min(m.cursor, max(len(m.comments)-1, 0))
	m.top = min(m.top, m.cursor)
	m.skip = 0
	m.ensureVisible()
	return kernel.Status("the comment is gone")
}

// --- moving -----------------------------------------------------------------

// layout is how the box's lines are divided. head, thread, prompt and composer
// add up to exactly the height the pane was given, at every height, which is
// what makes a 34-column sidebar and a whole screen the same code rather than
// two. chrome and editor are how the composer divides its own share.
type layout struct {
	head     int
	thread   int
	prompt   int
	composer int
	chrome   int
	editor   int
}

// layout divides the box. The thread is on top and the composer under it,
// because a comment is not written on a screen that hides what it is replying
// to: the composer takes 1+lines — the draft and the one row saying what it is
// and how to send it — with a floor of three so there is somewhere to start
// typing, and a ceiling of half the box so that the thread never goes away.
func (m *Model) layout() layout {
	lay := m.divide(min(2, m.height))
	if lay.thread == 0 && lay.head > 1 {
		// The rule under the identity line is the last thing worth a row: what
		// is being replied to earns one before the line under its name does.
		lay = m.divide(1)
	}
	return lay
}

func (m *Model) divide(head int) layout {
	lay := layout{head: head}
	rest := m.height - head
	if m.mode == confirming {
		lay.prompt = min(len(m.prompt), rest)
		rest -= lay.prompt
	}
	if m.mode == writing {
		lay.composer = min(composerHeight(m.composerLines, m.height), rest)
		// One line of the thread is worth more than the composer's chrome row;
		// below that there is nothing left to give.
		if rest-lay.composer == 0 && rest >= 2 {
			lay.composer = rest - 1
		}
		rest -= lay.composer
		if lay.composer >= 2 {
			lay.chrome = 1
		}
		lay.editor = lay.composer - lay.chrome
	}
	lay.thread = max(rest, 0)
	return lay
}

func composerHeight(lines, boxH int) int {
	return min(max(1+lines, 3), max(3, boxH/2))
}

// composerRows is the most rows the draft itself may take in a box this tall:
// the composer's ceiling, less the row of chrome above it.
func composerRows(boxH int) int { return max(composerHeight(editorLines, boxH)-1, 1) }

func (m *Model) threadHeight() int { return max(m.layout().thread, 1) }

// relayout re-derives everything a change of box or of draft moves: how many
// rows the draft occupies at this width, how far the thread may be panned, and
// where its window sits.
func (m *Model) relayout() {
	m.editor.MaxHeight = composerRows(m.height)
	m.editor.SetWidth(max(m.width, minBody))
	m.composerLines = m.editor.Height()
	// A box too short for the composer's own ceiling gives it fewer rows than
	// the draft asked for. The widget is held to what will actually be drawn, or
	// the cursor sits on a row nothing puts on screen.
	if rows := m.layout().editor; rows >= 1 && rows < m.editor.Height() {
		m.editor.SetHeight(rows)
		m.composerLines = m.editor.Height()
	}
	m.clampPan()
	m.ensureVisible()
}

// reprompt rebuilds the delete confirmation, which is laid out for the box it is
// drawn in rather than cut to fit it.
func (m *Model) reprompt() {
	if m.mode != confirming || m.width <= 0 {
		return
	}
	m.prompt = m.buildPrompt()
}

// blockAt lays one comment out at the width in hand, memoized on everything its
// lines depend on.
func (m *Model) blockAt(at int) *block {
	if at < 0 || at >= len(m.comments) {
		return nil
	}
	c := &m.comments[at]
	k := blockKey{
		id: c.ID, updated: c.Updated.UnixNano(), width: m.width,
		gen: m.styles.gen, selected: at == m.cursor,
	}
	if made, ok := m.blocks.get(k); ok {
		return made
	}
	made := renderBlock(c, m.width, at == m.cursor, m.styles, m.deps.Theme, m.location())
	m.blocks.put(k, made)
	return made
}

// blockHeight is how many lines a comment takes, which is all the scrolling
// needs to know. Asking for the lines themselves would mark a mouse zone around
// a block nothing is about to draw.
func (m *Model) blockHeight(at int) int {
	b := m.blockAt(at)
	if b == nil {
		return 0
	}
	// The blank line between one comment and the next is drawn outside the
	// block, so that it falls outside the zone.
	return len(b.lines) + 1
}

func (m *Model) blockLines(at int) []string {
	b := m.blockAt(at)
	if b == nil {
		return nil
	}
	return b.window(m.pan, m.deps.Theme.Glyphs.Ellipsis, m.zones, zoneComment+m.comments[at].ID)
}

func (m *Model) moveTo(at int) tea.Cmd {
	if len(m.comments) == 0 {
		m.cursor, m.top, m.skip = 0, 0, 0
		return nil
	}
	next := min(max(at, 0), len(m.comments)-1)
	if next == m.cursor {
		return m.pageAheadIfNeeded()
	}
	m.cursor = next
	m.ensureVisible()
	return m.pageAheadIfNeeded()
}

// jumpTo puts the cursor on a comment and the window with it, without walking
// what is in between: g g and G are the two moves whose distance is unbounded.
func (m *Model) jumpTo(at int) tea.Cmd {
	if len(m.comments) == 0 {
		return nil
	}
	m.cursor = min(max(at, 0), len(m.comments)-1)
	if m.cursor == len(m.comments)-1 {
		m.scrollToBottom()
	} else {
		m.top, m.skip = m.cursor, 0
	}
	return m.pageAheadIfNeeded()
}

// scrollToBottom puts the end of the thread at the bottom of the window, which
// is where a conversation is read from. It walks backwards from the newest
// comment and stops as soon as the window is full, so it never touches a
// comment nobody can see.
func (m *Model) scrollToBottom() {
	h := m.threadHeight()
	used, at := 0, len(m.comments)-1
	for at >= 0 {
		n := m.blockHeight(at)
		if used+n > h {
			break
		}
		used += n
		at--
	}
	switch {
	case at < 0:
		m.top, m.skip = 0, 0
	case used == 0:
		// One comment taller than the whole window: show it from the top,
		// because its first line is the one that says whose it is.
		m.top, m.skip = at, 0
	default:
		m.top = at
		m.skip = max(m.blockHeight(at)-(h-used), 0)
	}
}

// ensureVisible brings the cursor into the window by moving the window, and
// only ever looks at the comments between the top and the cursor.
func (m *Model) ensureVisible() {
	if m.cursor < m.top {
		m.top, m.skip = m.cursor, 0
		return
	}
	h := m.threadHeight()
	for m.top < m.cursor {
		used := 0
		for i := m.top; i <= m.cursor; i++ {
			n := m.blockHeight(i)
			if i == m.top {
				n -= m.skip
			}
			used += n
		}
		if used <= h {
			return
		}
		m.top++
		m.skip = 0
	}
	m.skip = min(m.skip, max(m.blockHeight(m.top)-1, 0))
}

// pageStep is how far a page key moves: as many comments as the window holds,
// counted from where the cursor is rather than from the top.
func (m *Model) pageStep(dir int) int {
	h, used, at := m.threadHeight(), 0, m.cursor
	for {
		next := at + dir
		if next < 0 || next >= len(m.comments) {
			return at
		}
		used += m.blockHeight(next)
		if used > h && next != m.cursor+dir {
			return at
		}
		at = next
		if used >= h {
			return at
		}
	}
}

// scroll moves the window by lines without moving the selection, which is what
// a wheel does everywhere else.
func (m *Model) scroll(delta int) {
	for ; delta > 0; delta-- {
		if m.skip+1 < m.blockHeight(m.top) {
			m.skip++
			continue
		}
		if m.top+1 >= len(m.comments) {
			return
		}
		m.top, m.skip = m.top+1, 0
	}
	for ; delta < 0; delta++ {
		if m.skip > 0 {
			m.skip--
			continue
		}
		if m.top == 0 {
			return
		}
		m.top--
		m.skip = max(m.blockHeight(m.top)-1, 0)
	}
}

// panBy moves the window sideways. Half the box at a time, so that the step is
// the same gesture whatever the box is: a 34-column sidebar and a 120-column
// screen both take two presses to move a screenful.
func (m *Model) panBy(dir int) {
	m.pan = max(m.pan+dir*max(m.width/2, 1), 0)
	m.clampPan()
}

// clampPan holds the pan against the widest line the window is actually showing,
// so that scrolling off the code block that was being read brings the thread
// back to its left edge rather than leaving the prose shifted out of the box.
func (m *Model) clampPan() {
	if m.pan == 0 {
		return
	}
	m.pan = min(m.pan, max(m.widestVisible()-m.width, 0))
}

// widestVisible is the widest line in the window, which is as far as the pane
// can usefully be panned.
func (m *Model) widestVisible() int {
	h, used, wide := m.threadHeight(), -m.skip, m.width
	for i := m.top; i < len(m.comments) && used < h; i++ {
		b := m.blockAt(i)
		if b == nil {
			break
		}
		wide = max(wide, b.wide)
		used += len(b.lines) + 1
	}
	return wide
}

// --- input ------------------------------------------------------------------

func (m *Model) key(msg tea.KeyPressMsg) tea.Cmd {
	switch m.mode {
	case writing:
		return m.editorKey(msg)
	case confirming:
		return m.confirmKey(msg.String())
	case browsing:
	}
	return m.browseKey(msg.String())
}

func (m *Model) browseKey(stroke string) tea.Cmd {
	if m.pendingGo {
		m.pendingGo = false
		switch stroke {
		case "g":
			return m.jumpTo(0)
		case "e":
			return m.jumpTo(len(m.comments) - 1)
		}
	}
	switch m.browse[stroke] {
	case actDown:
		return m.moveTo(m.cursor + 1)
	case actUp:
		return m.moveTo(m.cursor - 1)
	case actPageDown:
		return m.moveTo(m.pageStep(1))
	case actPageUp:
		return m.moveTo(m.pageStep(-1))
	case actHalfDown:
		m.scroll(m.threadHeight() / 2)
	case actHalfUp:
		m.scroll(-m.threadHeight() / 2)
	case actPanRight:
		m.panBy(1)
	case actPanLeft:
		m.panBy(-1)
	case actTop:
		return m.jumpTo(0)
	case actBottom:
		return m.jumpTo(len(m.comments) - 1)
	case actGo:
		m.pendingGo = true
	case actWrite:
		return m.openEditor("", jira.Comment{})
	case actEdit:
		return m.editSelected()
	case actDelete:
		return m.askToDelete()
	case actNone, actSend, actCancel, actConfirm:
	}
	return nil
}

func (m *Model) editorKey(msg tea.KeyPressMsg) tea.Cmd {
	switch {
	case kernel.Matches(msg, m.keys.Send):
		return m.send()
	case kernel.Matches(msg, m.keys.Cancel):
		return m.closeEditor()
	}
	if m.sending {
		return nil
	}
	var cmd tea.Cmd
	m.editor, cmd = m.editor.Update(msg)
	// The composer grows with the draft, and the thread above it gives up the
	// lines: the layout is re-derived on the keystroke rather than per frame.
	m.relayout()
	return tea.Batch(cmd, m.keepDraft(m.editor.Value()))
}

// confirmKey answers the delete confirmation. Only the key the prompt names
// goes ahead; every other key keeps the comment, so there is no mode to guess a
// way out of.
func (m *Model) confirmKey(stroke string) tea.Cmd {
	if m.confirm[stroke] == actConfirm {
		return m.confirmDelete()
	}
	m.mode, m.prompt = browsing, nil
	return nil
}

func (m *Model) click(msg tea.MouseClickMsg) tea.Cmd {
	if msg.Button != tea.MouseLeft {
		return nil
	}
	hit := func(name string) bool { return m.zones.Hit(name, msg) }
	switch m.mode {
	case confirming:
		switch {
		case hit(zoneConfirm):
			return m.confirmDelete()
		case hit(zoneRefuse):
			m.mode = browsing
		}
		return nil
	case writing:
		switch {
		case hit(zoneSend):
			return m.send()
		case hit(zoneCancel):
			return m.closeEditor()
		}
		return nil
	case browsing:
	}
	switch {
	case hit(zoneWrite):
		return m.openEditor("", jira.Comment{})
	case hit(zoneEdit):
		return m.editSelected()
	case hit(zoneDelete):
		return m.askToDelete()
	}
	for i := m.top; i < len(m.comments); i++ {
		id := zoneComment + m.comments[i].ID
		if !hit(id) {
			continue
		}
		if m.clicks.Double(id) {
			m.cursor = i
			return m.editSelected()
		}
		return m.moveTo(i)
	}
	return nil
}

// wheel scrolls the thread, including while a comment is being written: the
// thread is on screen then, and re-reading what is being replied to is the
// reason it is.
func (m *Model) wheel(msg tea.MouseWheelMsg) tea.Cmd {
	if m.mode == confirming {
		return nil
	}
	switch msg.Button {
	case tea.MouseWheelUp:
		m.scroll(-3)
	case tea.MouseWheelDown:
		m.scroll(3)
	default:
	}
	return nil
}

// --- rendering --------------------------------------------------------------

func (m *Model) mark(name, s string) string { return m.zones.Mark(name, s) }

// View draws the box it was given, in exactly as many lines as it was given:
// the thread, then whatever the thread is answering — a composer, or the
// sentence naming a comment about to go. It draws the window and nothing else,
// so a thread of a thousand comments and a thread of ten do the same work here,
// and the same instance draws a sidebar and a whole screen with no second
// layout.
func (m *Model) View() string {
	if m.width <= 0 || m.height <= 0 {
		return ""
	}
	m.clampPan()
	lay := m.layout()
	lines := m.lines[:0]
	lines = append(lines, m.headLines(lay.head)...)
	lines = m.appendThread(lines, lay.thread)
	lines = append(lines, m.prompt[:lay.prompt]...)
	lines = m.appendComposer(lines, lay)
	m.lines = lines
	return strings.Join(lines, "\n")
}

func (m *Model) appendThread(lines []string, h int) []string {
	if h <= 0 {
		return lines
	}
	if len(m.comments) == 0 {
		return m.appendEmpty(lines, h)
	}
	at := len(lines)
	for i := m.top; i < len(m.comments) && len(lines)-at < h; i++ {
		block := m.blockLines(i)
		from := 0
		if i == m.top {
			from = min(m.skip, len(block))
		}
		for _, line := range block[from:] {
			if len(lines)-at >= h {
				break
			}
			lines = append(lines, line)
		}
	}
	for len(lines)-at < h {
		lines = append(lines, "")
	}
	return lines
}

// appendComposer draws the composer under the thread: one row saying which
// comment this is and how to finish it, then the draft. The chrome row is what
// a box too short for both gives up, because what is left is the text being
// typed.
func (m *Model) appendComposer(lines []string, lay layout) []string {
	if lay.composer <= 0 {
		return lines
	}
	if lay.chrome > 0 {
		lines = append(lines, m.composerChrome())
	}
	drawn := 0
	for _, line := range strings.Split(m.editor.View(), "\n") {
		if drawn >= lay.editor {
			break
		}
		lines = append(lines, line)
		drawn++
	}
	for ; drawn < lay.editor; drawn++ {
		lines = append(lines, "")
	}
	return lines
}

func (m *Model) appendEmpty(lines []string, h int) []string {
	at := len(lines)
	switch {
	case m.issue == "":
		lines = append(lines,
			m.styles.muted.Render("  This view comments on one issue, and has not been told which."),
			m.styles.muted.Render("  Open an issue and come back."))
	case m.deps.Jira == nil:
		lines = append(lines, m.styles.muted.Render("  No Jira connection in this session yet."))
	case !m.loaded:
		lines = append(lines, m.styles.muted.Render("  Reading the thread"+m.deps.Theme.Glyphs.Ellipsis))
	default:
		lines = append(lines,
			m.styles.muted.Render("  Nobody has commented on "+m.issue+" yet."),
			m.styles.muted.Render("  Press a to write the first one."))
	}
	for len(lines)-at < h {
		lines = append(lines, "")
	}
	return lines[:at+h]
}
