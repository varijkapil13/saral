package adf_test

import (
	"encoding/json"
	"flag"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/charmbracelet/x/ansi"
	"github.com/varijkapil13/saral/pkg/adf"
)

var update = flag.Bool("update", false, "rewrite the golden files")

// bt is a backtick, which a raw string literal cannot hold.
const bt = "`"

// richFixture is the description of the issue the fixture server serves. It is
// read rather than copied so that a change to what Jira really sends shows up
// here as a golden-file diff.
const richFixture = "../jira/jiratest/fixtures/issue_rich_adf.json"

func TestMarkdown_RendersTheRichFixtureDescription(t *testing.T) {
	t.Parallel()
	d := fixtureDescription(t)

	for name, opt := range map[string]adf.Options{
		"rich_unicode.golden": {},
		"rich_ascii_20.golden": {
			TableWidth: 20,
			ASCII:      true,
		},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			golden(t, name, adf.MarkdownWith(d, opt))
		})
	}
}

func TestMarkdown_RendersEveryNodeTypeItKnows(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name string
		in   string
		want string
	}{
		{
			name: "a paragraph",
			in:   para(text("The basket is empty.")),
			want: "The basket is empty.",
		},
		{
			name: "a heading at every level it has",
			in: node("heading", `"attrs":{"level":1},"content":[`+text("one")+`]`) + "," +
				node("heading", `"attrs":{"level":6},"content":[`+text("six")+`]`),
			want: "# one\n\n###### six",
		},
		{
			name: "a heading deeper than markdown goes",
			in:   node("heading", `"attrs":{"level":9},"content":[`+text("clamped")+`]`),
			want: "###### clamped",
		},
		{
			name: "a heading with no level",
			in:   node("heading", `"content":[`+text("unlevelled")+`]`),
			want: "# unlevelled",
		},
		{
			name: "a heading with nothing in it, which is nothing to show",
			in:   node("heading", `"attrs":{"level":2}`),
			want: "",
		},
		{
			name: "bold, italic, struck through and underlined text",
			in: para(marked("bold", "strong") + "," + marked("italic", "em") + "," +
				marked("gone", "strike") + "," + marked("under", "underline")),
			want: "**bold***italic*~~gone~~_under_",
		},
		{
			name: "inline code",
			in:   para(marked("basketTotal()", "code")),
			want: bt + "basketTotal()" + bt,
		},
		{
			name: "inline code containing a backtick",
			in:   para(marked("a "+bt+" b", "code")),
			want: bt + bt + "a " + bt + " b" + bt + bt,
		},
		{
			name: "a link",
			in:   para(`{"type":"text","text":"the runbook","marks":[{"type":"link","attrs":{"href":"https://example.com/x"}}]}`),
			want: "[the runbook](https://example.com/x)",
		},
		{
			name: "a link whose text is its own address",
			in:   para(`{"type":"text","text":"https://example.com/x","marks":[{"type":"link","attrs":{"href":"https://example.com/x"}}]}`),
			want: "<https://example.com/x>",
		},
		{
			name: "a link to an address holding a bracket",
			in:   para(`{"type":"text","text":"wiki","marks":[{"type":"link","attrs":{"href":"https://example.com/a(b)"}}]}`),
			want: "[wiki](<https://example.com/a(b)>)",
		},
		{
			name: "emphasis around a trailing space, which is not emphasis",
			in:   para(marked("bold ", "strong") + "," + text("and on")),
			want: "**bold** and on",
		},
		{
			name: "a mark this package does not render",
			in:   para(`{"type":"text","text":"coloured","marks":[{"type":"textColor","attrs":{"color":"#ff0000"}}]}`),
			want: "coloured",
		},
		{
			name: "a hard break inside a paragraph",
			in:   para(text("first") + `,{"type":"hardBreak"},` + text("second")),
			want: "first\nsecond",
		},
		{
			name: "a mention",
			in:   para(text("Ask ") + `,{"type":"mention","attrs":{"id":"5b10ac8d","text":"@Someone"}},` + text(" about it")),
			want: "Ask @Someone about it",
		},
		{
			name: "a mention the editor stored without its text",
			in:   para(`{"type":"mention","attrs":{"id":"5b10ac8d"}}`),
			want: "@5b10ac8d",
		},
		{
			name: "a status lozenge",
			in:   para(`{"type":"status","attrs":{"text":"Wartet auf Freigabe","color":"yellow"}}`),
			want: "[Wartet auf Freigabe]",
		},
		{
			name: "an emoji",
			in:   para(`{"type":"emoji","attrs":{"shortName":":smile:","id":"1f604","text":"😄"}}`),
			want: "😄",
		},
		{
			name: "an emoji with no character to show",
			in:   para(`{"type":"emoji","attrs":{"shortName":":ship_it:","id":"atlassian-ship_it"}}`),
			want: ":ship_it:",
		},
		{
			name: "a date",
			in:   para(`{"type":"date","attrs":{"timestamp":"1772409600000"}}`),
			want: "2026-03-02",
		},
		{
			name: "a date whose timestamp is not a number",
			in:   para(`{"type":"date","attrs":{"timestamp":"soon"}}`),
			want: "soon",
		},
		{
			name: "an inline card",
			in:   para(`{"type":"inlineCard","attrs":{"url":"https://example.atlassian.net/browse/EX-2"}}`),
			want: "<https://example.atlassian.net/browse/EX-2>",
		},
		{
			name: "an inline card that resolved to a title",
			in:   para(`{"type":"inlineCard","attrs":{"data":{"url":"https://example.com/p","name":"The page"}}}`),
			want: "[The page](https://example.com/p)",
		},
		{
			name: "an inline image",
			in:   para(`{"type":"mediaInline","attrs":{"id":"3f6b1c72","type":"file","alt":"the trace"}}`),
			want: "![the trace](media:3f6b1c72)",
		},
		{
			name: "an image with no alt text",
			in:   node("mediaSingle", `"content":[{"type":"media","attrs":{"id":"3f6b1c72","type":"file"}}]`),
			want: "![media](media:3f6b1c72)",
		},
		{
			name: "an externally hosted image",
			in:   node("mediaSingle", `"content":[{"type":"media","attrs":{"type":"external","url":"https://example.com/a.png","alt":"a"}}]`),
			want: "![a](https://example.com/a.png)",
		},
		{
			name: "an image with a caption",
			in: node("mediaSingle", `"content":[{"type":"media","attrs":{"id":"3f","type":"file"}},`+
				node("caption", `"content":[`+text("Figure 1")+`]`)+`]`),
			want: "![media](media:3f)\nFigure 1",
		},
		{
			name: "prose whose characters are not one cell wide",
			in:   para(text("日本語 🙂 e\u0301cole — ok")),
			want: "日本語 🙂 e\u0301cole — ok",
		},
		{
			name: "a horizontal rule",
			in:   para(text("above")) + "," + node("rule", "") + "," + para(text("below")),
			want: "above\n\n---\n\nbelow",
		},
		{
			name: "a code block with a language",
			in:   node("codeBlock", `"attrs":{"language":"go"},"content":[`+text("x := 1\\ny := 2")+`]`),
			want: bt + bt + bt + "go\nx := 1\ny := 2\n" + bt + bt + bt,
		},
		{
			name: "a code block with no language",
			in:   node("codeBlock", `"content":[`+text("plain")+`]`),
			want: bt + bt + bt + "\nplain\n" + bt + bt + bt,
		},
		{
			name: "a code block holding a fence",
			in:   node("codeBlock", `"content":[`+text("a\\n"+bt+bt+bt+"\\nb")+`]`),
			want: bt + bt + bt + bt + "\na\n" + bt + bt + bt + "\nb\n" + bt + bt + bt + bt,
		},
		{
			name: "a blockquote",
			in:   node("blockquote", `"content":[`+para(text("one"))+`,`+para(text("two"))+`]`),
			want: "> one\n>\n> two",
		},
		{
			name: "an information panel",
			in:   node("panel", `"attrs":{"panelType":"info"},"content":[`+para(text("Read this."))+`]`),
			want: "> ℹ INFO\n> Read this.",
		},
		{
			name: "every other panel type",
			in: node("panel", `"attrs":{"panelType":"note"},"content":[`+para(text("n"))+`]`) + "," +
				node("panel", `"attrs":{"panelType":"success"},"content":[`+para(text("s"))+`]`) + "," +
				node("panel", `"attrs":{"panelType":"warning"},"content":[`+para(text("w"))+`]`) + "," +
				node("panel", `"attrs":{"panelType":"error"},"content":[`+para(text("e"))+`]`),
			want: "> ✎ NOTE\n> n\n\n> ✓ SUCCESS\n> s\n\n> ⚠ WARNING\n> w\n\n> ✗ ERROR\n> e",
		},
		{
			name: "a custom panel with its own icon",
			in:   node("panel", `"attrs":{"panelType":"custom","panelIconText":"🛠"},"content":[`+para(text("c"))+`]`),
			want: "> 🛠 PANEL\n> c",
		},
		{
			name: "a panel of a type nobody has published yet",
			in:   node("panel", `"attrs":{"panelType":"surprise"},"content":[`+para(text("p"))+`]`),
			want: "> ◆ SURPRISE\n> p",
		},
		{
			name: "a task list",
			in: node("taskList", `"content":[`+
				node("taskItem", `"attrs":{"state":"DONE"},"content":[`+text("done")+`]`)+`,`+
				node("taskItem", `"attrs":{"state":"TODO"},"content":[`+text("todo")+`]`)+`]`),
			want: "- [x] done\n- [ ] todo",
		},
		{
			name: "a decision list",
			in:   node("decisionList", `"content":[`+node("decisionItem", `"content":[`+text("ship it")+`]`)+`]`),
			want: "- ◇ ship it",
		},
		{
			name: "an expand",
			in:   node("expand", `"attrs":{"title":"The long version"},"content":[`+para(text("detail"))+`]`),
			want: "▾ The long version\n  detail",
		},
		{
			name: "an expand with no title",
			in:   node("expand", `"content":[`+para(text("detail"))+`]`),
			want: "▾\n  detail",
		},
		{
			name: "a placeholder",
			in:   para(`{"type":"placeholder","attrs":{"text":"Type something"}}`),
			want: "Type something",
		},
		{
			name: "a block card",
			in:   node("blockCard", `"attrs":{"url":"https://example.com/board"}`),
			want: "<https://example.com/board>",
		},
		{
			name: "a layout, whose columns stack",
			in: node("layoutSection", `"content":[`+
				node("layoutColumn", `"attrs":{"width":50},"content":[`+para(text("left"))+`]`)+`,`+
				node("layoutColumn", `"attrs":{"width":50},"content":[`+para(text("right"))+`]`)+`]`),
			want: "left\n\nright",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if got := adf.Markdown(parse(t, wrap(tc.in))); got != tc.want {
				t.Errorf("\n got %q\nwant %q", got, tc.want)
			}
		})
	}
}

func TestMarkdown_ShowsANodeTypeItHasNeverHeardOf(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name string
		in   string
		want string
	}{
		{
			name: "a block node with block content",
			in:   node("futureBlock", `"attrs":{"variant":"callout"},"content":[`+para(text("kept"))+`]`),
			want: "> [unsupported: futureBlock]\n> kept",
		},
		{
			name: "a block node with nothing in it",
			in:   node("futureBlock", `"attrs":{"variant":"callout"}`),
			want: "> [unsupported: futureBlock]",
		},
		{
			name: "a block node carrying its text in an attribute",
			in:   node("futureBlock", `"attrs":{"text":"a caption"}`),
			want: "> [unsupported: futureBlock]\n> a caption",
		},
		{
			name: "an inline node inside a paragraph",
			in:   para(text("before ") + `,{"type":"futureInline","attrs":{"x":1}},` + text(" after")),
			want: "before [unsupported: futureInline] after",
		},
		{
			name: "an inline node with text in it",
			in:   para(`{"type":"futureInline","content":[` + text("its words") + `]}`),
			want: "[unsupported: futureInline: its words]",
		},
		{
			name: "the node Jira itself stores for something it could not parse",
			in:   node("unsupportedBlock", `"attrs":{"originalValue":{"type":"someMacro","attrs":{"k":1}}}`),
			want: "> [unsupported: someMacro]",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if got := adf.Markdown(parse(t, wrap(tc.in))); got != tc.want {
				t.Errorf("\n got %q\nwant %q", got, tc.want)
			}
		})
	}
}

func TestMarkdown_RendersADocumentThatIsOnlyAnUnknownNode(t *testing.T) {
	t.Parallel()
	const in = `{"version":1,"type":"doc","content":[{"type":"multiBodiedExtension","attrs":{"extensionKey":"chart"},` +
		`"content":[{"type":"extensionFrame","content":[{"type":"paragraph","content":[{"type":"text","text":"a chart lives here"}]}]}]}]}`
	golden(t, "unknown_only.golden", adf.Markdown(parse(t, in)))
}

func TestMarkdown_IsEmptyForADocumentWithNoContent(t *testing.T) {
	t.Parallel()
	for name, in := range map[string]string{
		"an empty document":       `{"version":1,"type":"doc","content":[]}`,
		"a document with no key":  `{"version":1,"type":"doc"}`,
		"a null document":         `null`,
		"an empty paragraph":      wrap(node("paragraph", "")),
		"two empty paragraphs":    wrap(node("paragraph", "") + "," + node("paragraph", "")),
		"an empty table":          wrap(node("table", `"attrs":{"layout":"default"}`)),
		"a table with empty rows": wrap(node("table", `"content":[`+node("tableRow", "")+`]`)),
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			if got := adf.Markdown(parse(t, in)); got != "" {
				t.Errorf("want no output, got %q", got)
			}
		})
	}
}

func TestMarkdown_RendersTheSameBytesEveryTime(t *testing.T) {
	t.Parallel()
	d := fixtureDescription(t)
	first := adf.Markdown(d)
	for range 8 {
		if got := adf.Markdown(d); got != first {
			t.Fatalf("two renders of one document differ\n--- first ---\n%s\n--- then ---\n%s", first, got)
		}
	}
}

func TestMarkdown_IsEmptyForTheZeroDocument(t *testing.T) {
	t.Parallel()
	if got := adf.Markdown(adf.Doc{}); got != "" {
		t.Errorf("want no output, got %q", got)
	}
}

func TestMarkdown_NestsListsAndQuotesToAnyDepth(t *testing.T) {
	t.Parallel()
	golden(t, "nested_lists.golden", adf.Markdown(parse(t, nestedLists)))
}

func TestMarkdown_NumbersAnOrderedListFromItsStart(t *testing.T) {
	t.Parallel()
	in := wrap(node("orderedList", `"attrs":{"order":9},"content":[`+
		item("nine")+`,`+item("ten")+`,`+item("eleven")+`]`))
	const want = "9. nine\n10. ten\n11. eleven"
	if got := adf.Markdown(parse(t, in)); got != want {
		t.Errorf("\n got %q\nwant %q", got, want)
	}
}

func TestMarkdown_KeepsTheBulletOfAnEmptyListItem(t *testing.T) {
	t.Parallel()
	in := wrap(node("bulletList", `"content":[`+node("listItem", "")+`,`+item("second")+`]`))
	const want = "-\n- second"
	if got := adf.Markdown(parse(t, in)); got != want {
		t.Errorf("\n got %q\nwant %q", got, want)
	}
}

func TestMarkdown_RendersATableWithAndWithoutAHeaderRow(t *testing.T) {
	t.Parallel()
	for name, tc := range map[string]struct {
		in    string
		width int
	}{
		"table_header.golden":    {wrap(headedTable), 0},
		"table_no_header.golden": {wrap(headlessTable), 0},
		"table_ragged.golden":    {wrap(raggedTable), 0},
		"table_squeezed.golden":  {wrap(headedTable), 30},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			golden(t, name, adf.MarkdownWith(parse(t, tc.in), adf.Options{TableWidth: tc.width}))
		})
	}
}

func TestMarkdown_BoundsATableToTheWidthItWasGiven(t *testing.T) {
	t.Parallel()
	for _, width := range []int{80, 40, 20} {
		out := adf.MarkdownWith(parse(t, wrap(headedTable)), adf.Options{TableWidth: width})
		for line := range strings.SplitSeq(out, "\n") {
			if got := ansi.StringWidth(line); got > width {
				t.Errorf("at width %d a line came back %d cells wide: %q", width, got, line)
			}
		}
	}
}

func TestMarkdown_LinesUpAColumnOfCharactersThatAreNotOneCellWide(t *testing.T) {
	t.Parallel()
	out := adf.Markdown(parse(t, wrap(wideTable)))
	golden(t, "table_wide_runes.golden", out)

	lines := strings.Split(out, "\n")
	want := ansi.StringWidth(lines[0])
	for _, line := range lines[1:] {
		if got := ansi.StringWidth(line); got != want {
			t.Errorf("the grid is %d cells wide but this row is %d: %q", want, got, line)
		}
	}
	if want == len(lines[0]) {
		t.Fatal("the test data has to contain something wider than one byte per cell, or it proves nothing")
	}
}

func TestMarkdown_OrderingTheSameMarksDifferentlyRendersTheSame(t *testing.T) {
	t.Parallel()
	for name, orders := range map[string][][]string{
		"bold and italic": {{"strong", "em"}, {"em", "strong"}},
		"a link carrying emphasis": {
			{"link", "em", "strong"},
			{"strong", "em", "link"},
			{"em", "link", "strong"},
		},
		"every mark at once": {
			{"link", "em", "strong", "strike", "underline", "code"},
			{"code", "underline", "strike", "strong", "em", "link"},
			{"strike", "link", "code", "strong", "underline", "em"},
		},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			first := ""
			for i, order := range orders {
				in := wrap(para(`{"type":"text","text":"emphasis","marks":[` +
					strings.Join(marksJSON(order), ",") + `]}`))
				got := adf.Markdown(parse(t, in))
				switch {
				case i == 0:
					first = got
				case got != first:
					t.Errorf("%v rendered %q but %v rendered %q", orders[0], first, order, got)
				}
			}
			if !strings.Contains(first, "emphasis") {
				t.Fatalf("the marked text itself went missing: %q", first)
			}
		})
	}
}

func TestMarkdown_DropsTheControlCharactersATerminalWouldActOn(t *testing.T) {
	t.Parallel()
	const escape = `{"version":1,"type":"doc","content":[{"type":"paragraph","content":[` +
		`{"type":"text","text":"safe \u001b[31mred\u001b[0m and a bell\u0007"}]}]}`
	got := adf.Markdown(parse(t, escape))
	if strings.ContainsAny(got, "\x1b\x07") {
		t.Fatalf("an escape sequence survived into the output: %q", got)
	}
	if want := "safe [31mred[0m and a bell"; got != want {
		t.Errorf("\n got %q\nwant %q", got, want)
	}
}

func TestMarkdown_KeepsTabsAndNewlinesInCode(t *testing.T) {
	t.Parallel()
	in := wrap(node("codeBlock", `"content":[`+text("if x {\\n\\tdo()\\n}")+`]`))
	if got := adf.Markdown(parse(t, in)); !strings.Contains(got, "\n\tdo()\n") {
		t.Errorf("the indentation did not survive: %q", got)
	}
}

func TestMarkdown_RendersADateInTheLocationItWasGiven(t *testing.T) {
	t.Parallel()
	in := parse(t, wrap(para(`{"type":"date","attrs":{"timestamp":"1772409600000"}}`)))
	auckland, err := time.LoadLocation("Pacific/Auckland")
	if err != nil {
		t.Skipf("no timezone database here: %v", err)
	}
	if got := adf.MarkdownWith(in, adf.Options{Location: auckland}); got != "2026-03-02" {
		t.Errorf("got %q", got)
	}
	if got := adf.MarkdownWith(in, adf.Options{Location: time.FixedZone("west", -12*60*60)}); got != "2026-03-01" {
		t.Errorf("a date twelve hours behind UTC should fall on the day before, got %q", got)
	}
}

func TestMarkdown_SwapsInASCIIMarkersWhenAskedTo(t *testing.T) {
	t.Parallel()
	in := parse(t, wrap(
		node("panel", `"attrs":{"panelType":"warning"},"content":[`+para(text("careful"))+`]`)+","+
			node("decisionList", `"content":[`+node("decisionItem", `"content":[`+text("ship")+`]`)+`]`)+","+
			node("expand", `"attrs":{"title":"more"},"content":[`+para(text("detail"))+`]`)))

	got := adf.MarkdownWith(in, adf.Options{ASCII: true})
	for _, r := range got {
		if r > 127 {
			t.Fatalf("%q is not ascii, in:\n%s", r, got)
		}
	}
	const want = "> ! WARNING\n> careful\n\n- <> ship\n\nv more\n  detail"
	if got != want {
		t.Errorf("\n got %q\nwant %q", got, want)
	}
}

func TestMarkdown_EndsWithoutATrailingNewline(t *testing.T) {
	t.Parallel()
	got := adf.Markdown(fixtureDescription(t))
	if strings.HasSuffix(got, "\n") {
		t.Error("the output ends with a newline, so splitting it into lines yields an empty last line")
	}
	if strings.Contains(got, "\n\n\n") {
		t.Error("the output holds a run of blank lines")
	}
	for line := range strings.SplitSeq(got, "\n") {
		if strings.HasSuffix(line, " ") {
			t.Errorf("a line ends in a space: %q", line)
		}
	}
}

func TestAppendMarkdown_AppendsToTheBufferItWasGiven(t *testing.T) {
	t.Parallel()
	d := parse(t, wrap(para(text("second"))))
	got := string(adf.AppendMarkdown([]byte("first\n"), d, adf.Options{}))
	if want := "first\nsecond"; got != want {
		t.Errorf("\n got %q\nwant %q", got, want)
	}
}

func TestAppendMarkdown_ReusesOneBufferAcrossDocuments(t *testing.T) {
	t.Parallel()
	first := parse(t, wrap(para(text("one"))))
	second := parse(t, wrap(para(text("a much longer second document"))))

	buf := adf.AppendMarkdown(nil, first, adf.Options{})
	if got := string(buf); got != "one" {
		t.Fatalf("got %q", got)
	}
	buf = adf.AppendMarkdown(buf[:0], second, adf.Options{})
	if got, want := string(buf), "a much longer second document"; got != want {
		t.Errorf("\n got %q\nwant %q", got, want)
	}
}

// TestMarkdown_AllocatesPerNodeAndNotPerCharacter guards the property that
// makes this renderer safe to put in front of a viewport: doubling the text
// must not double the allocations. A renderer that builds a string per
// character passes every other test in this file and then falls over on a
// description somebody pasted a log into.
func TestMarkdown_AllocatesPerNodeAndNotPerCharacter(t *testing.T) {
	short := parse(t, paragraphs(50, 4))
	long := parse(t, paragraphs(50, 4000))

	shortRuns := testing.AllocsPerRun(20, func() { sink = adf.Markdown(short) })
	longRuns := testing.AllocsPerRun(20, func() { sink = adf.Markdown(long) })
	if longRuns > shortRuns+2 {
		t.Errorf("the same %d nodes cost %.0f allocations with four characters each and %.0f with four thousand",
			50, shortRuns, longRuns)
	}
}

// paragraphs builds a document of count paragraphs of size characters each.
func paragraphs(count, size int) string {
	blocks := make([]string, 0, count)
	for range count {
		blocks = append(blocks, para(text(strings.Repeat("word ", size/5))))
	}
	return wrap(strings.Join(blocks, ","))
}

func BenchmarkMarkdown_RichFixture(b *testing.B) {
	d := fixtureDescription(b)
	b.ReportAllocs()
	b.ResetTimer()
	for b.Loop() {
		sink = adf.Markdown(d)
	}
}

func BenchmarkMarkdown_LargeDocument(b *testing.B) {
	d := largeDoc(b, 500)
	b.ReportAllocs()
	b.ResetTimer()
	for b.Loop() {
		sink = adf.Markdown(d)
	}
}

func BenchmarkAppendMarkdown_ReusedBuffer(b *testing.B) {
	d := largeDoc(b, 500)
	buf := make([]byte, 0, 1<<16)
	b.ReportAllocs()
	b.ResetTimer()
	for b.Loop() {
		buf = adf.AppendMarkdown(buf[:0], d, adf.Options{})
	}
	byteSink = buf
}

var (
	sink     string
	byteSink []byte
)

// largeDoc repeats the fixture description until it is a document nobody would
// write by hand but a long-running issue eventually becomes.
func largeDoc(tb testing.TB, times int) adf.Doc {
	tb.Helper()
	one := fixtureDescription(tb)
	content := make([]adf.Node, 0, len(one.Content)*times)
	for range times {
		content = append(content, one.Clone().Content...)
	}
	return adf.NewDoc(content...)
}

func fixtureDescription(tb testing.TB) adf.Doc {
	tb.Helper()
	raw, err := os.ReadFile(richFixture)
	if err != nil {
		tb.Fatalf("reading %s: %v", richFixture, err)
	}
	var envelope struct {
		Fields struct {
			Description adf.Doc `json:"description"`
		} `json:"fields"`
	}
	if err := json.Unmarshal(raw, &envelope); err != nil {
		tb.Fatalf("parsing %s: %v", richFixture, err)
	}
	if envelope.Fields.Description.IsEmpty() {
		tb.Fatalf("%s carries no description", richFixture)
	}
	return envelope.Fields.Description
}

func parse(tb testing.TB, in string) adf.Doc {
	tb.Helper()
	d, err := adf.Unmarshal([]byte(in))
	if err != nil {
		tb.Fatalf("unmarshal %s: %v", in, err)
	}
	return d
}

func golden(t *testing.T, name, got string) {
	t.Helper()
	path := filepath.Join("testdata", name)
	if *update {
		if err := os.MkdirAll("testdata", 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(got), 0o644); err != nil {
			t.Fatal(err)
		}
		return
	}
	want, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("%v — run: go test ./pkg/adf -update", err)
	}
	if string(want) != got {
		t.Errorf("output differs from %s\n--- want ---\n%s\n--- got ---\n%s", path, want, got)
	}
}

func wrap(content string) string {
	return `{"version":1,"type":"doc","content":[` + content + `]}`
}

func node(typ, rest string) string {
	if rest == "" {
		return `{"type":"` + typ + `"}`
	}
	return `{"type":"` + typ + `",` + rest + `}`
}

func text(s string) string { return `{"type":"text","text":"` + s + `"}` }

func marked(s, mark string) string {
	return `{"type":"text","text":"` + s + `","marks":[{"type":"` + mark + `"}]}`
}

func para(content string) string {
	if content == "" {
		return node("paragraph", "")
	}
	return node("paragraph", `"content":[`+content+`]`)
}

func item(s string) string { return node("listItem", `"content":[`+para(text(s))+`]`) }

func cell(typ, s string) string { return node(typ, `"content":[`+para(text(s))+`]`) }

func row(cells ...string) string {
	return node("tableRow", `"content":[`+strings.Join(cells, ",")+`]`)
}

// nestedLists puts a list inside a list inside a list, and a list inside a
// quote, because indentation is where a line-prefix renderer goes wrong.
var nestedLists = wrap(
	node("bulletList", `"content":[`+
		node("listItem", `"content":[`+para(text("one"))+`,`+
			node("bulletList", `"content":[`+
				node("listItem", `"content":[`+para(text("one a"))+`,`+
					node("orderedList", `"attrs":{"order":3},"content":[`+
						item("deep")+`,`+
						node("listItem", `"content":[`+para(text("deeper"))+`,`+
							node("codeBlock", `"attrs":{"language":"sh"},"content":[`+text("make check")+`]`)+`]`)+
						`]`)+`]`)+
				`]`)+`]`)+`,`+
		node("listItem", `"content":[`+para(text("two"))+`,`+para(text("still two"))+`]`)+
		`]`) + `,` +
		node("blockquote", `"content":[`+para(text("quoted"))+`,`+
			node("bulletList", `"content":[`+item("in a quote")+`,`+
				node("listItem", `"content":[`+para(text("with a panel"))+`,`+
					node("panel", `"attrs":{"panelType":"info"},"content":[`+para(text("nested deep"))+`]`)+`]`)+
				`]`)+`]`),
)

var headedTable = node("table", `"attrs":{"isNumberColumnEnabled":false,"layout":"default"},"content":[`+
	row(cell("tableHeader", "Environment"), cell("tableHeader", "Reproduced"), cell("tableHeader", "Owner"))+`,`+
	row(cell("tableCell", "staging"), cell("tableCell", "twice"), cell("tableCell", "the checkout team"))+`,`+
	row(cell("tableCell", "production"), cell("tableCell", "never"), cell("tableCell", "nobody yet"))+
	`]`)

var headlessTable = node("table", `"content":[`+
	row(cell("tableCell", "left"), cell("tableCell", "right"))+`,`+
	row(cell("tableCell", "bottom left"), cell("tableCell", "bottom right"))+
	`]`)

// raggedTable is what an editor produces rather than what a schema promises: a
// merged cell leaving a row short, a cell holding more than one block, and a
// cell holding the character the grid is drawn with.
var raggedTable = node("table", `"content":[`+
	row(cell("tableHeader", "One"), cell("tableHeader", "Two"), cell("tableHeader", "Three"))+`,`+
	row(node("tableCell", `"attrs":{"colspan":2},"content":[`+para(text("spans two"))+`]`), cell("tableCell", "last"))+`,`+
	row(
		node("tableCell", `"content":[`+para(text("first paragraph"))+`,`+para(text("second paragraph"))+`]`),
		node("tableCell", `"content":[`+node("bulletList", `"content":[`+item("a")+`,`+item("b")+`]`)+`]`),
		cell("tableCell", "a | b"))+
	`]`)

// wideTable holds a column of characters that are two cells wide, a column of
// emoji, and a column with a combining acute, none of which a byte count or a
// rune count measures correctly.
var wideTable = node("table", `"content":[`+
	row(cell("tableHeader", "言語"), cell("tableHeader", "Emoji"), cell("tableHeader", "Combining"))+`,`+
	row(cell("tableCell", "日本語"), cell("tableCell", "🙂🙂"), cell("tableCell", "école"))+`,`+
	row(cell("tableCell", "ascii"), cell("tableCell", "🚀"), cell("tableCell", "e\u0301cole"))+
	`]`)

func marksJSON(types []string) []string {
	out := make([]string, 0, len(types))
	for _, m := range types {
		if m == "link" {
			out = append(out, `{"type":"link","attrs":{"href":"https://example.com"}}`)
			continue
		}
		out = append(out, `{"type":"`+m+`"}`)
	}
	return out
}

func TestMarkdown_NestedTaskListKeepsItsCheckboxes(t *testing.T) {
	t.Parallel()

	// Indenting an action item in the Jira editor stores a sibling taskList
	// inside its parent, which is what ADF's (taskItem | taskList)+ content
	// model allows. Treating that child as an item renders every one of its
	// entries as unsupported and loses the DONE state.
	const in = `{"version":1,"type":"doc","content":[{"type":"taskList","attrs":{"localId":"a"},"content":[
		{"type":"taskItem","attrs":{"localId":"b","state":"TODO"},"content":[{"type":"text","text":"ship the fix"}]},
		{"type":"taskList","attrs":{"localId":"c"},"content":[
			{"type":"taskItem","attrs":{"localId":"d","state":"DONE"},"content":[{"type":"text","text":"write the test"}]},
			{"type":"taskItem","attrs":{"localId":"e","state":"TODO"},"content":[{"type":"text","text":"update the docs"}]}]},
		{"type":"taskItem","attrs":{"localId":"f","state":"TODO"},"content":[{"type":"text","text":"tell the reporter"}]}]}]}`

	got := adf.Markdown(parse(t, in))
	for _, want := range []string{"- [ ] ship the fix", "- [x] write the test", "- [ ] update the docs", "- [ ] tell the reporter"} {
		if !strings.Contains(got, want) {
			t.Errorf("missing %q in:\n%s", want, got)
		}
	}
	if strings.Contains(got, "unsupported") {
		t.Errorf("a nested task list rendered as unsupported:\n%s", got)
	}
}

func TestMarkdown_MergedCellsKeepLaterValuesUnderTheirOwnHeading(t *testing.T) {
	t.Parallel()

	// A cell with colspan holds the grid positions it spans. Emitting one column
	// for it slides every value after it left, so the answer under a heading is
	// simply wrong rather than merely misaligned.
	const in = `{"version":1,"type":"doc","content":[{"type":"table","content":[
		{"type":"tableRow","content":[
			{"type":"tableHeader","content":[{"type":"paragraph","content":[{"type":"text","text":"Env"}]}]},
			{"type":"tableHeader","content":[{"type":"paragraph","content":[{"type":"text","text":"Runs"}]}]},
			{"type":"tableHeader","content":[{"type":"paragraph","content":[{"type":"text","text":"Owner"}]}]}]},
		{"type":"tableRow","content":[
			{"type":"tableCell","attrs":{"colspan":2},"content":[{"type":"paragraph","content":[{"type":"text","text":"both"}]}]},
			{"type":"tableCell","content":[{"type":"paragraph","content":[{"type":"text","text":"nobody"}]}]}]}]}]}`

	lines := strings.Split(strings.TrimRight(adf.Markdown(parse(t, in)), "\n"), "\n")
	if len(lines) != 3 {
		t.Fatalf("want a header, a delimiter and one row, got:\n%s", strings.Join(lines, "\n"))
	}
	cells := strings.Split(strings.Trim(lines[2], "| "), "|")
	if len(cells) != 3 {
		t.Fatalf("the merged row has %d columns, want 3: %q", len(cells), lines[2])
	}
	if got := strings.TrimSpace(cells[2]); got != "nobody" {
		t.Errorf("the Owner column holds %q, want \"nobody\": a merged cell shifted it", got)
	}
}

func TestMarkdown_ASCIIEllipsisDoesNotEraseASqueezedColumn(t *testing.T) {
	t.Parallel()

	// The ASCII ellipsis is three cells wide against the Unicode one's one, so a
	// floor of three leaves a squeezed column holding nothing but "...".
	const in = `{"version":1,"type":"doc","content":[{"type":"table","content":[
		{"type":"tableRow","content":[
			{"type":"tableCell","content":[{"type":"paragraph","content":[{"type":"text","text":"alpha bravo charlie"}]}]},
			{"type":"tableCell","content":[{"type":"paragraph","content":[{"type":"text","text":"delta echo foxtrot"}]}]}]}]}]}`

	got := adf.MarkdownWith(parse(t, in), adf.Options{ASCII: true, TableWidth: 24})
	for _, cell := range strings.Split(strings.Trim(strings.TrimSpace(got), "| "), "|") {
		if strings.TrimSpace(cell) == "..." {
			t.Errorf("a squeezed column shows only the ellipsis:\n%s", got)
		}
	}
}
