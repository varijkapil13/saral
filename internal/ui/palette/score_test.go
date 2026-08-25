package palette

import (
	"strings"
	"testing"
)

func TestMatch_FindsASubsequenceAndRefusesAnythingElse(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name  string
		query string
		text  string
		want  bool
	}{
		{name: "the whole word", query: "issues", text: "Issues", want: true},
		{name: "a prefix", query: "iss", text: "Issues", want: true},
		{name: "letters in order but not together", query: "crt", text: "Create an issue", want: true},
		{name: "case is not part of the question", query: "edit", text: "EDIT THIS ISSUE", want: true},
		{name: "the words a person types are often in the ID", query: "mine", text: "issues.mine", want: true},
		{name: "an empty filter matches everything", query: "", text: "Issues", want: true},
		{name: "letters that are not there", query: "zzz", text: "Issues", want: false},
		{name: "letters in the wrong order", query: "seussi", text: "Issues", want: false},
		{name: "more letters than the candidate has", query: "issuesissues", text: "Issues", want: false},
		{name: "nothing matches nothing", query: "iss", text: "", want: false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			_, got := newCandidate(tt.text).match([]rune(strings.ToLower(tt.query)))
			if got != tt.want {
				t.Errorf("match(%q, %q) = %t, want %t", tt.query, tt.text, got, tt.want)
			}
		})
	}
}

func TestMatch_PutsTheBetterCandidateFirst(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		query  string
		better string
		worse  string
	}{
		{
			name:   "the start of a word beats the middle of one",
			query:  "com",
			better: "Comments",
			worse:  "Uncomment the line",
		},
		{
			name:   "the shorter title wins when the match is otherwise the same",
			query:  "issues",
			better: "Issues",
			worse:  "Issues I reported last week",
		},
		{
			name:   "characters together beat characters spread out",
			query:  "cri",
			better: "Critical path",
			worse:  "Create an issue",
		},
		{
			name:   "a word beginning beats a run in the middle of a word",
			query:  "issue",
			better: "Fix the issue",
			worse:  "Reissued the token",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			query := []rune(strings.ToLower(tt.query))
			better, ok := newCandidate(tt.better).match(query)
			if !ok {
				t.Fatalf("%q does not match %q at all", tt.query, tt.better)
			}
			worse, ok := newCandidate(tt.worse).match(query)
			if !ok {
				return
			}
			if better <= worse {
				t.Errorf("%q scores %q at %d and %q at %d, so the worse one is offered first",
					tt.query, tt.better, better, tt.worse, worse)
			}
		})
	}
}

// The walk from the first place a query's opening character appears is not
// always its best alignment, so every starting point is tried.
func TestMatch_TakesTheBestAlignmentRatherThanTheFirstOne(t *testing.T) {
	t.Parallel()

	const text = "index of the issue"
	query := []rune("iss")
	c := newCandidate(text)

	first, ok := c.walk(query, strings.Index(text, "index"))
	if !ok {
		t.Fatal("the walk from the first i does not finish")
	}
	later, ok := c.walk(query, strings.Index(text, "issue"))
	if !ok {
		t.Fatal("the walk from the last i does not finish")
	}
	if later <= first {
		t.Fatalf("this candidate does not exercise the property: %d from the first i, %d from the last", first, later)
	}

	best, _ := c.match(query)
	if best <= first-len([]rune(text))/lengthDivisor {
		t.Errorf("match scored %d, which is the walk from the first i; the run at the end of the candidate is the better alignment",
			best)
	}
}
