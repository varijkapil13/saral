//go:build demo

package main

import (
	"bytes"
	"context"
	"errors"
	"os"
	"strconv"
	"strings"
	"testing"

	"github.com/varijkapil13/saral/pkg/jira"
	"github.com/varijkapil13/saral/pkg/jira/jiratest"
)

func TestFake_ASessionWithNoSiteNoTokenAndNothingLeftBehind(t *testing.T) {
	isolated(t)
	t.Setenv(envSite, "somewhere.example.com")
	t.Setenv(envToken, "a-real-token")

	opt := options{fake: true}
	cleanup, err := useFakeSite(&opt)
	if err != nil {
		cleanup()
		t.Fatal(err)
	}
	root := os.Getenv("SARAL_CONFIG_DIR")
	deps, _, notice, release, err := build(opt)
	if err != nil {
		cleanup()
		t.Fatalf("build: %v", err)
	}
	if notice != "" {
		t.Errorf("the demo starts with a warning: %q", notice)
	}
	if _, ok := deps.Jira.(*jiratest.Fake); !ok {
		t.Fatalf("the demo session talks to a %T", deps.Jira)
	}
	if deps.Site != "example.atlassian.net" || deps.Project != fakeProject {
		t.Errorf("the environment reached the demo: site %q, project %q", deps.Site, deps.Project)
	}
	page, err := deps.Jira.Search(context.Background(), jira.Query{
		JQL:    `project = "` + fakeProject + `" AND assignee = currentUser() ORDER BY updated DESC`,
		Fields: []string{"summary"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(page.Items) == 0 {
		t.Error("the issue list's opening query finds nothing on the demo site")
	}
	release()
	cleanup()
	if _, err := os.Stat(root); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("the demo left %s behind: %v", root, err)
	}
}

func TestRun_FakeDrawsAFrame(t *testing.T) {
	isolated(t)
	var out, errOut bytes.Buffer
	if err := run([]string{"-fake", "--bench-first-paint"}, &out, &errOut); err != nil {
		t.Fatalf("run: %v (stderr %q)", err, errOut.String())
	}
	if _, err := strconv.Atoi(strings.TrimSpace(out.String())); err != nil {
		t.Errorf("stdout is %q", out.String())
	}
	if errOut.Len() > 0 {
		t.Errorf("the demo warned: %q", errOut.String())
	}
}

func TestRun_FakeDoctorIsHealthy(t *testing.T) {
	isolated(t)
	var out bytes.Buffer
	if err := run([]string{"-fake", "doctor"}, &out, &bytes.Buffer{}); err != nil {
		t.Fatalf("doctor on the demo site: %v\n%s", err, out.String())
	}
}
