package cloud

import (
	"context"
	"maps"
	"net/url"
	"strconv"

	"github.com/varijkapil13/saral/pkg/jira"
)

// Jira pages two ways and this adapter drives both of them through the
// constructors in pkg/jira/page.go rather than looping here. That is where the
// guards live — a repeated cursor token is treated as exhaustion, and an empty
// page ends an offset walk whatever the server claims its total is — and they
// are tested there. What belongs here is only the request and response half.

// cursorPages drives an endpoint that pages by opaque token. build turns a
// token — empty for the first page — into the request that fetches that page,
// and decode reads one response into its items and the token after them.
func cursorPages[T any](
	ctx context.Context,
	c *Client,
	build func(token string) request,
	decode func(*response) ([]T, string, error),
) (jira.Page[T], error) {
	return jira.Cursor(ctx, func(ctx context.Context, token string) ([]T, string, error) {
		resp, err := c.do(ctx, build(token))
		if err != nil {
			return nil, "", err
		}
		return decode(resp)
	})
}

// offsetPages drives an endpoint that pages by startAt. build turns an offset
// into the request that fetches from it, and decode reads one response into its
// items, the total the endpoint claims and its own end-of-results flag.
func offsetPages[T any](
	ctx context.Context,
	c *Client,
	build func(startAt int) request,
	decode func(*response) ([]T, int, bool, error),
) (jira.Page[T], error) {
	return jira.Offset(ctx, func(ctx context.Context, startAt int) ([]T, int, bool, error) {
		resp, err := c.do(ctx, build(startAt))
		if err != nil {
			return nil, -1, false, err
		}
		return decode(resp)
	})
}

// tokenEnvelope is the platform API's cursor-paginated shape. /search/jql names
// its array issues and the other endpoints that page this way name theirs
// values; an endpoint sends one or the other and never both.
//
// There is no total here and there never will be: a cursor endpoint does not
// know one. Where a count genuinely matters it comes from a separate call to
// /search/approximate-count.
type tokenEnvelope[W any] struct {
	Issues        []W    `json:"issues"`
	Values        []W    `json:"values"`
	NextPageToken string `json:"nextPageToken"`
	IsLast        *bool  `json:"isLast"`
}

func (e tokenEnvelope[W]) items() []W {
	if e.Issues != nil {
		return e.Issues
	}
	return e.Values
}

// next is the token for the page after this one, and "" at the end. An absent
// token ends the walk on its own; isLast is honoured where an endpoint sends
// one, and several do not.
func (e tokenEnvelope[W]) next() string {
	if e.IsLast != nil && *e.IsLast {
		return ""
	}
	return e.NextPageToken
}

// decodeTokenPage reads one page of a cursor-paginated platform response.
func decodeTokenPage[W any](resp *response, op string) (items []W, next string, err error) {
	var envelope tokenEnvelope[W]
	if err := resp.decode(op, &envelope); err != nil {
		return nil, "", err
	}
	return envelope.items(), envelope.next(), nil
}

// agileEnvelope is the Agile API's paged shape: offsets, and a total the
// platform API never reports. Both total and isLast are pointers because an
// endpoint may send neither, and a zero total means something different from a
// missing one.
//
// There are three of these envelopes and no endpoint says which one it answers
// in. /board/{id}/issue and /board/{id}/backlog name their array issues and send
// no isLast; /board/{id}/version and /board/{id}/epic name theirs values and
// send no total; the rest send all four keys. So every combination has to decode,
// and the walk has to end on each of them.
type agileEnvelope[W any] struct {
	StartAt    int   `json:"startAt"`
	MaxResults int   `json:"maxResults"`
	Total      *int  `json:"total"`
	IsLast     *bool `json:"isLast"`
	Issues     []W   `json:"issues"`
	Values     []W   `json:"values"`
}

// items is whichever array arrived. An endpoint sends one or the other and never
// both, the same way the cursor-paginated endpoints do.
func (e agileEnvelope[W]) items() []W {
	if e.Issues != nil {
		return e.Issues
	}
	return e.Values
}

// total is the count the endpoint reported, or a negative number when it
// reported none, which is what jira.Offset reads as "no total".
func (e agileEnvelope[W]) total() int {
	if e.Total == nil {
		return -1
	}
	return *e.Total
}

// last reports the end of the walk. An endpoint that sends isLast is believed;
// one that sends a total is left to jira.Offset, which ends on it. The board
// issue endpoints send neither, so all they leave to go on is the length of the
// page against the maxResults the response itself echoes — a page shorter than
// the length asked for is the last one, and without this a walk over them costs
// an extra request that comes back empty.
func (e agileEnvelope[W]) last() bool {
	switch {
	case e.IsLast != nil:
		return *e.IsLast
	case e.Total != nil:
		return false
	default:
		return e.MaxResults > 0 && len(e.items()) < e.MaxResults
	}
}

// decodeAgilePage reads one page of an offset-paginated Agile response.
func decodeAgilePage[W any](resp *response, op string) (items []W, total int, isLast bool, err error) {
	var envelope agileEnvelope[W]
	if err := resp.decode(op, &envelope); err != nil {
		return nil, -1, false, err
	}
	return envelope.items(), envelope.total(), envelope.last(), nil
}

// pagedQuery copies a query and puts an offset on it, which is how every Agile
// request for a page after the first is built.
func pagedQuery(q url.Values, startAt, maxResults int) url.Values {
	out := url.Values{}
	maps.Copy(out, q)
	if startAt > 0 {
		out.Set("startAt", strconv.Itoa(startAt))
	}
	if maxResults > 0 {
		out.Set("maxResults", strconv.Itoa(maxResults))
	}
	return out
}
