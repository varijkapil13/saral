package cloud

import (
	"errors"
	"net/http"
	"slices"
	"testing"

	"github.com/varijkapil13/saral/pkg/jira"
	"github.com/varijkapil13/saral/pkg/jira/jiratest"
)

// One set of assertions, run against both adapters, for the label-edit half of
// jira.IssueWriter's UpdateIssue: what a patch that only adds or removes labels
// does, and what it refuses before either adapter sends or applies anything.

func labelsFromSite(t *testing.T) (jira.IssueWriter, *jiratest.Server) {
	t.Helper()

	s := issueServer()
	t.Cleanup(s.Close)
	c, _ := testClient(t, s.URL())
	return c, s
}

func labelsFake(t *testing.T) *jiratest.Fake {
	t.Helper()
	return conformFake(t, jiratest.WithIssues(jiratest.GenFor(conformProject, 1)))
}

func TestLabelEdits_BothAdaptersAnswerTheSameWay(t *testing.T) {
	t.Parallel()

	t.Run("adds and removes reach the issue", func(t *testing.T) {
		t.Parallel()

		t.Run("cloud", func(t *testing.T) {
			t.Parallel()

			c, s := labelsFromSite(t)
			patch := jira.IssuePatch{AddLabels: []string{"checkout"}, RemoveLabels: []string{"triage"}}
			if err := c.UpdateIssue(t.Context(), testIssueKey, patch); err != nil {
				t.Fatalf("UpdateIssue: %v", err)
			}
			update := sentUpdate(t, s, http.MethodPut, "/rest/api/3/issue/"+testIssueKey)
			if len(update["labels"]) != 2 {
				t.Fatalf("update.labels = %+v, want the add and the remove sent as ops", update["labels"])
			}
		})

		t.Run("fake", func(t *testing.T) {
			t.Parallel()

			f := labelsFake(t)
			key := conformProject + "-1"
			ctx := t.Context()
			if err := f.UpdateIssue(ctx, key, jira.IssuePatch{Labels: &[]string{"triage"}}); err != nil {
				t.Fatalf("seeding a known label list: %v", err)
			}
			patch := jira.IssuePatch{AddLabels: []string{"checkout"}, RemoveLabels: []string{"triage"}}
			if err := f.UpdateIssue(ctx, key, patch); err != nil {
				t.Fatalf("UpdateIssue: %v", err)
			}
			iss, err := f.Issue(ctx, key)
			if err != nil {
				t.Fatalf("Issue: %v", err)
			}
			if !slices.Contains(iss.Labels, "checkout") || slices.Contains(iss.Labels, "triage") {
				t.Errorf("Labels = %v, want checkout added and triage removed by the round trip", iss.Labels)
			}
		})
	})

	refusals := []struct {
		name  string
		patch jira.IssuePatch
	}{
		{"a replacement alongside an edit", jira.IssuePatch{AddLabels: []string{"a"}, Labels: &[]string{"b"}}},
		{"an empty label", jira.IssuePatch{AddLabels: []string{"  "}}},
		{"a label with a space", jira.IssuePatch{AddLabels: []string{"a b"}}},
		{"the same label added twice", jira.IssuePatch{AddLabels: []string{"a", "a"}}},
		{"the same label added and removed", jira.IssuePatch{AddLabels: []string{"a"}, RemoveLabels: []string{"a"}}},
	}
	for _, tc := range refusals {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			t.Run("cloud", func(t *testing.T) {
				t.Parallel()

				c, s := labelsFromSite(t)
				err := c.UpdateIssue(t.Context(), testIssueKey, tc.patch)
				var invalid *jira.ValidationError
				if !errors.As(err, &invalid) {
					t.Fatalf("got %T (%v), want a *jira.ValidationError", err, err)
				}
				if served := len(s.Requests()); served != 0 {
					t.Errorf("the site served %d requests for a patch the port refuses to send", served)
				}
			})

			t.Run("fake", func(t *testing.T) {
				t.Parallel()

				f := labelsFake(t)
				err := f.UpdateIssue(t.Context(), conformProject+"-1", tc.patch)
				var invalid *jira.ValidationError
				if !errors.As(err, &invalid) {
					t.Fatalf("got %T (%v), want a *jira.ValidationError", err, err)
				}
			})
		})
	}
}
