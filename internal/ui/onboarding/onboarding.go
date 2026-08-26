// Package onboarding is the first-run view: it collects a site, an account
// email and an API token, checks them against Jira before anything is written,
// asks where the token should live, picks a project, and then reports what the
// capability probe found in the probe's own words.
//
// Nothing is saved until it has been verified, and the token itself never
// reaches the config file: the file names a keychain entry, an environment
// variable or a command, and internal/config resolves it at each start.
package onboarding

import (
	"context"
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode"

	"charm.land/bubbles/v2/spinner"
	"charm.land/bubbles/v2/textinput"
	"charm.land/bubbles/v2/viewport"
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"

	"github.com/varijkapil13/saral/internal/app"
	"github.com/varijkapil13/saral/internal/config"
	"github.com/varijkapil13/saral/internal/ui/kernel"
	"github.com/varijkapil13/saral/internal/ui/widget"
	"github.com/varijkapil13/saral/pkg/jira"
)

// ViewID is the name this view is registered and opened under.
const ViewID = "onboarding"

// opTimeout bounds one verification, probe or save. It is generous because the
// user is watching a spinner they can cancel by going back, not a frame budget.
const opTimeout = 30 * time.Second

// Connector opens a client for credentials that have not been saved yet.
//
// The view cannot build one itself: internal/ui takes the port and never an
// adapter, so the composition root registers the adapter this build uses with
// SetConnector. What comes back is the same narrowed client a session runs on,
// because this view hands it straight to one.
type Connector func(site, email, token string) (jira.SessionClient, error)

var registered struct {
	mu sync.RWMutex
	fn Connector
}

// SetConnector records how this build opens a connection to a site the user has
// just typed in. It is called once, from the composition root.
func SetConnector(fn Connector) {
	registered.mu.Lock()
	defer registered.mu.Unlock()
	registered.fn = fn
}

func connector() Connector {
	registered.mu.RLock()
	defer registered.mu.RUnlock()
	return registered.fn
}

// New builds the view with whatever connector this build registered.
func New(d kernel.Deps) kernel.View { return NewWith(d, connector()) }

// NewWith builds the view over a connector of the caller's own, which is how a
// test drives the whole flow against pkg/jira/jiratest.
func NewWith(d kernel.Deps, connect Connector) kernel.View {
	if d.Theme == nil {
		d.Theme = kernel.NewTheme(kernel.ThemeAuto, true, kernel.UnicodeGlyphs())
	}
	m := Model{
		deps:    d,
		connect: connect,
		cfg:     config.Config{Mouse: true},
		cache:   &renderCache{},
		addr:    kernel.NewAddr(),
	}
	m.zones = widget.NewZoner(d.Zones)
	m.restyle()
	m.spin = spinner.New(spinner.WithSpinner(spinner.Spinner{
		Frames: d.Theme.Glyphs.Spinner,
		FPS:    time.Second / 12,
	}))
	m.spin.Style = d.Theme.Accent
	m.pane = viewport.New()
	for i := range m.input {
		m.input[i] = m.newInput(field(i))
	}
	_ = m.input[fieldSite].Focus()
	return m
}

var _ kernel.View = Model{}

var _ kernel.Blocker = Model{}

var _ kernel.KeyCapturer = Model{}

var _ kernel.Addressed = Model{}

// Model is the onboarding view: a state machine over five text inputs and one
// choice, with a verification between the steps that need one.
type Model struct {
	deps    kernel.Deps
	connect Connector
	zones   widget.Zoner

	width, height int

	step   step
	input  [fieldCount]textinput.Model
	store  storeKind
	secret [storeCount]string

	spin spinner.Model
	pane viewport.Model
	busy busy
	last busy
	seq  int

	cancel   context.CancelFunc
	cancelBg context.CancelFunc
	addr     kernel.Addr

	problem string
	note    string

	client  jira.SessionClient
	search  *app.Search
	account jira.User
	caps    jira.Capabilities
	probed  bool
	project string

	suggested []string
	looking   bool
	lookup    string

	cfg     config.Config
	cfgPath string
	cfgErr  error
	name    string
	savedTo string
	stored  string

	styles styles
	cache  *renderCache
}

// WantsRawKeys is true whenever a field is on screen. Every Atlassian API token
// contains digits, and without this the kernel's slot keys eat them before the
// field sees them — the credential that gets verified is then not the one that
// was typed, and the user is sent back to re-paste a token that was correct.
func (m Model) WantsRawKeys() bool {
	return m.step.field() != fieldNone
}

// Init loads the config file that may already exist, because onboarding also
// adds a profile to one, and because writing over a file this build cannot
// parse would lose whatever is in it.
func (m Model) Init() tea.Cmd { return kernel.Reply(loadConfig, m.addr) }

// Addr is where the kernel delivers what this view asked the site for. It is a
// root, so nothing discards it — but the palette opens over it, and a token
// being checked while that is up is an answer this view still needs.
func (m Model) Addr() kernel.Addr { return m.addr }

// Update routes one message. Every outcome has its own message type, and each
// carries the sequence number of the operation that produced it so that a
// result the user has already moved past is dropped rather than applied.
func (m Model) Update(msg tea.Msg) (kernel.View, tea.Cmd) {
	switch msg := msg.(type) {
	case kernel.SizeMsg:
		m.width, m.height = msg.Width, msg.Height
		m.resize()
		return m, nil

	case kernel.FocusMsg:
		if !msg.Focused {
			m.blurField()
			return m, nil
		}
		cmd := m.focusField()
		return m, cmd

	case kernel.ThemeMsg:
		m.deps.Theme = msg.Theme
		m.restyle()
		m.spin.Spinner = spinner.Spinner{Frames: msg.Theme.Glyphs.Spinner, FPS: time.Second / 12}
		m.spin.Style = msg.Theme.Accent
		for i := range m.input {
			m.styleInput(&m.input[i], field(i))
		}
		m.resize()
		m.cache.reset()
		return m, nil

	case kernel.RefreshMsg:
		cmd := m.retry()
		return m, cmd

	case configLoadedMsg:
		cmd := m.configLoaded(msg)
		return m, cmd

	case connectedMsg:
		cmd := m.connected(msg)
		return m, cmd

	case connectFailedMsg:
		cmd := m.connectFailed(msg)
		return m, cmd

	case projectsFoundMsg:
		if m.stale(msg.seq) {
			return m, nil
		}
		m.looking, m.suggested, m.lookup = false, msg.keys, ""
		return m, nil

	case projectsUnknownMsg:
		if m.stale(msg.seq) {
			return m, nil
		}
		m.looking = false
		m.lookup = "Saral could not list your recent projects, so type the key: " + reason(msg.err)
		return m, nil

	case probedMsg:
		cmd := m.probeLanded(msg)
		return m, cmd

	case probeFailedMsg:
		cmd := m.probeFailed(msg)
		return m, cmd

	case savedMsg:
		cmd := m.saveLanded(msg)
		return m, cmd

	case saveFailedMsg:
		if m.stale(msg.seq) {
			return m, nil
		}
		m.busy = busyNone
		m.problem = reason(msg.err)
		return m, nil

	case spinner.TickMsg:
		if m.busy == busyNone {
			return m, nil
		}
		var cmd tea.Cmd
		m.spin, cmd = m.spin.Update(msg)
		return m, cmd

	case tea.KeyPressMsg:
		return m.key(msg)

	case tea.PasteMsg:
		return m.toInput(msg)

	case tea.MouseClickMsg:
		return m.click(msg)

	case tea.MouseWheelMsg:
		if m.step.field() != fieldNone {
			return m, nil
		}
		var cmd tea.Cmd
		m.pane, cmd = m.pane.Update(msg)
		return m, cmd
	}
	return m, nil
}

// BlocksClose keeps a half-finished profile from being thrown away by the key
// that quits. Nothing typed here is recoverable — the token especially — and
// the kernel shows this reason instead of leaving.
func (m Model) BlocksClose() (string, bool) {
	if m.step == stepDone || !m.dirty() {
		return "", false
	}
	return "Saral is still being set up and nothing has been saved; ctrl+c leaves without saving", true
}

func (m Model) dirty() bool {
	for i := range m.input {
		if m.input[i].Value() != "" {
			return true
		}
	}
	return false
}

func (m Model) key(msg tea.KeyPressMsg) (kernel.View, tea.Cmd) {
	switch msg.String() {
	case "enter", "tab":
		if m.busy != busyNone {
			return m, nil
		}
		cmd := m.advance()
		return m, cmd
	case "shift+tab":
		if m.busy != busyNone {
			return m, nil
		}
		cmd := m.back()
		return m, cmd
	case "ctrl+r":
		cmd := m.retry()
		return m, cmd
	case "up", "down":
		if cmd, handled := m.choose(msg.String() == "down"); handled {
			return m, cmd
		}
	}
	if m.busy != busyNone {
		return m, nil
	}
	if m.step.field() == fieldNone {
		var cmd tea.Cmd
		m.pane, cmd = m.pane.Update(msg)
		return m, cmd
	}
	return m.toInput(msg)
}

// toInput hands a key or a paste to the field the current step owns. A step
// with no field — the review and the summary — swallows it rather than letting
// a stray keystroke look like it did something.
func (m Model) toInput(msg tea.Msg) (kernel.View, tea.Cmd) {
	f := m.step.field()
	if f == fieldNone {
		return m, nil
	}
	m.problem, m.note = "", ""
	var cmd tea.Cmd
	m.input[f], cmd = m.input[f].Update(msg)
	if f == fieldSecret {
		m.secret[m.store] = m.input[f].Value()
	}
	return m, cmd
}

// choose moves the selection on the two steps that have one: the token store
// and the project suggestions. Both are also clickable, and neither can use
// j/k, which the field below them is spelling.
func (m *Model) choose(down bool) (tea.Cmd, bool) {
	switch m.step {
	case stepStorage:
		next := m.store + 1
		if !down {
			next = m.store - 1
		}
		if next < 0 || next >= storeCount {
			return nil, true
		}
		return m.setStore(next), true
	case stepProject:
		if len(m.suggested) == 0 {
			return nil, false
		}
		return m.cycleSuggestion(down), true
	case stepSite, stepEmail, stepToken, stepReview, stepDone:
	}
	return nil, false
}

func (m *Model) cycleSuggestion(down bool) tea.Cmd {
	at := -1
	for i, key := range m.suggested {
		if key == m.input[fieldProject].Value() {
			at = i
			break
		}
	}
	switch {
	case down:
		at++
	case at < 0:
		at = len(m.suggested) - 1
	default:
		at--
	}
	if at < 0 || at >= len(m.suggested) {
		return nil
	}
	m.setValue(fieldProject, m.suggested[at])
	return nil
}

func (m Model) click(msg tea.MouseClickMsg) (kernel.View, tea.Cmd) {
	if msg.Button != tea.MouseLeft {
		return m, nil
	}
	switch m.step {
	case stepStorage:
		for kind := storeKind(0); kind < storeCount; kind++ {
			if m.zones.Hit("store:"+kind.String(), msg) {
				cmd := m.setStore(kind)
				return m, cmd
			}
		}
	case stepProject:
		for _, key := range m.suggested {
			if m.zones.Hit("project:"+key, msg) {
				m.setValue(fieldProject, key)
				return m, nil
			}
		}
	case stepSite, stepEmail, stepToken, stepReview, stepDone:
	}
	for s := stepSite; s < m.step; s++ {
		if m.zones.Hit("step:"+s.String(), msg) {
			cmd := m.goTo(s)
			return m, cmd
		}
	}
	return m, nil
}

func (m *Model) setStore(kind storeKind) tea.Cmd {
	if kind == m.store {
		return nil
	}
	m.store = kind
	m.problem = ""
	m.setValue(fieldSecret, m.storeValue())
	m.cache.reset()
	return nil
}

// storeValue is what the entry field shows for the current store: whatever the
// user typed for it before, or the default that most people want. Only what was
// typed is remembered, so a default follows a site that has since changed.
func (m Model) storeValue() string {
	if v := m.secret[m.store]; v != "" {
		return v
	}
	switch m.store {
	case storeKeychain:
		return "saral:" + m.profileName()
	case storeEnv:
		return "JIRA_TOKEN"
	default:
		return ""
	}
}

func (m *Model) setValue(f field, v string) {
	m.input[f].SetValue(v)
	m.input[f].CursorEnd()
}

func (m Model) value(f field) string { return strings.TrimSpace(m.input[f].Value()) }

// advance validates the current step and moves on, or starts the call that
// decides whether it can. Nothing typed is ever cleared by a refusal.
func (m *Model) advance() tea.Cmd {
	switch m.step {
	case stepSite:
		site, err := config.NormalizeSite(hostOf(m.value(fieldSite)))
		if err != nil {
			m.problem = err.Error()
			return nil
		}
		m.setValue(fieldSite, site)
		return m.goTo(stepEmail)

	case stepEmail:
		email := m.value(fieldEmail)
		if !strings.Contains(email, "@") || strings.ContainsFunc(email, unicode.IsSpace) {
			m.problem = "Jira Cloud pairs the account email with the API token as basic auth, so it has to be the address the token belongs to"
			return nil
		}
		m.setValue(fieldEmail, email)
		return m.goTo(stepToken)

	case stepToken:
		switch {
		case strings.TrimSpace(m.input[fieldToken].Value()) == "":
			m.problem = "an API token is required; create one at id.atlassian.com/manage-profile/security/api-tokens"
			return nil
		case m.connect == nil:
			m.problem = "this build cannot open a connection: nothing wired an adapter into onboarding.SetConnector"
			return nil
		}
		return m.verify()

	case stepStorage:
		if _, err := m.tokenSource(); err != nil {
			m.problem = err.Error()
			return nil
		}
		return m.goTo(stepProject)

	case stepProject:
		return m.probe()

	case stepReview:
		return m.save()

	case stepDone:
		return m.finish()
	}
	return nil
}

func (m *Model) back() tea.Cmd {
	if m.step == stepSite {
		return nil
	}
	return m.goTo(m.step - 1)
}

func (m *Model) goTo(to step) tea.Cmd {
	m.blurField()
	m.step = to
	m.problem, m.note = "", ""
	if to < stepStorage {
		m.name = ""
	}
	if to == stepStorage {
		m.name = m.profileName()
		m.setValue(fieldSecret, m.storeValue())
	}
	m.pane.SetYOffset(0)
	m.resize()
	m.cache.reset()
	return m.focusField()
}

func (m *Model) blurField() {
	if f := m.step.field(); f != fieldNone {
		m.input[f].Blur()
	}
}

func (m *Model) focusField() tea.Cmd {
	if f := m.step.field(); f != fieldNone {
		return m.input[f].Focus()
	}
	return nil
}

func (m *Model) finish() tea.Cmd {
	// A session that started without a client cannot use the profile it has just
	// written: nothing hands a kernel that is already running a new one.
	if m.deps.Jira == nil {
		return tea.Quit
	}
	return kernel.Status("profile " + m.name + " saved to " + m.savedTo)
}

// retry re-runs whatever last failed, which is what r means here. A transport
// failure is the ordinary reason to be looking at this screen twice.
func (m *Model) retry() tea.Cmd {
	if m.busy != busyNone {
		return nil
	}
	switch m.last {
	case busyConnect:
		return m.verify()
	case busyProbe:
		return m.probe()
	case busySave:
		return m.save()
	case busyNone:
	}
	if m.step == stepProject && m.client != nil {
		return m.suggest()
	}
	return nil
}

func (m Model) profileName() string {
	if m.name != "" {
		return m.name
	}
	base := profileNameFor(m.value(fieldSite))
	name := base
	for n := 2; ; n++ {
		if _, taken := m.cfg.Profiles[name]; !taken {
			return name
		}
		name = base + "-" + strconv.Itoa(n)
	}
}

// profileNameFor turns a host into a bare TOML key: the first label, lowercased,
// with anything that would have to be quoted dropped.
func profileNameFor(site string) string {
	label, _, _ := strings.Cut(site, ".")
	var b strings.Builder
	for _, r := range strings.ToLower(label) {
		if r == '-' || r == '_' || (r >= '0' && r <= '9') || (r >= 'a' && r <= 'z') {
			b.WriteRune(r)
		}
	}
	if b.Len() == 0 {
		return "default"
	}
	return b.String()
}

// resize sizes the fields to what is left of the row after the indent, the
// label column, the prompt arrow and the cell the cursor sits in.
func (m *Model) resize() {
	w := m.width - inputIndent - labelWidth - 6 - lipgloss.Width(m.deps.Theme.Glyphs.Arrow) - 2
	if w < 8 {
		w = 8
	}
	for i := range m.input {
		m.input[i].SetWidth(w)
	}
	m.pane.SetWidth(max(m.width, 1))
	m.pane.SetHeight(m.paneHeight())
	m.refreshPane()
}

// refreshPane loads the block the summary steps show. It is the one part of
// this view that can be taller than the box it is in — a probe answers with as
// many sentences as it has refusals — so it is the one part that scrolls.
func (m *Model) refreshPane() {
	switch m.step {
	case stepReview:
		m.pane.SetContentLines(m.summary())
	case stepDone:
		m.pane.SetContentLines(m.finished())
	case stepSite, stepEmail, stepToken, stepStorage, stepProject:
	}
}

func (m Model) newInput(f field) textinput.Model {
	in := textinput.New()
	in.Prompt = ""
	in.Placeholder = f.placeholder()
	if f == fieldToken {
		in.EchoMode = textinput.EchoPassword
		in.EchoCharacter = firstRune(m.deps.Theme.Glyphs.Bullet, '*')
	}
	m.styleInput(&in, f)
	return in
}

// styleInput dresses one field in the theme. The cursor does not blink: the
// kernel owns the frame and renders a string, so a blink is a full redraw of
// the form twice a second to move one reversed cell.
func (m Model) styleInput(in *textinput.Model, f field) {
	t := m.deps.Theme
	state := textinput.StyleState{Text: t.Base, Placeholder: t.Muted, Suggestion: t.Muted, Prompt: t.Muted}
	in.SetStyles(textinput.Styles{
		Focused: state,
		Blurred: state,
		Cursor:  textinput.CursorStyle{Color: t.Accent.GetForeground(), Blink: false},
	})
	if f == fieldToken {
		in.EchoCharacter = firstRune(t.Glyphs.Bullet, '*')
	}
}

func firstRune(s string, fallback rune) rune {
	for _, r := range s {
		return r
	}
	return fallback
}

// hostOf drops the path off what was pasted. What people have in the clipboard
// is the URL of a board or a ticket, and internal/config quite rightly refuses
// to keep a path in a profile — but refusing the paste is not the same as
// refusing the site it names.
func hostOf(raw string) string {
	site := strings.TrimSpace(raw)
	rest := site
	if i := strings.Index(site, "://"); i >= 0 {
		rest = site[i+len("://"):]
	}
	if j := strings.IndexAny(rest, "/?#"); j >= 0 {
		return site[:len(site)-len(rest)+j]
	}
	return site
}

// reason is the sentence to put in front of the user for an error, which for a
// typed Jira error is the wording the error already carries.
func reason(err error) string {
	if err == nil {
		return ""
	}
	text, _ := jira.Reason(err)
	return text
}
