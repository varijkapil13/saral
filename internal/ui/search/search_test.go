package search

import (
	"context"
	"errors"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"

	appsearch "github.com/varijkapil13/saral/internal/app/search"
	"github.com/varijkapil13/saral/internal/ui/issue"
	"github.com/varijkapil13/saral/internal/ui/kernel"
	"github.com/varijkapil13/saral/internal/ui/list"
	"github.com/varijkapil13/saral/internal/ui/uitest"
	"github.com/varijkapil13/saral/pkg/jira"
	"github.com/varijkapil13/saral/pkg/jira/jiratest"
)

type recorder struct {
	jira.SessionClient
	mu   sync.Mutex
	jqls []string
}

func (r *recorder) Search(ctx context.Context, q jira.Query) (jira.Page[jira.Issue], error) {
	r.mu.Lock()
	r.jqls = append(r.jqls, q.JQL)
	r.mu.Unlock()
	return r.SessionClient.Search(ctx, q)
}

func (r *recorder) sent() []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	return slices.Clone(r.jqls)
}

func recorded(f *jiratest.Fake) *recorder { return &recorder{SessionClient: f} }

func withKeyedIssues() []jira.Issue {
	return append(baseIssues(),
		issueOf("PROJ-9", "PROJ-9 crashes on start", 4),
		issueOf("PROJ-10", "See proj 9 for the context", 6),
	)
}

func TestSearch_OpensTypingWithTheKeyboardClaimed(t *testing.T) {
	t.Parallel()
	dr := slow(t, newFake(baseIssues()), 120, 30)
	if !dr.m.WantsRawKeys() {
		t.Fatal("a search opens taking typing, or q quits the program out from under it")
	}
	dr.typeText("qr1jk")
	if got := dr.m.input.Value(); got != "qr1jk" {
		t.Fatalf("typed qr1jk and the box holds %q", got)
	}
	if dr.pops != 0 || len(dr.statuses) != 0 || len(dr.opens) != 0 {
		t.Errorf("a letter went somewhere other than the box: pops %d, statuses %v, opens %v", dr.pops, dr.statuses, dr.opens)
	}
	dr.key("esc")
	if dr.pops != 1 {
		t.Errorf("esc asked to go back %d times, want once", dr.pops)
	}
}

func TestSearch_TypingWaitsForTheSettleBeforeAsking(t *testing.T) {
	t.Parallel()
	f := newFake(baseIssues())
	dr := slow(t, f, 120, 30)
	dr.typeText("lo")
	dr.typeText("gin")
	if n := countCalls(f, "Search"); n != 0 {
		t.Fatalf("%d searches ran before the pause", n)
	}
	if len(dr.clock.pending) == 0 {
		t.Fatal("typing set no timer to wait on")
	}
	for _, d := range dr.clock.waits {
		if d != settle {
			t.Errorf("the pause is %s, want %s", d, settle)
		}
	}
	dr.fire()
	if n := countCalls(f, "Search"); n != 1 {
		t.Errorf("%d searches ran after two quick strokes and one pause, want 1", n)
	}
}

func TestSearch_EnterRunsWithoutWaiting(t *testing.T) {
	t.Parallel()
	f := newFake(baseIssues())
	dr := slow(t, f, 120, 30)
	dr.typeText("login")
	dr.key("enter")
	if n := countCalls(f, "Search"); n != 1 {
		t.Fatalf("enter ran %d searches, want 1", n)
	}
	if !dr.m.browsing {
		t.Error("enter with results left the box taking typing")
	}
	dr.fire()
	if n := countCalls(f, "Search"); n != 1 {
		t.Errorf("the pause ran the same search again: %d searches", n)
	}
}

func TestSearch_OneLetterAsksNothing(t *testing.T) {
	t.Parallel()
	f := newFake(baseIssues())
	dr := settled(t, f, Seed{}, 120, 30)
	dr.typeText("a")
	if n := len(f.Calls()); n != 0 {
		t.Errorf("one letter reached the site: %v", f.Calls())
	}
	mustContain(t, dr.view(), "type a little more")
}

func TestSearch_AnIssueKeyIsAskedImmediately(t *testing.T) {
	t.Parallel()
	f := newFake(baseIssues())
	dr := settled(t, f, Seed{}, 120, 30)
	dr.typeText("p-1")
	if n := countCalls(f, "IssueFields"); n == 0 {
		t.Errorf("a key one letter long was held back by the gate: %v", f.Calls())
	}
}

func TestSearch_SendsTextAcrossAllProjectsByDefault(t *testing.T) {
	t.Parallel()
	rec := recorded(newFake(baseIssues()))
	dr := newDriver(t, testDeps(rec), Seed{}, 120, 30, withAfter((&ticks{}).after))
	dr.typeText("login timeout")
	dr.key("enter")
	want := `text ~ "login timeout*" ORDER BY updated DESC`
	if got := rec.sent(); !slices.Equal(got, []string{want}) {
		t.Fatalf("sent %q, want %q", got, want)
	}
	if got := dr.keys(); !slices.Equal(got, []string{"PROJ-1"}) {
		t.Errorf("rows %v, want PROJ-1", got)
	}
}

func TestSearch_TabNarrowsToTheSessionProjectAndBack(t *testing.T) {
	t.Parallel()
	rec := recorded(newFake(baseIssues()))
	dr := newDriver(t, testDeps(rec), Seed{}, 120, 30, withAfter((&ticks{}).after))
	dr.typeText("login")
	dr.key("enter")
	if !slices.Contains(dr.keys(), "OTHER-7") {
		t.Fatalf("every project was not searched: %v", dr.keys())
	}
	dr.key("tab")
	want := `project = "PROJ" AND text ~ "login*" ORDER BY updated DESC`
	if got := rec.sent(); got[len(got)-1] != want {
		t.Fatalf("tab sent %q, want %q", got[len(got)-1], want)
	}
	if slices.Contains(dr.keys(), "OTHER-7") {
		t.Errorf("the other project's issue is still listed: %v", dr.keys())
	}
	mustContain(t, dr.view(), "[PROJ]")
	dr.key("tab")
	if got := rec.sent(); got[len(got)-1] != `text ~ "login*" ORDER BY updated DESC` {
		t.Errorf("tab back sent %q", got[len(got)-1])
	}
	mustContain(t, dr.view(), "[all projects]")
}

func TestSearch_TabIsAbsentWithoutAProject(t *testing.T) {
	t.Parallel()
	d := testDeps(newFake(baseIssues()))
	d.Project = ""
	dr := newDriver(t, d, Seed{Query: "login"}, 120, 30, withAfter(immediately))
	before := dr.m.scope
	dr.key("tab")
	if dr.m.scope != before {
		t.Error("tab changed the scope of a session with no project")
	}
	set, _ := dr.m.LiveKeys()
	for _, b := range set.Acts {
		if b.Help().Key == "tab" {
			t.Errorf("the footer offers tab in a session with no project: %v", set.Acts)
		}
	}
}

func TestSearch_AnExactKeyIsPinnedFirstAndNotRepeated(t *testing.T) {
	t.Parallel()
	dr := settled(t, newFake(withKeyedIssues()), Seed{}, 120, 30)
	dr.typeText("PROJ-9")
	if got := dr.keys(); !slices.Equal(got, []string{"PROJ-9", "PROJ-10"}) {
		t.Fatalf("rows %v, want the exact key first and once", got)
	}
	if !dr.m.rows[0].pinned || dr.m.rows[1].pinned {
		t.Error("only the exact key is the pinned row")
	}
	mustContain(t, dr.view(), "key")
}

func TestSearch_AMissingKeyIsDroppedSilently(t *testing.T) {
	t.Parallel()
	dr := settled(t, newFake(baseIssues()), Seed{}, 120, 30)
	dr.typeText("PROJ-404")
	if len(dr.m.rows) != 0 {
		t.Errorf("a key nothing has produced rows %v", dr.keys())
	}
	if len(dr.statuses) != 0 {
		t.Errorf("a key that is not there raised %v", dr.statuses)
	}
	mustContain(t, dr.view(), `Jira found no issues for "PROJ-404" in all projects.`)
}

func TestSearch_APinThatLandsLateKeepsTheSelection(t *testing.T) {
	t.Parallel()
	dr := settled(t, newFake(baseIssues()), Seed{Query: "login"}, 120, 30)
	dr.key("down")
	dr.key("j")
	under := dr.m.selectedKey()
	if dr.m.cursor == 0 || under == "" {
		t.Fatalf("the cursor is on %q at %d", under, dr.m.cursor)
	}
	dr.send(keyMsg{gen: dr.m.gen, issue: issueOf("PROJ-5", "Unrelated chores", 1)})
	if dr.m.rows[0].iss.Key != "PROJ-5" {
		t.Fatalf("the pin did not land first: %v", dr.keys())
	}
	if got := dr.m.selectedKey(); got != under {
		t.Errorf("the selection moved from %s to %s when the pin landed", under, got)
	}
}

func TestSearch_ANewQueryCancelsTheOldOne(t *testing.T) {
	t.Parallel()
	m := New(testDeps(newFake(baseIssues())), Seed{Query: "login"}, withAfter(immediately))
	m.focused = true
	first := m.run(false)
	oldCtx := m.ctx
	m.typed = "straße"
	second := m.run(false)
	if !errors.Is(oldCtx.Err(), context.Canceled) {
		t.Fatal("starting a new search left the old one running")
	}
	dr := &driver{t: t, m: m}
	dr.run(second)
	dr.run(first)
	if got := dr.keys(); !slices.Equal(got, []string{"PROJ-4"}) {
		t.Errorf("rows %v, want the second search's PROJ-4 and nothing from the first", got)
	}
}

func TestSearch_TheKeyLookupFinishingDoesNotCancelTheTextSearch(t *testing.T) {
	t.Parallel()
	m := New(testDeps(newFake(withKeyedIssues())), Seed{Query: "PROJ-9"}, withAfter(immediately))
	cmds, ok := unwrapCmds(m.Init()())
	if !ok || len(cmds) != 2 {
		t.Fatalf("a key asks two questions, got %d commands", len(cmds))
	}
	dr := &driver{t: t, m: m}
	for _, cmd := range []tea.Cmd{cmds[1], cmds[0]} {
		dr.run(cmd)
	}
	if got := dr.keys(); !slices.Equal(got, []string{"PROJ-9", "PROJ-10"}) {
		t.Errorf("rows %v: the text search was cut short when the lookup finished", got)
	}
	if dr.m.failure != nil {
		t.Errorf("the search failed: %v", dr.m.failure)
	}
}

func TestSearch_CloseCancelsWhatIsInFlight(t *testing.T) {
	t.Parallel()
	f := newFake(baseIssues())
	f.Delay(time.Minute)
	m := New(testDeps(f), Seed{Query: "login"}, withAfter(immediately))
	cmd := m.Init()
	done := make(chan tea.Msg, 1)
	go func() { done <- cmd() }()
	m.Close()
	timer := time.NewTimer(10 * time.Second)
	defer timer.Stop()
	select {
	case msg := <-done:
		reply, _ := msg.(kernel.ReplyMsg)
		got, _ := reply.Msg.(searchMsg)
		if !errors.Is(got.err, context.Canceled) {
			t.Errorf("the search ended with %v, want it cancelled", got.err)
		}
	case <-timer.C:
		t.Fatal("closing the view did not stop the search")
	}
}

func TestSearch_FailurePaths(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name string
		err  error
		want string
	}{
		{"capability", &jira.CapabilityError{Capability: jira.CapPlans, Reason: "needs the Browse projects permission"}, "needs the Browse projects permission"},
		{"rate limit", &jira.RateLimitError{RetryAfter: 30 * time.Second}, "Jira asked to wait 30s before searching again."},
		{"transport", &jira.TransportError{Op: "Search", Err: errors.New("dial tcp: no route to host")}, "The site could not be reached: dial tcp: no route to host"},
		{"validation", &jira.ValidationError{Messages: []string{"unterminated string"}}, "Jira refused this search: unterminated string"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			f := newFake(baseIssues())
			f.FailNext(tc.err)
			dr := settled(t, f, Seed{Query: "login"}, 120, 30)
			mustContain(t, dr.view(), tc.want)
			if len(dr.m.rows) != 0 {
				t.Errorf("a failed search left rows %v", dr.keys())
			}
			if got := dr.lastStatus(); got.Level != kernel.LevelError || got.Text != tc.err.Error() {
				t.Errorf("status %+v, want an error carrying the reason", got)
			}
		})
	}
}

func TestSearch_ARateLimitSuppressesRunsUntilItHasPassed(t *testing.T) {
	t.Parallel()
	f := newFake(baseIssues())
	f.FailNext(&jira.RateLimitError{RetryAfter: 30 * time.Second})
	now := testNow
	d := testDeps(f)
	d.Now = func() time.Time { return now }
	dr := newDriver(t, d, Seed{Query: "login"}, 120, 30, withAfter(immediately))
	if n := countCalls(f, "Search"); n != 1 {
		t.Fatalf("%d searches, want 1", n)
	}
	dr.typeText(" time")
	if n := countCalls(f, "Search"); n != 1 {
		t.Errorf("typing during the wait searched again: %d searches", n)
	}
	dr.key("enter")
	if n := countCalls(f, "Search"); n != 1 {
		t.Errorf("enter during the wait searched again: %d searches", n)
	}
	now = now.Add(31 * time.Second)
	dr.key("enter")
	if n := countCalls(f, "Search"); n != 2 {
		t.Errorf("enter after the wait made %d searches, want 2", n)
	}
	if len(dr.m.rows) == 0 {
		t.Error("the retry brought nothing back")
	}
}

func TestSearch_PagesOnNearTheEndAndStopsAtTwoHundred(t *testing.T) {
	t.Parallel()
	f := newFake(bulkIssues(300))
	dr := settled(t, f, Seed{Query: "paging widget"}, 120, 30)
	if len(dr.m.rows) != appsearch.TextPageSize {
		t.Fatalf("the first page held %d rows, want %d", len(dr.m.rows), appsearch.TextPageSize)
	}
	dr.key("down")
	for range 8 {
		dr.key("G")
	}
	if got := len(dr.m.rows); got != maxRows {
		t.Fatalf("paging stopped at %d rows, want %d", got, maxRows)
	}
	if !dr.m.hasMoreRow() {
		t.Fatal("two hundred rows and more behind them drew no trailing row")
	}
	if n := countCalls(f, "Search"); n != 4 {
		t.Errorf("%d pages were read, want 4", n)
	}
	dr.key("G")
	mustContain(t, dr.view(), "more match - L shows every one in the issue list")
}

func TestSearch_ACursorLoopEnds(t *testing.T) {
	t.Parallel()
	f := newFake(bulkIssues(120))
	f.CursorLoop()
	dr := settled(t, f, Seed{Query: "paging widget"}, 120, 30)
	dr.key("down")
	for range 10 {
		dr.key("G")
	}
	if n := countCalls(f, "Search"); n > 5 {
		t.Errorf("a repeating cursor was followed %d times", n)
	}
	if dr.m.loading || dr.m.paging {
		t.Error("the view is still waiting for a page")
	}
}

func TestSearch_LSendsTheSameSearchToTheIssueList(t *testing.T) {
	t.Parallel()
	rec := recorded(newFake(baseIssues()))
	dr := newDriver(t, testDeps(rec), Seed{Query: "login"}, 120, 30, withAfter(immediately))
	dr.key("down", "I")
	if len(dr.opens) != 1 {
		t.Fatalf("L opened %d views", len(dr.opens))
	}
	open := dr.opens[0]
	got, ok := open.Then.(list.QueryMsg)
	if open.ID != list.ViewID || !ok {
		t.Fatalf("L opened %q with %T", open.ID, open.Then)
	}
	sent := rec.sent()
	if got.JQL != sent[len(sent)-1] {
		t.Errorf("the list was given %q, the site was asked %q", got.JQL, sent[len(sent)-1])
	}
	if got.Title != `Search "login"` {
		t.Errorf("the list is titled %q", got.Title)
	}
}

func TestSearch_EnterPushesTheIssuePaneSeededWithTheRow(t *testing.T) {
	t.Parallel()
	dr := settled(t, newFake(baseIssues()), Seed{Query: "login timeout"}, 120, 30)
	dr.key("down", "enter")
	if len(dr.pushes) != 1 {
		t.Fatalf("enter pushed %d views", len(dr.pushes))
	}
	if push := dr.pushes[0]; push.ID != issue.ViewID || push.Title != "PROJ-1" {
		t.Errorf("pushed %q titled %q", push.ID, push.Title)
	}
	sized, _ := dr.pushes[0].View.Update(kernel.SizeMsg{Width: 120, Height: 30})
	mustContain(t, ansiStrip(sized.View()), "PROJ-1", "Login timeout after a minute")
}

func TestSearch_ShareKeysActOnTheSelectedRow(t *testing.T) {
	t.Parallel()
	d := testDeps(newFake(baseIssues()))
	dr := newDriver(t, d, Seed{Query: "login timeout"}, 120, 30, withAfter(immediately))
	dr.key("down")
	for stroke, want := range map[string]func() string{
		"y": func() string { return "PROJ-1" },
		"Y": func() string { link, _ := kernel.IssueURL(d.Site, "PROJ-1"); return link },
	} {
		_, cmd := dr.m.Update(keyPress(stroke))
		if got := uitest.CopiedBy(cmd, nil); len(got) != 1 || got[0] != want() {
			t.Errorf("%s copied %q, want %q", stroke, got, want())
		}
	}
}

func TestSearch_RightClickSelectsTheRowUnderThePointer(t *testing.T) {
	t.Parallel()
	d := testDeps(newFake(baseIssues()))
	dr := newDriver(t, d, Seed{Query: "login"}, 120, 30, withAfter(immediately))
	if len(dr.m.rows) < 3 {
		t.Fatalf("rows %v", dr.keys())
	}
	target := dr.m.rows[2].iss.Key
	at := uitest.Zone(t, d.Zones, dr.m.View, dr.m.zones.ID(rowZone(target)))
	dr.send(tea.MouseClickMsg{X: at.StartX + 2, Y: at.StartY, Button: tea.MouseRight})
	if got := dr.m.selectedKey(); got != target {
		t.Errorf("the right-click left the cursor on %s, want %s", got, target)
	}
	if !dr.m.browsing {
		t.Error("a right-click on a row left the box taking typing, so the menu would act on a typed letter")
	}
}

func TestSearch_ClicksSelectThenOpen(t *testing.T) {
	t.Parallel()
	d := testDeps(newFake(baseIssues()))
	dr := newDriver(t, d, Seed{Query: "login"}, 120, 30, withAfter(immediately))
	target := dr.m.rows[1].iss.Key
	at := uitest.Zone(t, d.Zones, dr.m.View, dr.m.zones.ID(rowZone(target)))
	click := tea.MouseClickMsg{X: at.StartX + 2, Y: at.StartY, Button: tea.MouseLeft}
	dr.send(click)
	if dr.m.selectedKey() != target || len(dr.pushes) != 0 {
		t.Fatalf("one click selected %q and pushed %d views", dr.m.selectedKey(), len(dr.pushes))
	}
	dr.send(click)
	if len(dr.pushes) != 1 || dr.pushes[0].Title != target {
		t.Errorf("a second click pushed %v", dr.pushes)
	}
}

func TestSearch_ScopeChipIsClickable(t *testing.T) {
	t.Parallel()
	d := testDeps(newFake(baseIssues()))
	dr := newDriver(t, d, Seed{Query: "login"}, 120, 30, withAfter(immediately))
	at := uitest.Zone(t, d.Zones, dr.m.View, dr.m.zones.ID(scopeZone))
	dr.send(tea.MouseClickMsg{X: at.StartX + 1, Y: at.StartY, Button: tea.MouseLeft})
	if dr.m.scope != ScopeProject {
		t.Error("clicking the chip did not narrow the search")
	}
}

func TestSearch_MoreRowIsClickable(t *testing.T) {
	t.Parallel()
	d := testDeps(newFake(bulkIssues(300)))
	dr := newDriver(t, d, Seed{Query: "paging widget"}, 120, 30, withAfter(immediately))
	dr.key("down")
	for range 8 {
		dr.key("G")
	}
	at := uitest.Zone(t, d.Zones, dr.m.View, dr.m.zones.ID(moreZone))
	dr.send(tea.MouseClickMsg{X: at.StartX + 2, Y: at.StartY, Button: tea.MouseLeft})
	if len(dr.opens) != 1 || dr.opens[0].ID != list.ViewID {
		t.Errorf("clicking the trailing row opened %v", dr.opens)
	}
}

func TestSearch_RemembersTheLastQueryAndScope(t *testing.T) {
	t.Parallel()
	mem := &memory{}
	d := testDeps(newFake(baseIssues()))
	d.Memory = mem
	dr := newDriver(t, d, Seed{Query: "login"}, 120, 30, withAfter(immediately))
	dr.key("down", "tab")

	again, ok := NewView(d).(*Model)
	if !ok {
		t.Fatal("NewView did not return a *Model")
	}
	if again.input.Value() != "login" || again.scope != ScopeProject {
		t.Errorf("reopened on %q in scope %d, want login in the project", again.input.Value(), again.scope)
	}
}

type memory struct {
	mu sync.Mutex
	kv map[string]string
}

func (m *memory) Recall(view, key string) (string, bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	v, ok := m.kv[view+"/"+key]
	return v, ok
}

func (m *memory) Keep(view, key, value string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.kv == nil {
		m.kv = map[string]string{}
	}
	m.kv[view+"/"+key] = value
}

func (m *memory) Forget() {}

func TestSearch_ARecalledQueryTypedOverIsReplacedNotAppended(t *testing.T) {
	t.Parallel()
	dr := slow(t, newFake(baseIssues()), 120, 30)
	dr.m.input.SetValue("old words")
	dr.m.replaceOnType = true
	dr.typeText("new")
	if got := dr.m.input.Value(); got != "new" {
		t.Errorf("typing over a recalled query left %q", got)
	}
}

func TestSearch_LiveKeysChangeGenWithState(t *testing.T) {
	t.Parallel()
	dr := settled(t, newFake(baseIssues()), Seed{}, 120, 30)
	_, idle := dr.m.LiveKeys()
	dr.typeText("login")
	_, withRows := dr.m.LiveKeys()
	dr.key("down")
	_, browsing := dr.m.LiveKeys()
	dr.key("/")
	dr.m.input.SetValue("")
	dr.typeText("zzzz")
	dr.m.browsing = true
	_, empty := dr.m.LiveKeys()
	seen := map[int]bool{}
	for _, g := range []int{idle, withRows, browsing, empty} {
		if seen[g] {
			t.Errorf("two states share the generation %d", g)
		}
		seen[g] = true
	}
}

func TestSearch_BrowseKeysMoveTheSelectionAndSlashReturnsToTyping(t *testing.T) {
	t.Parallel()
	dr := settled(t, newFake(baseIssues()), Seed{Query: "login"}, 120, 30)
	dr.key("down")
	if !dr.m.browsing || dr.m.cursor != 0 {
		t.Fatalf("down from the box: browsing %v, cursor %d", dr.m.browsing, dr.m.cursor)
	}
	dr.key("j", "j")
	if dr.m.cursor != 2 {
		t.Errorf("cursor %d after two j, want 2", dr.m.cursor)
	}
	dr.key("g", "g")
	if dr.m.cursor != 0 {
		t.Errorf("cursor %d after g g, want 0", dr.m.cursor)
	}
	dr.key("G")
	if dr.m.cursor != len(dr.m.rows)-1 {
		t.Errorf("cursor %d after G, want the last row", dr.m.cursor)
	}
	dr.key("/")
	if dr.m.browsing {
		t.Error("/ did not return to the box")
	}
	dr.typeText("q")
	if !strings.HasSuffix(dr.m.input.Value(), "q") {
		t.Errorf("a q typed after / went somewhere other than the box: %q", dr.m.input.Value())
	}
}

func TestSearch_RefreshReRunsKeepingTheCursorOnTheSameKey(t *testing.T) {
	t.Parallel()
	f := newFake(baseIssues())
	dr := settled(t, f, Seed{Query: "login"}, 120, 30)
	dr.key("down", "j", "j")
	under := dr.m.selectedKey()
	dr.send(kernel.RefreshMsg{})
	if n := countCalls(f, "Search"); n != 2 {
		t.Errorf("r ran %d searches in all, want 2", n)
	}
	if got := dr.m.selectedKey(); got != under {
		t.Errorf("the cursor moved from %s to %s", under, got)
	}
}

func TestSearch_ASearchMsgForAnEarlierQueryIsDropped(t *testing.T) {
	t.Parallel()
	dr := settled(t, newFake(baseIssues()), Seed{Query: "login"}, 120, 30)
	before := dr.keys()
	dr.send(searchMsg{gen: dr.m.gen - 1, res: appResult(issueOf("PROJ-5", "Unrelated chores", 1))})
	if got := dr.keys(); !slices.Equal(got, before) {
		t.Errorf("a stale answer replaced the rows: %v", got)
	}
}
