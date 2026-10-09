package search

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	appquery "github.com/varijkapil13/saral/internal/app/query"
	"github.com/varijkapil13/saral/pkg/jira"
	"github.com/varijkapil13/saral/pkg/jira/jiratest"
)

func textFake(opts ...jiratest.Option) *jiratest.Fake {
	opts = append([]jiratest.Option{jiratest.WithProject("PROJ", jiratest.Scrum)}, opts...)
	return jiratest.New(opts...)
}

func textSearch(f *jiratest.Fake) *TextSearch { return NewTextSearch(appquery.NewSearch(f), f) }

type textFailure struct {
	name string
	err  error
	is   func(error) bool
}

func textFailures() []textFailure {
	return []textFailure{
		{"403", &jira.CapabilityError{Reason: "no permission"}, func(e error) bool {
			var c *jira.CapabilityError
			return errors.As(e, &c)
		}},
		{"429", &jira.RateLimitError{RetryAfter: 5 * time.Second}, func(e error) bool {
			var r *jira.RateLimitError
			return errors.As(e, &r)
		}},
		{"transport", &jira.TransportError{Op: "GET", Err: errors.New("no route to host")}, func(e error) bool {
			var r *jira.TransportError
			return errors.As(e, &r)
		}},
	}
}

func TestCompose(t *testing.T) {
	t.Parallel()

	tq := jira.ParseText("login failure")
	site, ok := Compose(tq, false, "PROJ")
	if !ok || !strings.HasSuffix(site, " ORDER BY updated DESC") || strings.Contains(site, "project =") {
		t.Errorf("site scope gave %q (%t)", site, ok)
	}
	proj, ok := Compose(tq, true, "PROJ")
	if !ok || !strings.HasPrefix(proj, `project = "PROJ" AND `) || !strings.HasSuffix(proj, " ORDER BY updated DESC") {
		t.Errorf("project scope gave %q (%t)", proj, ok)
	}
	if noProject, _ := Compose(tq, true, ""); noProject != site {
		t.Errorf("project scope without a project gave %q, want %q", noProject, site)
	}
	if jql, ok := Compose(jira.ParseText("  "), false, ""); ok || jql != "" {
		t.Errorf("an empty query composed %q (%t)", jql, ok)
	}
}

func TestTextSearch_RunReturnsTheFirstPageAndPagesOn(t *testing.T) {
	t.Parallel()

	f := textFake(jiratest.WithIssues(jiratest.Gen(5)), jiratest.WithPageSize(2))
	ts := textSearch(f)

	res, err := ts.Run(context.Background(), "project = PROJ ORDER BY updated DESC")
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if len(res.Page.Items) == 0 || !res.Page.HasMore() {
		t.Fatalf("first page held %d items, more=%t", len(res.Page.Items), res.Page.HasMore())
	}
	next, err := ts.Next(context.Background(), res.Page)
	if err != nil || len(next.Items) == 0 {
		t.Fatalf("next: %d items, %v", len(next.Items), err)
	}
}

func TestTextSearch_NextOnTheLastPageSaysSo(t *testing.T) {
	t.Parallel()

	ts := textSearch(textFake())
	if _, err := ts.Next(context.Background(), jira.Page[jira.Issue]{}); !errors.Is(err, jira.ErrNoMorePages) {
		t.Errorf("next on an exhausted page returned %v", err)
	}
}

func TestTextSearch_KeyReadsOneIssue(t *testing.T) {
	t.Parallel()

	issues := jiratest.Gen(3)
	ts := textSearch(textFake(jiratest.WithIssues(issues)))

	iss, err := ts.Key(context.Background(), issues[1].Key)
	if err != nil || iss.Key != issues[1].Key {
		t.Fatalf("key: %q, %v", iss.Key, err)
	}
}

func TestTextSearch_PortFailuresPassThrough(t *testing.T) {
	t.Parallel()

	for _, tc := range textFailures() {
		t.Run("run "+tc.name, func(t *testing.T) {
			t.Parallel()
			f := textFake(jiratest.WithIssues(jiratest.Gen(3)))
			ts := textSearch(f)
			f.FailNextN(5, tc.err)
			if _, err := ts.Run(context.Background(), "project = PROJ"); err == nil || !tc.is(err) {
				t.Errorf("run returned %v", err)
			}
		})
		t.Run("key "+tc.name, func(t *testing.T) {
			t.Parallel()
			issues := jiratest.Gen(3)
			f := textFake(jiratest.WithIssues(issues))
			ts := textSearch(f)
			f.FailNext(tc.err)
			if _, err := ts.Key(context.Background(), issues[0].Key); err == nil || !tc.is(err) {
				t.Errorf("key returned %v", err)
			}
		})
		t.Run("next "+tc.name, func(t *testing.T) {
			t.Parallel()
			f := textFake(jiratest.WithIssues(jiratest.Gen(5)), jiratest.WithPageSize(2))
			ts := textSearch(f)
			res, err := ts.Run(context.Background(), "project = PROJ")
			if err != nil || !res.Page.HasMore() {
				t.Fatalf("setup: more=%t, %v", res.Page.HasMore(), err)
			}
			f.FailNextN(5, tc.err)
			if _, err := ts.Next(context.Background(), res.Page); err == nil || !tc.is(err) {
				t.Errorf("next returned %v", err)
			}
		})
	}
}
