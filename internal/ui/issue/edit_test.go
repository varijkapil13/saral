package issue

import (
	"errors"
	"os"
	"slices"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/varijkapil13/saral/internal/ui/kernel"
	"github.com/varijkapil13/saral/pkg/adf"
	"github.com/varijkapil13/saral/pkg/jira"
	"github.com/varijkapil13/saral/pkg/jira/jiratest"
)

// openEditor puts the editor on screen with the issue as it stands, a draft
// directory of its own and a stand-in for the user's editor.
func openEditor(t *testing.T, client jira.Client, iss jira.Issue, w, h int, opts ...editOption) *panel {
	t.Helper()

	all := append([]editOption{withDrafts(tempDrafts(t))}, opts...)
	return newPanel(t, NewEdit(testDeps(client), iss, all...), w, h)
}

// fullIssue is the issue as the detail pane has it once its own read has landed.
func fullIssue(t *testing.T, f *jiratest.Fake, key string) jira.Issue {
	t.Helper()

	iss, err := f.Issue(t.Context(), key)
	if err != nil {
		t.Fatalf("Issue: %v", err)
	}
	return iss
}

// TestEdit_WritesOnlyTheFieldsTheIssueWasReadWith is the whole point of the
// packet. The row the list handed over was read with six fields; the editor
// offers five; the three outside the read are refused, and the write names the
// one field that changed and nothing else.
func TestEdit_WritesOnlyTheFieldsTheIssueWasReadWith(t *testing.T) {
	t.Parallel()

	f := newFake(4)
	client := record(f)
	seed := listSeed(t, f, "PROJ-1")
	// The re-read this pane would otherwise do is refused, so the mask stays
	// the narrow one the list produced.
	f.FailNext(&jira.CapabilityError{Reason: "you may not browse this project"})

	p := openEditor(t, client, seed, 100, 28)
	m := p.editor()
	for _, id := range []string{"description", "labels", "duedate"} {
		row := m.rowByID(id)
		if row == nil {
			t.Fatalf("no row for %s", id)
		}
		if row.fetched {
			t.Errorf("%s is offered for editing; the issue was not read with it", id)
		}
	}
	if !strings.Contains(p.frame(), "you may not browse this project") {
		t.Errorf("the pane does not say why the fields could not be read:\n%s", p.frame())
	}

	p.keys("enter")
	p.typed(" and again")
	p.keys("enter", "ctrl+s", "y")

	patch := client.lastPatch(t)
	if got := patchFieldNames(patch); !slices.Equal(got, []string{"summary"}) {
		t.Fatalf("the write named %v; every other name would empty a field nothing ever read", got)
	}
	if patch.Summary == nil || !strings.HasSuffix(*patch.Summary, "and again") {
		t.Errorf("Summary = %v, want the edited one", patch.Summary)
	}
}

func TestEdit_RefusesToChangeAFieldTheIssueWasNotReadWith(t *testing.T) {
	t.Parallel()

	f := newFake(4)
	seed := listSeed(t, f, "PROJ-1")
	f.FailNext(&jira.TransportError{Op: "GET /search/jql", Err: errors.New("no route to host")})
	p := openEditor(t, f, seed, 100, 28)

	p.keys("down", "down", "down") // labels
	if got := p.editor().row().id; got != "labels" {
		t.Fatalf("the cursor is on %s, want labels", got)
	}
	p.keys("enter")

	if p.editor().stage == stageTyping {
		t.Error("the pane opened a field the issue was never read with")
	}
	if !strings.Contains(p.lastStatus().Text, "Labels") {
		t.Errorf("status = %q, want it to name the field it refused", p.lastStatus().Text)
	}
}

func TestEdit_NamesEveryChangeBeforeItWritesOne(t *testing.T) {
	t.Parallel()

	f := newFake(4)
	client := record(f)
	p := openEditor(t, client, fullIssue(t, f, "PROJ-3"), 100, 30)

	p.keys("enter")
	p.typed("!")
	p.keys("enter")

	p.keys("ctrl+s")
	frame := p.frame()
	if !strings.Contains(frame, "Save these changes to PROJ-3?") {
		t.Fatalf("the confirmation does not say what it is about to do:\n%s", frame)
	}
	if !strings.Contains(frame, "y saves") {
		t.Errorf("the confirmation does not say how to answer it:\n%s", frame)
	}
	if client.writes() != 0 {
		t.Fatal("the write happened before the confirmation was answered")
	}

	// Anything that is not the confirmation is a refusal, and nothing is sent.
	p.keys("n")
	if client.writes() != 0 {
		t.Fatal("a key that is not the confirmation went ahead anyway")
	}
	if p.editor().stage != stageBrowse {
		t.Errorf("stage = %v, want the pane back where it was", p.editor().stage)
	}

	p.keys("ctrl+s", "y")
	if client.writes() != 1 {
		t.Fatalf("%d writes reached Jira, want 1", client.writes())
	}
	if p.pops != 1 {
		t.Errorf("the pane was popped %d times, want once", p.pops)
	}
	if len(p.broadcasts) == 0 {
		t.Error("nothing told the panes underneath that the issue changed")
	}
}

func TestEdit_KeepsEveryEditWhenJiraRefusesTheWrite(t *testing.T) {
	t.Parallel()

	f := newFake(4)
	drafts := tempDrafts(t)
	p := openEditor(t, f, fullIssue(t, f, "PROJ-3"), 100, 30, withDrafts(drafts))

	p.keys("enter")
	p.typed(" rewritten")
	p.keys("enter")

	f.FailNext(&jira.ValidationError{Fields: []jira.FieldError{{Field: "summary", Message: "Summary must be under 255 characters."}}})
	p.keys("ctrl+s", "y")

	m := p.editor()
	if !m.dirty() {
		t.Fatal("the refused write took the edit with it")
	}
	if got := m.rowByID("summary").problem; got != "Summary must be under 255 characters." {
		t.Errorf("the summary row carries %q, want Jira's own message on the field it named", got)
	}
	if !strings.Contains(p.frame(), "Summary must be under 255 characters.") {
		t.Errorf("the failure is not on screen:\n%s", p.frame())
	}
	kept, ok, err := drafts.load(m.deps.Site, "PROJ-3")
	if err != nil || !ok {
		t.Fatalf("the draft did not survive the refusal: ok=%v err=%v", ok, err)
	}
	if !strings.HasSuffix(kept.Values["summary"], " rewritten") {
		t.Errorf("the draft holds %q, want what was typed", kept.Values["summary"])
	}
}

func TestEdit_OffersToReReadAndReapplyAfterAConflict(t *testing.T) {
	t.Parallel()

	f := newFake(4)
	client := record(f)
	p := openEditor(t, client, fullIssue(t, f, "PROJ-3"), 100, 30)

	p.keys("enter")
	p.typed(" mine")
	p.keys("enter")

	f.FailNext(&jira.ConflictError{Resource: "issue PROJ-3", Detail: "someone else saved first"})
	p.keys("ctrl+s", "y")

	if p.editor().stage != stageConflict {
		t.Fatalf("stage = %v, want the pane offering reload-and-reapply", p.editor().stage)
	}
	if !strings.Contains(p.frame(), "still here") {
		t.Errorf("the pane does not say the text is safe:\n%s", p.frame())
	}

	p.keys("y")
	m := p.editor()
	if !m.dirty() {
		t.Fatal("re-reading the issue threw the edit away")
	}
	if got := m.rowByID("summary").value; !strings.HasSuffix(got, " mine") {
		t.Errorf("summary = %q, want the edit back on top of the re-read issue", got)
	}

	p.keys("ctrl+s", "y")
	if client.writes() != 1 {
		t.Fatalf("%d writes reached Jira after the conflict, want 1", client.writes())
	}
}

func TestEdit_HandsTheDescriptionToTheEditorAndKeepsWhatMarkdownCannotCarry(t *testing.T) {
	t.Parallel()

	f := newFake(4)
	iss := fullIssue(t, f, "PROJ-3")
	iss.Description = docWith("the first paragraph")
	client := record(f)

	rendered := adf.Markdown(iss.Description)
	edited := strings.Replace(rendered, "the first paragraph", "a different first paragraph", 1)
	p := openEditor(t, client, iss, 100, 30, withLauncher(scriptedEditor(t, edited, nil)))

	p.keys("down", "enter")
	row := p.editor().rowByID("description")
	if row.edited == nil {
		t.Fatal("nothing came back from the editor")
	}
	body, err := adf.Marshal(*row.edited)
	if err != nil {
		t.Fatalf("encoding what came back: %v", err)
	}
	if !strings.Contains(string(body), "acct-ada") {
		t.Fatalf("the mention lost its account id, so the edit went through ParseMarkdown rather than ParseMarkdownInto:\n%s", body)
	}
	if !strings.Contains(string(body), "a different first paragraph") {
		t.Errorf("the edit did not land:\n%s", body)
	}

	p.keys("ctrl+s", "y")
	patch := client.lastPatch(t)
	if got := patchFieldNames(patch); !slices.Equal(got, []string{"description"}) {
		t.Errorf("the write named %v, want the description alone", got)
	}
}

func TestEdit_WarnsWhatEditingTheDescriptionCosts(t *testing.T) {
	t.Parallel()

	f := newFake(4)
	iss := fullIssue(t, f, "PROJ-3")
	iss.Description = docWith("the first paragraph")

	rendered := adf.Markdown(iss.Description)
	edited := strings.Replace(rendered, "the first paragraph", "changed", 1)
	p := openEditor(t, f, iss, 100, 34, withLauncher(scriptedEditor(t, edited, nil)))

	p.keys("down", "enter", "ctrl+s")
	frame := p.frame()
	if !strings.Contains(frame, "mention") {
		t.Errorf("the confirmation does not say what an edited block loses:\n%s", frame)
	}
}

func TestEdit_TreatsAnEditorThatStoppedAsACancel(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name     string
		launcher func(t *testing.T, rendered string) editorLauncher
		want     string
	}{
		{
			name: "the editor exited without saving",
			launcher: func(t *testing.T, _ string) editorLauncher {
				return scriptedEditor(t, "", errors.New("exit status 1"))
			},
			want: "stopped without saving",
		},
		{
			name: "the file came back exactly as it went out",
			launcher: func(t *testing.T, rendered string) editorLauncher {
				return scriptedEditor(t, rendered, nil)
			},
			want: "unchanged",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			f := newFake(4)
			iss := fullIssue(t, f, "PROJ-3")
			iss.Description = docWith("the first paragraph")
			client := record(f)

			p := openEditor(t, client, iss, 100, 30, withLauncher(tc.launcher(t, adf.Markdown(iss.Description))))
			p.keys("down", "enter")

			m := p.editor()
			if m.rowByID("description").dirty() {
				t.Fatal("a cancelled handoff still changed the description")
			}
			if m.dirty() {
				t.Error("the pane thinks something changed")
			}
			if !strings.Contains(p.frame(), tc.want) {
				t.Errorf("the pane does not say %q happened:\n%s", tc.want, p.frame())
			}

			p.keys("ctrl+s")
			if client.writes() != 0 {
				t.Error("a cancelled edit was written to Jira")
			}
		})
	}
}

func TestEdit_SaysWhichLineOfMarkdownItCannotTurnIntoADocument(t *testing.T) {
	t.Parallel()

	f := newFake(4)
	iss := fullIssue(t, f, "PROJ-3")
	iss.Description = docWith("the first paragraph")

	// ADF has no table inside a quote, so this is markdown that cannot become a
	// document rather than markdown this package merely does not understand.
	broken := "> | a | b |\n> | --- | --- |\n> | 1 | 2 |\n"
	p := openEditor(t, f, iss, 100, 30, withLauncher(scriptedEditor(t, broken, nil)))
	p.keys("down", "enter")

	if !strings.Contains(p.statusText(), "line ") {
		t.Errorf("the failure does not say which line it stopped on: %q", p.statusText())
	}
	if p.editor().rowByID("description").dirty() {
		t.Error("markdown that would not parse was applied anyway")
	}
}

// TestEdit_LeavesTheAuthorsTextOnDiskWhenItCannotUseIt is the other half of
// never losing what somebody typed: a handoff that ends in markdown this
// package will not turn into a document says where the file is rather than
// deleting it.
func TestEdit_LeavesTheAuthorsTextOnDiskWhenItCannotUseIt(t *testing.T) {
	t.Parallel()

	f := newFake(4)
	iss := fullIssue(t, f, "PROJ-3")
	iss.Description = docWith("the first paragraph")

	var handed string
	broken := "> | a | b |\n> | --- | --- |\n> | 1 | 2 |\n"
	launcher := func(path string, done func(error) tea.Msg) tea.Cmd {
		handed = path
		return scriptedEditor(t, broken, nil)(path, done)
	}
	p := openEditor(t, f, iss, 100, 30, withLauncher(launcher))
	p.keys("down", "enter")

	if handed == "" {
		t.Fatal("the editor was never handed a file")
	}
	if _, err := os.Stat(handed); err != nil {
		t.Fatalf("the author's text was deleted: %v", err)
	}
	if !strings.Contains(p.statusText(), handed) {
		t.Errorf("the failure does not say where the text is: %q", p.statusText())
	}
	t.Cleanup(func() { _ = os.Remove(handed) })
}

func TestEdit_PicksUpAnUnsavedChangeFromLastTime(t *testing.T) {
	t.Parallel()

	f := newFake(4)
	drafts := tempDrafts(t)
	iss := fullIssue(t, f, "PROJ-3")

	first := openEditor(t, f, iss, 100, 30, withDrafts(drafts))
	first.keys("enter")
	first.typed(" half typed")
	first.keys("enter")

	second := openEditor(t, f, iss, 100, 30, withDrafts(drafts))
	m := second.editor()
	if !m.dirty() {
		t.Fatal("the unsaved change did not come back")
	}
	if got := m.rowByID("summary").value; !strings.HasSuffix(got, " half typed") {
		t.Errorf("summary = %q, want what was typed last time", got)
	}
	if !strings.Contains(second.frame(), "unsaved changes from last time") {
		t.Errorf("the pane does not say where the text came from:\n%s", second.frame())
	}
}

func TestEdit_RefusesToBeClosedWithAnUnsavedChangeAndThrowsItAwayOnlyWhenAsked(t *testing.T) {
	t.Parallel()

	f := newFake(4)
	drafts := tempDrafts(t)
	p := openEditor(t, f, fullIssue(t, f, "PROJ-3"), 100, 30, withDrafts(drafts))

	p.keys("enter")
	p.typed("!")
	p.keys("enter")

	reason, blocked := p.editor().BlocksClose()
	if !blocked {
		t.Fatal("the pane would let itself be closed over an unsaved change")
	}
	if !strings.Contains(reason, "ctrl+s") || !strings.Contains(reason, "X") {
		t.Errorf("reason = %q, want it to name both ways out", reason)
	}

	p.keys("X")
	if _, blocked := p.editor().BlocksClose(); blocked {
		t.Error("the pane still refuses to close after the change was thrown away")
	}
	if p.pops != 1 {
		t.Errorf("the pane was popped %d times, want once", p.pops)
	}
	if _, ok, _ := drafts.load(p.editor().deps.Site, "PROJ-3"); ok {
		t.Error("a change that was thrown away is still on disk")
	}
}

func TestEdit_OffersThePrioritiesTheSiteAllowsAndWritesTheirIds(t *testing.T) {
	t.Parallel()

	f := newFake(4)
	client := record(f)
	p := openEditor(t, client, fullIssue(t, f, "PROJ-3"), 100, 30)

	row := p.editor().rowByID("priority")
	if len(row.options) < 2 {
		t.Fatalf("the priority row offers %d values; they come from the create screen", len(row.options))
	}
	for _, option := range row.options[1:] {
		if option.ID == "" {
			t.Errorf("the picker offers %q with no id, and a name is what a localised site translates", option.Label)
		}
	}

	p.keys("down", "down", "right")
	p.keys("ctrl+s", "y")

	patch := client.lastPatch(t)
	if got := patchFieldNames(patch); !slices.Equal(got, []string{"priority"}) {
		t.Fatalf("the write named %v, want the priority alone", got)
	}
	if patch.PriorityID == nil {
		t.Fatal("the priority was not written")
	}
	if !slices.ContainsFunc(row.options, func(o jira.Option) bool { return o.ID == *patch.PriorityID }) {
		t.Errorf("PriorityID = %q, which is not one of the values the site offered", *patch.PriorityID)
	}
}

// TestEdit_ClickingARowPicksItAndClickingItAgainChangesIt keeps the third way
// in working: docs/UX.md asks for every action to be reachable by key, by the
// palette and by the mouse.
func TestEdit_ClickingARowPicksItAndClickingItAgainChangesIt(t *testing.T) {
	t.Parallel()

	f := newFake(8)
	d := testDeps(f)
	p := newPanel(t, NewEdit(d, fullIssue(t, f, "PROJ-6"), withDrafts(tempDrafts(t))), 100, 28)

	at := p.zoneAt(d, "row:labels")
	p.clickAt(at)
	if got := p.editor().row().id; got != "labels" {
		t.Fatalf("the click put the cursor on %s, want labels", got)
	}

	p.clickAt(at)
	if p.editor().stage != stageTyping {
		t.Error("a second click on the row under the cursor did not open it")
	}
}

func TestEdit_ReportsEveryWayASaveCanFail(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name string
		err  error
		want string
	}{
		{"a refusal", &jira.CapabilityError{Reason: "you need Edit Issues in this project"}, "Edit Issues"},
		{"a rate limit", &jira.RateLimitError{RetryAfter: 30 * time.Second}, "retry in 30s"},
		{"a transport failure", &jira.TransportError{Op: "PUT /issue", Err: errors.New("connection reset")}, "connection reset"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			f := newFake(4)
			p := openEditor(t, f, fullIssue(t, f, "PROJ-3"), 100, 30)
			p.keys("enter")
			p.typed("!")
			p.keys("enter")

			f.FailNext(tc.err)
			p.keys("ctrl+s", "y")

			if !strings.Contains(p.statusText(), tc.want) {
				t.Errorf("status = %q, want the error's own wording (%q)", p.statusText(), tc.want)
			}
			if !p.editor().dirty() {
				t.Error("the failed write took the edit with it")
			}
		})
	}
}

func TestEdit_SendsNothingWhenNothingChanged(t *testing.T) {
	t.Parallel()

	f := newFake(4)
	client := record(f)
	p := openEditor(t, client, fullIssue(t, f, "PROJ-3"), 100, 30)

	p.keys("ctrl+s")
	if p.editor().stage == stageConfirm {
		t.Fatal("the pane asked to confirm a write with nothing in it")
	}
	if client.writes() != 0 {
		t.Error("an empty patch was sent")
	}
	if !strings.Contains(p.lastStatus().Text, "nothing has changed") {
		t.Errorf("status = %q, want it to say why", p.lastStatus().Text)
	}
}

func TestEdit_RefusesADateItCannotSend(t *testing.T) {
	t.Parallel()

	f := newFake(4)
	client := record(f)
	p := openEditor(t, client, fullIssue(t, f, "PROJ-3"), 100, 30)

	p.keys("down", "down", "down", "down", "enter")
	p.typed("next tuesday")
	p.keys("enter", "ctrl+s")

	if client.writes() != 0 {
		t.Fatal("a date Jira cannot read was sent anyway")
	}
	if !strings.Contains(p.statusText(), "2006-01-02") {
		t.Errorf("status = %q, want it to say what a date looks like", p.statusText())
	}
}

func TestEdit_EmptiesAFieldThroughClearRatherThanAnEmptyValue(t *testing.T) {
	t.Parallel()

	f := newFake(8)
	client := record(f)
	p := openEditor(t, client, fullIssue(t, f, "PROJ-6"), 100, 30)

	p.keys("down", "down", "down", "down", "delete")
	p.keys("ctrl+s", "y")

	patch := client.lastPatch(t)
	if len(patch.Clear) != 1 || patch.Clear[0].ID != "duedate" {
		t.Fatalf("Clear = %v, want the due date named", patch.Clear)
	}
	if patch.Due != nil {
		t.Error("the due date was also sent as a value; emptying it is Clear's job")
	}
}

func TestEdit_LeavesTheIssueAloneWhenTheKeyboardNeverReachedIt(t *testing.T) {
	t.Parallel()

	f := newFake(4)
	client := record(f)
	before := fullIssue(t, f, "PROJ-3")
	p := openEditor(t, client, before, 100, 30, withLauncher(scriptedEditor(t, "", errors.New("exit status 1"))))

	p.keys("down", "enter", "esc")
	if client.writes() != 0 {
		t.Fatal("something was written")
	}
	after := fullIssue(t, f, "PROJ-3")
	if after.Summary != before.Summary || !after.Updated.Equal(before.Updated) {
		t.Errorf("the issue changed: %q at %v, was %q at %v", after.Summary, after.Updated, before.Summary, before.Updated)
	}
}

func TestEditor_ResolvesTheEditorTheWayEveryOtherProgramDoes(t *testing.T) {
	t.Setenv("VISUAL", "")
	t.Setenv("EDITOR", "")
	if _, _, err := editorCommand(); err != nil && !strings.Contains(err.Error(), "set $EDITOR") {
		t.Errorf("with nothing set the failure reads %q, want it to say what to set", err)
	}

	t.Setenv("EDITOR", "definitely-not-an-editor-on-this-machine")
	_, _, err := editorCommand()
	if err == nil || !strings.Contains(err.Error(), "$EDITOR") {
		t.Errorf("got %v, want a failure naming the variable that pointed at nothing", err)
	}

	t.Setenv("VISUAL", "go run")
	name, args, err := editorCommand()
	if err != nil {
		t.Fatalf("resolving a two-word $VISUAL: %v", err)
	}
	if name != "go" || !slices.Equal(args, []string{"run"}) {
		t.Errorf("got %q %v, want the arguments kept: an editor is a command line, not a program name", name, args)
	}
}

func TestDrafts_SurviveARoundTripAndAreRemovedWhenAsked(t *testing.T) {
	t.Parallel()

	store := draftStore{dir: t.TempDir()}
	kept := draft{
		Key:     "PROJ-1",
		Site:    "example.atlassian.net",
		SavedAt: time.Date(2026, time.March, 2, 9, 0, 0, 0, time.UTC),
		Values:  map[string]string{"summary": "half a thought"},
	}
	if err := store.save(kept); err != nil {
		t.Fatalf("save: %v", err)
	}
	got, ok, err := store.load(kept.Site, kept.Key)
	if err != nil || !ok {
		t.Fatalf("load: ok=%v err=%v", ok, err)
	}
	if got.Values["summary"] != "half a thought" {
		t.Errorf("got %q, want what was written", got.Values["summary"])
	}

	info, err := os.Stat(store.path(kept.Site, kept.Key))
	if err != nil {
		t.Fatalf("stat: %v", err)
	}
	if perm := info.Mode().Perm(); perm != 0o600 {
		t.Errorf("mode = %v, want a file only its owner can read", perm)
	}

	if err := store.discard(kept.Site, kept.Key); err != nil {
		t.Fatalf("discard: %v", err)
	}
	if _, ok, _ := store.load(kept.Site, kept.Key); ok {
		t.Error("the draft is still there")
	}
	if err := store.discard(kept.Site, kept.Key); err != nil {
		t.Errorf("discarding a draft that is already gone reported %v", err)
	}
}

func TestDrafts_KeepTwoSitesApart(t *testing.T) {
	t.Parallel()

	store := draftStore{dir: t.TempDir()}
	for _, site := range []string{"one.atlassian.net", "two.atlassian.net"} {
		if err := store.save(draft{Key: "PROJ-1", Site: site, Values: map[string]string{"summary": site}}); err != nil {
			t.Fatalf("save: %v", err)
		}
	}
	for _, site := range []string{"one.atlassian.net", "two.atlassian.net"} {
		got, ok, err := store.load(site, "PROJ-1")
		if err != nil || !ok {
			t.Fatalf("load %s: ok=%v err=%v", site, ok, err)
		}
		if got.Values["summary"] != site {
			t.Errorf("%s holds %q; one profile overwrote the other's draft", site, got.Values["summary"])
		}
	}
}

func TestDetail_OpensTheEditorAndThePickerFromAKeyAndFromThePalette(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name string
		fire func(p *panel)
		want string
	}{
		{"the edit key", func(p *panel) { p.keys("e") }, EditViewID},
		{"the move key", func(p *panel) { p.keys("t") }, MoveViewID},
		{"the palette's edit command", func(p *panel) { p.send(EditIssueMsg{}) }, EditViewID},
		{"the palette's move command", func(p *panel) { p.send(MoveIssueMsg{}) }, MoveViewID},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			f := newFake(4)
			p := newPanel(t, New(testDeps(f), seedOf(t, f, "PROJ-1")), 100, 30)
			tc.fire(p)

			if len(p.pushes) != 1 {
				t.Fatalf("%d panes were pushed, want 1", len(p.pushes))
			}
			if p.pushes[0].ID != tc.want {
				t.Errorf("pushed %q, want %q", p.pushes[0].ID, tc.want)
			}
			if p.pushes[0].Title != "PROJ-1" {
				t.Errorf("the pane is titled %q, want the issue it is about", p.pushes[0].Title)
			}
		})
	}
}

// TestRegistration_PutsBothPanesAndBothCommandsWhereTheFooterAndThePaletteLook
// is the other half of adding a gesture: a key nothing advertises is a key
// nobody finds.
func TestRegistration_PutsBothPanesAndBothCommandsWhereTheFooterAndThePaletteLook(t *testing.T) {
	t.Parallel()

	if errs := kernel.RegistrationErrors(); len(errs) > 0 {
		t.Fatalf("registration went wrong: %v", errs)
	}
	for _, scope := range []string{EditViewID, MoveViewID} {
		if kernel.KeysFor(scope).IsZero() {
			t.Errorf("%s registered no keys, so the help overlay has nothing to show", scope)
		}
	}

	commands := kernel.Commands()
	registered := make([]string, 0, len(commands))
	for _, cmd := range commands {
		registered = append(registered, cmd.ID)
	}
	for _, want := range []string{"issue.edit", "issue.transition"} {
		if !slices.Contains(registered, want) {
			t.Errorf("the palette has no %s; every action is reachable three ways", want)
		}
	}

	shown := kernel.KeysFor(ViewID)
	var advertised []string
	for _, row := range shown.Full {
		for _, binding := range row {
			advertised = append(advertised, binding.Help().Key)
		}
	}
	for _, want := range []string{"e", "t"} {
		if !slices.Contains(advertised, want) {
			t.Errorf("the detail pane does not advertise %q; the footer only shows keys that work", want)
		}
	}
}

// TestDetail_KeepsItsOwnGestureOnG proves the two new keys did not take one the
// detail pane already spends: g then e is still "go to the end".
func TestDetail_KeepsItsOwnGestureOnG(t *testing.T) {
	t.Parallel()

	f := newFake(4)
	p := newPanel(t, New(testDeps(f), seedOf(t, f, "PROJ-1")), 100, 12)

	p.keys("g", "e")
	if len(p.pushes) != 0 {
		t.Error("g then e opened the editor; it is the gesture that goes to the end of the issue")
	}
}
