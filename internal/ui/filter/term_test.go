package filter

import (
	"testing"
)

func TestTerms_ComposeTheClauseTheSiteIsAsked(t *testing.T) {
	t.Parallel()

	ada := Term{Facet: FacetAssignee, ID: "acct-ada", Label: "Ada Lovelace"}
	grace := Term{Facet: FacetAssignee, ID: "acct-grace", Label: "Grace Hopper"}
	nobody := Term{Facet: FacetAssignee, Label: "unassigned"}
	shipped := Term{Facet: FacetStatus, ID: "10203", Label: "Shipped"}
	chore := Term{Facet: FacetType, ID: "10303", Label: "Chore"}

	for name, tc := range map[string]struct {
		terms  Terms
		clause string
		words  string
	}{
		"nothing at all": {},
		"one person": {
			terms: Terms{ada}, clause: `assignee = "acct-ada"`, words: "assignee Ada Lovelace",
		},
		"two people are either of them": {
			terms:  Terms{ada, grace},
			clause: `assignee IN ("acct-ada", "acct-grace")`,
			words:  "assignee Ada Lovelace or Grace Hopper",
		},
		"nobody at all": {
			terms: Terms{nobody}, clause: "assignee IS EMPTY", words: "assignee unassigned",
		},
		"somebody or nobody": {
			terms:  Terms{ada, nobody},
			clause: `(assignee = "acct-ada" OR assignee IS EMPTY)`,
			words:  "assignee Ada Lovelace or unassigned",
		},
		"two people or nobody": {
			terms:  Terms{ada, grace, nobody},
			clause: `(assignee IN ("acct-ada", "acct-grace") OR assignee IS EMPTY)`,
			words:  "assignee Ada Lovelace or Grace Hopper or unassigned",
		},
		"two facets narrow together": {
			terms:  Terms{ada, shipped},
			clause: `assignee = "acct-ada" AND status = "10203"`,
			words:  "assignee Ada Lovelace and status Shipped",
		},
		"a type is issuetype in JQL": {
			terms: Terms{chore}, clause: `issuetype = "10303"`, words: "type Chore",
		},
		"a label is its own id": {
			terms:  Terms{{Facet: FacetLabel, ID: "tech-debt", Label: "tech-debt"}},
			clause: `labels = "tech-debt"`, words: "label tech-debt",
		},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			if got := tc.terms.Clause(); got != tc.clause {
				t.Errorf("clause is %q, want %q", got, tc.clause)
			}
			if got := tc.terms.Words(); got != tc.words {
				t.Errorf("words are %q, want %q", got, tc.words)
			}
		})
	}
}

// Three of the eleven account ids on the measured site carry a colon, and a
// label is whatever anybody typed. Both go into a clause the site parses, so
// both have to survive being quoted.
func TestTerms_QuoteWhatCannotBeWrittenBare(t *testing.T) {
	t.Parallel()

	for name, tc := range map[string]struct {
		term Term
		want string
	}{
		"an account id with a colon": {
			term: Term{Facet: FacetAssignee, ID: "5f2a:ee0c-92b1", Label: "Nightly Runner"},
			want: `assignee = "5f2a:ee0c-92b1"`,
		},
		"a label with a quote in it": {
			term: Term{Facet: FacetLabel, ID: `we"ird`, Label: `we"ird`},
			want: `labels = "we\"ird"`,
		},
		"a label with a backslash in it": {
			term: Term{Facet: FacetLabel, ID: `back\slash`, Label: `back\slash`},
			want: `labels = "back\\slash"`,
		},
		"a label that is not ASCII": {
			term: Term{Facet: FacetLabel, ID: "検索", Label: "検索"},
			want: `labels = "検索"`,
		},
		"a label with a space in it": {
			term: Term{Facet: FacetLabel, ID: "two words", Label: "two words"},
			want: `labels = "two words"`,
		},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			if got := (Terms{tc.term}).Clause(); got != tc.want {
				t.Errorf("clause is %q, want %q", got, tc.want)
			}
		})
	}
}

// The clause is written in the facets' own order rather than the order they
// were chosen in, so two ways of arriving at one filter ask the site one
// question — and store their rows under one cache key.
func TestTerms_TheClauseDoesNotDependOnTheOrderTheyWereChosenIn(t *testing.T) {
	t.Parallel()

	ada := Term{Facet: FacetAssignee, ID: "acct-ada", Label: "Ada Lovelace"}
	shipped := Term{Facet: FacetStatus, ID: "10203", Label: "Shipped"}
	urgent := Term{Facet: FacetPriority, ID: "10401", Label: "Urgent"}

	first := Terms{ada, shipped, urgent}.Clause()
	second := Terms{urgent, shipped, ada}.Clause()
	if first != second {
		t.Errorf("two orders of the same three terms ask two questions:\n%q\n%q", first, second)
	}
}

func TestTerms_ToggleAddsAValueAndTakesItOffAgain(t *testing.T) {
	t.Parallel()

	ada := Term{Facet: FacetAssignee, ID: "acct-ada", Label: "Ada Lovelace"}
	// The same account under the other name an endpoint gave it. An id is the
	// identity, so this must come off rather than be added a second time.
	adaAgain := Term{Facet: FacetAssignee, ID: "acct-ada", Label: "A. Lovelace"}

	on := Terms(nil).Toggle(ada)
	if len(on) != 1 || !on.Has(ada) {
		t.Fatalf("toggling a value on left %+v", on)
	}
	if off := on.Toggle(adaAgain); len(off) != 0 {
		t.Errorf("toggling the same account under another name left %+v", off)
	}
}

// Toggle answers with a new slice, so a picker holding the old one never sees
// the list's copy move under it.
func TestTerms_ToggleDoesNotWriteThroughToWhatItWasGiven(t *testing.T) {
	t.Parallel()

	held := Terms{{Facet: FacetStatus, ID: "10201", Label: "Triage"}}
	next := held.Toggle(Term{Facet: FacetStatus, ID: "10203", Label: "Shipped"})

	if len(held) != 1 {
		t.Fatalf("the slice it was given now holds %d terms", len(held))
	}
	if len(next) != 2 {
		t.Fatalf("the answer holds %d terms, want 2", len(next))
	}
	next[0] = Term{Facet: FacetLabel, ID: "elsewhere"}
	if held[0].ID != "10201" {
		t.Errorf("writing to the answer changed what it was given: %+v", held[0])
	}
}

func TestTerms_CountIsPerFacet(t *testing.T) {
	t.Parallel()

	held := Terms{
		{Facet: FacetAssignee, ID: "acct-ada"},
		{Facet: FacetAssignee, ID: "acct-grace"},
		{Facet: FacetStatus, ID: "10201"},
	}
	if got := held.Count(FacetAssignee); got != 2 {
		t.Errorf("two people count as %d", got)
	}
	if got := held.Count(FacetStatus); got != 1 {
		t.Errorf("one status counts as %d", got)
	}
	if got := held.Count(FacetLabel); got != 0 {
		t.Errorf("no labels count as %d", got)
	}
}

// Every facet the picker offers has to write a JQL field and a word for the
// screen, or it is a row that cannot compose a query.
func TestFacets_AllNameAFieldAndAWord(t *testing.T) {
	t.Parallel()

	for _, f := range Facets {
		if f.field() == "" {
			t.Errorf("facet %d writes no JQL field", f)
		}
		if f.Label() == "" {
			t.Errorf("facet %d has no word for the screen", f)
		}
	}
	if FacetNone.field() != "" || FacetNone.Label() != "" {
		t.Error("the empty facet names a field or a word, so it could compose a clause")
	}
}
