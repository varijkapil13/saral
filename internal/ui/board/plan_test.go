package board

import (
	"testing"

	appboard "github.com/varijkapil13/saral/internal/app/board"
	"github.com/varijkapil13/saral/pkg/jira"
)

// A board with no rank field is ordered by whatever its own filter sorted by,
// which is a thing to say out loud rather than a reordering to offer.
func TestPlan_ABoardWithoutARankFieldIsOrderedByItsFilter(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name  string
		rank  string
		words string
	}{
		{name: "a board with a rank field", rank: "customfield_13404", words: "ranked"},
		{name: "a board with none", words: "ordered by its filter"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			p := appboard.NewPlan(jira.BoardConfig{Columns: []jira.Column{{Name: "Waiting", StatusIDs: []string{"1"}}}, RankFieldID: tc.rank})
			if got := orderWords(p); got != tc.words {
				t.Errorf("orderWords = %q, want %q", got, tc.words)
			}
		})
	}
}
