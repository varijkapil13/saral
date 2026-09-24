package main

import (
	"bytes"
	"context"
	"errors"
	"io"
	"log"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/varijkapil13/saral/pkg/jira/cloud"
	"github.com/varijkapil13/saral/pkg/jira/jiratest"
)

func TestRedactAttr(t *testing.T) {
	tests := map[string]struct {
		attr slog.Attr
		gone string
		kept string
	}{
		"an Authorization header":     {attr: slog.String("Authorization", "Basic eW91OnRva2Vu"), gone: "eW91OnRva2Vu"},
		"a token by name":             {attr: slog.String("api_token", "t0k3n"), gone: "t0k3n"},
		"an email by name":            {attr: slog.String("email", "you@example.com"), gone: "you@example.com"},
		"basic auth inside a message": {attr: slog.String("msg", "sent Basic eW91OnRva2Vu upstream"), gone: "eW91OnRva2Vu", kept: "upstream"},
		"bearer inside a message":     {attr: slog.String("error", "bearer abc.def-ghi refused"), gone: "abc.def-ghi", kept: "refused"},
		"an email inside a message":   {attr: slog.String("error", "no account for you@example.com here"), gone: "you@example.com", kept: "here"},
		"an ordinary path":            {attr: slog.String("path", "/rest/api/3/myself"), kept: "/rest/api/3/myself"},
		"a number":                    {attr: slog.Int("status", 401), kept: "401"},
	}
	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			got := redactAttr(nil, tc.attr).Value.String()
			if tc.gone != "" && strings.Contains(got, tc.gone) {
				t.Errorf("%s survived as %q", tc.gone, got)
			}
			if tc.kept != "" && !strings.Contains(got, tc.kept) {
				t.Errorf("%q lost %q", got, tc.kept)
			}
		})
	}
}

type stubDoer struct {
	status int
	body   []byte
	err    error
	seen   *http.Request
}

func (s *stubDoer) Do(req *http.Request) (*http.Response, error) {
	s.seen = req
	if s.err != nil {
		return nil, s.err
	}
	return &http.Response{
		StatusCode: s.status,
		Header:     http.Header{"Content-Type": {"application/json"}},
		Body:       io.NopCloser(bytes.NewReader(s.body)),
		Request:    req,
	}, nil
}

func TestLoggingDoer_LogsTheRequestAndNothingSecret(t *testing.T) {
	body, err := jiratest.Fixture("myself.json")
	if err != nil {
		t.Fatal(err)
	}
	var buf bytes.Buffer
	stub := &stubDoer{status: http.StatusOK, body: body}
	client, err := cloud.New("example.atlassian.net", "you@example.com", "t0k3n-value",
		cloud.WithHTTPClient(loggingDoer{next: stub, log: newLogger(&buf), now: time.Now}))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := client.Me(context.Background()); err != nil {
		t.Fatalf("Me: %v", err)
	}
	if stub.seen == nil || stub.seen.Header.Get("Authorization") == "" {
		t.Fatal("the request carried no credentials, so their absence from the log proves nothing")
	}
	got := buf.String()
	for _, want := range []string{"method=GET", "path=/rest/api/3/myself", "status=200", "took="} {
		if !strings.Contains(got, want) {
			t.Errorf("the log line %q does not carry %s", got, want)
		}
	}
	for _, secret := range []string{"t0k3n-value", "you@example.com", stub.seen.Header.Get("Authorization"), "Basic"} {
		if strings.Contains(got, secret) {
			t.Errorf("the log carries %q: %s", secret, got)
		}
	}
}

func TestLoggingDoer_LeavesTheQueryStringOut(t *testing.T) {
	var buf bytes.Buffer
	d := loggingDoer{next: &stubDoer{status: http.StatusOK}, log: newLogger(&buf), now: time.Now}
	req, err := http.NewRequest(http.MethodGet, "https://example.atlassian.net/rest/api/3/search/jql?jql=summary~secret", http.NoBody)
	if err != nil {
		t.Fatal(err)
	}
	resp, err := d.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	_ = resp.Body.Close()
	if strings.Contains(buf.String(), "secret") || !strings.Contains(buf.String(), "/rest/api/3/search/jql") {
		t.Errorf("log line %q", buf.String())
	}
}

func TestLoggingDoer_LogsATransportFailureAndPassesItOn(t *testing.T) {
	var buf bytes.Buffer
	cause := errors.New("connection refused")
	d := loggingDoer{next: &stubDoer{err: cause}, log: newLogger(&buf), now: time.Now}
	req, err := http.NewRequest(http.MethodGet, "https://example.atlassian.net/rest/api/3/myself", http.NoBody)
	if err != nil {
		t.Fatal(err)
	}
	if resp, err := d.Do(req); !errors.Is(err, cause) {
		if resp != nil {
			_ = resp.Body.Close()
		}
		t.Errorf("the failure became %v", err)
	}
	if !strings.Contains(buf.String(), "connection refused") {
		t.Errorf("log line %q does not carry the failure", buf.String())
	}
}

func TestConnectWith_ALoggerSeesRealRequestsOnLoopback(t *testing.T) {
	srv := jiratest.NewServer()
	defer srv.Close()
	var buf bytes.Buffer
	client, err := connectWith(newLogger(&buf))(srv.URL(), "you@example.com", "t0k3n-value")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := client.Me(context.Background()); err != nil {
		t.Fatalf("Me: %v", err)
	}
	if !strings.Contains(buf.String(), "path=/rest/api/3/myself") || !strings.Contains(buf.String(), "status=200") {
		t.Errorf("log %q", buf.String())
	}
}

func TestRun_LogFlagOpensTheFile(t *testing.T) {
	writeProfile(t)
	t.Setenv("SARAL_TEST_TOKEN", "a-token")
	prevDefault := slog.Default()
	t.Cleanup(func() {
		slog.SetDefault(prevDefault)
		log.SetOutput(os.Stderr)
	})
	path := filepath.Join(t.TempDir(), "saral.log")

	if err := run([]string{"--log", path, "--bench-first-paint"}, &bytes.Buffer{}, &bytes.Buffer{}); err != nil {
		t.Fatalf("run: %v", err)
	}
	if _, err := os.Stat(path); err != nil {
		t.Errorf("--log did not create the file: %v", err)
	}
}

func TestRun_ALogFileThatCannotBeOpenedIsAUsageError(t *testing.T) {
	isolated(t)
	path := filepath.Join(t.TempDir(), "missing-dir", "saral.log")
	err := run([]string{"--log", path, "--bench-first-paint"}, &bytes.Buffer{}, &bytes.Buffer{})
	if exitCodeOf(err) != exitUsage {
		t.Errorf("an unwritable --log: %v (exit %d)", err, exitCodeOf(err))
	}
}

func TestRedactEmail(t *testing.T) {
	for in, want := range map[string]string{"you@example.com": "y…@example.com", "ünï@example.com": "ü…@example.com", "nobody": redacted, "@x": redacted} {
		if got := redactEmail(in); got != want {
			t.Errorf("redactEmail(%q) = %q, want %q", in, got, want)
		}
	}
}
