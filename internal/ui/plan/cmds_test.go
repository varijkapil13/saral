package plan

import (
	"context"
	"errors"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/varijkapil13/saral/pkg/jira"
)

func collected(t *testing.T, stub *versionStub, plan jira.Plan, known map[string]string) releasesMsg {
	t.Helper()
	msg, err := collectReleases(context.Background(), stub, plan, known)
	if err != nil {
		t.Fatalf("collectReleases: %v", err)
	}
	return msg
}

func sitePlanOf(sources []jira.PlanSource) jira.Plan {
	return jira.Plan{ID: "42", Name: "Delivery", Sources: sources}
}

func TestCollectReleases_NamesSiteProjectSourcesByKey(t *testing.T) {
	t.Parallel()

	stub := &versionStub{keys: map[string]string{"10000": "EX", "10001": "OPS", "10002": "WEB"}}
	msg := collected(t, stub, sitePlanOf(projectSources("10000", "10001", "10002")), nil)

	for ref, want := range stub.keys {
		if got := msg.names[ref]; got != want {
			t.Errorf("names[%s] = %q, want %q", ref, got, want)
		}
	}
	if msg.nameErr != nil {
		t.Errorf("nameErr = %v, want none", msg.nameErr)
	}
	slices.Sort(stub.projectCalls)
	if want := []string{"10000", "10001", "10002"}; !slices.Equal(stub.projectCalls, want) {
		t.Errorf("Project was asked about %v, want each source once", stub.projectCalls)
	}
}

func TestCollectReleases_ALocalPlanAsksNoProject(t *testing.T) {
	t.Parallel()

	stub := &versionStub{keys: map[string]string{"PROJ": "PROJ"}}
	msg := collected(t, stub, jira.Plan{ID: "local", Local: true, Sources: projectSources("PROJ")}, nil)

	if len(stub.projectCalls) != 0 {
		t.Errorf("Project was asked about %v for a plan the profile defines", stub.projectCalls)
	}
	if len(msg.names) != 0 {
		t.Errorf("names = %v, want none", msg.names)
	}
}

func TestCollectReleases_ANameThatCannotBeReadKeepsTheID(t *testing.T) {
	t.Parallel()

	for name, cause := range map[string]error{
		"not found":    &jira.NotFoundError{Kind: "project", ID: "10001"},
		"rate limited": &jira.RateLimitError{Endpoint: "project", RetryAfter: time.Second},
		"transport":    &jira.TransportError{Op: "GET project", Err: errors.New("connection reset")},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			stub := &versionStub{
				keys:       map[string]string{"10000": "EX"},
				projectErr: map[string]error{"10001": cause},
				versions:   map[string][]jira.Version{"10001": {{ID: "7", Name: "ops-1"}}},
			}
			msg := collected(t, stub, sitePlanOf(projectSources("10000", "10001")), nil)

			if msg.names["10000"] != "EX" {
				t.Errorf("names = %v, want the readable project still named", msg.names)
			}
			if _, named := msg.names["10001"]; named {
				t.Errorf("names = %v, want the unreadable project left as an id", msg.names)
			}
			if !errors.Is(msg.nameErr, cause) {
				t.Errorf("nameErr = %v, want %v", msg.nameErr, cause)
			}
			if len(msg.versions) != 1 || len(msg.read) != 2 {
				t.Errorf("read %d versions from %v, want the releases read regardless", len(msg.versions), msg.read)
			}
			held := releasesOf(false, &msg)
			if !strings.Contains(held.head, "id 10001") || !strings.Contains(held.head, "EX") {
				t.Errorf("head = %q, want EX and id 10001", held.head)
			}
		})
	}
}

func TestCollectReleases_BoardNamedProjectsAreNotAskedAgain(t *testing.T) {
	t.Parallel()

	stub := &versionStub{
		projects: map[int64][]jira.ProjectRef{17: {{ID: "10000", Key: "EX"}}},
		keys:     map[string]string{"10001": "OPS"},
	}
	sources := append(projectSources("10000", "10001"), boardSources("17")...)
	msg := collected(t, stub, sitePlanOf(sources), nil)

	if want := []string{"10001"}; !slices.Equal(stub.projectCalls, want) {
		t.Errorf("Project was asked about %v, want only the one no board named", stub.projectCalls)
	}
	if msg.names["10000"] != "EX" || msg.names["10001"] != "OPS" {
		t.Errorf("names = %v", msg.names)
	}
}

func TestCollectReleases_AKnownNameIsNotAskedAgain(t *testing.T) {
	t.Parallel()

	stub := &versionStub{keys: map[string]string{"10001": "OPS"}}
	msg := collected(t, stub, sitePlanOf(projectSources("10000", "10001")), map[string]string{"10000": "EX"})

	if want := []string{"10001"}; !slices.Equal(stub.projectCalls, want) {
		t.Errorf("Project was asked about %v, want only the unknown one", stub.projectCalls)
	}
	if msg.names["10000"] != "EX" || msg.names["10001"] != "OPS" {
		t.Errorf("names = %v", msg.names)
	}
}

func TestCollectReleases_ReloadReusesKnownNames(t *testing.T) {
	t.Parallel()

	stub := &versionStub{
		keys:     map[string]string{"10000": "EX"},
		versions: map[string][]jira.Version{"10000": {{ID: "1", Name: "1.0"}}},
	}
	plan := sitePlanOf(projectSources("10000"))
	reload := reloadOf(stub, plan, nil)

	for range 3 {
		set, err := reload(context.Background())
		if err != nil {
			t.Fatalf("reload: %v", err)
		}
		if got := set.Members[0].Project.Label; got != "EX" {
			t.Errorf("owner label = %q, want the key", got)
		}
		reload = set.Reload
	}
	if len(stub.projectCalls) != 1 {
		t.Errorf("Project was asked %d times over three reloads, want once", len(stub.projectCalls))
	}
}

func TestPlanBrowse_SetOwnersAreKeys(t *testing.T) {
	t.Parallel()

	stub := &versionStub{keys: map[string]string{"10000": "EX", "10001": "OPS"}}
	plan := sitePlanOf(projectSources("10000", "10001"))
	stub.versions = map[string][]jira.Version{
		"10000": {{ID: "1", Name: "1.0"}},
		"10001": {{ID: "2", Name: "ops-1"}},
	}
	msg := collected(t, stub, plan, nil)
	held := releasesOf(false, &msg)
	set := setOf(&plan, &held, nil)

	if len(set.Members) != 2 {
		t.Fatalf("the set holds %d members, want 2", len(set.Members))
	}
	for i, want := range []string{"EX", "OPS"} {
		if got := set.Members[i].Project.Label; got != want {
			t.Errorf("member %d is owned by %q, want %q", i, got, want)
		}
	}
	if !strings.Contains(set.Title, "from EX, OPS") || strings.Contains(set.Title, "id ") {
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
