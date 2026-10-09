package board

import (
	"testing"

	appterm "github.com/varijkapil13/saral/internal/app/term"
	"github.com/varijkapil13/saral/pkg/jira"
)

var ada = jira.User{AccountID: "acct-ada", DisplayName: "Ada Lovelace", Active: true}

// Only mine replaces whoever else the assignee facet names, and leaves the
// other facets alone.
func TestMine_ReplacesOtherAssigneesAndKeepsOtherFacets(t *testing.T) {
	t.Parallel()
	grace := appterm.Term{Facet: appterm.FacetAssignee, ID: "acct-grace", Label: "Grace Hopper"}
	bug := appterm.Term{Facet: appterm.FacetType, ID: "10004", Label: "Bug"}
	got := MineToggled(appterm.Terms{grace, bug}, ada, ada.DisplayName)
	mine := appterm.Term{Facet: appterm.FacetAssignee, ID: ada.AccountID}
	if !got.Has(mine) || got.Has(grace) || !got.Has(bug) || len(got) != 2 {
		t.Errorf("only mine over %v gave %v", appterm.Terms{grace, bug}, got)
	}
	if again := MineToggled(got, ada, ada.DisplayName); again.Count(appterm.FacetAssignee) != 0 || !again.Has(bug) {
		t.Errorf("a second toggle left %v", again)
	}
}

func TestContainsFold(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		s, sub string
		want   bool
	}{
		{"PROJ-12", "proj-1", true},
		{"Fix the Search index", "SEARCH", true},
		{"Déjà vu", "DÉJÀ", true},
		{"short", "longer than it", false},
		{"anything", "", true},
		{"PROJ-2", "proj-3", false},
	} {
		if got := ContainsFold(tc.s, tc.sub); got != tc.want {
			t.Errorf("ContainsFold(%q, %q) = %v, want %v", tc.s, tc.sub, got, tc.want)
		}
	}
}

// AND across facets, OR within one, and an empty id is the field being empty.
func TestMatchesTerms_AndAcrossFacetsOrWithinOne(t *testing.T) {
	t.Parallel()
	iss := jira.Issue{
		Key: "PROJ-1", Assignee: &ada, Status: jira.Status{ID: "3"}, Type: jira.IssueType{ID: "10004"},
		Labels: []string{"billing"},
	}
	assignee := func(id string) appterm.Term { return appterm.Term{Facet: appterm.FacetAssignee, ID: id} }
	status := func(id string) appterm.Term { return appterm.Term{Facet: appterm.FacetStatus, ID: id} }
	label := appterm.Term{Facet: appterm.FacetLabel, ID: "billing"}
	noPriority := appterm.Term{Facet: appterm.FacetPriority}
	for _, tc := range []struct {
		name  string
		terms appterm.Terms
		want  bool
	}{
		{"nothing in force", nil, true},
		{"one value that holds", appterm.Terms{assignee(ada.AccountID)}, true},
		{"either of two values", appterm.Terms{status("1"), status("3")}, true},
		{"one facet that fails", appterm.Terms{assignee(ada.AccountID), status("1")}, false},
		{"unassigned on an assigned issue", appterm.Terms{assignee("")}, false},
		{"a label and an empty priority", appterm.Terms{label, noPriority}, true},
	} {
		if got := MatchesTerms(&iss, tc.terms); got != tc.want {
			t.Errorf("%s: MatchesTerms = %v, want %v", tc.name, got, tc.want)
		}
	}
	if MatchesNeedle(nil, "") {
		t.Error("no issue matched a needle")
	}
	if !MatchesNeedle(&iss, "proj-1") {
		t.Error("a key did not match its own lower case")
	}
}

// A sprint value read with a schema is json text; read without, it is options.
func TestSprintsOn_ReadsEitherShapeOfTheSprintValue(t *testing.T) {
	t.Parallel()
	field := jira.FieldRef{ID: "customfield_10020", Name: "Sprint"}
	asText := jira.Issue{Fields: jira.FieldSet{}.With(field, jira.FieldValue{
		Kind: jira.KindText, Text: `[{"id":7,"name":"one"},{"id":0},{"id":9}]`,
	})}
	asOptions := jira.Issue{Fields: jira.FieldSet{}.With(field, jira.FieldValue{
		Kind: jira.KindOptions, Options: []jira.Option{{ID: "7"}, {ID: "x"}, {ID: " 9 "}},
	})}
	for name, iss := range map[string]jira.Issue{"text": asText, "options": asOptions} {
		if got := SprintsOn(&iss, field); len(got) != 2 || got[0] != 7 || got[1] != 9 {
			t.Errorf("%s: SprintsOn = %v, want [7 9]", name, got)
		}
	}
	if got := SprintsOn(&asText, jira.FieldRef{}); got != nil {
		t.Errorf("no sprint field still read %v", got)
	}
	if got := sprintIDsIn("not json"); got != nil {
		t.Errorf("sprintIDsIn read %v out of text that is no array", got)
	}
}

func TestDoneStatusesAndEstimate(t *testing.T) {
	t.Parallel()
	cfg := jira.BoardConfig{Columns: []jira.Column{
		{Name: "To do", StatusIDs: []string{"1"}},
		{Name: "Done", StatusIDs: []string{" 3 ", "4"}},
		{Name: "Unmapped"},
	}}
	done := DoneStatuses(cfg)
	if len(done) != 2 || !done["3"] || !done["4"] {
		t.Errorf("DoneStatuses = %v, want the last column that maps any", done)
	}
	if DoneStatuses(jira.BoardConfig{}) != nil {
		t.Error("a board with no columns has done statuses")
	}
	if got := EstimateOf(cfg); got.ID != "" {
		t.Errorf("a board that does not estimate estimates in %q", got.ID)
	}
}
