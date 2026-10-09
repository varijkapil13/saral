package connect

import (
	"errors"
	"testing"
	"time"

	"github.com/varijkapil13/saral/pkg/jira"
	"github.com/varijkapil13/saral/pkg/jira/jiratest"
)

func siteFailures() []error {
	return []error{
		&jira.AuthError{},
		&jira.CapabilityError{Capability: jira.CapBoards, Reason: "not for this token"},
		&jira.RateLimitError{RetryAfter: time.Second},
		&jira.TransportError{Op: "read", Status: 503},
	}
}

func TestVerify_ReturnsTheAccountOnACloudSite(t *testing.T) {
	t.Parallel()

	f := jiratest.New(jiratest.WithMe(jira.User{AccountID: "acc-1", DisplayName: "Ada"}))
	got, err := Verify(t.Context(), f)
	if err != nil {
		t.Fatalf("verifying: %v", err)
	}
	if got.AccountID != "acc-1" {
		t.Errorf("account is %+v, want acc-1", got)
	}
}

func TestVerify_RefusesASiteThatSaysItIsNotCloud(t *testing.T) {
	t.Parallel()

	f := jiratest.New(
		jiratest.WithMe(jira.User{AccountID: "acc-1"}),
		jiratest.WithServerInfo(jira.ServerInfo{DeploymentType: "Server"}),
	)
	if _, err := Verify(t.Context(), f); !errors.Is(err, ErrNotCloud) {
		t.Errorf("verifying a Server site failed with %v, want ErrNotCloud", err)
	}
}

func TestVerify_PassesTheIdentityFailureThroughAsItsOwnType(t *testing.T) {
	t.Parallel()

	for _, fail := range siteFailures() {
		f := jiratest.New(jiratest.WithMe(jira.User{AccountID: "acc-1"}))
		f.FailNext(fail)
		if _, err := Verify(t.Context(), f); !errors.Is(err, fail) {
			t.Errorf("verifying failed with %v, want the site's own %T", err, fail)
		}
	}
}

func TestRefuseNonCloud_LetsThroughASiteThatWillNotSayWhatItIs(t *testing.T) {
	t.Parallel()

	for _, fail := range siteFailures() {
		f := jiratest.New(jiratest.WithServerInfo(jira.ServerInfo{DeploymentType: "Server"}))
		f.FailNext(fail)
		if err := refuseNonCloud(t.Context(), f); err != nil {
			t.Errorf("a ServerInfo failure of %T refused the site: %v", fail, err)
		}
	}
}
