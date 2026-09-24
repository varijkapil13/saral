package jiratest_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/varijkapil13/saral/pkg/adf"
	"github.com/varijkapil13/saral/pkg/jira"
	"github.com/varijkapil13/saral/pkg/jira/jiratest"
)

func worklogIssue() []jira.Issue {
	return []jira.Issue{{
		ID: "1", Key: "EX-1", Project: jira.ProjectRef{Key: "EX"}, Summary: "Something to log time against",
		Description:  adf.NewDoc(adf.NewNode("paragraph", adf.NewText("The whole story."))),
		Labels:       []string{"already-here"},
		TimeTracking: &jira.TimeTracking{OriginalEstimate: 3600, RemainingEstimate: 3600},
	}}
}

func fakeWorklogInput(spent time.Duration) jira.WorklogInput {
	return jira.WorklogInput{Spent: spent, Started: time.Date(2026, time.March, 1, 9, 0, 0, 0, time.UTC)}
}

func TestFake_Worklogs_PagesByTheConfiguredPageSize(t *testing.T) {
	t.Parallel()

	f := jiratest.New(jiratest.WithIssues(worklogIssue()), jiratest.WithPageSize(1))
	for i := range 3 {
		if _, err := f.AddWorklog(t.Context(), "EX-1", fakeWorklogInput(time.Duration(i+1)*time.Minute)); err != nil {
			t.Fatalf("seeding worklog %d: %v", i, err)
		}
	}

	page, err := f.Worklogs(t.Context(), "EX-1")
	if err != nil {
		t.Fatalf("Worklogs: %v", err)
	}
	if len(page.Items) != 1 {
		t.Fatalf("got %d items on the first page, want 1 at a page size of 1", len(page.Items))
	}
	if !page.HasMore() {
		t.Fatal("three worklogs at a page size of one reports no more")
	}
	got, err := jira.Collect(t.Context(), page, 0)
	if err != nil {
		t.Fatalf("walking the pages: %v", err)
	}
	if len(got) != 3 {
		t.Fatalf("got %d worklogs across the walk, want 3", len(got))
	}
	if got[0].Spent != time.Minute || got[2].Spent != 3*time.Minute {
		t.Errorf("the walk lost its order: %+v", got)
	}
}

func TestFake_AddWorklog_AdjustsTimeTrackingTheWayTheEndpointDoes(t *testing.T) {
	t.Parallel()

	f := jiratest.New(jiratest.WithIssues(worklogIssue()))
	if _, err := f.AddWorklog(t.Context(), "EX-1", fakeWorklogInput(20*time.Minute)); err != nil {
		t.Fatalf("AddWorklog: %v", err)
	}

	iss, err := f.Issue(t.Context(), "EX-1")
	if err != nil {
		t.Fatalf("Issue: %v", err)
	}
	if iss.TimeTracking == nil {
		t.Fatal("the issue lost its time tracking")
	}
	if want := int64(20 * 60); iss.TimeTracking.TimeSpent != want {
		t.Errorf("TimeSpent = %d, want %d", iss.TimeTracking.TimeSpent, want)
	}
	if want := int64(3600 - 20*60); iss.TimeTracking.RemainingEstimate != want {
		t.Errorf("RemainingEstimate = %d, want %d", iss.TimeTracking.RemainingEstimate, want)
	}
}

func TestFake_AddWorklog_NeverTakesTheRemainingEstimateBelowZero(t *testing.T) {
	t.Parallel()

	f := jiratest.New(jiratest.WithIssues(worklogIssue()))
	if _, err := f.AddWorklog(t.Context(), "EX-1", fakeWorklogInput(2*time.Hour)); err != nil {
		t.Fatalf("AddWorklog: %v", err)
	}

	iss, err := f.Issue(t.Context(), "EX-1")
	if err != nil {
		t.Fatalf("Issue: %v", err)
	}
	if iss.TimeTracking.RemainingEstimate != 0 {
		t.Errorf("RemainingEstimate = %d, want 0 rather than negative", iss.TimeTracking.RemainingEstimate)
	}
}

func TestFake_AddWorklog_LogsAsTheAuthenticatedAccount(t *testing.T) {
	t.Parallel()

	me := jira.User{AccountID: "acct-someone-else", DisplayName: "Someone Else", Active: true}
	f := jiratest.New(jiratest.WithIssues(worklogIssue()), jiratest.WithMe(me))

	got, err := f.AddWorklog(t.Context(), "EX-1", fakeWorklogInput(5*time.Minute))
	if err != nil {
		t.Fatalf("AddWorklog: %v", err)
	}
	if got.Author.AccountID != me.AccountID {
		t.Errorf("Author = %+v, want the authenticated account %+v", got.Author, me)
	}
}

func TestFake_Worklog_MethodsHonourFailNext(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		run  func(*jiratest.Fake) error
	}{
		{name: "Worklogs", run: func(f *jiratest.Fake) error {
			_, err := f.Worklogs(context.Background(), "EX-1")
			return err
		}},
		{name: "AddWorklog", run: func(f *jiratest.Fake) error {
			_, err := f.AddWorklog(context.Background(), "EX-1", fakeWorklogInput(time.Minute))
			return err
		}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			f := jiratest.New(jiratest.WithIssues(worklogIssue()))
			f.FailNext(&jira.CapabilityError{Reason: "work logging is disabled on this site"})

			err := tc.run(f)
			var refused *jira.CapabilityError
			if !errors.As(err, &refused) {
				t.Fatalf("got %T (%v), want a *jira.CapabilityError", err, err)
			}
		})
	}
}

func TestFake_Worklog_MethodsReturnTheCallersOwnErrorWhenTheContextIsAlreadyDone(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		run  func(context.Context, *jiratest.Fake) error
	}{
		{name: "Worklogs", run: func(ctx context.Context, f *jiratest.Fake) error {
			_, err := f.Worklogs(ctx, "EX-1")
			return err
		}},
		{name: "AddWorklog", run: func(ctx context.Context, f *jiratest.Fake) error {
			_, err := f.AddWorklog(ctx, "EX-1", fakeWorklogInput(time.Minute))
			return err
		}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			f := jiratest.New(jiratest.WithIssues(worklogIssue()))
			ctx, cancel := context.WithCancel(context.Background())
			cancel()

			if err := tc.run(ctx, f); !errors.Is(err, context.Canceled) {
				t.Fatalf("got %v, want the context's own error", err)
			}
		})
	}
}

func TestFake_IssueFields_MasksTheAnswerAndLeavesTheStoredIssueAlone(t *testing.T) {
	t.Parallel()

	f := jiratest.New(jiratest.WithIssues(worklogIssue()))

	got, err := f.IssueFields(t.Context(), "EX-1", []string{"summary"})
	if err != nil {
		t.Fatalf("IssueFields: %v", err)
	}
	if !got.Description.IsZero() {
		t.Error("a field nobody asked for reached the caller")
	}
	if got.Summary == "" {
		t.Error("the field asked for did not reach the caller")
	}

	got.Labels = append(got.Labels, "caller-added")

	whole, err := f.Issue(t.Context(), "EX-1")
	if err != nil {
		t.Fatalf("Issue: %v", err)
	}
	if whole.Description.IsZero() {
		t.Error("the stored issue lost its description because a narrow read was masked")
	}
	if len(whole.Labels) != 1 || whole.Labels[0] != "already-here" {
		t.Errorf("Labels = %v, want the caller's append to have never reached the store", whole.Labels)
	}
}

func TestFake_ServerInfo_DefaultsToCloud(t *testing.T) {
	t.Parallel()

	got, err := jiratest.New().ServerInfo(t.Context())
	if err != nil {
		t.Fatalf("ServerInfo: %v", err)
	}
	if !got.Cloud() {
		t.Errorf("Cloud() is false for the default fake: %+v", got)
	}
	if got.BaseURL == "" || got.Version == "" || got.ServerTitle == "" {
		t.Errorf("the default server info leaves fields unset: %+v", got)
	}
}

func TestFake_ServerInfo_ReportsWhateverWithServerInfoWasGiven(t *testing.T) {
	t.Parallel()

	want := jira.ServerInfo{
		BaseURL: "https://jira.example.invalid", Version: "9.12.4",
		DeploymentType: "Server", ServerTitle: "On-Prem Jira",
	}
	got, err := jiratest.New(jiratest.WithServerInfo(want)).ServerInfo(t.Context())
	if err != nil {
		t.Fatalf("ServerInfo: %v", err)
	}
	if got != want {
		t.Errorf("got %+v, want %+v", got, want)
	}
	if got.Cloud() {
		t.Error("Cloud() is true for a site whose deploymentType is Server")
	}
}
