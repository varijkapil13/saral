package list

import (
	"errors"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/varijkapil13/saral/internal/ui/issue"
	"github.com/varijkapil13/saral/internal/ui/kernel"
	"github.com/varijkapil13/saral/pkg/jira"
	"github.com/varijkapil13/saral/pkg/jira/jiratest"
)

func TestList_Golden(t *testing.T) {
	t.Parallel()

	for name, size := range map[string]struct{ w, h int }{
		"120x40": {120, 40},
		"100x30": {100, 30},
		"80x20":  {80, 20},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			m := startAll(t, testDeps(newFake(12)), size.w, size.h)
			golden(t, "list_"+name+".golden", frame(m))
		})
	}
}

func TestList_OpensOnTheAccountsOwnWorkScopedToTheSessionsProject(t *testing.T) {
	t.Parallel()

	ada := jira.User{AccountID: "acct-ada", DisplayName: "Ada Lovelace", Active: true, TimeZone: time.UTC}
	dr := newDriver(t, testDeps(newFake(30, jiratest.WithMe(ada))), 120, 30)

	if !strings.Contains(dr.m.jql, `project = "PROJ"`) || !strings.Contains(dr.m.jql, "currentUser()") {
		t.Errorf("the opening query is %q, want the session's project and the current user", dr.m.jql)
	}
	if len(dr.m.issues) == 0 {
		t.Fatal("the opening query found nothing at all")
	}
	for i := range dr.m.issues {
		if dr.m.issues[i].Assignee == nil || dr.m.issues[i].Assignee.AccountID != ada.AccountID {
			t.Fatalf("%s is not assigned to the current user", dr.m.issues[i].Key)
		}
	}
}

func TestList_SaysSoWhenNothingMatches(t *testing.T) {
	t.Parallel()

	dr := newDriver(t, testDeps(newFake(0)), 120, 30)
	dr.send(QueryMsg{JQL: allJQL, Title: "All issues"})

	mustContain(t, dr.view(), "Nothing matches this search.", "0 issues")
	if dr.m.page.HasMore() {
		t.Error("an empty result claims another page exists")
	}
}

func TestList_ShowsAnOpenEndedCountWhileAnotherPageExists(t *testing.T) {
	t.Parallel()

	dr := openAll(t, testDeps(newFake(60, jiratest.WithPageSize(20))), 120, 30)

	mustContain(t, dr.view(), "20+ issues")
	if got := len(dr.m.issues); got != 20 {
		t.Fatalf("loaded %d issues on the first page, want 20", got)
	}
}

func TestList_AsksForTheNextPageAsTheCursorApproachesTheEnd(t *testing.T) {
	t.Parallel()

	f := newFake(60, jiratest.WithPageSize(20))
	dr := openAll(t, testDeps(f), 120, 30)

	before := countCalls(f, "Search")
	for range 12 {
		dr.key("j")
	}

	if got := len(dr.m.issues); got <= 20 {
		t.Errorf("still holding %d issues after scrolling towards the end, want a second page", got)
	}
	if got := countCalls(f, "Search") - before; got != 1 {
		t.Errorf("twelve cursor moves produced %d searches, want exactly one page", got)
	}
	if dr.m.cursor != 12 {
		t.Errorf("the cursor is at %d, want 12: paging must not move it", dr.m.cursor)
	}
}

func TestList_PagesToExhaustionWithoutRepeatingItself(t *testing.T) {
	t.Parallel()

	f := newFake(45, jiratest.WithPageSize(20))
	dr := openAll(t, testDeps(f), 120, 30)

	for range 60 {
		dr.key("j")
	}

	if got := len(dr.m.issues); got != 45 {
		t.Errorf("loaded %d issues, want all 45", got)
	}
	if dr.m.page.HasMore() {
		t.Error("the walk claims another page after the last one")
	}
	mustContain(t, dr.view(), "45 issues")
	mustNotContain(t, dr.view(), "45+")
}

func TestList_TreatsARepeatedPageTokenAsTheEnd(t *testing.T) {
	t.Parallel()

	f := newFake(60, jiratest.WithPageSize(20))
	f.CursorLoop()
	dr := openAll(t, testDeps(f), 120, 30)

	for range 60 {
		dr.key("j")
	}
	if got := len(dr.m.issues); got > 40 {
		t.Errorf("loaded %d issues through a looping cursor, want the walk to stop", got)
	}
}

func TestList_FilterNarrowsRowsLiveAndKeepsTheCursorOnItsIssue(t *testing.T) {
	t.Parallel()

	dr := openAll(t, testDeps(newFake(40)), 120, 30)
	dr.key("j", "j")

	dr.key("/")
	dr.typeText("login")

	if len(dr.m.view) == 0 {
		t.Fatal("the filter matched nothing at all")
	}
	if len(dr.m.view) == len(dr.m.issues) {
		t.Error("the filter left every row visible")
	}
	for _, at := range dr.m.view {
		if !strings.Contains(strings.ToLower(dr.m.issues[at].Summary), "login") {
			t.Errorf("%s is visible but does not match the filter", dr.m.issues[at].Key)
		}
	}

	// The cursor followed the filter onto a row that survived it, and clearing
	// the filter leaves it there rather than sending it back to the top.
	under := dr.m.selectedKey()
	dr.key("ctrl+g")
	if len(dr.m.view) != len(dr.m.issues) {
		t.Error("clearing the filter did not bring the rows back")
	}
	if got := dr.m.selectedKey(); got != under {
		t.Errorf("the cursor came back on %s, want %s", got, under)
	}
}

func TestList_FilterGolden(t *testing.T) {
	t.Parallel()

	dr := openAll(t, testDeps(newFake(40)), 120, 30)
	dr.key("/")
	dr.typeText("login")
	golden(t, "list_filter_120x30.golden", dr.view())
}

func TestList_ABackgroundRefreshPatchesRowsAndLeavesThePlaceAlone(t *testing.T) {
	t.Parallel()

	f := newFake(60, jiratest.WithPageSize(20))
	dr := openAll(t, testDeps(f), 120, 30)
	for range 32 {
		dr.key("j")
	}
	under, cursor, top, loaded := dr.m.selectedKey(), dr.m.cursor, dr.m.top, len(dr.m.issues)
	if top == 0 {
		t.Fatal("the list never scrolled, so this proves nothing about the offset")
	}

	summary := "Renamed by somebody else"
	if err := f.UpdateIssue(t.Context(), under, jira.IssuePatch{Summary: &summary}); err != nil {
		t.Fatalf("UpdateIssue: %v", err)
	}
	dr.send(kernel.RefreshMsg{})

	switch {
	case dr.m.cursor != cursor:
		t.Errorf("the refresh moved the cursor to %d, want %d", dr.m.cursor, cursor)
	case dr.m.top != top:
		t.Errorf("the refresh moved the scroll to %d, want %d", dr.m.top, top)
	case dr.m.selectedKey() != under:
		t.Errorf("the refresh left the cursor on %s, want %s", dr.m.selectedKey(), under)
	case len(dr.m.issues) != loaded:
		t.Errorf("the refresh came back with %d rows, want the %d that were loaded", len(dr.m.issues), loaded)
	}
	mustContain(t, dr.view(), summary)
}

func TestList_ARefreshKeepsTheFilterAndItsCursor(t *testing.T) {
	t.Parallel()

	dr := openAll(t, testDeps(newFake(40)), 120, 30)
	dr.key("/")
	dr.typeText("login")
	dr.key("enter")
	dr.key("j")
	under, visible := dr.m.selectedKey(), len(dr.m.view)

	dr.send(kernel.RefreshMsg{})

	if got := len(dr.m.view); got != visible {
		t.Errorf("the refresh left %d rows visible, want the %d the filter had", got, visible)
	}
	if got := dr.m.selectedKey(); got != under {
		t.Errorf("the refresh moved the cursor to %s, want %s", got, under)
	}
	if dr.m.query != "login" {
		t.Errorf("the refresh cleared the filter, which now reads %q", dr.m.query)
	}
}

func TestList_KeepsTheRowsItHasWhenTheNetworkFails(t *testing.T) {
	t.Parallel()

	f := newFake(20)
	dr := openAll(t, testDeps(f), 120, 30)
	before := len(dr.m.issues)

	f.FailNext(&jira.TransportError{Op: "search", Err: errors.New("dial tcp: no such host")})
	dr.send(kernel.RefreshMsg{})

	if got := len(dr.m.issues); got != before {
		t.Errorf("a transport failure dropped the rows: %d left of %d", got, before)
	}
	if status := dr.lastStatus(); status.Level != kernel.LevelError || !strings.Contains(status.Text, "no such host") {
		t.Errorf("the failure was not reported as an error: %+v", status)
	}
}

func TestList_ReportsWhatTheErrorItselfSays(t *testing.T) {
	t.Parallel()

	tests := map[string]struct {
		err  error
		want string
	}{
		"a permission it does not have": {
			err:  &jira.CapabilityError{Capability: jira.CapBoards, Reason: "needs Browse Projects permission"},
			want: "needs Browse Projects permission",
		},
		"the rate limiter": {
			err:  &jira.RateLimitError{RetryAfter: 30 * time.Second},
			want: "retry in 30s",
		},
		"a query the site refused": {
			err:  &jira.ValidationError{Fields: []jira.FieldError{{Field: "jql", Message: "unknown field"}}},
			want: "jql: unknown field",
		},
	}

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			f := newFake(10)
			f.FailNext(tc.err)
			dr := newDriver(t, testDeps(f), 120, 30)

			if status := dr.lastStatus(); !strings.Contains(status.Text, tc.want) {
				t.Errorf("the status line reads %q, want it to carry %q", status.Text, tc.want)
			}
		})
	}
}

func TestList_DropsTheAnswerToAQuestionTheUserHasChanged(t *testing.T) {
	t.Parallel()

	dr := openAll(t, testDeps(newFake(20)), 120, 30)
	stale := loadedMsg{gen: dr.m.gen - 1, page: jira.NewPage([]jira.Issue{{Key: "GONE-1", Summary: "from an older search"}}, nil)}
	dr.send(stale)

	mustNotContain(t, dr.view(), "GONE-1")
}

func TestList_EnterPushesTheDetailPaneForTheSelectedIssue(t *testing.T) {
	t.Parallel()

	dr := openAll(t, testDeps(newFake(20)), 120, 30)
	dr.key("j", "j")
	want := dr.m.selectedKey()
	dr.key("enter")

	if len(dr.pushes) != 1 {
		t.Fatalf("enter produced %d pushes, want one", len(dr.pushes))
	}
	got := dr.pushes[0]
	if got.ID != issue.ViewID || got.Title != want {
		t.Errorf("pushed %q titled %q, want %q titled %q", got.ID, got.Title, issue.ViewID, want)
	}
}

func TestList_EnterOnAnEmptyResultDoesNothing(t *testing.T) {
	t.Parallel()

	dr := newDriver(t, testDeps(newFake(0)), 120, 30)
	dr.send(QueryMsg{JQL: allJQL})
	dr.key("enter")

	if len(dr.pushes) != 0 {
		t.Errorf("enter pushed %d panes with no row under the cursor", len(dr.pushes))
	}
}

func TestList_RendersWhenTheCapabilityProbeFoundNothing(t *testing.T) {
	t.Parallel()

	// The fake has to answer with nothing too: the kernel probes on Init, so a
	// hand-built empty Caps would be replaced before the first frame.
	d := testDeps(newFake(12, jiratest.WithCapabilities(
		jiratest.NoPlans, jiratest.NoBulkMove, jiratest.NoBoards,
		jiratest.NoAttachments, jiratest.NoDeleteIssues, jiratest.NoTimeZone)))
	d.Caps = jira.Capabilities{}
	m := startAll(t, d, 120, 30)

	golden(t, "list_no_caps_120x30.golden", frame(m))
}

func TestList_RendersDatesInTheAccountsTimezoneAndNotTheMachines(t *testing.T) {
	t.Parallel()

	kolkata, err := time.LoadLocation("Asia/Kolkata")
	if err != nil {
		t.Skipf("no timezone database here: %v", err)
	}

	utc := openAll(t, testDeps(newFake(4)), 120, 30)
	d := testDeps(newFake(4))
	d.Caps.TimeZone = kolkata
	shifted := openAll(t, d, 120, 30)

	if utc.view() == shifted.view() {
		t.Error("the same issues render identically in two timezones, so the account's zone is being ignored")
	}
	mustContain(t, utc.view(), "02 Mar 12:00")
	mustContain(t, shifted.view(), "02 Mar 17:30")
}

func TestList_WheelScrollsTheRowsWithoutMovingTheSelection(t *testing.T) {
	t.Parallel()

	dr := openAll(t, testDeps(newFake(60)), 120, 30)
	under := dr.m.selectedKey()

	dr.send(tea.MouseWheelMsg{Button: tea.MouseWheelDown})

	if dr.m.top == 0 {
		t.Error("the wheel did not scroll")
	}
	if got := dr.m.selectedKey(); got != under {
		t.Errorf("the wheel moved the selection to %s, want it left on %s", got, under)
	}
}

func TestList_ClickingARowSelectsItAndClickingItAgainOpensIt(t *testing.T) {
	t.Parallel()

	d := testDeps(newFake(20))
	m := startAll(t, d, 120, 30, kernel.WithMouse(true))
	_ = m.Frame() // registering the zones is a side effect of drawing them

	var id string
	eventually(t, func() bool {
		id = zoneFor(d, "PROJ-3")
		return id != ""
	})

	at := d.Zones.Get(id)
	click := tea.MouseClickMsg{X: at.StartX + 2, Y: at.StartY, Button: tea.MouseLeft}

	m = send(t, m, click)
	mustContain(t, frame(m), "> PROJ-3")

	m = send(t, m, click)
	mustContain(t, frame(m), "PROJ-3 ")
	// The detail pane is the only view that draws a sidebar of the issue's own
	// fields, so that heading is what tells it apart from the rows behind it.
	if !strings.Contains(frame(m), "Details") {
		t.Errorf("a second click on the selected row did not open it:\n%s", frame(m))
	}
}

// zoneFor finds the zone id the list marked a row with. The prefix is handed
// out by the manager per component, so it is discovered rather than assumed,
// and the manager records a zone on its own goroutine, so it is looked for
// until it appears.
func zoneFor(d kernel.Deps, key string) string {
	for i := 1; i < 4096; i++ {
		id := "zone_" + strconv.Itoa(i) + "__row:" + key
		if !d.Zones.Get(id).IsZero() {
			return id
		}
	}
	return ""
}

func TestList_GAndCapitalGJumpToTheEnds(t *testing.T) {
	t.Parallel()

	dr := openAll(t, testDeps(newFake(40)), 120, 30)

	dr.key("G")
	if got, want := dr.m.cursor, len(dr.m.view)-1; got != want {
		t.Errorf("G left the cursor at %d, want %d", got, want)
	}
	dr.key("g", "g")
	if dr.m.cursor != 0 {
		t.Errorf("g g left the cursor at %d, want 0", dr.m.cursor)
	}
}

func TestList_SwitchingQueryStartsAgainRatherThanAppending(t *testing.T) {
	t.Parallel()

	dr := openAll(t, testDeps(newFake(40)), 120, 30)
	dr.key("j", "j", "j")

	dr.send(QueryMsg{JQL: `project = "PROJ" AND status = "Shipped" ORDER BY key`, Title: "Shipped"})

	if dr.m.cursor != 0 || dr.m.top != 0 {
		t.Errorf("a new query kept the old place: cursor %d, top %d", dr.m.cursor, dr.m.top)
	}
	for i := range dr.m.issues {
		if dr.m.issues[i].Status.Name != "Shipped" {
			t.Fatalf("%s is not in the new result set", dr.m.issues[i].Key)
		}
	}
	mustContain(t, dr.view(), "Shipped")
}

func TestList_WithNoJiraConnectionSaysSoRatherThanFailing(t *testing.T) {
	t.Parallel()

	d := testDeps(nil)
	d.Jira = nil
	dr := newDriver(t, d, 120, 30)

	mustContain(t, dr.view(), "No Jira connection")
	if len(dr.statuses) != 0 {
		t.Errorf("a session with no client produced a status message: %+v", dr.statuses)
	}
}

func TestCommands_AreRegisteredAndRetargetTheListAtTheSessionsProject(t *testing.T) {
	t.Parallel()

	want := map[string]bool{"issues.open": false, "issues.mine": false, "issues.reported": false, "issues.unassigned": false}
	var mine kernel.Command
	for _, cmd := range kernel.Commands() {
		if _, ok := want[cmd.ID]; ok {
			want[cmd.ID] = true
		}
		if cmd.ID == "issues.mine" {
			mine = cmd
		}
	}
	for id, found := range want {
		if !found {
			t.Fatalf("%s is not in the command registry, so the palette cannot reach it", id)
		}
	}

	var query QueryMsg
	for _, msg := range collect(mine.Run(kernel.Deps{Project: "PROJ"})) {
		if got, ok := msg.(kernel.BroadcastMsg); ok {
			query, _ = got.Msg.(QueryMsg)
		}
	}
	if !strings.Contains(query.JQL, `project = "PROJ"`) || !strings.Contains(query.JQL, "currentUser()") {
		t.Errorf("the command asks for %q, want the session's project and the current user", query.JQL)
	}
}

func collect(cmd tea.Cmd) []tea.Msg {
	var out []tea.Msg
	queue := []tea.Cmd{cmd}
	for len(queue) > 0 {
		next := queue[0]
		queue = queue[1:]
		if next == nil {
			continue
		}
		msg := next()
		if cmds, ok := unwrapCmds(msg); ok {
			queue = append(queue, cmds...)
			continue
		}
		if reply, addressed := msg.(kernel.ReplyMsg); addressed {
			msg = reply.Msg
		}
		out = append(out, msg)
	}
	return out
}

// The filter is typed through the kernel, which binds q, r, R, ? and the digits
// for itself. A filter that only works for the characters the kernel does not
// want is one that quietly answers a different question from the one asked —
// and q would quit the program out from under the typing.
func TestFilter_ReceivesEveryCharacterIncludingTheKernelsOwnBindings(t *testing.T) {
	t.Parallel()

	d := testDeps(jiratest.New(jiratest.WithProject("PROJ", jiratest.Scrum),
		jiratest.WithIssues(jiratest.Gen(40))))
	m := startAll(t, d, 120, 30)

	m = send(t, m, keyPress("/"))
	for _, r := range "q2rR?" {
		m = send(t, m, keyPress(string(r)))
	}

	shown := frame(m)
	if !strings.Contains(shown, "q2rR?") {
		t.Errorf("the filter reads something other than what was typed:\n%s", shown)
	}
	if strings.Contains(shown, "nothing is bound to 2") {
		t.Errorf("a digit reached the kernel's slot binding:\n%s", shown)
	}

	// And esc, which the kernel would otherwise have taken for itself, clears it.
	m = send(t, m, keyPress("esc"))
	if got := frame(m); strings.Contains(got, "q2rR?") {
		t.Errorf("esc did not clear the filter:\n%s", got)
	}
}

// injected builds a list holding exactly these issues in exactly this order.
// The local filter is a pass over the rows a search returned, so the order it
// returned them in is the thing a ranking test has to fix.
func injected(t *testing.T, issues []jira.Issue) *Model {
	t.Helper()

	view, ok := New(testDeps(nil)).(*Model)
	if !ok {
		t.Fatal("New did not return a *Model")
	}
	next, _ := view.Update(kernel.SizeMsg{Width: 120, Height: 30})
	m, _ := next.(*Model)
	next, _ = m.Update(loadedMsg{gen: m.gen, page: jira.NewPage(issues, nil)})
	m, _ = next.(*Model)
	return m
}

// typeFilter opens the / filter and types into it, one keypress at a time.
func typeFilter(t *testing.T, m *Model, query string) *Model {
	t.Helper()

	next, _ := m.Update(keyPress("/"))
	model, _ := next.(*Model)
	return typeMore(t, model, query)
}

// typeMore types into a filter that is already open.
func typeMore(t *testing.T, m *Model, more string) *Model {
	t.Helper()

	for _, r := range more {
		next, _ := m.Update(tea.KeyPressMsg{Code: r, Text: string(r)})
		m, _ = next.(*Model)
	}
	return m
}

// visible is the keys the filter leaves, in the order they are drawn.
func visible(m *Model) []string {
	out := make([]string, 0, len(m.view))
	for _, at := range m.view {
		out = append(out, m.issues[at].Key)
	}
	return out
}

func TestFilter_RanksTheRowsRatherThanKeepingTheOrderTheSearchReturned(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		issues []jira.Issue
		query  string
		want   []string
	}{
		{
			name: "a word start beats the same letters inside a word",
			issues: []jira.Issue{
				{Key: "PROJ-1400", Summary: "Speed up the catalogue export"},
				{Key: "PROJ-14", Summary: "Fix the login flow"},
			},
			query: "log",
			want:  []string{"PROJ-14", "PROJ-1400"},
		},
		{
			name: "a whole key beats one it is only the start of",
			issues: []jira.Issue{
				{Key: "PROJ-1400", Summary: "Speed up the catalogue export"},
				{Key: "PROJ-14", Summary: "Fix the login flow"},
			},
			query: "PROJ-14",
			want:  []string{"PROJ-14", "PROJ-1400"},
		},
		{
			name: "a summary beats a status matched the same way",
			issues: []jira.Issue{
				{Key: "PROJ-1", Summary: "Speed up the export", Status: jira.Status{Name: "Triage"}},
				{Key: "PROJ-2", Summary: "Triage the inbox", Status: jira.Status{Name: "Done"}},
			},
			query: "tri",
			want:  []string{"PROJ-2", "PROJ-1"},
		},
		{
			name: "nothing matching leaves nothing",
			issues: []jira.Issue{
				{Key: "PROJ-1", Summary: "Fix the login flow", Status: jira.Status{Name: "Done"}},
			},
			query: "zzzz",
			want:  []string{},
		},
		{
			name: "each field answers for itself, so no match spans two of them",
			issues: []jira.Issue{
				{Key: "PROJ-1", Summary: "Fix the login flow", Status: jira.Status{Name: "Done"}},
			},
			query: "flowdone",
			want:  []string{},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			m := typeFilter(t, injected(t, tt.issues), tt.query)
			if got := visible(m); !slices.Equal(got, tt.want) {
				t.Errorf("%q leaves %v, want %v", tt.query, got, tt.want)
			}
		})
	}
}

// Typing lands on the best match, which is what the palette does with the same
// scorer. Browsing is the other half of it, held below.
func TestFilter_TypingLandsTheCursorOnTheBestMatch(t *testing.T) {
	t.Parallel()

	m := injected(t, []jira.Issue{
		{Key: "PROJ-1400", Summary: "Speed up the catalogue export"},
		{Key: "PROJ-14", Summary: "Fix the login flow"},
	})
	m = typeFilter(t, m, "log")

	if got := m.selectedKey(); got != "PROJ-14" {
		t.Errorf("the cursor is on %s after typing, want the best match PROJ-14", got)
	}
	// And it follows the ranking as the query grows, rather than staying where
	// the first keystroke put it.
	m = typeMore(t, m, "ue")
	if got := m.selectedKey(); got != "PROJ-1400" {
		t.Errorf("the cursor is on %s, want the best match for %q", got, m.query)
	}
}

func TestFilter_ARebuildThatIsNotTypingLeavesTheCursorWhereItWas(t *testing.T) {
	t.Parallel()

	issues := []jira.Issue{
		{Key: "PROJ-1", Summary: "Fix the login flow"},
		{Key: "PROJ-2", Summary: "Document the login flow"},
		{Key: "PROJ-3", Summary: "Retire the login flow"},
	}
	m := typeFilter(t, injected(t, issues), "login")
	next, _ := m.Update(keyPress("enter"))
	m, _ = next.(*Model)
	next, _ = m.Update(keyPress("j"))
	m, _ = next.(*Model)
	under := m.selectedKey()
	if under == "" {
		t.Fatal("the filter left nothing under the cursor")
	}

	// The next page landing under a filter that has not changed: the rows are
	// re-ranked because there are new ones to rank, and the cursor owes the row it
	// was on rather than whatever now ranks best.
	more := []jira.Issue{{Key: "PROJ-4", Summary: "Login"}}
	next, _ = m.Update(pagedMsg{gen: m.gen, page: jira.NewPage(more, nil)})
	m, _ = next.(*Model)

	if !slices.Contains(visible(m), "PROJ-4") {
		t.Fatal("the page that landed is not on screen, so this proves nothing about the cursor")
	}
	if got := m.selectedKey(); got != under {
		t.Errorf("a page landing moved the cursor to %s, want it left on %s", got, under)
	}
}
