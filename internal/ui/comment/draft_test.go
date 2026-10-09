package comment

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	appcomment "github.com/varijkapil13/saral/internal/app/comment"
	"github.com/varijkapil13/saral/internal/ui/kernel"
)

func TestThread_SaysWhenADraftIsNotBeingKept(t *testing.T) {
	t.Parallel()

	f := newFake(3)
	dr := newDriver(t, testDeps(t, f), "PROJ-1", 100, 24)
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "comments"), []byte("not a directory"), 0o600); err != nil {
		t.Fatalf("preparing: %v", err)
	}
	dr.m.drafts = appcomment.OpenDrafts(dir, "")

	dr.key("a")
	dr.typeText("typed into a session that cannot keep it")

	if got := dr.statusText(); !strings.Contains(got, "not being kept on disk") {
		t.Errorf("the status line says %q, and does not say the draft is not being kept", got)
	}
	if got := dr.m.editor.Value(); got != "typed into a session that cannot keep it" {
		t.Errorf("the text was lost as well: %q", got)
	}
}

func TestDrafts_LiveUnderTheDraftsDirectoryTheSessionNames(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	d := openDrafts(kernel.Deps{DraftsDir: root})
	k := appcomment.DraftKey{Site: "example.atlassian.net", Issue: "PROJ-1"}
	if err := d.Write(k, "kept", ""); err != nil {
		t.Fatalf("writing: %v", err)
	}

	want := filepath.Join(root, "comments", "example_atlassian_net", "PROJ-1.new.md")
	if _, err := os.Stat(want); err != nil {
		t.Errorf("the draft is not at %s: %v", want, err)
	}
}
