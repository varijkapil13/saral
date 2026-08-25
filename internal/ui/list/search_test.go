package list

import (
	"strconv"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/varijkapil13/saral/internal/ui/kernel"
	"github.com/varijkapil13/saral/pkg/jira"
	"github.com/varijkapil13/saral/pkg/jira/jiratest"
)

// ada is somebody the generated issues are actually assigned to, so a session
// as her has work of her own and a session as the fake's default account —
// which owns none of it — has none. That second case is the one the site this
// was found on looked like: nineteen issues, three of them yours.
var ada = jira.User{AccountID: "acct-ada", DisplayName: "Ada Lovelace", Active: true, TimeZone: time.UTC}

// asMe is a project nothing in is assigned to the account asking.
func asMe(issues int) *jiratest.Fake { return newFake(issues) }

// asAda is the same project with work of the account's own in it.
func asAda(issues int) *jiratest.Fake { return newFake(issues, jiratest.WithMe(ada)) }

func TestList_ShowsEveryIssueInTheProjectIncludingWorkAssignedToNobody(t *testing.T) {
	t.Parallel()

	dr := newDriver(t, testDeps(asAda(12)), 120, 30)
	mine := len(dr.m.issues)
	if mine == 0 {
		t.Fatal("the account has nothing of its own, so this proves nothing about widening")
	}

	dr.key("a")

	if strings.Contains(dr.m.jql, "currentUser()") {
		t.Errorf("the whole-project search is %q, which still narrows by who you are", dr.m.jql)
	}
	if dr.m.title != "All issues in PROJ" {
		t.Errorf("the search is titled %q, want All issues in PROJ", dr.m.title)
	}
	if len(dr.m.issues) <= mine {
		t.Fatalf("the whole project holds %d issues against the %d assigned to the account", len(dr.m.issues), mine)
	}

	var unowned, somebodyElses string
	for i := range dr.m.issues {
		switch iss := &dr.m.issues[i]; {
		case iss.Assignee == nil:
			unowned = iss.Key
		case iss.Assignee.AccountID != ada.AccountID:
			somebodyElses = iss.Key
		}
	}
	if unowned == "" {
		t.Error("no issue assigned to nobody came back; that is the regression this search exists for")
	}
	if somebodyElses == "" {
		t.Error("no issue assigned to somebody else came back")
	}
	mustContain(t, dr.view(), unowned, somebodyElses)
}

func TestList_TheWholeProjectSearchIsReachableFromThePaletteToo(t *testing.T) {
	t.Parallel()

	var all kernel.Command
	for _, cmd := range kernel.Commands() {
		if cmd.ID == "issues.all" {
			all = cmd
		}
	}
	if all.ID == "" {
		t.Fatal("issues.all is not in the command registry, so the palette cannot reach the whole project")
	}
	if all.Group != "Search" {
		t.Errorf("the command is grouped under %q, want Search beside the other three", all.Group)
	}
	if len(all.Keys) != 1 || all.Keys[0] != "a" {
		t.Errorf("the command teaches the keys %v, want the one the view's own footer shows", all.Keys)
	}

	var query QueryMsg
	for _, msg := range collect(all.Run(kernel.Deps{Project: "PROJ"})) {
		if got, ok := msg.(kernel.BroadcastMsg); ok {
			query, _ = got.Msg.(QueryMsg)
		}
	}
	if !strings.Contains(query.JQL, `project = "PROJ"`) || strings.Contains(query.JQL, "currentUser()") {
		t.Errorf("the command asks for %q, want the session's project and nothing about who is asking", query.JQL)
	}
	if query.Title != "All issues in PROJ" {
		t.Errorf("the command titles it %q, want All issues in PROJ", query.Title)
	}
}

func TestList_AskingForTheWholeProjectTwiceSaysSoRatherThanSearchingAgain(t *testing.T) {
	t.Parallel()

	f := asAda(12)
	dr := newDriver(t, testDeps(f), 120, 30)
	dr.key("a")
	before := countCalls(f, "Search")

	dr.key("a")

	if got := countCalls(f, "Search"); got != before {
		t.Errorf("asking for the search already on screen produced %d more searches", got-before)
	}
	if status := dr.lastStatus(); !strings.Contains(status.Text, "already") {
		t.Errorf("the status line reads %q, want it to say the search is the one on screen", status.Text)
	}
}

// The default is the account's own work, because that is what a daily driver
// opens on. It only stops being the default when it would be an empty screen.
func TestList_OpensOnTheAccountsOwnWorkWhenThereIsAny(t *testing.T) {
	t.Parallel()

	dr := newDriver(t, testDeps(asAda(12)), 120, 30)

	if dr.m.title != "My issues in PROJ" {
		t.Errorf("the list opened on %q, want My issues in PROJ", dr.m.title)
	}
	if !strings.Contains(dr.m.jql, "currentUser()") {
		t.Errorf("the opening query is %q, want the account's own work", dr.m.jql)
	}
	if dr.m.widened {
		t.Error("a default with rows in it widened anyway")
	}
}

func TestList_OpensOnTheProjectWhenNothingInItIsAssignedToTheAccount(t *testing.T) {
	t.Parallel()

	dr := newDriver(t, testDeps(asMe(12)), 120, 30)

	if strings.Contains(dr.m.jql, "currentUser()") {
		t.Errorf("the list is still asking for %q, so it opened on an empty screen", dr.m.jql)
	}
	if dr.m.title != "All issues in PROJ" {
		t.Errorf("the list is titled %q, want All issues in PROJ", dr.m.title)
	}
	if len(dr.m.issues) != 12 {
		t.Errorf("the widened search holds %d rows, want the 12 in the project", len(dr.m.issues))
	}
	var said bool
	for _, status := range dr.statuses {
		if strings.Contains(status.Text, "assigned to you") {
			said = true
		}
	}
	if !said {
		t.Errorf("nothing explained why the search on screen is not the one asked for: %+v", dr.statuses)
	}
}

func TestList_DoesNotWidenAgainWhenTheProjectItselfIsEmpty(t *testing.T) {
	t.Parallel()

	f := asMe(0)
	dr := newDriver(t, testDeps(f), 120, 30)

	if got := countCalls(f, "Search"); got > 2 {
		t.Errorf("an empty project was searched %d times; the fallback must be tried once", got)
	}
	if !dr.m.widened {
		t.Fatal("the fallback never ran at all")
	}
	mustContain(t, dr.view(), "Nothing matches this search.")
}

func TestList_DoesNotWidenASearchTheUserRan(t *testing.T) {
	t.Parallel()

	dr := newDriver(t, testDeps(asAda(12)), 120, 30)
	empty := `project = "PROJ" AND status = "Nothing" ORDER BY key`
	dr.send(QueryMsg{JQL: empty, Title: "Nothing"})

	if dr.m.jql != empty {
		t.Errorf("a search the user ran was replaced with %q", dr.m.jql)
	}
	if dr.m.title != "Nothing" {
		t.Errorf("the title is %q, want the user's own", dr.m.title)
	}
}

func TestList_AProjectSwitchGetsTheFallbackBack(t *testing.T) {
	t.Parallel()

	// Ada has work in PROJ and none in a two-issue OTHER, so the switch is the
	// case the fallback exists for and the opening frame is not.
	f := jiratest.New(
		jiratest.WithProject("PROJ", jiratest.Scrum),
		jiratest.WithProject("OTHER", jiratest.Kanban),
		jiratest.WithIssues(append(jiratest.Gen(12), jiratest.GenFor("OTHER", 2)...)),
		jiratest.WithMe(ada),
	)
	dr := newDriver(t, testDeps(f), 120, 30)
	if dr.m.title != "My issues in PROJ" {
		t.Fatalf("the list opened on %q", dr.m.title)
	}

	dr.send(kernel.ProjectMsg{Project: "OTHER"})

	if dr.m.title != "All issues in OTHER" {
		t.Errorf("after the switch the list is titled %q, want All issues in OTHER", dr.m.title)
	}
	if len(dr.m.issues) != 2 {
		t.Errorf("the widened search holds %d rows, want the 2 in OTHER", len(dr.m.issues))
	}
}

func TestList_WithNoProjectDoesNotWidenToTheWholeSite(t *testing.T) {
	t.Parallel()

	d := testDeps(asMe(12))
	d.Project = ""
	dr := newDriver(t, d, 120, 30)

	if strings.Contains(dr.m.jql, "project =") {
		t.Errorf("an unscoped session widened to %q, naming a project it was never given", dr.m.jql)
	}
	if !strings.Contains(dr.m.jql, "currentUser()") {
		t.Errorf("the query is %q, want the account's own work left alone", dr.m.jql)
	}
	if dr.m.title != "My issues" {
		t.Errorf("the search is titled %q, want My issues", dr.m.title)
	}
}

func TestScoped_ComposesTheProjectAndTheClauseWithoutInventingEither(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		project string
		clause  string
		want    string
	}{
		{"a project and a clause", "PROJ", "assignee = currentUser()", `project = "PROJ" AND assignee = currentUser()`},
		{"a project and no clause at all", "PROJ", "", `project = "PROJ"`},
		{"a clause and no project", "", "assignee IS EMPTY", "assignee IS EMPTY"},
		{"neither", "", "", ""},
		{"a key somebody typed a quote into", `PR"OJ`, "", `project = "PROJ"`},
		{"a key with room around it", "  PROJ  ", "", `project = "PROJ"`},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if got := scoped(tc.project, tc.clause); got != tc.want {
				t.Errorf("scoped(%q, %q) = %q, want %q", tc.project, tc.clause, got, tc.want)
			}
		})
	}
}

func TestSearches_NameTheProjectTheyAreAboutAndOrderThemselves(t *testing.T) {
	t.Parallel()

	for _, s := range searches {
		t.Run(s.id, func(t *testing.T) {
			t.Parallel()

			jql, title := s.at("PROJ")
			if !strings.HasPrefix(jql, `project = "PROJ"`) {
				t.Errorf("%s composes %q, which is not scoped to the session's project", s.id, jql)
			}
			if !strings.Contains(jql, "ORDER BY") {
				t.Errorf("%s composes %q with no ordering, so the site picks one", s.id, jql)
			}
			if strings.Contains(jql, "AND ORDER BY") {
				t.Errorf("%s composes %q, which is not JQL", s.id, jql)
			}
			if !strings.HasSuffix(title, " in PROJ") {
				t.Errorf("%s is titled %q, which does not say what is on screen", s.id, title)
			}
			unscoped, plain := s.at("")
			if strings.Contains(unscoped, "project =") {
				t.Errorf("%s composes %q with no project to name", s.id, unscoped)
			}
			if strings.HasSuffix(plain, " in ") {
				t.Errorf("%s is titled %q", s.id, plain)
			}
		})
	}
}

// --- the search on screen, shown and edited where it is ---------------------

func TestList_TheQueryPromptShowsTheSearchThatIsActuallyRunning(t *testing.T) {
	t.Parallel()

	dr := newDriver(t, testDeps(asAda(12)), 120, 30)
	want := dr.m.jql

	dr.key("e")

	if !dr.m.asking {
		t.Fatal("e did not open the prompt")
	}
	if got := dr.m.ask.Value(); got != want {
		t.Errorf("the prompt holds %q, want the search on screen %q", got, want)
	}
	mustContain(t, dr.view(), "currentUser()", "enter runs it")
	if !dr.m.WantsRawKeys() {
		t.Error("the view is not claiming raw keys, so the kernel will eat what is typed into the prompt")
	}
}

func TestList_RunsTheSearchTypedIntoThePromptAndTitlesItAfterItself(t *testing.T) {
	t.Parallel()

	dr := newDriver(t, testDeps(asAda(12)), 120, 30)
	dr.key("e")
	for range len(dr.m.ask.Value()) {
		dr.send(tea.KeyPressMsg{Code: tea.KeyBackspace})
	}
	dr.typeText(`project = "PROJ" AND status = "Shipped" ORDER BY key`)
	dr.key("enter")

	if dr.m.asking {
		t.Error("enter left the prompt open")
	}
	if dr.m.jql != `project = "PROJ" AND status = "Shipped" ORDER BY key` {
		t.Fatalf("the list is running %q", dr.m.jql)
	}
	if dr.m.title != dr.m.jql {
		t.Errorf("the search is titled %q, want the search itself so the line says what is on screen", dr.m.title)
	}
	if len(dr.m.issues) == 0 {
		t.Fatal("the typed search found nothing at all")
	}
	for i := range dr.m.issues {
		if dr.m.issues[i].Status.Name != "Shipped" {
			t.Fatalf("%s is not in the result of the search that was typed", dr.m.issues[i].Key)
		}
	}
	mustContain(t, dr.view(), "Shipped")
}

func TestList_LeavingThePromptKeepsTheSearchThatWasOnScreen(t *testing.T) {
	t.Parallel()

	dr := newDriver(t, testDeps(asAda(12)), 120, 30)
	was, wasTitled := dr.m.jql, dr.m.title
	dr.key("e")
	dr.typeText(" AND nonsense")
	dr.key("esc")

	if dr.m.asking {
		t.Error("esc left the prompt open")
	}
	if dr.m.jql != was || dr.m.title != wasTitled {
		t.Errorf("esc left the list running %q titled %q, want %q titled %q", dr.m.jql, dr.m.title, was, wasTitled)
	}
}

func TestList_AnEmptyPromptRunsNothingAndSaysWhatIsStillOnScreen(t *testing.T) {
	t.Parallel()

	dr := newDriver(t, testDeps(asAda(12)), 120, 30)
	was, wasTitled := dr.m.jql, dr.m.title
	dr.key("e")
	for range len(dr.m.ask.Value()) {
		dr.send(tea.KeyPressMsg{Code: tea.KeyBackspace})
	}
	dr.key("enter")

	if dr.m.jql != was {
		t.Errorf("an empty prompt ran %q", dr.m.jql)
	}
	if status := dr.lastStatus(); status.Level != kernel.LevelWarn || !strings.Contains(status.Text, wasTitled) {
		t.Errorf("the status line reads %+v, want a warning naming the search still on screen", status)
	}
}

// The prompt is typed through the kernel, which binds q, r, R, ? and the digits
// for itself. A JQL query is mostly digits and quotes, so a prompt the kernel
// can reach into is one that runs something other than what was typed.
func TestQueryPrompt_ReceivesEveryCharacterIncludingTheKernelsOwnBindings(t *testing.T) {
	t.Parallel()

	m := start(t, testDeps(asAda(40)), 120, 30)
	m = keys(t, m, "e")
	for _, r := range "q2rR?" {
		m = send(t, m, keyPress(string(r)))
	}

	shown := frame(m)
	if !strings.Contains(shown, "q2rR?") {
		t.Errorf("the prompt reads something other than what was typed:\n%s", shown)
	}
	if strings.Contains(shown, "nothing is bound to 2") {
		t.Errorf("a digit reached the kernel's slot binding:\n%s", shown)
	}
	m = keys(t, m, "esc")
	if got := frame(m); strings.Contains(got, "q2rR?") {
		t.Errorf("esc did not put the prompt away:\n%s", got)
	}
}

func TestList_TheSearchIsAlsoEditableFromThePaletteAndByPointer(t *testing.T) {
	t.Parallel()

	d := testDeps(asAda(12))
	m := start(t, d, 120, 30, kernel.WithMouse(true))
	m = send(t, m, kernel.BroadcastMsg{Msg: EditQueryMsg{}})
	mustContain(t, frame(m), "enter runs it")

	m = keys(t, m, "esc")
	_ = m.Frame() // registering the zones is a side effect of drawing them

	var id string
	eventually(t, func() bool {
		id = zoneNamed(d, "title")
		return id != ""
	})
	at := d.Zones.Get(id)
	m = send(t, m, tea.MouseClickMsg{X: at.StartX + 1, Y: at.StartY, Button: tea.MouseLeft})

	if got := frame(m); !strings.Contains(got, "enter runs it") {
		t.Errorf("clicking the line that names the search did not offer to change it:\n%s", got)
	}
}

// zoneNamed finds a zone id the list marked. The prefix is handed out by the
// manager per component, so it is discovered rather than assumed, and the
// manager records a zone on its own goroutine, so it is looked for until it
// appears.
func zoneNamed(d kernel.Deps, name string) string {
	for i := 1; i < 4096; i++ {
		id := "zone_" + strconv.Itoa(i) + "__" + name
		if !d.Zones.Get(id).IsZero() {
			return id
		}
	}
	return ""
}

func TestList_TheTitleAlwaysNamesWhatIsOnScreen(t *testing.T) {
	t.Parallel()

	typed := `project = "PROJ" AND status = "Shipped" ORDER BY key`
	tests := []struct {
		name  string
		reach func(*driver)
		want  string
	}{
		{"the account's own work", func(*driver) {}, "My issues in PROJ"},
		{"every issue in the project", func(dr *driver) { dr.key("a") }, "All issues in PROJ"},
		{"a saved search the kernel dispatched", func(dr *driver) {
			dr.send(kernel.RunQueryMsg{JQL: typed, Title: "Shipped work"})
		}, "Shipped work"},
		{"a search typed into the prompt", func(dr *driver) {
			dr.key("e")
			for range len(dr.m.ask.Value()) {
				dr.send(tea.KeyPressMsg{Code: tea.KeyBackspace})
			}
			dr.typeText(typed)
			dr.key("enter")
		}, typed},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			dr := newDriver(t, testDeps(asAda(12)), 120, 30)
			tc.reach(dr)
			if dr.m.title != tc.want {
				t.Errorf("the list is titled %q, want %q", dr.m.title, tc.want)
			}
			mustContain(t, dr.view(), tc.want)
		})
	}
}

func TestList_QueryPromptGolden(t *testing.T) {
	t.Parallel()

	for name, size := range map[string]struct{ w, h int }{
		"120x30": {120, 30},
		"100x24": {100, 24},
		"80x20":  {80, 20},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			dr := newDriver(t, testDeps(asAda(12)), size.w, size.h)
			dr.key("e")
			golden(t, "list_query_"+name+".golden", dr.view())
		})
	}
}

func TestList_WidenedGolden(t *testing.T) {
	t.Parallel()

	for name, size := range map[string]struct{ w, h int }{
		"120x30": {120, 30},
		"80x20":  {80, 20},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			m := start(t, testDeps(asMe(12)), size.w, size.h)
			golden(t, "list_widened_"+name+".golden", frame(m))
		})
	}
}
