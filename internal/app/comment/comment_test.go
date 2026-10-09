package comment

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/varijkapil13/saral/pkg/adf"
	"github.com/varijkapil13/saral/pkg/jira"
	"github.com/varijkapil13/saral/pkg/jira/jiratest"
)

func newFake(opts ...jiratest.Option) *jiratest.Fake {
	return jiratest.New(append([]jiratest.Option{
		jiratest.WithProject("PROJ", jiratest.Scrum),
		jiratest.WithIssues(jiratest.Gen(3)),
	}, opts...)...)
}

func doc(text string) adf.Doc {
	return adf.NewDoc(adf.NewNode("paragraph", adf.NewText(text)))
}

func failures() []struct {
	name string
	err  error
} {
	return []struct {
		name string
		err  error
	}{
		{name: "refused", err: &jira.CapabilityError{Reason: "you may not touch this thread"}},
		{name: "rate limited", err: &jira.RateLimitError{RetryAfter: 30 * time.Second}},
		{name: "transport", err: &jira.TransportError{Op: "GET /comment", Err: errors.New("connection reset")}},
	}
}

func TestThread_AddEditDeleteRoundTrip(t *testing.T) {
	t.Parallel()

	f := newFake()
	ctx := t.Context()

	added, err := Add(ctx, f, "PROJ-1", doc("first"))
	if err != nil {
		t.Fatalf("Add: %v", err)
	}
	edited, err := Edit(ctx, f, "PROJ-1", added.ID, doc("second"))
	if err != nil {
		t.Fatalf("Edit: %v", err)
	}
	if edited.ID != added.ID || adf.Markdown(edited.Body) != "second" {
		t.Errorf("the edit came back as %+v", edited)
	}
	page, err := Load(ctx, f, "PROJ-1")
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if len(page.Items) != 1 || page.Items[0].ID != added.ID {
		t.Errorf("the thread holds %+v", page.Items)
	}
	if err := Delete(ctx, f, "PROJ-1", added.ID); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	page, err = Load(ctx, f, "PROJ-1")
	if err != nil || len(page.Items) != 0 {
		t.Errorf("after deleting the thread holds %+v, %v", page.Items, err)
	}
}

func TestMore_ReadsThePageAfterTheOneInHand(t *testing.T) {
	t.Parallel()

	f := newFake(jiratest.WithPageSize(2))
	for _, text := range []string{"one", "two", "three"} {
		if _, err := f.AddComment(t.Context(), "PROJ-1", doc(text)); err != nil {
			t.Fatal(err)
		}
	}
	first, err := Load(t.Context(), f, "PROJ-1")
	if err != nil || len(first.Items) != 2 || !first.HasMore() {
		t.Fatalf("the first page is %d items, more %v, %v", len(first.Items), first.HasMore(), err)
	}
	second, err := More(t.Context(), first)
	if err != nil || len(second.Items) != 1 || second.HasMore() {
		t.Errorf("the second page is %d items, more %v, %v", len(second.Items), second.HasMore(), err)
	}
}

func TestThread_EveryCallPassesTheSitesErrorThroughWhole(t *testing.T) {
	t.Parallel()

	calls := map[string]func(context.Context, *jiratest.Fake, string) error{
		"load": func(ctx context.Context, f *jiratest.Fake, _ string) error {
			_, err := Load(ctx, f, "PROJ-1")
			return err
		},
		"add": func(ctx context.Context, f *jiratest.Fake, _ string) error {
			_, err := Add(ctx, f, "PROJ-1", doc("x"))
			return err
		},
		"edit": func(ctx context.Context, f *jiratest.Fake, id string) error {
			_, err := Edit(ctx, f, "PROJ-1", id, doc("x"))
			return err
		},
		"delete": func(ctx context.Context, f *jiratest.Fake, id string) error {
			return Delete(ctx, f, "PROJ-1", id)
		},
		"people": func(ctx context.Context, f *jiratest.Fake, _ string) error {
			_, err := People(ctx, f, "sam", "PROJ")
			return err
		},
	}
	for name, call := range calls {
		for _, tc := range failures() {
			t.Run(name+"/"+tc.name, func(t *testing.T) {
				t.Parallel()

				f := newFake()
				c, err := f.AddComment(t.Context(), "PROJ-1", doc("there"))
				if err != nil {
					t.Fatal(err)
				}
				f.FailNext(tc.err)
				if got := call(t.Context(), f, c.ID); !errors.Is(got, tc.err) || got.Error() != tc.err.Error() {
					t.Errorf("got %v, want %v unwrapped", got, tc.err)
				}
			})
		}
	}
}

func TestMore_PassesAFailedPageThrough(t *testing.T) {
	t.Parallel()

	for _, tc := range failures() {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			f := newFake(jiratest.WithPageSize(1))
			for _, text := range []string{"one", "two"} {
				if _, err := f.AddComment(t.Context(), "PROJ-1", doc(text)); err != nil {
					t.Fatal(err)
				}
			}
			first, err := Load(t.Context(), f, "PROJ-1")
			if err != nil {
				t.Fatal(err)
			}
			f.FailNext(tc.err)
			if _, got := More(t.Context(), first); !errors.Is(got, tc.err) {
				t.Errorf("got %v, want %v", got, tc.err)
			}
		})
	}
}

func TestPeople_AsksForAFewAccountsInTheProject(t *testing.T) {
	t.Parallel()

	sam := jira.User{AccountID: "acc-sam", DisplayName: "Sam Tester", Active: true, Kind: jira.AccountPerson}
	f := newFake(jiratest.WithPeople([]jira.User{sam}))
	got, err := People(t.Context(), f, "sam", "PROJ")
	if err != nil {
		t.Fatalf("People: %v", err)
	}
	if len(got) != 1 || got[0].AccountID != sam.AccountID {
		t.Errorf("got %+v, want Sam", got)
	}
}

func TestRevise_KeepsWhatTheAuthorDidNotTouch(t *testing.T) {
	t.Parallel()

	original := adf.NewDoc(
		adf.NewNode("paragraph", adf.NewNode("status").WithAttrs(adf.Attrs{"text": "DONE", "color": "green"})),
		adf.NewNode("paragraph", adf.NewText("change me")),
	)
	text := Markdown(original)
	got, err := Revise(original, text)
	if err != nil {
		t.Fatalf("Revise: %v", err)
	}
	if Fingerprint(got) != Fingerprint(original) {
		t.Errorf("an untouched edit changed the document: %s", Markdown(got))
	}
	fresh, err := Compose("a new comment")
	if err != nil || adf.Markdown(fresh) != "a new comment" {
		t.Errorf("Compose gave %q, %v", adf.Markdown(fresh), err)
	}
}

func TestFingerprint_ChangesWithTheBody(t *testing.T) {
	t.Parallel()

	if Fingerprint(doc("a")) == Fingerprint(doc("b")) {
		t.Error("two bodies share a fingerprint")
	}
	if Fingerprint(doc("a")) != Fingerprint(adf.NewDoc(adf.NewNode("paragraph", adf.NewText("a")))) {
		t.Error("one body has two fingerprints")
	}
}

func TestOneWay_NamesOnlyTheConstructsTheDocumentActuallyHolds(t *testing.T) {
	t.Parallel()

	plain := adf.NewDoc(adf.NewNode("paragraph", adf.NewText("nothing special here")))
	if got := OneWay(plain); len(got) != 0 {
		t.Errorf("a paragraph of prose was reported as losing %v", got)
	}

	rich := adf.NewDoc(
		adf.NewNode("paragraph",
			adf.NewNode("mention").WithAttrs(adf.Attrs{"text": "@Someone"}),
			adf.NewNode("status").WithAttrs(adf.Attrs{"text": "DONE", "color": "green"}),
			adf.NewNode("status").WithAttrs(adf.Attrs{"text": "OPEN", "color": "blue"}),
		),
		adf.NewNode("table", adf.NewNode("tableRow", adf.NewNode("tableCell",
			adf.NewNode("paragraph", adf.NewText("one")), adf.NewNode("paragraph", adf.NewText("two"))))),
	)
	got := OneWay(rich)
	want := map[string]bool{"mention": false, "status": false, "table": false}
	for _, name := range got {
		if _, ours := want[name]; !ours {
			t.Errorf("%q is not in this document", name)
		}
		want[name] = true
	}
	for name, found := range want {
		if !found {
			t.Errorf("%q is in the document and was not named", name)
		}
	}
	if len(got) != len(want) {
		t.Errorf("got %v, which names something twice", got)
	}
}

// The editor is seeded and read back with one value, and it must be the one
// that does not truncate: a bounded render puts an ellipsis inside a table cell
// and an edit anywhere in that table would write the truncation back.
func TestEditorOptions_BoundNothingByWidth(t *testing.T) {
	t.Parallel()

	if editorOptions != (adf.Options{}) {
		t.Errorf("the editor renders with %+v, want the zero options", editorOptions)
	}
}

func TestPeople_AsksForNoMoreThanASuggestionListShows(t *testing.T) {
	t.Parallel()

	people := make([]jira.User, 0, 12)
	for i := range 12 {
		people = append(people, jira.User{AccountID: fmt.Sprintf("acc-%d", i), DisplayName: fmt.Sprintf("Sam %d", i), Active: true, Kind: jira.AccountPerson})
	}
	got, err := People(t.Context(), newFake(jiratest.WithPeople(people)), "sam", "PROJ")
	if err != nil || len(got) != peopleLimit {
		t.Errorf("got %d people, %v, want %d", len(got), err, peopleLimit)
	}
}
