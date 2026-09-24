package config

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"

	"github.com/BurntSushi/toml"
	"github.com/gofrs/flock"
)

const workProfile = "[profiles.work]\nsite  = \"example.atlassian.net\"\n" +
	"email = \"you@example.com\"\ntoken = { keychain = \"saral:work\" }\n"

func writeFile(t *testing.T, path, body string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
}

func TestLoadFile_AnUnknownKeyIsAWarningRatherThanARefusal(t *testing.T) {
	t.Parallel()

	tests := map[string]struct {
		body string
		key  string
	}{
		"a typo in a profile key": {
			body: "active = \"work\"\n\n" + workProfile + "sight = \"example.atlassian.net\"\n",
			key:  "profiles.work.sight",
		},
		"a key in a saved query": {
			body: "active = \"work\"\n\n" + workProfile +
				"\n[[profiles.work.queries]]\nname = \"Blockers\"\njql = \"priority = Highest\"\nslot = 2\n",
			key: "profiles.work.queries.slot",
		},
		"a top-level key a newer build added": {
			body: "active = \"work\"\nlayout = \"compact\"\n\n" + workProfile,
			key:  "layout",
		},
	}
	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			path := filepath.Join(t.TempDir(), "config.toml")
			writeFile(t, path, tc.body)
			cfg, err := LoadFile(path)
			if err != nil {
				t.Fatalf("a file with an unknown key stopped the program opening: %v", err)
			}
			if _, err := cfg.Get("work"); err != nil {
				t.Errorf("the profile beside the unknown key was lost: %v", err)
			}
			if len(cfg.Warnings) != 1 || !strings.Contains(cfg.Warnings[0], tc.key) {
				t.Errorf("warnings %q do not name %s", cfg.Warnings, tc.key)
			}
		})
	}
}

func TestLoadFile_TheCheckedInUnknownKeyFileWarns(t *testing.T) {
	t.Parallel()

	cfg, err := LoadFile(filepath.Join("testdata", "unknown_key.toml"))
	if err != nil {
		t.Fatalf("LoadFile: %v", err)
	}
	if len(cfg.Warnings) == 0 || !strings.Contains(cfg.Warnings[0], "profiles.work.sight") {
		t.Errorf("warnings %q do not name the misspelt key", cfg.Warnings)
	}
}

func TestLoadFile_ASecretLookingUnknownKeyIsStillRefused(t *testing.T) {
	t.Parallel()

	path := filepath.Join(t.TempDir(), "config.toml")
	writeFile(t, path, "version = 7\n\n"+workProfile+"api_token = \"abc\"\n")
	if _, err := LoadFile(path); !errors.Is(err, ErrSecretInFile) {
		t.Fatalf("a token under an unknown key in a newer file was accepted: %v", err)
	}
}

func TestLoadFile_ANewerFileIsReadButNeverRewritten(t *testing.T) {
	t.Parallel()

	path := filepath.Join(t.TempDir(), "config.toml")
	body := "version = 99\nactive = \"work\"\n\n" + workProfile + "density = \"tight\"\n"
	writeFile(t, path, body)

	cfg, err := LoadFile(path)
	if err != nil {
		t.Fatalf("an older binary refused a newer file: %v", err)
	}
	joined := strings.Join(cfg.Warnings, " | ")
	if !strings.Contains(joined, "version 99") || !strings.Contains(joined, "profiles.work.density") {
		t.Errorf("warnings %q do not say the file is newer and name the key it skipped", cfg.Warnings)
	}

	if err := cfg.Save(path); !errors.Is(err, ErrNewerConfig) {
		t.Errorf("Save over a newer file returned %v, want ErrNewerConfig", err)
	}
	if err := profileWith().Save(path); !errors.Is(err, ErrNewerConfig) {
		t.Errorf("a config built from nothing overwrote a newer file: %v", err)
	}
	if err := UpdateFile(path, func(*Config) error { return nil }); !errors.Is(err, ErrNewerConfig) {
		t.Errorf("UpdateFile over a newer file returned %v, want ErrNewerConfig", err)
	}
	after, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(after) != body {
		t.Errorf("the newer file was changed:\n%s", after)
	}
}

func TestLoadFile_AFileWithNoVersionIsTheCurrentLayout(t *testing.T) {
	t.Parallel()

	path := filepath.Join(t.TempDir(), "config.toml")
	writeFile(t, path, "active = \"work\"\n\n"+workProfile)
	cfg, err := LoadFile(path)
	if err != nil {
		t.Fatalf("LoadFile: %v", err)
	}
	if len(cfg.Warnings) != 0 {
		t.Errorf("a file with no version warned %q", cfg.Warnings)
	}
	if err := cfg.Save(path); err != nil {
		t.Fatalf("Save: %v", err)
	}
	if got := versionOnDisk(path); got != SchemaVersion {
		t.Errorf("the rewritten file says version %d, want %d", got, SchemaVersion)
	}
}

// Not parallel: it swaps the package's migration table, and parallel tests only
// start once every sequential one has finished.
func TestParse_RunsTheMigrationsFromTheFilesVersion(t *testing.T) {
	saved := migrations
	t.Cleanup(func() { migrations = saved })

	var ran []int
	migrations = map[int]func(*fileConfig, *toml.MetaData) error{
		0: func(f *fileConfig, _ *toml.MetaData) error {
			ran = append(ran, 0)
			for name, p := range f.Profiles {
				p.Theme = "dark"
				f.Profiles[name] = p
			}
			return nil
		},
	}
	cfg, err := parse([]byte("active = \"work\"\n\n" + workProfile))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if !slices.Equal(ran, []int{0}) {
		t.Errorf("migrations ran %v, want [0]", ran)
	}
	if got := cfg.Profiles["work"].Theme; got != "dark" {
		t.Errorf("the migrated theme is %q, want dark", got)
	}

	ran = nil
	if _, err := parse([]byte("version = 1\nactive = \"work\"\n\n" + workProfile)); err != nil {
		t.Fatalf("parse: %v", err)
	}
	if len(ran) != 0 {
		t.Errorf("a current file was migrated again: %v", ran)
	}

	migrations = map[int]func(*fileConfig, *toml.MetaData) error{
		0: func(*fileConfig, *toml.MetaData) error { return errors.New("cannot lift this one") },
	}
	if _, err := parse([]byte(workProfile)); err == nil || !strings.Contains(err.Error(), "cannot lift this one") {
		t.Errorf("a failing migration returned %v", err)
	}
}

func TestSave_WritesThroughASymlinkRatherThanReplacingIt(t *testing.T) {
	t.Parallel()

	dotfiles := filepath.Join(t.TempDir(), "dotfiles")
	if err := os.MkdirAll(dotfiles, 0o700); err != nil {
		t.Fatal(err)
	}
	target := filepath.Join(dotfiles, "saral.toml")
	writeFile(t, target, "active = \"work\"\n\n"+workProfile)
	link := filepath.Join(t.TempDir(), "config.toml")
	if err := os.Symlink(target, link); err != nil {
		t.Skipf("this filesystem cannot make a symlink: %v", err)
	}

	if err := UpdateFile(link, func(cfg *Config) error {
		p := cfg.Profiles["work"]
		p.Theme = "light"
		cfg.Profiles["work"] = p
		return nil
	}); err != nil {
		t.Fatalf("UpdateFile: %v", err)
	}

	info, err := os.Lstat(link)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode()&os.ModeSymlink == 0 {
		t.Fatal("saving replaced the symlink with a regular file, so the dotfiles copy stopped being the config")
	}
	got, err := LoadFile(target)
	if err != nil {
		t.Fatalf("reading the target: %v", err)
	}
	if theme := got.Profiles["work"].Theme; theme != "light" {
		t.Errorf("the target's theme is %q, want the write to have reached it", theme)
	}
	leftovers, _ := filepath.Glob(filepath.Join(dotfiles, ".saral-*.tmp"))
	if len(leftovers) != 0 {
		t.Errorf("temporary files left beside the target: %v", leftovers)
	}
}

func TestLockFile_ExcludesASecondHandleOnTheSameFile(t *testing.T) {
	t.Parallel()

	path := filepath.Join(t.TempDir(), "config.toml")
	unlock, err := lockFile(path)
	if err != nil {
		t.Fatalf("lockFile: %v", err)
	}
	other := flock.New(filepath.Join(filepath.Dir(path), ".config.toml.lock"))
	if ok, err := other.TryLock(); err != nil || ok {
		t.Fatalf("a second handle took the lock while the first held it (ok=%t, err=%v)", ok, err)
	}
	unlock()
	ok, err := other.TryLock()
	if err != nil || !ok {
		t.Fatalf("the lock outlived its release (ok=%t, err=%v)", ok, err)
	}
	if err := other.Unlock(); err != nil {
		t.Fatal(err)
	}
}

// Each goroutine opens its own lock handle, which is what two copies of Saral
// are: without the file lock, one read-merge-write reads before the other's
// rename and writes a file missing it.
func TestUpdateFile_ConcurrentWritersKeepEachOthersChanges(t *testing.T) {
	t.Parallel()

	path := filepath.Join(t.TempDir(), "config.toml")
	writeFile(t, path, "active = \"work\"\n\n"+workProfile)

	const writers, each = 2, 20
	var wg sync.WaitGroup
	errs := make(chan error, writers*each)
	for w := range writers {
		wg.Go(func() {
			for i := range each {
				errs <- UpdateFile(path, func(cfg *Config) error {
					p := cfg.Profiles["work"]
					p.Pinned = append(p.Pinned, fmt.Sprintf("field_%d_%d", w, i))
					cfg.Profiles["work"] = p
					return nil
				})
			}
		})
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatalf("UpdateFile: %v", err)
		}
	}
	cfg, err := LoadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if got := len(cfg.Profiles["work"].Pinned); got != writers*each {
		t.Errorf("the file holds %d of the %d pinned fields written, so a writer overwrote another's", got, writers*each)
	}
}

func TestRememberState_ConcurrentWritersKeepEachOthersKeys(t *testing.T) {
	t.Setenv("SARAL_CACHE_DIR", t.TempDir())

	scope := ProfileScope{Site: "example.atlassian.net", Account: "you@example.com"}
	const writers, each = 2, 20
	var wg sync.WaitGroup
	errs := make(chan error, writers*each)
	for w := range writers {
		wg.Go(func() {
			for i := range each {
				errs <- RememberState(scope, "list", fmt.Sprintf("k%d_%d", w, i), "v")
			}
		})
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatalf("RememberState: %v", err)
		}
	}
	state := LoadUIState()
	for w := range writers {
		for i := range each {
			if _, ok := state.RememberedState(scope, "list", fmt.Sprintf("k%d_%d", w, i)); !ok {
				t.Errorf("k%d_%d was lost to another writer", w, i)
			}
		}
	}
}
