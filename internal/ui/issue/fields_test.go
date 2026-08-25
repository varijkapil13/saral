package issue

import (
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"

	"github.com/varijkapil13/saral/internal/app"
	"github.com/varijkapil13/saral/pkg/jira"
)

// wideSummary is what breaks a two-column layout measured with len(): umlauts
// that are two bytes and one cell, CJK that is one rune and two cells, an emoji
// with a variation selector, a vulgar fraction and a typographic ligature.
const wideSummary = "Größe der Spalte prüfen — 日本語の要約 🚀 ¾ ligature ﬁ"

// relatedIssue is an issue with the three things the old pane comma-joined into
// a row of bare keys, plus a custom field only the site can name.
func relatedIssue() (jira.Issue, app.FieldLabels) {
	points := jira.Field{
		ID: "customfield_20001", Key: "customfield_20001", Name: "Aufwandsschätzung",
		Custom: true, Schema: jira.FieldSchema{Type: "number", Custom: "com.atlassian.jira:float"},
	}
	unread := jira.Field{
		ID: "customfield_20002", Key: "customfield_20002", Name: "Abnahmekriterien",
		Custom: true, Schema: jira.FieldSchema{Type: "string", Custom: "com.atlassian.jira:textarea"},
	}
	labels := app.NewFieldLabels([]jira.Field{points, unread}, []string{points.ID, unread.ID})

	iss := jira.Issue{
		ID: "30001", Key: "PROJ-2", Summary: wideSummary,
		Project: jira.ProjectRef{Key: "PROJ", Name: "Spaltenbreite"},
		Type:    jira.IssueType{Name: "Story"},
		Status:  jira.Status{Name: "Building", Category: jira.CategoryInProgress},
		Parent: &jira.IssueRef{
			Key: "PROJ-1", Summary: "Die Tabelle · 日本語",
			Status: jira.Status{Name: "Triage", Category: jira.CategoryToDo},
		},
		Subtasks: []jira.IssueRef{
			{Key: "PROJ-101", Summary: "Spaltenbreite messen", Status: jira.Status{Name: "Shipped", Category: jira.CategoryDone}},
			{Key: "PROJ-7", Summary: "🚀 launch checklist", Status: jira.Status{Name: "Triage", Category: jira.CategoryToDo}},
		},
		Links: []jira.IssueLink{
			{
				Type: "Blocks", Label: "is blocked by",
				Other: jira.IssueRef{Key: "PROJ-9", Summary: "Umlaute prüfen", Status: jira.Status{Name: "Building", Category: jira.CategoryInProgress}},
			},
		},
		Fields: jira.NewFieldSet(map[string]jira.FieldValue{
			points.ID: {Kind: jira.KindNumber, Number: 5},
		}),
		Requested: jira.NewFieldMask([]string{
			"summary", "status", "issuetype", "project", "parent", "subtasks", "issuelinks",
			points.ID, unread.ID,
		}),
	}
	return iss, labels
}

// fieldsPane is the pane showing relatedIssue, read the way a detail read hands
// it over.
func fieldsPane(t *testing.T, w, h int) *driver {
	t.Helper()

	iss, labels := relatedIssue()
	dr := newDriver(t, testDeps(newFake(4)), jira.Issue{Key: iss.Key, Summary: iss.Summary}, w, h)
	dr.send(loadedMsg{gen: dr.m.gen, issue: iss, labels: labels})
	return dr
}

// A custom field's ID differs on every site and its name is translated, so the
// label has to come from the answer the value arrived with.
func TestFields_ACustomFieldIsNamedTheWayThisSiteSpellsIt(t *testing.T) {
	t.Parallel()

	dr := fieldsPane(t, 80, 26)
	dr.key("tab", "G")

	got := dr.view()
	mustContain(t, got, "Aufwandsschätzung", "5")
	mustNotContain(t, got, "customfield_20001")
}

// Where the read did not ask for a field, the pane says so. An empty row would
// claim the site had nothing to send, which is the other answer — and a field
// that was asked for and came back empty is counted rather than drawn.
func TestFields_AFieldOutsideTheReadSaysSoRatherThanDrawingBlank(t *testing.T) {
	t.Parallel()

	dr := fieldsPane(t, 80, 26)
	dr.key("tab", "G")

	got := dr.view()
	// reporter, duedate, created, labels, components, fixVersions, timetracking,
	// resolution and resolutiondate are all outside this read.
	if n := strings.Count(got, absent); n < 5 {
		t.Errorf("only %d fields say they were not asked for:\n%s", n, got)
	}
	mustContain(t, got, "Reporter", absent, "1 more, all empty")
	// The one that was asked for and came back with nothing is not drawn at all.
	mustNotContain(t, got, "Abnahmekriterien")
}

// Subtasks and links used to be a comma-joined row of bare keys, which says
// nothing about what is blocking what. An IssueRef already carries the status
// and the summary.
func TestFields_SubtasksAndLinksCarryTheStatusAndTheSummary(t *testing.T) {
	t.Parallel()

	dr := fieldsPane(t, 80, 26)
	dr.key("tab", "G")

	got := dr.view()
	mustContain(t, got,
		"Parent", "PROJ-1", "Triage",
		"Subtasks", "PROJ-101", "Shipped", "Spaltenbreite messen",
		"is blocked by", "PROJ-9", "Building", "Umlaute prüfen",
	)
	// The keys are a column rather than a sentence, so a reader can scan them.
	lines := strings.Split(got, "\n")
	var subtasks []string
	for _, line := range lines {
		if strings.Contains(line, "PROJ-101") || strings.Contains(line, "PROJ-7 ") {
			subtasks = append(subtasks, line)
		}
	}
	if len(subtasks) != 2 {
		t.Fatalf("the two subtasks are on %d lines, want one each:\n%s", len(subtasks), got)
	}
	first := strings.Index(subtasks[0], "Shipped")
	second := strings.Index(subtasks[1], "Triage")
	if first != second {
		t.Errorf("the status column starts at %d on one row and %d on the other:\n%s", first, second, got)
	}
}

// The case that breaks a layout measured with len(): every row still has to be
// exactly as many cells as its box.
func TestFields_AWideSummaryDoesNotShiftTheColumns(t *testing.T) {
	t.Parallel()

	for _, size := range []struct{ w, h int }{{80, 20}, {90, 28}, {120, 38}} {
		dr := fieldsPane(t, size.w, size.h)
		for _, focus := range []int{0, 1, 2} {
			if focus > 0 {
				dr.key("tab")
			}
			for i, line := range strings.Split(dr.view(), "\n") {
				if got := ansi.StringWidth(line); got > size.w {
					t.Errorf("%dx%d row %d is %d cells wide, want at most %d: %q",
						size.w, size.h, i, got, size.w, line)
				}
			}
		}
	}

	// And the summary itself is cut on a cluster boundary rather than mid-rune:
	// the ligature and the emoji either survive whole or are gone.
	head := strings.SplitN(fieldsPane(t, 80, 20).view(), "\n", 2)[0]
	if strings.ContainsRune(head, '�') {
		t.Errorf("the identity line carries a replacement rune, so a cluster was cut: %q", head)
	}
}
