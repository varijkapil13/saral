package main

import (
	"bytes"
	"context"
	"errors"
	"net/url"
	"path/filepath"
	"strings"
	"testing"

	"github.com/varijkapil13/saral/internal/config"
	"github.com/varijkapil13/saral/internal/store"
	"github.com/varijkapil13/saral/pkg/jira"
	"github.com/varijkapil13/saral/pkg/jira/jiratest"
)

type capsFailing struct {
	*jiratest.Fake
	err error
}

func (c capsFailing) Capabilities(context.Context, string) (jira.Capabilities, error) {
	return jira.Capabilities{}, c.err
}

func doctorWith(t *testing.T, client jira.SessionClient, args ...string) (out string, err error) {
	t.Helper()
	opt := options{}
	if client != nil {
		opt.connectVia = func(config.Profile) (jira.SessionClient, error) { return client, nil }
	}
	var stdout, stderr bytes.Buffer
	err = runDoctor(&invocation{opt: opt, stdout: &stdout, stderr: &stderr}, args)
	return stdout.String(), err
}

func failingFake(err error) *jiratest.Fake {
	f := jiratest.New(jiratest.WithProject("PROJ", jiratest.Scrum))
	f.FailNext(err)
	return f
}

func TestDoctor_AHealthySetup(t *testing.T) {
	writeProfile(t)
	t.Setenv("SARAL_TEST_TOKEN", "s3cr3t-token-value")

	out, err := doctorWith(t, jiratest.New(jiratest.WithProject("PROJ", jiratest.Scrum)), "--project", "PROJ")
	if err != nil {
		t.Fatalf("doctor on a healthy setup: %v\n%s", err, out)
	}
	for _, want := range []string{
		"config", "profile", "work (from config.toml)", "example.atlassian.net", "/myself", "deployment",
		"Cloud", "boards yes", "cache", "proxy", "glyphs", "terminal", "TERM=",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("doctor does not report %q:\n%s", want, out)
		}
	}
	if strings.Contains(out, "FAIL") {
		t.Errorf("a healthy setup failed a check:\n%s", out)
	}
}

func TestDoctor_OutputIsSafeToPaste(t *testing.T) {
	writeProfile(t)
	t.Setenv("SARAL_TEST_TOKEN", "s3cr3t-token-value")

	out, _ := doctorWith(t, nil)
	if !strings.Contains(out, "token") {
		t.Fatalf("no token row, so this proves nothing:\n%s", out)
	}
	for _, secret := range []string{"s3cr3t-token-value", "you@example.com"} {
		if strings.Contains(out, secret) {
			t.Errorf("doctor printed %q:\n%s", secret, out)
		}
	}
}

func TestDoctor_ExitCodes(t *testing.T) {
	tests := map[string]struct {
		client jira.SessionClient
		code   int
		row    string
		says   string
	}{
		"the site refuses the token": {
			client: failingFake(&jira.AuthError{}), code: exitAuth, row: "/myself", says: "authentication failed",
		},
		"the site rate-limits": {
			client: failingFake(&jira.RateLimitError{}), code: exitOther, row: "/myself", says: "rate limited",
		},
		"the site cannot be reached": {
			client: failingFake(&jira.TransportError{Op: "GET /myself", Err: errors.New("connection refused")}),
			code:   exitOther, row: "/myself", says: "connection refused",
		},
		"the token may not do this": {
			client: failingFake(&jira.CapabilityError{Reason: "needs Browse projects"}), code: exitOther, row: "/myself",
			says: "needs Browse projects",
		},
		"the probe is refused": {
			client: capsFailing{Fake: jiratest.New(), err: &jira.CapabilityError{Reason: "forbidden here"}},
			code:   exitOther, row: "capabilities", says: "forbidden here",
		},
	}
	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			writeProfile(t)
			out, err := doctorWith(t, tc.client)
			if got := exitCodeOf(err); got != tc.code {
				t.Fatalf("exit %d (%v), want %d:\n%s", got, err, tc.code, out)
			}
			if !errors.Is(err, errAlreadyReported) {
				t.Errorf("main would print the error a second time: %v", err)
			}
			failed := failedRow(out, tc.row)
			if failed == "" {
				t.Fatalf("the %s row did not fail:\n%s", tc.row, out)
			}
			if !strings.Contains(failed, tc.says) {
				t.Errorf("the failing row %q does not say %q", failed, tc.says)
			}
		})
	}
}

func failedRow(out, name string) string {
	for line := range strings.SplitSeq(out, "\n") {
		fields := strings.Fields(line)
		if len(fields) >= 2 && fields[0] == "FAIL" && fields[1] == name {
			return line
		}
	}
	return ""
}

func TestDoctor_ATokenThatDoesNotResolveIsAnAuthFailureAndReachesNoSite(t *testing.T) {
	writeProfile(t)
	t.Setenv("SARAL_TEST_TOKEN", "")

	out, err := doctorWith(t, nil)
	if got := exitCodeOf(err); got != exitAuth {
		t.Fatalf("exit %d, want %d:\n%s", got, exitAuth, out)
	}
	if !strings.Contains(failedRow(out, "token"), "SARAL_TEST_TOKEN") {
		t.Errorf("the token row does not name the variable it looked in:\n%s", out)
	}
	if strings.Contains(out, "/myself") {
		t.Errorf("doctor went on to the site with no token:\n%s", out)
	}
}

func TestDoctor_NoProfileIsAConfigFailure(t *testing.T) {
	isolated(t)
	out, err := doctorWith(t, nil)
	if got := exitCodeOf(err); got != exitConfig {
		t.Fatalf("exit %d, want %d:\n%s", got, exitConfig, out)
	}
	if !strings.Contains(failedRow(out, "profile"), envSite) {
		t.Errorf("the profile row does not say how to supply one:\n%s", out)
	}
}

func TestDoctor_AnUnreadableConfigIsAConfigFailure(t *testing.T) {
	cfgDir, _ := isolated(t)
	writeConfigFile(t, cfgDir, "active = [")
	out, err := doctorWith(t, nil)
	if got := exitCodeOf(err); got != exitConfig {
		t.Fatalf("exit %d, want %d:\n%s", got, exitConfig, out)
	}
	if failedRow(out, "config") == "" {
		t.Errorf("the config row did not fail:\n%s", out)
	}
}

func TestDoctor_RefusesArguments(t *testing.T) {
	isolated(t)
	if _, err := doctorWith(t, nil, "extra"); exitCodeOf(err) != exitUsage {
		t.Errorf("doctor extra: %v", err)
	}
}

func TestDoctor_ReportsTheCache(t *testing.T) {
	writeProfile(t)
	f := jiratest.New()
	out, _ := doctorWith(t, f)
	if !strings.Contains(out, "not written yet") {
		t.Errorf("a missing cache is not named:\n%s", out)
	}

	dir, err := config.CacheDir()
	if err != nil {
		t.Fatal(err)
	}
	held, err := store.Open(filepath.Join(dir, cacheFile))
	if err != nil {
		t.Fatal(err)
	}
	out, _ = doctorWith(t, f)
	if !strings.Contains(out, "open in another copy of Saral") {
		t.Errorf("a held cache is not named:\n%s", out)
	}
	if err := held.Close(); err != nil {
		t.Fatal(err)
	}
	out, _ = doctorWith(t, f)
	if !strings.Contains(out, "readable") {
		t.Errorf("a healthy cache is not named:\n%s", out)
	}
}

func TestRedactProxy(t *testing.T) {
	u, err := url.Parse("http://user:hunter2@proxy.example.com:3128")
	if err != nil {
		t.Fatal(err)
	}
	got := redactProxy(u)
	if strings.Contains(got, "hunter2") || strings.Contains(got, "user") || !strings.Contains(got, "proxy.example.com:3128") {
		t.Errorf("redactProxy = %q", got)
	}
}

func TestRun_DoctorIsReachedThroughTheCommandLine(t *testing.T) {
	isolated(t)
	var out bytes.Buffer
	err := run([]string{"doctor"}, &out, &bytes.Buffer{})
	if exitCodeOf(err) != exitConfig || !strings.Contains(out.String(), "profile") {
		t.Errorf("saral doctor with nothing configured: %v\n%s", err, out.String())
	}
}
