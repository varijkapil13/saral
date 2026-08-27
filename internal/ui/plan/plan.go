// Package plan is the plans view: the site's Advanced Roadmaps plans where the
// token may read them, and the plans this profile defines itself where it may
// not — which is the ordinary case rather than the edge one.
//
// Every Plans endpoint is gated on Administer Jira, and the per-plan View and
// Edit rights the web UI hands out do not reach the API. So a 403 here is an
// answer: the view keeps its rows, draws the ones config defines, and says in
// the site's own words why they are not the site's. It is not an error screen
// and it is not an empty list.
package plan

import (
	"context"
	"errors"
	"strconv"
	"strings"

	tea "charm.land/bubbletea/v2"

	"github.com/varijkapil13/saral/internal/ui/kernel"
	"github.com/varijkapil13/saral/internal/ui/widget"
	"github.com/varijkapil13/saral/pkg/jira"
)

// ViewID is the name this view is registered and its keys scoped under.
const ViewID = "plans"

var (
	_ kernel.View        = (*Model)(nil)
	_ kernel.Addressed   = (*Model)(nil)
	_ kernel.KeyReporter = (*Model)(nil)
)

// source is where the plans on screen came from.
type source uint8

const (
	fromSite source = iota
	fromProfile
)

// planRow is one plan as this view holds it: what the site or the profile said,
// where it came from, and the search it renders to.
type planRow struct {
	plan    jira.Plan
	origin  string
	jql     string
	problem string
	dates   string
}

// releases is what one plan's projects answered with, or why they did not.
type releases struct {
	loading  bool
	read     bool
	versions []jira.Version
	err      error
}

// Option configures the view at construction.
type Option func(*Model)

// WithDefined hands over the plans the profile defines. Nothing in Deps carries
// them yet, so this is the seam config wires into; without it the view stands
// in for them from the session's project and its saved queries.
func WithDefined(defined []Defined) Option {
	return func(m *Model) {
		m.defined = append([]Defined(nil), defined...)
		m.derived = false
	}
}

// Model is the plans view.
type Model struct {
	deps kernel.Deps
	acts map[string]action

	defined []Defined
	derived bool

	plans  []planRow
	rows   []viewRow
	source source
	// reason is why the profile's plans are on screen instead of the site's, in
	// the words the site used wherever it supplied any.
	reason string

	open  map[string]bool
	rel   map[string]releases
	relOf string

	cursor, top   int
	width, height int

	loading, loaded bool
	failure         error
	gen             int
	cancel          context.CancelFunc
	addr            kernel.Addr

	styles *styles
	memo   *rowCache
	lay    layout

	head       string
	headText   string
	headAt     headKey
	reasonText string
	reasonAt   reasonKey
	lines      []string

	zones widget.Zoner
}

// New builds the view. It draws the profile's own plans before anything is
// asked of the site, so the common case — a token without Administer Jira —
// costs no round trip and shows no spinner.
func New(d kernel.Deps, opts ...Option) kernel.View {
	m := &Model{
		deps:    d,
		derived: true,
		open:    make(map[string]bool, 4),
		rel:     make(map[string]releases, 4),
		addr:    kernel.NewAddr(),
	}
	for _, opt := range opts {
		opt(m)
	}
	if m.deps.Theme == nil {
		m.deps.Theme = kernel.NewTheme(kernel.ThemeAuto, true, kernel.UnicodeGlyphs())
	}
	if m.derived {
		m.defined = derive(d.Project, d.Saved)
	}
	m.acts = defaultKeys().table()
	m.styles = newStyles(m.deps.Theme)
	m.memo = newRowCache(rowMemoLimit)
	m.zones = widget.NewZoner(d.Zones)
	m.lay = planLayout(m.width)
	m.settle()
	return m
}

// settle decides which plans are on screen without asking anything. The probe
// has already answered for CapPlans, so a session that cannot read the site's
// plans shows the profile's from the first frame rather than after a refusal.
func (m *Model) settle() {
	m.source, m.reason = fromSite, ""
	switch {
	case m.deps.Jira == nil:
		m.source, m.reason = fromProfile, "there is no Jira connection in this session"
	case !m.deps.Caps.Allows(jira.CapPlans):
		m.source, m.reason = fromProfile, m.capReason()
	}
	if m.source == fromProfile {
		m.takeProfilePlans()
	}
}

// capReason is the site's own words for the refusal, and a sentence of this
// program's only where the probe supplied none.
func (m *Model) capReason() string {
	if reason := strings.TrimSpace(m.deps.Caps.Capability(jira.CapPlans).Reason); reason != "" {
		return reason
	}
	return "the Plans API needs Administer Jira, which this token does not have"
}

func (m *Model) takeProfilePlans() {
	m.plans = m.plans[:0]
	for i, d := range m.defined {
		jql, problem := d.clause()
		name := strings.TrimSpace(d.Name)
		if name == "" {
			name = "unnamed plan"
		}
		m.plans = append(m.plans, planRow{
			plan: jira.Plan{
				// The index keeps two plans of one name apart, which their
				// memo keys and their mouse zones both need.
				ID:      "local:" + strconv.Itoa(i) + ":" + name,
				Name:    name,
				Sources: d.sources(),
				Local:   true,
			},
			origin:  originOf(d, m.derived),
			jql:     jql,
			problem: problem,
			dates:   d.dates(),
		})
	}
	m.loaded, m.loading, m.failure = true, false, nil
	m.reflow()
}

func (m *Model) takeSitePlans(plans []jira.Plan) {
	m.plans = m.plans[:0]
	for _, p := range plans {
		m.plans = append(m.plans, planRow{plan: p, origin: "read from the site"})
	}
	m.source, m.reason = fromSite, ""
	m.loaded, m.loading, m.failure = true, false, nil
	m.reflow()
}

// Init reads the site's plans, and only where they can be read at all.
func (m *Model) Init() tea.Cmd {
	if m.source == fromProfile {
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

	case kernel.ThemeMsg:
		m.deps.Theme = msg.Theme
		m.styles = newStyles(msg.Theme)
		m.memo.reset()
		m.head = ""

	case kernel.CapabilitiesMsg:
		m.deps.Caps = msg.Caps
		cmd = m.reprobe()

	case kernel.ProjectMsg:
		m.deps.Project = msg.Project
		cmd = m.rederive()

	case kernel.SavedQueriesMsg:
		m.deps.Saved = msg.Queries
		cmd = m.rederive()

	case kernel.RefreshMsg:
		cmd = m.refresh()

	case SourcesMsg:
		cmd = m.toggle(m.planUnderCursor())

	case plansMsg:
		cmd = m.tookPlans(msg)

	case releasesMsg:
		m.tookReleases(msg)

	case failedMsg:
		cmd = m.failed(msg)

	case tea.KeyPressMsg:
		cmd = m.key(msg)

	case tea.MouseClickMsg:
		cmd = m.click(msg)

	case tea.MouseWheelMsg:
		m.wheel(msg)
	}
	return m, cmd
}

// reprobe answers a fresh probe: a capability that has come back is worth a
// read, and one that has gone puts the profile's plans up with the new reason.
func (m *Model) reprobe() tea.Cmd {
	was := m.source
	m.settle()
	m.memo.reset()
	m.head = ""
	if m.source == fromSite && was == fromProfile {
		return m.load()
	}
	return nil
}

// rederive rebuilds the stand-in plans, which are the session's project and its
// saved queries and so move when either does. A profile that defines its own is
// left alone.
func (m *Model) rederive() tea.Cmd {
	if !m.derived {
		return nil
	}
	m.defined = derive(m.deps.Project, m.deps.Saved)
	if m.source == fromProfile {
		m.takeProfilePlans()
		m.memo.reset()
		m.head = ""
	}
	return nil
}

// refresh re-reads what the screen is showing. The profile's plans are read
// from a file this program does not own the reading of, so a refresh over them
// says so rather than looking like a read that did nothing.
func (m *Model) refresh() tea.Cmd {
	if m.source == fromProfile {
		m.takeProfilePlans()
		m.memo.reset()
		m.head = ""
		return kernel.Status(m.refreshedProfile())
	}
	m.rel = make(map[string]releases, 4)
	return m.load()
}

func (m *Model) refreshedProfile() string {
	if n := len(m.plans); n == 1 {
		return "1 plan, defined in this profile rather than read from the site"
	}
	return strconv.Itoa(len(m.plans)) + " plans, defined in this profile rather than read from the site"
}

func (m *Model) resize(w, h int) {
	if w == m.width && h == m.height {
		return
	}
	m.width, m.height = w, h
	m.lay = planLayout(w)
	m.memo.reset()
	m.head = ""
	m.reflow()
}

// --- fetching ---------------------------------------------------------------

// begin cancels whatever is in flight and opens a context for its replacement.
// One read is in flight at a time here: the plans, or the releases of the one
// plan that has just been opened.
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
	if m.relOf != "" {
		held := m.rel[m.relOf]
		held.loading = false
		m.rel[m.relOf] = held
		m.relOf = ""
	}
}

// Close lets go of a read still out with the site. A view that has been thrown
// away has nothing to draw with the answer.
//
// Losing the keyboard is not this: a FocusMsg is not handled at all, because a
// root switched away from is coming back and its read is still wanted.
func (m *Model) Close() { m.stop() }

// Addr is where the plans and the releases this view asked for come back to,
// whatever has since been pushed over it.
func (m *Model) Addr() kernel.Addr { return m.addr }

func (m *Model) reply(cmd tea.Cmd) tea.Cmd {
	return kernel.Reply(withCancel(m.cancel, cmd), m.addr)
}

// withCancel makes a command release its context however it ends.
func withCancel(cancel context.CancelFunc, cmd tea.Cmd) tea.Cmd {
	return func() tea.Msg {
		defer cancel()
		return cmd()
	}
}

func (m *Model) load() tea.Cmd {
	if m.deps.Jira == nil {
		return nil
	}
	ctx, gen := m.begin()
	m.loading, m.failure = true, nil
	m.head = ""
	return m.reply(readPlans(ctx, m.deps.Jira, gen))
}

func (m *Model) tookPlans(msg plansMsg) tea.Cmd {
	if msg.gen != m.gen {
		return nil
	}
	under := m.underCursor()
	m.takeSitePlans(msg.plans)
	m.memo.reset()
	m.head = ""
	m.putCursorBack(under)
	return nil
}

// failed turns the one refusal this view has a fallback for into the fallback,
// and everything else into a failure the pane keeps saying.
//
// The fallback is not for any 403: a token refused the plans is refused every
// Plans endpoint, and that is the case config defines plans for. A rate limit
// or a dead host is a read to try again, not a different source of plans.
func (m *Model) failed(msg failedMsg) tea.Cmd {
	if msg.gen != m.gen {
		return nil
	}
	m.loading = false
	if msg.plan != "" {
		m.tookReleaseFailure(msg)
		return kernel.Fail(msg.err)
	}
	if reason, refused := plansRefused(msg.err); refused {
		m.source, m.reason = fromProfile, reason
		m.takeProfilePlans()
		m.memo.reset()
		m.head = ""
		return kernel.Warn(reason)
	}
	m.failure = msg.err
	m.plans = m.plans[:0]
	m.reflow()
	m.memo.reset()
	m.head = ""
	return kernel.Fail(msg.err)
}

// plansRefused reports the refusal that names CapPlans, in the site's own
// words. It is matched by capability and never by status code: a 403 from
// somewhere else in the chain is not a statement about the Plans API.
func plansRefused(err error) (reason string, refused bool) {
	var capErr *jira.CapabilityError
	if !errors.As(err, &capErr) || capErr.Capability != jira.CapPlans {
		return "", false
	}
	if reason = strings.TrimSpace(capErr.Reason); reason == "" {
		reason = "the Plans API needs Administer Jira, which this token does not have"
	}
	return reason, true
}

func (m *Model) tookReleaseFailure(msg failedMsg) {
	held := m.rel[msg.plan]
	held.loading, held.read, held.err = false, true, msg.err
	m.rel[msg.plan] = held
	m.relOf = ""
	m.reflow()
	m.head = ""
}

func (m *Model) tookReleases(msg releasesMsg) {
	if msg.gen != m.gen {
		return
	}
	m.rel[msg.plan] = releases{read: true, versions: msg.versions}
	m.relOf = ""
	m.reflow()
	m.head = ""
}

// releasesFor asks for the versions of every project this plan draws from.
//
// Only a plan the profile defines can be asked: the site names a project source
// by a numeric id, no port method turns one into the key Versions takes, and
// guessing would ask about a project nobody named.
func (m *Model) releasesFor(at int) tea.Cmd {
	row := &m.plans[at]
	keys := projectKeys(row)
	if len(keys) == 0 || m.deps.Jira == nil {
		return nil
	}
	if held, ok := m.rel[row.plan.ID]; ok && (held.read || held.loading) {
		return nil
	}
	ctx, gen := m.begin()
	m.rel[row.plan.ID] = releases{loading: true}
	m.relOf = row.plan.ID
	m.head = ""
	return m.reply(readReleases(ctx, m.deps.Jira, row.plan.ID, keys, gen))
}

// projectKeys are the project sources this view may read releases for, which is
// only ever a plan the profile defined: those name a key, and the site's name an
// id.
func projectKeys(row *planRow) []string {
	if !row.plan.Local {
		return nil
	}
	var out []string
	for _, s := range row.plan.Sources {
		if s.Type == jira.PlanSourceProject && strings.TrimSpace(s.Value) != "" {
			out = append(out, s.Value)
		}
	}
	return out
}

// --- keys and selection -----------------------------------------------------

func (m *Model) key(msg tea.KeyPressMsg) tea.Cmd {
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
		m.moveTo(m.rowCount() - 1)
	case actToggle:
		return m.toggle(m.planUnderCursor())
	case actNone:
	}
	return nil
}

// toggle opens or closes one plan's detail: what it draws from, and the
// releases of the projects it names where those can be read.
func (m *Model) toggle(at int) tea.Cmd {
	if at < 0 {
		return nil
	}
	id := m.plans[at].plan.ID
	if m.open[id] {
		delete(m.open, id)
		m.reflow()
		m.head = ""
		return nil
	}
	m.open[id] = true
	m.reflow()
	m.head = ""
	return m.releasesFor(at)
}

// planUnderCursor is the plan the cursor's row belongs to, which is the plan
// itself or one of the lines under it.
func (m *Model) planUnderCursor() int {
	if m.cursor < 0 || m.cursor >= len(m.rows) {
		return -1
	}
	return m.rows[m.cursor].plan
}

func (m *Model) rowCount() int { return len(m.rows) }

func (m *Model) moveTo(at int) {
	n := m.rowCount()
	if n == 0 {
		m.cursor, m.top = 0, 0
		return
	}
	m.cursor = min(max(at, 0), n-1)
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
	m.top = min(max(m.top, 0), max(m.rowCount()-m.rowsHeight(), 0))
}

// rowsHeight is how many rows fit under the head line and its rule, less the
// line the reason keeps at the bottom.
func (m *Model) rowsHeight() int {
	h := m.height - headHeight
	if m.reasonShown() {
		h--
	}
	return max(h, 1)
}

// reasonShown is the state where the pane has rows and still owes the user a
// sentence: the plans on screen are the profile's, and why.
func (m *Model) reasonShown() bool { return m.reason != "" && m.rowCount() > 0 }

// underCursor names the plan the cursor is on, so that an answer replacing the
// rows does not throw the reader's place away.
func (m *Model) underCursor() string {
	if at := m.planUnderCursor(); at >= 0 {
		return m.plans[at].plan.ID
	}
	return ""
}

func (m *Model) putCursorBack(id string) {
	if id == "" {
		return
	}
	for i := range m.rows {
		if m.rows[i].kind == rowPlan && m.plans[m.rows[i].plan].plan.ID == id {
			m.moveTo(i)
			return
		}
	}
	m.moveTo(m.cursor)
}

// --- mouse ------------------------------------------------------------------

// click opens or closes the plan under the pointer, which is what clicking a
// fold does in the issue pane.
func (m *Model) click(msg tea.MouseClickMsg) tea.Cmd {
	if msg.Button != tea.MouseLeft {
		return nil
	}
	for i := m.top; i < min(m.top+m.rowsHeight(), m.rowCount()); i++ {
		if m.rows[i].kind != rowPlan || !m.zones.Hit(m.zoneOf(i), msg) {
			continue
		}
		m.moveTo(i)
		return m.toggle(m.rows[i].plan)
	}
	return nil
}

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
