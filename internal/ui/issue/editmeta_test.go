package issue

import (
	"errors"
	"testing"
	"time"

	"github.com/varijkapil13/saral/internal/app"
	"github.com/varijkapil13/saral/pkg/jira"
)

// A failure reading editmeta — a capability refusal, a rate limit, a transport
// error — must never reach Update at all: loadEditMeta's whole contract is
// that the ordering signal it carries is optional, and this is what makes
// that true regardless of which of the three ways a read can fail.
func TestLoadEditMeta_AFailedReadNeverArrives(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name string
		err  error
	}{
		{"a capability refusal", &jira.CapabilityError{Reason: "needs Browse projects"}},
		{"a rate limit", &jira.RateLimitError{RetryAfter: 30 * time.Second}},
		{"a transport failure", &jira.TransportError{Op: "GET editmeta", Err: errors.New("dial tcp: connection reset")}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			f := newFake(1)
			f.FailNext(tc.err)
			cmd := loadEditMeta(t.Context(), f, "PROJ-1", 1)
			if msg := cmd(); msg != nil {
				t.Fatalf("got %#v, want nil: a failed editmeta read must never reach the pane's Update", msg)
			}
		})
	}
}

// zebraID and alphaID are two custom fields on one issue, named so that
// alphabetical order and screen order disagree: Alpha sorts first by label,
// Zebra by nothing but the screen editmeta names it on.
const (
	zebraID = "customfield_30001"
	alphaID = "customfield_30002"
)

// orderIssue is an issue carrying two ordinary custom fields, both with
// values, so that reordering one above the other is visible without either
// disappearing.
func orderIssue() (jira.Issue, app.FieldLabels) {
	zebra := jira.Field{
		ID: zebraID, Key: zebraID, Name: "Zebra Field",
		Custom: true, Schema: jira.FieldSchema{Type: "string", Custom: "com.atlassian.jira:textfield"},
	}
	alpha := jira.Field{
		ID: alphaID, Key: alphaID, Name: "Alpha Field",
		Custom: true, Schema: jira.FieldSchema{Type: "string", Custom: "com.atlassian.jira:textfield"},
	}
	labels := app.NewFieldLabels([]jira.Field{zebra, alpha}, []string{zebraID, alphaID})
	iss := jira.Issue{
		ID: "40001", Key: "PROJ-4", Summary: "ordering",
		Project: jira.ProjectRef{Key: "PROJ"},
		Type:    jira.IssueType{Name: "Story"},
		Status:  jira.Status{Name: "Building", Category: jira.CategoryInProgress},
		Fields: jira.NewFieldSet(map[string]jira.FieldValue{
			zebraID: {Kind: jira.KindText, Text: "z-value"},
			alphaID: {Kind: jira.KindText, Text: "a-value"},
		}),
		Requested: jira.NewFieldMask([]string{"summary", "status", "issuetype", "project", zebraID, alphaID}),
	}
	return iss, labels
}

func fieldOrder(values []named) []string {
	out := make([]string, len(values))
	for i, v := range values {
		out[i] = v.label
	}
	return out
}

// Before editmeta arrives, and whenever it never does, the fields section
// falls back to the alphabetical order this program always drew: Order
// answers false for everything on the zero-value EditMeta, so noScreenOrder
// wins every comparison and label breaks every tie.
func TestFields_WithNoScreenSignalTheOrderIsAlphabetical(t *testing.T) {
	t.Parallel()

	iss, labels := orderIssue()
	dr := newDriver(t, testDeps(newFake(1)), jira.Issue{Key: iss.Key, Summary: iss.Summary}, 90, 28)
	dr.send(loadedMsg{gen: dr.m.gen, issue: iss, labels: labels})

	values, _, _ := dr.m.customFields(60)
	if got := fieldOrder(values); !equalOrder(got, "Alpha Field", "Zebra Field") {
		t.Fatalf("got %v, want Alpha before Zebra", got)
	}
}

// The site's own screen moves a field above ones that outrank it
// alphabetically, without dropping what it did not name: Alpha stays drawn,
// only later.
func TestFields_TheScreenTheSiteSentOrdersOnScreenFieldsFirst(t *testing.T) {
	t.Parallel()

	iss, labels := orderIssue()
	dr := newDriver(t, testDeps(newFake(1)), jira.Issue{Key: iss.Key, Summary: iss.Summary}, 90, 28)
	dr.send(loadedMsg{gen: dr.m.gen, issue: iss, labels: labels})
	dr.send(editMetaMsg{gen: dr.m.gen, meta: jira.EditMeta{
		Fields: []jira.FieldMeta{{Field: jira.FieldRef{ID: zebraID}}},
	}})

	values, _, _ := dr.m.customFields(60)
	if got := fieldOrder(values); !equalOrder(got, "Zebra Field", "Alpha Field") {
		t.Fatalf("got %v, want Zebra before Alpha: editmeta named Zebra and not Alpha", got)
	}
}

func equalOrder(got []string, want ...string) bool {
	if len(got) != len(want) {
		return false
	}
	for i := range got {
		if got[i] != want[i] {
			return false
		}
	}
	return true
}

// Opening an issue reads editmeta exactly once, alongside the issue read
// rather than after it: the two share one fetch, so a keystroke that redraws
// the pane without reloading anything must not ask again.
func TestFetch_AsksForEditMetaOnceAlongsideTheIssueRead(t *testing.T) {
	t.Parallel()

	f := newFake(3)
	dr := newDriver(t, testDeps(f), seedOf(t, f, "PROJ-1"), 90, 28)
	dr.key("tab", "G", "down", "up")

	if !dr.m.loadedIssue {
		t.Fatal("the issue never loaded, so this proves nothing about editmeta running alongside it")
	}
	if got := countCalls(f, "EditMeta"); got != 1 {
		t.Fatalf("EditMeta was called %d times opening and redrawing one issue, want exactly 1: %v", got, f.Calls())
	}
}

// With no Jira client to ask, opening the pane must not attempt editmeta any
// more than it attempts the issue read itself — the same guard covers both,
// because a sidebar this build cannot fetch anything for is never drawn from
// a live site.
func TestFetch_AsksForNothingWithNoJiraClient(t *testing.T) {
	t.Parallel()

	dr := newDriver(t, testDeps(nil), jira.Issue{Key: "PROJ-1"}, 90, 28)
	if dr.m.loadedIssue {
		t.Fatal("the issue loaded with no client to read it from")
	}
	if len(dr.m.edit.Fields) != 0 {
		t.Fatal("editmeta produced fields with no client to read it from")
	}
}
