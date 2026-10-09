package connect

import (
	"errors"
	"testing"

	appquery "github.com/varijkapil13/saral/internal/app/query"
	"github.com/varijkapil13/saral/pkg/jira"
	"github.com/varijkapil13/saral/pkg/jira/jiratest"
)

func TestPinnableFields_KeepsFieldsWithAnIDSortedByTheirName(t *testing.T) {
	t.Parallel()

	f := jiratest.New(jiratest.WithFields([]jira.Field{
		{ID: "customfield_1", Name: "Charlie"},
		{ID: "", Name: "No id"},
		{ID: "customfield_2", Name: "  "},
		{ID: "summary", Name: "Alpha"},
	}))
	got, err := PinnableFields(t.Context(), appquery.NewSearch(f))
	if err != nil {
		t.Fatalf("reading pinnable fields: %v", err)
	}
	want := []PinnableField{
		{ID: "summary", Label: "Alpha"},
		{ID: "customfield_1", Label: "Charlie"},
		{ID: "customfield_2", Label: "customfield_2"},
	}
	if len(got) != len(want) {
		t.Fatalf("fields are %+v, want %+v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("field %d is %+v, want %+v", i, got[i], want[i])
		}
	}
}

func TestPinnableFields_PassesTheSitesRefusalThroughAsItsOwnType(t *testing.T) {
	t.Parallel()

	for _, fail := range siteFailures() {
		f := jiratest.New()
		f.FailNext(fail)
		if _, err := PinnableFields(t.Context(), appquery.NewSearch(f)); !errors.Is(err, fail) {
			t.Errorf("reading fields failed with %v, want the site's own %T", err, fail)
		}
	}
}
