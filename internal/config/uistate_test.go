package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// ownCache points the lookup at a directory of this test's own. t.Setenv rules
// out t.Parallel, which is why nothing in this file runs in parallel.
func ownCache(t *testing.T) string {
	t.Helper()

	dir := t.TempDir()
	t.Setenv("SARAL_CACHE_DIR", dir)
	return dir
}

func TestSaveSplit_ComesBackAsTheShareItWasGiven(t *testing.T) {
	ownCache(t)

	if _, kept := LoadUIState().Split("issue"); kept {
		t.Fatal("a machine that has never chosen a split remembers one")
	}
	if err := SaveSplit("issue", 366); err != nil {
		t.Fatalf("SaveSplit: %v", err)
	}
	share, kept := LoadUIState().Split("issue")
	if !kept || share != 366 {
		t.Errorf("the split came back as %d, kept=%v, want 366", share, kept)
	}
}

// The file holds one entry per view, so writing one must not lose another. The
// read-merge-write is what makes that true, and it is the reason a second copy
// of Saral choosing a split for a different view does not cost this one its own.
func TestSaveSplit_KeepsEveryOtherViewsShare(t *testing.T) {
	ownCache(t)

	for view, share := range map[string]int{"issue": 300, "board": 480} {
		if err := SaveSplit(view, share); err != nil {
			t.Fatalf("SaveSplit(%q): %v", view, err)
		}
	}
	state := LoadUIState()
	for view, want := range map[string]int{"issue": 300, "board": 480} {
		if got, kept := state.Split(view); !kept || got != want {
			t.Errorf("%s kept %d (kept=%v), want %d", view, got, kept, want)
		}
	}
}

// Going back to the width's own answer is not a share, so it is removed rather
// than written as a number no gesture would produce.
func TestSaveSplit_ZeroForgetsTheChoiceRatherThanRecordingIt(t *testing.T) {
	dir := ownCache(t)

	if err := SaveSplit("issue", 420); err != nil {
		t.Fatalf("SaveSplit: %v", err)
	}
	if err := SaveSplit("issue", 0); err != nil {
		t.Fatalf("SaveSplit(0): %v", err)
	}
	if share, kept := LoadUIState().Split("issue"); kept {
		t.Errorf("the view still remembers a share of %d", share)
	}
	data, err := os.ReadFile(filepath.Join(dir, uiStateFile))
	if err != nil {
		t.Fatalf("reading the state file: %v", err)
	}
	if strings.Contains(string(data), "420") {
		t.Errorf("the forgotten share is still in the file:\n%s", data)
	}
}

// The file is the program's own record of how a pane was left, so anything it
// cannot read is worth ignoring rather than refusing to open an issue over.
func TestLoadUIState_RemembersNothingRatherThanFailing(t *testing.T) {
	for _, tc := range []struct {
		name string
		file string
	}{
		{name: "a file TOML cannot parse", file: "splits = [[[["},
		{name: "a share nothing would produce", file: "[splits]\nissue = 4000\n"},
		{name: "a share of zero", file: "[splits]\nissue = 0\n"},
		{name: "a negative share", file: "[splits]\nissue = -3\n"},
		{name: "a section with nothing in it", file: "[splits]\n"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dir := ownCache(t)
			if err := os.WriteFile(filepath.Join(dir, uiStateFile), []byte(tc.file), 0o600); err != nil {
				t.Fatalf("seeding: %v", err)
			}
			if share, kept := LoadUIState().Split("issue"); kept {
				t.Errorf("%s was read as a choice of %d", tc.name, share)
			}
		})
	}
}

func TestSaveSplit_ReportsWhyItCouldNotWrite(t *testing.T) {
	dir := ownCache(t)

	blocker := filepath.Join(dir, uiStateFile)
	if err := os.MkdirAll(blocker, 0o700); err != nil {
		t.Fatalf("seeding: %v", err)
	}
	err := SaveSplit("issue", 400)
	if err == nil {
		t.Fatal("SaveSplit succeeded with a directory where the file should be")
	}
	if !strings.Contains(err.Error(), blocker) {
		t.Errorf("error %q does not say which path it failed on", err)
	}
}

// The split is not a field of Profile, which is what keeps it out of the way of
// the thing that drops profile fields: onboarding builds the profile it writes
// from a zero value and the four fields on screen, so Theme, Glyphs, Timeline
// and the saved queries all go when somebody re-runs setup to re-check a token.
// This walks that same overwrite and asserts the split is still there, because
// it was never in the file being rewritten.
func TestSaveSplit_SurvivesAProfileBeingRebuiltFromAZeroValue(t *testing.T) {
	dir := ownCache(t)
	t.Setenv("SARAL_CONFIG_DIR", dir)

	if err := SaveSplit("issue", 358); err != nil {
		t.Fatalf("SaveSplit: %v", err)
	}
	path := filepath.Join(dir, fileName)
	before := Config{Active: "work", Mouse: true, Profiles: map[string]Profile{
		"work": {
			Name: "work", Site: "example.atlassian.net", Email: "you@example.com",
			Project: "PROJ", Token: TokenSource{Env: "JIRA_TOKEN"},
			Theme: "dark", Glyphs: "ascii",
		},
	}}
	if err := before.Save(path); err != nil {
		t.Fatalf("Save: %v", err)
	}

	// What onboarding does to a profile that already exists.
	after, err := LoadFile(path)
	if err != nil {
		t.Fatalf("LoadFile: %v", err)
	}
	after.Profiles["work"] = Profile{
		Name: "work", Site: "example.atlassian.net", Email: "you@example.com",
		Project: "PROJ", Token: TokenSource{Env: "JIRA_TOKEN"},
	}
	if err := after.Save(path); err != nil {
		t.Fatalf("Save: %v", err)
	}

	reloaded, err := LoadFile(path)
	if err != nil {
		t.Fatalf("LoadFile: %v", err)
	}
	if theme := reloaded.Profiles["work"].Theme; theme != "" {
		t.Fatalf("the rewrite kept the theme %q, so it is not the overwrite this is about", theme)
	}
	share, kept := LoadUIState().Split("issue")
	if !kept || share != 358 {
		t.Errorf("the split came back as %d (kept=%v) after a profile was rebuilt, want 358", share, kept)
	}
}

// And it is not in config.toml at all, which is the other half of the same
// decision: that file has to stay safe to hand somebody and readable by hand.
func TestSave_WritesNoSplitIntoTheConfigFile(t *testing.T) {
	dir := ownCache(t)

	if err := SaveSplit("issue", 358); err != nil {
		t.Fatalf("SaveSplit: %v", err)
	}
	path := filepath.Join(dir, fileName)
	cfg := Config{Active: "work", Mouse: true, Profiles: map[string]Profile{
		"work": {Site: "example.atlassian.net", Email: "you@example.com", Token: TokenSource{Env: "T"}},
	}}
	if err := cfg.Save(path); err != nil {
		t.Fatalf("Save: %v", err)
	}
	data, err := os.ReadFile(path) //nolint:gosec // the path is this test's own temporary directory
	if err != nil {
		t.Fatalf("reading the config: %v", err)
	}
	for _, unwanted := range []string{"split", "358"} {
		if strings.Contains(string(data), unwanted) {
			t.Errorf("config.toml carries %q:\n%s", unwanted, data)
		}
	}
}
