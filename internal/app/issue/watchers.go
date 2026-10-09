package issue

import (
	"context"

	"github.com/varijkapil13/saral/pkg/jira"
)

// WatchersOf is who watches an issue, and whether this session does.
func WatchersOf(ctx context.Context, w jira.WatcherManager, key string) (jira.WatcherList, error) {
	return w.Watchers(ctx, key)
}

// WatchClient is what watching and unwatching on one's own behalf runs on.
type WatchClient interface {
	jira.WatcherManager
	jira.Identifier
}

// Toggle starts this session watching key, or stops it when watching.
// Stopping names the account, which the site requires and starting does not.
func Toggle(ctx context.Context, c WatchClient, key string, watching bool) error {
	if !watching {
		return c.Watch(ctx, key, "")
	}
	me, err := c.Me(ctx)
	if err != nil {
		return err
	}
	return c.Unwatch(ctx, key, me.AccountID)
}

// AddWatcher adds an account to key's watchers.
func AddWatcher(ctx context.Context, w jira.WatcherManager, key, accountID string) error {
	return w.Watch(ctx, key, accountID)
}

// RemoveWatcher removes an account from key's watchers.
func RemoveWatcher(ctx context.Context, w jira.WatcherManager, key, accountID string) error {
	return w.Unwatch(ctx, key, accountID)
}

// FindAccounts searches every account the site lets this session see.
func FindAccounts(ctx context.Context, finder jira.PeopleFinder, match string, limit int) ([]jira.User, error) {
	return finder.FindPeople(ctx, jira.PeopleQuery{Match: match, Limit: limit})
}
