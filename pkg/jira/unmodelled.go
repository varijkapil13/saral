package jira

import (
	"bytes"
	"encoding/json"
	"strings"
)

// nameKeys are the keys a Jira structure carries a human label under, most
// preferred first. There is no schema left to consult by the time a value is
// KindUnknown — that is what made it unknown — so the label is found by the
// shape of what arrived, which is the same rule the adapter infers a value by.
var nameKeys = [...]string{"name", "value", "displayName", "label", "text", "summary", "key"}

// Names is what a value this client has no slot for reads as: the labels
// carried inside it, in the order they arrived.
//
// A sprint field is the everyday one. Its schema says `array` of `json`, which
// is a shape the adapter deliberately keeps verbatim rather than guessing at —
// turning a sprint into its name would drop its board, its state and its dates,
// and the bytes are what an edit writes back. So the value holds JSON, and
// something else has to answer what a person should see, which is this.
//
// It answers only for KindUnknown; every other kind has a slot of its own and a
// caller reads that. It answers with nothing for a shape carrying no label at
// all — a worklog, an issue link — and the caller says so in its own words
// rather than drawing the bytes, which clipped to a sidebar's width say nothing.
func (v FieldValue) Names() []string {
	if v.Kind != KindUnknown || strings.TrimSpace(v.Text) == "" {
		return nil
	}
	dec := json.NewDecoder(strings.NewReader(v.Text))
	// A Jira id is an integer that float64 rounds once it is large enough, and a
	// label falling back to one has to be the digits the site sent.
	dec.UseNumber()
	var parsed any
	if err := dec.Decode(&parsed); err != nil {
		return nil
	}
	if list, ok := parsed.([]any); ok {
		out := make([]string, 0, len(list))
		for _, item := range list {
			if name := labelOf(item); name != "" {
				out = append(out, name)
			}
		}
		if len(out) == 0 {
			return nil
		}
		return out
	}
	if name := labelOf(parsed); name != "" {
		return []string{name}
	}
	return nil
}

// labelOf is the one label a decoded JSON value carries, and "" for a shape
// that carries none.
func labelOf(v any) string {
	switch value := v.(type) {
	case string:
		return strings.TrimSpace(value)
	case json.Number:
		return value.String()
	case bool:
		if value {
			return "yes"
		}
		return "no"
	case map[string]any:
		for _, key := range nameKeys {
			if name, ok := value[key].(string); ok && strings.TrimSpace(name) != "" {
				return strings.TrimSpace(name)
			}
		}
		return ""
	default:
		return ""
	}
}

// isJSONArray reports whether a raw value is a JSON array, so that a caller with
// nothing to label can still say how many things it could not read.
func isJSONArray(text string) bool {
	trimmed := bytes.TrimSpace([]byte(text))
	return len(trimmed) > 0 && trimmed[0] == '['
}

// Count is how many things an unlabellable value holds, and 1 for a value that
// is one thing. It is what a caller says instead of drawing bytes: "3 entries"
// is a true sentence about a worklog where its JSON is not one anybody reads.
func (v FieldValue) Count() int {
	if v.Kind != KindUnknown || strings.TrimSpace(v.Text) == "" {
		return 0
	}
	if !isJSONArray(v.Text) {
		return 1
	}
	var list []json.RawMessage
	if err := json.Unmarshal([]byte(v.Text), &list); err != nil {
		return 1
	}
	return len(list)
}
