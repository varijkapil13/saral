package cloud

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/varijkapil13/saral/pkg/adf"
	"github.com/varijkapil13/saral/pkg/jira"
	"github.com/varijkapil13/saral/pkg/jira/jiratest"
)

const worklogRoute = "/rest/api/3/issue/{key}/worklog"

func worklogInput() jira.WorklogInput {
	return jira.WorklogInput{
		Spent:   45 * time.Minute,
		Started: time.Date(2026, time.February, 4, 9, 0, 0, 0, time.FixedZone("", 3600)),
		Comment: paragraph("Reviewed the sprocket migration."),
	}
}

func TestWorklogs_DecodesEveryFieldAcrossBothOffsetPages(t *testing.T) {
	t.Parallel()

	s := jiratest.NewServer()
	defer s.Close()

	c, _ := testClient(t, s.URL())
	first, err := c.Worklogs(t.Context(), testIssueKey)
	if err != nil {
		t.Fatalf("Worklogs: %v", err)
	}
	if total, ok := first.Count(); !ok || total != 3 {
		t.Errorf("got total %d (reported %v), want the 3 the envelope claims", total, ok)
	}
	if !first.HasMore() {
		t.Fatal("the first page reports no more, but the fixture holds a second one")
	}

	got, err := jira.Collect(t.Context(), first, 0)
	if err != nil {
		t.Fatalf("walking the pages: %v", err)
	}
	if len(got) != 3 {
		t.Fatalf("got %d worklogs, want the 3 across both pages", len(got))
	}

	first0 := got[0]
	if first0.ID != "10900" || first0.IssueID != "10001" {
		t.Errorf("got ID %q IssueID %q, want 10900 on 10001", first0.ID, first0.IssueID)
	}
	if first0.Author.DisplayName != "Example User" || first0.Author.AccountID != "5b10a2844c20165700ede21g" {
		t.Errorf("the author did not decode: %+v", first0.Author)
	}
	if first0.UpdateAuthor == nil || first0.UpdateAuthor.DisplayName != "Example User" {
		t.Errorf("the update author did not decode: %+v", first0.UpdateAuthor)
	}
	if got := adf.Markdown(first0.Comment); got == "" {
		t.Error("the comment did not decode")
	}
	wantStarted, err := time.Parse(platformTimeLayout, "2026-02-02T08:30:00.000+0100")
	if err != nil {
		t.Fatalf("parsing the fixture's own timestamp: %v", err)
	}
	if !first0.Started.Equal(wantStarted) {
		t.Errorf("Started = %v, want %v", first0.Started, wantStarted)
	}
	if first0.Spent != 90*time.Minute {
		t.Errorf("Spent = %v, want 1h30m", first0.Spent)
	}
	if first0.Visibility != nil {
		t.Errorf("an unrestricted entry came back restricted: %+v", *first0.Visibility)
	}

	second := got[1]
	if second.Spent != 30*time.Minute {
		t.Errorf("Spent = %v, want 30m", second.Spent)
	}
	if !second.Comment.IsZero() {
		t.Error("an entry with no comment came back with one")
	}

	third := got[2]
	if third.ID != "10902" {
		t.Errorf("the page-2 entry is %q, want 10902", third.ID)
	}
	if third.Spent != 135*time.Minute {
		t.Errorf("Spent = %v, want 2h15m", third.Spent)
	}
	if third.Visibility == nil || third.Visibility.Type != "role" || third.Visibility.Value != "Developers" {
		t.Errorf("the page-2 entry's visibility did not decode: %+v", third.Visibility)
	}
}

func TestWorklogs_ReportsAnEmptyLogAsAWellFormedEmptyPage(t *testing.T) {
	t.Parallel()

	s := jiratest.NewServer(jiratest.WithFixture(http.MethodGet, worklogRoute, "worklogs_empty.json"))
	defer s.Close()

	c, _ := testClient(t, s.URL())
	page, err := c.Worklogs(t.Context(), testIssueKey)
	if err != nil {
		t.Fatalf("Worklogs: %v", err)
	}
	if len(page.Items) != 0 {
		t.Errorf("got %d items, want none", len(page.Items))
	}
	if page.HasMore() {
		t.Error("an empty, exhausted page claims another one")
	}
	if total, ok := page.Count(); !ok || total != 0 {
		t.Errorf("got total %d (reported %v), want 0", total, ok)
	}
}

func TestAddWorklog_SendsSecondsAndTheCommentAndDecodesWhatCameBack(t *testing.T) {
	t.Parallel()

	s := jiratest.NewServer()
	defer s.Close()

	c, _ := testClient(t, s.URL())
	// Half a second on top of the whole minutes proves the truncation: the site
	// only ever sees whole seconds.
	in := worklogInput()
	in.Spent += 500 * time.Millisecond

	got, err := c.AddWorklog(t.Context(), testIssueKey, in)
	if err != nil {
		t.Fatalf("AddWorklog: %v", err)
	}
	if got.ID != "10903" {
		t.Errorf("ID = %q, want 10903", got.ID)
	}
	if got.Spent != 45*time.Minute {
		t.Errorf("Spent = %v, want 45m", got.Spent)
	}
	if adf.Markdown(got.Comment) == "" {
		t.Error("the stored comment did not decode")
	}

	sent := sentBody(t, sentTo(t, s, http.MethodPost, "/rest/api/3/issue/"+testIssueKey+"/worklog"))
	if sent["timeSpentSeconds"] != float64(45*60) {
		t.Errorf("timeSpentSeconds = %v, want %d: the sub-second remainder must not round up", sent["timeSpentSeconds"], 45*60)
	}
	started, _ := sent["started"].(string)
	if _, err := time.Parse(platformTimeLayout, started); err != nil {
		t.Errorf("started = %q, not in the platform layout: %v", started, err)
	}
	body, ok := sent["comment"].(map[string]any)
	if !ok || body["type"] != "doc" {
		t.Fatalf("the comment sent is not an ADF document: %v", sent["comment"])
	}
}

func TestAddWorklog_OmitsTheCommentKeyWhenNoneWasGiven(t *testing.T) {
	t.Parallel()

	s := jiratest.NewServer()
	defer s.Close()

	c, _ := testClient(t, s.URL())
	in := worklogInput()
	in.Comment = adf.Doc{}

	if _, err := c.AddWorklog(t.Context(), testIssueKey, in); err != nil {
		t.Fatalf("AddWorklog: %v", err)
	}
	sent := sentBody(t, sentTo(t, s, http.MethodPost, "/rest/api/3/issue/"+testIssueKey+"/worklog"))
	if _, ok := sent["comment"]; ok {
		t.Errorf("a worklog with no comment sent one: %v", sent)
	}
}

func TestAddWorklog_RefusesWhatCannotBeLoggedWithoutAskingTheSite(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name  string
		in    jira.WorklogInput
		field string
	}{
		{name: "no time at all", in: jira.WorklogInput{Started: time.Now()}, field: "timeSpentSeconds"},
		{name: "negative time", in: jira.WorklogInput{Spent: -time.Minute, Started: time.Now()}, field: "timeSpentSeconds"},
		{name: "no start time", in: jira.WorklogInput{Spent: time.Minute}, field: "started"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			s := jiratest.NewServer()
			defer s.Close()
			c, _ := testClient(t, s.URL())

			_, err := c.AddWorklog(t.Context(), testIssueKey, tc.in)
			var invalid *jira.ValidationError
			if !errors.As(err, &invalid) {
				t.Fatalf("got %T (%v), want a *jira.ValidationError", err, err)
			}
			if _, ok := invalid.For(tc.field); !ok {
				t.Errorf("the failure does not name %s: %v", tc.field, invalid)
			}
			if served := len(s.Requests()); served != 0 {
				t.Errorf("the site served %d requests for a worklog that could never be logged", served)
			}
		})
	}
}

func TestAddWorklog_MapsARejectedWriteToTheFieldTheSiteNamed(t *testing.T) {
	t.Parallel()

	s := jiratest.NewServer(jiratest.WithHandler(http.MethodPost, worklogRoute, jsonHandler(http.StatusBadRequest,
		`{"errorMessages":[],"errors":{"timeSpentSeconds":"Time spent is required."}}`)))
	defer s.Close()

	c, _ := testClient(t, s.URL())
	_, err := c.AddWorklog(t.Context(), testIssueKey, worklogInput())

	var invalid *jira.ValidationError
	if !errors.As(err, &invalid) {
		t.Fatalf("got %T (%v), want a *jira.ValidationError", err, err)
	}
	if _, ok := invalid.For("timeSpentSeconds"); !ok {
		t.Errorf("the failure does not name timeSpentSeconds: %v", invalid)
	}
}

// worklogCalls is the two methods this file covers, in the shape the failure
// tables in issue_test.go already drive.
func worklogCalls() []issueCall {
	return []issueCall{
		{
			name: "Worklogs", method: http.MethodGet, route: worklogRoute, decodes: true,
			run: func(ctx context.Context, c *Client) error {
				_, err := c.Worklogs(ctx, testIssueKey)
				return err
			},
		},
		{
			name: "AddWorklog", method: http.MethodPost, route: worklogRoute, decodes: true,
			run: func(ctx context.Context, c *Client) error {
				_, err := c.AddWorklog(ctx, testIssueKey, worklogInput())
				return err
			},
		},
	}
}

func TestWorklogCalls_ReportARefusalAsACapabilityAnswer(t *testing.T) {
	t.Parallel()

	for _, tc := range worklogCalls() {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			s := jiratest.NewServer(jiratest.WithHandler(tc.method, tc.route,
				jsonHandler(http.StatusForbidden, `{"errorMessages":["You do not have permission to log work on this issue."]}`)))
			defer s.Close()

			c, _ := testClient(t, s.URL())
			err := tc.run(t.Context(), c)

			var refused *jira.CapabilityError
			if !errors.As(err, &refused) {
				t.Fatalf("got %T (%v), want a *jira.CapabilityError", err, err)
			}
			if !strings.Contains(refused.Reason, "permission") {
				t.Errorf("Reason = %q, want Jira's own wording", refused.Reason)
			}
		})
	}
}

func TestWorklogCalls_ReportANotFoundIssueByItsKey(t *testing.T) {
	t.Parallel()

	for _, tc := range worklogCalls() {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			s := jiratest.NewServer(jiratest.WithHandler(tc.method, tc.route,
				jsonHandler(http.StatusNotFound, `{"errorMessages":["Issue does not exist or you do not have permission to see it."]}`)))
			defer s.Close()

			c, _ := testClient(t, s.URL())
			err := tc.run(t.Context(), c)

			var missing *jira.NotFoundError
			if !errors.As(err, &missing) {
				t.Fatalf("got %T (%v), want a *jira.NotFoundError", err, err)
			}
			if missing.Kind != "issue" || missing.ID != testIssueKey {
				t.Errorf("the failure names %s %s rather than the issue", missing.Kind, missing.ID)
			}
		})
	}
}

func TestWorklogCalls_ReportARateLimitWithTheWaitTheSiteAskedFor(t *testing.T) {
	t.Parallel()

	for _, tc := range worklogCalls() {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			s := jiratest.NewServer(jiratest.WithRateLimit(tc.method, tc.route, 30*time.Second))
			defer s.Close()

			c, _ := testClient(t, s.URL(), WithRetry(RetryPolicy{Attempts: 1}))
			err := tc.run(t.Context(), c)

			var limited *jira.RateLimitError
			if !errors.As(err, &limited) {
				t.Fatalf("got %T (%v), want a *jira.RateLimitError", err, err)
			}
			if limited.RetryAfter != 30*time.Second {
				t.Errorf("RetryAfter = %v, want 30s", limited.RetryAfter)
			}
		})
	}
}

func TestWorklogCalls_ReportABadGatewayAsATransportFailure(t *testing.T) {
	t.Parallel()

	for _, tc := range worklogCalls() {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			s := jiratest.NewServer(jiratest.WithHandler(tc.method, tc.route,
				jsonHandler(http.StatusBadGateway, `{"errorMessages":["upstream is unwell"]}`)))
			defer s.Close()

			c, _ := testClient(t, s.URL(), WithRetry(RetryPolicy{Attempts: 1}))
			err := tc.run(t.Context(), c)

			var down *jira.TransportError
			if !errors.As(err, &down) {
				t.Fatalf("got %T (%v), want a *jira.TransportError", err, err)
			}
			if down.Status != http.StatusBadGateway {
				t.Errorf("Status = %d, want %d", down.Status, http.StatusBadGateway)
			}
		})
	}
}

func TestWorklogCalls_ReportAHostThatNeverAnsweredAsATransportFailure(t *testing.T) {
	t.Parallel()

	for _, tc := range worklogCalls() {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			s := jiratest.NewServer()
			site := s.URL()
			s.Close()

			c, _ := testClient(t, site, WithRetry(RetryPolicy{Attempts: 1}))
			err := tc.run(t.Context(), c)

			var down *jira.TransportError
			if !errors.As(err, &down) {
				t.Fatalf("got %T (%v), want a *jira.TransportError", err, err)
			}
			if down.Status != 0 {
				t.Errorf("Status = %d, want 0: the request never reached a server", down.Status)
			}
		})
	}
}

func TestWorklogCalls_TreatABodyTheyCannotReadAsATransportFailure(t *testing.T) {
	t.Parallel()

	for _, tc := range worklogCalls() {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			s := jiratest.NewServer(jiratest.WithHandler(tc.method, tc.route,
				jsonHandler(http.StatusOK, `{"worklogs":`)))
			defer s.Close()

			c, _ := testClient(t, s.URL(), WithRetry(RetryPolicy{Attempts: 1}))
			err := tc.run(t.Context(), c)

			var down *jira.TransportError
			if !errors.As(err, &down) {
				t.Fatalf("got %T (%v), want a *jira.TransportError", err, err)
			}
			if down.Status != http.StatusOK {
				t.Errorf("Status = %d, want the 200 the body arrived with", down.Status)
			}
		})
	}
}

func TestWorklogCalls_ReturnTheCallersOwnErrorWhenItCancels(t *testing.T) {
	t.Parallel()

	for _, tc := range worklogCalls() {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			arrived, announce := gate()
			release, letGo := gate()
			s := jiratest.NewServer(jiratest.WithHandler(tc.method, tc.route, func(_ http.ResponseWriter, r *http.Request) {
				announce()
				select {
				case <-r.Context().Done():
				case <-release:
				}
			}))
			defer closeServer(t, s)
			defer letGo()

			c, _ := testClient(t, s.URL())
			ctx, cancel := context.WithCancel(t.Context())
			failed := make(chan error, 1)
			go func() { failed <- tc.run(ctx, c) }()

			receive(t, "the request to reach the site", arrived)
			cancel()
			if err := receive(t, "the cancelled call to come back", failed); !errors.Is(err, context.Canceled) {
				t.Fatalf("got %v, want the context's own error", err)
			}
		})
	}
}
