package richtext

import (
	"encoding/json"
	"strings"

	"github.com/charmbracelet/x/ansi"

	"github.com/varijkapil13/saral/pkg/adf"
)

// spaces is sliced to build an indent, so that indenting a list item does not
// allocate.
const spaces = "                                "

func indent(n int) string {
	if n > len(spaces) {
		n = len(spaces)
	}
	return spaces[:n]
}

// indentFor returns an indent as wide on screen as the marker it continues.
func indentFor(marker string) string { return indent(ansi.StringWidth(marker)) }

// attrString reads a string attribute, reporting whether it was there and of
// that type — an attribute this package models can still arrive as something
// else, and a document that does that must still render.
func attrString(a adf.Attrs, key string) (string, bool) {
	v, ok := a[key].(string)
	return v, ok
}

// attrInt reads a numeric attribute. JSON numbers decode to float64, but a
// document built in Go carries whatever the caller put there.
func attrInt(a adf.Attrs, key string) (int, bool) {
	switch v := a[key].(type) {
	case float64:
		return int(v), true
	case int:
		return v, true
	case int64:
		return int(v), true
	case json.Number:
		n, err := v.Int64()
		return int(n), err == nil
	default:
		return 0, false
	}
}

// rawText concatenates the text of a run of nodes, which is how a code block
// carries its lines. Nothing is dropped here: a code block's newlines are its
// structure.
func rawText(nodes []adf.Node) string {
	if len(nodes) == 1 {
		return nodes[0].Text
	}
	var b strings.Builder
	for i := range nodes {
		b.WriteString(nodes[i].Text)
	}
	return b.String()
}

func isInline(typ string) bool {
	switch typ {
	case "text", "hardBreak", "mention", "status", "emoji", "date",
		"inlineCard", "inlineEmbedCard", "media", "mediaInline",
		"placeholder", "inlineExtension", "unsupportedInline":
		return true
	default:
		return false
	}
}

func isList(typ string) bool {
	switch typ {
	case "bulletList", "orderedList", "taskList", "decisionList":
		return true
	default:
		return false
	}
}

func inlineOnly(nodes []adf.Node) bool {
	for i := range nodes {
		if !isInline(nodes[i].Type) {
			return false
		}
	}
	return len(nodes) > 0
}

// nodeName is what an unknown node is called on screen: the node it stands in
// for if it is one of ADF's own wrappers for something a client cannot read,
// the app's key if it is an extension, and its own type otherwise.
func nodeName(n adf.Node) string {
	switch n.Type {
	case "unsupportedBlock", "unsupportedInline":
		if original, ok := n.Attrs["originalValue"].(map[string]any); ok {
			if typ, ok := original["type"].(string); ok && typ != "" {
				return sanitize(typ)
			}
		}
	case "extension", "bodiedExtension", "inlineExtension", "multiBodiedExtension":
		if key, ok := attrString(n.Attrs, "extensionKey"); ok && key != "" {
			return sanitize(n.Type) + " " + sanitize(key)
		}
	}
	return sanitize(n.Type)
}

// countFolds counts the expands in a subtree, so that closing one does not
// renumber the folds inside it: an index has to mean the same node whatever the
// reader has opened.
func countFolds(nodes []adf.Node) int {
	n := 0
	for i := range nodes {
		if nodes[i].Type == "expand" || nodes[i].Type == "nestedExpand" {
			n++
		}
		n += countFolds(nodes[i].Content)
	}
	return n
}
