package main

import (
	"context"
	"encoding/json"
	"slices"
	"strings"
	"testing"

	"github.com/varijkapil13/saral/pkg/jira"
	"github.com/varijkapil13/saral/pkg/jira/jiratest"
)

func TestIssueView_Goldens(t *testing.T) {
	tests := map[string][]string{
		"issue_view_plain": {"issue", "view", "PROJ-1"},
		"issue_view_json":  {"issue", "view", "PROJ-1", "--json"},
		"issue_view_url":   {"issue", "view", "https://example.atlassian.net/browse/PROJ-3", "--json"},
	}
	for name, args := range tests {
		t.Run(name, func(t *testing.T) {
			writeProfile(t)
			out, err := script(t, siteFake(), args...)
			if err != nil {
				t.Fatal(err)
			}
			golden(t, name, out)
		})
	}
}

func TestIssueView_JSONKeepsItsSchema(t *testing.T) {
	writeProfile(t)
	out, err := script(t, siteFake(), "issue", "view", "PROJ-1", "--json")
	if err != nil {
		t.Fatal(err)
	}
	var got map[string]json.RawMessage
	if err := json.Unmarshal([]byte(out), &got); err != nil {
		t.Fatalf("not JSON: %v\n%s", err, out)
	}
	for _, member := range []string{
		"key", "id", "url", "project", "type", "status", "summary", "priority", "resolution", "assignee",
		"reporter", "labels", "components", "fixVersions", "parent", "subtasks", "links", "due", "created",
		"updated", "resolved", "timeTracking", "description", "fields",
	} {
		if _, ok := got[member]; !ok {
			t.Errorf("the issue view JSON has no %q member", member)
		}
	}
	var fields map[string]customJSON
	if err := json.Unmarshal(got["fields"], &fields); err != nil || len(fields) == 0 {
		t.Fatalf("no custom fields reached the JSON (%v): %s", err, got["fields"])
	}
	for id, f := range fields {
		if f.Name == "" || f.Name == id {
			t.Errorf("custom field %s went out without the name this site gives it", id)
		}
	}
}

func TestIssueView_ReadsTheIssueNotTheSearchIndex(t *testing.T) {
	writeProfile(t)
	f := siteFake()
	if _, err := script(t, f, "issue", "view", "PROJ-1"); err != nil {
		t.Fatal(err)
	}
	if !slices.Contains(f.Calls(), "IssueFields") || slices.Contains(f.Calls(), "Search") {
		t.Errorf("calls %v, want the issue read and no search", f.Calls())
	}
}

func TestIssueView_AMissingIssue(t *testing.T) {
	writeProfile(t)
	_, err := script(t, siteFake(), "issue", "view", "PROJ-999")
	wantCode(t, err, exitOther, "PROJ-999")
}

func TestSearch_Goldens(t *testing.T) {
	tests := map[string][]string{
		"search_plain":         {"search", "project = PROJ ORDER BY key ASC", "--limit", "5"},
		"search_json":          {"search", "project = PROJ ORDER BY key ASC", "--limit", "2", "--json"},
		"search_fields_plain":  {"search", "project = PROJ ORDER BY key ASC", "--limit", "4", "--fields", "summary, Story Points,labels,key"},
		"search_fields_json":   {"search", "project = PROJ ORDER BY key ASC", "--limit", "2", "--fields", "status,story points", "--json"},
		"search_nothing_found": {"search", "project = PROJ AND labels = nowhere", "--json"},
	}
	for name, args := range tests {
		t.Run(name, func(t *testing.T) {
			writeProfile(t)
			out, err := script(t, siteFake(), args...)
			if err != nil {
				t.Fatal(err)
			}
			golden(t, name, out)
		})
	}
}

func TestSearch_PagesUpToTheLimit(t *testing.T) {
	writeProfile(t)
	f := siteFake(jiratest.WithPageSize(3))
	out, err := script(t, f, "search", "project = PROJ", "--limit", "7", "--fields", "summary")
	if err != nil {
		t.Fatal(err)
	}
	if got := strings.Count(out, "\n"); got != 7 {
		t.Errorf("%d rows, want 7:\n%s", got, out)
	}
	searches := 0
	for _, c := range f.Calls() {
		if c == "Search" {
			searches++
		}
	}
	if searches != 3 {
		t.Errorf("%d search requests for 7 rows at 3 a page, want 3: %v", searches, f.Calls())
	}

	all, err := script(t, f, "search", "project = PROJ", "--limit", "500", "--json", "--fields", "summary")
	if err != nil {
		t.Fatal(err)
	}
	var page struct {
		Issues []json.RawMessage `json:"issues"`
		More   bool              `json:"more"`
	}
	if err := json.Unmarshal([]byte(all), &page); err != nil {
		t.Fatal(err)
	}
	if len(page.Issues) != 12 || page.More {
		t.Errorf("%d issues, more %v; want all 12 and no more", len(page.Issues), page.More)
	}
}

func TestSearch_ReadsTheQueryFromStdin(t *testing.T) {
	writeProfile(t)
	out, err := scriptRun{client: siteFake(), stdin: "project = PROJ ORDER BY key ASC\n"}.do(t, "search", "-", "--limit", "1", "--fields", "summary")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(out, "PROJ-1\t") {
		t.Errorf("stdin's query did not run: %q", out)
	}
}

func TestSearch_AsksForTheFieldsItPrintsAndNoMore(t *testing.T) {
	writeProfile(t)
	var asked []string
	f := siteFake()
	_, err := script(t, recordQuery{Fake: f, fields: &asked}, "search", "project = PROJ", "--fields", "labels,Story Points")
	if err != nil {
		t.Fatal(err)
	}
	if len(asked) != 2 || asked[0] != "labels" || !strings.HasPrefix(asked[1], "customfield_") {
		t.Errorf("the search asked for %v, want labels and the story points field's ID", asked)
	}
}

type recordQuery struct {
	*jiratest.Fake
	fields *[]string
}

func (r recordQuery) Search(ctx context.Context, q jira.Query) (jira.Page[jira.Issue], error) {
	*r.fields = slices.Clone(q.Fields)
	return r.Fake.Search(ctx, q)
}
