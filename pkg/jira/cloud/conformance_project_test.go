package cloud

import (
	"errors"
	"net/http"
	"testing"
	"time"

	"github.com/varijkapil13/saral/pkg/jira"
	"github.com/varijkapil13/saral/pkg/jira/jiratest"
)

type projectBuilder func(*testing.T) jira.ProjectReader

func projectFromSite(t *testing.T, opts ...jiratest.ServerOption) jira.ProjectReader {
	t.Helper()

	s := jiratest.NewServer(opts...)
	t.Cleanup(s.Close)
	c, _ := testClient(t, s.URL())
	return c
}

func TestConformance_Project_BothAdaptersAgree(t *testing.T) {
	t.Parallel()

	site := func(t *testing.T) jira.ProjectReader { return projectFromSite(t) }
	fake := func(t *testing.T) jira.ProjectReader { return conformFake(t) }

	cases := []struct {
		name   string
		ref    string
		cloud  projectBuilder
		fake   projectBuilder
		assert func(*testing.T, jira.ProjectReader, jira.ProjectRef, error)
	}{
		{
			name: "by key", ref: conformProject, cloud: site, fake: fake,
			assert: func(t *testing.T, _ jira.ProjectReader, got jira.ProjectRef, err error) {
				t.Helper()
				if err != nil {
					t.Fatalf("Project: %v", err)
				}
				if got.Key != conformProject || got.ID == "" || got.Name == "" {
					t.Errorf("got %+v, want the key %q with an id and a name", got, conformProject)
				}
			},
		},
		{
			name: "by id answers the same project as by key", ref: conformProject, cloud: site, fake: fake,
			assert: func(t *testing.T, under jira.ProjectReader, byKey jira.ProjectRef, err error) {
				t.Helper()
				if err != nil {
					t.Fatalf("Project by key: %v", err)
				}
				byID, err := under.Project(t.Context(), byKey.ID)
				if err != nil {
					t.Fatalf("Project by id %q: %v", byKey.ID, err)
				}
				if byID != byKey {
					t.Errorf("by id %+v, by key %+v; want one project", byID, byKey)
				}
			},
		},
		{
			name: "an unknown project is not found",
			ref:  "NOPE",
			cloud: func(t *testing.T) jira.ProjectReader {
				return projectFromSite(t, jiratest.WithHandler(http.MethodGet, projectPath+"/{key}",
					jsonHandler(http.StatusNotFound, `{"errorMessages":["No project could be found with key 'NOPE'."],"errors":{}}`)))
			},
			fake: fake,
			assert: func(t *testing.T, _ jira.ProjectReader, got jira.ProjectRef, err error) {
				t.Helper()
				var missing *jira.NotFoundError
				if !errors.As(err, &missing) {
					t.Fatalf("got %+v, %T (%v); want a *jira.NotFoundError", got, err, err)
				}
				if missing.Kind != "project" || missing.ID != "NOPE" {
					t.Errorf("not found names %q %q, want project NOPE", missing.Kind, missing.ID)
				}
			},
		},
		{name: "a blank reference is refused", ref: "  ", cloud: site, fake: fake, assert: assertProjectRefused},
		{name: "a slash is refused", ref: "EX/version", cloud: site, fake: fake, assert: assertProjectRefused},
		{name: "a space is refused", ref: "E X", cloud: site, fake: fake, assert: assertProjectRefused},
		{name: "a percent sign is refused", ref: "E%58", cloud: site, fake: fake, assert: assertProjectRefused},
		{name: "a dot is refused", ref: "..", cloud: site, fake: fake, assert: assertProjectRefused},
		{
			name: "a rate limit is a rate limit", ref: conformProject,
			cloud: func(t *testing.T) jira.ProjectReader {
				return projectFromSite(t, jiratest.WithRateLimit(http.MethodGet, projectPath+"/{key}", 30*time.Second))
			},
			fake: func(t *testing.T) jira.ProjectReader {
				f := conformFake(t)
				f.FailNext(&jira.RateLimitError{RetryAfter: 30 * time.Second})
				return f
			},
			assert: func(t *testing.T, _ jira.ProjectReader, got jira.ProjectRef, err error) {
				t.Helper()
				var limited *jira.RateLimitError
				if !errors.As(err, &limited) {
					t.Fatalf("got %+v, %T (%v); want a *jira.RateLimitError", got, err, err)
				}
			},
		},
		{
			name: "a transport failure is a transport failure", ref: conformProject,
			cloud: func(t *testing.T) jira.ProjectReader {
				return projectFromSite(t, jiratest.WithHandler(http.MethodGet, projectPath+"/{key}",
					jsonHandler(http.StatusBadGateway, `{"errorMessages":["upstream is unwell"]}`)))
			},
			fake: func(t *testing.T) jira.ProjectReader {
				f := conformFake(t)
				f.FailNext(&jira.TransportError{Op: "Project", Status: http.StatusBadGateway, Err: errors.New("upstream is unwell")})
				return f
			},
			assert: func(t *testing.T, _ jira.ProjectReader, got jira.ProjectRef, err error) {
				t.Helper()
				var down *jira.TransportError
				if !errors.As(err, &down) {
					t.Fatalf("got %+v, %T (%v); want a *jira.TransportError", got, err, err)
				}
			},
		},
	}

	for _, tt := range cases {
		for _, adapter := range []struct {
			name string
			open projectBuilder
		}{
			{name: "cloud", open: tt.cloud},
			{name: "fake", open: tt.fake},
		} {
			t.Run(tt.name+"/"+adapter.name, func(t *testing.T) {
				t.Parallel()

				under := adapter.open(t)
				got, err := under.Project(t.Context(), tt.ref)
				tt.assert(t, under, got, err)
			})
		}
	}
}

func assertProjectRefused(t *testing.T, _ jira.ProjectReader, got jira.ProjectRef, err error) {
	t.Helper()

	var invalid *jira.ValidationError
	if !errors.As(err, &invalid) {
		t.Fatalf("got %+v, %T (%v); want a *jira.ValidationError", got, err, err)
	}
}
