package jira_test

import (
	"slices"
	"testing"

	"github.com/varijkapil13/saral/pkg/jira"
)

// The value the bug was reported against: Jira Cloud answers the sprint field
// with an array of sprint objects, its schema says `array` of `json`, and the
// adapter keeps the bytes rather than dropping the board and the dates to
// arrive at the name.
const sprintValue = `[{"id":42,"name":"DA Sprint 14","state":"active","boardId":7,` +
	`"goal":"ship the thing","startDate":"2026-09-01T09:00:00.000Z","endDate":"2026-09-15T09:00:00.000Z"}]`

func TestFieldValueNames(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name  string
		value jira.FieldValue
		want  []string
	}{
		{
			name:  "a sprint reads as its name and not as its JSON",
			value: jira.FieldValue{Kind: jira.KindUnknown, Text: sprintValue},
			want:  []string{"DA Sprint 14"},
		},
		{
			name: "an issue in two sprints names both, in the order they arrived",
			value: jira.FieldValue{Kind: jira.KindUnknown, Text: `[{"id":41,"name":"DA Sprint 13","state":"closed"},` +
				`{"id":42,"name":"DA Sprint 14","state":"active"}]`},
			want: []string{"DA Sprint 13", "DA Sprint 14"},
		},
		{
			name:  "a lone object is one name",
			value: jira.FieldValue{Kind: jira.KindUnknown, Text: `{"id":9,"name":"Team Rocket"}`},
			want:  []string{"Team Rocket"},
		},
		{
			name:  "value is read where there is no name, which is what an option carries",
			value: jira.FieldValue{Kind: jira.KindUnknown, Text: `[{"id":"1","value":"Needs design"}]`},
			want:  []string{"Needs design"},
		},
		{
			name:  "displayName is read where there is neither, which is what a person carries",
			value: jira.FieldValue{Kind: jira.KindUnknown, Text: `{"accountId":"5b1","displayName":"Ada Lovelace"}`},
			want:  []string{"Ada Lovelace"},
		},
		{
			name:  "a shape with nothing to label answers with nothing",
			value: jira.FieldValue{Kind: jira.KindUnknown, Text: `[{"self":"https://example/1","id":"10"}]`},
			want:  nil,
		},
		{
			name:  "an element with no label is skipped rather than drawn blank",
			value: jira.FieldValue{Kind: jira.KindUnknown, Text: `[{"id":1},{"id":2,"name":"Sprint 3"}]`},
			want:  []string{"Sprint 3"},
		},
		{
			name:  "a string is its own label",
			value: jira.FieldValue{Kind: jira.KindUnknown, Text: `"just text"`},
			want:  []string{"just text"},
		},
		{
			name:  "a number keeps every digit the site sent",
			value: jira.FieldValue{Kind: jira.KindUnknown, Text: `[9007199254740993]`},
			want:  []string{"9007199254740993"},
		},
		{
			name:  "text that is not JSON answers with nothing rather than with itself",
			value: jira.FieldValue{Kind: jira.KindUnknown, Text: `[{"id":42,`},
			want:  nil,
		},
		{
			name:  "a kind with a slot of its own is not this function's question",
			value: jira.FieldValue{Kind: jira.KindText, Text: sprintValue},
			want:  nil,
		},
		{
			name:  "an empty value answers with nothing",
			value: jira.FieldValue{Kind: jira.KindUnknown},
			want:  nil,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if got := tc.value.Names(); !slices.Equal(got, tc.want) {
				t.Errorf("Names() = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestFieldValueCount(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name  string
		value jira.FieldValue
		want  int
	}{
		{"an array is counted", jira.FieldValue{Kind: jira.KindUnknown, Text: `[{"id":1},{"id":2},{"id":3}]`}, 3},
		{"an object is one thing", jira.FieldValue{Kind: jira.KindUnknown, Text: `{"id":1}`}, 1},
		{"an empty array holds nothing", jira.FieldValue{Kind: jira.KindUnknown, Text: `[]`}, 0},
		{"a kind with a slot of its own is not counted", jira.FieldValue{Kind: jira.KindText, Text: `[{"id":1}]`}, 0},
		{"an empty value holds nothing", jira.FieldValue{Kind: jira.KindUnknown}, 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if got := tc.value.Count(); got != tc.want {
				t.Errorf("Count() = %d, want %d", got, tc.want)
			}
		})
	}
}

// The bytes are what an edit writes back, so reading a value must not be a way
// of changing it. Names is the display form and Text stays the payload.
func TestFieldValueNamesLeavesTheBytesAlone(t *testing.T) {
	t.Parallel()

	v := jira.FieldValue{Kind: jira.KindUnknown, Text: sprintValue}
	_ = v.Names()
	_ = v.Count()
	if v.Text != sprintValue {
		t.Errorf("reading the value changed it:\n got %s\nwant %s", v.Text, sprintValue)
	}
}
