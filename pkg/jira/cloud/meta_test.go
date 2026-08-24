package cloud

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/varijkapil13/saral/pkg/jira"
	"github.com/varijkapil13/saral/pkg/jira/jiratest"
)

const (
	metaProject     = "EX"
	metaTaskID      = "10001"
	metaBugID       = "10004"
	metaTypesRoute  = "/rest/api/3/issue/createmeta/{projectIdOrKey}/issuetypes"
	metaFieldsRoute = "/rest/api/3/issue/createmeta/{projectIdOrKey}/issuetypes/{issueTypeId}"
	metaTypesPath   = "/rest/api/3/issue/createmeta/EX/issuetypes"
	metaFieldsPath  = "/rest/api/3/issue/createmeta/EX/issuetypes/10001"
)

func metaField(t *testing.T, schema jira.Schema, id string) jira.FieldMeta {
	t.Helper()

	for i := range schema.Fields {
		if schema.Fields[i].Field.ID == id {
			return schema.Fields[i]
		}
	}
	t.Fatalf("the create screen carries no field %q; it has %v", id, metaIDs(schema))
	return jira.FieldMeta{}
}

func metaIDs(schema jira.Schema) []string {
	out := make([]string, 0, len(schema.Fields))
	for i := range schema.Fields {
		out = append(out, schema.Fields[i].Field.ID)
	}
	return out
}

func metaLabels(options []jira.Option) []string {
	out := make([]string, 0, len(options))
	for _, o := range options {
		out = append(out, o.Label)
	}
	return out
}

func TestCreateMeta_ReadsOneIssueTypesCreateScreen(t *testing.T) {
	t.Parallel()

	s := jiratest.NewServer()
	defer s.Close()

	c, _ := testClient(t, s.URL())
	got, err := c.CreateMeta(t.Context(), metaProject, metaTaskID)
	if err != nil {
		t.Fatalf("reading the create screen: %v", err)
	}

	if got.Project.Key != metaProject || got.Project.Name == "" || got.Project.ID == "" {
		t.Errorf("the project reads as %+v, want the key, id and name the screen states", got.Project)
	}
	if got.IssueType.ID != metaTaskID || got.IssueType.Name == "" {
		t.Errorf("the issue type reads as %+v, want the one the project offers under %s", got.IssueType, metaTaskID)
	}
	if len(got.Fields) < 10 {
		t.Fatalf("the screen carries %d fields, want the whole page: %v", len(got.Fields), metaIDs(got))
	}

	summary := metaField(t, got, "summary")
	if !summary.Required {
		t.Error("summary is not required, and the screen says it is")
	}
	if summary.Field.Schema.Type != "string" || summary.Field.Schema.System != "summary" {
		t.Errorf("summary reads as %+v, want the schema the screen states", summary.Field.Schema)
	}

	required := make([]string, 0, 4)
	for _, f := range got.Required() {
		required = append(required, f.Field.ID)
	}
	if strings.Join(required, ",") != "project,issuetype,summary" {
		t.Errorf("the required fields are %v, want exactly the ones the screen marks required", required)
	}
}

func TestCreateMeta_AnswersADifferentScreenForADifferentIssueType(t *testing.T) {
	t.Parallel()

	s := jiratest.NewServer()
	defer s.Close()

	c, _ := testClient(t, s.URL())
	task, err := c.CreateMeta(t.Context(), metaProject, metaTaskID)
	if err != nil {
		t.Fatalf("reading the task screen: %v", err)
	}
	bug, err := c.CreateMeta(t.Context(), metaProject, metaBugID)
	if err != nil {
		t.Fatalf("reading the bug screen: %v", err)
	}

	if metaField(t, task, "description").Required {
		t.Error("description is required on the task screen, and only the bug screen requires it")
	}
	if !metaField(t, bug, "description").Required {
		t.Error("description is not required on the bug screen, and the screen says it is")
	}
	if bug.IssueType.ID == task.IssueType.ID {
		t.Errorf("both screens report issue type %s", bug.IssueType.ID)
	}

	taskOnly := metaIDs(task)
	bugOnly := metaIDs(bug)
	if strings.Join(taskOnly, ",") == strings.Join(bugOnly, ",") {
		t.Error("both issue types produced the same field list, so nothing is per-issue-type")
	}
}

func TestCreateMeta_KeepsTheAllowedValuesEachFieldStates(t *testing.T) {
	t.Parallel()

	s := jiratest.NewServer()
	defer s.Close()

	c, _ := testClient(t, s.URL())
	got, err := c.CreateMeta(t.Context(), metaProject, metaTaskID)
	if err != nil {
		t.Fatalf("reading the create screen: %v", err)
	}

	// The ids are the site's own, so the field is found by the schema that
	// describes it rather than by a customfield number written down here.
	var single, cascading, multiUser jira.FieldMeta
	for _, f := range got.Fields {
		switch {
		case f.Field.Schema.Type == "option" && f.Field.Schema.CustomID != 0:
			single = f
		case f.Field.Schema.Type == "option-with-child":
			cascading = f
		case f.Field.Schema.Type == "array" && f.Field.Schema.Items == "user" && f.Field.Schema.CustomID != 0:
			multiUser = f
		}
	}

	if len(single.AllowedValues) != 3 {
		t.Errorf("the single select offers %v, want the three values the screen states", metaLabels(single.AllowedValues))
	}
	if len(cascading.AllowedValues) != 2 {
		t.Fatalf("the cascading select offers %v, want two first-level values", metaLabels(cascading.AllowedValues))
	}
	if got, want := len(cascading.AllowedValues[0].Children), 2; got != want {
		t.Errorf("the first cascading value has %d children, want %d", got, want)
	}
	if cascading.AllowedValues[0].Children[0].ID == "" {
		t.Error("a cascading child arrived with no id, so nothing can be written back for it")
	}
	if len(cascading.AllowedValues[1].Children) != 0 {
		t.Error("the second cascading value invented children the screen does not state")
	}
	if multiUser.AutoCompleteURL == "" {
		t.Error("the user picker lost the autocomplete URL the screen states")
	}
}

func TestCreateMeta_ReadsAFieldWhoseNameIsTranslated(t *testing.T) {
	t.Parallel()

	// The bug screen carries a custom select whose display name is German. It
	// has to arrive with its options like any other, because nothing decides
	// anything from a display name.
	s := jiratest.NewServer()
	defer s.Close()

	c, _ := testClient(t, s.URL())
	got, err := c.CreateMeta(t.Context(), metaProject, metaBugID)
	if err != nil {
		t.Fatalf("reading the create screen: %v", err)
	}

	found := false
	for _, f := range got.Fields {
		if f.Field.Schema.Custom == "" || f.Field.Schema.Type != "option" {
			continue
		}
		if len(f.AllowedValues) == 0 {
			t.Errorf("the custom select %q arrived with no options", f.Name)
		}
		if f.Field.Name != f.Name || f.Name == "" {
			t.Errorf("the field reference and the label disagree: %q and %q", f.Field.Name, f.Name)
		}
		found = true
	}
	if !found {
		t.Fatal("the bug screen carries no custom select at all")
	}
}

func TestCreateMeta_AsksBothEndpointsAndNamesThePageSize(t *testing.T) {
	t.Parallel()

	s := jiratest.NewServer()
	defer s.Close()

	c, _ := testClient(t, s.URL())
	if _, err := c.CreateMeta(t.Context(), metaProject, metaTaskID); err != nil {
		t.Fatalf("reading the create screen: %v", err)
	}

	paths := make([]string, 0, 2)
	for _, req := range s.Requests() {
		paths = append(paths, req.Path)
		if !strings.Contains(req.Query, "maxResults=") {
			t.Errorf("%s was asked for without a page size: %q", req.Path, req.Query)
		}
	}
	want := []string{metaTypesPath, metaFieldsPath}
	if strings.Join(paths, " ") != strings.Join(want, " ") {
		t.Errorf("the calls were %v, want the issue type list and then that one type's fields", paths)
	}
}

func TestCreateMeta_NamesAnIssueTypeTheProjectDoesNotOffer(t *testing.T) {
	t.Parallel()

	s := jiratest.NewServer()
	defer s.Close()

	c, _ := testClient(t, s.URL())
	_, err := c.CreateMeta(t.Context(), metaProject, "99999")

	var missing *jira.NotFoundError
	if !errors.As(err, &missing) {
		t.Fatalf("got %v, want a *jira.NotFoundError naming the issue type", err)
	}
	if !strings.Contains(missing.ID, "99999") || !strings.Contains(missing.ID, metaProject) {
		t.Errorf("the failure reads %q, want it to name both the type and the project", missing.Error())
	}
	for _, req := range s.Requests() {
		if strings.HasPrefix(req.Path, metaTypesPath+"/") {
			t.Errorf("the fields of %s were fetched for a type the project does not offer", req.Path)
		}
	}
}

func TestCreateMeta_RefusesAScreenThatNamesNoProjectOrTypeWithoutAskingTheSite(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name        string
		project     string
		issueType   string
		wantsFields []string
	}{
		{name: "no project", project: "  ", issueType: metaTaskID, wantsFields: []string{"project"}},
		{name: "no issue type", project: metaProject, issueType: "", wantsFields: []string{"issuetype"}},
		{name: "neither", project: "", issueType: "", wantsFields: []string{"project", "issuetype"}},
		{name: "a project key that would change the route", project: "a/b", issueType: metaTaskID, wantsFields: []string{"project"}},
		{name: "an issue type id that would change the route", project: metaProject, issueType: "1/2", wantsFields: []string{"issuetype"}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			s := jiratest.NewServer()
			defer s.Close()

			c, _ := testClient(t, s.URL())
			_, err := c.CreateMeta(t.Context(), tt.project, tt.issueType)

			var invalid *jira.ValidationError
			if !errors.As(err, &invalid) {
				t.Fatalf("got %v, want a *jira.ValidationError", err)
			}
			for _, field := range tt.wantsFields {
				if _, ok := invalid.For(field); !ok {
					t.Errorf("the refusal says nothing about %q: %v", field, invalid)
				}
			}
			if n := len(s.Requests()); n != 0 {
				t.Errorf("the site was asked %d times for a screen that cannot be addressed", n)
			}
		})
	}
}

func TestCreateMeta_WalksEveryPageOfFields(t *testing.T) {
	t.Parallel()

	pages := []string{
		`{"startAt":0,"maxResults":1,"total":2,"fields":[
			{"required":true,"schema":{"type":"string","system":"summary"},"name":"Summary","key":"summary","fieldId":"summary","operations":["set"]}]}`,
		`{"startAt":1,"maxResults":1,"total":2,"fields":[
			{"required":false,"schema":{"type":"number","custom":"x:float","customId":10032},"name":"Points","key":"customfield_10032","fieldId":"customfield_10032","operations":["set"]}]}`,
	}
	served := 0
	s := jiratest.NewServer(jiratest.WithHandler(http.MethodGet, metaFieldsRoute, func(w http.ResponseWriter, _ *http.Request) {
		body := pages[min(served, len(pages)-1)]
		served++
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(body))
	}))
	defer s.Close()

	c, _ := testClient(t, s.URL())
	got, err := c.CreateMeta(t.Context(), metaProject, metaTaskID)
	if err != nil {
		t.Fatalf("reading the create screen: %v", err)
	}
	if ids := metaIDs(got); strings.Join(ids, ",") != "summary,customfield_10032" {
		t.Errorf("the screen reads as %v, want both pages", ids)
	}
	if served != 2 {
		t.Errorf("the fields endpoint was asked %d times, want one per page", served)
	}
}

func TestCreateMeta_StopsAtThePageThatCarriesTheIssueType(t *testing.T) {
	t.Parallel()

	served := 0
	s := jiratest.NewServer(jiratest.WithHandler(http.MethodGet, metaTypesRoute, func(w http.ResponseWriter, _ *http.Request) {
		served++
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"startAt":0,"maxResults":1,"total":4,"issueTypes":[{"id":"` + metaTaskID + `","name":"Task"}]}`))
	}))
	defer s.Close()

	c, _ := testClient(t, s.URL())
	if _, err := c.CreateMeta(t.Context(), metaProject, metaTaskID); err != nil {
		t.Fatalf("reading the create screen: %v", err)
	}
	if served != 1 {
		t.Errorf("the issue type list was walked %d pages deep, want it to stop where the type was found", served)
	}
}

func TestCreateMeta_MapsEveryRefusedStatusToItsTypedError(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		route  string
		status int
		body   string
		assert func(*testing.T, error)
	}{
		{
			name:   "a project this token may not create in",
			route:  metaTypesRoute,
			status: http.StatusForbidden,
			body:   "plans_403.json",
			assert: func(t *testing.T, err error) {
				var missing *jira.CapabilityError
				if !errors.As(err, &missing) {
					t.Fatalf("got %v, want a *jira.CapabilityError: a 403 is an answer about what is possible", err)
				}
				if missing.Reason == "" {
					t.Error("the refusal carries no reason to show the user")
				}
			},
		},
		{
			name:   "an issue type screen this token may not see",
			route:  metaFieldsRoute,
			status: http.StatusForbidden,
			body:   "plans_403.json",
			assert: func(t *testing.T, err error) {
				var missing *jira.CapabilityError
				if !errors.As(err, &missing) {
					t.Fatalf("got %v, want a *jira.CapabilityError", err)
				}
			},
		},
		{
			name:   "a project that does not exist",
			route:  metaTypesRoute,
			status: http.StatusNotFound,
			assert: func(t *testing.T, err error) {
				var gone *jira.NotFoundError
				if !errors.As(err, &gone) {
					t.Fatalf("got %v, want a *jira.NotFoundError", err)
				}
				if gone.Kind != "project" || gone.ID != metaProject {
					t.Errorf("the failure names %s %q, want the project it asked about", gone.Kind, gone.ID)
				}
			},
		},
		{
			name:   "a token the site rejects",
			route:  metaTypesRoute,
			status: http.StatusUnauthorized,
			assert: func(t *testing.T, err error) {
				var denied *jira.AuthError
				if !errors.As(err, &denied) {
					t.Fatalf("got %v, want a *jira.AuthError", err)
				}
			},
		},
		{
			name:   "the site failing on its own account",
			route:  metaFieldsRoute,
			status: http.StatusInternalServerError,
			assert: func(t *testing.T, err error) {
				var broken *jira.TransportError
				if !errors.As(err, &broken) {
					t.Fatalf("got %v, want a *jira.TransportError", err)
				}
				if broken.Status != http.StatusInternalServerError {
					t.Errorf("the failure reports HTTP %d, want 500", broken.Status)
				}
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			s := jiratest.NewServer(jiratest.WithStatus(http.MethodGet, tt.route, tt.status, tt.body))
			defer s.Close()

			c, _ := testClient(t, s.URL(), WithRetry(RetryPolicy{Attempts: 1}))
			_, err := c.CreateMeta(t.Context(), metaProject, metaTaskID)
			if err == nil {
				t.Fatal("the create screen was read from a site that refused it")
			}
			tt.assert(t, err)
		})
	}
}

func TestCreateMeta_ReportsARateLimitWithTheIntervalTheSiteAskedFor(t *testing.T) {
	t.Parallel()

	s := jiratest.NewServer(jiratest.WithRateLimit(http.MethodGet, metaFieldsRoute, 45*time.Second))
	defer s.Close()

	c, _ := testClient(t, s.URL(), WithRetry(RetryPolicy{Attempts: 1}))
	_, err := c.CreateMeta(t.Context(), metaProject, metaTaskID)

	var limited *jira.RateLimitError
	if !errors.As(err, &limited) {
		t.Fatalf("got %v, want a *jira.RateLimitError", err)
	}
	if limited.RetryAfter != 45*time.Second {
		t.Errorf("the countdown is %s, want 45s", limited.RetryAfter)
	}
	if limited.Endpoint != metaFieldsPath {
		t.Errorf("the limit names %q, want %q", limited.Endpoint, metaFieldsPath)
	}
}

func TestCreateMeta_TreatsABodyThatWillNotDecodeAsATransportFailure(t *testing.T) {
	t.Parallel()

	s := jiratest.NewServer(jiratest.WithHandler(http.MethodGet, metaFieldsRoute, jsonHandler(http.StatusOK, `{"fields":[{"required":`)))
	defer s.Close()

	c, _ := testClient(t, s.URL())
	_, err := c.CreateMeta(t.Context(), metaProject, metaTaskID)

	var broken *jira.TransportError
	if !errors.As(err, &broken) {
		t.Fatalf("got %v, want a *jira.TransportError: the call reached Jira and came back unusable", err)
	}
}

func TestCreateMeta_ReportsAnUnreachableSiteAsATransportFailure(t *testing.T) {
	t.Parallel()

	s := jiratest.NewServer()
	url := s.URL()
	s.Close()

	c, _ := testClient(t, url, WithRetry(RetryPolicy{Attempts: 1}))
	_, err := c.CreateMeta(t.Context(), metaProject, metaTaskID)

	var broken *jira.TransportError
	if !errors.As(err, &broken) {
		t.Fatalf("got %v, want a *jira.TransportError", err)
	}
	if broken.Status != 0 {
		t.Errorf("the failure reports HTTP %d, want no status at all", broken.Status)
	}
}

func TestCreateMeta_ReturnsTheCallersOwnErrorWhenItCancels(t *testing.T) {
	t.Parallel()

	ctx, cancel := context.WithCancel(t.Context())
	s := jiratest.NewServer(jiratest.WithHandler(http.MethodGet, metaTypesRoute, func(w http.ResponseWriter, r *http.Request) {
		cancel()
		<-r.Context().Done()
	}))
	defer s.Close()

	c, _ := testClient(t, s.URL())
	_, err := c.CreateMeta(ctx, metaProject, metaTaskID)

	if !errors.Is(err, context.Canceled) {
		t.Fatalf("got %v, want the caller's own context.Canceled", err)
	}
}
