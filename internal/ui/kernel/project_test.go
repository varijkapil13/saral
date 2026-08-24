package kernel

import (
	"context"
	"errors"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/varijkapil13/saral/pkg/jira"
	"github.com/varijkapil13/saral/pkg/jira/jiratest"
)

// probeRecorder is the fake with the project key of every probe written down,
// answering with capabilities that name the key it was asked about. Which probe
// an answer came from is otherwise invisible, and that is the whole question a
// project switch raises.
type probeRecorder struct {
	*jiratest.Fake

	mu     sync.Mutex
	probed []string
}

func newProbeRecorder(keys ...string) *probeRecorder {
	opts := make([]jiratest.Option, 0, len(keys))
	for _, key := range keys {
		opts = append(opts, jiratest.WithProject(key, jiratest.Scrum))
	}
	return &probeRecorder{Fake: jiratest.New(opts...)}
}

func (p *probeRecorder) Capabilities(ctx context.Context, projectKey string) (jira.Capabilities, error) {
	p.mu.Lock()
	p.probed = append(p.probed, projectKey)
	p.mu.Unlock()

	caps, err := p.Fake.Capabilities(ctx, projectKey)
	if err != nil {
		return caps, err
	}
	caps.Plans = jira.Capability{Reason: answeredFor(projectKey)}
	return caps, nil
}

func (p *probeRecorder) probes() []string {
	p.mu.Lock()
	defer p.mu.Unlock()
	return slices.Clone(p.probed)
}

func answeredFor(key string) string {
	if key == "" {
		return "answered for the whole site"
	}
	return "answered for " + key
}

// projectSpy is a root view that writes down the switches it is told about,
// including the ones that arrive while it is parked off screen.
type projectSpy struct {
	stubView
	projects []string
}

func (p *projectSpy) Update(msg tea.Msg) (View, tea.Cmd) {
	if m, ok := msg.(ProjectMsg); ok {
		p.projects = append(p.projects, m.Project)
	}
	_, cmd := p.stubView.Update(msg)
	return p, cmd
}

// scopedDeps is a session pointed at a project, with a client that answers for
// it and capabilities that have not been probed yet.
func scopedDeps(rec *probeRecorder, project string) Deps {
	d := testDeps()
	d.Jira, d.Project, d.Caps = rec, project, jira.Capabilities{}
	return d
}

// runCmd runs a command tree and returns the messages it produced without
// feeding any of them back, so a test can choose the order they land in.
func runCmd(cmd tea.Cmd) []tea.Msg {
	if cmd == nil {
		return nil
	}
	msg := cmd()
	if batch, ok := msg.(tea.BatchMsg); ok {
		out := make([]tea.Msg, 0, len(batch))
		for _, c := range batch {
			out = append(out, runCmd(c)...)
		}
		return out
	}
	if msg == nil {
		return nil
	}
	return []tea.Msg{msg}
}

// deliver runs a command tree against the model the way the runtime would.
func deliver(t *testing.T, m Model, cmd tea.Cmd) Model {
	t.Helper()
	for _, msg := range runCmd(cmd) {
		next, follow := m.Update(msg)
		m = next.(Model)
		m = deliver(t, m, follow)
	}
	return m
}

// probeAnswer picks the probe's reply out of everything a command tree produced.
func probeAnswer(t *testing.T, cmd tea.Cmd) tea.Msg {
	t.Helper()
	for _, msg := range runCmd(cmd) {
		switch msg.(type) {
		case capsProbedMsg, capsFailedMsg:
			return msg
		}
	}
	t.Fatal("no probe was started")
	return nil
}

func switchTo(t *testing.T, m Model, key string) (Model, tea.Cmd) {
	t.Helper()
	msg, ok := SetProject(key)().(ProjectMsg)
	if !ok {
		t.Fatal("SetProject did not produce a ProjectMsg")
	}
	next, cmd := m.Update(msg)
	return next.(Model), cmd
}

func TestInit_AsksWhatTheTokenCanDoInTheSessionsProject(t *testing.T) {
	resetRegistry()
	t.Cleanup(resetRegistry)
	RegisterView(spec("board", 1, "", &stubView{id: "board"}))

	rec := newProbeRecorder("ONE")
	m := newAt(t, scopedDeps(rec, "ONE"), 120, 30)
	if m.capsProbed {
		t.Fatal("a session claims to have probed before anything ran")
	}

	m = deliver(t, m, m.Init())

	if got := rec.probes(); !slices.Equal(got, []string{"ONE"}) {
		t.Errorf("startup probed %v, want one probe for ONE", got)
	}
	if !m.capsProbed {
		t.Error("the probe answered and the session still says nothing has been checked")
	}
	if got := m.deps.Caps.Plans.Reason; got != answeredFor("ONE") {
		t.Errorf("the session is holding %q, want the answer for ONE", got)
	}
}

func TestOpen_ExplainsWhyAGatedViewWillNotOpen(t *testing.T) {
	tests := map[string]struct {
		probed  bool
		caps    jira.Capabilities
		want    string
		refused string
	}{
		"nothing has been probed": {
			want:    "nothing has been checked on this site yet",
			refused: "is not available on this site",
		},
		"the probe gave a reason": {
			probed: true,
			caps:   jira.Capabilities{Plans: jira.Capability{Reason: "Plans need Administer Jira"}},
			want:   "Plans need Administer Jira",
		},
		"the probe gave no reason": {
			probed: true,
			want:   "Plans is not available on this site",
		},
	}

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			resetRegistry()
			t.Cleanup(resetRegistry)
			RegisterView(spec("board", 1, "", &stubView{id: "board"}))
			RegisterView(spec("plans", 2, jira.CapPlans, &stubView{id: "plans"}))

			d := testDeps()
			d.Caps = jira.Capabilities{}
			m := newAt(t, d, 120, 30)
			if tc.probed {
				next, _ := m.Update(CapabilitiesMsg{Caps: tc.caps})
				m = next.(Model)
			}

			m, _ = press(m, "g", "2")
			got := ansi.Strip(m.Frame())
			if !strings.Contains(got, tc.want) {
				t.Errorf("the status does not say %q:\n%s", tc.want, got)
			}
			if tc.refused != "" && strings.Contains(got, tc.refused) {
				t.Errorf("a probe that never ran was reported as a refusal:\n%s", got)
			}
		})
	}
}

func TestSetProject_ReprobesAgainstTheNewKeyAndSaysSo(t *testing.T) {
	resetRegistry()
	t.Cleanup(resetRegistry)
	RegisterView(spec("board", 1, "", &stubView{id: "board"}))

	rec := newProbeRecorder("ONE", "TWO")
	m := newAt(t, scopedDeps(rec, "ONE"), 120, 30)
	m = deliver(t, m, m.Init())

	m, cmd := switchTo(t, m, "TWO")
	if got := ansi.Strip(m.Frame()); !strings.Contains(got, "TWO") || !strings.Contains(got, "re-checking") {
		t.Errorf("a switch did not say it was re-checking:\n%s", got)
	}
	if got := m.deps.Caps.Plans.Reason; got != answeredFor("ONE") {
		t.Errorf("the capabilities were blanked during the probe: %q", got)
	}

	m = deliver(t, m, cmd)
	if got := rec.probes(); !slices.Equal(got, []string{"ONE", "TWO"}) {
		t.Errorf("probed %v, want ONE then TWO", got)
	}
	if m.deps.Project != "TWO" {
		t.Errorf("the session is scoped to %q, want TWO", m.deps.Project)
	}
	if got := m.deps.Caps.Plans.Reason; got != answeredFor("TWO") {
		t.Errorf("the session is holding %q, want the answer for TWO", got)
	}
}

func TestSetProject_SwitchingToNoProjectIsAScopeOfItsOwn(t *testing.T) {
	resetRegistry()
	t.Cleanup(resetRegistry)
	RegisterView(spec("board", 1, "", &stubView{id: "board"}))

	rec := newProbeRecorder("ONE")
	m := newAt(t, scopedDeps(rec, "ONE"), 120, 30)
	m = deliver(t, m, m.Init())

	m, cmd := switchTo(t, m, "")
	m = deliver(t, m, cmd)

	if got := rec.probes(); !slices.Equal(got, []string{"ONE", ""}) {
		t.Errorf("probed %v, want ONE then the whole site", got)
	}
	if m.deps.Project != "" {
		t.Errorf("the session is scoped to %q, want no project", m.deps.Project)
	}
	if got := ansi.Strip(m.Frame()); !strings.Contains(got, "no project is selected") {
		t.Errorf("dropping the project did not say what the session is scoped to now:\n%s", got)
	}
}

func TestSetProject_SwitchingToTheSameKeyChangesNothing(t *testing.T) {
	resetRegistry()
	t.Cleanup(resetRegistry)
	RegisterView(spec("board", 1, "", &stubView{id: "board"}))

	rec := newProbeRecorder("ONE")
	m := newAt(t, scopedDeps(rec, "ONE"), 120, 30)
	m = deliver(t, m, m.Init())

	m, cmd := switchTo(t, m, "  ONE  ")
	m = deliver(t, m, cmd)

	if got := rec.probes(); !slices.Equal(got, []string{"ONE"}) {
		t.Errorf("probed %v; switching to the project already open should not ask again", got)
	}
	if m.status != "" {
		t.Errorf("a switch that changed nothing put %q on the status line", m.status)
	}
}

func TestSetProject_TheAnswerToAnOvertakenProbeIsDropped(t *testing.T) {
	resetRegistry()
	t.Cleanup(resetRegistry)
	RegisterView(spec("board", 1, "", &stubView{id: "board"}))

	rec := newProbeRecorder("ONE", "TWO", "THREE")
	m := newAt(t, scopedDeps(rec, "ONE"), 120, 30)
	m = deliver(t, m, m.Init())

	m, first := switchTo(t, m, "TWO")
	older := probeAnswer(t, first)
	m, second := switchTo(t, m, "THREE")
	newer := probeAnswer(t, second)

	for _, msg := range []tea.Msg{newer, older} {
		next, cmd := m.Update(msg)
		m = deliver(t, next.(Model), cmd)
	}

	if got := m.deps.Caps.Plans.Reason; got != answeredFor("THREE") {
		t.Errorf("the session is holding %q; the overtaken probe's answer won", got)
	}
}

func TestSetProject_ReachesARootViewTheUserSwitchedAwayFrom(t *testing.T) {
	resetRegistry()
	t.Cleanup(resetRegistry)
	board := &projectSpy{stubView: stubView{id: "board"}}
	RegisterView(ViewSpec{ID: "board", Title: "Board", Slot: 1, New: func(Deps) View { return board }})
	RegisterView(spec("backlog", 2, "", &stubView{id: "backlog"}))

	rec := newProbeRecorder("ONE", "TWO")
	m := newAt(t, scopedDeps(rec, "ONE"), 120, 30)
	m, _ = press(m, "g", "2")

	m, cmd := switchTo(t, m, "TWO")
	_ = deliver(t, m, cmd)

	if !slices.Equal(board.projects, []string{"TWO"}) {
		t.Errorf("a parked root view was told %v about the switch, want TWO", board.projects)
	}
}

func TestHeader_NamesTheProjectAndRebuildsWhenItChanges(t *testing.T) {
	resetRegistry()
	t.Cleanup(resetRegistry)
	RegisterView(spec("board", 1, "", &stubView{id: "board"}))

	rec := newProbeRecorder("ONE", "TWO")
	m := newAt(t, scopedDeps(rec, "ONE"), 120, 30)

	first := firstLine(ansi.Strip(m.Frame()))
	if !strings.Contains(first, "ONE") || !strings.Contains(first, "example.atlassian.net") {
		t.Fatalf("the header does not name what the session is pointed at:\n%s", first)
	}

	m, cmd := switchTo(t, m, "TWO")
	m = deliver(t, m, cmd)

	switched := firstLine(ansi.Strip(m.Frame()))
	if strings.Contains(switched, "ONE") || !strings.Contains(switched, "TWO") {
		t.Errorf("the memoized header survived a switch:\n%s", switched)
	}
}

func TestHeader_IsNotMemoizedAcrossAProjectChange(t *testing.T) {
	resetRegistry()
	t.Cleanup(resetRegistry)
	RegisterView(spec("board", 1, "", &stubView{id: "board"}))

	rec := newProbeRecorder("ONE", "TWO")
	m := newAt(t, scopedDeps(rec, "ONE"), 120, 30)
	if got := firstLine(ansi.Strip(m.Frame())); !strings.Contains(got, "ONE") {
		t.Fatalf("the header does not name the project:\n%s", got)
	}

	// Nothing else the memo key holds moves here, so the project on its own has
	// to be enough to rebuild the header.
	m.deps.Project = "TWO"

	if got := firstLine(ansi.Strip(m.Frame())); !strings.Contains(got, "TWO") {
		t.Errorf("the memoized header outlived the project it names:\n%s", got)
	}
}

func TestHeader_NamesOnlyTheSiteWhenNoProjectIsSelected(t *testing.T) {
	resetRegistry()
	t.Cleanup(resetRegistry)
	RegisterView(spec("board", 1, "", &stubView{id: "board"}))

	m := newAt(t, testDeps(), 120, 30)
	if got := firstLine(ansi.Strip(m.Frame())); strings.Count(got, "|") != 1 {
		t.Errorf("an unscoped session drew a separator for a project it does not have:\n%s", got)
	}
}

func TestSetProject_AFailedReprobeLeavesTheStandingAnswersAlone(t *testing.T) {
	tests := map[string]error{
		"403": &jira.CapabilityError{Capability: jira.CapBulkMove, Reason: "needs Bulk Change permission"},
		"429": &jira.RateLimitError{RetryAfter: 30 * time.Second, Endpoint: "/rest/api/3/mypermissions"},
		"transport": &jira.TransportError{
			Op: "GET /rest/api/3/mypermissions", Err: errors.New("connection refused"),
		},
	}

	for name, failure := range tests {
		t.Run(name, func(t *testing.T) {
			resetRegistry()
			t.Cleanup(resetRegistry)
			RegisterView(spec("board", 1, "", &stubView{id: "board"}))

			rec := newProbeRecorder("ONE", "TWO")
			m := newAt(t, scopedDeps(rec, "ONE"), 120, 30)
			m = deliver(t, m, m.Init())

			rec.FailNext(failure)
			m, cmd := switchTo(t, m, "TWO")
			m = deliver(t, m, cmd)

			want, _ := jira.Reason(failure)
			if m.status != want {
				t.Errorf("the status reads %q, want the error's own wording %q", m.status, want)
			}
			if m.statusLevel != LevelError {
				t.Errorf("a failed probe was reported at level %v", m.statusLevel)
			}
			if got := m.deps.Caps.Plans.Reason; got != answeredFor("ONE") {
				t.Errorf("a failed probe threw away what was already known: %q", got)
			}
			if m.deps.Project != "TWO" {
				t.Errorf("the session is scoped to %q, want TWO whatever the probe did", m.deps.Project)
			}
		})
	}
}

func firstLine(s string) string {
	line, _, _ := strings.Cut(s, "\n")
	return line
}

// BenchmarkFrameScopedToAProject holds the redraw budget for a header that
// names a project as well as a site.
func BenchmarkFrameScopedToAProject(b *testing.B) {
	resetRegistry()
	b.Cleanup(resetRegistry)
	RegisterView(ViewSpec{ID: "board", Title: "Board", Slot: 1,
		New: func(Deps) View { return &stubView{id: "board", content: strings.Repeat("row\n", 40)} }})
	RegisterKeys("board", KeySet{Short: []Binding{Bind([]string{"enter"}, "enter", "open")}})

	d := testDeps()
	d.Project = "PROJ"
	m, err := New(d, WithSize(200, 60), WithMouse(false))
	if err != nil {
		b.Fatal(err)
	}
	next, _ := m.Update(tea.WindowSizeMsg{Width: 200, Height: 60})
	m = next.(Model)

	b.ReportAllocs()
	b.ResetTimer()
	for range b.N {
		_ = m.Frame()
	}
}
