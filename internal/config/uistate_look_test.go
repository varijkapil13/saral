package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestSaveLook_ComesBackAsTheLookItWasGivenAndKeepsTheRest(t *testing.T) {
	dir := ownCache(t)

	if got := LoadUIState().Look(); got != "" {
		t.Fatalf("a machine that has never chosen a look remembers %q", got)
	}
	if err := SaveSort("issues", SortSpec{Field: "key"}); err != nil {
		t.Fatalf("SaveSort: %v", err)
	}
	if err := SaveLook("compact"); err != nil {
		t.Fatalf("SaveLook: %v", err)
	}
	state := LoadUIState()
	if got := state.Look(); got != "compact" {
		t.Errorf("the look came back as %q, want compact", got)
	}
	if _, kept := state.Sort("issues"); !kept {
		t.Error("saving the look lost the sort beside it")
	}
	data, err := os.ReadFile(filepath.Join(dir, uiStateFile)) //nolint:gosec // a file under the test's own directory
	if err != nil {
		t.Fatalf("reading the state file: %v", err)
	}
	var table bool
	for _, line := range strings.Split(string(data), "\n") {
		switch strings.TrimSpace(line) {
		case "[look]":
			table = true
		case `look = "compact"`:
			if !table {
				t.Errorf("the look is written outside the [look] table:\n%s", data)
			}
			return
		}
	}
	t.Errorf("the file does not keep the look under [look]:\n%s", data)
}

func TestSaveLook_BlankForgetsTheChoiceRatherThanRecordingIt(t *testing.T) {
	dir := ownCache(t)

	if err := SaveLook("lines"); err != nil {
		t.Fatalf("SaveLook: %v", err)
	}
	if err := SaveLook("  "); err != nil {
		t.Fatalf("SaveLook(blank): %v", err)
	}
	if got := LoadUIState().Look(); got != "" {
		t.Errorf("the look is still %q", got)
	}
	data, err := os.ReadFile(filepath.Join(dir, uiStateFile)) //nolint:gosec // a file under the test's own directory
	if err != nil {
		t.Fatalf("reading the state file: %v", err)
	}
	if strings.Contains(string(data), "look") {
		t.Errorf("the forgotten look is still in the file:\n%s", data)
	}
}

func TestSaveLook_KeepsAWordItDoesNotKnowForTheReaderToJudge(t *testing.T) {
	ownCache(t)

	if err := SaveLook("sparkly"); err != nil {
		t.Fatalf("SaveLook: %v", err)
	}
	if got := LoadUIState().Look(); got != "sparkly" {
		t.Errorf("the look came back as %q, want the word as written", got)
	}
}

func TestLoadUIState_ReadsAHandWrittenLook(t *testing.T) {
	dir := ownCache(t)

	for body, want := range map[string]string{
		"[look]\nlook = \" roomy \"\n": "roomy",
		"[look]\n":                     "",
		"":                             "",
		"[look]\nlook = 3\n":           "",
	} {
		if err := os.WriteFile(filepath.Join(dir, uiStateFile), []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
		if got := LoadUIState().Look(); got != want {
			t.Errorf("%q read as %q, want %q", body, got, want)
		}
	}
}
