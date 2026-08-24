package adf_test

import (
	"encoding/json"
	"errors"
	"os"
	"strings"
	"testing"

	"github.com/varijkapil13/saral/pkg/adf"
)

// encoded is the JSON a document or a node writes, which is what "byte-stable"
// is measured against.
func encoded(tb testing.TB, d adf.Doc) string {
	tb.Helper()
	b, err := adf.Marshal(d)
	if err != nil {
		tb.Fatalf("encoding: %v", err)
	}
	return string(b)
}

func encodedNode(tb testing.TB, n adf.Node) string {
	tb.Helper()
	b, err := n.MarshalJSON()
	if err != nil {
		tb.Fatalf("encoding: %v", err)
	}
	return string(b)
}

// corpus is the set of documents both round-trip properties are asserted over.
// It is the renderer's own test data plus the fixture, so that a change to
// either shows up here rather than in a golden file nobody re-reads.
func corpus(tb testing.TB) map[string]adf.Doc {
	tb.Helper()
	out := map[string]adf.Doc{"the rich fixture description": fixtureDescription(tb)}
	for name, in := range map[string]string{
		"nested lists and quotes": nestedLists,
		"a table with a header":   wrap(headedTable),
		"a table with no header":  wrap(headlessTable),
		"a ragged table":          wrap(raggedTable),
		"a table of wide runes":   wrap(wideTable),
		"an empty document":       `{"version":1,"type":"doc","content":[]}`,
		"a document that is null": `null`,
		"a paragraph":             wrap(para(text("The basket is empty."))),
		"every heading level":     wrap(node("heading", `"attrs":{"level":1},"content":[`+text("one")+`]`) + "," + node("heading", `"attrs":{"level":6},"content":[`+text("six")+`]`)),
		"every mark":              wrap(para(marked("bold", "strong") + "," + marked("italic", "em") + "," + marked("gone", "strike") + "," + marked("under", "underline") + "," + marked("code()", "code"))),
		"a link":                  wrap(para(`{"type":"text","text":"the runbook","marks":[{"type":"link","attrs":{"href":"https://example.com/x"}}]}`)),
		"a link whose text is its address": wrap(para(
			`{"type":"text","text":"https://example.com/x","marks":[{"type":"link","attrs":{"href":"https://example.com/x"}}]}`)),
		"a link to an address holding a bracket": wrap(para(
			`{"type":"text","text":"wiki","marks":[{"type":"link","attrs":{"href":"https://example.com/a(b)"}}]}`)),
		"a link whose text holds a bracket": wrap(para(
			`{"type":"text","text":"a [b] c","marks":[{"type":"link","attrs":{"href":"https://example.com/x"}}]}`)),
		"inline code holding a backtick": wrap(para(marked("a "+bt+" b", "code"))),
		"a hard break":                   wrap(para(text("first") + `,{"type":"hardBreak"},` + text("second"))),
		"a mention":                      wrap(para(text("Ask ") + `,{"type":"mention","attrs":{"id":"5b10ac8d","text":"@Someone"}},` + text(" about it"))),
		"a status lozenge":               wrap(para(`{"type":"status","attrs":{"text":"Wartet auf Freigabe","color":"yellow"}}`)),
		"an emoji":                       wrap(para(`{"type":"emoji","attrs":{"shortName":":smile:","id":"1f604","text":"😄"}}`)),
		"a date":                         wrap(para(`{"type":"date","attrs":{"timestamp":"1772409600000"}}`)),
		"an inline card":                 wrap(para(`{"type":"inlineCard","attrs":{"url":"https://example.atlassian.net/browse/EX-2"}}`)),
		"an image with a caption": wrap(node("mediaSingle", `"content":[{"type":"media","attrs":{"id":"3f","type":"file"}},`+
			node("caption", `"content":[`+text("Figure 1")+`]`)+`]`)),
		"a group of images": wrap(node("mediaGroup", `"content":[`+
			`{"type":"media","attrs":{"id":"a","type":"file"}},{"type":"media","attrs":{"id":"b","type":"file"}}]`)),
		"a rule between paragraphs":    wrap(para(text("above")) + "," + node("rule", "") + "," + para(text("below"))),
		"a code block":                 wrap(node("codeBlock", `"attrs":{"language":"go"},"content":[`+text("x := 1\\ny := 2")+`]`)),
		"a code block holding a fence": wrap(node("codeBlock", `"content":[`+text("a\\n"+bt+bt+bt+"\\nb")+`]`)),
		"every panel type": wrap(node("panel", `"attrs":{"panelType":"info"},"content":[`+para(text("i"))+`]`) + "," +
			node("panel", `"attrs":{"panelType":"note"},"content":[`+para(text("n"))+`]`) + "," +
			node("panel", `"attrs":{"panelType":"success"},"content":[`+para(text("s"))+`]`) + "," +
			node("panel", `"attrs":{"panelType":"warning"},"content":[`+para(text("w"))+`]`) + "," +
			node("panel", `"attrs":{"panelType":"error"},"content":[`+para(text("e"))+`]`)),
		"a custom panel with its own icon": wrap(node("panel", `"attrs":{"panelType":"custom","panelIconText":"🛠"},"content":[`+para(text("c"))+`]`)),
		"a task list": wrap(node("taskList", `"content":[`+
			node("taskItem", `"attrs":{"state":"DONE"},"content":[`+text("done")+`]`)+`,`+
			node("taskItem", `"attrs":{"state":"TODO"},"content":[`+text("todo")+`]`)+`]`)),
		"a nested task list": wrap(node("taskList", `"content":[`+
			node("taskItem", `"attrs":{"state":"TODO"},"content":[`+text("ship")+`]`)+`,`+
			node("taskList", `"content":[`+node("taskItem", `"attrs":{"state":"DONE"},"content":[`+text("test")+`]`)+`]`)+`,`+
			node("taskItem", `"attrs":{"state":"TODO"},"content":[`+text("tell")+`]`)+`]`)),
		"a decision list": wrap(node("decisionList", `"content":[`+node("decisionItem", `"content":[`+text("ship it")+`]`)+`]`)),
		"an ordered list that does not start at one": wrap(node("orderedList", `"attrs":{"order":9},"content":[`+
			item("nine")+`,`+item("ten")+`,`+item("eleven")+`]`)),
		"a list item with nothing in it": wrap(node("bulletList", `"content":[`+node("listItem", "")+`,`+item("second")+`]`)),
		"an expand":                      wrap(node("expand", `"attrs":{"title":"The long version"},"content":[`+para(text("detail"))+`]`)),
		"an unknown block":               wrap(node("futureBlock", `"attrs":{"variant":"callout"},"content":[`+para(text("kept"))+`]`)),
		"an unknown inline":              wrap(para(text("before ") + `,{"type":"futureInline","attrs":{"x":1}},` + text(" after"))),
		"the node Jira stores for something it could not parse": wrap(
			node("unsupportedBlock", `"attrs":{"originalValue":{"type":"someMacro","attrs":{"k":1}}}`)),
	} {
		out[name] = parse(tb, in)
	}
	return out
}

// TestParseMarkdownInto_LeavesAnUntouchedDocumentByteIdentical is the property
// the packet exists for. Rendering a document and parsing it straight back must
// return the bytes that came in — not an equivalent document, the same one —
// including the node types, attributes and key order this package does not
// model.
func TestParseMarkdownInto_LeavesAnUntouchedDocumentByteIdentical(t *testing.T) {
	t.Parallel()
	for name, d := range corpus(t) {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			for _, opt := range renderOptions() {
				md := adf.MarkdownWith(d, opt)
				got, err := adf.ParseMarkdownInto(d, md, opt)
				if err != nil {
					t.Fatalf("%+v: %v", opt, err)
				}
				if back, want := encoded(t, got), encoded(t, d); back != want {
					t.Errorf("%+v\n--- want ---\n%s\n--- got ---\n%s\n--- markdown ---\n%s", opt, want, back, md)
				}
			}
		})
	}
}

// TestParseMarkdown_ReachesAFixedPointAfterOneRender is the property a parse
// without the original document can hold: what the author saw is what comes
// back. The first render can lose what markdown cannot say; rendering the parse
// of it must not lose anything more.
func TestParseMarkdown_ReachesAFixedPointAfterOneRender(t *testing.T) {
	t.Parallel()
	for name, d := range corpus(t) {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			for _, opt := range editOptions() {
				once := adf.MarkdownWith(d, opt)
				out, err := adf.ParseMarkdownWith(once, opt)
				if errors.Is(err, adf.ErrUnsupportedMarker) {
					// The only refusal this corpus is allowed to provoke, and
					// the reason ParseMarkdownInto exists.
					continue
				}
				if err != nil {
					t.Fatalf("%+v: %v\n%s", opt, err, once)
				}
				if twice := adf.MarkdownWith(out, opt); twice != once {
					t.Errorf("%+v\n--- rendered ---\n%q\n--- re-rendered ---\n%q", opt, once, twice)
				}
			}
		})
	}
}

func renderOptions() []adf.Options {
	return []adf.Options{{}, {ASCII: true}, {TableWidth: 20}, {ASCII: true, TableWidth: 30}}
}

// editOptions are the options markdown meant to be edited is rendered with. A
// TableWidth is missing on purpose: bounding a table truncates its cells with
// an ellipsis and repads the columns, so what comes back is a table of
// truncated text. TestParseMarkdown_CannotUndoTheseRenderings has the case.
func editOptions() []adf.Options { return []adf.Options{{}, {ASCII: true}} }

// TestParseMarkdownInto_KeepsTheBytesJiraSent is what "byte-stable" has to mean
// to be worth anything: not that two documents re-encode to the same canonical
// form, but that the indentation and key order the wire happened to carry come
// back out. A test that marshals both sides would pass on a parser that threw
// the original away.
func TestParseMarkdownInto_KeepsTheBytesJiraSent(t *testing.T) {
	t.Parallel()
	raw, err := os.ReadFile(richFixture)
	if err != nil {
		t.Fatal(err)
	}
	var envelope struct {
		Fields struct {
			Description json.RawMessage `json:"description"`
		} `json:"fields"`
	}
	if err := json.Unmarshal(raw, &envelope); err != nil {
		t.Fatal(err)
	}
	sent := string(envelope.Fields.Description)
	if !strings.Contains(sent, "\n      ") {
		t.Fatal("the fixture is no longer indented, so this test proves nothing")
	}

	d, err := adf.Unmarshal([]byte(sent))
	if err != nil {
		t.Fatal(err)
	}
	out, err := adf.ParseMarkdownInto(d, adf.Markdown(d), adf.Options{})
	if err != nil {
		t.Fatal(err)
	}
	if back := encoded(t, out); back != sent {
		t.Errorf("the document came back re-encoded\n--- sent ---\n%s\n--- back ---\n%s", sent, back)
	}
}

// TestParseMarkdownInto_ReparsesOnlyTheBlockThatChanged proves the half of the
// promise that is not about doing nothing: an edit to one paragraph must not
// re-encode the blocks around it, or an unknown node three blocks away is lost
// to a typo.
func TestParseMarkdownInto_ReparsesOnlyTheBlockThatChanged(t *testing.T) {
	t.Parallel()
	d := fixtureDescription(t)
	md := adf.Markdown(d)
	edited := strings.Replace(md, "The basket total is", "The basket sum is", 1)
	if edited == md {
		t.Fatal("the fixture no longer holds the sentence this test edits")
	}

	out, err := adf.ParseMarkdownInto(d, edited, adf.Options{})
	if err != nil {
		t.Fatal(err)
	}
	if len(out.Content) != len(d.Content) {
		t.Fatalf("the document went from %d blocks to %d", len(d.Content), len(out.Content))
	}
	for i := range out.Content {
		got, want := encodedNode(t, out.Content[i]), encodedNode(t, d.Content[i])
		switch {
		case i == 0 && got == want:
			t.Error("the edited paragraph came back unchanged")
		case i > 0 && got != want:
			t.Errorf("block %d was re-encoded although it was not edited\n got %s\nwant %s", i, got, want)
		}
	}
}

// TestParseMarkdownInto_KeepsAnUnknownNodeAcrossAnEditBesideIt is the case the
// issue names: the marker the renderer leaves for a node type it has never
// heard of carries the type and nothing else, so the only way that node comes
// back is out of the document it came from.
func TestParseMarkdownInto_KeepsAnUnknownNodeAcrossAnEditBesideIt(t *testing.T) {
	t.Parallel()
	d := fixtureDescription(t)
	edited := adf.Markdown(d) + "\n\nOne more note."

	out, err := adf.ParseMarkdownInto(d, edited, adf.Options{})
	if err != nil {
		t.Fatal(err)
	}
	if got := out.NodeTypes()["futureBlock"]; got != 1 {
		t.Fatalf("the unknown node is there %d times, want once", got)
	}
	last := out.Content[len(out.Content)-1]
	if last.Type != "paragraph" {
		t.Fatalf("the appended block came back as %q", last.Type)
	}
	unknown := encodedNode(t, out.Content[len(out.Content)-2])
	if want := encodedNode(t, d.Content[len(d.Content)-1]); unknown != want {
		t.Errorf("the unknown node was rebuilt rather than restored\n got %s\nwant %s", unknown, want)
	}
}

func TestParseMarkdown_RefusesAMarkerItCannotRebuild(t *testing.T) {
	t.Parallel()
	for name, in := range map[string]string{
		"a block":   "> [unsupported: futureBlock]\n> kept",
		"an inline": "before [unsupported: futureInline] after",
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			_, err := adf.ParseMarkdown(in)
			var perr *adf.ParseError
			if !errors.As(err, &perr) {
				t.Fatalf("want a *adf.ParseError, got %v", err)
			}
			if !errors.Is(err, adf.ErrUnsupportedMarker) {
				t.Errorf("want ErrUnsupportedMarker, got %v", err)
			}
			if perr.Line != 1 {
				t.Errorf("want line 1, got %d", perr.Line)
			}
		})
	}
}
