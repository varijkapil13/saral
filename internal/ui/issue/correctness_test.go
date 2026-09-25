package issue

import (
	"errors"
	"os"
	"slices"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/varijkapil13/saral/pkg/adf"
	"github.com/varijkapil13/saral/pkg/jira"
)

const testSite = "example.atlassian.net"

func rightAway(_ time.Duration, fn func() tea.Msg) tea.Cmd { return fn }

func editSummary(t *testing.T, p *panel, to string) {
	t.Helper()
	p.editor().focus = regionDetails
	rowAt(t, p, "summary")
	p.keys("enter")
	p.editor().input.SetValue(to)
	p.keys("enter")
}

func TestDescriptionEditor_EscKeepsTheTextAndReopeningRestoresIt(t *testing.T) {
	t.Parallel()

	f := newFake(3)
	drafts := tempDrafts(t)
	p, rec := openEditable(t, f, "PROJ-1", withDrafts(drafts), withAfter(rightAway))
	p.editor().focus = regionDesc

	p.keys("e")
	p.typed("half a thought")
	p.keys("esc")

	m := p.editor()
	row := m.rowByID("description")
	if m.stage != sideBrowse {
		t.Fatalf("esc left the textarea open: stage %v", m.stage)
	}
	if row.pending == nil || !strings.Contains(*row.pending, "half a thought") {
		t.Fatalf("esc threw the typed text away: pending %v", row.pending)
	}
	if row.dirty() {
		t.Error("text that was never kept is counted as an edit to send")
	}
	d, ok, err := drafts.load(testSite, "PROJ-1")
	if err != nil || !ok || d.DescriptionText == nil || !strings.Contains(*d.DescriptionText, "half a thought") {
		t.Fatalf("the draft does not hold the typed text: %+v ok=%v err=%v", d, ok, err)
	}

	p.keys("e")
	if got := m.docArea.Value(); !strings.Contains(got, "half a thought") {
		t.Fatalf("reopening the editor did not restore the text: %q", got)
	}
	p.keys("esc")

	allEditable(f, "PROJ-1")
	next := newPanel(t, New(testDeps(t, f), readIssue(t, f, "PROJ-1"), withDrafts(drafts)), 100, 30)
	next.send(loadedMsg{gen: next.editor().gen, issue: readIssue(t, f, "PROJ-1")})
	next.editor().focus = regionDesc
	next.keys("e")
	if got := next.editor().docArea.Value(); !strings.Contains(got, "half a thought") {
		t.Fatalf("a rebuilt pane did not restore the text: %q", got)
	}
	if rec.writes() != 0 {
		t.Errorf("putting the text aside wrote to the site %d times", rec.writes())
	}
}

func TestDescriptionEditor_TypingReachesTheDraftOnlyWhenItPauses(t *testing.T) {
	t.Parallel()

	var waiting []func() tea.Msg
	hold := func(_ time.Duration, fn func() tea.Msg) tea.Cmd {
		waiting = append(waiting, fn)
		return nil
	}
	f := newFake(3)
	drafts := tempDrafts(t)
	p, _ := openEditable(t, f, "PROJ-1", withDrafts(drafts), withAfter(hold))
	p.editor().focus = regionDesc
	p.keys("e")
	p.typed("abc")

	if len(waiting) != 3 {
		t.Fatalf("three keystrokes started %d pauses, want 3", len(waiting))
	}
	p.run(waiting[0])
	if _, ok, _ := drafts.load(testSite, "PROJ-1"); ok {
		t.Fatal("a pause cut short by the next keystroke wrote the draft")
	}
	p.run(waiting[2])
	d, ok, err := drafts.load(testSite, "PROJ-1")
	if err != nil || !ok || d.DescriptionText == nil || !strings.Contains(*d.DescriptionText, "abc") {
		t.Fatalf("the last pause did not write the typed text: %+v", d)
	}
}

func TestDescriptionEditor_RevertThrowsTheHeldTextAway(t *testing.T) {
	t.Parallel()

	f := newFake(3)
	drafts := tempDrafts(t)
	p, _ := openEditable(t, f, "PROJ-1", withDrafts(drafts), withAfter(rightAway))
	p.editor().focus = regionDesc
	p.keys("e")
	p.typed("gone soon")
	p.keys("esc", "x")

	if row := p.editor().rowByID("description"); row.pending != nil {
		t.Fatalf("x kept the held text: %q", *row.pending)
	}
	if _, ok, _ := drafts.load(testSite, "PROJ-1"); ok {
		t.Error("the draft still holds text x threw away")
	}
}

func TestSave_RefusesToOverwriteAFieldChangedOnTheSite(t *testing.T) {
	t.Parallel()

	f := newFake(3)
	p, rec := openEditable(t, f, "PROJ-1", withDrafts(tempDrafts(t)))
	editSummary(t, p, "Mine")

	theirs := "Theirs"
	if err := f.UpdateIssue(t.Context(), "PROJ-1", jira.IssuePatch{Summary: &theirs}); err != nil {
		t.Fatal(err)
	}
	p.keys("s")

	if rec.writes() != 0 {
		t.Fatalf("the save overwrote a summary changed on the site (%d writes)", rec.writes())
	}
	row := p.editor().rowByID("summary")
	if !row.dirty() || row.value != "Mine" {
		t.Fatalf("the conflict cost the edit: %q dirty=%v", row.value, row.dirty())
	}
	if row.problem == "" {
		t.Error("the row the site changed is not marked")
	}
	if !strings.Contains(p.statusText(), "changed on the site") {
		t.Errorf("nothing said the site moved: %q", p.statusText())
	}

	p.keys("s")
	if rec.writes() != 1 {
		t.Fatalf("saving again after the review wrote %d times, want 1", rec.writes())
	}
	if got := readIssue(t, f, "PROJ-1").Summary; got != "Mine" {
		t.Errorf("the site holds %q after the reviewed save", got)
	}
}

func TestSave_ALabelAddedOnTheSiteSurvivesALabelEdit(t *testing.T) {
	t.Parallel()

	f := newFake(3)
	p, rec := openEditable(t, f, "PROJ-1", withDrafts(tempDrafts(t)))
	was := readIssue(t, f, "PROJ-1").Labels
	rowAt(t, p, "labels")
	p.keys("enter")
	p.editor().input.SetValue(strings.Join(append(slices.Clone(was), "mine"), ", "))
	p.keys("enter")

	if err := f.UpdateIssue(t.Context(), "PROJ-1", jira.IssuePatch{AddLabels: []string{"theirs"}}); err != nil {
		t.Fatal(err)
	}
	p.keys("s")

	if rec.writes() != 1 {
		t.Fatalf("a labels edit beside somebody else's label wrote %d times, want 1", rec.writes())
	}
	got := readIssue(t, f, "PROJ-1").Labels
	if !slices.Contains(got, "theirs") || !slices.Contains(got, "mine") {
		t.Errorf("the site holds %v, want both labels", got)
	}
}

func TestSave_TheBaseCheckFailingWritesNothing(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name string
		err  error
	}{
		{"forbidden", &jira.CapabilityError{Reason: "no Browse projects"}},
		{"rate limited", &jira.RateLimitError{RetryAfter: time.Second}},
		{"transport", &jira.TransportError{Op: "issue", Err: errors.New("connection reset")}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			f := newFake(3)
			p, rec := openEditable(t, f, "PROJ-1", withDrafts(tempDrafts(t)))
			editSummary(t, p, "Mine")
			f.FailNext(tc.err)
			p.keys("s")
			if rec.writes() != 0 {
				t.Fatalf("a failed base check still wrote %d times", rec.writes())
			}
			if !p.editor().rowByID("summary").dirty() {
				t.Error("the failure cost the edit")
			}
			if p.editor().stage != sideBrowse {
				t.Errorf("the pane is stuck in stage %v", p.editor().stage)
			}
		})
	}
}

func TestSave_TheReadAfterASaveUsesTheIssueEndpoint(t *testing.T) {
	t.Parallel()

	f := newFake(3)
	p, _ := openEditable(t, f, "PROJ-1", withDrafts(tempDrafts(t)))
	editSummary(t, p, "Read straight back")
	before := len(f.Calls())
	p.keys("s")

	calls := f.Calls()[before:]
	if slices.Contains(calls, "Search") {
		t.Errorf("the re-read went through search, whose index trails the write: %v", calls)
	}
	if n := strings.Count(strings.Join(calls, " "), "IssueFields"); n < 2 {
		t.Errorf("want the base check and the re-read both through IssueFields, got %v", calls)
	}
	if got := p.editor().issue.Summary; got != "Read straight back" {
		t.Errorf("the pane shows %q after the save", got)
	}
}

func TestDraft_ARestoredDraftIsCheckedAgainstItsBase(t *testing.T) {
	t.Parallel()

	f := newFake(3)
	drafts := tempDrafts(t)
	p, _ := openEditable(t, f, "PROJ-1", withDrafts(drafts))
	editSummary(t, p, "Mine, from yesterday")

	theirs := "Theirs, from this morning"
	if err := f.UpdateIssue(t.Context(), "PROJ-1", jira.IssuePatch{Summary: &theirs}); err != nil {
		t.Fatal(err)
	}
	allEditable(f, "PROJ-1")
	next := newPanel(t, New(testDeps(t, f), jira.Issue{Key: "PROJ-1"}, withDrafts(drafts)), 100, 30)
	next.send(loadedMsg{gen: next.editor().gen, issue: readIssue(t, f, "PROJ-1")})

	row := next.editor().rowByID("summary")
	if row.value != "Mine, from yesterday" || !row.dirty() {
		t.Fatalf("the draft was not restored: %q", row.value)
	}
	if row.problem == "" {
		t.Error("a draft whose field moved on the site is not flagged")
	}
	if !strings.Contains(next.statusText(), "changed on the site") {
		t.Errorf("nothing said the draft's field moved: %q", next.statusText())
	}
}

func TestTransition_CarryingTheDirtySetChecksItsBase(t *testing.T) {
	t.Parallel()

	f := newFake(6)
	p, rec := openPickable(t, f, "PROJ-3", nil, withDrafts(tempDrafts(t)))
	editSummary(t, p, "Mine")
	theirs := "Theirs"
	if err := f.UpdateIssue(t.Context(), "PROJ-3", jira.IssuePatch{Summary: &theirs}); err != nil {
		t.Fatal(err)
	}

	p.keys("t")
	moves := p.editor().pick.moves
	at := slices.IndexFunc(moves, func(tr jira.Transition) bool { return len(requiredFields(tr)) == 0 })
	if at < 0 {
		t.Fatal("no screen-free move on this fixture")
	}
	for range at {
		p.keys("down")
	}
	p.keys("enter", "y")

	if rec.writes() != 0 {
		t.Fatalf("a move carrying a stale summary was applied (%d writes)", rec.writes())
	}
	if !p.editor().rowByID("summary").dirty() {
		t.Error("the refused move cost the edit")
	}
}

func TestRevert_FollowsTheRegionWithTheKeyboard(t *testing.T) {
	t.Parallel()

	f := newFake(3)
	p, _ := openEditable(t, f, "PROJ-1", withDrafts(tempDrafts(t)),
		withLauncher(scriptedEditor(t, "Rewritten.", nil)))
	editSummary(t, p, "Mine")
	p.editor().focus = regionDesc
	p.keys("E")
	m := p.editor()
	if !m.rowByID("description").dirty() {
		t.Fatal("the description never became dirty")
	}

	m.focus = regionComments
	p.keys("x")
	if !m.rowByID("summary").dirty() || !m.rowByID("description").dirty() {
		t.Fatal("x in the comments reverted a field")
	}
	if got := p.lastStatus().Text; got != "nothing to revert here" {
		t.Errorf("x in the comments said %q", got)
	}

	m.focus = regionDesc
	rowAt(t, p, "summary")
	m.focus = regionDesc
	p.keys("x")
	if m.rowByID("description").dirty() {
		t.Error("x in the description did not revert it")
	}
	if !m.rowByID("summary").dirty() {
		t.Error("x in the description reverted the sidebar row under the cursor")
	}

	m.focus = regionDetails
	rowAt(t, p, "summary")
	p.keys("backspace")
	if m.rowByID("summary").dirty() {
		t.Error("backspace no longer reverts the row under the cursor")
	}
}

func TestRevert_XAndUBothRevertAll(t *testing.T) {
	t.Parallel()

	for _, key := range []string{"X", "U"} {
		t.Run(key, func(t *testing.T) {
			t.Parallel()
			f := newFake(3)
			p, _ := openEditable(t, f, "PROJ-1", withDrafts(tempDrafts(t)))
			editSummary(t, p, "Mine")
			p.keys(key)
			if p.editor().anyDirty() {
				t.Errorf("%s left the edit in place", key)
			}
		})
	}
}

func TestHeader_FactsOpenTheListsTheirRowsOpen(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		zone string
		pick string
	}{
		{zoneFactStatus, "status"},
		{zoneFactPriority, "priority"},
		{zoneFactAssignee, "assignee"},
	} {
		t.Run(tc.pick, func(t *testing.T) {
			t.Parallel()
			f := newFake(6)
			iss := firstAssignedIssue(t, f, 6)
			allEditableWithChoices(f, iss.Key, fakePriorityOptions...)
			d := testDeps(t, record(f))
			p := newPanel(t, New(d, readIssue(t, f, iss.Key), withDrafts(tempDrafts(t))), 120, 30)
			p.send(loadedMsg{gen: p.editor().gen, issue: readIssue(t, f, iss.Key)})

			p.clickAt(p.zoneAt(d, tc.zone))
			m := p.editor()
			if m.stage != sidePicking || m.pick == nil || m.pick.id != tc.pick {
				t.Fatalf("a click on the header's %s opened stage %v pick %+v", tc.pick, m.stage, m.pick)
			}
			if m.focus != regionDetails {
				t.Error("the list opened without the keyboard moving to it")
			}
		})
	}
}

func TestHeader_AnIssueOpenedByKeyShowsPlaceholdersUntilItIsRead(t *testing.T) {
	t.Parallel()

	p := newPanel(t, New(testDeps(t, nil), jira.Issue{Key: "PROJ-1"}, withDrafts(tempDrafts(t))), 100, 30)
	g := p.editor().deps.Theme.Glyphs
	head := strings.Split(p.frame(), "\n")[1]
	if !strings.Contains(head, g.TypeOther+" "+g.Ellipsis) || !strings.Contains(head, g.CategoryUnknown+" "+g.Ellipsis) {
		t.Fatalf("the facts line has no placeholders while loading: %q", head)
	}

	p.send(failedMsg{gen: p.editor().gen, err: &jira.TransportError{Op: "issue", Err: errors.New("offline")}})
	head = strings.Split(p.frame(), "\n")[1]
	if !strings.Contains(head, "unknown") || strings.Contains(head, g.Ellipsis) {
		t.Fatalf("a read that failed still says it is loading: %q", head)
	}
	golden(t, "issue_offline_by_key_100x30.golden", p.frame())
}

func TestEditorWords_KeepsQuotedPathsWhole(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name, value, goos string
		want              []string
	}{
		{"arguments", "code --wait", "linux", []string{"code", "--wait"}},
		{"a quoted path with spaces", `"/Applications/My Editor.app/bin/ed" -w`, "darwin", []string{"/Applications/My Editor.app/bin/ed", "-w"}},
		{"escaped spaces", `/opt/my\ editor/bin/ed`, "linux", []string{"/opt/my editor/bin/ed"}},
		{"a windows path", `"C:\Program Files\Notepad++\notepad++.exe" -multiInst`, "windows", []string{`C:\Program Files\Notepad++\notepad++.exe`, "-multiInst"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got, err := editorWords(tc.value, tc.goos)
			if err != nil {
				t.Fatal(err)
			}
			if !slices.Equal(got, tc.want) {
				t.Errorf("editorWords(%q) = %q, want %q", tc.value, got, tc.want)
			}
		})
	}
	if _, err := editorWords(`"unclosed`, "linux"); err == nil {
		t.Error("an unbalanced quote was accepted")
	}
}

func lossyDoc() adf.Doc {
	return adf.NewDoc(adf.NewNode("paragraph",
		adf.NewText("Ready: "),
		adf.Node{Type: "status", Attrs: map[string]any{"text": "Done", "color": "green"}},
	))
}

func TestHandoff_NamesTheLossesBeforeTheEditAndDropsTheLineAfter(t *testing.T) {
	t.Parallel()

	f := newFake(3)
	iss := readIssue(t, f, "PROJ-1")
	iss.Description = lossyDoc()
	var opened string
	untouched := func(path string, done func(error) tea.Msg) tea.Cmd {
		return func() tea.Msg {
			body, err := os.ReadFile(path) //nolint:gosec // the handoff file this test was handed
			if err != nil {
				t.Error(err)
			}
			opened = string(body)
			return done(nil)
		}
	}
	p := newPanel(t, New(testDeps(t, nil), iss, withDrafts(tempDrafts(t)), withLauncher(untouched)), 100, 30)
	p.send(loadedMsg{gen: p.editor().gen, issue: iss})
	p.editor().edit = jira.EditMeta{Fields: []jira.FieldMeta{{Field: jira.FieldRef{ID: "description"}}}}
	p.editor().relist()
	p.editor().focus = regionDesc
	p.keys("E")

	if !strings.HasPrefix(opened, handoffMark) || !strings.Contains(opened, "loses") {
		t.Fatalf("the handoff file does not open by naming what the edit costs:\n%s", opened)
	}
	if got := p.lastStatus().Text; got != "the description is unchanged" {
		t.Errorf("an untouched file reads back as %q", got)
	}
}

func TestHandoff_AnAppliedEditWarnsWhatItCost(t *testing.T) {
	t.Parallel()

	f := newFake(3)
	iss := readIssue(t, f, "PROJ-1")
	iss.Description = lossyDoc()
	p := newPanel(t, New(testDeps(t, nil), iss, withDrafts(tempDrafts(t)),
		withLauncher(scriptedEditor(t, "Rewritten entirely.", nil))), 100, 30)
	p.send(loadedMsg{gen: p.editor().gen, issue: iss})
	p.editor().focus = regionDesc
	p.editor().edit = jira.EditMeta{Fields: []jira.FieldMeta{{Field: jira.FieldRef{ID: "description"}}}}
	p.editor().relist()
	p.keys("E")

	if !p.editor().rowByID("description").dirty() {
		t.Fatal("the edit was not applied")
	}
	if !strings.Contains(p.statusText(), "editing this as markdown loses") {
		t.Errorf("applying the edit said nothing of its cost: %q", p.statusText())
	}
}

func TestDescriptionEditor_NamesTheLossesWhileItIsOpen(t *testing.T) {
	t.Parallel()

	f := newFake(3)
	iss := readIssue(t, f, "PROJ-1")
	iss.Description = lossyDoc()
	p := newPanel(t, New(testDeps(t, nil), iss, withDrafts(tempDrafts(t))), 100, 30)
	p.send(loadedMsg{gen: p.editor().gen, issue: iss})
	p.editor().edit = jira.EditMeta{Fields: []jira.FieldMeta{{Field: jira.FieldRef{ID: "description"}}}}
	p.editor().relist()
	p.editor().focus = regionDesc
	p.keys("e")

	if !strings.Contains(p.frame(), "editing this as markdown loses") {
		t.Fatalf("the open editor does not say what an edit costs:\n%s", p.frame())
	}
}
