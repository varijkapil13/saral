package attach

import (
	"context"
	"flag"
	"io"
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

// TestMain points the download directory at one of this run's own. Every test in
// this package builds the pane through New, which locates that directory, and
// none of them may reach the machine's cache directory to do it.
func TestMain(m *testing.M) {
	dir, err := os.MkdirTemp("", "saral-attach-cache")
	if err != nil {
		panic(err)
	}
	if err := os.Setenv("SARAL_CACHE_DIR", dir); err != nil {
		panic(err)
	}
	code := m.Run()
	_ = os.RemoveAll(dir)
	os.Exit(code)
}

func fullCaps() jira.Capabilities {
	ok := jira.Capability{OK: true}
	return jira.Capabilities{
		Plans: ok, BulkMove: ok, Boards: ok, Attachments: ok, DeleteIssues: ok, People: ok,
		Graphics: jira.GraphicsNone, TimeZone: time.UTC,
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

func newFake(issues int, opts ...jiratest.Option) *jiratest.Fake {
	return jiratest.New(append([]jiratest.Option{
		jiratest.WithProject("PROJ", jiratest.Scrum),
		jiratest.WithIssues(jiratest.Gen(issues)),
	}, opts...)...)
}

// file is one attachment to seed.
type file struct {
	name string
	body string
}

// attached puts files on an issue. Uploading them is the only way the fake grows
// an attachment, so a pane that lists three files is one an upload seeded.
func attached(t *testing.T, f *jiratest.Fake, key string, files ...file) []jira.Attachment {
	t.Helper()
	refs := make([]jira.FileRef, 0, len(files))
	for _, one := range files {
		body := one.body
		refs = append(refs, jira.FileRef{
			Name: one.name,
			Size: int64(len(body)),
			Open: func() (io.ReadCloser, error) { return io.NopCloser(strings.NewReader(body)), nil },
		})
	}
	added, err := f.Upload(t.Context(), key, refs)
	if err != nil {
		t.Fatalf("seeding the attachments: %v", err)
	}
	return added
}

// sampleFiles are the three shapes the pane has to draw: an image, a document
// the desktop opens, and a file the site could not name a media type for.
func sampleFiles() []file {
	return []file{
		{name: "screenshot.png", body: strings.Repeat("p", 2048)},
		{name: "handover.pdf", body: strings.Repeat("d", 40960)},
		{name: "capture", body: "raw"},
	}
}

// runs records what the pane asked the machine to run, so that a test can hold it
// to handing chafa a path of this program's own and never a URL.
type runs struct {
	mu     sync.Mutex
	calls  [][]string
	opened []string
	out    []byte
	err    error
}

func (r *runs) run(_ context.Context, name string, args ...string) ([]byte, error) {
	r.mu.Lock()
	r.calls = append(r.calls, append([]string{name}, args...))
	r.mu.Unlock()
	return r.out, r.err
}

// open stands in for the desktop hand-off, so that no test starts a program on
// whoever is running it.
func (r *runs) openFile(path string) tea.Cmd {
	r.mu.Lock()
	r.opened = append(r.opened, path)
	r.mu.Unlock()
	return kernel.Status("opened " + path)
}

func (r *runs) handedOver() []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]string(nil), r.opened...)
}

func (r *runs) argv() [][]string {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([][]string(nil), r.calls...)
}

// testTools puts the downloads in a directory of this test's own and replaces the
// renderer with one that starts no process. Without it a machine with chafa
// installed would run it and one without would not, so the same test would
// measure two different programs.
func testTools(t *testing.T) (tools, *runs) {
	t.Helper()
	seen := &runs{out: []byte("##\n##\n")}
	return tools{dir: filepath.Join(t.TempDir(), "attachments"), run: seen.run, open: seen.openFile}, seen
}

// driver runs the pane the way the kernel would, but keeps the messages it sends
// upward instead of acting on them, so a test can assert what it asked for.
type driver struct {
	t          *testing.T
	m          *Model
	seen       *runs
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
	dr.m.tools, dr.seen = testTools(t)
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

// run executes commands to exhaustion. The only command here that waits on
// anything is the one taking a download's running total, and it takes it from a
// buffered channel the download has already closed by the time this runs it, so
// it terminates.
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

// onto moves the cursor to the file of this name, the way somebody would.
func (d *driver) onto(name string) {
	d.t.Helper()
	d.send(keyPress("home"))
	for i := range d.m.files {
		if d.m.files[i].Filename == name {
			for range i {
				d.send(keyPress("j"))
			}
			return
		}
	}
	d.t.Fatalf("no file called %q is on the list", name)
}

func (d *driver) view() string { return ansi.Strip(d.m.View()) }

func (d *driver) lastStatus() kernel.StatusMsg {
	if len(d.statuses) == 0 {
		return kernel.StatusMsg{}
	}
	return d.statuses[len(d.statuses)-1]
}

func (d *driver) names() []string {
	out := make([]string, 0, len(d.m.files))
	for i := range d.m.files {
		out = append(out, d.m.files[i].Filename)
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
	case "ctrl+s":
		return tea.KeyPressMsg{Code: 's', Mod: tea.ModCtrl}
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
		t.Fatalf("%v — run: go test ./internal/ui/attach -update", err)
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

// pngBytes is a file that really is a PNG, which the fake cannot produce: its
// download derives the bytes from the attachment id. Both inline protocols sniff
// the bytes before claiming a format, so this is what a real image looks like to
// them.
func pngBytes() []byte {
	head := []byte{0x89, 'P', 'N', 'G', '\r', '\n', 0x1a, '\n'}
	return append(head, []byte("IHDR-and-then-some-pixels")...)
}

// lipglossWidth is what a terminal would count of a line. A graphics escape is a
// private sequence of no display width, and this is what says so.
func lipglossWidth(s string) int { return ansi.StringWidth(s) }
