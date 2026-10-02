package plan

import (
	"context"
	"strings"
	"testing"

	"github.com/varijkapil13/saral/pkg/jira"
)

type versionStub struct {
	versions map[string][]jira.Version
	errs     map[string]error
}

func (s versionStub) Versions(_ context.Context, ref string) ([]jira.Version, error) {
	if err := s.errs[ref]; err != nil {
		return nil, err
	}
	return s.versions[ref], nil
}

func (versionStub) UnresolvedCount(context.Context, string) (int, error) { return 0, nil }

const browseRefusal = "You must have browse project rights in order to view versions."

func TestReadReleases_LeavesOutAProjectTheTokenCannotBrowse(t *testing.T) {
	t.Parallel()

	stub := versionStub{
		versions: map[string][]jira.Version{"10021": {{ID: "1", Name: "1.0"}}, "10453": {{ID: "2", Name: "2.0"}}},
		errs:     map[string]error{"10011": &jira.ValidationError{Messages: []string{browseRefusal}}},
	}
	msg := readReleases(context.Background(), stub, "42", []string{"10021", "10011", "10453"}, 3)()

	got, ok := msg.(releasesMsg)
	if !ok {
		t.Fatalf("one project refusing failed the whole read: %#v", msg)
	}
	if len(got.versions) != 2 {
		t.Errorf("kept %d versions, want the 2 the readable projects answered", len(got.versions))
	}
	if len(got.refused) != 1 || got.refused[0].project != "10011" || got.refused[0].reason != browseRefusal {
		t.Errorf("refused = %+v, want project 10011 in the site's words", got.refused)
	}
}

func TestReadReleases_NamesAMissingProjectOnce(t *testing.T) {
	t.Parallel()

	stub := versionStub{errs: map[string]error{"10011": &jira.NotFoundError{Kind: "project", ID: "10011"}}}
	got, ok := readReleases(context.Background(), stub, "42", []string{"10011"}, 3)().(releasesMsg)
	if !ok || len(got.refused) != 1 {
		t.Fatalf("a project the site cannot find failed the read: %+v", got)
	}
	if strings.Contains(got.refused[0].reason, "10011") {
		t.Errorf("reason %q names the project the row already names", got.refused[0].reason)
	}
}

func TestReadReleases_ARateLimitStillFailsTheRead(t *testing.T) {
	t.Parallel()

	stub := versionStub{
		versions: map[string][]jira.Version{"10021": {{ID: "1", Name: "1.0"}}},
		errs:     map[string]error{"10011": &jira.RateLimitError{}},
	}
	msg := readReleases(context.Background(), stub, "42", []string{"10021", "10011"}, 3)()
	if _, ok := msg.(failedMsg); !ok {
		t.Fatalf("a rate limit was taken as a refusal of one project: %#v", msg)
	}
}

func TestPlans_ARefusedProjectIsNamedBesideTheReleasesThatWereRead(t *testing.T) {
	t.Parallel()

	f := newFake(5)
	dr := newDriver(t, testDeps(f), 160, 30)
	dr.send(plansMsg{gen: dr.m.gen, plans: []jira.Plan{{
		ID: "42", Name: "Delivery", Status: "Active",
		Sources: []jira.PlanSource{
			{Type: jira.PlanSourceProject, Value: "10021"},
			{Type: jira.PlanSourceProject, Value: "10011"},
		},
	}}})
	dr.key("enter")
	dr.send(releasesMsg{
		gen: dr.m.gen, plan: "42",
		versions: []jira.Version{{ID: "1", Name: "Spring drop"}},
		refused:  []refusal{{project: "10011", reason: browseRefusal}},
	})

	frame := dr.view()
	mustContain(t, frame, "Spring drop", "project id 10011 left out", "browse project rights")
}
