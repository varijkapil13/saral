// Package uitest holds what a view's tests need to drive a frame through the
// pointer. It is imported only from _test.go files.
package uitest

import (
	"runtime"
	"testing"
	"time"

	zone "github.com/lrstanley/bubblezone/v2"
)

// zoneWait bounds the wait for the zone manager's worker. It is generous
// because it is a ceiling and not an expectation: the worker answers in
// microseconds unless the race detector is holding everything else up.
const zoneWait = 10 * time.Second

// Zone is where a marked element sits in the frame drawn now, as a click has to
// find it. also names every other zone the click will resolve through — a view
// that finds the region under the pointer by zone, and then the element inside
// it, consults two — and Zone waits for all of them.
//
// The manager stores zones off the event loop, in the order their end markers
// appear in the frame, tagged with the scan that found them; an older scan's
// entries are purged only once the newer scan's sentinel is processed. Two
// things follow, and both bit. Get(id) can answer with where an element was in
// the frame before — same id, stale coordinates — so a poll for "non-zero" is
// satisfied at once by the stale entry, and a click there misses by however far
// the element moved. And an outer zone whose end marker closes after the inner
// ones is stored last, so a poll on the inner one proves nothing about the
// outer: the click resolves the region against a stale entry with the bounds
// of an earlier layout and is dropped before the element is looked at. Clearing
// every id first is synchronous, so an entry present afterwards can only be
// this frame's.
func Zone(t testing.TB, mgr *zone.Manager, frame func() string, id string, also ...string) zone.ZoneInfo {
	t.Helper()
	ids := append([]string{id}, also...)
	for _, want := range ids {
		mgr.Clear(want)
	}
	_ = mgr.Scan(frame())
	deadline := time.Now().Add(zoneWait)
	for {
		ready := true
		for _, want := range ids {
			if mgr.Get(want).IsZero() {
				ready = false
				break
			}
		}
		if ready {
			return *mgr.Get(id)
		}
		if time.Now().After(deadline) {
			t.Fatalf("nothing on screen is marked %q after %s", ids, zoneWait)
		}
		runtime.Gosched()
	}
}
