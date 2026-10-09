package issue

import (
	"context"

	"github.com/varijkapil13/saral/pkg/jira"
)

// Links are an issue's links to other issues.
func Links(ctx context.Context, reader jira.IssueReader, key string) ([]jira.IssueLink, error) {
	iss, err := reader.IssueFields(ctx, key, []string{"issuelinks"})
	if err != nil {
		return nil, err
	}
	return iss.Links, nil
}

// LinkTypes are the kinds of link this site has.
func LinkTypes(ctx context.Context, l jira.Linker) ([]jira.LinkType, error) {
	return l.IssueLinkTypes(ctx)
}

// LinkBetween is the link of a kind between key and other, read in the
// inward phrase when inward is set and in the outward one otherwise.
func LinkBetween(typeID string, inward bool, key, other string) jira.LinkInput {
	if inward {
		return jira.LinkInput{TypeID: typeID, From: other, To: key}
	}
	return jira.LinkInput{TypeID: typeID, From: key, To: other}
}

// Link makes one link.
func Link(ctx context.Context, l jira.Linker, in jira.LinkInput) error {
	return l.LinkIssues(ctx, in)
}

// Unlink removes one link.
func Unlink(ctx context.Context, l jira.Linker, linkID string) error {
	return l.DeleteLink(ctx, linkID)
}

// CopyLinks gives to the links of another issue, matching each kind by name,
// and returns how many could not be made.
func CopyLinks(ctx context.Context, l jira.Linker, links []jira.IssueLink, types []jira.LinkType, to string) int {
	failed := 0
	for i := range links {
		lk := &links[i]
		in := jira.LinkInput{From: to, To: lk.Other.Key}
		if lk.Direction == jira.LinkInward {
			in.From, in.To = lk.Other.Key, to
		}
		for _, t := range types {
			if t.Name == lk.Type {
				in.TypeID = t.ID
			}
		}
		if in.TypeID == "" || l.LinkIssues(ctx, in) != nil {
			failed++
		}
	}
	return failed
}
