package jiratest_test

import (
	"context"
	"errors"
	"io"
	"slices"
	"strings"
	"testing"

	"github.com/varijkapil13/saral/pkg/jira"
)

// fakeLazyReader strips away whatever optimisations the concrete reader
// underneath offers — io.Copy takes a single-shot io.WriterTo fast path when
// the source has one, which would defeat a test of progress reported as the
// copy goes.
type fakeLazyReader struct{ io.Reader }

func TestUpdateIssue_AddLabelsDedupesAndMovesTheUpdatedStamp(t *testing.T) {
	t.Parallel()

	c := fakeNewWithIssues(t, 1)
	ctx := t.Context()
	before, err := c.Issue(ctx, "PROJ-1")
	if err != nil {
		t.Fatalf("Issue: %v", err)
	}

	if err := c.UpdateIssue(ctx, "PROJ-1", jira.IssuePatch{Labels: &[]string{"existing"}}); err != nil {
		t.Fatalf("seeding a known label list: %v", err)
	}
	if err := c.UpdateIssue(ctx, "PROJ-1", jira.IssuePatch{AddLabels: []string{"existing", "new"}}); err != nil {
		t.Fatalf("UpdateIssue: %v", err)
	}

	after, err := c.Issue(ctx, "PROJ-1")
	if err != nil {
		t.Fatalf("Issue: %v", err)
	}
	if !slices.Equal(after.Labels, []string{"existing", "new"}) {
		t.Errorf("Labels = %v, want existing kept once and new appended", after.Labels)
	}
	if after.Updated.Equal(before.Updated) {
		t.Error("the Updated stamp did not move after the edit was applied")
	}
}

func TestUpdateIssue_RemoveLabelsDeletesWhatIsThereAndIgnoresWhatIsNot(t *testing.T) {
	t.Parallel()

	c := fakeNewWithIssues(t, 1)
	ctx := t.Context()
	if err := c.UpdateIssue(ctx, "PROJ-1", jira.IssuePatch{Labels: &[]string{"a", "b"}}); err != nil {
		t.Fatalf("seeding: %v", err)
	}
	if err := c.UpdateIssue(ctx, "PROJ-1", jira.IssuePatch{RemoveLabels: []string{"a", "c"}}); err != nil {
		t.Fatalf("UpdateIssue: %v", err)
	}
	iss, err := c.Issue(ctx, "PROJ-1")
	if err != nil {
		t.Fatalf("Issue: %v", err)
	}
	if !slices.Equal(iss.Labels, []string{"b"}) {
		t.Errorf("Labels = %v, want only b left: a removed and c was never there to remove", iss.Labels)
	}
}

func TestUpdateIssue_AddsAndRemovesLabelsInOneCall(t *testing.T) {
	t.Parallel()

	c := fakeNewWithIssues(t, 1)
	ctx := t.Context()
	if err := c.UpdateIssue(ctx, "PROJ-1", jira.IssuePatch{Labels: &[]string{"triage"}}); err != nil {
		t.Fatalf("seeding: %v", err)
	}
	patch := jira.IssuePatch{AddLabels: []string{"checkout"}, RemoveLabels: []string{"triage"}}
	if err := c.UpdateIssue(ctx, "PROJ-1", patch); err != nil {
		t.Fatalf("UpdateIssue: %v", err)
	}
	iss, err := c.Issue(ctx, "PROJ-1")
	if err != nil {
		t.Fatalf("Issue: %v", err)
	}
	if !slices.Equal(iss.Labels, []string{"checkout"}) {
		t.Errorf("Labels = %v, want triage removed and checkout added", iss.Labels)
	}
}

func TestUpdateIssue_RefusesConflictingLabelEditsAndAppliesNothing(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name  string
		patch jira.IssuePatch
	}{
		{"add alongside a full replacement", jira.IssuePatch{AddLabels: []string{"checkout"}, Labels: &[]string{"other"}}},
		{"remove alongside clearing labels", jira.IssuePatch{RemoveLabels: []string{"checkout"}, Clear: []jira.FieldRef{{ID: "labels"}}}},
		{"an empty label", jira.IssuePatch{AddLabels: []string{"  "}}},
		{"a label with a space", jira.IssuePatch{AddLabels: []string{"needs review"}}},
		{"the same label twice", jira.IssuePatch{AddLabels: []string{"a", "a"}}},
		{"the same label added and removed", jira.IssuePatch{AddLabels: []string{"a"}, RemoveLabels: []string{"a"}}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			c := fakeNewWithIssues(t, 1)
			ctx := t.Context()
			if err := c.UpdateIssue(ctx, "PROJ-1", jira.IssuePatch{Labels: &[]string{"baseline"}}); err != nil {
				t.Fatalf("seeding: %v", err)
			}
			before, err := c.Issue(ctx, "PROJ-1")
			if err != nil {
				t.Fatalf("Issue: %v", err)
			}

			err = c.UpdateIssue(ctx, "PROJ-1", tc.patch)
			var invalid *jira.ValidationError
			if !errors.As(err, &invalid) {
				t.Fatalf("got %T (%v), want a *jira.ValidationError", err, err)
			}

			after, err := c.Issue(ctx, "PROJ-1")
			if err != nil {
				t.Fatalf("Issue: %v", err)
			}
			if !slices.Equal(after.Labels, before.Labels) {
				t.Errorf("Labels changed from %v to %v on a patch the fake refused", before.Labels, after.Labels)
			}
			if !after.Updated.Equal(before.Updated) {
				t.Error("the Updated stamp moved on a patch the fake refused")
			}
		})
	}
}

func TestUpload_ReportsProgressCumulativelyAsTheCopyGoes(t *testing.T) {
	t.Parallel()

	c := fakeNewWithIssues(t, 1)
	body := strings.Repeat("saral upload ", 8000) // past io.Copy's 32KiB buffer
	var progress []int64
	file := jira.FileRef{
		Name: "trace.log", Size: int64(len(body)),
		Open:     func() (io.ReadCloser, error) { return io.NopCloser(fakeLazyReader{strings.NewReader(body)}), nil },
		Progress: func(sent int64) { progress = append(progress, sent) },
	}
	if _, err := c.Upload(t.Context(), "PROJ-1", []jira.FileRef{file}); err != nil {
		t.Fatalf("Upload: %v", err)
	}
	if len(progress) < 2 {
		t.Fatalf("progress was reported %d times for %d bytes, want it as the copy goes", len(progress), len(body))
	}
	for i := 1; i < len(progress); i++ {
		if progress[i] <= progress[i-1] {
			t.Fatalf("progress must be cumulative and increasing, got %v", progress)
		}
	}
	if last := progress[len(progress)-1]; last != int64(len(body)) {
		t.Errorf("the last progress call reported %d, want the whole %d", last, len(body))
	}
}

func TestUpload_CancellingMidReadReturnsTheContextsOwnError(t *testing.T) {
	t.Parallel()

	c := fakeNewWithIssues(t, 1)
	body := strings.Repeat("saral upload ", 8000)
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()

	first := true
	file := jira.FileRef{
		Name: "trace.log", Size: int64(len(body)),
		Open: func() (io.ReadCloser, error) { return io.NopCloser(fakeLazyReader{strings.NewReader(body)}), nil },
		Progress: func(int64) {
			if first {
				first = false
				cancel()
			}
		},
	}
	_, err := c.Upload(ctx, "PROJ-1", []jira.FileRef{file})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("got %v, want the context's own error", err)
	}
}

func TestUpload_RefusesAFileThatReadsShorterThanItsDeclaredSize(t *testing.T) {
	t.Parallel()

	c := fakeNewWithIssues(t, 1)
	file := jira.FileRef{
		Name: "notes.txt", Size: 20,
		Open: func() (io.ReadCloser, error) { return io.NopCloser(strings.NewReader("too short")), nil },
	}
	_, err := c.Upload(t.Context(), "PROJ-1", []jira.FileRef{file})
	var invalid *jira.ValidationError
	if !errors.As(err, &invalid) {
		t.Fatalf("got %T (%v), want a *jira.ValidationError", err, err)
	}
	if !strings.Contains(invalid.Error(), "changed while it was being sent") {
		t.Errorf("the refusal does not explain why: %q", invalid.Error())
	}
}
