package jiratest_test

import (
	"context"
	"errors"
	"slices"
	"testing"

	"github.com/varijkapil13/saral/pkg/jira"
	"github.com/varijkapil13/saral/pkg/jira/jiratest"
)

func fakeWithLinkableIssues(opts ...jiratest.Option) *jiratest.Fake {
	return jiratest.New(append([]jiratest.Option{
		jiratest.WithProject("PROJ", jiratest.Scrum),
		jiratest.WithIssues(jiratest.Gen(3)),
	}, opts...)...)
}

func TestFake_IssueLinkTypes_ReturnsTheInventedTypesInOrder(t *testing.T) {
	t.Parallel()

	f := jiratest.New()
	got, err := f.IssueLinkTypes(t.Context())
	if err != nil {
		t.Fatalf("IssueLinkTypes: %v", err)
	}
	if len(got) == 0 {
		t.Fatal("the fake came back with no link types at all")
	}
	want := []jira.LinkType{
		{ID: "20001", Name: "Holds up", Inward: "is held up by", Outward: "holds up"},
		{ID: "20002", Name: "Echoes", Inward: "is echoed by", Outward: "echoes"},
		{ID: "20003", Name: "Leans on", Inward: "is leaned on by", Outward: "leans on"},
	}
	if !slices.Equal(got, want) {
		t.Errorf("got %+v, want %+v", got, want)
	}

	got[0].Name = "corrupted"
	again, err := f.IssueLinkTypes(t.Context())
	if err != nil {
		t.Fatalf("IssueLinkTypes a second time: %v", err)
	}
	if again[0].Name == "corrupted" {
		t.Error("mutating the answer reached back into the fake's own state")
	}
}

func TestFake_LinkIssues_WritesBothEnds(t *testing.T) {
	t.Parallel()

	f := fakeWithLinkableIssues()
	types, err := f.IssueLinkTypes(t.Context())
	if err != nil || len(types) == 0 {
		t.Fatalf("reading link types: %v", err)
	}
	kind := types[0]

	if err := f.LinkIssues(t.Context(), jira.LinkInput{TypeID: kind.ID, From: "PROJ-1", To: "PROJ-2"}); err != nil {
		t.Fatalf("linking PROJ-1 to PROJ-2: %v", err)
	}

	from, err := f.Issue(t.Context(), "PROJ-1")
	if err != nil {
		t.Fatalf("reading PROJ-1 back: %v", err)
	}
	idx := slices.IndexFunc(from.Links, func(l jira.IssueLink) bool { return l.Other.Key == "PROJ-2" })
	if idx < 0 {
		t.Fatalf("PROJ-1 carries no link to PROJ-2: %+v", from.Links)
	}
	if from.Links[idx].Direction != jira.LinkOutward || from.Links[idx].Label != kind.Outward || from.Links[idx].Type != kind.Name {
		t.Errorf("PROJ-1's link reads %+v, want the outward phrase of %+v", from.Links[idx], kind)
	}

	to, err := f.Issue(t.Context(), "PROJ-2")
	if err != nil {
		t.Fatalf("reading PROJ-2 back: %v", err)
	}
	idx = slices.IndexFunc(to.Links, func(l jira.IssueLink) bool { return l.Other.Key == "PROJ-1" })
	if idx < 0 {
		t.Fatalf("PROJ-2 carries no link to PROJ-1: %+v", to.Links)
	}
	if to.Links[idx].Direction != jira.LinkInward || to.Links[idx].Label != kind.Inward || to.Links[idx].Type != kind.Name {
		t.Errorf("PROJ-2's link reads %+v, want the inward phrase of %+v", to.Links[idx], kind)
	}
}

func TestFake_LinkIssues_TheTwoEndsShareOneLinkID(t *testing.T) {
	t.Parallel()

	f := fakeWithLinkableIssues()
	if err := f.LinkIssues(t.Context(), jira.LinkInput{TypeID: "20001", From: "PROJ-1", To: "PROJ-2"}); err != nil {
		t.Fatalf("linking: %v", err)
	}
	from, err := f.Issue(t.Context(), "PROJ-1")
	if err != nil {
		t.Fatalf("reading PROJ-1: %v", err)
	}
	to, err := f.Issue(t.Context(), "PROJ-2")
	if err != nil {
		t.Fatalf("reading PROJ-2: %v", err)
	}
	if len(from.Links) != 1 || len(to.Links) != 1 {
		t.Fatalf("got %d and %d links, want one each", len(from.Links), len(to.Links))
	}
	if from.Links[0].ID == "" || from.Links[0].ID != to.Links[0].ID {
		t.Errorf("the two ends carry ids %q and %q, want the same link id on both", from.Links[0].ID, to.Links[0].ID)
	}
}

func TestFake_LinkIssues_ADuplicateLinkIsANoOp(t *testing.T) {
	t.Parallel()

	f := fakeWithLinkableIssues()
	in := jira.LinkInput{TypeID: "20001", From: "PROJ-1", To: "PROJ-2"}
	if err := f.LinkIssues(t.Context(), in); err != nil {
		t.Fatalf("linking the first time: %v", err)
	}
	if err := f.LinkIssues(t.Context(), in); err != nil {
		t.Fatalf("linking the same pair again: %v", err)
	}
	from, err := f.Issue(t.Context(), "PROJ-1")
	if err != nil {
		t.Fatalf("reading PROJ-1: %v", err)
	}
	if len(from.Links) != 1 {
		t.Errorf("got %d links after linking the same pair twice, want one: %+v", len(from.Links), from.Links)
	}
}

func TestFake_LinkIssues_AnUnknownTypeIsNotFound(t *testing.T) {
	t.Parallel()

	f := fakeWithLinkableIssues()
	err := f.LinkIssues(t.Context(), jira.LinkInput{TypeID: "99999", From: "PROJ-1", To: "PROJ-2"})
	var missing *jira.NotFoundError
	if !errors.As(err, &missing) {
		t.Fatalf("got %T (%v), want a *jira.NotFoundError", err, err)
	}
	if missing.Kind != "issue link type" {
		t.Errorf("the failure names %q, want the link type", missing.Kind)
	}
}

func TestFake_LinkIssues_AnUnknownIssueIsNotFound(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name string
		in   jira.LinkInput
	}{
		{name: "the issue it starts at", in: jira.LinkInput{TypeID: "20001", From: "PROJ-999", To: "PROJ-2"}},
		{name: "the issue it ends at", in: jira.LinkInput{TypeID: "20001", From: "PROJ-1", To: "PROJ-999"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			f := fakeWithLinkableIssues()
			err := f.LinkIssues(t.Context(), tc.in)
			var missing *jira.NotFoundError
			if !errors.As(err, &missing) {
				t.Fatalf("got %T (%v), want a *jira.NotFoundError", err, err)
			}
			if missing.Kind != "issue" || missing.ID != "PROJ-999" {
				t.Errorf("the failure names %s %s, want issue PROJ-999", missing.Kind, missing.ID)
			}
		})
	}
}

func TestFake_LinkIssues_RefusesInvalidInputBeforeTouchingAnything(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name  string
		in    jira.LinkInput
		field string
	}{
		{name: "no type", in: jira.LinkInput{From: "PROJ-1", To: "PROJ-2"}, field: "type"},
		{name: "no from", in: jira.LinkInput{TypeID: "20001", To: "PROJ-2"}, field: "inwardIssue"},
		{name: "no to", in: jira.LinkInput{TypeID: "20001", From: "PROJ-1"}, field: "outwardIssue"},
		{name: "from equals to", in: jira.LinkInput{TypeID: "20001", From: "PROJ-1", To: "PROJ-1"}, field: "outwardIssue"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			f := fakeWithLinkableIssues()
			err := f.LinkIssues(t.Context(), tc.in)
			var invalid *jira.ValidationError
			if !errors.As(err, &invalid) {
				t.Fatalf("got %T (%v), want a *jira.ValidationError", err, err)
			}
			if _, ok := invalid.For(tc.field); !ok {
				t.Errorf("the failure does not name %s: %v", tc.field, invalid)
			}
		})
	}
}

func TestFake_DeleteLink_RemovesFromBothEnds(t *testing.T) {
	t.Parallel()

	f := fakeWithLinkableIssues()
	if err := f.LinkIssues(t.Context(), jira.LinkInput{TypeID: "20001", From: "PROJ-1", To: "PROJ-2"}); err != nil {
		t.Fatalf("linking: %v", err)
	}
	from, err := f.Issue(t.Context(), "PROJ-1")
	if err != nil || len(from.Links) != 1 {
		t.Fatalf("reading PROJ-1 back: %v, %+v", err, from.Links)
	}
	id := from.Links[0].ID

	if err := f.DeleteLink(t.Context(), id); err != nil {
		t.Fatalf("deleting the link: %v", err)
	}

	from, err = f.Issue(t.Context(), "PROJ-1")
	if err != nil {
		t.Fatalf("reading PROJ-1 after the delete: %v", err)
	}
	if len(from.Links) != 0 {
		t.Errorf("PROJ-1 still carries %+v after the link was deleted", from.Links)
	}
	to, err := f.Issue(t.Context(), "PROJ-2")
	if err != nil {
		t.Fatalf("reading PROJ-2 after the delete: %v", err)
	}
	if len(to.Links) != 0 {
		t.Errorf("PROJ-2 still carries %+v after the link was deleted", to.Links)
	}
}

func TestFake_DeleteLink_AnUnknownIDIsNotFound(t *testing.T) {
	t.Parallel()

	f := fakeWithLinkableIssues()
	err := f.DeleteLink(t.Context(), "nope")
	var missing *jira.NotFoundError
	if !errors.As(err, &missing) {
		t.Fatalf("got %T (%v), want a *jira.NotFoundError", err, err)
	}
	if missing.Kind != "issue link" || missing.ID != "nope" {
		t.Errorf("the failure names %s %s, want issue link nope", missing.Kind, missing.ID)
	}
}

func TestFake_DeleteLink_RefusesAnEmptyIDBeforeTouchingAnything(t *testing.T) {
	t.Parallel()

	f := fakeWithLinkableIssues()
	err := f.DeleteLink(t.Context(), "   ")
	var invalid *jira.ValidationError
	if !errors.As(err, &invalid) {
		t.Fatalf("got %T (%v), want a *jira.ValidationError", err, err)
	}
}

func TestFake_Link_FailNextIsReturnedFromWhicheverMethodIsCalledNext(t *testing.T) {
	t.Parallel()

	f := fakeWithLinkableIssues()
	want := &jira.CapabilityError{Reason: "issue linking is switched off"}
	f.FailNext(want)

	_, err := f.IssueLinkTypes(t.Context())
	var got *jira.CapabilityError
	if !errors.As(err, &got) || got != want {
		t.Fatalf("got %T (%v), want the exact error queued: %v", err, err, want)
	}

	if _, err := f.IssueLinkTypes(t.Context()); err != nil {
		t.Errorf("a call after the queued failure was consumed also failed: %v", err)
	}
}

func TestFake_Link_ACancelledContextReturnsTheCallersOwnError(t *testing.T) {
	t.Parallel()

	f := fakeWithLinkableIssues()
	ctx, cancel := context.WithCancel(t.Context())
	cancel()

	if _, err := f.IssueLinkTypes(ctx); !errors.Is(err, context.Canceled) {
		t.Errorf("IssueLinkTypes: got %v, want the context's own error", err)
	}
	if err := f.LinkIssues(ctx, jira.LinkInput{TypeID: "20001", From: "PROJ-1", To: "PROJ-2"}); !errors.Is(err, context.Canceled) {
		t.Errorf("LinkIssues: got %v, want the context's own error", err)
	}
	if err := f.DeleteLink(ctx, "1"); !errors.Is(err, context.Canceled) {
		t.Errorf("DeleteLink: got %v, want the context's own error", err)
	}
}
