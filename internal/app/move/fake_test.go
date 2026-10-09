package move

import (
	"errors"
	"testing"
	"time"

	"github.com/varijkapil13/saral/pkg/jira"
	"github.com/varijkapil13/saral/pkg/jira/jiratest"
)

// failures are the three ways a port call goes wrong.
func failures() map[string]error {
	return map[string]error{
		"a 403":               &jira.CapabilityError{Capability: jira.CapBulkMove, Reason: "You may not browse PROJ"},
		"a 429":               &jira.RateLimitError{RetryAfter: 30 * time.Second, Endpoint: "/issue/createmeta"},
		"a transport failure": &jira.TransportError{Op: "read", Err: errors.New("dial tcp: no route to host")},
	}
}

func mustBe(t *testing.T, got, want error) {
	t.Helper()
	if !errors.Is(got, want) || got.Error() != want.Error() {
		t.Errorf("the error came back as %v (%T), want the port's own %v", got, got, want)
	}
}

func newFake(issues int, opts ...jiratest.Option) *jiratest.Fake {
	return jiratest.New(append([]jiratest.Option{
		jiratest.WithProject("PROJ", jiratest.Scrum),
		jiratest.WithProject("OTHER", jiratest.Scrum),
		jiratest.WithIssues(jiratest.Gen(issues)),
	}, opts...)...)
}

func seeded(t *testing.T, f *jiratest.Fake, keys ...string) []jira.Issue {
	t.Helper()
	out := make([]jira.Issue, 0, len(keys))
	for _, key := range keys {
		iss, err := f.Issue(t.Context(), key)
		if err != nil {
			t.Fatalf("reading %s out of the fake: %v", key, err)
		}
		out = append(out, iss)
	}
	return out
}

func countCalls(f *jiratest.Fake, name string) int {
	n := 0
	for _, call := range f.Calls() {
		if call == name {
			n++
		}
	}
	return n
}
