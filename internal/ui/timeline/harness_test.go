package timeline

import (
	"context"
	"flag"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"
	"unicode/utf8"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"
	zone "github.com/lrstanley/bubblezone/v2"

	"github.com/varijkapil13/saral/internal/app"
	"github.com/varijkapil13/saral/internal/ui/kernel"
	"github.com/varijkapil13/saral/pkg/jira"
	"github.com/varijkapil13/saral/pkg/jira/jiratest"
)

var update = flag.Bool("update", false, "rewrite the golden files")

// TestMain points the config lookup at a directory of this run's own. New reads
// the profile's timeline field names, and no test in this package may reach
// whatever the person running it has configured.
func TestMain(m *testing.M) {
	dir, err := os.MkdirTemp("", "saral-timeline-config")
	if err != nil {
		panic(err)
	}
	if err := os.Setenv("SARAL_CONFIG_DIR", dir); err != nil {
		panic(err)
	}
	code := m.Run()
	_ = os.RemoveAll(dir)
	os.Exit(code)
}

// theDay is the clock every test in this package runs on. It sits between the
// fake's seeded sprints and its second version's release date, so that the today
// line, a sprint boundary and a version marker are all on screen at once.
var theDay = time.Date(2026, time.March, 5, 9, 0, 0, 0, time.UTC)

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
		Now:     func() time.Time { return theDay },
	}
}

// plainDeps draws with a theme that writes no escape sequence of its own, so
// that an escape left in a frame can only be a zone marker. The no-colour theme
// is not enough: NO_COLOR asks for colour to go away, not for bold and faint to.
func plainDeps(client jira.SessionClient) kernel.Deps {
	d := testDeps(client)
	th := kernel.NewTheme(kernel.ThemeNoColor, true, kernel.ASCIIGlyphs())
	plain := lipgloss.NewStyle()
	for _, style := range []*lipgloss.Style{
		&th.Base, &th.Muted, &th.Accent, &th.Danger, &th.Warning, &th.Success, &th.Title,
		&th.Selected, &th.Badge, &th.StaleBadge,
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

// driver runs the chart the way the kernel would, but keeps the messages it
// sends upward instead of acting on them, so a test can assert what it asked
// for.
type driver struct {
	t          *testing.T
	m          *Model
	statuses   []kernel.StatusMsg
	pushes     []kernel.PushMsg
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
		if reply, addressed := msg.(kernel.ReplyMsg); addressed {
			msg = reply.Msg
		}
		switch msg := msg.(type) {
		case kernel.StatusMsg:
			d.statuses = append(d.statuses, msg)
		case kernel.PushMsg:
			d.pushes = append(d.pushes, msg)
		case kernel.BroadcastMsg:
			d.broadcasts = append(d.broadcasts, msg.Msg)
		case kernel.OpenMsg:
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

func (d *driver) view() string { return ansi.Strip(d.m.View()) }

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
		t.Fatalf("%v — run: go test ./internal/ui/timeline -update", err)
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

// customFake is a site holding exactly the issues a test built, so that a shape
// Gen never produces — an issue with no date at all, a parent with dated
// children — can be put in front of the cascade.
func customFake(issues []jira.Issue, opts ...jiratest.Option) *jiratest.Fake {
	return jiratest.New(append([]jiratest.Option{
		jiratest.WithProject("PROJ", jiratest.Scrum),
		jiratest.WithIssues(issues),
	}, opts...)...)
}

func issueIn(key, summary string) jira.Issue {
	return jira.Issue{
		ID:      "9" + key,
		Key:     key,
		Summary: summary,
		Project: jira.ProjectRef{ID: "10000", Key: "PROJ", Name: "PROJ Project"},
		Type:    jira.IssueType{ID: "10301", Name: "Story"},
		Status:  jira.Status{ID: "10201", Name: "Triage", Category: jira.CategoryToDo},
	}
}

// fieldRef resolves a field by name against the fake's own catalogue, which is
// the only way to name a custom field: its id differs on every site.
func fieldRef(t *testing.T, f *jiratest.Fake, name string) jira.FieldRef {
	t.Helper()
	catalogue, err := f.Fields(context.Background())
	if err != nil {
		t.Fatalf("reading the field catalogue: %v", err)
	}
	field, ok := jira.FieldByName(catalogue, name)
	if !ok {
		t.Fatalf("the fake has no field called %q", name)
	}
	return field.Ref()
}

func withDate(iss jira.Issue, ref jira.FieldRef, on jira.Date) jira.Issue {
	iss.Fields = iss.Fields.With(ref, jira.FieldValue{Kind: jira.KindDate, Date: on})
	return iss
}

func day(y int, m time.Month, d int) jira.Date { return jira.Date{Year: y, Month: m, Day: d} }

// spy records the searches a chart issues, so a test can hold the field set it
// asked for against the field set it needs.
type spy struct {
	*jiratest.Fake
	mu      sync.Mutex
	queries []jira.Query
}

func newSpy(f *jiratest.Fake) *spy { return &spy{Fake: f} }

func (s *spy) Search(ctx context.Context, q jira.Query) (jira.Page[jira.Issue], error) {
	s.mu.Lock()
	s.queries = append(s.queries, q)
	s.mu.Unlock()
	return s.Fake.Search(ctx, q)
}

func (s *spy) lastQuery(t *testing.T) jira.Query {
	t.Helper()
	s.mu.Lock()
	defer s.mu.Unlock()
	if len(s.queries) == 0 {
		t.Fatal("no search was issued")
	}
	return s.queries[len(s.queries)-1]
}

// memCache is an app.Cache in memory, so a test can put rows on disk without a
// disk.
type memCache struct {
	rows  map[string][]jira.Issue
	stamp time.Time
	gen   uint64
	fail  error
}

func newMemCache() *memCache {
	return &memCache{rows: map[string][]jira.Issue{}, stamp: theDay.Add(-time.Hour)}
}

func (c *memCache) Rows(jql string) (app.Snapshot, bool) {
	issues, ok := c.rows[jql]
	if !ok {
		return app.Snapshot{}, false
	}
	return app.Snapshot{Issues: issues, StoredAt: c.stamp}, true
}

func (c *memCache) PutRows(jql string, issues []jira.Issue, _ bool) error {
	if c.fail != nil {
		return c.fail
	}
	c.rows[jql] = slices.Clone(issues)
	c.gen++
	return nil
}

func (c *memCache) Forget(jql string) error {
	delete(c.rows, jql)
	c.gen++
	return nil
}

func (c *memCache) EachIssue(fn func(jira.Issue, time.Time) bool) error {
	for _, issues := range c.rows {
		for _, iss := range issues {
			if !fn(iss, c.stamp) {
				return nil
			}
		}
	}
	return nil
}

func (c *memCache) Generation() uint64 { return c.gen }
