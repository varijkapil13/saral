package jiratest_test

import (
	"bytes"
	"encoding/json"
	"errors"
	"io/fs"
	"maps"
	"net/http"
	"path"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/varijkapil13/saral/pkg/adf"
	"github.com/varijkapil13/saral/pkg/jira"
	"github.com/varijkapil13/saral/pkg/jira/jiratest"
)

// srvAllowedAt are the only spans in which an "@" may appear. A mention handle
// is one of them because ADF writes the "@" into the node's text attribute; it
// cannot be an address, which needs a local part in front of the sign.
var srvAllowedAt = []string{"user@example.com", "@user"}

var (
	srvHostRe  = regexp.MustCompile(`(?i)[a-z0-9.-]*atlassian\.net`)
	srvTokenRe = regexp.MustCompile(`(?i)ATATT[a-z0-9]{8,}|bearer\s+[a-z0-9._~+/=-]{8,}|basic\s+[a-z0-9+/=]{8,}|\b[0-9a-f]{32,}\b|"(?:password|secret|api[_-]?key|authorization|credential)"`)
)

func srvFixtureTree(t *testing.T) map[string][]byte {
	t.Helper()
	out := make(map[string][]byte)
	err := fs.WalkDir(jiratest.Fixtures, ".", func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			return nil
		}
		b, err := fs.ReadFile(jiratest.Fixtures, p)
		if err != nil {
			return err
		}
		out[p] = b
		return nil
	})
	if err != nil {
		t.Fatalf("walking the fixture tree: %v", err)
	}
	if len(out) == 0 {
		t.Fatal("the embedded fixture tree is empty")
	}
	return out
}

func srvJSONFixtures(t *testing.T) map[string][]byte {
	t.Helper()
	out := make(map[string][]byte)
	for name, body := range srvFixtureTree(t) {
		switch path.Ext(name) {
		case ".json":
			out[name] = body
		case ".md":
		default:
			t.Errorf("%s: a fixture must be .json, or the README", name)
		}
	}
	return out
}

func TestFixtures_AreAllValidJSON(t *testing.T) {
	t.Parallel()

	for name, body := range srvJSONFixtures(t) {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			var v any
			if err := json.Unmarshal(body, &v); err != nil {
				t.Fatalf("%s is not valid JSON: %v", name, err)
			}
		})
	}
}

func TestFixtures_CarryNoEmailAddressBeyondThePlaceholder(t *testing.T) {
	t.Parallel()

	for name, body := range srvJSONFixtures(t) {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			s := string(body)
			for i, r := range s {
				if r != '@' {
					continue
				}
				if !srvAtIsAllowed(s, i) {
					t.Errorf("%s: unscrubbed address near %q", name, srvAround(s, i))
				}
			}
		})
	}
}

// srvAtIsAllowed reports whether the "@" at i falls inside one of the spans an
// unscrubbed fixture may never contain an address outside of.
func srvAtIsAllowed(s string, i int) bool {
	for _, allowed := range srvAllowedAt {
		at := strings.IndexByte(allowed, '@')
		start := i - at
		if start < 0 || start+len(allowed) > len(s) {
			continue
		}
		if s[start:start+len(allowed)] == allowed {
			return true
		}
	}
	return false
}

func srvAround(s string, i int) string {
	start := max(0, i-24)
	end := min(len(s), i+24)
	return s[start:end]
}

func TestFixtures_NameOnlyTheExampleSite(t *testing.T) {
	t.Parallel()

	for name, body := range srvJSONFixtures(t) {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			for _, host := range srvHostRe.FindAllString(string(body), -1) {
				if host != "example.atlassian.net" {
					t.Errorf("%s: unscrubbed host %q", name, host)
				}
			}
		})
	}
}

func TestFixtures_CarryNothingThatLooksLikeACredential(t *testing.T) {
	t.Parallel()

	for name, body := range srvJSONFixtures(t) {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			if hit := srvTokenRe.FindString(string(body)); hit != "" {
				t.Errorf("%s: looks like a credential: %q", name, hit)
			}
		})
	}
}

// srvRealAccountRe is the shape of a live Atlassian account id: a numeric site
// prefix, a colon, then a UUID. The placeholder ids these fixtures use are the
// older opaque form and do not match it.
var srvRealAccountRe = regexp.MustCompile(`\b\d{5,7}:[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}\b`)

// A real account id reached a public branch through the `self` link of a
// captured /myself, where the scrubber's field-level rules did not look. The
// scrubber handles it now; this is the check that would have caught it first.
func TestFixtures_CarryNoRealAccountID(t *testing.T) {
	t.Parallel()

	for name, body := range srvJSONFixtures(t) {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			if hit := srvRealAccountRe.FindString(string(body)); hit != "" {
				t.Errorf("%s carries what looks like a live Atlassian account id: %q", name, hit)
			}
		})
	}
}

func TestFixtures_CoverEveryResponseTheServerReplays(t *testing.T) {
	t.Parallel()

	want := []string{
		"approximate_count.json",
		"attachment_disabled.json",
		"attachment_meta.json",
		"attachment_upload.json",
		"board.json",
		"board_config_estimation.json",
		"board_config_no_estimation.json",
		"board_epics.json",
		"board_issues.json",
		"board_quickfilters.json",
		"board_quickfilters_empty.json",
		"bulk_400.json",
		"bulk_403.json",
		"bulkmove_submit.json",
		"bulkmove_task_complete.json",
		"bulkmove_task_enqueued.json",
		"bulkmove_task_failed.json",
		"bulkmove_task_running.json",
		"comments.json",
		"configuration.json",
		"createmeta_bug.json",
		"createmeta_issuetypes.json",
		"createmeta_task.json",
		"editmeta.json",
		"field.json",
		"field_localised.json",
		"forbidden_browse_users.json",
		"issue_rich_adf.json",
		"labels.json",
		"labels_page2.json",
		"mypermissions_admin.json",
		"mypermissions_basic.json",
		"myself.json",
		"not_found_board.json",
		"plans_403.json",
		"plans_ok.json",
		"priority_search.json",
		"problem_method_not_allowed.json",
		"problem_no_endpoint.json",
		"project_statuses.json",
		"rate_limited.json",
		"search_page1.json",
		"search_page2.json",
		"sprint_created.json",
		"sprint_one.json",
		"sprint_page.json",
		"sprint_updated.json",
		"task_cancel_requested.json",
		"task_complete.json",
		"task_enqueued.json",
		"task_failed.json",
		"task_running.json",
		"transitions.json",
		"user_assignable.json",
		"user_bulk.json",
		"user_bulk_page2.json",
		"user_search.json",
		"validation_error.json",
		"version_created.json",
		"version_one.json",
		"version_released.json",
		"version_unresolved_count.json",
		"versions.json",
	}
	got := slices.Sorted(maps.Keys(srvJSONFixtures(t)))
	if !slices.Equal(got, want) {
		t.Errorf("fixture set drifted:\n got %v\nwant %v", got, want)
	}
}

// The shapes a filter picker reads, none of which is the shape beside it.
func TestFixtures_CoverTheShapesAPeoplePickerReads(t *testing.T) {
	t.Parallel()

	t.Run("an account search is a bare array with no envelope at all", func(t *testing.T) {
		t.Parallel()

		for _, name := range []string{"user_search.json", "user_assignable.json"} {
			var rows []map[string]any
			if err := json.Unmarshal(srvFixture(t, name), &rows); err != nil {
				t.Fatalf("%s is not the bare array the endpoint answers: %v", name, err)
			}
			if len(rows) == 0 {
				t.Fatalf("%s has no rows, so nothing decodes it wrongly and passes", name)
			}
			for _, row := range rows {
				for _, key := range []string{"accountId", "accountType", "displayName"} {
					if _, ok := row[key]; !ok {
						t.Errorf("%s: an account sends no %s", name, key)
					}
				}
			}
		}
	})

	t.Run("the site-wide search answers every kind of account", func(t *testing.T) {
		t.Parallel()

		kinds := srvAccountTypes(t, "user_search.json")
		for _, want := range []string{"atlassian", "app", "customer"} {
			if !slices.Contains(kinds, want) {
				t.Errorf("user_search.json lists %v, and a real site holds a %q too", kinds, want)
			}
		}
		// The assignable endpoint drops the accounts that cannot be given work,
		// which is a shape claim and not a convenience: it is what makes the
		// scoped search readable on a site that is mostly robots.
		for _, kind := range srvAccountTypes(t, "user_assignable.json") {
			if kind != "atlassian" {
				t.Errorf("user_assignable.json lists a %q account, and the endpoint answers only people", kind)
			}
		}
	})

	t.Run("an account id can carry a colon", func(t *testing.T) {
		t.Parallel()

		found := false
		for _, row := range srvAccounts(t, "user_search.json") {
			id, _ := row["accountId"].(string)
			found = found || strings.Contains(id, ":")
		}
		if !found {
			t.Error("no account id carries a colon, and anything that splits an id or builds a JQL clause has to survive one")
		}
	})

	t.Run("a bulk read answers null for an id the site does not know", func(t *testing.T) {
		t.Parallel()

		page := srvDecode(t, srvFixture(t, "user_bulk.json"))
		values, ok := page["values"].([]any)
		if !ok || len(values) == 0 {
			t.Fatalf("user_bulk.json values = %v, want the array the envelope carries", page["values"])
		}
		if !slices.Contains(values, nil) {
			t.Error("no entry is null, and a null is what an unknown id decodes into as a blank row")
		}
		// The pages have to chain, or the walk that counts the null never
		// reaches the account after it.
		if last, _ := page["isLast"].(bool); last {
			t.Error("the first page says isLast, so the second is never asked for")
		}
		if final, _ := srvDecode(t, srvFixture(t, "user_bulk_page2.json"))["isLast"].(bool); !final {
			t.Error("the last page does not say isLast")
		}
	})

	t.Run("a project's statuses are per issue type, and two ids share a name", func(t *testing.T) {
		t.Parallel()

		var types []struct {
			ID       string `json:"id"`
			Name     string `json:"name"`
			Statuses []struct {
				ID   string `json:"id"`
				Name string `json:"name"`
			} `json:"statuses"`
		}
		if err := json.Unmarshal(srvFixture(t, "project_statuses.json"), &types); err != nil {
			t.Fatalf("project_statuses.json is not the bare array the endpoint answers: %v", err)
		}
		if len(types) < 2 {
			t.Fatalf("got %d issue types, want more than one so that two workflows can differ", len(types))
		}

		ids := make(map[string]map[string]bool)
		widths := make(map[int]bool)
		for _, entry := range types {
			widths[len(entry.Statuses)] = true
			for _, status := range entry.Statuses {
				if ids[status.Name] == nil {
					ids[status.Name] = make(map[string]bool)
				}
				ids[status.Name][status.ID] = true
			}
		}
		if len(widths) < 2 {
			t.Error("every issue type reaches the same number of statuses, and two types in one project run different workflows")
		}
		if len(ids["In Review"]) < 2 {
			t.Errorf(`"In Review" resolves to %v, want the two ids a team-managed project mints under one name`, ids["In Review"])
		}
	})

	t.Run("labels are bare strings and not all of them are ASCII", func(t *testing.T) {
		t.Parallel()

		seen := make([]string, 0, 8)
		for _, name := range []string{"labels.json", "labels_page2.json"} {
			var page struct {
				Values []string `json:"values"`
			}
			if err := json.Unmarshal(srvFixture(t, name), &page); err != nil {
				t.Fatalf("%s does not carry an array of bare strings: %v", name, err)
			}
			seen = append(seen, page.Values...)
		}
		if len(seen) == 0 {
			t.Fatal("the label pages are empty")
		}
		if !slices.ContainsFunc(seen, func(l string) bool { return utf8.RuneCountInString(l) != len(l) }) {
			t.Errorf("every label in %q is ASCII, and a label is whatever anybody typed", seen)
		}
	})
}

func srvAccounts(t *testing.T, fixture string) []map[string]any {
	t.Helper()

	var rows []map[string]any
	if err := json.Unmarshal(srvFixture(t, fixture), &rows); err != nil {
		t.Fatalf("%s is not a bare array of accounts: %v", fixture, err)
	}
	return rows
}

func srvAccountTypes(t *testing.T, fixture string) []string {
	t.Helper()

	out := make([]string, 0, 8)
	for _, row := range srvAccounts(t, fixture) {
		kind, _ := row["accountType"].(string)
		out = append(out, kind)
	}
	return out
}

// Three paging envelopes, and no endpoint says which one it answers in.
func TestFixtures_CoverAllThreeAgilePagingEnvelopes(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		fixture  string
		array    string
		absent   []string
		required []string
	}{
		{
			name: "the board's issues and its backlog", fixture: "board_issues.json",
			array: "issues", absent: []string{"isLast", "values"}, required: []string{"startAt", "maxResults", "total"},
		},
		{
			name: "an epic page", fixture: "board_epics.json",
			array: "values", absent: []string{"total", "issues"}, required: []string{"startAt", "maxResults", "isLast"},
		},
		{
			name: "a sprint page, which sends all four", fixture: "sprint_page.json",
			array: "values", absent: []string{"issues"}, required: []string{"startAt", "maxResults", "total", "isLast"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			body := srvDecode(t, srvFixture(t, tt.fixture))

			rows, ok := body[tt.array].([]any)
			if !ok {
				t.Fatalf("%s does not name its array %q", tt.fixture, tt.array)
			}
			if len(rows) == 0 {
				t.Errorf("%s has no rows, so nothing decodes it wrongly and passes", tt.fixture)
			}
			for _, key := range tt.required {
				if _, ok := body[key]; !ok {
					t.Errorf("%s sends no %s", tt.fixture, key)
				}
			}
			for _, key := range tt.absent {
				if _, ok := body[key]; ok {
					t.Errorf("%s sends %s, and the endpoint it stands for does not", tt.fixture, key)
				}
			}
		})
	}
}

// The last page of a cursor walk says isLast and stops sending a token, so a
// decoder that only reads one of the two still terminates.
func TestFixtures_TheLastSearchPageSaysIsLastAndSendsNoToken(t *testing.T) {
	t.Parallel()

	last := srvDecode(t, srvFixture(t, "search_page2.json"))
	if isLast, _ := last["isLast"].(bool); !isLast {
		t.Errorf("search_page2.json isLast = %v, want true", last["isLast"])
	}
	if _, ok := last["nextPageToken"]; ok {
		t.Error("the last page still carries a nextPageToken key, and a real one omits it entirely")
	}

	first := srvDecode(t, srvFixture(t, "search_page1.json"))
	if token, _ := first["nextPageToken"].(string); token == "" {
		t.Error("search_page1.json hands out no token, so the walk it stands for is one page long")
	}
}

// The shapes a lookup by display name cannot resolve, all of which a real
// catalogue carries.
func TestFixtures_TheLocalisedCatalogueCarriesTheNamesThatDoNotResolve(t *testing.T) {
	t.Parallel()

	var fields []struct {
		ID               string   `json:"id"`
		Name             string   `json:"name"`
		UntranslatedName string   `json:"untranslatedName"`
		Custom           bool     `json:"custom"`
		ClauseNames      []string `json:"clauseNames"`
	}
	if err := json.Unmarshal(srvFixture(t, "field_localised.json"), &fields); err != nil {
		t.Fatalf("decoding field_localised.json: %v", err)
	}

	var compressed, unJQLable, translated int
	names := make(map[string]int)
	for _, f := range fields {
		names[strings.ToLower(f.Name)]++
		if f.ClauseNames != nil && len(f.ClauseNames) == 0 {
			unJQLable++
		}
		if !f.Custom {
			if f.UntranslatedName != "" {
				t.Errorf("%s is a system field carrying an untranslatedName, which Jira sends on custom fields only", f.ID)
			}
			continue
		}
		if f.UntranslatedName == "" {
			t.Errorf("%s is a custom field with no untranslatedName", f.ID)
		}
		if !strings.EqualFold(f.UntranslatedName, f.Name) {
			translated++
		}
		if srvRunTogetherRe.MatchString(f.UntranslatedName) {
			compressed++
		}
	}

	if compressed == 0 {
		t.Error("no field spells its untranslatedName as one run-together word, which is the case a lookup by display name misses: the English screen puts a space in it")
	}
	if unJQLable == 0 {
		t.Error("no field sends an empty clauseNames, so nothing proves a field can be unaddressable in JQL")
	}
	if translated == 0 {
		t.Error("no field's display name differs from its untranslated one, so this is not a localised catalogue")
	}
	var collisions int
	for _, count := range names {
		if count > 1 {
			collisions++
		}
	}
	if collisions == 0 {
		t.Error("no two fields share a display name, which is what translation does to two of a site's statuses")
	}
}

// srvRunTogetherRe is the compressed spelling untranslatedName uses: two words
// with the separator taken out, which is never what a screen displays.
var srvRunTogetherRe = regexp.MustCompile(`[a-z][A-Z]`)

func TestFixtures_CarryBothEnvelopesASiteRefusesIn(t *testing.T) {
	t.Parallel()

	t.Run("the Agile shape, whose sentence is under a URL parameter", func(t *testing.T) {
		t.Parallel()
		body := srvDecode(t, srvFixture(t, "not_found_board.json"))

		if msgs, _ := body["errorMessages"].([]any); len(msgs) != 0 {
			t.Errorf("errorMessages = %v, want the empty array the endpoint sends", msgs)
		}
		errs, ok := body["errors"].(map[string]any)
		if !ok || len(errs) == 0 {
			t.Fatalf("errors = %v, want the object holding the reason", body["errors"])
		}
		for key, value := range errs {
			if !strings.HasSuffix(key, "Id") {
				t.Errorf("errors is keyed by %q; the shape being pinned is a URL parameter name", key)
			}
			if sentence, _ := value.(string); len(sentence) < 20 {
				t.Errorf("%q carries %q, which is not the sentence a user has to read", key, sentence)
			}
		}
	})

	for _, name := range []string{"problem_no_endpoint.json", "problem_method_not_allowed.json"} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			body := srvDecode(t, srvFixture(t, name))

			for _, key := range []string{"type", "title", "status", "detail", "instance"} {
				if _, ok := body[key]; !ok {
					t.Errorf("%s sends no %s", name, key)
				}
			}
			for _, key := range []string{"errorMessages", "errors", "message"} {
				if _, ok := body[key]; ok {
					t.Errorf("%s carries %s, and a problem+json body does not", name, key)
				}
			}
			if kind, _ := body["type"].(string); kind != "about:blank" {
				t.Errorf("type = %q, want about:blank", kind)
			}
			status, _ := body["status"].(float64)
			if title, _ := body["title"].(string); title != http.StatusText(int(status)) {
				t.Errorf("title = %q, want the status %d spelt out", title, int(status))
			}
			if detail, _ := body["detail"].(string); detail == "" {
				t.Errorf("%s carries no detail, which is the only part that says anything", name)
			}
		})
	}
}

func TestFixtures_RichDescriptionRoundTripsByteStably(t *testing.T) {
	t.Parallel()

	raw := srvDescription(t)
	doc, err := adf.Unmarshal(raw)
	if err != nil {
		t.Fatalf("parsing the description: %v", err)
	}
	out, err := adf.Marshal(doc)
	if err != nil {
		t.Fatalf("re-encoding the description: %v", err)
	}
	if string(out) != string(raw) {
		t.Errorf("the description did not survive the round trip byte for byte:\n got %s\nwant %s", out, raw)
	}
}

func TestFixtures_RichDescriptionExercisesTheNodesARendererMustHandle(t *testing.T) {
	t.Parallel()

	doc, err := adf.Unmarshal(srvDescription(t))
	if err != nil {
		t.Fatalf("parsing the description: %v", err)
	}
	types := doc.NodeTypes()
	for _, want := range []string{
		"paragraph", "heading", "bulletList", "listItem", "codeBlock", "panel",
		"table", "tableRow", "tableHeader", "tableCell", "mediaSingle", "media",
		"mention", "inlineCard", "text",
		// Not an ADF node type on any published schema: the preservation path
		// needs something it cannot possibly model.
		"futureBlock",
	} {
		if types[want] == 0 {
			t.Errorf("the description has no %s node", want)
		}
	}

	marks := make(map[string]int)
	doc.Walk(func(n adf.Node) bool {
		for _, m := range n.Marks {
			marks[m.Type]++
		}
		return true
	})
	for _, want := range []string{"link", "strong", "code"} {
		if marks[want] == 0 {
			t.Errorf("the description carries no %s mark", want)
		}
	}
}

func TestFixtures_CommentBodiesRoundTripByteStably(t *testing.T) {
	t.Parallel()

	var page struct {
		Comments []struct {
			ID   string          `json:"id"`
			Body json.RawMessage `json:"body"`
		} `json:"comments"`
	}
	if err := json.Unmarshal(srvFixture(t, "comments.json"), &page); err != nil {
		t.Fatalf("decoding comments.json: %v", err)
	}
	if len(page.Comments) == 0 {
		t.Fatal("comments.json has no comments")
	}
	for _, c := range page.Comments {
		t.Run(c.ID, func(t *testing.T) {
			t.Parallel()
			doc, err := adf.Unmarshal(c.Body)
			if err != nil {
				t.Fatalf("parsing the body: %v", err)
			}
			out, err := adf.Marshal(doc)
			if err != nil {
				t.Fatalf("re-encoding the body: %v", err)
			}
			if string(out) != string(c.Body) {
				t.Errorf("body changed:\n got %s\nwant %s", out, c.Body)
			}
		})
	}
}

func srvDescription(t *testing.T) json.RawMessage {
	t.Helper()
	var issue struct {
		Fields struct {
			Description json.RawMessage `json:"description"`
		} `json:"fields"`
	}
	if err := json.Unmarshal(srvFixture(t, "issue_rich_adf.json"), &issue); err != nil {
		t.Fatalf("decoding issue_rich_adf.json: %v", err)
	}
	if len(issue.Fields.Description) == 0 {
		t.Fatal("issue_rich_adf.json has no description")
	}
	return issue.Fields.Description
}

// srvTaskProgress is TaskProgressBeanObject, the body the generic task endpoint
// answers: timestamps in epoch millis, submittedBy a legacy numeric user id
// rather than an account id, and a result the schema leaves untyped.
type srvTaskProgress struct {
	Self           string          `json:"self"`
	ID             string          `json:"id"`
	Description    string          `json:"description"`
	Status         string          `json:"status"`
	Message        string          `json:"message"`
	Progress       int64           `json:"progress"`
	ElapsedRuntime int64           `json:"elapsedRuntime"`
	Submitted      int64           `json:"submitted"`
	Started        *int64          `json:"started"`
	LastUpdate     int64           `json:"lastUpdate"`
	Finished       *int64          `json:"finished"`
	SubmittedBy    int64           `json:"submittedBy"`
	Result         json.RawMessage `json:"result"`
}

// srvBulkProgress is the bulk-move queue body: the same three key names as the
// one above and eight of its own, and submittedBy is an object here.
type srvBulkProgress struct {
	TaskID          string `json:"taskId"`
	Status          string `json:"status"`
	ProgressPercent int64  `json:"progressPercent"`
	SubmittedBy     struct {
		AccountID string `json:"accountId"`
	} `json:"submittedBy"`
	Created                         int64               `json:"created"`
	Started                         int64               `json:"started"`
	Updated                         int64               `json:"updated"`
	TotalIssueCount                 int64               `json:"totalIssueCount"`
	ProcessedAccessibleIssues       []int64             `json:"processedAccessibleIssues"`
	FailedAccessibleIssues          map[string][]string `json:"failedAccessibleIssues"`
	InvalidOrInaccessibleIssueCount int64               `json:"invalidOrInaccessibleIssueCount"`
}

var srvTaskStates = map[string]string{
	"task_enqueued.json":         "ENQUEUED",
	"task_running.json":          "RUNNING",
	"task_cancel_requested.json": "CANCEL_REQUESTED",
	"task_complete.json":         "COMPLETE",
	"task_failed.json":           "FAILED",
}

func srvTaskFixtureNames() []string { return slices.Sorted(maps.Keys(srvTaskStates)) }

// srvTaskHasStopped is the port's own predicate, so a fixture and Done() cannot
// drift apart. CANCEL_REQUESTED is deliberately on the running side of it.
func srvTaskHasStopped(status string) bool {
	return jira.TaskState(status).Done()
}

func srvDecodeStrict(t *testing.T, body []byte, into any) {
	t.Helper()
	dec := json.NewDecoder(bytes.NewReader(body))
	dec.DisallowUnknownFields()
	if err := dec.Decode(into); err != nil {
		t.Fatalf("decoding: %v", err)
	}
}

func srvTask(t *testing.T, name string) srvTaskProgress {
	t.Helper()
	var task srvTaskProgress
	srvDecodeStrict(t, srvFixture(t, name), &task)
	return task
}

func TestFixtures_GenericTaskAnswersTaskProgressBeanObject(t *testing.T) {
	t.Parallel()

	for _, name := range srvTaskFixtureNames() {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			task := srvTask(t, name)

			if task.ID == "" {
				t.Error("no id")
			}
			if want := "https://example.atlassian.net/rest/api/3/task/" + task.ID; task.Self != want {
				t.Errorf("self = %q, want %q", task.Self, want)
			}
			if task.SubmittedBy <= 0 {
				t.Error("submittedBy is not a numeric user id")
			}
			if got, want := task.Status, srvTaskStates[name]; got != want {
				t.Errorf("status = %q, want %q", got, want)
			}
			if task.Progress < 0 || task.Progress > 100 {
				t.Errorf("progress = %d, want a percentage", task.Progress)
			}
			if task.Description == "" {
				t.Error("no description")
			}
		})
	}
}

// A millisecond timestamp of this era has thirteen digits; a seconds one has ten.
const srvMillisFloor = 1_000_000_000_000

func TestFixtures_GenericTaskTimestampsAreEpochMillis(t *testing.T) {
	t.Parallel()

	for _, name := range srvTaskFixtureNames() {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			task := srvTask(t, name)

			stamps := map[string]int64{"submitted": task.Submitted, "lastUpdate": task.LastUpdate}
			if task.Started != nil {
				stamps["started"] = *task.Started
			}
			if task.Finished != nil {
				stamps["finished"] = *task.Finished
			}
			for field, at := range stamps {
				if at < srvMillisFloor {
					t.Errorf("%s = %d, which is not epoch millis", field, at)
				}
			}
			if task.LastUpdate < task.Submitted {
				t.Errorf("lastUpdate %d predates submitted %d", task.LastUpdate, task.Submitted)
			}
		})
	}
}

func TestFixtures_GenericTaskRuntimeAgreesWithItsTimestamps(t *testing.T) {
	t.Parallel()

	for _, name := range srvTaskFixtureNames() {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			task := srvTask(t, name)

			if task.Started == nil {
				if task.Status != "ENQUEUED" {
					t.Errorf("a %s task has no started", task.Status)
				}
				if task.ElapsedRuntime != 0 {
					t.Errorf("elapsedRuntime = %d on a task that never started", task.ElapsedRuntime)
				}
				if task.LastUpdate != task.Submitted {
					t.Errorf("lastUpdate %d moved on a task that never started", task.LastUpdate)
				}
				return
			}
			if want := task.LastUpdate - *task.Started; task.ElapsedRuntime != want {
				t.Errorf("elapsedRuntime = %d, want lastUpdate - started = %d", task.ElapsedRuntime, want)
			}
			if *task.Started < task.Submitted {
				t.Errorf("started %d predates submitted %d", *task.Started, task.Submitted)
			}
		})
	}
}

func TestFixtures_GenericTaskFinishesOnlyWhenItHasStopped(t *testing.T) {
	t.Parallel()

	for _, name := range srvTaskFixtureNames() {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			task := srvTask(t, name)

			if stopped := srvTaskHasStopped(task.Status); stopped != (task.Finished != nil) {
				t.Errorf("status %s with finished set = %v", task.Status, task.Finished != nil)
			}
			if task.Finished != nil && *task.Finished != task.LastUpdate {
				t.Errorf("finished %d and lastUpdate %d disagree", *task.Finished, task.LastUpdate)
			}
		})
	}
}

// The schema types result as unknown and the published example sends a string,
// so a fixture that also sent a string would let `Result string` pass here.
func TestFixtures_GenericTaskResultIsOnlyReadableAsRawJSON(t *testing.T) {
	t.Parallel()

	for _, name := range srvTaskFixtureNames() {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			task := srvTask(t, name)

			if !srvTaskHasStopped(task.Status) {
				if task.Result != nil {
					t.Errorf("a %s task already carries a result: %s", task.Status, task.Result)
				}
				return
			}
			if task.Result == nil {
				t.Fatalf("a %s task carries no result", task.Status)
			}
			var asString string
			if err := json.Unmarshal(task.Result, &asString); err == nil {
				t.Error("result decoded into a string, so it no longer pins the untyped shape")
			}
			var asObject map[string]any
			if err := json.Unmarshal(task.Result, &asObject); err != nil {
				t.Fatalf("result is neither a string nor an object: %v", err)
			}
			if _, ok := asObject["modifiedIssues"]; !ok {
				t.Errorf("result names no modified issues: %s", task.Result)
			}
		})
	}
}

func TestFixtures_TheTwoTaskShapesAreNotInterchangeable(t *testing.T) {
	t.Parallel()

	generic := srvFixture(t, "task_complete.json")
	bulk := srvFixture(t, "bulkmove_task_complete.json")

	t.Run("the generic body does not decode as bulk-move progress", func(t *testing.T) {
		t.Parallel()
		var into srvBulkProgress
		srvWantSubmittedByTypeError(t, json.Unmarshal(generic, &into))
	})

	t.Run("the bulk-move body does not decode as a task progress bean", func(t *testing.T) {
		t.Parallel()
		var into srvTaskProgress
		srvWantSubmittedByTypeError(t, json.Unmarshal(bulk, &into))
	})

	// status and started line up; submittedBy is the type clash above.
	t.Run("the two bodies overlap on three keys and no more", func(t *testing.T) {
		t.Parallel()
		shared := slices.Sorted(maps.Keys(srvSharedKeys(t, generic, bulk)))
		if want := []string{"started", "status", "submittedBy"}; !slices.Equal(shared, want) {
			t.Errorf("shared keys = %v, want %v", shared, want)
		}
	})

	t.Run("the two endpoints number their tasks separately", func(t *testing.T) {
		t.Parallel()
		var task srvTaskProgress
		srvDecodeStrict(t, generic, &task)
		var queued srvBulkProgress
		srvDecodeStrict(t, bulk, &queued)
		if task.ID == queued.TaskID {
			t.Errorf("both registries number a task %q, which reads as one task at two endpoints", task.ID)
		}
	})
}

// submittedBy is the field that cannot survive the swap: int64 here, object there.
func srvWantSubmittedByTypeError(t *testing.T, err error) {
	t.Helper()
	var typeErr *json.UnmarshalTypeError
	if !errors.As(err, &typeErr) {
		t.Fatalf("err = %v, want a type error", err)
	}
	if typeErr.Field != "submittedBy" {
		t.Errorf("the type error is on %q, want submittedBy", typeErr.Field)
	}
}

func srvSharedKeys(t *testing.T, left, right []byte) map[string]struct{} {
	t.Helper()
	shared := make(map[string]struct{})
	l, r := srvDecode(t, left), srvDecode(t, right)
	for key := range l {
		if _, ok := r[key]; ok {
			shared[key] = struct{}{}
		}
	}
	return shared
}

func TestFixtures_CoverTheShapesAnAttachmentAnswersIn(t *testing.T) {
	t.Parallel()

	t.Run("the metadata read is one object carrying every key the port maps from", func(t *testing.T) {
		t.Parallel()
		meta := srvDecode(t, srvFixture(t, "attachment_meta.json"))

		for _, key := range []string{"self", "id", "filename", "mimeType", "size", "created", "author", "content", "thumbnail"} {
			if _, ok := meta[key]; !ok {
				t.Errorf("attachment_meta.json sends no %s", key)
			}
		}
		author, ok := meta["author"].(map[string]any)
		if !ok {
			t.Fatalf("author = %v, want the account object every read carries", meta["author"])
		}
		for _, key := range []string{"accountId", "accountType", "displayName"} {
			if _, ok := author[key]; !ok {
				t.Errorf("the author sends no %s", key)
			}
		}
	})

	t.Run("the upload answers a bare array, so a two-file upload has two rows to decode", func(t *testing.T) {
		t.Parallel()
		rows := srvAttachments(t, "attachment_upload.json")

		if len(rows) < 2 {
			t.Fatalf("attachment_upload.json holds %d rows, want the several a multi-file upload answers", len(rows))
		}
		for _, row := range rows {
			for _, key := range []string{"id", "filename", "mimeType", "size", "created", "author", "content"} {
				if _, ok := row[key]; !ok {
					t.Errorf("an uploaded attachment sends no %s", key)
				}
			}
		}
	})

	t.Run("an id is a number on the metadata read and a string wherever else it appears", func(t *testing.T) {
		t.Parallel()

		if _, ok := srvDecode(t, srvFixture(t, "attachment_meta.json"))["id"].(float64); !ok {
			t.Error("attachment_meta.json sends its id as something other than a JSON number")
		}
		for _, fixture := range []string{"attachment_upload.json", "issue_rich_adf.json"} {
			for _, row := range srvAttachments(t, fixture) {
				if _, ok := row["id"].(string); !ok {
					t.Errorf("%s sends an attachment id as something other than a string: %v", fixture, row["id"])
				}
			}
		}
	})

	t.Run("a thumbnail is there only for a file the site could make one of", func(t *testing.T) {
		t.Parallel()

		var withThumb, without int
		for _, row := range srvAttachments(t, "attachment_upload.json") {
			kind, _ := row["mimeType"].(string)
			_, ok := row["thumbnail"]
			switch {
			case ok && strings.HasPrefix(kind, "image/"):
				withThumb++
			case !ok && !strings.HasPrefix(kind, "image/"):
				without++
			default:
				t.Errorf("a %s attachment sends thumbnail = %v", kind, ok)
			}
		}
		if withThumb == 0 || without == 0 {
			t.Errorf("the upload answers %d thumbnailed and %d plain rows, and a renderer has to survive both", withThumb, without)
		}
	})

	t.Run("attachments switched off refuse in the classic envelope", func(t *testing.T) {
		t.Parallel()
		body := srvDecode(t, srvFixture(t, "attachment_disabled.json"))

		if msgs, _ := body["errorMessages"].([]any); len(msgs) == 0 {
			t.Error("attachment_disabled.json carries no errorMessages, which is the only part that says anything")
		}
		if errs, ok := body["errors"].(map[string]any); !ok || len(errs) != 0 {
			t.Errorf("errors = %v, want the empty object this endpoint sends beside the sentence", body["errors"])
		}
		for _, key := range []string{"type", "title", "status", "detail"} {
			if _, ok := body[key]; ok {
				t.Errorf("attachment_disabled.json carries %s, and the classic envelope does not", key)
			}
		}
	})
}

func srvAttachments(t *testing.T, fixture string) []map[string]any {
	t.Helper()

	body := srvFixture(t, fixture)
	var rows []map[string]any
	if err := json.Unmarshal(body, &rows); err == nil {
		return rows
	}
	var issue struct {
		Fields struct {
			Attachment []map[string]any `json:"attachment"`
		} `json:"fields"`
	}
	if err := json.Unmarshal(body, &issue); err != nil {
		t.Fatalf("%s is neither an array of attachments nor an issue carrying them: %v", fixture, err)
	}
	if len(issue.Fields.Attachment) == 0 {
		t.Fatalf("%s carries no attachments", fixture)
	}
	return issue.Fields.Attachment
}

func TestFixtures_TheUnresolvedCountIsItsOwnCallAndOutrunsTheStatusBuckets(t *testing.T) {
	t.Parallel()

	count := srvDecode(t, srvFixture(t, "version_unresolved_count.json"))
	unresolved, ok := count["issuesUnresolvedCount"].(float64)
	if !ok {
		t.Fatalf("issuesUnresolvedCount = %v, and that spelling is the whole reason this fixture exists", count["issuesUnresolvedCount"])
	}
	if _, ok := count["issuesCount"].(float64); !ok {
		t.Errorf("issuesCount = %v, want the total the endpoint sends beside the unresolved one", count["issuesCount"])
	}
	if unresolved <= 0 {
		t.Error("the count is zero, so nothing here reaches the decision a release gate exists to put in front of a user")
	}
	for _, name := range srvVersionEndpoints {
		if strings.Contains(string(srvFixture(t, name)), "issuesUnresolvedCount") {
			t.Errorf("%s carries the unresolved count, and no version read does", name)
		}
	}

	// The buckets count by status category and the gate counts by resolution, so
	// an issue can be done and unresolved at once and the two numbers differ.
	self, _ := count["self"].(string)
	buckets := srvVersionBuckets(t, self)
	var open float64
	for _, key := range []string{"unmapped", "toDo", "inProgress"} {
		value, ok := buckets[key].(float64)
		if !ok {
			t.Fatalf("the version's %s bucket is %v", key, buckets[key])
		}
		open += value
	}
	if unresolved == open {
		t.Errorf("the unresolved count and the open buckets both say %v, so nothing pins that only the count is the gate", open)
	}
}

func srvVersionBuckets(t *testing.T, self string) map[string]any {
	t.Helper()

	var page struct {
		Values []struct {
			Self   string         `json:"self"`
			Status map[string]any `json:"issuesStatusForFixVersion"`
		} `json:"values"`
	}
	if err := json.Unmarshal(srvFixture(t, "versions.json"), &page); err != nil {
		t.Fatalf("decoding versions.json: %v", err)
	}
	for _, v := range page.Values {
		if v.Self == self && v.Status != nil {
			return v.Status
		}
	}
	t.Fatalf("versions.json lists no version %q with status buckets, so the count is for a version nothing else here holds", self)
	return nil
}

// A version reached other than from a version endpoint — an issue's fixVersions,
// a createmeta allowedValue — arrives trimmed, and the overdue rule below does
// not hold for those.
var srvVersionEndpoints = []string{"version_created.json", "version_one.json", "version_released.json", "versions.json"}

func TestFixtures_EveryVersionSaysWhetherItShippedTheWayASiteDoes(t *testing.T) {
	t.Parallel()

	var released, unreleased, trimmed int
	for _, name := range slices.Sorted(maps.Keys(srvJSONFixtures(t))) {
		for _, found := range srvVersions(t, name) {
			at := name + found.at
			if id, ok := found.version["projectId"]; ok {
				if _, number := id.(float64); !number {
					t.Errorf("%s: projectId = %v, and a site sends a JSON number there whatever the port's field type is", at, id)
				}
			}
			if _, ok := found.version["archived"].(bool); !ok {
				t.Errorf("%s: archived = %v, want the bool every version carries", at, found.version["archived"])
			}
			shipped, ok := found.version["released"].(bool)
			if !ok {
				t.Errorf("%s: released = %v, want the bool every version carries", at, found.version["released"])
				continue
			}
			_, overdue := found.version["overdue"]
			switch {
			case !slices.Contains(srvVersionEndpoints, name):
				if overdue {
					t.Errorf("%s: a version trimmed into another read carries overdue, which only a version endpoint sends", at)
				}
				trimmed++
			case shipped && overdue:
				t.Errorf("%s: a released version carries overdue, and a site drops the key", at)
			case !shipped && !overdue:
				t.Errorf("%s: an unreleased version sends no overdue, and a site sends it explicitly false", at)
			case shipped:
				released++
			default:
				unreleased++
			}
		}
	}
	if released == 0 || unreleased == 0 || trimmed == 0 {
		t.Errorf("the sweep saw %d released, %d unreleased and %d trimmed versions, and it means nothing without all three", released, unreleased, trimmed)
	}
}

type srvVersionAt struct {
	at      string
	version map[string]any
}

// Nothing but a version carries any of these, so one is enough to sweep an
// incomplete version in rather than let it escape the walk.
var srvVersionKeys = []string{
	"archived",
	"issuesStatusForFixVersion",
	"overdue",
	"releaseDate",
	"released",
	"userReleaseDate",
	"userStartDate",
}

func srvVersions(t *testing.T, fixture string) []srvVersionAt {
	t.Helper()

	var body any
	if err := json.Unmarshal(srvFixture(t, fixture), &body); err != nil {
		t.Fatalf("decoding %s: %v", fixture, err)
	}
	var out []srvVersionAt
	var walk func(node any, at string)
	walk = func(node any, at string) {
		switch node := node.(type) {
		case map[string]any:
			if slices.ContainsFunc(srvVersionKeys, func(key string) bool { _, ok := node[key]; return ok }) {
				out = append(out, srvVersionAt{at: at, version: node})
			}
			for _, key := range slices.Sorted(maps.Keys(node)) {
				walk(node[key], at+"."+key)
			}
		case []any:
			for i, item := range node {
				walk(item, at+"["+strconv.Itoa(i)+"]")
			}
		}
	}
	walk(body, "")
	return out
}

// srvPlatformLayout is what parses a date-time on a platform field: an offset of
// four digits with no colon in it.
const srvPlatformLayout = "2006-01-02T15:04:05.000-0700"

func TestFixtures_SprintDatesNeedRFC3339InBothSpellingsASiteSends(t *testing.T) {
	t.Parallel()

	if _, err := time.Parse(srvPlatformLayout, srvAttachmentCreated(t)); err != nil {
		t.Fatalf("the platform layout no longer parses an attachment's created: %v", err)
	}

	zulu := make(map[bool][]string)
	for _, fixture := range []string{"sprint_one.json", "sprint_created.json", "sprint_updated.json", "sprint_page.json"} {
		for _, sprint := range srvSprints(t, fixture) {
			for _, key := range []string{"startDate", "endDate", "completeDate", "createdDate"} {
				raw, sent := sprint[key]
				if !sent {
					continue
				}
				at, ok := raw.(string)
				if !ok {
					t.Errorf("%s: %s = %v is a %T, and every Agile route spells a sprint date as a string; epoch millis is the generic task endpoint", fixture, key, raw, raw)
					continue
				}
				if _, err := time.Parse(time.RFC3339, at); err != nil {
					t.Errorf("%s: %s = %q does not parse as RFC 3339: %v", fixture, key, at, err)
				}
				if _, err := time.Parse(srvPlatformLayout, at); err == nil {
					t.Errorf("%s: %s = %q parses with the platform layout, so it is not the Agile spelling", fixture, key, at)
				}
				spelling := strings.HasSuffix(at, "Z")
				zulu[spelling] = append(zulu[spelling], fixture+" "+key)
			}
		}
	}
	if len(zulu[true]) == 0 || len(zulu[false]) == 0 {
		t.Errorf("every sprint date is spelt one way — %d normalised to Z, %d with an offset — and a site sends both", len(zulu[true]), len(zulu[false]))
	}
}

func srvAttachmentCreated(t *testing.T) string {
	t.Helper()

	created, _ := srvDecode(t, srvFixture(t, "attachment_meta.json"))["created"].(string)
	if created == "" {
		t.Fatal("attachment_meta.json carries no created")
	}
	return created
}

func srvSprints(t *testing.T, fixture string) []map[string]any {
	t.Helper()

	body := srvDecode(t, srvFixture(t, fixture))
	values, ok := body["values"].([]any)
	if !ok {
		return []map[string]any{body}
	}
	out := make([]map[string]any, 0, len(values))
	for _, value := range values {
		sprint, ok := value.(map[string]any)
		if !ok {
			t.Fatalf("%s carries a sprint that is not an object: %v", fixture, value)
		}
		out = append(out, sprint)
	}
	return out
}

func TestFixtures_AGenericTaskMessageIsNotAlwaysASentence(t *testing.T) {
	t.Parallel()

	var keys int
	for _, name := range srvTaskFixtureNames() {
		message := srvTask(t, name).Message
		if message != "" && !strings.Contains(message, " ") && strings.Contains(message, ".") {
			keys++
		}
	}
	if keys == 0 {
		t.Error("every message reads as prose, and a live poll answered one with an unresolved i18n key that a view would print verbatim")
	}
}

func TestServer_TaskRoutesServeTheTwoShapesSeparately(t *testing.T) {
	t.Parallel()
	s := srvNewServer(t)

	generic := srvDo(t, s, http.MethodGet, "/rest/api/3/task/11072", "")
	if generic.status != http.StatusOK {
		t.Fatalf("generic task status = %d, want 200", generic.status)
	}
	if !bytes.Equal(generic.body, srvFixture(t, "task_complete.json")) {
		t.Error("the generic task route is not serving task_complete.json verbatim")
	}

	queued := srvDo(t, s, http.MethodGet, "/rest/api/3/bulk/queue/10641", "")
	if queued.status != http.StatusOK {
		t.Fatalf("bulk queue status = %d, want 200", queued.status)
	}
	if !bytes.Equal(queued.body, srvFixture(t, "bulkmove_task_complete.json")) {
		t.Error("the bulk queue route is not serving bulkmove_task_complete.json verbatim")
	}
}

func TestServer_WalksATaskThroughItsStates(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name   string
		states []string
	}{
		{"settling on complete", []string{"task_enqueued.json", "task_running.json", "task_complete.json"}},
		{"settling on failed", []string{"task_enqueued.json", "task_running.json", "task_failed.json"}},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			s := srvNewServer(t, jiratest.WithHandler(http.MethodGet, "/rest/api/3/task/{id}", srvReplay(t, tc.states)))

			for _, want := range tc.states {
				got := srvDo(t, s, http.MethodGet, "/rest/api/3/task/11072", "")
				if got.status != http.StatusOK {
					t.Fatalf("status = %d, want 200", got.status)
				}
				var task srvTaskProgress
				srvDecodeStrict(t, got.body, &task)
				if task.Status != srvTaskStates[want] {
					t.Errorf("poll answered %s, want %s", task.Status, srvTaskStates[want])
				}
			}
		})
	}
}

// srvReplay repeats the last fixture once it runs out, so a poller that reads a
// settled task twice still gets an answer.
func srvReplay(t *testing.T, names []string) http.HandlerFunc {
	t.Helper()
	bodies := make([][]byte, len(names))
	for i, name := range names {
		bodies[i] = srvFixture(t, name)
	}
	var mu sync.Mutex
	var served int
	return func(w http.ResponseWriter, _ *http.Request) {
		mu.Lock()
		body := bodies[min(served, len(bodies)-1)]
		served++
		mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write(body)
	}
}
