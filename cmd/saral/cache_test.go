package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/varijkapil13/saral/internal/app"
	"github.com/varijkapil13/saral/internal/config"
	"github.com/varijkapil13/saral/internal/store"
	"github.com/varijkapil13/saral/pkg/jira/jiratest"
)

func cachePath(t *testing.T) string {
	t.Helper()
	dir, err := config.CacheDir()
	if err != nil {
		t.Fatalf("CacheDir: %v", err)
	}
	return filepath.Join(dir, cacheFile)
}

// A cache.db that is not a database used to warn on every launch and never be
// replaced, so a session never cached anything again.
func TestBuild_ACorruptCacheIsMovedAsideAndReplacedOnce(t *testing.T) {
	writeProfile(t)
	t.Setenv("SARAL_TEST_TOKEN", "a-token")

	path := cachePath(t)
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("this was never a bbolt file"), 0o600); err != nil {
		t.Fatal(err)
	}

	deps, _, notice, closeCache, err := build(options{})
	if err != nil {
		t.Fatalf("build: %v", err)
	}
	if deps.Cache == nil {
		t.Fatal("a corrupt cache left the session with none, on this launch and every one after")
	}
	if !strings.Contains(notice, "moved to "+path+".corrupt-") {
		t.Errorf("the notice %q does not say where the unreadable file went", notice)
	}
	if err := deps.Cache.PutRows(`project = "PROJ"`, jiratest.Gen(1), false); err != nil {
		t.Errorf("the fresh cache cannot be written: %v", err)
	}
	closeCache()

	aside, _ := filepath.Glob(path + ".corrupt-*")
	if len(aside) != 1 {
		t.Errorf("found %v moved aside, want exactly the one file", aside)
	}

	deps, _, notice, closeCache, err = build(options{})
	if err != nil {
		t.Fatalf("the second build: %v", err)
	}
	defer closeCache()
	if notice != "" {
		t.Errorf("the second launch said %q; the recovery is to be said once", notice)
	}
	if _, ok := deps.Cache.Rows(`project = "PROJ"`); !ok {
		t.Error("what the first session stored into the fresh cache did not survive to the second")
	}
}

func TestBuild_DropsTheCacheOfAProfileNoLongerConfigured(t *testing.T) {
	writeProfile(t)
	t.Setenv("SARAL_TEST_TOKEN", "a-token")

	gone := store.Scope{Site: "example.atlassian.net", Account: "old.address@example.com"}
	elsewhere := store.Scope{Site: "other.atlassian.net", Account: "you@example.com"}
	db, err := store.Open(cachePath(t))
	if err != nil {
		t.Fatal(err)
	}
	for _, s := range []store.Scope{gone, elsewhere} {
		if err := app.NewCache(db, s).PutRows(`project = "PROJ"`, jiratest.Gen(2), false); err != nil {
			t.Fatal(err)
		}
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}

	_, _, _, closeCache, err := build(options{})
	if err != nil {
		t.Fatalf("build: %v", err)
	}
	closeCache()

	db, err = store.Open(cachePath(t))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close() }()
	held, err := db.Scopes()
	if err != nil {
		t.Fatal(err)
	}
	for _, s := range held {
		if s == gone || s == elsewhere {
			t.Errorf("the cache still holds %v, which no profile in config.toml names", s)
		}
	}
}

func TestBuild_SweepsWhatIsPastItsRetention(t *testing.T) {
	writeProfile(t)
	t.Setenv("SARAL_TEST_TOKEN", "a-token")

	scope := store.Scope{Site: "example.atlassian.net", Account: "you@example.com"}
	db, err := store.Open(cachePath(t))
	if err != nil {
		t.Fatal(err)
	}
	long := time.Now().Add(-app.KindSearch.Retention().MaxAge - time.Hour)
	old := app.NewCache(db, scope, app.WithClock(func() time.Time { return long }))
	if err := old.PutRows(`project = "OLD"`, jiratest.Gen(2), false); err != nil {
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}

	deps, _, _, closeCache, err := build(options{})
	if err != nil {
		t.Fatalf("build: %v", err)
	}
	defer closeCache()
	if _, ok := deps.Cache.Rows(`project = "OLD"`); ok {
		t.Error("a search stored past its retention was still served after the session opened")
	}
}

func TestBuild_AnUnknownConfigKeyIsOnTheStatusLineRatherThanFatal(t *testing.T) {
	writeProfile(t)
	t.Setenv("SARAL_TEST_TOKEN", "a-token")
	path, err := config.Path()
	if err != nil {
		t.Fatal(err)
	}
	f, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.WriteString("density = \"tight\"\n"); err != nil {
		t.Fatal(err)
	}
	if err := f.Close(); err != nil {
		t.Fatal(err)
	}

	deps, _, notice, closeCache, err := build(options{})
	if err != nil {
		t.Fatalf("an unknown key stopped the program: %v", err)
	}
	defer closeCache()
	if deps.Jira == nil {
		t.Error("the profile beside the unknown key did not open")
	}
	if !strings.Contains(notice, "profiles.work.density") {
		t.Errorf("the notice %q does not name the key that was ignored", notice)
	}
}
