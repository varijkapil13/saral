package cloud

import (
	"errors"
	"net/http"
	"slices"
	"testing"
	"time"

	"github.com/varijkapil13/saral/pkg/jira"
	"github.com/varijkapil13/saral/pkg/jira/jiratest"
)

// One set of assertions, run against both adapters, for issue linking. The two
// sites cannot agree on what state either of them holds, so the case that links
// two issues and reads them back only makes sense on the fake, which has
// somewhere to read from; the cloud side of that case asserts the wire mapping
// instead, which is the property the fixture server can prove.

type linkFullBuilder func(*testing.T) jira.Client

func linkClientFromSite(t *testing.T, opts ...jiratest.ServerOption) jira.Client {
	t.Helper()

	s := jiratest.NewServer(opts...)
	t.Cleanup(s.Close)
	c, _ := testClient(t, s.URL(), WithRetry(RetryPolicy{Attempts: 1}))
	return c
}

func conformFakeWithLinkableIssues(t *testing.T) *jiratest.Fake {
	t.Helper()
	return conformFake(t, jiratest.WithIssues(jiratest.GenFor(conformProject, 2)))
}

func TestIssueLinks_BothAdaptersAnswerTheSameWay(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name  string
		cloud linkFullBuilder
		fake  linkFullBuilder
		run   func(*testing.T, jira.Client)
	}{
		{
			name:  "every link type is identified and phrased both ways",
			cloud: func(t *testing.T) jira.Client { return linkClientFromSite(t) },
			fake:  func(t *testing.T) jira.Client { return conformFakeWithLinkableIssues(t) },
			run: func(t *testing.T, c jira.Client) {
				t.Helper()
				got, err := c.IssueLinkTypes(t.Context())
				if err != nil {
					t.Fatalf("reading the site's link types: %v", err)
				}
				if len(got) == 0 {
					t.Fatal("the site came back with no link types at all")
				}
				for _, lt := range got {
					if lt.ID == "" {
						t.Errorf("%q has no id, and a link is written by id, never by the localised name", lt.Name)
					}
					if lt.Outward == "" || lt.Inward == "" {
						t.Errorf("%q is missing one of its two directional phrases: %+v", lt.Name, lt)
					}
				}
			},
		},
		{
			// The fixture server holds no state, so cloud asserts the wire and the fake the round trip.
			name:  "linking two issues is answered on both ends",
			cloud: func(t *testing.T) jira.Client { return linkClientFromSite(t) },
			fake:  func(t *testing.T) jira.Client { return conformFakeWithLinkableIssues(t) },
			run: func(t *testing.T, c jira.Client) {
				t.Helper()
				switch impl := c.(type) {
				case *jiratest.Fake:
					from, to := conformProject+"-1", conformProject+"-2"
					types, err := impl.IssueLinkTypes(t.Context())
					if err != nil || len(types) == 0 {
						t.Fatalf("reading the fake's link types: %v", err)
					}
					kind := types[0]
					if err := impl.LinkIssues(t.Context(), jira.LinkInput{TypeID: kind.ID, From: from, To: to}); err != nil {
						t.Fatalf("linking %s to %s: %v", from, to, err)
					}

					source, err := impl.Issue(t.Context(), from)
					if err != nil {
						t.Fatalf("reading %s back: %v", from, err)
					}
					idx := slices.IndexFunc(source.Links, func(l jira.IssueLink) bool { return l.Other.Key == to })
					if idx < 0 {
						t.Fatalf("%s carries no link to %s: %+v", from, to, source.Links)
					}
					link := source.Links[idx]
					if link.Direction != jira.LinkOutward || link.Label != kind.Outward {
						t.Errorf("From's link reads %+v, want the outward phrase %q", link, kind.Outward)
					}

					target, err := impl.Issue(t.Context(), to)
					if err != nil {
						t.Fatalf("reading %s back: %v", to, err)
					}
					idx = slices.IndexFunc(target.Links, func(l jira.IssueLink) bool { return l.Other.Key == from })
					if idx < 0 {
						t.Fatalf("%s carries no link to %s: %+v", to, from, target.Links)
					}
					tlink := target.Links[idx]
					if tlink.Direction != jira.LinkInward || tlink.Label != kind.Inward {
						t.Errorf("To's link reads %+v, want the inward phrase %q", tlink, kind.Inward)
					}

					if err := impl.DeleteLink(t.Context(), link.ID); err != nil {
						t.Fatalf("deleting the link: %v", err)
					}
					source, err = impl.Issue(t.Context(), from)
					if err != nil {
						t.Fatalf("reading %s back after the delete: %v", from, err)
					}
					if slices.ContainsFunc(source.Links, func(l jira.IssueLink) bool { return l.ID == link.ID }) {
						t.Errorf("%s still carries the link this test deleted", from)
					}
				case *Client:
					s := jiratest.NewServer()
					t.Cleanup(s.Close)
					cc, _ := testClient(t, s.URL(), WithRetry(RetryPolicy{Attempts: 1}))

					if err := cc.LinkIssues(t.Context(), jira.LinkInput{TypeID: "10300", From: "EX-1", To: "EX-2"}); err != nil {
						t.Fatalf("linking on the wire: %v", err)
					}
					sent := sentBody(t, sentTo(t, s, http.MethodPost, linkRoute))
					typ, _ := sent["type"].(map[string]any)
					inward, _ := sent["inwardIssue"].(map[string]any)
					outward, _ := sent["outwardIssue"].(map[string]any)
					if typ["id"] != "10300" || inward["key"] != "EX-1" || outward["key"] != "EX-2" {
						t.Errorf("the wire mapping is %+v, want type 10300, inwardIssue EX-1, outwardIssue EX-2", sent)
					}

					if err := cc.DeleteLink(t.Context(), "20001"); err != nil {
						t.Fatalf("deleting on the wire: %v", err)
					}
					del := sentTo(t, s, http.MethodDelete, linkIDPath)
					if want := "/rest/api/3/issueLink/20001"; del.Path != want {
						t.Errorf("deleted %q, want %q", del.Path, want)
					}
				default:
					t.Fatalf("unexpected adapter type %T", c)
				}
			},
		},
		{
			name: "a capability refusal reads as one on both sites",
			cloud: func(t *testing.T) jira.Client {
				return linkClientFromSite(t, jiratest.WithStatus(http.MethodPost, linkRoute, http.StatusForbidden, "plans_403.json"))
			},
			fake: func(t *testing.T) jira.Client {
				f := conformFakeWithLinkableIssues(t)
				f.FailNext(&jira.CapabilityError{Reason: "issue linking is switched off for this site"})
				return f
			},
			run: func(t *testing.T, c jira.Client) {
				t.Helper()
				err := c.LinkIssues(t.Context(), jira.LinkInput{TypeID: "10300", From: conformProject + "-1", To: conformProject + "-2"})
				var refused *jira.CapabilityError
				if !errors.As(err, &refused) {
					t.Fatalf("got %T (%v), want a *jira.CapabilityError", err, err)
				}
			},
		},
		{
			name: "a rate limit reads as one on both sites",
			cloud: func(t *testing.T) jira.Client {
				return linkClientFromSite(t, jiratest.WithRateLimit(http.MethodPost, linkRoute, 30*time.Second))
			},
			fake: func(t *testing.T) jira.Client {
				f := conformFakeWithLinkableIssues(t)
				f.FailNext(&jira.RateLimitError{RetryAfter: 30 * time.Second})
				return f
			},
			run: func(t *testing.T, c jira.Client) {
				t.Helper()
				err := c.LinkIssues(t.Context(), jira.LinkInput{TypeID: "10300", From: conformProject + "-1", To: conformProject + "-2"})
				var limited *jira.RateLimitError
				if !errors.As(err, &limited) {
					t.Fatalf("got %T (%v), want a *jira.RateLimitError", err, err)
				}
			},
		},
		{
			name: "deleting a link nobody has is a 404 naming the link on both sites",
			cloud: func(t *testing.T) jira.Client {
				return linkClientFromSite(t, jiratest.WithStatus(http.MethodDelete, linkIDRoute, http.StatusNotFound, ""))
			},
			fake: func(t *testing.T) jira.Client { return conformFakeWithLinkableIssues(t) },
			run: func(t *testing.T, c jira.Client) {
				t.Helper()
				err := c.DeleteLink(t.Context(), "99999")
				var missing *jira.NotFoundError
				if !errors.As(err, &missing) {
					t.Fatalf("got %T (%v), want a *jira.NotFoundError", err, err)
				}
			},
		},
		{
			name:  "a link with nothing to link is refused before either site is asked",
			cloud: func(t *testing.T) jira.Client { return linkClientFromSite(t) },
			fake:  func(t *testing.T) jira.Client { return conformFakeWithLinkableIssues(t) },
			run: func(t *testing.T, c jira.Client) {
				t.Helper()
				err := c.LinkIssues(t.Context(), jira.LinkInput{})
				var invalid *jira.ValidationError
				if !errors.As(err, &invalid) {
					t.Fatalf("got %T (%v), want a *jira.ValidationError", err, err)
				}
			},
		},
	}

	for _, tt := range cases {
		for _, adapter := range []struct {
			name string
			open linkFullBuilder
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
