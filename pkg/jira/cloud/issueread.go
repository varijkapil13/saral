package cloud

import (
	"context"
	"net/http"
	"net/url"
	"strings"

	"github.com/varijkapil13/saral/pkg/jira"
)

var _ jira.IssueReader = (*Client)(nil)

// IssueFields reads one issue with only the fields named, through the issue
// endpoint rather than /search/jql.
//
// The difference is when the answer was true. /search/jql answers from an
// index a write reaches seconds later, so a search made straight after an edit
// can hand back the value the edit replaced; the issue endpoint reads the issue
// itself. That makes this the read to confirm a write with, and the read to
// check a field has not moved underneath a pending one.
//
// A read naming no field is refused rather than sent: the endpoint answers
// every field the site has without a list, which is what Issue is for.
func (c *Client) IssueFields(ctx context.Context, key string, fields []string) (jira.Issue, error) {
	id, err := issueKey(key)
	if err != nil {
		return jira.Issue{}, err
	}
	wanted := uniqueStrings(fields)
	if len(wanted) == 0 {
		return jira.Issue{}, invalidField("fields",
			"a narrow issue read must name the fields it wants; Issue is the read for all of them")
	}
	query := url.Values{"fields": {strings.Join(wanted, ",")}}
	if needsSchema(wanted) {
		query.Set("expand", issueDetailExpand)
	}
	r := request{
		method: http.MethodGet,
		path:   issuePath + "/" + url.PathEscape(id),
		query:  query,
		kind:   "issue",
		id:     id,
	}
	var detail apiIssueDetail
	if err := c.doJSON(ctx, r, &detail); err != nil {
		return jira.Issue{}, err
	}
	return decodeIssue(detail.apiIssue, detail.Schema, jira.NewFieldMask(wanted)), nil
}
