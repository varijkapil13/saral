package cloud

import (
	"testing"

	"github.com/varijkapil13/saral/pkg/jira"
)

func TestBoardConfig_ReadsTheColumnConstraintTypeOffColumnConfig(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		body string
		want jira.ColumnConstraint
	}{
		{
			name: "none, which enforces nothing",
			body: `"columnConfig":{"constraintType":"none","columns":[]},"ranking":{}`,
			want: jira.ConstraintNone,
		},
		{
			name: "issueCount, which counts every issue including sub-tasks",
			body: `"columnConfig":{"constraintType":"issueCount","columns":[]},"ranking":{}`,
			want: jira.ConstraintIssueCount,
		},
		{
			name: "issueCountExclSubs, which leaves sub-tasks out of the count",
			body: `"columnConfig":{"constraintType":"issueCountExclSubs","columns":[]},"ranking":{}`,
			want: jira.ConstraintIssueCountExclSubs,
		},
		{
			name: "absent, which Enforced() treats the same as none",
			body: `"columnConfig":{"columns":[]},"ranking":{}`,
			want: jira.ColumnConstraint(""),
		},
		{
			name: "a value nobody has documented, kept verbatim rather than guessed at",
			want: jira.ColumnConstraint("somethingNew"),
			body: `"columnConfig":{"constraintType":"somethingNew","columns":[]},"ranking":{}`,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			c, _ := boardClient(t, boardConfigAnswering(boardConfigWith(tt.body)))
			got, err := c.BoardConfig(t.Context(), boardTestID)
			if err != nil {
				t.Fatalf("reading the configuration: %v", err)
			}
			if got.Constraint != tt.want {
				t.Errorf("Constraint = %q, want %q", got.Constraint, tt.want)
			}
		})
	}
}

func TestColumnConstraint_EnforcedIsTrueOnlyForTheTwoCountingConstraints(t *testing.T) {
	t.Parallel()

	tests := []struct {
		constraint jira.ColumnConstraint
		enforced   bool
	}{
		{jira.ConstraintNone, false},
		{jira.ConstraintIssueCount, true},
		{jira.ConstraintIssueCountExclSubs, true},
		{jira.ColumnConstraint(""), false},
		{jira.ColumnConstraint("somethingNew"), false},
	}

	for _, tt := range tests {
		t.Run(string(tt.constraint), func(t *testing.T) {
			t.Parallel()

			if got := tt.constraint.Enforced(); got != tt.enforced {
				t.Errorf("Enforced() = %v, want %v", got, tt.enforced)
			}
		})
	}
}

func TestColumnConstraint_CountsSubtasksIsTrueOnlyForTheConstraintThatIncludesThem(t *testing.T) {
	t.Parallel()

	tests := []struct {
		constraint jira.ColumnConstraint
		counts     bool
	}{
		{jira.ConstraintNone, false},
		{jira.ConstraintIssueCount, true},
		{jira.ConstraintIssueCountExclSubs, false},
		{jira.ColumnConstraint(""), false},
	}

	for _, tt := range tests {
		t.Run(string(tt.constraint), func(t *testing.T) {
			t.Parallel()

			if got := tt.constraint.CountsSubtasks(); got != tt.counts {
				t.Errorf("CountsSubtasks() = %v, want %v", got, tt.counts)
			}
		})
	}
}
