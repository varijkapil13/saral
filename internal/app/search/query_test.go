package search

import (
	"strings"
	"testing"
)

func TestScoped_ComposesTheProjectAndTheClauseWithoutInventingEither(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		project string
		clause  string
		want    string
	}{
		{"a project and a clause", "PROJ", "assignee = currentUser()", `project = "PROJ" AND assignee = currentUser()`},
		{"a project and no clause at all", "PROJ", "", `project = "PROJ"`},
		{"a clause and no project", "", "assignee IS EMPTY", "assignee IS EMPTY"},
		{"neither", "", "", ""},
		{"a key somebody typed a quote into", `PR"OJ`, "", `project = "PROJ"`},
		{"a key with room around it", "  PROJ  ", "", `project = "PROJ"`},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if got := Scoped(tc.project, tc.clause); got != tc.want {
				t.Errorf("Scoped(%q, %q) = %q, want %q", tc.project, tc.clause, got, tc.want)
			}
		})
	}
}

func TestCannedSearch_ComposesTheSameBytesItAlwaysDid(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		search  CannedSearch
		project string
		want    string
	}{
		{"every issue in a project", EveryIssue, "PROJ", `project = "PROJ" ORDER BY updated DESC`},
		{"every issue anywhere", EveryIssue, "", "ORDER BY updated DESC"},
		{"my issues in a project", MyIssues, "PROJ", `project = "PROJ" AND assignee = currentUser() ORDER BY updated DESC`},
		{"issues I reported", ReportedIssues, "PROJ", `project = "PROJ" AND reporter = currentUser() ORDER BY created DESC`},
		{"unassigned issues", UnassignedIssues, "PROJ", `project = "PROJ" AND assignee IS EMPTY ORDER BY created DESC`},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if got := tc.search.At(tc.project); got != tc.want {
				t.Errorf("At(%q) = %q, want %q", tc.project, got, tc.want)
			}
		})
	}
}

func TestProbeJQL_IsTheOpeningSearchWithNoProject(t *testing.T) {
	t.Parallel()

	got := ProbeJQL()
	if got != "assignee = currentUser() ORDER BY updated DESC" {
		t.Errorf("ProbeJQL() = %q", got)
	}
	if strings.Contains(got, "project") {
		t.Errorf("the probe %q is scoped to a project", got)
	}
}

func TestSlotFromKey_TakesOnlyTheDigitsASlotCanBe(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		stroke string
		slot   int
		ok     bool
	}{
		{"1", 1, true},
		{"9", MaxSavedSlot, true},
		{"0", 0, false},
		{"10", 0, false},
		{"-1", 0, false},
		{"a", 0, false},
		{"", 0, false},
	} {
		slot, ok := SlotFromKey(tc.stroke)
		if slot != tc.slot || ok != tc.ok {
			t.Errorf("SlotFromKey(%q) = %d, %v, want %d, %v", tc.stroke, slot, ok, tc.slot, tc.ok)
		}
	}
}
