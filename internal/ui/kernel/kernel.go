package kernel

import (
	"context"
	"errors"
	"fmt"
	"maps"
	"slices"
	"strconv"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"
	zone "github.com/lrstanley/bubblezone/v2"

	"github.com/varijkapil13/saral/internal/app"
	"github.com/varijkapil13/saral/pkg/jira"
)

// MinWidth and MinHeight are the smallest terminal the UI draws in. Below it
// the kernel says so in words rather than drawing a broken frame.
const (
	MinWidth  = 80
	MinHeight = 20
)

// PaletteViewID is the view the command palette registers itself as. The kernel
// binds ctrl+k to opening it and says so when nothing has registered it.
const PaletteViewID = "palette"

// SetupViewID is the view that collects a profile. A caller that finds nothing
// configured opens it with WithInitialView; naming it here rather than importing
// the package keeps the composition root free of any view.
const SetupViewID = "onboarding"

// KeyCapturer is the optional interface a view implements while it is taking
// typing — a filter, a form field, the command palette. While it says yes, every
// key except ctrl+c goes to it untouched, because a view that cannot receive the
// letter q or the escape key is not one a user can type into.
//
// It is asked on every key rather than latched, so a view answers for the state
// it is in rather than remembering to tell the kernel when it changes.
type KeyCapturer interface {
	WantsRawKeys() bool
}

// Blocker is the optional interface a view implements when it is holding
// something the user would lose — a draft, an in-flight write. The kernel asks
// before anything that would discard the view: quitting, going back, and
// switching to another view. It shows the reason instead.
//
// Going back asks the view being popped. Quitting and switching root view ask
// every entry on the stack, because both throw all of it away and the one
// holding a draft is often underneath.
type Blocker interface {
	BlocksClose() (reason string, blocked bool)
}

type stackEntry struct {
	spec ViewSpec
	view View
}

type chromeKey struct {
	width    int
	themeGen int
	capsGen  int
	savedGen int
	// keysGen is what the focused view answers when asked which of its keys work
	// right now. It is a number rather than the set because this key is compared
	// with ==, and a KeySet holds slices.
	keysGen   int
	rootID    string
	topID     string
	title     string
	project   string
	status    string
	help      bool
	depth     int
	palette   bool
	capturing bool
	prefixed  bool
}

type chromeCache struct {
	key    chromeKey
	header string
	footer string

	bodyW, bodyH int
	bodySet      bool
	body         lipgloss.Style
}

var _ tea.Model = Model{}

// Model is the root Bubble Tea model: the view stack, the footer, the help
// overlay and the global keys. Views are reached through the registry, never
// through a switch here.
type Model struct {
	deps  Deps
	keys  GlobalKeys
	roots []ViewSpec
	stack []stackEntry

	// live keeps a root view alive after the user switches away from it, so
	// that coming back lands on the same row with the same filter. The kernel
	// is the only writer, and Bubble Tea runs it on one goroutine.
	live map[string]View

	// chrome memoizes the header and footer, which are otherwise rebuilt on
	// every frame and would put a few hundred allocations under every scroll.
	chrome *chromeCache

	// capsProbed records that a probe has actually answered. Without it the zero
	// jira.Capabilities is indistinguishable from a site where the token may do
	// nothing, and the kernel invents a denial for a question never asked.
	capsProbed bool

	// capsSeq tags each probe so that an answer overtaken by a newer one is
	// dropped; scopeSeq is the probe a project switch is waiting on, zero when
	// none is — which is also the sequence the startup probe carries.
	capsSeq  int
	scopeSeq int

	// prefix holds the go-to key while it waits for the one that completes it.
	// It is buffered rather than forwarded, because a view that spends g on its
	// own gestures must not be left half way through one when the kernel takes
	// the digit that follows.
	prefix    tea.KeyPressMsg
	prefixSet bool

	width, height int
	capsGen       int
	savedGen      int
	status        string
	statusLevel   StatusLevel
	showHelp      bool
	mouse         bool
	zonePrefix    string
	quitting      bool
	initialView   string
}

// Option configures the root model.
type Option func(*Model)

// WithSize starts the model at a known size, which is what the benchmark and
// the golden tests need since there is no terminal to ask.
func WithSize(w, h int) Option {
	return func(m *Model) { m.width, m.height = w, h }
}

// WithInitialView opens a specific view rather than the first allocated slot.
func WithInitialView(id string) Option {
	return func(m *Model) { m.initialView = id }
}

// WithMouse turns mouse reporting on or off. Off is what a user who relies on
// terminal text selection asks for.
func WithMouse(enabled bool) Option {
	return func(m *Model) { m.mouse = enabled }
}

// InitialViewOf reports which view a set of options would open, which is how a
// composition root tests its own routing without building a program.
func InitialViewOf(opts ...Option) string {
	var m Model
	for _, opt := range opts {
		opt(&m)
	}
	return m.initialView
}

// WithGlobalKeys replaces the global keymap.
func WithGlobalKeys(g GlobalKeys) Option {
	return func(m *Model) { m.keys = g }
}

// New builds the root model from whatever registered itself.
//
// It fails rather than starts when a registration was malformed: a view that
// silently did not register is far harder to diagnose later than a message at
// startup.
func New(d Deps, opts ...Option) (Model, error) {
	if errs := RegistrationErrors(); len(errs) > 0 {
		return Model{}, fmt.Errorf("kernel: %d bad registration(s): %w", len(errs), errors.Join(errs...))
	}
	if d.Now == nil {
		d.Now = time.Now
	}
	if d.Theme == nil {
		d.Theme = NewTheme(ThemeAuto, true, UnicodeGlyphs())
	}
	m := Model{
		deps:   d,
		keys:   DefaultGlobalKeys(),
		mouse:  true,
		live:   make(map[string]View),
		chrome: &chromeCache{},
	}
	for _, opt := range opts {
		opt(&m)
	}
	if m.deps.Zones == nil {
		m.deps.Zones = zone.New()
	}
	// A manager is enabled from birth, so without this every view still writes
	// markers into a frame that nothing scans them back out of.
	m.deps.Zones.SetEnabled(m.mouse)
	m.zonePrefix = m.deps.Zones.NewPrefix()
	m.roots = Views()

	spec, ok := m.startView()
	if !ok {
		return m, nil
	}
	root := spec.New(m.deps)
	m.live[spec.ID] = root
	m.stack = []stackEntry{{spec: spec, view: root}}
	return m, nil
}

func (m Model) startView() (ViewSpec, bool) {
	if m.initialView != "" {
		spec, ok := LookupView(m.initialView)
		if ok && m.available(spec) {
			return spec, true
		}
	}
	for _, spec := range m.roots {
		if spec.Slot > 0 && m.available(spec) {
			return spec, true
		}
	}
	for _, spec := range m.roots {
		if m.available(spec) {
			return spec, true
		}
	}
	return ViewSpec{}, false
}

func (m Model) available(spec ViewSpec) bool {
	return spec.Requires == "" || m.deps.Caps.Allows(spec.Requires)
}

// Init starts the model. It asks the terminal for its background colour so the
// theme can settle before the second frame; the first frame is already drawn.
//
// It also asks the site what this token can do here: until that answers, every
// capability-gated view is hidden with nothing to say about why.
func (m Model) Init() tea.Cmd {
	cmds := []tea.Cmd{tea.RequestBackgroundColor, m.probeAt(m.capsSeq)}
	if len(m.stack) > 0 {
		cmds = append(cmds, m.top().view.Init())
	}
	return tea.Batch(cmds...)
}

func (m Model) top() stackEntry { return m.stack[len(m.stack)-1] }

// Update routes a message, then re-sizes if the status line appeared or went
// away — it costs the focused view a row, and a view that is not told loses its
// bottom line for as long as the message is up.
func (m Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	had := m.status != ""
	next, cmd := m.route(msg)
	updated, ok := next.(Model)
	if !ok || (updated.status != "") == had {
		return next, cmd
	}
	return updated, tea.Batch(cmd, updated.resizeAll())
}

func (m Model) route(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width, m.height = msg.Width, msg.Height
		return m, m.resizeAll()

	case tea.BackgroundColorMsg:
		if m.deps.Theme.Mode != ThemeAuto {
			return m, nil
		}
		return m.retheme(NewTheme(ThemeAuto, msg.IsDark(), m.deps.Theme.Glyphs))

	case ThemeMsg:
		return m.retheme(msg.Theme)

	case tea.KeyPressMsg:
		return m.handleKey(msg)

	case RunCommandMsg:
		return m.runCommand(msg.ID)

	case PushMsg:
		return m.push(msg)

	case PopMsg:
		return m.pop()

	case OpenMsg:
		return m.open(msg.ID)

	case StatusMsg:
		m.status, m.statusLevel = msg.Text, msg.Level
		return m, nil

	case BindQueryMsg:
		return m.bindQuery(msg)

	case CapabilitiesMsg:
		return m.applyCaps(msg.Caps)

	case capsProbedMsg:
		if msg.seq != m.capsSeq {
			return m, nil
		}
		return m.settle(msg.seq, msg.caps)

	case capsFailedMsg:
		if msg.seq != m.capsSeq {
			return m, nil
		}
		text, _ := jira.Reason(msg.err)
		m.status, m.statusLevel = text, LevelError
		return m, nil

	case ProjectMsg:
		return m.setProject(msg.Project)

	case BroadcastMsg:
		return m.forwardAll(msg.Msg)

	// Every kind, not just the click: a wheel or a drag reaching the view under
	// the help overlay scrolls something nobody can see.
	case tea.MouseMsg:
		return m.handleMouse(msg)
	}
	return m.forwardTop(msg)
}

func (m Model) handleKey(msg tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	if msg.String() == "ctrl+c" {
		m.quitting = true
		return m, tea.Quit
	}
	// A view taking typing gets the keys first. ctrl+c above and the palette key
	// here are the only exceptions: neither is a character anybody types into a
	// field, and a palette that cannot be opened from a filter, a form or an
	// editor cannot be opened from most of the program.
	if m.capturing() {
		if Matches(msg, m.keys.Palette) {
			return m.openPalette()
		}
		return m.forwardTop(msg)
	}
	if m.showHelp {
		switch {
		case Matches(msg, m.keys.Help), Matches(msg, m.keys.Back), msg.String() == "q":
			m.showHelp = false
			return m, m.resizeAll()
		}
		return m, nil
	}
	if m.prefixSet {
		return m.resolvePrefix(msg)
	}
	if Matches(msg, m.keys.Go) {
		m.prefix, m.prefixSet = msg, true
		return m, nil
	}

	switch {
	case Matches(msg, m.keys.Help):
		m.showHelp = true
		return m, m.resizeAll()

	case Matches(msg, m.keys.Palette):
		return m.openPalette()

	case Matches(msg, m.keys.Saved):
		// A pushed view keeps its own digits: a saved query belongs to the root,
		// and a view switch is g and the digit from anywhere.
		if len(m.stack) != 1 {
			break
		}
		slot, err := strconv.Atoi(msg.String())
		if err != nil {
			break
		}
		return m.runSaved(slot)

	case Matches(msg, m.keys.Quit):
		if len(m.stack) > 1 {
			return m.pop()
		}
		if reason, blocked := m.blocked(); blocked {
			return m.refuse(reason)
		}
		m.quitting = true
		return m, tea.Quit

	case Matches(msg, m.keys.Back):
		if len(m.stack) > 1 {
			return m.pop()
		}
		m.status = ""
		return m, nil

	case Matches(msg, m.keys.Refresh):
		return m.forwardTop(RefreshMsg{})

	case Matches(msg, m.keys.Purge):
		next, probe := m.probeCaps()
		return next.forwardTopWith(RefreshMsg{Purge: true}, probe)
	}
	m.status = ""
	return m.forwardTop(msg)
}

// handleMouse routes one mouse message. A session with mouse = false still
// forwards them: nothing is reporting, so nothing arrives, and a view handed one
// anyway looks its zones up in a manager that is disabled and misses.
func (m Model) handleMouse(msg tea.MouseMsg) (tea.Model, tea.Cmd) {
	if click, ok := msg.(tea.MouseClickMsg); ok && m.mouse && click.Button == tea.MouseLeft {
		if next, cmd, hit := m.clickFooter(click); hit {
			return next, cmd
		}
	}
	if m.showHelp {
		return m, nil
	}
	return m.forwardTop(msg)
}

// clickFooter resolves a click on the chrome. An action is delivered as the key
// it advertises rather than as a second way of doing it, which is the only way
// docs/UX.md's three routes to an action stay one implementation.
//
// While the overlay is up the row offers one action — the key that closes it — and
// the other two cells are left alone: switching root view under an overlay would
// leave it covering a view nobody asked to see.
func (m Model) clickFooter(click tea.MouseClickMsg) (tea.Model, tea.Cmd, bool) {
	if len(m.stack) == 0 {
		return m, nil, false
	}
	if !m.showHelp {
		if m.deps.Zones.Get(m.zonePrefix + rootZone).InBounds(click) {
			return withHit(m.open(m.stack[0].spec.ID))
		}
		if m.deps.Zones.Get(m.zonePrefix + overflowZone).InBounds(click) {
			// The overlay is where the actions that did not fit are listed.
			m.showHelp = true
			return m, m.resizeAll(), true
		}
	}
	// An action that is not on the row has no zone in the frame just scanned, so
	// the lookup misses and the click falls through to the view.
	for _, b := range m.footerActs() {
		if !m.deps.Zones.Get(m.zonePrefix + actZone + b.Help().Key).InBounds(click) {
			continue
		}
		press, ok := Stroke(b)
		if !ok {
			return m, nil, true
		}
		return withHit(m.handleKey(press))
	}
	return m, nil, false
}

func withHit(model tea.Model, cmd tea.Cmd) (tea.Model, tea.Cmd, bool) {
	return model, cmd, true
}

// capturing reports whether the focused view is taking typing right now.
func (m Model) capturing() bool {
	if len(m.stack) == 0 || m.showHelp {
		return false
	}
	c, ok := m.top().view.(KeyCapturer)
	return ok && c.WantsRawKeys()
}

// blocked is the first entry anywhere on the stack that is holding something,
// in that entry's own words. The whole stack is asked because quitting and
// switching root view discard all of it, and the entry with the draft is often
// not the top one — the palette is pushed over whatever it was opened from and
// holds nothing itself.
func (m Model) blocked() (string, bool) {
	for _, entry := range m.stack {
		if reason, yes := blocks(entry.view); yes {
			return reason, true
		}
	}
	return "", false
}

// blockedOnTop asks only the view a pop would discard.
func (m Model) blockedOnTop() (string, bool) {
	if len(m.stack) == 0 {
		return "", false
	}
	return blocks(m.top().view)
}

func blocks(v View) (string, bool) {
	b, ok := v.(Blocker)
	if !ok {
		return "", false
	}
	return b.BlocksClose()
}

// refuse puts the reason a view gave for staying open into the status line.
func (m Model) refuse(reason string) (tea.Model, tea.Cmd) {
	m.status, m.statusLevel = reason, LevelWarn
	return m, nil
}

func (m Model) openSlot(slot int) (tea.Model, tea.Cmd) {
	for _, spec := range m.roots {
		if spec.Slot != slot {
			continue
		}
		return m.open(spec.ID)
	}
	m.status, m.statusLevel = fmt.Sprintf("nothing is bound to %d yet", slot), LevelInfo
	return m, nil
}

// resolvePrefix spends the buffered go-to key on whatever followed it. A digit
// is the kernel's; esc throws the gesture away; anything else was meant for the
// view, which then sees both keys in the order they were typed.
func (m Model) resolvePrefix(msg tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	buffered := m.prefix
	m.prefix, m.prefixSet = tea.KeyPressMsg{}, false
	switch {
	case Matches(msg, m.keys.Back):
		return m, nil
	case Matches(msg, m.keys.Slot):
		if slot, err := strconv.Atoi(msg.String()); err == nil {
			return m.openSlot(slot)
		}
	}
	first, cmd := m.forwardTop(buffered)
	model, ok := first.(Model)
	if !ok {
		return first, cmd
	}
	next, follow := model.forwardTop(msg)
	return next, tea.Batch(cmd, follow)
}

// runSaved opens the view that runs searches and hands it the query bound to a
// number key.
func (m Model) runSaved(slot int) (tea.Model, tea.Cmd) {
	query, bound := m.deps.Saved.BySlot(slot)
	if !bound {
		m.status, m.statusLevel = fmt.Sprintf("no saved query is bound to %d yet", slot), LevelInfo
		return m, nil
	}
	spec, ok := m.queryView()
	if !ok {
		m.status, m.statusLevel = "nothing in this build can run a saved query", LevelWarn
		return m, nil
	}
	opened, cmd := m.open(spec.ID)
	model, ok := opened.(Model)
	if !ok || len(model.stack) == 0 || model.top().spec.ID != spec.ID {
		return opened, cmd
	}
	next, follow := model.forwardTop(RunQueryMsg{JQL: query.JQL, Title: query.Name})
	return next, tea.Batch(cmd, follow)
}

func (m Model) queryView() (ViewSpec, bool) {
	for _, spec := range m.roots {
		if spec.RunsQueries && m.available(spec) {
			return spec, true
		}
	}
	return ViewSpec{}, false
}

// bindQuery puts a query on a number key and writes it back to the profile. The
// set is the kernel's because the keypress is: a view holding a copy of its own
// would dispatch a key the kernel no longer agrees about.
func (m Model) bindQuery(msg BindQueryMsg) (tea.Model, tea.Cmd) {
	replaced, taken := m.deps.Saved.BySlot(msg.Slot)
	saved, err := m.deps.Saved.Add(app.SavedQuery{Name: msg.Name, JQL: msg.JQL, Slot: msg.Slot})
	if err != nil {
		m.status, m.statusLevel = err.Error(), LevelWarn
		return m, nil
	}
	m.deps.Saved = saved
	m.savedGen++
	told, cmd := m.forwardAll(SavedQueriesMsg{Queries: saved})
	model, ok := told.(Model)
	if !ok {
		return told, cmd
	}
	note := fmt.Sprintf("%d runs %q", msg.Slot, msg.Name)
	switch {
	case msg.Slot == 0:
		note = fmt.Sprintf("%q is saved, on no key", msg.Name)
	case taken && !strings.EqualFold(replaced.Name, msg.Name):
		note = fmt.Sprintf("%d runs %q instead of %q", msg.Slot, msg.Name, replaced.Name)
	}
	if model.deps.SaveQueries == nil {
		note += ", for this session; there is no profile to save it to"
	}
	model.status, model.statusLevel = note, LevelInfo
	return model, tea.Batch(cmd, model.persistQueries(saved))
}

// persistQueries writes the set back where it came from. The key already works
// when this runs, so a failure is reported without taking the binding away.
func (m Model) persistQueries(saved app.SavedQueries) tea.Cmd {
	save := m.deps.SaveQueries
	if save == nil {
		return nil
	}
	return func() tea.Msg {
		if err := save(saved); err != nil {
			return StatusMsg{Text: "the key works for this session, but saving it failed: " + err.Error(), Level: LevelError}
		}
		return nil
	}
}

func (m Model) open(id string) (tea.Model, tea.Cmd) {
	spec, ok := LookupView(id)
	if !ok {
		m.status, m.statusLevel = fmt.Sprintf("%s is not available in this build", id), LevelWarn
		return m, nil
	}
	if !m.available(spec) {
		m.status, m.statusLevel = m.unavailable(spec), LevelWarn
		return m, nil
	}
	if len(m.stack) == 1 && m.stack[0].spec.ID == id {
		return m, nil
	}
	if reason, blocked := m.blocked(); blocked {
		return m.refuse(reason)
	}
	m.keepRoot()

	view, resumed := m.live[id]
	if !resumed {
		view = spec.New(m.deps)
		m.live[id] = view
	}
	blurred := m.blur()
	m.stack = []stackEntry{{spec: spec, view: view}}
	m.status = ""
	cmds := []tea.Cmd{blurred, m.focus(), m.resizeAll()}
	if !resumed {
		cmds = append(cmds, view.Init())
	}
	return m, tea.Batch(cmds...)
}

// openPalette puts the palette over whatever is on screen. Switching to it as a
// root view would discard the editor, form or thread it was opened from, leave
// esc with nothing to pop back to, and silence every command that reaches a view
// by broadcast. A draft does not refuse it, because pushing over one loses
// nothing, and it is built fresh each time so that a command is offered the
// session as it is rather than as it was the first time ctrl+k was pressed.
func (m Model) openPalette() (tea.Model, tea.Cmd) {
	// The key reaches here from inside the palette too, which takes typing.
	if len(m.stack) > 0 && m.top().spec.ID == PaletteViewID {
		return m, nil
	}
	spec, ok := LookupView(PaletteViewID)
	if !ok {
		m.status, m.statusLevel = fmt.Sprintf("%s is not available in this build", PaletteViewID), LevelWarn
		return m, nil
	}
	if !m.available(spec) {
		m.status, m.statusLevel = m.unavailable(spec), LevelWarn
		return m, nil
	}
	return m.push(PushMsg{View: spec.New(m.deps), ID: spec.ID, Title: spec.Title})
}

// unavailable is why a view cannot be opened. A session that has probed nothing
// knows nothing about this site, which is a different answer from a probe that
// came back without the capability.
func (m Model) unavailable(spec ViewSpec) string {
	return m.refusal(spec.Requires, spec.Title)
}

func (m Model) refusal(needs jira.CapabilityKey, what string) string {
	if reason := m.deps.Caps.Capability(needs).Reason; reason != "" {
		return reason
	}
	if !m.capsProbed {
		return fmt.Sprintf("nothing has been checked on this site yet, so whether %s works here is unknown", what)
	}
	return fmt.Sprintf("%s is not available on this site", what)
}

// runCommand runs a registered command by ID, which is the only way anything
// runs one. The palette knows which command was chosen and nothing else: the
// deps a command needs are the kernel's, current as of this keypress rather than
// as of whenever the palette was built, and whether a capability allows it is
// answered here rather than once per caller.
func (m Model) runCommand(id string) (tea.Model, tea.Cmd) {
	command, ok := LookupCommand(id)
	if !ok {
		m.status, m.statusLevel = fmt.Sprintf("%s is not available in this build", id), LevelWarn
		return m, nil
	}
	if command.Requires != "" && !m.deps.Caps.Allows(command.Requires) {
		return m.refuse(m.refusal(command.Requires, command.Title))
	}
	// The run goes out before the palette is popped, because the palette is the
	// view most likely to be counting them and it is the one about to go.
	told, cmd := m.forwardAll(CommandRanMsg{ID: command.ID, Keys: command.Keys})
	next, ok := told.(Model)
	if !ok {
		return told, cmd
	}
	if len(next.stack) > 1 && next.top().spec.ID == PaletteViewID {
		popped, back := next.pop()
		if model, isModel := popped.(Model); isModel {
			next, cmd = model, tea.Batch(cmd, back)
		}
	}
	return next, tea.Batch(cmd, command.Run(next.deps))
}

func (m Model) push(msg PushMsg) (tea.Model, tea.Cmd) {
	if msg.View == nil {
		return m, nil
	}
	spec := ViewSpec{ID: msg.ID, Title: msg.Title}
	if spec.Title == "" && len(m.stack) > 0 {
		spec.Title = m.top().spec.Title
	}
	blurred := m.blur()
	m.stack = append(append([]stackEntry(nil), m.stack...), stackEntry{spec: spec, view: msg.View})
	m.status = ""
	return m, tea.Batch(blurred, msg.View.Init(), m.focus(), m.resizeAll())
}

func (m Model) pop() (tea.Model, tea.Cmd) {
	if len(m.stack) <= 1 {
		return m, nil
	}
	if reason, blocked := m.blockedOnTop(); blocked {
		return m.refuse(reason)
	}
	blurred := m.blur()
	m.stack = append([]stackEntry(nil), m.stack[:len(m.stack)-1]...)
	m.status = ""
	return m, tea.Batch(blurred, m.focus(), m.resizeAll())
}

func (m Model) retheme(t *Theme) (tea.Model, tea.Cmd) {
	m.deps.Theme = t
	next, cmd := m.forwardAll(ThemeMsg{Theme: t})
	updated, ok := next.(Model)
	if !ok {
		return next, cmd
	}
	return updated, tea.Batch(cmd, updated.resizeAll())
}

// bodySize is the box a view gets once the chrome is taken out.
func (m Model) bodySize() (width, height int) {
	h := m.height - 2
	if m.status != "" {
		h--
	}
	if h < 0 {
		h = 0
	}
	return m.width, h
}

func (m Model) resizeAll() tea.Cmd {
	if len(m.stack) == 0 {
		return nil
	}
	w, h := m.bodySize()
	cmds := make([]tea.Cmd, 0, len(m.stack)+1)
	for i := range m.stack {
		view, cmd := m.stack[i].view.Update(SizeMsg{Width: w, Height: h})
		m.stack[i].view = view
		cmds = append(cmds, cmd)
	}
	return tea.Batch(cmds...)
}

func (m Model) forwardTop(msg tea.Msg) (tea.Model, tea.Cmd) {
	return m.forwardTopWith(msg, nil)
}

func (m Model) forwardTopWith(msg tea.Msg, extra tea.Cmd) (tea.Model, tea.Cmd) {
	if len(m.stack) == 0 {
		return m, extra
	}
	stack := append([]stackEntry(nil), m.stack...)
	view, cmd := stack[len(stack)-1].view.Update(msg)
	stack[len(stack)-1].view = view
	m.stack = stack
	m.keepRoot()
	return m, tea.Batch(cmd, extra)
}

// blur tells the view losing focus, and focus tells the one gaining it. Only
// the top of the stack ever has focus, so a view can stop a cursor blinking or
// pause a poller the moment it stops being looked at.
func (m Model) blur() tea.Cmd { return m.tellTop(FocusMsg{}) }

func (m Model) focus() tea.Cmd { return m.tellTop(FocusMsg{Focused: true}) }

func (m Model) tellTop(msg FocusMsg) tea.Cmd {
	if len(m.stack) == 0 {
		return nil
	}
	top := len(m.stack) - 1
	view, cmd := m.stack[top].view.Update(msg)
	m.stack[top].view = view
	return cmd
}

// keepRoot remembers the current root's state so that switching away and back
// does not reset the cursor, scroll offset or filter.
func (m Model) keepRoot() {
	if len(m.stack) > 0 {
		m.live[m.stack[0].spec.ID] = m.stack[0].view
	}
}

// forwardAll delivers a message to every view this session is holding, not just
// the ones on the stack: a root the user switched away from is still live, and
// it has to hear about a theme change or an edit made somewhere else before it
// is resumed.
func (m Model) forwardAll(msg tea.Msg) (tea.Model, tea.Cmd) {
	stack := append([]stackEntry(nil), m.stack...)
	current := ""
	if len(stack) > 0 {
		current = stack[0].spec.ID
	}
	cmds := make([]tea.Cmd, 0, len(stack)+len(m.live))
	for _, id := range slices.Sorted(maps.Keys(m.live)) {
		if id == current {
			continue
		}
		view, cmd := m.live[id].Update(msg)
		m.live[id] = view
		cmds = append(cmds, cmd)
	}
	for i := range stack {
		view, cmd := stack[i].view.Update(msg)
		stack[i].view = view
		cmds = append(cmds, cmd)
	}
	m.stack = stack
	m.keepRoot()
	return m, tea.Batch(cmds...)
}

// ProjectMsg carries the project the session is scoped to after a switch. An
// empty key is the whole site, which is a scope of its own rather than the
// absence of one.
type ProjectMsg struct{ Project string }

// SetProject returns a command that re-scopes the session to a project key, or
// to the whole site when the key is empty.
func SetProject(key string) tea.Cmd {
	return func() tea.Msg { return ProjectMsg{Project: key} }
}

// capsProbedMsg is a probe answer tagged with the request that asked for it. Two
// project switches in quick succession put two probes in flight, and without the
// tag the slower one wins whichever project it was asked about.
type capsProbedMsg struct {
	seq  int
	caps jira.Capabilities
}

// capsFailedMsg is a probe that never answered, tagged the same way.
type capsFailedMsg struct {
	seq int
	err error
}

// setProject re-scopes the session. Every view hears it, including the roots
// parked off screen, and the probe runs again because boards, Move and Delete
// are per-project answers. What the last project answered stands until the new
// answer lands, rather than the zero Capabilities standing in for it.
func (m Model) setProject(key string) (tea.Model, tea.Cmd) {
	key = strings.TrimSpace(key)
	if key == m.deps.Project {
		return m, nil
	}
	m.deps.Project = key
	next, probe := m.probeCaps()
	next.scopeSeq = next.capsSeq
	told, cmd := next.forwardAll(ProjectMsg{Project: key})
	model, ok := told.(Model)
	if !ok {
		return told, tea.Batch(cmd, probe)
	}
	model.status, model.statusLevel = scopeNote(key, probe != nil), LevelInfo
	return model, tea.Batch(cmd, probe)
}

// scopeNote says what the session is scoped to now, and whether anything is
// still being asked about it.
func scopeNote(key string, probing bool) string {
	note := "no project is selected, so per-project answers stay unknown"
	if key != "" {
		note = "this session is scoped to " + key
	}
	if !probing {
		return note
	}
	return note + ", and Saral is re-checking what this token can do"
}

// settle installs a probe result and, when it is the one a switch was waiting
// for, replaces the note that said it was still being checked.
func (m Model) settle(seq int, caps jira.Capabilities) (tea.Model, tea.Cmd) {
	awaited := m.scopeSeq != 0 && seq == m.scopeSeq
	next, cmd := m.applyCaps(caps)
	model, ok := next.(Model)
	if !ok || !awaited {
		return next, cmd
	}
	model.status, model.statusLevel = scopeNote(model.deps.Project, false), LevelInfo
	return model, cmd
}

// applyCaps installs a probe result. Views hear one message whichever probe
// answered, so a view has a single case to write.
func (m Model) applyCaps(caps jira.Capabilities) (tea.Model, tea.Cmd) {
	m.deps.Caps, m.capsProbed = caps, true
	m.roots = Views()
	m.capsGen++
	return m.forwardAll(CapabilitiesMsg{Caps: caps})
}

// probeCaps re-runs the capability probe, which is what R means beyond a
// refetch: permissions and instance settings can change under a session, and a
// project switch changes the answer outright. Only the newest question's answer
// is applied.
func (m Model) probeCaps() (Model, tea.Cmd) {
	m.capsSeq++
	return m, m.probeAt(m.capsSeq)
}

func (m Model) probeAt(seq int) tea.Cmd {
	client, project := m.deps.Jira, m.deps.Project
	if client == nil {
		return nil
	}
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()
		caps, err := client.Capabilities(ctx, project)
		if err != nil {
			return capsFailedMsg{seq: seq, err: err}
		}
		return capsProbedMsg{seq: seq, caps: caps}
	}
}

// View renders the frame. Alt screen, mouse mode and the window title are set
// here and nowhere else.
func (m Model) View() tea.View {
	v := tea.NewView(m.Frame())
	v.AltScreen = true
	v.WindowTitle = "saral"
	if m.mouse {
		v.MouseMode = tea.MouseModeCellMotion
	}
	return v
}

// Frame renders the whole screen to a string. It is what View wraps, and what
// golden tests and the first-paint benchmark measure directly.
func (m Model) Frame() string {
	if m.quitting {
		return ""
	}
	if m.width < MinWidth || m.height < MinHeight {
		return m.deps.Theme.Base.Render(fmt.Sprintf(
			"saral needs a terminal at least %d×%d.\nThis one is %d×%d.",
			MinWidth, MinHeight, m.width, m.height))
	}

	header, footer := m.chromeFor()
	rows := make([]string, 0, 4)
	rows = append(rows, header, m.body())
	if m.status != "" {
		rows = append(rows, m.statusLine())
	}
	rows = append(rows, footer)

	frame := strings.Join(rows, "\n")
	if m.mouse {
		return m.deps.Zones.Scan(frame)
	}
	return frame
}

// chromeFor returns the header and footer, rebuilding them only when something
// they depend on has changed.
func (m Model) chromeFor() (header, footer string) {
	_, palette := LookupView(PaletteViewID)
	_, keysGen := m.viewKeys()
	key := chromeKey{
		width: m.width, themeGen: m.deps.Theme.Gen, capsGen: m.capsGen,
		savedGen: m.savedGen, keysGen: keysGen, project: m.deps.Project,
		status: m.status, help: m.showHelp,
		depth: len(m.stack), palette: palette,
		capturing: m.capturing(), prefixed: m.prefixSet,
	}
	if len(m.stack) > 0 {
		key.rootID = m.stack[0].spec.ID
		key.topID = m.top().spec.ID
		key.title = m.top().spec.Title
	}
	if m.chrome != nil && m.chrome.header != "" && m.chrome.key == key {
		return m.chrome.header, m.chrome.footer
	}
	header, footer = m.header(), m.footer()
	if m.chrome != nil {
		*m.chrome = chromeCache{key: key, header: header, footer: footer}
	}
	return header, footer
}

func (m Model) header() string {
	t := m.deps.Theme
	title := "saral"
	if len(m.stack) > 0 && m.top().spec.Title != "" {
		title = "saral " + t.Glyphs.Separator + " " + m.top().spec.Title
	}
	right := m.headerRight()
	gap := m.width - lipgloss.Width(title) - lipgloss.Width(right) - 2
	if gap < 1 {
		gap = 1
		right = ""
	}
	return t.Header.Width(m.width).Render(oneLine(title+strings.Repeat(" ", gap)+right, m.width-2, t.Glyphs.Ellipsis))
}

// headerRight names what the session is pointed at: the project it is scoped
// to, when there is one, and the site it is talking to.
func (m Model) headerRight() string {
	switch {
	case m.deps.Project == "":
		return m.deps.Site
	case m.deps.Site == "":
		return m.deps.Project
	default:
		return m.deps.Project + " " + m.deps.Theme.Glyphs.Separator + " " + m.deps.Site
	}
}

func (m Model) body() string {
	w, h := m.bodySize()
	style := m.bodyStyle(w, h)
	var content string
	switch {
	case m.showHelp:
		content = m.helpView()
	case len(m.stack) == 0:
		content = m.emptyState()
	default:
		content = m.top().view.View()
	}
	return style.Render(content)
}

// bodyStyle is rebuilt only on resize; constructing a lipgloss.Style is the
// expensive half of rendering and this one is on every frame.
func (m Model) bodyStyle(w, h int) lipgloss.Style {
	if m.chrome == nil {
		return lipgloss.NewStyle().Width(w).Height(h).MaxHeight(h)
	}
	if !m.chrome.bodySet || m.chrome.bodyW != w || m.chrome.bodyH != h {
		m.chrome.bodyW, m.chrome.bodyH, m.chrome.bodySet = w, h, true
		m.chrome.body = lipgloss.NewStyle().Width(w).Height(h).MaxHeight(h)
	}
	return m.chrome.body
}

func (m Model) emptyState() string {
	t := m.deps.Theme
	return t.Muted.Render("No views are registered in this build.\n" +
		"Views self-register from their own package; see docs/ARCHITECTURE.md.")
}

// viewKeys is what the focused view says works right now, and a number that
// changes when that changes. A view whose keys move with its state implements
// KeyReporter; one whose keys never move is answered from the registry, which
// costs it nothing.
func (m Model) viewKeys() (set KeySet, gen int) {
	if len(m.stack) == 0 {
		return KeySet{}, 0
	}
	top := m.top()
	if reporter, ok := top.view.(KeyReporter); ok {
		return reporter.LiveKeys()
	}
	return KeysFor(top.spec.ID), 0
}

func (m Model) helpView() string {
	view, _ := m.viewKeys()
	h := m.deps.Theme.HelpModel
	h.ShowAll = true
	h.SetWidth(m.width)
	return h.View(mergeKeys(view, m.liveGlobals()))
}

func (m Model) statusLine() string {
	t := m.deps.Theme
	style := t.StatusBar
	switch m.statusLevel {
	case LevelWarn:
		style = t.StatusWarn
	case LevelError:
		style = t.StatusFail
	case LevelInfo:
	}
	return style.Width(m.width).Render(oneLine(m.status, m.width-2, t.Glyphs.Ellipsis))
}

// oneLine keeps a string to a single row. lipgloss.Style.Width word-wraps rather
// than clamping, and a chrome row that wraps pushes the footer off the bottom of
// the screen — which is exactly when it happens, because the long strings are
// error messages.
func oneLine(s string, width int, ellipsis string) string {
	if width < 1 {
		return ""
	}
	s = strings.ReplaceAll(strings.ReplaceAll(s, "\r\n", " "), "\n", " ")
	return ansi.Truncate(s, width, ellipsis)
}

// footerSep separates two entries in the action cell. The root cell carries its
// own padding, so whatever follows it takes one space rather than two.
const footerSep = "  "

// The zones the footer mints, under the kernel's own prefix. A click on an action
// is delivered as the key that action advertises, so the key, the palette and the
// pointer stay one implementation of the same thing.
const (
	rootZone     = "root"
	actZone      = "act:"
	overflowZone = "overflow"
)

// footerLevel is one rung of the ladder the row climbs down when it will not
// fit: whether the root cell is still drawn, and whether the actions still carry
// their descriptions.
type footerLevel struct{ root, terse bool }

// footerLevels is the order things are given up in. Actions fold into a +N at
// every rung; the root cell goes before the descriptions do, because a row of
// bare keys with nothing saying where you are is harder to read than a shorter
// list of named ones. The globals are not on this ladder.
var footerLevels = [3]footerLevel{{root: true}, {}, {terse: true}}

// footer draws one row in three cells: the root this session is in, what can be
// done to whatever is in front of you, and the globals.
//
// Only the globals are guaranteed a place, and the order the rest are given up in
// is footerLevels. The inventory a view offers outgrows eighty columns, which is
// the width docs/UX.md supports, so something has to go; the way out is not it.
// There is no second row at any width either: the constraint is width rather than
// height, so another row would be truncated the same way while costing a view one
// line in thirteen.
func (m Model) footer() string {
	globals, globalsW := m.globalCell()
	acts := m.footerActs()
	root, rootW := m.rootCell()

	room := m.width - globalsW
	if globalsW > 0 {
		room--
	}
	left, leftW := "", 0
	for rung, level := range footerLevels {
		labels := footerLabels(acts, level.terse)
		// A rung that cannot name a single action has nothing to show for
		// itself, so the row drops the root cell and then the descriptions
		// instead. The last rung takes what it gets: at eighty columns that is
		// unreachable, and a row that cannot be drawn is worse than a count.
		least := min(1, len(labels))
		if rung == len(footerLevels)-1 {
			least = 0
		}
		cell, cellW := "", 0
		if level.root {
			cell, cellW = root, rootW
		}
		fitted := false
		for named := len(labels); named >= least; named-- {
			left, leftW = m.drawLeft(cell, cellW, labels, named)
			if leftW <= room {
				fitted = true
				break
			}
		}
		if fitted {
			break
		}
	}
	line := left
	if globals != "" {
		gap := m.width - leftW - globalsW
		if gap < 1 {
			gap = 1
		}
		line += strings.Repeat(" ", gap) + globals
	}
	return m.deps.Theme.Footer.MaxWidth(m.width).Render(line)
}

// globalCell is the way out, and nothing takes it away. It is bare keys: there is
// no honest version of this row with room for "help", "commands" and "back" as
// well, and those sentences are one keystroke away in the overlay ? opens.
func (m Model) globalCell() (cell string, width int) {
	// The overlay swallows every global but the one that closes it, and a latched
	// gesture holds all of them until it is finished or thrown away. Both say so
	// in the action cell instead.
	if m.showHelp || m.prefixSet {
		return "", 0
	}
	_, palette := LookupView(PaletteViewID)
	capturing := m.capturing()
	keys := make([]string, 0, 3)
	if !capturing {
		keys = append(keys, m.keys.Help.Help().Key)
	}
	if palette {
		keys = append(keys, m.keys.Palette.Help().Key)
	}
	switch {
	case capturing:
		// A view with the keyboard swallows the rest, and docs/UX.md asks the
		// footer to show only what works right now — which cuts both ways.
	case len(m.stack) > 1:
		keys = append(keys, m.keys.Back.Help().Key)
	default:
		keys = append(keys, m.keys.Quit.Help().Key)
	}
	if len(keys) == 0 {
		return "", 0
	}
	plain := strings.Join(keys, " ")
	return m.deps.Theme.HintKey.Render(plain), ansi.StringWidth(plain)
}

// footerActs is the inventory the middle cell names. Two of the kernel's own
// states answer for themselves, because both have taken the view's keys away;
// everything else is what the focused view says works right now.
func (m Model) footerActs() []Binding {
	switch {
	case m.showHelp:
		return []Binding{Bind([]string{"?", "esc", "q"}, "?", "close help")}
	case m.prefixSet:
		return []Binding{
			Bind(m.keys.Slot.Keys(), "1-9", "switch view"),
			Bind(m.keys.Back.Keys(), "esc", "cancel"),
		}
	}
	set, _ := m.viewKeys()
	acts := set.Acts
	if len(acts) == 0 {
		acts = set.Short
	}
	// The digits are the one action a root view has that no view registered: they
	// run the profile's own searches. A view taking typing is spending them.
	if bound := m.boundQueries(); len(bound) > 0 && !m.capturing() {
		return append([]Binding{savedHint(bound)}, acts...)
	}
	return acts
}

// rootCell names the root the session is in: where esc lands, and what a click
// here goes back to. The header already says what is on top of it, so this is
// orientation rather than a second copy of that title.
func (m Model) rootCell() (cell string, width int) {
	if len(m.stack) == 0 {
		return "", 0
	}
	title := m.stack[0].spec.Title
	if title == "" {
		title = m.stack[0].spec.ID
	}
	rendered := m.deps.Theme.SlotOn.Render(title)
	return m.deps.Zones.Mark(m.zonePrefix+rootZone, rendered), lipgloss.Width(rendered)
}

// footerLabel is one action as the row spells it: the key, what it does unless
// the row has given descriptions up, and the zone a click resolves through. A
// terse row keeps the key and drops what it does, which is the last thing given
// up before a +N — a key with no name is still a key somebody can press and then
// find in the overlay.
type footerLabel struct{ key, desc, zone string }

func footerLabels(acts []Binding, terse bool) []footerLabel {
	out := make([]footerLabel, 0, len(acts))
	for _, b := range acts {
		if !b.Enabled() {
			continue
		}
		h := b.Help()
		label := footerLabel{key: h.Key, desc: h.Desc, zone: actZone + h.Key}
		if terse {
			label.desc = ""
		}
		out = append(out, label)
	}
	return out
}

func (l footerLabel) width() int {
	if l.desc == "" {
		return ansi.StringWidth(l.key)
	}
	return ansi.StringWidth(l.key) + 1 + ansi.StringWidth(l.desc)
}

// drawLeft renders the root cell and the first named actions, folding the rest
// into a +N the overlay then lists in full. Measuring and drawing are one walk,
// so the width the caller checks is the width of the row it gets.
func (m Model) drawLeft(root string, rootW int, labels []footerLabel, named int) (row string, width int) {
	t := m.deps.Theme
	var out strings.Builder
	out.WriteString(root)
	width, drawn := rootW, 0
	lead := func() (string, int) {
		switch {
		case drawn > 0:
			return footerSep, len(footerSep)
		case width > 0:
			return " ", 1
		default:
			return "", 0
		}
	}
	for _, label := range labels[:named] {
		sep, sepW := lead()
		entry := t.HintKey.Render(label.key)
		if label.desc != "" {
			entry += " " + t.HintDesc.Render(label.desc)
		}
		out.WriteString(sep)
		out.WriteString(m.deps.Zones.Mark(m.zonePrefix+label.zone, entry))
		width += sepW + label.width()
		drawn++
	}
	if rest := len(labels) - named; rest > 0 {
		sep, sepW := lead()
		count := "+" + strconv.Itoa(rest)
		out.WriteString(sep)
		out.WriteString(m.deps.Zones.Mark(m.zonePrefix+overflowZone, t.HintKey.Render(count)))
		width += sepW + len(count)
	}
	return out.String(), width
}

// SlotGesture is the gesture that reaches a footer slot, built from the keymap
// the kernel runs rather than written down. A registrar naming the key for a
// command that opens a view derives it from the view's slot with this, so that
// moving a view between slots cannot leave a command teaching the key of a
// different one.
//
// The footer no longer spells it out. One row cannot hold nine destinations and
// the actions as well, and the destinations are the half a user needs least
// often, so the digits are taught by the ? overlay and by the palette rows that
// carry them.
//
// It answers for the default keymap, which is the only one an init() can know
// about.
func SlotGesture(slot int) string { return slotGesture(DefaultGlobalKeys(), slot) }

func slotGesture(g GlobalKeys, slot int) string {
	return g.Go.Keys()[0] + strconv.Itoa(slot)
}

// liveGlobals is the global keymap with the entries that would do nothing right
// now taken out. docs/UX.md asks the footer to show only keys that work, and
// the only way that stays true is to derive it rather than write it down.
func (m Model) liveGlobals() KeySet {
	g := m.keys
	set := KeySet{Short: make([]Binding, 0, 4)}
	bound := m.boundQueries()
	if len(bound) > 0 {
		set.Short = append(set.Short, savedHint(bound))
	}
	set.Short = append(set.Short, g.Help)
	if _, ok := LookupView(PaletteViewID); ok {
		set.Short = append(set.Short, g.Palette)
	}
	if len(m.stack) > 1 {
		set.Short = append(set.Short, g.Back)
	} else {
		set.Short = append(set.Short, g.Quit)
	}
	set.Full = [][]Binding{{g.Saved, g.Slot, g.Back, g.Refresh, g.Purge}, {g.Palette, g.Help, g.Quit}}
	if len(bound) > 0 {
		set.Full = append([][]Binding{bound}, set.Full...)
	}
	return set
}

// boundQueries is one binding per saved query that has a number key, named
// after the query. It is empty in a pushed view, where the digits are the
// view's own.
func (m Model) boundQueries() []Binding {
	if len(m.stack) != 1 {
		return nil
	}
	slots := m.deps.Saved.Slots()
	out := make([]Binding, 0, len(slots))
	for _, slot := range slots {
		query, ok := m.deps.Saved.BySlot(slot)
		if !ok {
			continue
		}
		digit := strconv.Itoa(slot)
		out = append(out, Bind([]string{digit}, digit, query.Name))
	}
	return out
}

// savedHint collapses the bound digits into the one entry the footer has room
// for; the help overlay is where they are listed by name.
func savedHint(bound []Binding) Binding {
	keys := make([]string, 0, len(bound))
	for _, b := range bound {
		keys = append(keys, b.Keys()...)
	}
	return Bind(keys, strings.Join(keys, "/"), "saved query")
}
