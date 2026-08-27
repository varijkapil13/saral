// Package attach is the attachment pane: the files on one issue, and a look at
// the one under the cursor without leaving the terminal.
//
// An image is drawn inline where the terminal can draw one — kitty's graphics
// protocol, then iTerm2's, then chafa's half-blocks — and named with its size
// where it cannot. Everything that is not an image goes to the desktop's own
// handler.
//
// Nothing here ever sees the signed media URL a download redirects through. The
// port takes an attachment id and a writer, so the credential lives and dies
// inside the adapter; what this package hands to chafa and to the system handler
// is a file of its own under the cache directory.
package attach

import (
	"context"
	"strings"

	"charm.land/bubbles/v2/textinput"
	tea "charm.land/bubbletea/v2"

	"github.com/varijkapil13/saral/internal/ui/kernel"
	"github.com/varijkapil13/saral/internal/ui/widget"
	"github.com/varijkapil13/saral/pkg/jira"
)

// ViewID is the name this view's keys are registered under, and the id it must
// be pushed with so that KeysFor finds them.
const ViewID = "attach"

// previewLimit is the largest file this pane will read into memory to hand a
// terminal as one inline image. Both graphics protocols carry the bytes in the
// escape sequence itself, so a file past this is opened in the desktop handler
// instead of base64'd into a frame.
const previewLimit = 8 << 20

var (
	_ kernel.View        = (*Model)(nil)
	_ kernel.KeyCapturer = (*Model)(nil)
	_ kernel.Addressed   = (*Model)(nil)
	_ kernel.Closer      = (*Model)(nil)
)

type mode uint8

const (
	browsing mode = iota
	typing
	confirming
)

// Option configures a pane at construction.
type Option func(*Model)

// WithIssue names the issue whose files these are. There is no default: a pane
// with no issue behind it has nothing to read and nothing to attach to, which is
// why nothing in the registry opens this one.
func WithIssue(key string) Option {
	return func(m *Model) { m.issue = strings.TrimSpace(key) }
}

// Model is the attachment pane.
type Model struct {
	deps  kernel.Deps
	issue string

	mode      mode
	acts      map[string]action
	inPrompt  map[string]action
	inConfirm map[string]action
	input     textinput.Model
	clicks    *widget.Clicks
	confirm   string

	files       []jira.Attachment
	cursor, top int
	loaded      bool
	loading     bool
	failure     error

	// shown is the preview on screen and asked is the download behind one that
	// has not arrived. Moving the cursor changes neither: a preview costs a round
	// trip, so it is asked for rather than followed.
	shown   preview
	asked   string
	written int64
	total   int64

	// grown is the preview taking the whole box, with the list folded away.
	grown bool

	width, height int

	gen int
	// ctx is what the request in flight is running under, kept so that the work
	// which follows an answer — a renderer started on the file just downloaded —
	// is cancelled by the same stop that cancels the read.
	ctx    context.Context
	cancel context.CancelFunc
	addr   kernel.Addr

	canWrite bool
	graphics jira.GraphicsMode
	tools    tools

	styles *styles
	memo   *rowCache
	lay    layout
	lines  []string

	// The chrome lines are memoized on comparable keys, because a frame whose
	// rows are all cache hits is otherwise mostly the cost of rebuilding these.
	top1   string
	head   string
	headAt headKey
	div    string
	divAt  divKey
	pane   []string
	paneAt paneKey

	zones widget.Zoner
}

// New builds the pane. It reads nothing until it is told which issue it is
// about, which is what WithIssue is for.
func New(d kernel.Deps, opts ...Option) kernel.View {
	m := &Model{
		deps: d, input: newInput(), addr: kernel.NewAddr(),
		tools: newTools(), ctx: context.Background(),
	}
	for _, opt := range opts {
		opt(m)
	}
	if m.deps.Theme == nil {
		m.deps.Theme = kernel.NewTheme(kernel.ThemeAuto, true, kernel.UnicodeGlyphs())
	}
	m.acts, m.inPrompt, m.inConfirm = defaultKeys().tables()
	m.clicks = widget.NewClicks(d.Now)
	m.styles = newStyles(m.deps.Theme)
	m.memo = newRowCache(rowMemoLimit)
	m.zones = widget.NewZoner(d.Zones)
	m.lay = planLayout(m.width)
	m.graphics = d.Caps.Graphics
	m.canWrite = m.writable()
	return m
}

// repaint drops the memoized chrome. Every one of those lines is cached on a key
// that says what it was built from, and this is the belt: a state that moved
// without moving a key would otherwise leave a stale line on screen.
func (m *Model) repaint() {
	m.top1, m.head, m.div, m.pane = "", "", "", nil
}

// writable reports whether this token may add and remove files here. Reading
// them needs nothing beyond seeing the issue; adding one is a site setting no
// permission makes up for.
func (m *Model) writable() bool {
	return m.deps.Jira != nil && m.deps.Caps.Allows(jira.CapAttachments)
}

// refusal is why files cannot be added here, in the site's own words where it gave any.
func (m *Model) refusal() string {
	if m.deps.Jira == nil {
		return "there is no Jira connection in this session"
	}
	if reason := m.deps.Caps.Capability(jira.CapAttachments).Reason; reason != "" {
		return reason
	}
	return "attachments are switched off on this site"
}

// WantsRawKeys is true while a path is being typed and while a deletion is
// waiting for an answer. Without it the kernel matches its own bindings first,
// so a path loses every digit, q quits out from under the typing, and esc pops
// the pane instead of leaving the file where it is.
func (m *Model) WantsRawKeys() bool { return m.mode != browsing }

// Init reads the files on the issue, and only when there is an issue to read
// them for.
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
		m.resize(msg.Width, msg.Height)

	case kernel.FocusMsg:
		m.focus(msg.Focused)

	case kernel.ThemeMsg:
		m.deps.Theme = msg.Theme
		m.styles = newStyles(msg.Theme)
		m.memo.reset()
		m.repaint()

	case kernel.CapabilitiesMsg:
		m.deps.Caps = msg.Caps
		m.graphics = msg.Caps.Graphics
		m.canWrite = m.writable()
		m.memo.reset()
		m.repaint()

	case kernel.RefreshMsg:
		if msg.Purge {
			m.shown, m.asked = preview{}, ""
		}
		cmd = m.load()

	case listedMsg:
		m.tookList(msg)

	case downloadedMsg:
		cmd = m.tookDownload(msg)

	case progressMsg:
		cmd = m.tookProgress(msg)

	case previewMsg:
		m.tookPreview(msg)

	case uploadedMsg:
		cmd = m.tookUpload(msg)

	case deletedMsg:
		cmd = m.tookDelete(msg)

	case failedMsg:
		cmd = m.failed(msg)

	case ShowMsg:
		cmd = m.show()

	case OpenOutsideMsg:
		cmd = m.openOutside()

	case UploadMsg:
		cmd = m.startUpload()

	case DeleteMsg:
		cmd = m.startDelete()

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
	m.lay = planLayout(w)
	m.input.SetWidth(max(w-inputChrome, 8))
	m.memo.reset()
	m.repaint()
	// A preview drawn for another box is the wrong number of cells wide, and both
	// graphics protocols are told the geometry rather than measuring it.
	m.shown = preview{}
	m.clampScroll()
}

// focus keeps the cursor out of a prompt nobody is typing into. It lets go of
// no request: losing the keys is not being closed, and the kernel blurs a view
// it is pushing over as well as one it is discarding.
func (m *Model) focus(on bool) {
	if on && m.mode == typing {
		_ = m.input.Focus()
		return
	}
	m.input.Blur()
}

func newInput() textinput.Model {
	ti := textinput.New()
	ti.Prompt = "> "
	ti.Placeholder = "a path to the file to attach"
	return ti
}

// --- keys -------------------------------------------------------------------

func (m *Model) key(msg tea.KeyPressMsg) tea.Cmd {
	switch m.mode {
	case typing:
		return m.typingKey(msg)
	case confirming:
		return m.confirmingKey(msg)
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
		m.moveTo(len(m.files) - 1)
	case actShow:
		return m.show()
	case actOpen:
		return m.openOutside()
	case actUpload:
		return m.startUpload()
	case actDelete:
		return m.startDelete()
	case actGrow:
		m.toggleGrown()
	case actNone, actSend, actCancel, actConfirm:
	}
	return nil
}

// typingKey takes the keys while a path is being typed. Everything the table
// does not claim is text, which is why the pane claims raw keys here.
func (m *Model) typingKey(msg tea.KeyPressMsg) tea.Cmd {
	switch m.inPrompt[msg.String()] {
	case actSend:
		return m.sendUpload()
	case actCancel:
		m.mode = browsing
		m.input.Blur()
		m.input.Reset()
		m.repaint()
		return nil
	case actNone, actUp, actDown, actPageUp, actPageDown, actTop, actBottom,
		actShow, actOpen, actUpload, actDelete, actGrow, actConfirm:
	}
	// The input's own command is a cursor blink, which is a timer this view would
	// then own for as long as it is up. Dropping it costs a blinking block and
	// keeps every frame reproducible.
	m.input, _ = m.input.Update(msg)
	return nil
}

// confirmingKey answers for a deletion. Only the confirm key deletes: the key
// that opened this state is not in this table, so a held-down d cannot carry a
// file past the question.
func (m *Model) confirmingKey(msg tea.KeyPressMsg) tea.Cmd {
	switch m.inConfirm[msg.String()] {
	case actConfirm:
		return m.sendDelete()
	case actCancel:
		m.mode, m.confirm = browsing, ""
		m.repaint()
		return kernel.Status("the file is where it was")
	case actNone, actUp, actDown, actPageUp, actPageDown, actTop, actBottom,
		actShow, actOpen, actUpload, actDelete, actGrow, actSend:
	}
	return nil
}

// toggleGrown folds the list away and gives the preview the whole box, or puts it
// back. The preview goes with it: both graphics protocols are told the geometry
// rather than measuring it, so one drawn for the small box is the wrong size in
// the large one.
func (m *Model) toggleGrown() {
	m.grown = !m.grown
	m.shown = preview{}
	m.repaint()
	m.clampScroll()
}

// --- actions ----------------------------------------------------------------

func (m *Model) selected() *jira.Attachment {
	if m.cursor < 0 || m.cursor >= len(m.files) {
		return nil
	}
	return &m.files[m.cursor]
}

// show asks for the file under the cursor. An image is drawn here; anything else
// goes to the desktop's own handler, because a terminal cannot show a PDF and
// pretending otherwise is a pane full of bytes.
func (m *Model) show() tea.Cmd {
	att := m.selected()
	switch {
	case att == nil:
		return nil
	case !isImage(*att):
		return m.openOutside()
	case att.Size > previewLimit:
		return kernel.Warn(att.Filename + " is " + humanSize(att.Size) + ", too large to draw inline; " +
			defaultKeys().Open.Help().Key + " opens it")
	case m.shown.id == att.ID && m.shown.kind != previewNone:
		return nil
	}
	return m.fetch(*att, previewIntent)
}

// openOutside hands the file to whatever this desktop opens files with. It is
// downloaded first: the only URL that would reach the handler otherwise is the
// signed one the port redirects through, which is a credential with minutes on
// it and never leaves this process.
func (m *Model) openOutside() tea.Cmd {
	att := m.selected()
	if att == nil {
		return nil
	}
	return m.fetch(*att, openIntent)
}

func (m *Model) startUpload() tea.Cmd {
	if !m.canWrite {
		return kernel.Warn(m.refusal())
	}
	m.mode = typing
	m.input.Reset()
	m.input.SetWidth(max(m.width-inputChrome, 8))
	_ = m.input.Focus()
	m.repaint()
	return nil
}

func (m *Model) sendUpload() tea.Cmd {
	path := strings.TrimSpace(m.input.Value())
	if path == "" {
		return kernel.Warn("there is no path here to attach")
	}
	file, err := jira.FileFromPath(path)
	if err != nil {
		return kernel.Fail(err)
	}
	m.mode = browsing
	m.input.Blur()
	m.input.Reset()
	m.repaint()
	ctx, gen := m.begin()
	return m.reply(upload(ctx, m.deps.Jira, m.issue, file, gen))
}

// startDelete opens the confirmation. It never deletes: docs/UX.md asks for a
// named confirmation before anything destructive, and the file it names is read
// here so that a cursor moved under the prompt cannot change what y removes.
func (m *Model) startDelete() tea.Cmd {
	if !m.canWrite {
		return kernel.Warn(m.refusal())
	}
	att := m.selected()
	if att == nil {
		return nil
	}
	m.mode, m.confirm = confirming, att.ID
	m.repaint()
	return nil
}

func (m *Model) sendDelete() tea.Cmd {
	id := m.confirm
	m.mode, m.confirm = browsing, ""
	m.repaint()
	at := m.indexOf(id)
	if at < 0 {
		return nil
	}
	ctx, gen := m.begin()
	return m.reply(remove(ctx, m.deps.Jira, id, m.files[at].Filename, gen))
}

func (m *Model) indexOf(id string) int {
	for i := range m.files {
		if m.files[i].ID == id {
			return i
		}
	}
	return -1
}

// --- fetching ---------------------------------------------------------------

// begin cancels whatever is in flight and opens a context for its replacement.
// The generation it returns is what a landing answer is checked against, so a
// reply to a question the reader has moved on from is dropped rather than drawn.
func (m *Model) begin() (ctx context.Context, gen int) {
	m.stop()
	m.gen++
	ctx, cancel := context.WithCancel(context.Background())
	m.ctx, m.cancel = ctx, cancel
	m.failure = nil
	return ctx, m.gen
}

func (m *Model) stop() {
	if m.cancel != nil {
		m.cancel()
		m.cancel = nil
	}
	m.loading = false
	m.asked = ""
}

// Addr is where the kernel delivers what this pane asked the site for, whatever
// has since been pushed over it.
func (m *Model) Addr() kernel.Addr { return m.addr }

// reply puts this pane's address on a command, so what it asked for comes back
// here rather than to whatever the stack has on top by then.
func (m *Model) reply(cmd tea.Cmd) tea.Cmd { return kernel.Reply(cmd, m.addr) }

// Close lets go of a read, a download, an upload or a deletion still out with
// the site. A pane that has been thrown away has nothing to draw.
func (m *Model) Close() { m.stop() }

func (m *Model) load() tea.Cmd {
	if m.deps.Jira == nil || m.issue == "" {
		return nil
	}
	ctx, gen := m.begin()
	m.loading = true
	m.repaint()
	return m.reply(list(ctx, m.deps.Jira, m.issue, gen))
}

// fetch downloads one attachment and then does what the reader asked for with
// it. A file already on disk from this session is not asked for again: an
// attachment is never rewritten in place — an edit of one is a new id — so what
// is cached cannot be stale.
func (m *Model) fetch(att jira.Attachment, why intent) tea.Cmd {
	if m.deps.Jira == nil {
		return nil
	}
	ctx, gen := m.begin()
	m.asked, m.written, m.total = att.ID, 0, att.Size
	m.repaint()
	if why == previewIntent {
		m.shown = preview{}
	}
	if path, ok := m.tools.cached(m.deps.Site, att); ok {
		return m.reply(func() tea.Msg {
			return downloadedMsg{gen: gen, id: att.ID, why: why, path: path}
		})
	}
	steps := make(chan int64, 1)
	// Each half is addressed on its own. A batch wrapped in one Reply is a
	// kernel message inside an envelope meant for a view, and the kernel hands
	// the list of commands to the pane rather than running them.
	return tea.Batch(
		m.reply(download(ctx, m.deps.Jira, m.tools, m.deps.Site, att, why, gen, steps)),
		m.reply(awaitProgress(steps, att.ID, gen)),
	)
}

// tookProgress draws the running total and waits for the next one. The channel
// is closed when the download ends, which is what stops this re-arming.
func (m *Model) tookProgress(msg progressMsg) tea.Cmd {
	if msg.gen != m.gen || msg.id != m.asked {
		return nil
	}
	m.written = msg.written
	m.repaint()
	return m.reply(awaitProgress(msg.steps, msg.id, msg.gen))
}

func (m *Model) tookList(msg listedMsg) {
	if msg.gen != m.gen {
		return
	}
	m.loading, m.loaded, m.failure = false, true, nil
	under := ""
	if att := m.selected(); att != nil {
		under = att.ID
	}
	m.files = msg.files
	m.memo.reset()
	m.repaint()
	m.cursor = max(m.indexOf(under), 0)
	m.scrollToCursor()
}

func (m *Model) tookDownload(msg downloadedMsg) tea.Cmd {
	if msg.gen != m.gen || msg.id != m.asked {
		return nil
	}
	if msg.why == openIntent {
		m.asked = ""
		m.repaint()
		if m.tools.open == nil {
			return nil
		}
		return m.tools.open(msg.path)
	}
	at := m.indexOf(msg.id)
	if at < 0 {
		m.asked = ""
		return nil
	}
	return m.reply(render(m.ctx, m.tools, m.files[at], msg.path, m.previewBox(), m.graphics, msg.gen))
}

func (m *Model) tookPreview(msg previewMsg) {
	if msg.gen != m.gen || msg.shown.id != m.asked {
		return
	}
	m.asked = ""
	m.shown = msg.shown
	m.repaint()
}

// tookUpload puts what the site said it stored on the list. It does not re-read
// the issue: the upload's own answer is the attachments as stored, and confirming
// a write by reading it back is how a screen ends up reporting a different
// failure from the one that happened.
func (m *Model) tookUpload(msg uploadedMsg) tea.Cmd {
	if msg.gen != m.gen {
		return nil
	}
	m.failure = nil
	if len(msg.added) == 0 {
		return kernel.Warn("the site stored nothing and said nothing about it")
	}
	m.files = append(m.files, msg.added...)
	m.loaded = true
	m.memo.reset()
	m.repaint()
	m.cursor = len(m.files) - len(msg.added)
	m.scrollToCursor()
	return kernel.Status("attached " + nameList(msg.added))
}

// tookDelete takes the row off and says what a description that referenced the
// file will now do, because Jira validates a media node against the issue it is
// on and refuses the whole document once the file is gone.
func (m *Model) tookDelete(msg deletedMsg) tea.Cmd {
	if msg.gen != m.gen {
		return nil
	}
	m.failure = nil
	at := m.indexOf(msg.id)
	if at < 0 {
		return nil
	}
	m.files = append(m.files[:at], m.files[at+1:]...)
	if m.shown.id == msg.id {
		m.shown = preview{}
	}
	m.memo.reset()
	m.repaint()
	m.cursor = min(m.cursor, max(len(m.files)-1, 0))
	m.scrollToCursor()
	return kernel.Warn("deleted " + msg.name +
		"; a description or comment that showed it will be refused until the reference is taken out")
}

// failed keeps the refusal in the pane as well as on the status line: a status
// line is overwritten by the next thing that happens, and a pane that is empty
// because the site said no has to keep saying so.
func (m *Model) failed(msg failedMsg) tea.Cmd {
	if msg.gen != m.gen {
		return nil
	}
	m.loading, m.failure = false, msg.err
	m.asked = ""
	m.repaint()
	if msg.why != noIntent {
		m.shown = preview{}
	}
	return kernel.Fail(msg.err)
}

// --- selection --------------------------------------------------------------

func (m *Model) moveTo(at int) {
	if len(m.files) == 0 {
		m.cursor, m.top = 0, 0
		return
	}
	m.cursor = min(max(at, 0), len(m.files)-1)
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
	m.top = min(max(m.top, 0), max(len(m.files)-m.rowsHeight(), 0))
}

// --- mouse ------------------------------------------------------------------

// click selects, and a double-click on the row already selected does what enter does.
func (m *Model) click(msg tea.MouseClickMsg) tea.Cmd {
	if msg.Button != tea.MouseLeft {
		return nil
	}
	rows := m.rowsHeight()
	for i := m.top; i < min(m.top+rows, len(m.files)); i++ {
		zone := m.zoneOf(i)
		if !m.zones.Hit(zone, msg) {
			continue
		}
		if m.clicks.Double(zone) {
			m.moveTo(i)
			return m.show()
		}
		m.moveTo(i)
		return nil
	}
	if m.zones.Hit(zonePreview, msg) {
		m.clicks.Forget()
		m.toggleGrown()
	}
	return nil
}

// wheel scrolls without moving the selection, which is what a wheel does everywhere else.
func (m *Model) wheel(msg tea.MouseWheelMsg) {
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
