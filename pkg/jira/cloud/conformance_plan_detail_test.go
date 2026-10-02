package cloud

import (
	"errors"
	"net/http"
	"testing"
	"time"

	"github.com/varijkapil13/saral/pkg/jira"
	"github.com/varijkapil13/saral/pkg/jira/jiratest"
)

type planDetailBuilder func(*testing.T) jira.PlanDetailReader

func planDetailFromSite(t *testing.T, opts ...jiratest.ServerOption) jira.PlanDetailReader {
	t.Helper()

	s := jiratest.NewServer(opts...)
	t.Cleanup(s.Close)
	c, _ := testClient(t, s.URL(), WithRetry(RetryPolicy{Attempts: 1}))
	return c
}

func TestConformance_PlanDetailRefusals_BothAdaptersAgree(t *testing.T) {
	t.Parallel()

	route := planPath + "/{id}"
	cases := []struct {
		name   string
		id     string
		cloud  planDetailBuilder
		fake   planDetailBuilder
		assert func(*testing.T, error)
	}{
		{
			name: "an empty id is invalid",
			id:   "",
			cloud: func(t *testing.T) jira.PlanDetailReader {
				return planDetailFromSite(t)
			},
			fake: func(t *testing.T) jira.PlanDetailReader { return conformFake(t) },
			assert: func(t *testing.T, err error) {
				t.Helper()
				var invalid *jira.ValidationError
				if !errors.As(err, &invalid) {
					t.Fatalf("got %T (%v), want a *jira.ValidationError", err, err)
				}
			},
		},
		{
			name: "an unknown plan is not found as a plan",
			id:   "999",
			cloud: func(t *testing.T) jira.PlanDetailReader {
				return planDetailFromSite(t, jiratest.WithStatus(http.MethodGet, route, http.StatusNotFound, "problem_no_endpoint.json"))
			},
			fake: func(t *testing.T) jira.PlanDetailReader { return conformFake(t) },
			assert: func(t *testing.T, err error) {
				t.Helper()
				var missing *jira.NotFoundError
				if !errors.As(err, &missing) {
					t.Fatalf("got %T (%v), want a *jira.NotFoundError", err, err)
				}
				if missing.Kind != "plan" || missing.ID != "999" {
					t.Errorf("the absence names %q %q, want plan 999", missing.Kind, missing.ID)
				}
			},
		},
		{
			name: "no Administer Jira names CapPlans with a reason",
			id:   "7",
			cloud: func(t *testing.T) jira.PlanDetailReader {
				return planDetailFromSite(t, jiratest.WithStatus(http.MethodGet, route, http.StatusForbidden, "plans_403.json"))
			},
			fake: func(t *testing.T) jira.PlanDetailReader {
				return conformFake(t, jiratest.WithCapabilities(jiratest.NoPlans))
			},
			assert: func(t *testing.T, err error) {
				t.Helper()
				var refused *jira.CapabilityError
				if !errors.As(err, &refused) {
					t.Fatalf("got %T (%v), want a *jira.CapabilityError", err, err)
				}
				if refused.Capability != jira.CapPlans || refused.Reason == "" {
					t.Errorf("refusal = %+v, want CapPlans with a reason", refused)
				}
			},
		},
		{
			name: "a rate limit is a rate limit",
			id:   "7",
			cloud: func(t *testing.T) jira.PlanDetailReader {
				return planDetailFromSite(t, jiratest.WithRateLimit(http.MethodGet, route, 30*time.Second))
			},
			fake: func(t *testing.T) jira.PlanDetailReader {
				f := conformFake(t)
				f.FailNext(&jira.RateLimitError{RetryAfter: 30 * time.Second})
				return f
			},
			assert: func(t *testing.T, err error) {
				t.Helper()
				var limited *jira.RateLimitError
				if !errors.As(err, &limited) {
					t.Fatalf("got %T (%v), want a *jira.RateLimitError", err, err)
				}
			},
		},
	}

	for _, tt := range cases {
		for _, adapter := range []struct {
			name string
			open planDetailBuilder
		}{
			{name: "cloud", open: tt.cloud},
			{name: "fake", open: tt.fake},
		} {
			t.Run(tt.name+"/"+adapter.name, func(t *testing.T) {
				t.Parallel()

				got, err := adapter.open(t).PlanDetail(t.Context(), tt.id)
				if got.ID != "" || len(got.CrossProjectReleases) != 0 {
					t.Errorf("the failure came back with %+v attached", got)
				}
				tt.assert(t, err)
			})
		}
	}
}
