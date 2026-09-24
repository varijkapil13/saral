package settings

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/gofrs/flock"

	"github.com/varijkapil13/saral/internal/config"
)

func twoProfiles() *config.Config {
	return &config.Config{
		Active: "work",
		Profiles: map[string]config.Profile{
			"work": {Site: "example.atlassian.net", Email: "me@example.com", Token: config.TokenSource{Env: "JIRA_TOKEN"}, Pinned: []string{"customfield_1"}},
			"home": {Site: "home.atlassian.net", Email: "me@home.com", Token: config.TokenSource{Env: "JIRA_TOKEN_HOME"}},
		},
	}
}

func TestWriters_WaitForTheFileLockRatherThanWritingPastIt(t *testing.T) {
	dir := useConfig(t, twoProfiles())
	path := filepath.Join(dir, "config.toml")
	before, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	other := flock.New(filepath.Join(dir, ".config.toml.lock"))
	if err := other.Lock(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = other.Unlock() })

	for name, write := range map[string]func() error{
		"pinned": func() error { return writePinned("example.atlassian.net", []string{"customfield_2"}) },
		"active": func() error { return writeActiveProfile("home") },
	} {
		if err := write(); err == nil || !strings.Contains(err.Error(), "another copy") {
			t.Errorf("%s wrote while another copy held the lock: %v", name, err)
		}
	}
	after, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(after, before) {
		t.Errorf("the file changed while it was locked:\n%s", after)
	}
}

func TestWriters_AChangeThatChangesNothingLeavesTheFileAlone(t *testing.T) {
	dir := useConfig(t, twoProfiles())
	path := filepath.Join(dir, "config.toml")
	body, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	handWritten := append([]byte("# kept by hand\n"), body...)
	if err := os.WriteFile(path, handWritten, 0o600); err != nil {
		t.Fatal(err)
	}

	if err := writePinned("example.atlassian.net", []string{"customfield_1"}); err != nil {
		t.Fatalf("pinning what is already pinned: %v", err)
	}
	if err := writeActiveProfile("work"); err != nil {
		t.Fatalf("activating the active profile: %v", err)
	}
	after, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(after, handWritten) {
		t.Errorf("a no-op rewrote the file:\n%s", after)
	}
}

func TestWriters_KeepTheOtherProfiles(t *testing.T) {
	useConfig(t, twoProfiles())
	if err := writePinned("example.atlassian.net", []string{"customfield_2"}); err != nil {
		t.Fatal(err)
	}
	if err := writeActiveProfile("home"); err != nil {
		t.Fatal(err)
	}
	path, err := config.Path()
	if err != nil {
		t.Fatal(err)
	}
	cfg, err := config.LoadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Active != "home" || len(cfg.Profiles) != 2 {
		t.Errorf("active %q with %d profiles, want home with both", cfg.Active, len(cfg.Profiles))
	}
	if got := cfg.Profiles["work"].Pinned; len(got) != 1 || got[0] != "customfield_2" {
		t.Errorf("work pins %v, want [customfield_2]", got)
	}
}
