package plan

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/varijkapil13/saral/pkg/jira"
	"github.com/varijkapil13/saral/pkg/jira/jiratest"
)

func newFake(opts ...jiratest.Option) *jiratest.Fake {
	return jiratest.New(append([]jiratest.Option{
		jiratest.WithProject("PROJ", jiratest.Scrum),
		jiratest.WithIssues(jiratest.Gen(3)),
	}, opts...)...)
}

func TestPlans_ReadsTheSitesPlans(t *testing.T) {
	t.Parallel()

	plans, err := ReadPlans(context.Background(), newFake())
	if err != nil || len(plans) != 1 || plans[0].Name != "PROJ delivery" {
		t.Fatalf("Plans = %+v, %v; want the fake's one plan", plans, err)
	}
}

func TestPlans_FailuresPassThroughUnwrapped(t *testing.T) {
	t.Parallel()

	for name, cause := range map[string]error{
		"refused":      &jira.CapabilityError{Capability: jira.CapPlans, Reason: "the Plans API needs Administer Jira"},
		"rate limited": &jira.RateLimitError{RetryAfter: time.Second},
		"transport":    &jira.TransportError{Op: "GET plans", Err: errors.New("connection reset")},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			f := newFake()
			f.FailNext(cause)
			if _, err := ReadPlans(context.Background(), f); !errors.Is(err, cause) || err.Error() != cause.Error() {
				t.Errorf("err = %v, want %v as it was", err, cause)
			}
		})
	}
}

func TestPlansRefused_OnlyTheRefusalThatNamesPlans(t *testing.T) {
	t.Parallel()

	for name, tc := range map[string]struct {
		err     error
		reason  string
		refused bool
	}{
		"plans refused":         {&jira.CapabilityError{Capability: jira.CapPlans, Reason: " no admin "}, "no admin", true},
		"plans refused, silent": {&jira.CapabilityError{Capability: jira.CapPlans}, "", true},
		"another capability":    {&jira.CapabilityError{Capability: jira.CapBoards, Reason: "no"}, "", false},
		"a rate limit":          {&jira.RateLimitError{}, "", false},
		"a transport error":     {&jira.TransportError{Op: "GET plans", Status: 503}, "", false},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			reason, refused := PlansRefused(tc.err)
			if reason != tc.reason || refused != tc.refused {
				t.Errorf("PlansRefused = %q, %v; want %q, %v", reason, refused, tc.reason, tc.refused)
			}
		})
	}
}

func TestReadReleases_AgainstTheFake(t *testing.T) {
	t.Parallel()

	f := newFake()
	plans, err := ReadPlans(context.Background(), f)
	if err != nil || len(plans) == 0 {
		t.Fatalf("Plans = %v, %v", plans, err)
	}
	got, err := ReadReleases(context.Background(), f, plans[0], nil)
	if err != nil {
		t.Fatal(err)
	}
	ref := plans[0].Sources[0].Value
	if len(got.Versions) == 0 || got.Names[ref] != "PROJ" || got.Detail == nil {
		t.Errorf("got %+v, want PROJ's versions named by key with the plan's detail", got)
	}
}

func TestReadReleases_AgainstTheFakeFailurePaths(t *testing.T) {
	t.Parallel()

	local := Defined{Name: "own", Projects: []string{"PROJ"}}.Plan(0)

	t.Run("a refused project is left out", func(t *testing.T) {
		t.Parallel()
		f := newFake()
		f.FailNext(&jira.CapabilityError{Capability: jira.CapBoards, Reason: "forbidden"})
		got, err := ReadReleases(context.Background(), f, local, nil)
		if err != nil || len(got.Refused) != 1 || got.Refused[0].Ref != "PROJ" {
			t.Errorf("got %+v, %v; want PROJ left out", got, err)
		}
	})
	for name, cause := range map[string]error{
		"rate limited": &jira.RateLimitError{RetryAfter: time.Second},
		"transport":    &jira.TransportError{Op: "GET versions", Err: errors.New("connection reset")},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			f := newFake()
			f.FailNext(cause)
			if _, err := ReadReleases(context.Background(), f, local, nil); !errors.Is(err, cause) {
				t.Errorf("err = %v, want %v", err, cause)
			}
		})
	}
}

func TestPlanSources(t *testing.T) {
	t.Parallel()

	plan := jira.Plan{Sources: []jira.PlanSource{
		{Type: jira.PlanSourceProject, Value: "10000"},
		{Type: jira.PlanSourceProject, Value: " "},
		{Type: jira.PlanSourceFilter, Value: "7"},
	}}
	if got := ProjectRefs(&plan); len(got) != 1 || got[0] != "10000" {
		t.Errorf("ProjectRefs = %v", got)
	}
	if !HasReleaseSources(&plan) {
		t.Error("a plan with a project source has none to read releases from")
	}
	filters := jira.Plan{Sources: []jira.PlanSource{{Type: jira.PlanSourceFilter, Value: "7"}}}
	if HasReleaseSources(&filters) {
		t.Error("a filter-only plan claims release sources")
	}
}
