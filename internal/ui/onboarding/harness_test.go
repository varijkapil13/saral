package onboarding

import (
	"context"
	"errors"
	"flag"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"charm.land/bubbles/v2/spinner"
	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/varijkapil13/saral/internal/config"
	"github.com/varijkapil13/saral/internal/ui/kernel"
	"github.com/varijkapil13/saral/pkg/jira"
	"github.com/varijkapil13/saral/pkg/jira/jiratest"
)

var update = flag.Bool("update", false, "rewrite the golden files")

const (
	testSite  = "example.atlassian.net"
	testEmail = "you@example.com"
	// testToken is what must never appear in a frame or on disk.
	testToken = "9d8f7a6b5c4d3e2f1a0b"
)

func testTheme() *kernel.Theme {
	return kernel.NewTheme(kernel.ThemeNoColor, true, kernel.ASCIIGlyphs())
}

func testDeps() kernel.Deps {
	return kernel.Deps{
		Theme: testTheme(),
		Site:  testSite,
		Now:   func() time.Time { return time.Date(2026, 8, 20, 9, 0, 0, 0, time.UTC) },
	}
}

func testFake(opts ...jiratest.Option) *jiratest.Fake {
	base := make([]jiratest.Option, 0, 3+len(opts))
	base = append(base,
		jiratest.WithProject("PROJ", jiratest.Scrum),
		jiratest.WithIssues(jiratest.Gen(6)),
		jiratest.WithMe(jira.User{AccountID: "acct-me", DisplayName: "Sam Tester", Active: true, TimeZone: time.UTC}),
	)
	return jiratest.New(append(base, opts...)...)
}

// driver runs the view the way the kernel would: it delivers a message, keeps
// the frame that came out of it, and runs whatever command came back.
type driver struct {
	t      *testing.T
	view   kernel.View
	frames []string
	quit   bool
	dir    string
	path   string
}

func newDriver(t *testing.T, client jira.Client, opts ...func(*kernel.Deps)) *driver {
	t.Helper()
	return newDriverWith(t, connectorFor(client), opts...)
}

func connectorFor(client jira.Client) Connector {
	return func(string, string, string) (jira.Client, error) { return client, nil }
}

func newDriverWith(t *testing.T, connect Connector, opts ...func(*kernel.Deps)) *driver {
	t.Helper()
	deps := testDeps()
	for _, o := range opts {
		o(&deps)
	}
	d := &driver{t: t, dir: t.TempDir()}
	d.path = filepath.Join(d.dir, "config.toml")
	d.view = NewWith(deps, connect)
	d.send(kernel.SizeMsg{Width: 100, Height: 30})
	// The real Init reads the config file from its XDG location; the tests point
	// it at a temporary one so that they can run in parallel and read it back.
	d.send(configLoadedMsg{cfg: config.Config{Mouse: true}, path: d.path})
	return d
}

func (d *driver) send(msg tea.Msg) {
	d.t.Helper()
	view, cmd := d.view.Update(msg)
	d.view = view
	d.frames = append(d.frames, view.View())
	d.run(cmd)
}

// run executes a command tree the way Bubble Tea would, minus the two commands
// that are timers: a spinner tick and a cursor blink would each turn a test
// into a wait.
func (d *driver) run(cmd tea.Cmd) {
	d.t.Helper()
	if cmd == nil {
		return
	}
	switch msg := cmd().(type) {
	case nil:
	case tea.BatchMsg:
		for _, c := range msg {
			d.run(c)
		}
	case spinner.TickMsg:
	case tea.QuitMsg:
		d.quit = true
	default:
		d.send(msg)
	}
}

func (d *driver) typeIn(s string) {
	d.t.Helper()
	for _, r := range s {
		d.send(tea.KeyPressMsg{Code: r, Text: string(r)})
	}
}

func (d *driver) press(keys ...string) {
	d.t.Helper()
	for _, k := range keys {
		d.send(keyPress(k))
	}
}

func keyPress(s string) tea.KeyPressMsg {
	switch s {
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
	case "ctrl+r":
		return tea.KeyPressMsg{Code: 'r', Mod: tea.ModCtrl}
	case "backspace":
		return tea.KeyPressMsg{Code: tea.KeyBackspace}
	default:
		r, _ := utf8.DecodeRuneInString(s)
		return tea.KeyPressMsg{Code: r, Text: s}
	}
}

// clearField empties whatever the current step is asking for, which is what a
// user does to a field that came with a default in it.
func (d *driver) clearField() {
	d.t.Helper()
	for range len(d.model().input[d.model().step.field()].Value()) {
		d.press("backspace")
	}
}

// refusesToSearch is the fake with one method broken, because FailNext queues
// an error for the next call whichever it is, and the picker's search is never
// the next call.
type refusesToSearch struct {
	*jiratest.Fake
	err error
}

func (r refusesToSearch) Search(context.Context, jira.Query) (jira.Page[jira.Issue], error) {
	return jira.Page[jira.Issue]{}, r.err
}

func (d *driver) model() Model {
	d.t.Helper()
	m, ok := d.view.(Model)
	if !ok {
		d.t.Fatalf("the view is a %T, not a Model", d.view)
	}
	return m
}

func (d *driver) frame() string { return ansi.Strip(d.view.View()) }

// forget drops the frames drawn so far, which is what a test that has just
// resized wants before it starts looking at every frame.
func (d *driver) forget() { d.frames = nil }

// credentials walks the three fields every path starts with.
func (d *driver) credentials() {
	d.t.Helper()
	d.typeIn(testSite)
	d.press("enter")
	d.typeIn(testEmail)
	d.press("enter")
	d.typeIn(testToken)
	d.press("enter")
}

func (d *driver) atStep(want step) {
	d.t.Helper()
	if got := d.model().step; got != want {
		d.t.Fatalf("on step %v, want %v — last frame:\n%s", got, want, d.frame())
	}
}

func (d *driver) mustContain(want string) {
	d.t.Helper()
	if !strings.Contains(d.frame(), want) {
		d.t.Errorf("the frame does not mention %q:\n%s", want, d.frame())
	}
}

// noTokenAnywhere is the assertion that matters most here: the token may not be
// in any frame the flow drew, nor in anything it wrote.
func (d *driver) noTokenAnywhere() {
	d.t.Helper()
	for i, frame := range d.frames {
		if strings.Contains(ansi.Strip(frame), testToken) {
			d.t.Fatalf("frame %d shows the token:\n%s", i, ansi.Strip(frame))
		}
	}
	entries, err := os.ReadDir(d.dir)
	if err != nil {
		d.t.Fatalf("reading %s: %v", d.dir, err)
	}
	for _, entry := range entries {
		body, err := os.ReadFile(filepath.Join(d.dir, entry.Name()))
		if err != nil {
			d.t.Fatalf("reading %s: %v", entry.Name(), err)
		}
		if strings.Contains(string(body), testToken) {
			d.t.Fatalf("%s holds the token:\n%s", entry.Name(), body)
		}
	}
}

func (d *driver) written() string {
	d.t.Helper()
	body, err := os.ReadFile(d.path)
	if err != nil {
		d.t.Fatalf("reading the config back: %v", err)
	}
	return string(body)
}

func (d *driver) nothingWritten() {
	d.t.Helper()
	if _, err := os.Stat(d.path); !errors.Is(err, os.ErrNotExist) {
		d.t.Errorf("a config file was written at %s (%v)", d.path, err)
	}
}

func golden(t *testing.T, name, got string) {
	t.Helper()
	path := filepath.Join("testdata", name)
	if *update {
		if err := os.MkdirAll("testdata", 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(got), 0o644); err != nil {
			t.Fatal(err)
		}
		return
	}
	want, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("%v — run: go test ./internal/ui/onboarding -update", err)
	}
	if string(want) != got {
		t.Errorf("frame differs from %s\n--- want ---\n%s\n--- got ---\n%s", path, want, got)
	}
}
