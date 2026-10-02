package cloud

import (
	"context"
	"net/http"
	"net/url"
	"strconv"
	"strings"

	"github.com/varijkapil13/saral/pkg/jira"
)

var _ jira.ProjectReader = (*Client)(nil)

type apiProjectRead struct {
	ID   flexString `json:"id"`
	Key  string     `json:"key"`
	Name string     `json:"name"`
}

// Project resolves a project by id or key; a project the token cannot browse is a 404.
func (c *Client) Project(ctx context.Context, idOrKey string) (jira.ProjectRef, error) {
	ref := strings.TrimSpace(idOrKey)
	switch {
	case ref == "":
		return jira.ProjectRef{}, invalidField("project", "a project id or key is required")
	case !jira.IsPathSegment(ref):
		return jira.ProjectRef{}, invalidField("project", strconv.Quote(ref)+" is not a project id or key")
	}
	var body apiProjectRead
	err := c.doJSON(ctx, request{
		method: http.MethodGet,
		path:   projectPath + "/" + url.PathEscape(ref),
		kind:   "project",
		id:     ref,
	}, &body)
	if err != nil {
		return jira.ProjectRef{}, err
	}
	return jira.ProjectRef{ID: string(body.ID), Key: body.Key, Name: body.Name}, nil
}
