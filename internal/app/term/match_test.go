package term

import (
	"testing"

	"github.com/varijkapil13/saral/pkg/jira"
)

func TestTermsMatch_IsAndAcrossFacetsAndOrWithinOne(t *testing.T) {
	t.Parallel()

	iss := &jira.Issue{
		Key:      "PROJ-1",
		Assignee: &jira.User{AccountID: "ada"},
		Status:   jira.Status{ID: "3"},
		Type:     jira.IssueType{ID: "10"},
		Labels:   []string{"ui"},
	}
	assignee := func(id string) Term { return Term{Facet: FacetAssignee, ID: id} }
	status := func(id string) Term { return Term{Facet: FacetStatus, ID: id} }
	label := Term{Facet: FacetLabel, ID: "ui"}
	noPriority := Term{Facet: FacetPriority}
	tests := []struct {
		name  string
		terms Terms
		want  bool
	}{
		{"no terms", nil, true},
		{"one value that matches", Terms{assignee("ada")}, true},
		{"either of two values", Terms{assignee("bob"), assignee("ada")}, true},
		{"neither of two values", Terms{assignee("bob"), assignee("cy")}, false},
		{"two facets that both match", Terms{assignee("ada"), status("3")}, true},
		{"two facets where one does not", Terms{assignee("ada"), status("4")}, false},
		{"unassigned on an assigned issue", Terms{assignee("")}, false},
		{"no priority on an issue with none", Terms{noPriority}, true},
		{"a label it carries", Terms{label}, true},
		{"a label and an empty priority", Terms{label, noPriority}, true},
		{"a type it is not", Terms{{Facet: FacetType, ID: "11"}}, false},
		{"no reporter on an issue with none", Terms{{Facet: FacetReporter}}, true},
		{"no facet at all", Terms{{Facet: FacetNone, ID: "x"}}, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			if got := tt.terms.Match(iss); got != tt.want {
				t.Errorf("Match = %v, want %v", got, tt.want)
			}
		})
	}
}
