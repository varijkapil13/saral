package adf_test

import (
	"bytes"
	"encoding/json"
	"fmt"
	"reflect"
	"testing"

	"github.com/varijkapil13/saral/pkg/adf"
)

// richDoc exercises a node type, a mark and an attribute this package models,
// alongside ones it does not.
const richDoc = `{"version":1,"type":"doc","content":[{"type":"paragraph","content":[{"type":"text","text":"see ","marks":[]},{"type":"text","text":"the docs","marks":[{"type":"link","attrs":{"href":"https://example.com"}},{"type":"strong"}]}]},{"type":"someFutureNode","attrs":{"kind":7,"nested":{"b":true,"a":[1,2]}},"content":[{"type":"text","text":"kept"}],"unknownKey":{"z":1}},{"type":"codeBlock","attrs":{"language":"go"},"content":[{"type":"text","text":"x := 1\n"}]}]}`

func TestUnmarshalMarshal_IsByteStableForAnUntouchedDocument(t *testing.T) {
	t.Parallel()
	for name, in := range map[string]string{
		"a rich document":                    richDoc,
		"an empty document":                  `{"version":1,"type":"doc","content":[]}`,
		"keys in an unexpected order":        `{"content":[{"text":"hi","type":"text"}],"type":"doc","version":1}`,
		"a document with whitespace":         "{\n  \"version\": 1,\n  \"type\": \"doc\",\n  \"content\": []\n}",
		"an unmodelled top-level key":        `{"version":1,"type":"doc","content":[],"meta":{"x":1}}`,
		"a mark carrying unmodelled attrs":   `{"version":1,"type":"doc","content":[{"type":"text","text":"a","marks":[{"type":"textColor","attrs":{"color":"#ff0000","alpha":0.5}}]}]}`,
		"characters json would escape":       `{"version":1,"type":"doc","content":[{"type":"text","text":"a < b && c > d"}]}`,
		"a document at a future ADF version": `{"version":2,"type":"doc","content":[]}`,
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			d, err := adf.Unmarshal([]byte(in))
			if err != nil {
				t.Fatalf("unmarshal: %v", err)
			}
			out, err := adf.Marshal(d)
			if err != nil {
				t.Fatalf("marshal: %v", err)
			}
			if string(out) != in {
				t.Errorf("round trip changed the bytes\n got: %s\nwant: %s", out, in)
			}
		})
	}
}

func TestStdlibMarshal_StaysValidButCompactsAndEscapes(t *testing.T) {
	t.Parallel()
	const in = "{\n  \"version\": 1,\n  \"type\": \"doc\",\n  \"content\": [{\"type\":\"text\",\"text\":\"a < b\"}]\n}"
	d, err := adf.Unmarshal([]byte(in))
	if err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	out, err := json.Marshal(d)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	const want = `{"version":1,"type":"doc","content":[{"type":"text","text":"a \u003c b"}]}`
	if string(out) != want {
		t.Errorf("got  %s\nwant %s", out, want)
	}
	back, err := adf.Unmarshal(out)
	if err != nil {
		t.Fatalf("reparse: %v", err)
	}
	if got := back.Content[0].Text; got != "a < b" {
		t.Errorf("escaping changed the text: %q", got)
	}
}

// preciseDoc carries values a canonical re-encode through float64 would change:
// an integer beyond 2^53 and a float written with a trailing zero.
const preciseDoc = `{"version":1,"type":"doc","content":[` +
	`{"type":"paragraph","content":[{"type":"text","text":"before"}]},` +
	`{"type":"extension","attrs":{"extensionType":"com.acme.app","extensionKey":"chart","parameters":{"recordId":9007199254740993,"scale":1.0}}},` +
	`{"type":"paragraph","content":[{"type":"text","text":"after"}]}]}`

func TestMarshal_LeavesAnUntouchedSiblingByteForByteAlone(t *testing.T) {
	t.Parallel()
	d, err := adf.Unmarshal([]byte(preciseDoc))
	if err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	d.Content[0].Content[0].Text = "BEFORE"

	out, err := adf.Marshal(d)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	const sibling = `{"type":"extension","attrs":{"extensionType":"com.acme.app","extensionKey":"chart","parameters":{"recordId":9007199254740993,"scale":1.0}}}`
	if !containsAll(string(out), sibling) {
		t.Errorf("the untouched sibling was re-encoded, losing precision and key order:\n got: %s\nwant it to contain: %s", out, sibling)
	}
	if !containsAll(string(out), `"text":"BEFORE"`) {
		t.Errorf("the edit did not survive: %s", out)
	}
}

func TestMarshal_ReEncodesOnlyWhatChanged(t *testing.T) {
	t.Parallel()
	d, err := adf.Unmarshal([]byte(richDoc))
	if err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	d.Content[0].Content[0].Text = "look at "

	out, err := adf.Marshal(d)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	back, err := adf.Unmarshal(out)
	if err != nil {
		t.Fatalf("reparse: %v", err)
	}
	if got := back.Content[0].Content[0].Text; got != "look at " {
		t.Errorf("edit did not survive: got %q", got)
	}
	// The untouched sibling subtree keeps its unmodelled key and attrs.
	if !containsAll(string(out), `{"type":"someFutureNode","attrs":{"kind":7,"nested":{"b":true,"a":[1,2]}},"content":[{"type":"text","text":"kept"}],"unknownKey":{"z":1}}`) {
		t.Errorf("the untouched sibling was not emitted verbatim: %s", out)
	}
}

func TestMarshal_RefusesToWriteAnEmptyTextNodeItWasAskedToBuild(t *testing.T) {
	t.Parallel()
	if _, err := adf.Marshal(adf.NewDoc(adf.NewNode("paragraph", adf.NewText("")))); err == nil {
		t.Error("an empty text node marshalled; ADF rejects one and so should this")
	}
	// A parsed document is still reproduced exactly, whatever it contains.
	const odd = `{"version":1,"type":"doc","content":[{"type":"text","text":""}]}`
	d, err := adf.Unmarshal([]byte(odd))
	if err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	out, err := adf.Marshal(d)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if string(out) != odd {
		t.Errorf("got %s, want %s", out, odd)
	}
}

func TestMarshal_FillsInTheVersionAndTypeOfADocumentBuiltFieldByField(t *testing.T) {
	t.Parallel()
	var d adf.Doc
	d.Content = []adf.Node{adf.NewNode("paragraph", adf.NewText("hi"))}

	out, err := adf.Marshal(d)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	const want = `{"version":1,"type":"doc","content":[{"type":"paragraph","content":[{"type":"text","text":"hi"}]}]}`
	if string(out) != want {
		t.Errorf("got  %s\nwant %s", out, want)
	}
}

func TestMarshal_KeepsUnmodelledKeysOnAModifiedNode(t *testing.T) {
	t.Parallel()
	var d adf.Doc
	if err := json.Unmarshal([]byte(richDoc), &d); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	d.Content[1].Attrs["kind"] = 8

	out, err := json.Marshal(d)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if !containsAll(string(out), `"kind":8`, `"unknownKey":{"z":1}`) {
		t.Errorf("a modified node dropped its unmodelled key: %s", out)
	}
}

func TestUnmarshal_NullBecomesTheZeroDocumentAndWritesBackAsNull(t *testing.T) {
	t.Parallel()
	var d adf.Doc
	if err := json.Unmarshal([]byte("null"), &d); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if !d.IsZero() {
		t.Error("null should parse to the zero document")
	}
	out, err := json.Marshal(d)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if string(out) != "null" {
		t.Errorf("zero document wrote %s, want null", out)
	}
}

func TestUnmarshal_DefaultsVersionAndType(t *testing.T) {
	t.Parallel()
	var d adf.Doc
	if err := json.Unmarshal([]byte(`{"content":[]}`), &d); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if d.Version != 1 || d.Type != "doc" {
		t.Errorf("got version %d type %q, want 1 and doc", d.Version, d.Type)
	}
}

func TestUnmarshal_RejectsMalformedJSON(t *testing.T) {
	t.Parallel()
	var d adf.Doc
	if err := json.Unmarshal([]byte(`{"content":[{"type":}]}`), &d); err == nil {
		t.Error("malformed JSON parsed without error")
	}
}

func TestNewDoc_MarshalsAsAnEmptyDocument(t *testing.T) {
	t.Parallel()
	out, err := json.Marshal(adf.NewDoc())
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if string(out) != `{"version":1,"type":"doc","content":[]}` {
		t.Errorf("got %s", out)
	}
}

func TestBuilders_ProduceTheExpectedShape(t *testing.T) {
	t.Parallel()
	d := adf.NewDoc(
		adf.NewNode("paragraph",
			adf.NewText("bold", adf.NewMark("strong", nil)),
			adf.NewText(" and a link", adf.NewMark("link", adf.Attrs{"href": "https://example.com"})),
		),
		adf.NewNode("codeBlock", adf.NewText("x")).WithAttrs(adf.Attrs{"language": "go"}),
	)
	out, err := json.Marshal(d)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	const want = `{"version":1,"type":"doc","content":[{"type":"paragraph","content":[{"type":"text","marks":[{"type":"strong"}],"text":"bold"},{"type":"text","marks":[{"type":"link","attrs":{"href":"https://example.com"}}],"text":" and a link"}]},{"type":"codeBlock","attrs":{"language":"go"},"content":[{"type":"text","text":"x"}]}]}`
	if string(out) != want {
		t.Errorf("got  %s\nwant %s", out, want)
	}
}

func TestWalkAndNodeTypes(t *testing.T) {
	t.Parallel()
	var d adf.Doc
	if err := json.Unmarshal([]byte(richDoc), &d); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	want := map[string]int{"paragraph": 1, "text": 4, "someFutureNode": 1, "codeBlock": 1}
	if got := d.NodeTypes(); !reflect.DeepEqual(got, want) {
		t.Errorf("census: got %v want %v", got, want)
	}

	var visited int
	d.Walk(func(n adf.Node) bool {
		visited++
		return n.Type != "someFutureNode"
	})
	if visited != 6 {
		t.Errorf("refusing to descend visited %d nodes, want 6", visited)
	}
}

func TestIsEmpty(t *testing.T) {
	t.Parallel()
	if !adf.NewDoc().IsEmpty() {
		t.Error("a document with no content is empty")
	}
	if adf.NewDoc(adf.NewText("x")).IsEmpty() {
		t.Error("a document with content is not empty")
	}
}

func containsAll(s string, subs ...string) bool {
	for _, sub := range subs {
		found := false
		for i := 0; i+len(sub) <= len(s); i++ {
			if s[i:i+len(sub)] == sub {
				found = true
				break
			}
		}
		if !found {
			return false
		}
	}
	return true
}

func TestNodeAndMark_MarshalOnTheirOwn(t *testing.T) {
	t.Parallel()
	const in = `{"type":"text","text":"hi","marks":[{"type":"link","attrs":{"href":"https://example.com"}}]}`
	var n adf.Node
	if err := json.Unmarshal([]byte(in), &n); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	out, err := n.MarshalJSON()
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if string(out) != in {
		t.Errorf("a standalone node did not round-trip\n got: %s\nwant: %s", out, in)
	}

	mark, err := n.Marks[0].MarshalJSON()
	if err != nil {
		t.Fatalf("marshal mark: %v", err)
	}
	if want := `{"type":"link","attrs":{"href":"https://example.com"}}`; string(mark) != want {
		t.Errorf("a standalone mark did not round-trip\n got: %s\nwant: %s", mark, want)
	}

	n.Text = "bye"
	out, err = n.MarshalJSON()
	if err != nil {
		t.Fatalf("marshal after edit: %v", err)
	}
	if !containsAll(string(out), `"text":"bye"`, `"href":"https://example.com"`) {
		t.Errorf("editing a standalone node lost something: %s", out)
	}
}

func TestWithHelpers_ReplaceWhatTheyName(t *testing.T) {
	t.Parallel()
	n := adf.NewNode("paragraph", adf.NewText("old")).
		WithContent(adf.NewText("new")).
		WithMarks(adf.NewMark("strong", nil)).
		WithAttrs(adf.Attrs{"localId": "abc"})

	out, err := adf.Marshal(adf.NewDoc(n))
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	const want = `{"version":1,"type":"doc","content":[{"type":"paragraph","attrs":{"localId":"abc"},"content":[{"type":"text","text":"new"}],"marks":[{"type":"strong"}]}]}`
	if string(out) != want {
		t.Errorf("got  %s\nwant %s", out, want)
	}
}

func TestMarshal_ReportsAnAttributeItCannotEncode(t *testing.T) {
	t.Parallel()
	for name, doc := range map[string]adf.Doc{
		"on a node": adf.NewDoc(adf.NewNode("panel").WithAttrs(adf.Attrs{"bad": make(chan int)})),
		"on a mark": adf.NewDoc(adf.NewText("x", adf.NewMark("link", adf.Attrs{"bad": make(chan int)}))),
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			if _, err := adf.Marshal(doc); err == nil {
				t.Error("an unencodable attribute marshalled without error")
			}
		})
	}
}

func TestUnmarshal_RejectsAMalformedNodeOrMark(t *testing.T) {
	t.Parallel()
	for name, in := range map[string]string{
		"a node that is not an object": `{"version":1,"type":"doc","content":["nope"]}`,
		"a mark that is not an object": `{"version":1,"type":"doc","content":[{"type":"text","text":"a","marks":[7]}]}`,
		"attrs that are not an object": `{"version":1,"type":"doc","content":[{"type":"panel","attrs":"nope"}]}`,
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			if _, err := adf.Unmarshal([]byte(in)); err == nil {
				t.Error("malformed content parsed without error")
			}
		})
	}
}

// benchDoc is a description of the size a real ticket reaches: prose, a list,
// a code block and a table.
func benchDoc(b *testing.B) []byte {
	b.Helper()
	var buf bytes.Buffer
	buf.WriteString(`{"version":1,"type":"doc","content":[`)
	for i := range 40 {
		if i > 0 {
			buf.WriteByte(',')
		}
		fmt.Fprintf(&buf, `{"type":"paragraph","content":[{"type":"text","text":"paragraph %d with "},`+
			`{"type":"text","text":"emphasis","marks":[{"type":"strong"},{"type":"em"}]},`+
			`{"type":"text","text":" and a "},`+
			`{"type":"text","text":"link","marks":[{"type":"link","attrs":{"href":"https://example.com/%d"}}]}]}`, i, i)
	}
	buf.WriteString(`,{"type":"codeBlock","attrs":{"language":"go"},"content":[{"type":"text","text":"func main() {}\n"}]}]}`)
	return buf.Bytes()
}

func BenchmarkUnmarshal(b *testing.B) {
	in := benchDoc(b)
	b.ReportAllocs()
	b.SetBytes(int64(len(in)))
	b.ResetTimer()
	for range b.N {
		if _, err := adf.Unmarshal(in); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkMarshalUntouched(b *testing.B) {
	d, err := adf.Unmarshal(benchDoc(b))
	if err != nil {
		b.Fatal(err)
	}
	b.ReportAllocs()
	b.ResetTimer()
	for range b.N {
		if _, err := adf.Marshal(d); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkMarshalEdited(b *testing.B) {
	d, err := adf.Unmarshal(benchDoc(b))
	if err != nil {
		b.Fatal(err)
	}
	d.Content[0].Content[0].Text = "edited"
	b.ReportAllocs()
	b.ResetTimer()
	for range b.N {
		if _, err := adf.Marshal(d); err != nil {
			b.Fatal(err)
		}
	}
}

func TestClone_SharesNothingWithTheOriginal(t *testing.T) {
	t.Parallel()
	original, err := adf.Unmarshal([]byte(richDoc))
	if err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	before, err := adf.Marshal(original)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}

	clone := original.Clone()
	clone.Content[0].Content[0].Text = "edited"
	clone.Content[1].Attrs["kind"] = 99
	clone.Content[1].Attrs["nested"].(map[string]any)["a"] = []any{9}
	clone.Content[0].Content[1].Marks[0].Attrs["href"] = "https://elsewhere.example"
	clone.Content = append(clone.Content, adf.NewText("extra"))

	after, err := adf.Marshal(original)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if !bytes.Equal(after, before) {
		t.Errorf("editing the clone changed the original\n got: %s\nwant: %s", after, before)
	}

	// And the clone is still a document, not a shallow husk.
	out, err := adf.Marshal(clone)
	if err != nil {
		t.Fatalf("marshal clone: %v", err)
	}
	if !containsAll(string(out), `"text":"edited"`, `"kind":99`, `https://elsewhere.example`) {
		t.Errorf("the clone did not keep its edits: %s", out)
	}
}

func TestClone_OfAnUntouchedDocumentStillRoundTripsByteStably(t *testing.T) {
	t.Parallel()
	original, err := adf.Unmarshal([]byte(richDoc))
	if err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	out, err := adf.Marshal(original.Clone())
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if string(out) != richDoc {
		t.Errorf("cloning cost the document its verbatim bytes\n got: %s\nwant: %s", out, richDoc)
	}
}
