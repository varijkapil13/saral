package cloud

import (
	"encoding/json"
	"errors"
	"net/http"
	"slices"
	"strings"
	"testing"

	"github.com/varijkapil13/saral/pkg/jira"
	"github.com/varijkapil13/saral/pkg/jira/jiratest"
)

// sentUpdate reads the update object out of a write the server recorded.
func sentUpdate(t *testing.T, s *jiratest.Server, method, path string) map[string][]apiEditOp {
	t.Helper()

	var body struct {
		Update map[string][]apiEditOp `json:"update"`
	}
	sent := sentTo(t, s, method, path)
	if err := json.Unmarshal([]byte(sent.Body), &body); err != nil {
		t.Fatalf("reading the body of %s %s: %v", method, path, err)
	}
	return body.Update
}

func TestUpdateIssue_SendsLabelEditsAsUpdateOpsInAddThenRemoveOrder(t *testing.T) {
	t.Parallel()

	s := issueServer()
	defer s.Close()

	c, _ := testClient(t, s.URL())
	patch := jira.IssuePatch{AddLabels: []string{"checkout", "regression"}, RemoveLabels: []string{"triage"}}
	if err := c.UpdateIssue(t.Context(), testIssueKey, patch); err != nil {
		t.Fatalf("UpdateIssue: %v", err)
	}

	fields := sentFields(t, s, http.MethodPut, "/rest/api/3/issue/"+testIssueKey)
	if _, has := fields["labels"]; has {
		t.Errorf("fields carries labels %s; an add/remove patch must not replace the whole list", fields["labels"])
	}
	update := sentUpdate(t, s, http.MethodPut, "/rest/api/3/issue/"+testIssueKey)
	want := []apiEditOp{{Add: "checkout"}, {Add: "regression"}, {Remove: "triage"}}
	if !slices.Equal(update["labels"], want) {
		t.Errorf("update.labels = %+v, want %+v in add-then-remove order", update["labels"], want)
	}
}

func TestUpdateIssue_SendsNoFieldsKeyForALabelsOnlyPatch(t *testing.T) {
	t.Parallel()

	s := issueServer()
	defer s.Close()

	c, _ := testClient(t, s.URL())
	if err := c.UpdateIssue(t.Context(), testIssueKey, jira.IssuePatch{AddLabels: []string{"checkout"}}); err != nil {
		t.Fatalf("UpdateIssue: %v", err)
	}
	sent := sentTo(t, s, http.MethodPut, "/rest/api/3/issue/"+testIssueKey)
	if strings.Contains(sent.Body, `"fields"`) {
		t.Errorf("a labels-only patch sent a fields key: %s", sent.Body)
	}
}

func TestTransition_CarriesTheSameLabelEditsUpdateIssueDoes(t *testing.T) {
	t.Parallel()

	s := issueServer()
	defer s.Close()

	c, _ := testClient(t, s.URL())
	patch := jira.IssuePatch{AddLabels: []string{"checkout"}, RemoveLabels: []string{"triage"}}
	if err := c.Transition(t.Context(), testIssueKey, "31", patch); err != nil {
		t.Fatalf("Transition: %v", err)
	}
	update := sentUpdate(t, s, http.MethodPost, "/rest/api/3/issue/"+testIssueKey+"/transitions")
	want := []apiEditOp{{Add: "checkout"}, {Remove: "triage"}}
	if !slices.Equal(update["labels"], want) {
		t.Errorf("update.labels = %+v, want %+v", update["labels"], want)
	}
}

func TestUpdateIssue_RefusesLabelEditsThatConflictWithoutSendingARequest(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name  string
		patch jira.IssuePatch
	}{
		{"add alongside a full replacement", jira.IssuePatch{AddLabels: []string{"checkout"}, Labels: &[]string{"other"}}},
		{"remove alongside a full replacement", jira.IssuePatch{RemoveLabels: []string{"checkout"}, Labels: &[]string{"other"}}},
		{"add alongside clearing labels", jira.IssuePatch{AddLabels: []string{"checkout"}, Clear: []jira.FieldRef{{ID: "labels"}}}},
		{"add alongside labels set through Fields", jira.IssuePatch{
			AddLabels: []string{"checkout"},
			Fields:    jira.NewFieldSet(map[string]jira.FieldValue{"labels": {Kind: jira.KindOptions}}),
		}},
		{"an empty label to add", jira.IssuePatch{AddLabels: []string{"  "}}},
		{"an empty label to remove", jira.IssuePatch{RemoveLabels: []string{""}}},
		{"a label with a space", jira.IssuePatch{AddLabels: []string{"needs review"}}},
		{"the same label added twice", jira.IssuePatch{AddLabels: []string{"checkout", "checkout"}}},
		{"the same label added and removed", jira.IssuePatch{AddLabels: []string{"checkout"}, RemoveLabels: []string{"checkout"}}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			s := issueServer()
			defer s.Close()

			c, _ := testClient(t, s.URL())
			err := c.UpdateIssue(t.Context(), testIssueKey, tc.patch)

			var invalid *jira.ValidationError
			if !errors.As(err, &invalid) {
				t.Fatalf("got %v, want a *jira.ValidationError", err)
			}
			if _, ok := invalid.For("labels"); !ok {
				t.Errorf("the failure does not name labels: %v", invalid)
			}
			if served := len(s.Requests()); served != 0 {
				t.Errorf("the site served %d requests for a patch the port refuses to send", served)
			}
		})
	}
}

func TestIssuePatchIsEmpty_AccountsForLabelEdits(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name  string
		patch jira.IssuePatch
		want  bool
	}{
		{"the zero patch", jira.IssuePatch{}, true},
		{"a summary change", jira.IssuePatch{Summary: str("x")}, false},
		{"a label to add", jira.IssuePatch{AddLabels: []string{"checkout"}}, false},
		{"a label to remove", jira.IssuePatch{RemoveLabels: []string{"checkout"}}, false},
		{"both an add and a remove", jira.IssuePatch{AddLabels: []string{"a"}, RemoveLabels: []string{"b"}}, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			if got := tt.patch.IsEmpty(); got != tt.want {
				t.Errorf("IsEmpty() = %v, want %v", got, tt.want)
			}
		})
	}
}
