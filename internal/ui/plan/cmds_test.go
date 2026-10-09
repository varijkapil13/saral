package plan

import (
	"context"
	"strings"
	"testing"

	appplan "github.com/varijkapil13/saral/internal/app/plan"
	"github.com/varijkapil13/saral/pkg/jira"
	"github.com/varijkapil13/saral/pkg/jira/jiratest"
)

func firstSitePlan(t *testing.T, f *jiratest.Fake) jira.Plan {
	t.Helper()
	plans, err := f.Plans(context.Background())
	if err != nil || len(plans) == 0 {
		t.Fatalf("the fake answered %d plans and %v", len(plans), err)
	}
	return plans[0]
}

func TestReleasesOf_AProjectWithNoNameKeepsItsID(t *testing.T) {
	t.Parallel()

	held := releasesOf(false, &appplan.Releases{
		Versions: []jira.Version{{ID: "7", Name: "ops-1"}},
		Owners:   []string{"10001"},
		Names:    map[string]string{"10000": "EX"},
		Read:     []string{"10000", "10001"},
	})
	if !strings.Contains(held.head, "id 10001") || !strings.Contains(held.head, "EX") {
		t.Errorf("head = %q, want EX and id 10001", held.head)
	}
}

func TestReload_ReusesKnownNames(t *testing.T) {
	t.Parallel()

	f := newFake(5)
	plan := firstSitePlan(t, f)
	reload := reloadOf(f, plan, nil)

	for range 3 {
		set, err := reload(context.Background())
		if err != nil {
			t.Fatalf("reload: %v", err)
		}
		if got := set.Members[0].Project.Label; got != "PROJ" {
			t.Errorf("owner label = %q, want the key", got)
		}
		reload = set.Reload
	}
	if n := countCalls(f, "Project"); n != 1 {
		t.Errorf("Project was asked %d times over three reloads, want once", n)
	}
}

func TestPlanBrowse_SetOwnersAreKeys(t *testing.T) {
	t.Parallel()

	f := newFake(5, jiratest.WithProject("OPS", jiratest.Kanban))
	plan := firstSitePlan(t, f)
	plans, _ := f.Plans(context.Background())
	for _, p := range plans[1:] {
		plan.Sources = append(plan.Sources, p.Sources...)
	}
	got, err := appplan.ReadReleases(context.Background(), f, plan, nil)
	if err != nil {
		t.Fatal(err)
	}
	held := releasesOf(false, &got)
	set := setOf(&plan, &held, nil)

	labels := map[string]bool{}
	for _, m := range set.Members {
		labels[m.Project.Label] = true
	}
	if !labels["PROJ"] || !labels["OPS"] || len(labels) != 2 {
		t.Errorf("the set's owners are %v, want PROJ and OPS", labels)
	}
	if !strings.Contains(set.Title, "from OPS, PROJ") || strings.Contains(set.Title, "id ") {
		t.Errorf("title = %q, want projects named by key", set.Title)
	}
}

func TestPlans_AnOpenedPlanKeepsKeysAcrossExpansions(t *testing.T) {
	t.Parallel()

	f := newFake(5)
	plans, err := f.Plans(context.Background())
	if err != nil || len(plans) == 0 {
		t.Fatalf("the fake answered %d plans and %v", len(plans), err)
	}
	dr := newDriver(t, testDeps(f), 120, 30)
	dr.send(plansMsg{gen: dr.m.gen, plans: plans[:1]})
	dr.key("enter")
	dr.key("enter")
	dr.key("enter")

	if n := countCalls(f, "Project"); n != 1 {
		t.Errorf("Project was asked %d times across reopening the plan, want once", n)
	}
	if got := dr.m.known[plans[0].Sources[0].Value]; got != "PROJ" {
		t.Errorf("known = %v, want the key remembered", dr.m.known)
	}
}
