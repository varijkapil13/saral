package issue

import (
	"strings"
	"testing"

	"github.com/varijkapil13/saral/pkg/adf"
	"github.com/varijkapil13/saral/pkg/jira"
	"github.com/varijkapil13/saral/pkg/jira/jiratest"
)

// allEditable sets editmeta so every row this build knows how to edit is
// listed on the issue's screen — summary, description, labels and due.
func allEditable(f *jiratest.Fake, key string) {
	f.SetEditMeta(key,
		jira.FieldMeta{Field: jira.FieldRef{ID: "summary"}},
		jira.FieldMeta{Field: jira.FieldRef{ID: "description"}},
		jira.FieldMeta{Field: jira.FieldRef{ID: "labels"}},
		jira.FieldMeta{Field: jira.FieldRef{ID: "duedate"}},
	)
}

// openEditable builds the pane on a fully-read, fully-editable issue and
// focuses the details region, which is where every test below starts.
func openEditable(t *testing.T, f *jiratest.Fake, key string, opts ...modelOption) (*panel, *recorder) {
	t.Helper()
	allEditable(f, key)
	rec := record(f)
	d := testDeps(rec)
	p := newPanel(t, New(d, readIssue(t, f, key), opts...), 100, 30)
	p.send(loadedMsg{gen: p.editor().gen, issue: readIssue(t, f, key)})
	p.editor().focus = regionDetails
	return p, rec
}

// rowAt puts the sidebar cursor on a row by id, failing the test if the row is
// not on screen at all.
func rowAt(t *testing.T, p *panel, id string) {
	t.Helper()
	for i, cr := range p.editor().sideRows {
		if cr.id == id {
			p.editor().cursor = i
			return
		}
	}
	t.Fatalf("no sidebar row named %q; have %d rows", id, len(p.editor().sideRows))
}

// TestDirty_EditingTheSummaryAndSavingSendsExactlyThatField is the packet's
// first claim: one field edited in place and saved sends a patch naming only
// that field.
func TestDirty_EditingTheSummaryAndSavingSendsExactlyThatField(t *testing.T) {
	t.Parallel()

	f := newFake(3)
	p, rec := openEditable(t, f, "PROJ-1", withDrafts(tempDrafts(t)))

	rowAt(t, p, "summary")
	p.keys("enter")
	if p.editor().stage != sideTyping {
		t.Fatalf("enter on the summary row did not open it: stage %v", p.editor().stage)
	}
	p.editor().input.SetValue("Renamed by the sidebar")
	p.keys("enter")
	if !p.editor().rowByID("summary").dirty() {
		t.Fatal("the summary row is not dirty after being edited")
	}

	p.keys("s")
	patch := rec.lastPatch(t)
	if names := patchFieldNames(patch); len(names) != 1 || names[0] != "summary" {
		t.Fatalf("patch named %v, want exactly [summary]", names)
	}
	if patch.Summary == nil || *patch.Summary != "Renamed by the sidebar" {
		t.Fatalf("patch.Summary = %v, want %q", patch.Summary, "Renamed by the sidebar")
	}
	if p.editor().anyDirty() {
		t.Error("the dirty set survived a save that landed")
	}
}

// TestDirty_ThreeEditsOneSave is the accumulation claim: nothing here sends a
// request per field, and the one it does send names every dirty row at once.
func TestDirty_ThreeEditsOneSave(t *testing.T) {
	t.Parallel()

	f := newFake(3)
	p, rec := openEditable(t, f, "PROJ-1", withDrafts(tempDrafts(t)))

	rowAt(t, p, "summary")
	p.keys("enter")
	p.editor().input.SetValue("New title")
	p.keys("enter")

	rowAt(t, p, "labels")
	p.keys("enter")
	p.editor().input.SetValue("alpha, beta")
	p.keys("enter")

	rowAt(t, p, "duedate")
	p.keys("enter")
	p.editor().input.SetValue("2026-01-15")
	p.keys("enter")

	if got := p.editor().dirtyCount(); got != 3 {
		t.Fatalf("dirty count = %d, want 3", got)
	}

	p.keys("s")
	if writes := rec.writes(); writes != 1 {
		t.Fatalf("the site saw %d writes, want exactly 1", writes)
	}
	patch := rec.lastPatch(t)
	names := patchFieldNames(patch)
	if len(names) != 3 {
		t.Fatalf("patch named %v, want summary, labels and duedate", names)
	}
	if patch.Summary == nil || *patch.Summary != "New title" {
		t.Errorf("patch.Summary = %v", patch.Summary)
	}
	if patch.Labels == nil || len(*patch.Labels) != 2 || (*patch.Labels)[0] != "alpha" || (*patch.Labels)[1] != "beta" {
		t.Errorf("patch.Labels = %v, want [alpha beta]", patch.Labels)
	}
	if patch.Due == nil || patch.Due.String() != "2026-01-15" {
		t.Errorf("patch.Due = %v, want 2026-01-15", patch.Due)
	}
}

// TestDirty_UndoRowAndUndoAll cover u — backspace here, since u is already
// half-page-up on this pane — and U.
func TestDirty_UndoRowAndUndoAll(t *testing.T) {
	t.Parallel()

	f := newFake(3)
	p, _ := openEditable(t, f, "PROJ-1", withDrafts(tempDrafts(t)))

	rowAt(t, p, "summary")
	p.keys("enter")
	p.editor().input.SetValue("Changed")
	p.keys("enter")
	rowAt(t, p, "labels")
	p.keys("enter")
	p.editor().input.SetValue("gamma")
	p.keys("enter")
	if p.editor().dirtyCount() != 2 {
		t.Fatalf("dirty count = %d, want 2", p.editor().dirtyCount())
	}

	rowAt(t, p, "summary")
	p.keys("backspace")
	if p.editor().rowByID("summary").dirty() {
		t.Error("undo this did not clear the summary row")
	}
	if !p.editor().rowByID("labels").dirty() {
		t.Error("undo this cleared a row it was not asked to")
	}

	p.keys("U")
	if p.editor().anyDirty() {
		t.Error("undo all left something dirty")
	}
}

// TestDirty_EscWithDirtyRowsAsksThenAnswers drives the leave prompt through
// its own keys: y saves and closes, n discards and closes, esc stays.
func TestDirty_EscWithDirtyRowsAsksThenAnswers(t *testing.T) {
	t.Parallel()

	t.Run("esc stays", func(t *testing.T) {
		t.Parallel()
		f := newFake(3)
		p, _ := openEditable(t, f, "PROJ-1", withDrafts(tempDrafts(t)))
		rowAt(t, p, "summary")
		p.keys("enter")
		p.editor().input.SetValue("Changed")
		p.keys("enter")

		if cmd := p.editor().AskClose(); cmd != nil {
			p.run(cmd)
		}
		if !p.editor().leaving {
			t.Fatal("AskClose did not put the pane into the leave prompt")
		}
		p.keys("esc")
		if p.editor().leaving {
			t.Error("esc did not close the leave prompt")
		}
		if !p.editor().anyDirty() {
			t.Error("esc from the leave prompt threw the edit away")
		}
		if p.pops != 0 {
			t.Error("esc from the leave prompt popped the view")
		}
	})

	t.Run("n discards and closes", func(t *testing.T) {
		t.Parallel()
		f := newFake(3)
		p, rec := openEditable(t, f, "PROJ-1", withDrafts(tempDrafts(t)))
		rowAt(t, p, "summary")
		p.keys("enter")
		p.editor().input.SetValue("Changed")
		p.keys("enter")

		p.run(p.editor().AskClose())
		p.keys("n")
		if p.editor().anyDirty() {
			t.Error("n left the edit in place")
		}
		if p.pops == 0 {
			t.Error("n did not close the view")
		}
		if rec.writes() != 0 {
			t.Error("n wrote to the site")
		}
	})

	t.Run("y saves and closes", func(t *testing.T) {
		t.Parallel()
		f := newFake(3)
		p, rec := openEditable(t, f, "PROJ-1", withDrafts(tempDrafts(t)))
		rowAt(t, p, "summary")
		p.keys("enter")
		p.editor().input.SetValue("Changed")
		p.keys("enter")

		p.run(p.editor().AskClose())
		p.keys("y")
		if rec.writes() != 1 {
			t.Fatalf("the site saw %d writes, want exactly 1", rec.writes())
		}
		if p.pops == 0 {
			t.Error("y did not close the view once the save landed")
		}
	})
}

// TestDirty_ADraftIsPickedBackUpAfterTheModelIsRebuilt is docs/UX.md
// principle 6 for this pane: a crash or a "stay" keeps the dirty set, and
// reopening the issue restores it.
func TestDirty_ADraftIsPickedBackUpAfterTheModelIsRebuilt(t *testing.T) {
	t.Parallel()

	f := newFake(3)
	drafts := tempDrafts(t)
	p, _ := openEditable(t, f, "PROJ-1", withDrafts(drafts))

	rowAt(t, p, "summary")
	p.keys("enter")
	p.editor().input.SetValue("Kept across a rebuild")
	p.keys("enter")
	if !p.editor().rowByID("summary").dirty() {
		t.Fatal("the row never became dirty")
	}

	// A fresh pane on the same issue, from the same drafts directory — a crash
	// and a restart, or a "stay" that later reopens it.
	allEditable(f, "PROJ-1")
	next := newPanel(t, New(testDeps(f), readIssue(t, f, "PROJ-1"), withDrafts(drafts)), 100, 30)
	next.send(loadedMsg{gen: next.editor().gen, issue: readIssue(t, f, "PROJ-1")})

	if !next.editor().draftRestored {
		t.Error("the rebuilt pane does not know its dirty set came from a draft")
	}
	row := next.editor().rowByID("summary")
	if row == nil || row.value != "Kept across a rebuild" {
		t.Fatalf("the restored summary is %q, want the draft's value", row.display())
	}
}

// TestDirty_AConflictRereadsAndKeepsTheEdit is the 409 path: the site changed
// underneath the edit, and the answer is a reread with the edit rebased back
// on top of it, never a dropped edit.
func TestDirty_AConflictRereadsAndKeepsTheEdit(t *testing.T) {
	t.Parallel()

	f := newFake(3)
	p, _ := openEditable(t, f, "PROJ-1", withDrafts(tempDrafts(t)))

	rowAt(t, p, "summary")
	p.keys("enter")
	p.editor().input.SetValue("Survives a conflict")
	p.keys("enter")

	f.FailNext(&jira.ConflictError{Resource: "PROJ-1", Detail: "updated by somebody else"})
	p.keys("s")

	if !p.editor().rowByID("summary").dirty() {
		t.Error("the conflict dropped the edit instead of keeping it")
	}
	if row := p.editor().rowByID("summary"); row == nil || row.value != "Survives a conflict" {
		t.Error("the conflict lost the typed text")
	}
	if p.editor().saveFail == "" {
		t.Error("the conflict said nothing about what happened")
	}
}

// TestDirty_APerFieldRefusalShowsOnItsRowAndKeepsTheText is a 422 naming one
// field: the row it is about carries the message, and the text is not thrown
// away because it was refused.
func TestDirty_APerFieldRefusalShowsOnItsRowAndKeepsTheText(t *testing.T) {
	t.Parallel()

	f := newFake(3)
	p, _ := openEditable(t, f, "PROJ-1", withDrafts(tempDrafts(t)))

	rowAt(t, p, "labels")
	p.keys("enter")
	p.editor().input.SetValue("not a valid label!!")
	p.keys("enter")

	f.FailNext(&jira.ValidationError{Fields: []jira.FieldError{
		{Field: "labels", Message: "a label may not contain punctuation"},
	}})
	p.keys("s")

	row := p.editor().rowByID("labels")
	if row == nil || row.problem == "" {
		t.Fatal("the refusal did not land on the labels row")
	}
	if row.value != "not a valid label!!" {
		t.Error("the refused text was thrown away")
	}
	if !row.dirty() {
		t.Error("a refused save must not look like it succeeded")
	}
}

// TestDirty_RowsEditmetaDoesNotListAreNotEditable checks the other half of
// editable(): fetched is not enough, and the one word every such row answers
// Enter with is "read-only".
func TestDirty_RowsEditmetaDoesNotListAreNotEditable(t *testing.T) {
	t.Parallel()

	f := newFake(3)
	// No SetEditMeta call: the fake answers an empty screen, which is a site
	// saying nothing here is editable right now.
	d := testDeps(f)
	p := newPanel(t, New(d, readIssue(t, f, "PROJ-1")), 100, 30)
	p.send(loadedMsg{gen: p.editor().gen, issue: readIssue(t, f, "PROJ-1")})
	p.editor().focus = regionDetails

	rowAt(t, p, "summary")
	if p.editor().rowByID("summary").editable() {
		t.Fatal("a row editmeta never listed reports itself editable")
	}
	p.keys("enter")
	if p.editor().stage != sideBrowse {
		t.Error("enter on a read-only row opened it anyway")
	}
	if got := p.lastStatus().Text; got != "read-only" {
		t.Errorf("enter on a read-only row said %q, want read-only", got)
	}
}

// TestDirty_TheEditorHandoffStillWorksFromTheRow is P2.3's $EDITOR launcher,
// reached from the description row directly rather than from a pushed pane.
func TestDirty_TheEditorHandoffStillWorksFromTheRow(t *testing.T) {
	t.Parallel()

	f := newFake(3)
	p, _ := openEditable(t, f, "PROJ-1",
		withDrafts(tempDrafts(t)), withLauncher(scriptedEditor(t, "Written in $EDITOR.", nil)))
	p.editor().focus = regionDesc

	p.keys("E")

	row := p.editor().rowByID("description")
	if row == nil || !row.dirty() {
		t.Fatal("the $EDITOR handoff did not mark the description dirty")
	}
	if got := strings.TrimSpace(adf.Markdown(row.documentNow())); got != "Written in $EDITOR." {
		t.Fatalf("the description is %q, want the editor's own text", got)
	}
}

// TestDirty_ARowNoOneCanReadStaysReadOnly checks that a row this pane cannot
// even see the true kind of — a summary the issue was never read with — is
// refused the same way an editmeta refusal is, and never crashes on a nil
// value.
func TestDirty_ARowNoOneCanReadStaysReadOnly(t *testing.T) {
	t.Parallel()

	// No client: a real one would settle Init's own re-read before newPanel
	// even returns, since the fake answers synchronously, which would replace
	// this narrow seed with a full one before the row was ever built from it.
	seed := jira.Issue{Key: "PROJ-1", Summary: "narrow seed"}
	p := newPanel(t, New(testDeps(nil), seed, withDrafts(tempDrafts(t))), 100, 30)
	p.editor().focus = regionDetails

	rowAt(t, p, "summary")
	if p.editor().rowByID("summary").editable() {
		t.Fatal("a row from a narrow read that was never fetched reports itself editable")
	}
	p.keys("enter")
	if p.editor().stage != sideBrowse {
		t.Error("enter on an unfetched row opened it anyway")
	}
}

// TestDirty_ClickingSaveInTheHeaderSaves is item 7: the word itself is a zone,
// wherever the pointer happens to be over the header rather than a region.
func TestDirty_ClickingSaveInTheHeaderSaves(t *testing.T) {
	t.Parallel()

	f := newFake(3)
	p, rec := openEditable(t, f, "PROJ-1", withDrafts(tempDrafts(t)))

	rowAt(t, p, "summary")
	p.keys("enter")
	p.editor().input.SetValue("Saved by a click")
	p.keys("enter")

	p.clickAt(p.zoneAt(p.editor().deps, "dirty:save"))
	if rec.writes() != 1 {
		t.Fatalf("clicking save wrote %d times, want exactly 1", rec.writes())
	}
}

// TestDirty_ClickingTheLeavePromptAnswersIt covers the mouse route through
// the leave prompt: each word is a zone, the same as its key.
func TestDirty_ClickingTheLeavePromptAnswersIt(t *testing.T) {
	t.Parallel()

	f := newFake(3)
	p, rec := openEditable(t, f, "PROJ-1", withDrafts(tempDrafts(t)))

	rowAt(t, p, "summary")
	p.keys("enter")
	p.editor().input.SetValue("Left by a click")
	p.keys("enter")

	p.run(p.editor().AskClose())
	p.clickAt(p.zoneAt(p.editor().deps, "leave:y"))
	if rec.writes() != 1 {
		t.Fatalf("clicking y wrote %d times, want exactly 1", rec.writes())
	}
	if p.pops == 0 {
		t.Error("clicking y did not close the view once the save landed")
	}
}

// TestDirty_TypingIntoARowRedrawsOnEveryKeystroke guards a memo bug: the
// sidebar's own cache key carries the cursor and the stage, neither of which
// moves while a row is only gaining characters, so a keystroke that changed
// nothing about either used to redraw the same frame the row was opened with.
func TestDirty_TypingIntoARowRedrawsOnEveryKeystroke(t *testing.T) {
	t.Parallel()

	f := newFake(3)
	p, _ := openEditable(t, f, "PROJ-1", withDrafts(tempDrafts(t)))

	rowAt(t, p, "summary")
	p.keys("enter")
	for range 40 {
		p.keys("backspace")
	}
	p.typed("HELLO")

	if got := p.editor().input.Value(); got != "HELLO" {
		t.Fatalf("the input holds %q, want HELLO", got)
	}
	if frame := p.frame(); !strings.Contains(frame, "HELLO") {
		t.Errorf("the typed value never reached the frame:\n%s", frame)
	}
}
