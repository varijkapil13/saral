package issue

import (
	"errors"
	"slices"
	"testing"
	"time"

	"github.com/varijkapil13/saral/pkg/adf"
	"github.com/varijkapil13/saral/pkg/jira"
	"github.com/varijkapil13/saral/pkg/jira/jiratest"
)

func testFake(issues int) *jiratest.Fake {
	return jiratest.New(
		jiratest.WithProject("PROJ", jiratest.Scrum),
		jiratest.WithIssues(jiratest.Gen(issues)),
	)
}

func callsTo(f *jiratest.Fake, method string) int {
	n := 0
	for _, call := range f.Calls() {
		if call == method {
			n++
		}
	}
	return n
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

func TestSave_WritesOnlyOverWhatItWasMadeAgainst(t *testing.T) {
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
			err := Save(t.Context(), f, "PROJ-1", BaseOf(iss, "summary"), jira.IssuePatch{Summary: &mine})
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
		err := Save(t.Context(), f, "PROJ-1", BaseOf(iss, "summary"), jira.IssuePatch{Summary: &mine})
		var conflict *jira.ConflictError
		if !errors.As(err, &conflict) || callsTo(f, "UpdateIssue") != 1 {
			t.Fatalf("err = %v, calls %v; want a conflict and no write", err, f.Calls())
		}
	})
}

func failures() map[string]error {
	return map[string]error{
		"403":       &jira.CapabilityError{Reason: "no Browse projects permission"},
		"429":       &jira.RateLimitError{RetryAfter: 30 * time.Second},
		"transport": &jira.TransportError{Op: "issue", Err: errors.New("connection reset by peer")},
	}
}
