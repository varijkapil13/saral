package comment

import (
	"testing"

	"github.com/varijkapil13/saral/pkg/adf"
	"github.com/varijkapil13/saral/pkg/jira"
)

func TestVisibilityLabel_SaysWhoCanReadARestrictedComment(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name string
		in   *jira.Visibility
		want string
	}{
		{name: "nothing restricting it", in: nil, want: ""},
		{name: "a role", in: &jira.Visibility{Type: "role", Value: "Developers"}, want: "only the Developers role"},
		{name: "a group", in: &jira.Visibility{Type: "group", Value: "jira-developers"}, want: "only the jira-developers group"},
		{name: "a restriction with no name", in: &jira.Visibility{Type: "role"}, want: "only one role"},
		{name: "a name with no kind", in: &jira.Visibility{Value: "Developers"}, want: "only Developers"},
		{name: "neither", in: &jira.Visibility{}, want: "restricted"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			if got := visibilityLabel(tc.in); got != tc.want {
				t.Errorf("got %q, want %q", got, tc.want)
			}
		})
	}
}

func TestOneWay_NamesOnlyTheConstructsTheDocumentActuallyHolds(t *testing.T) {
	t.Parallel()

	plain := adf.NewDoc(adf.NewNode("paragraph", adf.NewText("nothing special here")))
	if got := oneWay(plain); len(got) != 0 {
		t.Errorf("a paragraph of prose was reported as losing %v", got)
	}

	rich := adf.NewDoc(
		adf.NewNode("paragraph",
			adf.NewNode("mention").WithAttrs(adf.Attrs{"id": "acct-1", "text": "@Someone"}),
			adf.NewNode("status").WithAttrs(adf.Attrs{"text": "DONE", "color": "green"}),
		),
		adf.NewNode("table"),
	)
	got := oneWay(rich)
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

func TestList_ReadsAsASentence(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		in   []string
		want string
	}{
		{in: nil, want: ""},
		{in: []string{"mention"}, want: "mention"},
		{in: []string{"mention", "table"}, want: "mention and table"},
		{in: []string{"mention", "status", "table"}, want: "mention, status and table"},
	} {
		if got := list(tc.in); got != tc.want {
			t.Errorf("list(%v) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

func TestFirstWords_TakesTheOpeningLineAndNoMore(t *testing.T) {
	t.Parallel()

	body := adf.NewDoc(
		adf.NewNode("paragraph", adf.NewText("The first line of it.")),
		adf.NewNode("paragraph", adf.NewText("The second, which nobody needs in a confirmation.")),
	)
	if got := firstWords(body, 40); got != "The first line of it." {
		t.Errorf("got %q", got)
	}
	if got := firstWords(body, 10); got != "The first…" {
		t.Errorf("a narrow confirmation got %q", got)
	}
	if got := firstWords(adf.Doc{}, 40); got != "" {
		t.Errorf("an empty document got %q", got)
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
