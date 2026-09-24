package cloud

import (
	"context"
	"net/http"
	"net/url"
	"strings"

	"github.com/varijkapil13/saral/pkg/jira"
)

var _ jira.WatcherManager = (*Client)(nil)

func watchersPath(key string) string { return issuePath + "/" + url.PathEscape(key) + "/watchers" }

type apiWatchers struct {
	WatchCount int       `json:"watchCount"`
	IsWatching bool      `json:"isWatching"`
	Watchers   []apiUser `json:"watchers"`
}

// Watchers reports who watches an issue. A token that may not view voters and
// watchers is answered the count and an empty list, so People is who the site
// named and Count is how many there are.
func (c *Client) Watchers(ctx context.Context, key string) (jira.WatcherList, error) {
	id, err := issueKey(key)
	if err != nil {
		return jira.WatcherList{}, err
	}
	var body apiWatchers
	err = c.doJSON(ctx, request{method: http.MethodGet, path: watchersPath(id), kind: "issue", id: id}, &body)
	if err != nil {
		return jira.WatcherList{}, err
	}
	out := jira.WatcherList{Count: body.WatchCount, Watching: body.IsWatching}
	for i := range body.Watchers {
		if body.Watchers[i].AccountID != "" {
			out.People = append(out.People, body.Watchers[i].domain())
		}
	}
	return out, nil
}

// Watch adds a watcher. The body is the account id as a bare JSON string, not
// an object, and a request with no body at all adds the authenticated account.
func (c *Client) Watch(ctx context.Context, key, accountID string) error {
	id, err := issueKey(key)
	if err != nil {
		return err
	}
	r := request{method: http.MethodPost, path: watchersPath(id), kind: "issue", id: id}
	if account := strings.TrimSpace(accountID); account != "" {
		r.body = account
	}
	_, err = c.do(ctx, r)
	return err
}

// Unwatch removes a watcher. The account goes in the query, and the endpoint
// takes no default: removing the authenticated account names it.
func (c *Client) Unwatch(ctx context.Context, key, accountID string) error {
	id, err := issueKey(key)
	if err != nil {
		return err
	}
	account := strings.TrimSpace(accountID)
	if account == "" {
		return invalidField("accountId", "removing a watcher names the account, the authenticated one included")
	}
	_, err = c.do(ctx, request{
		method: http.MethodDelete,
		path:   watchersPath(id),
		query:  url.Values{"accountId": {account}},
		kind:   "issue",
		id:     id,
	})
	return err
}
