package config

import (
	"path/filepath"
	"strings"
	"testing"
)

// The two that were got wrong when this was written, and both are the everyday
// case rather than an edge: a pseudo-version separates its timestamp from a
// tagged base with a dot and not a dash, and Go appends "+dirty" to any build
// made with uncommitted changes — which is every build during development.
// Either one alone read a checkout as a release and sent it at the installed
// copy's profile.
func TestIsDevVersion(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		version string
		dev     bool
	}{
		{"v0.2.1", false},
		{"v1.0.0", false},
		{"v0.3.0-rc1", false},
		{"v0.3.0-beta.2", false},
		{"v0.2.2-0.20260904063733-5c1923c93f74", true},
		{"v0.2.2-0.20260904063733-5c1923c93f74+dirty", true},
		{"v0.0.0-20260904063733-5c1923c93f74", true},
		{"v0.3.0-rc1.0.20260904063733-5c1923c93f74", true},
		{"(devel)", true},
		{"", true},
		{"   ", true},
		{"unknown", true},
		{"0.2.1", true},
	} {
		if got := isDevVersion(tc.version); got != tc.dev {
			t.Errorf("isDevVersion(%q) = %v, want %v", tc.version, got, tc.dev)
		}
	}
}

// A test binary is built from a checkout, so this suite runs as a development
// build — which is the half of the split that can be asserted from inside it.
func TestDirName_ATestBinaryIsADevelopmentBuild(t *testing.T) {
	t.Parallel()

	if !IsDevBuild() {
		t.Fatalf("a test binary resolved to %q, so the split is not deciding on the build", dirName())
	}
	if dirName() == appName {
		t.Errorf("the development directory is %q, which is the installed copy's", dirName())
	}
}

// Both directories carry the qualification, or a development build keeps its
// profile apart and then fights the installed copy over the cache's file lock.
func TestDirs_ConfigAndCacheAreBothQualifiedByTheBuild(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", "/tmp/xdg-config")
	t.Setenv("XDG_CACHE_HOME", "/tmp/xdg-cache")
	t.Setenv("SARAL_CONFIG_DIR", "")
	t.Setenv("SARAL_CACHE_DIR", "")

	for name, get := range map[string]func() (string, error){"config": Dir, "cache": CacheDir} {
		got, err := get()
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		if base := filepath.Base(got); base != devName {
			t.Errorf("the %s directory is %q, whose last element is %q and not %q", name, got, base, devName)
		}
	}
}

// The explicit override names a directory outright. Qualifying it again would
// mean a session pointed at one directory writing into a child of it, which is
// not what somebody who set the variable asked for — docs/DEMO.md drives the
// program that way and the fixtures live where it says.
func TestDirs_AnExplicitOverrideIsNotQualifiedAgain(t *testing.T) {
	t.Setenv("SARAL_CONFIG_DIR", "/tmp/saral-demo/config")
	t.Setenv("SARAL_CACHE_DIR", "/tmp/saral-demo/cache")

	dir, err := Dir()
	if err != nil {
		t.Fatal(err)
	}
	if dir != "/tmp/saral-demo/config" {
		t.Errorf("Dir() = %q, want the override verbatim", dir)
	}
	if strings.Contains(dir, devName) {
		t.Errorf("Dir() = %q, which qualified a directory the caller named outright", dir)
	}
	cache, err := CacheDir()
	if err != nil {
		t.Fatal(err)
	}
	if cache != "/tmp/saral-demo/cache" {
		t.Errorf("CacheDir() = %q, want the override verbatim", cache)
	}
}
