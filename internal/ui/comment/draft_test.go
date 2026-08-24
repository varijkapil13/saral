package comment

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestDrafts_KeepsAndReturnsWhatWasTyped(t *testing.T) {
	t.Parallel()

	d := &drafts{root: t.TempDir()}
	k := draftKey{site: "example.atlassian.net", issue: "PROJ-1"}

	if got := d.read(k); got != "" {
		t.Errorf("a draft nobody wrote came back as %q", got)
	}
	if err := d.write(k, "half a thought"); err != nil {
		t.Fatalf("writing: %v", err)
	}
	if got := d.read(k); got != "half a thought" {
		t.Errorf("the draft came back as %q", got)
	}
	if err := d.write(k, "a whole one"); err != nil {
		t.Fatalf("rewriting: %v", err)
	}
	if got := d.read(k); got != "a whole one" {
		t.Errorf("the rewritten draft came back as %q", got)
	}
	d.discard(k)
	if got := d.read(k); got != "" {
		t.Errorf("a discarded draft came back as %q", got)
	}
}

func TestDrafts_KeepsANewCommentApartFromAnEditOfAnExistingOne(t *testing.T) {
	t.Parallel()

	d := &drafts{root: t.TempDir()}
	fresh := draftKey{site: "example.atlassian.net", issue: "PROJ-1"}
	editing := draftKey{site: "example.atlassian.net", issue: "PROJ-1", comment: "10701"}

	if err := d.write(fresh, "the new one"); err != nil {
		t.Fatalf("writing: %v", err)
	}
	if err := d.write(editing, "the edit"); err != nil {
		t.Fatalf("writing: %v", err)
	}
	d.discard(editing)

	if got := d.read(fresh); got != "the new one" {
		t.Errorf("abandoning an edit took the new comment with it: %q", got)
	}
}

func TestDrafts_TwoSitesWithOneIssueKeyDoNotShareADraft(t *testing.T) {
	t.Parallel()

	d := &drafts{root: t.TempDir()}
	here := draftKey{site: "one.atlassian.net", issue: "PROJ-1"}
	there := draftKey{site: "two.atlassian.net", issue: "PROJ-1"}

	if err := d.write(here, "about this site's PROJ-1"); err != nil {
		t.Fatalf("writing: %v", err)
	}
	if got := d.read(there); got != "" {
		t.Errorf("the other site's PROJ-1 read %q", got)
	}
}

func TestDrafts_APathCannotReachOutsideTheDraftsDirectory(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	d := &drafts{root: root}
	k := draftKey{site: "../../etc", issue: "../../../passwd", comment: "/../.."}

	if err := d.write(k, "not going anywhere"); err != nil {
		t.Fatalf("writing: %v", err)
	}
	path := d.path(k)
	if !strings.HasPrefix(filepath.Clean(path), filepath.Clean(root)+string(filepath.Separator)) {
		t.Fatalf("the draft went to %q, which is outside %q", path, root)
	}
	if got := d.read(k); got != "not going anywhere" {
		t.Errorf("the draft came back as %q", got)
	}
}

func TestDrafts_AreReadableOnlyByTheAccountThatWroteThem(t *testing.T) {
	t.Parallel()

	d := &drafts{root: t.TempDir()}
	k := draftKey{site: "example.atlassian.net", issue: "PROJ-1"}
	if err := d.write(k, "private until it is sent"); err != nil {
		t.Fatalf("writing: %v", err)
	}

	info, err := os.Stat(d.path(k))
	if err != nil {
		t.Fatalf("stat: %v", err)
	}
	if perm := info.Mode().Perm(); perm != 0o600 {
		t.Errorf("the draft is mode %o, want 600: it is somebody's unpublished words", perm)
	}
}

func TestDrafts_WithNowhereToWriteKeepNothingRatherThanFailing(t *testing.T) {
	t.Parallel()

	d := &drafts{}
	k := draftKey{site: "example.atlassian.net", issue: "PROJ-1"}

	if err := d.write(k, "nowhere to put this"); err != nil {
		t.Errorf("a store with no directory reported %v, want silence", err)
	}
	if got := d.read(k); got != "" {
		t.Errorf("a store with no directory returned %q", got)
	}
	d.discard(k)
}

func TestDrafts_ReportsAWriteItCannotMake(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	// A file where the directory has to go: the store cannot create the tree,
	// and has to say so rather than pretend the words are kept.
	blocked := filepath.Join(root, "drafts")
	if err := os.WriteFile(blocked, []byte("not a directory"), 0o600); err != nil {
		t.Fatalf("preparing: %v", err)
	}
	d := &drafts{root: blocked}

	if err := d.write(draftKey{site: "example.atlassian.net", issue: "PROJ-1"}, "text"); err == nil {
		t.Error("writing into a file reported success")
	}
}

func TestThread_SaysWhenADraftIsNotBeingKept(t *testing.T) {
	t.Parallel()

	f := newFake(3)
	dr := newDriver(t, testDeps(t, f), "PROJ-1", 100, 24)
	blocked := filepath.Join(t.TempDir(), "drafts")
	if err := os.WriteFile(blocked, []byte("not a directory"), 0o600); err != nil {
		t.Fatalf("preparing: %v", err)
	}
	dr.m.drafts = &drafts{root: blocked}

	dr.key("a")
	dr.typeText("typed into a session that cannot keep it")

	if got := dr.statusText(); !strings.Contains(got, "not being kept on disk") {
		t.Errorf("the status line says %q, and does not say the draft is not being kept", got)
	}
	if got := dr.m.editor.Value(); got != "typed into a session that cannot keep it" {
		t.Errorf("the text was lost as well: %q", got)
	}
}

func TestSafeName_KeepsOnlyWhatEveryFilesystemMeansTheSameBy(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct{ in, want string }{
		{in: "PROJ-1", want: "PROJ-1"},
		{in: "example.atlassian.net", want: "example_atlassian_net"},
		{in: "../..", want: "_____"},
		{in: "", want: "unnamed"},
		{in: "a/b", want: "a_b"},
	} {
		if got := safeName(tc.in); got != tc.want {
			t.Errorf("safeName(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}
