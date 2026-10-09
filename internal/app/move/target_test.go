package move

import (
	"slices"
	"strings"
	"testing"

	"github.com/varijkapil13/saral/pkg/jira"
	"github.com/varijkapil13/saral/pkg/jira/jiratest"
)

func TestCandidates_OffersTheProjectsBehindRecentIssuesLessTheOnesBeingMovedFrom(t *testing.T) {
	t.Parallel()
	f := newFake(6, jiratest.WithIssues(jiratest.GenFor("OTHER", 4)))
	iss := seeded(t, f, "PROJ-1")

	got, err := Candidates(t.Context(), f, iss)
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Contains(got, "OTHER") {
		t.Errorf("the candidates are %v, want OTHER among them", got)
	}
	if slices.Contains(got, "PROJ") {
		t.Errorf("the project the issues are already in was offered: %v", got)
	}
}

func TestCandidates_IsEmptyAndNotAnErrorOnASiteWithNothingToOffer(t *testing.T) {
	t.Parallel()
	f := jiratest.New(jiratest.WithProject("PROJ", jiratest.Scrum))
	got, err := Candidates(t.Context(), f, nil)
	if err != nil || len(got) != 0 {
		t.Errorf("Candidates = %v, %v; want nothing and no error", got, err)
	}
	if n := countCalls(f, "Search"); n < 2 {
		t.Errorf("an empty answer for the account's own work was not followed by a wider one (%d searches)", n)
	}
}

func TestDistinctProjects_KeepsTheOrderTheyCameIn(t *testing.T) {
	t.Parallel()
	issues := []jira.Issue{
		{Project: jira.ProjectRef{Key: "B"}}, {Project: jira.ProjectRef{Key: "A"}},
		{Project: jira.ProjectRef{Key: "B"}}, {},
	}
	if got := distinctProjects(issues); !slices.Equal(got, []string{"B", "A"}) {
		t.Errorf("distinctProjects = %v", got)
	}
}

func TestTheReadsAndTheSubmit_PassAPortFailureThroughUnwrapped(t *testing.T) {
	t.Parallel()
	calls := map[string]func(t *testing.T, f *jiratest.Fake) error{
		"Candidates": func(t *testing.T, f *jiratest.Fake) error {
			_, err := Candidates(t.Context(), f, nil)
			return err
		},
		"Vocabulary": func(t *testing.T, f *jiratest.Fake) error {
			_, err := Vocabulary(t.Context(), f, "OTHER")
			return err
		},
		"Schema": func(t *testing.T, f *jiratest.Fake) error {
			_, err := Schema(t.Context(), f, "OTHER", "10001")
			return err
		},
		"Submit": func(t *testing.T, f *jiratest.Fake) error {
			_, err := Submit(t.Context(), f, jira.MoveRequest{Keys: []string{"PROJ-1"}, TargetProjectKey: "OTHER", TargetIssueTypeID: "10001"})
			return err
		},
	}
	for call, run := range calls {
		for name, want := range failures() {
			t.Run(call+"/"+name, func(t *testing.T) {
				t.Parallel()
				f := newFake(2)
				f.FailNext(want)
				mustBe(t, run(t, f), want)
			})
		}
	}
}

func TestVocabularySchemaAndSubmit_AnswerWhatTheSiteSays(t *testing.T) {
	t.Parallel()
	f := newFake(3, jiratest.WithIssues(jiratest.GenFor("OTHER", 1)))
	types, err := Vocabulary(t.Context(), f, "OTHER")
	if err != nil || len(types) == 0 {
		t.Fatalf("Vocabulary = %v, %v", types, err)
	}
	typ := types[0].Type
	if _, err := Schema(t.Context(), f, "OTHER", typ.ID); err != nil {
		t.Fatalf("Schema: %v", err)
	}
	ref, err := Submit(t.Context(), f, jira.MoveRequest{Keys: []string{"PROJ-1"}, TargetProjectKey: "OTHER", TargetIssueTypeID: typ.ID})
	if err != nil {
		t.Fatalf("Submit: %v", err)
	}
	if ref.ID == "" || !strings.Contains(ref.URL, "/bulk/queue/") {
		t.Errorf("the submit answered %+v; a bulk move is followed on its own queue", ref)
	}
}
