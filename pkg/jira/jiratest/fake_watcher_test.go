package jiratest_test

import (
	"context"
	"errors"
	"slices"
	"testing"

	"github.com/varijkapil13/saral/pkg/jira"
	"github.com/varijkapil13/saral/pkg/jira/jiratest"
)

func fakeWithWatchableIssue(opts ...jiratest.Option) *jiratest.Fake {
	return jiratest.New(append([]jiratest.Option{
		jiratest.WithProject("PROJ", jiratest.Scrum),
		jiratest.WithIssues(jiratest.Gen(1)),
	}, opts...)...)
}

func TestFake_Watchers_AnUnwatchedIssueIsAWellFormedEmptyList(t *testing.T) {
	t.Parallel()

	f := fakeWithWatchableIssue()
	got, err := f.Watchers(t.Context(), "PROJ-1")
	if err != nil {
		t.Fatalf("Watchers: %v", err)
	}
	if got.Count != 0 || len(got.People) != 0 || got.Watching {
		t.Errorf("an issue nobody watches came back as %+v", got)
	}
}

func TestFake_Watchers_AnUnknownIssueIsNotFound(t *testing.T) {
	t.Parallel()

	f := fakeWithWatchableIssue()
	_, err := f.Watchers(t.Context(), "PROJ-999")
	var missing *jira.NotFoundError
	if !errors.As(err, &missing) {
		t.Fatalf("got %T (%v), want a *jira.NotFoundError", err, err)
	}
	if missing.Kind != "issue" || missing.ID != "PROJ-999" {
		t.Errorf("the failure names %s %s, want issue PROJ-999", missing.Kind, missing.ID)
	}
}

func TestFake_Watch_WithNoAccountUsesTheAuthenticatedOne(t *testing.T) {
	t.Parallel()

	f := fakeWithWatchableIssue()
	if err := f.Watch(t.Context(), "PROJ-1", ""); err != nil {
		t.Fatalf("watching as self: %v", err)
	}
	got, err := f.Watchers(t.Context(), "PROJ-1")
	if err != nil {
		t.Fatalf("Watchers: %v", err)
	}
	if got.Count != 1 || len(got.People) != 1 {
		t.Fatalf("got %+v, want exactly one watcher", got)
	}
	if got.People[0].AccountID != "acct-me" {
		t.Errorf("the watcher self-watching added is %+v, want the authenticated account", got.People[0])
	}
	if !got.Watching {
		t.Error("the authenticated account just watched, and Watching came back false")
	}
}

func TestFake_Watch_IsIdempotent(t *testing.T) {
	t.Parallel()

	f := fakeWithWatchableIssue()
	if err := f.Watch(t.Context(), "PROJ-1", "acct-ada"); err != nil {
		t.Fatalf("watching the first time: %v", err)
	}
	if err := f.Watch(t.Context(), "PROJ-1", "acct-ada"); err != nil {
		t.Fatalf("watching the same account again: %v", err)
	}
	got, err := f.Watchers(t.Context(), "PROJ-1")
	if err != nil {
		t.Fatalf("Watchers: %v", err)
	}
	if got.Count != 1 {
		t.Errorf("watching the same account twice left a count of %d, want 1", got.Count)
	}
}

func TestFake_Watch_AnUnknownAccountIsNotFound(t *testing.T) {
	t.Parallel()

	f := fakeWithWatchableIssue()
	err := f.Watch(t.Context(), "PROJ-1", "acct-nobody")
	var missing *jira.NotFoundError
	if !errors.As(err, &missing) {
		t.Fatalf("got %T (%v), want a *jira.NotFoundError", err, err)
	}
	if missing.Kind != "user" || missing.ID != "acct-nobody" {
		t.Errorf("the failure names %s %s, want user acct-nobody", missing.Kind, missing.ID)
	}
}

func TestFake_Watch_AnUnknownIssueIsNotFound(t *testing.T) {
	t.Parallel()

	f := fakeWithWatchableIssue()
	err := f.Watch(t.Context(), "PROJ-999", "")
	var missing *jira.NotFoundError
	if !errors.As(err, &missing) {
		t.Fatalf("got %T (%v), want a *jira.NotFoundError", err, err)
	}
	if missing.Kind != "issue" {
		t.Errorf("the failure names %q, want issue", missing.Kind)
	}
}

func TestFake_Unwatch_RemovesTheAccount(t *testing.T) {
	t.Parallel()

	f := fakeWithWatchableIssue()
	if err := f.Watch(t.Context(), "PROJ-1", "acct-ada"); err != nil {
		t.Fatalf("watching: %v", err)
	}
	if err := f.Watch(t.Context(), "PROJ-1", "acct-grace"); err != nil {
		t.Fatalf("watching: %v", err)
	}
	if err := f.Unwatch(t.Context(), "PROJ-1", "acct-ada"); err != nil {
		t.Fatalf("unwatching: %v", err)
	}
	got, err := f.Watchers(t.Context(), "PROJ-1")
	if err != nil {
		t.Fatalf("Watchers: %v", err)
	}
	if got.Count != 1 {
		t.Fatalf("got %d watchers after removing one of two, want 1", got.Count)
	}
	ids := make([]string, 0, len(got.People))
	for _, u := range got.People {
		ids = append(ids, u.AccountID)
	}
	if slices.Contains(ids, "acct-ada") {
		t.Errorf("acct-ada is still watching: %v", ids)
	}
	if !slices.Contains(ids, "acct-grace") {
		t.Errorf("acct-grace was removed too: %v", ids)
	}
}

func TestFake_Unwatch_ARemovalThatWasNeverWatchingIsNotAnError(t *testing.T) {
	t.Parallel()

	f := fakeWithWatchableIssue()
	if err := f.Unwatch(t.Context(), "PROJ-1", "acct-ada"); err != nil {
		t.Fatalf("removing an account that was never watching: %v", err)
	}
}

func TestFake_Unwatch_RequiresAnAccount(t *testing.T) {
	t.Parallel()

	f := fakeWithWatchableIssue()
	err := f.Unwatch(t.Context(), "PROJ-1", "  ")
	var invalid *jira.ValidationError
	if !errors.As(err, &invalid) {
		t.Fatalf("got %T (%v), want a *jira.ValidationError", err, err)
	}
	if _, ok := invalid.For("accountId"); !ok {
		t.Errorf("the failure does not name accountId: %v", invalid)
	}
}

func TestFake_Watcher_FailNextIsReturnedFromWhicheverMethodIsCalledNext(t *testing.T) {
	t.Parallel()

	f := fakeWithWatchableIssue()
	want := &jira.RateLimitError{RetryAfter: 30}
	f.FailNext(want)

	_, err := f.Watchers(t.Context(), "PROJ-1")
	var got *jira.RateLimitError
	if !errors.As(err, &got) || got != want {
		t.Fatalf("got %T (%v), want the exact error queued: %v", err, err, want)
	}
	if _, err := f.Watchers(t.Context(), "PROJ-1"); err != nil {
		t.Errorf("a call after the queued failure was consumed also failed: %v", err)
	}
}

func TestFake_Watcher_ACancelledContextReturnsTheCallersOwnError(t *testing.T) {
	t.Parallel()

	f := fakeWithWatchableIssue()
	ctx, cancel := context.WithCancel(t.Context())
	cancel()

	if _, err := f.Watchers(ctx, "PROJ-1"); !errors.Is(err, context.Canceled) {
		t.Errorf("Watchers: got %v, want the context's own error", err)
	}
	if err := f.Watch(ctx, "PROJ-1", ""); !errors.Is(err, context.Canceled) {
		t.Errorf("Watch: got %v, want the context's own error", err)
	}
	if err := f.Unwatch(ctx, "PROJ-1", "acct-ada"); !errors.Is(err, context.Canceled) {
		t.Errorf("Unwatch: got %v, want the context's own error", err)
	}
}
