package jiratest_test

import (
	"encoding/json"
	"io/fs"
	"maps"
	"path"
	"regexp"
	"slices"
	"strings"
	"testing"

	"github.com/varijkapil13/saral/pkg/adf"
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
		"board.json",
		"board_config_estimation.json",
		"board_config_no_estimation.json",
		"bulkmove_submit.json",
		"bulkmove_task_complete.json",
		"bulkmove_task_enqueued.json",
		"bulkmove_task_failed.json",
		"bulkmove_task_running.json",
		"comments.json",
		"configuration.json",
		"createmeta_bug.json",
		"createmeta_task.json",
		"field.json",
		"issue_rich_adf.json",
		"mypermissions_admin.json",
		"mypermissions_basic.json",
		"myself.json",
		"plans_403.json",
		"rate_limited.json",
		"search_page1.json",
		"search_page2.json",
		"sprint_page.json",
		"transitions.json",
		"validation_error.json",
		"versions.json",
	}
	got := slices.Sorted(maps.Keys(srvJSONFixtures(t)))
	if !slices.Equal(got, want) {
		t.Errorf("fixture set drifted:\n got %v\nwant %v", got, want)
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
