package release

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

var (
	_ kernel.View        = (*Model)(nil)
	_ kernel.KeyCapturer = (*Model)(nil)
	_ kernel.Blocker     = (*Model)(nil)
	_ kernel.Addressed   = (*Model)(nil)
)

// NewVersionMsg, EditVersionMsg, ArchiveMsg and ReleaseMsg are what the palette
// sends this view. They are broadcasts because the palette knows which command
// was run and never which version is under the cursor.
type (
	// NewVersionMsg opens the editor on a version that does not exist yet.
	NewVersionMsg struct{}
	// EditVersionMsg opens the editor on the version under the cursor.
	EditVersionMsg struct{}
	// ArchiveMsg archives the version under the cursor, or unarchives it.
	ArchiveMsg struct{}
	// ReleaseMsg opens the release flow over the version under the cursor.
	ReleaseMsg struct{}
)

// mode is what the list is doing.
type mode uint8

const (
	browsing mode = iota
	editing
)

// Model is the versions list.
type Model struct {
	deps    kernel.Deps
	acts    map[string]action
	inEdit  map[string]action
	pending bool

	versions []jira.Version
	// cells are the versions drawn out, one row's worth each, and day is the
	// date they were drawn against.
	cells   []rowCells
	day     jira.Date
	loading bool
	loaded  bool
	failure error
	what    string
	checked time.Time

	// counting is the id of the version whose open issues are being counted,
	// and "" when none is. A release is the only thing that needs the number,
	// so it is read on the way into the flow rather than per row.
	counting string

	mode   mode
	form   form
	saving bool

	cursor, top   int
	width, height int

	gen    int
	cancel context.CancelFunc
	addr   kernel.Addr

	styles *styles
	rows   *memo[rowKey]
	lay    layout
	head   string
	sum    string
	sumAt  summaryKey
	lines  []string

	zones  widget.Zoner
	clicks *widget.Clicks
}

// New builds the versions list. It draws before anything is asked of the site,
// because a first frame is drawn without Init ever being called.
func New(d kernel.Deps) kernel.View {
	m := &Model{deps: d, addr: kernel.NewAddr()}
	if m.deps.Theme == nil {
		m.deps.Theme = kernel.NewTheme(kernel.ThemeAuto, true, kernel.UnicodeGlyphs())
	}
	m.acts, m.inEdit = defaultKeys().tables()
	m.styles = newStyles(m.deps.Theme)
	m.rows = newMemo[rowKey](rowCacheLimit)
	m.zones = widget.NewZoner(d.Zones)
	m.clicks = widget.NewClicks(d.Now)
	m.form = newForm()
	m.relayout()
	return m
}

// Init reads the project's versions, and only where there is a project and a
// site to read them from.
func (m *Model) Init() tea.Cmd {
	if m.deps.Jira == nil || strings.TrimSpace(m.deps.Project) == "" {
		return nil
	}
	return m.load()
}

// WantsRawKeys is true while a version is being typed. Without it the kernel
// matches its own bindings first, so a name loses every digit, q quits the
// program out from under the typing and esc never reaches the editor.
func (m *Model) WantsRawKeys() bool { return m.mode == editing }

// BlocksClose refuses to throw away a version being typed. The kernel asks
// before quitting, before going back and before switching to another root, and
// shows this instead.
func (m *Model) BlocksClose() (string, bool) {
	if m.mode != editing || m.form.blank() {
		return "", false
	}
	return "this version has not been saved — ctrl+s saves it, esc throws it away", true
}

// Addr is where the kernel delivers the versions, the counts and the saves this
// list asked for, whatever has since been pushed over it.
func (m *Model) Addr() kernel.Addr { return m.addr }

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
		m.rows.reset()
		m.head, m.sum = "", ""
		m.relayout()

	case kernel.CapabilitiesMsg:
		m.deps.Caps = msg.Caps
		m.rows.reset()
		m.sum = ""

	case kernel.ProjectMsg:
		cmd = m.reproject(msg.Project)

	case kernel.RefreshMsg:
		cmd = m.load()

	case versionsMsg:
		m.tookVersions(msg)

	case savedMsg:
		cmd = m.tookSave(msg)

	case countedMsg:
		cmd = m.tookCount(msg)

	case releasedMsg:
		m.tookRelease(msg)

	case failedMsg:
		cmd = m.failed(msg)

	case NewVersionMsg:
		cmd = m.startCreate()

	case EditVersionMsg:
		cmd = m.startEdit()

	case ArchiveMsg:
		cmd = m.toggleArchive()

	case ReleaseMsg:
		cmd = m.startRelease()

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
	m.rows.reset()
	m.sum = ""
	m.relayout()
	m.form.resize(w)
	m.clampScroll()
}

// focus keeps the cursor out of an editor nobody is typing into. It does not
// let go of a read: losing the keys is not being closed, and the kernel blurs a
// view it is pushing the flow over as well as one it is switching away from.
func (m *Model) focus(on bool) {
	if on && m.mode == editing {
		m.form.focus()
		return
	}
	m.form.blur()
}

// reproject throws the versions away and reads the new project's. A version
// belongs to one project, so keeping the old rows on screen would be showing
// somebody another project's releases.
func (m *Model) reproject(project string) tea.Cmd {
	if project == m.deps.Project {
		return nil
	}
	m.deps.Project = project
	m.stop()
	m.versions, m.loaded, m.failure = nil, false, nil
	m.cursor, m.top = 0, 0
	m.mode, m.saving, m.counting = browsing, false, ""
	m.sum = ""
	m.rebuildCells()
	return m.load()
}

// --- reading ----------------------------------------------------------------

// begin cancels whatever is in flight and opens a context for its replacement.
// The generation it returns is what a landing answer is checked against, so an
// answer to a question that has since changed is dropped rather than drawn.
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
	m.loading, m.saving, m.counting = false, false, ""
}

// reply puts this list's address on a command, so what it asked for comes back
// here rather than to whatever the stack has on top by then — the release flow
// usually.
func (m *Model) reply(cmd tea.Cmd) tea.Cmd {
	return kernel.Reply(withCancel(m.cancel, cmd), m.addr)
}

func (m *Model) load() tea.Cmd {
	if m.deps.Jira == nil || strings.TrimSpace(m.deps.Project) == "" {
		return nil
	}
	ctx, gen := m.begin()
	m.loading, m.failure = true, nil
	m.sum = ""
	return m.reply(loadVersions(ctx, m.deps.Jira, m.deps.Project, gen))
}

func (m *Model) current(gen int) bool { return gen == m.gen }

// tookVersions replaces the rows and keeps the reader's place: the cursor goes
// back onto the version it was on, by id, rather than onto whatever row number
// it happened to be.
func (m *Model) tookVersions(msg versionsMsg) {
	if !m.current(msg.gen) {
		return
	}
	under := m.selectedID()
	m.loading, m.loaded, m.failure = false, true, nil
	m.checked = m.now()
	m.versions = msg.versions
	m.head, m.sum = "", ""
	m.relayout()
	m.rebuildCells()
	m.moveOnto(under)
}

func (m *Model) tookSave(msg savedMsg) tea.Cmd {
	if !m.current(msg.gen) {
		return nil
	}
	m.saving = false
	m.mode = browsing
	m.form.close()
	m.put(msg.version)
	m.relayout()
	m.rebuildCells()
	m.moveOnto(msg.version.ID)
	verb := "saved"
	if msg.created {
		verb = "created"
	}
	return kernel.Status(msg.version.Name + " " + verb + ".")
}

// tookCount pushes the flow, which is the only thing that wanted the number.
// The count travels into it rather than being read there, so the flow opens on
// the decision instead of on a wait.
func (m *Model) tookCount(msg countedMsg) tea.Cmd {
	if !m.current(msg.gen) || msg.id != m.counting {
		return nil
	}
	m.counting = ""
	version, ok := m.byID(msg.id)
	if !ok {
		return nil
	}
	open := msg.open
	version.Unresolved = &open
	m.put(version)
	m.rebuildCells()
	return kernel.Push(FlowViewID, "Release "+version.Name,
		NewFlow(m.deps, version, open, m.moveTargets(version.ID)))
}

// tookRelease patches the row the flow shipped. It is a broadcast rather than a
// refetch, because a refetch would throw the reader's place away for one row
// that is already in hand.
func (m *Model) tookRelease(msg releasedMsg) {
	m.put(msg.version)
	m.sum = ""
	m.rebuildCells()
	m.moveOnto(msg.version.ID)
}

// failed keeps the refusal in the pane as well as on the status line: a status
// line is overwritten by the next thing that happens, and a list that is empty
// because the site said no has to keep saying so.
func (m *Model) failed(msg failedMsg) tea.Cmd {
	if !m.current(msg.gen) {
		return nil
	}
	m.loading, m.saving, m.counting = false, false, ""
	m.failure, m.what = msg.err, msg.what
	m.sum = ""
	return kernel.Fail(msg.err)
}

// put replaces a version by id, or appends it. A create comes back with an id
// the list has never seen and belongs at the end, which is where the project
// put it.
func (m *Model) put(v jira.Version) {
	for i := range m.versions {
		if m.versions[i].ID == v.ID {
			// A count already read stays read: the write that came back knows
			// nothing about what is open, and nil would say nobody had asked.
			if v.Unresolved == nil {
				v.Unresolved = m.versions[i].Unresolved
			}
			m.versions[i] = v
			return
		}
	}
	m.versions = append(m.versions, v)
}

func (m *Model) byID(id string) (jira.Version, bool) {
	for i := range m.versions {
		if m.versions[i].ID == id {
			return m.versions[i], true
		}
	}
	return jira.Version{}, false
}

// moveTargets are the versions the open issues on one version could move to:
// the ones that are neither this one, nor already released, nor archived.
// Moving open work onto a version that has shipped is not somewhere to put it.
func (m *Model) moveTargets(exclude string) []jira.Version {
	out := make([]jira.Version, 0, len(m.versions))
	for i := range m.versions {
		v := m.versions[i]
		if v.ID == exclude || v.Released || v.Archived {
			continue
		}
		out = append(out, v)
	}
	return out
}

// --- actions ----------------------------------------------------------------

func (m *Model) startCreate() tea.Cmd {
	if m.saving {
		return nil
	}
	if m.deps.Jira == nil {
		return kernel.Warn("there is no Jira connection in this session")
	}
	if strings.TrimSpace(m.deps.Project) == "" {
		return kernel.Warn("a version belongs to a project, and this session is not scoped to one")
	}
	m.mode = editing
	m.form.open(jira.Version{}, m.width)
	m.sum = ""
	return nil
}

func (m *Model) startEdit() tea.Cmd {
	if m.saving {
		return nil
	}
	v, ok := m.selected()
	if !ok {
		return nil
	}
	if m.deps.Jira == nil {
		return kernel.Warn("there is no Jira connection in this session")
	}
	m.mode = editing
	m.form.open(v, m.width)
	m.sum = ""
	return nil
}

// toggleArchive flips the one flag and sends the version back whole. Archiving
// is reversible by the same key, which is why it is not put behind a confirm:
// nothing is lost and the status line says which way it went.
func (m *Model) toggleArchive() tea.Cmd {
	if m.saving || m.mode == editing {
		return nil
	}
	v, ok := m.selected()
	if !ok {
		return nil
	}
	if m.deps.Jira == nil {
		return kernel.Warn("there is no Jira connection in this session")
	}
	in := updateOf(v)
	archived := !v.Archived
	in.Archived = &archived
	ctx, gen := m.begin()
	m.saving = true
	m.sum = ""
	return m.reply(saveVersion(ctx, m.deps.Jira, in, false, gen))
}

// startRelease reads what is open on the version and pushes the flow with the
// answer. It never releases anything: the flow shows the count, offers the three
// choices Jira's own API skips, and takes a confirm.
func (m *Model) startRelease() tea.Cmd {
	if m.saving || m.mode == editing || m.counting != "" {
		return nil
	}
	v, ok := m.selected()
	if !ok {
		return nil
	}
	switch {
	case m.deps.Jira == nil:
		return kernel.Warn("there is no Jira connection in this session")
	case v.Released:
		return kernel.Warn(v.Name + " has already been released")
	case v.Archived:
		return kernel.Warn(v.Name + " is archived; unarchive it with A before releasing it")
	}
	ctx, gen := m.begin()
	m.counting = v.ID
	m.sum = ""
	return m.reply(countOpen(ctx, m.deps.Jira, v.ID, gen))
}

func (m *Model) save() tea.Cmd {
	in, err := m.form.versionInput(m.deps.Project)
	if err != "" {
		m.form.problem = err
		return nil
	}
	ctx, gen := m.begin()
	m.saving, m.form.problem = true, ""
	m.sum = ""
	return m.reply(saveVersion(ctx, m.deps.Jira, in, in.ID == "", gen))
}

func (m *Model) cancelEdit() tea.Cmd {
	m.mode = browsing
	m.form.close()
	m.sum = ""
	return nil
}

// --- keys -------------------------------------------------------------------

func (m *Model) key(msg tea.KeyPressMsg) tea.Cmd {
	if m.saving {
		return nil
	}
	if m.mode == editing {
		return m.editKey(msg)
	}
	stroke := msg.String()
	if m.pending {
		m.pending = false
		if stroke == "g" {
			m.moveTo(0)
			return nil
		}
	}
	switch m.acts[stroke] {
	case actUp:
		m.moveTo(m.cursor - 1)
	case actDown:
		m.moveTo(m.cursor + 1)
	case actPageUp:
		m.moveTo(m.cursor - m.rowsHeight())
	case actPageDown:
		m.moveTo(m.cursor + m.rowsHeight())
	case actGo:
		m.pending = true
	case actTop:
		m.moveTo(0)
	case actBottom:
		m.moveTo(len(m.versions) - 1)
	case actRelease:
		return m.startRelease()
	case actNew:
		return m.startCreate()
	case actEdit:
		return m.startEdit()
	case actArchive:
		return m.toggleArchive()
	case actNone, actNextField, actPrevField, actSave, actCancel:
	}
	return nil
}

// editKey takes the keys while a version is being typed. Everything the table
// does not claim is text, which is why the list claims raw keys here.
func (m *Model) editKey(msg tea.KeyPressMsg) tea.Cmd {
	switch m.inEdit[msg.String()] {
	case actNextField:
		m.form.move(1)
		return nil
	case actPrevField:
		m.form.move(-1)
		return nil
	case actSave:
		return m.save()
	case actCancel:
		return m.cancelEdit()
	case actNone, actUp, actDown, actPageUp, actPageDown, actGo, actTop, actBottom,
		actRelease, actNew, actEdit, actArchive:
	}
	m.form.typed(msg)
	return nil
}

// --- selection --------------------------------------------------------------

func (m *Model) selected() (jira.Version, bool) {
	if m.cursor < 0 || m.cursor >= len(m.versions) {
		return jira.Version{}, false
	}
	return m.versions[m.cursor], true
}

func (m *Model) selectedID() string {
	if v, ok := m.selected(); ok {
		return v.ID
	}
	return ""
}

func (m *Model) moveTo(at int) {
	n := len(m.versions)
	if n == 0 {
		m.cursor, m.top = 0, 0
		return
	}
	m.cursor = min(max(at, 0), n-1)
	m.scrollToCursor()
}

// moveOnto puts the cursor back on a version by id. A row number is not the
// same place after a create, a rename or a refetch.
func (m *Model) moveOnto(id string) {
	if id == "" {
		m.clampScroll()
		return
	}
	for i := range m.versions {
		if m.versions[i].ID == id {
			m.moveTo(i)
			return
		}
	}
	m.clampScroll()
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
	m.top = min(max(m.top, 0), max(len(m.versions)-m.rowsHeight(), 0))
}

// rowsHeight is how many rows fit under the summary line and the caption, less
// whatever the editor is taking below them.
func (m *Model) rowsHeight() int {
	h := m.height - headHeight - m.form.height(m.mode == editing)
	return max(h, 1)
}

func (m *Model) now() time.Time {
	if m.deps.Now == nil {
		return time.Time{}
	}
	return m.deps.Now()
}

// --- mouse ------------------------------------------------------------------

// click selects the row under the pointer, and a double-click on it does what
// enter does. The pair is timed rather than read as two clicks on one row,
// because pointing at a version and pointing at it again a minute later is not
// a gesture that should open a release.
func (m *Model) click(msg tea.MouseClickMsg) tea.Cmd {
	if msg.Button != tea.MouseLeft || m.mode == editing {
		return nil
	}
	for i := m.top; i < min(m.top+m.rowsHeight(), len(m.versions)); i++ {
		id := rowZone(m.versions[i].ID)
		if !m.zones.Hit(id, msg) {
			continue
		}
		m.moveTo(i)
		if m.clicks.Double(id) {
			return m.startRelease()
		}
		return nil
	}
	return nil
}

// wheel scrolls the rows without moving the selection, which is what a wheel
// does everywhere else.
func (m *Model) wheel(msg tea.MouseWheelMsg) {
	switch msg.Button {
	case tea.MouseWheelUp:
		m.top -= widget.WheelStep
	case tea.MouseWheelDown:
		m.top += widget.WheelStep
	default:
		return
	}
	m.clicks.Forget()
	m.clampScroll()
}

// --- the editor -------------------------------------------------------------

// field is one line of the editor. The four are what VersionInput carries that
// a person types; archived is a key of its own and released cannot be set here
// at all.
type field uint8

const (
	fieldName field = iota
	fieldDescription
	fieldStart
	fieldRelease
	fieldCount
)

var fieldLabels = [fieldCount]string{"name", "description", "starts", "releases"}

// form is the editor: four lines, one of them being typed into.
type form struct {
	id      string
	name    string
	values  [fieldCount]string
	at      field
	input   textinput.Model
	problem string
	open_   bool
}

func newForm() form {
	ti := textinput.New()
	ti.Prompt = "> "
	return form{input: ti}
}

func (f *form) open(v jira.Version, width int) {
	f.id, f.name, f.open_, f.problem = v.ID, v.Name, true, ""
	f.values = [fieldCount]string{v.Name, v.Description, v.StartDate.String(), v.ReleaseDate.String()}
	f.at = fieldName
	f.input.SetValue(f.values[f.at])
	f.resize(width)
	f.focus()
}

func (f *form) close() {
	f.open_ = false
	f.values = [fieldCount]string{}
	f.id, f.name, f.problem = "", "", ""
	f.input.Reset()
	f.blur()
}

func (f *form) focus() { _ = f.input.Focus() }
func (f *form) blur()  { f.input.Blur() }

func (f *form) resize(width int) {
	f.input.SetWidth(max(width-labelWidth-inputChrome, 8))
}

// move stores what is typed and opens the next field. The values live in the
// form rather than in the input, which holds exactly one of them at a time.
func (f *form) move(by int) {
	f.values[f.at] = f.input.Value()
	next := int(f.at) + by
	switch {
	case next < 0:
		next = int(fieldCount) - 1
	case next >= int(fieldCount):
		next = 0
	}
	f.at = field(next)
	f.input.SetValue(f.values[f.at])
}

func (f *form) typed(msg tea.KeyPressMsg) {
	// The input's own command is a cursor blink, which is a timer this view
	// would then own for as long as the editor is up. Dropping it costs a
	// blinking block and keeps every frame reproducible.
	f.input, _ = f.input.Update(msg)
	f.values[f.at] = f.input.Value()
}

func (f *form) blank() bool {
	for i := range f.values {
		if i == int(f.at) {
			if strings.TrimSpace(f.input.Value()) != "" {
				return false
			}
			continue
		}
		if strings.TrimSpace(f.values[i]) != "" {
			return false
		}
	}
	return true
}

// height is what the editor takes off the rows: a line per field, the line
// being typed into, and the line a refusal sits on.
func (f *form) height(showing bool) int {
	if !showing {
		return 0
	}
	h := int(fieldCount) + 1
	if f.problem != "" {
		h++
	}
	return h
}

// input builds what the site is sent, or says in one sentence why it cannot be.
// Both dates are read with the port's own parser, so a typed one is refused
// here rather than turned into a day somewhere east of the reader.
func (f *form) versionInput(project string) (jira.VersionInput, string) {
	values := f.values
	values[f.at] = f.input.Value()

	name := strings.TrimSpace(values[fieldName])
	if name == "" {
		return jira.VersionInput{}, "a version needs a name"
	}
	start, problem := readDay(values[fieldStart], fieldLabels[fieldStart])
	if problem != "" {
		return jira.VersionInput{}, problem
	}
	release, problem := readDay(values[fieldRelease], fieldLabels[fieldRelease])
	if problem != "" {
		return jira.VersionInput{}, problem
	}
	if !start.IsZero() && !release.IsZero() && release.Before(start) {
		return jira.VersionInput{}, "a version cannot be released before it starts"
	}
	in := jira.VersionInput{
		ID:          f.id,
		Name:        name,
		Description: strings.TrimSpace(values[fieldDescription]),
		StartDate:   start,
		ReleaseDate: release,
	}
	if in.ID == "" {
		in.ProjectKey = project
	}
	return in, ""
}

func readDay(typed, what string) (jira.Date, string) {
	typed = strings.TrimSpace(typed)
	if typed == "" {
		return jira.Date{}, ""
	}
	day, err := jira.ParseDate(typed)
	if err != nil {
		return jira.Date{}, what + " has to be a date like " + exampleDay + ", not " + strconv.Quote(typed)
	}
	return day, ""
}

// exampleDay is the shape Jira writes a version's dates in, spelt out because a
// refused date is no use without one.
const exampleDay = "2026-03-05"
