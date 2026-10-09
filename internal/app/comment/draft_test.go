package comment

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	appdraft "github.com/varijkapil13/saral/internal/app/draft"
	"github.com/varijkapil13/saral/pkg/adf"
)

func TestDrafts_KeepsAndReturnsWhatWasTyped(t *testing.T) {
	t.Parallel()

	d := &Drafts{store: appdraft.Open(t.TempDir())}
	k := DraftKey{Site: "example.atlassian.net", Issue: "PROJ-1"}

	if got := d.Read(k); got != "" {
		t.Errorf("a draft nobody wrote came back as %q", got)
	}
	if err := d.Write(k, "half a thought", ""); err != nil {
		t.Fatalf("writing: %v", err)
	}
	if got := d.Read(k); got != "half a thought" {
		t.Errorf("the draft came back as %q", got)
	}
	if err := d.Write(k, "a whole one", ""); err != nil {
		t.Fatalf("rewriting: %v", err)
	}
	if got := d.Read(k); got != "a whole one" {
		t.Errorf("the rewritten draft came back as %q", got)
	}
	d.Discard(k)
	if got := d.Read(k); got != "" {
		t.Errorf("a discarded draft came back as %q", got)
	}
}

func TestDrafts_KeepsANewCommentApartFromAnEditOfAnExistingOne(t *testing.T) {
	t.Parallel()

	d := &Drafts{store: appdraft.Open(t.TempDir())}
	fresh := DraftKey{Site: "example.atlassian.net", Issue: "PROJ-1"}
	editing := DraftKey{Site: "example.atlassian.net", Issue: "PROJ-1", Comment: "10701"}

	if err := d.Write(fresh, "the new one", ""); err != nil {
		t.Fatalf("writing: %v", err)
	}
	if err := d.Write(editing, "the edit", ""); err != nil {
		t.Fatalf("writing: %v", err)
	}
	d.Discard(editing)

	if got := d.Read(fresh); got != "the new one" {
		t.Errorf("abandoning an edit took the new comment with it: %q", got)
	}
}

func TestDrafts_TwoSitesWithOneIssueKeyDoNotShareADraft(t *testing.T) {
	t.Parallel()

	d := &Drafts{store: appdraft.Open(t.TempDir())}
	here := DraftKey{Site: "one.atlassian.net", Issue: "PROJ-1"}
	there := DraftKey{Site: "two.atlassian.net", Issue: "PROJ-1"}

	if err := d.Write(here, "about this site's PROJ-1", ""); err != nil {
		t.Fatalf("writing: %v", err)
	}
	if got := d.Read(there); got != "" {
		t.Errorf("the other site's PROJ-1 read %q", got)
	}
}

func TestDrafts_APathCannotReachOutsideTheDraftsDirectory(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	d := &Drafts{store: appdraft.Open(root)}
	k := DraftKey{Site: "../../etc", Issue: "../../../passwd", Comment: "/../.."}

	if err := d.Write(k, "not going anywhere", ""); err != nil {
		t.Fatalf("writing: %v", err)
	}
	path := d.path(k)
	if !strings.HasPrefix(filepath.Clean(path), filepath.Clean(root)+string(filepath.Separator)) {
		t.Fatalf("the draft went to %q, which is outside %q", path, root)
	}
	if got := d.Read(k); got != "not going anywhere" {
		t.Errorf("the draft came back as %q", got)
	}
}

func TestDrafts_AreReadableOnlyByTheAccountThatWroteThem(t *testing.T) {
	t.Parallel()

	d := &Drafts{store: appdraft.Open(t.TempDir())}
	k := DraftKey{Site: "example.atlassian.net", Issue: "PROJ-1"}
	if err := d.Write(k, "private until it is sent", ""); err != nil {
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

	d := &Drafts{}
	k := DraftKey{Site: "example.atlassian.net", Issue: "PROJ-1"}

	if err := d.Write(k, "nowhere to put this", ""); err != nil {
		t.Errorf("a store with no directory reported %v, want silence", err)
	}
	if got := d.Read(k); got != "" {
		t.Errorf("a store with no directory returned %q", got)
	}
	d.Discard(k)
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
	d := &Drafts{store: appdraft.Open(blocked)}

	if err := d.Write(DraftKey{Site: "example.atlassian.net", Issue: "PROJ-1"}, "text", ""); err == nil {
		t.Error("writing into a file reported success")
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

func TestDrafts_LiveUnderTheDraftsDirectoryTheSessionNames(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	d := OpenDrafts(root, "")
	k := DraftKey{Site: "example.atlassian.net", Issue: "PROJ-1"}
	if err := d.Write(k, "kept", ""); err != nil {
		t.Fatalf("writing: %v", err)
	}

	want := filepath.Join(root, "comments", "example_atlassian_net", "PROJ-1.new.md")
	if _, err := os.Stat(want); err != nil {
		t.Errorf("the draft is not at %s: %v", want, err)
	}
}

func TestDrafts_KeepForgetsADraftLeftBlank(t *testing.T) {
	t.Parallel()

	d := &Drafts{store: appdraft.Open(t.TempDir())}
	k := DraftKey{Site: "example.atlassian.net", Issue: "PROJ-1"}
	if err := d.Keep(k, "words", ""); err != nil {
		t.Fatal(err)
	}
	if err := d.Keep(k, "  \n", ""); err != nil {
		t.Fatal(err)
	}
	if got := d.Read(k); got != "" {
		t.Errorf("a blanked draft is still kept as %q", got)
	}
}

func TestDrafts_OpenSeedsFromTheDraftOrTheSite(t *testing.T) {
	t.Parallel()

	body := adf.NewDoc(adf.NewNode("paragraph", adf.NewText("on the site")))
	edit := DraftKey{Site: "example.atlassian.net", Issue: "PROJ-1", Comment: "10701"}
	fresh := DraftKey{Site: "example.atlassian.net", Issue: "PROJ-1"}

	d := &Drafts{store: appdraft.Open(t.TempDir())}
	if got := d.Open(fresh, adf.Doc{}); got != (Opening{Text: Markdown(adf.Doc{})}) {
		t.Errorf("a new comment with no draft opened as %+v", got)
	}
	if got := d.Open(edit, body); got != (Opening{Text: "on the site", Base: Fingerprint(body)}) {
		t.Errorf("an edit with no draft opened as %+v", got)
	}

	if err := d.Write(edit, "mine", Fingerprint(body)); err != nil {
		t.Fatal(err)
	}
	if got := d.Open(edit, body); got != (Opening{Text: "mine", Base: Fingerprint(body), Restored: true}) {
		t.Errorf("a draft over the same body opened as %+v", got)
	}

	moved := adf.NewDoc(adf.NewNode("paragraph", adf.NewText("changed since")))
	if got := d.Open(edit, moved); !got.Stale || got.Base != Fingerprint(body) || got.Text != "mine" {
		t.Errorf("a draft over a body the site has moved opened as %+v", got)
	}

	if err := d.Write(fresh, "a new one", ""); err != nil {
		t.Fatal(err)
	}
	if got := d.Open(fresh, adf.Doc{}); got.Stale || !got.Restored || got.Base != "" {
		t.Errorf("a new comment's draft opened as %+v", got)
	}
}
