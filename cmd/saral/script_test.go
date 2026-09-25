package main

import (
	"bytes"
	"context"
	"errors"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/varijkapil13/saral/internal/config"
	"github.com/varijkapil13/saral/pkg/adf"
	"github.com/varijkapil13/saral/pkg/jira"
	"github.com/varijkapil13/saral/pkg/jira/jiratest"
)

type scriptRun struct {
	stdin  string
	client jira.SessionClient
}

func (r scriptRun) do(t *testing.T, args ...string) (string, error) {
	t.Helper()
	sub, ok := lookupSubcommand(args[0])
	if !ok {
		t.Fatalf("no subcommand %s", args[0])
	}
	opt := options{}
	if r.client != nil {
		opt.connectVia = func(config.Profile) (jira.SessionClient, error) { return r.client, nil }
	}
	var stdout, stderr bytes.Buffer
	inv := &invocation{opt: opt, stdin: strings.NewReader(r.stdin), stdout: &stdout, stderr: &stderr}
	err := sub.run(inv, args[1:])
	if stderr.Len() > 0 {
		t.Errorf("saral %s wrote to stderr: %q", strings.Join(args, " "), stderr.String())
	}
	return stdout.String(), err
}

func script(t *testing.T, client jira.SessionClient, args ...string) (string, error) {
	t.Helper()
	return scriptRun{client: client}.do(t, args...)
}

func siteFake(opts ...jiratest.Option) *jiratest.Fake {
	return jiratest.New(append([]jiratest.Option{
		jiratest.WithProject("PROJ", jiratest.Scrum),
		jiratest.WithIssues(jiratest.GenFor("PROJ", 12)),
	}, opts...)...)
}

func wantCode(t *testing.T, err error, code int, says string) {
	t.Helper()
	if got := exitCodeOf(err); got != code {
		t.Fatalf("exit %d (%v), want %d", got, err, code)
	}
	if says != "" && (err == nil || !strings.Contains(err.Error(), says)) {
		t.Errorf("the error %v does not mention %q", err, says)
	}
}

// writesFail refuses only the writes, so a failure reaches past the reads.
type writesFail struct {
	*jiratest.Fake
	err error
}

func (w writesFail) CreateIssue(context.Context, jira.IssueInput) (jira.Issue, error) {
	return jira.Issue{}, w.err
}

func (w writesFail) UpdateIssue(context.Context, string, jira.IssuePatch) error { return w.err }

func (w writesFail) Transition(context.Context, string, string, jira.IssuePatch) error { return w.err }

func (w writesFail) AddComment(context.Context, string, adf.Doc) (jira.Comment, error) {
	return jira.Comment{}, w.err
}

var siteFailures = map[string]struct {
	err  error
	code int
	says string
}{
	"403":       {err: &jira.CapabilityError{Reason: "needs Edit issues"}, code: exitOther, says: "needs Edit issues"},
	"429":       {err: &jira.RateLimitError{RetryAfter: 30 * time.Second}, code: exitOther, says: "rate limited"},
	"transport": {err: &jira.TransportError{Op: "POST /rest/api/3/issue", Err: errors.New("connection refused")}, code: exitOther, says: "connection refused"},
	"401":       {err: &jira.AuthError{}, code: exitAuth, says: "authentication failed"},
}

var scriptCalls = map[string][]string{
	"issue view":   {"issue", "view", "PROJ-1"},
	"issue create": {"issue", "create", "--project", "PROJ", "--type", "Story", "--summary", "A new one"},
	"search":       {"search", "project = PROJ"},
	"transition":   {"transition", "PROJ-2", "Triage"},
	"comment add":  {"comment", "add", "PROJ-1", "-m", "hello"},
	"assign":       {"assign", "PROJ-1", "me"},
}

func TestScript_TheFirstRequestFailing(t *testing.T) {
	for cmd, args := range scriptCalls {
		for name, tc := range siteFailures {
			t.Run(cmd+"/"+name, func(t *testing.T) {
				writeProfile(t)
				f := siteFake()
				f.FailNext(tc.err)
				out, err := script(t, f, args...)
				wantCode(t, err, tc.code, tc.says)
				if out != "" {
					t.Errorf("a failed run printed %q", out)
				}
			})
		}
	}
}

func TestScript_TheWriteFailing(t *testing.T) {
	writes := []string{"issue create", "transition", "comment add", "assign"}
	for _, cmd := range writes {
		for name, tc := range siteFailures {
			t.Run(cmd+"/"+name, func(t *testing.T) {
				writeProfile(t)
				f := siteFake()
				out, err := script(t, writesFail{Fake: f, err: tc.err}, scriptCalls[cmd]...)
				wantCode(t, err, tc.code, tc.says)
				if out != "" {
					t.Errorf("a refused write printed %q", out)
				}
			})
		}
	}
}

func TestScript_WithoutAProfile(t *testing.T) {
	for cmd, args := range scriptCalls {
		t.Run(cmd, func(t *testing.T) {
			isolated(t)
			_, err := script(t, nil, args...)
			wantCode(t, err, exitConfig, envToken)
		})
	}
}

func TestScript_TheTokenDoesNotResolve(t *testing.T) {
	writeProfile(t)
	t.Setenv("SARAL_TEST_TOKEN", "")
	_, err := script(t, nil, "issue", "view", "PROJ-1")
	wantCode(t, err, exitAuth, "SARAL_TEST_TOKEN")
}

func TestScript_TheClientCannotBeOpened(t *testing.T) {
	writeProfile(t)
	opt := options{connectVia: func(config.Profile) (jira.SessionClient, error) { return nil, errors.New("no keychain") }}
	sub, _ := lookupSubcommand("search")
	err := sub.run(&invocation{opt: opt, stdout: &bytes.Buffer{}, stderr: &bytes.Buffer{}}, []string{"project = PROJ"})
	wantCode(t, err, exitAuth, "no keychain")
}

func TestScript_UsageMistakes(t *testing.T) {
	tests := map[string]struct {
		args []string
		says string
	}{
		"an issue group with no action":  {args: []string{"issue"}, says: "create, view"},
		"an unknown issue action":        {args: []string{"issue", "delete", "PROJ-1"}, says: "delete"},
		"a view with no key":             {args: []string{"issue", "view"}, says: "one issue key"},
		"a view of something not a key":  {args: []string{"issue", "view", "board"}, says: "not an issue key"},
		"a link to another site":         {args: []string{"issue", "view", "https://other.atlassian.net/browse/PROJ-1"}, says: "other.atlassian.net"},
		"an unknown flag":                {args: []string{"issue", "view", "PROJ-1", "--yaml"}, says: "--help"},
		"a search with no query":         {args: []string{"search"}, says: "quoted"},
		"a search with a blank query":    {args: []string{"search", "  "}, says: "needs a JQL"},
		"an unquoted search":             {args: []string{"search", "project", "=", "PROJ"}, says: "quoted"},
		"a limit of zero":                {args: []string{"search", "project = PROJ", "--limit", "0"}, says: "at least 1"},
		"a field this site has not":      {args: []string{"search", "project = PROJ", "--fields", "Velocity Index"}, says: "Velocity Index"},
		"a create with no summary":       {args: []string{"issue", "create", "--project", "PROJ", "--type", "Story"}, says: "--summary"},
		"a create with no type":          {args: []string{"issue", "create", "--project", "PROJ", "--summary", "x"}, says: "--type"},
		"a create with no project":       {args: []string{"issue", "create", "--type", "Story", "--summary", "x"}, says: "--project"},
		"a create of an unknown type":    {args: []string{"issue", "create", "--project", "PROJ", "--type", "Saga", "--summary", "x"}, says: "Story"},
		"a create with a bad parent":     {args: []string{"issue", "create", "--project", "PROJ", "--type", "Story", "--summary", "x", "--parent", "nope"}, says: "nope"},
		"a create with a missing file":   {args: []string{"issue", "create", "--project", "PROJ", "--type", "Story", "--summary", "x", "--description-file", "/nonexistent/d.md"}, says: "/nonexistent/d.md"},
		"a create with a stray argument": {args: []string{"issue", "create", "PROJ"}, says: "flags only"},
		"a transition with no target":    {args: []string{"transition", "PROJ-1"}, says: "where to move it"},
		"a transition nowhere":           {args: []string{"transition", "PROJ-2", "Orbit"}, says: "Move to"},
		"a comment with nothing in it":   {args: []string{"comment", "add", "PROJ-1", "-m", "   "}, says: "something to say"},
		"a comment two ways":             {args: []string{"comment", "add", "PROJ-1", "-m", "a", "--file", "b"}, says: "not both"},
		"a comment with no key":          {args: []string{"comment", "add", "-m", "hi"}, says: "one issue key"},
		"an assign to nobody named":      {args: []string{"assign", "PROJ-1"}, says: "who to assign"},
		"an assign nobody matches":       {args: []string{"assign", "PROJ-1", "zebedee"}, says: "zebedee"},
		"an assign several match":        {args: []string{"assign", "PROJ-1", "a"}, says: "acct-ada"},
		"a completion for no shell":      {args: []string{"completion"}, says: "bash, zsh, fish"},
		"a completion for another shell": {args: []string{"completion", "tcsh"}, says: "tcsh"},
		"an open of no key":              {args: []string{"open"}, says: "one issue key"},
	}
	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			writeProfile(t)
			out, err := script(t, siteFake(), tc.args...)
			wantCode(t, err, exitUsage, tc.says)
			if out != "" {
				t.Errorf("a refused run printed %q", out)
			}
		})
	}
}

func TestScript_FlagsMayFollowTheArguments(t *testing.T) {
	writeProfile(t)
	f := siteFake()
	before, err := script(t, f, "issue", "view", "--json", "PROJ-1")
	if err != nil {
		t.Fatal(err)
	}
	after, err := script(t, f, "issue", "view", "PROJ-1", "--json")
	if err != nil {
		t.Fatal(err)
	}
	if before != after || !strings.HasPrefix(before, "{") {
		t.Errorf("--json before and after the key differ, or neither is JSON:\n%s\n---\n%s", before, after)
	}
}

func TestScript_DoubleDashEndsTheFlags(t *testing.T) {
	writeProfile(t)
	f := siteFake()
	out, err := script(t, f, "comment", "add", "PROJ-1", "--", "-m")
	wantCode(t, err, exitUsage, "one issue key")
	if out != "" || slices.Contains(f.Calls(), "AddComment") {
		t.Errorf("-m after -- was read as a flag: %q, calls %v", out, f.Calls())
	}
}

func TestScript_HelpGoesToStdoutAndSucceeds(t *testing.T) {
	for _, args := range [][]string{
		{"issue", "view", "--help"}, {"issue", "create", "-h"}, {"search", "-h"}, {"transition", "-h"},
		{"comment", "add", "-h"}, {"assign", "-h"}, {"open", "-h"}, {"issue", "--help"}, {"comment", "help"},
		{"completion", "--help"},
	} {
		t.Run(strings.Join(args, " "), func(t *testing.T) {
			isolated(t)
			out, err := script(t, nil, args...)
			if err != nil {
				t.Fatalf("%v", err)
			}
			if !strings.HasPrefix(out, "usage: saral "+args[0]) {
				t.Errorf("help does not start with its usage line:\n%s", out)
			}
		})
	}
}

func TestScript_PlainOutputCannotRepaintTheTerminal(t *testing.T) {
	writeProfile(t)
	issues := jiratest.GenFor("PROJ", 1)
	issues[0].Summary = "one\ttwo\nthree \x1b[2Jfour\u202e"
	f := jiratest.New(jiratest.WithProject("PROJ", jiratest.Scrum), jiratest.WithIssues(issues))
	for _, args := range [][]string{
		{"search", "project = PROJ", "--fields", "summary"},
		{"issue", "view", issues[0].Key},
	} {
		out, err := script(t, f, args...)
		if err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(out, "one two three four") {
			t.Errorf("%v: the summary did not arrive as one clean cell:\n%q", args, out)
		}
		if strings.ContainsAny(out, "\x1b\u202e") {
			t.Errorf("%v: a control sequence reached the terminal:\n%q", args, out)
		}
	}
}
