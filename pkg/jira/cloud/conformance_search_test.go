package cloud

import (
	"net/http"
	"testing"

	"github.com/varijkapil13/saral/pkg/jira"
	"github.com/varijkapil13/saral/pkg/jira/jiratest"
)

// One set of assertions about the account on an issue, run against both
// adapters. A picker badges an app account and sinks it below the people, so the
// issue that account is assigned to has to say the same thing about it — a read
// that drops accountType looks, on screen, like the account changing kind
// between two views.

type searchBuilder func(*testing.T) jira.Searcher

type kindCase struct {
	name  string
	cloud searchBuilder
	fake  searchBuilder
	want  jira.AccountKind
}

func TestSearch_BothAdaptersAnswerTheSameWayAboutAnAccount(t *testing.T) {
	t.Parallel()

	const (
		app     = `{"accountId":"acct:nightly-bot","displayName":"Nightly Runner","active":true,"accountType":"app"}`
		person  = `{"accountId":"acct-ada","displayName":"Ada Lovelace","active":true,"accountType":"atlassian"}`
		unnamed = `{"accountId":"acct-ada","displayName":"Ada Lovelace","active":true}`
	)

	cases := []kindCase{
		{
			name:  "an app account is assigned work and says so",
			cloud: func(t *testing.T) jira.Searcher { return searchFromSite(t, app) },
			fake: func(t *testing.T) jira.Searcher {
				return searchFromFake(t, jira.User{AccountID: "acct:nightly-bot", DisplayName: "Nightly Runner", Active: true, Kind: jira.AccountApp})
			},
			want: jira.AccountApp,
		},
		{
			name:  "a person is a person on the issue as well as in the picker",
			cloud: func(t *testing.T) jira.Searcher { return searchFromSite(t, person) },
			fake: func(t *testing.T) jira.Searcher {
				return searchFromFake(t, jira.User{AccountID: "acct-ada", DisplayName: "Ada Lovelace", Active: true, Kind: jira.AccountPerson})
			},
			want: jira.AccountPerson,
		},
		{
			name:  "an answer that did not say leaves the kind unknown rather than guessing",
			cloud: func(t *testing.T) jira.Searcher { return searchFromSite(t, unnamed) },
			fake: func(t *testing.T) jira.Searcher {
				return searchFromFake(t, jira.User{AccountID: "acct-ada", DisplayName: "Ada Lovelace", Active: true})
			},
			want: jira.AccountUnknown,
		},
	}

	for _, tt := range cases {
		for _, adapter := range []struct {
			name string
			open searchBuilder
		}{
			{name: "cloud", open: tt.cloud},
			{name: "fake", open: tt.fake},
		} {
			t.Run(tt.name+"/"+adapter.name, func(t *testing.T) {
				t.Parallel()

				got := searchOneIssue(t, adapter.open(t))
				if got.Assignee == nil {
					t.Fatalf("%s came back unassigned", got.Key)
				}
				if got.Assignee.Kind != tt.want {
					t.Errorf("the assignee %s reads as %s, want %s", got.Assignee.AccountID, kindName(got.Assignee.Kind), kindName(tt.want))
				}
			})
		}
	}
}

// searchFromSite is the cloud adapter over a site that answers one issue,
// assigned to whatever account JSON the case wrote.
func searchFromSite(t *testing.T, account string) jira.Searcher {
	t.Helper()

	page := `{"issues":[{"id":"20001","key":"` + conformProject + `-1","fields":{"summary":"Somebody is on this","assignee":` + account + `}}],"isLast":true}`
	s := jiratest.NewServer(jiratest.WithHandler(http.MethodPost, searchJQLPath, func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(page))
	}))
	t.Cleanup(s.Close)
	c, _ := testClient(t, s.URL())
	return c
}

// searchFromFake is the in-memory fake holding the same issue, assigned to the
// same account described in the terms the fake stores accounts in.
func searchFromFake(t *testing.T, assignee jira.User) jira.Searcher {
	t.Helper()

	return conformFake(t, jiratest.WithIssues([]jira.Issue{{
		ID:       "20001",
		Key:      conformProject + "-1",
		Project:  jira.ProjectRef{Key: conformProject},
		Summary:  "Somebody is on this",
		Assignee: &assignee,
	}}))
}

func searchOneIssue(t *testing.T, c jira.Searcher) jira.Issue {
	t.Helper()

	page, err := c.Search(t.Context(), jira.Query{
		JQL:    "key = " + conformProject + "-1",
		Fields: []string{"summary", "assignee"},
	})
	if err != nil {
		t.Fatalf("searching: %v", err)
	}
	got, err := jira.Collect(t.Context(), page, 0)
	if err != nil {
		t.Fatalf("walking the search: %v", err)
	}
	if len(got) == 0 {
		t.Fatal("the search came back with no issues at all")
	}
	return got[0]
}

// kindName spells out the kind a badge deliberately draws as nothing, so that a
// failure about it does not report a blank.
func kindName(k jira.AccountKind) string {
	if k == jira.AccountUnknown {
		return "no kind at all"
	}
	return k.String()
}
