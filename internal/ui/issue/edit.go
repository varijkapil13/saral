package issue

import (
	"context"
	"errors"
	"strings"

	"charm.land/bubbles/v2/textinput"
	tea "charm.land/bubbletea/v2"

	"github.com/varijkapil13/saral/internal/app"
	"github.com/varijkapil13/saral/internal/ui/kernel"
	"github.com/varijkapil13/saral/internal/ui/widget"
	"github.com/varijkapil13/saral/pkg/adf"
	"github.com/varijkapil13/saral/pkg/jira"
)

var (
	_ kernel.View        = (*editModel)(nil)
	_ kernel.KeyCapturer = (*editModel)(nil)
	_ kernel.Blocker     = (*editModel)(nil)
	_ kernel.Addressed   = (*editModel)(nil)
)

// editStage is what the pane is doing, which decides both what a key means and
// whether the kernel's own keys are reachable.
type editStage uint8

const (
	stageBrowse editStage = iota
	stageTyping
	stageConfirm
	stageSaving
	stageConflict
)

// editModel is the field editor for one issue.
//
// It never writes a field the issue was not read with. jira.Issue.Requested is
// the whole of that answer: a field outside it is absent because nothing asked,
// not because Jira had nothing to send, and putting it in a patch would write
// an empty value over whatever is really there.
type editModel struct {
	deps   kernel.Deps
	keys   editKeyMap
	styles *editStyles

	issue  jira.Issue
	rows   []editRow
	cursor int
	input  textinput.Model

	stage editStage
	// note is the last thing that happened, shown under the rows.
	note  string
	fail  string
	moved bool

	search *app.Search
	launch editorLauncher
	drafts draftStore

	width, height int
	// top is the first field row on screen: the pane has more fields than a
	// short terminal has room for. follow is set when the cursor has moved and
	// the window has to come with it, and cleared once it has — otherwise the
	// cursor would pull every frame back and the wheel could scroll nothing.
	top    int
	follow bool
	// reach is how far the last frame could scroll. The wheel clamps against it
	// rather than against nothing: an offset allowed to run past the end has to
	// be wound all the way back before the next notch moves anything.
	reach int

	zones  widget.Zoner
	clicks *widget.Clicks

	gen    int
	cancel context.CancelFunc
	addr   kernel.Addr
}

// Addr is where the kernel delivers this pane's re-read and the create screen
// behind its pickers, whatever has since been pushed over it.
func (m *editModel) Addr() kernel.Addr { return m.addr }

// editOption configures the pane at construction. Nothing outside the package
// builds one; the tests use it to stand in for the user's editor.
type editOption func(*editModel)

// withLauncher replaces the handoff to the user's editor.
func withLauncher(l editorLauncher) editOption {
	return func(m *editModel) { m.launch = l }
}

// withDrafts replaces where drafts are kept.
func withDrafts(s draftStore) editOption {
	return func(m *editModel) { m.drafts = s }
}

// NewEdit builds the field editor around the issue the detail pane has.
//
// The issue is drawn as it stands and the pane re-reads it for the fields it
// edits, because the row that opened the detail pane carries six fields and
// this pane can only offer what was actually read.
func NewEdit(d kernel.Deps, iss jira.Issue, opts ...editOption) kernel.View {
	m := &editModel{
		deps:   d,
		keys:   defaultEditKeys(),
		issue:  iss,
		input:  newEditInput(),
		launch: launchEditor,
		addr:   kernel.NewAddr(),
	}
	if m.deps.Theme == nil {
		m.deps.Theme = kernel.NewTheme(kernel.ThemeAuto, true, kernel.UnicodeGlyphs())
	}
	m.styles = newEditStyles(m.deps.Theme)
	if d.Jira != nil {
		m.search = app.NewSearch(d.Jira)
	}
	m.zones = widget.NewZoner(d.Zones)
	m.clicks = widget.NewClicks(d.Now)
	if store, err := newDraftStore(); err == nil {
		m.drafts = store
	}
	for _, o := range opts {
		if o != nil {
			o(m)
		}
	}
	m.rows = buildRows(iss)
	m.restoreDraft()
	return m
}

func newEditInput() textinput.Model {
	ti := widget.NewInput()
	ti.Prompt = ""
	return ti
}

// WantsRawKeys is true while a field is taking typing and while an answer is
// being waited for. Without it q quits the program out from under a summary
// being typed, r refetches the issue mid-edit, and esc throws away the answer
// to a confirmation instead of being the answer.
func (m *editModel) WantsRawKeys() bool {
	return m.stage == stageTyping || m.stage == stageConfirm || m.stage == stageConflict
}

// BlocksClose refuses to let the pane be discarded while it holds an edit
// nobody has saved. The draft is on disk either way; this is what stops esc
// from looking like it worked.
func (m *editModel) BlocksClose() (string, bool) {
	if !m.dirty() || m.moved {
		return "", false
	}
	return m.issue.Key + " has unsaved changes: ctrl+s saves them, X throws them away", true
}

// Init re-reads the fields this pane edits, narrowly.
func (m *editModel) Init() tea.Cmd { return m.fetch() }

// Update handles one message.
func (m *editModel) Update(msg tea.Msg) (kernel.View, tea.Cmd) {
	var cmd tea.Cmd
	switch msg := msg.(type) {
	case kernel.SizeMsg:
		m.resize(msg.Width, msg.Height)

	case kernel.ThemeMsg:
		m.deps.Theme = msg.Theme
		m.styles = newEditStyles(msg.Theme)

	case kernel.CapabilitiesMsg:
		m.deps.Caps = msg.Caps

	case kernel.RefreshMsg:
		cmd = m.reread()

	case editLoadedMsg:
		cmd = m.loaded(msg)

	case editSchemaMsg:
		cmd = m.schema(msg)

	case editFailedMsg:
		cmd = m.failed(msg)

	case editedMsg:
		cmd = m.edited(msg)

	case editSavedMsg:
		cmd = m.saved(msg)

	case tea.KeyPressMsg:
		cmd = m.key(msg)

	case tea.MouseClickMsg:
		cmd = m.click(msg)

	case tea.MouseWheelMsg:
		m.wheel(msg)
	}
	return m, cmd
}

func (m *editModel) resize(w, h int) {
	m.width, m.height = w, h
	m.input.SetWidth(max(w-editLabelWidth-4, 8))
}

func (m *editModel) current(gen int) bool { return gen == m.gen }

func (m *editModel) stop() {
	if m.cancel != nil {
		m.cancel()
		m.cancel = nil
	}
}

// reply puts this pane's address on a command, so what it asked for comes back
// here rather than to whatever the stack has on top by then.
func (m *editModel) reply(cmd tea.Cmd) tea.Cmd { return kernel.Reply(cmd, m.addr) }

// Close lets go of the re-read and the schema behind it. The pane is only ever
// pushed, so this is the whole of its life ending.
func (m *editModel) Close() { m.stop() }

func (m *editModel) begin() (ctx context.Context, gen int) {
	m.stop()
	m.gen++
	next, cancel := context.WithCancel(context.Background())
	m.cancel = cancel
	return next, m.gen
}

// fetch re-reads the issue with a field list naming exactly what this pane can
// change. It is skipped when the issue in hand was already read with all of
// them, so opening the editor on a loaded issue costs nothing.
func (m *editModel) fetch() tea.Cmd {
	if m.search == nil || m.issue.Key == "" || m.covered() {
		return m.loadSchema()
	}
	ctx, gen := m.begin()
	return m.reply(loadForEdit(ctx, m.search, m.issue.Key, gen))
}

// covered reports whether the issue was read with every field the pane offers,
// plus the two the create screen is keyed by.
func (m *editModel) covered() bool {
	for _, id := range editProjection().IDs {
		if !m.issue.Requested.Has(id) {
			return false
		}
	}
	return true
}

func (m *editModel) loadSchema() tea.Cmd {
	if m.deps.Jira == nil || m.issue.Project.Key == "" || m.issue.Type.ID == "" {
		return nil
	}
	if row := m.rowByID("priority"); row == nil || len(row.options) > 0 {
		return nil
	}
	ctx, gen := m.begin()
	return m.reply(loadEditSchema(ctx, m.deps.Jira, m.issue.Project.Key, m.issue.Type.ID, gen))
}

// loaded takes the re-read issue, keeping whatever the user has already typed.
func (m *editModel) loaded(msg editLoadedMsg) tea.Cmd {
	if !m.current(msg.gen) {
		return nil
	}
	m.issue = msg.issue
	m.rebase()
	return m.loadSchema()
}

// rebase rebuilds the rows around a fresh reading of the issue and puts the
// user's own edits back on top, which is both what a re-read after opening and
// what reload-and-reapply after a 409 need.
func (m *editModel) rebase() {
	edits := m.edits()
	options := map[string][]jira.Option{}
	for i := range m.rows {
		if len(m.rows[i].options) > 0 {
			options[m.rows[i].id] = m.rows[i].options
		}
	}
	m.rows = buildRows(m.issue)
	for i := range m.rows {
		if opts, ok := options[m.rows[i].id]; ok {
			m.rows[i].setOptions(opts)
		}
	}
	m.apply(edits)
	m.cursor = min(m.cursor, max(len(m.rows)-1, 0))
}

func (m *editModel) schema(msg editSchemaMsg) tea.Cmd {
	if !m.current(msg.gen) {
		return nil
	}
	for i := range msg.schema.Fields {
		meta := msg.schema.Fields[i]
		row := m.rowByID(meta.Field.ID)
		if row == nil || len(meta.AllowedValues) == 0 {
			continue
		}
		row.setOptions(meta.AllowedValues)
	}
	return nil
}

func (m *editModel) failed(msg editFailedMsg) tea.Cmd {
	if !m.current(msg.gen) {
		return nil
	}
	if m.stage == stageSaving {
		return m.saveFailed(msg.err)
	}
	// A field this pane could not re-read stays refused rather than editable:
	// the reason it gives is why, in Jira's own words.
	reason, _ := jira.Reason(msg.err)
	for i := range m.rows {
		if !m.rows[i].fetched {
			m.rows[i].reason = reason
		}
	}
	return kernel.Fail(msg.err)
}

// --- input ------------------------------------------------------------------

func (m *editModel) key(msg tea.KeyPressMsg) tea.Cmd {
	switch m.stage {
	case stageTyping:
		return m.typingKey(msg)
	case stageConfirm:
		return m.confirmKey(msg)
	case stageConflict:
		return m.conflictKey(msg)
	case stageSaving:
		return nil
	case stageBrowse:
	}
	switch {
	case kernel.Matches(msg, m.keys.Down):
		m.moveTo(m.cursor + 1)
	case kernel.Matches(msg, m.keys.Up):
		m.moveTo(m.cursor - 1)
	case kernel.Matches(msg, m.keys.Act):
		return m.act()
	case kernel.Matches(msg, m.keys.Next):
		return m.cycle(1)
	case kernel.Matches(msg, m.keys.Prev):
		return m.cycle(-1)
	case kernel.Matches(msg, m.keys.Clear):
		return m.clearRow()
	case kernel.Matches(msg, m.keys.Save):
		return m.startSave()
	case kernel.Matches(msg, m.keys.Discard):
		return m.discard()
	}
	return nil
}

func (m *editModel) moveTo(at int) {
	if len(m.rows) == 0 {
		return
	}
	m.cursor = min(max(at, 0), len(m.rows)-1)
	m.follow = true
	m.note, m.fail = "", ""
}

func (m *editModel) row() *editRow {
	if m.cursor < 0 || m.cursor >= len(m.rows) {
		return nil
	}
	return &m.rows[m.cursor]
}

func (m *editModel) rowByID(id string) *editRow {
	for i := range m.rows {
		if m.rows[i].id == id {
			return &m.rows[i]
		}
	}
	return nil
}

// act does whatever the row under the cursor is for.
func (m *editModel) act() tea.Cmd {
	row := m.row()
	if row == nil {
		return nil
	}
	if reason, blocked := row.blocked(); blocked {
		return kernel.Warn(reason)
	}
	switch row.kind {
	case editDoc:
		return m.handOff(row)
	case editPick:
		return m.cycle(1)
	case editText, editLabels, editDate:
		m.stage = stageTyping
		m.input.SetValue(row.value)
		m.input.CursorEnd()
		return m.input.Focus()
	}
	return nil
}

func (m *editModel) cycle(by int) tea.Cmd {
	row := m.row()
	if row == nil || row.kind != editPick {
		return nil
	}
	if reason, blocked := row.blocked(); blocked {
		return kernel.Warn(reason)
	}
	if len(row.options) == 0 {
		return kernel.Warn("this site has not said what " + strings.ToLower(row.label) + " may be set to")
	}
	row.chosen = (row.chosen + by + len(row.options)) % len(row.options)
	return m.keepDraft()
}

func (m *editModel) clearRow() tea.Cmd {
	row := m.row()
	if row == nil {
		return nil
	}
	if reason, blocked := row.blocked(); blocked {
		return kernel.Warn(reason)
	}
	switch row.kind {
	case editText:
		return kernel.Warn(row.label + " cannot be emptied")
	case editPick:
		row.chosen = 0
	case editDoc:
		row.edited, row.cleared = nil, true
	case editLabels, editDate:
		row.value = ""
	}
	return m.keepDraft()
}

func (m *editModel) typingKey(msg tea.KeyPressMsg) tea.Cmd {
	switch {
	case kernel.Matches(msg, m.keys.Accept):
		row := m.row()
		m.stage = stageBrowse
		m.input.Blur()
		if row == nil {
			return nil
		}
		row.value = strings.TrimSpace(m.input.Value())
		return m.keepDraft()
	case kernel.Matches(msg, m.keys.Cancel):
		m.stage = stageBrowse
		m.input.Blur()
		return nil
	}
	// The input's own command is a cursor blink, which would make this pane own
	// a timer for as long as a field is open and every frame irreproducible.
	m.input, _ = m.input.Update(msg)
	return nil
}

func (m *editModel) confirmKey(msg tea.KeyPressMsg) tea.Cmd {
	if !kernel.Matches(msg, m.keys.Yes) {
		m.stage = stageBrowse
		return nil
	}
	return m.save()
}

func (m *editModel) conflictKey(msg tea.KeyPressMsg) tea.Cmd {
	if !kernel.Matches(msg, m.keys.Yes) {
		m.stage = stageBrowse
		return nil
	}
	return m.reread()
}

// reread reads the issue again and rebases the edits onto it, which is what
// both reload-and-reapply after a 409 and the refresh key mean here. It is the
// one refresh that must not throw anything away: docs/UX.md principle 5 is
// about a cursor, and this pane is holding text as well.
func (m *editModel) reread() tea.Cmd {
	if m.search == nil {
		return nil
	}
	m.stage = stageBrowse
	m.note = "re-reading " + m.issue.Key + " and putting your edits back on top"
	ctx, gen := m.begin()
	return m.reply(loadForEdit(ctx, m.search, m.issue.Key, gen))
}

// click puts the cursor on the row under the pointer, and opens it on a
// double-click. A single click never opens a field: the pane hands a description
// to $EDITOR, which is not what somebody pointing at a row asked for.
func (m *editModel) click(msg tea.MouseClickMsg) tea.Cmd {
	if msg.Button != tea.MouseLeft || m.stage != stageBrowse {
		return nil
	}
	for i := range m.rows {
		zone := editRowZone(m.rows[i].id)
		if !m.zones.Hit(zone, msg) {
			continue
		}
		if m.clicks.Double(zone) {
			m.moveTo(i)
			return m.act()
		}
		m.moveTo(i)
		return nil
	}
	return nil
}

// wheel scrolls the field rows under the pointer. The offset is clamped where
// the frame is drawn, because how many lines a row occupies depends on what the
// site refused and how wide the terminal is.
func (m *editModel) wheel(msg tea.MouseWheelMsg) {
	switch msg.Button {
	case tea.MouseWheelUp:
		m.top -= widget.WheelStep
	case tea.MouseWheelDown:
		m.top += widget.WheelStep
	default:
	}
	m.top = min(max(m.top, 0), m.reach)
}

// --- the editor handoff -----------------------------------------------------

func (m *editModel) handOff(row *editRow) tea.Cmd {
	_, gen := m.begin()
	return handOffToEditor(m.launch, m.addr, gen, m.issue.Key, row.documentNow())
}

func (m *editModel) edited(msg editedMsg) tea.Cmd {
	if !m.current(msg.gen) {
		return nil
	}
	row := m.rowByID("description")
	switch {
	case msg.err != nil:
		m.fail = msg.err.Error()
		return kernel.Fail(msg.err)
	case row == nil, msg.doc == nil && !msg.cleared:
		m.note = msg.note
		return nil
	case msg.cleared:
		row.edited, row.cleared = nil, true
	default:
		row.edited, row.cleared = msg.doc, false
	}
	m.note = msg.note
	return m.keepDraft()
}

// --- saving -----------------------------------------------------------------

func (m *editModel) startSave() tea.Cmd {
	if !m.dirty() {
		return kernel.Warn("nothing has changed")
	}
	if _, err := m.patch(); err != nil {
		m.fail, _ = jira.Reason(err)
		return kernel.Fail(err)
	}
	m.stage = stageConfirm
	return nil
}

func (m *editModel) save() tea.Cmd {
	patch, err := m.patch()
	if err != nil {
		m.stage = stageBrowse
		m.fail, _ = jira.Reason(err)
		return kernel.Fail(err)
	}
	if m.deps.Jira == nil {
		m.stage = stageBrowse
		return kernel.Warn("there is no Jira connection in this session")
	}
	m.stage = stageSaving
	m.fail = ""
	ctx, gen := m.begin()
	return m.reply(saveEdit(ctx, m.deps.Jira, m.issue.Key, patch, gen))
}

func (m *editModel) saved(msg editSavedMsg) tea.Cmd {
	if !m.current(msg.gen) {
		return nil
	}
	m.stage, m.moved = stageBrowse, true
	// The write landed, so the draft has nothing left to protect.
	if err := m.drafts.discard(m.deps.Site, m.issue.Key); err != nil {
		return kernel.Warn(err.Error())
	}
	return tea.Sequence(
		kernel.Pop(),
		kernel.Broadcast(kernel.RefreshMsg{}),
		kernel.Status(m.issue.Key+" saved"),
	)
}

// saveFailed keeps every edit on screen and on disk. docs/UX.md principle 6 is
// that a refused write never costs the user their text, and a 409 is the case
// that principle was written for.
func (m *editModel) saveFailed(err error) tea.Cmd {
	m.markFieldProblems(err)
	var conflict *jira.ConflictError
	if errors.As(err, &conflict) {
		m.stage = stageConflict
		m.fail, _ = jira.Reason(err)
		return nil
	}
	m.stage = stageBrowse
	m.fail, _ = jira.Reason(err)
	return kernel.Fail(err)
}

// markFieldProblems puts each of Jira's per-field messages on the row it is
// about, so a rejected write annotates the field rather than a status line.
func (m *editModel) markFieldProblems(err error) {
	for i := range m.rows {
		m.rows[i].problem = ""
	}
	var invalid *jira.ValidationError
	if !errors.As(err, &invalid) {
		return
	}
	for _, field := range invalid.Fields {
		if row := m.rowByID(field.Field); row != nil {
			row.problem = field.Message
		}
	}
}

func (m *editModel) discard() tea.Cmd {
	if !m.dirty() {
		return kernel.Pop()
	}
	m.moved = true
	key := m.issue.Key
	if err := m.drafts.discard(m.deps.Site, key); err != nil {
		return tea.Batch(kernel.Warn(err.Error()), kernel.Pop())
	}
	return tea.Sequence(kernel.Pop(), kernel.Status("the changes to "+key+" were thrown away"))
}

// --- edits, drafts and the patch --------------------------------------------

func (m *editModel) dirty() bool {
	for i := range m.rows {
		if m.rows[i].dirty() {
			return true
		}
	}
	return false
}

// edits is what the user has changed, in the form it was typed, which is what a
// draft holds and what reload-and-reapply puts back.
func (m *editModel) edits() draft {
	out := draft{Key: m.issue.Key, Site: m.deps.Site, Values: map[string]string{}}
	for i := range m.rows {
		row := &m.rows[i]
		if !row.dirty() {
			continue
		}
		if row.kind == editDoc {
			if row.cleared {
				out.Values[row.id] = ""
				continue
			}
			if body, err := adf.Marshal(*row.edited); err == nil {
				out.Description = body
			}
			continue
		}
		out.Values[row.id] = row.editedValue()
	}
	if len(out.Values) == 0 {
		out.Values = nil
	}
	return out
}

// apply puts a draft's edits back onto the rows, skipping anything the issue
// was not read with — a draft outlives a session, and the field list of the
// read that rebuilt these rows is not the one that produced it.
func (m *editModel) apply(d draft) {
	for id, value := range d.Values {
		row := m.rowByID(id)
		if row == nil || !row.fetched {
			continue
		}
		if row.kind == editDoc {
			row.cleared, row.edited = true, nil
			continue
		}
		row.setEdited(value)
	}
	if len(d.Description) == 0 {
		return
	}
	row := m.rowByID("description")
	if row == nil || !row.fetched {
		return
	}
	doc, err := adf.Unmarshal(d.Description)
	if err != nil {
		return
	}
	row.edited, row.cleared = &doc, false
}

func (m *editModel) restoreDraft() {
	kept, ok, err := m.drafts.load(m.deps.Site, m.issue.Key)
	if err != nil || !ok {
		return
	}
	m.apply(kept)
	m.note = "picked up the unsaved changes from last time"
}

func (m *editModel) keepDraft() tea.Cmd {
	if err := m.drafts.save(m.edits()); err != nil {
		return kernel.Warn(err.Error())
	}
	return nil
}

// patch turns the changed rows into a sparse patch.
//
// Every row it reads is one the issue was actually read with, so nothing here
// can name a field the site was never asked about — which is the difference
// between an edit and blanking somebody's ticket.
func (m *editModel) patch() (jira.IssuePatch, error) {
	var out jira.IssuePatch
	for i := range m.rows {
		row := &m.rows[i]
		if !row.dirty() {
			continue
		}
		if !row.fetched {
			return jira.IssuePatch{}, notRead(row)
		}
		if err := row.into(&out); err != nil {
			return jira.IssuePatch{}, err
		}
	}
	return out, nil
}

func notRead(row *editRow) error {
	return &jira.ValidationError{Fields: []jira.FieldError{{
		Field:   row.id,
		Message: row.label + " was not read with this issue, so writing it would empty whatever is really there",
	}}}
}

// editProjection is the field list this pane reads an issue with: what it can
// change, plus what the create screen behind the pickers is keyed by.
func editProjection() app.Projection {
	return app.Projection{
		Name: "issue editor",
		IDs: []string{
			"summary", "description", "labels", "duedate", "priority",
			"status", "issuetype", "project", "updated",
		},
	}
}

// editableIDs is what the pane offers a row for, in the order they are drawn.
var editableIDs = []string{"summary", "description", "priority", "labels", "duedate"}

func buildRows(iss jira.Issue) []editRow {
	rows := make([]editRow, 0, len(editableIDs))
	for _, id := range editableIDs {
		rows = append(rows, newEditRow(id, iss))
	}
	return rows
}
