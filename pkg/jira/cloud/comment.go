package cloud

import (
	"context"
	"encoding/json"
	"net/http"
	"net/url"
	"strings"

	"github.com/varijkapil13/saral/pkg/adf"
	"github.com/varijkapil13/saral/pkg/jira"
)

// commentPageSize is how many comments one request asks for. The endpoint
// defaults to fifty and caps what it will send anyway; asking explicitly is what
// makes the offsets a test walks reproducible.
const commentPageSize = 50

// commentsPath is the collection under one issue.
func commentsPath(key string) string {
	return "/rest/api/3/issue/" + url.PathEscape(key) + "/comment"
}

// commentPath is one comment under one issue.
func commentPath(key, id string) string {
	return commentsPath(key) + "/" + url.PathEscape(id)
}

// apiCommentPage is the envelope GET /issue/{key}/comment answers with. It pages
// by startAt like the Agile API does, but names its array comments rather than
// values and sends no isLast, so neither of this package's shared envelopes
// reads it.
type apiCommentPage struct {
	StartAt    int          `json:"startAt"`
	MaxResults int          `json:"maxResults"`
	Total      *int         `json:"total"`
	Comments   []apiComment `json:"comments"`
}

func (p apiCommentPage) total() int {
	if p.Total == nil {
		return -1
	}
	return *p.Total
}

// last reports the end of the walk for an envelope that reported no total. That
// shape carries no isLast either, so the only thing left to end on is a page
// shorter than the one the server said it was serving — and it is the server's
// own maxResults that says how long a full page is, because a site may cap the
// number asked for without saying so anywhere else.
func (p apiCommentPage) last() bool {
	if p.Total != nil {
		return false
	}
	asked := p.MaxResults
	if asked <= 0 {
		asked = commentPageSize
	}
	return len(p.Comments) < asked
}

// apiComment is one comment. The body is decoded into a document rather than
// kept raw because that is the shape the port carries; a body that will not read
// as ADF fails the call rather than arriving as an empty comment.
type apiComment struct {
	ID           string          `json:"id"`
	Author       *apiUser        `json:"author"`
	UpdateAuthor *apiUser        `json:"updateAuthor"`
	Body         adf.Doc         `json:"body"`
	Created      timestamp       `json:"created"`
	Updated      timestamp       `json:"updated"`
	Visibility   json.RawMessage `json:"visibility"`
}

func (c apiComment) domain() jira.Comment {
	out := jira.Comment{
		ID:      c.ID,
		Body:    c.Body,
		Created: c.Created.Time,
		Updated: c.Updated.Time,
	}
	if c.Author != nil {
		out.Author = c.Author.domain()
	}
	if c.UpdateAuthor != nil {
		author := c.UpdateAuthor.domain()
		out.UpdateAuthor = &author
	}
	out.Visibility = decodeVisibility(c.Visibility)
	return out
}

// apiVisibility is a comment's restriction. identifier is the role or group id,
// which is the half of this that is not localised; value is the display name,
// which on a German site is German.
type apiVisibility struct {
	Type       string `json:"type"`
	Value      string `json:"value"`
	Identifier string `json:"identifier"`
}

// decodeVisibility reads the restriction a comment carries, and nil for one that
// carries none. The domain type has no room for the identifier, which is why an
// edit sends the original object back rather than rebuilding one from this.
func decodeVisibility(raw json.RawMessage) *jira.Visibility {
	if !hasVisibility(raw) {
		return nil
	}
	var v apiVisibility
	if err := json.Unmarshal(raw, &v); err != nil || v.Type == "" {
		return nil
	}
	label := v.Value
	if label == "" {
		label = v.Identifier
	}
	return &jira.Visibility{Type: v.Type, Value: label}
}

func hasVisibility(raw json.RawMessage) bool {
	trimmed := strings.TrimSpace(string(raw))
	return trimmed != "" && trimmed != "null" && trimmed != "{}"
}

// apiCommentBody is what a write sends. Visibility is echoed back exactly as the
// site sent it, keys this client does not model included.
type apiCommentBody struct {
	Body       json.RawMessage `json:"body"`
	Visibility json.RawMessage `json:"visibility,omitempty"`
}

// Comments lists an issue's comments, oldest first.
//
// The endpoint pages by startAt and reports a total, like the Agile API, while
// living on the platform API and naming its array comments. It is asked for
// created order explicitly: the default is documented nowhere and the port
// promises oldest first.
func (c *Client) Comments(ctx context.Context, key string) (jira.Page[jira.Comment], error) {
	path := commentsPath(key)
	query := url.Values{"orderBy": []string{"created"}}
	build := func(startAt int) request {
		return request{
			method: http.MethodGet,
			path:   path,
			query:  pagedQuery(query, startAt, commentPageSize),
			kind:   "issue",
			id:     key,
		}
	}
	op := http.MethodGet + " " + path
	return offsetPages(ctx, c, build, func(resp *response) ([]jira.Comment, int, bool, error) {
		var page apiCommentPage
		if err := resp.decode(op, &page); err != nil {
			return nil, -1, false, err
		}
		out := make([]jira.Comment, 0, len(page.Comments))
		for i := range page.Comments {
			out = append(out, page.Comments[i].domain())
		}
		return out, page.total(), page.last(), nil
	})
}

// AddComment adds a comment.
//
// A new comment carries no restriction: Jira treats an absent visibility as
// "everyone who can see the issue", which is what somebody writing a comment
// with no further ceremony means.
func (c *Client) AddComment(ctx context.Context, key string, body adf.Doc) (jira.Comment, error) {
	encoded, err := encodeCommentBody(body)
	if err != nil {
		return jira.Comment{}, err
	}
	r := request{
		method: http.MethodPost,
		path:   commentsPath(key),
		body:   apiCommentBody{Body: encoded},
		kind:   "issue",
		id:     key,
	}
	var out apiComment
	if err := c.doJSON(ctx, r, &out); err != nil {
		return jira.Comment{}, err
	}
	return out.domain(), nil
}

// EditComment replaces a comment's body and keeps whatever restriction the
// comment already had.
//
// The PUT is a replace, so a request that names only a body is a request to
// publish a comment that was restricted to one role. The restriction is not
// something the port can carry — the only safe thing to send is the object the
// site sent, so the comment is read back first and its visibility echoed
// verbatim, identifier included.
func (c *Client) EditComment(ctx context.Context, key, id string, body adf.Doc) (jira.Comment, error) {
	encoded, err := encodeCommentBody(body)
	if err != nil {
		return jira.Comment{}, err
	}
	current, err := c.comment(ctx, key, id)
	if err != nil {
		return jira.Comment{}, err
	}
	payload := apiCommentBody{Body: encoded}
	if hasVisibility(current.Visibility) {
		payload.Visibility = current.Visibility
	}
	r := request{
		method: http.MethodPut,
		path:   commentPath(key, id),
		body:   payload,
		kind:   "comment",
		id:     id,
	}
	var out apiComment
	if err := c.doJSON(ctx, r, &out); err != nil {
		return jira.Comment{}, err
	}
	return out.domain(), nil
}

// DeleteComment removes a comment.
func (c *Client) DeleteComment(ctx context.Context, key, id string) error {
	r := request{
		method: http.MethodDelete,
		path:   commentPath(key, id),
		kind:   "comment",
		id:     id,
	}
	_, err := c.do(ctx, r)
	return err
}

// comment reads one comment, which is how an edit learns what it must not
// discard.
func (c *Client) comment(ctx context.Context, key, id string) (apiComment, error) {
	r := request{
		method: http.MethodGet,
		path:   commentPath(key, id),
		kind:   "comment",
		id:     id,
	}
	var out apiComment
	if err := c.doJSON(ctx, r, &out); err != nil {
		return apiComment{}, err
	}
	return out, nil
}

// encodeCommentBody turns a document into the bytes a write sends, and refuses
// an empty one here rather than spending a round trip on a 400.
func encodeCommentBody(body adf.Doc) (json.RawMessage, error) {
	if body.IsZero() || body.IsEmpty() {
		return nil, &jira.ValidationError{Fields: []jira.FieldError{{
			Field:   "body",
			Message: "a comment needs something in it",
		}}}
	}
	encoded, err := adf.Marshal(body)
	if err != nil {
		return nil, &jira.ValidationError{Fields: []jira.FieldError{{
			Field:   "body",
			Message: "this comment is not a document Jira can store: " + err.Error(),
		}}}
	}
	return encoded, nil
}
