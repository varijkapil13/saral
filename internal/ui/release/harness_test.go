package release

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

// The versions every fake project is seeded with, by id. They are the fake's
// own, so a test names them rather than inventing an id the store does not hold.
const (
	oneOh   = "ver-PROJ-0"
	twoOh   = "ver-PROJ-1"
	threeOh = "ver-PROJ-2"
)

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

// plainDeps draws with a theme that writes no escape sequence of its own, so
// that an escape left in a frame can only be a zone marker.
func plainDeps(client jira.SessionClient) kernel.Deps {
	d := testDeps(client)
	th := kernel.NewTheme(kernel.ThemeNoColor, true, kernel.ASCIIGlyphs())
	plain := lipgloss.NewStyle()
	for _, style := range []*lipgloss.Style{
		&th.Base, &th.Muted, &th.Accent, &th.Danger, &th.Warning, &th.Success, &th.Title,
		&th.Header, &th.Selected, &th.Badge, &th.StaleBadge,
	} {
		*style = plain
	}
	d.Theme = th
	return d
}

func newFake(issues int, opts ...jiratest.Option) *jiratest.Fake {
	return jiratest.New(append([]jiratest.Option{
		jiratest.WithProject("PROJ", jiratest.Scrum),
		jiratest.WithIssues(jiratest.Gen(issues)),
	}, opts...)...)
}

// openOn is an issue set where exactly n issues carry a version and none of
// them is done, so that the fake's unresolved count is a number this test chose.
func openOn(id string, n int) []jira.Issue {
	issues := jiratest.Gen(24)
	put := 0
	for i := range issues {
		issues[i].FixVersions = nil
		if put == n || issues[i].Status.Category == jira.CategoryDone {
			continue
		}
		issues[i].FixVersions = []jira.Version{{ID: id}}
		put++
	}
	return issues
}

// watcher records every version write that reaches the port, which is the only
// way to hold the view to sending a whole version rather than the one field it
// meant to change, and to hold the flow to the policy the confirm showed.
type watcher struct {
	*jiratest.Fake
	mu       sync.Mutex
	saves    []jira.VersionInput
	releases []releaseCall
}

type releaseCall struct {
	id string
	in jira.ReleaseInput
}

func watching(f *jiratest.Fake) *watcher { return &watcher{Fake: f} }

func (w *watcher) SaveVersion(ctx context.Context, in jira.VersionInput) (jira.Version, error) {
	w.mu.Lock()
	w.saves = append(w.saves, in)
	w.mu.Unlock()
	return w.Fake.SaveVersion(ctx, in)
}

func (w *watcher) ReleaseVersion(ctx context.Context, id string, in jira.ReleaseInput) (jira.Version, error) {
	w.mu.Lock()
	w.releases = append(w.releases, releaseCall{id: id, in: in})
	w.mu.Unlock()
	return w.Fake.ReleaseVersion(ctx, id, in)
}

func (w *watcher) saved() []jira.VersionInput {
	w.mu.Lock()
	defer w.mu.Unlock()
	return append([]jira.VersionInput(nil), w.saves...)
}

func (w *watcher) released() []releaseCall {
	w.mu.Lock()
	defer w.mu.Unlock()
	return append([]releaseCall(nil), w.releases...)
}

// driver runs a view the way the kernel would, but keeps the messages it sends
// upward instead of acting on them, so a test can assert what it asked for.
type driver struct {
	t          *testing.T
	m          kernel.View
	statuses   []kernel.StatusMsg
	pops       int
	pushes     []kernel.PushMsg
	broadcasts []tea.Msg
}

func newDriver(t *testing.T, view kernel.View, w, h int) *driver {
	t.Helper()
	dr := &driver{t: t, m: view}
	dr.send(kernel.SizeMsg{Width: w, Height: h})
	dr.send(kernel.FocusMsg{Focused: true})
	dr.run(dr.m.Init())
	return dr
}

func listOf(t *testing.T, d kernel.Deps, w, h int) *driver {
	t.Helper()
	return newDriver(t, New(d), w, h)
}

func flowOf(t *testing.T, d kernel.Deps, v jira.Version, open int, targets []jira.Version, w, h int) *driver {
	t.Helper()
	return newDriver(t, NewFlow(d, v, open, targets), w, h)
}

func (d *driver) list() *Model {
	d.t.Helper()
	m, ok := d.m.(*Model)
	if !ok {
		d.t.Fatalf("the view under test is a %T, not the versions list", d.m)
	}
	return m
}

func (d *driver) flow() *Flow {
	d.t.Helper()
	f, ok := d.m.(*Flow)
	if !ok {
		d.t.Fatalf("the view under test is a %T, not the release flow", d.m)
	}
	return f
}

func (d *driver) send(msg tea.Msg) {
	d.t.Helper()
	view, cmd := d.m.Update(msg)
	d.m = view
	d.run(cmd)
}

// run executes commands to exhaustion. Nothing in this package returns a command
// that waits on a clock, so it terminates.
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
		case kernel.PushMsg:
			d.pushes = append(d.pushes, msg)
		case kernel.BroadcastMsg:
			d.broadcasts = append(d.broadcasts, msg.Msg)
		default:
			view, follow := d.m.Update(msg)
			d.m = view
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

// pushed is the last view this one asked the kernel to put over it.
func (d *driver) pushed() (kernel.PushMsg, bool) {
	if len(d.pushes) == 0 {
		return kernel.PushMsg{}, false
	}
	return d.pushes[len(d.pushes)-1], true
}

// released is the version the flow shipped, as it told the rest of the program.
func (d *driver) released() (releasedMsg, bool) {
	for i := len(d.broadcasts) - 1; i >= 0; i-- {
		if msg, ok := d.broadcasts[i].(releasedMsg); ok {
			return msg, true
		}
	}
	return releasedMsg{}, false
}

// moveTo walks the cursor onto a version by id, the way somebody would.
func (d *driver) moveTo(id string) {
	d.t.Helper()
	m := d.list()
	d.key("home")
	for i := range m.versions {
		if m.versions[i].ID == id {
			for range i {
				d.key("j")
			}
			return
		}
	}
	d.t.Fatalf("no version %q is on the list", id)
}

// pressOn scans the frame the view would draw and presses the left button in the
// first cell of one of its zones. The manager records a zone on its own
// goroutine, so the zone is waited for rather than assumed.
func pressOn(t *testing.T, d kernel.Deps, dr *driver, name string) {
	t.Helper()

	_ = d.Zones.Scan(dr.m.View())
	id := zoner(dr).ID(name)
	eventually(t, func() bool { return !d.Zones.Get(id).IsZero() })
	at := d.Zones.Get(id)
	dr.send(tea.MouseClickMsg{X: at.StartX, Y: at.StartY, Button: tea.MouseLeft})
}

func zoner(dr *driver) interface{ ID(string) string } {
	switch v := dr.m.(type) {
	case *Model:
		return v.zones
	case *Flow:
		return v.zones
	default:
		dr.t.Fatalf("a %T marks no zones", dr.m)
		return nil
	}
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
		t.Fatalf("%v — run: go test ./internal/ui/release -update", err)
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
