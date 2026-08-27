package palette

import (
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/varijkapil13/saral/internal/app"
	"github.com/varijkapil13/saral/internal/ui/kernel"
	"github.com/varijkapil13/saral/internal/ui/list"
	"github.com/varijkapil13/saral/pkg/jira/jiratest"
)

// The case the kernel seam exists for. internal/ui/list registers three
// searches whose Run narrows the JQL with Deps.Project; the palette holds a
// Deps copied when it was built, so a palette that called Run itself would
// search whichever project the session started in and look like it worked.
func TestSession_RunsASearchScopedToTheProjectTheSessionIsOnNow(t *testing.T) {
	resetShared(t)

	s := boot(t, 120, 30)
	s.send(kernel.ProjectMsg{Project: "OTHER"})
	s.press("ctrl+k")
	s.typeText("my issues")
	s.press("enter")

	jql := s.queries()
	if len(jql) != 1 {
		t.Fatalf("the palette produced %v searches, want one", jql)
	}
	if !strings.Contains(jql[0], `project = "OTHER"`) {
		t.Errorf("the search reads %q; the session was re-scoped to OTHER before it ran", jql[0])
	}
	if !strings.Contains(jql[0], "assignee = currentUser()") {
		t.Errorf("the search reads %q, which is not My issues at all", jql[0])
	}
}

// ctrl+k opens over the view it was pressed in, esc puts it away, and the footer
// shows the palette's own keys plus the one global it cannot swallow.
func TestSession_Frame(t *testing.T) {
	resetShared(t)

	s := boot(t, 120, 30)
	s.press("ctrl+k")
	golden(t, "session_120x30.golden", ansi.Strip(s.m.Frame()))

	footer := lastLine(ansi.Strip(s.m.Frame()))
	mustContain(t, footer, "run it", "close", "ctrl+k")
	mustNotContain(t, footer, "? ctrl+k", "quit", "help")

	s.press("esc")
	mustNotContain(t, ansi.Strip(s.m.Frame()), "what do you want to do?")
}

// Pressing it twice leaves one palette, and the second press does not reset the
// filter typed into the first: the kernel refuses to stack another.
func TestSession_CtrlKTwiceLeavesTheFilterAlone(t *testing.T) {
	resetShared(t)

	s := boot(t, 120, 30)
	s.press("ctrl+k")
	s.typeText("dark")
	s.press("ctrl+k")
	mustContain(t, ansi.Strip(s.m.Frame()), "> dark")
}

// The hint has to survive the pop: the kernel broadcasts CommandRanMsg while the
// palette is still on the stack, then pops it — and popping clears the status
// line the hint is asking for.
func TestSession_TheThirdRunFromThePaletteNamesTheKeyOnTheStatusLine(t *testing.T) {
	resetShared(t)

	s := boot(t, 120, 30)
	for run := 1; run <= 3; run++ {
		s.press("ctrl+k")
		s.typeText("edit this issue")
		s.press("enter")
		hinted := strings.Contains(ansi.Strip(s.m.Frame()), "e runs Edit this issue without the palette")
		if hinted != (run == hintAfter) {
			t.Errorf("run %d: the status line hints %t", run, hinted)
		}
	}
}

// The other half of the palette, through the whole program: a few letters of an
// issue key, and the detail pane opens over the list the palette was opened from
// rather than over the palette itself.
func TestSession_OpensACachedIssueOverTheViewThePaletteWasOpenedFrom(t *testing.T) {
	resetShared(t)

	cache := newFakeCache()
	for _, iss := range jiratest.Gen(4) {
		cache.hold(iss.Key, iss.Summary, clockAt.Add(-time.Minute))
	}
	s := bootWith(t, cache, 120, 30)
	s.press("ctrl+k")
	s.typeText("proj-3")
	s.press("enter")

	header := firstLine(ansi.Strip(s.m.Frame()))
	if !strings.Contains(header, "PROJ-3") {
		t.Fatalf("the frame is headed %q, want the issue that was chosen", header)
	}
	mustNotContain(t, ansi.Strip(s.m.Frame()), "what do you want to do")

	s.press("esc")
	back := firstLine(ansi.Strip(s.m.Frame()))
	if strings.Contains(back, "PROJ-3") || !strings.Contains(back, "Issues") {
		t.Errorf("esc from the issue landed on %q, want the view the palette was opened over", back)
	}
}

// resetShared empties the tables the running program shares between opens, so
// that a test asserting an order does not depend on what another test ran. The
// kernel builds the palette through New and the picker through the registered
// command, and those are the shared tables' only users.
func resetShared(t *testing.T) {
	t.Helper()
	for _, freq := range []*table{sharedTable(), sharedProjectTable()} {
		freq.mu.Lock()
		freq.uses = make(map[string]use, 8)
		freq.mu.Unlock()
	}
}

// session drives the whole program: the kernel, the registered views and the
// palette the kernel builds on ctrl+k. It keeps every message that passed
// through so that a test can assert what a command asked for.
type session struct {
	t    *testing.T
	m    kernel.Model
	msgs []tea.Msg
}

func boot(t *testing.T, w, h int) *session { return bootWith(t, nil, w, h) }

func bootWith(t *testing.T, cache app.Cache, w, h int) *session {
	t.Helper()
	d := paletteDeps()
	d.Caps = fullCaps()
	d.Cache = cache
	d.Jira = jiratest.New(
		jiratest.WithProject("PROJ", jiratest.Scrum),
		jiratest.WithIssues(jiratest.Gen(4)),
	)
	m, err := kernel.New(d, kernel.WithSize(w, h), kernel.WithMouse(false))
	if err != nil {
		t.Fatalf("kernel.New: %v", err)
	}
	s := &session{t: t, m: m}
	s.send(tea.WindowSizeMsg{Width: w, Height: h})
	s.run(s.m.Init())
	return s
}

func (s *session) send(msg tea.Msg) {
	s.t.Helper()
	next, cmd := s.m.Update(msg)
	model, ok := next.(kernel.Model)
	if !ok {
		s.t.Fatal("the kernel stopped returning its own model")
	}
	s.m = model
	s.run(cmd)
}

func (s *session) run(cmd tea.Cmd) {
	s.t.Helper()
	queue := []tea.Cmd{cmd}
	for steps := 0; len(queue) > 0; steps++ {
		if steps > 4000 {
			s.t.Fatal("commands never settled")
		}
		next := queue[0]
		queue = queue[1:]
		if next == nil {
			continue
		}
		msg := next()
		if msg == nil {
			continue
		}
		if cmds, ok := unwrapCmds(msg); ok {
			queue = append(queue, cmds...)
			continue
		}
		s.msgs = append(s.msgs, msg)
		updated, follow := s.m.Update(msg)
		model, ok := updated.(kernel.Model)
		if !ok {
			s.t.Fatal("the kernel stopped returning its own model")
		}
		s.m = model
		queue = append(queue, follow)
	}
}

func (s *session) press(keys ...string) {
	s.t.Helper()
	for _, k := range keys {
		if k == "ctrl+k" {
			s.send(tea.KeyPressMsg{Code: 'k', Mod: tea.ModCtrl})
			continue
		}
		s.send(stroke(k))
	}
}

func (s *session) typeText(text string) {
	s.t.Helper()
	for _, r := range text {
		if r == ' ' {
			s.send(tea.KeyPressMsg{Code: tea.KeySpace, Text: " "})
			continue
		}
		s.send(tea.KeyPressMsg{Code: r, Text: string(r)})
	}
}

// queries are the searches the issue list was handed, which is where a search
// command ends up.
func (s *session) queries() []string {
	out := []string{}
	for _, msg := range s.msgs {
		broadcast, ok := msg.(kernel.BroadcastMsg)
		if !ok {
			continue
		}
		if query, isQuery := broadcast.Msg.(list.QueryMsg); isQuery {
			out = append(out, query.JQL)
		}
	}
	return out
}

func firstLine(frame string) string {
	return strings.SplitN(frame, "\n", 2)[0]
}

func lastLine(frame string) string {
	lines := strings.Split(strings.TrimRight(frame, "\n"), "\n")
	return lines[len(lines)-1]
}

// The whole gesture through the running program: ctrl+k, the command, the
// picker, a project. kernel.SetProject had no caller anywhere in the tree, so a
// session was stuck in the scope it started in — this is the proof it is not.
func TestSession_SwitchesProjectFromThePalette(t *testing.T) {
	resetShared(t)

	s := boot(t, 120, 30)
	s.send(kernel.ProjectMsg{Project: "OTHER"})
	s.press("ctrl+k")
	s.typeText("switch project")
	s.press("enter")

	header := firstLine(ansi.Strip(s.m.Frame()))
	if !strings.Contains(header, "Project") {
		t.Fatalf("the frame is headed %q, want the picker the command opens", header)
	}
	mustContain(t, ansi.Strip(s.m.Frame()), "which project?", "The whole site")

	s.typeText("PROJ")
	s.press("enter")

	// The picker is gone and the view underneath it is on the new scope: the
	// kernel forwards the switch to the roots parked off screen, so the search
	// this session is looking at is the one the project chose.
	frame := ansi.Strip(s.m.Frame())
	mustNotContain(t, frame, "which project?")
	mustContain(t, frame, "PROJ", "All issues in PROJ")
	if got := s.scoped(); len(got) == 0 || got[len(got)-1] != "PROJ" {
		t.Errorf("the session was re-scoped to %v, want PROJ last", got)
	}
}

// And the whole site, which is a scope of its own: the kernel says so in its own
// words rather than reading as a cleared field.
func TestSession_SwitchesToTheWholeSiteFromThePalette(t *testing.T) {
	resetShared(t)

	s := boot(t, 120, 30)
	s.press("ctrl+k")
	s.typeText("switch project")
	s.press("enter")
	s.press("enter")

	// The header, not the status line: the scope note lands there and the list's
	// own sentence about an empty search replaces it, so the header is the only
	// place the scope is still legible a frame later.
	if head := firstLine(ansi.Strip(s.m.Frame())); strings.Contains(head, "PROJ") {
		t.Errorf("the header still names PROJ after a switch to the whole site:\n%s", head)
	}
	if got := s.scoped(); len(got) != 1 || got[0] != "" {
		t.Errorf("the session was re-scoped to %v, want the whole site", got)
	}
}

// scoped is every project the session was re-scoped to, in order.
func (s *session) scoped() []string {
	out := []string{}
	for _, msg := range s.msgs {
		if project, ok := msg.(kernel.ProjectMsg); ok {
			out = append(out, project.Project)
		}
	}
	return out
}
