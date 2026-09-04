package cloud

import (
	"testing"

	"github.com/varijkapil13/saral/pkg/jira"
	"github.com/varijkapil13/saral/pkg/jira/jiratest"
)

// One property of EditMeta, run against both adapters: what it names is on
// the issue's screen right now, in the order the site sent it. The fixture
// site and the fake are given different screens on purpose — the case is a
// property of the shape, not a value the two sites are made to agree on.
func TestEditMeta_BothAdaptersOrderTheScreenTheSiteSent(t *testing.T) {
	t.Parallel()

	t.Run("cloud", func(t *testing.T) {
		t.Parallel()

		s := jiratest.NewServer()
		defer s.Close()
		c, _ := testClient(t, s.URL())

		got, err := c.EditMeta(t.Context(), "EX-1")
		if err != nil {
			t.Fatalf("reading the edit screen: %v", err)
		}
		assertScreenOrder(t, got, "customfield_10032", "summary", "labels")
	})

	t.Run("fake", func(t *testing.T) {
		t.Parallel()

		issues := jiratest.GenFor(conformProject, 1)
		key := issues[0].Key
		f := conformFake(t, jiratest.WithIssues(issues))
		f.SetEditMeta(key,
			jira.FieldMeta{Field: jira.FieldRef{ID: "labels"}},
			jira.FieldMeta{Field: jira.FieldRef{ID: "summary"}},
		)

		got, err := f.EditMeta(t.Context(), key)
		if err != nil {
			t.Fatalf("reading the edit screen: %v", err)
		}
		assertScreenOrder(t, got, "labels", "summary")
	})
}

func assertScreenOrder(t *testing.T, got jira.EditMeta, want ...string) {
	t.Helper()
	if len(got.Fields) != len(want) {
		t.Fatalf("got %d fields %v, want %d in this order: %v", len(got.Fields), fieldMetaIDs(got.Fields), len(want), want)
	}
	for i, id := range want {
		if got.Fields[i].Field.ID != id {
			t.Errorf("field %d is %q, want %q in this order: %v", i, got.Fields[i].Field.ID, id, fieldMetaIDs(got.Fields))
		}
	}
}
