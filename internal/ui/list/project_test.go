package list

import (
	"strings"
	"testing"
	"time"

	"github.com/varijkapil13/saral/internal/ui/kernel"
	"github.com/varijkapil13/saral/pkg/jira"
	"github.com/varijkapil13/saral/pkg/jira/jiratest"
)

// twoProjects is a site the session can be re-scoped inside, with work of the
// account's own in both so a switch has something to land on.
func twoProjects() *jiratest.Fake {
	ada := jira.User{AccountID: "acct-ada", DisplayName: "Ada Lovelace", Active: true, TimeZone: time.UTC}
	return jiratest.New(
		jiratest.WithProject("PROJ", jiratest.Scrum),
		jiratest.WithProject("OTHER", jiratest.Kanban),
		jiratest.WithIssues(append(jiratest.Gen(12), jiratest.GenFor("OTHER", 12)...)),
		jiratest.WithMe(ada),
	)
}

func TestList_OpensOnTheWholeSiteWhenNoProjectIsSelected(t *testing.T) {
	t.Parallel()

	d := testDeps(newFake(12))
	d.Project = ""
	dr := newDriver(t, d, 120, 30)

	if strings.Contains(dr.m.jql, "project =") {
		t.Errorf("an unscoped session opened on %q, which names a project it was never given", dr.m.jql)
	}
	if !strings.Contains(dr.m.jql, "currentUser()") {
		t.Errorf("the opening query is %q, want the account's own work", dr.m.jql)
	}
	if dr.m.title != "My issues" {
		t.Errorf("the unscoped search is titled %q, want My issues", dr.m.title)
	}
}

func TestList_AProjectSwitchRetargetsTheSearchItChoseItself(t *testing.T) {
	t.Parallel()

	dr := newDriver(t, testDeps(twoProjects()), 120, 30)
	if !strings.Contains(dr.m.jql, `project = "PROJ"`) {
		t.Fatalf("the list did not open scoped to the session's project: %q", dr.m.jql)
	}

	dr.send(kernel.ProjectMsg{Project: "OTHER"})

	if !strings.Contains(dr.m.jql, `project = "OTHER"`) {
		t.Errorf("the search is still %q after a switch to OTHER", dr.m.jql)
	}
	if dr.m.title != "My issues in OTHER" {
		t.Errorf("the search is titled %q, want My issues in OTHER", dr.m.title)
	}
	if dr.m.deps.Project != "OTHER" {
		t.Errorf("the view is still built for %q, so a pushed detail pane would be too", dr.m.deps.Project)
	}
	if len(dr.m.issues) == 0 {
		t.Fatal("the retargeted search found nothing at all")
	}
	for i := range dr.m.issues {
		if !strings.HasPrefix(dr.m.issues[i].Key, "OTHER-") {
			t.Fatalf("%s is not in the project the session switched to", dr.m.issues[i].Key)
		}
	}
}

func TestList_AProjectSwitchDoesNotDiscardASearchTheUserRan(t *testing.T) {
	t.Parallel()

	dr := newDriver(t, testDeps(twoProjects()), 120, 30)
	dr.send(QueryMsg{JQL: shippedJQL, Title: "Shipped"})

	dr.send(kernel.ProjectMsg{Project: "OTHER"})

	if dr.m.jql != shippedJQL {
		t.Errorf("a switch replaced the query the user ran with %q", dr.m.jql)
	}
	if dr.m.title != "Shipped" {
		t.Errorf("the search is titled %q, want the user's own title", dr.m.title)
	}
	if dr.m.deps.Project != "OTHER" {
		t.Errorf("the view is still built for %q; the key travels even when the search does not", dr.m.deps.Project)
	}
}

func TestList_ASwitchBackToTheSameProjectRefetchesNothing(t *testing.T) {
	t.Parallel()

	dr := newDriver(t, testDeps(twoProjects()), 120, 30)
	before := dr.m.gen

	dr.send(kernel.ProjectMsg{Project: "PROJ"})

	if dr.m.gen != before {
		t.Errorf("a switch to the project already open started another search (generation %d to %d)", before, dr.m.gen)
	}
}

func TestList_GoldenAfterAProjectSwitch(t *testing.T) {
	t.Parallel()

	m := start(t, testDeps(twoProjects()), 120, 30)
	m = send(t, m, kernel.ProjectMsg{Project: "OTHER"})

	golden(t, "list_switched_120x30.golden", frame(m))
}
