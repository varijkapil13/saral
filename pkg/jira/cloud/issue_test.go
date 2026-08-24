package cloud

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/varijkapil13/saral/pkg/adf"
	"github.com/varijkapil13/saral/pkg/jira"
	"github.com/varijkapil13/saral/pkg/jira/jiratest"
)

const (
	testIssueKey = "EX-1"
	issueRoute   = "/rest/api/3/issue/{key}"
	// createdIssue is what POST /issue answers with, and all it answers with.
	createdIssue = `{"id":"10001","key":"EX-1","self":"https://example.atlassian.net/rest/api/3/issue/10001"}`
)

func str(s string) *string { return &s }

// issueCall is one of the five methods this file covers, in a shape the failure
// tables can drive. route is what a test overrides to make that call misbehave.
type issueCall struct {
	name    string
	method  string
	route   string
	decodes bool
	run     func(ctx context.Context, c *Client) error
}

func issueCalls() []issueCall {
	return []issueCall{
		{
			name: "Issue", method: http.MethodGet, route: issueRoute, decodes: true,
			run: func(ctx context.Context, c *Client) error {
				_, err := c.Issue(ctx, testIssueKey)
				return err
			},
		},
		{
			name: "CreateIssue", method: http.MethodPost, route: "/rest/api/3/issue", decodes: true,
			run: func(ctx context.Context, c *Client) error {
				_, err := c.CreateIssue(ctx, newIssue())
				return err
			},
		},
		{
			name: "UpdateIssue", method: http.MethodPut, route: issueRoute,
			run: func(ctx context.Context, c *Client) error {
				return c.UpdateIssue(ctx, testIssueKey, jira.IssuePatch{Summary: str("a new summary")})
			},
		},
		{
			name: "Transitions", method: http.MethodGet, route: "/rest/api/3/issue/{key}/transitions", decodes: true,
			run: func(ctx context.Context, c *Client) error {
				_, err := c.Transitions(ctx, testIssueKey)
				return err
			},
		},
		{
			name: "Transition", method: http.MethodPost, route: "/rest/api/3/issue/{key}/transitions",
			run: func(ctx context.Context, c *Client) error {
				return c.Transition(ctx, testIssueKey, "31", jira.IssuePatch{})
			},
		},
	}
}

func newIssue() jira.IssueInput {
	return jira.IssueInput{ProjectKey: "EX", IssueTypeID: "10004", Summary: "Something to do"}
}

// writeRoutes answers the two endpoints the fixture server has no default for,
// so that a test of one call is not derailed by another one 404ing.
func writeRoutes() []jiratest.ServerOption {
	return []jiratest.ServerOption{
		jiratest.WithHandler(http.MethodPost, "/rest/api/3/issue", jsonHandler(http.StatusCreated, createdIssue)),
		jiratest.WithHandler(http.MethodPut, issueRoute, jsonHandler(http.StatusNoContent, "")),
		jiratest.WithHandler(http.MethodPost, "/rest/api/3/issue/{key}/transitions", jsonHandler(http.StatusNoContent, "")),
	}
}

func issueServer(opts ...jiratest.ServerOption) *jiratest.Server {
	return jiratest.NewServer(append(writeRoutes(), opts...)...)
}

// sentTo returns the last request the server served on a route, which is how a
// test reads the body an adapter actually put on the wire.
func sentTo(t *testing.T, s *jiratest.Server, method, path string) jiratest.Request {
	t.Helper()

	served := s.Requests()
	for i := len(served) - 1; i >= 0; i-- {
		if served[i].Method == method && served[i].Path == path {
			return served[i]
		}
	}
	t.Fatalf("the site was never sent a %s %s; it served %v", method, path, served)
	return jiratest.Request{}
}

// sentFields reads the fields object out of a write the server recorded.
func sentFields(t *testing.T, s *jiratest.Server, method, path string) map[string]json.RawMessage {
	t.Helper()

	var body struct {
		Fields map[string]json.RawMessage `json:"fields"`
	}
	sent := sentTo(t, s, method, path)
	if err := json.Unmarshal([]byte(sent.Body), &body); err != nil {
		t.Fatalf("reading the body of %s %s: %v", method, path, err)
	}
	return body.Fields
}

func sentKeys(t *testing.T, s *jiratest.Server, method, path string) []string {
	t.Helper()

	fields := sentFields(t, s, method, path)
	out := make([]string, 0, len(fields))
	for id := range fields {
		out = append(out, id)
	}
	slices.Sort(out)
	return out
}

func TestIssue_ReadsOneIssueAndSaysItWasReadWithEveryField(t *testing.T) {
	t.Parallel()

	s := issueServer()
	defer s.Close()

	c, _ := testClient(t, s.URL())
	iss, err := c.Issue(t.Context(), testIssueKey)
	if err != nil {
		t.Fatalf("Issue: %v", err)
	}
	if iss.Key != testIssueKey {
		t.Errorf("Key = %q, want %q", iss.Key, testIssueKey)
	}
	if iss.Summary == "" {
		t.Error("the issue carries no summary")
	}
	if !iss.Requested.Wide() {
		t.Error("Requested is not wide; a bare issue read asks the site for every field it has")
	}

	sent := sentTo(t, s, http.MethodGet, "/rest/api/3/issue/"+testIssueKey)
	if !strings.Contains(sent.Query, "expand="+issueDetailExpand) {
		t.Errorf("query = %q, want it to expand %q so a custom field is typed rather than guessed", sent.Query, issueDetailExpand)
	}
}

func TestIssue_RefusesAKeyThatNamesNothingWithoutAskingTheSite(t *testing.T) {
	t.Parallel()

	s := issueServer()
	defer s.Close()

	c, _ := testClient(t, s.URL())
	for _, key := range []string{"", "   "} {
		_, err := c.Issue(t.Context(), key)

		var invalid *jira.ValidationError
		if !errors.As(err, &invalid) {
			t.Fatalf("with key %q got %v, want a *jira.ValidationError", key, err)
		}
		if _, ok := invalid.For("issueIdOrKey"); !ok {
			t.Errorf("the failure does not name the key: %v", invalid)
		}
	}
	if served := len(s.Requests()); served != 0 {
		t.Errorf("the site served %d requests; a call with no issue to read must not be sent", served)
	}
}

func TestCreateIssue_SendsWhatTheInputNamesAndReadsTheStoredIssueBack(t *testing.T) {
	t.Parallel()

	s := issueServer()
	defer s.Close()

	description := adf.NewDoc(adf.NewNode("paragraph", adf.NewText("why this matters")))
	c, _ := testClient(t, s.URL())
	created, err := c.CreateIssue(t.Context(), jira.IssueInput{
		ProjectKey:  "EX",
		IssueTypeID: "10004",
		Summary:     "Something to do",
		Description: description,
		Labels:      []string{"checkout"},
		Assignee:    "acct-ada",
		ParentKey:   "EX-9",
	})
	if err != nil {
		t.Fatalf("CreateIssue: %v", err)
	}
	if created.Key != testIssueKey {
		t.Errorf("Key = %q, want the key the site allocated", created.Key)
	}
	if !created.Requested.Wide() {
		t.Error("the created issue was read back, so its mask should be the wide one")
	}

	fields := sentFields(t, s, http.MethodPost, "/rest/api/3/issue")
	for id, want := range map[string]string{
		"project":   `{"key":"EX"}`,
		"issuetype": `{"id":"10004"}`,
		"summary":   `"Something to do"`,
		"labels":    `["checkout"]`,
		"assignee":  `{"accountId":"acct-ada"}`,
		"parent":    `{"key":"EX-9"}`,
	} {
		if got := string(fields[id]); got != want {
			t.Errorf("fields[%q] = %s, want %s", id, got, want)
		}
	}
	if !strings.Contains(string(fields["description"]), "why this matters") {
		t.Errorf("the description did not travel as ADF: %s", fields["description"])
	}
}

// TestCreateIssue_KeepsTheIssueItMadeWhenTheReadBackFails is the duplicate
// guard: the issue exists by then, so an error here is one the caller would
// answer by creating a second one.
func TestCreateIssue_KeepsTheIssueItMadeWhenTheReadBackFails(t *testing.T) {
	t.Parallel()

	s := issueServer(jiratest.WithHandler(http.MethodGet, issueRoute, jsonHandler(http.StatusInternalServerError, `{"errorMessages":["boom"]}`)))
	defer s.Close()

	c, _ := testClient(t, s.URL(), WithRetry(RetryPolicy{Attempts: 1}))
	created, err := c.CreateIssue(t.Context(), newIssue())
	if err != nil {
		t.Fatalf("CreateIssue reported %v; the issue was created and a retry would make a second one", err)
	}
	if created.Key != testIssueKey || created.ID != "10001" {
		t.Errorf("got %+v, want the identity the site allocated", created)
	}
	if created.Requested.Wide() || created.Requested.Len() != 0 {
		t.Error("the mask claims fields were read; nothing was read back")
	}
}

func TestCreateIssue_RefusesAnInputTheSiteCouldNotActOnWithoutAskingIt(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name  string
		in    jira.IssueInput
		field string
	}{
		{"no project", jira.IssueInput{IssueTypeID: "10004", Summary: "x"}, "project"},
		{"no issue type", jira.IssueInput{ProjectKey: "EX", Summary: "x"}, "issuetype"},
		{"no summary", jira.IssueInput{ProjectKey: "EX", IssueTypeID: "10004", Summary: "  "}, "summary"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			s := issueServer()
			defer s.Close()

			c, _ := testClient(t, s.URL())
			_, err := c.CreateIssue(t.Context(), tc.in)

			var invalid *jira.ValidationError
			if !errors.As(err, &invalid) {
				t.Fatalf("got %v, want a *jira.ValidationError", err)
			}
			if _, ok := invalid.For(tc.field); !ok {
				t.Errorf("the failure does not name %s: %v", tc.field, invalid)
			}
			if served := len(s.Requests()); served != 0 {
				t.Errorf("the site served %d requests for an input it could not act on", served)
			}
		})
	}
}

// TestUpdateIssue_SendsOnlyTheFieldsThePatchNames is the whole reason the patch
// is sparse: a key set to null empties that field, so anything the patch did
// not name has to be absent rather than nulled.
func TestUpdateIssue_SendsOnlyTheFieldsThePatchNames(t *testing.T) {
	t.Parallel()

	s := issueServer()
	defer s.Close()

	c, _ := testClient(t, s.URL())
	if err := c.UpdateIssue(t.Context(), testIssueKey, jira.IssuePatch{Summary: str("a new summary")}); err != nil {
		t.Fatalf("UpdateIssue: %v", err)
	}

	got := sentKeys(t, s, http.MethodPut, "/rest/api/3/issue/"+testIssueKey)
	if !slices.Equal(got, []string{"summary"}) {
		t.Fatalf("the write named %v; only the summary was asked for, and every other key would empty a field", got)
	}
}

func TestUpdateIssue_WritesEachKindOfValueInTheShapeTheFieldHolds(t *testing.T) {
	t.Parallel()

	s := issueServer()
	defer s.Close()

	due := jira.Date{Year: 2026, Month: time.March, Day: 2}
	patch := jira.IssuePatch{
		Summary:    str("a new summary"),
		Assignee:   str("acct-ada"),
		Labels:     &[]string{"checkout", "regression"},
		PriorityID: str("2"),
		Due:        &due,
		Fields: jira.NewFieldSet(map[string]jira.FieldValue{
			"customfield_13401": {Kind: jira.KindNumber, Number: 5},
			"customfield_13405": {Kind: jira.KindDate, Date: due},
			"customfield_13407": {Kind: jira.KindOptions, Options: []jira.Option{{ID: "10", Label: "Red"}, {Label: "loose"}}},
			"customfield_13408": {Kind: jira.KindUser, Users: []jira.User{{AccountID: "acct-grace"}}},
			"customfield_13409": {Kind: jira.KindUnknown, Text: `{"anything":"this client cannot type"}`},
		}),
		Clear: []jira.FieldRef{{ID: "customfield_13406"}},
	}
	c, _ := testClient(t, s.URL())
	if err := c.UpdateIssue(t.Context(), testIssueKey, patch); err != nil {
		t.Fatalf("UpdateIssue: %v", err)
	}

	fields := sentFields(t, s, http.MethodPut, "/rest/api/3/issue/"+testIssueKey)
	for id, want := range map[string]string{
		"summary":           `"a new summary"`,
		"assignee":          `{"accountId":"acct-ada"}`,
		"labels":            `["checkout","regression"]`,
		"priority":          `{"id":"2"}`,
		"duedate":           `"2026-03-02"`,
		"customfield_13401": `5`,
		"customfield_13405": `"2026-03-02"`,
		"customfield_13407": `[{"id":"10"},"loose"]`,
		"customfield_13408": `{"accountId":"acct-grace"}`,
		"customfield_13409": `{"anything":"this client cannot type"}`,
		"customfield_13406": `null`,
	} {
		if got := string(fields[id]); got != want {
			t.Errorf("fields[%q] = %s, want %s", id, got, want)
		}
	}
}

func TestUpdateIssue_RefusesTheTwoFieldsAnEditCannotChange(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name  string
		patch jira.IssuePatch
		field string
	}{
		{
			"status as a field value",
			jira.IssuePatch{Fields: jira.NewFieldSet(map[string]jira.FieldValue{
				"status": {Kind: jira.KindOption, Options: []jira.Option{{ID: "10002", Label: "Released"}}},
			})},
			"status",
		},
		{
			"project as a field value",
			jira.IssuePatch{Fields: jira.NewFieldSet(map[string]jira.FieldValue{
				"project": {Kind: jira.KindOption, Options: []jira.Option{{ID: "10000", Label: "Other"}}},
			})},
			"project",
		},
		{
			"status nulled through Clear",
			jira.IssuePatch{Clear: []jira.FieldRef{{ID: "status"}}},
			"status",
		},
		{
			"summary emptied",
			jira.IssuePatch{Summary: str("   ")},
			"summary",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			s := issueServer()
			defer s.Close()

			c, _ := testClient(t, s.URL())
			err := c.UpdateIssue(t.Context(), testIssueKey, tc.patch)

			var invalid *jira.ValidationError
			if !errors.As(err, &invalid) {
				t.Fatalf("got %v, want a *jira.ValidationError", err)
			}
			if _, ok := invalid.For(tc.field); !ok {
				t.Errorf("the failure does not name %s: %v", tc.field, invalid)
			}
			if served := len(s.Requests()); served != 0 {
				t.Errorf("the site served %d requests for a write the port promises it cannot make", served)
			}
		})
	}
}

func TestUpdateIssue_SendsNothingForAPatchThatWouldChangeNothing(t *testing.T) {
	t.Parallel()

	s := issueServer()
	defer s.Close()

	c, _ := testClient(t, s.URL())
	if err := c.UpdateIssue(t.Context(), testIssueKey, jira.IssuePatch{}); err != nil {
		t.Fatalf("UpdateIssue: %v", err)
	}
	if served := len(s.Requests()); served != 0 {
		t.Errorf("the site served %d requests for a patch that names no field", served)
	}
}

func TestUpdateIssue_CarriesTheNotificationChoiceAsTheEndpointTakesIt(t *testing.T) {
	t.Parallel()

	for _, notify := range []bool{true, false} {
		s := issueServer()
		c, _ := testClient(t, s.URL())
		if err := c.UpdateIssue(t.Context(), testIssueKey, jira.IssuePatch{Summary: str("x"), Notify: &notify}); err != nil {
			t.Fatalf("UpdateIssue: %v", err)
		}
		sent := sentTo(t, s, http.MethodPut, "/rest/api/3/issue/"+testIssueKey)
		want := "notifyUsers=" + map[bool]string{true: "true", false: "false"}[notify]
		if sent.Query != want {
			t.Errorf("query = %q, want %q", sent.Query, want)
		}
		s.Close()
	}
}

func TestUpdateIssue_MapsARejectedWriteToTheFieldsJiraNamed(t *testing.T) {
	t.Parallel()

	s := issueServer(jiratest.WithStatus(http.MethodPut, issueRoute, http.StatusBadRequest, "validation_error.json"))
	defer s.Close()

	c, _ := testClient(t, s.URL())
	err := c.UpdateIssue(t.Context(), testIssueKey, jira.IssuePatch{Summary: str("x")})

	var invalid *jira.ValidationError
	if !errors.As(err, &invalid) {
		t.Fatalf("got %T (%v), want a *jira.ValidationError", err, err)
	}
	for _, field := range []string{"summary", "duedate", "customfield_10032"} {
		if _, ok := invalid.For(field); !ok {
			t.Errorf("the failure does not name %s: %v", field, invalid)
		}
	}
	if len(invalid.Messages) == 0 {
		t.Error("the messages with no field of their own were dropped")
	}
}

func TestUpdateIssue_ReportsAWriteThatLostARaceAsAConflict(t *testing.T) {
	t.Parallel()

	s := issueServer(jiratest.WithHandler(http.MethodPut, issueRoute,
		jsonHandler(http.StatusConflict, `{"errorMessages":["The issue has been updated since you loaded it."]}`)))
	defer s.Close()

	c, _ := testClient(t, s.URL())
	err := c.UpdateIssue(t.Context(), testIssueKey, jira.IssuePatch{Summary: str("x")})

	var conflict *jira.ConflictError
	if !errors.As(err, &conflict) {
		t.Fatalf("got %T (%v), want a *jira.ConflictError so the caller can offer reload-and-reapply", err, err)
	}
	if !strings.Contains(conflict.Resource, testIssueKey) {
		t.Errorf("Resource = %q, want it to name the issue", conflict.Resource)
	}
}

func TestTransitions_ReadsWhatThisIssueCanDoRightNowIncludingItsScreen(t *testing.T) {
	t.Parallel()

	s := issueServer()
	defer s.Close()

	c, _ := testClient(t, s.URL())
	moves, err := c.Transitions(t.Context(), testIssueKey)
	if err != nil {
		t.Fatalf("Transitions: %v", err)
	}
	if len(moves) != 3 {
		t.Fatalf("got %d transitions, want the 3 the fixture carries", len(moves))
	}

	sent := sentTo(t, s, http.MethodGet, "/rest/api/3/issue/"+testIssueKey+"/transitions")
	if !strings.Contains(sent.Query, "expand=transitions.fields") {
		t.Errorf("query = %q; without the fields expansion a screen with a required field looks like a move that needs nothing", sent.Query)
	}

	at := slices.IndexFunc(moves, func(tr jira.Transition) bool { return tr.ID == "31" })
	if at < 0 {
		t.Fatalf("no transition with id 31 in %+v", moves)
	}
	release := moves[at]
	if !release.HasScreen {
		t.Error("the transition with a screen does not report one")
	}
	if release.To.ID != "10002" || release.To.Category != jira.CategoryDone {
		t.Errorf("To = %+v, want the done-category status the fixture names", release.To)
	}
	if len(release.Fields) != 2 {
		t.Fatalf("got %d screen fields, want 2", len(release.Fields))
	}
	if !release.Fields[0].Required || release.Fields[0].Field.ID != "resolution" {
		t.Errorf("first screen field = %+v, want the required resolution first", release.Fields[0])
	}
	if release.Fields[1].Required {
		t.Errorf("second screen field = %+v, want the optional one after the required one", release.Fields[1])
	}
	labels := make([]string, 0, len(release.Fields[0].AllowedValues))
	for _, option := range release.Fields[0].AllowedValues {
		labels = append(labels, option.ID+":"+option.Label)
	}
	if !slices.Equal(labels, []string{"10000:Done", "10001:Won't Do"}) {
		t.Errorf("allowed values = %v, want the ids the picker has to send", labels)
	}
}

func TestTransitions_LeavesOutAMoveTheSiteSaysIsNotAvailable(t *testing.T) {
	t.Parallel()

	body := `{"transitions":[
		{"id":"21","name":"Start review","to":{"id":"10001","name":"In Review"},"isAvailable":true},
		{"id":"41","name":"Reopen","to":{"id":"10000","name":"Backlog"},"isAvailable":false}
	]}`
	s := issueServer(jiratest.WithHandler(http.MethodGet, "/rest/api/3/issue/{key}/transitions", jsonHandler(http.StatusOK, body)))
	defer s.Close()

	c, _ := testClient(t, s.URL())
	moves, err := c.Transitions(t.Context(), testIssueKey)
	if err != nil {
		t.Fatalf("Transitions: %v", err)
	}
	if len(moves) != 1 || moves[0].ID != "21" {
		t.Fatalf("got %+v, want only the move the site says can be made", moves)
	}
}

func TestTransition_MovesByIdAndFillsTheScreenFromThePatch(t *testing.T) {
	t.Parallel()

	s := issueServer()
	defer s.Close()

	c, _ := testClient(t, s.URL())
	patch := jira.IssuePatch{Fields: jira.NewFieldSet(map[string]jira.FieldValue{
		"resolution": {Kind: jira.KindOption, Options: []jira.Option{{ID: "10000", Label: "Done"}}},
	})}
	if err := c.Transition(t.Context(), testIssueKey, "31", patch); err != nil {
		t.Fatalf("Transition: %v", err)
	}

	path := "/rest/api/3/issue/" + testIssueKey + "/transitions"
	sent := sentTo(t, s, http.MethodPost, path)
	var body struct {
		Transition struct {
			ID string `json:"id"`
		} `json:"transition"`
	}
	if err := json.Unmarshal([]byte(sent.Body), &body); err != nil {
		t.Fatalf("reading the body: %v", err)
	}
	if body.Transition.ID != "31" {
		t.Errorf("transition.id = %q, want 31: a transition is matched by id, never by its localised name", body.Transition.ID)
	}
	fields := sentFields(t, s, http.MethodPost, path)
	if got := string(fields["resolution"]); got != `{"id":"10000"}` {
		t.Errorf("fields[resolution] = %s, want the id the screen offered", got)
	}
}

func TestTransition_RefusesAMoveWithNoTransitionToMake(t *testing.T) {
	t.Parallel()

	s := issueServer()
	defer s.Close()

	c, _ := testClient(t, s.URL())
	err := c.Transition(t.Context(), testIssueKey, "  ", jira.IssuePatch{})

	var invalid *jira.ValidationError
	if !errors.As(err, &invalid) {
		t.Fatalf("got %v, want a *jira.ValidationError", err)
	}
	if _, ok := invalid.For("transition"); !ok {
		t.Errorf("the failure does not name the transition: %v", invalid)
	}
	if served := len(s.Requests()); served != 0 {
		t.Errorf("the site served %d requests for a move that names nothing to do", served)
	}
}

func TestIssueCalls_ReportARefusalAsACapabilityAnswer(t *testing.T) {
	t.Parallel()

	for _, call := range issueCalls() {
		t.Run(call.name, func(t *testing.T) {
			t.Parallel()

			s := issueServer(jiratest.WithHandler(call.method, call.route,
				jsonHandler(http.StatusForbidden, `{"errorMessages":["You do not have permission to edit issues in this project."]}`)))
			defer s.Close()

			c, _ := testClient(t, s.URL())
			err := call.run(t.Context(), c)

			var refused *jira.CapabilityError
			if !errors.As(err, &refused) {
				t.Fatalf("got %T (%v), want a *jira.CapabilityError: a 403 is an answer about what is possible", err, err)
			}
			if !strings.Contains(refused.Reason, "permission") {
				t.Errorf("Reason = %q, want Jira's own wording, which is what the UI shows", refused.Reason)
			}
		})
	}
}

func TestIssueCalls_ReportARateLimitWithTheWaitTheSiteAskedFor(t *testing.T) {
	t.Parallel()

	for _, call := range issueCalls() {
		t.Run(call.name, func(t *testing.T) {
			t.Parallel()

			s := issueServer(jiratest.WithRateLimit(call.method, call.route, 30*time.Second))
			defer s.Close()

			c, _ := testClient(t, s.URL(), WithRetry(RetryPolicy{Attempts: 1}))
			err := call.run(t.Context(), c)

			var limited *jira.RateLimitError
			if !errors.As(err, &limited) {
				t.Fatalf("got %T (%v), want a *jira.RateLimitError", err, err)
			}
			if limited.RetryAfter != 30*time.Second {
				t.Errorf("RetryAfter = %v, want the 30s the header carried", limited.RetryAfter)
			}
		})
	}
}

func TestIssueCalls_ReportAHostThatNeverAnsweredAsATransportFailure(t *testing.T) {
	t.Parallel()

	for _, call := range issueCalls() {
		t.Run(call.name, func(t *testing.T) {
			t.Parallel()

			s := issueServer()
			site := s.URL()
			s.Close()

			c, _ := testClient(t, site, WithRetry(RetryPolicy{Attempts: 1}))
			err := call.run(t.Context(), c)

			var broken *jira.TransportError
			if !errors.As(err, &broken) {
				t.Fatalf("got %T (%v), want a *jira.TransportError", err, err)
			}
			if broken.Status != 0 {
				t.Errorf("Status = %d, want 0: the request never reached a server", broken.Status)
			}
		})
	}
}

// TestIssueCalls_TreatABodyTheyCannotReadAsATransportFailure covers both halves
// of the answer: a call that decodes a body fails on one it cannot read, and a
// call that decodes nothing is not derailed by whatever the site sent with its
// success.
func TestIssueCalls_TreatABodyTheyCannotReadAsATransportFailure(t *testing.T) {
	t.Parallel()

	for _, call := range issueCalls() {
		t.Run(call.name, func(t *testing.T) {
			t.Parallel()

			s := issueServer(jiratest.WithHandler(call.method, call.route, jsonHandler(http.StatusOK, `{"transitions":`)))
			defer s.Close()

			c, _ := testClient(t, s.URL(), WithRetry(RetryPolicy{Attempts: 1}))
			err := call.run(t.Context(), c)

			if !call.decodes {
				if err != nil {
					t.Fatalf("got %v; this call reads no body, so what came with the success is not its problem", err)
				}
				return
			}
			var broken *jira.TransportError
			if !errors.As(err, &broken) {
				t.Fatalf("got %T (%v), want a *jira.TransportError", err, err)
			}
			if broken.Status != http.StatusOK {
				t.Errorf("Status = %d, want the 200 the body arrived with", broken.Status)
			}
		})
	}
}

func TestIssueCalls_ReturnTheContextErrorWhenTheCallerCancelsMidFlight(t *testing.T) {
	t.Parallel()

	for _, call := range issueCalls() {
		t.Run(call.name, func(t *testing.T) {
			t.Parallel()

			arrived := make(chan struct{}, 1)
			s := issueServer(jiratest.WithHandler(call.method, call.route, func(_ http.ResponseWriter, r *http.Request) {
				select {
				case arrived <- struct{}{}:
				default:
				}
				<-r.Context().Done()
			}))
			defer s.Close()

			c, _ := testClient(t, s.URL())
			ctx, cancel := context.WithCancel(t.Context())
			failed := make(chan error, 1)
			go func() { failed <- call.run(ctx, c) }()

			<-arrived
			cancel()
			if err := <-failed; !errors.Is(err, context.Canceled) {
				t.Fatalf("got %v, want the context's own error", err)
			}
		})
	}
}

// TestCreateIssue_IsNeverReplayedAfterAServerFailure is the difference between
// a 429 and a 5xx: the first refused the request before it ran, the second may
// have got far enough to make an issue.
func TestCreateIssue_IsNeverReplayedAfterAServerFailure(t *testing.T) {
	t.Parallel()

	s := issueServer(jiratest.WithHandler(http.MethodPost, "/rest/api/3/issue", jsonHandler(http.StatusBadGateway, `{"errorMessages":["upstream"]}`)))
	defer s.Close()

	c, _ := testClient(t, s.URL())
	if _, err := c.CreateIssue(t.Context(), newIssue()); err == nil {
		t.Fatal("CreateIssue reported no error for a 502")
	}
	if served := len(s.Requests()); served != 1 {
		t.Errorf("the site served %d create requests; a 5xx may already have made an issue", served)
	}
}

func TestFieldJSON_RefusesAValueThatCannotBeWrittenBack(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name  string
		value jira.FieldValue
	}{
		{"nothing to write", jira.FieldValue{Kind: jira.KindEmpty}},
		{"bytes that are not the JSON they were read as", jira.FieldValue{Kind: jira.KindUnknown, Text: "not json"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			_, err := fieldJSON("customfield_13401", tc.value)

			var invalid *jira.ValidationError
			if !errors.As(err, &invalid) {
				t.Fatalf("got %v, want a *jira.ValidationError", err)
			}
			if _, ok := invalid.For("customfield_13401"); !ok {
				t.Errorf("the failure does not name the field: %v", invalid)
			}
		})
	}
}

func TestOptionJSON_WritesACascadingSelectAsTheParentAndItsChild(t *testing.T) {
	t.Parallel()

	got := string(optionJSON(jira.Option{ID: "100", Label: "Europe", Children: []jira.Option{{ID: "101", Label: "Berlin"}}}))
	if got != `{"child":{"id":"101"},"id":"100"}` {
		t.Errorf("got %s, want the parent and its child by id", got)
	}
}
