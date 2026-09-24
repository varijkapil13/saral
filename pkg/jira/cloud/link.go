package cloud

import (
	"context"
	"net/http"
	"net/url"
	"strings"

	"github.com/varijkapil13/saral/pkg/jira"
)

var _ jira.Linker = (*Client)(nil)

const (
	linkTypePath = "/rest/api/3/issueLinkType"
	linkPath     = "/rest/api/3/issueLink"
)

type apiLinkType struct {
	ID      flexString `json:"id"`
	Name    string     `json:"name"`
	Inward  string     `json:"inward"`
	Outward string     `json:"outward"`
}

type apiLinkRef struct {
	Key string `json:"key"`
}

type apiLinkTypeRef struct {
	ID string `json:"id"`
}

// apiLinkWrite is the create body. The two issue slots are named for the
// phrase that is read from the other end: the inwardIssue is the one the link
// starts at, and reads the outward phrase — "inwardIssue blocks outwardIssue".
type apiLinkWrite struct {
	Type         apiLinkTypeRef `json:"type"`
	InwardIssue  apiLinkRef     `json:"inwardIssue"`
	OutwardIssue apiLinkRef     `json:"outwardIssue"`
}

// IssueLinkTypes lists the kinds of link the site has, in the order it sends
// them. The names are the site's and are localised, which is why a link is
// written by the id beside them.
func (c *Client) IssueLinkTypes(ctx context.Context) ([]jira.LinkType, error) {
	var body struct {
		IssueLinkTypes []apiLinkType `json:"issueLinkTypes"`
	}
	err := c.doJSON(ctx, request{
		method: http.MethodGet,
		path:   linkTypePath,
		kind:   "the site's issue link types",
		id:     linkTypePath,
	}, &body)
	if err != nil {
		return nil, err
	}
	out := make([]jira.LinkType, 0, len(body.IssueLinkTypes))
	for _, t := range body.IssueLinkTypes {
		out = append(out, jira.LinkType{ID: string(t.ID), Name: t.Name, Inward: t.Inward, Outward: t.Outward})
	}
	return out, nil
}

// LinkIssues links From to To so that From reads the type's outward phrase.
//
// The create answers 201 with no body and no id: the new link is found again on
// either issue's issuelinks, which is where DeleteLink's id comes from.
func (c *Client) LinkIssues(ctx context.Context, in jira.LinkInput) error {
	typeID := strings.TrimSpace(in.TypeID)
	from, to := strings.TrimSpace(in.From), strings.TrimSpace(in.To)
	switch {
	case typeID == "":
		return invalidField("type", "a link needs a link type id; match it by id, never by the localised name")
	case from == "":
		return invalidField("inwardIssue", "a link needs the issue it starts at")
	case to == "":
		return invalidField("outwardIssue", "a link needs the issue it ends at")
	case from == to:
		return invalidField("outwardIssue", from+" cannot be linked to itself")
	}
	_, err := c.do(ctx, request{
		method: http.MethodPost,
		path:   linkPath,
		body: apiLinkWrite{
			Type:         apiLinkTypeRef{ID: typeID},
			InwardIssue:  apiLinkRef{Key: from},
			OutwardIssue: apiLinkRef{Key: to},
		},
		kind: "issue",
		id:   from + " or " + to,
	})
	return err
}

// DeleteLink removes a link. The id is the link's own, which is the one
// Issue.Links carries, and removing it removes it from both ends.
func (c *Client) DeleteLink(ctx context.Context, linkID string) error {
	id, err := numericID("linkId", "issue link", linkID)
	if err != nil {
		return err
	}
	_, err = c.do(ctx, request{
		method: http.MethodDelete,
		path:   linkPath + "/" + url.PathEscape(id),
		kind:   "issue link",
		id:     id,
	})
	return err
}
