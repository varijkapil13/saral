package kernel

import (
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"

	"github.com/varijkapil13/saral/internal/app"
)

// recordingCache counts what is asked of it, so a test can say that the kernel
// carried it somewhere rather than used it.
type recordingCache struct{ calls int }

func (c *recordingCache) Get(string, string) (app.Entry, bool, error) {
	c.calls++
	return app.Entry{}, false, nil
}

func (c *recordingCache) Put(string, string, []byte) error { c.calls++; return nil }

func (c *recordingCache) Each(string, func(string, app.Entry) error) error { c.calls++; return nil }

func (c *recordingCache) Purge(string) error { c.calls++; return nil }

var _ app.Cache = (*recordingCache)(nil)

// exercise runs a session through the messages the kernel handles itself.
func exercise(t *testing.T, d Deps) string {
	t.Helper()

	m := newAt(t, d, 120, 30)
	m, _ = press(m, "j")
	m, _ = press(m, "r")
	next, _ := m.Update(CapabilitiesMsg{Caps: fullCaps()})
	next, _ = next.(Model).Update(ProjectMsg{Project: "PROJ"})
	return ansi.Strip(next.(Model).Frame())
}

func TestCache_ASessionWithoutOneRunsExactlyAsOneWithIt(t *testing.T) {
	resetRegistry()
	t.Cleanup(resetRegistry)
	RegisterView(spec("board", 1, "", &stubView{id: "board"}))

	none := testDeps()
	if none.Cache != nil {
		t.Fatal("the test session already carries a cache; this test is about not having one")
	}
	withNone := exercise(t, none)

	cache := &recordingCache{}
	held := testDeps()
	held.Cache = cache
	withOne := exercise(t, held)

	if !strings.Contains(withNone, "board body") {
		t.Fatalf("a session with no cache does not draw:\n%s", withNone)
	}
	if withNone != withOne {
		t.Errorf("a cache changed the frame:\n--- without ---\n%s\n--- with ---\n%s", withNone, withOne)
	}
	if cache.calls != 0 {
		t.Errorf("the kernel made %d calls on the cache; it carries one to the views and reads none itself", cache.calls)
	}
}

func TestCache_ReachesAViewThroughTheDepsItIsBuiltWith(t *testing.T) {
	resetRegistry()
	t.Cleanup(resetRegistry)
	cache := &recordingCache{}
	var got app.Cache
	RegisterView(ViewSpec{ID: "board", Title: "Board", Slot: 1, New: func(d Deps) View {
		got = d.Cache
		return &stubView{id: "board"}
	}})

	d := testDeps()
	d.Cache = cache
	if _, err := New(d, WithSize(120, 30)); err != nil {
		t.Fatalf("New: %v", err)
	}
	if got != app.Cache(cache) {
		t.Errorf("the view was built with cache %v, want the one the session was given", got)
	}
}
