package app

import (
	"runtime"
	"testing"
	"time"

	"github.com/varijkapil13/saral/pkg/jira/jiratest"
)

func testFake(issues int) *jiratest.Fake {
	return jiratest.New(
		jiratest.WithProject("PROJ", jiratest.Scrum),
		jiratest.WithIssues(jiratest.Gen(issues)),
	)
}

// callsTo counts how many times the fake was asked for one thing.
func callsTo(f *jiratest.Fake, method string) int {
	n := 0
	for _, call := range f.Calls() {
		if call == method {
			n++
		}
	}
	return n
}

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
