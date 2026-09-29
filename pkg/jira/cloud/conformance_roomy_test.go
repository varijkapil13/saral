package cloud

import (
	"net/http"
	"slices"
	"testing"

	"github.com/varijkapil13/saral/pkg/jira"
	"github.com/varijkapil13/saral/pkg/jira/jiratest"
)

var roomyFields = []string{"summary", "status", "duedate", "subtasks", "fixVersions"}

const roomyPage = `{"issues":[{"id":"20001","key":"EX-1","fields":{
	"summary":"A card with everything on it",
	"status":{"id":"3","name":"Doing","statusCategory":{"id":4,"key":"indeterminate","name":"Doing"}},
	"duedate":"2026-10-02",
	"subtasks":[
		{"id":"20002","key":"EX-2","fields":{"summary":"First half","status":{"id":"5","name":"Shipped","statusCategory":{"id":3,"key":"done","name":"Shipped"}},"issuetype":{"id":"10003","name":"Sub-task","subtask":true}}},
		{"id":"20003","key":"EX-3","fields":{"summary":"Second half","status":{"id":"1","name":"Waiting","statusCategory":{"id":2,"key":"new","name":"Waiting"}},"issuetype":{"id":"10003","name":"Sub-task","subtask":true}}}
	],
	"fixVersions":[{"id":"10100","name":"2026.4","released":false,"archived":false,"releaseDate":"2026-11-01"}]
}}],"isLast":true}`

func TestSearch_BothAdaptersFillWhatARoomyCardDraws(t *testing.T) {
	t.Parallel()

	for _, adapter := range []struct {
		name string
		open func(*testing.T) jira.Searcher
	}{
		{"cloud", func(t *testing.T) jira.Searcher {
			s := jiratest.NewServer(jiratest.WithHandler(http.MethodPost, searchJQLPath, jsonHandler(http.StatusOK, roomyPage)))
			t.Cleanup(s.Close)
			c, _ := testClient(t, s.URL())
			return c
		}},
		{"fake", func(t *testing.T) jira.Searcher {
			due, err := jira.ParseDate("2026-10-02")
			if err != nil {
				t.Fatal(err)
			}
			release, _ := jira.ParseDate("2026-11-01")
			return conformFake(t, jiratest.WithIssues([]jira.Issue{{
				ID: "20001", Key: conformProject + "-1", Project: jira.ProjectRef{Key: conformProject},
				Summary: "A card with everything on it",
				Status:  jira.Status{ID: "3", Name: "Doing", Category: jira.CategoryInProgress},
				Due:     due,
				Subtasks: []jira.IssueRef{
					{ID: "20002", Key: "EX-2", Summary: "First half", Status: jira.Status{ID: "5", Name: "Shipped", Category: jira.CategoryDone}},
					{ID: "20003", Key: "EX-3", Summary: "Second half", Status: jira.Status{ID: "1", Name: "Waiting", Category: jira.CategoryToDo}},
				},
				FixVersions: []jira.Version{{ID: "10100", Name: "2026.4", ReleaseDate: release}},
			}}))
		}},
	} {
		t.Run(adapter.name, func(t *testing.T) {
			t.Parallel()

			page, err := adapter.open(t).Search(t.Context(), jira.Query{JQL: "key = EX-1", Fields: roomyFields})
			if err != nil {
				t.Fatalf("searching: %v", err)
			}
			got := firstIssue(t, page)
			if got.Due.String() != "2026-10-02" {
				t.Errorf("Due = %q, want 2026-10-02", got.Due.String())
			}
			if len(got.Subtasks) != 2 {
				t.Fatalf("Subtasks = %+v, want two", got.Subtasks)
			}
			done := 0
			for _, s := range got.Subtasks {
				if s.Status.Category == jira.CategoryDone {
					done++
				}
			}
			if done != 1 || got.Subtasks[1].Status.Category != jira.CategoryToDo || got.Subtasks[0].Key != "EX-2" {
				t.Errorf("the subtasks read as %+v, want one done and one to do, by category", got.Subtasks)
			}
			names := make([]string, 0, len(got.FixVersions))
			for _, v := range got.FixVersions {
				names = append(names, v.Name)
			}
			if !slices.Equal(names, []string{"2026.4"}) || got.FixVersions[0].ReleaseDate.String() != "2026-11-01" {
				t.Errorf("FixVersions = %+v, want 2026.4 due 2026-11-01", got.FixVersions)
			}
		})
	}
}
