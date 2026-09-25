package arch

import (
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// isolationRoots is every tree whose test packages can build a kernel.Deps,
// open the config file or open the disk cache: every internal/ui/* package,
// internal/app, internal/config and cmd/saral. internal/config.Dir and
// internal/config.CacheDir resolve from SARAL_CONFIG_DIR/SARAL_CACHE_DIR
// first and XDG_CONFIG_HOME/XDG_CACHE_HOME second, so a test binary that
// never sets any of the four falls through to whoever is running the suite's
// own directories.
var isolationRoots = []string{"internal/ui", "internal/app", "internal/config", "cmd/saral"}

// isolationExempt is the closed list of packages under isolationRoots that
// TestTestPackages_IsolateConfigAndCacheDirs does not require
// testsupport.IsolateDirs from, each with why. Fail-closed: a package under
// isolationRoots that is not listed here and does not call it fails the
// build, so a new one starts covered rather than opting in later.
var isolationExempt = map[string]string{}

func underAnyIsolationRoot(pkgDir string) bool {
	for _, root := range isolationRoots {
		if underPath(pkgDir, root) {
			return true
		}
	}
	return false
}

// TestTestPackages_IsolateConfigAndCacheDirs is what keeps a test package
// from ever reading or writing the real config.toml or cache.db of whoever
// runs the suite: every package under isolationRoots that has test files
// must wire testsupport.IsolateDirs into a TestMain, once for the whole
// binary rather than per test, because t.Setenv panics on a parallel test.
func TestTestPackages_IsolateConfigAndCacheDirs(t *testing.T) {
	t.Parallel()

	root := moduleRoot(t)
	byPkg := map[string][]string{}

	walkErr := filepath.WalkDir(root, func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.IsDir() {
			if path != root && skipDir(entry.Name()) {
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(entry.Name(), "_test.go") {
			return nil
		}
		rel, err := relativePath(root, path)
		if err != nil {
			return err
		}
		pkgDir := dirOf(rel)
		if !underAnyIsolationRoot(pkgDir) {
			return nil
		}
		content, err := os.ReadFile(path)
		if err != nil {
			return fmt.Errorf("reading %s: %w", rel, err)
		}
		byPkg[pkgDir] = append(byPkg[pkgDir], string(content))
		return nil
	})
	if walkErr != nil {
		t.Fatalf("walking %s: %v", root, walkErr)
	}
	if len(byPkg) == 0 {
		t.Fatalf("scanned no test packages under %v: the walk found nothing, so this check proves nothing", isolationRoots)
	}

	for pkgDir, files := range byPkg {
		if reason, exempt := isolationExempt[pkgDir]; exempt {
			if strings.TrimSpace(reason) == "" {
				t.Errorf("isolationExempt exempts %q with no reason given", pkgDir)
			}
			continue
		}
		var hasTestMain, hasIsolate bool
		for _, content := range files {
			hasTestMain = hasTestMain || strings.Contains(content, "func TestMain(m *testing.M)")
			hasIsolate = hasIsolate || strings.Contains(content, "testsupport.IsolateDirs(")
		}
		switch {
		case !hasTestMain:
			t.Errorf("%s has tests and no TestMain, so it can read or write the real config and cache "+
				"directories: add one calling testsupport.IsolateDirs(m), or exempt it in isolationExempt with why", pkgDir)
		case !hasIsolate:
			t.Errorf("%s has a TestMain that never calls testsupport.IsolateDirs, so it can still read or "+
				"write the real config and cache directories", pkgDir)
		}
	}

	for pkgDir := range isolationExempt {
		if _, ok := byPkg[pkgDir]; !ok {
			t.Errorf("isolationExempt names %q, which has no test files under it: an exemption naming "+
				"nothing is misspelt or left behind", pkgDir)
		}
	}
}
