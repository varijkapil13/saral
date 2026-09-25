package app

import (
	"errors"
	"slices"
	"testing"
	"time"

	"github.com/varijkapil13/saral/pkg/adf"
	"github.com/varijkapil13/saral/pkg/jira"
)

func TestReadIssue_ReadsThroughTheIssueEndpointWithTheProjection(t *testing.T) {
	t.Parallel()
	f := testFake(3)
	s := NewSearch(f)
	iss, labels, err := s.ReadIssue(t.Context(), f, "PROJ-2", DetailProjection())
	if err != nil {
		t.Fatal(err)
	}
	if iss.Key != "PROJ-2" || !iss.Requested.Has("summary") || !iss.Requested.Has("description") {
		t.Fatalf("read %s with mask %v", iss.Key, iss.Requested.IDs())
	}
	if callsTo(f, "Search") != 0 || callsTo(f, "IssueFields") != 1 {
		t.Errorf("calls %v, want one IssueFields and no Search", f.Calls())
	}
	if labels.Len() == 0 {
		t.Error("the custom fields' names did not come back with the read")
	}
}

func TestReadIssue_AnIssueTheSiteLacksIsNotFound(t *testing.T) {
	t.Parallel()
	f := testFake(1)
	_, _, err := NewSearch(f).ReadIssue(t.Context(), f, "PROJ-99", DetailProjection())
	var nf *jira.NotFoundError
	if !errors.As(err, &nf) {
		t.Fatalf("err = %v, want a NotFoundError", err)
	}
}

func TestFingerprint_ChangesOnlyWithTheValue(t *testing.T) {
	t.Parallel()
	base := jira.Issue{
		Summary: "a", Description: adf.NewDoc(adf.NewNode("paragraph", adf.NewText("x"))),
		Priority: &jira.Priority{ID: "1"}, Assignee: &jira.User{AccountID: "u1"}, Labels: []string{"b", "a"},
	}
	same := base
	same.Labels = []string{"a", "b"}
	same.Updated = time.Now()
	for _, id := range []string{"summary", "description", "priority", "assignee", "labels", "duedate"} {
		if Fingerprint(base, id) != Fingerprint(same, id) {
			t.Errorf("%s: an unchanged value fingerprints differently", id)
		}
	}
	for _, tc := range []struct {
		id     string
		change func(*jira.Issue)
	}{
		{"summary", func(i *jira.Issue) { i.Summary = "b" }},
		{"description", func(i *jira.Issue) { i.Description = adf.NewDoc(adf.NewNode("paragraph", adf.NewText("y"))) }},
		{"priority", func(i *jira.Issue) { i.Priority = &jira.Priority{ID: "2"} }},
		{"assignee", func(i *jira.Issue) { i.Assignee = nil }},
		{"labels", func(i *jira.Issue) { i.Labels = []string{"a"} }},
	} {
		moved := base
		tc.change(&moved)
		if Fingerprint(base, tc.id) == Fingerprint(moved, tc.id) {
			t.Errorf("%s: a changed value fingerprints the same", tc.id)
		}
	}
}

func TestCheckBase(t *testing.T) {
	t.Parallel()

	t.Run("nothing moved", func(t *testing.T) {
		t.Parallel()
		f := testFake(2)
		iss, _ := f.Issue(t.Context(), "PROJ-1")
		if err := CheckBase(t.Context(), f, "PROJ-1", BaseOf(iss, "summary", "description")); err != nil {
			t.Fatal(err)
		}
	})
	t.Run("a written field moved", func(t *testing.T) {
		t.Parallel()
		f := testFake(2)
		iss, _ := f.Issue(t.Context(), "PROJ-1")
		base := BaseOf(iss, "summary")
		other := "theirs"
		if err := f.UpdateIssue(t.Context(), "PROJ-1", jira.IssuePatch{Summary: &other}); err != nil {
			t.Fatal(err)
		}
		err := CheckBase(t.Context(), f, "PROJ-1", base)
		var conflict *jira.ConflictError
		if !errors.As(err, &conflict) || conflict.Resource != "PROJ-1" {
			t.Fatalf("err = %v, want a ConflictError on PROJ-1", err)
		}
	})
	t.Run("a field it does not write moved", func(t *testing.T) {
		t.Parallel()
		f := testFake(2)
		iss, _ := f.Issue(t.Context(), "PROJ-1")
		base := BaseOf(iss, "description")
		other := "theirs"
		if err := f.UpdateIssue(t.Context(), "PROJ-1", jira.IssuePatch{Summary: &other}); err != nil {
			t.Fatal(err)
		}
		if err := CheckBase(t.Context(), f, "PROJ-1", base); err != nil {
			t.Fatalf("a change to a field nobody is writing conflicted: %v", err)
		}
	})
	t.Run("an empty base asks nothing", func(t *testing.T) {
		t.Parallel()
		f := testFake(2)
		if err := CheckBase(t.Context(), f, "PROJ-1", EditBase{}); err != nil {
			t.Fatal(err)
		}
		if len(f.Calls()) != 0 {
			t.Errorf("calls %v, want none", f.Calls())
		}
	})
}

func TestSaveIssue_WritesOnlyOverWhatItWasMadeAgainst(t *testing.T) {
	t.Parallel()
	mine := "mine"
	for _, tc := range []struct {
		name  string
		setup func(*testing.T, interface{ FailNext(error) })
		fails bool
	}{
		{name: "clean", setup: func(*testing.T, interface{ FailNext(error) }) {}},
		{name: "forbidden", fails: true, setup: func(_ *testing.T, f interface{ FailNext(error) }) {
			f.FailNext(&jira.CapabilityError{Reason: "no Browse projects"})
		}},
		{name: "rate limited", fails: true, setup: func(_ *testing.T, f interface{ FailNext(error) }) {
			f.FailNext(&jira.RateLimitError{RetryAfter: time.Second})
		}},
		{name: "transport", fails: true, setup: func(_ *testing.T, f interface{ FailNext(error) }) {
			f.FailNext(&jira.TransportError{Op: "issue", Err: errors.New("reset")})
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			f := testFake(2)
			iss, _ := f.Issue(t.Context(), "PROJ-1")
			tc.setup(t, f)
			err := SaveIssue(t.Context(), f, "PROJ-1", BaseOf(iss, "summary"), jira.IssuePatch{Summary: &mine})
			if (err != nil) != tc.fails {
				t.Fatalf("err = %v, want failure %v", err, tc.fails)
			}
			wrote := slices.Contains(f.Calls(), "UpdateIssue")
			if wrote == tc.fails {
				t.Errorf("UpdateIssue called = %v with err %v", wrote, err)
			}
		})
	}
	t.Run("conflict", func(t *testing.T) {
		t.Parallel()
		f := testFake(2)
		iss, _ := f.Issue(t.Context(), "PROJ-1")
		other := "theirs"
		_ = f.UpdateIssue(t.Context(), "PROJ-1", jira.IssuePatch{Summary: &other})
		err := SaveIssue(t.Context(), f, "PROJ-1", BaseOf(iss, "summary"), jira.IssuePatch{Summary: &mine})
		var conflict *jira.ConflictError
		if !errors.As(err, &conflict) || callsTo(f, "UpdateIssue") != 1 {
			t.Fatalf("err = %v, calls %v; want a conflict and no write", err, f.Calls())
		}
	})
}
