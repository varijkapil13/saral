package cloud

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/varijkapil13/saral/pkg/adf"
	"github.com/varijkapil13/saral/pkg/jira"
	"github.com/varijkapil13/saral/pkg/jira/jiratest"
)

const (
	movePattern       = "/rest/api/3/bulk/issues/move"
	bulkQueuePattern  = "/rest/api/3/bulk/queue/{id}"
	taskPattern       = "/rest/api/3/task/{id}"
	movedTaskID       = "10641"
	movedTaskPath     = "/rest/api/3/bulk/queue/10641"
	genericTaskPath   = "/rest/api/3/task/11072"
	moveTargetProject = "EX2"
	moveTargetType    = "10305"
)

func moveClient(t *testing.T, opts ...jiratest.ServerOption) (*Client, *jiratest.Server) {
	t.Helper()

	s := jiratest.NewServer(opts...)
	t.Cleanup(s.Close)
	c, _ := testClient(t, s.URL(), WithRetry(RetryPolicy{Attempts: 1}))
	return c, s
}

// aMove is a move the endpoint would accept: two issues, a target, a remap of
// two source statuses onto one target status and a third onto another, and one
// mandatory field value.
func aMove() jira.MoveRequest {
	return jira.MoveRequest{
		Keys:              []string{"EX-1", "EX-2"},
		TargetProjectKey:  moveTargetProject,
		TargetIssueTypeID: moveTargetType,
		StatusMap: []jira.StatusMapping{
			{FromStatusID: "10201", ToStatusID: "10501"},
			{FromStatusID: "10204", ToStatusID: "10501"},
			{FromStatusID: "10203", ToStatusID: "10502"},
		},
		Fields: jira.NewFieldSet(map[string]jira.FieldValue{
			"customfield_13401": {Kind: jira.KindNumber, Number: 5},
		}),
		Notify: true,
	}
}

// sentMove reads the bytes a submit put on the wire.
//
// It returns them as text and never as apiBulkMove: a body read back through the
// struct that encoded it agrees with itself whatever every key in it is called,
// so no misspelt json tag can fail a test written that way. The payload
// assertions below compare against a literal document instead.
func sentMove(t *testing.T, s *jiratest.Server) string {
	t.Helper()

	return sentTo(t, s, http.MethodPost, movePattern).Body
}

// assertSameJSON compares two documents by value, so that whitespace and key
// order are not the subject and every key name is.
func assertSameJSON(t *testing.T, what, got, want string) {
	t.Helper()

	var gotDoc, wantDoc any
	if err := json.Unmarshal([]byte(got), &gotDoc); err != nil {
		t.Fatalf("reading %s: %v\nin: %s", what, err, got)
	}
	if err := json.Unmarshal([]byte(want), &wantDoc); err != nil {
		t.Fatalf("the expected %s is not JSON: %v", what, err)
	}
	if !reflect.DeepEqual(gotDoc, wantDoc) {
		t.Errorf("%s went out as\n\t%s\nwant\n\t%s", what, compactedJSON(t, got), compactedJSON(t, want))
	}
}

func compactedJSON(t *testing.T, in string) string {
	t.Helper()

	var out bytes.Buffer
	if err := json.Compact(&out, []byte(in)); err != nil {
		return in
	}
	return out.String()
}

// wantMovePayload is every byte aMove puts on the wire, written out by hand.
//
// The mandatory-field wrapper is the reason it is written out rather than
// rebuilt: this endpoint takes {retain, type, value} with a raw value as a list
// of strings, and the edit endpoint's encoder — which writes 5 for a number and
// an ADF document bare — produces a payload the schema accepts and the site
// mishandles. Only a literal catches that, and it catches every key name with it.
const wantMovePayload = `{
	"sendBulkNotification": true,
	"targetToSourcesMapping": {
		"EX2,10305": {
			"inferClassificationDefaults": true,
			"inferFieldDefaults": false,
			"inferStatusDefaults": false,
			"inferSubtaskTypeDefault": true,
			"issueIdsOrKeys": ["EX-1", "EX-2"],
			"targetMandatoryFields": [
				{"fields": {"customfield_13401": {"retain": false, "type": "raw", "value": ["5"]}}}
			],
			"targetStatus": [
				{"statuses": {"10501": ["10201", "10204"], "10502": ["10203"]}}
			]
		}
	}
}`

func TestBulkMove_SendsOneTargetMappingAndPointsTheTaskAtTheBulkQueue(t *testing.T) {
	t.Parallel()

	c, s := moveClient(t)

	ref, err := c.BulkMove(t.Context(), aMove())
	if err != nil {
		t.Fatalf("submitting the move: %v", err)
	}

	if ref.ID != movedTaskID {
		t.Errorf("the task is %q, want %q from the submit response", ref.ID, movedTaskID)
	}
	if want := s.URL() + movedTaskPath; ref.URL != want {
		t.Errorf("the task is polled at %q, want %q: the generic task endpoint answers a different shape", ref.URL, want)
	}

	assertSameJSON(t, "the move payload", sentMove(t, s), wantMovePayload)
}

// wantDefaultedMovePayload is the same move with nothing to remap. The three
// mappings are absent rather than empty: the endpoint documents each as defined
// only when its infer flag is false, so a key beside a true flag contradicts it.
const wantDefaultedMovePayload = `{
	"sendBulkNotification": false,
	"targetToSourcesMapping": {
		"EX2,10305": {
			"inferClassificationDefaults": true,
			"inferFieldDefaults": true,
			"inferStatusDefaults": true,
			"inferSubtaskTypeDefault": true,
			"issueIdsOrKeys": ["EX-1", "EX-2"]
		}
	}
}`

func TestBulkMove_AsksForTheTargetsOwnDefaultsWhenThereIsNothingToRemap(t *testing.T) {
	t.Parallel()

	c, s := moveClient(t)

	in := aMove()
	in.StatusMap, in.Fields, in.Notify = nil, jira.FieldSet{}, false

	if _, err := c.BulkMove(t.Context(), in); err != nil {
		t.Fatalf("submitting the move: %v", err)
	}

	assertSameJSON(t, "the move payload", sentMove(t, s), wantDefaultedMovePayload)
}

// TestBulkMove_WrapsEveryFieldKindTheWayTheMoveEndpointTakesIt pins the wrapper
// per kind, because the shape differs from the edit endpoint's for all of them
// and identically for none.
func TestBulkMove_WrapsEveryFieldKindTheWayTheMoveEndpointTakesIt(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name  string
		value jira.FieldValue
		want  string
	}{
		{
			name:  "text",
			value: jira.FieldValue{Kind: jira.KindText, Text: "Phase Three"},
			want:  `{"retain": false, "type": "raw", "value": ["Phase Three"]}`,
		},
		{
			name:  "a number, which an edit writes bare",
			value: jira.FieldValue{Kind: jira.KindNumber, Number: 5},
			want:  `{"retain": false, "type": "raw", "value": ["5"]}`,
		},
		{
			name:  "a fractional number",
			value: jira.FieldValue{Kind: jira.KindNumber, Number: 2.5},
			want:  `{"retain": false, "type": "raw", "value": ["2.5"]}`,
		},
		{
			name:  "a flag",
			value: jira.FieldValue{Kind: jira.KindBool, Bool: true},
			want:  `{"retain": false, "type": "raw", "value": ["true"]}`,
		},
		{
			name:  "a date",
			value: jira.FieldValue{Kind: jira.KindDate, Date: jira.Date{Year: 2026, Month: time.March, Day: 9}},
			want:  `{"retain": false, "type": "raw", "value": ["2026-03-09"]}`,
		},
		{
			name: "a date-time",
			value: jira.FieldValue{
				Kind: jira.KindTime,
				Time: time.Date(2026, time.March, 9, 14, 30, 0, 0, time.UTC),
			},
			want: `{"retain": false, "type": "raw", "value": ["2026-03-09T14:30:00.000+0000"]}`,
		},
		{
			name: "an option, which an edit writes as an object",
			value: jira.FieldValue{
				Kind:    jira.KindOption,
				Options: []jira.Option{{ID: "10064", Label: "Phase Three"}},
			},
			want: `{"retain": false, "type": "raw", "value": ["10064"]}`,
		},
		{
			name: "several options",
			value: jira.FieldValue{
				Kind:    jira.KindOptions,
				Options: []jira.Option{{ID: "10064"}, {ID: "10065"}},
			},
			want: `{"retain": false, "type": "raw", "value": ["10064", "10065"]}`,
		},
		{
			// The one shape that produces an option with no id: a labels-like
			// array of bare strings, which has nothing but its own text.
			name: "an option a site gave no id",
			value: jira.FieldValue{
				Kind:    jira.KindOption,
				Options: []jira.Option{{Label: "urgent"}},
			},
			want: `{"retain": false, "type": "raw", "value": ["urgent"]}`,
		},
		{
			name: "an account, which an edit writes as an object",
			value: jira.FieldValue{
				Kind:  jira.KindUser,
				Users: []jira.User{{AccountID: "5b10a2844c20165700ede21g", DisplayName: "Example User"}},
			},
			want: `{"retain": false, "type": "raw", "value": ["5b10a2844c20165700ede21g"]}`,
		},
		{
			name: "rich text, which is the one kind that is not raw",
			value: jira.FieldValue{
				Kind: jira.KindDoc,
				Doc:  adf.NewDoc(adf.NewNode("paragraph", adf.NewText("New description"))),
			},
			want: `{"retain": false, "type": "adf", "value": {"version": 1, "type": "doc",
				"content": [{"type": "paragraph", "content": [{"type": "text", "text": "New description"}]}]}}`,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			c, s := moveClient(t)

			in := aMove()
			in.StatusMap = nil
			in.Fields = jira.NewFieldSet(map[string]jira.FieldValue{"customfield_13401": tt.value})

			if _, err := c.BulkMove(t.Context(), in); err != nil {
				t.Fatalf("submitting the move: %v", err)
			}

			assertSameJSON(t, "the field wrapper", sentField(t, s, "customfield_13401"), tt.want)
		})
	}
}

// sentField digs one mandatory-field wrapper out of the payload by name, reading
// the keys as the wire spells them rather than through the encoder's own struct.
func sentField(t *testing.T, s *jiratest.Server, id string) string {
	t.Helper()

	var body struct {
		Mapping map[string]struct {
			Fields []struct {
				Fields map[string]json.RawMessage `json:"fields"`
			} `json:"targetMandatoryFields"`
		} `json:"targetToSourcesMapping"`
	}
	sent := sentMove(t, s)
	if err := json.Unmarshal([]byte(sent), &body); err != nil {
		t.Fatalf("reading the move payload: %v", err)
	}
	if len(body.Mapping) != 1 {
		t.Fatalf("the move sent %d target mappings, want exactly one: a duplicate key is dropped without failing the request",
			len(body.Mapping))
	}
	for _, target := range body.Mapping {
		if len(target.Fields) != 1 {
			t.Fatalf("the move sent %d field groups, want one: %s", len(target.Fields), sent)
		}
		raw, held := target.Fields[0].Fields[id]
		if !held {
			t.Fatalf("the payload carries no %s: %s", id, sent)
		}
		return string(raw)
	}
	return ""
}

func TestBulkMove_RefusesAMoveItCannotDescribeWithoutSendingIt(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		in   func(jira.MoveRequest) jira.MoveRequest
		// field is the one the refusal has to name; accepted marks the case
		// that pins the cap from below, so that widening the constant is caught
		// as well as narrowing it.
		field    string
		accepted bool
	}{
		{
			name:  "no issues at all",
			in:    func(in jira.MoveRequest) jira.MoveRequest { in.Keys = nil; return in },
			field: "issues",
		},
		{
			// 1001 spelt out, not bulkMoveMax+1: a cap compared against its own
			// constant is satisfied by any value the constant takes, and the
			// fake carries its own literal 1000 that this has to agree with.
			name: "more issues than the endpoint takes",
			in: func(in jira.MoveRequest) jira.MoveRequest {
				in.Keys = make([]string, 1001)
				for i := range in.Keys {
					in.Keys[i] = "EX-1"
				}
				return in
			},
			field: "issues",
		},
		{
			name: "exactly the thousand the endpoint takes, which is not a refusal",
			in: func(in jira.MoveRequest) jira.MoveRequest {
				in.Keys = make([]string, 1000)
				for i := range in.Keys {
					in.Keys[i] = "EX-1"
				}
				return in
			},
			accepted: true,
		},
		{
			name:  "an issue with no key",
			in:    func(in jira.MoveRequest) jira.MoveRequest { in.Keys = []string{"EX-1", "  "}; return in },
			field: "issues",
		},
		{
			name:  "no target project",
			in:    func(in jira.MoveRequest) jira.MoveRequest { in.TargetProjectKey = " "; return in },
			field: "project",
		},
		{
			name:  "no target issue type",
			in:    func(in jira.MoveRequest) jira.MoveRequest { in.TargetIssueTypeID = ""; return in },
			field: "issuetype",
		},
		{
			name:  "a target that would corrupt the mapping key",
			in:    func(in jira.MoveRequest) jira.MoveRequest { in.TargetProjectKey = "EX2,10305"; return in },
			field: "project",
		},
		{
			name: "a status remapped to nothing",
			in: func(in jira.MoveRequest) jira.MoveRequest {
				in.StatusMap = []jira.StatusMapping{{FromStatusID: "10201"}}
				return in
			},
			field: "status",
		},
		{
			name: "a remap that names no source status",
			in: func(in jira.MoveRequest) jira.MoveRequest {
				in.StatusMap = []jira.StatusMapping{{ToStatusID: "10501"}}
				return in
			},
			field: "status",
		},
		{
			name: "one status remapped to two",
			in: func(in jira.MoveRequest) jira.MoveRequest {
				in.StatusMap = []jira.StatusMapping{
					{FromStatusID: "10201", ToStatusID: "10501"},
					{FromStatusID: "10201", ToStatusID: "10502"},
				}
				return in
			},
			field: "status",
		},
		{
			name: "a status written as a field value",
			in: func(in jira.MoveRequest) jira.MoveRequest {
				in.Fields = jira.NewFieldSet(map[string]jira.FieldValue{
					"status": {Kind: jira.KindText, Text: "Shipped"},
				})
				return in
			},
			field: "status",
		},
		{
			// The edit endpoint refuses this one with "use BulkMove", which
			// inside BulkMove is no help at all.
			name: "the target project written as a field value",
			in: func(in jira.MoveRequest) jira.MoveRequest {
				in.Fields = jira.NewFieldSet(map[string]jira.FieldValue{
					"project": {Kind: jira.KindText, Text: "EX2"},
				})
				return in
			},
			field: "project",
		},
		{
			name: "a mandatory field given nothing",
			in: func(in jira.MoveRequest) jira.MoveRequest {
				in.Fields = jira.NewFieldSet(map[string]jira.FieldValue{
					"customfield_13401": {Kind: jira.KindEmpty},
				})
				return in
			},
			field: "customfield_13401",
		},
		{
			name: "a mandatory date left unset",
			in: func(in jira.MoveRequest) jira.MoveRequest {
				in.Fields = jira.NewFieldSet(map[string]jira.FieldValue{
					"customfield_13401": {Kind: jira.KindDate},
				})
				return in
			},
			field: "customfield_13401",
		},
		{
			name: "a value this client could not type",
			in: func(in jira.MoveRequest) jira.MoveRequest {
				in.Fields = jira.NewFieldSet(map[string]jira.FieldValue{
					"customfield_13401": {Kind: jira.KindUnknown, Text: `{"self":"https://example.atlassian.net/x"}`},
				})
				return in
			},
			field: "customfield_13401",
		},
		{
			name: "a cascading option, which one flat list cannot express",
			in: func(in jira.MoveRequest) jira.MoveRequest {
				in.Fields = jira.NewFieldSet(map[string]jira.FieldValue{
					"customfield_13401": {
						Kind:    jira.KindOption,
						Options: []jira.Option{{ID: "10064", Children: []jira.Option{{ID: "10070"}}}},
					},
				})
				return in
			},
			field: "customfield_13401",
		},
		{
			name: "an account with no id",
			in: func(in jira.MoveRequest) jira.MoveRequest {
				in.Fields = jira.NewFieldSet(map[string]jira.FieldValue{
					"customfield_13401": {Kind: jira.KindUser, Users: []jira.User{{DisplayName: "Example User"}}},
				})
				return in
			},
			field: "customfield_13401",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			c, s := moveClient(t)

			_, err := c.BulkMove(t.Context(), tt.in(aMove()))

			if tt.accepted {
				if err != nil {
					t.Fatalf("the move was refused locally: %v", err)
				}
				if served := len(s.Requests()); served != 1 {
					t.Errorf("the site served %d requests, want the one submission", served)
				}
				return
			}
			var invalid *jira.ValidationError
			if !errors.As(err, &invalid) {
				t.Fatalf("got %T (%v), want a *jira.ValidationError", err, err)
			}
			if _, named := invalid.For(tt.field); !named {
				t.Errorf("the refusal does not name %s: %v", tt.field, invalid)
			}
			if served := len(s.Requests()); served != 0 {
				t.Errorf("the site served %d requests for a move that cannot be described", served)
			}
		})
	}
}

// TestBulkMove_SaysTheCapCountsSubtasksItWasNotGiven covers the half of the cap
// this client cannot enforce: the endpoint's thousand includes every subtask of
// every issue named, which is a number no caller knows before it submits.
func TestBulkMove_SaysTheCapCountsSubtasksItWasNotGiven(t *testing.T) {
	t.Parallel()

	c, _ := moveClient(t)

	in := aMove()
	in.Keys = make([]string, 1001)
	for i := range in.Keys {
		in.Keys[i] = "EX-1"
	}

	_, err := c.BulkMove(t.Context(), in)

	var invalid *jira.ValidationError
	if !errors.As(err, &invalid) {
		t.Fatalf("got %T (%v), want a *jira.ValidationError", err, err)
	}
	said, _ := invalid.For("issues")
	if !strings.Contains(said, "subtask") {
		t.Errorf("the refusal reads %q and never mentions subtasks, so a caller reads the thousand as a promise", said)
	}
}

// TestBulkMove_SaysWhereARefusedFieldValueBelongs covers the wording and not only
// the field name. The edit endpoint refuses these same two with "use BulkMove"
// and "move the issue with Transition", which inside a move sends the reader back
// to where they already are.
func TestBulkMove_SaysWhereARefusedFieldValueBelongs(t *testing.T) {
	t.Parallel()

	tests := []struct {
		field string
		says  string
	}{
		{field: "project", says: "MoveRequest.TargetProjectKey"},
		{field: "status", says: "MoveRequest.StatusMap"},
	}

	for _, tt := range tests {
		t.Run(tt.field, func(t *testing.T) {
			t.Parallel()

			c, _ := moveClient(t)

			in := aMove()
			in.Fields = jira.NewFieldSet(map[string]jira.FieldValue{
				tt.field: {Kind: jira.KindText, Text: "EX2"},
			})

			_, err := c.BulkMove(t.Context(), in)

			var invalid *jira.ValidationError
			if !errors.As(err, &invalid) {
				t.Fatalf("got %T (%v), want a *jira.ValidationError", err, err)
			}
			said, named := invalid.For(tt.field)
			if !named {
				t.Fatalf("the refusal does not name %s: %v", tt.field, invalid)
			}
			if !strings.Contains(said, tt.says) {
				t.Errorf("the refusal reads %q and never says the value belongs in %s", said, tt.says)
			}
			if strings.Contains(said, "BulkMove") || strings.Contains(said, "Transition") {
				t.Errorf("the refusal reads %q, which sends a caller of BulkMove to another call", said)
			}
		})
	}
}

// TestBulkMove_ReadsARefusalInTheBulkEndpointsOwnEnvelope covers the 403 a bulk
// route answers in its own shape rather than the platform one plans_403.json
// carries. The body is inline because no fixture holds this shape and
// pkg/jira/jiratest owns the fixtures; a bulk one belongs there.
func TestBulkMove_ReadsARefusalInTheBulkEndpointsOwnEnvelope(t *testing.T) {
	t.Parallel()

	const says = "You do not have the Bulk Change global permission."
	const body = `{"errors":[{"message":"` + says + `","errorType":"PERMISSION"}]}`

	for _, call := range moveCalls() {
		if !call.capability {
			continue
		}
		t.Run(call.name, func(t *testing.T) {
			t.Parallel()

			c, s := moveClient(t, jiratest.WithHandler(call.method, call.route,
				jsonHandler(http.StatusForbidden, body)))

			err := call.run(t.Context(), c, s.URL())

			var refused *jira.CapabilityError
			if !errors.As(err, &refused) {
				t.Fatalf("got %T (%v), want a *jira.CapabilityError", err, err)
			}
			if !strings.Contains(refused.Reason, says) {
				t.Errorf("the refusal reads %q and not the site's own %q, which only this envelope carries",
					refused.Reason, says)
			}
			if refused.Capability != jira.CapBulkMove {
				t.Errorf("the refusal names the capability %q, want %q", refused.Capability, jira.CapBulkMove)
			}
		})
	}
}

func TestBulkMove_ReportsASubmitThatNamesNoTask(t *testing.T) {
	t.Parallel()

	c, _ := moveClient(t, jiratest.WithHandler(http.MethodPost, movePattern,
		jsonHandler(http.StatusCreated, `{}`)))

	ref, err := c.BulkMove(t.Context(), aMove())

	var broken *jira.TransportError
	if !errors.As(err, &broken) {
		t.Fatalf("got %T (%v), want a *jira.TransportError: a move nothing can report on is not a success", err, err)
	}
	if broken.Status != http.StatusCreated {
		t.Errorf("the failure reports status %d, want the %d the site answered", broken.Status, http.StatusCreated)
	}
	if ref != (jira.TaskRef{}) {
		t.Errorf("the failure came back with %+v attached, want no ref at all", ref)
	}
}

func TestBulkMove_IsNeitherReplayedNorSharedWithAnIdenticalRequest(t *testing.T) {
	t.Parallel()

	if (request{method: http.MethodPost, path: bulkMovePath}).canRepeat() {
		t.Error("a bulk move is marked repeatable, so it can be replayed and coalesced: it moves issues")
	}

	_, s := moveClient(t, jiratest.WithHandler(http.MethodPost, movePattern,
		jsonHandler(http.StatusBadGateway, `{"errorMessages":["upstream"]}`)))
	// The default retry policy, so that a request left un-replayed is the
	// adapter's own doing and not the test's.
	c, _ := testClient(t, s.URL())

	if _, err := c.BulkMove(t.Context(), aMove()); err == nil {
		t.Fatal("BulkMove reported no error for a 502")
	}
	if served := len(s.Requests()); served != 1 {
		t.Errorf("the site served %d submissions; a 5xx may already have queued the move", served)
	}
}

func TestTask_ReadsTheBulkQueueProgressInEveryState(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		fixture  string
		state    jira.TaskState
		progress int
		message  string
		failed   []string
	}{
		{
			name: "queued behind other work", fixture: "bulkmove_task_enqueued.json",
			state: jira.TaskEnqueued, progress: 0, message: "0 of 3 issues processed",
		},
		{
			name: "part way through", fixture: "bulkmove_task_running.json",
			state: jira.TaskRunning, progress: 33, message: "1 of 3 issues processed",
		},
		{
			name: "finished", fixture: "bulkmove_task_complete.json",
			state: jira.TaskComplete, progress: 100, message: "3 of 3 issues processed",
		},
		{
			name: "finished with two issues left behind", fixture: "bulkmove_task_failed.json",
			state: jira.TaskFailed, progress: 100, message: "1 of 3 issues processed, 2 failed",
			failed: []string{"10002", "10003"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			c, s := moveClient(t, jiratest.WithFixture(http.MethodGet, bulkQueuePattern, tt.fixture))
			ref := jira.TaskRef{ID: movedTaskID, URL: s.URL() + movedTaskPath}

			got, err := c.Task(t.Context(), ref)
			if err != nil {
				t.Fatalf("polling the move: %v", err)
			}

			if got.State != tt.state {
				t.Errorf("the task reads as %q, want %q", got.State, tt.state)
			}
			if got.Progress != tt.progress {
				t.Errorf("progress is %d%%, want %d%%", got.Progress, tt.progress)
			}
			if got.Message != tt.message {
				t.Errorf("the message is %q, want %q built from the counts", got.Message, tt.message)
			}
			if !slices.Equal(got.Failed, tt.failed) {
				t.Errorf("the failures are %v, want %v", got.Failed, tt.failed)
			}
			if got.Ref != ref {
				t.Errorf("the snapshot names %+v, want the ref it was polled with %+v", got.Ref, ref)
			}
		})
	}
}

func TestTask_ReadsTheTaskRegistryProgressInEveryState(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		fixture  string
		state    jira.TaskState
		progress int
		message  string
		stopped  bool
	}{
		{
			name: "queued", fixture: "task_enqueued.json",
			state: jira.TaskEnqueued, progress: 0,
		},
		{
			name: "part way through", fixture: "task_running.json",
			state: jira.TaskRunning, progress: 40,
			message: "Updating the issues that still select Phase Three.",
		},
		{
			name: "finished", fixture: "task_complete.json",
			state: jira.TaskComplete, progress: 100, stopped: true,
			message: "Phase Three was replaced with Phase Two on 2 issues.",
		},
		{
			name: "stopped by a failure", fixture: "task_failed.json",
			state: jira.TaskFailed, progress: 60, stopped: true,
			message: "The replacement stopped: one issue could not be updated.",
		},
		{
			// A cancellation is a live state for as long as the task takes to
			// stop, and the message is the i18n key the site failed to resolve.
			name: "asked to stop and still running", fixture: "task_cancel_requested.json",
			state: jira.TaskCancelRequested, progress: 60,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			c, s := moveClient(t, jiratest.WithFixture(http.MethodGet, taskPattern, tt.fixture))
			ref := jira.TaskRef{ID: "11072", URL: s.URL() + genericTaskPath}

			got, err := c.Task(t.Context(), ref)
			if err != nil {
				t.Fatalf("polling the task: %v", err)
			}

			if got.State != tt.state {
				t.Errorf("the task reads as %q, want %q", got.State, tt.state)
			}
			if got.Progress != tt.progress {
				t.Errorf("progress is %d%%, want %d%%", got.Progress, tt.progress)
			}
			if got.Message != tt.message {
				t.Errorf("the message is %q, want %q", got.Message, tt.message)
			}
			if got.State.Done() != tt.stopped {
				t.Errorf("%s reads as stopped=%v, want %v", got.State, got.State.Done(), tt.stopped)
			}
			if len(got.Failed) != 0 {
				t.Errorf("the task reported %v as failed, and this registry names no issues at all", got.Failed)
			}
		})
	}
}

// The queue bodies below are written inline rather than taken from a fixture
// because no fixture holds either shape: every bulkmove_task_*.json carries both
// counts and a zero invalid count, which is exactly the reason these two clauses
// were dead. The shapes are Atlassian's own documented ones, and both belong in
// pkg/jira/jiratest/fixtures — which this packet does not own.
const (
	// The documented body of a task part way through: a percentage and no
	// counts at all.
	runningWithNoCounts = `{"taskId":"10641","status":"RUNNING","progressPercent":65,
		"submittedBy":{"accountId":"5b10a2844c20165700ede21g"},
		"created":1770802882000,"started":1770802885000,"updated":1770802924000}`
	// A run that finished with issues this account could not see. The count is
	// absent when it is zero, so a non-zero one is the only way to reach the
	// clause that reports it.
	completeWithIssuesNobodyCanSee = `{"taskId":"10641","status":"COMPLETE","progressPercent":100,
		"submittedBy":{"accountId":"5b10a2844c20165700ede21g"},
		"created":1770802882000,"totalIssueCount":3,"processedAccessibleIssues":[10001],
		"invalidOrInaccessibleIssueCount":2}`
)

func TestTask_ReportsWhatTheQueueSaysAndNotWhatItLeftOut(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		body     string
		state    jira.TaskState
		progress int
		message  string
	}{
		{
			// The bug this covers: "0 issues processed" beside a bar at 65 per
			// cent, from counts the running body does not carry.
			name: "part way through, with the counts the real body omits",
			body: runningWithNoCounts, state: jira.TaskRunning, progress: 65,
			message: "",
		},
		{
			name: "finished, with issues this account cannot see",
			body: completeWithIssuesNobodyCanSee, state: jira.TaskComplete, progress: 100,
			message: "1 of 3 issues processed, 2 invalid or not visible to this account",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			c, s := moveClient(t, jiratest.WithHandler(http.MethodGet, bulkQueuePattern,
				jsonHandler(http.StatusOK, tt.body)))

			got, err := c.Task(t.Context(), jira.TaskRef{ID: movedTaskID, URL: s.URL() + movedTaskPath})
			if err != nil {
				t.Fatalf("polling the move: %v", err)
			}

			if got.State != tt.state {
				t.Errorf("the task reads as %q, want %q", got.State, tt.state)
			}
			if got.Progress != tt.progress {
				t.Errorf("progress is %d%%, want %d%%", got.Progress, tt.progress)
			}
			if got.Message != tt.message {
				t.Errorf("the message is %q, want %q", got.Message, tt.message)
			}
		})
	}
}

// TestTask_NamesTheTaskTheSiteAnswersAboutAndNotTheOnePolled pins where the
// snapshot's id comes from. Every fixture's task id happens to equal the id the
// tests poll with, so nothing else in this file can tell the two apart — and a
// poller handed back its own argument cannot notice a site answering about
// another task.
func TestTask_NamesTheTaskTheSiteAnswersAboutAndNotTheOnePolled(t *testing.T) {
	t.Parallel()

	const polled = "99999"
	tests := []struct {
		name    string
		route   string
		path    string
		fixture string
		want    string
	}{
		{
			name: "the bulk queue", route: bulkQueuePattern, path: movedTaskPath,
			fixture: "bulkmove_task_complete.json", want: movedTaskID,
		},
		{
			name: "the task registry", route: taskPattern, path: genericTaskPath,
			fixture: "task_complete.json", want: "11072",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			c, s := moveClient(t, jiratest.WithFixture(http.MethodGet, tt.route, tt.fixture))
			ref := jira.TaskRef{ID: polled, URL: s.URL() + tt.path}

			got, err := c.Task(t.Context(), ref)
			if err != nil {
				t.Fatalf("polling the task: %v", err)
			}

			if got.Ref.ID != tt.want {
				t.Errorf("the snapshot names task %q, want the %q the site answered about", got.Ref.ID, tt.want)
			}
			if got.Ref.URL != ref.URL {
				t.Errorf("the snapshot is polled at %q, want the %q it was asked at", got.Ref.URL, ref.URL)
			}
		})
	}
}

// TestTask_FindsTheProgressEndpointUnderASiteContextPath covers the site shape
// the replay server cannot have: parseSite keeps a context path, so a ref's URL
// carries it and the path sent must not.
func TestTask_FindsTheProgressEndpointUnderASiteContextPath(t *testing.T) {
	t.Parallel()

	c, _ := testClient(t, "https://jira.example.invalid/jira")

	tests := []struct {
		name string
		url  string
		want progressEndpoint
	}{
		{
			name: "the bulk queue",
			url:  "https://jira.example.invalid/jira" + movedTaskPath,
			want: progressEndpoint{path: movedTaskPath, bulk: true},
		},
		{
			name: "the task registry",
			url:  "https://jira.example.invalid/jira" + genericTaskPath,
			want: progressEndpoint{path: genericTaskPath},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			got, err := c.taskEndpoint(jira.TaskRef{ID: movedTaskID, URL: tt.url})
			if err != nil {
				t.Fatalf("reading the progress endpoint off %s: %v", tt.url, err)
			}
			if got != tt.want {
				t.Errorf("%s resolves to %+v, want %+v: the context path belongs to the site and not to the request", tt.url, got, tt.want)
			}
		})
	}
}

func TestTask_RefusesTheProgressBodyOfTheOtherRegistry(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		route   string
		fixture string
		url     string
	}{
		{
			name:  "the bulk queue answering a task registry body",
			route: bulkQueuePattern, fixture: "task_complete.json", url: movedTaskPath,
		},
		{
			name:  "the task registry answering a bulk queue body",
			route: taskPattern, fixture: "bulkmove_task_complete.json", url: genericTaskPath,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			c, s := moveClient(t, jiratest.WithFixture(http.MethodGet, tt.route, tt.fixture))

			got, err := c.Task(t.Context(), jira.TaskRef{ID: movedTaskID, URL: s.URL() + tt.url})

			var broken *jira.TransportError
			if !errors.As(err, &broken) {
				t.Fatalf("got %T (%+v), want a *jira.TransportError: the two bodies read as empty versions of each other",
					err, got)
			}
			if broken.Status != http.StatusOK {
				t.Errorf("the failure reports status %d, want 200: the call reached Jira", broken.Status)
			}
		})
	}
}

func TestTask_RefusesARefItCannotPoll(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		ref  func(site string) jira.TaskRef
	}{
		{
			name: "a ref rebuilt from the id alone",
			ref:  func(string) jira.TaskRef { return jira.TaskRef{ID: movedTaskID} },
		},
		{
			name: "an endpoint that is not a URL",
			ref:  func(string) jira.TaskRef { return jira.TaskRef{ID: movedTaskID, URL: "http://[::1"} },
		},
		{
			name: "a task on another site",
			ref: func(string) jira.TaskRef {
				return jira.TaskRef{ID: movedTaskID, URL: "https://other.example.invalid" + movedTaskPath}
			},
		},
		{
			name: "an endpoint in neither registry",
			ref: func(site string) jira.TaskRef {
				return jira.TaskRef{ID: movedTaskID, URL: site + "/rest/api/3/bulk/queue"}
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			c, s := moveClient(t)

			_, err := c.Task(t.Context(), tt.ref(s.URL()))

			var invalid *jira.ValidationError
			if !errors.As(err, &invalid) {
				t.Fatalf("got %T (%v), want a *jira.ValidationError", err, err)
			}
			if served := len(s.Requests()); served != 0 {
				t.Errorf("the site served %d requests for a ref that names no progress endpoint", served)
			}
		})
	}
}

// moveCall is one of the two methods this file covers, in the shape the failure
// tables drive. Task appears twice because which registry it polls is the
// interesting variable.
type moveCall struct {
	name string
	// capability is whether a 403 on this call is the bulk-move capability
	// answering rather than an unrelated refusal.
	capability bool
	method     string
	route      string
	// kind and id are what a 404 on this call is about. Without them the
	// assertion cannot tell "task 10641 does not exist" from a bare URL, which
	// is what request.target() falls back to.
	kind string
	id   string
	// rejection is the body this endpoint answers a 400 in, and says is the
	// sentence inside it. The envelope differs per endpoint and only the
	// endpoint says which one is coming.
	rejection string
	says      string
	run       func(ctx context.Context, c *Client, site string) error
}

// The two envelopes a 400 arrives in here, both read off the published schema.
//
// /bulk/** declares BulkOperationErrorResponse for its 400 and its 401: an array
// of objects under errors, where every other endpoint puts either sentences
// under errorMessages or an object keyed by field. Neither shape is held as a
// fixture for a bulk endpoint, and pkg/jira/jiratest owns the fixtures.
const (
	bulkRejection     = `{"errors":[{"message":"The target project has no issue type with that id.","errorType":"VALIDATION"}]}`
	bulkRejectionSays = "The target project has no issue type with that id."
	taskRejection     = `{"errorMessages":["That task id is not a number."],"errors":{}}`
	taskRejectionSays = "That task id is not a number."
)

// forbiddenFixtureSays is the sentence plans_403.json carries. It is the only
// 403 fixture in the tree and its wording is about the Plans API, so a
// /bulk/** one belongs in pkg/jira/jiratest/fixtures, which this packet does not
// own. What it proves here is that a 403's reason is the site's and not this
// client's fallback.
const forbiddenFixtureSays = "The Plans API requires the Administer Jira global permission"

func moveCalls() []moveCall {
	return []moveCall{
		{
			name: "BulkMove", capability: true, method: http.MethodPost, route: movePattern,
			kind: "the bulk move endpoint", id: bulkMovePath,
			rejection: bulkRejection, says: bulkRejectionSays,
			run: func(ctx context.Context, c *Client, _ string) error {
				_, err := c.BulkMove(ctx, aMove())
				return err
			},
		},
		{
			name: "Task on the bulk queue", capability: true, method: http.MethodGet, route: bulkQueuePattern,
			kind: "task", id: movedTaskID,
			rejection: bulkRejection, says: bulkRejectionSays,
			run: func(ctx context.Context, c *Client, site string) error {
				_, err := c.Task(ctx, jira.TaskRef{ID: movedTaskID, URL: site + movedTaskPath})
				return err
			},
		},
		{
			name: "Task on the task registry", method: http.MethodGet, route: taskPattern,
			kind: "task", id: "11072",
			rejection: taskRejection, says: taskRejectionSays,
			run: func(ctx context.Context, c *Client, site string) error {
				_, err := c.Task(ctx, jira.TaskRef{ID: "11072", URL: site + genericTaskPath})
				return err
			},
		},
	}
}

func TestBulkMove_ReportsARefusalRateLimitAndTransportFailureAsThemselves(t *testing.T) {
	t.Parallel()

	for _, call := range moveCalls() {
		t.Run(call.name+"/a refusal", func(t *testing.T) {
			t.Parallel()

			c, s := moveClient(t, jiratest.WithStatus(call.method, call.route, http.StatusForbidden, "plans_403.json"))

			err := call.run(t.Context(), c, s.URL())

			var refused *jira.CapabilityError
			if !errors.As(err, &refused) {
				t.Fatalf("got %T (%v), want a *jira.CapabilityError", err, err)
			}
			// The site's own sentence, not this client's fallback. reasonOr
			// guarantees Reason is never empty, so a test asserting only that it
			// is non-empty cannot see a message dropped on the way.
			if !strings.Contains(refused.Reason, forbiddenFixtureSays) {
				t.Errorf("the refusal reads %q and not the site's own %q, so a hidden action explains itself in this client's words",
					refused.Reason, forbiddenFixtureSays)
			}
			want := jira.CapabilityKey("")
			if call.capability {
				want = jira.CapBulkMove
			}
			if refused.Capability != want {
				t.Errorf("the refusal names the capability %q, want %q", refused.Capability, want)
			}
		})

		t.Run(call.name+"/a request the site will not take", func(t *testing.T) {
			t.Parallel()

			c, s := moveClient(t, jiratest.WithHandler(call.method, call.route,
				jsonHandler(http.StatusBadRequest, call.rejection)))

			err := call.run(t.Context(), c, s.URL())

			var invalid *jira.ValidationError
			if !errors.As(err, &invalid) {
				t.Fatalf("got %T (%v), want a *jira.ValidationError", err, err)
			}
			if !strings.Contains(invalid.Error(), call.says) {
				t.Errorf("the rejection reads %q, want the site's own %q; the endpoint's own envelope is the only "+
					"place that sentence is, and losing it leaves a wizard holding a 400 with nothing to show",
					invalid.Error(), call.says)
			}
		})

		t.Run(call.name+"/a rate limit", func(t *testing.T) {
			t.Parallel()

			c, s := moveClient(t, jiratest.WithRateLimit(call.method, call.route, 30*time.Second))

			err := call.run(t.Context(), c, s.URL())

			var limited *jira.RateLimitError
			if !errors.As(err, &limited) {
				t.Fatalf("got %T (%v), want a *jira.RateLimitError", err, err)
			}
			if limited.RetryAfter != 30*time.Second {
				t.Errorf("Retry-After came back as %s, want 30s", limited.RetryAfter)
			}
		})

		t.Run(call.name+"/a task nobody has heard of", func(t *testing.T) {
			t.Parallel()

			c, s := moveClient(t, jiratest.WithStatus(call.method, call.route, http.StatusNotFound, "not_found_board.json"))

			err := call.run(t.Context(), c, s.URL())

			var missing *jira.NotFoundError
			if !errors.As(err, &missing) {
				t.Fatalf("got %T (%v), want a *jira.NotFoundError", err, err)
			}
			if missing.Kind != call.kind || missing.ID != call.id {
				t.Errorf("the failure names %q %q, want %q %q: request.target() falls back to \"resource\" and a URL",
					missing.Kind, missing.ID, call.kind, call.id)
			}
		})

		t.Run(call.name+"/a site that never answered", func(t *testing.T) {
			t.Parallel()

			dead := jiratest.NewServer()
			site := dead.URL()
			dead.Close()
			c, _ := testClient(t, site, WithRetry(RetryPolicy{Attempts: 1}))

			err := call.run(t.Context(), c, site)

			var broken *jira.TransportError
			if !errors.As(err, &broken) {
				t.Fatalf("got %T (%v), want a *jira.TransportError", err, err)
			}
			if broken.Status != 0 {
				t.Errorf("the failure reports status %d, want 0: nothing answered", broken.Status)
			}
		})

		t.Run(call.name+"/a body that is not JSON", func(t *testing.T) {
			t.Parallel()

			status := http.StatusOK
			if call.method == http.MethodPost {
				status = http.StatusCreated
			}
			c, s := moveClient(t, jiratest.WithHandler(call.method, call.route,
				jsonHandler(status, "<html>your proxy has opinions</html>")))

			err := call.run(t.Context(), c, s.URL())

			var broken *jira.TransportError
			if !errors.As(err, &broken) {
				t.Fatalf("got %T (%v), want a *jira.TransportError", err, err)
			}
			if broken.Status != status {
				t.Errorf("the failure reports status %d, want %d", broken.Status, status)
			}
		})

		t.Run(call.name+"/a caller who has already gone", func(t *testing.T) {
			t.Parallel()

			c, s := moveClient(t)
			ctx, cancel := context.WithCancel(t.Context())
			cancel()

			err := call.run(ctx, c, s.URL())

			if !errors.Is(err, context.Canceled) {
				t.Fatalf("got %v, want context.Canceled unwrapped", err)
			}
			if served := len(s.Requests()); served != 0 {
				t.Errorf("the site served %d requests for a call whose caller had gone", served)
			}
		})
	}
}

func TestTask_ComesBackWhenTheCallerGivesUpMidPoll(t *testing.T) {
	t.Parallel()

	arrived, announce := gate()
	release, letGo := gate()
	s := jiratest.NewServer(jiratest.WithHandler(http.MethodGet, bulkQueuePattern,
		func(_ http.ResponseWriter, r *http.Request) {
			announce()
			select {
			case <-r.Context().Done():
			case <-release:
			}
		}))
	defer closeServer(t, s)
	defer letGo()

	c, _ := testClient(t, s.URL(), WithRetry(RetryPolicy{Attempts: 1}))
	ctx, cancel := context.WithCancel(t.Context())
	failed := make(chan error, 1)
	go func() {
		_, err := c.Task(ctx, jira.TaskRef{ID: movedTaskID, URL: s.URL() + movedTaskPath})
		failed <- err
	}()

	receive(t, "the poll to reach the site", arrived)
	cancel()
	if err := receive(t, "the cancelled poll to come back", failed); !errors.Is(err, context.Canceled) {
		t.Fatalf("got %v, want context.Canceled unwrapped", err)
	}
}

func TestProse_DropsAnUnresolvedI18nKeyAndKeepsASentence(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		in   string
		want string
	}{
		{name: "an unresolved key", in: "task.progress.cancellation.requested"},
		{name: "a sentence", in: "Moved 3 issues.", want: "Moved 3 issues."},
		{name: "one word", in: "Abgeschlossen", want: "Abgeschlossen"},
		{name: "a sentence ending in a dot with no space", in: "Done.", want: "Done."},
		{name: "nothing at all", in: "  "},
		{name: "a key with a capital in it", in: "Task.Progress.Cancelled", want: "Task.Progress.Cancelled"},
		{name: "the other key a task sends", in: "bulk.operation.progress.percent.complete"},
		{name: "a bare address", in: "https://example.atlassian.net", want: "https://example.atlassian.net"},
		{name: "a filename", in: "design.spec.pdf", want: "design.spec.pdf"},
		{name: "a dotted word that is not a key", in: "moved.three.issues", want: "moved.three.issues"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			if got := prose(tt.in); got != tt.want {
				t.Errorf("prose(%q) = %q, want %q", tt.in, got, tt.want)
			}
		})
	}
}

func TestPercent_KeepsThePortsPromiseThatProgressIsAPercentage(t *testing.T) {
	t.Parallel()

	for in, want := range map[int]int{-1: 0, 0: 0, 50: 50, 100: 100, 143: 100} {
		if got := percent(in); got != want {
			t.Errorf("percent(%d) = %d, want %d", in, got, want)
		}
	}
}
