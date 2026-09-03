package palette

import (
	"context"
	"strconv"
	"strings"
	"time"

	"charm.land/bubbles/v2/textinput"
	tea "charm.land/bubbletea/v2"

	"github.com/varijkapil13/saral/internal/app"
	"github.com/varijkapil13/saral/internal/ui/kernel"
	"github.com/varijkapil13/saral/internal/ui/widget"
	"github.com/varijkapil13/saral/pkg/jira"
)

// projectViewID scopes the picker's header and its click zones. It registers no
// keys, for the reason the palette registers none: it takes typing from the
// moment it opens, so kernel.KeyReporter answers for every state it has.
const projectViewID = "palette.project"

// ProjectViewID is projectViewID, exported so a second door onto this same
// picker — the settings screen's session.project row — can push it under the
// ID it already answers to.
const ProjectViewID = projectViewID

const switchCommandID = "project.switch"

// sessionSection is the settings section session.project registers into. It is
// not exported: the settings screen groups by the section name kernel.Settings
// hands back, not by a shared constant.
const sessionSection = "Session"

func init() { kernel.RegisterSetting(projectSetting()) }

// projectSetting is session.project: state rather than a verb, so it is a
// setting and not only a command, per docs/SETTINGS.md. Options answers with
// the scope already in force rather than the site's whole list — the site is
// read asynchronously, inside the picker this row opens, and Setting.Options
// has no room for a read in flight — which keeps it consistent with Value by
// construction rather than by convention.
//
// Switching project is not written anywhere: cmd/saral reads profile.Project
// once at startup and nothing here persists a later switch, so the scope is
// this run's alone and Scope says so.
func projectSetting() kernel.Setting {
	return kernel.Setting{
		ID:      "session.project",
		Section: sessionSection,
		Order:   0,
		Title:   "Project",
		Summary: `what a search means by "this project", and what the probe ran against`,
		Kind:    kernel.KindChoice,
		Scope:   kernel.ScopeSession,
		Options: func(d kernel.Deps) []kernel.SettingOption {
			if d.Project == "" {
				return []kernel.SettingOption{{ID: "", Label: "The whole site"}}
			}
			return []kernel.SettingOption{{ID: d.Project, Label: d.Project}}
		},
		Value: func(d kernel.Deps) string { return d.Project },
		Set:   func(_ kernel.Deps, id string) tea.Cmd { return kernel.SetProject(id) },
	}
}

// NewProjectPicker builds the project-switching picker for a caller outside
// this package. It is the exact view project.switch already opens — a filtered,
// frecency-ranked list with a current marker and its own click zones — and the
// settings screen's session.project row is its second door onto it rather than
// a copy of it.
func NewProjectPicker(d kernel.Deps) kernel.View { return newProject(d) }

// lookTimeout bounds the one read the picker makes. Somebody is waiting on this
// one, so it is shorter than a setup step's.
const lookTimeout = 15 * time.Second

// suggestionLimit is how many issues are read to find project keys. It is a
// page, not a walk: the answer is a handful of keys, and paging further only
// finds projects this account has not touched recently.
const suggestionLimit = 50

// zoneProject prefixes a project row's click target.
const zoneProject = "proj:"

// namePenalty is what finding a project by its name rather than by its key
// costs: app.Pattern's step nine times over, the calibration the palette and the
// value picker already use.
const namePenalty = 9 * scoreTier

var (
	_ kernel.View        = (*projectModel)(nil)
	_ kernel.KeyCapturer = (*projectModel)(nil)
	_ kernel.KeyReporter = (*projectModel)(nil)
	_ kernel.Addressed   = (*projectModel)(nil)
	_ kernel.Closer      = (*projectModel)(nil)
)

// projectsFoundMsg carries the projects behind this account's recent issues.
type projectsFoundMsg struct{ found []project }

// projectsFailedMsg is a read that brought nothing back. The error travels whole
// so that a refusal reaches the user in the words the site used.
type projectsFailedMsg struct{ err error }

// project is one project as the site named it.
type project struct {
	key  string
	name string
}

// projectRow is one thing on offer: a project, or the whole site.
type projectRow struct {
	// key is what kernel.SetProject is called with, and "" for the whole site,
	// which is a scope of its own rather than the absence of one.
	key   string
	label string
	note  string
	// current marks the scope this session is already on, so that switching to
	// it is visibly a no-op rather than a mystery.
	current bool
}

// match is the best of the two ways a project can be found.
func (r *projectRow) match(p app.Pattern) (int, bool) {
	best, ok := p.Score(r.label)
	if score, hit := p.Score(r.note); hit && (!ok || score-namePenalty > best) {
		best, ok = score-namePenalty, true
	}
	return best, ok
}

// projectModel is the picker the "Switch project" command opens. It is a pane of
// its own rather than a mode of the palette: the palette filters a fixed list
// built at construction, and this one reads its list from the site and ranks it
// against a table of its own.
type projectModel struct {
	deps kernel.Deps
	addr kernel.Addr
	keys projectKeys
	acts map[string]action

	input textinput.Model
	freq  *table
	query string

	rows  []projectRow
	shown []int
	ranks []ranked
	// found is what the read answered with, kept so that a scope changing under
	// the picker rebuilds the rows without asking the site again.
	found []project

	// looking is a read in flight, and problem is why the last one answered
	// with nothing. A failure is a note and never a refusal: the whole site and
	// the project this session is on are both pickable without the site's help.
	looking bool
	problem string
	cancel  context.CancelFunc

	cursor, top   int
	width, height int

	styles     *styles
	memo       *rowCache
	lay        layout
	lines      []string
	head       string
	headAt     projectHeadKey
	zonePrefix string
}

// newProject builds the picker against the session as it is at the keypress. The
// deps a command's Run is given are the kernel's own, so Deps.Project here is the
// project the session is on now and not the one it was on when the command was
// registered.
func newProject(d kernel.Deps) kernel.View { return buildProject(d, sharedProjectTable()) }

func buildProject(d kernel.Deps, freq *table) *projectModel {
	m := &projectModel{
		deps:  d,
		addr:  kernel.NewAddr(),
		keys:  defaultProjectKeys(),
		input: newProjectInput(),
		freq:  freq,
		memo:  newRowCache(rowMemoLimit),
	}
	if m.deps.Theme == nil {
		m.deps.Theme = kernel.NewTheme(kernel.ThemeAuto, true, kernel.UnicodeGlyphs())
	}
	if m.deps.Now == nil {
		m.deps.Now = time.Now
	}
	if d.Zones != nil {
		m.zonePrefix = d.Zones.NewPrefix()
	}
	m.acts = m.keys.table()
	m.styles = newStyles(m.deps.Theme)
	m.rows = m.buildRows(nil)
	m.lay = planLayout(m.width, 0)
	_ = m.input.Focus()
	m.refilter()
	return m
}

func newProjectInput() textinput.Model {
	ti := widget.NewInput()
	ti.Prompt = "> "
	ti.Placeholder = "which project?"
	return ti
}

// buildRows is the whole site, then whatever the site answered with, then the
// project this session is on if that was not among them. A scope in force that
// is not on the list is a scope nobody can get back to from here.
func (m *projectModel) buildRows(found []project) []projectRow {
	out := make([]projectRow, 0, len(found)+2)
	out = append(out, projectRow{
		label: "The whole site",
		// The note column is the palette's, and it is narrow: a sentence about the
		// scope would arrive truncated.
		note:    "every project",
		current: m.deps.Project == "",
	})
	seen := false
	for _, p := range found {
		if p.key == "" {
			continue
		}
		seen = seen || p.key == m.deps.Project
		out = append(out, projectRow{key: p.key, label: p.key, note: p.name, current: p.key == m.deps.Project})
	}
	if !seen && m.deps.Project != "" {
		out = append(out, projectRow{key: m.deps.Project, label: m.deps.Project, current: true})
	}
	return out
}

// Addr is where the one read this picker makes comes back to.
func (m *projectModel) Addr() kernel.Addr { return m.addr }

// WantsRawKeys is always true: the filter has the keyboard for as long as the
// picker is up, the way it does in the palette.
func (m *projectModel) WantsRawKeys() bool { return true }

// Init asks which projects this account has been working in. There is no
// project-list endpoint on the port, so the answer comes from a narrow read over
// recent issues — the shape onboarding's picker already uses.
func (m *projectModel) Init() tea.Cmd { return m.look() }

// Close cancels a read the kernel has thrown the view away for.
func (m *projectModel) Close() { m.stop() }

func (m *projectModel) stop() {
	if m.cancel != nil {
		m.cancel()
		m.cancel = nil
	}
	m.looking = false
}

func (m *projectModel) look() tea.Cmd {
	if m.deps.Jira == nil {
		m.problem = "this session has no connection, so only the scopes it already knows are offered"
		return nil
	}
	m.stop()
	search := app.NewSearch(m.deps.Jira)
	ctx, cancel := context.WithTimeout(context.Background(), lookTimeout)
	m.cancel, m.looking, m.problem = cancel, true, ""
	return kernel.Reply(func() tea.Msg {
		defer cancel()
		found, err := recentProjects(ctx, search)
		if err != nil {
			return projectsFailedMsg{err: err}
		}
		return projectsFoundMsg{found: found}
	}, m.addr)
}

// Update handles one message.
func (m *projectModel) Update(msg tea.Msg) (kernel.View, tea.Cmd) {
	var cmd tea.Cmd
	switch msg := msg.(type) {
	case kernel.SizeMsg:
		m.resize(msg.Width, msg.Height)

	case kernel.FocusMsg:
		if msg.Focused {
			_ = m.input.Focus()
		} else {
			m.input.Blur()
		}

	case kernel.ThemeMsg:
		m.deps.Theme = msg.Theme
		m.styles = newStyles(msg.Theme)
		m.memo.reset()
		m.head = ""

	case projectsFoundMsg:
		m.landed(msg)

	case kernel.ProjectMsg:
		m.rescope(msg.Project)

	case projectsFailedMsg:
		m.stop()
		text, _ := jira.Reason(msg.err)
		m.problem = text
		cmd = kernel.Warn(text)

	case tea.KeyPressMsg:
		cmd = m.key(msg)

	case tea.MouseClickMsg:
		cmd = m.click(msg)

	case tea.MouseWheelMsg:
		m.wheel(msg)
	}
	return m, cmd
}

func (m *projectModel) landed(msg projectsFoundMsg) {
	m.stop()
	if len(msg.found) == 0 {
		m.problem = "nothing this account has touched recently names a project"
	}
	m.found = msg.found
	m.rebuild()
}

// rescope is the session changing scope while the picker is up, which is what
// choosing here does and what another view doing it looks like from here.
func (m *projectModel) rescope(key string) {
	if key == m.deps.Project {
		return
	}
	m.deps.Project = key
	m.rebuild()
}

// rebuild redraws the list around what it holds, landing back on the row the
// cursor was on: nothing here was typed, so nothing may move the selection.
func (m *projectModel) rebuild() {
	under := m.selection()
	m.rows = m.buildRows(m.found)
	m.memo.reset()
	m.head = ""
	m.refilter()
	if at := m.indexOf(under); at >= 0 {
		m.cursor = at
	}
	m.scrollToCursor()
}

func (m *projectModel) resize(w, h int) {
	if w == m.width && h == m.height {
		return
	}
	m.width, m.height = w, h
	m.lay = planLayout(w, 0)
	m.input.SetWidth(max(w-inputPrompt, 8))
	m.memo.reset()
	m.head = ""
	m.clampScroll()
}

func (m *projectModel) key(msg tea.KeyPressMsg) tea.Cmd {
	switch m.acts[msg.String()] {
	case actUp:
		m.moveTo(m.cursor - 1)
		return nil
	case actDown:
		m.moveTo(m.cursor + 1)
		return nil
	case actPageUp:
		m.moveTo(m.cursor - m.rowsHeight())
		return nil
	case actPageDown:
		m.moveTo(m.cursor + m.rowsHeight())
		return nil
	case actRun:
		return m.choose()
	case actClose:
		return kernel.Pop()
	case actNone:
	}
	m.input, _ = m.input.Update(msg)
	if q := m.input.Value(); q != m.query {
		m.query = q
		m.refilter()
	}
	return nil
}

// choose re-scopes the session. The pop goes first so that the note the kernel
// writes about the new scope is the last thing on the status line.
func (m *projectModel) choose() tea.Cmd {
	if len(m.shown) == 0 {
		return nil
	}
	row := &m.rows[m.shown[m.cursor]]
	m.freq.ran(row.key, m.deps.Now())
	return tea.Sequence(kernel.Pop(), kernel.SetProject(row.key))
}

func (m *projectModel) click(msg tea.MouseClickMsg) tea.Cmd {
	if msg.Button != tea.MouseLeft || m.deps.Zones == nil {
		return nil
	}
	for i := m.top; i < min(m.top+m.rowsHeight(), len(m.shown)); i++ {
		if !m.deps.Zones.Get(m.zone(m.shown[i])).InBounds(msg) {
			continue
		}
		if i == m.cursor {
			return m.choose()
		}
		m.moveTo(i)
		return nil
	}
	return nil
}

func (m *projectModel) zone(at int) string {
	return m.zonePrefix + zoneProject + strconv.Itoa(at)
}

func (m *projectModel) wheel(msg tea.MouseWheelMsg) {
	switch msg.Button {
	case tea.MouseWheelUp:
		m.top -= 3
	case tea.MouseWheelDown:
		m.top += 3
	default:
		return
	}
	m.clampScroll()
}

// refilter recomputes what the filter leaves and in what order. Typing lands on
// the best match, which is what typing is for; both slices are the model's own,
// so a keystroke allocates nothing.
func (m *projectModel) refilter() {
	m.shown, m.ranks = m.shown[:0], m.ranks[:0]
	pattern := app.NewPattern(strings.TrimSpace(m.query))
	now := m.deps.Now()
	for i := range m.rows {
		score, ok := m.rows[i].match(pattern)
		if !ok {
			continue
		}
		m.ranks = append(m.ranks, ranked{at: i, score: score, freq: m.freq.score(m.rows[i].key, now)})
	}
	// The filter decides which projects and frecency orders the equals, so a
	// habit never demotes a better match. The whole site keeps its place at the
	// top of an unfiltered list, because index order is the tie-break and it is
	// row zero.
	sortRanks(m.ranks)
	for _, rk := range m.ranks {
		m.shown = append(m.shown, rk.at)
	}
	m.cursor = 0
	m.scrollToCursor()
}

// selection names the row under the cursor so that a list rebuilt around it —
// the read landing — can land on it again.
func (m *projectModel) selection() string {
	if len(m.shown) == 0 || m.cursor >= len(m.shown) {
		return ""
	}
	return m.rows[m.shown[m.cursor]].label
}

func (m *projectModel) indexOf(label string) int {
	if label == "" {
		return -1
	}
	for i, at := range m.shown {
		if m.rows[at].label == label {
			return i
		}
	}
	return -1
}

func (m *projectModel) moveTo(at int) {
	if len(m.shown) == 0 {
		m.cursor, m.top = 0, 0
		return
	}
	m.cursor = min(max(at, 0), len(m.shown)-1)
	m.scrollToCursor()
}

func (m *projectModel) scrollToCursor() {
	h := m.rowsHeight()
	if m.cursor < m.top {
		m.top = m.cursor
	}
	if m.cursor >= m.top+h {
		m.top = m.cursor - h + 1
	}
	m.clampScroll()
}

func (m *projectModel) clampScroll() {
	h := m.rowsHeight()
	m.top = min(m.top, max(len(m.shown)-h, 0))
	m.top = max(m.top, 0)
}

func (m *projectModel) rowsHeight() int { return max(m.height-headHeight, 1) }

// recentProjects reads the projects behind this account's own recent issues, and
// then anything it can see at all. Both queries ask for one field.
//
// The port exposes no project-list method, so a narrow read is the only answer
// there is. Onboarding's picker asks the same question in its own package.
func recentProjects(ctx context.Context, search *app.Search) ([]project, error) {
	projection := app.Projection{Name: "project picker", IDs: []string{"project"}}
	for _, jql := range []string{"assignee = currentUser() ORDER BY updated DESC", "ORDER BY updated DESC"} {
		result, err := search.Run(ctx, app.Request{JQL: jql, Projection: projection, MaxResults: suggestionLimit})
		if err != nil {
			return nil, err
		}
		if found := distinctProjects(result.Page.Items); len(found) > 0 {
			return found, nil
		}
	}
	return nil, nil
}

// distinctProjects keeps the order the issues came back in, which is the order
// the query sorted them by and therefore the order worth offering.
func distinctProjects(issues []jira.Issue) []project {
	seen := make(map[string]bool, len(issues))
	out := make([]project, 0, 4)
	for i := range issues {
		ref := issues[i].Project
		if ref.Key == "" || seen[ref.Key] {
			continue
		}
		seen[ref.Key] = true
		out = append(out, project{key: ref.Key, name: ref.Name})
	}
	return out
}
