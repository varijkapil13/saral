package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestProfileProject_RoundTripsAndIsOmittedWhenUnset(t *testing.T) {
	t.Parallel()

	tests := map[string]struct {
		project string
		want    string
	}{
		"a scoped profile writes the key":   {project: "PROJ", want: "project = \"PROJ\"\n"},
		"an unscoped profile writes no key": {project: "", want: ""},
	}

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			cfg := Config{
				Active: "work",
				Mouse:  true,
				Profiles: map[string]Profile{"work": {
					Name:    "work",
					Site:    "example.atlassian.net",
					Email:   "you@example.com",
					Project: tc.project,
					Token:   TokenSource{Keychain: "saral:work"},
				}},
			}

			path := filepath.Join(t.TempDir(), "config.toml")
			if err := cfg.Save(path); err != nil {
				t.Fatalf("Save: %v", err)
			}
			written, err := os.ReadFile(path)
			if err != nil {
				t.Fatalf("reading back: %v", err)
			}
			if tc.want == "" {
				if strings.Contains(string(written), "project") {
					t.Errorf("an unscoped profile still wrote a project key:\n%s", written)
				}
			} else if !strings.Contains(string(written), tc.want) {
				t.Errorf("wrote\n%s\nwant a line %q", written, strings.TrimSpace(tc.want))
			}

			got, err := LoadFile(path)
			if err != nil {
				t.Fatalf("LoadFile: %v", err)
			}
			if got.Profiles["work"].Project != tc.project {
				t.Errorf("project round-tripped as %q, want %q", got.Profiles["work"].Project, tc.project)
			}
		})
	}
}

func TestValidate_RefusesAProjectThatIsNotAKey(t *testing.T) {
	t.Parallel()

	p := Profile{
		Name:    "work",
		Site:    "example.atlassian.net",
		Email:   "you@example.com",
		Project: "two words",
		Token:   TokenSource{Env: "JIRA_TOKEN"},
	}
	err := p.Validate()
	if err == nil {
		t.Fatal("a project key with a space was accepted")
	}
	if !strings.Contains(err.Error(), "two words") {
		t.Errorf("error %q does not quote the offending value", err)
	}
}

func TestValidate_AcceptsEveryKnownSchemeAndRefusesAnythingElse(t *testing.T) {
	t.Parallel()

	base := Profile{
		Name: "work", Site: "example.atlassian.net", Email: "you@example.com",
		Token: TokenSource{Env: "JIRA_TOKEN"},
	}
	for _, scheme := range []string{"", "default", "nord", "dracula", "solarized", "gruvbox"} {
		p := base
		p.Scheme = scheme
		if err := p.Validate(); err != nil {
			t.Errorf("scheme %q was refused: %v", scheme, err)
		}
	}

	p := base
	p.Scheme = "monokai"
	err := p.Validate()
	if err == nil {
		t.Fatal("an unknown colour scheme was accepted")
	}
	if !strings.Contains(err.Error(), "monokai") {
		t.Errorf("error %q does not quote the offending value", err)
	}
}
