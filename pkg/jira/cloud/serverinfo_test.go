package cloud

import (
	"context"
	"errors"
	"net/http"
	"testing"
	"time"

	"github.com/varijkapil13/saral/pkg/jira"
	"github.com/varijkapil13/saral/pkg/jira/jiratest"
)

func TestServerInfo_DecodesTheCloudFixtureAndReportsCloud(t *testing.T) {
	t.Parallel()

	s := jiratest.NewServer()
	defer s.Close()

	c, _ := testClient(t, s.URL())
	got, err := c.ServerInfo(t.Context())
	if err != nil {
		t.Fatalf("ServerInfo: %v", err)
	}
	if got.BaseURL != "https://example.atlassian.net" {
		t.Errorf("BaseURL = %q", got.BaseURL)
	}
	if got.Version != "1001.0.0-SNAPSHOT" {
		t.Errorf("Version = %q", got.Version)
	}
	if got.BuildNumber != 100287 {
		t.Errorf("BuildNumber = %d, want 100287", got.BuildNumber)
	}
	if got.ServerTitle != "Example Jira" {
		t.Errorf("ServerTitle = %q", got.ServerTitle)
	}
	if got.DeploymentType != jira.DeploymentCloud {
		t.Errorf("DeploymentType = %q, want %q", got.DeploymentType, jira.DeploymentCloud)
	}
	if !got.Cloud() {
		t.Error("Cloud() is false for a site whose deploymentType is Cloud")
	}
}

func TestServerInfo_ReadsTheServerFixtureAsNotCloud(t *testing.T) {
	t.Parallel()

	s := jiratest.NewServer(jiratest.WithFixture(http.MethodGet, serverInfoPath, "server_info_server.json"))
	defer s.Close()

	c, _ := testClient(t, s.URL())
	got, err := c.ServerInfo(t.Context())
	if err != nil {
		t.Fatalf("ServerInfo: %v", err)
	}
	if got.DeploymentType != "Server" {
		t.Errorf("DeploymentType = %q, want %q", got.DeploymentType, "Server")
	}
	if got.Cloud() {
		t.Error("Cloud() is true for a site whose deploymentType is Server")
	}
}

func TestServerInfo_A401IsAnAuthError(t *testing.T) {
	t.Parallel()

	s := jiratest.NewServer(jiratest.WithHandler(http.MethodGet, serverInfoPath,
		jsonHandler(http.StatusUnauthorized, `{"errorMessages":["Client must be authenticated to access this resource."]}`)))
	defer s.Close()

	c, _ := testClient(t, s.URL())
	_, err := c.ServerInfo(t.Context())

	var unauth *jira.AuthError
	if !errors.As(err, &unauth) {
		t.Fatalf("got %T (%v), want a *jira.AuthError", err, err)
	}
}

func TestServerInfo_A404IsANotFoundError(t *testing.T) {
	t.Parallel()

	s := jiratest.NewServer(jiratest.WithHandler(http.MethodGet, serverInfoPath,
		jsonHandler(http.StatusNotFound, `{"errorMessages":["Not found."]}`)))
	defer s.Close()

	c, _ := testClient(t, s.URL())
	_, err := c.ServerInfo(t.Context())

	var missing *jira.NotFoundError
	if !errors.As(err, &missing) {
		t.Fatalf("got %T (%v), want a *jira.NotFoundError", err, err)
	}
}

func TestServerInfo_A429CarriesTheWaitTheSiteAskedFor(t *testing.T) {
	t.Parallel()

	s := jiratest.NewServer(jiratest.WithRateLimit(http.MethodGet, serverInfoPath, 30*time.Second))
	defer s.Close()

	c, _ := testClient(t, s.URL(), WithRetry(RetryPolicy{Attempts: 1}))
	_, err := c.ServerInfo(t.Context())

	var limited *jira.RateLimitError
	if !errors.As(err, &limited) {
		t.Fatalf("got %T (%v), want a *jira.RateLimitError", err, err)
	}
	if limited.RetryAfter != 30*time.Second {
		t.Errorf("RetryAfter = %v, want 30s", limited.RetryAfter)
	}
}

func TestServerInfo_ABadGatewayIsATransportFailure(t *testing.T) {
	t.Parallel()

	s := jiratest.NewServer(jiratest.WithHandler(http.MethodGet, serverInfoPath,
		jsonHandler(http.StatusBadGateway, `{"errorMessages":["upstream is unwell"]}`)))
	defer s.Close()

	c, _ := testClient(t, s.URL(), WithRetry(RetryPolicy{Attempts: 1}))
	_, err := c.ServerInfo(t.Context())

	var down *jira.TransportError
	if !errors.As(err, &down) {
		t.Fatalf("got %T (%v), want a *jira.TransportError", err, err)
	}
	if down.Status != http.StatusBadGateway {
		t.Errorf("Status = %d, want %d", down.Status, http.StatusBadGateway)
	}
}

func TestServerInfo_AHostThatNeverAnsweredIsATransportFailure(t *testing.T) {
	t.Parallel()

	s := jiratest.NewServer()
	site := s.URL()
	s.Close()

	c, _ := testClient(t, site, WithRetry(RetryPolicy{Attempts: 1}))
	_, err := c.ServerInfo(t.Context())

	var down *jira.TransportError
	if !errors.As(err, &down) {
		t.Fatalf("got %T (%v), want a *jira.TransportError", err, err)
	}
	if down.Status != 0 {
		t.Errorf("Status = %d, want 0: the request never reached a server", down.Status)
	}
}

func TestServerInfo_ABodyItCannotReadIsATransportFailure(t *testing.T) {
	t.Parallel()

	s := jiratest.NewServer(jiratest.WithHandler(http.MethodGet, serverInfoPath,
		jsonHandler(http.StatusOK, `{"baseUrl":`)))
	defer s.Close()

	c, _ := testClient(t, s.URL(), WithRetry(RetryPolicy{Attempts: 1}))
	_, err := c.ServerInfo(t.Context())

	var down *jira.TransportError
	if !errors.As(err, &down) {
		t.Fatalf("got %T (%v), want a *jira.TransportError", err, err)
	}
	if down.Status != http.StatusOK {
		t.Errorf("Status = %d, want the 200 the body arrived with", down.Status)
	}
}

func TestServerInfo_ReturnsTheCallersOwnErrorWhenItCancels(t *testing.T) {
	t.Parallel()

	arrived, announce := gate()
	release, letGo := gate()
	s := jiratest.NewServer(jiratest.WithHandler(http.MethodGet, serverInfoPath, func(_ http.ResponseWriter, r *http.Request) {
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
	go func() {
		_, err := c.ServerInfo(ctx)
		failed <- err
	}()

	receive(t, "the request to reach the site", arrived)
	cancel()
	if err := receive(t, "the cancelled call to come back", failed); !errors.Is(err, context.Canceled) {
		t.Fatalf("got %v, want the context's own error", err)
	}
}
