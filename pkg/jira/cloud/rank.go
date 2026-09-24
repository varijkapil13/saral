package cloud

import (
	"context"
	"net/http"
	"slices"
	"strconv"
	"strings"

	"github.com/varijkapil13/saral/pkg/jira"
)

var _ jira.Ranker = (*Client)(nil)

const rankPath = "/rest/agile/1.0/issue/rank"

// rankChunk is the most issues the rank endpoint takes in one call.
const rankChunk = 50

type apiRankBody struct {
	Issues            []string `json:"issues"`
	RankBeforeIssue   string   `json:"rankBeforeIssue,omitempty"`
	RankAfterIssue    string   `json:"rankAfterIssue,omitempty"`
	RankCustomFieldID int64    `json:"rankCustomFieldId,omitempty"`
}

// apiRankAnswer is the 207 a call answers when any issue in it was refused.
// Every issue of the call has an entry, and the status on each is that issue's
// own.
type apiRankAnswer struct {
	Entries []struct {
		IssueID  flexString `json:"issueId"`
		IssueKey string     `json:"issueKey"`
		Status   int        `json:"status"`
		Errors   []string   `json:"errors"`
	} `json:"entries"`
}

// RankIssues moves issues to just before or just after another issue.
//
// The endpoint takes fifty issues a call. Before an anchor, each call goes
// before that same anchor, which leaves the calls in order ahead of it. After
// an anchor, each call goes after the last issue the previous one placed —
// sending every call after the anchor would put the second fifty between the
// anchor and the first.
//
// A call is answered 204 when every issue in it moved and 207 when any did not,
// and the 207 names each issue's own status: a refused issue is recorded and
// the rest of the walk continues, because one issue on a board the token
// cannot schedule says nothing about the next. A call refused whole, or never
// answered, stops the walk, and what was not sent is Pending.
func (c *Client) RankIssues(ctx context.Context, keys []string, at jira.RankPosition) error {
	wanted, anchor, after, field, err := rankRequest(keys, at)
	if err != nil {
		return err
	}
	if len(wanted) == 0 {
		return nil
	}

	var (
		ranked []string
		failed []jira.RankFailure
	)
	stopped := func(start int, err error) error {
		if len(ranked) == 0 && len(failed) == 0 {
			return err
		}
		return &jira.PartialRankError{Ranked: ranked, Failed: failed, Pending: slices.Clone(wanted[start:]), Err: err}
	}
	for start := 0; start < len(wanted); start += rankChunk {
		chunk := slices.Clone(wanted[start:min(start+rankChunk, len(wanted))])
		body := apiRankBody{Issues: chunk, RankCustomFieldID: field}
		if after {
			body.RankAfterIssue = anchor
		} else {
			body.RankBeforeIssue = anchor
		}
		r := request{method: http.MethodPut, path: rankPath, body: body, kind: "issue", id: anchor}
		resp, err := c.do(ctx, r)
		if err != nil {
			return stopped(start, err)
		}
		moved, refused, err := rankOutcome(resp, r.op(), chunk)
		if err != nil {
			return stopped(start, err)
		}
		ranked = append(ranked, moved...)
		failed = append(failed, refused...)
		if after && len(moved) > 0 {
			anchor = moved[len(moved)-1]
		}
	}
	if len(failed) > 0 {
		return &jira.PartialRankError{Ranked: ranked, Failed: failed}
	}
	return nil
}

// rankOutcome sorts one call's issues into those that moved and those the site
// refused. A 207 whose entries do not account for an issue of the call is read
// as that issue moving, because the site names only what it answered for and
// 207 is a success of the call.
func rankOutcome(resp *response, op string, chunk []string) (moved []string, refused []jira.RankFailure, err error) {
	if resp.status != http.StatusMultiStatus {
		return chunk, nil, nil
	}
	var answer apiRankAnswer
	if err := resp.decode(op, &answer); err != nil {
		return nil, nil, err
	}
	reasons := make(map[string]string, len(answer.Entries))
	for _, entry := range answer.Entries {
		if entry.Status >= http.StatusOK && entry.Status < http.StatusMultipleChoices {
			continue
		}
		reason := strings.Join(entry.Errors, "; ")
		if reason == "" {
			reason = "the site refused to rank this issue (HTTP " + strconv.Itoa(entry.Status) + ")"
		}
		for _, name := range []string{entry.IssueKey, string(entry.IssueID)} {
			if name != "" {
				reasons[name] = reason
			}
		}
	}
	for _, key := range chunk {
		if reason, ok := reasons[key]; ok {
			refused = append(refused, jira.RankFailure{Key: key, Reason: reason})
			continue
		}
		moved = append(moved, key)
	}
	return moved, refused, nil
}

// rankRequest validates a reorder before anything is sent. An issue ranked
// relative to itself has no position to go to, and the site refuses that call
// whole — which would strand every other issue in it.
func rankRequest(keys []string, at jira.RankPosition) (wanted []string, anchor string, after bool, field int64, err error) {
	before, afterKey := strings.TrimSpace(at.Before), strings.TrimSpace(at.After)
	switch {
	case before == "" && afterKey == "":
		return nil, "", false, 0, invalidField("rank", "a rank needs an issue to go before or after")
	case before != "" && afterKey != "":
		return nil, "", false, 0, invalidField("rank", "a rank goes before one issue or after one, not both")
	}
	anchor, after = at.Anchor()
	wanted = uniqueStrings(keys)
	if slices.Contains(wanted, anchor) {
		return nil, "", false, 0, invalidField("issues", anchor+" cannot be ranked relative to itself")
	}
	if id := strings.TrimSpace(at.FieldID); id != "" {
		number, parseErr := strconv.ParseInt(strings.TrimPrefix(id, "customfield_"), 10, 64)
		if parseErr != nil || number <= 0 {
			return nil, "", false, 0, invalidField("rankCustomFieldId",
				id+" is not a custom field id; pass BoardConfig.RankFieldID")
		}
		field = number
	}
	return wanted, anchor, after, field, nil
}
