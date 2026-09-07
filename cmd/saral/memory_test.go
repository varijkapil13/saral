package main

import "testing"

func TestProfileMemory_RoundTripsThroughTheRealConfigPackage(t *testing.T) {
	t.Setenv("SARAL_CONFIG_DIR", t.TempDir())
	t.Setenv("SARAL_CACHE_DIR", t.TempDir())

	mem := newMemory("example.atlassian.net", "you@example.com")
	if _, ok := mem.Recall("list", "terms"); ok {
		t.Fatal("a fresh profile remembers something")
	}
	mem.Keep("list", "terms", `[{"facet":"status","id":"1","label":"Done"}]`)
	got, ok := mem.Recall("list", "terms")
	if !ok || got != `[{"facet":"status","id":"1","label":"Done"}]` {
		t.Errorf("Recall = %q, ok=%v", got, ok)
	}
	mem.Forget()
	if _, ok := mem.Recall("list", "terms"); ok {
		t.Error("Forget left something behind")
	}
}

// One profile's memory must never answer for another's, the same isolation
// config.ProfileScope's own tests already prove at the config layer.
func TestProfileMemory_DoesNotLeakBetweenSites(t *testing.T) {
	t.Setenv("SARAL_CONFIG_DIR", t.TempDir())
	t.Setenv("SARAL_CACHE_DIR", t.TempDir())

	mine := newMemory("example.atlassian.net", "you@example.com")
	other := newMemory("other.atlassian.net", "you@example.com")

	mine.Keep("board", "quickfilters", "[10,20]")
	if _, ok := other.Recall("board", "quickfilters"); ok {
		t.Error("another site's memory answered for this profile's")
	}
}
