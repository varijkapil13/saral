package search

import (
	"flag"
	"fmt"
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

	"github.com/varijkapil13/saral/internal/app"
	"github.com/varijkapil13/saral/internal/testsupport"
	"github.com/varijkapil13/saral/internal/ui/kernel"
	"github.com/varijkapil13/saral/pkg/adf"
	"github.com/varijkapil13/saral/pkg/jira"
	"github.com/varijkapil13/saral/pkg/jira/jiratest"
)

func TestMain(m *testing.M) { os.Exit(testsupport.IsolateDirs(m)) }

var update = flag.Bool("update", false, "rewrite the golden files")

var testNow = time.Date(2025, time.March, 5, 9, 0, 0, 0, time.UTC)

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
		Now:     func() time.Time { return testNow },
	}
}

func colourDeps(client jira.Client) kernel.Deps {
	d := testDeps(client)
	d.Theme = kernel.NewTheme(kernel.ThemeDark, true, kernel.UnicodeGlyphs())
	return d
}

func doc(text string) adf.Doc { return adf.NewDoc(adf.NewNode("paragraph", adf.NewText(text))) }

func issueOf(key, summary string, hoursAgo int) jira.Issue {
	project, num, _ := strings.Cut(key, "-")
	return jira.Issue{
		ID: num + map[string]string{"PROJ": "1", "OTHER": "2"}[project], Key: key,
		Project: jira.ProjectRef{Key: project},
		Summary: summary,
		Type:    jira.IssueType{ID: "10001", Name: "Task"},
		Status:  jira.Status{ID: "10201", Name: "In Progress", Category: jira.CategoryInProgress},
		Updated: testNow.Add(-time.Duration(hoursAgo) * time.Hour),
		Created: testNow.Add(-time.Duration(hoursAgo+100) * time.Hour),
	}
}

func baseIssues() []jira.Issue {
	pointer := issueOf("PROJ-2", "Report export", 5)
	pointer.Description = doc("A null pointer appears when the login page loads")
	return []jira.Issue{
		issueOf("PROJ-1", "Login timeout after a minute", 2),
		pointer,
		issueOf("PROJ-3", "İstanbul office login", 30),
		issueOf("PROJ-4", "Straße address login 🚀", 50),
		issueOf("PROJ-5", "Unrelated chores", 1),
		issueOf("OTHER-7", "Login redirect loop", 3),
	}
}

func bulkIssues(n int) []jira.Issue {
	out := make([]jira.Issue, 0, n)
	for i := range n {
		out = append(out, issueOf(fmt.Sprintf("PROJ-%d", 1000+i), fmt.Sprintf("Paging widget %03d", i), i+10))
	}
	return out
}

func newFake(issues []jira.Issue, opts ...jiratest.Option) *jiratest.Fake {
	return jiratest.New(append([]jiratest.Option{
		jiratest.WithProject("PROJ", jiratest.Scrum),
		jiratest.WithIssues(issues),
	}, opts...)...)
}

// ticks stands in for the timer keystrokes settle on, so that a test decides
// when a pause has happened.
type ticks struct {
	pending []func() tea.Msg
	waits   []time.Duration
}

func (t *ticks) after(d time.Duration, fn func() tea.Msg) tea.Cmd {
	t.pending = append(t.pending, fn)
	t.waits = append(t.waits, d)
	return nil
}

func immediately(_ time.Duration, fn func() tea.Msg) tea.Cmd {
	return func() tea.Msg { return fn() }
}

type driver struct {
	t        *testing.T
	m        *Model
	statuses []kernel.StatusMsg
	pushes   []kernel.PushMsg
	opens    []kernel.OpenMsg
	pops     int
	clock    *ticks
}

func newDriver(t *testing.T, d kernel.Deps, seed Seed, w, h int, opts ...Option) *driver {
	t.Helper()
	m := New(d, seed, opts...)
	dr := &driver{t: t, m: m}
	dr.send(kernel.SizeMsg{Width: w, Height: h})
	dr.send(kernel.FocusMsg{Focused: true})
	dr.run(m.Init())
	return dr
}

func settled(t *testing.T, client jira.Client, seed Seed, w, h int) *driver {
	t.Helper()
	return newDriver(t, testDeps(client), seed, w, h, withAfter(immediately))
}

func slow(t *testing.T, client jira.Client, w, h int) *driver {
	t.Helper()
	clock := &ticks{}
	dr := newDriver(t, testDeps(client), Seed{}, w, h, withAfter(clock.after))
	dr.clock = clock
	return dr
}

func (d *driver) send(msg tea.Msg) {
	d.t.Helper()
	view, cmd := d.m.Update(msg)
	d.m, _ = view.(*Model)
	d.run(cmd)
}

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
		if reply, addressed := msg.(kernel.ReplyMsg); addressed {
			msg = reply.Msg
		}
		switch msg := msg.(type) {
		case kernel.StatusMsg:
			d.statuses = append(d.statuses, msg)
		case kernel.PushMsg:
			d.pushes = append(d.pushes, msg)
		case kernel.OpenMsg:
			d.opens = append(d.opens, msg)
		case kernel.PopMsg:
			d.pops++
		default:
			view, follow := d.m.Update(msg)
			d.m, _ = view.(*Model)
			queue = append(queue, follow)
		}
	}
}

func (d *driver) fire() {
	d.t.Helper()
	pending := d.clock.pending
	d.clock.pending, d.clock.waits = nil, nil
	for _, fn := range pending {
		d.send(fn())
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

func (d *driver) keys() []string {
	out := make([]string, len(d.m.rows))
	for i, r := range d.m.rows {
		out[i] = r.iss.Key
	}
	return out
}

func (d *driver) lastStatus() kernel.StatusMsg {
	if len(d.statuses) == 0 {
		return kernel.StatusMsg{}
	}
	return d.statuses[len(d.statuses)-1]
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
	case "ctrl+n":
		return tea.KeyPressMsg{Code: 'n', Mod: tea.ModCtrl}
	case "ctrl+d":
		return tea.KeyPressMsg{Code: 'd', Mod: tea.ModCtrl}
	case "ctrl+u":
		return tea.KeyPressMsg{Code: 'u', Mod: tea.ModCtrl}
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
		t.Fatalf("%v — run: go test ./internal/ui/search -update", err)
	}
	if string(want) != got {
		t.Errorf("frame differs from %s\n--- want ---\n%s\n--- got ---\n%s", path, want, got)
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

func mustContain(t *testing.T, got string, want ...string) {
	t.Helper()
	for _, w := range want {
		if !strings.Contains(got, w) {
			t.Errorf("output does not contain %q:\n%s", w, got)
		}
	}
}

func ansiStrip(s string) string { return ansi.Strip(s) }

func appResult(issues ...jira.Issue) app.Result {
	return app.Result{Page: jira.NewPage(issues, nil)}
}
