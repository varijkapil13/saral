package search

import "testing"

func TestApplySort_ReplacesTheOrderAndKeepsTheRest(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		jql    string
		choice SortChoice
		want   string
	}{
		{"no choice leaves the query alone", `project = "PROJ" ORDER BY updated DESC`, SortChoice{}, `project = "PROJ" ORDER BY updated DESC`},
		{"an unknown field leaves the query alone", "x = 1 ORDER BY key", SortChoice{Field: "customfield_1"}, "x = 1 ORDER BY key"},
		{"replaces an existing order", `project = "PROJ" ORDER BY updated DESC`, SortChoice{Field: "status"}, `project = "PROJ" ORDER BY status ASC`},
		{"matches order by in any case", "x = 1 order   by key asc", SortChoice{Field: "due", Desc: true}, "x = 1 ORDER BY duedate DESC"},
		{"adds an order to a query with none", "assignee IS EMPTY", SortChoice{Field: "type"}, "assignee IS EMPTY ORDER BY issuetype ASC"},
		{"a query that is only an order", "ORDER BY updated DESC", SortChoice{Field: "key"}, "ORDER BY updated DESC ORDER BY key ASC"},
		{"an empty query is only the order", "", SortChoice{Field: "key"}, "ORDER BY key ASC"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if got := ApplySort(tc.jql, tc.choice); got != tc.want {
				t.Errorf("ApplySort(%q, %+v) = %q, want %q", tc.jql, tc.choice, got, tc.want)
			}
		})
	}
}

func TestSortChoice_NextTogglesTheFieldInForceAndOpensAnotherInItsOwnDirection(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name  string
		from  SortChoice
		field string
		want  SortChoice
	}{
		{"a first choice of an alphabetic field", SortChoice{}, "status", SortChoice{Field: "status"}},
		{"a first choice of a date field", SortChoice{}, "updated", SortChoice{Field: "updated", Desc: true}},
		{"the same field again flips it", SortChoice{Field: "status"}, "status", SortChoice{Field: "status", Desc: true}},
		{"and back", SortChoice{Field: "status", Desc: true}, "status", SortChoice{Field: "status"}},
		{"another field opens in its own direction", SortChoice{Field: "created", Desc: false}, "key", SortChoice{Field: "key"}},
		{"a field this build does not know changes nothing", SortChoice{Field: "key"}, "nope", SortChoice{Field: "key"}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if got := tc.from.Next(tc.field); got != tc.want {
				t.Errorf("%+v.Next(%q) = %+v, want %+v", tc.from, tc.field, got, tc.want)
			}
		})
	}
}

func TestSortFields_NameOnlyWhatJQLItselfDefines(t *testing.T) {
	t.Parallel()

	seen := map[string]bool{}
	for i, f := range SortFields {
		if seen[f.ID] {
			t.Errorf("%s is offered twice", f.ID)
		}
		seen[f.ID] = true
		if SortFieldIndex(f.ID) != i {
			t.Errorf("SortFieldIndex(%q) = %d, want %d", f.ID, SortFieldIndex(f.ID), i)
		}
		if got, ok := SortFieldByID(f.ID); !ok || got != f {
			t.Errorf("SortFieldByID(%q) = %+v, %v", f.ID, got, ok)
		}
	}
	if SortFieldIndex("nope") != 0 {
		t.Error("an unknown field does not land the cursor on the first")
	}
	if (SortChoice{}).Chosen() || !(SortChoice{Field: "key"}).Chosen() {
		t.Error("Chosen disagrees with whether a field was chosen")
	}
}
