package board

import (
	"strconv"
	"testing"

	"github.com/varijkapil13/saral/internal/ui/filter"
	"github.com/varijkapil13/saral/pkg/jira"
)

// twoColumnBoard is a board with one column deep enough to scroll on its own
// and one shallow enough that most of it fits in a small window, which is the
// shape the coupled-scroll bug was reported against: a long Backlog beside a
// short column that flashed whenever the long one moved.
func twoColumnBoard() jira.BoardConfig {
	return jira.BoardConfig{BoardID: 1, Name: "Ledger", Columns: []jira.Column{
		{Name: "Backlog", StatusIDs: []string{"10201"}},
		{Name: "Done", StatusIDs: []string{"10202"}},
	}}
}

// genIssues is n issues in one status, keyed prefix1..prefixN in the order a
// board with no rank field draws them.
func genIssues(prefix, statusID, statusName string, n int) []jira.Issue {
	out := make([]jira.Issue, 0, n)
	for i := 1; i <= n; i++ {
		out = append(out, jira.Issue{
			Key:     prefix + strconv.Itoa(i),
			Summary: "card " + strconv.Itoa(i),
			Status:  jira.Status{ID: statusID, Name: statusName},
		})
	}
	return out
}

// Moving the cursor down a long column must not change what a short column
// beside it shows: the bug this fixes was every other column's card flashing
// in and out because the whole grid shared one scroll position.
func TestBoardScroll_ALongColumnScrollingLeavesAShortColumnUnchanged(t *testing.T) {
	t.Parallel()
	issues := append(genIssues("PROJ-B", "10201", "Backlog", 40), genIssues("PROJ-D", "10202", "Done", 3)...)
	_, dr := stocked(t, twoColumnBoard(), issues, 40, 10) // rowsHeight = 10 - 3 = 7

	before := dr.view()
	mustContain(t, before, "PROJ-D1", "PROJ-D2", "PROJ-D3", "PROJ-B1")

	dr.m.moveTo(0, 39) // the last card in the long column
	if dr.m.rowTopAt(0) == 0 {
		t.Fatal("setup: the long column did not scroll")
	}
	if got := dr.m.rowTopAt(1); got != 0 {
		t.Errorf("the short column's own offset moved to %d after only the long column scrolled", got)
	}

	after := dr.view()
	mustContain(t, after, "PROJ-D1", "PROJ-D2", "PROJ-D3", "PROJ-B40")
	mustNotContain(t, after, "PROJ-B1")
}

// A column keeps its own offset while another column has the keyboard, and
// gets it back exactly as it was left once the cursor returns to a row still
// inside that offset's own window.
func TestBoardScroll_SwitchingColumnsPreservesEachOnesOwnOffset(t *testing.T) {
	t.Parallel()
	issues := append(genIssues("PROJ-B", "10201", "Backlog", 40), genIssues("PROJ-D", "10202", "Done", 20)...)
	_, dr := stocked(t, twoColumnBoard(), issues, 40, 10) // a seven-row window

	dr.m.moveTo(0, 15)
	col0Offset := dr.m.rowTopAt(0)
	if col0Offset == 0 {
		t.Fatal("setup: column 0 did not scroll")
	}

	dr.m.moveTo(1, 15) // switch focus; the row still fits both columns unclamped
	if got := dr.m.rowTopAt(0); got != col0Offset {
		t.Fatalf("switching to another column moved column 0's own offset to %d, want %d", got, col0Offset)
	}
	dr.m.moveTo(1, 19) // scroll column 1 on its own
	col1Offset := dr.m.rowTopAt(1)
	if col1Offset == 0 {
		t.Fatal("setup: column 1 did not scroll")
	}
	dr.m.moveTo(1, 15) // navigate back up before switching away, the way a person would

	dr.m.moveTo(0, 15) // switch back to the long column
	if got := dr.m.rowTopAt(0); got != col0Offset {
		t.Errorf("column 0's own scroll position changed to %d after switching back to it, want %d", got, col0Offset)
	}
	if got := dr.m.rowTopAt(1); got != col1Offset {
		t.Errorf("switching away from column 1 changed its own offset to %d, want %d kept", got, col1Offset)
	}
}

// A filter, a move, or anything else that shrinks a column through place must
// clamp a stale offset rather than draw blank rows above the real cards that
// are left, or index past the end of the column.
func TestBoardScroll_PlaceClampsAColumnsOffsetWhenItShrinks(t *testing.T) {
	t.Parallel()
	backlog := genIssues("PROJ-B", "10201", "Backlog", 40)
	backlog[0].Labels = []string{"kept"}
	backlog[1].Labels = []string{"kept"}
	done := genIssues("PROJ-D", "10202", "Done", 3)
	for i := range done {
		done[i].Labels = []string{"kept"}
	}
	_, dr := stocked(t, twoColumnBoard(), append(backlog, done...), 40, 10) // a seven-row window

	dr.m.moveTo(0, 39)
	if dr.m.rowTopAt(0) == 0 {
		t.Fatal("setup: the long column did not scroll")
	}

	dr.send(filter.ChosenMsg{Term: filter.Term{Facet: filter.FacetLabel, ID: "kept", Label: "kept"}})

	if got := dr.m.columnLen(0); got != 2 {
		t.Fatalf("setup: column 0 holds %d cards after the filter, want 2", got)
	}
	if got := dr.m.rowTopAt(0); got != 0 {
		t.Errorf("column 0's offset is %d once it shrank to fewer cards than the window, want 0", got)
	}
	if got := dr.m.columnLen(1); got != 3 {
		t.Errorf("column 1 lost cards the filter should have left alone: holds %d, want 3", got)
	}

	frame := dr.view()
	mustContain(t, frame, "PROJ-B1", "PROJ-B2")
}

// A two-column board scrolled so that the columns' visible windows genuinely
// differ is direct proof of the fix: the long column shows its tail while the
// short one still shows its own top, in the same frame.
func TestBoardRender_IndependentColumnScrollGolden(t *testing.T) {
	t.Parallel()
	issues := append(genIssues("PROJ-B", "10201", "Backlog", 12), genIssues("PROJ-D", "10202", "Done", 3)...)
	_, dr := stocked(t, twoColumnBoard(), issues, 60, 12) // rowsHeight = 12 - 3 = 9

	dr.m.moveTo(0, 11) // the last card in the long column
	golden(t, "board_mixed_scroll_60x12.golden", dr.view())
}
