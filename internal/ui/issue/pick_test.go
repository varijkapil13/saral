package issue

import (
	"fmt"
	"slices"
	"strings"
	"testing"

	"github.com/varijkapil13/saral/pkg/jira"
	"github.com/varijkapil13/saral/pkg/jira/jiratest"
)

// allEditableWithChoices lists every row this packet's inline lists cover as
// editable, on top of the four the previous packet already had: priority is
// given editmeta's own allowed values, since that is where openChoicePicker
// reads them from rather than a separate site read.
func allEditableWithChoices(f *jiratest.Fake, key string, priorities ...jira.Option) {
	f.SetEditMeta(key,
		jira.FieldMeta{Field: jira.FieldRef{ID: "summary"}},
		jira.FieldMeta{Field: jira.FieldRef{ID: "description"}},
		jira.FieldMeta{Field: jira.FieldRef{ID: "labels"}},
		jira.FieldMeta{Field: jira.FieldRef{ID: "duedate"}},
		jira.FieldMeta{Field: jira.FieldRef{ID: "priority"}, AllowedValues: priorities},
		jira.FieldMeta{Field: jira.FieldRef{ID: "assignee"}},
	)
}

// openPickable is openEditable plus editmeta for priority and assignee, so
// this packet's own rows are on the sidebar's cursor as editable too.
func openPickable(t *testing.T, f *jiratest.Fake, key string, priorities []jira.Option, opts ...modelOption) (*panel, *recorder) {
	t.Helper()
	allEditableWithChoices(f, key, priorities...)
	rec := record(f)
	d := testDeps(rec)
	p := newPanel(t, New(d, readIssue(t, f, key), opts...), 100, 30)
	p.send(loadedMsg{gen: p.editor().gen, issue: readIssue(t, f, key)})
	p.editor().focus = regionDetails
	return p, rec
}

// fakePriorityOptions mirrors jiratest's own generated priorities: the fake's
// UpdateIssue refuses a priority id it never minted, so a test choosing one
// has to choose a real one rather than an invented id.
var fakePriorityOptions = []jira.Option{
	{ID: "10401", Label: "Urgent"},
	{ID: "10402", Label: "Normal"},
	{ID: "10403", Label: "Whenever"},
}

// otherPriority is a real priority that is not the one iss already carries,
// so choosing it is guaranteed to dirty the row.
func otherPriority(iss jira.Issue) jira.Option {
	for _, o := range fakePriorityOptions {
		if iss.Priority == nil || o.ID != iss.Priority.ID {
			return o
		}
	}
	return fakePriorityOptions[0]
}

// firstAssignedIssue is the first generated issue that already carries a real
// assignee, which is what a test about clearing one needs to start from.
func firstAssignedIssue(t *testing.T, f *jiratest.Fake, n int) jira.Issue {
	t.Helper()
	for i := 1; i <= n; i++ {
		iss, err := f.Issue(t.Context(), fmt.Sprintf("PROJ-%d", i))
		if err == nil && iss.Assignee != nil {
			return iss
		}
	}
	t.Fatal("no generated issue carries an assignee")
	return jira.Issue{}
}

// pickOptionByID finds a ranked candidate, failing the test if the inline
// list never offered it.
func pickOptionByID(t *testing.T, opts []pickOption, id string) int {
	t.Helper()
	for i, o := range opts {
		if o.id == id {
			return i
		}
	}
	t.Fatalf("the inline list does not offer %q: %+v", id, opts)
	return -1
}

// TestPick_PriorityChoosesFromEditmetasAllowedValues is item 1's own claim for
// a single choice: the options come from editmeta's AllowedValues, which is
// the same read fetch() already asked for — there is no second request to
// open this list at all.
func TestPick_PriorityChoosesFromEditmetasAllowedValues(t *testing.T) {
	t.Parallel()

	f := newFake(3)
	iss := readIssue(t, f, "PROJ-1")
	want := otherPriority(iss)
	p, rec := openPickable(t, f, "PROJ-1", fakePriorityOptions, withDrafts(tempDrafts(t)))

	rowAt(t, p, "priority")
	p.keys("enter")
	if p.editor().stage != sidePicking || p.editor().pick == nil || p.editor().pick.kind != rkChoice {
		t.Fatalf("enter on the priority row did not open its inline list: stage %v", p.editor().stage)
	}
	if len(p.editor().pick.ranked) != len(fakePriorityOptions) {
		t.Fatalf("the list holds %d options, want editmeta's own %d", len(p.editor().pick.ranked), len(fakePriorityOptions))
	}

	// The cursor already sits on whatever the row currently holds — see
	// rerankPick — so it is set directly here rather than by a guessed number
	// of downs from an assumed start.
	p.editor().pick.cursor = pickOptionByID(t, p.editor().pick.ranked, want.ID)
	p.keys("enter")

	row := p.editor().rowByID("priority")
	if row.chosenID != want.ID || !row.dirty() {
		t.Fatalf("chosenID = %q, dirty = %v, want %q and dirty", row.chosenID, row.dirty(), want.ID)
	}
	if row.value != want.Label {
		t.Errorf("row.value = %q, want %q", row.value, want.Label)
	}
	if p.editor().stage != sideBrowse {
		t.Error("choosing a value did not close the inline list")
	}

	p.keys("s")
	patch := rec.lastPatch(t)
	if names := patchFieldNames(patch); len(names) != 1 || names[0] != "priority" {
		t.Fatalf("patch named %v, want exactly [priority]", names)
	}
	if patch.PriorityID == nil || *patch.PriorityID != want.ID {
		t.Fatalf("patch.PriorityID = %v, want %q", patch.PriorityID, want.ID)
	}
}

// TestPick_APriorityEditmetaDoesNotListStaysReadOnly is the other half of
// editable() for the new kinds: a fetch this build never got a screen for is
// still read-only, in the same one word every such row answers with.
func TestPick_APriorityEditmetaDoesNotListStaysReadOnly(t *testing.T) {
	t.Parallel()

	f := newFake(3)
	d := testDeps(f)
	p := newPanel(t, New(d, readIssue(t, f, "PROJ-1")), 100, 30)
	p.send(loadedMsg{gen: p.editor().gen, issue: readIssue(t, f, "PROJ-1")})
	p.editor().focus = regionDetails

	rowAt(t, p, "priority")
	if p.editor().rowByID("priority").editable() {
		t.Fatal("a priority editmeta never listed reports itself editable")
	}
	p.keys("enter")
	if p.editor().stage != sideBrowse {
		t.Error("enter on a read-only priority row opened it anyway")
	}
	if got := p.lastStatus().Text; got != "read-only" {
		t.Errorf("status = %q, want read-only", got)
	}
}

// TestPick_AssigneeSearchesTypedTextAndSendsTheAccountID is the person half:
// typing part of a name searches the site, choosing one dirties the row by
// account id, and saving sends that id rather than the name.
func TestPick_AssigneeSearchesTypedTextAndSendsTheAccountID(t *testing.T) {
	t.Parallel()

	f := newFake(3)
	p, rec := openPickable(t, f, "PROJ-1", nil, withDrafts(tempDrafts(t)))

	rowAt(t, p, "assignee")
	p.keys("enter")
	if p.editor().pick == nil || p.editor().pick.kind != rkPerson {
		t.Fatal("enter on the assignee row did not open the person picker")
	}

	p.typed("Lov")
	at := pickOptionByID(t, p.editor().pick.ranked, "acct-ada")
	p.editor().pick.cursor = at
	p.keys("enter")

	row := p.editor().rowByID("assignee")
	if row.chosenID != "acct-ada" {
		t.Fatalf("chosenID = %q, want acct-ada", row.chosenID)
	}
	if row.value != "Ada Lovelace" {
		t.Errorf("row.value = %q, want the display name", row.value)
	}

	p.keys("s")
	patch := rec.lastPatch(t)
	if patch.Assignee == nil || *patch.Assignee != "acct-ada" {
		t.Fatalf("patch.Assignee = %v, want acct-ada", patch.Assignee)
	}
}

// TestPick_UnassignSetsAnEmptyAccountID is the palette's "Unassign": the same
// commit choosing "Unassigned" out of the list would have made.
func TestPick_UnassignSetsAnEmptyAccountID(t *testing.T) {
	t.Parallel()

	f := newFake(6)
	iss := firstAssignedIssue(t, f, 6)
	p, rec := openPickable(t, f, iss.Key, nil, withDrafts(tempDrafts(t)))

	p.send(UnassignMsg{})
	row := p.editor().rowByID("assignee")
	if row.chosenID != "" || !row.dirty() {
		t.Fatalf("chosenID = %q, dirty = %v, want empty and dirty", row.chosenID, row.dirty())
	}
	if row.value != "unassigned" {
		t.Errorf("row.value = %q, want unassigned", row.value)
	}

	p.keys("s")
	patch := rec.lastPatch(t)
	if patch.Assignee == nil || *patch.Assignee != "" {
		t.Fatalf("patch.Assignee = %v, want an empty string", patch.Assignee)
	}
}

// TestPick_AssignToMeUsesMe is the palette's "Assign to me": the account
// Me() names, read once and kept for the pane rather than a second question.
func TestPick_AssignToMeUsesMe(t *testing.T) {
	t.Parallel()

	f := newFake(3)
	p, rec := openPickable(t, f, "PROJ-1", nil, withDrafts(tempDrafts(t)))

	p.send(AssignSelfMsg{})
	row := p.editor().rowByID("assignee")
	if row.chosenID != "acct-me" {
		t.Fatalf("chosenID = %q, want the fake's own account", row.chosenID)
	}
	if row.value != "Sam Tester" {
		t.Errorf("row.value = %q, want the display name", row.value)
	}
	if p.editor().me == nil || p.editor().me.AccountID != "acct-me" {
		t.Error("Me() was not kept on the pane")
	}

	p.keys("s")
	patch := rec.lastPatch(t)
	if patch.Assignee == nil || *patch.Assignee != "acct-me" {
		t.Fatalf("patch.Assignee = %v, want acct-me", patch.Assignee)
	}
}

// TestPick_TheAssigneePickerOffersMeAndUnassignedFirst is item 2's own claim:
// both are on offer before anything is typed and before the site has answered
// anything at all.
func TestPick_TheAssigneePickerOffersMeAndUnassignedFirst(t *testing.T) {
	t.Parallel()

	f := newFake(3)
	p, _ := openPickable(t, f, "PROJ-1", nil, withDrafts(tempDrafts(t)))

	rowAt(t, p, "assignee")
	p.keys("enter")
	found := map[string]bool{}
	for _, o := range p.editor().pick.all {
		found[o.id] = true
	}
	if !found["acct-me"] {
		t.Error("Me is never among the candidates")
	}
	if !found[""] {
		t.Error("Unassigned is never among the candidates")
	}
}

// TestPick_NoCapPeopleSaysWhyRatherThanShowingAnEmptyList is docs/UX.md's own
// rule applied to a token missing a site-wide permission: the reason, in the
// probe's own words, not an empty list that looks like nobody exists.
func TestPick_NoCapPeopleSaysWhyRatherThanShowingAnEmptyList(t *testing.T) {
	t.Parallel()

	f := newFake(3)
	allEditableWithChoices(f, "PROJ-1")
	d := testDeps(f)
	d.Caps.People = jira.Capability{Reason: "needs the Browse users and groups permission"}
	p := newPanel(t, New(d, readIssue(t, f, "PROJ-1"), withDrafts(tempDrafts(t))), 100, 30)
	p.send(loadedMsg{gen: p.editor().gen, issue: readIssue(t, f, "PROJ-1")})
	p.editor().focus = regionDetails

	rowAt(t, p, "assignee")
	p.keys("enter")
	if p.editor().stage == sidePicking {
		t.Fatal("the picker opened despite the missing capability")
	}
	if !strings.Contains(p.lastStatus().Text, "Browse users and groups") {
		t.Errorf("status = %q, want the capability's own reason", p.lastStatus().Text)
	}
}

// TestPick_StatusTransitionSendsTheDirtySetThroughTheTransitionEndpoint is
// item 3's own claim: a status change carries the rest of the dirty set in
// the same request, since Transition takes fields exactly as UpdateIssue does.
func TestPick_StatusTransitionSendsTheDirtySetThroughTheTransitionEndpoint(t *testing.T) {
	t.Parallel()

	f := newFake(6)
	p, rec := openPickable(t, f, "PROJ-3", nil, withDrafts(tempDrafts(t)))

	rowAt(t, p, "summary")
	p.keys("enter")
	p.editor().input.SetValue("Renamed before moving")
	p.keys("enter")
	if !p.editor().rowByID("summary").dirty() {
		t.Fatal("the summary is not dirty")
	}

	p.keys("t")
	if p.editor().pick == nil || p.editor().pick.kind != rkStatus {
		t.Fatal("t did not open the inline status picker")
	}
	moves := p.editor().pick.moves
	at := slices.IndexFunc(moves, func(tr jira.Transition) bool { return len(requiredFields(tr)) == 0 })
	if at < 0 {
		t.Fatal("every move on this issue needs a screen; this fixture cannot show a screen-free confirmation")
	}
	for range at {
		p.keys("down")
	}
	p.keys("enter")
	if !p.editor().pick.confirming {
		t.Fatalf("choosing a screen-free move did not go straight to the confirmation")
	}
	if got := p.editor().confirmTransitionQuestion(); !strings.Contains(got, "1 change") {
		t.Errorf("the confirmation does not mention the dirty field it is also saving: %q", got)
	}
	p.keys("y")

	move := rec.lastMove(t)
	if move.id != moves[at].ID {
		t.Errorf("move id = %q, want %q", move.id, moves[at].ID)
	}
	if move.patch.Summary == nil || *move.patch.Summary != "Renamed before moving" {
		t.Errorf("the transition's own patch does not carry the dirty summary: %+v", move.patch)
	}
	if p.editor().anyDirty() {
		t.Error("the dirty set survived a save that landed through the transition")
	}
	if p.editor().pick != nil || p.editor().stage != sideBrowse {
		t.Error("the picker did not close once the move landed")
	}
}

// TestPick_AFailedMoveKeepsTheDirtySetAndSaysWhy is the failure path
// docs/PARALLEL.md asks every packet to cover: a refusal at the transition
// endpoint must not look like it landed, and must not cost the reader the
// edit riding along with it.
func TestPick_AFailedMoveKeepsTheDirtySetAndSaysWhy(t *testing.T) {
	t.Parallel()

	f := newFake(6)
	p, rec := openPickable(t, f, "PROJ-3", nil, withDrafts(tempDrafts(t)))

	rowAt(t, p, "summary")
	p.keys("enter")
	p.editor().input.SetValue("Kept through a failed move")
	p.keys("enter")

	p.keys("t")
	moves := p.editor().pick.moves
	at := slices.IndexFunc(moves, func(tr jira.Transition) bool { return len(requiredFields(tr)) == 0 })
	if at < 0 {
		t.Fatal("every move on this issue needs a screen")
	}
	p.editor().pick.cursor = at
	p.keys("enter")
	if !p.editor().pick.confirming {
		t.Fatal("did not reach the confirmation")
	}

	f.FailNext(&jira.CapabilityError{Reason: "you need Transition Issues in this project"})
	p.keys("y")

	if rec.writes() != 0 {
		t.Fatal("a move that failed still reached the recorder as a write")
	}
	if !strings.Contains(p.lastStatus().Text, "Transition Issues") {
		t.Errorf("status = %q, want the refusal's own words", p.lastStatus().Text)
	}
	if !p.editor().rowByID("summary").dirty() {
		t.Error("the dirty summary was lost when the move failed")
	}
	if p.editor().pick == nil {
		t.Error("the picker closed even though the move never landed")
	}
}

// TestPick_ADraftOfAChoiceSurvivesAModelRebuild is item 5's own claim extended
// to the two new kinds: the draft's Choices, not just its Values.
func TestPick_ADraftOfAChoiceSurvivesAModelRebuild(t *testing.T) {
	t.Parallel()

	f := newFake(3)
	drafts := tempDrafts(t)
	iss := readIssue(t, f, "PROJ-1")
	want := otherPriority(iss)
	p, _ := openPickable(t, f, "PROJ-1", fakePriorityOptions, withDrafts(drafts))

	rowAt(t, p, "priority")
	p.keys("enter")
	p.editor().pick.cursor = pickOptionByID(t, p.editor().pick.ranked, want.ID)
	p.keys("enter")
	row := p.editor().rowByID("priority")
	if !row.dirty() {
		t.Fatal("the priority row never became dirty")
	}
	wantID, wantValue := row.chosenID, row.value

	allEditableWithChoices(f, "PROJ-1", fakePriorityOptions...)
	next := newPanel(t, New(testDeps(f), readIssue(t, f, "PROJ-1"), withDrafts(drafts)), 100, 30)
	next.send(loadedMsg{gen: next.editor().gen, issue: readIssue(t, f, "PROJ-1")})

	if !next.editor().draftRestored {
		t.Error("the rebuilt pane does not know its dirty set came from a draft")
	}
	restored := next.editor().rowByID("priority")
	if restored.chosenID != wantID || restored.value != wantValue {
		t.Fatalf("restored priority = (%q, %q), want (%q, %q)", restored.chosenID, restored.value, wantID, wantValue)
	}
}

// TestPick_ClickingACandidateChoosesIt is item 7's own claim: every entry in
// the inline list is a zone, the same as the sidebar's own rows are.
func TestPick_ClickingACandidateChoosesIt(t *testing.T) {
	t.Parallel()

	f := newFake(3)
	iss := readIssue(t, f, "PROJ-1")
	want := otherPriority(iss)
	allEditableWithChoices(f, "PROJ-1", fakePriorityOptions...)
	d := testDeps(record(f))
	p := newPanel(t, New(d, readIssue(t, f, "PROJ-1"), withDrafts(tempDrafts(t))), 100, 30)
	p.send(loadedMsg{gen: p.editor().gen, issue: readIssue(t, f, "PROJ-1")})
	p.editor().focus = regionDetails

	rowAt(t, p, "priority")
	p.keys("enter")
	at := p.zoneAt(d, pickZone("priority", want.ID))
	p.clickAt(at)

	row := p.editor().rowByID("priority")
	if row.chosenID != want.ID || !row.dirty() {
		t.Fatalf("clicking the candidate did not choose it: chosenID=%q dirty=%v", row.chosenID, row.dirty())
	}
	if p.editor().stage != sideBrowse {
		t.Error("choosing a candidate by click did not close the inline list")
	}
}

// TestPick_InlineListGolden is the golden this packet owes docs/UX.md: an
// inline list open, beneath its row, on the pane the reader is already on.
func TestPick_InlineListGolden(t *testing.T) {
	f := newFake(3)
	allEditableWithChoices(f, "PROJ-1", fakePriorityOptions...)
	dr := newDriver(t, testDeps(f), seedOf(t, f, "PROJ-1"), 100, 28)
	dr.send(loadedMsg{gen: dr.m.gen, issue: readIssue(t, f, "PROJ-1")})
	dr.m.focus = regionDetails
	for i, cr := range dr.m.sideRows {
		if cr.id == "priority" {
			dr.m.cursor = i
		}
	}
	dr.key("enter")
	golden(t, "pick_inline_100x28.golden", dr.view())
}
