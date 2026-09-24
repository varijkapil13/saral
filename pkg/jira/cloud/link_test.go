package cloud

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/varijkapil13/saral/pkg/jira"
	"github.com/varijkapil13/saral/pkg/jira/jiratest"
)

const (
	linkTypeRoute = "/rest/api/3/issueLinkType"
	linkRoute     = "/rest/api/3/issueLink"
	linkIDRoute   = "/rest/api/3/issueLink/{id}"
	linkIDPath    = "/rest/api/3/issueLink/20001"
)

func linkClient(t *testing.T, opts ...jiratest.ServerOption) (*Client, *jiratest.Server) {
	t.Helper()

	s := jiratest.NewServer(opts...)
	t.Cleanup(s.Close)
	c, _ := testClient(t, s.URL(), WithRetry(RetryPolicy{Attempts: 1}))
	return c, s
}

func validLinkInput() jira.LinkInput {
	return jira.LinkInput{TypeID: "10300", From: testIssueKey, To: "EX-2"}
}

// linkCall is one of the three methods this file covers, so the failure-path
// tables can drive all three with one loop.
type linkCall struct {
	name   string
	method string
	route  string
	run    func(ctx context.Context, c *Client) error
}

func linkCalls() []linkCall {
	return []linkCall{
		{
			name: "IssueLinkTypes", method: http.MethodGet, route: linkTypeRoute,
			run: func(ctx context.Context, c *Client) error {
				_, err := c.IssueLinkTypes(ctx)
				return err
			},
		},
		{
			name: "LinkIssues", method: http.MethodPost, route: linkRoute,
			run: func(ctx context.Context, c *Client) error {
				return c.LinkIssues(ctx, validLinkInput())
			},
		},
		{
			name: "DeleteLink", method: http.MethodDelete, route: linkIDRoute,
			run: func(ctx context.Context, c *Client) error {
				return c.DeleteLink(ctx, "20001")
			},
		},
	}
}

func TestIssueLinkTypes_DecodesEveryTypeInOrderWithIDsAsStrings(t *testing.T) {
	t.Parallel()

	c, _ := linkClient(t)
	got, err := c.IssueLinkTypes(t.Context())
	if err != nil {
		t.Fatalf("reading the site's link types: %v", err)
	}
	want := []jira.LinkType{
		{ID: "10300", Name: "Holds up", Inward: "is held up by", Outward: "holds up"},
		{ID: "10301", Name: "Echoes", Inward: "is echoed by", Outward: "echoes"},
		{ID: "10302", Name: "Leans on", Inward: "is leaned on by", Outward: "leans on"},
	}
	if len(got) != len(want) {
		t.Fatalf("got %d link types, want %d: %+v", len(got), len(want), got)
	}
	for i, lt := range got {
		if lt != want[i] {
			t.Errorf("type %d is %+v, want %+v", i, lt, want[i])
		}
	}
}

func TestLinkIssues_SendsTheInwardOutwardMappingAndTheTypeID(t *testing.T) {
	t.Parallel()

	c, s := linkClient(t)
	if err := c.LinkIssues(t.Context(), jira.LinkInput{TypeID: "10300", From: "EX-1", To: "EX-2"}); err != nil {
		t.Fatalf("linking EX-1 to EX-2: %v", err)
	}

	sent := sentBody(t, sentTo(t, s, http.MethodPost, linkRoute))
	typ, ok := sent["type"].(map[string]any)
	if !ok || typ["id"] != "10300" {
		t.Errorf("the type sent is %v, want id 10300", sent["type"])
	}
	inward, ok := sent["inwardIssue"].(map[string]any)
	if !ok || inward["key"] != "EX-1" {
		t.Errorf("inwardIssue is %v, want the key From reads at: EX-1", sent["inwardIssue"])
	}
	outward, ok := sent["outwardIssue"].(map[string]any)
	if !ok || outward["key"] != "EX-2" {
		t.Errorf("outwardIssue is %v, want the key To reads at: EX-2", sent["outwardIssue"])
	}
}

func TestDeleteLink_SendsTheLinkIDOnThePath(t *testing.T) {
	t.Parallel()

	c, s := linkClient(t)
	if err := c.DeleteLink(t.Context(), "20001"); err != nil {
		t.Fatalf("deleting a link: %v", err)
	}
	sent := sentTo(t, s, http.MethodDelete, linkIDPath)
	if want := "/rest/api/3/issueLink/20001"; sent.Path != want {
		t.Errorf("deleted %q, want %q", sent.Path, want)
	}
}

func TestIssueLinkTypes_ADisabledSiteAnswers404(t *testing.T) {
	t.Parallel()

	c, _ := linkClient(t, jiratest.WithStatus(http.MethodGet, linkTypeRoute, http.StatusNotFound, ""))
	_, err := c.IssueLinkTypes(t.Context())
	var missing *jira.NotFoundError
	if !errors.As(err, &missing) {
		t.Fatalf("got %T (%v), want a *jira.NotFoundError", err, err)
	}
}

func TestLinkIssues_TheSiteRefusesAnUnknownTypeIDAsInvalid(t *testing.T) {
	t.Parallel()

	body := `{"errorMessages":[],"errors":{"type":"The issue link type with id '99999' does not exist."}}`
	c, _ := linkClient(t, jiratest.WithHandler(http.MethodPost, linkRoute, jsonHandler(http.StatusBadRequest, body)))

	err := c.LinkIssues(t.Context(), jira.LinkInput{TypeID: "99999", From: "EX-1", To: "EX-2"})
	var invalid *jira.ValidationError
	if !errors.As(err, &invalid) {
		t.Fatalf("got %T (%v), want a *jira.ValidationError", err, err)
	}
	if msg, ok := invalid.For("type"); !ok || msg == "" {
		t.Errorf("the failure does not name the type field: %v", invalid)
	}
}

func TestLink_RefusalBecomesTheSentenceTheUserReads(t *testing.T) {
	t.Parallel()

	for _, tc := range linkCalls() {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			body := `{"errorMessages":["Issue linking is switched off for this site."],"errors":{}}`
			c, _ := linkClient(t, jiratest.WithHandler(tc.method, tc.route, jsonHandler(http.StatusForbidden, body)))

			err := tc.run(t.Context(), c)
			var refused *jira.CapabilityError
			if !errors.As(err, &refused) {
				t.Fatalf("got %T (%v), want a *jira.CapabilityError", err, err)
			}
			if !strings.Contains(refused.Error(), "Issue linking is switched off") {
				t.Errorf("the reason lost the site's own wording: %q", refused.Error())
			}
		})
	}
}

func TestLink_RateLimitCarriesTheWaitTheSiteAskedFor(t *testing.T) {
	t.Parallel()

	for _, tc := range linkCalls() {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			c, _ := linkClient(t, jiratest.WithRateLimit(tc.method, tc.route, 30*time.Second))

			err := tc.run(t.Context(), c)
			var limited *jira.RateLimitError
			if !errors.As(err, &limited) {
				t.Fatalf("got %T (%v), want a *jira.RateLimitError", err, err)
			}
			if limited.RetryAfter != 30*time.Second {
				t.Errorf("got a wait of %s, want the 30s the header asked for", limited.RetryAfter)
			}
		})
	}
}

func TestLink_TransportFailureIsATransportFailure(t *testing.T) {
	t.Parallel()

	for _, tc := range linkCalls() {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			c, _ := linkClient(t, jiratest.WithHandler(tc.method, tc.route,
				jsonHandler(http.StatusBadGateway, `{"errorMessages":["upstream is unwell"]}`)))

			err := tc.run(t.Context(), c)
			var down *jira.TransportError
			if !errors.As(err, &down) {
				t.Fatalf("got %T (%v), want a *jira.TransportError", err, err)
			}
			if down.Status != http.StatusBadGateway {
				t.Errorf("the failure reports HTTP %d", down.Status)
			}
		})
	}
}

func TestLink_AHostThatNeverAnswersIsATransportFailure(t *testing.T) {
	t.Parallel()

	for _, tc := range linkCalls() {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			dead := jiratest.NewServer()
			site := dead.URL()
			dead.Close()
			c, _ := testClient(t, site, WithRetry(RetryPolicy{Attempts: 1}))

			err := tc.run(t.Context(), c)
			var broken *jira.TransportError
			if !errors.As(err, &broken) {
				t.Fatalf("got %T (%v), want a *jira.TransportError", err, err)
			}
			if broken.Status != 0 {
				t.Errorf("a host that never answered reports HTTP %d", broken.Status)
			}
		})
	}
}

func TestIssueLinkTypes_ABodyThisClientCannotReadIsATransportFailure(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name string
		body string
	}{
		{name: "JSON that stops half way", body: `{"issueLinkTypes":[`},
		{name: "an envelope that is an array", body: `[]`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			c, _ := linkClient(t, jiratest.WithHandler(http.MethodGet, linkTypeRoute, jsonHandler(http.StatusOK, tc.body)))

			_, err := c.IssueLinkTypes(t.Context())
			var down *jira.TransportError
			if !errors.As(err, &down) {
				t.Fatalf("got %T (%v), want a *jira.TransportError", err, err)
			}
		})
	}
}

func TestLink_ReturnsTheCallersOwnErrorWhenItCancels(t *testing.T) {
	t.Parallel()

	for _, tc := range linkCalls() {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			c, s := linkClient(t)
			ctx, cancel := context.WithCancel(t.Context())
			cancel()

			if err := tc.run(ctx, c); !errors.Is(err, context.Canceled) {
				t.Fatalf("got %v, want the context's own error", err)
			}
			if served := len(s.Requests()); served != 0 {
				t.Errorf("the site served %d requests after the caller had already gone", served)
			}
		})
	}
}

func TestLinkIssues_RefusesInvalidInputWithoutAskingTheSite(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name  string
		input jira.LinkInput
		field string
	}{
		{name: "no link type at all", input: jira.LinkInput{From: "EX-1", To: "EX-2"}, field: "type"},
		{name: "no issue to start at", input: jira.LinkInput{TypeID: "10300", To: "EX-2"}, field: "inwardIssue"},
		{name: "no issue to end at", input: jira.LinkInput{TypeID: "10300", From: "EX-1"}, field: "outwardIssue"},
		{name: "the same issue on both ends", input: jira.LinkInput{TypeID: "10300", From: "EX-1", To: "EX-1"}, field: "outwardIssue"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			c, s := linkClient(t)
			err := c.LinkIssues(t.Context(), tc.input)
			var invalid *jira.ValidationError
			if !errors.As(err, &invalid) {
				t.Fatalf("got %T (%v), want a *jira.ValidationError", err, err)
			}
			if _, ok := invalid.For(tc.field); !ok {
				t.Errorf("the failure does not name %s: %v", tc.field, invalid)
			}
			if served := len(s.Requests()); served != 0 {
				t.Errorf("the site served %d requests for a link that was never valid", served)
			}
		})
	}
}

func TestDeleteLink_RefusesAnEmptyIDWithoutAskingTheSite(t *testing.T) {
	t.Parallel()

	c, s := linkClient(t)
	err := c.DeleteLink(t.Context(), "   ")
	var invalid *jira.ValidationError
	if !errors.As(err, &invalid) {
		t.Fatalf("got %T (%v), want a *jira.ValidationError", err, err)
	}
	if served := len(s.Requests()); served != 0 {
		t.Errorf("the site served %d requests for a delete with no id", served)
	}
}
