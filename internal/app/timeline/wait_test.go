package timeline

import (
	"runtime"
	"testing"
	"time"
)

// waitFor spins until cond holds, so that a test waits on another goroutine
// reaching a state rather than on a duration.
func waitFor(t *testing.T, what string, cond func() bool) {
	t.Helper()

	deadline := time.Now().Add(10 * time.Second)
	for !cond() {
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for %s", what)
		}
		runtime.Gosched()
	}
}
