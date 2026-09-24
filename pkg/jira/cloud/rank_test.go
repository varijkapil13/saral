package cloud

import (
	"context"
	"errors"
	"net/http"
	"slices"
	"strconv"
	"testing"
	"time"

	"github.com/varijkapil13/saral/pkg/jira"
	"github.com/varijkapil13/saral/pkg/jira/jiratest"
)

func rankClient(t *testing.T, opts ...jiratest.ServerOption) (*Client, *jiratest.Server) {
	t.Helper()

	s := jiratest.NewServer(opts...)
	t.Cleanup(s.Close)
	c, _ := testClient(t, s.URL(), WithRetry(RetryPolicy{Attempts: 1}))
	return c, s
}

func rankKeys(n int) []string {
	out := make([]string, 0, n)
	for i := 1; i <= n; i++ {
		out = append(out, "EX-"+strconv.Itoa(i))
	}
	return out
}

func TestRankIssues_SendsTheMethodAndBodyTheEndpointTakes(t *testing.T) {
	t.Parallel()

	c, s := rankClient(t)
	err := c.RankIssues(t.Context(), []string{"EX-1", "EX-2"}, jira.RankBefore("EX-9"))
	if err != nil {
		t.Fatalf("ranking: %v", err)
	}

	sent := sentTo(t, s, http.MethodPut, rankPath)
	body := sentBody(t, sent)
	issues, ok := body["issues"].([]any)
	if !ok || len(issues) != 2 {
		t.Fatalf("issues = %v, want the two keys sent", body["issues"])
	}
	if before, _ := body["rankBeforeIssue"].(string); before != "EX-9" {
		t.Errorf("rankBeforeIssue = %q, want EX-9", before)
	}
	if _, has := body["rankAfterIssue"]; has {
		t.Errorf("rankAfterIssue was sent on a before-anchored rank: %v", body)
	}
	if _, has := body["rankCustomFieldId"]; has {
		t.Errorf("rankCustomFieldId was sent with no FieldID set: %v", body)
	}
}

func TestRankIssues_SendsTheCustomFieldIdParsedFromTheFieldID(t *testing.T) {
	t.Parallel()

	c, s := rankClient(t)
	err := c.RankIssues(t.Context(), []string{"EX-1"}, jira.RankPosition{After: "EX-9", FieldID: "customfield_10019"})
	if err != nil {
		t.Fatalf("ranking: %v", err)
	}
	body := sentBody(t, sentTo(t, s, http.MethodPut, rankPath))
	if after, _ := body["rankAfterIssue"].(string); after != "EX-9" {
		t.Errorf("rankAfterIssue = %q, want EX-9", after)
	}
	if field, _ := body["rankCustomFieldId"].(float64); int64(field) != 10019 {
		t.Errorf("rankCustomFieldId = %v, want 10019", body["rankCustomFieldId"])
	}
}

func TestRankIssues_ChunksAHundredAndTwentyKeysIntoFiftyFiftyAndTwenty(t *testing.T) {
	t.Parallel()

	c, s := rankClient(t)
	keys := rankKeys(120)

	if err := c.RankIssues(t.Context(), keys, jira.RankBefore("EX-999")); err != nil {
		t.Fatalf("ranking: %v", err)
	}

	var chunks [][]string
	for _, sent := range s.Requests() {
		if sent.Path != rankPath {
			continue
		}
		body := sentBody(t, sent)
		raw, _ := body["issues"].([]any)
		chunk := make([]string, 0, len(raw))
		for _, k := range raw {
			text, _ := k.(string)
			chunk = append(chunk, text)
		}
		chunks = append(chunks, chunk)
	}
	if len(chunks) != 3 {
		t.Fatalf("120 keys went in %d calls, want 3", len(chunks))
	}
	if len(chunks[0]) != 50 || len(chunks[1]) != 50 || len(chunks[2]) != 20 {
		t.Errorf("the chunks are %d/%d/%d, want 50/50/20", len(chunks[0]), len(chunks[1]), len(chunks[2]))
	}
	var all []string
	for _, chunk := range chunks {
		all = append(all, chunk...)
	}
	if !slices.Equal(all, keys) {
		t.Errorf("the chunks together sent %d keys, want the %d asked for, in order", len(all), len(keys))
	}
}

func TestRankIssues_KeepsEveryChunkBeforeTheSameAnchorWhenRankingBefore(t *testing.T) {
	t.Parallel()

	c, s := rankClient(t)
	if err := c.RankIssues(t.Context(), rankKeys(120), jira.RankBefore("EX-999")); err != nil {
		t.Fatalf("ranking: %v", err)
	}

	for _, sent := range s.Requests() {
		if sent.Path != rankPath {
			continue
		}
		body := sentBody(t, sent)
		if before, _ := body["rankBeforeIssue"].(string); before != "EX-999" {
			t.Errorf("a chunk ranked before %q, want every chunk before the same anchor EX-999", before)
		}
	}
}

func TestRankIssues_ChainsEachChunkAfterTheLastIssueThePreviousOnePlaced(t *testing.T) {
	t.Parallel()

	c, s := rankClient(t)
	keys := rankKeys(120)
	if err := c.RankIssues(t.Context(), keys, jira.RankAfter("EX-999")); err != nil {
		t.Fatalf("ranking: %v", err)
	}

	var anchors []string
	for _, sent := range s.Requests() {
		if sent.Path != rankPath {
			continue
		}
		body := sentBody(t, sent)
		after, _ := body["rankAfterIssue"].(string)
		anchors = append(anchors, after)
	}
	want := []string{"EX-999", "EX-50", "EX-100"}
	if !slices.Equal(anchors, want) {
		t.Errorf("the chunks ranked after %v, want %v: each chunk chains after the last issue the previous one placed", anchors, want)
	}
}

func TestRankIssues_A207ReportsRankedAndFailedPerKey(t *testing.T) {
	t.Parallel()

	c, _ := rankClient(t, jiratest.WithStatus(http.MethodPut, rankPath, http.StatusMultiStatus, "rank_partial.json"))
	err := c.RankIssues(t.Context(), []string{"EX-1", "EX-2"}, jira.RankBefore("EX-9"))

	var partial *jira.PartialRankError
	if !errors.As(err, &partial) {
		t.Fatalf("got %T (%v), want a *jira.PartialRankError", err, err)
	}
	if !slices.Equal(partial.Ranked, []string{"EX-1"}) {
		t.Errorf("Ranked = %v, want [EX-1]", partial.Ranked)
	}
	if len(partial.Failed) != 1 || partial.Failed[0].Key != "EX-2" {
		t.Fatalf("Failed = %+v, want one entry for EX-2", partial.Failed)
	}
	if partial.Failed[0].Reason == "" {
		t.Error("the failed entry carries no reason, and the site's sentence is the only thing that says why")
	}
	if len(partial.Pending) != 0 {
		t.Errorf("Pending = %v, want none: the site answered for every issue in the one and only chunk", partial.Pending)
	}
}

func TestRankIssues_ALaterChunkRefusedReportsWhatMovedAndWhatIsPending(t *testing.T) {
	t.Parallel()

	calls := 0
	c, s := rankClient(t, jiratest.WithHandler(http.MethodPut, rankPath, func(w http.ResponseWriter, r *http.Request) {
		calls++
		if calls == 1 {
			jsonHandler(http.StatusNoContent, "")(w, r)
			return
		}
		jsonHandler(http.StatusForbidden, `{"errorMessages":["You do not have permission to rank these issues."],"errors":{}}`)(w, r)
	}))

	keys := rankKeys(80)
	err := c.RankIssues(t.Context(), keys, jira.RankBefore("EX-999"))

	var partial *jira.PartialRankError
	if !errors.As(err, &partial) {
		t.Fatalf("got %T (%v), want a *jira.PartialRankError", err, err)
	}
	if !slices.Equal(partial.Ranked, keys[:50]) {
		t.Errorf("Ranked = %v, want the first 50 keys the first chunk moved", partial.Ranked)
	}
	if !slices.Equal(partial.Pending, keys[50:]) {
		t.Errorf("Pending = %v, want the 30 the refusal stopped", partial.Pending)
	}
	if len(partial.Failed) != 0 {
		t.Errorf("Failed = %+v, want none: the second chunk was refused whole, not issue by issue", partial.Failed)
	}
	var refused *jira.CapabilityError
	if !errors.As(err, &refused) {
		t.Fatalf("the partial error does not unwrap to the *jira.CapabilityError underneath: %v", err)
	}
	if chunks := len(s.Requests()); chunks != 2 {
		t.Errorf("the walk made %d calls, want 2: the first, then the refusal", chunks)
	}
}

func TestRankIssues_AFirstChunkRefusedIsThePlainError(t *testing.T) {
	t.Parallel()

	c, _ := rankClient(t, jiratest.WithStatus(http.MethodPut, rankPath, http.StatusForbidden, "plans_403.json"))

	err := c.RankIssues(t.Context(), []string{"EX-1"}, jira.RankBefore("EX-9"))
	var partial *jira.PartialRankError
	if errors.As(err, &partial) {
		t.Errorf("a first-chunk refusal came back as a partial rank of %+v, want the plain error: nothing moved", partial)
	}
	var refused *jira.CapabilityError
	if !errors.As(err, &refused) {
		t.Fatalf("got %T (%v), want a *jira.CapabilityError", err, err)
	}
}

func TestRankIssues_A429IsARateLimitError(t *testing.T) {
	t.Parallel()

	c, _ := rankClient(t, jiratest.WithRateLimit(http.MethodPut, rankPath, 30*time.Second))

	err := c.RankIssues(t.Context(), []string{"EX-1"}, jira.RankBefore("EX-9"))
	var limited *jira.RateLimitError
	if !errors.As(err, &limited) {
		t.Fatalf("got %T (%v), want a *jira.RateLimitError", err, err)
	}
	if limited.RetryAfter != 30*time.Second {
		t.Errorf("RetryAfter = %s, want 30s", limited.RetryAfter)
	}
}

func TestRankIssues_A502AndADeadServerAreTransportFailures(t *testing.T) {
	t.Parallel()

	t.Run("a 502", func(t *testing.T) {
		t.Parallel()

		c, _ := rankClient(t, jiratest.WithStatus(http.MethodPut, rankPath, http.StatusBadGateway, ""))
		err := c.RankIssues(t.Context(), []string{"EX-1"}, jira.RankBefore("EX-9"))
		var broken *jira.TransportError
		if !errors.As(err, &broken) {
			t.Fatalf("got %T (%v), want a *jira.TransportError", err, err)
		}
		if broken.Status != http.StatusBadGateway {
			t.Errorf("Status = %d, want 502", broken.Status)
		}
	})

	t.Run("a dead server", func(t *testing.T) {
		t.Parallel()

		s := jiratest.NewServer()
		dead := s.URL()
		s.Close()
		c, _ := testClient(t, dead, WithRetry(RetryPolicy{Attempts: 1}))

		err := c.RankIssues(t.Context(), []string{"EX-1"}, jira.RankBefore("EX-9"))
		var broken *jira.TransportError
		if !errors.As(err, &broken) {
			t.Fatalf("got %T (%v), want a *jira.TransportError", err, err)
		}
		if broken.Status != 0 {
			t.Errorf("Status = %d, want 0: nothing answered", broken.Status)
		}
	})
}

func TestRankIssues_A207BodyThatIsNotJSONIsATransportFailure(t *testing.T) {
	t.Parallel()

	c, _ := rankClient(t, jiratest.WithHandler(http.MethodPut, rankPath,
		jsonHandler(http.StatusMultiStatus, "<html>your proxy has opinions</html>")))

	err := c.RankIssues(t.Context(), []string{"EX-1"}, jira.RankBefore("EX-9"))
	var broken *jira.TransportError
	if !errors.As(err, &broken) {
		t.Fatalf("got %T (%v), want a *jira.TransportError", err, err)
	}
}

func TestRankIssues_ComesBackWithTheCallersOwnErrorWhenItCancels(t *testing.T) {
	t.Parallel()

	c, s := rankClient(t)
	ctx, cancel := context.WithCancel(t.Context())
	cancel()

	if err := c.RankIssues(ctx, []string{"EX-1"}, jira.RankBefore("EX-9")); !errors.Is(err, context.Canceled) {
		t.Fatalf("got %v, want context.Canceled unwrapped", err)
	}
	if served := len(s.Requests()); served != 0 {
		t.Errorf("the site was sent %v after the caller had already gone", served)
	}
}

func TestRankIssues_ValidationCasesSendNoRequest(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		keys []string
		at   jira.RankPosition
	}{
		{name: "no anchor at all", keys: []string{"EX-1"}, at: jira.RankPosition{}},
		{name: "both anchors set", keys: []string{"EX-1"}, at: jira.RankPosition{Before: "EX-8", After: "EX-9"}},
		{name: "the anchor is one of the issues being ranked", keys: []string{"EX-1", "EX-2"}, at: jira.RankBefore("EX-2")},
		{name: "a FieldID that is not a custom field id", keys: []string{"EX-1"}, at: jira.RankPosition{Before: "EX-9", FieldID: "status"}},
		{name: "a FieldID of zero", keys: []string{"EX-1"}, at: jira.RankPosition{Before: "EX-9", FieldID: "customfield_0"}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			c, s := rankClient(t)
			err := c.RankIssues(t.Context(), tt.keys, tt.at)
			var invalid *jira.ValidationError
			if !errors.As(err, &invalid) {
				t.Fatalf("got %T (%v), want a *jira.ValidationError", err, err)
			}
			if served := len(s.Requests()); served != 0 {
				t.Errorf("the site was sent %d requests for a rank refused before the wire", served)
			}
		})
	}
}

func TestRankIssues_AnEmptyKeyListSendsNothingAndReturnsNil(t *testing.T) {
	t.Parallel()

	c, s := rankClient(t)
	if err := c.RankIssues(t.Context(), nil, jira.RankBefore("EX-9")); err != nil {
		t.Fatalf("ranking nothing: %v", err)
	}
	if served := len(s.Requests()); served != 0 {
		t.Errorf("ranking no issues cost %d requests", served)
	}
}

func TestRankIssues_DeduplicatesRepeatedKeys(t *testing.T) {
	t.Parallel()

	c, s := rankClient(t)
	if err := c.RankIssues(t.Context(), []string{"EX-1", "EX-2", "EX-1"}, jira.RankBefore("EX-9")); err != nil {
		t.Fatalf("ranking: %v", err)
	}
	body := sentBody(t, sentTo(t, s, http.MethodPut, rankPath))
	raw, _ := body["issues"].([]any)
	if len(raw) != 2 {
		t.Errorf("issues = %v, want the two distinct keys sent once each", raw)
	}
}
