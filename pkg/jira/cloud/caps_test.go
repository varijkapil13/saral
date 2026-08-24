package cloud

import (
	"context"
	"errors"
	"net/http"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/varijkapil13/saral/pkg/jira"
	"github.com/varijkapil13/saral/pkg/jira/jiratest"
)

const capsProject = "EX"

// capsPlansReason is what plans_403.json says, which is the sentence the probe
// has to put in front of the user rather than the status code.
const capsPlansReason = "The Plans API requires the Administer Jira global permission, and a Jira Premium subscription."

// capsJSON answers a route with a body written here rather than in a fixture,
// for the two shapes no fixture carries: a project with no board, and a
// timezone no zoneinfo database knows.
func capsJSON(method, pattern string, status int, body string) jiratest.ServerOption {
	return jiratest.WithHandler(method, pattern, func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)
		_, _ = w.Write([]byte(body))
	})
}

func capsFakeEnv(vars map[string]string) capsEnv {
	return func(name string) string { return vars[name] }
}

// capsAssert checks one probe result, reason included: the reason is UI text,
// so a change to it is a change to what the user reads.
func capsAssert(t *testing.T, name string, got jira.Capability, wantOK bool, wantReason string) {
	t.Helper()

	if got.OK != wantOK {
		t.Errorf("%s.OK = %v, want %v (reason %q)", name, got.OK, wantOK, got.Reason)
	}
	if got.Reason != wantReason {
		t.Errorf("%s.Reason = %q, want %q", name, got.Reason, wantReason)
	}
}

func capsServed(t *testing.T, s *jiratest.Server, path string) jiratest.Request {
	t.Helper()

	for _, req := range s.Requests() {
		if req.Path == path {
			return req
		}
	}
	t.Fatalf("no request was served for %s", path)
	return jiratest.Request{}
}

func capsCount(s *jiratest.Server, path string) int {
	n := 0
	for _, req := range s.Requests() {
		if req.Path == path {
			n++
		}
	}
	return n
}

func TestCapabilities_AnswersEveryCapabilityForATokenThatMayDoEverything(t *testing.T) {
	t.Parallel()

	s := jiratest.NewServer()
	defer s.Close()

	c, _ := testClient(t, s.URL())
	got, err := c.Capabilities(t.Context(), capsProject)
	if err != nil {
		t.Fatalf("probing %s: %v", capsProject, err)
	}

	tests := []struct {
		name   string
		got    jira.Capability
		wantOK bool
		reason string
	}{
		{name: "Attachments", got: got.Attachments, wantOK: true},
		{name: "Boards", got: got.Boards, wantOK: true},
		{name: "BulkMove", got: got.BulkMove, wantOK: true},
		{name: "DeleteIssues", got: got.DeleteIssues, wantOK: true},
		{name: "Plans", got: got.Plans, reason: capsPlansReason},
	}
	for _, tt := range tests {
		capsAssert(t, tt.name, tt.got, tt.wantOK, tt.reason)
	}

	if served := len(s.Requests()); served != 5 {
		t.Errorf("the site served %d requests, want one per probe", served)
	}
}

func TestCapabilities_ExplainsWhatABasicTokenMayNotDo(t *testing.T) {
	t.Parallel()

	s := jiratest.NewServer(jiratest.WithFixture(http.MethodGet, capsPermissionsPath, "mypermissions_basic.json"))
	defer s.Close()

	c, _ := testClient(t, s.URL())
	got, err := c.Capabilities(t.Context(), capsProject)
	if err != nil {
		t.Fatalf("probing %s: %v", capsProject, err)
	}

	// The basic fixture refuses Move Issues and never mentions Bulk Change at
	// all, and the two are different answers: one is a denial the site made and
	// the other is a question it did not answer.
	capsAssert(t, "BulkMove", got.BulkMove, false,
		"You need the Move Issues permission to move issues between projects, and Jira did not answer for the Bulk Change permission")
	capsAssert(t, "DeleteIssues", got.DeleteIssues, false,
		"You need the Delete Issues permission to delete issues")
}

func TestCapabilities_NamesEveryPermissionAnActionNeeds(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		answers map[string]capsPermission
		needs   []capsRequirement
		wantOK  bool
		reason  string
	}{
		{
			name: "every permission granted",
			answers: map[string]capsPermission{
				"BULK_CHANGE":   {Name: "Make bulk changes", HavePermission: true},
				"MOVE_ISSUES":   {Name: "Move Issues", HavePermission: true},
				"CREATE_ISSUES": {Name: "Create Issues", HavePermission: true},
			},
			needs:  capsBulkMoveNeeds,
			wantOK: true,
		},
		{
			name: "two of the three refused, in the site's own words",
			answers: map[string]capsPermission{
				"BULK_CHANGE":   {Name: "Massenänderungen", HavePermission: false},
				"MOVE_ISSUES":   {Name: "Vorgänge verschieben", HavePermission: false},
				"CREATE_ISSUES": {Name: "Vorgänge erstellen", HavePermission: true},
			},
			needs:  capsBulkMoveNeeds,
			reason: "You need the Massenänderungen and Vorgänge verschieben permissions to move issues between projects",
		},
		{
			name: "all three refused reads as a list",
			answers: map[string]capsPermission{
				"BULK_CHANGE":   {Name: "Make bulk changes"},
				"MOVE_ISSUES":   {Name: "Move Issues"},
				"CREATE_ISSUES": {Name: "Create Issues"},
			},
			needs:  capsBulkMoveNeeds,
			reason: "You need the Make bulk changes, Move Issues and Create Issues permissions to move issues between projects",
		},
		{
			name:    "a site that answered for none of them",
			answers: map[string]capsPermission{},
			needs:   capsDeleteIssuesNeeds,
			reason:  "Jira did not answer for the Delete Issues permission, which is needed to delete issues",
		},
		{
			name: "a permission with no name of its own falls back to ours",
			answers: map[string]capsPermission{
				"DELETE_ISSUES": {HavePermission: false},
			},
			needs:  capsDeleteIssuesNeeds,
			reason: "You need the Delete Issues permission to delete issues",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			action := "delete issues"
			if len(tt.needs) > 1 {
				action = "move issues between projects"
			}
			capsAssert(t, "capsAllow", capsAllow(tt.answers, tt.needs, action), tt.wantOK, tt.reason)
		})
	}
}

func TestCapabilities_RepeatsJirasOwnWordsWhenItRefusesAProbe(t *testing.T) {
	t.Parallel()

	const refusal = "You do not have permission to view this project, or it does not exist."
	s := jiratest.NewServer(capsJSON(http.MethodGet, capsPermissionsPath, http.StatusForbidden,
		`{"errorMessages":["`+refusal+`"],"errors":{}}`))
	defer s.Close()

	c, _ := testClient(t, s.URL())
	got, err := c.Capabilities(t.Context(), capsProject)
	if err != nil {
		t.Fatalf("probing %s: %v", capsProject, err)
	}

	capsAssert(t, "BulkMove", got.BulkMove, false, refusal)
	capsAssert(t, "DeleteIssues", got.DeleteIssues, false, refusal)
	capsAssert(t, "Boards", got.Boards, true, "")
}

func TestCapabilities_ReportsAPlansApiThisTokenCanReach(t *testing.T) {
	t.Parallel()

	s := jiratest.NewServer(jiratest.WithFixture(http.MethodGet, capsPlansPath, "plans_ok.json"))
	defer s.Close()

	c, _ := testClient(t, s.URL())
	got, err := c.Capabilities(t.Context(), capsProject)
	if err != nil {
		t.Fatalf("probing %s: %v", capsProject, err)
	}
	capsAssert(t, "Plans", got.Plans, true, "")
}

func TestCapabilities_SaysWhenTheSiteHasNoPlansApiAtAll(t *testing.T) {
	t.Parallel()

	s := jiratest.NewServer(jiratest.WithStatus(http.MethodGet, capsPlansPath, http.StatusNotFound, ""))
	defer s.Close()

	c, _ := testClient(t, s.URL())
	got, err := c.Capabilities(t.Context(), capsProject)
	if err != nil {
		t.Fatalf("probing %s: %v", capsProject, err)
	}
	capsAssert(t, "Plans", got.Plans, false,
		"This site has no Plans API, which arrives with a Jira Premium subscription")
}

func TestCapabilities_SaysWhenAProjectHasNoBoard(t *testing.T) {
	t.Parallel()

	s := jiratest.NewServer(capsJSON(http.MethodGet, capsBoardsPath, http.StatusOK,
		`{"maxResults":1,"startAt":0,"total":0,"isLast":true,"values":[]}`))
	defer s.Close()

	c, _ := testClient(t, s.URL())
	got, err := c.Capabilities(t.Context(), capsProject)
	if err != nil {
		t.Fatalf("probing %s: %v", capsProject, err)
	}
	capsAssert(t, "Boards", got.Boards, false, capsProject+" has no board")
}

func TestCapabilities_SaysWhenTheSiteHasAttachmentsSwitchedOff(t *testing.T) {
	t.Parallel()

	s := jiratest.NewServer(capsJSON(http.MethodGet, capsConfigurationPath, http.StatusOK,
		`{"attachmentsEnabled":false,"votingEnabled":true,"timeTrackingEnabled":true}`))
	defer s.Close()

	c, _ := testClient(t, s.URL())
	got, err := c.Capabilities(t.Context(), capsProject)
	if err != nil {
		t.Fatalf("probing %s: %v", capsProject, err)
	}
	capsAssert(t, "Attachments", got.Attachments, false,
		"Attachments are switched off for this site, which only a Jira administrator can change")
}

func TestCapabilities_KeepsTheOtherAnswersWhenOneProbeFails(t *testing.T) {
	t.Parallel()

	s := jiratest.NewServer(jiratest.WithStatus(http.MethodGet, capsConfigurationPath, http.StatusInternalServerError, ""))
	defer s.Close()

	c, _ := testClient(t, s.URL(), WithRetry(RetryPolicy{Attempts: 1}))
	got, err := c.Capabilities(t.Context(), capsProject)
	if err != nil {
		t.Fatalf("a failed probe sank the whole capability probe: %v", err)
	}

	capsAssert(t, "Attachments", got.Attachments, false,
		"Jira did not answer whether attachments are enabled on this site: GET /rest/api/3/configuration failed with HTTP 500: Internal Server Error")
	capsAssert(t, "Boards", got.Boards, true, "")
	capsAssert(t, "BulkMove", got.BulkMove, true, "")
	if got.TimeZone == nil {
		t.Error("the timezone probe was lost with the configuration one")
	}
	if got.TimeZoneReason != "" {
		t.Errorf("TimeZoneReason = %q while the zone came back; the two are never both set", got.TimeZoneReason)
	}
}

func TestCapabilities_SaysWhichAnswersNeedAProjectWhenThereIsNone(t *testing.T) {
	t.Parallel()

	s := jiratest.NewServer()
	defer s.Close()

	c, _ := testClient(t, s.URL())
	got, err := c.Capabilities(t.Context(), "   ")
	if err != nil {
		t.Fatalf("probing with no project: %v", err)
	}

	capsAssert(t, "Boards", got.Boards, false,
		"No project is selected, and a board belongs to a project")
	capsAssert(t, "BulkMove", got.BulkMove, false,
		"No project is selected, and Jira grants Move Issues and Create Issues per project")
	capsAssert(t, "DeleteIssues", got.DeleteIssues, false,
		"No project is selected, and Jira grants Delete Issues per project")
	capsAssert(t, "Attachments", got.Attachments, true, "")
	capsAssert(t, "Plans", got.Plans, false, capsPlansReason)

	// Asking Jira for a project permission without naming a project is worse
	// than not asking: it answers yes when the token holds it in any project at
	// all, which is a different question from the one being put.
	for _, path := range []string{capsPermissionsPath, capsBoardsPath} {
		if n := capsCount(s, path); n != 0 {
			t.Errorf("%s was requested %d times with no project to scope it to", path, n)
		}
	}
}

func TestCapabilities_ReadsTheAccountTimezoneRatherThanTheMachines(t *testing.T) {
	t.Parallel()

	s := jiratest.NewServer()
	defer s.Close()

	c, _ := testClient(t, s.URL())
	got, err := c.Capabilities(t.Context(), capsProject)
	if err != nil {
		t.Fatalf("probing %s: %v", capsProject, err)
	}

	if got.TimeZone == nil {
		t.Fatal("the account timezone came back nil")
	}
	if name := got.TimeZone.String(); name != "Europe/Berlin" {
		t.Errorf("timezone = %s, want the one myself.json reports", name)
	}
	if got.Location() != got.TimeZone {
		t.Error("Location did not return the probed zone")
	}
	zone, why := got.Zone()
	if zone != got.TimeZone || why != "" {
		t.Errorf("Zone() = %s with reason %q, want the account's zone and nothing to explain", zone, why)
	}
}

// TestCapabilities_SaysWhyTheTimezoneIsNotTheAccountsOwn covers the three ways
// the zone can be missing, which are three different sentences: a zone renders
// as UTC whichever it was, and this reason is the only thing on screen that can
// tell someone in Berlin why their timestamps are an hour out.
func TestCapabilities_SaysWhyTheTimezoneIsNotTheAccountsOwn(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		serve  jiratest.ServerOption
		reason string
	}{
		{
			name: "this machine has no entry for the zone Jira named",
			serve: capsJSON(http.MethodGet, capsMyselfPath, http.StatusOK,
				`{"accountId":"5b10a2844c20165700ede21g","timeZone":"Mars/Olympus"}`),
			reason: "This machine has no zoneinfo entry for Mars/Olympus, the timezone this account is set to",
		},
		{
			name: "the account answered without a zone at all",
			serve: capsJSON(http.MethodGet, capsMyselfPath, http.StatusOK,
				`{"accountId":"5b10a2844c20165700ede21g"}`),
			reason: "Jira did not say what timezone this account is in",
		},
		{
			// The site knows which permission scheme refused and this client does
			// not, so the sentence a user reads has to be Jira's own.
			name: "the site refused to say who this token is",
			serve: capsJSON(http.MethodGet, capsMyselfPath, http.StatusForbidden,
				`{"errorMessages":["You do not have permission to view this user profile."],"errors":{}}`),
			reason: "You do not have permission to view this user profile.",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			s := jiratest.NewServer(tt.serve)
			defer s.Close()

			c, _ := testClient(t, s.URL())
			got, err := c.Capabilities(t.Context(), capsProject)
			if err != nil {
				t.Fatalf("an unusable timezone sank the whole probe: %v", err)
			}
			if got.TimeZone != nil {
				t.Errorf("timezone = %v, want none", got.TimeZone)
			}
			if got.TimeZoneReason != tt.reason {
				t.Errorf("TimeZoneReason = %q, want %q", got.TimeZoneReason, tt.reason)
			}
			zone, why := got.Zone()
			if zone != time.UTC || why != tt.reason {
				t.Errorf("Zone() = %s with reason %q, want UTC with %q", zone, why, tt.reason)
			}
			capsAssert(t, "Boards", got.Boards, true, "")
			capsAssert(t, "Attachments", got.Attachments, true, "")
		})
	}
}

func TestCapabilities_KeepsTheOtherAnswersWhenTheTimezoneProbeFails(t *testing.T) {
	t.Parallel()

	s := jiratest.NewServer(jiratest.WithStatus(http.MethodGet, capsMyselfPath, http.StatusInternalServerError, ""))
	defer s.Close()

	c, _ := testClient(t, s.URL(), WithRetry(RetryPolicy{Attempts: 1}))
	got, err := c.Capabilities(t.Context(), capsProject)
	if err != nil {
		t.Fatalf("the failed timezone probe sank the whole capability probe: %v", err)
	}

	const reason = "Jira did not answer what timezone this account is in: " +
		"GET /rest/api/3/myself failed with HTTP 500: Internal Server Error"
	if got.TimeZoneReason != reason {
		t.Errorf("TimeZoneReason = %q, want %q", got.TimeZoneReason, reason)
	}
	if got.Location() != time.UTC {
		t.Errorf("Location = %v, want UTC", got.Location())
	}
	capsAssert(t, "Boards", got.Boards, true, "")
	capsAssert(t, "BulkMove", got.BulkMove, true, "")
	capsAssert(t, "Attachments", got.Attachments, true, "")
}

func TestCapabilities_ReturnsTheRejectedCredentialRatherThanAbsentCapabilities(t *testing.T) {
	t.Parallel()

	s := jiratest.NewServer(capsJSON(http.MethodGet, capsMyselfPath, http.StatusUnauthorized,
		`{"errorMessages":["Client must be authenticated to access this resource."],"errors":{}}`))
	defer s.Close()

	c, _ := testClient(t, s.URL())
	got, err := c.Capabilities(t.Context(), capsProject)

	var rejected *jira.AuthError
	if !errors.As(err, &rejected) {
		t.Fatalf("got %T (%v), want a *jira.AuthError", err, err)
	}
	if got != (jira.Capabilities{}) {
		t.Errorf("capabilities = %+v, want none alongside a rejected credential", got)
	}
}

func TestCapabilities_ReturnsTheRateLimitRatherThanAbsentCapabilities(t *testing.T) {
	t.Parallel()

	s := jiratest.NewServer(jiratest.WithRateLimit(http.MethodGet, capsPermissionsPath, 30*time.Second))
	defer s.Close()

	c, _ := testClient(t, s.URL(), WithRetry(RetryPolicy{Attempts: 1}))
	got, err := c.Capabilities(t.Context(), capsProject)

	var limited *jira.RateLimitError
	if !errors.As(err, &limited) {
		t.Fatalf("got %T (%v), want a *jira.RateLimitError", err, err)
	}
	if limited.RetryAfter != 30*time.Second {
		t.Errorf("RetryAfter = %s, want the 30s the site asked for", limited.RetryAfter)
	}
	if got != (jira.Capabilities{}) {
		t.Errorf("capabilities = %+v, want none while the site is refusing to answer", got)
	}
}

func TestCapabilities_StopsWhenTheCallerCancels(t *testing.T) {
	t.Parallel()

	s := jiratest.NewServer()
	defer s.Close()

	ctx, cancel := context.WithCancel(t.Context())
	cancel()

	c, _ := testClient(t, s.URL())
	got, err := c.Capabilities(ctx, capsProject)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("got %v, want the caller's own cancellation", err)
	}
	if got != (jira.Capabilities{}) {
		t.Errorf("capabilities = %+v, want none from a cancelled probe", got)
	}
	if served := len(s.Requests()); served != 0 {
		t.Errorf("the site served %d requests after the caller left", served)
	}
}

func TestCapabilities_AsksOnlyForThePermissionsItNeeds(t *testing.T) {
	t.Parallel()

	s := jiratest.NewServer()
	defer s.Close()

	c, _ := testClient(t, s.URL())
	if _, err := c.Capabilities(t.Context(), capsProject); err != nil {
		t.Fatalf("probing %s: %v", capsProject, err)
	}

	permissions, err := url.ParseQuery(capsServed(t, s, capsPermissionsPath).Query)
	if err != nil {
		t.Fatalf("reading the permissions query: %v", err)
	}
	if got := permissions.Get("projectKey"); got != capsProject {
		t.Errorf("projectKey = %q, want the project being probed", got)
	}
	if got := permissions.Get("permissions"); got != "BULK_CHANGE,CREATE_ISSUES,DELETE_ISSUES,MOVE_ISSUES" {
		t.Errorf("permissions = %q, want the four the capabilities need and no more", got)
	}

	boards, err := url.ParseQuery(capsServed(t, s, capsBoardsPath).Query)
	if err != nil {
		t.Fatalf("reading the board query: %v", err)
	}
	if got := boards.Get("projectKeyOrId"); got != capsProject {
		t.Errorf("projectKeyOrId = %q, want the project being probed", got)
	}
	if got := boards.Get("maxResults"); got != "1" {
		t.Errorf("maxResults = %q, want the one board that answers the question", got)
	}
}

func TestCapabilities_ProbesEachEndpointOnce(t *testing.T) {
	t.Parallel()

	s := jiratest.NewServer()
	defer s.Close()

	c, _ := testClient(t, s.URL())
	if _, err := c.Capabilities(t.Context(), capsProject); err != nil {
		t.Fatalf("probing %s: %v", capsProject, err)
	}

	for _, path := range []string{
		capsPermissionsPath, capsConfigurationPath, capsMyselfPath, capsPlansPath, capsBoardsPath,
	} {
		if n := capsCount(s, path); n != 1 {
			t.Errorf("%s was requested %d times, want once", path, n)
		}
	}
}

func TestDetectGraphics_ReadsTheTerminalOutOfTheEnvironment(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		env  map[string]string
		want jira.GraphicsMode
	}{
		{
			name: "nothing in the environment is not a terminal",
			env:  map[string]string{},
			want: jira.GraphicsNone,
		},
		{
			name: "a dumb terminal gets text",
			env:  map[string]string{"TERM": "dumb", "COLORTERM": "truecolor"},
			want: jira.GraphicsNone,
		},
		{
			name: "kitty by its TERM",
			env:  map[string]string{"TERM": "xterm-kitty"},
			want: jira.GraphicsKitty,
		},
		{
			name: "kitty by its window id under a borrowed TERM",
			env:  map[string]string{"TERM": "xterm-256color", "KITTY_WINDOW_ID": "1"},
			want: jira.GraphicsKitty,
		},
		{
			name: "ghostty speaks the kitty protocol",
			env:  map[string]string{"TERM": "xterm-256color", "TERM_PROGRAM": "ghostty"},
			want: jira.GraphicsKitty,
		},
		{
			name: "wezterm speaks it too",
			env:  map[string]string{"TERM": "xterm-256color", "WEZTERM_PANE": "0"},
			want: jira.GraphicsKitty,
		},
		{
			name: "iTerm2 by its program name",
			env:  map[string]string{"TERM": "xterm-256color", "TERM_PROGRAM": "iTerm.app"},
			want: jira.GraphicsITerm2,
		},
		{
			name: "iTerm2 forwarded through ssh by LC_TERMINAL",
			env:  map[string]string{"TERM": "xterm-256color", "LC_TERMINAL": "iTerm2"},
			want: jira.GraphicsITerm2,
		},
		{
			name: "mintty speaks the iTerm2 protocol on Windows",
			env:  map[string]string{"TERM": "mintty", "TERM_PROGRAM": "mintty"},
			want: jira.GraphicsITerm2,
		},
		{
			name: "an image protocol does not survive tmux",
			env:  map[string]string{"TERM": "screen-256color", "TMUX": "/tmp/tmux-501/default,1,0", "KITTY_WINDOW_ID": "1"},
			want: jira.GraphicsHalfBlocks,
		},
		{
			name: "a 256 colour terminal draws half blocks",
			env:  map[string]string{"TERM": "xterm-256color"},
			want: jira.GraphicsHalfBlocks,
		},
		{
			name: "true colour with a plain TERM draws them as well",
			env:  map[string]string{"TERM": "xterm", "COLORTERM": "truecolor"},
			want: jira.GraphicsHalfBlocks,
		},
		{
			name: "sixteen colours are not enough to be worth it",
			env:  map[string]string{"TERM": "xterm"},
			want: jira.GraphicsNone,
		},
		{
			name: "NO_COLOR takes the colour away and with it the blocks",
			env:  map[string]string{"TERM": "xterm-256color", "NO_COLOR": "1"},
			want: jira.GraphicsNone,
		},
		{
			name: "NO_COLOR is not a request to stop showing images",
			env:  map[string]string{"TERM": "xterm-kitty", "NO_COLOR": "1"},
			want: jira.GraphicsKitty,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			if got := capsDetectGraphics(capsFakeEnv(tt.env)); got != tt.want {
				t.Errorf("graphics = %s, want %s", got, tt.want)
			}
		})
	}
}

// The environment is process-wide, so this one cannot run in parallel with
// anything: t.Setenv says so and the testing package enforces it.
func TestCapabilities_ReportsTheTerminalItWasStartedIn(t *testing.T) {
	s := jiratest.NewServer()
	defer s.Close()

	for _, unset := range []string{"KITTY_WINDOW_ID", "TERM_PROGRAM", "LC_TERMINAL", "WEZTERM_PANE", "TMUX", "NO_COLOR"} {
		t.Setenv(unset, "")
	}
	t.Setenv("TERM", "xterm-kitty")

	c, _ := testClient(t, s.URL())
	got, err := c.Capabilities(t.Context(), capsProject)
	if err != nil {
		t.Fatalf("probing %s: %v", capsProject, err)
	}
	if got.Graphics != jira.GraphicsKitty {
		t.Errorf("graphics = %s, want kitty from TERM", got.Graphics)
	}
}

func TestCapsList_WritesAListTheWayASentenceDoes(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name  string
		items []string
		want  string
	}{
		{name: "nothing at all", items: nil, want: ""},
		{name: "one", items: []string{"Move Issues"}, want: "Move Issues"},
		{name: "two", items: []string{"Move Issues", "Create Issues"}, want: "Move Issues and Create Issues"},
		{
			name:  "three",
			items: []string{"Bulk Change", "Move Issues", "Create Issues"},
			want:  "Bulk Change, Move Issues and Create Issues",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			if got := capsList(tt.items); got != tt.want {
				t.Errorf("capsList = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestCapsFailed_PrefersJirasOwnSentenceToOurs(t *testing.T) {
	t.Parallel()

	refused := capsFailed("whether this token can read plans",
		&jira.CapabilityError{Capability: jira.CapPlans, Reason: capsPlansReason})
	if refused != capsPlansReason {
		t.Errorf("reason = %q, want Jira's own sentence", refused)
	}

	broken := capsFailed("whether this token can read plans", errors.New("the connection was reset"))
	const want = "Jira did not answer whether this token can read plans: the connection was reset"
	if broken != want {
		t.Errorf("reason = %q, want %q", broken, want)
	}
	if !strings.HasPrefix(broken, "Jira did not answer") {
		t.Error("a failure to reach Jira reads as a refusal by Jira")
	}
}

func TestCapabilities_ReportsAnUnreachableSiteRatherThanFiveDenials(t *testing.T) {
	t.Parallel()

	// A mistyped site, a DNS failure, captive wifi. Every probe fails at the
	// transport, and answering "you may do nothing" would be cached — and worse,
	// the kernel replaces a good capability set with it on a refresh.
	refused := &stubDoer{err: &url.Error{
		Op:  "Get",
		URL: "https://example.atlassian.net/rest/api/3/configuration",
		Err: errors.New("dial tcp 127.0.0.1:9: connect: connection refused"),
	}}
	c, _ := testClient(t, "example.atlassian.net",
		WithHTTPClient(refused), WithRetry(RetryPolicy{Attempts: 1}))

	caps, err := c.Capabilities(t.Context(), "EX")
	var broken *jira.TransportError
	if !errors.As(err, &broken) {
		t.Fatalf("got caps=%+v err=%v; want a *jira.TransportError: a host that never answered has told us nothing", caps, err)
	}
	if caps.Attachments.OK || caps.BulkMove.OK {
		t.Error("a voided probe should not also hand back capabilities")
	}
}
