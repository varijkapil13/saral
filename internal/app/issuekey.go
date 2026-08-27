package app

import (
	"net/url"
	"regexp"
	"strings"
)

// issueKey is the shape a Jira issue key has: a project key, a hyphen, and a
// number that does not start with a zero.
//
// It is a shape and never a claim that the issue exists, or even that the site
// has such a project. The project key charset is per-instance and a project can
// be renamed, so only a read can answer that; what this saves is the read for
// something that cannot be a key at all, and the guesswork in telling a key
// from a view name.
var issueKey = regexp.MustCompile(`^[A-Z][A-Z0-9_]*-[1-9]\d*$`)

// ParseKey reads an issue key out of what somebody typed, spelt the way Jira
// spells one. It reports false for anything that is not the shape of a key.
func ParseKey(s string) (string, bool) {
	key := strings.ToUpper(strings.TrimSpace(s))
	if !issueKey.MatchString(key) {
		return "", false
	}
	return key, true
}

// ParseIssueURL reads the issue a Jira URL points at, and the host it points at
// it on.
//
// The host comes back lower-cased so that a URL for another site can be named as
// the mistake it is rather than 404 against this one. It is a host and not a
// site: a caller comparing it with a profile has to normalise that side too,
// because nothing after onboarding checks the shape of what a profile holds.
//
// Three shapes reach a clipboard: /browse/KEY, and the board and backlog URLs
// the web app produces with the issue in ?selectedIssue= and a project key in
// the path. The query is read first for exactly that reason, and the path is
// then read from the end, where every shape that carries a key at all puts it.
func ParseIssueURL(raw string) (key, host string, ok bool) {
	trimmed := strings.TrimSpace(raw)
	if !strings.Contains(trimmed, "/") {
		return "", "", false
	}
	// A pasted URL usually carries its scheme; one copied out of a browser's
	// address bar sometimes does not, and refusing that would be pedantry.
	if !strings.Contains(trimmed, "://") {
		trimmed = "https://" + trimmed
	}
	u, err := url.Parse(trimmed)
	if err != nil || u.Host == "" {
		return "", "", false
	}
	host = strings.ToLower(u.Host)
	if key, found := ParseKey(u.Query().Get("selectedIssue")); found {
		return key, host, true
	}
	segments := strings.Split(u.Path, "/")
	for i := len(segments) - 1; i >= 0; i-- {
		if key, found := ParseKey(segments[i]); found {
			return key, host, true
		}
	}
	return "", "", false
}
