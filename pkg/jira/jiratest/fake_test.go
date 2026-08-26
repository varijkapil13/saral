package jiratest_test

import (
	"bytes"
	"context"
	"errors"
	"io"
	"reflect"
	"slices"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/varijkapil13/saral/pkg/adf"
	"github.com/varijkapil13/saral/pkg/jira"
	"github.com/varijkapil13/saral/pkg/jira/jiratest"
)

var fakeNarrow = []string{"summary", "status", "assignee", "labels"}

func fakeNewWithIssues(t *testing.T, n int, opts ...jiratest.Option) *jiratest.Fake {
	t.Helper()
	all := append([]jiratest.Option{
		jiratest.WithProject("PROJ", jiratest.Scrum),
		jiratest.WithIssues(jiratest.Gen(n)),
	}, opts...)
	return jiratest.New(all...)
}

func fakeBoard(t *testing.T, c *jiratest.Fake) jira.Board {
	t.Helper()
	boards, err := c.Boards(t.Context(), "PROJ")
	if err != nil {
		t.Fatalf("Boards: %v", err)
	}
	if len(boards) != 1 {
		t.Fatalf("want one board, got %d", len(boards))
	}
	return boards[0]
}

func fakeSprintsOf(t *testing.T, c *jiratest.Fake, boardID int64) []jira.Sprint {
	t.Helper()
	page, err := c.Sprints(t.Context(), boardID)
	if err != nil {
		t.Fatalf("Sprints: %v", err)
	}
	sprints, err := jira.Collect(t.Context(), page, 0)
	if err != nil {
		t.Fatalf("collecting sprints: %v", err)
	}
	return sprints
}

func fakeVersionByName(t *testing.T, c *jiratest.Fake, name string) jira.Version {
	t.Helper()
	versions, err := c.Versions(t.Context(), "PROJ")
	if err != nil {
		t.Fatalf("Versions: %v", err)
	}
	for i := range versions {
		if versions[i].Name == name {
			return versions[i]
		}
	}
	t.Fatalf("no version named %q in %v", name, versions)
	return jira.Version{}
}

// TestFake_RunsTheUsagePrintedInTheTestingDoc keeps docs/TESTING.md honest: the
// snippet it shows has to compile and work exactly as written.
func TestFake_RunsTheUsagePrintedInTheTestingDoc(t *testing.T) {
	t.Parallel()
	c := jiratest.New(
		jiratest.WithProject("PROJ", jiratest.Scrum),
		jiratest.WithIssues(jiratest.Gen(500)),
		jiratest.WithCapabilities(jiratest.NoBulkMove, jiratest.NoPlans),
	)
	c.FailNext(&jira.RateLimitError{RetryAfter: 30 * time.Second})
	c.FailNext(&jira.CapabilityError{Reason: "needs Bulk Change permission"})
	c.Delay(200 * time.Millisecond)
	c.CursorLoop()
	c.Delay(0)

	ctx := t.Context()
	var rl *jira.RateLimitError
	if _, err := c.Me(ctx); !errors.As(err, &rl) || rl.RetryAfter != 30*time.Second {
		t.Fatalf("first call: want the queued rate limit, got %v", err)
	}
	var capErr *jira.CapabilityError
	if _, err := c.Me(ctx); !errors.As(err, &capErr) {
		t.Fatalf("second call: want the queued capability error, got %v", err)
	}
	if _, err := c.Me(ctx); err != nil {
		t.Fatalf("third call: the queue is empty, want success, got %v", err)
	}
	if _, err := c.Plans(ctx); !errors.As(err, &capErr) {
		t.Fatalf("Plans: want a capability error, got %v", err)
	}
	page, err := c.Search(ctx, jira.Query{JQL: `project = PROJ`, Fields: fakeNarrow})
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if len(page.Items) != 50 {
		t.Fatalf("want the default page of 50 issues, got %d", len(page.Items))
	}
}

func TestSearch_PagesByCursorAndReportsNoTotal(t *testing.T) {
	t.Parallel()
	c := fakeNewWithIssues(t, 25, jiratest.WithPageSize(10))
	ctx := t.Context()

	page, err := c.Search(ctx, jira.Query{JQL: `project = PROJ ORDER BY key ASC`, Fields: fakeNarrow})
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if _, ok := page.Count(); ok {
		t.Error("a cursor-paginated page must not claim a total")
	}
	all, err := jira.Collect(ctx, page, 0)
	if err != nil {
		t.Fatalf("Collect: %v", err)
	}
	if len(all) != 25 {
		t.Fatalf("want 25 issues across the walk, got %d", len(all))
	}
	if all[0].Key != "PROJ-1" || all[24].Key != "PROJ-25" {
		t.Errorf("want PROJ-1 first and PROJ-25 last, got %s and %s", all[0].Key, all[24].Key)
	}
	if got := len(c.Calls()); got != 3 {
		t.Errorf("want one call per page of 10 over 25 issues, got %d calls: %v", got, c.Calls())
	}
}

func TestSearch_TreatsARepeatedPageTokenAsExhaustion(t *testing.T) {
	t.Parallel()
	c := fakeNewWithIssues(t, 35, jiratest.WithPageSize(10))
	c.CursorLoop()
	ctx := t.Context()

	first, err := c.Search(ctx, jira.Query{JQL: `project = PROJ ORDER BY key`, Fields: fakeNarrow})
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if !first.HasMore() {
		t.Fatal("the first page of 35 issues must have more")
	}
	second, err := first.Next(ctx)
	if err != nil {
		t.Fatalf("Next: %v", err)
	}
	if second.HasMore() {
		t.Fatal("a page whose token was handed out before must read as exhausted")
	}
	if _, err := second.Next(ctx); !errors.Is(err, jira.ErrNoMorePages) {
		t.Fatalf("want ErrNoMorePages past the loop, got %v", err)
	}
	all, err := jira.Collect(ctx, first, 0)
	if err != nil {
		t.Fatalf("Collect: %v", err)
	}
	if len(all) != 20 {
		t.Fatalf("the walk must stop at the repeated token, got %d issues", len(all))
	}
}

func TestSearch_RefusesAQueryThatNamesNoFields(t *testing.T) {
	t.Parallel()
	c := fakeNewWithIssues(t, 3)
	_, err := c.Search(t.Context(), jira.Query{JQL: `project = PROJ`})
	var ve *jira.ValidationError
	if !errors.As(err, &ve) {
		t.Fatalf("want a validation error, got %v", err)
	}
	if _, ok := ve.For("fields"); !ok {
		t.Errorf("the validation error must name fields, got %v", ve)
	}
}

func TestSearch_RefusesJQLItCannotParseRatherThanMatchingEverything(t *testing.T) {
	t.Parallel()
	c := fakeNewWithIssues(t, 5)
	cases := []struct {
		name string
		jql  string
	}{
		{"an operator the fake does not implement", `summary ~ "login"`},
		{"a field the fake does not index", `sprint = 4`},
		{"a function call as a value", `created >= -7d`},
		{"an OR", `project = PROJ OR project = OTHER`},
		{"an OR between two bracketed clauses", `(status = "10201") OR (status = "10203")`},
		{"an IN list with a trailing comma", `status IN ("10201",)`},
		{"an IN list with nothing in it", `status IN ()`},
		{"a field the picker does not offer either", `component = "web"`},
		{"an unterminated quote", `status = "Triage`},
		{"an order by field the fake cannot sort on", `project = PROJ ORDER BY rank`},
		{"a nonsense sort direction", `project = PROJ ORDER BY key sideways`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			_, err := c.Search(t.Context(), jira.Query{JQL: tc.jql, Fields: fakeNarrow})
			var ve *jira.ValidationError
			if !errors.As(err, &ve) {
				t.Fatalf("want a validation error for %q, got %v", tc.jql, err)
			}
			if _, ok := ve.For("jql"); !ok {
				t.Errorf("the validation error must name jql, got %v", ve)
			}
		})
	}
}

func TestSearch_FiltersOnTheJQLSubsetItSupports(t *testing.T) {
	t.Parallel()
	c := fakeNewWithIssues(t, 30, jiratest.WithPageSize(100))
	ctx := t.Context()

	one, err := c.Issue(ctx, "PROJ-7")
	if err != nil {
		t.Fatalf("Issue: %v", err)
	}
	cases := []struct {
		name  string
		jql   string
		check func(t *testing.T, got []jira.Issue)
	}{
		{
			name: "everything in the project",
			jql:  `project = PROJ`,
			check: func(t *testing.T, got []jira.Issue) {
				if len(got) != 30 {
					t.Fatalf("want all 30, got %d", len(got))
				}
			},
		},
		{
			name: "one key",
			jql:  `key = PROJ-7`,
			check: func(t *testing.T, got []jira.Issue) {
				if len(got) != 1 || got[0].Key != "PROJ-7" {
					t.Fatalf("want just PROJ-7, got %d issues", len(got))
				}
			},
		},
		{
			name: "a quoted status",
			jql:  `status = "` + one.Status.Name + `"`,
			check: func(t *testing.T, got []jira.Issue) {
				if len(got) == 0 {
					t.Fatal("want at least one issue in that status")
				}
				for _, iss := range got {
					if iss.Status.Name != one.Status.Name {
						t.Fatalf("%s is in %s, not %s", iss.Key, iss.Status.Name, one.Status.Name)
					}
				}
			},
		},
		{
			name: "unassigned",
			jql:  `assignee IS EMPTY`,
			check: func(t *testing.T, got []jira.Issue) {
				if len(got) == 0 {
					t.Fatal("Gen must leave some issues unassigned")
				}
				for _, iss := range got {
					if iss.Assignee != nil {
						t.Fatalf("%s is assigned to %s", iss.Key, iss.Assignee.DisplayName)
					}
				}
			},
		},
		{
			name: "assigned to an account",
			jql:  `assignee = acct-ada`,
			check: func(t *testing.T, got []jira.Issue) {
				if len(got) == 0 {
					t.Fatal("want some issues assigned to that account")
				}
				for _, iss := range got {
					if iss.Assignee == nil || iss.Assignee.AccountID != "acct-ada" {
						t.Fatalf("%s is not assigned to acct-ada", iss.Key)
					}
				}
			},
		},
		{
			name: "a label",
			jql:  `labels = infra`,
			check: func(t *testing.T, got []jira.Issue) {
				for _, iss := range got {
					if !fakeContainsFold(iss.Labels, "infra") {
						t.Fatalf("%s does not carry the label, only %v", iss.Key, iss.Labels)
					}
				}
			},
		},
		{
			name: "two clauses joined by AND",
			jql:  `project = PROJ AND assignee IS EMPTY`,
			check: func(t *testing.T, got []jira.Issue) {
				for _, iss := range got {
					if iss.Assignee != nil || iss.Project.Key != "PROJ" {
						t.Fatalf("%s does not satisfy both clauses", iss.Key)
					}
				}
			},
		},
		{
			name: "nothing matches",
			jql:  `project = NOPE`,
			check: func(t *testing.T, got []jira.Issue) {
				if len(got) != 0 {
					t.Fatalf("want no matches, got %d", len(got))
				}
			},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			page, err := c.Search(t.Context(), jira.Query{JQL: tc.jql, Fields: fakeNarrow})
			if err != nil {
				t.Fatalf("Search(%q): %v", tc.jql, err)
			}
			tc.check(t, page.Items)
		})
	}
}

// The filter picker composes a clause over any of six facets and runs it at the
// site, so the fake has to answer for all six — and for the IN list two values
// of one facet compose to, and the bracketed OR that lets "nobody" sit beside a
// named person. A shape the fake refuses is a facet whose rows nothing checks.
func TestSearch_SelectsTheRightIssuesForEveryFieldAFilterComposes(t *testing.T) {
	t.Parallel()
	const issues = 30
	c := fakeNewWithIssues(t, issues, jiratest.WithPageSize(100))
	fields := []string{"summary", "status", "assignee", "reporter", "labels", "priority", "issuetype"}

	cases := []struct {
		name  string
		jql   string
		match func(iss *jira.Issue) bool
	}{
		{
			name: "a reporter by account id",
			jql:  `reporter = "acct-grace"`,
			match: func(iss *jira.Issue) bool {
				return iss.Reporter != nil && iss.Reporter.AccountID == "acct-grace"
			},
		},
		{
			name:  "an issue type by the id the site minted",
			jql:   `issuetype = "10302"`,
			match: func(iss *jira.Issue) bool { return iss.Type.ID == "10302" },
		},
		{
			name:  "an issue type by its name, under the spelling JQL also takes",
			jql:   `type = "Story"`,
			match: func(iss *jira.Issue) bool { return iss.Type.Name == "Story" },
		},
		{
			name:  "a priority by name",
			jql:   `priority = "Normal"`,
			match: func(iss *jira.Issue) bool { return iss.Priority != nil && iss.Priority.Name == "Normal" },
		},
		{
			name:  "two statuses, which is what two values of one facet compose to",
			jql:   `status IN ("10201", "10203")`,
			match: func(iss *jira.Issue) bool { return iss.Status.ID == "10201" || iss.Status.ID == "10203" },
		},
		{
			name: "two accounts",
			jql:  `assignee IN ("acct-ada", "acct-grace")`,
			match: func(iss *jira.Issue) bool {
				return iss.Assignee != nil &&
					(iss.Assignee.AccountID == "acct-ada" || iss.Assignee.AccountID == "acct-grace")
			},
		},
		{
			name: "nobody, or one named person",
			jql:  `(assignee = "acct-ada" OR assignee IS EMPTY)`,
			match: func(iss *jira.Issue) bool {
				return iss.Assignee == nil || iss.Assignee.AccountID == "acct-ada"
			},
		},
		{
			name: "a facet and a person together",
			jql:  `priority = "10401" AND reporter = "acct-grace"`,
			match: func(iss *jira.Issue) bool {
				return iss.Priority != nil && iss.Priority.ID == "10401" &&
					iss.Reporter != nil && iss.Reporter.AccountID == "acct-grace"
			},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			want := fakeKeysMatching(jiratest.Gen(issues), tc.match)
			if len(want) == 0 || len(want) == issues {
				t.Fatalf("%q matches %d of %d issues, so it would pass against a fake that filtered nothing",
					tc.jql, len(want), issues)
			}
			page, err := c.Search(t.Context(), jira.Query{JQL: tc.jql, Fields: fields})
			if err != nil {
				t.Fatalf("Search(%q): %v", tc.jql, err)
			}
			if got := fakeKeysMatching(page.Items, func(*jira.Issue) bool { return true }); !slices.Equal(got, want) {
				t.Errorf("%q selected %v, want %v", tc.jql, got, want)
			}
		})
	}
}

// fakeKeysMatching is the keys of the issues a predicate keeps, in the order
// they were given, which is the order a search with no ORDER BY answers in.
func fakeKeysMatching(issues []jira.Issue, match func(iss *jira.Issue) bool) []string {
	out := make([]string, 0, len(issues))
	for i := range issues {
		if match(&issues[i]) {
			out = append(out, issues[i].Key)
		}
	}
	return out
}

func TestSearch_OrdersDescendingWhenTheQuerySaysSo(t *testing.T) {
	t.Parallel()
	c := fakeNewWithIssues(t, 12, jiratest.WithPageSize(100))
	page, err := c.Search(t.Context(), jira.Query{JQL: `project = PROJ ORDER BY key DESC`, Fields: fakeNarrow})
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if page.Items[0].Key != "PROJ-12" || page.Items[11].Key != "PROJ-1" {
		t.Fatalf("want PROJ-12 down to PROJ-1, got %s to %s", page.Items[0].Key, page.Items[11].Key)
	}
}

func TestSearch_NarrowsTheFieldSetToTheFieldsAsked(t *testing.T) {
	t.Parallel()
	c := fakeNewWithIssues(t, 6, jiratest.WithPageSize(100))
	ctx := t.Context()

	fields, err := c.Fields(ctx)
	if err != nil {
		t.Fatalf("Fields: %v", err)
	}
	rank, ok := jira.FieldByName(fields, "Rank")
	if !ok {
		t.Fatal("the fake catalogue must carry a rank field")
	}
	narrow, err := c.Search(ctx, jira.Query{JQL: `key = PROJ-1`, Fields: []string{"summary"}})
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if _, present := narrow.Items[0].Fields.Get(rank.Ref()); present {
		t.Error("a field the query did not ask for must not come back")
	}
	wide, err := c.Search(ctx, jira.Query{JQL: `key = PROJ-1`, Fields: []string{jira.FieldsAll}})
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if _, present := wide.Items[0].Fields.Get(rank.Ref()); !present {
		t.Error("*all must return the whole field set")
	}
}

// TestSearch_ReportsTheSameFieldsItMasked is the anti-lying test: the fake
// blanks the fields the query did not name and reports what it blanked from the
// same value, so it cannot mask one set and claim another. A fake that could
// would be worse than no fake, since every consumer of the mask is written
// against it.
func TestSearch_ReportsTheSameFieldsItMasked(t *testing.T) {
	t.Parallel()
	c := fakeNewWithIssues(t, 6, jiratest.WithPageSize(100))

	page, err := c.Search(t.Context(), jira.Query{JQL: `project = PROJ ORDER BY key`, Fields: fakeNarrow})
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if len(page.Items) == 0 {
		t.Fatal("the search matched nothing")
	}

	want := slices.Sorted(slices.Values(fakeNarrow))
	for _, iss := range page.Items {
		if iss.Requested.Wide() {
			t.Errorf("%s claims every field was asked for", iss.Key)
		}
		if got := iss.Requested.IDs(); !slices.Equal(got, want) {
			t.Errorf("%s reports %q as requested, want %q", iss.Key, got, want)
		}
		for _, id := range iss.Fields.IDs() {
			if !iss.Requested.Has(id) {
				t.Errorf("%s carries the field %s, which it says was never asked for", iss.Key, id)
			}
		}
		carried := map[string]bool{
			"summary":      iss.Summary != "",
			"status":       iss.Status.Name != "",
			"description":  !iss.Description.IsZero(),
			"issuetype":    iss.Type.ID != "",
			"priority":     iss.Priority != nil,
			"reporter":     iss.Reporter != nil,
			"components":   iss.Components != nil,
			"fixVersions":  iss.FixVersions != nil,
			"duedate":      !iss.Due.IsZero(),
			"created":      !iss.Created.IsZero(),
			"updated":      !iss.Updated.IsZero(),
			"timetracking": iss.TimeTracking != nil,
		}
		for id, present := range carried {
			if present && !iss.Requested.Has(id) {
				t.Errorf("%s carries %s while reporting it was not asked for", iss.Key, id)
			}
		}
		if !carried["summary"] || !carried["status"] {
			t.Errorf("%s is missing a field the query did ask for: %+v", iss.Key, iss)
		}
	}
}

func TestSearch_ReportsAWideMaskForAWildcard(t *testing.T) {
	t.Parallel()
	c := fakeNewWithIssues(t, 2, jiratest.WithPageSize(100))

	for _, wildcard := range []string{jira.FieldsAll, jira.FieldsNavigable} {
		page, err := c.Search(t.Context(), jira.Query{JQL: `project = PROJ`, Fields: []string{wildcard}})
		if err != nil {
			t.Fatalf("Search with %s: %v", wildcard, err)
		}
		iss := page.Items[0]
		if !iss.Requested.Wide() {
			t.Errorf("%s asked for with %s does not report a wide read", iss.Key, wildcard)
		}
		if !iss.Requested.Has("assignee") || !iss.Requested.Has("a-field-nothing-has-enumerated") {
			t.Errorf("a wide read must answer for every field, including ones nothing enumerated")
		}
	}
}

// TestIssue_AndCreateIssue_ComeBackWide covers the two calls that return a bare
// issue with no field list anywhere: both read everything, so an edit built off
// one may write any field back.
func TestIssue_AndCreateIssue_ComeBackWide(t *testing.T) {
	t.Parallel()
	c := fakeNewWithIssues(t, 3)
	ctx := t.Context()

	fetched, err := c.Issue(ctx, "PROJ-1")
	if err != nil {
		t.Fatalf("Issue: %v", err)
	}
	if !fetched.Requested.Wide() {
		t.Error("a fetched issue does not report a wide read, so an edit off it would refuse every field")
	}

	created, err := c.CreateIssue(ctx, jira.IssueInput{
		ProjectKey:  "PROJ",
		IssueTypeID: fetched.Type.ID,
		Summary:     "A new row",
	})
	if err != nil {
		t.Fatalf("CreateIssue: %v", err)
	}
	if !created.Requested.Wide() {
		t.Error("a created issue does not report a wide read")
	}
	again, err := c.Issue(ctx, created.Key)
	if err != nil {
		t.Fatalf("Issue after CreateIssue: %v", err)
	}
	if !again.Requested.Wide() {
		t.Error("an issue read back after creation does not report a wide read")
	}
}

// TestSearch_RefusesAFieldListThatOnlyLooksLikeOne pins the fake to the cloud
// adapter, which drops blanks before it counts what it was given: a list of
// spaces names no fields, and an adapter that accepts it teaches a view that a
// search with no fields works.
func TestSearch_RefusesAFieldListThatOnlyLooksLikeOne(t *testing.T) {
	t.Parallel()
	c := fakeNewWithIssues(t, 2)

	for _, fields := range [][]string{nil, {}, {"  "}, {"", "\t"}} {
		_, err := c.Search(t.Context(), jira.Query{JQL: `project = PROJ`, Fields: fields})

		var invalid *jira.ValidationError
		if !errors.As(err, &invalid) {
			t.Fatalf("Fields %q was answered with %v, want a *jira.ValidationError", fields, err)
		}
		if _, ok := invalid.For("fields"); !ok {
			t.Errorf("the refusal of %q does not name the fields list: %v", fields, invalid)
		}
	}
}

func TestSprints_PageByOffsetAndReportATotal(t *testing.T) {
	t.Parallel()
	c := fakeNewWithIssues(t, 1, jiratest.WithPageSize(2))
	ctx := t.Context()
	board := fakeBoard(t, c)

	page, err := c.Sprints(ctx, board.ID)
	if err != nil {
		t.Fatalf("Sprints: %v", err)
	}
	total, ok := page.Count()
	if !ok || total != 3 {
		t.Fatalf("want an offset page reporting 3 sprints, got %d (reported=%v)", total, ok)
	}
	if len(page.Items) != 2 || !page.HasMore() {
		t.Fatalf("want a first page of 2 with more to come, got %d (more=%v)", len(page.Items), page.HasMore())
	}
	second, err := page.Next(ctx)
	if err != nil {
		t.Fatalf("Next: %v", err)
	}
	if len(second.Items) != 1 || second.HasMore() {
		t.Fatalf("want a last page of 1, got %d (more=%v)", len(second.Items), second.HasMore())
	}
	states := make([]jira.SprintState, 0, len(page.Items)+len(second.Items))
	for _, sp := range slices.Concat(page.Items, second.Items) {
		states = append(states, sp.State)
	}
	want := []jira.SprintState{jira.SprintClosed, jira.SprintActive, jira.SprintFuture}
	if !reflect.DeepEqual(states, want) {
		t.Errorf("a scrum board seeds one sprint in each state, got %v", states)
	}
}

func TestCapabilities_GateTheMethodsThatDependOnThem(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name string
		mod  jiratest.CapMod
		key  jira.CapabilityKey
		call func(ctx context.Context, c *jiratest.Fake) error
	}{
		{"Plans without Administer Jira", jiratest.NoPlans, jira.CapPlans,
			func(ctx context.Context, c *jiratest.Fake) error { _, err := c.Plans(ctx); return err }},
		{"BulkMove without Bulk Change", jiratest.NoBulkMove, jira.CapBulkMove,
			func(ctx context.Context, c *jiratest.Fake) error {
				_, err := c.BulkMove(ctx, jira.MoveRequest{Keys: []string{"PROJ-1"}, TargetProjectKey: "PROJ"})
				return err
			}},
		{"Upload with attachments disabled", jiratest.NoAttachments, jira.CapAttachments,
			func(ctx context.Context, c *jiratest.Fake) error { _, err := c.Upload(ctx, "PROJ-1", nil); return err }},
		{"DeleteAttachment with attachments disabled", jiratest.NoAttachments, jira.CapAttachments,
			func(ctx context.Context, c *jiratest.Fake) error { return c.DeleteAttachment(ctx, "att-1") }},
		{"Boards with no board", jiratest.NoBoards, jira.CapBoards,
			func(ctx context.Context, c *jiratest.Fake) error { _, err := c.Boards(ctx, "PROJ"); return err }},
		{"BoardConfig with no board", jiratest.NoBoards, jira.CapBoards,
			func(ctx context.Context, c *jiratest.Fake) error { _, err := c.BoardConfig(ctx, 1); return err }},
		{"Sprints with no board", jiratest.NoBoards, jira.CapBoards,
			func(ctx context.Context, c *jiratest.Fake) error { _, err := c.Sprints(ctx, 1); return err }},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			c := fakeNewWithIssues(t, 2, jiratest.WithCapabilities(tc.mod))
			err := tc.call(t.Context(), c)
			var capErr *jira.CapabilityError
			if !errors.As(err, &capErr) {
				t.Fatalf("want a *jira.CapabilityError, got %#v", err)
			}
			if capErr.Capability != tc.key {
				t.Errorf("want the error to name %s, got %s", tc.key, capErr.Capability)
			}
			if capErr.Reason == "" {
				t.Error("a capability error must carry the probe's own reason")
			}
		})
	}
}

func TestCapReason_CarriesTheWordingItWasGiven(t *testing.T) {
	t.Parallel()
	c := fakeNewWithIssues(t, 1, jiratest.WithCapabilities(jiratest.CapReason(jira.CapPlans, "your token is read-only")))
	_, err := c.Plans(t.Context())
	var capErr *jira.CapabilityError
	if !errors.As(err, &capErr) {
		t.Fatalf("want a capability error, got %v", err)
	}
	if capErr.Reason != "your token is read-only" {
		t.Errorf("want the custom reason, got %q", capErr.Reason)
	}
}

func TestFailNext_ReturnsTheQueuedErrorToWhicheverCallComesNext(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name string
		err  error
		as   func(error) bool
	}{
		{"a 429", &jira.RateLimitError{RetryAfter: 12 * time.Second}, func(err error) bool {
			var e *jira.RateLimitError
			return errors.As(err, &e) && e.RetryAfter == 12*time.Second
		}},
		{"a 403", &jira.CapabilityError{Capability: jira.CapBulkMove, Reason: "no"}, func(err error) bool {
			var e *jira.CapabilityError
			return errors.As(err, &e)
		}},
		{"a transport failure", &jira.TransportError{Op: "GET /issue", Err: io.ErrUnexpectedEOF}, func(err error) bool {
			var e *jira.TransportError
			return errors.As(err, &e) && errors.Is(err, io.ErrUnexpectedEOF)
		}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			c := fakeNewWithIssues(t, 2)
			c.FailNext(tc.err)
			if _, err := c.Issue(t.Context(), "PROJ-1"); !tc.as(err) {
				t.Fatalf("want the queued error back, got %#v", err)
			}
			if _, err := c.Issue(t.Context(), "PROJ-1"); err != nil {
				t.Fatalf("the queue holds one error, the next call must succeed, got %v", err)
			}
		})
	}
}

func TestFailNextN_FailsExactlyThatManyCalls(t *testing.T) {
	t.Parallel()
	c := fakeNewWithIssues(t, 2)
	c.FailNextN(3, &jira.RateLimitError{})
	ctx := t.Context()
	for i := range 3 {
		if _, err := c.Me(ctx); err == nil {
			t.Fatalf("call %d must fail", i+1)
		}
	}
	if _, err := c.Me(ctx); err != nil {
		t.Fatalf("the fourth call must succeed, got %v", err)
	}
}

func TestDelay_ReturnsTheContextErrorInsteadOfSittingOutTheWait(t *testing.T) {
	t.Parallel()
	c := fakeNewWithIssues(t, 2)
	c.Delay(time.Hour)

	t.Run("cancelled while the call is in flight", func(t *testing.T) {
		t.Parallel()
		ctx, cancel := context.WithCancel(context.Background())
		done := make(chan error, 1)
		go func() {
			_, err := c.Issue(ctx, "PROJ-1")
			done <- err
		}()
		cancel()
		if err := <-done; !errors.Is(err, context.Canceled) {
			t.Fatalf("want context.Canceled, got %v", err)
		}
	})

	t.Run("deadline shorter than the delay", func(t *testing.T) {
		t.Parallel()
		ctx, cancel := context.WithTimeout(context.Background(), time.Millisecond)
		defer cancel()
		if _, err := c.Issue(ctx, "PROJ-1"); !errors.Is(err, context.DeadlineExceeded) {
			t.Fatalf("want context.DeadlineExceeded, got %v", err)
		}
	})
}

func TestEveryCall_ShortCircuitsOnAContextThatIsAlreadyDone(t *testing.T) {
	t.Parallel()
	c := fakeNewWithIssues(t, 2)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	calls := map[string]func() error{
		"Capabilities": func() error { _, err := c.Capabilities(ctx, "PROJ"); return err },
		"Search": func() error {
			_, err := c.Search(ctx, jira.Query{JQL: "project = PROJ", Fields: fakeNarrow})
			return err
		},
		"Issue":       func() error { _, err := c.Issue(ctx, "PROJ-1"); return err },
		"CreateIssue": func() error { _, err := c.CreateIssue(ctx, jira.IssueInput{}); return err },
		"UpdateIssue": func() error { return c.UpdateIssue(ctx, "PROJ-1", jira.IssuePatch{}) },
		"Comments":    func() error { _, err := c.Comments(ctx, "PROJ-1"); return err },
		"Sprints":     func() error { _, err := c.Sprints(ctx, 1); return err },
		"Download":    func() error { return c.Download(ctx, "att-1", io.Discard, jira.DownloadOptions{}) },
		"Me":          func() error { _, err := c.Me(ctx); return err },
	}
	for name, call := range calls {
		if err := call(); !errors.Is(err, context.Canceled) {
			t.Errorf("%s: want context.Canceled, got %v", name, err)
		}
	}
	if len(c.Calls()) != 0 {
		t.Errorf("a call that never started must not be recorded, got %v", c.Calls())
	}
}

func TestFake_IsSafeForConcurrentUse(t *testing.T) {
	t.Parallel()
	c := fakeNewWithIssues(t, 60, jiratest.WithPageSize(20))
	ctx := t.Context()
	board := fakeBoard(t, c)

	work := []func() error{
		func() error { _, err := c.Issue(ctx, "PROJ-3"); return err },
		func() error {
			_, err := c.Search(ctx, jira.Query{JQL: "project = PROJ", Fields: fakeNarrow})
			return err
		},
		func() error { return c.UpdateIssue(ctx, "PROJ-4", jira.IssuePatch{Labels: &[]string{"hot"}}) },
		func() error { _, err := c.AddComment(ctx, "PROJ-5", adf.NewDoc()); return err },
		func() error { _, err := c.Sprints(ctx, board.ID); return err },
		func() error { _, err := c.Capabilities(ctx, "PROJ"); return err },
		func() error {
			_, err := c.CreateIssue(ctx, jira.IssueInput{ProjectKey: "PROJ", IssueTypeID: "10301", Summary: "concurrent"})
			return err
		},
		func() error { _, err := c.Versions(ctx, "PROJ"); return err },
	}
	var wg sync.WaitGroup
	errs := make(chan error, len(work)*8)
	for range 8 {
		for _, fn := range work {
			wg.Add(1)
			go func() {
				defer wg.Done()
				if err := fn(); err != nil {
					errs <- err
				}
			}()
		}
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		t.Errorf("concurrent call failed: %v", err)
	}
	if got := len(c.Calls()); got < len(work)*8 {
		t.Errorf("want at least %d recorded calls, got %d", len(work)*8, got)
	}
}

func TestSprintLifecycle_AllowsOnlyFutureThenActiveThenClosed(t *testing.T) {
	t.Parallel()
	c := fakeNewWithIssues(t, 2)
	ctx := t.Context()
	board := fakeBoard(t, c)
	start := time.Date(2026, time.April, 1, 9, 0, 0, 0, time.UTC)
	end := start.AddDate(0, 0, 14)

	created, err := c.CreateSprint(ctx, jira.SprintInput{BoardID: board.ID, Name: "Sprint 4", Goal: "finish the port", Start: &start, End: &end})
	if err != nil {
		t.Fatalf("CreateSprint: %v", err)
	}
	if created.State != jira.SprintFuture {
		t.Fatalf("a new sprint starts in the future state, got %s", created.State)
	}
	if _, err := c.CompleteSprint(ctx, created.ID); err == nil {
		t.Fatal("a future sprint cannot be completed")
	}
	started, err := c.StartSprint(ctx, created.ID)
	if err != nil {
		t.Fatalf("StartSprint: %v", err)
	}
	if started.State != jira.SprintActive {
		t.Fatalf("want active after start, got %s", started.State)
	}
	if _, err := c.StartSprint(ctx, created.ID); err == nil {
		t.Fatal("an active sprint cannot be started again")
	}
	closed, err := c.CompleteSprint(ctx, created.ID)
	if err != nil {
		t.Fatalf("CompleteSprint: %v", err)
	}
	if closed.State != jira.SprintClosed || closed.Complete == nil {
		t.Fatalf("want a closed sprint with a completion time, got %s %v", closed.State, closed.Complete)
	}
	if _, err := c.CompleteSprint(ctx, created.ID); err == nil {
		t.Fatal("a closed sprint cannot be completed twice")
	}
}

func TestStartSprint_RefusesUnlessBothDatesAreSet(t *testing.T) {
	t.Parallel()
	when := time.Date(2026, time.April, 1, 9, 0, 0, 0, time.UTC)
	cases := []struct {
		name       string
		start, end *time.Time
		wantFields []string
	}{
		{"neither date", nil, nil, []string{"startDate", "endDate"}},
		{"no end date", &when, nil, []string{"endDate"}},
		{"no start date", nil, &when, []string{"startDate"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			c := fakeNewWithIssues(t, 1)
			ctx := t.Context()
			board := fakeBoard(t, c)
			sp, err := c.CreateSprint(ctx, jira.SprintInput{BoardID: board.ID, Name: "Sprint 4", Start: tc.start, End: tc.end})
			if err != nil {
				t.Fatalf("CreateSprint: %v", err)
			}
			_, err = c.StartSprint(ctx, sp.ID)
			var ve *jira.ValidationError
			if !errors.As(err, &ve) {
				t.Fatalf("want a validation error, got %v", err)
			}
			for _, field := range tc.wantFields {
				if _, ok := ve.For(field); !ok {
					t.Errorf("want the error to name %s, got %v", field, ve)
				}
			}
		})
	}
}

// TestUpdateSprint_TouchesOnlyTheFieldsThePatchNames is the reason the port has
// no raw PUT: the endpoint underneath nulls everything it is not sent.
func TestUpdateSprint_TouchesOnlyTheFieldsThePatchNames(t *testing.T) {
	t.Parallel()
	c := fakeNewWithIssues(t, 1)
	ctx := t.Context()
	board := fakeBoard(t, c)

	before := fakeSprintsOf(t, c, board.ID)[1]
	if before.Goal == "" || before.Start == nil || before.End == nil {
		t.Fatalf("the fixture sprint must start out fully populated, got %+v", before)
	}
	renamed := "Sprint two, renamed"
	after, err := c.UpdateSprint(ctx, before.ID, jira.SprintPatch{Name: &renamed})
	if err != nil {
		t.Fatalf("UpdateSprint: %v", err)
	}
	if after.Name != renamed {
		t.Errorf("want the new name, got %q", after.Name)
	}
	if after.Goal != before.Goal {
		t.Errorf("the goal must survive a name-only patch, got %q want %q", after.Goal, before.Goal)
	}
	if after.Start == nil || !after.Start.Equal(*before.Start) {
		t.Errorf("the start date must survive a name-only patch, got %v", after.Start)
	}
	if after.End == nil || !after.End.Equal(*before.End) {
		t.Errorf("the end date must survive a name-only patch, got %v", after.End)
	}
	if after.State != before.State {
		t.Errorf("the state must survive a name-only patch, got %s", after.State)
	}
}

func TestUpdateSprint_RefusesDatesOnAClosedSprint(t *testing.T) {
	t.Parallel()
	c := fakeNewWithIssues(t, 1)
	ctx := t.Context()
	board := fakeBoard(t, c)
	closed := fakeSprintsOf(t, c, board.ID)[0]
	if closed.State != jira.SprintClosed {
		t.Fatalf("want the closed fixture sprint first, got %s", closed.State)
	}

	when := time.Date(2026, time.May, 1, 9, 0, 0, 0, time.UTC)
	_, err := c.UpdateSprint(ctx, closed.ID, jira.SprintPatch{Start: &when})
	var ve *jira.ValidationError
	if !errors.As(err, &ve) {
		t.Fatalf("want a validation error, got %v", err)
	}
	if _, ok := ve.For("startDate"); !ok {
		t.Errorf("want the error to name startDate, got %v", ve)
	}
	goal := "retrospective notes"
	if _, err := c.UpdateSprint(ctx, closed.ID, jira.SprintPatch{Goal: &goal}); err != nil {
		t.Errorf("a closed sprint still accepts name and goal, got %v", err)
	}
}

func TestMoveToSprint_RecordsMembershipAndRefusesMoreThanFiftyAtOnce(t *testing.T) {
	t.Parallel()
	c := fakeNewWithIssues(t, 60)
	ctx := t.Context()
	board := fakeBoard(t, c)
	sprint := fakeSprintsOf(t, c, board.ID)[2]

	fields, err := c.Fields(ctx)
	if err != nil {
		t.Fatalf("Fields: %v", err)
	}
	sprintField, ok := jira.FieldByName(fields, "Sprint")
	if !ok {
		t.Fatal("the catalogue must carry a sprint field")
	}
	if err := c.MoveToSprint(ctx, sprint.ID, []string{"PROJ-1", "PROJ-2"}); err != nil {
		t.Fatalf("MoveToSprint: %v", err)
	}
	iss, err := c.Issue(ctx, "PROJ-1")
	if err != nil {
		t.Fatalf("Issue: %v", err)
	}
	if opts, present := iss.Fields.Options(sprintField.Ref()); !present || len(opts) != 1 || opts[0].Label != sprint.Name {
		t.Fatalf("want the sprint recorded on the issue, got %v", opts)
	}
	if err := c.MoveToBacklog(ctx, []string{"PROJ-1"}); err != nil {
		t.Fatalf("MoveToBacklog: %v", err)
	}
	if iss, err = c.Issue(ctx, "PROJ-1"); err != nil {
		t.Fatalf("Issue: %v", err)
	}
	if _, present := iss.Fields.Get(sprintField.Ref()); present {
		t.Error("the backlog move must take the sprint field off the issue")
	}

	tooMany := make([]string, 0, 51)
	for i := 1; i <= 51; i++ {
		tooMany = append(tooMany, "PROJ-"+strconv.Itoa(i))
	}
	var ve *jira.ValidationError
	if err := c.MoveToSprint(ctx, sprint.ID, tooMany); !errors.As(err, &ve) {
		t.Fatalf("want a validation error past the 50-issue cap, got %v", err)
	}
}

func TestReleaseVersion_HandlesEveryUnresolvedPolicy(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name   string
		policy jira.UnresolvedPolicy
		check  func(t *testing.T, before, after []jira.Issue, from, to jira.Version)
	}{
		{
			name:   "MoveUnresolved carries the open issues to another version",
			policy: jira.MoveUnresolved,
			check: func(t *testing.T, before, after []jira.Issue, from, to jira.Version) {
				for i, iss := range after {
					if before[i].Status.Category == jira.CategoryDone {
						if !fakeHasVersion(iss.FixVersions, from.ID) {
							t.Errorf("%s is done, its fix version must be left alone", iss.Key)
						}
						continue
					}
					if fakeHasVersion(iss.FixVersions, from.ID) {
						t.Errorf("%s is open, it must have left %s", iss.Key, from.Name)
					}
					if !fakeHasVersion(iss.FixVersions, to.ID) {
						t.Errorf("%s is open, it must have landed on %s", iss.Key, to.Name)
					}
				}
			},
		},
		{
			name:   "StripUnresolved takes the version off the open issues",
			policy: jira.StripUnresolved,
			check: func(t *testing.T, before, after []jira.Issue, from, _ jira.Version) {
				for i, iss := range after {
					want := before[i].Status.Category == jira.CategoryDone
					if got := fakeHasVersion(iss.FixVersions, from.ID); got != want {
						t.Errorf("%s: carries %s = %v, want %v", iss.Key, from.Name, got, want)
					}
				}
			},
		},
		{
			name:   "ReleaseAnyway leaves every issue where it is",
			policy: jira.ReleaseAnyway,
			check: func(t *testing.T, _, after []jira.Issue, from, _ jira.Version) {
				for _, iss := range after {
					if !fakeHasVersion(iss.FixVersions, from.ID) {
						t.Errorf("%s must keep %s", iss.Key, from.Name)
					}
				}
			},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			c := fakeNewWithIssues(t, 24, jiratest.WithPageSize(100))
			ctx := t.Context()
			from := fakeVersionByName(t, c, "2.0")
			to := fakeVersionByName(t, c, "3.0")

			before := fakeIssuesOnVersion(t, c, from.ID)
			if len(before) < 3 {
				t.Fatalf("the fixture needs several issues on %s, got %d", from.Name, len(before))
			}
			released, err := c.ReleaseVersion(ctx, from.ID, jira.ReleaseInput{
				Unresolved:      tc.policy,
				MoveToVersionID: to.ID,
				ReleaseDate:     jira.Date{Year: 2026, Month: time.March, Day: 2},
			})
			if err != nil {
				t.Fatalf("ReleaseVersion: %v", err)
			}
			if !released.Released {
				t.Error("the version must come back released")
			}
			if released.ReleaseDate.String() != "2026-03-02" {
				t.Errorf("want the release date the caller gave, got %s", released.ReleaseDate)
			}
			after := make([]jira.Issue, 0, len(before))
			for _, iss := range before {
				got, err := c.Issue(ctx, iss.Key)
				if err != nil {
					t.Fatalf("Issue(%s): %v", iss.Key, err)
				}
				after = append(after, got)
			}
			tc.check(t, before, after, from, to)
		})
	}
}

func TestReleaseVersion_RefusesToMoveOpenIssuesOntoTheVersionBeingReleased(t *testing.T) {
	t.Parallel()
	c := fakeNewWithIssues(t, 12)
	ctx := t.Context()
	version := fakeVersionByName(t, c, "2.0")

	_, err := c.ReleaseVersion(ctx, version.ID, jira.ReleaseInput{Unresolved: jira.MoveUnresolved, MoveToVersionID: version.ID})
	var ve *jira.ValidationError
	if !errors.As(err, &ve) {
		t.Fatalf("want a validation error, got %v", err)
	}
	if _, ok := ve.For("moveToVersionId"); !ok {
		t.Errorf("want the error to name moveToVersionId, got %v", ve)
	}
}

func TestReleaseVersion_RefusesAnUnknownDestinationVersion(t *testing.T) {
	t.Parallel()
	c := fakeNewWithIssues(t, 12)
	version := fakeVersionByName(t, c, "2.0")

	_, err := c.ReleaseVersion(t.Context(), version.ID, jira.ReleaseInput{Unresolved: jira.MoveUnresolved, MoveToVersionID: "ver-nope"})
	var nf *jira.NotFoundError
	if !errors.As(err, &nf) {
		t.Fatalf("want a not-found error, got %v", err)
	}
}

func TestUnresolvedCount_CountsOnlyIssuesOutsideTheDoneCategory(t *testing.T) {
	t.Parallel()
	c := fakeNewWithIssues(t, 24, jiratest.WithPageSize(100))
	ctx := t.Context()
	version := fakeVersionByName(t, c, "2.0")

	on := fakeIssuesOnVersion(t, c, version.ID)
	want := 0
	for _, iss := range on {
		if iss.Status.Category != jira.CategoryDone {
			want++
		}
	}
	if want == 0 || want == len(on) {
		t.Fatalf("the fixture must mix done and open issues on the version, got %d of %d open", want, len(on))
	}
	got, err := c.UnresolvedCount(ctx, version.ID)
	if err != nil {
		t.Fatalf("UnresolvedCount: %v", err)
	}
	if got != want {
		t.Errorf("want %d unresolved, got %d", want, got)
	}
}

// TestUpdateIssue_LeavesWritesAndClearsInTheSameCall covers all three arms of
// the sparse patch at once, because it is their interaction that goes wrong.
func TestUpdateIssue_LeavesWritesAndClearsInTheSameCall(t *testing.T) {
	t.Parallel()
	c := fakeNewWithIssues(t, 6)
	ctx := t.Context()

	fields, err := c.Fields(ctx)
	if err != nil {
		t.Fatalf("Fields: %v", err)
	}
	points, ok := jira.FieldByName(fields, "Story Points")
	if !ok {
		t.Fatal("the catalogue must carry story points")
	}
	assignee := "acct-grace"
	if err := c.UpdateIssue(ctx, "PROJ-1", jira.IssuePatch{Assignee: &assignee}); err != nil {
		t.Fatalf("seeding the assignee: %v", err)
	}
	var seed jira.FieldSet
	seed = seed.With(points.Ref(), jira.FieldValue{Kind: jira.KindNumber, Number: 8})
	if err := c.UpdateIssue(ctx, "PROJ-1", jira.IssuePatch{Fields: seed}); err != nil {
		t.Fatalf("seeding story points: %v", err)
	}
	before, err := c.Issue(ctx, "PROJ-1")
	if err != nil {
		t.Fatalf("Issue: %v", err)
	}

	summary := "Rewritten by the patch"
	if err := c.UpdateIssue(ctx, "PROJ-1", jira.IssuePatch{
		Summary: &summary,
		Clear:   []jira.FieldRef{points.Ref()},
	}); err != nil {
		t.Fatalf("UpdateIssue: %v", err)
	}
	after, err := c.Issue(ctx, "PROJ-1")
	if err != nil {
		t.Fatalf("Issue: %v", err)
	}
	if after.Summary != summary {
		t.Errorf("a set pointer must write: got %q", after.Summary)
	}
	if after.Assignee == nil || after.Assignee.AccountID != "acct-grace" {
		t.Errorf("a nil pointer must leave the field alone: got %v", after.Assignee)
	}
	if _, present := after.Fields.Get(points.Ref()); present {
		t.Error("a field named in Clear must be gone")
	}
	if _, present := before.Fields.Get(points.Ref()); !present {
		t.Error("the fixture must have had story points before the clear")
	}
	if !after.Updated.After(before.Created) {
		t.Errorf("an update must stamp Updated, got %s", after.Updated)
	}
}

func TestUpdateIssue_ClearsSystemFieldsAsWellAsCustomOnes(t *testing.T) {
	t.Parallel()
	c := fakeNewWithIssues(t, 6)
	ctx := t.Context()
	assignee := "acct-ada"
	if err := c.UpdateIssue(ctx, "PROJ-2", jira.IssuePatch{Assignee: &assignee}); err != nil {
		t.Fatalf("seeding: %v", err)
	}
	if err := c.UpdateIssue(ctx, "PROJ-2", jira.IssuePatch{Clear: []jira.FieldRef{{ID: "assignee"}}}); err != nil {
		t.Fatalf("UpdateIssue: %v", err)
	}
	iss, err := c.Issue(ctx, "PROJ-2")
	if err != nil {
		t.Fatalf("Issue: %v", err)
	}
	if iss.Assignee != nil {
		t.Errorf("clearing the assignee must unassign the issue, got %v", iss.Assignee)
	}
}

func TestCreateIssue_RefusesWhatTheCreateScreenWouldRefuse(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name      string
		in        jira.IssueInput
		wantField string
	}{
		{"no summary", jira.IssueInput{ProjectKey: "PROJ", IssueTypeID: "10301"}, "summary"},
		{"blank summary", jira.IssueInput{ProjectKey: "PROJ", IssueTypeID: "10301", Summary: "   "}, "summary"},
		{"unknown project", jira.IssueInput{ProjectKey: "NOPE", IssueTypeID: "10301", Summary: "x"}, "project"},
		{"unknown issue type", jira.IssueInput{ProjectKey: "PROJ", IssueTypeID: "999999", Summary: "x"}, "issuetype"},
		{"unknown parent", jira.IssueInput{ProjectKey: "PROJ", IssueTypeID: "10301", Summary: "x", ParentKey: "PROJ-9999"}, "parent"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			c := fakeNewWithIssues(t, 3)
			_, err := c.CreateIssue(t.Context(), tc.in)
			var ve *jira.ValidationError
			if !errors.As(err, &ve) {
				t.Fatalf("want a validation error, got %#v", err)
			}
			if _, ok := ve.For(tc.wantField); !ok {
				t.Errorf("want the error to name %s, got %v", tc.wantField, ve)
			}
		})
	}
}

func TestCreateIssue_StoresTheIssueUnderTheNextKeyInTheProject(t *testing.T) {
	t.Parallel()
	c := fakeNewWithIssues(t, 3, jiratest.WithNow(time.Date(2026, time.June, 1, 12, 0, 0, 0, time.UTC)))
	ctx := t.Context()

	created, err := c.CreateIssue(ctx, jira.IssueInput{
		ProjectKey: "PROJ", IssueTypeID: "10302", Summary: "Something new", Labels: []string{"triage"},
	})
	if err != nil {
		t.Fatalf("CreateIssue: %v", err)
	}
	if created.Key != "PROJ-4" {
		t.Errorf("want PROJ-4 after three seeded issues, got %s", created.Key)
	}
	if !created.Created.Equal(time.Date(2026, time.June, 1, 12, 0, 0, 0, time.UTC)) {
		t.Errorf("want the injected clock on Created, got %s", created.Created)
	}
	if created.Status.Category != jira.CategoryToDo {
		t.Errorf("a new issue starts in the to-do category, got %s", created.Status.Category)
	}
	round, err := c.Issue(ctx, "PROJ-4")
	if err != nil {
		t.Fatalf("Issue: %v", err)
	}
	if round.Summary != created.Summary {
		t.Errorf("the created issue must be readable back, got %q", round.Summary)
	}
}

func TestTransition_MovesTheIssueAndEnforcesTheScreenItAdvertises(t *testing.T) {
	t.Parallel()
	c := fakeNewWithIssues(t, 4)
	ctx := t.Context()

	transitions, err := c.Transitions(ctx, "PROJ-1")
	if err != nil {
		t.Fatalf("Transitions: %v", err)
	}
	var done jira.Transition
	for _, tr := range transitions {
		if tr.To.Category == jira.CategoryDone {
			done = tr
		}
		if tr.To.ID == "" {
			t.Fatalf("every transition must name a target status, got %+v", tr)
		}
	}
	if !done.HasScreen || len(done.Fields) == 0 {
		t.Fatalf("the transition into done must advertise its screen, got %+v", done)
	}
	if err := c.Transition(ctx, "PROJ-1", done.ID, jira.IssuePatch{}); err == nil {
		t.Fatal("the screen names a required resolution, the empty patch must be refused")
	}

	var screen jira.FieldSet
	screen = screen.With(done.Fields[0].Field, jira.FieldValue{
		Kind:    jira.KindOption,
		Options: []jira.Option{done.Fields[0].AllowedValues[0]},
	})
	if err := c.Transition(ctx, "PROJ-1", done.ID, jira.IssuePatch{Fields: screen}); err != nil {
		t.Fatalf("Transition: %v", err)
	}
	iss, err := c.Issue(ctx, "PROJ-1")
	if err != nil {
		t.Fatalf("Issue: %v", err)
	}
	if iss.Status.Category != jira.CategoryDone {
		t.Errorf("want the issue in the done category, got %s", iss.Status.Category)
	}
	if iss.Resolved == nil || iss.Resolution == nil {
		t.Errorf("a done issue carries a resolution and a resolved time, got %v %v", iss.Resolution, iss.Resolved)
	}
}

func TestUploadAndDownload_RoundTripDeterministicBytes(t *testing.T) {
	t.Parallel()
	c := fakeNewWithIssues(t, 2)
	ctx := t.Context()

	body := strings.Repeat("saral", 2000)
	opens := 0
	file := jira.FileRef{
		Name: "notes.txt",
		Size: int64(len(body)),
		Open: func() (io.ReadCloser, error) {
			opens++
			return io.NopCloser(strings.NewReader(body)), nil
		},
	}
	added, err := c.Upload(ctx, "PROJ-1", []jira.FileRef{file})
	if err != nil {
		t.Fatalf("Upload: %v", err)
	}
	if opens != 1 {
		t.Errorf("Upload must open each file exactly once, got %d", opens)
	}
	if len(added) != 1 || added[0].Size != int64(len(body)) || added[0].MimeType != "text/plain" {
		t.Fatalf("want one text attachment sized %d, got %+v", len(body), added)
	}
	listed, err := c.Attachments(ctx, "PROJ-1")
	if err != nil {
		t.Fatalf("Attachments: %v", err)
	}
	if len(listed) != 1 || listed[0].ID != added[0].ID {
		t.Fatalf("the upload must show up in the listing, got %+v", listed)
	}

	var first, second bytes.Buffer
	var progress []int64
	if err := c.Download(ctx, added[0].ID, &first, jira.DownloadOptions{Progress: func(n int64) { progress = append(progress, n) }}); err != nil {
		t.Fatalf("Download: %v", err)
	}
	if err := c.Download(ctx, added[0].ID, &second, jira.DownloadOptions{}); err != nil {
		t.Fatalf("Download: %v", err)
	}
	if first.Len() != len(body) {
		t.Errorf("want %d bytes downloaded, got %d", len(body), first.Len())
	}
	if !bytes.Equal(first.Bytes(), second.Bytes()) {
		t.Error("two downloads of one attachment must produce the same bytes")
	}
	if len(progress) < 2 {
		t.Fatalf("want progress reported in chunks, got %v", progress)
	}
	for i := 1; i < len(progress); i++ {
		if progress[i] <= progress[i-1] {
			t.Fatalf("progress must be cumulative and increasing, got %v", progress)
		}
	}
	if progress[len(progress)-1] != int64(len(body)) {
		t.Errorf("the last progress call must report the whole file, got %d", progress[len(progress)-1])
	}

	if err := c.DeleteAttachment(ctx, added[0].ID); err != nil {
		t.Fatalf("DeleteAttachment: %v", err)
	}
	if listed, err = c.Attachments(ctx, "PROJ-1"); err != nil || len(listed) != 0 {
		t.Fatalf("want an empty listing after the delete, got %+v (%v)", listed, err)
	}
}

func TestBulkMove_WalksItsTaskToCompletionAndThenMovesTheIssues(t *testing.T) {
	t.Parallel()
	c := jiratest.New(
		jiratest.WithProject("PROJ", jiratest.Scrum),
		jiratest.WithProject("OTHER", jiratest.Kanban),
		jiratest.WithIssues(jiratest.Gen(4)),
	)
	ctx := t.Context()

	ref, err := c.BulkMove(ctx, jira.MoveRequest{Keys: []string{"PROJ-1"}, TargetProjectKey: "OTHER", TargetIssueTypeID: "10303"})
	if err != nil {
		t.Fatalf("BulkMove: %v", err)
	}
	if ref.ID == "" || ref.URL == "" {
		t.Fatalf("a task reference must carry an id and the URL to poll, got %+v", ref)
	}
	wantStates := []jira.TaskState{jira.TaskEnqueued, jira.TaskRunning, jira.TaskComplete}
	for i, want := range wantStates {
		status, err := c.Task(ctx, ref)
		if err != nil {
			t.Fatalf("Task poll %d: %v", i, err)
		}
		if status.State != want {
			t.Fatalf("poll %d: want %s, got %s", i, want, status.State)
		}
	}
	if _, err := c.Issue(ctx, "PROJ-1"); err == nil {
		t.Error("the moved issue must no longer answer to its old key")
	}
	moved, err := c.Issue(ctx, "OTHER-1")
	if err != nil {
		t.Fatalf("the moved issue must answer to its new key: %v", err)
	}
	if moved.Project.Key != "OTHER" || moved.Type.ID != "10303" {
		t.Errorf("want the issue in OTHER as the target type, got %s / %s", moved.Project.Key, moved.Type.Name)
	}
}

func TestBulkMove_ReportsAFailedTaskWithTheKeysItCouldNotMove(t *testing.T) {
	t.Parallel()
	c := jiratest.New(
		jiratest.WithProject("PROJ", jiratest.Scrum),
		jiratest.WithProject("OTHER", jiratest.Kanban),
		jiratest.WithIssues(jiratest.Gen(4)),
	)
	ctx := t.Context()
	c.FailNextTask()

	ref, err := c.BulkMove(ctx, jira.MoveRequest{Keys: []string{"PROJ-2"}, TargetProjectKey: "OTHER", TargetIssueTypeID: "10303"})
	if err != nil {
		t.Fatalf("BulkMove: %v", err)
	}
	var status jira.TaskStatus
	for range 3 {
		if status, err = c.Task(ctx, ref); err != nil {
			t.Fatalf("Task: %v", err)
		}
	}
	if status.State != jira.TaskFailed || !status.State.Done() {
		t.Fatalf("want a failed task, got %s", status.State)
	}
	if len(status.Failed) != 1 || status.Failed[0] != "PROJ-2" {
		t.Errorf("a failed task must name the keys it could not move, got %v", status.Failed)
	}
	if _, err := c.Issue(ctx, "PROJ-2"); err != nil {
		t.Errorf("a failed move must leave the issue alone, got %v", err)
	}
}

func TestUnknownKeysAndIDs_ComeBackAsNotFound(t *testing.T) {
	t.Parallel()
	c := fakeNewWithIssues(t, 3)
	ctx := t.Context()
	calls := map[string]func() error{
		"Issue":            func() error { _, err := c.Issue(ctx, "PROJ-999"); return err },
		"UpdateIssue":      func() error { return c.UpdateIssue(ctx, "PROJ-999", jira.IssuePatch{}) },
		"Transitions":      func() error { _, err := c.Transitions(ctx, "PROJ-999"); return err },
		"Comments":         func() error { _, err := c.Comments(ctx, "PROJ-999"); return err },
		"AddComment":       func() error { _, err := c.AddComment(ctx, "PROJ-999", adf.NewDoc()); return err },
		"DeleteComment":    func() error { return c.DeleteComment(ctx, "PROJ-1", "cmt-999") },
		"Attachments":      func() error { _, err := c.Attachments(ctx, "PROJ-999"); return err },
		"DeleteAttachment": func() error { return c.DeleteAttachment(ctx, "att-999") },
		"Download":         func() error { return c.Download(ctx, "att-999", io.Discard, jira.DownloadOptions{}) },
		"Versions":         func() error { _, err := c.Versions(ctx, "NOPE"); return err },
		"UnresolvedCount":  func() error { _, err := c.UnresolvedCount(ctx, "ver-999"); return err },
		"ReleaseVersion":   func() error { _, err := c.ReleaseVersion(ctx, "ver-999", jira.ReleaseInput{}); return err },
		"BoardConfig":      func() error { _, err := c.BoardConfig(ctx, 999999); return err },
		"Sprints":          func() error { _, err := c.Sprints(ctx, 999999); return err },
		"StartSprint":      func() error { _, err := c.StartSprint(ctx, 999999); return err },
		"CompleteSprint":   func() error { _, err := c.CompleteSprint(ctx, 999999); return err },
		"UpdateSprint":     func() error { _, err := c.UpdateSprint(ctx, 999999, jira.SprintPatch{}); return err },
		"CreateSprint":     func() error { _, err := c.CreateSprint(ctx, jira.SprintInput{BoardID: 999999}); return err },
		"MoveToSprint":     func() error { return c.MoveToSprint(ctx, 999999, nil) },
		"MoveToBacklog":    func() error { return c.MoveToBacklog(ctx, []string{"PROJ-999"}) },
		"CreateMeta":       func() error { _, err := c.CreateMeta(ctx, "NOPE", "10301"); return err },
		"Task":             func() error { _, err := c.Task(ctx, jira.TaskRef{ID: "task-999"}); return err },
	}
	for name, call := range calls {
		var nf *jira.NotFoundError
		if err := call(); !errors.As(err, &nf) {
			t.Errorf("%s: want a *jira.NotFoundError, got %#v", name, err)
		}
	}
}

func TestCommentsAndBoardConfig_ReadBackWhatTheSiteHolds(t *testing.T) {
	t.Parallel()
	c := fakeNewWithIssues(t, 3, jiratest.WithPageSize(2))
	ctx := t.Context()

	for i := range 3 {
		if _, err := c.AddComment(ctx, "PROJ-1", adf.NewDoc(adf.NewNode("paragraph", adf.NewText("note "+strconv.Itoa(i))))); err != nil {
			t.Fatalf("AddComment: %v", err)
		}
	}
	page, err := c.Comments(ctx, "PROJ-1")
	if err != nil {
		t.Fatalf("Comments: %v", err)
	}
	all, err := jira.Collect(ctx, page, 0)
	if err != nil {
		t.Fatalf("Collect: %v", err)
	}
	if len(all) != 3 {
		t.Fatalf("want three comments across two pages, got %d", len(all))
	}
	if err := c.DeleteComment(ctx, "PROJ-1", all[1].ID); err != nil {
		t.Fatalf("DeleteComment: %v", err)
	}

	board := fakeBoard(t, c)
	cfg, err := c.BoardConfig(ctx, board.ID)
	if err != nil {
		t.Fatalf("BoardConfig: %v", err)
	}
	if len(cfg.Columns) != 3 {
		t.Fatalf("want one column per status category, got %d", len(cfg.Columns))
	}
	if cfg.Estimation.Type != jira.EstimationField || cfg.Estimation.Field.ID == "" {
		t.Errorf("a scrum board estimates in a field, got %+v", cfg.Estimation)
	}
	if cfg.RankFieldID == "" {
		t.Error("the board config must report the rank field")
	}
}

func TestCreateMeta_MarksTheFieldsTheCreateScreenRequires(t *testing.T) {
	t.Parallel()
	c := fakeNewWithIssues(t, 2)
	schema, err := c.CreateMeta(t.Context(), "PROJ", "10301")
	if err != nil {
		t.Fatalf("CreateMeta: %v", err)
	}
	required := map[string]bool{}
	for _, meta := range schema.Required() {
		required[meta.Field.ID] = true
	}
	for _, id := range []string{"summary", "issuetype", "project"} {
		if !required[id] {
			t.Errorf("%s must be required on the create screen", id)
		}
	}
	if required["description"] {
		t.Error("description must not be required")
	}
}

func TestGen_IsIdenticalOnEveryRun(t *testing.T) {
	t.Parallel()
	if !reflect.DeepEqual(jiratest.Gen(120), jiratest.Gen(120)) {
		t.Fatal("Gen must produce the same issues every time it is called")
	}
	if !reflect.DeepEqual(jiratest.GenFor("OTHER", 30), jiratest.GenFor("OTHER", 30)) {
		t.Fatal("GenFor must produce the same issues every time it is called")
	}
	if reflect.DeepEqual(jiratest.Gen(30), jiratest.GenFor("OTHER", 30)) {
		t.Fatal("two projects must not generate the same issues")
	}
}

func TestGen_LooksEnoughLikeRealDataToRender(t *testing.T) {
	t.Parallel()
	issues := jiratest.Gen(60)
	if len(issues) != 60 {
		t.Fatalf("want 60 issues, got %d", len(issues))
	}
	categories := map[jira.StatusCategory]int{}
	types := map[string]int{}
	priorities := map[string]int{}
	unassigned, labelled, parented, described := 0, 0, 0, 0
	for i, iss := range issues {
		if iss.Key != "PROJ-"+strconv.Itoa(i+1) {
			t.Fatalf("want keys PROJ-1..PROJ-60, got %s at index %d", iss.Key, i)
		}
		categories[iss.Status.Category]++
		types[iss.Type.Name]++
		if iss.Priority != nil {
			priorities[iss.Priority.Name]++
		}
		if iss.Assignee == nil {
			unassigned++
		}
		if len(iss.Labels) > 0 {
			labelled++
		}
		if iss.Parent != nil {
			parented++
		}
		if counts := iss.Description.NodeTypes(); counts["paragraph"] > 0 && counts["bulletList"] > 0 {
			described++
		}
		if !iss.Created.Before(iss.Updated) {
			t.Fatalf("%s: Updated must follow Created, got %s and %s", iss.Key, iss.Created, iss.Updated)
		}
	}
	if len(categories) != 3 {
		t.Errorf("want every status category represented, got %v", categories)
	}
	if len(types) < 3 || len(priorities) < 3 {
		t.Errorf("want the issue types and priorities to rotate, got %v and %v", types, priorities)
	}
	if unassigned == 0 || unassigned == len(issues) {
		t.Errorf("want some issues unassigned and some not, got %d of %d", unassigned, len(issues))
	}
	if labelled == 0 || parented == 0 {
		t.Errorf("want some labels and some parents, got %d labelled and %d parented", labelled, parented)
	}
	if described != len(issues) {
		t.Errorf("every issue needs a paragraph and a bullet list, got %d of %d", described, len(issues))
	}
}

func TestGen_HangsChildrenOffIssuesThatExist(t *testing.T) {
	t.Parallel()
	issues := jiratest.Gen(60)
	known := map[string]bool{}
	for _, iss := range issues {
		known[iss.Key] = true
	}
	for _, iss := range issues {
		if iss.Parent != nil && !known[iss.Parent.Key] {
			t.Errorf("%s points at a parent that does not exist: %s", iss.Key, iss.Parent.Key)
		}
	}
}

func TestCalls_RecordEveryCallInOrderIncludingTheFailedOnes(t *testing.T) {
	t.Parallel()
	c := fakeNewWithIssues(t, 2)
	ctx := t.Context()
	c.FailNext(&jira.RateLimitError{})
	if _, err := c.Me(ctx); err == nil {
		t.Fatal("the queued error must land")
	}
	if _, err := c.Issue(ctx, "PROJ-1"); err != nil {
		t.Fatalf("Issue: %v", err)
	}
	if _, err := c.Capabilities(ctx, "PROJ"); err != nil {
		t.Fatalf("Capabilities: %v", err)
	}
	want := []string{"Me", "Issue", "Capabilities"}
	if got := c.Calls(); !reflect.DeepEqual(got, want) {
		t.Errorf("want %v, got %v", want, got)
	}
}

func TestReset_PutsTheFakeBackToHowNewLeftIt(t *testing.T) {
	t.Parallel()
	c := fakeNewWithIssues(t, 4)
	ctx := t.Context()

	summary := "changed"
	if err := c.UpdateIssue(ctx, "PROJ-1", jira.IssuePatch{Summary: &summary}); err != nil {
		t.Fatalf("UpdateIssue: %v", err)
	}
	if _, err := c.CreateIssue(ctx, jira.IssueInput{ProjectKey: "PROJ", IssueTypeID: "10301", Summary: "extra"}); err != nil {
		t.Fatalf("CreateIssue: %v", err)
	}
	c.FailNext(&jira.RateLimitError{})
	c.Delay(time.Hour)
	c.Reset()

	if got := c.Calls(); len(got) != 0 {
		t.Errorf("Reset must clear the call log, got %v", got)
	}
	iss, err := c.Issue(ctx, "PROJ-1")
	if err != nil {
		t.Fatalf("Issue after reset: %v", err)
	}
	if iss.Summary == summary {
		t.Error("Reset must undo the edit")
	}
	if _, err := c.Issue(ctx, "PROJ-5"); err == nil {
		t.Error("Reset must forget the created issue")
	}
}

func TestWithFields_TakesTheEstimationFieldAwayWithTheCatalogueEntry(t *testing.T) {
	t.Parallel()
	c := jiratest.New(
		jiratest.WithProject("PROJ", jiratest.Scrum),
		jiratest.WithFields([]jira.Field{{ID: "summary", Key: "summary", Name: "Summary"}}),
	)
	ctx := t.Context()
	board := fakeBoard(t, c)
	cfg, err := c.BoardConfig(ctx, board.ID)
	if err != nil {
		t.Fatalf("BoardConfig: %v", err)
	}
	if cfg.Estimation.Type != jira.EstimationNone {
		t.Errorf("a site with no story points field cannot estimate in one, got %+v", cfg.Estimation)
	}
	if cfg.RankFieldID != "" {
		t.Errorf("a site with no rank field must report none, got %q", cfg.RankFieldID)
	}
}

func TestWithMe_IsWhoCurrentUserResolvesTo(t *testing.T) {
	t.Parallel()
	me := jira.User{AccountID: "acct-ada", DisplayName: "Ada Lovelace", TimeZone: time.UTC, Active: true}
	c := fakeNewWithIssues(t, 20, jiratest.WithMe(me), jiratest.WithPageSize(100))
	ctx := t.Context()

	got, err := c.Me(ctx)
	if err != nil {
		t.Fatalf("Me: %v", err)
	}
	if got.AccountID != me.AccountID {
		t.Fatalf("want the injected account, got %s", got.AccountID)
	}
	page, err := c.Search(ctx, jira.Query{JQL: `assignee = currentUser()`, Fields: fakeNarrow})
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if len(page.Items) == 0 {
		t.Fatal("want the issues assigned to the current user")
	}
	for _, iss := range page.Items {
		if iss.Assignee == nil || iss.Assignee.AccountID != me.AccountID {
			t.Fatalf("%s is not assigned to the current user", iss.Key)
		}
	}
}

func TestFake_ReturnsCopiesSoACallerCannotEditTheStore(t *testing.T) {
	t.Parallel()
	c := fakeNewWithIssues(t, 3)
	ctx := t.Context()

	iss, err := c.Issue(ctx, "PROJ-2")
	if err != nil {
		t.Fatalf("Issue: %v", err)
	}
	iss.Summary = "mutated by the caller"
	iss.Labels = append(iss.Labels, "mutated")
	if iss.Assignee != nil {
		iss.Assignee.DisplayName = "mutated"
	}
	again, err := c.Issue(ctx, "PROJ-2")
	if err != nil {
		t.Fatalf("Issue: %v", err)
	}
	if again.Summary == iss.Summary {
		t.Error("editing a returned issue must not reach the store")
	}
	if fakeContainsFold(again.Labels, "mutated") {
		t.Error("editing a returned label slice must not reach the store")
	}
	if again.Assignee != nil && again.Assignee.DisplayName == "mutated" {
		t.Error("editing a returned user must not reach the store")
	}
}

func fakeContainsFold(values []string, want string) bool {
	for _, v := range values {
		if strings.EqualFold(v, want) {
			return true
		}
	}
	return false
}

func fakeHasVersion(versions []jira.Version, id string) bool {
	for i := range versions {
		if versions[i].ID == id {
			return true
		}
	}
	return false
}

func fakeIssuesOnVersion(t *testing.T, c *jiratest.Fake, versionID string) []jira.Issue {
	t.Helper()
	page, err := c.Search(t.Context(), jira.Query{JQL: "project = PROJ ORDER BY key", Fields: []string{jira.FieldsAll}})
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	all, err := jira.Collect(t.Context(), page, 0)
	if err != nil {
		t.Fatalf("Collect: %v", err)
	}
	out := make([]jira.Issue, 0, len(all))
	for i := range all {
		if fakeHasVersion(all[i].FixVersions, versionID) {
			out = append(out, all[i])
		}
	}
	return out
}

func TestUpdateIssue_RefusesAFieldIDThatIsNotInTheSitesCatalogue(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	c := jiratest.New(jiratest.WithProject("PROJ", jiratest.Scrum), jiratest.WithIssues(jiratest.Gen(3)))

	before, err := c.Issue(ctx, "PROJ-1")
	if err != nil {
		t.Fatalf("Issue: %v", err)
	}

	var patch jira.IssuePatch
	patch.Fields = patch.Fields.With(jira.FieldRef{ID: "customfield_10016"}, jira.FieldValue{Kind: jira.KindNumber, Number: 8})

	err = c.UpdateIssue(ctx, "PROJ-1", patch)
	var invalid *jira.ValidationError
	if !errors.As(err, &invalid) {
		t.Fatalf("UpdateIssue = %v, want a *jira.ValidationError for a hardcoded field ID", err)
	}
	if _, ok := invalid.For("customfield_10016"); !ok {
		t.Errorf("the error does not name the field: %v", invalid)
	}
	after, err := c.Issue(ctx, "PROJ-1")
	if err != nil {
		t.Fatalf("Issue: %v", err)
	}
	if _, ok := after.Fields.ByID("customfield_10016"); ok {
		t.Error("the rejected field was stored anyway")
	}
	if after.Updated != before.Updated {
		t.Error("a rejected patch still stamped the issue as updated")
	}
}

func TestUpdateIssue_AppliesNothingWhenAnyPartOfThePatchIsRejected(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	c := jiratest.New(jiratest.WithProject("PROJ", jiratest.Scrum), jiratest.WithIssues(jiratest.Gen(3)))

	before, err := c.Issue(ctx, "PROJ-1")
	if err != nil {
		t.Fatalf("Issue: %v", err)
	}
	renamed, bogus := "renamed", "99999"

	err = c.UpdateIssue(ctx, "PROJ-1", jira.IssuePatch{Summary: &renamed, PriorityID: &bogus})
	var invalid *jira.ValidationError
	if !errors.As(err, &invalid) {
		t.Fatalf("UpdateIssue = %v, want a *jira.ValidationError", err)
	}
	after, err := c.Issue(ctx, "PROJ-1")
	if err != nil {
		t.Fatalf("Issue: %v", err)
	}
	if after.Summary != before.Summary {
		t.Errorf("summary is now %q; a rejected patch was half applied", after.Summary)
	}
}

func TestCreateIssue_RefusesASubtaskWithNoParent(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	c := jiratest.New(jiratest.WithProject("PROJ", jiratest.Scrum), jiratest.WithIssues(jiratest.Gen(3)))

	meta, err := c.CreateMeta(ctx, "PROJ", "10305")
	if err != nil {
		t.Fatalf("CreateMeta: %v", err)
	}
	var advertised bool
	for _, f := range meta.Required() {
		if f.Field.ID == "parent" {
			advertised = true
		}
	}
	if !advertised {
		t.Skip("this issue type does not advertise a required parent")
	}

	_, err = c.CreateIssue(ctx, jira.IssueInput{ProjectKey: "PROJ", IssueTypeID: "10305", Summary: "orphan"})
	var invalid *jira.ValidationError
	if !errors.As(err, &invalid) {
		t.Fatalf("CreateIssue = %v, want a *jira.ValidationError naming parent", err)
	}
	if _, ok := invalid.For("parent"); !ok {
		t.Errorf("the error does not name parent: %v", invalid)
	}
}

func TestSprints_HandOutCopiesOfTheirDates(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	c := jiratest.New(jiratest.WithProject("PROJ", jiratest.Scrum))

	boards, err := c.Boards(ctx, "PROJ")
	if err != nil || len(boards) == 0 {
		t.Fatalf("Boards: %v (%d boards)", err, len(boards))
	}
	page, err := c.Sprints(ctx, boards[0].ID)
	if err != nil {
		t.Fatalf("Sprints: %v", err)
	}
	sprints, err := jira.Collect(ctx, page, 0)
	if err != nil {
		t.Fatalf("Collect: %v", err)
	}
	var dated *jira.Sprint
	for i := range sprints {
		if sprints[i].End != nil {
			dated = &sprints[i]
			break
		}
	}
	if dated == nil {
		t.Skip("no seeded sprint carries an end date")
	}
	want := *dated.End
	*dated.End = want.AddDate(0, 0, 7)

	again, err := c.Sprints(ctx, boards[0].ID)
	if err != nil {
		t.Fatalf("Sprints: %v", err)
	}
	fresh, err := jira.Collect(ctx, again, 0)
	if err != nil {
		t.Fatalf("Collect: %v", err)
	}
	for _, s := range fresh {
		if s.ID == dated.ID && !s.End.Equal(want) {
			t.Errorf("the stored sprint moved to %s; a caller wrote through the pointer it was handed", s.End)
		}
	}
}

func TestSearch_LeavesOutTheStructFieldsTheQueryDidNotName(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	c := jiratest.New(jiratest.WithProject("PROJ", jiratest.Scrum), jiratest.WithIssues(jiratest.Gen(5)))

	page, err := c.Search(ctx, jira.Query{JQL: "project = PROJ", Fields: []string{"summary", "status"}})
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if len(page.Items) == 0 {
		t.Fatal("no issues came back")
	}
	got := page.Items[0]
	if got.Key == "" || got.ID == "" {
		t.Error("identity should always come back")
	}
	if got.Summary == "" || got.Status.ID == "" {
		t.Error("the fields the query named are missing")
	}
	for name, present := range map[string]bool{
		"assignee":    got.Assignee != nil,
		"priority":    got.Priority != nil,
		"labels":      len(got.Labels) > 0,
		"description": !got.Description.IsZero(),
		"created":     !got.Created.IsZero(),
		"issuetype":   got.Type.ID != "",
	} {
		if present {
			t.Errorf("%s came back although the query did not ask for it; a list view built on this would render blank against a real site", name)
		}
	}
}

func TestCapabilities_AreAnsweredPerProject(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	c := jiratest.New(
		jiratest.WithProject("PROJ", jiratest.Scrum),
		jiratest.WithProject("OPS", jiratest.NoBoard),
	)

	with, err := c.Capabilities(ctx, "PROJ")
	if err != nil {
		t.Fatalf("Capabilities: %v", err)
	}
	if !with.Allows(jira.CapBoards) {
		t.Error("a project with a board should report boards as available")
	}

	without, err := c.Capabilities(ctx, "OPS")
	if err != nil {
		t.Fatalf("Capabilities: %v", err)
	}
	if without.Allows(jira.CapBoards) {
		t.Error("a project with no board reported boards as available")
	}
	if reason := without.Capability(jira.CapBoards).Reason; !strings.Contains(reason, "OPS") {
		t.Errorf("the reason does not name the project: %q", reason)
	}

	site, err := c.Capabilities(ctx, "")
	if err != nil {
		t.Fatalf("Capabilities: %v", err)
	}
	for _, k := range []jira.CapabilityKey{jira.CapBoards, jira.CapBulkMove, jira.CapDeleteIssues} {
		if site.Allows(k) {
			t.Errorf("%s was answered without a project to answer it for", k)
		}
	}
	if _, err := c.Capabilities(ctx, "NOPE"); err == nil {
		t.Error("an unknown project should not probe clean")
	}
}

func TestSprints_CanBeNarrowedToTheStatesAsked(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	c := jiratest.New(jiratest.WithProject("PROJ", jiratest.Scrum))

	boards, err := c.Boards(ctx, "PROJ")
	if err != nil || len(boards) == 0 {
		t.Fatalf("Boards: %v (%d)", err, len(boards))
	}

	all, err := c.Sprints(ctx, boards[0].ID)
	if err != nil {
		t.Fatalf("Sprints: %v", err)
	}
	every, err := jira.Collect(ctx, all, 0)
	if err != nil {
		t.Fatalf("Collect: %v", err)
	}

	page, err := c.Sprints(ctx, boards[0].ID, jira.SprintActive)
	if err != nil {
		t.Fatalf("Sprints: %v", err)
	}
	active, err := jira.Collect(ctx, page, 0)
	if err != nil {
		t.Fatalf("Collect: %v", err)
	}
	if len(active) == 0 || len(active) >= len(every) {
		t.Fatalf("filtering gave %d of %d sprints", len(active), len(every))
	}
	for _, s := range active {
		if s.State != jira.SprintActive {
			t.Errorf("a %s sprint came back from an active-only query", s.State)
		}
	}
}

func TestDownload_ResumesFromAnOffset(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	c := jiratest.New(jiratest.WithProject("PROJ", jiratest.Scrum), jiratest.WithIssues(jiratest.Gen(2)))

	up, err := c.Upload(ctx, "PROJ-1", []jira.FileRef{{
		Name: "log.txt", Size: 4,
		Open: func() (io.ReadCloser, error) { return io.NopCloser(strings.NewReader("abcd")), nil },
	}})
	if err != nil || len(up) != 1 {
		t.Fatalf("Upload: %v (%d)", err, len(up))
	}

	var whole bytes.Buffer
	if err := c.Download(ctx, up[0].ID, &whole, jira.DownloadOptions{}); err != nil {
		t.Fatalf("Download: %v", err)
	}
	if whole.Len() < 3 {
		t.Fatalf("attachment is only %d bytes, too small to resume into", whole.Len())
	}

	var rest bytes.Buffer
	if err := c.Download(ctx, up[0].ID, &rest, jira.DownloadOptions{From: 2}); err != nil {
		t.Fatalf("resumed Download: %v", err)
	}
	if got, want := rest.String(), whole.String()[2:]; got != want {
		t.Errorf("resume gave %q, want %q", got, want)
	}
	if err := c.Download(ctx, up[0].ID, io.Discard, jira.DownloadOptions{From: int64(whole.Len() + 1)}); err == nil {
		t.Error("resuming past the end should be an error, not an empty success")
	}
}
