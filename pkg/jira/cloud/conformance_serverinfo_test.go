package cloud

import (
	"errors"
	"net/http"
	"testing"
	"time"

	"github.com/varijkapil13/saral/pkg/jira"
	"github.com/varijkapil13/saral/pkg/jira/jiratest"
)

// One set of assertions about what a site says about itself, run against both
// adapters. Onboarding refuses a site that is not Jira Cloud before it ever
// tries against one, so both adapters have to agree on Cloud() for a Cloud site
// and for one that names itself something else.

type serverInfoBuilder func(*testing.T) jira.ServerInfoReader

func serverInfoFromSite(t *testing.T, opts ...jiratest.ServerOption) jira.ServerInfoReader {
	t.Helper()

	s := jiratest.NewServer(opts...)
	t.Cleanup(s.Close)
	c, _ := testClient(t, s.URL())
	return c
}

func TestServerInfo_BothAdaptersAnswerTheSameWay(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name   string
		cloud  serverInfoBuilder
		fake   serverInfoBuilder
		assert func(*testing.T, jira.ServerInfo, error)
	}{
		{
			name:  "a Cloud site says so",
			cloud: func(t *testing.T) jira.ServerInfoReader { return serverInfoFromSite(t) },
			fake:  func(t *testing.T) jira.ServerInfoReader { return jiratest.New() },
			assert: func(t *testing.T, got jira.ServerInfo, err error) {
				t.Helper()
				if err != nil {
					t.Fatalf("ServerInfo: %v", err)
				}
				if !got.Cloud() {
					t.Errorf("Cloud() is false for %+v", got)
				}
				if got.DeploymentType != jira.DeploymentCloud {
					t.Errorf("DeploymentType = %q, want %q", got.DeploymentType, jira.DeploymentCloud)
				}
				if got.BaseURL == "" || got.Version == "" {
					t.Errorf("the site came back with %+v unset", got)
				}
			},
		},
		{
			name: "a non-Cloud site says so too",
			cloud: func(t *testing.T) jira.ServerInfoReader {
				return serverInfoFromSite(t, jiratest.WithFixture(http.MethodGet, serverInfoPath, "server_info_server.json"))
			},
			fake: func(t *testing.T) jira.ServerInfoReader {
				return jiratest.New(jiratest.WithServerInfo(jira.ServerInfo{
					BaseURL: "https://jira.example.invalid", Version: "9.12.4",
					DeploymentType: "Server", ServerTitle: "Example Jira",
				}))
			},
			assert: func(t *testing.T, got jira.ServerInfo, err error) {
				t.Helper()
				if err != nil {
					t.Fatalf("ServerInfo: %v", err)
				}
				if got.Cloud() {
					t.Errorf("Cloud() is true for %+v", got)
				}
				if got.DeploymentType == jira.DeploymentCloud {
					t.Errorf("DeploymentType = %q, want anything but Cloud", got.DeploymentType)
				}
			},
		},
		{
			name: "a rate limit is a rate limit",
			cloud: func(t *testing.T) jira.ServerInfoReader {
				return serverInfoFromSite(t, jiratest.WithRateLimit(http.MethodGet, serverInfoPath, 30*time.Second))
			},
			fake: func(t *testing.T) jira.ServerInfoReader {
				f := jiratest.New()
				f.FailNext(&jira.RateLimitError{RetryAfter: 30 * time.Second})
				return f
			},
			assert: func(t *testing.T, got jira.ServerInfo, err error) {
				t.Helper()
				var limited *jira.RateLimitError
				if !errors.As(err, &limited) {
					t.Fatalf("got %+v, %T (%v); want a *jira.RateLimitError", got, err, err)
				}
			},
		},
		{
			name: "a transport failure is a transport failure",
			cloud: func(t *testing.T) jira.ServerInfoReader {
				return serverInfoFromSite(t, jiratest.WithHandler(http.MethodGet, serverInfoPath,
					jsonHandler(http.StatusBadGateway, `{"errorMessages":["upstream is unwell"]}`)))
			},
			fake: func(t *testing.T) jira.ServerInfoReader {
				f := jiratest.New()
				f.FailNext(&jira.TransportError{Op: "ServerInfo", Status: http.StatusBadGateway, Err: errors.New("upstream is unwell")})
				return f
			},
			assert: func(t *testing.T, got jira.ServerInfo, err error) {
				t.Helper()
				var down *jira.TransportError
				if !errors.As(err, &down) {
					t.Fatalf("got %+v, %T (%v); want a *jira.TransportError", got, err, err)
				}
			},
		},
	}

	for _, tt := range cases {
		for _, adapter := range []struct {
			name string
			open serverInfoBuilder
		}{
			{name: "cloud", open: tt.cloud},
			{name: "fake", open: tt.fake},
		} {
			t.Run(tt.name+"/"+adapter.name, func(t *testing.T) {
				t.Parallel()

				got, err := adapter.open(t).ServerInfo(t.Context())
				tt.assert(t, got, err)
			})
		}
	}
}
