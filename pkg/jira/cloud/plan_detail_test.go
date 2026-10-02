package cloud

import (
	"errors"
	"net/http"
	"reflect"
	"testing"
	"time"

	"github.com/varijkapil13/saral/pkg/jira"
	"github.com/varijkapil13/saral/pkg/jira/jiratest"
)

const planDetailRoute = planPath + "/{id}"

func planDetailAnswering(body string) jiratest.ServerOption {
	return jiratest.WithHandler(http.MethodGet, planDetailRoute, jsonHandler(http.StatusOK, body))
}

func TestPlanDetail_ReadsCrossProjectReleasesAndExcludedReleaseIDs(t *testing.T) {
	t.Parallel()

	c, s := planClient(t)

	got, err := c.PlanDetail(t.Context(), "7")
	if err != nil {
		t.Fatalf("reading plan 7: %v", err)
	}
	if got.ID != "7" || got.Name != "EX Rollout Plan" || got.Status != "Active" || got.Local {
		t.Errorf("plan = %+v, want the list's fields read from the detail", got.Plan)
	}
	if len(got.Sources) != 1 || got.Sources[0].Type != jira.PlanSourceProject || got.Sources[0].Value != "10000" {
		t.Errorf("Sources = %+v, want the one project source by id", got.Sources)
	}
	want := []jira.CrossProjectRelease{
		{Name: "Spring launch", VersionIDs: []string{"10100", "10200", "10300"}},
		{Name: "Summer launch", VersionIDs: []string{"10400", "10500"}},
	}
	if !reflect.DeepEqual(got.CrossProjectReleases, want) {
		t.Errorf("CrossProjectReleases = %+v, want %+v", got.CrossProjectReleases, want)
	}
	if want := []string{"10600", "10700"}; !reflect.DeepEqual(got.ExcludedVersionIDs, want) {
		t.Errorf("ExcludedVersionIDs = %v, want %v", got.ExcludedVersionIDs, want)
	}

	reqs := s.Requests()
	if len(reqs) != 1 || reqs[0].Method != http.MethodGet || reqs[0].Path != planPath+"/7" || reqs[0].Query != "" {
		t.Errorf("requests = %+v, want one bare GET of %s/7", reqs, planPath)
	}
}

func TestPlanDetail_DecodesNumericIDsAsStrings(t *testing.T) {
	t.Parallel()

	c, _ := planClient(t, planDetailAnswering(`{
  "id": 12, "name": "Mixed", "status": "Active",
  "crossProjectReleases": [{"name": "R", "releaseIds": [10100, "10200"]}],
  "exclusionRules": {"releaseIds": [9007199254740993]}
}`))

	got, err := c.PlanDetail(t.Context(), "12")
	if err != nil {
		t.Fatalf("reading plan 12: %v", err)
	}
	if got.ID != "12" {
		t.Errorf("ID = %q, want 12", got.ID)
	}
	if want := []string{"10100", "10200"}; !reflect.DeepEqual(got.CrossProjectReleases[0].VersionIDs, want) {
		t.Errorf("VersionIDs = %v, want %v", got.CrossProjectReleases[0].VersionIDs, want)
	}
	if want := []string{"9007199254740993"}; !reflect.DeepEqual(got.ExcludedVersionIDs, want) {
		t.Errorf("ExcludedVersionIDs = %v, want %v, an id past 2^53 kept exactly", got.ExcludedVersionIDs, want)
	}
}

func TestPlanDetail_RefusesAnIDThatIsNotANumberWithoutSending(t *testing.T) {
	t.Parallel()

	for _, id := range []string{"", "  ", "local:", "local:EX", "7/../8", "-3", "0", "1.5"} {
		t.Run(id, func(t *testing.T) {
			t.Parallel()

			c, s := planClient(t)

			got, err := c.PlanDetail(t.Context(), id)
			var invalid *jira.ValidationError
			if !errors.As(err, &invalid) {
				t.Fatalf("got %+v, %T (%v), want a *jira.ValidationError", got, err, err)
			}
			if _, ok := invalid.For("planId"); !ok {
				t.Errorf("the refusal names %+v, want planId", invalid.Fields)
			}
			if n := len(s.Requests()); n != 0 {
				t.Errorf("%d requests reached the site, want none", n)
			}
		})
	}
}

func TestPlanDetail_A403NamesCapPlansInTheSitesWords(t *testing.T) {
	t.Parallel()

	c, _ := planClient(t, jiratest.WithStatus(http.MethodGet, planDetailRoute, http.StatusForbidden, "plans_403.json"))

	got, err := c.PlanDetail(t.Context(), "7")
	var refused *jira.CapabilityError
	if !errors.As(err, &refused) {
		t.Fatalf("got %+v, %T (%v), want a *jira.CapabilityError", got, err, err)
	}
	if refused.Capability != jira.CapPlans {
		t.Errorf("Capability = %q, want %q", refused.Capability, jira.CapPlans)
	}
	if refused.Reason == "" {
		t.Error("the refusal carries no reason, and the reason is what the view shows")
	}
}

func TestPlanDetail_A404IsNotFound(t *testing.T) {
	t.Parallel()

	c, _ := planClient(t, jiratest.WithStatus(http.MethodGet, planDetailRoute, http.StatusNotFound, "problem_no_endpoint.json"))

	_, err := c.PlanDetail(t.Context(), "99")
	var missing *jira.NotFoundError
	if !errors.As(err, &missing) {
		t.Fatalf("got %T (%v), want a *jira.NotFoundError", err, err)
	}
	if missing.Kind != "plan" || missing.ID != "99" {
		t.Errorf("the absence names %q %q, want plan 99", missing.Kind, missing.ID)
	}
	var refused *jira.CapabilityError
	if errors.As(err, &refused) {
		t.Errorf("a missing plan came back as a capability refusal: %v", refused)
	}
}

func TestPlanDetail_RateLimitAndTransportFailAsThemselves(t *testing.T) {
	t.Parallel()

	t.Run("rate limit", func(t *testing.T) {
		t.Parallel()

		c, _ := planClient(t, jiratest.WithRateLimit(http.MethodGet, planDetailRoute, 30*time.Second))

		_, err := c.PlanDetail(t.Context(), "7")
		var limited *jira.RateLimitError
		if !errors.As(err, &limited) {
			t.Fatalf("got %T (%v), want a *jira.RateLimitError", err, err)
		}
		if limited.RetryAfter != 30*time.Second {
			t.Errorf("RetryAfter = %s, want 30s", limited.RetryAfter)
		}
	})

	t.Run("a site that broke", func(t *testing.T) {
		t.Parallel()

		c, _ := planClient(t, jiratest.WithStatus(http.MethodGet, planDetailRoute, http.StatusInternalServerError, ""))

		_, err := c.PlanDetail(t.Context(), "7")
		planWantTransport(t, err, http.StatusInternalServerError)
	})

	t.Run("an answer that is not JSON", func(t *testing.T) {
		t.Parallel()

		c, _ := planClient(t, planDetailAnswering("<html>a proxy answered instead</html>"))

		_, err := c.PlanDetail(t.Context(), "7")
		planWantTransport(t, err, http.StatusOK)
	})

	t.Run("a host that never answered", func(t *testing.T) {
		t.Parallel()

		s := jiratest.NewServer()
		dead := s.URL()
		s.Close()
		c, _ := testClient(t, dead, WithRetry(RetryPolicy{Attempts: 1}))

		_, err := c.PlanDetail(t.Context(), "7")
		planWantTransport(t, err, 0)
	})
}

func TestPlanDetail_MissingBlocksAreEmptyNotNil(t *testing.T) {
	t.Parallel()

	for name, body := range map[string]string{
		"absent":    `{"id": 7, "name": "Bare", "status": "Active"}`,
		"null":      `{"id": 7, "name": "Bare", "status": "Active", "crossProjectReleases": null, "exclusionRules": null}`,
		"no ids":    `{"id": 7, "name": "Bare", "status": "Active", "crossProjectReleases": [], "exclusionRules": {}}`,
		"empty ids": `{"id": 7, "name": "Bare", "status": "Active", "exclusionRules": {"releaseIds": []}}`,
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			c, _ := planClient(t, planDetailAnswering(body))

			got, err := c.PlanDetail(t.Context(), "7")
			if err != nil {
				t.Fatalf("reading a plan with nothing planned across projects: %v", err)
			}
			if got.CrossProjectReleases == nil || len(got.CrossProjectReleases) != 0 {
				t.Errorf("CrossProjectReleases = %#v, want empty and non-nil", got.CrossProjectReleases)
			}
			if got.ExcludedVersionIDs == nil || len(got.ExcludedVersionIDs) != 0 {
				t.Errorf("ExcludedVersionIDs = %#v, want empty and non-nil", got.ExcludedVersionIDs)
			}
		})
	}
}
