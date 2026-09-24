package onboarding

import (
	"context"
	"errors"
	"net"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/zalando/go-keyring"

	"github.com/varijkapil13/saral/internal/config"
	"github.com/varijkapil13/saral/pkg/jira"
	"github.com/varijkapil13/saral/pkg/jira/jiratest"
)

// refusesServerInfo is the fake with only the deployment probe broken, which
// FailNext cannot reach: the identity check is always the call before it.
type refusesServerInfo struct {
	*jiratest.Fake
	err error
}

func (r refusesServerInfo) ServerInfo(context.Context) (jira.ServerInfo, error) {
	return jira.ServerInfo{}, r.err
}

// recordingConnector hands out client and remembers the credentials it was
// asked to connect with.
type recordingConnector struct {
	mu     sync.Mutex
	client jira.SessionClient
	token  string
}

func (r *recordingConnector) connect(_, _, token string) (jira.SessionClient, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.token = token
	return r.client, nil
}

func (r *recordingConnector) got() string {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.token
}

func TestToken_APastedTrailingNewlineIsNotPartOfTheToken(t *testing.T) {
	keyring.MockInit()
	t.Cleanup(keyring.MockInit)

	rec := &recordingConnector{client: testFake()}
	d := newDriverWith(t, rec.connect)

	d.typeIn(testSite)
	d.press("enter")
	d.typeIn(testEmail)
	d.press("enter")
	d.send(tea.PasteMsg{Content: testToken + "\n"})
	d.press("enter")

	d.atStep(stepStorage)
	if got := rec.got(); got != testToken {
		t.Fatalf("the site was asked about token %q, want %q", got, testToken)
	}
	d.press("enter")
	d.typeIn("PROJ")
	d.press("enter", "enter")
	d.atStep(stepDone)
	if d.model().problem != "" {
		t.Errorf("saving reported a problem: %s", d.model().problem)
	}

	stored, err := keyring.Get("saral", "example")
	if err != nil {
		t.Fatalf("reading the keychain back: %v", err)
	}
	if stored != testToken {
		t.Errorf("the keychain holds %q, want %q", stored, testToken)
	}
	cfg, err := config.LoadFile(d.path)
	if err != nil {
		t.Fatalf("loading back: %v", err)
	}
	profile, err := cfg.Current()
	if err != nil {
		t.Fatalf("no usable profile: %v", err)
	}
	if got, _ := profile.ResolveToken(t.Context()); got != testToken {
		t.Errorf("the written profile resolves to %q, want %q", got, testToken)
	}
}

func TestFlow_ABareSiteNameThatCannotBeReachedSuggestsTheCloudDomain(t *testing.T) {
	t.Parallel()

	fake := testFake()
	fake.FailNext(&jira.TransportError{
		Op:  "GET /rest/api/3/myself",
		Err: &net.OpError{Op: "dial", Net: "tcp", Err: &net.DNSError{Err: "no such host", Name: "acme", IsNotFound: true}},
	})
	d := newDriver(t, fake)

	d.typeIn("acme")
	d.press("enter")
	d.typeIn(testEmail)
	d.press("enter")
	d.typeIn(testToken)
	d.press("enter")

	d.atStep(stepSite)
	d.mustContain("acme.atlassian.net")
	d.mustContain("Nothing was written")
	if got := d.model().value(fieldSite); got != "acme" {
		t.Errorf("the suggestion rewrote the site to %q; it is only a suggestion", got)
	}
	d.nothingWritten()
}

func TestFlow_ASiteWithADomainIsNotToldToAddOne(t *testing.T) {
	t.Parallel()

	fake := testFake()
	fake.FailNext(&jira.TransportError{Op: "GET /rest/api/3/myself", Err: &net.DNSError{Err: "no such host", Name: testSite}})
	d := newDriver(t, fake)
	d.credentials()

	d.atStep(stepSite)
	if strings.Contains(d.frame(), "has no domain") {
		t.Errorf("a site with a domain was told it has none:\n%s", d.frame())
	}
}

func TestFlow_ABareSiteThatAnsweredWithAnHTTPErrorIsNotToldToAddADomain(t *testing.T) {
	t.Parallel()

	fake := testFake()
	fake.FailNext(&jira.TransportError{Op: "GET /rest/api/3/myself", Status: 502})
	d := newDriver(t, fake)
	d.typeIn("acme")
	d.press("enter")
	d.typeIn(testEmail)
	d.press("enter")
	d.typeIn(testToken)
	d.press("enter")

	d.atStep(stepSite)
	if strings.Contains(d.frame(), "has no domain") {
		t.Errorf("a host that answered was told it does not exist:\n%s", d.frame())
	}
}

func TestFlow_ASiteThatIsNotJiraCloudIsRefusedBeforeAnythingIsWritten(t *testing.T) {
	t.Parallel()

	for _, deployment := range []jira.DeploymentType{"Server", "DataCenter"} {
		t.Run(string(deployment), func(t *testing.T) {
			t.Parallel()

			d := newDriver(t, testFake(jiratest.WithServerInfo(jira.ServerInfo{DeploymentType: deployment})))
			d.credentials()

			d.atStep(stepSite)
			d.mustContain("Jira Data Center or Server, which is not supported yet")
			d.mustContain("Only Jira Cloud sites")
			if d.model().client != nil {
				t.Error("a refused site was kept as the connected client")
			}
			d.nothingWritten()
			d.noTokenAnywhere()
		})
	}
}

// The identity check has already answered on the Cloud API by the time the
// deployment is asked about, so a probe that fails is no reason to refuse.
func TestFlow_ADeploymentProbeThatFailsDoesNotBlockSetup(t *testing.T) {
	t.Parallel()

	for name, err := range map[string]error{
		"403":       &jira.CapabilityError{Reason: "not permitted"},
		"429":       &jira.RateLimitError{RetryAfter: time.Second},
		"transport": &jira.TransportError{Op: "GET /rest/api/3/serverInfo", Err: errors.New("connection reset")},
		"no answer": nil,
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			var client jira.SessionClient = refusesServerInfo{Fake: testFake(), err: err}
			if err == nil {
				client = testFake(jiratest.WithServerInfo(jira.ServerInfo{}))
			}
			d := newDriverWith(t, connectorFor(client))
			d.credentials()

			d.atStep(stepStorage)
		})
	}
}

func TestFlow_ConnectFailuresStayOnTheTokenAndSayWhy(t *testing.T) {
	t.Parallel()

	for name, tc := range map[string]struct {
		err  error
		want string
		on   step
	}{
		"403":       {&jira.CapabilityError{Reason: "this token may not browse users"}, "may not browse users", stepToken},
		"429":       {&jira.RateLimitError{RetryAfter: 30 * time.Second}, "rate limited", stepToken},
		"transport": {&jira.TransportError{Op: "GET /rest/api/3/myself", Err: errors.New("connection reset")}, "connection reset", stepSite},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			fake := testFake()
			fake.FailNext(tc.err)
			d := newDriver(t, fake)
			d.credentials()

			d.atStep(tc.on)
			d.mustContain(tc.want)
			d.nothingWritten()
			d.noTokenAnywhere()

			for range stepToken - tc.on + 1 {
				d.press("enter")
			}
			d.atStep(stepStorage)
		})
	}
}

func TestFlow_ProbeFailuresStayOnTheProjectAndWriteNothing(t *testing.T) {
	t.Parallel()

	for name, tc := range map[string]struct {
		err  error
		want string
		on   step
	}{
		"401":       {&jira.AuthError{Reason: "raw body"}, "Jira refused this token", stepToken},
		"403":       {&jira.CapabilityError{Reason: "this token may not browse PROJ"}, "may not browse PROJ", stepProject},
		"429":       {&jira.RateLimitError{RetryAfter: 30 * time.Second}, "rate limited", stepProject},
		"transport": {&jira.TransportError{Op: "GET /rest/api/3/mypermissions", Err: errors.New("connection reset")}, "connection reset", stepProject},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			fake := testFake()
			d := newDriver(t, fake)
			d.credentials()
			d.press("enter")
			d.typeIn("PROJ")
			fake.FailNext(tc.err)
			d.press("enter")

			d.atStep(tc.on)
			d.mustContain(tc.want)
			d.nothingWritten()
		})
	}
}

func TestSave_AConfigFileThatCannotBeWrittenIsReportedOnTheReview(t *testing.T) {
	keyring.MockInit()
	t.Cleanup(keyring.MockInit)

	d := newDriver(t, testFake())
	blocker := filepath.Join(d.dir, "not-a-dir")
	if err := os.WriteFile(blocker, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	d.send(configLoadedMsg{cfg: config.Config{Mouse: true}, path: filepath.Join(blocker, "config.toml")})

	d.credentials()
	d.press("enter")
	d.typeIn("PROJ")
	d.press("enter", "enter")

	d.atStep(stepReview)
	if d.model().problem == "" {
		t.Errorf("a write that cannot happen reported nothing:\n%s", d.frame())
	}
}

// TestSave_ASecondProfileKeepsTheFirstAndWhatWasWrittenSince is the point of
// writing through the file lock: the file is re-read at save time, so a profile
// and a setting written after this view loaded the file both survive.
func TestSave_ASecondProfileKeepsTheFirstAndWhatWasWrittenSince(t *testing.T) {
	keyring.MockInit()
	t.Cleanup(keyring.MockInit)

	d := newDriver(t, testFake())
	first := config.Config{
		Active: "work",
		Mouse:  true,
		Profiles: map[string]config.Profile{"work": {
			Name: "work", Site: "other.atlassian.net", Email: testEmail,
			Token: config.TokenSource{Env: "JIRA_TOKEN"}, Theme: "dark",
			Pinned: []string{"priority"},
		}},
	}
	if err := first.Save(d.path); err != nil {
		t.Fatal(err)
	}
	d.send(configLoadedMsg{cfg: first, path: d.path})

	if err := config.UpdateFile(d.path, func(cfg *config.Config) error {
		cfg.Mouse = false
		cfg.Profiles["late"] = config.Profile{
			Name: "late", Site: "late.atlassian.net", Email: testEmail,
			Token: config.TokenSource{Env: "LATE_TOKEN"},
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}

	d.credentials()
	d.press("enter")
	d.typeIn("PROJ")
	d.press("enter", "enter")
	d.atStep(stepDone)

	cfg, err := config.LoadFile(d.path)
	if err != nil {
		t.Fatalf("loading back: %v", err)
	}
	if cfg.Active != "example" {
		t.Errorf("active is %q, want the profile just set up", cfg.Active)
	}
	for _, name := range []string{"work", "late", "example"} {
		if _, err := cfg.Get(name); err != nil {
			t.Errorf("the file lost profile %s: %v", name, err)
		}
	}
	work, _ := cfg.Get("work")
	if work.Theme != "dark" || len(work.Pinned) != 1 {
		t.Errorf("the first profile's settings were dropped: %+v", work)
	}
	if cfg.Mouse {
		t.Error("the mouse setting written after the file was loaded was overwritten")
	}
}
