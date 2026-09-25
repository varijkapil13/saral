package board

import (
	"context"
	"flag"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"
	"unicode/utf8"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"
	zone "github.com/lrstanley/bubblezone/v2"

	"github.com/varijkapil13/saral/internal/testsupport"
	"github.com/varijkapil13/saral/internal/ui/kernel"
	"github.com/varijkapil13/saral/internal/ui/uitest"
	"github.com/varijkapil13/saral/pkg/jira"
	"github.com/varijkapil13/saral/pkg/jira/jiratest"
)

func TestMain(m *testing.M) { os.Exit(testsupport.IsolateDirs(m)) }

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

// newFake is a Scrum board whose running sprint holds every issue, which is
// what a board that shows its active sprint and nothing else needs to draw
// them all.
func newFake(issues int, opts ...jiratest.Option) *jiratest.Fake {
	gen := jiratest.Gen(issues)
	f := jiratest.New(append([]jiratest.Option{
		jiratest.WithProject("PROJ", jiratest.Scrum),
		jiratest.WithIssues(gen),
	}, opts...)...)
	scheduleAll(f, "PROJ", keysOf(gen))
	return f
}

// scheduleAll puts issues into a project's running sprint, straight through
// the fake, and records how many calls that took so none of them is counted
// as the view's.
func scheduleAll(f *jiratest.Fake, project string, keys []string) {
	ctx := context.Background()
	defer func() { setupCalls.Store(f, len(f.Calls())) }()
	boards, err := f.Boards(ctx, project)
	if err != nil || len(boards) == 0 {
		return
	}
	page, err := f.Sprints(ctx, boards[0].ID, jira.SprintActive)
	if err != nil || len(page.Items) == 0 {
		return
	}
	for len(keys) > 0 {
		n := min(len(keys), 50)
		_ = f.MoveToSprint(ctx, page.Items[0].ID, keys[:n])
		keys = keys[n:]
	}
}

func keysOf(issues []jira.Issue) []string {
	keys := make([]string, 0, len(issues))
	for i := range issues {
		keys = append(keys, issues[i].Key)
	}
	return keys
}

// setupCalls is how many calls a test's own setup made of a fake, which is
// not the view's to answer for.
var setupCalls sync.Map

// viewCalls is the calls a fake answered after its setup was done.
func viewCalls(f *jiratest.Fake) []string {
	calls := f.Calls()
	if n, ok := setupCalls.Load(f); ok {
		if skip, isInt := n.(int); isInt {
			return calls[min(skip, len(calls)):]
		}
	}
	return calls
}

// driver runs the board the way the kernel would, but keeps the messages it
// sends upward instead of acting on them, so a test can assert what it asked
// for.
type driver struct {
	t          *testing.T
	m          *Model
	statuses   []kernel.StatusMsg
	pushes     []kernel.PushMsg
	pops       int
	proceeds   int
	broadcasts []tea.Msg
	// holdPages keeps every page after the first out of the board once it has
	// been read, the way a page still in flight is, until release hands them
	// over.
	holdPages bool
	heldPages []tea.Msg
	// park keeps back every message it says yes to, the way holdPages keeps a
	// page, so a test can look at the board while that answer is in flight.
	park   func(tea.Msg) bool
	parked []tea.Msg
}

// unpark delivers the oldest message park kept back.
func (d *driver) unpark() {
	d.t.Helper()
	if len(d.parked) == 0 {
		d.t.Fatal("nothing is parked")
	}
	msg := d.parked[0]
	d.parked = d.parked[1:]
	d.send(msg)
}

// release delivers the pages holdPages kept back, and lets every page after
// them through as it arrives.
func (d *driver) release() {
	d.t.Helper()
	d.holdPages = false
	held := d.heldPages
	d.heldPages = nil
	for _, msg := range held {
		d.send(msg)
	}
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
		if page, isPage := msg.(issuesMsg); isPage && d.holdPages && !page.first {
			d.heldPages = append(d.heldPages, msg)
			continue
		}
		if d.park != nil && d.park(msg) {
			d.parked = append(d.parked, msg)
			continue
		}
		switch msg := msg.(type) {
		case kernel.StatusMsg:
			d.statuses = append(d.statuses, msg)
		case kernel.PushMsg:
			d.pushes = append(d.pushes, msg)
		case kernel.PopMsg:
			d.pops++
		case kernel.ProceedMsg:
			d.proceeds++
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

// stocked is a board holding a configuration and cards with no site behind it:
// both arrive as the messages a read would have produced, which is how a shape
// the fake cannot be talked into is still put in front of the view.
func stocked(t *testing.T, cfg jira.BoardConfig, issues []jira.Issue, w, h int) (kernel.Deps, *driver) {
	t.Helper()
	d := testDeps(nil)
	dr := newDriver(t, d, w, h)
	dr.send(boardsMsg{gen: dr.m.gen, boards: []jira.Board{{ID: cfg.BoardID, Name: cfg.Name, Type: cfg.Type}}})
	dr.send(configMsg{gen: dr.m.gen, cfg: cfg})
	dr.send(firstPage(dr.m.gen, issues))
	return d, dr
}

func (d *driver) key(keys ...string) {
	d.t.Helper()
	for _, k := range keys {
		d.send(keyPress(k))
	}
}

// firstPage is the answer to a read as the board receives it: one page, and
// the first, so the board replaces what it holds rather than appending to it.
func firstPage(gen int, issues []jira.Issue) issuesMsg {
	return issuesMsg{gen: gen, first: true, page: jira.Page[jira.Issue]{Items: issues}}
}

func (d *driver) view() string { return ansi.Strip(d.m.View()) }

func (d *driver) lastStatus() kernel.StatusMsg {
	if len(d.statuses) == 0 {
		return kernel.StatusMsg{}
	}
	return d.statuses[len(d.statuses)-1]
}

// column is the keys in one column, top to bottom, which is what a test asserts
// a move against.
func (d *driver) column(at int) []string {
	out := make([]string, 0, d.m.columnLen(at))
	for row := range d.m.columnLen(at) {
		out = append(out, d.m.issueAt(at, row).Key)
	}
	return out
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
	case "ctrl+g":
		return tea.KeyPressMsg{Code: 'g', Mod: tea.ModCtrl}
	case "space":
		return tea.KeyPressMsg{Code: tea.KeySpace, Text: " "}
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
		t.Fatalf("%v — run: go test ./internal/ui/board -update", err)
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

func countCalls(f *jiratest.Fake, name string) int {
	n := 0
	for _, call := range viewCalls(f) {
		if call == name {
			n++
		}
	}
	return n
}

// pressOn scans the frame the board would draw and presses the left button in
// the first cell of one of its zones. The manager records a zone on its own
// goroutine, so the zone is waited for rather than assumed.
func pressOn(t *testing.T, d kernel.Deps, dr *driver, name string) {
	t.Helper()
	at := zoneOf(t, d, dr, name)
	dr.send(tea.MouseClickMsg{X: at.StartX, Y: at.StartY, Button: tea.MouseLeft})
}

func zoneOf(t *testing.T, d kernel.Deps, dr *driver, name string) zone.ZoneInfo {
	t.Helper()
	return uitest.Zone(t, d.Zones, dr.m.View, dr.m.zones.ID(name))
}
