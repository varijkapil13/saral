package palette

import (
	"context"
	"slices"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"
	zone "github.com/lrstanley/bubblezone/v2"

	"github.com/varijkapil13/saral/internal/ui/kernel"
	"github.com/varijkapil13/saral/pkg/jira"
	"github.com/varijkapil13/saral/pkg/jira/jiratest"
)

// twoProjects is a site with work in two projects, none of it assigned to this
// account, so the read falls through to what the token can see at all — which is
// the half of the shape that answers on a first day.
func twoProjects(keys ...string) *jiratest.Fake {
	if len(keys) == 0 {
		keys = []string{"PROJ", "OPSHOP"}
	}
	opts := make([]jiratest.Option, 0, 2+len(keys))
	opts = append(opts,
		jiratest.WithProject(keys[0], jiratest.Scrum),
		jiratest.WithMe(jira.User{AccountID: "acct-nobody", DisplayName: "Nobody At All", TimeZone: time.UTC, Active: true}),
	)
	for _, key := range keys {
		opts = append(opts, jiratest.WithIssues(jiratest.GenFor(key, 2)))
	}
	return jiratest.New(opts...)
}

// refusesToSearch is the fake with the one call the picker makes broken.
type refusesToSearch struct {
	*jiratest.Fake
	err error
}

func (r refusesToSearch) Search(context.Context, jira.Query) (jira.Page[jira.Issue], error) {
	return jira.Page[jira.Issue]{}, r.err
}

func projectDeps(client jira.SessionClient) kernel.Deps {
	d := paletteDeps()
	d.Jira = client
	return d
}

// picker drives the project pane the way the kernel would, keeping what it asked
// for instead of acting on it.
type picker struct {
	t    *testing.T
	m    *projectModel
	msgs []tea.Msg
}

func openPicker(t *testing.T, d kernel.Deps, freq *table, w, h int) *picker {
	t.Helper()

	p := &picker{t: t, m: buildProject(d, freq)}
	p.send(kernel.SizeMsg{Width: w, Height: h})
	p.send(kernel.FocusMsg{Focused: true})
	p.run(p.m.Init())
	return p
}

func (p *picker) send(msg tea.Msg) {
	p.t.Helper()
	view, cmd := p.m.Update(msg)
	model, ok := view.(*projectModel)
	if !ok {
		p.t.Fatalf("Update returned a %T", view)
	}
	p.m = model
	p.run(cmd)
}

// run executes what the view returned, feeding the view its own answers back the
// way the kernel does with an addressed reply and keeping the rest.
func (p *picker) run(cmd tea.Cmd) {
	p.t.Helper()
	queue := []tea.Cmd{cmd}
	for steps := 0; len(queue) > 0; steps++ {
		if steps > 500 {
			p.t.Fatal("commands never settled")
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
		if reply, addressed := msg.(kernel.ReplyMsg); addressed {
			if len(reply.To) == 0 || reply.To[0] != p.m.addr {
				p.t.Fatalf("an answer came back addressed to %v, not to the picker", reply.To)
			}
			view, follow := p.m.Update(reply.Msg)
			model, ok := view.(*projectModel)
			if !ok {
				p.t.Fatalf("Update returned a %T", view)
			}
			p.m = model
			queue = append(queue, follow)
			continue
		}
		p.msgs = append(p.msgs, msg)
	}
}

func (p *picker) press(keys ...string) {
	p.t.Helper()
	for _, k := range keys {
		p.send(stroke(k))
	}
}

func (p *picker) typeText(s string) {
	p.t.Helper()
	for _, r := range s {
		p.send(tea.KeyPressMsg{Code: r, Text: string(r)})
	}
}

func (p *picker) frame() string { return ansi.Strip(p.m.View()) }

// labels are the scopes on offer, in the order they are drawn.
func (p *picker) labels() []string {
	out := make([]string, 0, len(p.m.shown))
	for _, at := range p.m.shown {
		out = append(out, p.m.rows[at].label)
	}
	return out
}

// scoped is the project every kernel.SetProject the picker produced named, and
// whether the empty key was one of them.
func (p *picker) scoped() []string {
	out := []string{}
	for _, msg := range p.msgs {
		if project, ok := msg.(kernel.ProjectMsg); ok {
			out = append(out, project.Project)
		}
	}
	return out
}

func (p *picker) popped() bool {
	for _, msg := range p.msgs {
		if _, ok := msg.(kernel.PopMsg); ok {
			return true
		}
	}
	return false
}

func (p *picker) statuses() []string {
	out := []string{}
	for _, msg := range p.msgs {
		if status, ok := msg.(kernel.StatusMsg); ok {
			out = append(out, status.Text)
		}
	}
	return out
}

func memoryProjects() *table { return openTable("", projectsPart) }

// The reason #87 was filed: kernel.SetProject had no caller anywhere in the
// tree, so a session was stuck in the scope it started in.
func TestProject_ChoosingOneReScopesTheSession(t *testing.T) {
	t.Parallel()

	p := openPicker(t, projectDeps(twoProjects()), memoryProjects(), 120, 24)
	p.typeText("OPSHOP")
	p.press("enter")

	if got := p.scoped(); len(got) != 1 || got[0] != "OPSHOP" {
		t.Errorf("the picker re-scoped the session to %v, want OPSHOP", got)
	}
	if !p.popped() {
		t.Error("the picker stayed on the stack after switching")
	}
}

// The empty key is a scope of its own and not the absence of one, so it is a row
// with a name rather than a field somebody has to clear.
func TestProject_TheWholeSiteIsAPickableOptionWithAName(t *testing.T) {
	t.Parallel()

	p := openPicker(t, projectDeps(twoProjects()), memoryProjects(), 120, 24)
	if got := p.labels(); len(got) == 0 || got[0] != "The whole site" {
		t.Fatalf("the picker offers %v, want the whole site first", got)
	}
	p.press("enter")

	got := p.scoped()
	if len(got) != 1 || got[0] != "" {
		t.Errorf("choosing the whole site named %v, want the empty key", got)
	}
}

func TestProject_OffersTheProjectsBehindTheAccountsRecentWork(t *testing.T) {
	t.Parallel()

	p := openPicker(t, projectDeps(twoProjects()), memoryProjects(), 120, 24)
	labels := strings.Join(p.labels(), " ")
	for _, want := range []string{"PROJ", "OPSHOP"} {
		if !strings.Contains(labels, want) {
			t.Errorf("the picker offers %s, want %s among them", labels, want)
		}
	}
}

// The scope in force is marked, and it is offered even when the read did not
// answer with it: a scope that is not on the list is one nobody can get back to.
func TestProject_MarksTheScopeTheSessionIsAlreadyOnAndOffersItRegardless(t *testing.T) {
	t.Parallel()

	d := projectDeps(twoProjects())
	d.Project = "ELSEWHERE"
	p := openPicker(t, d, memoryProjects(), 120, 24)

	found := false
	for _, at := range p.m.shown {
		row := &p.m.rows[at]
		if row.key == "ELSEWHERE" {
			found = row.current
		}
	}
	if !found {
		t.Errorf("the scope in force is not offered as the current one: %v", p.labels())
	}
	mustContain(t, p.frame(), "ELSEWHERE", "(current)")
}

// A failed read is a note and never a refusal: the whole site and the scope in
// force are both pickable without the site's help, and the refusal reaches the
// user in the words the site used.
func TestProject_AFailedReadIsANoteAndNotARefusal(t *testing.T) {
	t.Parallel()

	client := refusesToSearch{
		Fake: twoProjects(),
		err:  &jira.RateLimitError{RetryAfter: 30 * time.Second, Endpoint: "/search/jql"},
	}
	p := openPicker(t, projectDeps(client), memoryProjects(), 120, 24)

	if got := p.labels(); len(got) == 0 {
		t.Fatal("a failed read left nothing to pick at all")
	}
	if !strings.Contains(strings.Join(p.statuses(), " "), "rate limited by Jira") {
		t.Errorf("the refusal reached the user as %v", p.statuses())
	}
	p.press("enter")
	if got := p.scoped(); len(got) != 1 || got[0] != "" {
		t.Errorf("the whole site was not pickable after a failed read: %v", got)
	}
}

func TestProject_ARefusedTokenStillGetsTheReasonInTheSitesWords(t *testing.T) {
	t.Parallel()

	client := refusesToSearch{
		Fake: twoProjects(),
		err:  &jira.CapabilityError{Reason: "you do not have permission to search issues"},
	}
	p := openPicker(t, projectDeps(client), memoryProjects(), 120, 24)

	if !strings.Contains(strings.Join(p.statuses(), " "), "permission") {
		t.Errorf("a 403 reached the user as %v", p.statuses())
	}
}

func TestProject_ATransportFailureReachesTheStatusLine(t *testing.T) {
	t.Parallel()

	client := refusesToSearch{
		Fake: twoProjects(),
		err:  &jira.TransportError{Op: "search", Err: context.DeadlineExceeded},
	}
	p := openPicker(t, projectDeps(client), memoryProjects(), 120, 24)

	if !strings.Contains(strings.Join(p.statuses(), " "), "search failed") {
		t.Errorf("a transport failure reached the user as %v", p.statuses())
	}
}

func TestProject_WithNoConnectionAtAllStillOffersTheScopesItKnows(t *testing.T) {
	t.Parallel()

	d := projectDeps(nil)
	d.Project = "PROJ"
	p := openPicker(t, d, memoryProjects(), 120, 24)

	if got := p.labels(); len(got) != 2 {
		t.Errorf("a session with no connection offers %v, want the whole site and the scope in force", got)
	}
	p.typeText("zzzz")
	mustContain(t, p.frame(), `Nothing matches "zzzz"`, "no connection")
}

func TestProject_TypingRanksAndLandsOnTheBestMatch(t *testing.T) {
	t.Parallel()

	p := openPicker(t, projectDeps(twoProjects()), memoryProjects(), 120, 24)
	p.typeText("op")

	got := p.labels()
	if len(got) == 0 || got[0] != "OPSHOP" {
		t.Fatalf("%q offers %v first, want OPSHOP", "op", got)
	}
	if p.m.cursor != 0 {
		t.Errorf("the cursor is on row %d after typing, want the best match", p.m.cursor)
	}
	if got := p.m.rows[p.m.shown[p.m.cursor]].key; got != "OPSHOP" {
		t.Errorf("enter would switch to %q", got)
	}
}

func TestProject_NothingMatchingOffersNoKeyThatWouldDoAnything(t *testing.T) {
	t.Parallel()

	p := openPicker(t, projectDeps(twoProjects()), memoryProjects(), 120, 24)
	p.typeText("zzzz")

	set, gen := p.m.LiveKeys()
	if gen != int(projectNothing) {
		t.Errorf("the keys are in state %d with nothing on offer", gen)
	}
	if labels := actsOf(set); strings.Contains(labels, "switch to it") {
		t.Errorf("the footer offers enter with nothing to switch to: %s", labels)
	}
	p.press("enter")
	if got := p.scoped(); len(got) != 0 {
		t.Errorf("enter re-scoped the session to %v with nothing on offer", got)
	}
}

// The frecency table is the same code the commands use, over a file of its own.
// Both keys score identically against one letter, so nothing but the habit can
// order them — and it orders them either way round, so this is not the corpus
// order passing for a ranking.
func TestProject_PutsTheProjectThisMachineActuallyPicksFirst(t *testing.T) {
	t.Parallel()

	for _, habit := range []string{"ONE", "OWL"} {
		t.Run(habit, func(t *testing.T) {
			t.Parallel()

			freq := memoryProjects()
			freq.ran(habit, clockAt)
			freq.ran(habit, clockAt)

			p := openPicker(t, projectDeps(twoProjects("ONE", "OWL")), freq, 120, 24)
			p.typeText("o")

			got := p.labels()
			first, second := slices.Index(got, "ONE"), slices.Index(got, "OWL")
			if habit == "OWL" {
				first, second = second, first
			}
			if first < 0 || second < 0 {
				t.Fatalf("the picker offers %v, want both projects", got)
			}
			if first > second {
				t.Errorf("the picker offers %v; %s is what this machine picks most", got, habit)
			}
		})
	}
}

// Closing the picker has to cancel the read it started, or a discarded view goes
// on waiting for an answer nothing will deliver. The read hands its context over
// and then waits for it, so the assertion is about the cancel and not about a
// clock: the timeout on it has not run out when this looks.
func TestProject_ClosingCancelsTheReadItStarted(t *testing.T) {
	t.Parallel()

	client := capturesContext{Fake: twoProjects(), got: make(chan context.Context, 1)}
	m := buildProject(projectDeps(client), memoryProjects())
	cmd := m.Init()
	if cmd == nil {
		t.Fatal("the picker made no read at all")
	}

	answered := make(chan tea.Msg, 1)
	go func() { answered <- cmd() }()
	ctx := <-client.got
	kernel.CloseView(m)

	if ctx.Err() == nil {
		t.Fatal("closing the picker left its read running")
	}
	if _, ok := (<-answered).(kernel.ReplyMsg); !ok {
		t.Error("the read did not come back addressed to the picker")
	}
}

// capturesContext hands over the context the picker's read was made under, then
// waits for it the way a slow site would.
type capturesContext struct {
	*jiratest.Fake
	got chan context.Context
}

func (c capturesContext) Search(ctx context.Context, _ jira.Query) (jira.Page[jira.Issue], error) {
	c.got <- ctx
	<-ctx.Done()
	return jira.Page[jira.Issue]{}, ctx.Err()
}

func TestProject_Golden(t *testing.T) {
	t.Parallel()

	d := projectDeps(twoProjects())
	d.Zones = zone.New()
	p := openPicker(t, d, memoryProjects(), 120, 20)
	golden(t, "project_120x20.golden", p.frame())
}

// The trap recorded on #12: a command's Run is handed the kernel's own deps, so
// the picker is built against the session as of the keypress. A Run that closed
// over a Deps captured at registration would mark the wrong scope as current and
// offer no way back to the right one.
func TestProject_TheCommandBuildsThePickerFromTheSessionAsItIsNow(t *testing.T) {
	t.Parallel()

	cmd, ok := kernel.LookupCommand(switchCommandID)
	if !ok {
		t.Fatalf("%s is not registered, so nothing opens the picker", switchCommandID)
	}
	if len(cmd.Keys) != 0 {
		t.Errorf("the command teaches %v, and no key reaches the picker", cmd.Keys)
	}

	d := projectDeps(nil)
	d.Project = "LATER"
	msg, isPush := cmd.Run(d)().(kernel.PushMsg)
	if !isPush {
		t.Fatalf("running %s did not push a view", switchCommandID)
	}
	if msg.ID != projectViewID {
		t.Errorf("the picker was pushed as %q", msg.ID)
	}
	model, isPicker := msg.View.(*projectModel)
	if !isPicker {
		t.Fatalf("the command pushed a %T", msg.View)
	}
	if got := model.deps.Project; got != "LATER" {
		t.Errorf("the picker was built against project %q, want the one the session is on now", got)
	}
}

// A scope changing under the picker — this pane is not the only thing that can
// change one — re-marks the rows without asking the site again.
func TestProject_AScopeChangingUnderThePickerReMarksTheRows(t *testing.T) {
	t.Parallel()

	p := openPicker(t, projectDeps(twoProjects()), memoryProjects(), 120, 24)
	p.send(kernel.ProjectMsg{Project: "OPSHOP"})

	for _, at := range p.m.shown {
		row := &p.m.rows[at]
		if want := row.key == "OPSHOP"; row.current != want {
			t.Errorf("%q is marked current=%t after the switch, want %t", row.label, row.current, want)
		}
	}
	mustContain(t, p.frame(), "OPSHOP  (current)")
}
