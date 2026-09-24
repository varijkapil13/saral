package settings

import (
	"errors"
	"strings"
	"testing"

	"github.com/varijkapil13/saral/internal/app"
	"github.com/varijkapil13/saral/internal/ui/kernel"
)

func runCmd(t *testing.T, d kernel.Deps) kernel.StatusMsg {
	t.Helper()
	cmd := clearCache(d)
	if cmd == nil {
		t.Fatal("clearing returned no command, so nothing is said")
	}
	msg, ok := cmd().(kernel.StatusMsg)
	if !ok {
		t.Fatalf("clearing said %T, want a status line", msg)
	}
	return msg
}

type clearableCache struct {
	app.Cache
	cleared int
}

func (c *clearableCache) Clear() error { c.cleared++; return nil }

func TestClearCache_EmptiesTheSessionsCache(t *testing.T) {
	t.Parallel()

	cache := &clearableCache{}
	cmd := clearCache(kernel.Deps{Cache: cache})
	if cache.cleared != 0 {
		t.Fatal("the cache was cleared on the event loop rather than in the command")
	}
	if cmd == nil {
		t.Fatal("clearing returned no command")
	}
	msg, _ := cmd().(kernel.StatusMsg)
	if cache.cleared != 1 {
		t.Errorf("Clear ran %d times, want once", cache.cleared)
	}
	if msg.Level != kernel.LevelInfo || !strings.Contains(msg.Text, "cache cleared") {
		t.Errorf("clearing said %+v", msg)
	}
}

func TestClearCache_ASessionWithNoCacheIsToldSo(t *testing.T) {
	t.Parallel()

	msg := runCmd(t, kernel.Deps{})
	if msg.Level != kernel.LevelWarn || !strings.Contains(msg.Text, "no cache") {
		t.Errorf("clearing with no cache said %+v", msg)
	}
}

type failingCache struct{ app.Cache }

func (failingCache) Clear() error { return errors.New("disk on fire") }

func TestClearCache_AFailureIsAWarning(t *testing.T) {
	t.Parallel()

	msg := runCmd(t, kernel.Deps{Cache: failingCache{}})
	if msg.Level != kernel.LevelWarn || !strings.Contains(msg.Text, "disk on fire") {
		t.Errorf("a failed clear said %+v", msg)
	}
}

func TestClearCache_IsRegisteredBesideForgetTheRememberedView(t *testing.T) {
	t.Parallel()

	var memory, cache *kernel.Setting
	for _, s := range kernel.Settings() {
		switch s.ID {
		case "session.memory":
			memory = &s
		case "session.cache":
			cache = &s
		}
	}
	if cache == nil || memory == nil {
		t.Fatalf("session.cache registered = %t, session.memory = %t", cache != nil, memory != nil)
	}
	if cache.Section != memory.Section || cache.Order != memory.Order+1 {
		t.Errorf("session.cache is %s/%d, want right after session.memory at %s/%d",
			cache.Section, cache.Order, memory.Section, memory.Order)
	}
}
