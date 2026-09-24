package adf_test

import (
	"strings"
	"testing"

	"github.com/varijkapil13/saral/pkg/adf"
)

// A mention as the Jira editor stores it: the accessLevel and localId are what
// markdown does not carry even now that it carries the account id.
const storedMention = `{"type":"mention","attrs":{"id":"5b10ac8d","text":"@Someone","accessLevel":"","localId":"m-1"}}`

// edit renders d, applies one textual replacement that must hit, and parses
// the result back into d.
func edit(tb testing.TB, d adf.Doc, from, to string) adf.Doc {
	tb.Helper()
	md := adf.Markdown(d)
	edited := strings.Replace(md, from, to, 1)
	if edited == md {
		tb.Fatalf("%q is not in the markdown, so nothing was edited:\n%s", from, md)
	}
	out, err := adf.ParseMarkdownInto(d, edited, adf.Options{})
	if err != nil {
		tb.Fatal(err)
	}
	return out
}

// TestParseMarkdownInto_RestoresTheUntouchedPartsOfAnEditedBlock is the case
// the top-level matching could not reach: one list item, one cell or one
// paragraph of a panel is edited, the block around it no longer matches, and
// everything else inside it used to be re-parsed from markdown — mentions lost
// their localId, cells their background and span.
func TestParseMarkdownInto_RestoresTheUntouchedPartsOfAnEditedBlock(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name     string
		in       string
		from, to string
		kept     []string // subtrees that must come back byte for byte
		want     string
	}{
		{
			name: "a mention in the list item beside the one edited",
			in: wrap(node("bulletList", `"content":[`+
				item("first")+`,`+
				node("listItem", `"attrs":{"localId":"li-2"},"content":[`+para(text("ask ")+`,`+storedMention)+`]`)+`]`)),
			from: "first", to: "first, edited",
			kept: []string{node("listItem", `"attrs":{"localId":"li-2"},"content":[`+para(text("ask ")+`,`+storedMention)+`]`)},
			want: "first, edited",
		},
		{
			name: "a mention in a nested list below the item edited",
			in: wrap(node("bulletList", `"content":[`+node("listItem", `"content":[`+para(text("parent"))+`,`+
				node("bulletList", `"content":[`+node("listItem", `"content":[`+para(storedMention)+`]`)+`]`)+`]`)+`]`)),
			from: "parent", to: "parent, edited",
			kept: []string{node("bulletList", `"content":[`+node("listItem", `"content":[`+para(storedMention)+`]`)+`]`)},
			want: "parent, edited",
		},
		{
			name: "the cells beside the one edited, with their background and span",
			in: wrap(node("table", `"attrs":{"layout":"wide","localId":"t-1"},"content":[`+
				row(node("tableCell", `"attrs":{"background":"#deebff","colspan":2},"content":[`+para(text("merged"))+`]`),
					cell("tableCell", "edit me"))+`,`+
				row(cell("tableCell", "one"), node("tableCell", `"attrs":{"background":"#ffebe6"},"content":[`+para(storedMention)+`]`),
					cell("tableCell", "three"))+`]`)),
			from: "edit me", to: "edited",
			kept: []string{
				node("tableCell", `"attrs":{"background":"#deebff","colspan":2},"content":[`+para(text("merged"))+`]`),
				row(cell("tableCell", "one"), node("tableCell", `"attrs":{"background":"#ffebe6"},"content":[`+para(storedMention)+`]`),
					cell("tableCell", "three")),
				`"layout":"wide","localId":"t-1"`,
			},
			want: "edited",
		},
		{
			name: "a span in the same row as the cell edited",
			in: wrap(node("table", `"content":[`+
				row(node("tableCell", `"attrs":{"colspan":2},"content":[`+para(text("wide"))+`]`), cell("tableCell", "narrow"))+`]`)),
			from: "narrow", to: "slim",
			kept: []string{node("tableCell", `"attrs":{"colspan":2},"content":[`+para(text("wide"))+`]`)},
			want: "slim",
		},
		{
			name: "the attributes of the cell edited",
			in: wrap(node("table", `"content":[`+
				row(node("tableHeader", `"attrs":{"background":"#deebff","colwidth":[120]},"content":[`+para(text("head"))+`]`),
					cell("tableCell", "plain"))+`]`)),
			from: "head", to: "header",
			kept: []string{`"attrs":{"background":"#deebff","colwidth":[120]}`, `"type":"tableHeader"`},
			want: "header",
		},
		{
			name: "a ragged row beside the row edited",
			in: wrap(node("table", `"content":[`+
				row(cell("tableCell", "a"), cell("tableCell", "b"))+`,`+
				row(node("tableCell", `"attrs":{"localId":"short"},"content":[`+para(text("short"))+`]`))+`]`)),
			from: "| a ", to: "| A ",
			kept: []string{row(node("tableCell", `"attrs":{"localId":"short"},"content":[`+para(text("short"))+`]`))},
			want: "A",
		},
		{
			name: "a panel's colour and the paragraph beside the one edited",
			in: wrap(node("panel", `"attrs":{"panelType":"custom","panelColor":"#ABF5D1","panelIcon":":tada:"},"content":[`+
				para(text("edit me"))+`,`+para(storedMention)+`]`)),
			from: "edit me", to: "edited",
			kept: []string{`"panelColor":"#ABF5D1","panelIcon":":tada:"`, para(storedMention)},
			want: "edited",
		},
		{
			name: "a panelType the renderer upper-cased",
			in:   wrap(node("panel", `"attrs":{"panelType":"Surprise"},"content":[`+para(text("edit me"))+`]`)),
			from: "edit me", to: "edited",
			kept: []string{`"panelType":"Surprise"`},
			want: "edited",
		},
		{
			name: "the paragraph beside the one edited in a quote",
			in:   wrap(node("blockquote", `"content":[`+para(text("edit me"))+`,`+para(storedMention)+`]`)),
			from: "edit me", to: "edited",
			kept: []string{para(storedMention)},
			want: "edited",
		},
		{
			name: "a code block in the item beside the one edited",
			in: wrap(node("bulletList", `"content":[`+item("edit me")+`,`+
				node("listItem", `"content":[`+node("codeBlock", `"attrs":{"language":"go","uniqueId":"c-1"},"content":[`+text("x := 1")+`]`)+`]`)+`]`)),
			from: "edit me", to: "edited",
			kept: []string{node("codeBlock", `"attrs":{"language":"go","uniqueId":"c-1"},"content":[`+text("x := 1")+`]`)},
			want: "edited",
		},
		{
			name: "the localId of a task that was ticked",
			in: wrap(node("taskList", `"attrs":{"localId":"tl"},"content":[`+
				node("taskItem", `"attrs":{"localId":"t-1","state":"TODO"},"content":[`+text("ship")+`]`)+`,`+
				node("taskItem", `"attrs":{"localId":"t-2","state":"TODO"},"content":[`+storedMention+`]`)+`]`)),
			from: "- [ ] ship", to: "- [x] ship",
			kept: []string{`"localId":"t-1","state":"DONE"`, node("taskItem", `"attrs":{"localId":"t-2","state":"TODO"},"content":[`+storedMention+`]`), `"localId":"tl"`},
			want: "- [x] ship",
		},
		{
			name: "the state of a decision the renderer does not draw",
			in: wrap(node("decisionList", `"content":[`+
				node("decisionItem", `"attrs":{"localId":"d-1","state":"UNDECIDED"},"content":[`+text("maybe")+`]`)+`]`)),
			from: "maybe", to: "perhaps",
			kept: []string{`"localId":"d-1","state":"UNDECIDED"`},
			want: "perhaps",
		},
		{
			name: "the items around one inserted between them",
			in: wrap(node("bulletList", `"content":[`+
				node("listItem", `"attrs":{"localId":"a"},"content":[`+para(text("a"))+`]`)+`,`+
				node("listItem", `"attrs":{"localId":"c"},"content":[`+para(storedMention)+`]`)+`]`)),
			from: "- a\n", to: "- a\n- b\n",
			kept: []string{
				node("listItem", `"attrs":{"localId":"a"},"content":[`+para(text("a"))+`]`),
				node("listItem", `"attrs":{"localId":"c"},"content":[`+para(storedMention)+`]`),
			},
			want: "- b",
		},
		{
			name: "the items after one deleted",
			in: wrap(node("orderedList", `"attrs":{"order":1},"content":[`+
				item("gone")+`,`+node("listItem", `"attrs":{"localId":"stay"},"content":[`+para(storedMention)+`]`)+`]`)),
			from: "1. gone\n2. ", to: "1. ",
			kept: []string{node("listItem", `"attrs":{"localId":"stay"},"content":[`+para(storedMention)+`]`)},
			want: "1. @[Someone]",
		},
		{
			name: "the alignment of a paragraph that was edited",
			in:   wrap(node("paragraph", `"marks":[{"type":"alignment","attrs":{"align":"center"}}],"content":[`+text("edit me")+`]`)),
			from: "edit me", to: "edited",
			kept: []string{`"marks":[{"type":"alignment","attrs":{"align":"center"}}]`},
			want: "edited",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			out := edit(t, parse(t, tc.in), tc.from, tc.to)
			got := encoded(t, out)
			for _, want := range tc.kept {
				if !strings.Contains(got, want) {
					t.Errorf("lost\n%s\nfrom\n%s", want, got)
				}
			}
			if md := adf.Markdown(out); !strings.Contains(md, tc.want) {
				t.Errorf("the edit did not survive: %q is not in\n%s", tc.want, md)
			}
		})
	}
}

func TestParseMarkdownInto_TakesWhatMarkdownSpellsFromTheEdit(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name, in, from, to string
		want, gone         []string
	}{
		{
			name: "a list renumbered",
			in:   wrap(node("orderedList", `"attrs":{"order":5,"localId":"ol"},"content":[`+item("five")+`]`)),
			from: "5. five", to: "1. five",
			want: []string{`"localId":"ol"`},
			gone: []string{`"order":5`},
		},
		{
			name: "a heading re-levelled",
			in:   wrap(node("heading", `"attrs":{"level":2,"localId":"h"},"content":[`+text("title")+`]`)),
			from: "## title", to: "### title",
			want: []string{`"level":3`, `"localId":"h"`},
		},
		{
			name: "a code block's language",
			in:   wrap(node("codeBlock", `"attrs":{"language":"go","uniqueId":"c"},"content":[`+text("x")+`]`)),
			from: "```go", to: "```sh",
			want: []string{`"language":"sh"`, `"uniqueId":"c"`},
		},
		{
			name: "a header row the author added",
			in: wrap(node("table", `"content":[`+
				row(cell("tableCell", "a"), cell("tableCell", "b"))+`,`+row(cell("tableCell", "1"), cell("tableCell", "2"))+`]`)),
			from: "| a   | b   |\n", to: "| a   | b   |\n| --- | --- |\n",
			want: []string{`{"type":"tableHeader","content":[{"type":"paragraph","content":[{"type":"text","text":"a"}]}]}`},
		},
		{
			name: "a span the author typed into",
			in: wrap(node("table", `"content":[`+
				row(node("tableCell", `"attrs":{"colspan":2},"content":[`+para(text("wide"))+`]`), cell("tableCell", "x"))+`]`)),
			from: "| wide |     |", to: "| wide | now |",
			want: []string{`"text":"now"`},
			gone: []string{`"colspan":2`},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got := encoded(t, edit(t, parse(t, tc.in), tc.from, tc.to))
			for _, want := range tc.want {
				if !strings.Contains(got, want) {
					t.Errorf("want %s in\n%s", want, got)
				}
			}
			for _, gone := range tc.gone {
				if strings.Contains(got, gone) {
					t.Errorf("%s survived the edit that changed it:\n%s", gone, got)
				}
			}
		})
	}
}

func TestParseMarkdownInto_KeepsAnEmptyParagraphNobodyCouldSee(t *testing.T) {
	t.Parallel()
	in := wrap(node("blockquote", `"content":[`+para(text("edit me"))+`,`+para("")+`,`+para(text("after"))+`]`))
	out := edit(t, parse(t, in), "edit me", "edited")
	if got := encoded(t, out); !strings.Contains(got, `{"type":"paragraph"},`) {
		t.Errorf("the empty paragraph went:\n%s", got)
	}
}
