package adf_test

import (
	"strings"
	"testing"

	"github.com/varijkapil13/saral/pkg/adf"
)

func TestMarkdown_EscapesWhatTheParserWouldReadAsMarkup(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name, text, want string
		opt              adf.Options
	}{
		{name: "stars", text: "a *b* c", want: `a \*b\* c`},
		{name: "a star between spaces", text: "5 * 3", want: "5 * 3"},
		{name: "strike", text: "~~x~~", want: `\~\~x\~\~`},
		{name: "one tilde inside a word", text: "about ~5 min", want: "about ~5 min"},
		{name: "backticks", text: "run " + bt + "rm" + bt, want: `run \` + bt + `rm\` + bt},
		{name: "underscores around a word", text: "_x_", want: `\_x\_`},
		{name: "an underscore inside a word", text: "snake_case", want: "snake_case"},
		{name: "a link", text: "[link](http://evil)", want: `\[link\](http://evil)`},
		{name: "an image", text: "![a](media:1)", want: `!\[a\](media:1)`},
		{name: "an autolink", text: "<https://x.test/>", want: `\<https://x.test/>`},
		{name: "a comparison", text: "a < b", want: "a < b"},
		{name: "a backslash before punctuation", text: `C:\*`, want: `C:\\\*`},
		{name: "a backslash before a letter", text: `C:\path`, want: `C:\path`},
		{name: "a trailing backslash", text: `end\`, want: `end\\`},
		{name: "a mention spelled out", text: "@[x](accountid:y)", want: `@\[x\](accountid:y)`},
		{name: "a bracket with no partner", text: "array[0", want: "array[0"},
		{name: "a closing bracket with no partner", text: "0]", want: `0\]`},
		{name: "a lozenge's spelling", text: "[Done]", want: "[Done]"},
		{name: "an unsupported marker", text: "[unsupported: x]", want: `\[unsupported: x\]`},
		{name: "a bullet", text: "- not a list", want: `\- not a list`},
		{name: "a plus bullet", text: "+ not a list", want: `\+ not a list`},
		{name: "a hyphenated word", text: "-5 degrees", want: "-5 degrees"},
		{name: "a rule", text: "---", want: `\---`},
		{name: "a heading", text: "## not a heading", want: `\## not a heading`},
		{name: "a hashtag", text: "#hashtag", want: "#hashtag"},
		{name: "an ordered item", text: "1. not a list", want: `1\. not a list`},
		{name: "a decimal", text: "3.14 is pi", want: "3.14 is pi"},
		{name: "a quote", text: "> not a quote", want: `\> not a quote`},
		{name: "a table row", text: "| not a table", want: `\| not a table`},
		{name: "a checkbox", text: "[ ] not a task", want: `\[ \] not a task`},
		{name: "a fence", text: "~~~", want: `\~\~\~`},
		{name: "an expand", text: "▾ not an expand", want: `\▾ not an expand`},
		{name: "an ASCII expand", text: "v not an expand", want: `\v not an expand`, opt: adf.Options{ASCII: true}},
		{name: "a v in Unicode mode", text: "v not an expand", want: "v not an expand"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			d := adf.NewDoc(adf.NewNode("paragraph", adf.NewText(tc.text)))
			md := adf.MarkdownWith(d, tc.opt)
			if md != tc.want {
				t.Errorf("rendered\n got %q\nwant %q", md, tc.want)
			}
			back, err := adf.ParseMarkdownWith(md, tc.opt)
			if err != nil {
				t.Fatal(err)
			}
			if got := plainText(t, back); got != tc.text {
				t.Errorf("read back as %s, want the text %q", encoded(t, back), tc.text)
			}
		})
	}
}

func plainText(tb testing.TB, d adf.Doc) string {
	tb.Helper()
	if len(d.Content) != 1 || d.Content[0].Type != "paragraph" {
		tb.Fatalf("want one paragraph, got %s", encoded(tb, d))
	}
	var b strings.Builder
	for i := range d.Content[0].Content {
		n := &d.Content[0].Content[i]
		if n.Type != "text" || len(n.Marks) > 0 {
			tb.Fatalf("want unmarked text, got %s", encoded(tb, d))
		}
		b.WriteString(n.Text)
	}
	return b.String()
}

func TestParseMarkdownInto_KeepsLiteralMarkupLiteralAcrossAnEdit(t *testing.T) {
	t.Parallel()
	literal := "use *args, ~~x~~, " + bt + "tick" + bt + ", _x_ and [link](http://evil.test) as written"
	d := adf.NewDoc(adf.NewNode("paragraph", adf.NewText(literal)))
	md := adf.Markdown(d)
	edited := md + " today"

	out, err := adf.ParseMarkdownInto(d, edited, adf.Options{})
	if err != nil {
		t.Fatal(err)
	}
	if got := plainText(t, out); got != literal+" today" {
		t.Errorf("\n got %q\nwant %q", got, literal+" today")
	}
}

func TestParseMarkdown_ReadsAnEscapeTheRendererWouldNotHaveWritten(t *testing.T) {
	t.Parallel()
	d, err := adf.ParseMarkdown(`Step 1\. is **bold** \(really\)`)
	if err != nil {
		t.Fatal(err)
	}
	want := adf.NewDoc(adf.NewNode("paragraph",
		adf.NewText("Step 1. is "), adf.NewText("bold", adf.NewMark("strong", nil)), adf.NewText(" (really)")))
	if got, want := encoded(t, d), encoded(t, want); got != want {
		t.Errorf("\n got %s\nwant %s", got, want)
	}
}

func TestMarkdown_EscapesABangOrAnAtSignThatALinkWouldJoin(t *testing.T) {
	t.Parallel()
	link := adf.NewMark("link", adf.Attrs{"href": "accountid:x"})
	for _, tc := range []struct{ name, before, want string }{
		{name: "a bang", before: "Look!", want: `Look\![here](accountid:x) now`},
		{name: "an at sign", before: "@", want: `\@[here](accountid:x) now`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			d := adf.NewDoc(adf.NewNode("paragraph",
				adf.NewText(tc.before), adf.NewText("here", link), adf.NewText(" now")))
			md := adf.Markdown(d)
			if md != tc.want {
				t.Fatalf("\n got %q\nwant %q", md, tc.want)
			}
			back, err := adf.ParseMarkdown(md)
			if err != nil {
				t.Fatal(err)
			}
			if got, want := encoded(t, back), encoded(t, d); got != want {
				t.Errorf("\n got %s\nwant %s", got, want)
			}
		})
	}
}

func TestMarkdown_KeepsAPipeAndTheBackslashesBeforeItInACell(t *testing.T) {
	t.Parallel()
	for _, content := range []adf.Node{
		adf.NewText("a|b"),
		adf.NewText(`a\|b`),
		adf.NewText(`a\\|b`),
		adf.NewText(`trailing\`),
		adf.NewText(`a\|b`, adf.NewMark("code", nil)),
		adf.NewText(`x \\`, adf.NewMark("code", nil)),
		adf.NewText("| at the start"),
	} {
		d := adf.NewDoc(adf.NewNode("table", adf.NewNode("tableRow",
			adf.NewNode("tableCell", adf.NewNode("paragraph", content)),
			adf.NewNode("tableCell", adf.NewNode("paragraph", adf.NewText("next"))))))
		md := adf.Markdown(d)
		back, err := adf.ParseMarkdown(md)
		if err != nil {
			t.Fatal(err)
		}
		if got, want := encoded(t, back), encoded(t, d); got != want {
			t.Errorf("%q\n got %s\nwant %s", md, got, want)
		}
	}
}

func TestParseMarkdown_KeepsAMentionsAccountID(t *testing.T) {
	t.Parallel()
	mention := func(id, text string) adf.Node {
		return adf.NewNode("mention").WithAttrs(adf.Attrs{"id": id, "text": text})
	}
	for name, d := range map[string]adf.Doc{
		"in a paragraph": adf.NewDoc(adf.NewNode("paragraph",
			adf.NewText("Ask "), mention("5b10ac8d", "@Someone"), adf.NewText(" about it"))),
		"in a list item": adf.NewDoc(adf.NewNode("bulletList", adf.NewNode("listItem",
			adf.NewNode("paragraph", mention("5b10ac8d", "@Someone"))))),
		"in a table cell": adf.NewDoc(adf.NewNode("table", adf.NewNode("tableRow",
			adf.NewNode("tableCell", adf.NewNode("paragraph", mention("5b10ac8d", "@Some | one")))))),
		"in a task": adf.NewDoc(adf.NewNode("taskList", adf.NewNode("taskItem",
			mention("5b10ac8d", "@Someone")).WithAttrs(adf.Attrs{"state": "TODO"}))),
		"with an id Jira writes with a colon": adf.NewDoc(adf.NewNode("paragraph",
			mention("557058:f58131cb-b67d-43c7-b30d-6b58d40bd077", "@Someone"))),
		"with markup in the name": adf.NewDoc(adf.NewNode("paragraph",
			mention("5b10ac8d", "@*Some* [one]"))),
		"two side by side": adf.NewDoc(adf.NewNode("paragraph",
			mention("a1", "@One"), mention("b2", "@Two"))),
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			md := adf.Markdown(d)
			back, err := adf.ParseMarkdown(md)
			if err != nil {
				t.Fatal(err)
			}
			if got, want := encoded(t, back), encoded(t, d); got != want {
				t.Errorf("%q\n got %s\nwant %s", md, got, want)
			}
		})
	}
}

func TestMentionMarkdown_ParsesToTheMentionItSpells(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct{ name, id, want string }{
		{name: "Someone", id: "5b10ac8d", want: "@[Someone](accountid:5b10ac8d)"},
		{name: "@Someone", id: "5b10ac8d", want: "@[Someone](accountid:5b10ac8d)"},
		{name: "", id: "5b10ac8d", want: "@[5b10ac8d](accountid:5b10ac8d)"},
		{name: "A [b] c", id: "x(y)", want: "@[A [b] c](<accountid:x(y)>)"},
	} {
		md := adf.MentionMarkdown(tc.name, tc.id)
		if md != tc.want {
			t.Errorf("%q, %q: got %q, want %q", tc.name, tc.id, md, tc.want)
		}
		d, err := adf.ParseMarkdown("Ask " + md + " now")
		if err != nil {
			t.Fatal(err)
		}
		got := d.Content[0].Content
		if len(got) != 3 || got[1].Type != "mention" || got[1].Attrs["id"] != tc.id {
			t.Errorf("%q did not read back as a mention of %q: %s", md, tc.id, encoded(t, d))
		}
	}
}
