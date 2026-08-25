package palette

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
	"github.com/varijkapil13/saral/pkg/jira"
)

var update = flag.Bool("update", false, "rewrite the golden files")

// clockAt is the instant every test starts from, so that a decay measured in
// days is measured against something written down rather than against now.
var clockAt = time.Date(2026, time.August, 20, 9, 0, 0, 0, time.UTC)

// TestMain points the frecency table at a directory of this run's own. Nothing
// here may reach the cache directory of whoever is running the suite.
func TestMain(m *testing.M) {
	dir, err := os.MkdirTemp("", "saral-palette-cache")
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

// noBulkMove is a site whose token cannot move issues between projects, with
// the refusal in the words a probe would have used.
const noBulkMove = "you need the Bulk Change permission to move issues between projects"

func capsWithoutBulkMove() jira.Capabilities {
	ok := jira.Capability{OK: true}
	return jira.Capabilities{
		Plans: ok, Boards: ok, Attachments: ok, DeleteIssues: ok,
		BulkMove: jira.Capability{Reason: noBulkMove},
		TimeZone: time.UTC,
	}
}

func fullCaps() jira.Capabilities {
	caps := capsWithoutBulkMove()
	caps.BulkMove = jira.Capability{OK: true}
	return caps
}

func paletteDeps() kernel.Deps {
	return kernel.Deps{
		Caps:    capsWithoutBulkMove(),
		Project: "PROJ",
		Theme:   kernel.NewTheme(kernel.ThemeNoColor, true, kernel.ASCIIGlyphs()),
		Zones:   zone.New(),
		Site:    "example.atlassian.net",
		Now:     func() time.Time { return clockAt },
	}
}

// sample is the command list the palette is tested against, in the order
// kernel.Commands() hands one over: group, then title, then ID. It is injected
// rather than registered so that these tests do not depend on which view
// packages happen to be linked into the test binary.
func sample() []kernel.Command {
	run := func(kernel.Deps) tea.Cmd { return nil }
	return []kernel.Command{
		{ID: "theme.dark", Title: "Use the dark theme", Group: "Appearance", Run: run},
		{ID: "comments.write", Title: "Write a comment", Group: "Comments", Keys: []string{"a"}, Run: run},
		{ID: "issues.open", Title: "Issues", Group: "Go to", Keys: []string{"g1"}, Run: run},
		{ID: "issue.edit", Title: "Edit this issue", Group: "Issue", Keys: []string{"e"}, Run: run},
		{
			ID: "issue.move", Title: "Move issues between projects", Group: "Issue",
			Requires: jira.CapBulkMove, Run: run,
		},
		{ID: "issue.create", Title: "Create an issue", Group: "Issues", Run: run},
		{ID: "issues.mine", Title: "My issues", Group: "Search", Run: run},
	}
}

// memoryTable is a frecency table with nowhere to write, which is what a first
// run, an unwritable home and a test all have in common.
func memoryTable() *table { return openTable("") }

// pilot drives the palette the way the kernel would, but keeps what the view
// asked for instead of acting on it, so a test can assert that a keypress
// produced a RunCommandMsg and not a call to Run.
type pilot struct {
	t    *testing.T
	m    *Model
	msgs []tea.Msg
}

func fly(t *testing.T, d kernel.Deps, cmds []kernel.Command, freq *table, w, h int) *pilot {
	t.Helper()
	p := &pilot{t: t, m: build(d, cmds, freq)}
	p.send(kernel.SizeMsg{Width: w, Height: h})
	p.send(kernel.FocusMsg{Focused: true})
	p.run(p.m.Init())
	return p
}

func (p *pilot) send(msg tea.Msg) {
	p.t.Helper()
	view, cmd := p.m.Update(msg)
	model, ok := view.(*Model)
	if !ok {
		p.t.Fatal("Update did not return a *Model")
	}
	p.m = model
	p.run(cmd)
}

// run records what a command produced. Nothing is fed back into the view: every
// message the palette sends is addressed to the kernel.
func (p *pilot) run(cmd tea.Cmd) {
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
		p.msgs = append(p.msgs, msg)
	}
}

func (p *pilot) press(keys ...string) {
	p.t.Helper()
	for _, k := range keys {
		p.send(stroke(k))
	}
}

func (p *pilot) typeText(s string) {
	p.t.Helper()
	for _, r := range s {
		p.send(tea.KeyPressMsg{Code: r, Text: string(r)})
	}
}

func (p *pilot) frame() string { return ansi.Strip(p.m.View()) }

// titles are the commands on offer, in the order they are drawn.
func (p *pilot) titles() []string {
	out := make([]string, 0, len(p.m.shown))
	for _, i := range p.m.shown {
		out = append(out, p.m.rows[i].cmd.Title)
	}
	return out
}

func (p *pilot) ran() []string {
	out := []string{}
	for _, msg := range p.msgs {
		if run, ok := msg.(kernel.RunCommandMsg); ok {
			out = append(out, run.ID)
		}
	}
	return out
}

func (p *pilot) statuses() []string {
	out := []string{}
	for _, msg := range p.msgs {
		if status, ok := msg.(kernel.StatusMsg); ok {
			out = append(out, status.Text)
		}
	}
	return out
}

func (p *pilot) popped() bool {
	for _, msg := range p.msgs {
		if _, ok := msg.(kernel.PopMsg); ok {
			return true
		}
	}
	return false
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

func stroke(s string) tea.KeyPressMsg {
	switch s {
	case "enter":
		return tea.KeyPressMsg{Code: tea.KeyEnter}
	case "esc":
		return tea.KeyPressMsg{Code: tea.KeyEsc}
	case "up":
		return tea.KeyPressMsg{Code: tea.KeyUp}
	case "down":
		return tea.KeyPressMsg{Code: tea.KeyDown}
	case "pgup":
		return tea.KeyPressMsg{Code: tea.KeyPgUp}
	case "pgdown":
		return tea.KeyPressMsg{Code: tea.KeyPgDown}
	case "backspace":
		return tea.KeyPressMsg{Code: tea.KeyBackspace}
	case "ctrl+n":
		return tea.KeyPressMsg{Code: 'n', Mod: tea.ModCtrl}
	case "ctrl+p":
		return tea.KeyPressMsg{Code: 'p', Mod: tea.ModCtrl}
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
		t.Fatalf("%v — run: go test ./internal/ui/palette -update", err)
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
