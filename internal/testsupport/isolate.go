// Package testsupport holds what every test binary in this module needs
// before the first test runs: never the real config or cache directory.
// internal/config.Dir and internal/config.CacheDir resolve from
// SARAL_CONFIG_DIR/SARAL_CACHE_DIR first and XDG_CONFIG_HOME/XDG_CACHE_HOME
// second, so a test process that never sets any of the four falls through to
// the machine's own directories — real config.toml, real cache.db, on
// whoever's machine the suite happens to run on. It imports nothing from
// internal/config so that internal/config's own tests can import it too
// without a cycle, and nothing from internal/ui so that internal/app and
// cmd/saral can import it without breaking the layering internal/arch checks.
package testsupport

import (
	"os"
	"testing"
)

var configDir, cacheDir string

// IsolateDirs points every one of the four variables config.Dir and
// config.CacheDir consult at a pair of temporary directories, runs m, cleans
// up and returns the exit code. A package's TestMain is:
//
//	func TestMain(m *testing.M) { os.Exit(testsupport.IsolateDirs(m)) }
//
// It sets the environment once for the whole binary rather than per test,
// because t.Setenv panics on a parallel test and this suite runs parallel by
// default.
func IsolateDirs(m *testing.M) int {
	cfg, err := os.MkdirTemp("", "saral-test-config-*")
	if err != nil {
		panic(err)
	}
	defer func() { _ = os.RemoveAll(cfg) }()
	cache, err := os.MkdirTemp("", "saral-test-cache-*")
	if err != nil {
		panic(err)
	}
	defer func() { _ = os.RemoveAll(cache) }()

	for _, kv := range [...][2]string{
		{"SARAL_CONFIG_DIR", cfg},
		{"SARAL_CACHE_DIR", cache},
		{"XDG_CONFIG_HOME", cfg},
		{"XDG_CACHE_HOME", cache},
	} {
		if err := os.Setenv(kv[0], kv[1]); err != nil {
			panic(err)
		}
	}
	configDir, cacheDir = cfg, cache

	return m.Run()
}

// ConfigDir is the directory IsolateDirs pointed SARAL_CONFIG_DIR at, for a
// test that wants to assert config.Dir() actually resolved to it rather than
// to a real one. It is empty until IsolateDirs has run.
func ConfigDir() string { return configDir }

// CacheDir is CacheDir()'s equivalent of ConfigDir.
func CacheDir() string { return cacheDir }
