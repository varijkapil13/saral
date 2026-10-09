package search

import (
	"errors"
	"testing"
	"time"

	appquery "github.com/varijkapil13/saral/internal/app/query"
	"github.com/varijkapil13/saral/pkg/jira"
	"github.com/varijkapil13/saral/pkg/jira/jiratest"
)

func TestRecentProjects_FallsBackToEverythingVisibleAndKeepsTheOrderRead(t *testing.T) {
	t.Parallel()

	f := jiratest.New(jiratest.WithProject("PROJ", jiratest.Scrum), jiratest.WithIssues(jiratest.Gen(5)))
	got, err := RecentProjects(t.Context(), appquery.NewSearch(f), 50)
	if err != nil {
		t.Fatalf("reading recent projects: %v", err)
	}
	if len(got) != 1 || got[0].Key != "PROJ" {
		t.Errorf("recent projects are %+v, want PROJ once", got)
	}
}

func TestRecentProjects_DropsRepeatsAndIssuesWithNoProject(t *testing.T) {
	t.Parallel()

	got := projectsDistinct([]jira.Issue{
		{Project: jira.ProjectRef{Key: "B", Name: "Bee"}},
		{},
		{Project: jira.ProjectRef{Key: "A", Name: "Ay"}},
		{Project: jira.ProjectRef{Key: "B", Name: "Bee"}},
	})
	if len(got) != 2 || got[0].Key != "B" || got[1].Key != "A" {
		t.Errorf("distinct projects are %+v, want B then A", got)
	}
}

func TestRecentProjects_PassesTheSitesRefusalThroughAsItsOwnType(t *testing.T) {
	t.Parallel()

	for _, err := range []error{
		&jira.CapabilityError{Capability: jira.CapPeople, Reason: "not for this token"},
		&jira.RateLimitError{RetryAfter: time.Second},
		&jira.TransportError{Op: "search", Status: 503},
	} {
		f := jiratest.New(jiratest.WithProject("PROJ", jiratest.Scrum), jiratest.WithIssues(jiratest.Gen(3)))
		f.FailNext(err)
		_, got := RecentProjects(t.Context(), appquery.NewSearch(f), 50)
		if !errors.Is(got, err) {
			t.Errorf("reading recent projects failed with %v, want the site's own %T", got, err)
		}
	}
}
