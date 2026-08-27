// Package form is the create screen. It asks the site what a project and issue
// type require, turns that answer into widgets, checks what it can before a
// write, and puts whatever Jira refuses on the field it is about.
//
// Nothing about a field is written down here. Which fields exist, which are
// required, what each one holds and which values it allows all come from
// createmeta at runtime, so a site with a mandatory custom field gets a form
// with that field on it and a site without it does not. Nothing is decided from
// a display name either, because a translated site spells them in its own
// language.
package form

import (
	"context"
	"errors"
	"slices"
	"strconv"
	"strings"

	"charm.land/bubbles/v2/textarea"
	"charm.land/bubbles/v2/textinput"
	tea "charm.land/bubbletea/v2"

	"github.com/varijkapil13/saral/internal/app"
	"github.com/varijkapil13/saral/internal/ui/kernel"
	"github.com/varijkapil13/saral/internal/ui/widget"
	"github.com/varijkapil13/saral/pkg/jira"
)

// ViewID is the name this view registers itself under and the scope its keys
// are registered in.
const ViewID = "form"

// rowCacheLimit is how many rendered rows are kept: a screen of fields several
// relayouts deep, in both their focused and unfocused forms.
const rowCacheLimit = 512

var (
	_ kernel.View        = (*Model)(nil)
	_ kernel.KeyCapturer = (*Model)(nil)
	_ kernel.Blocker     = (*Model)(nil)
	_ kernel.Addressed   = (*Model)(nil)
)

// stage is which half of the flow the form is in: an issue type has to be
// chosen before there is a create screen to read.
type stage uint8

const (
	stageTypes stage = iota
	stageFields
)

// editor is which editing pane is open over the field list.
type editor uint8

const (
	editNone editor = iota
	editText
	editDoc
	editChoose
)

// CreateMsg opens the form on one issue type. It is exported so that anything
// holding an issue type — the palette, a list, a later packet's edit flow — can
// drive this view without holding a pointer to it.
type CreateMsg struct{ IssueTypeID string }

// Model is the create form.
type Model struct {
	deps     kernel.Deps
	search   *app.Search
	cache    *schemaCache
	drafts   *draftStore
	styles   *styles
	inList   map[string]action
	inChoose map[string]action
	rows     *rowCache

	project string

	zones  widget.Zoner
	clicks *widget.Clicks

	stage stage

	types      []jira.IssueType
	typeCursor int
	typeTop    int
	sought     string

	chosen jira.IssueType
	schema jira.Schema
	fields []*field
	hidden []hiddenField
	shown  bool
	index  []row

	me     jira.User
	haveMe bool

	cursor, top   int
	width, height int
	lay           layout

	edit    editor
	editing int
	input   textinput.Model
	area    textarea.Model
	filter  textinput.Model
	choices []choice
	pick    int
	pickTop int

	banner  []string
	note    string
	loading bool
	busy    bool

	gen    int
	cancel context.CancelFunc
	addr   kernel.Addr

	// lines is the frame under construction and head the caption above it, kept
	// between frames so that drawing a screen allocates neither.
	lines   []string
	head    string
	headKey headingKey
}

// hiddenField is a field the form does not offer, and the reason it does not.
type hiddenField struct {
	name     string
	reason   string
	required bool
}

// choice is one entry of a picker: what it shows, and what is stored when it is
// taken.
type choice struct {
	label string
	value jira.Option
	on    bool
}

// New builds the create form for the project this session is scoped to.
func New(d kernel.Deps) kernel.View { return newWith(d, schemas, drafts) }

// Addr is where the kernel delivers the issue types, the create screen and the
// issue this form asked for, whatever has since been pushed over it.
func (m *Model) Addr() kernel.Addr { return m.addr }

func newWith(d kernel.Deps, cache *schemaCache, store *draftStore) *Model {
	if d.Theme == nil {
		d.Theme = kernel.NewTheme(kernel.ThemeAuto, true, kernel.UnicodeGlyphs())
	}
	m := &Model{
		deps:    d,
		addr:    kernel.NewAddr(),
		cache:   cache,
		drafts:  store,
		styles:  newStyles(d.Theme),
		rows:    newRowCache(rowCacheLimit),
		project: strings.TrimSpace(d.Project),
		input:   widget.NewInput(),
		area:    widget.NewArea(),
		filter:  newFilter(),
	}
	m.inList, m.inChoose = defaultKeys().tables()
	if d.Jira != nil {
		m.search = app.NewSearch(d.Jira)
	}
	m.zones = widget.NewZoner(d.Zones)
	m.clicks = widget.NewClicks(d.Now)
	m.relayout()
	return m
}

func newFilter() textinput.Model {
	ti := widget.NewInput()
	ti.Prompt = "/"
	ti.Placeholder = "narrow these values"
	return ti
}

// WantsRawKeys is true while an editor is open. Without it the kernel spends
// the digits on saved queries, q on quitting and esc on going back, so a
// summary could not contain a number and a chooser could not be closed.
func (m *Model) WantsRawKeys() bool { return m.edit != editNone }

// BlocksClose refuses to throw the view away while Jira is being asked to
// create the issue, because the answer has nowhere else to land. Anything typed
// survives closing on its own: it is kept and restored the next time the same
// screen is opened.
func (m *Model) BlocksClose() (string, bool) {
	if !m.busy {
		return "", false
	}
	return "Jira is still being asked to create this issue", true
}

// Init finds out which issue types this project uses.
func (m *Model) Init() tea.Cmd { return m.loadTypes() }

// Update handles one message.
func (m *Model) Update(msg tea.Msg) (kernel.View, tea.Cmd) {
	var cmd tea.Cmd
	switch msg := msg.(type) {
	case kernel.SizeMsg:
		m.resize(msg.Width, msg.Height)

	case kernel.FocusMsg:
		m.setFocus(msg.Focused)

	case kernel.ThemeMsg:
		m.deps.Theme = msg.Theme
		m.styles = newStyles(msg.Theme)
		m.rows.reset()
		m.relayout()

	case kernel.CapabilitiesMsg:
		m.deps.Caps = msg.Caps
		for _, f := range m.fields {
			f.loc = msg.Caps.Location()
		}

	case kernel.RefreshMsg:
		cmd = m.refresh(msg.Purge)

	case CreateMsg:
		cmd = m.chooseType(msg.IssueTypeID)

	case typesFoundMsg:
		cmd = m.typesFound(msg)

	case typesFailedMsg:
		cmd = m.typesFailed(msg)

	case schemaLoadedMsg:
		cmd = m.schemaLoaded(msg)

	case schemaFailedMsg:
		cmd = m.schemaFailed(msg)

	case accountMsg:
		m.accountFound(msg)

	case createdMsg:
		cmd = m.created(msg)

	case createFailedMsg:
		cmd = m.createFailed(msg)

	case tea.KeyPressMsg:
		cmd = m.key(msg)

	case tea.MouseClickMsg:
		cmd = m.click(msg)

	case tea.MouseWheelMsg:
		m.wheel(msg)
	}
	return m, cmd
}

func (m *Model) setFocus(on bool) {
	if m.edit == editNone {
		return
	}
	if !on {
		m.input.Blur()
		m.area.Blur()
		m.filter.Blur()
		return
	}
	m.focusEditor()
}

func (m *Model) resize(w, h int) {
	if w == m.width && h == m.height {
		return
	}
	m.width, m.height = w, h
	m.relayout()
	m.scrollToCursor()
	m.sizeEditor()
}

// relayout recomputes the column plan, which changes on a resize and on the
// field list changing and on nothing else.
func (m *Model) relayout() {
	lay := planLayout(m.width, m.widestLabel())
	if lay != m.lay {
		m.lay = lay
		m.rows.reset()
	}
}

func (m *Model) widestLabel() int {
	widest := 0
	for _, f := range m.fields {
		widest = max(widest, labelWidth(f))
	}
	for _, h := range m.hidden {
		widest = max(widest, h.width())
	}
	return widest
}

// --- fetching ---------------------------------------------------------------

// begin cancels whatever is in flight and opens a context for its replacement.
// The generation it returns is what a landing answer is checked against, so a
// result for a question the user has moved on from is dropped rather than drawn.
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
	m.loading, m.busy = false, false
}

// reply puts this form's address on a command, so what it asked for comes back
// here rather than to whatever the stack has on top by then.
func (m *Model) reply(cmd tea.Cmd) tea.Cmd {
	return kernel.Reply(withCancel(m.cancel, cmd), m.addr)
}

// Close lets go of the issue types and the field schema behind them. A create
// screen that has been thrown away has nowhere to draw either.
func (m *Model) Close() { m.stop() }

func (m *Model) current(gen int) bool { return gen == m.gen }

func (m *Model) loadTypes() tea.Cmd {
	if m.project == "" {
		m.note = "this session is not scoped to a project, so there is nothing to create an issue in"
		return nil
	}
	if m.search == nil {
		m.note = "there is no Jira connection in this session yet"
		return nil
	}
	ctx, gen := m.begin()
	m.loading, m.stage = true, stageTypes
	return m.reply(loadTypes(ctx, m.search, m.project, gen))
}

func (m *Model) typesFound(msg typesFoundMsg) tea.Cmd {
	if !m.current(msg.gen) {
		return nil
	}
	m.loading = false
	m.types, m.typeCursor, m.typeTop = msg.types, 0, 0
	if len(m.types) == 0 {
		m.note = "nothing in " + m.project + " says which issue types it has; " +
			"the port has no endpoint that lists them, so this asks the issues the account can see"
		return nil
	}
	m.note = ""
	if m.sought != "" {
		sought := m.sought
		m.sought = ""
		return m.chooseType(sought)
	}
	return nil
}

func (m *Model) typesFailed(msg typesFailedMsg) tea.Cmd {
	if !m.current(msg.gen) {
		return nil
	}
	m.loading = false
	m.note = "the issue types in " + m.project + " could not be read"
	return kernel.Fail(msg.err)
}

// chooseType reads the create screen for one issue type. A type the picker has
// not heard of is remembered until the picker answers, and then opened anyway:
// the picker is built from the issues the account can see, so it is a shortcut
// rather than the authority on what the project can create. createmeta is.
func (m *Model) chooseType(id string) tea.Cmd {
	wanted := strings.TrimSpace(id)
	if wanted == "" {
		return nil
	}
	if at := slices.IndexFunc(m.types, func(t jira.IssueType) bool { return t.ID == wanted }); at >= 0 {
		m.typeCursor = at
		return m.openType(m.types[at])
	}
	if m.loading {
		m.sought = wanted
		return nil
	}
	return m.openType(jira.IssueType{ID: wanted, Name: wanted})
}

func (m *Model) openType(typ jira.IssueType) tea.Cmd {
	if m.deps.Jira == nil {
		m.note = "there is no Jira connection in this session yet"
		return nil
	}
	m.chosen = typ
	ctx, gen := m.begin()
	m.loading = true
	cmds := []tea.Cmd{m.reply(loadSchema(ctx, m.deps.Jira, m.cache, m.screenKey(), gen))}
	if !m.haveMe {
		cmds = append(cmds, kernel.Reply(loadAccount(context.WithoutCancel(ctx), m.deps.Jira, gen), m.addr))
	}
	return tea.Batch(cmds...)
}

func (m *Model) screenKey() screen { return screen{project: m.project, issueType: m.chosen.ID} }

func (m *Model) schemaLoaded(msg schemaLoadedMsg) tea.Cmd {
	if !m.current(msg.gen) {
		return nil
	}
	m.loading = false
	m.schema, m.stage = msg.schema, stageFields
	if msg.schema.IssueType.ID != "" {
		m.chosen = msg.schema.IssueType
	}
	m.build()
	m.restoreDraft()
	m.cursor, m.top, m.banner = 0, 0, nil
	return nil
}

func (m *Model) schemaFailed(msg schemaFailedMsg) tea.Cmd {
	if !m.current(msg.gen) {
		return nil
	}
	m.loading = false
	m.note = "the create screen for a " + m.chosen.Name + " in " + m.project + " could not be read"
	return kernel.Fail(msg.err)
}

func (m *Model) accountFound(msg accountMsg) {
	if !m.current(msg.gen) || msg.user.AccountID == "" {
		return
	}
	m.me, m.haveMe = msg.user, true
}

func (m *Model) refresh(purge bool) tea.Cmd {
	if purge {
		m.cache.purge()
		if m.search != nil {
			m.search.Invalidate()
		}
	}
	if m.stage == stageTypes || m.chosen.ID == "" {
		return m.loadTypes()
	}
	return m.openType(m.chosen)
}

// build turns the create screen into widgets, and records what it did not offer
// and why.
func (m *Model) build() {
	m.fields, m.hidden = m.fields[:0], m.hidden[:0]
	loc := m.deps.Caps.Location()
	for i := range m.schema.Fields {
		meta := m.schema.Fields[i]
		if ok, reason := offer(meta, m.project, m.chosen); !ok {
			m.hidden = append(m.hidden, hiddenField{name: meta.Name, reason: reason, required: meta.Required})
			continue
		}
		m.fields = append(m.fields, newField(meta, loc))
	}
	// Required first, then the screen's own order, so that the fields Jira will
	// refuse the issue without are the ones on screen before any scrolling.
	slices.SortStableFunc(m.fields, func(a, b *field) int {
		switch {
		case a.meta.Required == b.meta.Required:
			return 0
		case a.meta.Required:
			return -1
		default:
			return 1
		}
	})
	m.rows.reset()
	m.reindex()
	m.relayout()
}

func (m *Model) restoreDraft() {
	kept := m.drafts.get(m.screenKey())
	if len(kept) == 0 {
		return
	}
	for _, f := range m.fields {
		value, ok := kept[f.id()]
		if !ok {
			continue
		}
		f.text, f.picked, f.rev = value.text, value.picked, f.rev+1
	}
	m.validateAll()
	m.note = "what was typed here before has been put back"
}

func (m *Model) keepDraft() {
	if m.stage == stageFields && m.chosen.ID != "" {
		m.drafts.put(m.screenKey(), m.fields)
	}
}

// --- rows -------------------------------------------------------------------

// rowKind is what one line of the field list is.
type rowKind uint8

const (
	rowField rowKind = iota
	rowNotes
	rowHidden
	rowSubmit
)

type row struct {
	kind rowKind
	at   int
}

// reindex rebuilds the line plan: every field, the note about the fields that
// are not offered, those fields when the note is open, and the row that creates
// the issue.
func (m *Model) reindex() {
	m.index = m.index[:0]
	for i := range m.fields {
		m.index = append(m.index, row{kind: rowField, at: i})
	}
	if len(m.hidden) > 0 {
		m.index = append(m.index, row{kind: rowNotes})
		if m.shown {
			for i := range m.hidden {
				m.index = append(m.index, row{kind: rowHidden, at: i})
			}
		}
	}
	m.index = append(m.index, row{kind: rowSubmit})
}

func (m *Model) rowsHeight() int {
	h := m.height - m.headerHeight() - m.editorHeight()
	return max(h, 1)
}

func (m *Model) headerHeight() int { return 1 + min(len(m.banner), 2) }

// editorHeight is how many lines the open editor takes. It is capped against
// the box the kernel handed the view, so that a pane a terminal cannot fit
// shrinks rather than drawing past the bottom of the frame.
func (m *Model) editorHeight() int {
	want := 0
	switch m.edit {
	case editText:
		want = 2
	case editDoc:
		want = min(max(m.height-6, 3), 14)
	case editChoose:
		want = min(max(m.height-6, 3), 12)
	case editNone:
		return 0
	}
	return min(want, max(m.height-m.headerHeight()-1, 0))
}

func (m *Model) moveTo(at int) {
	if len(m.index) == 0 {
		m.cursor, m.top = 0, 0
		return
	}
	m.cursor = min(max(at, 0), len(m.index)-1)
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
	m.top = min(max(m.top, 0), max(len(m.index)-m.rowsHeight(), 0))
}

// focused is the field under the cursor, and nil when the cursor is on a row
// that is not one.
func (m *Model) focused() *field {
	if m.cursor < 0 || m.cursor >= len(m.index) {
		return nil
	}
	if at := m.index[m.cursor]; at.kind == rowField {
		return m.fields[at.at]
	}
	return nil
}

// --- input ------------------------------------------------------------------

func (m *Model) key(msg tea.KeyPressMsg) tea.Cmd {
	if m.edit != editNone {
		return m.editKey(msg)
	}
	if m.stage == stageTypes {
		return m.typeKey(msg.String())
	}
	return m.listKey(msg.String())
}

func (m *Model) typeKey(stroke string) tea.Cmd {
	switch m.inList[stroke] {
	case actUp:
		m.typeCursor = max(m.typeCursor-1, 0)
	case actDown:
		m.typeCursor = min(m.typeCursor+1, max(len(m.types)-1, 0))
	case actTop:
		m.typeCursor = 0
	case actBottom:
		m.typeCursor = max(len(m.types)-1, 0)
	case actEdit:
		if m.typeCursor < len(m.types) {
			return m.openType(m.types[m.typeCursor])
		}
	case actNone, actPageUp, actPageDown, actClear, actSubmit, actRetype, actToggle, actAccept, actDone:
	}
	m.scrollTypes()
	return nil
}

func (m *Model) scrollTypes() {
	h := max(m.height-2, 1)
	if m.typeCursor < m.typeTop {
		m.typeTop = m.typeCursor
	}
	if m.typeCursor >= m.typeTop+h {
		m.typeTop = m.typeCursor - h + 1
	}
	m.typeTop = min(max(m.typeTop, 0), max(len(m.types)-h, 0))
}

func (m *Model) listKey(stroke string) tea.Cmd {
	switch m.inList[stroke] {
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
		m.moveTo(len(m.index) - 1)
	case actEdit:
		return m.activate()
	case actClear:
		return m.clearFocused()
	case actSubmit:
		return m.submit()
	case actRetype:
		return m.backToTypes()
	case actNone, actToggle, actAccept, actDone:
	}
	return nil
}

func (m *Model) backToTypes() tea.Cmd {
	m.keepDraft()
	m.stage, m.banner, m.note = stageTypes, nil, ""
	m.stop()
	if len(m.types) == 0 {
		return m.loadTypes()
	}
	return nil
}

func (m *Model) clearFocused() tea.Cmd {
	f := m.focused()
	if f == nil {
		return nil
	}
	f.clear()
	if f.kind == kindDoc {
		f.text = ""
	}
	f.problem = f.validate()
	m.keepDraft()
	return nil
}

// activate does whatever the row under the cursor is for.
func (m *Model) activate() tea.Cmd {
	if m.cursor >= len(m.index) {
		return nil
	}
	switch at := m.index[m.cursor]; at.kind {
	case rowField:
		m.openEditor(at.at)
	case rowNotes:
		m.shown = !m.shown
		m.reindex()
		m.clampScroll()
	case rowHidden:
		return kernel.Status(m.hidden[at.at].reason)
	case rowSubmit:
		return m.submit()
	}
	return nil
}

// --- editors ----------------------------------------------------------------

func (m *Model) openEditor(at int) {
	f := m.fields[at]
	m.editing, m.edit = at, f.kind.pane()
	switch m.edit {
	case editChoose:
		m.choices = m.choicesFor(f)
		m.filter.Reset()
		m.pick, m.pickTop = 0, 0
	case editDoc:
		m.area.SetValue(f.text)
	case editText:
		m.input.SetValue(f.text)
		m.input.CursorEnd()
	case editNone:
	}
	m.sizeEditor()
	m.focusEditor()
	m.clampScroll()
}

func (m *Model) focusEditor() {
	switch m.edit {
	case editText:
		_ = m.input.Focus()
	case editDoc:
		_ = m.area.Focus()
	case editChoose:
		_ = m.filter.Focus()
	case editNone:
	}
}

func (m *Model) sizeEditor() {
	width := max(m.width-2, 8)
	m.input.SetWidth(width)
	m.filter.SetWidth(width)
	m.area.SetWidth(width)
	m.area.SetHeight(max(m.editorHeight()-2, 1))
}

// closeEditor puts what was typed on the field and closes the pane. Nothing is
// thrown away here: docs/UX.md asks that text a user typed survives, and a form
// that discarded a body on esc would be the worst place to break that.
func (m *Model) closeEditor() {
	if m.edit == editNone {
		return
	}
	f := m.fields[m.editing]
	switch m.edit {
	case editText:
		f.text, f.rev = m.input.Value(), f.rev+1
	case editDoc:
		f.text, f.rev = m.area.Value(), f.rev+1
	case editChoose:
		f.picked, f.rev = m.chosenOptions(), f.rev+1
	case editNone:
	}
	f.problem = f.validate()
	m.input.Blur()
	m.area.Blur()
	m.filter.Blur()
	m.edit = editNone
	m.keepDraft()
	m.clampScroll()
}

func (m *Model) editKey(msg tea.KeyPressMsg) tea.Cmd {
	stroke := msg.String()
	if m.edit == editChoose {
		return m.chooseKey(msg, stroke)
	}
	switch stroke {
	case "esc":
		m.closeEditor()
		return nil
	case "enter":
		if m.edit == editText {
			m.closeEditor()
			return nil
		}
	case "ctrl+d":
		if m.edit == editDoc {
			m.closeEditor()
			return nil
		}
	}
	// The component's own command is a cursor blink, which would be a timer
	// this view then owns for as long as the editor is open.
	if m.edit == editDoc {
		m.area, _ = m.area.Update(msg)
		return nil
	}
	m.input, _ = m.input.Update(msg)
	return nil
}

func (m *Model) chooseKey(msg tea.KeyPressMsg, stroke string) tea.Cmd {
	visible := m.visibleChoices()
	switch m.inChoose[stroke] {
	case actUp:
		m.pick = max(m.pick-1, 0)
		m.scrollChoices()
		return nil
	case actDown:
		m.pick = min(m.pick+1, max(len(visible)-1, 0))
		m.scrollChoices()
		return nil
	case actPageUp:
		m.pick = max(m.pick-m.chooserHeight(), 0)
		m.scrollChoices()
		return nil
	case actPageDown:
		m.pick = min(m.pick+m.chooserHeight(), max(len(visible)-1, 0))
		m.scrollChoices()
		return nil
	case actToggle:
		m.toggle(visible)
		return nil
	case actAccept:
		if m.fields[m.editing].kind.multiple() {
			m.toggle(visible)
			m.closeEditor()
			return nil
		}
		m.take(visible)
		m.closeEditor()
		return nil
	case actDone:
		m.closeEditor()
		return nil
	case actNone, actTop, actBottom, actEdit, actClear, actSubmit, actRetype:
	}
	m.filter, _ = m.filter.Update(msg)
	m.pick, m.pickTop = 0, 0
	return nil
}

func (m *Model) toggle(visible []int) {
	if m.pick < 0 || m.pick >= len(visible) {
		return
	}
	at := visible[m.pick]
	if !m.fields[m.editing].kind.multiple() {
		for i := range m.choices {
			m.choices[i].on = i == at
		}
		return
	}
	m.choices[at].on = !m.choices[at].on
}

func (m *Model) take(visible []int) {
	if m.pick < 0 || m.pick >= len(visible) {
		return
	}
	for i := range m.choices {
		m.choices[i].on = i == visible[m.pick]
	}
}

func (m *Model) chosenOptions() []jira.Option {
	out := make([]jira.Option, 0, 2)
	for _, c := range m.choices {
		if c.on {
			out = append(out, c.value)
		}
	}
	return out
}

// visibleChoices are the indices the filter leaves.
func (m *Model) visibleChoices() []int {
	needle := strings.ToLower(strings.TrimSpace(m.filter.Value()))
	out := make([]int, 0, len(m.choices))
	for i := range m.choices {
		if needle == "" || strings.Contains(strings.ToLower(m.choices[i].label), needle) {
			out = append(out, i)
		}
	}
	return out
}

func (m *Model) chooserHeight() int { return max(m.editorHeight()-2, 1) }

func (m *Model) scrollChoices() {
	h := m.chooserHeight()
	if m.pick < m.pickTop {
		m.pickTop = m.pick
	}
	if m.pick >= m.pickTop+h {
		m.pickTop = m.pick - h + 1
	}
	m.pickTop = max(m.pickTop, 0)
}

// choicesFor builds a picker from what the site said the field allows. A
// cascading select is flattened, so that the first level and each of its second
// levels are one entry each and the value stored carries both.
func (m *Model) choicesFor(f *field) []choice {
	out := make([]choice, 0, len(f.meta.AllowedValues)+2)
	switch f.kind {
	case kindCascade:
		for _, parent := range f.meta.AllowedValues {
			top := jira.Option{ID: parent.ID, Label: parent.Label}
			out = append(out, choice{label: parent.Label, value: top})
			for _, child := range parent.Children {
				value := top
				value.Children = []jira.Option{{ID: child.ID, Label: child.Label}}
				out = append(out, choice{label: parent.Label + " / " + child.Label, value: value})
			}
		}
	case kindUser, kindUsers:
		out = append(out, m.userChoices(f)...)
	default:
		for _, option := range f.meta.AllowedValues {
			out = append(out, choice{label: option.Label, value: option})
		}
	}
	for i := range out {
		out[i].on = slices.ContainsFunc(f.picked, func(p jira.Option) bool {
			return p.ID == out[i].value.ID && cascadeLabel(p) == cascadeLabel(out[i].value)
		})
	}
	return out
}

// userChoices are the accounts a person picker can offer. The field's own list
// comes first where it has one; otherwise the only account this client can name
// without a user-search endpoint is the authenticated one, plus whatever has
// already been chosen.
func (m *Model) userChoices(f *field) []choice {
	out := make([]choice, 0, len(f.meta.AllowedValues)+2)
	seen := make(map[string]bool, 4)
	add := func(option jira.Option) {
		if option.ID == "" || seen[option.ID] {
			return
		}
		seen[option.ID] = true
		out = append(out, choice{label: option.Label, value: option})
	}
	for _, option := range f.meta.AllowedValues {
		add(option)
	}
	if m.haveMe {
		add(userOption(m.me))
	}
	for _, option := range f.picked {
		add(option)
	}
	return out
}

// --- submitting -------------------------------------------------------------

func (m *Model) submit() tea.Cmd {
	if m.stage != stageFields {
		return nil
	}
	if m.deps.Jira == nil {
		return kernel.Warn("there is no Jira connection in this session yet")
	}
	if m.busy {
		return nil
	}
	m.banner = nil
	if bad := m.validateAll(); bad > 0 {
		if at := m.firstProblem(); at >= 0 {
			m.moveTo(at)
		}
		return kernel.Warn(plural(bad, "field") + " on this form still need attention")
	}
	if missing := m.missingRequired(); len(missing) > 0 {
		m.banner = append(m.banner, "Jira requires "+strings.Join(missing, ", ")+", which this form cannot fill in")
	}
	in := m.issueInput()
	ctx, gen := m.begin()
	m.busy = true
	return m.reply(create(ctx, m.deps.Jira, in, gen))
}

// missingRequired names the fields Jira requires that the form does not offer.
// It is said out loud rather than left to the refusal, because the user cannot
// act on it from here and the create is going to fail.
func (m *Model) missingRequired() []string {
	out := make([]string, 0, 2)
	for _, h := range m.hidden {
		if h.required {
			out = append(out, h.name)
		}
	}
	return out
}

// issueInput assembles what will be created. The fields the port carries in
// their own right are read out of their widgets by the system name the site
// gave them; everything else travels by field id in the FieldSet.
func (m *Model) issueInput() jira.IssueInput {
	in := jira.IssueInput{ProjectKey: m.project, IssueTypeID: m.chosen.ID}
	values := make(map[string]jira.FieldValue, len(m.fields))
	for _, f := range m.fields {
		if f.empty() {
			continue
		}
		if m.assign(&in, f) {
			continue
		}
		if value, ok := f.value(); ok {
			values[f.id()] = value
		}
	}
	if len(values) > 0 {
		in.Fields = jira.NewFieldSet(values)
	}
	return in
}

// assign puts a field on the input itself where the port has a slot for it, and
// reports whether it did.
func (m *Model) assign(in *jira.IssueInput, f *field) bool {
	switch f.meta.Field.Schema.System {
	case "summary":
		in.Summary = strings.TrimSpace(f.text)
	case "description":
		doc, err := f.document()
		if err != nil {
			return false
		}
		in.Description = doc
	case "parent":
		in.ParentKey = strings.TrimSpace(f.text)
	case "labels":
		in.Labels = f.labels()
	case "assignee":
		if len(f.picked) == 0 {
			return false
		}
		in.Assignee = f.picked[0].ID
	default:
		return false
	}
	return true
}

func (m *Model) created(msg createdMsg) tea.Cmd {
	if !m.current(msg.gen) {
		return nil
	}
	m.busy = false
	m.drafts.clear(m.screenKey())
	for _, f := range m.fields {
		f.clear()
	}
	m.rows.reset()
	return tea.Sequence(
		kernel.Pop(),
		kernel.Broadcast(kernel.RefreshMsg{}),
		kernel.Status(msg.issue.Key+" created"),
	)
}

func (m *Model) createFailed(msg createFailedMsg) tea.Cmd {
	if !m.current(msg.gen) {
		return nil
	}
	m.busy = false
	var invalid *jira.ValidationError
	if errors.As(msg.err, &invalid) {
		m.applyValidationError(invalid)
		m.rows.reset()
		return kernel.Warn("Jira refused this issue; the fields it named say why")
	}
	return kernel.Fail(msg.err)
}

// --- mouse ------------------------------------------------------------------

func (m *Model) click(msg tea.MouseClickMsg) tea.Cmd {
	if msg.Button != tea.MouseLeft {
		return nil
	}
	if m.stage == stageTypes {
		return m.clickType(msg)
	}
	if m.edit == editChoose {
		return m.clickChoice(msg)
	}
	if m.edit != editNone {
		return nil
	}
	for i := m.top; i < min(m.top+m.rowsHeight(), len(m.index)); i++ {
		zone := m.rowZone(i)
		if !m.zones.Hit(zone, msg) {
			continue
		}
		if m.clicks.Double(zone) {
			m.moveTo(i)
			return m.activate()
		}
		m.moveTo(i)
		return nil
	}
	return nil
}

func (m *Model) clickType(msg tea.MouseClickMsg) tea.Cmd {
	for i := m.typeTop; i < min(m.typeTop+max(m.height-2, 1), len(m.types)); i++ {
		zone := m.typeZone(i)
		if !m.zones.Hit(zone, msg) {
			continue
		}
		if m.clicks.Double(zone) {
			m.typeCursor = i
			return m.openType(m.types[i])
		}
		m.typeCursor = i
		m.scrollTypes()
		return nil
	}
	return nil
}

func (m *Model) clickChoice(msg tea.MouseClickMsg) tea.Cmd {
	visible := m.visibleChoices()
	for i := m.pickTop; i < min(m.pickTop+m.chooserHeight(), len(visible)); i++ {
		zone := m.choiceZone(i)
		if !m.zones.Hit(zone, msg) {
			continue
		}
		if m.clicks.Double(zone) {
			m.pick = i
			return m.chooseKey(tea.KeyPressMsg{Code: tea.KeyEnter}, "enter")
		}
		m.pick = i
		m.scrollChoices()
		return nil
	}
	return nil
}

func (m *Model) wheel(msg tea.MouseWheelMsg) {
	step := 0
	switch msg.Button {
	case tea.MouseWheelUp:
		step = -3
	case tea.MouseWheelDown:
		step = 3
	default:
		return
	}
	switch {
	case m.stage == stageTypes:
		m.typeTop = min(max(m.typeTop+step, 0), max(len(m.types)-max(m.height-2, 1), 0))
	case m.edit == editChoose:
		m.pickTop = min(max(m.pickTop+step, 0), max(len(m.visibleChoices())-m.chooserHeight(), 0))
	default:
		m.top += step
		m.clampScroll()
	}
}

func plural(n int, what string) string {
	if n == 1 {
		return "1 " + what
	}
	return strconv.Itoa(n) + " " + what + "s"
}
