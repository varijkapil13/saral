package comment

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

	"github.com/varijkapil13/saral/internal/ui/kernel"
	"github.com/varijkapil13/saral/pkg/adf"
	"github.com/varijkapil13/saral/pkg/jira"
	"github.com/varijkapil13/saral/pkg/jira/jiratest"
)

var update = flag.Bool("update", false, "rewrite the golden files")

// TestMain points the drafts at a directory of this run's own. Every test in
// this package writes drafts through the real store, and none of them may reach
// the machine's cache directory to do it.
func TestMain(m *testing.M) {
	dir, err := os.MkdirTemp("", "saral-comment-cache")
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	if err := os.Setenv("SARAL_CACHE_DIR", dir); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	code := m.Run()
	_ = os.RemoveAll(dir)
	os.Exit(code)
}

func fullCaps() jira.Capabilities {
	ok := jira.Capability{OK: true}
	return jira.Capabilities{
		Plans: ok, BulkMove: ok, Boards: ok, Attachments: ok, DeleteIssues: ok,
		TimeZone: time.UTC,
	}
}

// testDeps gives every test a site of its own, because the site is half of the
// key a draft is filed under and these tests run in parallel against one drafts
// directory. Two tests writing a draft for PROJ-1 must not read each other's.
func testDeps(t *testing.T, client jira.Client) kernel.Deps {
	t.Helper()

	return kernel.Deps{
		Jira:    client,
		Caps:    fullCaps(),
		Project: "PROJ",
		Theme:   kernel.NewTheme(kernel.ThemeNoColor, true, kernel.ASCIIGlyphs()),
		Zones:   zone.New(),
		Site:    t.Name() + ".example.atlassian.net",
		Now:     func() time.Time { return time.Date(2025, time.March, 5, 9, 0, 0, 0, time.UTC) },
	}
}

func newFake(issues int, opts ...jiratest.Option) *jiratest.Fake {
	return jiratest.New(append([]jiratest.Option{
		jiratest.WithProject("PROJ", jiratest.Scrum),
		jiratest.WithIssues(jiratest.Gen(issues)),
	}, opts...)...)
}

func doc(paragraphs ...string) adf.Doc {
	nodes := make([]adf.Node, 0, len(paragraphs))
	for _, p := range paragraphs {
		nodes = append(nodes, adf.NewNode("paragraph", adf.NewText(p)))
	}
	return adf.NewDoc(nodes...)
}

// comment writes a comment onto an issue the way a person would, so the thread
// under test is one the fake actually holds.
func comment(t *testing.T, f *jiratest.Fake, key string, paragraphs ...string) jira.Comment {
	t.Helper()

	stored, err := f.AddComment(t.Context(), key, doc(paragraphs...))
	if err != nil {
		t.Fatalf("AddComment: %v", err)
	}
	return stored
}

type driver struct {
	t        *testing.T
	m        *Model
	statuses []kernel.StatusMsg
}

func newDriver(t *testing.T, d kernel.Deps, key string, w, h int) *driver {
	t.Helper()

	dr := &driver{t: t, m: Thread(d, key)}
	dr.send(kernel.SizeMsg{Width: w, Height: h})
	dr.run(dr.m.Init())
	dr.send(kernel.FocusMsg{Focused: true})
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

func (d *driver) run(cmd tea.Cmd) {
	d.t.Helper()

	queue := []tea.Cmd{cmd}
	for steps := 0; len(queue) > 0; steps++ {
		if steps > 2000 {
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
		if status, ok := msg.(kernel.StatusMsg); ok {
			d.statuses = append(d.statuses, status)
			continue
		}
		view, follow := d.m.Update(msg)
		model, ok := view.(*Model)
		if !ok {
			d.t.Fatal("Update did not return a *Model")
		}
		d.m = model
		queue = append(queue, follow)
	}
}

func (d *driver) key(keys ...string) {
	d.t.Helper()

	for _, k := range keys {
		d.send(keyPress(k))
	}
}

// typeText sends one key press per rune, which is what the editor and the draft
// store actually see.
func (d *driver) typeText(s string) {
	d.t.Helper()

	for _, r := range s {
		if r == '\n' {
			d.send(tea.KeyPressMsg{Code: tea.KeyEnter})
			continue
		}
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

func (d *driver) statusText() string { return d.lastStatus().Text }

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
	case "enter":
		return tea.KeyPressMsg{Code: tea.KeyEnter}
	case "esc":
		return tea.KeyPressMsg{Code: tea.KeyEscape}
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
	case "ctrl+s":
		return tea.KeyPressMsg{Code: 's', Mod: tea.ModCtrl}
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
		t.Fatalf("%v — run: go test ./internal/ui/comment -update", err)
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
