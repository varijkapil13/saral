package cloud

import (
	"encoding/json"
	"errors"
	"net/http"
	"reflect"
	"slices"
	"testing"

	"github.com/varijkapil13/saral/pkg/jira"
	"github.com/varijkapil13/saral/pkg/jira/jiratest"
)

func sentVersionVerbs(t *testing.T, s *jiratest.Server) []apiVersionVerb {
	t.Helper()

	var body struct {
		Fields map[string]json.RawMessage  `json:"fields"`
		Update map[string][]apiVersionVerb `json:"update"`
	}
	sent := sentTo(t, s, http.MethodPut, "/rest/api/3/issue/"+testIssueKey)
	if err := json.Unmarshal([]byte(sent.Body), &body); err != nil {
		t.Fatalf("reading the body of the edit: %v", err)
	}
	if _, has := body.Fields["fixVersions"]; has {
		t.Errorf("fields carries fixVersions %s; an add/remove patch must not replace the whole list", body.Fields["fixVersions"])
	}
	return body.Update["fixVersions"]
}

func TestFixVersionEdits_BothAdaptersAnswerTheSameWay(t *testing.T) {
	t.Parallel()

	t.Run("adds and removes reach the issue", func(t *testing.T) {
		t.Parallel()

		t.Run("cloud", func(t *testing.T) {
			t.Parallel()

			s := issueServer()
			t.Cleanup(s.Close)
			c, _ := testClient(t, s.URL())
			patch := jira.IssuePatch{AddFixVersions: []string{"10001", " 10002 "}, RemoveFixVersions: []string{"10000"}}
			if err := c.UpdateIssue(t.Context(), testIssueKey, patch); err != nil {
				t.Fatalf("UpdateIssue: %v", err)
			}
			want := []apiVersionVerb{
				{Add: &apiVersionRef{ID: "10001"}},
				{Add: &apiVersionRef{ID: "10002"}},
				{Remove: &apiVersionRef{ID: "10000"}},
			}
			if got := sentVersionVerbs(t, s); !reflect.DeepEqual(got, want) {
				t.Errorf("update.fixVersions = %+v, want %+v", got, want)
			}
		})

		t.Run("fake", func(t *testing.T) {
			t.Parallel()

			f := conformFake(t, jiratest.WithIssues(jiratest.GenFor(conformProject, 1)))
			versions := jiratest.VersionsFor(conformProject)
			key := conformProject + "-1"
			ctx := t.Context()
			if err := f.UpdateIssue(ctx, key, jira.IssuePatch{AddFixVersions: []string{versions[1].ID, versions[2].ID}}); err != nil {
				t.Fatalf("adding: %v", err)
			}
			if err := f.UpdateIssue(ctx, key, jira.IssuePatch{
				AddFixVersions: []string{versions[2].ID}, RemoveFixVersions: []string{versions[1].ID},
			}); err != nil {
				t.Fatalf("adding one already held and removing another: %v", err)
			}
			iss, err := f.Issue(ctx, key)
			if err != nil {
				t.Fatalf("Issue: %v", err)
			}
			ids := make([]string, 0, len(iss.FixVersions))
			for _, v := range iss.FixVersions {
				ids = append(ids, v.ID)
			}
			if slices.Contains(ids, versions[1].ID) || !slices.Contains(ids, versions[2].ID) {
				t.Errorf("FixVersions = %v, want %s removed and %s held once", ids, versions[1].ID, versions[2].ID)
			}
			if n := len(slices.DeleteFunc(slices.Clone(ids), func(id string) bool { return id != versions[2].ID })); n != 1 {
				t.Errorf("%s is held %d times, want an add of a held version to change nothing", versions[2].ID, n)
			}
		})
	})

	refusals := []struct {
		name  string
		patch jira.IssuePatch
	}{
		{"an empty id", jira.IssuePatch{AddFixVersions: []string{"  "}}},
		{"the same version twice", jira.IssuePatch{AddFixVersions: []string{"1"}, RemoveFixVersions: []string{"1"}}},
		{"an edit alongside a clear", jira.IssuePatch{
			AddFixVersions: []string{"1"}, Clear: []jira.FieldRef{{ID: "fixVersions"}},
		}},
	}
	for _, tc := range refusals {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			t.Run("cloud", func(t *testing.T) {
				t.Parallel()

				s := issueServer()
				t.Cleanup(s.Close)
				c, _ := testClient(t, s.URL())
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

				f := conformFake(t, jiratest.WithIssues(jiratest.GenFor(conformProject, 1)))
				err := f.UpdateIssue(t.Context(), conformProject+"-1", tc.patch)
				var invalid *jira.ValidationError
				if !errors.As(err, &invalid) {
					t.Fatalf("got %T (%v), want a *jira.ValidationError", err, err)
				}
			})
		})
	}
}
