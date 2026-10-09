// Package cachetest builds a real disk cache for tests outside the cache
// package, so they never have to open the store themselves.
package cachetest

import (
	"path/filepath"
	"testing"

	"github.com/varijkapil13/saral/internal/app/cache"
	"github.com/varijkapil13/saral/internal/store"
)

// Scope is the site and account every cache Open builds is scoped to.
var Scope = store.Scope{Site: "example.atlassian.net", Account: "you@example.com"}

// Open builds a cache over a fresh database in the test's temporary
// directory, closed when the test ends.
func Open(t testing.TB, opts ...cache.Option) *cache.Disk {
	t.Helper()

	db, err := store.Open(filepath.Join(t.TempDir(), "cache.db"))
	if err != nil {
		t.Fatalf("opening the cache: %v", err)
	}
	t.Cleanup(func() {
		if err := db.Close(); err != nil {
			t.Errorf("closing the cache: %v", err)
		}
	})
	return cache.New(db, Scope, opts...)
}
