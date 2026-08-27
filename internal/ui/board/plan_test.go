package board

import (
	"slices"
	"testing"

	"github.com/varijkapil13/saral/internal/app"
	"github.com/varijkapil13/saral/pkg/jira"
)

// twoColumns is a configuration whose two columns hold statuses that share a
// display name under different ids, which is what a team-managed project mints
// and what a site in another language hands back for every status it has.
func twoColumns() []jira.Column {
	return []jira.Column{
		{Name: "Waiting", StatusIDs: []string{"10201", "10777"}},
		{Name: "Under way", StatusIDs: []string{"10202", "10204"}},
	}
}

func TestPlan_PutsAnIssueInAColumnByStatusIDAndNeverByName(t *testing.T) {
	t.Parallel()
	p := newPlan(jira.BoardConfig{BoardID: 7, Name: "Ledger", Type: jira.BoardScrum, Columns: twoColumns()})

	for _, tc := range []struct {
		name     string
		statusID string
		want     int
		mapped   bool
	}{
		{name: "the first id of the first column", statusID: "10201", want: 0, mapped: true},
		{name: "the second id of the first column", statusID: "10777", want: 0, mapped: true},
		{name: "an id sharing a display name with another column's", statusID: "10204", want: 1, mapped: true},
		{name: "an id the board maps nowhere", statusID: "10203", mapped: false},
		{name: "a display name is not an id", statusID: "Under way", mapped: false},
		{name: "no status at all", statusID: "", mapped: false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			at, mapped := p.columnOf(tc.statusID)
			if mapped != tc.mapped {
				t.Fatalf("columnOf(%q) mapped = %v, want %v", tc.statusID, mapped, tc.mapped)
			}
			if mapped && at != tc.want {
				t.Errorf("columnOf(%q) = column %d, want %d", tc.statusID, at, tc.want)
			}
		})
	}
}

// A status listed in two columns belongs to the first that claims it. Jira lets
// one be mapped twice and a board draws the issue once, so the second claim is
// dropped rather than the issue being drawn in both.
func TestPlan_AStatusMappedTwiceBelongsToTheFirstColumnThatClaimsIt(t *testing.T) {
	t.Parallel()
	p := newPlan(jira.BoardConfig{Columns: []jira.Column{
		{Name: "Left", StatusIDs: []string{"1", "2"}},
		{Name: "Right", StatusIDs: []string{"2", "3"}},
	}})
	if at, _ := p.columnOf("2"); at != 0 {
		t.Errorf("a status mapped into both columns landed in %d, want the first that claimed it", at)
	}
	if got := p.columns[1].statuses; !slices.Equal(got, []string{"3"}) {
		t.Errorf("the second column kept %v, want only the status no other column claimed", got)
	}
}

// A board that does not estimate at all and a board that has turned estimation
// off are different answers with the same consequence, and neither of them has a
// field. A board that estimates in issue counts has no field either.
func TestPlan_OnlyABoardWithAnEstimationFieldEstimates(t *testing.T) {
	t.Parallel()
	points := jira.FieldRef{ID: "customfield_13401", Name: "Story Points"}

	for _, tc := range []struct {
		name string
		est  *jira.Estimation
		want bool
	}{
		{name: "a board that sent no estimation object", est: nil},
		{name: "a board that turned estimation off", est: &jira.Estimation{Type: jira.EstimationNone}},
		{name: "a board that counts issues", est: &jira.Estimation{Type: jira.EstimationIssueCount}},
		{name: "a board whose estimation field this site does not have", est: &jira.Estimation{Type: jira.EstimationField}},
		{name: "a board that estimates in a field", est: &jira.Estimation{Type: jira.EstimationField, Field: points}, want: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			p := newPlan(jira.BoardConfig{Columns: twoColumns(), Estimation: tc.est})
			if p.estimates != tc.want {
				t.Fatalf("estimates = %v, want %v", p.estimates, tc.want)
			}
			asked := p.projection().IDs
			held := slices.Contains(asked, points.ID)
			if held != tc.want {
				t.Errorf("the projection asks for %v; a board that estimates = %v", asked, tc.want)
			}
			if slices.Contains(asked, jira.FieldsAll) || slices.Contains(asked, jira.FieldsNavigable) {
				t.Errorf("the projection asks for a wildcard: %v", asked)
			}
			if len(asked) <= len(app.ListProjection().IDs) && tc.want {
				t.Errorf("a board that estimates asks for %v, which is no wider than a list row's fields", asked)
			}
		})
	}
}

// A board with no rank field is ordered by whatever its own filter sorted by,
// which is a thing to say out loud rather than a reordering to offer.
func TestPlan_ABoardWithoutARankFieldIsOrderedByItsFilter(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name  string
		rank  string
		order jira.Ordering
		words string
	}{
		{name: "a board with a rank field", rank: "customfield_13404", order: jira.OrderRank, words: "ranked"},
		{name: "a board with none", order: jira.OrderFilter, words: "ordered by its filter"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			p := newPlan(jira.BoardConfig{Columns: twoColumns(), RankFieldID: tc.rank})
			if p.ordering != tc.order {
				t.Errorf("ordering = %v, want %v", p.ordering, tc.order)
			}
			if got := p.orderWords(); got != tc.words {
				t.Errorf("orderWords = %q, want %q", got, tc.words)
			}
		})
	}
}

// A Kanban board's own condition about resolved issues is read off the
// configuration and kept, because the read that fills the board applies the
// board's filter and its columns and leaves this one part to the caller.
func TestPlan_AKanbanBoardsRuleAboutResolvedIssuesIsKept(t *testing.T) {
	t.Parallel()
	const rule = "resolved >= -14d OR resolved is EMPTY"

	kanban := newPlan(jira.BoardConfig{Type: jira.BoardKanban, Columns: twoColumns(), SubQuery: "  " + rule + "  "})
	if kanban.subQuery != rule {
		t.Errorf("the Kanban plan carries the sub-query %q, want %q", kanban.subQuery, rule)
	}
	scrum := newPlan(jira.BoardConfig{Type: jira.BoardScrum, Columns: twoColumns()})
	if scrum.subQuery != "" {
		t.Errorf("the Scrum plan carries the sub-query %q; SubQuery is empty on a Scrum board", scrum.subQuery)
	}
}

// The board the read asks about is the one the configuration answered for, so a
// project with several boards cannot draw one board's cards under another's
// columns.
func TestPlan_IsBuiltForTheBoardItsConfigurationAnswersFor(t *testing.T) {
	t.Parallel()
	p := newPlan(jira.BoardConfig{BoardID: 7, Name: "Ledger", Type: jira.BoardScrum, Columns: twoColumns()})
	if p.boardID != 7 {
		t.Errorf("the plan is for board %d, want 7", p.boardID)
	}
}
