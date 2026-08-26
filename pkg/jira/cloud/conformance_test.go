package cloud

import (
	"errors"
	"net/http"
	"testing"

	"github.com/varijkapil13/saral/pkg/jira"
	"github.com/varijkapil13/saral/pkg/jira/jiratest"
)

// One set of assertions, run against both adapters. A rule that lives in only
// one of them is a rule no test meets: everything above the port is tested
// against the fake, so the suite stays green while the binary fails against a
// site. It has happened twice — a field list the fake accepted and the site
// refuses, and a 200 naming nobody the fake accepted and the cloud adapter
// refuses.
//
// This covers Me. Another method is another table beside this one, driven over
// the port role that names it.

// meBuilder opens one adapter in that adapter's own terms. The role is what
// makes a single table runnable twice.
type meBuilder func(*testing.T) jira.Identifier

type meCase struct {
	name   string
	cloud  meBuilder
	fake   meBuilder
	assert func(*testing.T, jira.User, error)
}

func TestMe_BothAdaptersAnswerTheSameWay(t *testing.T) {
	t.Parallel()

	const namedNobody = `{"displayName":"Example User","active":true}`
	const unloadableZone = `{"accountId":"5b10a2844c20165700ede21g","displayName":"Example User",
		"emailAddress":"user@example.com","active":true,"avatarUrls":{"48x48":"https://example.atlassian.net/avatar"},
		"timeZone":"Mars/Olympus_Mons"}`

	cases := []meCase{
		{
			name:   "an account the site names in full",
			cloud:  func(t *testing.T) jira.Identifier { return meFromSite(t) },
			fake:   func(*testing.T) jira.Identifier { return jiratest.New() },
			assert: func(t *testing.T, got jira.User, err error) { assertWholeAccount(t, got, err) },
		},
		{
			name:  "a 200 that names nobody",
			cloud: func(t *testing.T) jira.Identifier { return meFromSite(t, meAnswering(namedNobody)) },
			fake: func(*testing.T) jira.Identifier {
				return jiratest.New(jiratest.WithMe(jira.User{DisplayName: "Example User", Active: true}))
			},
			assert: assertRefusedNobody,
		},
		{
			name:  "an account whose timezone this machine cannot load",
			cloud: func(t *testing.T) jira.Identifier { return meFromSite(t, meAnswering(unloadableZone)) },
			// A zone no database has reaches a caller as no zone at all, which is
			// the only form the in-memory fake can hold it in.
			fake: func(*testing.T) jira.Identifier {
				return jiratest.New(jiratest.WithMe(jira.User{
					AccountID:   "5b10a2844c20165700ede21g",
					DisplayName: "Example User",
					Email:       "user@example.com",
					Active:      true,
					AvatarURL:   "https://example.atlassian.net/avatar",
				}))
			},
			assert: assertAccountWithoutAZone,
		},
	}

	for _, tt := range cases {
		for _, adapter := range []struct {
			name string
			open meBuilder
		}{
			{name: "cloud", open: tt.cloud},
			{name: "fake", open: tt.fake},
		} {
			t.Run(tt.name+"/"+adapter.name, func(t *testing.T) {
				t.Parallel()

				got, err := adapter.open(t).Me(t.Context())
				tt.assert(t, got, err)
			})
		}
	}
}

// assertWholeAccount names the fields that came back empty rather than the first
// one, because a divergence is usually one field and the name of it is the
// finding. The avatar is the field that was missing from the fake.
func assertWholeAccount(t *testing.T, got jira.User, err error) {
	t.Helper()

	if err != nil {
		t.Fatalf("reading the account: %v", err)
	}
	unset := make([]string, 0, 6)
	if got.AccountID == "" {
		unset = append(unset, "AccountID")
	}
	if got.DisplayName == "" {
		unset = append(unset, "DisplayName")
	}
	if got.Email == "" {
		unset = append(unset, "Email")
	}
	if got.TimeZone == nil {
		unset = append(unset, "TimeZone")
	}
	if !got.Active {
		unset = append(unset, "Active")
	}
	if got.AvatarURL == "" {
		unset = append(unset, "AvatarURL")
	}
	if got.Kind == jira.AccountUnknown {
		unset = append(unset, "Kind")
	}
	if len(unset) > 0 {
		t.Errorf("the account came back with %v unset: %+v", unset, got)
	}
}

func assertRefusedNobody(t *testing.T, got jira.User, err error) {
	t.Helper()

	if err == nil {
		t.Fatalf("an answer naming nobody read as the account %+v, and onboarding takes that for proof", got)
	}
	var broken *jira.TransportError
	if !errors.As(err, &broken) {
		t.Fatalf("got %T (%v), want a *jira.TransportError", err, err)
	}
	if got != (jira.User{}) {
		t.Errorf("the refusal came back with %+v attached, want no account at all", got)
	}
}

func assertAccountWithoutAZone(t *testing.T, got jira.User, err error) {
	t.Helper()

	if err != nil {
		t.Fatalf("an unusable zone failed the whole read: %v", err)
	}
	if got.TimeZone != nil {
		t.Errorf("the timezone is %s, want none", got.TimeZone)
	}
	if got.AccountID == "" || got.DisplayName == "" || got.Email == "" || !got.Active {
		t.Errorf("the account reads as %+v, want everything but the zone", got)
	}
}

// meFromSite is the cloud adapter pointed at a replay server, which is as close
// to a site as a test is allowed to get.
func meFromSite(t *testing.T, opts ...jiratest.ServerOption) jira.Identifier {
	t.Helper()

	s := jiratest.NewServer(opts...)
	t.Cleanup(s.Close)
	c, _ := testClient(t, s.URL())
	return c
}

func meAnswering(body string) jiratest.ServerOption {
	return jiratest.WithHandler(http.MethodGet, mePath, func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(body))
	})
}
