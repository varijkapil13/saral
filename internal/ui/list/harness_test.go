package list

import (
	"flag"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"
	zone "github.com/lrstanley/bubblezone/v2"

	"github.com/varijkapil13/saral/internal/ui/kernel"
	"github.com/varijkapil13/saral/pkg/jira"
	"github.com/varijkapil13/saral/pkg/jira/jiratest"
)

var update = flag.Bool("update", false, "rewrite the golden files")

// allJQL asks for the whole project in key order. The query the view opens on
// is the account's own work, which is the right default and the wrong fixture:
// it depends on who the fake says you are.
const allJQL = `project = "PROJ" ORDER BY key`

// allUpdated is the search a is bound to, and the one a term set with nothing
// left in it lands back on.
const allUpdated = `project = "PROJ" ORDER BY updated DESC`

func fullCaps() jira.Capabilities {
	ok := jira.Capability{OK: true}
	return jira.Capabilities{
		Plans: ok, BulkMove: ok, Boards: ok, Attachments: ok, DeleteIssues: ok, People: ok,
		TimeZone: time.UTC,
	}
}

func testDeps(client jira.Client) kernel.Deps {
	return kernel.Deps{
		Jira:    client,
		Caps:    fullCaps(),
		Project: "PROJ",
		Theme:   kernel.NewTheme(kernel.ThemeNoColor, true, kernel.ASCIIGlyphs()),
		Zones:   zone.New(),
		Site:    "example.atlassian.net",
		Now:     func() time.Time { return time.Date(2025, time.March, 5, 9, 0, 0, 0, time.UTC) },
	}
}

func newFake(issues int, opts ...jiratest.Option) *jiratest.Fake {
	return jiratest.New(append([]jiratest.Option{
		jiratest.WithProject("PROJ", jiratest.Scrum),
		jiratest.WithIssues(jiratest.Gen(issues)),
	}, opts...)...)
}

// driver runs the view the way the kernel would, but keeps the messages the
// view sends upward — a status line, a pushed pane — instead of acting on them,
// so a test can assert what the view asked for.
type driver struct {
	t        *testing.T
	m        *Model
	statuses []kernel.StatusMsg
	pushes   []kernel.PushMsg
}

func newDriver(t *testing.T, d kernel.Deps, w, h int) *driver {
	t.Helper()
	view, ok := New(d).(*Model)
	if !ok {
		t.Fatal("New did not return a *Model")
	}
	dr := &driver{t: t, m: view}
	dr.send(kernel.SizeMsg{Width: w, Height: h})
	dr.send(kernel.FocusMsg{Focused: true})
	dr.run(dr.m.Init())
	return dr
}

// open is a driver on every issue in the project rather than on the account's
// own, which is what the fixtures want.
func openAll(t *testing.T, d kernel.Deps, w, h int) *driver {
	t.Helper()
	dr := newDriver(t, d, w, h)
	dr.send(QueryMsg{JQL: allJQL, Title: "All issues"})
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

// run executes commands to exhaustion. Nothing in this package returns a
// command that waits on a clock, so it terminates.
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
		case kernel.PushMsg:
			d.pushes = append(d.pushes, msg)
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

// start builds the kernel around the registered list view, sizes it and lets
// the first search settle, exactly as a running program would.
func start(t *testing.T, d kernel.Deps, w, h int, opts ...kernel.Option) kernel.Model {
	t.Helper()
	m, err := kernel.New(d, append([]kernel.Option{kernel.WithSize(w, h)}, opts...)...)
	if err != nil {
		t.Fatalf("kernel.New: %v", err)
	}
	next, _ := m.Update(tea.WindowSizeMsg{Width: w, Height: h})
	return drain(t, next.(kernel.Model), next.(kernel.Model).Init())
}

func startAll(t *testing.T, d kernel.Deps, w, h int, opts ...kernel.Option) kernel.Model {
	t.Helper()
	m := start(t, d, w, h, opts...)
	return send(t, m, kernel.BroadcastMsg{Msg: QueryMsg{JQL: allJQL, Title: "All issues"}})
}

func send(t *testing.T, m kernel.Model, msg tea.Msg) kernel.Model {
	t.Helper()
	next, cmd := m.Update(msg)
	return drain(t, next.(kernel.Model), cmd)
}

// drain runs commands to exhaustion against the kernel, which is what the
// Bubble Tea runtime does for a real program.
func drain(t *testing.T, m kernel.Model, cmd tea.Cmd) kernel.Model {
	t.Helper()
	queue := []tea.Cmd{cmd}
	for steps := 0; len(queue) > 0; steps++ {
		if steps > 4000 {
			t.Fatal("commands never settled")
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
		updated, follow := m.Update(msg)
		m = updated.(kernel.Model)
		queue = append(queue, follow)
	}
	return m
}

// unwrapCmds opens a batch or a sequence. Bubble Tea exports the first as
// BatchMsg and keeps the second unexported, so both are recognised by shape
// rather than by name.
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
	case "up":
		return tea.KeyPressMsg{Code: tea.KeyUp}
	case "down":
		return tea.KeyPressMsg{Code: tea.KeyDown}
	case "home":
		return tea.KeyPressMsg{Code: tea.KeyHome}
	case "end":
		return tea.KeyPressMsg{Code: tea.KeyEnd}
	case "pgdown":
		return tea.KeyPressMsg{Code: tea.KeyPgDown}
	case "pgup":
		return tea.KeyPressMsg{Code: tea.KeyPgUp}
	case "ctrl+g":
		return tea.KeyPressMsg{Code: 'g', Mod: tea.ModCtrl}
	case "ctrl+d":
		return tea.KeyPressMsg{Code: 'd', Mod: tea.ModCtrl}
	case "ctrl+u":
		return tea.KeyPressMsg{Code: 'u', Mod: tea.ModCtrl}
	default:
		r, _ := utf8.DecodeRuneInString(s)
		return tea.KeyPressMsg{Code: r, Text: s}
	}
}

func frame(m kernel.Model) string { return ansi.Strip(m.Frame()) }

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
		t.Fatalf("%v — run: go test ./internal/ui/list -update", err)
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
