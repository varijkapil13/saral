package form

import (
	"flag"
	"os"
	"path/filepath"
	"reflect"
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

// The fake site's own issue type ids. They are not the ones a stock Jira
// allocates, which is the point: a form that wrote one down would find nothing.
const (
	fakeStory   = "10301"
	fakeSubtask = "10305"
)

func fullCaps() jira.Capabilities {
	ok := jira.Capability{OK: true}
	return jira.Capabilities{
		Plans: ok, BulkMove: ok, Boards: ok, Attachments: ok, DeleteIssues: ok,
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
		Now:     func() time.Time { return time.Date(2026, time.March, 5, 9, 0, 0, 0, time.UTC) },
	}
}

func newFake(issues int, opts ...jiratest.Option) *jiratest.Fake {
	return jiratest.New(append([]jiratest.Option{
		jiratest.WithProject("PROJ", jiratest.Scrum),
		jiratest.WithIssues(jiratest.Gen(issues)),
	}, opts...)...)
}

// driver runs the view the way the kernel would, but keeps the messages the
// view sends upward instead of acting on them, so a test can assert what the
// view asked for.
type driver struct {
	t        *testing.T
	m        *Model
	statuses []kernel.StatusMsg
	pops     int
	casts    []kernel.BroadcastMsg
}

func newDriver(t *testing.T, d kernel.Deps, w, h int) *driver {
	t.Helper()

	dr := &driver{t: t, m: newWith(d, newSchemaCache(schemaTTL, time.Now), newDraftStore())}
	dr.send(kernel.SizeMsg{Width: w, Height: h})
	dr.send(kernel.FocusMsg{Focused: true})
	dr.run(dr.m.Init())
	return dr
}

// openOn drives the picker to one issue type and settles on its create screen.
func openOn(t *testing.T, d kernel.Deps, w, h int, issueType string) *driver {
	t.Helper()

	dr := newDriver(t, d, w, h)
	dr.send(CreateMsg{IssueTypeID: issueType})
	if dr.m.stage != stageFields {
		t.Fatalf("the form is still picking an issue type; it noted %q", dr.m.note)
	}
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
			d.casts = append(d.casts, msg)
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

// focus puts the cursor on the field with this id, whatever the screen's order.
func (d *driver) focus(id string) {
	d.t.Helper()

	for i, at := range d.m.index {
		if at.kind == rowField && d.m.fields[at.at].id() == id {
			d.m.moveTo(i)
			return
		}
	}
	d.t.Fatalf("no field %q on this form; it has %v", id, d.ids())
}

// submitRow puts the cursor on the row that creates the issue.
func (d *driver) submitRow() {
	d.t.Helper()

	d.m.moveTo(len(d.m.index) - 1)
}

func (d *driver) field(id string) *field {
	d.t.Helper()

	for _, f := range d.m.fields {
		if f.id() == id {
			return f
		}
	}
	d.t.Fatalf("no field %q on this form; it has %v", id, d.ids())
	return nil
}

func (d *driver) ids() []string {
	out := make([]string, 0, len(d.m.fields))
	for _, f := range d.m.fields {
		out = append(out, f.id())
	}
	return out
}

func (d *driver) view() string { return ansi.Strip(d.m.View()) }

func (d *driver) lastStatus() kernel.StatusMsg {
	if len(d.statuses) == 0 {
		return kernel.StatusMsg{}
	}
	return d.statuses[len(d.statuses)-1]
}

// unwrapCmds opens a batch or a sequence. Bubble Tea exports the first as
// BatchMsg and keeps the second unexported, so both are recognised by shape.
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
	case "up":
		return tea.KeyPressMsg{Code: tea.KeyUp}
	case "down":
		return tea.KeyPressMsg{Code: tea.KeyDown}
	case "home":
		return tea.KeyPressMsg{Code: tea.KeyHome}
	case "end":
		return tea.KeyPressMsg{Code: tea.KeyEnd}
	case "backspace":
		return tea.KeyPressMsg{Code: tea.KeyBackspace}
	case "ctrl+s":
		return tea.KeyPressMsg{Code: 's', Mod: tea.ModCtrl}
	case "ctrl+d":
		return tea.KeyPressMsg{Code: 'd', Mod: tea.ModCtrl}
	case "ctrl+t":
		return tea.KeyPressMsg{Code: 't', Mod: tea.ModCtrl}
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
		t.Fatalf("%v — run: go test ./internal/ui/form -update", err)
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
