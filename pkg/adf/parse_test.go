package adf_test

import (
	"errors"
	"strings"
	"testing"

	"github.com/varijkapil13/saral/pkg/adf"
)

// txt spells a text node the way the canonical encoder writes one: type first,
// then marks, then the text itself.
func txt(s string, marks ...string) string {
	if len(marks) == 0 {
		return `{"type":"text","text":"` + s + `"}`
	}
	return `{"type":"text","marks":[` + strings.Join(marks, ",") + `],"text":"` + s + `"}`
}

func mark(typ string) string { return `{"type":"` + typ + `"}` }

func linkMark(href string) string {
	return `{"type":"link","attrs":{"href":"` + href + `"}}`
}

func paraOf(content ...string) string {
	if len(content) == 0 {
		return `{"type":"paragraph"}`
	}
	return `{"type":"paragraph","content":[` + strings.Join(content, ",") + `]}`
}

func TestParseMarkdown_ReadsEveryShapeTheRendererWrites(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name string
		in   string
		want string
	}{
		{
			name: "a paragraph",
			in:   "The basket is empty.",
			want: paraOf(txt("The basket is empty.")),
		},
		{
			name: "a heading",
			in:   "## Steps",
			want: `{"type":"heading","attrs":{"level":2},"content":[` + txt("Steps") + `]}`,
		},
		{
			name: "a hash with no space after it, which is a word and not a heading",
			in:   "#checkout is the label",
			want: paraOf(txt("#checkout is the label")),
		},
		{
			name: "seven hashes, which is deeper than markdown goes",
			in:   "####### too deep",
			want: paraOf(txt("####### too deep")),
		},
		{
			name: "every mark",
			in:   "**bold** *italic* ~~gone~~ _under_ `code()`",
			want: paraOf(
				txt("bold", mark("strong")), txt(" "), txt("italic", mark("em")), txt(" "),
				txt("gone", mark("strike")), txt(" "), txt("under", mark("underline")), txt(" "),
				txt("code()", mark("code"))),
		},
		{
			name: "marks nested the way the renderer nests them",
			in:   "[**the runbook**](https://example.com/x)",
			want: paraOf(txt("the runbook", linkMark("https://example.com/x"), mark("strong"))),
		},
		{
			name: "an underscore inside a word, which is a name and not emphasis",
			in:   "snake_case_name",
			want: paraOf(txt("snake_case_name")),
		},
		{
			name: "a link whose text holds brackets, which the renderer does not escape",
			in:   "[a [b] c](https://example.com/x)",
			want: paraOf(txt("a [b] c", linkMark("https://example.com/x"))),
		},
		{
			name: "an address in angle brackets, which is how one holding a bracket is written",
			in:   "[wiki](<https://example.com/a(b)>)",
			want: paraOf(txt("wiki", linkMark("https://example.com/a(b)"))),
		},
		{
			name: "an address whose angle brackets the renderer had to escape",
			in:   "[q](<https://example.com/s?a=%3Cb%3E c>)",
			want: paraOf(txt("q", linkMark(`https://example.com/s?a=\u003cb\u003e c`))),
		},
		{
			name: "an autolink",
			in:   "<https://example.com/x>",
			want: paraOf(txt("https://example.com/x", linkMark("https://example.com/x"))),
		},
		{
			name: "angle brackets that are not an address",
			in:   "a < b and b > a",
			want: paraOf(txt(`a \u003c b and b \u003e a`)),
		},
		{
			name: "inline code holding a backtick, which is fenced in two",
			in:   "``a ` b``",
			want: paraOf(txt("a ` b", mark("code"))),
		},
		{
			name: "inline code that had to be padded off its own fence",
			in:   "`` ` ``",
			want: paraOf(txt("`", mark("code"))),
		},
		{
			name: "a hard break, which is the only reason prose runs to a second line",
			in:   "first\nsecond",
			want: paraOf(txt("first"), `{"type":"hardBreak"}`, txt("second")),
		},
		{
			name: "a bullet list",
			in:   "- one\n- two",
			want: `{"type":"bulletList","content":[` +
				`{"type":"listItem","content":[` + paraOf(txt("one")) + `]},` +
				`{"type":"listItem","content":[` + paraOf(txt("two")) + `]}]}`,
		},
		{
			name: "an ordered list that does not start at one",
			in:   "9. nine\n10. ten",
			want: `{"type":"orderedList","attrs":{"order":9},"content":[` +
				`{"type":"listItem","content":[` + paraOf(txt("nine")) + `]},` +
				`{"type":"listItem","content":[` + paraOf(txt("ten")) + `]}]}`,
		},
		{
			name: "an ordered list that does start at one, which needs no attribute",
			in:   "1. one",
			want: `{"type":"orderedList","content":[{"type":"listItem","content":[` + paraOf(txt("one")) + `]}]}`,
		},
		{
			name: "a list item with nothing in it",
			in:   "-\n- second",
			want: `{"type":"bulletList","content":[` +
				`{"type":"listItem","content":[` + paraOf() + `]},` +
				`{"type":"listItem","content":[` + paraOf(txt("second")) + `]}]}`,
		},
		{
			name: "a list inside a list",
			in:   "- one\n  - nested",
			want: `{"type":"bulletList","content":[{"type":"listItem","content":[` + paraOf(txt("one")) + `,` +
				`{"type":"bulletList","content":[{"type":"listItem","content":[` + paraOf(txt("nested")) + `]}]}]}]}`,
		},
		{
			name: "a second paragraph under a list item",
			in:   "- two\n\n  still two",
			want: `{"type":"bulletList","content":[{"type":"listItem","content":[` +
				paraOf(txt("two")) + `,` + paraOf(txt("still two")) + `]}]}`,
		},
		{
			name: "a task list",
			in:   "- [ ] todo\n- [x] done",
			want: `{"type":"taskList","content":[` +
				`{"type":"taskItem","attrs":{"state":"TODO"},"content":[` + txt("todo") + `]},` +
				`{"type":"taskItem","attrs":{"state":"DONE"},"content":[` + txt("done") + `]}]}`,
		},
		{
			name: "a task list indented under another one, which ADF stores as a sibling",
			in:   "- [ ] ship\n  - [x] test",
			want: `{"type":"taskList","content":[` +
				`{"type":"taskItem","attrs":{"state":"TODO"},"content":[` + txt("ship") + `]},` +
				`{"type":"taskList","content":[{"type":"taskItem","attrs":{"state":"DONE"},"content":[` + txt("test") + `]}]}]}`,
		},
		{
			name: "a decision list",
			in:   "- ◇ ship it",
			want: `{"type":"decisionList","content":[` +
				`{"type":"decisionItem","attrs":{"state":"DECIDED"},"content":[` + txt("ship it") + `]}]}`,
		},
		{
			name: "a quote",
			in:   "> quoted\n>\n> twice",
			want: `{"type":"blockquote","content":[` + paraOf(txt("quoted")) + `,` + paraOf(txt("twice")) + `]}`,
		},
		{
			name: "a panel",
			in:   "> ⚠ WARNING\n> Do not retry.",
			want: `{"type":"panel","attrs":{"panelType":"warning"},"content":[` + paraOf(txt("Do not retry.")) + `]}`,
		},
		{
			name: "a panel of a type nobody has published yet, which came back uppercased",
			in:   "> ◆ SURPRISE\n> p",
			want: `{"type":"panel","attrs":{"panelType":"surprise"},"content":[` + paraOf(txt("p")) + `]}`,
		},
		{
			name: "a custom panel carrying its own icon",
			in:   "> 🛠 PANEL\n> c",
			want: `{"type":"panel","attrs":{"panelIconText":"🛠","panelType":"custom"},"content":[` + paraOf(txt("c")) + `]}`,
		},
		{
			name: "a quote whose first line only looks like a panel",
			in:   "> ⚠ Warning: this is prose",
			want: `{"type":"blockquote","content":[` + paraOf(txt("⚠ Warning: this is prose")) + `]}`,
		},
		{
			name: "a code block with a language",
			in:   "```go\nx := 1\n```",
			want: `{"type":"codeBlock","attrs":{"language":"go"},"content":[` + txt("x := 1") + `]}`,
		},
		{
			name: "a code block with no language and nothing in it",
			in:   "```\n\n```",
			want: `{"type":"codeBlock"}`,
		},
		{
			name: "a line that only looks like a list, inside a code fence",
			in:   "```\n- not a bullet\n# not a heading\n```",
			want: `{"type":"codeBlock","content":[` + txt(`- not a bullet\n# not a heading`) + `]}`,
		},
		{
			name: "a fence longer than three, which is how code holding a fence is written",
			in:   "````\na\n```\nb\n````",
			want: `{"type":"codeBlock","content":[` + txt(`a\n`+"```"+`\nb`) + `]}`,
		},
		{
			name: "a fence nobody closed",
			in:   "```sh\nmake check",
			want: `{"type":"codeBlock","attrs":{"language":"sh"},"content":[` + txt("make check") + `]}`,
		},
		{
			name: "a rule",
			in:   "---",
			want: `{"type":"rule"}`,
		},
		{
			name: "an expand",
			in:   "▾ The long version\n  detail",
			want: `{"type":"expand","attrs":{"title":"The long version"},"content":[` + paraOf(txt("detail")) + `]}`,
		},
		{
			name: "an expand with no title",
			in:   "▾\n  detail",
			want: `{"type":"expand","content":[` + paraOf(txt("detail")) + `]}`,
		},
		{
			name: "an expand inside an expand, which ADF spells with a different node",
			in:   "▾ outer\n  ▾ inner\n    detail",
			want: `{"type":"expand","attrs":{"title":"outer"},"content":[` +
				`{"type":"nestedExpand","attrs":{"title":"inner"},"content":[` + paraOf(txt("detail")) + `]}]}`,
		},
		{
			name: "an image on its own line",
			in:   "![the trace](media:3f)",
			want: `{"type":"mediaSingle","content":[{"type":"media","attrs":{"alt":"the trace","id":"3f","type":"file"}}]}`,
		},
		{
			name: "an image with the alt the renderer writes when there was none",
			in:   "![media](media:3f)",
			want: `{"type":"mediaSingle","content":[{"type":"media","attrs":{"id":"3f","type":"file"}}]}`,
		},
		{
			name: "an externally hosted image",
			in:   "![a](https://example.com/a.png)",
			want: `{"type":"mediaSingle","content":[{"type":"media","attrs":{"alt":"a","type":"external","url":"https://example.com/a.png"}}]}`,
		},
		{
			name: "an image with a caption under it",
			in:   "![media](media:3f)\nFigure 1",
			want: `{"type":"mediaSingle","content":[{"type":"media","attrs":{"id":"3f","type":"file"}},` +
				`{"type":"caption","content":[` + paraOf(txt("Figure 1")) + `]}]}`,
		},
		{
			name: "a row of images, which is a group rather than several singles",
			in:   "![media](media:a)\n![media](media:b)",
			want: `{"type":"mediaGroup","content":[{"type":"media","attrs":{"id":"a","type":"file"}},` +
				`{"type":"media","attrs":{"id":"b","type":"file"}}]}`,
		},
		{
			name: "an image inside a sentence, which is inline",
			in:   "see ![a](media:x) here",
			want: paraOf(txt("see "), `{"type":"mediaInline","attrs":{"alt":"a","id":"x","type":"file"}}`, txt(" here")),
		},
		{
			name: "a table with a header row",
			in:   "| a | b |\n| - | - |\n| 1 | 2 |",
			want: `{"type":"table","content":[` +
				`{"type":"tableRow","content":[` +
				`{"type":"tableHeader","content":[` + paraOf(txt("a")) + `]},` +
				`{"type":"tableHeader","content":[` + paraOf(txt("b")) + `]}]},` +
				`{"type":"tableRow","content":[` +
				`{"type":"tableCell","content":[` + paraOf(txt("1")) + `]},` +
				`{"type":"tableCell","content":[` + paraOf(txt("2")) + `]}]}]}`,
		},
		{
			name: "a table with no header row",
			in:   "| left | right |",
			want: `{"type":"table","content":[{"type":"tableRow","content":[` +
				`{"type":"tableCell","content":[` + paraOf(txt("left")) + `]},` +
				`{"type":"tableCell","content":[` + paraOf(txt("right")) + `]}]}]}`,
		},
		{
			name: "a table cell holding the character the grid is drawn with",
			in:   `| a \| b | c |`,
			want: `{"type":"table","content":[{"type":"tableRow","content":[` +
				`{"type":"tableCell","content":[` + paraOf(txt(`a | b`)) + `]},` +
				`{"type":"tableCell","content":[` + paraOf(txt("c")) + `]}]}]}`,
		},
		{
			name: "a table cell with nothing in it",
			in:   "| a |  |",
			want: `{"type":"table","content":[{"type":"tableRow","content":[` +
				`{"type":"tableCell","content":[` + paraOf(txt("a")) + `]},` +
				`{"type":"tableCell","content":[` + paraOf() + `]}]}]}`,
		},
		{
			name: "a pipe in prose, which is not a table",
			in:   "a | b",
			want: paraOf(txt("a | b")),
		},
		{
			name: "trailing whitespace, which the renderer does not write and this does not keep",
			in:   "a line   ",
			want: paraOf(txt("a line")),
		},
		{
			name: "the control characters a terminal would act on",
			in:   "safe \u001b[31mred\u001b[0m",
			want: paraOf(txt(`safe [31mred[0m`)),
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			d, err := adf.ParseMarkdown(tc.in)
			if err != nil {
				t.Fatalf("%v", err)
			}
			got, err := adf.Marshal(d)
			if err != nil {
				t.Fatal(err)
			}
			if want := wrap(tc.want); string(got) != want {
				t.Errorf("\n got %s\nwant %s", got, want)
			}
		})
	}
}

func TestParseMarkdown_ReadsTheASCIIMarkerSetWhenAskedTo(t *testing.T) {
	t.Parallel()
	const in = "> ! WARNING\n> careful\n\n- <> ship\n\nv more\n  detail"
	d, err := adf.ParseMarkdownWith(in, adf.Options{ASCII: true})
	if err != nil {
		t.Fatal(err)
	}
	for typ, want := range map[string]int{"panel": 1, "decisionList": 1, "expand": 1} {
		if got := d.NodeTypes()[typ]; got != want {
			t.Errorf("%s appears %d times, want %d", typ, got, want)
		}
	}

	// The same markdown read with the Unicode marker set is prose and a quote,
	// which is why the option has to match the one it was rendered with.
	unicode, err := adf.ParseMarkdown(in)
	if err != nil {
		t.Fatal(err)
	}
	if got := unicode.NodeTypes()["panel"]; got != 0 {
		t.Errorf("the ASCII markers were read as a panel with Unicode markers in force")
	}
}

func TestParseMarkdown_HandlesADocumentWithNothingInIt(t *testing.T) {
	t.Parallel()
	for name, in := range map[string]string{
		"empty":                 "",
		"one newline":           "\n",
		"only whitespace":       "   \n\t\n  ",
		"only blank lines":      "\n\n\n\n",
		"windows line endings":  "\r\n\r\n",
		"a byte order mark":     "\ufeff",
		"whitespace and a mark": "\ufeff   ",
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			d, err := adf.ParseMarkdown(in)
			if err != nil {
				t.Fatal(err)
			}
			got, err := adf.Marshal(d)
			if err != nil {
				t.Fatal(err)
			}
			if want := `{"version":1,"type":"doc","content":[]}`; string(got) != want {
				t.Errorf("\n got %s\nwant %s", got, want)
			}
		})
	}
}

func TestParseMarkdown_ReadsWindowsAndClassicMacLineEndings(t *testing.T) {
	t.Parallel()
	want, err := adf.ParseMarkdown("# One\n\nTwo\nthree")
	if err != nil {
		t.Fatal(err)
	}
	wantJSON := encoded(t, want)
	for name, in := range map[string]string{
		"crlf": "# One\r\n\r\nTwo\r\nthree",
		"cr":   "# One\r\rTwo\rthree",
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			got, err := adf.ParseMarkdown(in)
			if err != nil {
				t.Fatal(err)
			}
			if gotJSON := encoded(t, got); gotJSON != wantJSON {
				t.Errorf("\n got %s\nwant %s", gotJSON, wantJSON)
			}
		})
	}
}

func TestParseMarkdown_RefusesANestingADFHasNoShapeFor(t *testing.T) {
	t.Parallel()
	for name, in := range map[string]string{
		"a table inside a quote":       "> | a | b |",
		"a table inside a list item":   "- one\n  | a | b |",
		"a heading inside a quote":     "> # shouted",
		"a heading inside a list item": "- one\n  # shouted",
		"a rule inside a quote":        "> above\n>\n> ---",
		"a panel inside a panel":       "> ℹ INFO\n> > ⚠ WARNING\n> > careful",
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			_, err := adf.ParseMarkdown(in)
			if !errors.Is(err, adf.ErrNesting) {
				t.Fatalf("want ErrNesting, got %v", err)
			}
			var perr *adf.ParseError
			if !errors.As(err, &perr) {
				t.Fatalf("want a *adf.ParseError, got %T", err)
			}
			if perr.Line == 0 {
				t.Error("the error does not say which line it stopped on")
			}
		})
	}
}

// TestParseMarkdown_NeverWritesAnEmptyTextNode guards the one rule in the ADF
// schema that a text-shaped parser breaks by accident: text has minLength 1, and
// a document carrying an empty one is rejected outright.
func TestParseMarkdown_NeverWritesAnEmptyTextNode(t *testing.T) {
	t.Parallel()
	for _, in := range []string{
		"****", "``", "~~~~", "__", "[]()", "**", "*", "_", "`", "[](x)", "![]()",
		"a ****", "> ", ">", "- ", "|  |", "```\n```", "#", "# ",
	} {
		t.Run(in, func(t *testing.T) {
			t.Parallel()
			d, err := adf.ParseMarkdown(in)
			if err != nil {
				return
			}
			d.Walk(func(n adf.Node) bool {
				if n.Type == "text" && n.Text == "" {
					t.Errorf("%q parsed to a text node with no text", in)
				}
				return true
			})
			if _, err := adf.Marshal(d); err != nil {
				t.Errorf("%q parsed to a document that will not encode: %v", in, err)
			}
		})
	}
}

// TestParseMarkdown_AlwaysAnswersADocument keeps the envelope honest: whatever
// comes in, what goes out has a version, a type and a content array, because
// anything else is rejected before Jira looks at the content.
func TestParseMarkdown_AlwaysAnswersADocument(t *testing.T) {
	t.Parallel()
	for _, in := range []string{"", "a", "- a", "> a", "| a |", "```\na\n```", "---", "# a"} {
		d, err := adf.ParseMarkdown(in)
		if err != nil {
			t.Fatalf("%q: %v", in, err)
		}
		if d.Version != 1 || d.Type != "doc" {
			t.Errorf("%q answered version %d type %q", in, d.Version, d.Type)
		}
		got, err := adf.Marshal(d)
		if err != nil {
			t.Fatalf("%q: %v", in, err)
		}
		if !strings.HasPrefix(string(got), `{"version":1,"type":"doc","content":[`) {
			t.Errorf("%q answered %s", in, got)
		}
	}
}

func TestParseMarkdownDropsOnly_NamesTheConstructsTheTestsProve(t *testing.T) {
	t.Parallel()
	drops := adf.ParseMarkdownDropsOnly()
	if len(drops) == 0 {
		t.Fatal("the list is empty, which is a claim this package cannot make")
	}
	for _, want := range []string{"mention", "status", "date", "table", "media", "heading", "hardBreak", "text", "marks"} {
		found := false
		for _, drop := range drops {
			found = found || strings.HasPrefix(drop, want+":")
		}
		if !found {
			t.Errorf("%q is lossy and is not in the list", want)
		}
	}
}

// TestParseMarkdown_TurnsWhatItCannotRebuildBackIntoProse is the honest half of
// the loss: a mention, a lozenge and a date are not dropped, they stop being
// nodes. A reader still sees every word the author wrote.
func TestParseMarkdown_TurnsWhatItCannotRebuildBackIntoProse(t *testing.T) {
	t.Parallel()
	for name, tc := range map[string]struct{ in, want string }{
		"a mention": {
			in:   wrap(para(text("Ask ") + `,{"type":"mention","attrs":{"id":"5b10ac8d","text":"@Someone"}},` + text(" about it"))),
			want: "Ask @Someone about it",
		},
		"a status lozenge": {
			in:   wrap(para(`{"type":"status","attrs":{"text":"Wartet auf Freigabe","color":"yellow"}}`)),
			want: "[Wartet auf Freigabe]",
		},
		"a date": {
			in:   wrap(para(`{"type":"date","attrs":{"timestamp":"1772409600000"}}`)),
			want: "2026-03-02",
		},
		"an emoji": {
			in:   wrap(para(`{"type":"emoji","attrs":{"shortName":":smile:","id":"1f604","text":"😄"}}`)),
			want: "😄",
		},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			out, err := adf.ParseMarkdown(adf.Markdown(parse(t, tc.in)))
			if err != nil {
				t.Fatal(err)
			}
			if got := adf.Markdown(out); got != tc.want {
				t.Errorf("\n got %q\nwant %q", got, tc.want)
			}
			for typ := range out.NodeTypes() {
				switch typ {
				case "mention", "status", "date", "emoji":
					t.Errorf("a %s came back as a node, which markdown cannot carry the attributes of", typ)
				}
			}
		})
	}
}
