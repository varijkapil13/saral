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
type Blocker interface {
	BlocksClose() (reason string, blocked bool)
}

type stackEntry struct {
	spec ViewSpec
	view View
}

type chromeKey struct {
	width     int
	themeGen  int
	capsGen   int
	rootID    string
	topID     string
	title     string
	status    string
	help      bool
	depth     int
	palette   bool
	capturing bool
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

	width, height int
	capsGen       int
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

// WithInitialView opens a specific view rather than the first footer slot.
func WithInitialView(id string) Option {
	return func(m *Model) { m.initialView = id }
}

// WithMouse turns mouse reporting on or off. Off is what a user who relies on
// terminal text selection asks for.
func WithMouse(enabled bool) Option {
	return func(m *Model) { m.mouse = enabled }
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
func (m Model) Init() tea.Cmd {
	cmds := []tea.Cmd{tea.RequestBackgroundColor}
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

	case tea.MouseClickMsg:
		return m.handleClick(msg)

	case PushMsg:
		return m.push(msg)

	case PopMsg:
		return m.pop()

	case OpenMsg:
		return m.open(msg.ID)

	case StatusMsg:
		m.status, m.statusLevel = msg.Text, msg.Level
		return m, nil

	case CapabilitiesMsg:
		m.deps.Caps = msg.Caps
		m.roots = Views()
		m.capsGen++
		return m.forwardAll(msg)

	case BroadcastMsg:
		return m.forwardAll(msg.Msg)
	}
	return m.forwardTop(msg)
}

func (m Model) handleKey(msg tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	if msg.String() == "ctrl+c" {
		m.quitting = true
		return m, tea.Quit
	}
	// A view taking typing gets the keys first. ctrl+c above is the exception,
	// because a terminal program that cannot be interrupted is broken whatever
	// it is doing.
	if m.capturing() {
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

	switch {
	case Matches(msg, m.keys.Help):
		m.showHelp = true
		return m, m.resizeAll()

	case Matches(msg, m.keys.Palette):
		return m.open(PaletteViewID)

	case Matches(msg, m.keys.Slot):
		slot, err := strconv.Atoi(msg.String())
		if err != nil {
			break
		}
		return m.openSlot(slot)

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
		return m.forwardTopWith(RefreshMsg{Purge: true}, m.probeCaps())
	}
	m.status = ""
	return m.forwardTop(msg)
}

func (m Model) handleClick(msg tea.MouseClickMsg) (tea.Model, tea.Cmd) {
	if m.mouse && msg.Button == tea.MouseLeft {
		for _, spec := range m.roots {
			if spec.Slot == 0 || !m.available(spec) {
				continue
			}
			if m.deps.Zones.Get(m.zonePrefix + "slot:" + spec.ID).InBounds(msg) {
				return m.open(spec.ID)
			}
		}
	}
	return m.forwardTop(msg)
}

// capturing reports whether the focused view is taking typing right now.
func (m Model) capturing() bool {
	if len(m.stack) == 0 || m.showHelp {
		return false
	}
	c, ok := m.top().view.(KeyCapturer)
	return ok && c.WantsRawKeys()
}

func (m Model) blocked() (string, bool) {
	if len(m.stack) == 0 {
		return "", false
	}
	if b, ok := m.top().view.(Blocker); ok {
		return b.BlocksClose()
	}
	return "", false
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

func (m Model) open(id string) (tea.Model, tea.Cmd) {
	spec, ok := LookupView(id)
	if !ok {
		m.status, m.statusLevel = fmt.Sprintf("%s is not available in this build", id), LevelWarn
		return m, nil
	}
	if !m.available(spec) {
		m.status, m.statusLevel = m.deps.Caps.Capability(spec.Requires).Reason, LevelWarn
		if m.status == "" {
			m.status = fmt.Sprintf("%s is not available on this site", spec.Title)
		}
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
	if reason, blocked := m.blocked(); blocked {
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

// probeCaps re-runs the capability probe, which is what R means beyond a
// refetch: permissions and instance settings can change under a session.
func (m Model) probeCaps() tea.Cmd {
	client, project := m.deps.Jira, m.deps.Project
	if client == nil {
		return nil
	}
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()
		caps, err := client.Capabilities(ctx, project)
		if err != nil {
			text, _ := jira.Reason(err)
			return StatusMsg{Text: text, Level: LevelError}
		}
		return CapabilitiesMsg{Caps: caps}
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
	key := chromeKey{
		width: m.width, themeGen: m.deps.Theme.Gen, capsGen: m.capsGen,
		status: m.status, help: m.showHelp, depth: len(m.stack), palette: palette,
		capturing: m.capturing(),
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
	right := m.deps.Site
	gap := m.width - lipgloss.Width(title) - lipgloss.Width(right) - 2
	if gap < 1 {
		gap = 1
		right = ""
	}
	return t.Header.Width(m.width).Render(oneLine(title+strings.Repeat(" ", gap)+right, m.width-2, t.Glyphs.Ellipsis))
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

func (m Model) helpView() string {
	view := KeySet{}
	if len(m.stack) > 0 {
		view = KeysFor(m.top().spec.ID)
	}
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

// footer draws the view slots and the hints for the keys that work right now.
// Both come from the registries, so neither can drift from what is real.
func (m Model) footer() string {
	t := m.deps.Theme
	slots := make([]string, 0, len(m.roots))
	for _, spec := range m.roots {
		if spec.Slot == 0 || !m.available(spec) {
			continue
		}
		style := t.SlotOff
		if len(m.stack) > 0 && m.stack[0].spec.ID == spec.ID {
			style = t.SlotOn
		}
		label := strconv.Itoa(spec.Slot) + " " + spec.Title
		slots = append(slots, m.deps.Zones.Mark(m.zonePrefix+"slot:"+spec.ID, style.Render(label)))
	}
	left := strings.Join(slots, "")

	hints := m.hintLine(m.width - lipgloss.Width(left) - 1)
	gap := m.width - lipgloss.Width(left) - lipgloss.Width(hints)
	if gap < 1 {
		gap = 1
		hints = ""
	}
	return t.Footer.MaxWidth(m.width).Render(left + strings.Repeat(" ", gap) + hints)
}

// hintLine shows only the keys that work right now, in the space the footer
// slots left over. The help component truncates with its own ellipsis rather
// than the line wrapping.
func (m Model) hintLine(width int) string {
	if width < 8 {
		return ""
	}
	h := m.deps.Theme.HelpModel
	h.ShowAll = false
	h.SetWidth(width)
	if m.showHelp {
		return h.View(keyMap{short: []Binding{Bind([]string{"?", "esc", "q"}, "?", "close help")}})
	}
	set := KeySet{}
	if len(m.stack) > 0 {
		set = KeysFor(m.top().spec.ID)
	}
	if m.capturing() {
		// The globals are unreachable while the view has the keyboard, and
		// docs/UX.md asks the footer to show only what works right now.
		return h.View(keyMap{short: set.Short})
	}
	return h.View(mergeKeys(set, m.liveGlobals()))
}

// liveGlobals is the global keymap with the entries that would do nothing right
// now taken out. docs/UX.md asks the footer to show only keys that work, and
// the only way that stays true is to derive it rather than write it down.
func (m Model) liveGlobals() KeySet {
	g := m.keys
	set := KeySet{Short: make([]Binding, 0, 3)}
	set.Short = append(set.Short, g.Help)
	if _, ok := LookupView(PaletteViewID); ok {
		set.Short = append(set.Short, g.Palette)
	}
	if len(m.stack) > 1 {
		set.Short = append(set.Short, g.Back)
	} else {
		set.Short = append(set.Short, g.Quit)
	}
	set.Full = [][]Binding{{g.Slot, g.Back, g.Refresh, g.Purge}, {g.Palette, g.Help, g.Quit}}
	return set
}
