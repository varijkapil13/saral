package sprint

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

func newFake(opts ...jiratest.Option) *jiratest.Fake {
	return jiratest.New(append([]jiratest.Option{
		jiratest.WithProject("PROJ", jiratest.Scrum),
		jiratest.WithIssues(jiratest.Gen(6)),
	}, opts...)...)
}

// watcher records the states every sprint read was narrowed to, which is the
// only way to hold the view to asking the endpoint for the states it wants
// rather than walking a board's whole history and filtering what came back.
type watcher struct {
	*jiratest.Fake
	mu    sync.Mutex
	asked [][]jira.SprintState
}

func watching(f *jiratest.Fake) *watcher { return &watcher{Fake: f} }

func (w *watcher) Sprints(ctx context.Context, boardID int64, states ...jira.SprintState) (jira.Page[jira.Sprint], error) {
	w.mu.Lock()
	w.asked = append(w.asked, append([]jira.SprintState(nil), states...))
	w.mu.Unlock()
	return w.Fake.Sprints(ctx, boardID, states...)
}

func (w *watcher) reads() [][]jira.SprintState {
	w.mu.Lock()
	defer w.mu.Unlock()
	return append([][]jira.SprintState(nil), w.asked...)
}

// driver runs the view the way the kernel would, but keeps the messages it
// sends upward instead of acting on them, so a test can assert what it asked
// for.
type driver struct {
	t          *testing.T
	m          *Model
	statuses   []kernel.StatusMsg
	pops       int
	broadcasts []tea.Msg
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

// onSprint puts the cursor on the sprint with this name, and fails when the
// list does not hold one — a test that silently acted on the wrong row is worse
// than one that stops.
func (d *driver) onSprint(name string) {
	d.t.Helper()
	for i := range d.m.sprints {
		if d.m.sprints[i].Name == name {
			d.m.cursor = i
			d.m.scrollToCursor()
			return
		}
	}
	d.t.Fatalf("no sprint called %q is on the list; it holds %v", name, d.names())
}

func (d *driver) names() []string {
	out := make([]string, 0, len(d.m.sprints))
	for i := range d.m.sprints {
		out = append(out, d.m.sprints[i].Name)
	}
	return out
}

// setField replaces what is in one of the form's fields, the way somebody who
// selected the lot and typed over it would.
func (d *driver) setField(at field, text string) {
	d.t.Helper()
	if d.m.state != filling {
		d.t.Fatal("the form is not open")
	}
	d.m.form.at = at
	d.m.form.focus()
	d.m.form.inputs[at].SetValue("")
	d.typeText(text)
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
	case "ctrl+s":
		return tea.KeyPressMsg{Code: 's', Mod: tea.ModCtrl}
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
		t.Fatalf("%v — run: go test ./internal/ui/sprint -update", err)
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
