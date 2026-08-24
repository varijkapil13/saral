package adf_test

import (
	"errors"
	"math/rand/v2"
	"strings"
	"testing"

	"github.com/varijkapil13/saral/pkg/adf"
)

// FuzzParseMarkdown asserts the two things that must hold for any input at all,
// including input no renderer produced: the parser answers a document Jira
// would accept, and rendering and re-parsing it settles.
//
// Settling is what stops a save from eating text. The first render is allowed
// to change the markdown, because the renderer does not escape prose — a
// paragraph that reads "0)" is spelled exactly like an ordered list, and one
// pass through markdown makes it one. What must not happen is drift: a document
// that grows a marker, or loses a character, every time it is opened and saved.
// TestParseMarkdown_HoldsBothPropertiesOverGeneratedDocuments asserts the
// stronger one-pass version over documents that started life as ADF.
func FuzzParseMarkdown(f *testing.F) {
	for _, seed := range []string{
		"", "\n", "   ", "\r\n", "a", "# a", "###### a", "####### a",
		"- a\n- b", "1. a\n2. b", "9. a", "- [ ] a\n- [x] b", "- ◇ a",
		"- a\n  - b\n    - c", "- a\n\n  b", "-", "- ",
		"> a", ">", "> a\n>\n> b", "> ℹ INFO\n> a", "> ◆ PANEL\n> a", "> 🛠 PANEL\n> a",
		"> [unsupported: futureBlock]\n> a", "[unsupported: futureInline]",
		"```", "```go\na\n```", "````\n```\n````", "```\n\n```", "~~~\na\n~~~",
		"---", "***", "___", "- - -",
		"| a | b |", "| a | b |\n| - | - |\n| 1 | 2 |", `| a \| b |`, "|  |  |", "|",
		"**a**", "*a*", "~~a~~", "_a_", "`a`", "``a ` b``", "`` ` ``",
		"***a***", "**a *b* c**", "*a **b** c*", "a_b_c", "__a__",
		"[a](b)", "[a [b] c](d)", "[a](<b c>)", "[a](<b%3Cc%3E d>)", "<https://x/y>", "<a>",
		"![a](media:1)", "![a](media:1)\n![b](media:2)", "![a](media:1)\ncaption", "![](x)",
		"▾ a\n  b", "▾", "v a\n  b",
		"a\nb", "a\n- b", "- a\n# b", "a | b", "a  ",
		"a\n\n\n\nb", "\ufeffa", "# \n\n#",
		"> | a |", "> # a", "- | a |",
	} {
		f.Add(seed)
	}

	f.Fuzz(func(t *testing.T, md string) {
		for _, opt := range []adf.Options{{}, {ASCII: true}} {
			at := md
			for range 4 {
				d, err := adf.ParseMarkdownWith(at, opt)
				if err != nil {
					var perr *adf.ParseError
					if !errors.As(err, &perr) {
						t.Fatalf("%q: an error that is not a *adf.ParseError: %v", at, err)
					}
					break
				}
				mustBeValid(t, d, at)
				next := adf.MarkdownWith(d, opt)
				if next == at {
					break
				}
				if at = next; at == "" {
					break
				}
			}
			settled, err := adf.ParseMarkdownWith(at, opt)
			if err != nil {
				continue
			}
			if got := adf.MarkdownWith(settled, opt); got != at {
				t.Fatalf("%q does not settle\n--- at ---\n%q\n--- then ---\n%q", md, at, got)
			}
		}
	})
}

// mustBeValid checks the rules a document has to obey before Jira will look at
// what is in it.
func mustBeValid(tb testing.TB, d adf.Doc, from string) {
	tb.Helper()
	if d.Version != 1 || d.Type != "doc" {
		tb.Fatalf("%q answered version %d type %q", from, d.Version, d.Type)
	}
	d.Walk(func(n adf.Node) bool {
		switch {
		case n.Type == "":
			tb.Fatalf("%q answered a node with no type", from)
		case n.Type == "text" && n.Text == "":
			tb.Fatalf("%q answered an empty text node", from)
		case n.Type != "text" && n.Text != "":
			tb.Fatalf("%q answered a %s carrying text", from, n.Type)
		}
		return true
	})
	if _, err := adf.Marshal(d); err != nil {
		tb.Fatalf("%q answered a document that will not encode: %v", from, err)
	}
}

// TestParseMarkdown_HoldsBothPropertiesOverGeneratedDocuments walks a few
// thousand documents nobody would write by hand. The seeded generator makes the
// failures reproducible, and the seed is printed with any of them.
//
// Byte-stability is asserted for every option set. The fixed point is asserted
// only where the markdown is what a caller would hand to an editor: bounding a
// table's width truncates its cells, and truncation is not a round trip.
func TestParseMarkdown_HoldsBothPropertiesOverGeneratedDocuments(t *testing.T) {
	t.Parallel()
	for seed := range seeds() {
		rng := rand.New(rand.NewPCG(seed, 0x5a4a1))
		in := generated(rng)
		d := parse(t, in)

		for _, opt := range renderOptions() {
			md := adf.MarkdownWith(d, opt)

			into, err := adf.ParseMarkdownInto(d, md, opt)
			if err != nil {
				t.Fatalf("seed %d %+v: %v\n%s", seed, opt, err, in)
			}
			if got, want := encoded(t, into), encoded(t, d); got != want {
				t.Fatalf("seed %d %+v: an untouched document did not come back\n--- want ---\n%s\n--- got ---\n%s\n--- markdown ---\n%s",
					seed, opt, want, got, md)
			}

			if opt.TableWidth != 0 {
				continue
			}
			fresh, err := adf.ParseMarkdownWith(md, opt)
			if errors.Is(err, adf.ErrUnsupportedMarker) {
				continue
			}
			if err != nil {
				t.Fatalf("seed %d %+v: %v\n%s", seed, opt, err, md)
			}
			mustBeValid(t, fresh, md)
			if twice := adf.MarkdownWith(fresh, opt); twice != md {
				t.Fatalf("seed %d %+v: the render does not settle\n--- once ---\n%q\n--- twice ---\n%q",
					seed, opt, md, twice)
			}
		}
	}
}

// seeds is how many documents to walk. The number is a trade against the race
// suite's wall clock, which pays about ten times per document; raising it
// locally and running without -race is how to go looking for more.
func seeds() uint64 {
	if testing.Short() {
		return 200
	}
	return 1500
}

// generated builds one ADF document as JSON. It writes JSON rather than nodes
// so that the failing case can be pasted straight into a test.
func generated(rng *rand.Rand) string {
	blocks := make([]string, 0, 8)
	for range rng.IntN(7) + 1 {
		blocks = append(blocks, genBlock(rng, 0))
	}
	return wrap(strings.Join(blocks, ","))
}

func genBlock(rng *rand.Rand, depth int) string {
	kinds := []string{"paragraph", "heading", "bulletList", "orderedList", "codeBlock", "rule",
		"panel", "table", "taskList", "decisionList", "blockquote", "expand", "mediaSingle", "unknown"}
	if depth > 0 {
		// ADF has no shape for a heading, a rule or a table below the root, and
		// the parser refuses to invent one, so the generator does not either.
		kinds = []string{"paragraph", "bulletList", "orderedList", "codeBlock", "taskList", "mediaSingle"}
	}
	switch kinds[rng.IntN(len(kinds))] {
	case "heading":
		// No hard break: a heading is one line in markdown and cannot hold one.
		return node("heading", `"attrs":{"level":`+itoa(rng.IntN(6)+1)+`},"content":[`+text(genWords(rng))+`]`)
	case "bulletList":
		return node("bulletList", `"content":[`+genItems(rng, depth, "listItem")+`]`)
	case "orderedList":
		return node("orderedList", `"attrs":{"order":`+itoa(rng.IntN(20)+1)+`},"content":[`+genItems(rng, depth, "listItem")+`]`)
	case "codeBlock":
		return node("codeBlock", `"attrs":{"language":"`+pick(rng, "go", "sh", "")+`"},"content":[`+
			text(pick(rng, `x := 1`, `a\nb`, `if x {\n\tdo()\n}`, "`"+"`"+"`"))+`]`)
	case "rule":
		return node("rule", "")
	case "panel":
		return node("panel", `"attrs":{"panelType":"`+pick(rng, "info", "note", "warning", "error", "success", "custom", "surprise")+
			`"},"content":[`+node("paragraph", `"content":[`+genInline(rng)+`]`)+`]`)
	case "table":
		return genTable(rng)
	case "taskList":
		return node("taskList", `"content":[`+genItems(rng, depth, "taskItem")+`]`)
	case "decisionList":
		return node("decisionList", `"content":[`+genItems(rng, depth, "decisionItem")+`]`)
	case "blockquote":
		return node("blockquote", `"content":[`+node("paragraph", `"content":[`+genInline(rng)+`]`)+`]`)
	case "expand":
		return node("expand", `"attrs":{"title":"`+pick(rng, "The long version", "")+`"},"content":[`+
			node("paragraph", `"content":[`+genInline(rng)+`]`)+`]`)
	case "mediaSingle":
		return node("mediaSingle", `"content":[{"type":"media","attrs":{"id":"`+
			pick(rng, "3f6b", "a1", "c9d2")+`","type":"file","collection":"jira-issue-1"}}]`)
	case "unknown":
		return node("futureBlock", `"attrs":{"variant":"callout"},"content":[`+
			node("paragraph", `"content":[`+genInline(rng)+`]`)+`]`)
	}
	return node("paragraph", `"content":[`+genInline(rng)+`]`)
}

func genItems(rng *rand.Rand, depth int, item string) string {
	items := make([]string, 0, 3)
	for range rng.IntN(3) + 1 {
		switch item {
		case "taskItem":
			items = append(items, node("taskItem", `"attrs":{"state":"`+pick(rng, "TODO", "DONE")+`"},"content":[`+genInline(rng)+`]`))
		case "decisionItem":
			items = append(items, node("decisionItem", `"attrs":{"state":"DECIDED"},"content":[`+genInline(rng)+`]`))
		default:
			body := node("paragraph", `"content":[`+genInline(rng)+`]`)
			if depth < 2 && rng.IntN(3) == 0 {
				body += "," + genBlock(rng, depth+1)
			}
			items = append(items, node("listItem", `"content":[`+body+`]`))
		}
	}
	return strings.Join(items, ",")
}

func genTable(rng *rand.Rand) string {
	columns, header := rng.IntN(3)+1, rng.IntN(2) == 0
	rows := make([]string, 0, 3)
	for r := range rng.IntN(3) + 1 {
		cells := make([]string, 0, columns)
		for range columns {
			typ := "tableCell"
			if header && r == 0 {
				typ = "tableHeader"
			}
			cells = append(cells, node(typ, `"content":[`+node("paragraph", `"content":[`+genInline(rng)+`]`)+`]`))
		}
		rows = append(rows, node("tableRow", `"content":[`+strings.Join(cells, ",")+`]`))
	}
	return node("table", `"attrs":{"layout":"default"},"content":[`+strings.Join(rows, ",")+`]`)
}

// genInline builds a run of inline nodes. Its words start with a letter on
// purpose: the renderer does not escape prose, so text that begins a line with
// a bullet or a hash reads back as the block it looks like, which is the one
// ambiguity this dialect cannot resolve and which has its own test.
func genInline(rng *rand.Rand) string {
	out := make([]string, 0, 4)
	for range rng.IntN(4) + 1 {
		switch rng.IntN(10) {
		case 0:
			out = append(out, `{"type":"mention","attrs":{"id":"5b10ac8d","text":"@Someone"}}`)
		case 1:
			out = append(out, `{"type":"status","attrs":{"text":"Blocked","color":"red"}}`)
		case 2:
			out = append(out, `{"type":"emoji","attrs":{"shortName":":smile:","id":"1f604","text":"😄"}}`)
		case 3:
			out = append(out, `{"type":"inlineCard","attrs":{"url":"https://example.com/p"}}`)
		case 4:
			out = append(out, `{"type":"text","text":"`+genWords(rng)+`","marks":[`+genMarks(rng)+`]}`)
		case 5:
			// Never first: a hard break with nothing before it on its line is
			// a blank line in markdown, and markdown drops those between
			// blocks. TestParseMarkdown_CannotUndoTheseRenderings covers it.
			if len(out) == 0 {
				out = append(out, text(genWords(rng)))
				continue
			}
			out = append(out, `{"type":"hardBreak"}`, text(genWords(rng)))
		case 6:
			out = append(out, `{"type":"futureInline","attrs":{"x":1}}`)
		default:
			out = append(out, text(genWords(rng)))
		}
	}
	return strings.Join(out, ",")
}

func genWords(rng *rand.Rand) string {
	words := make([]string, 0, 4)
	words = append(words, pick(rng, "basket", "checkout", "order", "retry", "staging"))
	for range rng.IntN(4) {
		words = append(words, pick(rng,
			"total", "empty", "日本語", "e\\u0301cole", "snake_case", "a|b", "x(y)",
			"two  spaces", "trailing", "0", "-", "*", "#", "|", "[x]", "@you"))
	}
	return strings.Join(words, " ")
}

func genMarks(rng *rand.Rand) string {
	all := []string{`{"type":"strong"}`, `{"type":"em"}`, `{"type":"strike"}`,
		`{"type":"underline"}`, `{"type":"code"}`, `{"type":"link","attrs":{"href":"https://example.com/x"}}`}
	rng.Shuffle(len(all), func(i, j int) { all[i], all[j] = all[j], all[i] })
	return strings.Join(all[:rng.IntN(3)+1], ",")
}

func pick(rng *rand.Rand, from ...string) string { return from[rng.IntN(len(from))] }

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var digits []byte
	for n > 0 {
		digits = append([]byte{byte('0' + n%10)}, digits...)
		n /= 10
	}
	return string(digits)
}
