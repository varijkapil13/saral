package form

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/varijkapil13/saral/internal/ui/kernel"
	"github.com/varijkapil13/saral/pkg/jira"
)

// fresh builds a form the way the kernel does every time it is pushed: a new
// Model, sharing nothing in memory with the last one.
func fresh(t *testing.T, d kernel.Deps, issueType string) *driver {
	t.Helper()

	dr := &driver{t: t, m: newWith(d, newSchemaCache(schemaTTL, time.Now))}
	dr.send(kernel.SizeMsg{Width: 100, Height: 24})
	dr.send(kernel.FocusMsg{Focused: true})
	dr.run(dr.m.Init())
	dr.send(CreateMsg{IssueTypeID: issueType})
	if dr.m.stage != stageFields {
		t.Fatalf("the form is still picking an issue type; it noted %q", dr.m.note)
	}
	return dr
}

func draftFile(d kernel.Deps, issueType string) string {
	return filepath.Join(d.DraftsDir, "create", safeName(d.Site), "PROJ."+issueType+".json")
}

func (d *driver) fill(id, text string) {
	d.t.Helper()

	d.focus(id)
	d.key("enter")
	d.typeText(text)
	d.key("enter")
}

func TestDraft_SurvivesTheFormBeingBuiltAgain(t *testing.T) {
	t.Parallel()

	c := newFake(20)
	d := testDeps(t, c)

	first := fresh(t, d, fakeStory)
	first.fill("summary", "Half a thought")
	first.focus("priority")
	first.key("enter", "down", "enter")
	chosen := first.field("priority").picked

	info, err := os.Stat(draftFile(d, fakeStory))
	if err != nil {
		t.Fatalf("committing a field wrote no draft: %v", err)
	}
	if perm := info.Mode().Perm(); perm != 0o600 {
		t.Errorf("the draft is readable as %o, want 0600: it is text nobody else has a copy of", perm)
	}
	dir, err := os.Stat(filepath.Dir(draftFile(d, fakeStory)))
	if err != nil {
		t.Fatal(err)
	}
	if perm := dir.Mode().Perm(); perm != 0o700 {
		t.Errorf("the draft directory is %o, want 0700", perm)
	}

	second := fresh(t, d, fakeStory)
	if got := second.field("summary").text; got != "Half a thought" {
		t.Errorf("the summary reads %q, want what was typed before the form was rebuilt", got)
	}
	if got := second.field("priority").picked; len(got) != 1 || got[0].ID != chosen[0].ID {
		t.Errorf("the priority is %+v, want %+v back by id", got, chosen)
	}
	mustContain(t, second.view(), "Half a thought", "put back")
}

func TestDraft_IsKeptPerIssueType(t *testing.T) {
	t.Parallel()

	c := newFake(20)
	d := testDeps(t, c)

	story := fresh(t, d, fakeStory)
	story.fill("summary", "A story")

	sub := fresh(t, d, fakeSubtask)
	if got := sub.field("summary").text; got != "" {
		t.Errorf("a subtask opened with %q, which was typed into a story", got)
	}
	if _, err := os.Stat(draftFile(d, fakeSubtask)); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("opening a subtask wrote a draft for it: %v", err)
	}

	again := fresh(t, d, fakeStory)
	if got := again.field("summary").text; got != "A story" {
		t.Errorf("the story's draft reads %q after a subtask was opened", got)
	}
}

func TestDraft_IsKeptPerSite(t *testing.T) {
	t.Parallel()

	c := newFake(20)
	d := testDeps(t, c)
	fresh(t, d, fakeStory).fill("summary", "On one site")

	other := d
	other.Site = "other.example.invalid"
	if got := fresh(t, other, fakeStory).field("summary").text; got != "" {
		t.Errorf("another site's form opened with %q", got)
	}
}

func TestDraft_IsThrownAwayOnceTheIssueExists(t *testing.T) {
	t.Parallel()

	c := newFake(20)
	d := testDeps(t, c)
	dr := fresh(t, d, fakeStory)
	dr.fill("summary", "Something real")
	dr.submitRow()
	dr.key("enter")

	if dr.pops != 1 {
		t.Fatalf("the form popped %d times, want once after the create", dr.pops)
	}
	if _, err := os.Stat(draftFile(d, fakeStory)); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("the draft outlived the issue it created: %v", err)
	}
	if got := fresh(t, d, fakeStory).field("summary").text; got != "" {
		t.Errorf("a form opened after the create reads %q", got)
	}
}

func TestDraft_OutlivesACreateJiraDidNotTake(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		err  error
	}{
		{"a project this token may not create in", &jira.CapabilityError{Reason: "You do not have permission to create issues in this project."}},
		{"a site that is rate limiting", &jira.RateLimitError{RetryAfter: 30 * time.Second}},
		{"a site that could not be reached", &jira.TransportError{Op: "POST issue", Err: errors.New("connection refused")}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			c := newFake(20)
			d := testDeps(t, c)
			dr := fresh(t, d, fakeStory)
			dr.fill("summary", "Not lost")
			c.FailNext(tt.err)
			dr.submitRow()
			dr.key("enter")

			if dr.pops != 0 {
				t.Fatal("the form closed although nothing was created")
			}
			if got := dr.lastStatus(); got.Level != kernel.LevelError {
				t.Errorf("the status line says %+v, want the failure", got)
			}
			if got := fresh(t, d, fakeStory).field("summary").text; got != "Not lost" {
				t.Errorf("after a refused create the draft reads %q", got)
			}
		})
	}
}

func TestDraft_ClosingWithSomethingTypedAsks(t *testing.T) {
	t.Parallel()

	dr := fresh(t, testDeps(t, newFake(20)), fakeStory)
	if _, blocked := dr.m.BlocksClose(); blocked {
		t.Error("an empty form refuses to close")
	}
	dr.run(dr.m.AskClose())
	if dr.pops != 1 || dr.m.leaving {
		t.Fatalf("an empty form asked before closing (pops %d, leaving %v)", dr.pops, dr.m.leaving)
	}

	dr = fresh(t, testDeps(t, newFake(20)), fakeStory)
	dr.fill("summary", "Typed and not created")
	reason, blocked := dr.m.BlocksClose()
	if !blocked || reason == "" {
		t.Fatal("the form would be thrown away holding an issue nobody created")
	}
	dr.run(dr.m.AskClose())
	if dr.pops != 0 || !dr.m.leaving {
		t.Fatalf("closing did not ask (pops %d, leaving %v)", dr.pops, dr.m.leaving)
	}
	if !dr.m.WantsRawKeys() {
		t.Error("the prompt does not take esc, so the kernel would pop instead of staying")
	}
	mustContain(t, dr.view(), "before leaving?", "y create", "n discard", "k keep for later", "esc stay")

	dr.key("esc")
	if dr.m.leaving || dr.pops != 0 {
		t.Errorf("esc did not stay (pops %d, leaving %v)", dr.pops, dr.m.leaving)
	}
	if got := dr.field("summary").text; got != "Typed and not created" {
		t.Errorf("staying cost the summary: %q", got)
	}
}

func TestDraft_LeavePromptAnswers(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		stroke   string
		kept     bool
		creates  bool
		wantPops int
	}{
		{name: "n throws it away", stroke: "n", wantPops: 1},
		{name: "k keeps it for next time", stroke: "k", kept: true, wantPops: 1},
		{name: "y creates it first", stroke: "y", creates: true, wantPops: 1},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			c := newFake(20)
			d := testDeps(t, c)
			dr := fresh(t, d, fakeStory)
			dr.fill("summary", "Decide")
			dr.run(dr.m.AskClose())
			dr.key(tt.stroke)

			if dr.pops != tt.wantPops {
				t.Errorf("the form popped %d times, want %d", dr.pops, tt.wantPops)
			}
			if _, blocked := dr.m.BlocksClose(); blocked {
				t.Error("the form still blocks after the prompt was answered, so the pop it sent is refused")
			}
			_, err := os.Stat(draftFile(d, fakeStory))
			if kept := err == nil; kept != tt.kept {
				t.Errorf("the draft is on disk: %v, want %v", kept, tt.kept)
			}
			created := false
			for _, call := range c.Calls() {
				created = created || call == "CreateIssue"
			}
			if created != tt.creates {
				t.Errorf("an issue was created: %v, want %v", created, tt.creates)
			}
		})
	}
}

func TestDraft_LeavePromptStaysWhenTheCreateFails(t *testing.T) {
	t.Parallel()

	c := newFake(20)
	d := testDeps(t, c)
	dr := fresh(t, d, fakeStory)
	dr.fill("summary", "Refused")
	dr.run(dr.m.AskClose())
	c.FailNext(&jira.RateLimitError{RetryAfter: 5 * time.Second})
	dr.key("y")

	if dr.pops != 0 {
		t.Error("the form closed although the create was refused")
	}
	if dr.m.leaving {
		t.Error("the prompt is still up over a refused create")
	}
	if _, err := os.Stat(draftFile(d, fakeStory)); err != nil {
		t.Errorf("the refused create lost the draft: %v", err)
	}
}

func TestDraft_LeavePromptAnswersAClick(t *testing.T) {
	t.Parallel()

	d := testDeps(t, newFake(20))
	dr := fresh(t, d, fakeStory)
	dr.fill("summary", "Clicked away")
	dr.run(dr.m.AskClose())

	dr.send(clickIn(t, d, dr.m, dr.m.View, zoneLeaveDiscard))
	if dr.pops != 1 {
		t.Errorf("clicking discard popped %d times, want once", dr.pops)
	}
	if _, err := os.Stat(draftFile(d, fakeStory)); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("clicking discard left the draft: %v", err)
	}
}

func TestDraft_AskingWhileAnEditorIsOpenKeepsWhatIsInIt(t *testing.T) {
	t.Parallel()

	d := testDeps(t, newFake(20))
	dr := fresh(t, d, fakeStory)
	dr.focus("summary")
	dr.key("enter")
	dr.typeText("still typing")
	dr.run(dr.m.AskClose())

	if !dr.m.leaving || dr.m.edit != editNone {
		t.Fatalf("closing over an open editor did not ask (leaving %v, editor %v)", dr.m.leaving, dr.m.edit)
	}
	if got := fresh(t, d, fakeStory).field("summary").text; got != "still typing" {
		t.Errorf("the text in the open editor was not kept: %q", got)
	}
}

func TestDraft_RefusesToCloseWhileTheCreateIsInFlight(t *testing.T) {
	t.Parallel()

	dr := fresh(t, testDeps(t, newFake(20)), fakeStory)
	dr.m.busy = true
	if reason, blocked := dr.m.BlocksClose(); !blocked || !strings.Contains(reason, "create") {
		t.Errorf("BlocksClose = %q, %v while Jira is answering a create", reason, blocked)
	}
	dr.run(dr.m.AskClose())
	if dr.pops != 0 || dr.m.leaving {
		t.Error("the form closed or asked while the create was in flight")
	}
	if got := dr.lastStatus(); got.Level != kernel.LevelWarn {
		t.Errorf("the status line says %+v, want the reason it stays", got)
	}
}

func TestDraft_ACorruptOrUnreadableDraftIsSaidAndOverwritten(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name  string
		plant func(t *testing.T, path string)
	}{
		{"not JSON", func(t *testing.T, path string) {
			t.Helper()
			if err := os.WriteFile(path, []byte("{half"), 0o600); err != nil {
				t.Fatal(err)
			}
		}},
		{"not a file", func(t *testing.T, path string) {
			t.Helper()
			if err := os.Mkdir(path, 0o700); err != nil {
				t.Fatal(err)
			}
		}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			d := testDeps(t, newFake(20))
			path := draftFile(d, fakeStory)
			if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
				t.Fatal(err)
			}
			tt.plant(t, path)

			dr := fresh(t, d, fakeStory)
			if got := dr.lastStatus(); got.Level != kernel.LevelWarn || !strings.Contains(got.Text, "draft") {
				t.Errorf("the status line says %+v, want the draft that could not be read named", got)
			}
			if got := dr.field("summary").text; got != "" {
				t.Errorf("the summary reads %q from a draft that could not be read", got)
			}
			dr.fill("summary", "Fresh")
			if tt.name == "not a file" {
				if got := dr.lastStatus(); got.Level != kernel.LevelWarn {
					t.Errorf("writing over a directory said %+v, want a warning", got)
				}
				return
			}
			if got := fresh(t, d, fakeStory).field("summary").text; got != "Fresh" {
				t.Errorf("the corrupt draft was not replaced: %q", got)
			}
		})
	}
}

func TestDraftStore_KeepsOnlyTheFieldsSomethingWasPutIn(t *testing.T) {
	t.Parallel()

	store := newDraftStore(t.TempDir())
	key := draftKey{site: "example.atlassian.net", project: "PROJ", issueType: "10001"}

	filled := newField(meta("summary", "Summary", jira.FieldSchema{Type: "string"}), time.UTC)
	filled.text = "half a thought"
	empty := newField(meta("duedate", "Due", jira.FieldSchema{Type: "date"}), time.UTC)
	chosen := newField(meta("cascade", "Where", jira.FieldSchema{Type: "option-with-child"}, option("1", "One")), time.UTC)
	chosen.picked = []jira.Option{{ID: "1", Label: "One", Children: []jira.Option{{ID: "2", Label: "Two"}}}}
	at := time.Date(2026, time.March, 5, 9, 0, 0, 0, time.UTC)

	if err := store.save(key, draftOf(key, []*field{filled, empty, chosen}, at)); err != nil {
		t.Fatal(err)
	}
	kept, ok, err := store.load(key)
	if err != nil || !ok {
		t.Fatalf("load = %v, %v", ok, err)
	}
	if len(kept.Values) != 2 {
		t.Fatalf("the draft holds %d fields, want only the two filled in: %+v", len(kept.Values), kept.Values)
	}
	if kept.Values["summary"].Text != "half a thought" {
		t.Errorf("the summary reads %q", kept.Values["summary"].Text)
	}
	back := fromDraftOptions(kept.Values["cascade"].Picked)
	if len(back) != 1 || len(back[0].Children) != 1 || back[0].Children[0].ID != "2" {
		t.Errorf("the cascade came back as %+v", back)
	}
	if !kept.SavedAt.Equal(at) || kept.Site != key.site || kept.IssueType != key.issueType {
		t.Errorf("the draft's own record reads %+v", kept)
	}

	if err := store.save(key, draftOf(key, []*field{empty}, at)); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(store.path(key)); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("a form emptied of everything left its draft file: %v", err)
	}
}

func TestDraftStore_KeepsNothingWithNowhereToWrite(t *testing.T) {
	t.Parallel()

	store := newDraftStore("")
	key := draftKey{site: "s", project: "PROJ", issueType: "1"}
	f := newField(meta("summary", "Summary", jira.FieldSchema{Type: "string"}), time.UTC)
	f.text = "x"
	if err := store.save(key, draftOf(key, []*field{f}, time.Time{})); err != nil {
		t.Errorf("save = %v", err)
	}
	if _, ok, err := store.load(key); ok || err != nil {
		t.Errorf("load = %v, %v from a store with no directory", ok, err)
	}
	if err := store.discard(key); err != nil {
		t.Errorf("discard = %v", err)
	}
}

func TestDraftStore_NeverLeavesItsOwnDirectory(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	store := newDraftStore(root)
	path := store.path(draftKey{site: "../..", project: "../x", issueType: "a/b"})
	rel, err := filepath.Rel(filepath.Join(root, "create"), path)
	if err != nil || strings.HasPrefix(rel, "..") || strings.Count(rel, string(filepath.Separator)) != 1 {
		t.Errorf("a hostile key put the draft at %s", path)
	}
}

func TestDraftStore_WritesJSONAnotherBuildCanRead(t *testing.T) {
	t.Parallel()

	store := newDraftStore(t.TempDir())
	key := draftKey{site: "s", project: "PROJ", issueType: "1"}
	f := newField(meta("summary", "Summary", jira.FieldSchema{Type: "string"}), time.UTC)
	f.text = "portable"
	if err := store.save(key, draftOf(key, []*field{f}, time.Time{})); err != nil {
		t.Fatal(err)
	}
	body, err := os.ReadFile(store.path(key))
	if err != nil {
		t.Fatal(err)
	}
	var raw map[string]any
	if err := json.Unmarshal(body, &raw); err != nil {
		t.Fatalf("the draft is not JSON: %v", err)
	}
	for _, name := range []string{"site", "project", "issueType", "savedAt", "values"} {
		if _, ok := raw[name]; !ok {
			t.Errorf("the draft has no %q: %s", name, body)
		}
	}
}
