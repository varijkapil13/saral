//go:build demo

package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/varijkapil13/saral/internal/config"
	"github.com/varijkapil13/saral/pkg/jira"
	"github.com/varijkapil13/saral/pkg/jira/jiratest"
)

const (
	fakeProfile = "demo"
	fakeProject = "PROJ"
	fakeIssues  = 60

	fakeSprintIssues = 18
)

func init() {
	hiddenFlags["fake"] = true
	extraRootFlags = append(extraRootFlags, func(fs *flag.FlagSet, opt *options) {
		fs.BoolVar(&opt.fake, "fake", false, "demo mode: synthetic issues in memory, no site, no token, nothing saved")
	})
	startFake = useFakeSite
}

func useFakeSite(opt *options) (cleanup func(), err error) {
	cleanup = func() {}
	root, err := os.MkdirTemp("", "saral-fake-")
	if err != nil {
		return cleanup, err
	}
	cleanup = func() { _ = os.RemoveAll(root) }
	cfgDir, cacheDir := filepath.Join(root, "config"), filepath.Join(root, "cache")
	for _, dir := range []string{cfgDir, cacheDir} {
		if err := os.MkdirAll(dir, 0o700); err != nil {
			return cleanup, err
		}
	}

	client, me, err := newFakeClient()
	if err != nil {
		return cleanup, err
	}
	cfg := config.Config{
		Active: fakeProfile,
		Mouse:  true,
		Profiles: map[string]config.Profile{fakeProfile: {
			Name:    fakeProfile,
			Site:    "example.atlassian.net",
			Email:   me.Email,
			Project: fakeProject,
			Token:   config.TokenSource{Env: "SARAL_FAKE_TOKEN_UNUSED"},
		}},
	}
	if err := cfg.Save(filepath.Join(cfgDir, fileNameTOML)); err != nil {
		return cleanup, err
	}
	for name, value := range map[string]string{"SARAL_CONFIG_DIR": cfgDir, "SARAL_CACHE_DIR": cacheDir} {
		if err := os.Setenv(name, value); err != nil {
			return cleanup, err
		}
	}
	for _, name := range []string{envProfile, envSite, envEmail, envToken} {
		if err := os.Unsetenv(name); err != nil {
			return cleanup, err
		}
	}
	opt.profile = fakeProfile
	opt.connectVia = func(config.Profile) (jira.SessionClient, error) { return client, nil }
	return cleanup, nil
}

// Every third issue goes to the fake's own account, so the list's opening query has rows.
func newFakeClient() (*jiratest.Fake, jira.User, error) {
	me, err := jiratest.New().Me(context.Background())
	if err != nil {
		return nil, jira.User{}, fmt.Errorf("the demo site has no account: %w", err)
	}
	issues := jiratest.GenFor(fakeProject, fakeIssues)
	for i := range issues {
		if i%3 == 0 {
			issues[i].Assignee = &me
		}
	}
	fake := jiratest.New(jiratest.WithProject(fakeProject, jiratest.Scrum), jiratest.WithIssues(issues))
	if err := seedRunningSprint(context.Background(), fake, issues, time.Now()); err != nil {
		return nil, jira.User{}, fmt.Errorf("the demo board has no running sprint: %w", err)
	}
	return fake, me, nil
}

// The fake dates its sprints around a fixed epoch and fills none, so the board would draw an empty sprint long over.
func seedRunningSprint(ctx context.Context, fake *jiratest.Fake, issues []jira.Issue, now time.Time) error {
	boards, err := fake.Boards(ctx, fakeProject)
	if err != nil || len(boards) == 0 {
		return fmt.Errorf("no board for %s: %w", fakeProject, err)
	}
	running, err := fake.Sprints(ctx, boards[0].ID, jira.SprintActive)
	if err != nil || len(running.Items) == 0 {
		return fmt.Errorf("no active sprint on %s: %w", boards[0].Name, err)
	}
	sprint := running.Items[0]
	start, end := now.AddDate(0, 0, -6), now.AddDate(0, 0, 8)
	if _, err := fake.UpdateSprint(ctx, sprint.ID, jira.SprintPatch{Start: &start, End: &end}); err != nil {
		return err
	}
	var keys []string
	for i := range issues {
		if i%2 == 0 && len(keys) < fakeSprintIssues {
			keys = append(keys, issues[i].Key)
		}
	}
	return fake.MoveToSprint(ctx, sprint.ID, keys)
}
