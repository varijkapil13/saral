package plan

import (
	"context"
	"errors"
	"strconv"
	"strings"
	"testing"
	"time"

	appplan "github.com/varijkapil13/saral/internal/app/plan"
	"github.com/varijkapil13/saral/internal/ui/kernel"
	"github.com/varijkapil13/saral/internal/ui/release"
	"github.com/varijkapil13/saral/pkg/jira"
	"github.com/varijkapil13/saral/pkg/jira/jiratest"
)

// sitePlan opens the fake's first plan on a fresh view and hands back the driver
// with the cursor on the plan.
func sitePlan(t *testing.T, f *jiratest.Fake, w, h int) (*driver, jira.Plan) {
	t.Helper()
	plans, err := f.Plans(context.Background())
	if err != nil || len(plans) == 0 {
		t.Fatalf("the fake answered %d plans and %v; the test needs one", len(plans), err)
	}
	dr := newDriver(t, testDeps(f), w, h)
	return dr, plans[0]
}

func projectVersionIDs(t *testing.T, f *jiratest.Fake) []string {
	t.Helper()
	versions, err := f.Versions(context.Background(), "PROJ")
	if err != nil || len(versions) < 3 {
		t.Fatalf("the fake answered %d versions and %v; the test needs three", len(versions), err)
	}
	ids := make([]string, 0, len(versions))
	for i := range versions {
		ids = append(ids, versions[i].ID)
	}
	return ids
}

func rowOfKind(t *testing.T, dr *driver, kind rowKind) int {
	t.Helper()
	for i := range dr.m.rows {
		if dr.m.rows[i].kind == kind {
			return i
		}
	}
	t.Fatalf("no row of kind %d among %d rows", kind, len(dr.m.rows))
	return -1
}

func TestPlans_OpeningASitePlanReadsItsDetailBesideItsReleases(t *testing.T) {
	t.Parallel()

	f := newFake(5)
	ids := projectVersionIDs(t, f)
	plans, _ := f.Plans(context.Background())
	f = newFake(5, jiratest.WithPlanDetail(plans[0].ID, jira.PlanDetail{
		Plan: plans[0],
		CrossProjectReleases: []jira.CrossProjectRelease{
			{Name: "Spring launch", VersionIDs: ids[:2]},
			{Name: "Summer launch", VersionIDs: ids[2:3]},
		},
		ExcludedVersionIDs: ids[1:2],
	}))
	dr, plan := sitePlan(t, f, 120, 20)
	dr.send(plansMsg{gen: dr.m.gen, plans: []jira.Plan{plan}})
	dr.key("enter")

	if n := countCalls(f, "PlanDetail"); n != 1 {
		t.Fatalf("opening a site plan read its detail %d times, want once", n)
	}
	if n := countCalls(f, "Versions"); n != 1 {
		t.Errorf("opening a site plan read the versions %d times, want once", n)
	}
	held := dr.m.rel[plan.ID]
	if held.detail == nil || len(held.detail.CrossProjectReleases) != 2 {
		t.Fatalf("the detail was not kept with the releases: %+v", held.detail)
	}
	mustContain(t, dr.view(), "enter browses", "2 cross-space releases, 1 excluded by the plan")
}

func TestPlans_ALocalPlanAsksForNoDetail(t *testing.T) {
	t.Parallel()

	f := newFake(5)
	dr := newDriver(t, refusedDeps(f), 120, 20, WithDefined(defined()))
	dr.key("enter")

	if n := countCalls(f, "PlanDetail"); n != 0 {
		t.Errorf("opening a plan the profile defines read the site's detail %d times", n)
	}
	mustContain(t, dr.view(), localWords)
}

func TestPlans_TheReleasesCollapseToASummary(t *testing.T) {
	t.Parallel()

	dr := newDriver(t, testDeps(newFake(5)), 120, 30)
	dr.send(plansMsg{gen: dr.m.gen, plans: []jira.Plan{{
		ID: "42", Name: "Delivery", Sources: projectSources("10000", "10001", "10002"),
	}}})
	dr.key("enter")
	versions, owners := manyVersions(476, []string{"10000", "10001", "10002"})
	dr.send(releasesMsg{gen: dr.m.gen, plan: "42", got: appplan.Releases{
		Versions: versions,
		Owners:   owners,
		Names:    map[string]string{"10000": "EX", "10001": "OPS", "10002": "WEB"},
		Read:     []string{"10000", "10001", "10002"},
		Detail:   &jira.PlanDetail{},
	}})

	frame := dr.view()
	mustContain(t, frame, "476 across EX, OPS, WEB - enter browses", "no cross-space releases")
	mustNotContain(t, frame, "release-7")
	if got := countRows(dr.m, 42, rowReleases); got != 1 {
		t.Errorf("the plan has %d summary rows, want 1", got)
	}
	if got := len(dr.m.rows); got > 10 {
		t.Errorf("an opened plan of 476 versions is %d rows", got)
	}
}

func countRows(m *Model, _ int, kind rowKind) int {
	n := 0
	for i := range m.rows {
		if m.rows[i].kind == kind {
			n++
		}
	}
	return n
}

func manyVersions(n int, owners []string) (versions []jira.Version, owned []string) {
	versions = make([]jira.Version, 0, n)
	owned = make([]string, 0, n)
	for i := range n {
		versions = append(versions, jira.Version{ID: strconv.Itoa(1000 + i), Name: "release-" + strconv.Itoa(i)})
		owned = append(owned, owners[i%len(owners)])
	}
	return versions, owned
}

func TestPlans_EnterOnTheSummaryPushesTheBrowserWithEveryVersionAndItsProject(t *testing.T) {
	t.Parallel()

	f := newFake(5)
	ids := projectVersionIDs(t, f)
	dr, plan := sitePlan(t, f, 120, 20)
	dr.send(plansMsg{gen: dr.m.gen, plans: []jira.Plan{{ID: "plan-1", Name: "Delivery", Sources: plan.Sources}}})
	dr.key("enter")
	dr.send(releasesMsg{gen: dr.m.gen, plan: "plan-1", got: appplan.Releases{
		Versions: []jira.Version{{ID: ids[0], Name: "1.0"}, {ID: "77", Name: "ops-1"}},
		Owners:   []string{"10000", "10001"},
		Names:    map[string]string{"10000": "EX"},
		Refused:  []appplan.Refusal{{Kind: appplan.RefusedProject, Ref: "10002", Err: &jira.ValidationError{Messages: []string{browseRefusal}}}},
		Read:     []string{"10000", "10001"},
		Detail: &jira.PlanDetail{
			CrossProjectReleases: []jira.CrossProjectRelease{{Name: "Spring launch", VersionIDs: []string{ids[0], "77"}}},
			ExcludedVersionIDs:   []string{"77"},
		},
	}})
	dr.m.moveTo(rowOfKind(t, dr, rowReleases))
	if set, _ := dr.m.LiveKeys(); len(set.Acts) == 0 || !strings.Contains(actsOf(set), "browse") {
		t.Errorf("the summary row advertises %q, want browse", actsOf(set))
	}
	dr.key("enter")

	if len(dr.pushed) != 1 || dr.pushed[0].ID != release.SetViewID {
		t.Fatalf("enter on the summary pushed %+v, want the release browser", dr.pushed)
	}
	if got := dr.pushed[0].Title; got != "Releases in Delivery" {
		t.Errorf("the pushed view is titled %q", got)
	}

	held := dr.m.rel["plan-1"]
	set := setOf(&dr.m.plans[0].plan, &held, nil)
	if len(set.Members) != 2 {
		t.Fatalf("the set holds %d members, want every version", len(set.Members))
	}
	if got := set.Members[0].Project; got.Ref != "10000" || got.Label != "EX" {
		t.Errorf("the first version's project is %+v, want the key where one is known", got)
	}
	if got := set.Members[1].Project; got.Ref != "10001" || got.Label != "id 10001" {
		t.Errorf("the second version's project is %+v, want an id where no key is known", got)
	}
	if len(set.Groups) != 1 || set.Groups[0].Name != "Spring launch" || len(set.Groups[0].VersionIDs) != 2 {
		t.Errorf("groups = %+v, want the plan's cross-space release", set.Groups)
	}
	if !set.Excluded["77"] || len(set.Excluded) != 1 {
		t.Errorf("excluded = %v, want the plan's one excluded version", set.Excluded)
	}
	if len(set.Notes) != 1 || !strings.Contains(set.Notes[0], "project id 10002 left out") {
		t.Errorf("notes = %v, want the refusal named", set.Notes)
	}
}

func TestPlans_IBrowsesFromAnyLineOfAnOpenPlan(t *testing.T) {
	t.Parallel()

	dr := newDriver(t, refusedDeps(newFake(5)), 120, 30, WithDefined(defined()))

	dr.key("I")
	if len(dr.pushed) != 0 {
		t.Fatal("I browsed a plan that was not open")
	}
	if !strings.Contains(dr.lastStatus().Text, "open the plan first") {
		t.Errorf("I over a closed plan said %q", dr.lastStatus().Text)
	}

	dr.key("enter")
	for line := range dr.m.rows {
		if dr.m.rows[line].plan != 0 {
			break
		}
		dr.pushed = nil
		dr.m.moveTo(line)
		dr.key("I")
		if len(dr.pushed) != 1 {
			t.Errorf("I on line %d (%q) pushed %d views, want one", line, dr.m.rows[line].text, len(dr.pushed))
		}
	}
	if set, _ := dr.m.LiveKeys(); !strings.Contains(actsOf(set), "I releases") {
		t.Errorf("an open plan advertises %q, want I", actsOf(set))
	}
}

func TestPlans_TheReleasesCommandBrowsesThePlanUnderTheCursor(t *testing.T) {
	t.Parallel()

	dr := newDriver(t, refusedDeps(newFake(5)), 120, 30, WithDefined(defined()))
	dr.key("enter")
	dr.send(ReleasesMsg{})

	if len(dr.pushed) != 1 || dr.pushed[0].ID != release.SetViewID {
		t.Errorf("the command pushed %+v, want the release browser", dr.pushed)
	}
	if _, ok := kernel.LookupCommand("plans.releases"); !ok {
		t.Error("plans.releases is not registered")
	}
}

func TestPlans_ARefusedDetailStillBrowsesByProjectAndSaysWhy(t *testing.T) {
	t.Parallel()

	for name, err := range map[string]error{
		"403": &jira.CapabilityError{Capability: jira.CapPlans, Reason: "the Plans API needs Administer Jira"},
		"404": &jira.NotFoundError{Kind: "plan", ID: "plan-1"},
		"429": &jira.RateLimitError{RetryAfter: 30 * time.Second},
		"transport": &jira.TransportError{
			Op: "GET /rest/api/3/plans/plan/1", Err: errors.New("connection reset"),
		},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			f := newFake(5)
			dr, plan := sitePlan(t, f, 160, 20)
			dr.send(plansMsg{gen: dr.m.gen, plans: []jira.Plan{plan}})
			f.FailNext(err)
			dr.key("enter")

			held := dr.m.rel[plan.ID]
			if held.err != nil || !held.read || len(held.versions) == 0 {
				t.Fatalf("a refused detail failed the versions as well: %+v", held)
			}
			reason, _ := jira.Reason(err)
			if held.detailErr == nil || !held.crossWarn {
				t.Fatal("the refusal was not kept beside the releases")
			}
			mustContain(t, dr.view(), "enter browses", "cross-space releases not read", "arranges by project")

			dr.m.moveTo(rowOfKind(t, dr, rowReleases))
			dr.key("enter")
			if len(dr.pushed) != 1 {
				t.Fatalf("a refused detail left nothing to browse: %d pushes", len(dr.pushed))
			}
			set := setOf(&dr.m.plans[0].plan, &held, nil)
			if len(set.Groups) != 0 || set.Explain != "" {
				t.Errorf("the set carries groups %v and %q from a detail that was never read", set.Groups, set.Explain)
			}
			if !strings.Contains(set.Title, "by project") || !strings.Contains(set.Title, reason) {
				t.Errorf("the title %q does not say why the browser arranges by project (%q)", set.Title, reason)
			}
		})
	}
}

func TestPlans_AReloadReadsThePlanAgainWithAFreshContext(t *testing.T) {
	t.Parallel()

	f := newFake(5)
	dr, plan := sitePlan(t, f, 120, 20)
	dr.send(plansMsg{gen: dr.m.gen, plans: []jira.Plan{plan}})
	dr.key("enter")
	dr.m.moveTo(rowOfKind(t, dr, rowReleases))
	dr.key("enter")
	if len(dr.pushed) != 1 {
		t.Fatalf("nothing was pushed: %+v", dr.pushed)
	}
	view := dr.pushed[0].View
	view.Update(kernel.SizeMsg{Width: 120, Height: 20})

	before := countCalls(f, "PlanDetail")
	_, cmd := view.Update(kernel.RefreshMsg{})
	if cmd == nil {
		t.Fatal("a refresh of the browser asked for nothing")
	}
	msg := cmd()
	if reply, ok := msg.(kernel.ReplyMsg); ok {
		msg = reply.Msg
	}
	view.Update(msg)

	if got := countCalls(f, "PlanDetail") - before; got != 1 {
		t.Errorf("a reload read the plan's detail %d times, want once", got)
	}
}
