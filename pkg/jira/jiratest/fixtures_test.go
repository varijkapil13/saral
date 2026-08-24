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
	"strings"
	"sync"
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
		"createmeta_issuetypes.json",
		"createmeta_task.json",
		"field.json",
		"issue_rich_adf.json",
		"mypermissions_admin.json",
		"mypermissions_basic.json",
		"myself.json",
		"plans_403.json",
		"plans_ok.json",
		"rate_limited.json",
		"search_page1.json",
		"search_page2.json",
		"sprint_page.json",
		"task_complete.json",
		"task_enqueued.json",
		"task_failed.json",
		"task_running.json",
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
	"task_enqueued.json": "ENQUEUED",
	"task_running.json":  "RUNNING",
	"task_complete.json": "COMPLETE",
	"task_failed.json":   "FAILED",
}

func srvTaskFixtureNames() []string { return slices.Sorted(maps.Keys(srvTaskStates)) }

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

			stopped := task.Status == "COMPLETE" || task.Status == "FAILED"
			if stopped != (task.Finished != nil) {
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

			if task.Status == "ENQUEUED" || task.Status == "RUNNING" {
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
