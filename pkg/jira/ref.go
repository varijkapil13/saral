package jira

import (
	"regexp"
	"strings"
	"unicode"
)

var (
	issueKeyPattern  = regexp.MustCompile(`^[A-Za-z][A-Za-z0-9_]*-\d+$`)
	numericIDPattern = regexp.MustCompile(`^\d+$`)
)

// IsIssueKey reports whether s is written the way Jira Cloud writes an issue
// key: a project key, a hyphen and the issue's number.
func IsIssueKey(s string) bool { return issueKeyPattern.MatchString(s) }

// IsID reports whether s is one of the numeric ids Jira gives an issue, a
// comment, an attachment, a version or an issue type.
func IsID(s string) bool { return numericIDPattern.MatchString(s) }

// IsIssueRef reports whether s names an issue either way the REST API takes
// one: by key or by numeric id.
func IsIssueRef(s string) bool { return IsIssueKey(s) || IsID(s) }

// IsPathSegment reports whether s can stand as one segment of a request path
// once escaped: it is not empty, is not a dot segment a server would resolve
// against its parent, and carries no separator, percent sign, whitespace,
// control or invisible format character that an intermediary might split,
// decode or normalise on.
//
// It is the check for a value whose format the site decides, such as a project
// key, where anything stricter would refuse a spelling some site uses.
func IsPathSegment(s string) bool {
	if s == "" || s == "." || s == ".." {
		return false
	}
	return !strings.ContainsFunc(s, func(r rune) bool {
		return r == '/' || r == '\\' || r == '%' || r == unicode.ReplacementChar ||
			unicode.IsSpace(r) || unicode.IsControl(r) || unicode.Is(unicode.Cf, r)
	})
}
