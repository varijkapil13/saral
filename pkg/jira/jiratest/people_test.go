package jiratest_test

import (
	"errors"
	"maps"
	"slices"
	"testing"

	"github.com/varijkapil13/saral/pkg/jira"
	"github.com/varijkapil13/saral/pkg/jira/jiratest"
)

func peopleNamesOf(users []jira.User) []string {
	out := make([]string, 0, len(users))
	for _, u := range users {
		out = append(out, u.DisplayName)
	}
	return out
}

func peopleFound(t *testing.T, f *jiratest.Fake, q jira.PeopleQuery) []jira.User {
	t.Helper()

	got, err := f.FindPeople(t.Context(), q)
	if err != nil {
		t.Fatalf("FindPeople(%+v): %v", q, err)
	}
	return got
}

func TestFindPeople_MatchesAWordPrefixInitialsOrAnEmailAddress(t *testing.T) {
	t.Parallel()

	f := jiratest.New()
	tests := map[string]struct {
		match string
		want  []string
	}{
		"a prefix of the first word":  {match: "ada", want: []string{"Ada Lovelace"}},
		"a prefix of the last word":   {match: "hopp", want: []string{"Grace Hopper"}},
		"the initials":                {match: "gh", want: []string{"Grace Hopper"}},
		"part of the email address":   {match: "sam@", want: []string{"Sam Tester"}},
		"a needle inside a word":      {match: "ovelace", want: nil},
		"a needle nobody answers to":  {match: "zzz", want: nil},
		"nothing typed matches all":   {match: "", want: nil},
		"case is folded on both ends": {match: "ADA", want: []string{"Ada Lovelace"}},
	}

	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			got := peopleNamesOf(peopleFound(t, f, jira.PeopleQuery{Match: tt.match}))
			if tt.match == "" {
				if len(got) < 2 {
					t.Fatalf("an empty needle matched %v, want every account", got)
				}
				return
			}
			if !slices.Equal(got, tt.want) {
				t.Errorf("got %v, want %v", got, tt.want)
			}
		})
	}
}

// Jira's own order is not a ranking, so the fake's is deliberately not one
// either: a picker that presents whatever arrived as its own ranking has to look
// wrong somewhere, and this is where.
func TestFindPeople_AnswersInAnOrderThatIsNotRelevance(t *testing.T) {
	t.Parallel()

	f := jiratest.New()
	got := peopleFound(t, f, jira.PeopleQuery{Match: "e"})
	if len(got) < 2 {
		t.Fatalf("got %v, want more than one account to order", peopleNamesOf(got))
	}

	ids := make([]string, 0, len(got))
	for _, u := range got {
		ids = append(ids, u.AccountID)
	}
	if !slices.IsSorted(ids) {
		t.Errorf("the answer came back in %v, want the account-id order the fake promises", ids)
	}
}

func TestFindPeople_ScopedToAProjectOffersOnlyAccountsThatCanBeGivenWork(t *testing.T) {
	t.Parallel()

	f := jiratest.New(jiratest.WithProject("PROJ", jiratest.Kanban))

	site := peopleFound(t, f, jira.PeopleQuery{})
	if !slices.ContainsFunc(site, func(u jira.User) bool { return u.Kind != jira.AccountPerson }) {
		t.Fatal("the site-wide search hides the accounts that are not people, and it must not")
	}

	scoped := peopleFound(t, f, jira.PeopleQuery{Project: "PROJ"})
	if len(scoped) == 0 {
		t.Fatal("the project came back with nobody to assign work to")
	}
	for _, u := range scoped {
		if u.Kind != jira.AccountPerson {
			t.Errorf("%s (%v) is assignable in PROJ, and the assignable endpoint answers only people", u.DisplayName, u.Kind)
		}
	}
}

func TestFindPeople_RefusesAProjectTheSiteDoesNotHave(t *testing.T) {
	t.Parallel()

	f := jiratest.New(jiratest.WithProject("PROJ", jiratest.Kanban))
	got, err := f.FindPeople(t.Context(), jira.PeopleQuery{Project: "NOSUCH"})
	var missing *jira.NotFoundError
	if !errors.As(err, &missing) {
		t.Fatalf("got %+v, %T (%v); want a *jira.NotFoundError", got, err, err)
	}
}

func TestFindPeople_HonoursTheCeilingAndTreatsZeroAsNoCeiling(t *testing.T) {
	t.Parallel()

	f := jiratest.New()
	if got := peopleFound(t, f, jira.PeopleQuery{Limit: 2}); len(got) != 2 {
		t.Errorf("got %d accounts for a Limit of 2", len(got))
	}
	if got := peopleFound(t, f, jira.PeopleQuery{Limit: -1}); len(got) < 2 {
		t.Errorf("a negative Limit came back with %v, want the default rather than nothing", peopleNamesOf(got))
	}
}

func TestFindPeople_SearchesTheDirectoryItWasGiven(t *testing.T) {
	t.Parallel()

	only := []jira.User{{AccountID: "acct-one", DisplayName: "Only Person", Active: true, Kind: jira.AccountPerson}}
	f := jiratest.New(jiratest.WithPeople(only))

	got := peopleNamesOf(peopleFound(t, f, jira.PeopleQuery{}))
	if !slices.Equal(got, []string{"Only Person"}) {
		t.Errorf("got %v, want only the directory the test set", got)
	}
}

func TestPeople_LeavesOutAnIdTheSiteDoesNotKnow(t *testing.T) {
	t.Parallel()

	f := jiratest.New()
	got, err := f.People(t.Context(), []string{"acct-ada", "acct-gone", "acct-ada", " acct-grace "})
	if err != nil {
		t.Fatalf("People: %v", err)
	}
	if names := peopleNamesOf(got); !slices.Equal(names, []string{"Ada Lovelace", "Grace Hopper"}) {
		t.Fatalf("got %v, want the two ids the site knows, each once", names)
	}
	for _, u := range got {
		if u.AccountID == "" || u.DisplayName == "" {
			t.Errorf("a blank row reached the caller: %+v", u)
		}
	}
}

func TestPeople_AsksNothingWhenThereIsNothingToAsk(t *testing.T) {
	t.Parallel()

	f := jiratest.New()
	got, err := f.People(t.Context(), nil)
	if err != nil || len(got) != 0 {
		t.Fatalf("got %+v, %v; want nothing and no error", got, err)
	}
	// A real bulk read with no accountId is a 400, so the call is not made at
	// all rather than made and forgiven.
	if calls := f.Calls(); len(calls) != 0 {
		t.Errorf("the fake recorded %v for a request nobody could have sent", calls)
	}
}

func TestPeopleAndFindPeople_RefuseWhenTheTokenMayNotBrowseUsers(t *testing.T) {
	t.Parallel()

	f := jiratest.New(jiratest.WithCapabilities(jiratest.NoPeople))

	for name, call := range map[string]func() ([]jira.User, error){
		"a search":    func() ([]jira.User, error) { return f.FindPeople(t.Context(), jira.PeopleQuery{}) },
		"a bulk read": func() ([]jira.User, error) { return f.People(t.Context(), []string{"acct-ada"}) },
	} {
		got, err := call()
		var refused *jira.CapabilityError
		if !errors.As(err, &refused) {
			t.Fatalf("%s gave %+v, %T (%v); want a *jira.CapabilityError", name, got, err, err)
		}
		if refused.Capability != jira.CapPeople {
			t.Errorf("%s named the capability %q, want %q", name, refused.Capability, jira.CapPeople)
		}
	}
}

func TestIssueTypeStatuses_GivesTheSubtaskTypeItsOwnWorkflow(t *testing.T) {
	t.Parallel()

	f := jiratest.New(jiratest.WithProject("PROJ", jiratest.Scrum))
	got, err := f.IssueTypeStatuses(t.Context(), "PROJ")
	if err != nil {
		t.Fatalf("IssueTypeStatuses: %v", err)
	}
	if len(got) == 0 {
		t.Fatal("PROJ came back with no issue types")
	}

	ids := make(map[string]map[string]bool)
	subtasks := 0
	for _, entry := range got {
		if entry.Type.Subtask {
			subtasks++
		}
		for _, status := range entry.Statuses {
			if ids[status.Name] == nil {
				ids[status.Name] = make(map[string]bool)
			}
			ids[status.Name][status.ID] = true
		}
	}
	if subtasks == 0 {
		t.Error("no subtask type came back, and the answer covers every type in the project")
	}
	// Two ids under one name is what a team-managed project mints, and it is the
	// whole reason this answer carries ids.
	if len(ids["Building"]) != 2 {
		t.Errorf(`"Building" resolved to %v, want the two distinct ids one site can hold under one name`, ids["Building"])
	}
}

func TestIssueTypeStatuses_RefusesAMissingOrUnknownProject(t *testing.T) {
	t.Parallel()

	f := jiratest.New(jiratest.WithProject("PROJ", jiratest.Kanban))

	got, err := f.IssueTypeStatuses(t.Context(), "  ")
	var invalid *jira.ValidationError
	if !errors.As(err, &invalid) {
		t.Fatalf("got %+v, %T (%v); want a *jira.ValidationError", got, err, err)
	}
	if _, named := invalid.For("projectKey"); !named {
		t.Errorf("the refusal does not name projectKey: %v", invalid)
	}

	got, err = f.IssueTypeStatuses(t.Context(), "NOSUCH")
	var missing *jira.NotFoundError
	if !errors.As(err, &missing) {
		t.Fatalf("got %+v, %T (%v); want a *jira.NotFoundError", got, err, err)
	}
}

func TestIssueTypeStatuses_HandsBackACopyOfEachWorkflow(t *testing.T) {
	t.Parallel()

	f := jiratest.New(jiratest.WithProject("PROJ", jiratest.Kanban))
	first, err := f.IssueTypeStatuses(t.Context(), "PROJ")
	if err != nil {
		t.Fatalf("IssueTypeStatuses: %v", err)
	}
	first[0].Statuses[0] = jira.Status{ID: "written-through", Name: "written through"}

	second, err := f.IssueTypeStatuses(t.Context(), "PROJ")
	if err != nil {
		t.Fatalf("IssueTypeStatuses: %v", err)
	}
	if second[0].Statuses[0].ID == "written-through" {
		t.Error("a caller writing into what it was handed changed the fake site")
	}
}

func TestPriorities_KeepRankingOrderRatherThanTheAlphabet(t *testing.T) {
	t.Parallel()

	f := jiratest.New()
	got, err := f.Priorities(t.Context())
	if err != nil {
		t.Fatalf("Priorities: %v", err)
	}
	names := make([]string, 0, len(got))
	for _, p := range got {
		if p.ID == "" {
			t.Errorf("the priority %q has no id", p.Name)
		}
		names = append(names, p.Name)
	}
	if slices.IsSorted(names) {
		t.Errorf("got %v, which is the alphabet; a priority list is in ranking order", names)
	}
}

func TestLabels_PageEveryLabelTheSiteHasIncludingTheIssuesOwn(t *testing.T) {
	t.Parallel()

	f := fakeNewWithIssues(t, 40, jiratest.WithPageSize(4))
	page, err := f.Labels(t.Context())
	if err != nil {
		t.Fatalf("Labels: %v", err)
	}
	if !page.HasMore() {
		t.Error("the whole site fitted on one page, so nothing here exercises the walk")
	}
	got, err := jira.Collect(t.Context(), page, 0)
	if err != nil {
		t.Fatalf("walking the labels: %v", err)
	}

	if !slices.IsSorted(got) {
		t.Errorf("got %q, want the sorted order the endpoint answers in", got)
	}
	if len(slices.Compact(slices.Clone(got))) != len(got) {
		t.Errorf("got %q, which repeats a label", got)
	}
	// A label is whatever anybody typed, so a column measured with len() over one
	// is wrong. The site carries two that prove it.
	for _, want := range []string{"prüfung", "検索"} {
		if !slices.Contains(got, want) {
			t.Errorf("%q is not in %q", want, got)
		}
	}
	if total, ok := page.Count(); !ok || total != len(got) {
		t.Errorf("the first page claimed %d labels (stated %v) and the walk found %d", total, ok, len(got))
	}
}

func TestWithPeople_SurvivesAReset(t *testing.T) {
	t.Parallel()

	f := jiratest.New(jiratest.WithPeople(nil))
	if got := peopleFound(t, f, jira.PeopleQuery{}); len(got) != 0 {
		t.Fatalf("got %v, want the empty directory the option set", peopleNamesOf(got))
	}
	f.Reset()
	if got := peopleFound(t, f, jira.PeopleQuery{}); len(got) != 0 {
		t.Errorf("Reset did not re-apply WithPeople: %v", peopleNamesOf(got))
	}
}

// A site states what kind of account it is answering on the assignee and the
// reporter of an issue, exactly as it does in a picker. An issue seed that left
// the kind out would badge an app account in the picker and not on the issue it
// is assigned to, which reads as the account changing kind between two screens.
func TestSearch_NamesTheKindOfEveryAccountTheDirectoryAlsoKnows(t *testing.T) {
	t.Parallel()

	f := jiratest.New(jiratest.WithIssues(jiratest.Gen(30)))
	page, err := f.Search(t.Context(), jira.Query{JQL: "project = PROJ", Fields: []string{"assignee", "reporter"}})
	if err != nil {
		t.Fatalf("searching: %v", err)
	}
	issues, err := jira.Collect(t.Context(), page, 0)
	if err != nil {
		t.Fatalf("walking the search: %v", err)
	}

	onIssues := make(map[string]jira.AccountKind)
	for _, iss := range issues {
		for _, u := range []*jira.User{iss.Assignee, iss.Reporter} {
			if u == nil {
				continue
			}
			if u.Kind == jira.AccountUnknown {
				t.Errorf("%s names %s (%s) with no kind at all", iss.Key, u.DisplayName, u.AccountID)
			}
			onIssues[u.AccountID] = u.Kind
		}
	}
	if len(onIssues) == 0 {
		t.Fatal("no issue named an account, so nothing here was checked")
	}

	ids := slices.Sorted(maps.Keys(onIssues))
	directory, err := f.People(t.Context(), ids)
	if err != nil {
		t.Fatalf("resolving the accounts the issues name: %v", err)
	}
	if len(directory) != len(ids) {
		t.Errorf("the directory knows %v of the %v the issues name", peopleIDsOf(directory), ids)
	}
	for _, u := range directory {
		if want := onIssues[u.AccountID]; u.Kind != want {
			t.Errorf("%s is %v in the directory and %v on an issue", u.AccountID, u.Kind, want)
		}
	}
}

func TestUpdateIssue_AssignsAnAccountAsTheDirectoryDescribesIt(t *testing.T) {
	t.Parallel()

	f := jiratest.New(jiratest.WithIssues(jiratest.Gen(3)))
	app, ok := peopleOneOfKind(peopleFound(t, f, jira.PeopleQuery{}), jira.AccountApp)
	if !ok {
		t.Fatal("the directory holds no app account, and a real site's is mostly app accounts")
	}

	if err := f.UpdateIssue(t.Context(), "PROJ-1", jira.IssuePatch{Assignee: &app.AccountID}); err != nil {
		t.Fatalf("assigning %s: %v", app.AccountID, err)
	}
	got, err := f.Issue(t.Context(), "PROJ-1")
	if err != nil {
		t.Fatalf("reading the issue back: %v", err)
	}

	if got.Assignee == nil {
		t.Fatal("the issue came back unassigned")
	}
	if got.Assignee.Kind != jira.AccountApp || got.Assignee.DisplayName != app.DisplayName {
		t.Errorf("the assignee reads as %+v, want the account the directory describes: %+v", got.Assignee, app)
	}
}

func peopleIDsOf(users []jira.User) []string {
	out := make([]string, 0, len(users))
	for _, u := range users {
		out = append(out, u.AccountID)
	}
	slices.Sort(out)
	return out
}

func peopleOneOfKind(users []jira.User, kind jira.AccountKind) (jira.User, bool) {
	for _, u := range users {
		if u.Kind == kind {
			return u, true
		}
	}
	return jira.User{}, false
}
