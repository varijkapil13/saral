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

const mePath = "/rest/api/3/myself"

func TestMe_ReadsTheWholeAccountAndNotJustTheZone(t *testing.T) {
	t.Parallel()

	s := jiratest.NewServer()
	defer s.Close()

	c, _ := testClient(t, s.URL())
	got, err := c.Me(t.Context())
	if err != nil {
		t.Fatalf("reading the account: %v", err)
	}

	if got.AccountID == "" {
		t.Error("the account has no ID, which is the one field that says a token belongs to somebody")
	}
	if got.DisplayName == "" || got.Email == "" {
		t.Errorf("the account reads as %+v, want the name and email the site states", got)
	}
	if !got.Active {
		t.Error("the account reads as inactive, and the site says it is active")
	}
	if got.AvatarURL == "" {
		t.Error("no avatar was picked, and the site offers four sizes")
	}
	if got.TimeZone == nil {
		t.Fatal("the account has no timezone, which is the zone Jira renders its own dates in")
	}
	if got.TimeZone.String() == time.UTC.String() {
		t.Errorf("the timezone is %s, want the one the site states rather than a fallback", got.TimeZone)
	}
}

func TestMe_AndTheCapabilityProbeAgreeAboutTheZone(t *testing.T) {
	t.Parallel()

	s := jiratest.NewServer()
	defer s.Close()

	c, _ := testClient(t, s.URL())
	account, err := c.Me(t.Context())
	if err != nil {
		t.Fatalf("reading the account: %v", err)
	}
	caps, err := c.Capabilities(t.Context(), "")
	if err != nil {
		t.Fatalf("probing: %v", err)
	}

	if caps.TimeZone == nil {
		t.Fatalf("the probe found no timezone and said %q", caps.TimeZoneReason)
	}
	if account.TimeZone.String() != caps.TimeZone.String() {
		t.Errorf("Me says %s and the probe says %s; they read one endpoint and must not disagree",
			account.TimeZone, caps.TimeZone)
	}
}

// The two read the same endpoint and decode it differently on purpose: the probe
// owes the user a sentence about a zone it could not use, and an account is
// still an account without one.
func TestMe_SurvivesAZoneThisMachineHasNoDatabaseFor(t *testing.T) {
	t.Parallel()

	const body = `{"accountId":"5b10a2844c20165700ede21g","displayName":"Example User",
		"emailAddress":"user@example.com","active":true,"timeZone":"Mars/Olympus_Mons"}`

	s := jiratest.NewServer(jiratest.WithHandler(http.MethodGet, mePath, func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(body))
	}))
	defer s.Close()

	c, _ := testClient(t, s.URL())
	got, err := c.Me(t.Context())
	if err != nil {
		t.Fatalf("an unknown zone failed the whole read: %v", err)
	}
	if got.AccountID == "" || got.DisplayName == "" {
		t.Errorf("the account reads as %+v, want everything but the zone", got)
	}
	if got.TimeZone != nil {
		t.Errorf("the timezone is %s, want none: this machine has no entry for it", got.TimeZone)
	}

	caps, err := c.Capabilities(t.Context(), "")
	if err != nil {
		t.Fatalf("probing: %v", err)
	}
	if caps.TimeZone != nil || !strings.Contains(caps.TimeZoneReason, "Mars/Olympus_Mons") {
		t.Errorf("the probe said %q, want a reason naming the zone it could not load", caps.TimeZoneReason)
	}
}

func TestMe_RefusesAnAnswerThatNamesNobody(t *testing.T) {
	t.Parallel()

	tests := map[string]string{
		"an empty object":          `{}`,
		"a body with no accountId": `{"displayName":"Example User","active":true}`,
	}

	for name, body := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			s := jiratest.NewServer(jiratest.WithHandler(http.MethodGet, mePath, func(w http.ResponseWriter, _ *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				_, _ = w.Write([]byte(body))
			}))
			defer s.Close()

			c, _ := testClient(t, s.URL())
			got, err := c.Me(t.Context())
			if err == nil {
				t.Fatalf("a 200 naming nobody read as the account %+v, and onboarding takes that for proof", got)
			}
			var broken *jira.TransportError
			if !errors.As(err, &broken) {
				t.Fatalf("got %T (%v), want a *jira.TransportError", err, err)
			}
		})
	}
}

func TestMe_ReportsARefusalRateLimitAndTransportFailureAsThemselves(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		opt    jiratest.ServerOption
		assert func(*testing.T, error)
	}{
		{
			name: "the token may not ask",
			opt:  jiratest.WithStatus(http.MethodGet, mePath, http.StatusForbidden, ""),
			assert: func(t *testing.T, err error) {
				t.Helper()
				var refused *jira.CapabilityError
				if !errors.As(err, &refused) {
					t.Fatalf("got %T (%v), want a *jira.CapabilityError", err, err)
				}
			},
		},
		{
			name: "the credentials were rejected",
			opt:  jiratest.WithStatus(http.MethodGet, mePath, http.StatusUnauthorized, ""),
			assert: func(t *testing.T, err error) {
				t.Helper()
				var rejected *jira.AuthError
				if !errors.As(err, &rejected) {
					t.Fatalf("got %T (%v), want a *jira.AuthError", err, err)
				}
			},
		},
		{
			name: "the site is rate limiting",
			opt:  jiratest.WithRateLimit(http.MethodGet, mePath, 30*time.Second),
			assert: func(t *testing.T, err error) {
				t.Helper()
				var limited *jira.RateLimitError
				if !errors.As(err, &limited) {
					t.Fatalf("got %T (%v), want a *jira.RateLimitError", err, err)
				}
				if limited.RetryAfter != 30*time.Second {
					t.Errorf("RetryAfter = %s, want the 30s the site asked for", limited.RetryAfter)
				}
			},
		},
		{
			name: "the site broke",
			opt:  jiratest.WithStatus(http.MethodGet, mePath, http.StatusInternalServerError, ""),
			assert: func(t *testing.T, err error) {
				t.Helper()
				var broken *jira.TransportError
				if !errors.As(err, &broken) {
					t.Fatalf("got %T (%v), want a *jira.TransportError", err, err)
				}
			},
		},
		{
			name: "the answer is not JSON",
			opt: jiratest.WithHandler(http.MethodGet, mePath, func(w http.ResponseWriter, _ *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				_, _ = w.Write([]byte("<html>a proxy answered instead</html>"))
			}),
			assert: func(t *testing.T, err error) {
				t.Helper()
				var broken *jira.TransportError
				if !errors.As(err, &broken) {
					t.Fatalf("got %T (%v), want a *jira.TransportError", err, err)
				}
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			s := jiratest.NewServer(tt.opt)
			defer s.Close()

			c, _ := testClient(t, s.URL(), WithRetry(RetryPolicy{Attempts: 1}))
			got, err := c.Me(t.Context())
			if err == nil {
				t.Fatalf("the failure came back as the account %+v", got)
			}
			tt.assert(t, err)
		})
	}
}

func TestMe_HonoursACancelledContext(t *testing.T) {
	t.Parallel()

	arrived, announce := gate()
	release, letGo := gate()

	s := jiratest.NewServer(jiratest.WithHandler(http.MethodGet, mePath, func(w http.ResponseWriter, r *http.Request) {
		announce()
		select {
		case <-release:
		case <-r.Context().Done():
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer closeServer(t, s)
	defer letGo()

	ctx, cancel := context.WithCancel(t.Context())
	c, _ := testClient(t, s.URL())

	errs := make(chan error, 1)
	go func() {
		_, err := c.Me(ctx)
		errs <- err
	}()

	receive(t, "the request to reach the site", arrived)
	cancel()

	err := receive(t, "the cancelled call to come back", errs)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("got %v, want the cancellation the caller asked for", err)
	}
}
