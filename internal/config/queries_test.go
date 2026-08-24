package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/varijkapil13/saral/internal/app"
)

func profileWith(queries ...app.SavedQuery) Config {
	return Config{
		Active: "work",
		Mouse:  true,
		Profiles: map[string]Profile{"work": {
			Name:    "work",
			Site:    "example.atlassian.net",
			Email:   "you@example.com",
			Token:   TokenSource{Keychain: "saral:work"},
			Queries: queries,
		}},
	}
}

func saveAndLoad(t *testing.T, cfg Config) (string, Config) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "config.toml")
	if err := cfg.Save(path); err != nil {
		t.Fatalf("Save: %v", err)
	}
	written, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("reading back: %v", err)
	}
	got, err := LoadFile(path)
	if err != nil {
		t.Fatalf("LoadFile: %v\n%s", err, written)
	}
	return string(written), got
}

func TestSavedQueries_RoundTripThroughTheFile(t *testing.T) {
	t.Parallel()

	queries := []app.SavedQuery{
		{Name: "Blockers", JQL: "priority = Highest AND resolution = EMPTY ORDER BY updated DESC", Slot: 2},
		{Name: "Anything I reported", JQL: "reporter = currentUser() ORDER BY created DESC"},
	}
	written, got := saveAndLoad(t, profileWith(queries...))

	if !strings.Contains(written, "[[profiles.work.queries]]") || !strings.Contains(written, "key  = 2") {
		t.Errorf("the file does not read as a list of queries:\n%s", written)
	}
	back := got.Profiles["work"].Queries
	if len(back) != len(queries) {
		t.Fatalf("read back %d queries, want %d:\n%s", len(back), len(queries), written)
	}
	for i, want := range queries {
		if got := back[i]; got.Name != want.Name || got.JQL != want.JQL || got.Slot != want.Slot {
			t.Errorf("query %d round-tripped as %+v, want %+v", i, got, want)
		}
	}
	if strings.Contains(written, "key  = 0") {
		t.Errorf("a query bound to no key wrote one anyway:\n%s", written)
	}
}

func TestSavedQueries_AProfileWithNoneWritesNoSection(t *testing.T) {
	t.Parallel()

	written, got := saveAndLoad(t, profileWith())
	if strings.Contains(written, "queries") {
		t.Errorf("a profile with no saved queries wrote a section for them:\n%s", written)
	}
	if len(got.Profiles["work"].Queries) != 0 {
		t.Errorf("read back %+v, want nothing", got.Profiles["work"].Queries)
	}
}

func TestSavedQueries_TheFileIsHeldToTheSameRulesAsTheKeyboard(t *testing.T) {
	t.Parallel()

	const header = "active = \"work\"\n\n[profiles.work]\nsite  = \"example.atlassian.net\"\n" +
		"email = \"you@example.com\"\ntoken = { keychain = \"saral:work\" }\n"

	tests := map[string]struct {
		queries string
		want    string
	}{
		"a key that is not on the keyboard row": {
			queries: "\n[[profiles.work.queries]]\nname = \"Blockers\"\njql = \"priority = Highest\"\nkey = 12\n",
			want:    "the keys are 1 to 9",
		},
		"a query with nothing to run": {
			queries: "\n[[profiles.work.queries]]\nname = \"Blockers\"\njql = \"\"\nkey = 2\n",
			want:    "has no JQL to run",
		},
		"a query with no name to reach it by": {
			queries: "\n[[profiles.work.queries]]\njql = \"priority = Highest\"\nkey = 2\n",
			want:    "needs a name",
		},
		"two queries on one key": {
			queries: "\n[[profiles.work.queries]]\nname = \"Blockers\"\njql = \"priority = Highest\"\nkey = 2\n" +
				"\n[[profiles.work.queries]]\nname = \"Mine\"\njql = \"assignee = currentUser()\"\nkey = 2\n",
			want: "both ask for key 2",
		},
		"two queries under one name": {
			queries: "\n[[profiles.work.queries]]\nname = \"Blockers\"\njql = \"priority = Highest\"\n" +
				"\n[[profiles.work.queries]]\nname = \"blockers\"\njql = \"assignee = currentUser()\"\n",
			want: "two saved queries are called",
		},
		"a key nobody meant to write": {
			queries: "\n[[profiles.work.queries]]\nname = \"Blockers\"\njql = \"priority = Highest\"\nslot = 2\n",
			want:    "unknown key",
		},
	}

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			path := filepath.Join(t.TempDir(), "config.toml")
			if err := os.WriteFile(path, []byte(header+tc.queries), 0o600); err != nil {
				t.Fatal(err)
			}
			_, err := LoadFile(path)
			if err == nil {
				t.Fatalf("the file was accepted:\n%s", header+tc.queries)
			}
			if !strings.Contains(err.Error(), tc.want) {
				t.Errorf("error %q does not say %q", err, tc.want)
			}
		})
	}
}

func TestSavedQueries_ARefusedSetIsNeverWritten(t *testing.T) {
	t.Parallel()

	cfg := profileWith(app.SavedQuery{Name: "Blockers", JQL: "priority = Highest", Slot: app.MaxSavedSlot + 1})
	path := filepath.Join(t.TempDir(), "config.toml")
	if err := cfg.Save(path); err == nil {
		t.Fatal("a query on a key that does not exist was written to the file")
	}
	if _, err := os.Stat(path); err == nil {
		t.Error("the file was replaced although the config was refused")
	}
}
