package draft

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"testing"
)

func TestStore_WritesReadsAndRemoves(t *testing.T) {
	t.Parallel()

	s := Open(t.TempDir())
	path := s.Path("example_atlassian_net", "PROJ-1.json")

	if body, ok, err := s.Read(path); ok || err != nil || body != nil {
		t.Errorf("a draft nobody wrote read as %q, %v, %v", body, ok, err)
	}
	if err := s.Write(path, []byte("half a thought")); err != nil {
		t.Fatalf("writing: %v", err)
	}
	if err := s.Write(path, []byte("a whole one")); err != nil {
		t.Fatalf("rewriting: %v", err)
	}
	if body, ok, err := s.Read(path); !ok || err != nil || string(body) != "a whole one" {
		t.Errorf("the rewritten draft read as %q, %v, %v", body, ok, err)
	}
	if err := s.Remove(path); err != nil {
		t.Fatalf("removing: %v", err)
	}
	if err := s.Remove(path); err != nil {
		t.Errorf("removing a draft already gone reported %v", err)
	}
	if _, ok, _ := s.Read(path); ok {
		t.Error("a removed draft is still there")
	}
}

func TestStore_LeavesNoTemporaryFileBehind(t *testing.T) {
	t.Parallel()

	s := Open(t.TempDir())
	path := s.Path("site", "one.json")
	if err := s.Write(path, []byte("kept")); err != nil {
		t.Fatal(err)
	}
	entries, err := os.ReadDir(filepath.Dir(path))
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 || entries[0].Name() != "one.json" {
		t.Errorf("the directory holds %v, want only the draft", entries)
	}
}

func TestStore_DraftsAreReadableOnlyByTheAccountThatWroteThem(t *testing.T) {
	t.Parallel()

	s := Open(t.TempDir())
	path := s.Path("site", "one.md")
	if err := s.Write(path, []byte("private until it is sent")); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if perm := info.Mode().Perm(); perm != 0o600 {
		t.Errorf("the draft is mode %o, want 600", perm)
	}
	dir, err := os.Stat(filepath.Dir(path))
	if err != nil {
		t.Fatal(err)
	}
	if perm := dir.Mode().Perm(); perm != 0o700 {
		t.Errorf("the draft's directory is mode %o, want 700", perm)
	}
}

func TestStore_WithNowhereToWriteKeepsNothingRatherThanFailing(t *testing.T) {
	t.Parallel()

	var s Store
	if s.Available() {
		t.Error("the zero store says it keeps things")
	}
	path := s.Path("site", "one.md")
	if path != "" {
		t.Errorf("the zero store named a path %q", path)
	}
	if err := s.Write(path, []byte("nowhere")); err != nil {
		t.Errorf("writing reported %v, want silence", err)
	}
	if _, ok, err := s.Read(path); ok || err != nil {
		t.Errorf("reading gave %v, %v", ok, err)
	}
	if err := s.Remove(path); err != nil {
		t.Errorf("removing reported %v", err)
	}
}

func TestStore_ReportsWhatItCannotDo(t *testing.T) {
	t.Parallel()

	blocked := filepath.Join(t.TempDir(), "drafts")
	if err := os.WriteFile(blocked, []byte("not a directory"), 0o600); err != nil {
		t.Fatal(err)
	}
	s := Open(blocked)
	if err := s.Write(s.Path("site", "one.md"), []byte("text")); err == nil {
		t.Error("writing under a file reported success")
	}
	if _, _, err := s.Read(s.Path("site", "one.md")); err == nil || errors.Is(err, fs.ErrNotExist) {
		t.Errorf("reading under a file reported %v, want an error that is not a missing draft", err)
	}
}

func TestSafeName_KeepsOnlyWhatEveryFilesystemMeansTheSameBy(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct{ in, also, want string }{
		{in: "PROJ-1", want: "PROJ-1"},
		{in: "example.atlassian.net", want: "example_atlassian_net"},
		{in: "example.atlassian.net", also: ".", want: "example.atlassian.net"},
		{in: "../..", want: "_____"},
		{in: "a/b", also: ".", want: "a_b"},
		{in: "Déjà", want: "D_j_"},
		{in: "", want: "unnamed"},
	} {
		if got := SafeName(tc.in, tc.also); got != tc.want {
			t.Errorf("SafeName(%q, %q) = %q, want %q", tc.in, tc.also, got, tc.want)
		}
	}
}

func TestMigrate_MovesEveryDraftOutOfTheOldDirectory(t *testing.T) {
	t.Parallel()

	from := filepath.Join(t.TempDir(), "drafts")
	to := filepath.Join(t.TempDir(), "comments")
	writeFile(t, filepath.Join(from, "one_atlassian_net", "PROJ-1.new.md"), "first site")
	writeFile(t, filepath.Join(from, "two_atlassian_net", "PROJ-2.10701.md"), "an edit")

	Migrate(from, to, ".md")

	for path, want := range map[string]string{
		filepath.Join(to, "one_atlassian_net", "PROJ-1.new.md"):   "first site",
		filepath.Join(to, "two_atlassian_net", "PROJ-2.10701.md"): "an edit",
	} {
		if body, err := os.ReadFile(path); err != nil || string(body) != want {
			t.Errorf("%s read %q, %v after moving, want %q", path, body, err, want)
		}
	}
	if _, err := os.Stat(from); !os.IsNotExist(err) {
		t.Errorf("the old drafts directory is still there: %v", err)
	}
}

func TestMigrate_KeepsTheNewerDraftAndLeavesTheOldOneInPlace(t *testing.T) {
	t.Parallel()

	from := filepath.Join(t.TempDir(), "drafts")
	to := filepath.Join(t.TempDir(), "comments")
	old := filepath.Join(from, "example_atlassian_net", "PROJ-1.new.md")
	newer := filepath.Join(to, "example_atlassian_net", "PROJ-1.new.md")
	writeFile(t, old, "from the old build")
	writeFile(t, newer, "typed since")

	Migrate(from, to, ".md")

	if body, err := os.ReadFile(newer); err != nil || string(body) != "typed since" {
		t.Errorf("moving replaced the newer draft with %q, %v", body, err)
	}
	if body, err := os.ReadFile(old); err != nil || string(body) != "from the old build" {
		t.Errorf("the old draft was lost rather than left behind: %q, %v", body, err)
	}
}

func TestMigrate_LeavesFilesOfAnotherKindAlone(t *testing.T) {
	t.Parallel()

	from := filepath.Join(t.TempDir(), "drafts")
	to := filepath.Join(t.TempDir(), "comments")
	other := filepath.Join(from, "example_atlassian_net", "notes.txt")
	writeFile(t, other, "not a draft")

	Migrate(from, to, ".md")

	if _, err := os.Stat(other); err != nil {
		t.Errorf("a file that is no draft was moved or lost: %v", err)
	}
}

func TestMigrate_NothingToMoveIsNotAnError(t *testing.T) {
	t.Parallel()

	to := filepath.Join(t.TempDir(), "comments")
	Migrate(filepath.Join(t.TempDir(), "absent"), to, ".md")

	if _, err := os.Stat(to); !os.IsNotExist(err) {
		t.Errorf("moving nothing created %s: %v", to, err)
	}
}

func writeFile(t *testing.T, path, body string) {
	t.Helper()

	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
}
