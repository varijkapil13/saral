package main

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/varijkapil13/saral/pkg/adf"
	"github.com/varijkapil13/saral/pkg/jira"
	"github.com/varijkapil13/saral/pkg/jira/jiratest"
)

func readBack(t *testing.T, f *jiratest.Fake, key string) jira.Issue {
	t.Helper()
	iss, err := f.Issue(context.Background(), key)
	if err != nil {
		t.Fatal(err)
	}
	return iss
}

func TestIssueCreate_WritesWhatItWasGiven(t *testing.T) {
	writeProfile(t)
	f := siteFake()
	path := filepath.Join(t.TempDir(), "d.md")
	if err := os.WriteFile(path, []byte("Steps:\n\n- **one**\n- two\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	out, err := script(t, f, "issue", "create", "--project", "PROJ", "--type", "defect", "--summary", " Export drops rows ",
		"--description-file", path)
	if err != nil {
		t.Fatal(err)
	}
	key, link, ok := strings.Cut(strings.TrimSuffix(out, "\n"), "\t")
	if !ok || link != "https://example.atlassian.net/browse/"+key {
		t.Fatalf("create printed %q, want the key and its link", out)
	}
	iss := readBack(t, f, key)
	if iss.Summary != "Export drops rows" || iss.Type.Name != "Defect" || iss.Project.Key != "PROJ" {
		t.Errorf("created %q, a %s in %s", iss.Summary, iss.Type.Name, iss.Project.Key)
	}
	if md := adf.Markdown(iss.Description); !strings.Contains(md, "**one**") {
		t.Errorf("the description went out as %q", md)
	}
}

func TestIssueCreate_TakesTheProfileProjectAndStdin(t *testing.T) {
	cfgDir, _ := isolated(t)
	cfg := "active = \"work\"\n\n[profiles.work]\nsite  = \"example.atlassian.net\"\n" +
		"email = \"you@example.com\"\nproject = \"PROJ\"\ntoken = { env = \"SARAL_TEST_TOKEN\" }\n"
	if err := os.WriteFile(filepath.Join(cfgDir, "config.toml"), []byte(cfg), 0o600); err != nil {
		t.Fatal(err)
	}
	f := siteFake()
	out, err := scriptRun{client: f, stdin: "from a pipe"}.do(t,
		"issue", "create", "--type", "10301", "--summary", "Piped", "--description-file", "-", "--json")
	if err != nil {
		t.Fatal(err)
	}
	var created struct{ Key, ID, URL string }
	if err := json.Unmarshal([]byte(out), &created); err != nil || created.Key == "" || created.ID == "" || created.URL == "" {
		t.Fatalf("create --json printed %q (%v)", out, err)
	}
	iss := readBack(t, f, created.Key)
	if iss.Type.ID != "10301" || !strings.Contains(adf.Markdown(iss.Description), "from a pipe") {
		t.Errorf("created a %s with %q", iss.Type.ID, adf.Markdown(iss.Description))
	}
}

func TestIssueCreate_ASubtaskUnderItsParent(t *testing.T) {
	writeProfile(t)
	f := siteFake()
	out, err := script(t, f, "issue", "create", "--project", "PROJ", "--type", "Offshoot", "--summary", "Child", "--parent", "proj-1")
	if err != nil {
		t.Fatal(err)
	}
	key, _, _ := strings.Cut(out, "\t")
	if iss := readBack(t, f, key); iss.Parent == nil || iss.Parent.Key != "PROJ-1" {
		t.Errorf("the subtask hangs off %+v", iss.Parent)
	}
	_, err = script(t, f, "issue", "create", "--project", "PROJ", "--type", "Offshoot", "--summary", "Orphan")
	wantCode(t, err, exitOther, "parent")
}

type twinTypes struct{ *jiratest.Fake }

func (twinTypes) IssueTypeStatuses(context.Context, string) ([]jira.IssueTypeStatuses, error) {
	return []jira.IssueTypeStatuses{
		{Type: jira.IssueType{ID: "1", Name: "Task"}},
		{Type: jira.IssueType{ID: "2", Name: "task"}},
	}, nil
}

func TestIssueCreate_ATypeNameTwoTypesShare(t *testing.T) {
	writeProfile(t)
	_, err := script(t, twinTypes{siteFake()}, "issue", "create", "--project", "PROJ", "--type", "Task", "--summary", "x")
	wantCode(t, err, exitUsage, "1, 2")
}

func TestTransition_MovesByStatusOrByName(t *testing.T) {
	writeProfile(t)
	f := siteFake()
	moves, err := f.Transitions(context.Background(), "PROJ-2")
	if err != nil || len(moves) == 0 {
		t.Fatalf("no moves on PROJ-2 (%v)", err)
	}
	var plain jira.Transition
	for _, m := range moves {
		if m.To.Category != jira.CategoryDone {
			plain = m
			break
		}
	}
	out, err := script(t, f, "transition", "PROJ-2", strings.ToUpper(plain.To.Name))
	if err != nil {
		t.Fatal(err)
	}
	if out != "PROJ-2\t"+plain.To.Name+"\n" {
		t.Errorf("transition printed %q", out)
	}
	if got := readBack(t, f, "PROJ-2").Status; got.ID != plain.To.ID {
		t.Errorf("PROJ-2 is in %s, want %s", got.Name, plain.To.Name)
	}

	moves, err = f.Transitions(context.Background(), "PROJ-2")
	if err != nil {
		t.Fatal(err)
	}
	var next jira.Transition
	for _, m := range moves {
		if !m.HasScreen && m.To.ID != plain.To.ID {
			next = m
		}
	}
	if next.ID == "" {
		t.Fatalf("no second move without a screen: %+v", moves)
	}
	if _, err := script(t, f, append([]string{"transition", "PROJ-2"}, strings.Fields(next.Name)...)...); err != nil {
		t.Fatalf("an unquoted transition name did not move it: %v", err)
	}
	if got := readBack(t, f, "PROJ-2").Status; got.ID != next.To.ID {
		t.Errorf("PROJ-2 is in %s, want %s", got.Name, next.To.Name)
	}
}

func TestTransition_FillsTheScreen(t *testing.T) {
	writeProfile(t)
	f := siteFake()
	var done jira.Transition
	moves, _ := f.Transitions(context.Background(), "PROJ-1")
	for _, m := range moves {
		if m.HasScreen {
			done = m
		}
	}
	if done.ID == "" || len(done.Fields) == 0 || len(done.Fields[0].AllowedValues) == 0 {
		t.Fatalf("the fake has no move with a screen to fill: %+v", moves)
	}
	meta := done.Fields[0]
	choice := meta.AllowedValues[len(meta.AllowedValues)-1]

	_, err := script(t, f, "transition", "PROJ-1", done.To.Name)
	wantCode(t, err, exitUsage, meta.Name)
	_, err = script(t, f, "transition", "PROJ-1", done.To.Name, "--field", meta.Name+"=Maybe")
	wantCode(t, err, exitUsage, choice.Label)
	_, err = script(t, f, "transition", "PROJ-1", done.To.Name, "--field", "Colour=Red")
	wantCode(t, err, exitUsage, meta.Name)
	_, err = script(t, f, "transition", "PROJ-1", done.To.Name, "--field", "no-equals")
	wantCode(t, err, exitUsage, "Name=Value")
	if got := readBack(t, f, "PROJ-1").Status; got.ID == done.To.ID {
		t.Fatal("a refused screen still moved the issue")
	}

	if _, err := script(t, f, "transition", "PROJ-1", done.To.Name, "--field", strings.ToLower(meta.Name)+"="+strings.ToUpper(choice.Label)); err != nil {
		t.Fatal(err)
	}
	iss := readBack(t, f, "PROJ-1")
	if iss.Status.ID != done.To.ID || iss.Resolution == nil || iss.Resolution.ID != choice.ID {
		t.Errorf("PROJ-1 is %s resolved %+v, want %s resolved %s", iss.Status.Name, iss.Resolution, done.To.Name, choice.Label)
	}
}

type twinMoves struct{ *jiratest.Fake }

func (twinMoves) Transitions(context.Context, string) ([]jira.Transition, error) {
	to := jira.Status{ID: "9", Name: "Closed"}
	return []jira.Transition{{ID: "1", Name: "Close", To: to}, {ID: "2", Name: "Abandon", To: to}}, nil
}

func (twinMoves) Transition(_ context.Context, _, id string, _ jira.IssuePatch) error {
	if id != "2" {
		return errors.New("moved through " + id)
	}
	return nil
}

func TestTransition_TwoMovesToOneStatus(t *testing.T) {
	writeProfile(t)
	c := twinMoves{siteFake()}
	_, err := script(t, c, "transition", "PROJ-1", "closed")
	wantCode(t, err, exitUsage, `"Abandon"`)
	if _, err := script(t, c, "transition", "PROJ-1", "Abandon"); err != nil {
		t.Errorf("naming the transition did not pick it: %v", err)
	}
}

func TestComment_Adds(t *testing.T) {
	path := filepath.Join(t.TempDir(), "c.md")
	if err := os.WriteFile(path, []byte("from a *file*"), 0o600); err != nil {
		t.Fatal(err)
	}
	tests := map[string]struct {
		stdin string
		args  []string
		want  string
	}{
		"from -m":    {args: []string{"-m", "looks **fine**"}, want: "**fine**"},
		"from -file": {args: []string{"--file", path}, want: "*file*"},
		"from stdin": {stdin: "piped in", args: []string{"--file", "-"}, want: "piped in"},
	}
	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			writeProfile(t)
			f := siteFake()
			out, err := scriptRun{client: f, stdin: tc.stdin}.do(t, append([]string{"comment", "add", "PROJ-3"}, tc.args...)...)
			if err != nil {
				t.Fatal(err)
			}
			page, err := f.Comments(context.Background(), "PROJ-3")
			if err != nil || len(page.Items) == 0 {
				t.Fatalf("no comment on PROJ-3 (%v)", err)
			}
			last := page.Items[len(page.Items)-1]
			if out != "PROJ-3\t"+last.ID+"\n" {
				t.Errorf("printed %q, want the key and comment %s", out, last.ID)
			}
			if md := adf.Markdown(last.Body); !strings.Contains(md, tc.want) {
				t.Errorf("the comment went out as %q, want %q in it", md, tc.want)
			}
		})
	}
}

func TestAssign_ToEachKindOfWho(t *testing.T) {
	tests := map[string]struct {
		who     []string
		people  []jira.User
		want    string
		printed string
	}{
		"me":            {who: []string{"me"}, want: "acct-me", printed: "Sam Tester\tacct-me"},
		"nobody":        {who: []string{"none"}, want: "", printed: "unassigned"},
		"a name":        {who: []string{"grace"}, want: "acct-grace", printed: "Grace Hopper"},
		"an email":      {who: []string{"ada@example.invalid"}, want: "acct-ada", printed: "acct-ada"},
		"an account ID": {who: []string{"acct:nightly-bot"}, want: "acct:nightly-bot", printed: "Nightly Runner"},
		"an exact name among many": {
			who:  []string{"SAM"},
			want: "acct-sam", printed: "acct-sam",
			people: []jira.User{
				{AccountID: "acct-samuel", DisplayName: "Samuel Pepys", Active: true, Kind: jira.AccountPerson},
				{AccountID: "acct-sam", DisplayName: "Sam", Active: true, Kind: jira.AccountPerson},
			},
		},
	}
	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			writeProfile(t)
			f := siteFake()
			if tc.people != nil {
				f = siteFake(jiratest.WithPeople(tc.people))
			}
			out, err := script(t, f, append([]string{"assign", "PROJ-4"}, tc.who...)...)
			if err != nil {
				t.Fatal(err)
			}
			if !strings.HasPrefix(out, "PROJ-4\t") || !strings.Contains(out, tc.printed) {
				t.Errorf("printed %q, want %q in it", out, tc.printed)
			}
			iss := readBack(t, f, "PROJ-4")
			got := ""
			if iss.Assignee != nil {
				got = iss.Assignee.AccountID
			}
			if got != tc.want {
				t.Errorf("PROJ-4 is assigned to %q, want %q", got, tc.want)
			}
		})
	}
}

func TestAssign_WithoutBrowseUsers(t *testing.T) {
	writeProfile(t)
	f := siteFake(jiratest.WithCapabilities(jiratest.CapReason(jira.CapPeople, "needs Browse users and groups")))
	_, err := script(t, f, "assign", "PROJ-1", "grace")
	wantCode(t, err, exitOther, "Browse users")
	if _, err := script(t, f, "assign", "PROJ-1", "me"); err != nil {
		t.Errorf("assigning to me needs no directory, but failed: %v", err)
	}
}
