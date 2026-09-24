package jira

import (
	"fmt"
	"strings"
	"time"

	"github.com/varijkapil13/saral/pkg/adf"
)

// RankPosition says where RankIssues puts the issues it is given: immediately
// before one issue or immediately after one, and never both. The issues keep
// the order they were given in.
//
// FieldID is the rank field to write, which is BoardConfig.RankFieldID for the
// board the reorder happened on. Empty leaves the choice to the site, which
// writes its default rank field.
type RankPosition struct {
	Before  string
	After   string
	FieldID string
}

// RankBefore places issues immediately before key.
func RankBefore(key string) RankPosition { return RankPosition{Before: key} }

// RankAfter places issues immediately after key.
func RankAfter(key string) RankPosition { return RankPosition{After: key} }

// Anchor is the issue the position is relative to, and whether the issues go
// after it rather than before.
func (p RankPosition) Anchor() (key string, after bool) {
	if before := strings.TrimSpace(p.Before); before != "" {
		return before, false
	}
	return strings.TrimSpace(p.After), true
}

// RankFailure is one issue the site refused to rank, in its own words.
type RankFailure struct {
	Key    string
	Reason string
}

// PartialRankError reports a rank that did not land for every issue.
//
// The rank endpoint takes fifty issues per call and answers each call issue by
// issue, so a reorder can fail in two ways at once. Failed are the issues the
// site answered for and refused; the rest of their call ranked. Pending are the
// issues never sent, because a whole call was refused or never answered and Err
// says why. A view rolling an optimistic reorder back rolls back Failed and
// Pending and keeps Ranked.
type PartialRankError struct {
	Ranked  []string
	Failed  []RankFailure
	Pending []string
	Err     error
}

func (e *PartialRankError) Error() string {
	total := len(e.Ranked) + len(e.Failed) + len(e.Pending)
	msg := fmt.Sprintf("ranked %d of %d issues", len(e.Ranked), total)
	if len(e.Failed) > 0 {
		msg += fmt.Sprintf("; the site refused %d: %s", len(e.Failed), e.Failed[0].Key)
		if e.Failed[0].Reason != "" {
			msg += " (" + e.Failed[0].Reason + ")"
		}
	}
	if e.Err != nil {
		msg += fmt.Sprintf("; %d were not sent: %v", len(e.Pending), e.Err)
	}
	return msg
}

func (e *PartialRankError) Unwrap() error { return e.Err }

// LinkType is one kind of relationship a site lets issues have. Its names are
// the site's own and are localised, so a link is written by ID.
//
// Outward is the phrase read from the issue a link starts at — "blocks" — and
// Inward the phrase read from the one it ends at — "is blocked by".
type LinkType struct {
	ID      string
	Name    string
	Inward  string
	Outward string
}

// LinkInput is a link to create: From <Outward phrase> To, so that From blocks
// To for a type whose outward phrase is "blocks". Read back, the link is
// LinkOutward on From and LinkInward on To.
type LinkInput struct {
	TypeID string
	From   string
	To     string
}

// Worklog is time logged against an issue.
type Worklog struct {
	ID           string
	IssueID      string
	Author       User
	UpdateAuthor *User
	Comment      adf.Doc
	Started      time.Time
	Spent        time.Duration
	Created      time.Time
	Updated      time.Time
	Visibility   *Visibility
}

// WorklogInput is time to log. Spent goes out in seconds rather than as the
// site's own "1d 2h" notation, because how long a day is in that notation is
// site configuration. Started is when the work began and is required.
type WorklogInput struct {
	Spent   time.Duration
	Started time.Time
	Comment adf.Doc
}

// WatcherList is who is watching an issue. Count is the site's number and
// People is who it was willing to name: a token without the permission to view
// voters and watchers gets the count and nobody, so People can be shorter than
// Count and an empty People is not an issue nobody watches.
type WatcherList struct {
	Count    int
	Watching bool
	People   []User
}

// DeploymentType is what a site says it is.
type DeploymentType string

// DeploymentCloud is a Jira Cloud site, which is the only kind this client's
// adapter is written against. Anything else is a site to refuse at connect.
const DeploymentCloud DeploymentType = "Cloud"

// ServerInfo is what a site reports about itself.
type ServerInfo struct {
	BaseURL        string
	Version        string
	BuildNumber    int64
	DeploymentType DeploymentType
	ServerTitle    string
}

// Cloud reports whether the site is Jira Cloud.
func (s ServerInfo) Cloud() bool { return s.DeploymentType == DeploymentCloud }
