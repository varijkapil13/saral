package kernel

import (
	"path/filepath"
	"testing"

	"github.com/varijkapil13/saral/internal/config"
)

func TestDraftRoot_IsTheSessionsDirectoryWhenOneIsNamed(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	got, err := Deps{DraftsDir: dir}.DraftRoot()
	if err != nil || got != dir {
		t.Errorf("DraftRoot() = %q, %v, want %q", got, err, dir)
	}
}

func TestDraftRoot_FallsBackToBesideTheProfile(t *testing.T) {
	t.Parallel()

	profile, err := config.Dir()
	if err != nil {
		t.Skipf("no config directory on this machine: %v", err)
	}
	got, err := Deps{}.DraftRoot()
	if want := filepath.Join(profile, "drafts"); err != nil || got != want {
		t.Errorf("DraftRoot() = %q, %v, want %q", got, err, want)
	}
}
