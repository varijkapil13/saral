package jira

import "testing"

func TestRefs_TellAnIdentifierFromSomethingThatWouldLeaveThePath(t *testing.T) {
	t.Parallel()

	tests := []struct {
		in                    string
		key, id, ref, segment bool
	}{
		{in: "EX-1", key: true, ref: true, segment: true},
		{in: "ex-12", key: true, ref: true, segment: true},
		{in: "AB_2C-907", key: true, ref: true, segment: true},
		{in: "10501", id: true, ref: true, segment: true},
		{in: "EX", segment: true},
		{in: "A:B", segment: true},
		{in: "Ü", segment: true},
		{in: ""},
		{in: "."},
		{in: ".."},
		{in: "../../myself"},
		{in: "EX-1/.."},
		{in: "EX-", segment: true},
		{in: "-1", segment: true},
		{in: "1EX-1", segment: true},
		{in: "EX-1 "},
		{in: "EX 1"},
		{in: "EX%2F1"},
		{in: `EX\1`},
		{in: "EX\x00"},
		{in: "EX\u202e"},
		{in: "10501x", segment: true},
		{in: "EX-1?x", segment: true},
	}
	for _, tt := range tests {
		if got := IsIssueKey(tt.in); got != tt.key {
			t.Errorf("IsIssueKey(%q) = %t, want %t", tt.in, got, tt.key)
		}
		if got := IsID(tt.in); got != tt.id {
			t.Errorf("IsID(%q) = %t, want %t", tt.in, got, tt.id)
		}
		if got := IsIssueRef(tt.in); got != tt.ref {
			t.Errorf("IsIssueRef(%q) = %t, want %t", tt.in, got, tt.ref)
		}
		if got := IsPathSegment(tt.in); got != tt.segment {
			t.Errorf("IsPathSegment(%q) = %t, want %t", tt.in, got, tt.segment)
		}
	}
}
