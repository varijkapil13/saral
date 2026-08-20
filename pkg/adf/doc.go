// Package adf models the Atlassian Document Format, the JSON document shape
// that Jira Cloud's REST API v3 requires for descriptions, comments, worklog
// comments and multi-line custom fields.
//
// The model is deliberately lossless. ADF grows node types faster than any
// client can follow, so a document parsed by this package and written back
// unchanged is byte-identical to what came in — including node types, marks
// and attributes this package has never heard of. Only the parts a caller
// actually edits are re-encoded.
package adf

import (
	"bytes"
	"encoding/json"
	"fmt"
	"hash/fnv"
	"sort"
)

// Attrs are a node's or mark's attributes, held as decoded JSON so that
// attributes this package does not model still survive a round trip.
type Attrs map[string]any

// Doc is an ADF document: the root of the tree, always of type "doc".
type Doc struct {
	Version int
	Type    string
	Content []Node

	raw   json.RawMessage
	canon uint64
	extra map[string]json.RawMessage
}

// Node is one node in an ADF document. Which of Content, Marks and Text carry
// anything depends on Type; the ADF schema is not encoded here on purpose,
// because rejecting an unknown shape would lose data.
type Node struct {
	Type    string
	Attrs   Attrs
	Content []Node
	Marks   []Mark
	Text    string

	raw   json.RawMessage
	canon uint64
	extra map[string]json.RawMessage
}

// Mark is an inline annotation on a text node — strong, link, textColor and so
// on.
type Mark struct {
	Type  string
	Attrs Attrs

	raw   json.RawMessage
	canon uint64
	extra map[string]json.RawMessage
}

// Marshal encodes a document with full fidelity.
//
// Prefer it over encoding/json: json.Marshal post-processes whatever a
// MarshalJSON method returns, compacting it and escaping <, > and & into \u
// escapes. The result stays semantically identical and Jira accepts it, but it
// is no longer byte-for-byte what came off the wire.
func Marshal(d Doc) ([]byte, error) { return d.MarshalJSON() }

// Unmarshal parses a document.
func Unmarshal(b []byte) (Doc, error) {
	var d Doc
	err := d.UnmarshalJSON(b)
	return d, err
}

// NewDoc builds a document from scratch.
func NewDoc(content ...Node) Doc {
	return Doc{Version: 1, Type: "doc", Content: content}
}

// NewNode builds a node of the given type with optional children.
func NewNode(typ string, content ...Node) Node {
	return Node{Type: typ, Content: content}
}

// NewText builds a text node.
func NewText(text string, marks ...Mark) Node {
	return Node{Type: "text", Text: text, Marks: marks}
}

// NewMark builds a mark. attrs may be nil.
func NewMark(typ string, attrs Attrs) Mark {
	return Mark{Type: typ, Attrs: attrs}
}

// WithAttrs returns a copy of the node carrying the given attributes.
func (n Node) WithAttrs(a Attrs) Node { n.Attrs = a; return n }

// WithContent returns a copy of the node carrying the given children.
func (n Node) WithContent(c ...Node) Node { n.Content = c; return n }

// WithMarks returns a copy of the node carrying the given marks.
func (n Node) WithMarks(m ...Mark) Node { n.Marks = m; return n }

// IsZero reports whether the document is the zero value, which is how a Jira
// field that is set to null arrives and how it is written back.
func (d Doc) IsZero() bool {
	return d.Version == 0 && d.Type == "" && len(d.Content) == 0 && d.raw == nil
}

// IsEmpty reports whether the document carries no content.
func (d Doc) IsEmpty() bool { return len(d.Content) == 0 }

// Walk calls fn for every node in the document in document order, descending
// into a node's children unless fn returns false for it.
func (d Doc) Walk(fn func(Node) bool) { walk(d.Content, fn) }

func walk(nodes []Node, fn func(Node) bool) {
	for i := range nodes {
		if !fn(nodes[i]) {
			continue
		}
		walk(nodes[i].Content, fn)
	}
}

// NodeTypes counts the node types present, which is how a document's shape is
// surveyed before deciding what a renderer must handle.
func (d Doc) NodeTypes() map[string]int {
	counts := make(map[string]int)
	d.Walk(func(n Node) bool {
		counts[n.Type]++
		return true
	})
	return counts
}

// MarshalJSON writes the document, reproducing the original bytes exactly for
// every subtree that has not been modified since it was parsed.
func (d Doc) MarshalJSON() ([]byte, error) {
	if d.IsZero() {
		return []byte("null"), nil
	}
	var buf bytes.Buffer
	if err := d.write(&buf, true); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

// UnmarshalJSON parses a document, keeping the original bytes so that an
// unmodified document round-trips exactly.
func (d *Doc) UnmarshalJSON(b []byte) error {
	*d = Doc{}
	if bytes.Equal(bytes.TrimSpace(b), []byte("null")) {
		return nil
	}
	fields, err := split(b, "document")
	if err != nil {
		return err
	}
	d.Version, d.Type = 1, "doc"
	for key, value := range fields {
		var err error
		switch key {
		case "version":
			err = json.Unmarshal(value, &d.Version)
		case "type":
			err = json.Unmarshal(value, &d.Type)
		case "content":
			err = json.Unmarshal(value, &d.Content)
		default:
			d.keepExtra(key, value)
			continue
		}
		if err != nil {
			return fmt.Errorf("adf: parsing document %s: %w", key, err)
		}
	}
	if d.Type == "" {
		d.Type = "doc"
	}
	d.raw = append(json.RawMessage(nil), b...)
	d.canon, err = canonHash(d.writeCanonical)
	return err
}

func (d *Doc) keepExtra(key string, value json.RawMessage) {
	if d.extra == nil {
		d.extra = make(map[string]json.RawMessage, 1)
	}
	d.extra[key] = value
}

func (d Doc) writeCanonical(buf *bytes.Buffer) error { return d.write(buf, false) }

func (d Doc) write(buf *bytes.Buffer, verbatim bool) error {
	if verbatim {
		if done, err := writeVerbatim(buf, d.raw, d.canon, d.writeCanonical); done || err != nil {
			return err
		}
	}
	version, typ := d.Version, d.Type
	if version == 0 {
		version = 1
	}
	if typ == "" {
		typ = "doc"
	}
	buf.WriteString(`{"version":`)
	fmt.Fprintf(buf, "%d", version)
	buf.WriteString(`,"type":`)
	if err := writeJSON(buf, typ); err != nil {
		return err
	}
	buf.WriteString(`,"content":`)
	if err := writeNodes(buf, d.Content, verbatim); err != nil {
		return err
	}
	if err := writeExtra(buf, d.extra); err != nil {
		return err
	}
	buf.WriteByte('}')
	return nil
}

// MarshalJSON writes the node, reproducing the original bytes exactly when it
// and everything below it are unmodified.
func (n Node) MarshalJSON() ([]byte, error) {
	var buf bytes.Buffer
	if err := n.write(&buf, true); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

// UnmarshalJSON parses a node, keeping the original bytes and any keys this
// package does not model.
func (n *Node) UnmarshalJSON(b []byte) error {
	*n = Node{}
	fields, err := split(b, "node")
	if err != nil {
		return err
	}
	for key, value := range fields {
		var err error
		switch key {
		case "type":
			err = json.Unmarshal(value, &n.Type)
		case "attrs":
			err = json.Unmarshal(value, &n.Attrs)
		case "content":
			err = json.Unmarshal(value, &n.Content)
		case "marks":
			err = json.Unmarshal(value, &n.Marks)
		case "text":
			err = json.Unmarshal(value, &n.Text)
		default:
			if n.extra == nil {
				n.extra = make(map[string]json.RawMessage, 1)
			}
			n.extra[key] = value
			continue
		}
		if err != nil {
			return fmt.Errorf("adf: parsing node %s: %w", key, err)
		}
	}
	n.raw = append(json.RawMessage(nil), b...)
	n.canon, err = canonHash(n.writeCanonical)
	return err
}

func (n Node) writeCanonical(buf *bytes.Buffer) error { return n.write(buf, false) }

func (n Node) write(buf *bytes.Buffer, verbatim bool) error {
	if verbatim {
		if done, err := writeVerbatim(buf, n.raw, n.canon, n.writeCanonical); done || err != nil {
			return err
		}
		if n.Type == "text" && n.Text == "" {
			return fmt.Errorf("adf: a text node must carry text, and this one is empty")
		}
	}
	buf.WriteString(`{"type":`)
	if err := writeJSON(buf, n.Type); err != nil {
		return err
	}
	if len(n.Attrs) > 0 {
		buf.WriteString(`,"attrs":`)
		if err := writeJSON(buf, n.Attrs); err != nil {
			return err
		}
	}
	if len(n.Content) > 0 {
		buf.WriteString(`,"content":`)
		if err := writeNodes(buf, n.Content, verbatim); err != nil {
			return err
		}
	}
	if len(n.Marks) > 0 {
		buf.WriteString(`,"marks":[`)
		for i := range n.Marks {
			if i > 0 {
				buf.WriteByte(',')
			}
			if err := n.Marks[i].write(buf, verbatim); err != nil {
				return err
			}
		}
		buf.WriteByte(']')
	}
	if n.Text != "" {
		buf.WriteString(`,"text":`)
		if err := writeJSON(buf, n.Text); err != nil {
			return err
		}
	}
	if err := writeExtra(buf, n.extra); err != nil {
		return err
	}
	buf.WriteByte('}')
	return nil
}

// MarshalJSON writes the mark, reproducing the original bytes when unmodified.
func (m Mark) MarshalJSON() ([]byte, error) {
	var buf bytes.Buffer
	if err := m.write(&buf, true); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

// UnmarshalJSON parses a mark, keeping the original bytes.
func (m *Mark) UnmarshalJSON(b []byte) error {
	*m = Mark{}
	fields, err := split(b, "mark")
	if err != nil {
		return err
	}
	for key, value := range fields {
		var err error
		switch key {
		case "type":
			err = json.Unmarshal(value, &m.Type)
		case "attrs":
			err = json.Unmarshal(value, &m.Attrs)
		default:
			if m.extra == nil {
				m.extra = make(map[string]json.RawMessage, 1)
			}
			m.extra[key] = value
			continue
		}
		if err != nil {
			return fmt.Errorf("adf: parsing mark %s: %w", key, err)
		}
	}
	m.raw = append(json.RawMessage(nil), b...)
	m.canon, err = canonHash(m.writeCanonical)
	return err
}

func (m Mark) writeCanonical(buf *bytes.Buffer) error { return m.write(buf, false) }

func (m Mark) write(buf *bytes.Buffer, verbatim bool) error {
	if verbatim {
		if done, err := writeVerbatim(buf, m.raw, m.canon, m.writeCanonical); done || err != nil {
			return err
		}
	}
	buf.WriteString(`{"type":`)
	if err := writeJSON(buf, m.Type); err != nil {
		return err
	}
	if len(m.Attrs) > 0 {
		buf.WriteString(`,"attrs":`)
		if err := writeJSON(buf, m.Attrs); err != nil {
			return err
		}
	}
	if err := writeExtra(buf, m.extra); err != nil {
		return err
	}
	buf.WriteByte('}')
	return nil
}

// writeVerbatim emits the bytes a value was parsed from, if the canonical form
// of the value still hashes to what it hashed to then. Comparing canonical
// forms rather than trusting a dirty flag is what makes verbatim output safe
// even though the model's fields are freely assignable.
//
// It short-circuits the whole subtree, so an edit costs a re-encode only along
// the path from the root to the node that changed; the untouched siblings come
// straight off their original bytes.
func writeVerbatim(buf *bytes.Buffer, raw json.RawMessage, want uint64, canonical func(*bytes.Buffer) error) (bool, error) {
	if raw == nil {
		return false, nil
	}
	var scratch bytes.Buffer
	if err := canonical(&scratch); err != nil {
		return false, err
	}
	if hashBytes(scratch.Bytes()) != want {
		return false, nil
	}
	buf.Write(raw)
	return true, nil
}

func canonHash(write func(*bytes.Buffer) error) (uint64, error) {
	var buf bytes.Buffer
	if err := write(&buf); err != nil {
		return 0, err
	}
	return hashBytes(buf.Bytes()), nil
}

func hashBytes(b []byte) uint64 {
	h := fnv.New64a()
	_, _ = h.Write(b)
	return h.Sum64()
}

func writeNodes(buf *bytes.Buffer, nodes []Node, verbatim bool) error {
	buf.WriteByte('[')
	for i := range nodes {
		if i > 0 {
			buf.WriteByte(',')
		}
		if err := nodes[i].write(buf, verbatim); err != nil {
			return err
		}
	}
	buf.WriteByte(']')
	return nil
}

func writeJSON(buf *bytes.Buffer, v any) error {
	b, err := json.Marshal(v)
	if err != nil {
		return fmt.Errorf("adf: encoding: %w", err)
	}
	buf.Write(b)
	return nil
}

func writeExtra(buf *bytes.Buffer, extra map[string]json.RawMessage) error {
	if len(extra) == 0 {
		return nil
	}
	keys := make([]string, 0, len(extra))
	for k := range extra {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		buf.WriteByte(',')
		if err := writeJSON(buf, k); err != nil {
			return err
		}
		buf.WriteByte(':')
		buf.Write(extra[k])
	}
	return nil
}

// split takes an object apart into its keys without decoding the values, so
// that each key is decoded once and keys this package does not model can be
// held on to verbatim.
func split(b []byte, what string) (map[string]json.RawMessage, error) {
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(b, &fields); err != nil {
		return nil, fmt.Errorf("adf: parsing %s: %w", what, err)
	}
	return fields, nil
}
