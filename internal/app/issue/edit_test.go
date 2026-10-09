package issue

import (
	"errors"
	"testing"

	"github.com/varijkapil13/saral/internal/app/cache/cachetest"
	"github.com/varijkapil13/saral/pkg/jira"
	"github.com/varijkapil13/saral/pkg/jira/jiratest"
)

func readIssue(t *testing.T, f *jiratest.Fake, key string) jira.Issue {
	t.Helper()
	iss, err := f.Issue(t.Context(), key)
	if err != nil {
		t.Fatal(err)
	}
	return iss
}

func TestAssign_ToSomeoneToMeAndToNobody(t *testing.T) {
	t.Parallel()
	f := collabFake()
	ctx := t.Context()
	who, err := Assign(ctx, f, readIssue(t, f, "PROJ-1"), testOther.AccountID, false)
	if err != nil || who.AccountID != testOther.AccountID {
		t.Fatalf("assigned to %+v, %v", who, err)
	}
	who, err = Assign(ctx, f, readIssue(t, f, "PROJ-1"), "", true)
	if err != nil || who.DisplayName != testMe.DisplayName {
		t.Fatalf("assigned to %+v, %v; want this session's own account", who, err)
	}
	if got := readIssue(t, f, "PROJ-1"); got.Assignee == nil || got.Assignee.AccountID != testMe.AccountID {
		t.Errorf("the issue is assigned to %+v", got.Assignee)
	}
	if _, err := Assign(ctx, f, readIssue(t, f, "PROJ-1"), "", false); err != nil {
		t.Fatal(err)
	}
	if got := readIssue(t, f, "PROJ-1"); got.Assignee != nil {
		t.Errorf("the issue is still assigned to %+v", got.Assignee)
	}
}

func TestAssign_OverAChangeMadeSinceItWasReadConflicts(t *testing.T) {
	t.Parallel()
	f := collabFake()
	stale := readIssue(t, f, "PROJ-1")
	if _, err := Assign(t.Context(), f, readIssue(t, f, "PROJ-1"), testOther.AccountID, false); err != nil {
		t.Fatal(err)
	}
	_, err := Assign(t.Context(), f, stale, testMe.AccountID, false)
	var conflict *jira.ConflictError
	if !errors.As(err, &conflict) {
		t.Fatalf("err = %v, want a conflict", err)
	}
}

func TestSetPriorityAndMoveFrom(t *testing.T) {
	t.Parallel()
	f := collabFake()
	ctx := t.Context()
	list, err := f.Priorities(ctx)
	if err != nil || len(list) == 0 {
		t.Fatalf("priorities %v, %v", list, err)
	}
	if err := SetPriority(ctx, f, readIssue(t, f, "PROJ-1"), list[0].ID); err != nil {
		t.Fatal(err)
	}
	if got := readIssue(t, f, "PROJ-1"); got.Priority == nil || got.Priority.ID != list[0].ID {
		t.Errorf("priority is %+v", got.Priority)
	}
	moves, err := Moves(ctx, f, "PROJ-1")
	if err != nil || len(moves) == 0 {
		t.Fatalf("moves %v, %v", moves, err)
	}
	if err := MoveFrom(ctx, f, readIssue(t, f, "PROJ-1"), moves[0].ID); err != nil {
		t.Fatal(err)
	}
	if got := readIssue(t, f, "PROJ-1"); got.Status.ID != moves[0].To.ID {
		t.Errorf("status is %+v, want %+v", got.Status, moves[0].To)
	}
}

func TestEditing_Failures(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	target := jira.Issue{Key: "PROJ-1"}
	for name, call := range map[string]func(*jiratest.Fake) error{
		"assign":   func(f *jiratest.Fake) error { _, err := Assign(ctx, f, target, testOther.AccountID, false); return err },
		"me":       func(f *jiratest.Fake) error { _, err := Assign(ctx, f, target, "", true); return err },
		"priority": func(f *jiratest.Fake) error { return SetPriority(ctx, f, target, "1") },
		"move":     func(f *jiratest.Fake) error { return MoveFrom(ctx, f, target, "11") },
		"moves":    func(f *jiratest.Fake) error { _, err := Moves(ctx, f, "PROJ-1"); return err },
		"screen":   func(f *jiratest.Fake) error { _, err := EditScreen(ctx, f, "PROJ-1"); return err },
		"account":  func(f *jiratest.Fake) error { _, err := Account(ctx, f); return err },
		"fields":   func(f *jiratest.Fake) error { _, err := ReadFields(ctx, f, "PROJ-1", []string{"summary"}); return err },
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			failsWith(t, call)
		})
	}
}

func TestMove_ChecksBeforeItTransitions(t *testing.T) {
	t.Parallel()
	f := collabFake()
	stale := readIssue(t, f, "PROJ-1")
	moves, _ := Moves(t.Context(), f, "PROJ-1")
	if err := MoveFrom(t.Context(), f, stale, moves[0].ID); err != nil {
		t.Fatal(err)
	}
	before := callsTo(f, "Transition")
	err := MoveFrom(t.Context(), f, stale, moves[0].ID)
	var conflict *jira.ConflictError
	if !errors.As(err, &conflict) || callsTo(f, "Transition") != before {
		t.Errorf("err = %v after %d transitions; want a conflict and no second transition", err, callsTo(f, "Transition")-before)
	}
}

func TestFromCacheAndKeep(t *testing.T) {
	t.Parallel()
	held := cachetest.Open(t)
	seed := jira.Issue{Key: "PROJ-1", Summary: "seeded"}
	if got, ok := FromCache(held, seed); ok || got.Summary != "seeded" {
		t.Errorf("an empty cache answered %+v, %v", got, ok)
	}
	full := jira.Issue{Key: "PROJ-1", Summary: "read", Requested: jira.NewFieldMask([]string{"summary", "description"})}
	if err := Keep(held, full); err != nil {
		t.Fatal(err)
	}
	if got, ok := FromCache(held, jira.Issue{Key: "PROJ-1"}); !ok || got.Key != "PROJ-1" {
		t.Errorf("the kept issue came back as %+v, %v", got, ok)
	}
}
