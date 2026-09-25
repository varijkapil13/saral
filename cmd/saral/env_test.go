package main

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/varijkapil13/saral/internal/app"
	"github.com/varijkapil13/saral/internal/config"
)

const twoProfiles = `active = "work"

[profiles.work]
site  = "example.atlassian.net"
email = "you@example.com"
token = { env = "SARAL_TEST_TOKEN" }

[profiles.other]
site  = "other.example.com"
email = "other@example.com"
token = { env = "SARAL_OTHER_TOKEN" }
`

func writeConfigFile(t *testing.T, dir, body string) string {
	t.Helper()
	path := filepath.Join(dir, "config.toml")
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestResolveProfile_Precedence(t *testing.T) {
	tests := map[string]struct {
		file    string
		flag    string
		env     map[string]string
		name    string
		site    string
		email   string
		token   string
		fromEnv bool
		fails   string
	}{
		"the file alone": {
			file: twoProfiles, name: "work", site: "example.atlassian.net", email: "you@example.com", token: "SARAL_TEST_TOKEN",
		},
		"SARAL_PROFILE picks a profile": {
			file: twoProfiles, env: map[string]string{envProfile: "other"}, name: "other", site: "other.example.com",
			email: "other@example.com", token: "SARAL_OTHER_TOKEN",
		},
		"--profile beats SARAL_PROFILE": {
			file: twoProfiles, flag: "work", env: map[string]string{envProfile: "other"}, name: "work",
			site: "example.atlassian.net", email: "you@example.com", token: "SARAL_TEST_TOKEN",
		},
		"SARAL_SITE and SARAL_EMAIL override the file": {
			file: twoProfiles, env: map[string]string{envSite: "https://Elsewhere.example.com/", envEmail: "ci@example.com"},
			name: "work", site: "elsewhere.example.com", email: "ci@example.com", token: "SARAL_TEST_TOKEN",
		},
		"SARAL_TOKEN replaces the file's token source": {
			file: twoProfiles, env: map[string]string{envToken: "t0k3n"}, name: "work", site: "example.atlassian.net",
			email: "you@example.com", token: envToken,
		},
		"no file at all": {
			env:  map[string]string{envSite: "example.atlassian.net", envEmail: "ci@example.com", envToken: "t0k3n"},
			name: envProfileName, site: "example.atlassian.net", email: "ci@example.com", token: envToken, fromEnv: true,
		},
		"no file and no token still names SARAL_TOKEN": {
			env:  map[string]string{envSite: "example.atlassian.net", envEmail: "ci@example.com"},
			name: envProfileName, site: "example.atlassian.net", email: "ci@example.com", token: envToken, fromEnv: true,
		},
		"no file and no email": {
			env: map[string]string{envSite: "example.atlassian.net"}, fails: "email",
		},
		"a site that is not one": {
			file: twoProfiles, env: map[string]string{envSite: "http://example.atlassian.net"}, fails: envSite,
		},
		"a named profile that is not there is not replaced by the environment": {
			file: twoProfiles, flag: "missing", env: map[string]string{envSite: "example.atlassian.net"}, fails: "missing",
		},
	}
	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			cfgDir, _ := isolated(t)
			for k, v := range tc.env {
				t.Setenv(k, v)
			}
			var cfg config.Config
			if tc.file != "" {
				var err error
				if cfg, err = config.LoadFile(writeConfigFile(t, cfgDir, tc.file)); err != nil {
					t.Fatal(err)
				}
			}
			p, fromEnv, err := resolveProfile(cfg, tc.flag)
			if tc.fails != "" {
				if err == nil || !strings.Contains(err.Error(), tc.fails) {
					t.Fatalf("got %v, want an error naming %q", err, tc.fails)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if p.Name != tc.name || p.Site != tc.site || p.Email != tc.email || p.Token.Env != tc.token || fromEnv != tc.fromEnv {
				t.Errorf("got %s (fromEnv %v), want %s on %s as %s with token from %s (fromEnv %v)",
					p, fromEnv, tc.name, tc.site, tc.email, tc.token, tc.fromEnv)
			}
		})
	}
}

func TestBuild_AProfileFromTheEnvironmentAloneReachesTheSiteAndSavesNothing(t *testing.T) {
	cfgDir, _ := isolated(t)
	t.Setenv(envSite, "example.atlassian.net")
	t.Setenv(envEmail, "ci@example.com")
	t.Setenv(envToken, "t0k3n-value")

	deps, opts, notice, release, err := build(options{})
	if err != nil {
		t.Fatalf("build: %v", err)
	}
	defer release()
	if deps.Jira == nil {
		t.Fatalf("no client from a complete environment (notice %q)", notice)
	}
	if got := initialView(opts); got == "onboarding" {
		t.Error("a complete environment still opened setup")
	}
	if err := deps.SaveQueries(app.SavedQueries{}); !errors.Is(err, errEnvProfile) {
		t.Errorf("saving queries from an environment profile: %v, want errEnvProfile", err)
	}
	if _, err := os.Stat(filepath.Join(cfgDir, "config.toml")); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("an environment-only run wrote a config file: %v", err)
	}
}

func TestBuild_SARAL_TOKENIsNeverWrittenIntoTheFile(t *testing.T) {
	cfgDir, _ := isolated(t)
	path := writeConfigFile(t, cfgDir, twoProfiles)
	t.Setenv(envToken, "t0k3n-value")

	deps, _, _, release, err := build(options{})
	if err != nil {
		t.Fatalf("build: %v", err)
	}
	defer release()
	if deps.Jira == nil {
		t.Fatal("SARAL_TOKEN did not produce a client")
	}
	saved, err := app.NewSavedQueries(app.SavedQuery{Name: "mine", JQL: "assignee = currentUser()", Slot: 1})
	if err != nil {
		t.Fatal(err)
	}
	if err := deps.SaveQueries(saved); err != nil {
		t.Fatalf("SaveQueries: %v", err)
	}
	body, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	switch {
	case !strings.Contains(string(body), "mine"):
		t.Fatalf("the query was not saved, so this proves nothing:\n%s", body)
	case strings.Contains(string(body), envToken), strings.Contains(string(body), "t0k3n-value"):
		t.Errorf("the environment's token reached the file:\n%s", body)
	case !strings.Contains(string(body), "SARAL_TEST_TOKEN"):
		t.Errorf("the file's own token source was replaced:\n%s", body)
	}
}
