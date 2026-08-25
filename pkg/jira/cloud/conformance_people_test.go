package cloud

import (
	"errors"
	"net/http"
	"slices"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/varijkapil13/saral/pkg/jira"
	"github.com/varijkapil13/saral/pkg/jira/jiratest"
)

// One set of assertions, run against both adapters, for the five methods a
// filter picker calls. Everything above the port is tested against the fake, so
// a rule only the cloud adapter holds is a rule no test meets — the suite stays
// green and the binary fails against a site.
//
// The cases are properties, not answers. The two adapters describe different
// sites on purpose and cannot agree on who is in them; what they must agree on
// is that an unknown id is absent rather than blank, that a ceiling is a
// ceiling, that a refusal names CapPeople, and that a status is identified by
// its id.

type (
	peopleBuilder func(*testing.T) jira.PeopleFinder
	vocabBuilder  func(*testing.T) jira.FilterVocabulary
)

func peopleFromSite(t *testing.T, opts ...jiratest.ServerOption) jira.PeopleFinder {
	t.Helper()

	s := jiratest.NewServer(opts...)
	t.Cleanup(s.Close)
	c, _ := testClient(t, s.URL())
	return c
}

func vocabFromSite(t *testing.T, opts ...jiratest.ServerOption) jira.FilterVocabulary {
	t.Helper()

	s := jiratest.NewServer(opts...)
	t.Cleanup(s.Close)
	c, _ := testClient(t, s.URL())
	return c
}

// conformProject is a project both adapters have: EX is the fixture site's, and
// the fake is given one under the same key.
const conformProject = "EX"

func conformFake(t *testing.T, opts ...jiratest.Option) *jiratest.Fake {
	t.Helper()
	return jiratest.New(append([]jiratest.Option{jiratest.WithProject(conformProject, jiratest.Scrum)}, opts...)...)
}

func TestFindPeople_BothAdaptersAnswerTheSameWay(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name   string
		cloud  peopleBuilder
		fake   peopleBuilder
		query  jira.PeopleQuery
		assert func(*testing.T, []jira.User, error)
	}{
		{
			name:  "an empty needle asks for everybody rather than nobody",
			cloud: func(t *testing.T) jira.PeopleFinder { return peopleFromSite(t) },
			fake:  func(t *testing.T) jira.PeopleFinder { return conformFake(t) },
			query: jira.PeopleQuery{},
			assert: func(t *testing.T, got []jira.User, err error) {
				t.Helper()
				if err != nil {
					t.Fatalf("searching with no needle: %v", err)
				}
				if len(got) == 0 {
					t.Fatal("an empty needle came back with nobody, and it is the state a picker opens in")
				}
				conformNamesEverybody(t, got)
			},
		},
		{
			name:  "every account says what kind of account it is",
			cloud: func(t *testing.T) jira.PeopleFinder { return peopleFromSite(t) },
			fake:  func(t *testing.T) jira.PeopleFinder { return conformFake(t) },
			query: jira.PeopleQuery{},
			assert: func(t *testing.T, got []jira.User, err error) {
				t.Helper()
				if err != nil {
					t.Fatalf("searching with no needle: %v", err)
				}
				for _, u := range got {
					if u.Kind == jira.AccountUnknown {
						t.Errorf("%s (%s) came back with no kind, and a people read states one", u.DisplayName, u.AccountID)
					}
				}
				// A site that is mostly robots is unreadable without the
				// distinction, and one that hides them loses rows, so both
				// adapters have to hold more than one kind.
				kinds := make(map[jira.AccountKind]bool, len(got))
				for _, u := range got {
					kinds[u.Kind] = true
				}
				if len(kinds) < 2 {
					t.Errorf("every account is a %v; the site is supposed to hold more than one kind", got[0].Kind)
				}
			},
		},
		{
			name:  "a ceiling the caller named is a ceiling",
			cloud: func(t *testing.T) jira.PeopleFinder { return peopleFromSite(t) },
			fake:  func(t *testing.T) jira.PeopleFinder { return conformFake(t) },
			query: jira.PeopleQuery{Limit: 2},
			assert: func(t *testing.T, got []jira.User, err error) {
				t.Helper()
				if err != nil {
					t.Fatalf("searching with a ceiling: %v", err)
				}
				if len(got) > 2 {
					t.Errorf("got %d accounts for a Limit of 2; a picker sized on Limit has nowhere to draw them", len(got))
				}
				if len(got) == 0 {
					t.Error("a ceiling of two came back with nobody")
				}
			},
		},
		{
			name: "a project the site does not have",
			cloud: func(t *testing.T) jira.PeopleFinder {
				return peopleFromSite(t, jiratest.WithStatus(
					http.MethodGet, peopleAssignablePath, http.StatusNotFound, "not_found_board.json"))
			},
			fake:  func(t *testing.T) jira.PeopleFinder { return conformFake(t) },
			query: jira.PeopleQuery{Project: "NOSUCH"},
			assert: func(t *testing.T, got []jira.User, err error) {
				t.Helper()
				var missing *jira.NotFoundError
				if !errors.As(err, &missing) {
					t.Fatalf("got %+v, %T (%v); want a *jira.NotFoundError", got, err, err)
				}
			},
		},
		{
			name: "a token that may not browse users",
			cloud: func(t *testing.T) jira.PeopleFinder {
				return peopleFromSite(t, jiratest.WithStatus(
					http.MethodGet, peopleSearchPath, http.StatusForbidden, "forbidden_browse_users.json"))
			},
			fake: func(t *testing.T) jira.PeopleFinder {
				return conformFake(t, jiratest.WithCapabilities(jiratest.NoPeople))
			},
			query:  jira.PeopleQuery{},
			assert: conformRefusedPeople,
		},
	}

	for _, tt := range cases {
		for _, adapter := range []struct {
			name string
			open peopleBuilder
		}{
			{name: "cloud", open: tt.cloud},
			{name: "fake", open: tt.fake},
		} {
			t.Run(tt.name+"/"+adapter.name, func(t *testing.T) {
				t.Parallel()

				got, err := adapter.open(t).FindPeople(t.Context(), tt.query)
				tt.assert(t, got, err)
			})
		}
	}
}

func TestPeople_BothAdaptersAnswerTheSameWay(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name   string
		cloud  peopleBuilder
		fake   peopleBuilder
		ids    []string
		assert func(*testing.T, []jira.User, error)
	}{
		{
			name:  "an id this site does not know is absent, never a blank row",
			cloud: func(t *testing.T) jira.PeopleFinder { return peopleFromSite(t) },
			fake:  func(t *testing.T) jira.PeopleFinder { return conformFake(t) },
			ids:   []string{"5b10a2844c20165700ede21g", "acct-ada", "nobody-at-all"},
			assert: func(t *testing.T, got []jira.User, err error) {
				t.Helper()
				if err != nil {
					t.Fatalf("resolving account ids: %v", err)
				}
				if len(got) == 0 {
					t.Fatal("an id the site does know resolved to nothing")
				}
				conformNamesEverybody(t, got)
				for _, u := range got {
					if u.AccountID == "nobody-at-all" {
						t.Errorf("the site invented %+v for an id it does not have", u)
					}
				}
			},
		},
		{
			name:  "nobody asked for means nothing asked of the site",
			cloud: func(t *testing.T) jira.PeopleFinder { return peopleFromSite(t) },
			fake:  func(t *testing.T) jira.PeopleFinder { return conformFake(t) },
			ids:   nil,
			assert: func(t *testing.T, got []jira.User, err error) {
				t.Helper()
				if err != nil {
					t.Fatalf("resolving no ids at all: %v", err)
				}
				if len(got) != 0 {
					t.Errorf("got %+v for no ids at all", got)
				}
			},
		},
		{
			name: "a token that may not browse users",
			cloud: func(t *testing.T) jira.PeopleFinder {
				return peopleFromSite(t, jiratest.WithStatus(
					http.MethodGet, peopleBulkPath, http.StatusForbidden, "forbidden_browse_users.json"))
			},
			fake: func(t *testing.T) jira.PeopleFinder {
				return conformFake(t, jiratest.WithCapabilities(jiratest.NoPeople))
			},
			ids:    []string{"5b10a2844c20165700ede21g", "acct-ada"},
			assert: conformRefusedPeople,
		},
	}

	for _, tt := range cases {
		for _, adapter := range []struct {
			name string
			open peopleBuilder
		}{
			{name: "cloud", open: tt.cloud},
			{name: "fake", open: tt.fake},
		} {
			t.Run(tt.name+"/"+adapter.name, func(t *testing.T) {
				t.Parallel()

				got, err := adapter.open(t).People(t.Context(), tt.ids)
				tt.assert(t, got, err)
			})
		}
	}
}

func TestFilterVocabulary_BothAdaptersAnswerTheSameWay(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name  string
		cloud vocabBuilder
		fake  vocabBuilder
		run   func(*testing.T, jira.FilterVocabulary)
	}{
		{
			name:  "statuses are identified by id, because a name can be shared",
			cloud: func(t *testing.T) jira.FilterVocabulary { return vocabFromSite(t) },
			fake:  func(t *testing.T) jira.FilterVocabulary { return conformFake(t) },
			run: func(t *testing.T, c jira.FilterVocabulary) {
				t.Helper()
				got, err := c.IssueTypeStatuses(t.Context(), conformProject)
				if err != nil {
					t.Fatalf("reading the statuses in %s: %v", conformProject, err)
				}
				if len(got) == 0 {
					t.Fatal("the project came back with no issue types")
				}

				ids := make(map[string]map[string]bool)
				for _, entry := range got {
					if entry.Type.ID == "" {
						t.Errorf("the issue type %q has no id", entry.Type.Name)
					}
					if len(entry.Statuses) == 0 {
						t.Errorf("%q reaches no status at all", entry.Type.Name)
					}
					for _, status := range entry.Statuses {
						if status.ID == "" {
							t.Errorf("the status %q has no id", status.Name)
						}
						if status.Category == jira.CategoryUnknown {
							t.Errorf("the status %q (%s) has no category", status.Name, status.ID)
						}
						if ids[status.Name] == nil {
							ids[status.Name] = make(map[string]bool)
						}
						ids[status.Name][status.ID] = true
					}
				}
				shared := false
				for _, under := range ids {
					shared = shared || len(under) > 1
				}
				if !shared {
					t.Errorf("no display name in %v is shared by two ids, and both sites are supposed to hold one", ids)
				}
			},
		},
		{
			name:  "there is no site-wide answer about statuses",
			cloud: func(t *testing.T) jira.FilterVocabulary { return vocabFromSite(t) },
			fake:  func(t *testing.T) jira.FilterVocabulary { return conformFake(t) },
			run: func(t *testing.T, c jira.FilterVocabulary) {
				t.Helper()
				got, err := c.IssueTypeStatuses(t.Context(), "")
				var invalid *jira.ValidationError
				if !errors.As(err, &invalid) {
					t.Fatalf("got %+v, %T (%v); want a *jira.ValidationError", got, err, err)
				}
				if _, named := invalid.For("projectKey"); !named {
					t.Errorf("the refusal does not name projectKey: %v", invalid)
				}
			},
		},
		{
			name:  "priorities come back in the site's order and not the alphabet",
			cloud: func(t *testing.T) jira.FilterVocabulary { return vocabFromSite(t) },
			fake:  func(t *testing.T) jira.FilterVocabulary { return conformFake(t) },
			run: func(t *testing.T, c jira.FilterVocabulary) {
				t.Helper()
				got, err := c.Priorities(t.Context())
				if err != nil {
					t.Fatalf("reading the priorities: %v", err)
				}
				if len(got) < 2 {
					t.Fatalf("got %d priorities, want the site's whole list", len(got))
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
			},
		},
		{
			name:  "labels page, and one of them is not ASCII",
			cloud: func(t *testing.T) jira.FilterVocabulary { return vocabFromSite(t) },
			fake:  func(t *testing.T) jira.FilterVocabulary { return conformFake(t, jiratest.WithPageSize(3)) },
			run: func(t *testing.T, c jira.FilterVocabulary) {
				t.Helper()
				page, err := c.Labels(t.Context())
				if err != nil {
					t.Fatalf("reading the labels: %v", err)
				}
				if !page.HasMore() {
					t.Error("the whole site fitted on one page, so nothing here exercises the walk")
				}
				got, err := jira.Collect(t.Context(), page, 0)
				if err != nil {
					t.Fatalf("walking the labels: %v", err)
				}
				if len(got) == 0 {
					t.Fatal("the site came back with no labels at all")
				}
				if slices.Contains(got, "") {
					t.Errorf("an empty label reached the caller: %q", got)
				}
				// A label is whatever anybody typed, so a column measured with
				// len() over one is wrong. Both sites carry one that proves it.
				if !slices.ContainsFunc(got, func(l string) bool { return utf8.RuneCountInString(l) != len(l) }) {
					t.Errorf("every label in %q is ASCII, and a real site's are not", got)
				}
			},
		},
	}

	for _, tt := range cases {
		for _, adapter := range []struct {
			name string
			open vocabBuilder
		}{
			{name: "cloud", open: tt.cloud},
			{name: "fake", open: tt.fake},
		} {
			t.Run(tt.name+"/"+adapter.name, func(t *testing.T) {
				t.Parallel()
				tt.run(t, adapter.open(t))
			})
		}
	}
}

// conformNamesEverybody holds both adapters to the rule a blank row breaks: an
// account that reached a caller has an id to act on and a name to draw.
func conformNamesEverybody(t *testing.T, got []jira.User) {
	t.Helper()

	for _, u := range got {
		if strings.TrimSpace(u.AccountID) == "" || strings.TrimSpace(u.DisplayName) == "" {
			t.Errorf("a row with nothing on it reached the caller: %+v", u)
		}
	}
}

func conformRefusedPeople(t *testing.T, got []jira.User, err error) {
	t.Helper()

	var refused *jira.CapabilityError
	if !errors.As(err, &refused) {
		t.Fatalf("got %+v, %T (%v); want a *jira.CapabilityError", got, err, err)
	}
	if refused.Capability != jira.CapPeople {
		t.Errorf("Capability = %q, want %q so a caller can tell this refusal from every other", refused.Capability, jira.CapPeople)
	}
	if refused.Reason == "" {
		t.Error("the refusal carries no reason, and the reason is what the UI shows instead of the picker")
	}
	if len(got) != 0 {
		t.Errorf("the refusal came back with %+v attached", got)
	}
}
