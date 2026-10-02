package jiratest_test

import (
	"errors"
	"reflect"
	"testing"

	"github.com/varijkapil13/saral/pkg/jira"
	"github.com/varijkapil13/saral/pkg/jira/jiratest"
)

func TestFake_PlanDetail(t *testing.T) {
	t.Parallel()

	set := jira.PlanDetail{
		Plan:                 jira.Plan{ID: "7", Name: "Set"},
		CrossProjectReleases: []jira.CrossProjectRelease{{Name: "R", VersionIDs: []string{"1", "2"}}},
		ExcludedVersionIDs:   []string{"3"},
	}

	t.Run("a plan set with WithPlanDetail answers as set", func(t *testing.T) {
		t.Parallel()

		f := jiratest.New(jiratest.WithProject("EX", jiratest.Scrum), jiratest.WithPlanDetail("7", set))
		got, err := f.PlanDetail(t.Context(), "7")
		if err != nil || !reflect.DeepEqual(got, set) {
			t.Errorf("got %+v, %v; want %+v", got, err, set)
		}
	})

	t.Run("a listed plan answers with nothing planned across projects", func(t *testing.T) {
		t.Parallel()

		f := jiratest.New(jiratest.WithProject("EX", jiratest.Scrum))
		plans, err := f.Plans(t.Context())
		if err != nil || len(plans) == 0 {
			t.Fatalf("Plans = %+v, %v", plans, err)
		}
		got, err := f.PlanDetail(t.Context(), plans[0].ID)
		if err != nil {
			t.Fatalf("PlanDetail: %v", err)
		}
		if got.ID != plans[0].ID || got.CrossProjectReleases == nil || len(got.CrossProjectReleases) != 0 || got.ExcludedVersionIDs == nil || len(got.ExcludedVersionIDs) != 0 {
			t.Errorf("got %#v, want the listed plan with empty non-nil blocks", got)
		}
	})

	t.Run("an unknown id is not found", func(t *testing.T) {
		t.Parallel()

		_, err := jiratest.New(jiratest.WithProject("EX", jiratest.Scrum)).PlanDetail(t.Context(), "nope")
		var missing *jira.NotFoundError
		if !errors.As(err, &missing) || missing.Kind != "plan" {
			t.Errorf("got %T (%v), want a plan *jira.NotFoundError", err, err)
		}
	})

	t.Run("an empty id is invalid", func(t *testing.T) {
		t.Parallel()

		_, err := jiratest.New().PlanDetail(t.Context(), "")
		var invalid *jira.ValidationError
		if !errors.As(err, &invalid) {
			t.Errorf("got %T (%v), want a *jira.ValidationError", err, err)
		}
	})

	t.Run("CapPlans is checked first", func(t *testing.T) {
		t.Parallel()

		f := jiratest.New(jiratest.WithCapabilities(jiratest.NoPlans), jiratest.WithPlanDetail("7", set))
		_, err := f.PlanDetail(t.Context(), "")
		var refused *jira.CapabilityError
		if !errors.As(err, &refused) || refused.Capability != jira.CapPlans {
			t.Errorf("got %T (%v), want a CapPlans *jira.CapabilityError", err, err)
		}
	})
}
