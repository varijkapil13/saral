package search

import (
	"errors"
	"slices"
	"testing"
	"time"

	appquery "github.com/varijkapil13/saral/internal/app/query"
	"github.com/varijkapil13/saral/pkg/jira"
	"github.com/varijkapil13/saral/pkg/jira/jiratest"
)

const testJQL = "project = PROJ ORDER BY key"

func TestSavedQueries_RefusesTheOnesThatCouldNotBeRun(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name  string
		query SavedQuery
	}{
		{name: "no name to reach it by", query: SavedQuery{Name: "  ", JQL: testJQL}},
		{name: "nothing to run", query: SavedQuery{Name: "Mine", JQL: "   "}},
		{name: "a key that is not on the keyboard row", query: SavedQuery{Name: "Mine", JQL: testJQL, Slot: MaxSavedSlot + 1}},
		{name: "a negative key", query: SavedQuery{Name: "Mine", JQL: testJQL, Slot: -1}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			if _, err := NewSavedQueries(tt.query); err == nil {
				t.Fatalf("%+v was accepted", tt.query)
			}
		})
	}
}

func TestSavedQueries_KeepsTheOrderAddedAndFindsAQueryByNameOrKey(t *testing.T) {
	t.Parallel()

	saved, err := NewSavedQueries(
		SavedQuery{Name: "My open work", JQL: "assignee = currentUser()", Slot: 1},
		SavedQuery{Name: "Recently updated", JQL: testJQL, Slot: 2},
	)
	if err != nil {
		t.Fatalf("saving: %v", err)
	}

	if got := saved.All(); len(got) != 2 || got[0].Name != "My open work" {
		t.Errorf("the saved queries are %+v", got)
	}
	if got, ok := saved.ByName("my open WORK"); !ok || got.Slot != 1 {
		t.Errorf("looking a query up by name gave %+v (%t); a name is not case sensitive", got, ok)
	}
	if got, ok := saved.BySlot(2); !ok || got.Name != "Recently updated" {
		t.Errorf("key 2 runs %+v (%t)", got, ok)
	}
	if _, ok := saved.BySlot(0); ok {
		t.Error("an unbound query was reachable by key zero")
	}
	if saved.Remove("My open work").Len() != 1 || saved.Len() != 2 {
		t.Error("removing a query changed the set it was removed from")
	}
}

func TestSavedQueries_ListsTheKeysThatActuallyRunSomething(t *testing.T) {
	t.Parallel()

	saved, err := NewSavedQueries(
		SavedQuery{Name: "Third", JQL: testJQL, Slot: 3},
		SavedQuery{Name: "Unbound", JQL: testJQL},
		SavedQuery{Name: "First", JQL: testJQL, Slot: 1},
	)
	if err != nil {
		t.Fatalf("saving: %v", err)
	}
	if got := saved.Slots(); !slices.Equal(got, []int{1, 3}) {
		t.Errorf("the bound keys are %v, want [1 3] in that order", got)
	}
	if got := (SavedQueries{}).Slots(); len(got) != 0 {
		t.Errorf("an empty set reports keys %v", got)
	}
}

func TestSavedQueries_RebindingAKeyTakesItFromWhicheverQueryHadIt(t *testing.T) {
	t.Parallel()

	saved, err := NewSavedQueries(
		SavedQuery{Name: "My open work", JQL: "assignee = currentUser()", Slot: 1},
		SavedQuery{Name: "Recently updated", JQL: testJQL, Slot: 1},
	)
	if err != nil {
		t.Fatalf("saving: %v", err)
	}

	got, ok := saved.BySlot(1)
	if !ok || got.Name != "Recently updated" {
		t.Errorf("key 1 runs %+v (%t), want the query that took it", got, ok)
	}
	previous, ok := saved.ByName("My open work")
	if !ok || previous.Slot != 0 {
		t.Errorf("the query that held key 1 is now %+v; it should still be there, unbound", previous)
	}
}

func TestSavedQueries_ReplacingAQueryKeepsItsPlaceInTheList(t *testing.T) {
	t.Parallel()

	saved, err := NewSavedQueries(
		SavedQuery{Name: "First", JQL: "project = A"},
		SavedQuery{Name: "Second", JQL: "project = B"},
	)
	if err != nil {
		t.Fatalf("saving: %v", err)
	}
	saved, err = saved.Add(SavedQuery{Name: "first", JQL: "project = C"})
	if err != nil {
		t.Fatalf("replacing: %v", err)
	}

	all := saved.All()
	if len(all) != 2 {
		t.Fatalf("the set holds %d queries, want 2: a query with a name already there replaces it", len(all))
	}
	if all[0].JQL != "project = C" || all[1].Name != "Second" {
		t.Errorf("the set reads %+v", all)
	}
}

func TestRunSaved_RunsTheQueryBehindTheNameAndSaysSoWhenThereIsNone(t *testing.T) {
	t.Parallel()

	saved, err := NewSavedQueries(SavedQuery{Name: "Everything", JQL: testJQL, Slot: 1})
	if err != nil {
		t.Fatalf("saving: %v", err)
	}
	f := jiratest.New(jiratest.WithProject("PROJ", jiratest.Scrum), jiratest.WithIssues(jiratest.Gen(5)))
	s := appquery.NewSearch(f)

	got, err := RunSaved(t.Context(), s, saved, "everything")
	if err != nil {
		t.Fatalf("running a saved query: %v", err)
	}
	if len(got.Page.Items) != 5 {
		t.Errorf("the saved query returned %d issues, want 5", len(got.Page.Items))
	}
	if got.Page.Items[0].Summary == "" {
		t.Error("a saved query with no field set of its own fetched nothing to render")
	}

	if _, err := RunSaved(t.Context(), s, saved, "nothing by this name"); err == nil {
		t.Error("running a saved query nobody saved succeeded")
	}
}

func TestSavedQuery_AFieldSetOfNothingButCustomFieldsIsNotTheListDefault(t *testing.T) {
	t.Parallel()

	q := SavedQuery{Name: "Estimates", JQL: testJQL, Projection: appquery.Projection{Name: "estimates", Custom: true}}
	got := q.projection()
	if !got.Custom {
		t.Error("a saved query asking only for the custom fields fell back to the list field set, which asks for none of them")
	}
	if len(got.IDs) != 0 {
		t.Errorf("the field set grew to %v", got.IDs)
	}

	empty := SavedQuery{Name: "Anything", JQL: testJQL}.projection()
	if len(empty.IDs) != len(appquery.ListProjection().IDs) || empty.Custom {
		t.Errorf("a saved query with no field set of its own resolved to %+v, want the list one", empty)
	}
}

func TestRunSaved_PassesTheSitesRefusalThroughAsItsOwnType(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		err  error
		is   func(error) bool
	}{
		{"a 403", &jira.CapabilityError{Capability: jira.CapPeople, Reason: "not for this token"}, func(err error) bool {
			var target *jira.CapabilityError
			return errors.As(err, &target)
		}},
		{"a 429", &jira.RateLimitError{RetryAfter: time.Second}, func(err error) bool {
			var target *jira.RateLimitError
			return errors.As(err, &target)
		}},
		{"a transport failure", &jira.TransportError{Op: "search", Status: 503}, func(err error) bool {
			var target *jira.TransportError
			return errors.As(err, &target)
		}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			saved, err := NewSavedQueries(SavedQuery{Name: "Everything", JQL: testJQL})
			if err != nil {
				t.Fatalf("saving: %v", err)
			}
			f := jiratest.New(jiratest.WithProject("PROJ", jiratest.Scrum), jiratest.WithIssues(jiratest.Gen(3)))
			f.FailNext(tt.err)
			if _, err := RunSaved(t.Context(), appquery.NewSearch(f), saved, "Everything"); !tt.is(err) {
				t.Errorf("running the saved query failed with %v, want the site's own %T", err, tt.err)
			}
		})
	}
}
