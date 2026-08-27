package move

import (
	"testing"

	"github.com/varijkapil13/saral/pkg/jira"
)

// The default has to come from the status category and never from the display
// name. Both are on offer here and they disagree: one measured site had four
// pairs of distinct status ids sharing a name, and a German site translates every
// name while the category key stays the same three words.
func TestDefaultRemap_ComesFromTheCategoryAndNotFromTheName(t *testing.T) {
	t.Parallel()
	source := []jira.Issue{{
		Key:    "PROJ-1",
		Status: jira.Status{ID: "10202", Name: "Building", Category: jira.CategoryInProgress},
	}}
	targets := []jira.Status{
		{ID: "20001", Name: "Building", Category: jira.CategoryDone},
		{ID: "20002", Name: "Werkstatt", Category: jira.CategoryInProgress},
	}

	rows := defaultRemap(sourceStatuses(source), targets)
	if len(rows) != 1 {
		t.Fatalf("one source status became %d rows", len(rows))
	}
	got := targets[rows[0].to]
	if got.ID != "20002" {
		t.Errorf("Building was pointed at %s (%q, category %v); the only target in the source's category "+
			"is 20002, so this default came from the name", got.ID, got.Name, got.Category)
	}
}

// A target workflow with nothing in the source's category still has to offer
// something, and the row says so by being on screen for the user to change.
func TestDefaultRemap_FallsBackToTheFirstTargetWhenNoCategoryMatches(t *testing.T) {
	t.Parallel()
	source := []jira.Issue{{Status: jira.Status{ID: "1", Name: "Triage", Category: jira.CategoryToDo}}}
	targets := []jira.Status{{ID: "9", Name: "Shipped", Category: jira.CategoryDone}}

	rows := defaultRemap(sourceStatuses(source), targets)
	if rows[0].to != 0 {
		t.Errorf("the fallback landed on %d, want the first target", rows[0].to)
	}

	none := defaultRemap(sourceStatuses(source), nil)
	if none[0].to != -1 {
		t.Errorf("a target with no statuses left the row pointing at %d rather than at nothing", none[0].to)
	}
}

// Two statuses that share a display name are two rows, because they are two
// statuses. Collapsing them is how a remap silently moves half the selection to
// the wrong place.
func TestSourceStatuses_AreDistinctByIdAndNotByName(t *testing.T) {
	t.Parallel()
	issues := []jira.Issue{
		{Status: jira.Status{ID: "10202", Name: "Building", Category: jira.CategoryInProgress}},
		{Status: jira.Status{ID: "10204", Name: "Building", Category: jira.CategoryInProgress}},
		{Status: jira.Status{ID: "10202", Name: "Building", Category: jira.CategoryInProgress}},
	}
	rows := sourceStatuses(issues)
	if len(rows) != 2 {
		t.Fatalf("three issues on two status ids became %d rows: %v", len(rows), names(rows))
	}
	if rows[0].from.ID != "10202" || rows[0].count != 2 {
		t.Errorf("the first row is %s with %d issues, want 10202 with 2", rows[0].from.ID, rows[0].count)
	}
	if rows[1].from.ID != "10204" || rows[1].count != 1 {
		t.Errorf("the second row is %s with %d issues, want 10204 with 1", rows[1].from.ID, rows[1].count)
	}
}

// The workflow is per issue type, so the same project answers differently for two
// of them and the lookup has to be by type id.
func TestStatusesFor_AnswersPerIssueTypeAndNotPerProject(t *testing.T) {
	t.Parallel()
	vocab := []jira.IssueTypeStatuses{
		{Type: jira.IssueType{ID: "10301"}, Statuses: []jira.Status{{ID: "a"}}},
		{Type: jira.IssueType{ID: "10305"}, Statuses: []jira.Status{{ID: "b"}}},
	}
	if got := statusesFor(vocab, "10305"); len(got) != 1 || got[0].ID != "b" {
		t.Errorf("the subtask type's workflow came back as %v", got)
	}
	if got := statusesFor(vocab, "nosuch"); got != nil {
		t.Errorf("a type this project does not run answered with %v rather than nothing", got)
	}
}

func TestTooMany_RefusesAboveTheEndpointsCapAndSaysBothNumbers(t *testing.T) {
	t.Parallel()
	if _, over := tooMany(maxKeys); over {
		t.Errorf("%d issues was refused and the endpoint takes it", maxKeys)
	}
	reason, over := tooMany(maxKeys + 1)
	if !over {
		t.Fatalf("%d issues was accepted and the endpoint takes %d", maxKeys+1, maxKeys)
	}
	mustContain(t, reason, "1000", "1001")
}

// Backoff has to grow and then stop growing: a poll a minute apart reports a move
// finished long after it was.
func TestBackoff_GrowsToACeilingAndStartsAtOnce(t *testing.T) {
	t.Parallel()
	if got := backoff(0); got != 0 {
		t.Errorf("the first question waits %s", got)
	}
	last := backoff(1)
	for at := 2; at < 12; at++ {
		got := backoff(at)
		switch {
		case got < last:
			t.Fatalf("question %d waits %s after %s", at, got, last)
		case got > pollCap:
			t.Fatalf("question %d waits %s, past the %s ceiling", at, got, pollCap)
		}
		last = got
	}
	if last != pollCap {
		t.Errorf("the wait settled at %s rather than the %s ceiling", last, pollCap)
	}
}
