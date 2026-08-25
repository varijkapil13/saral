package cloud

import (
	"context"
	"errors"
	"net/http"
	"net/url"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/varijkapil13/saral/pkg/jira"
)

const (
	peopleSearchPath     = "/rest/api/3/user/search"
	peopleAssignablePath = "/rest/api/3/user/assignable/search"
	peopleBulkPath       = "/rest/api/3/user/bulk"
)

// peopleDefaultLimit is how many accounts a search asks for when the caller
// names no ceiling. A picker shows a screenful and asks the user to type more.
const peopleDefaultLimit = 50

// peopleBulkChunk is how many account ids go into one /user/bulk request. They
// travel in the query string, one parameter each, so the bound is on the length
// of a URL rather than on anything Jira states.
const peopleBulkChunk = 50

// FindPeople searches the site's accounts.
//
// Two endpoints, and the query's project decides which. /user/assignable/search
// answers only accounts that can be assigned work in that project, which drops
// app accounts without being asked — on the site this was measured against that
// was ten of eleven accounts. /user/search is the site-wide fallback.
//
// Neither is a ranking. What matches is Jira's own undocumented rule and the
// order is Jira's own; jira.PeopleQuery is where that contract is written down
// for the caller.
func (c *Client) FindPeople(ctx context.Context, q jira.PeopleQuery) ([]jira.User, error) {
	limit := peopleLimit(q.Limit)
	query := url.Values{"maxResults": []string{strconv.Itoa(limit)}}
	// query must be sent even when it is empty: absent is a 400, and empty
	// matches every account. There is no way to ask this endpoint for nothing.
	query.Set("query", q.Match)

	r := request{
		method: http.MethodGet,
		path:   peopleSearchPath,
		query:  query,
		kind:   "the site's accounts",
		id:     peopleSearchPath,
	}
	if project := strings.TrimSpace(q.Project); project != "" {
		query.Set("project", project)
		r.path, r.kind, r.id = peopleAssignablePath, "project", project
	}

	var body []peopleAccount
	if err := c.doJSON(ctx, r, &body); err != nil {
		return nil, peopleRefusal(err)
	}
	out := make([]jira.User, 0, min(limit, len(body)))
	for _, account := range body {
		// The ceiling is honoured here as well as asked for, because maxResults
		// is a request rather than a promise: Jira caps it silently on other
		// endpoints and never echoes what it used, so a caller sizing a picker
		// on Limit cannot be left holding more rows than it has room for.
		if len(out) >= limit {
			break
		}
		if account.AccountID == "" {
			continue
		}
		out = append(out, account.domain())
	}
	return out, nil
}

// People resolves account ids to accounts.
//
// /user/bulk answers a JSON null inside values for an id it does not know, which
// decodes into an account with no id and no name. Those are dropped: a caller
// asking for five ids and drawing five rows would put a blank one on screen, and
// a blank row is worse than an absence. So the answer is keyed by AccountID, and
// an id this site does not know is simply not in it.
//
// It also defaults to ten per page whatever it was asked for, so this pages even
// for a list of eleven.
func (c *Client) People(ctx context.Context, accountIDs []string) ([]jira.User, error) {
	wanted := peopleIDs(accountIDs)
	if len(wanted) == 0 {
		// A bulk read with no accountId parameter is a 400. Nobody asked for
		// anything, so there is nothing to report and nothing to ask.
		return nil, nil
	}

	out := make([]jira.User, 0, len(wanted))
	for chunk := range slices.Chunk(wanted, peopleBulkChunk) {
		found, err := c.peopleChunk(ctx, chunk)
		if err != nil {
			return nil, err
		}
		out = append(out, found...)
	}
	return out, nil
}

func (c *Client) peopleChunk(ctx context.Context, chunk []string) ([]jira.User, error) {
	page, err := offsetPages(ctx, c, func(startAt int) request {
		return request{
			method: http.MethodGet,
			path:   peopleBulkPath,
			query:  peopleBulkQuery(chunk, startAt),
			kind:   "the site's accounts",
			id:     peopleBulkPath,
		}
	}, peopleDecodeBulk)
	if err != nil {
		return nil, peopleRefusal(err)
	}
	walked, err := jira.Collect(ctx, page, 0)
	if err != nil {
		return nil, peopleRefusal(err)
	}
	out := make([]jira.User, 0, len(walked))
	for _, u := range walked {
		if u.AccountID != "" {
			out = append(out, u)
		}
	}
	return out, nil
}

// peopleDecodeBulk keeps the unknown ids in the page it hands back, as zero
// users. Dropping them here would shorten the page, and an offset walk ends on a
// short page — so a chunk whose last ten ids are all unknown would stop the walk
// with the rest of the ids unread.
func peopleDecodeBulk(resp *response) (items []jira.User, total int, isLast bool, err error) {
	slots, total, isLast, err := decodeAgilePage[*peopleAccount](resp, http.MethodGet+" "+peopleBulkPath)
	if err != nil {
		return nil, -1, false, err
	}
	out := make([]jira.User, len(slots))
	for i, slot := range slots {
		if slot != nil {
			out[i] = slot.domain()
		}
	}
	return out, total, isLast, nil
}

// peopleBulkQuery names every id in the chunk as its own parameter, which is
// what this endpoint takes instead of a list. Two account ids on the measured
// site contained a colon, and Jira answers a percent-encoded one with the raw
// form, so nothing may build this string by hand or compare the two spellings.
func peopleBulkQuery(chunk []string, startAt int) url.Values {
	query := url.Values{
		"accountId":  slices.Clone(chunk),
		"maxResults": []string{strconv.Itoa(len(chunk))},
	}
	if startAt > 0 {
		query.Set("startAt", strconv.Itoa(startAt))
	}
	return query
}

// peopleIDs drops the empty ids and the repeats. A repeat costs a row Jira would
// have sent twice, and an empty one widens the request for nothing.
func peopleIDs(in []string) []string {
	out := make([]string, 0, len(in))
	seen := make(map[string]struct{}, len(in))
	for _, id := range in {
		trimmed := strings.TrimSpace(id)
		if trimmed == "" {
			continue
		}
		if _, dup := seen[trimmed]; dup {
			continue
		}
		seen[trimmed] = struct{}{}
		out = append(out, trimmed)
	}
	return out
}

func peopleLimit(limit int) int {
	if limit <= 0 {
		return peopleDefaultLimit
	}
	return limit
}

// peopleRefusal names the capability on a 403, so a caller can tell the one
// refusal it can act on — offer the ids it already holds instead of a picker —
// from every other way a call can fail.
func peopleRefusal(err error) error {
	var refused *jira.CapabilityError
	if !errors.As(err, &refused) || refused.Capability != "" {
		return err
	}
	return &jira.CapabilityError{Capability: jira.CapPeople, Reason: refused.Reason}
}

// peopleAccount is a user as the people endpoints send one. The issue reader has
// a decoder of its own that does not read accountType, so an account off an issue
// carries no Kind and one from here does.
type peopleAccount struct {
	AccountID   string            `json:"accountId"`
	AccountType string            `json:"accountType"`
	DisplayName string            `json:"displayName"`
	Email       string            `json:"emailAddress"`
	Active      bool              `json:"active"`
	TimeZone    string            `json:"timeZone"`
	AvatarURLs  map[string]string `json:"avatarUrls"`
}

func (a peopleAccount) domain() jira.User {
	out := jira.User{
		AccountID:   a.AccountID,
		DisplayName: a.DisplayName,
		Email:       a.Email,
		Active:      a.Active,
		Kind:        jira.ParseAccountKind(a.AccountType),
	}
	for _, size := range avatarSizes {
		if link := a.AvatarURLs[size]; link != "" {
			out.AvatarURL = link
			break
		}
	}
	// A zone this machine has no database for is a rendering detail, never a
	// reason to lose the account it belongs to.
	if a.TimeZone != "" {
		if loc, err := time.LoadLocation(a.TimeZone); err == nil {
			out.TimeZone = loc
		}
	}
	return out
}
