package move

import (
	"context"
	"flag"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"
	"unicode/utf8"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"
	zone "github.com/lrstanley/bubblezone/v2"

	"github.com/varijkapil13/saral/internal/ui/kernel"
	"github.com/varijkapil13/saral/pkg/jira"
	"github.com/varijkapil13/saral/pkg/jira/jiratest"
)

var update = flag.Bool("update", false, "rewrite the golden files")

func fullCaps() jira.Capabilities {
	ok := jira.Capability{OK: true}
	return jira.Capabilities{
		Plans: ok, BulkMove: ok, Boards: ok, Attachments: ok, DeleteIssues: ok, People: ok,
		TimeZone: time.UTC,
	}
}

// noMoveCaps is the normal answer for this wizard: Bulk Change is a global
// permission most tokens do not hold, and the probe says so in its own words.
func noMoveCaps() jira.Capabilities {
	caps := fullCaps()
	caps.BulkMove = jira.Capability{Reason: "You need the Bulk Change permission to move issues between projects"}
	return caps
}

func testDeps(client jira.SessionClient) kernel.Deps {
	return kernel.Deps{
		Jira:    client,
		Caps:    fullCaps(),
		Project: "PROJ",
		Theme:   kernel.NewTheme(kernel.ThemeNoColor, true, kernel.ASCIIGlyphs()),
		Zones:   zone.New(),
		Site:    "example.atlassian.net",
		Now:     func() time.Time { return time.Date(2026, time.March, 5, 9, 0, 0, 0, time.UTC) },
	}
}

// plainDeps draws with a theme that writes no escape sequence of its own, so
// that an escape left in a frame can only be a zone marker.
func plainDeps(client jira.SessionClient) kernel.Deps {
	d := testDeps(client)
	th := kernel.NewTheme(kernel.ThemeNoColor, true, kernel.ASCIIGlyphs())
	plain := lipgloss.NewStyle()
	for _, style := range []*lipgloss.Style{
		&th.Base, &th.Muted, &th.Accent, &th.Danger, &th.Warning, &th.Success, &th.Title,
		&th.Selected, &th.Badge, &th.StaleBadge,
	} {
		*style = plain
	}
	d.Theme = th
	return d
}

// newFake seeds two projects: the one the issues are in and the one they are
// being moved to. A wizard with one project has nowhere to go.
func newFake(issues int, opts ...jiratest.Option) *jiratest.Fake {
	return jiratest.New(append([]jiratest.Option{
		jiratest.WithProject("PROJ", jiratest.Scrum),
		jiratest.WithProject("OTHER", jiratest.Scrum),
		jiratest.WithIssues(jiratest.Gen(issues)),
	}, opts...)...)
}

// seeded is the issues a list would have handed over, read out of the fake so
// that their statuses and types are the ones the site actually holds.
func seeded(t *testing.T, f *jiratest.Fake, keys ...string) []jira.Issue {
	t.Helper()
	out := make([]jira.Issue, 0, len(keys))
	for _, key := range keys {
		iss, err := f.Issue(t.Context(), key)
		if err != nil {
			t.Fatalf("reading %s out of the fake: %v", key, err)
		}
		out = append(out, iss)
	}
	return out
}

// immediate is the pause between two questions about a task, held to account
// without being spent. Every duration it is asked for is recorded, which is how
// the backoff is asserted rather than waited out.
type immediate struct {
	mu    sync.Mutex
	waits []time.Duration
}

func (i *immediate) wait(ctx context.Context, d time.Duration) error {
	i.mu.Lock()
	i.waits = append(i.waits, d)
	i.mu.Unlock()
	return ctx.Err()
}

func (i *immediate) asked() []time.Duration {
	i.mu.Lock()
	defer i.mu.Unlock()
	return append([]time.Duration(nil), i.waits...)
}

// driver runs the wizard the way the kernel would, but keeps the messages it
// sends upward instead of acting on them, so a test can assert what it asked
// for.
type driver struct {
	t          *testing.T
	m          *Model
	statuses   []kernel.StatusMsg
	pops       int
	broadcasts []tea.Msg
}

func newDriver(t *testing.T, d kernel.Deps, w, h int, opts ...Option) *driver {
	t.Helper()
	view, ok := New(d, opts...).(*Model)
	if !ok {
		t.Fatal("New did not return a *Model")
	}
	dr := &driver{t: t, m: view}
	dr.send(kernel.SizeMsg{Width: w, Height: h})
	dr.send(kernel.FocusMsg{Focused: true})
	dr.run(dr.m.Init())
	return dr
}

func (d *driver) send(msg tea.Msg) {
	d.t.Helper()
	view, cmd := d.m.Update(msg)
	model, ok := view.(*Model)
	if !ok {
		d.t.Fatal("Update did not return a *Model")
	}
	d.m = model
	d.run(cmd)
}

// run executes commands to exhaustion. The poll's own pause is injected in every
// test that reaches one, so nothing here waits on a clock.
func (d *driver) run(cmd tea.Cmd) {
	d.t.Helper()
	queue := []tea.Cmd{cmd}
	for steps := 0; len(queue) > 0; steps++ {
		if steps > 4000 {
			d.t.Fatal("commands never settled")
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
		// The kernel takes the envelope off a view's own answer and hands the
		// message inside to the view the address names. There is one view here.
		if reply, addressed := msg.(kernel.ReplyMsg); addressed {
			msg = reply.Msg
		}
		switch msg := msg.(type) {
		case kernel.StatusMsg:
			d.statuses = append(d.statuses, msg)
		case kernel.PopMsg:
			d.pops++
		case kernel.BroadcastMsg:
			d.broadcasts = append(d.broadcasts, msg.Msg)
		default:
			view, follow := d.m.Update(msg)
			model, ok := view.(*Model)
			if !ok {
				d.t.Fatal("Update did not return a *Model")
			}
			d.m = model
			queue = append(queue, follow)
		}
	}
}

func (d *driver) key(keys ...string) {
	d.t.Helper()
	for _, k := range keys {
		d.send(keyPress(k))
	}
}

func (d *driver) typeText(text string) {
	d.t.Helper()
	for _, r := range text {
		d.send(tea.KeyPressMsg{Code: r, Text: string(r)})
	}
}

func (d *driver) view() string { return ansi.Strip(d.m.View()) }

func (d *driver) lastStatus() kernel.StatusMsg {
	if len(d.statuses) == 0 {
		return kernel.StatusMsg{}
	}
	return d.statuses[len(d.statuses)-1]
}

// walkTo takes the wizard from the target project to the confirm screen the way
// a user does: choose the project, choose the issue type, accept the remap and
// whatever the target insists on.
func (d *driver) walkTo(target string) {
	d.t.Helper()
	d.typeKey(target)
	d.key("enter") // the issue type under the cursor
	d.key("enter") // the status remap as it stands
	if d.m.step == stepFields {
		d.key("enter")
	}
	if d.m.step != stepConfirm {
		d.t.Fatalf("the walk stopped on step %d rather than the confirm screen:\n%s", d.m.step, d.view())
	}
}

func (d *driver) typeKey(target string) {
	d.t.Helper()
	d.key("i")
	d.typeText(target)
	d.key("enter")
}

func unwrapCmds(msg tea.Msg) ([]tea.Cmd, bool) {
	v := reflect.ValueOf(msg)
	if v.Kind() != reflect.Slice || v.Type().Elem() != reflect.TypeOf(tea.Cmd(nil)) {
		return nil, false
	}
	out := make([]tea.Cmd, 0, v.Len())
	for i := range v.Len() {
		cmd, _ := v.Index(i).Interface().(tea.Cmd)
		out = append(out, cmd)
	}
	return out, true
}

func keyPress(s string) tea.KeyPressMsg {
	switch s {
	case "esc":
		return tea.KeyPressMsg{Code: tea.KeyEsc}
	case "enter":
		return tea.KeyPressMsg{Code: tea.KeyEnter}
	case "tab":
		return tea.KeyPressMsg{Code: tea.KeyTab}
	case "shift+tab":
		return tea.KeyPressMsg{Code: tea.KeyTab, Mod: tea.ModShift}
	case "up":
		return tea.KeyPressMsg{Code: tea.KeyUp}
	case "down":
		return tea.KeyPressMsg{Code: tea.KeyDown}
	case "left":
		return tea.KeyPressMsg{Code: tea.KeyLeft}
	case "right":
		return tea.KeyPressMsg{Code: tea.KeyRight}
	case "home":
		return tea.KeyPressMsg{Code: tea.KeyHome}
	case "end":
		return tea.KeyPressMsg{Code: tea.KeyEnd}
	case "pgdown":
		return tea.KeyPressMsg{Code: tea.KeyPgDown}
	case "pgup":
		return tea.KeyPressMsg{Code: tea.KeyPgUp}
	case "backspace":
		return tea.KeyPressMsg{Code: tea.KeyBackspace}
	default:
		r, _ := utf8.DecodeRuneInString(s)
		return tea.KeyPressMsg{Code: r, Text: s}
	}
}

func golden(t *testing.T, name, got string) {
	t.Helper()
	path := filepath.Join("testdata", name)
	if *update {
		if err := os.MkdirAll("testdata", 0o750); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(got), 0o600); err != nil {
			t.Fatal(err)
		}
		return
	}
	want, err := os.ReadFile(path) //nolint:gosec // the path is a literal under testdata
	if err != nil {
		t.Fatalf("%v — run: go test ./internal/ui/move -update", err)
	}
	if string(want) != got {
		t.Errorf("frame differs from %s\n--- want ---\n%s\n--- got ---\n%s", path, want, got)
	}
}

func mustContain(t *testing.T, got string, want ...string) {
	t.Helper()
	for _, w := range want {
		if !strings.Contains(got, w) {
			t.Errorf("output does not contain %q:\n%s", w, got)
		}
	}
}

func mustNotContain(t *testing.T, got string, unwanted ...string) {
	t.Helper()
	for _, w := range unwanted {
		if strings.Contains(got, w) {
			t.Errorf("output still contains %q:\n%s", w, got)
		}
	}
}

func eventually(t *testing.T, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for !cond() {
		if time.Now().After(deadline) {
			t.Fatal("condition never became true")
		}
		runtime.Gosched()
	}
}

func countCalls(f *jiratest.Fake, name string) int {
	n := 0
	for _, call := range f.Calls() {
		if call == name {
			n++
		}
	}
	return n
}

// answer is what the kernel hands a view: the command's own reply with the
// envelope the kernel addresses it by taken off. It is how a command can be
// inspected before it is run, which is what proves a close gives up a read.
func answer(cmd tea.Cmd) tea.Msg {
	msg := cmd()
	if reply, addressed := msg.(kernel.ReplyMsg); addressed {
		return reply.Msg
	}
	return msg
}

// once hands the wizard one message and gives back the command it answered with
// rather than running it. It is how the poll chain is stopped between two
// answers, so that a state the fake never reaches can be delivered by hand.
func (d *driver) once(msg tea.Msg) tea.Cmd {
	d.t.Helper()
	view, cmd := d.m.Update(msg)
	model, ok := view.(*Model)
	if !ok {
		d.t.Fatal("Update did not return a *Model")
	}
	d.m = model
	return cmd
}

// running takes the wizard to a submitted move without letting the queue answer
// it, so that the answer under test is the first one it sees.
func (d *driver) running() {
	d.t.Helper()
	cmd := d.once(keyPress("y"))
	if cmd == nil {
		d.t.Fatal("the confirm screen submitted nothing")
	}
	sub, ok := answer(cmd).(submittedMsg)
	if !ok {
		d.t.Fatalf("the submit answered with %T", answer(cmd))
	}
	d.once(sub)
	if d.m.step != stepRunning {
		d.t.Fatalf("a submitted move left the wizard on step %d", d.m.step)
	}
}
