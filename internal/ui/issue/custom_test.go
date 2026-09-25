package issue

import (
	"context"
	"errors"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/varijkapil13/saral/pkg/adf"
	"github.com/varijkapil13/saral/pkg/jira"
	"github.com/varijkapil13/saral/pkg/jira/jiratest"
)

// These are invented custom fields, one of each shape these tests need, added
// to the fake's own catalogue by newCustomFake.
const (
	pointsID    = "customfield_39101"
	phaseID     = "customfield_39102"
	checklistID = "customfield_39103"
	scopeID     = "customfield_39104"
	reviewersID = "customfield_39105"
	noteID      = "customfield_39106"
	notesDocID  = "customfield_39107"
)

var customCatalogue = []jira.Field{
	{ID: pointsID, Key: pointsID, Name: "Effort", Custom: true, Schema: jira.FieldSchema{Type: "number", Custom: "x:float"}},
	{ID: phaseID, Key: phaseID, Name: "Phase", Custom: true, Schema: jira.FieldSchema{Type: "option", Custom: "x:select"}},
	{ID: checklistID, Key: checklistID, Name: "Checklist", Custom: true, Schema: jira.FieldSchema{Type: "array", Items: "option", Custom: "x:multicheckboxes"}},
	{ID: scopeID, Key: scopeID, Name: "Scope", Custom: true, Schema: jira.FieldSchema{Type: "option-with-child", Custom: "x:cascadingselect"}},
	{ID: reviewersID, Key: reviewersID, Name: "Reviewers", Custom: true, Schema: jira.FieldSchema{Type: "array", Items: "user", Custom: "x:people"}},
	{ID: noteID, Key: noteID, Name: "Note", Custom: true, Schema: jira.FieldSchema{Type: "string", Custom: "x:textfield"}},
	{ID: notesDocID, Key: notesDocID, Name: "Notes", Custom: true, Schema: jira.FieldSchema{Type: "string", Custom: "x:textarea"}},
}

func newCustomFake(t *testing.T) *jiratest.Fake {
	t.Helper()
	base, err := newFake(3).Fields(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	return newFake(3, jiratest.WithFields(append(base, customCatalogue...)))
}

var (
	phaseValues = []jira.Option{{ID: "20001", Label: "Pilot"}, {ID: "20002", Label: "General"}}
	checkValues = []jira.Option{{ID: "20011", Label: "Docs"}, {ID: "20012", Label: "Tests"}, {ID: "20013", Label: "Ops"}}
	scopeValues = []jira.Option{
		{ID: "20021", Label: "Region", Children: []jira.Option{{ID: "20031", Label: "North"}, {ID: "20032", Label: "South"}}},
	}
)

// screenField is the editmeta entry the fake's catalogue field would have on a
// screen that lets it be set.
func screenField(t *testing.T, f *jiratest.Fake, id string, allowed ...jira.Option) jira.FieldMeta {
	t.Helper()
	fields, err := f.Fields(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	at := slices.IndexFunc(fields, func(fl jira.Field) bool { return fl.ID == id })
	if at < 0 {
		t.Fatalf("the fake has no field %s", id)
	}
	return jira.FieldMeta{Field: fields[at].Ref(), Name: fields[at].Name, Operations: []string{"set"}, AllowedValues: allowed}
}

func openCustom(t *testing.T, f *jiratest.Fake, c jira.Client, metas []jira.FieldMeta, opts ...modelOption) *panel {
	t.Helper()
	f.SetEditMeta("PROJ-1", metas...)
	p := newPanel(t, New(testDeps(t, c), readIssue(t, f, "PROJ-1"), opts...), 100, 40)
	p.editor().focus = regionDetails
	return p
}

func seed(t *testing.T, f *jiratest.Fake, values map[string]jira.FieldValue) {
	t.Helper()
	if err := f.UpdateIssue(t.Context(), "PROJ-1", jira.IssuePatch{Fields: jira.NewFieldSet(values)}); err != nil {
		t.Fatal(err)
	}
}

func fieldValue(t *testing.T, f *jiratest.Fake, id string) (jira.FieldValue, bool) {
	t.Helper()
	iss, err := f.Issue(t.Context(), "PROJ-1")
	if err != nil {
		t.Fatal(err)
	}
	return iss.Fields.ByID(id)
}

func TestCustomKindOf(t *testing.T) {
	t.Parallel()

	set := []string{"set"}
	opts := []jira.Option{{ID: "1", Label: "one"}}
	meta := func(s jira.FieldSchema, allowed ...jira.Option) jira.FieldMeta {
		return jira.FieldMeta{Field: jira.FieldRef{ID: "customfield_1", Schema: s}, Operations: set, AllowedValues: allowed}
	}
	cases := []struct {
		name string
		meta jira.FieldMeta
		want customKind
	}{
		{"text", meta(jira.FieldSchema{Type: "string", Custom: "x:textfield"}), ckText},
		{"paragraph", meta(jira.FieldSchema{Type: "string", Custom: "x:textarea"}), ckDoc},
		{"url", meta(jira.FieldSchema{Type: "string", Custom: "x:url"}), ckURL},
		{"number", meta(jira.FieldSchema{Type: "number", Custom: "x:float"}), ckNumber},
		{"date", meta(jira.FieldSchema{Type: "date", Custom: "x:datepicker"}), ckDate},
		{"datetime", meta(jira.FieldSchema{Type: "datetime", Custom: "x:datetime"}), ckDateTime},
		{"labels", meta(jira.FieldSchema{Type: "array", Items: "string", Custom: "x:labels"}), ckLabels},
		{"select", meta(jira.FieldSchema{Type: "option", Custom: "x:select"}, opts...), ckSelect},
		{"select with no values", meta(jira.FieldSchema{Type: "option", Custom: "x:select"}), ckNone},
		{"multi-select", meta(jira.FieldSchema{Type: "array", Items: "option", Custom: "x:multiselect"}, opts...), ckMulti},
		{"cascade", meta(jira.FieldSchema{Type: "option-with-child", Custom: "x:cascadingselect"}, opts...), ckCascade},
		{"user", meta(jira.FieldSchema{Type: "user", Custom: "x:userpicker"}), ckUser},
		{"users", meta(jira.FieldSchema{Type: "array", Items: "user", Custom: "x:people"}), ckUsers},
		{"a json array", meta(jira.FieldSchema{Type: "array", Items: "json", Custom: "x:sprint"}), ckNone},
		{"a system field", meta(jira.FieldSchema{Type: "string", System: "environment"}), ckNone},
		{"no set operation", jira.FieldMeta{Field: jira.FieldRef{Schema: jira.FieldSchema{Type: "string", Custom: "x:textfield"}}}, ckNone},
	}
	for _, tc := range cases {
		if got := customKindOf(tc.meta); got != tc.want {
			t.Errorf("%s: got %d, want %d", tc.name, got, tc.want)
		}
	}
}

func TestCustomParseTyped(t *testing.T) {
	t.Parallel()

	berlin := time.FixedZone("CET", 3600)
	cases := []struct {
		kind    customKind
		text    string
		want    jira.FieldValue
		problem bool
	}{
		{ckNumber, "2,5", jira.FieldValue{Kind: jira.KindNumber, Number: 2.5}, false},
		{ckNumber, "many", jira.FieldValue{}, true},
		{ckDate, "2026-03-01", jira.FieldValue{Kind: jira.KindDate, Date: jira.Date{Year: 2026, Month: 3, Day: 1}}, false},
		{ckDate, "1 March", jira.FieldValue{}, true},
		{ckDateTime, "2026-03-01 09:30", jira.FieldValue{Kind: jira.KindTime, Time: time.Date(2026, 3, 1, 9, 30, 0, 0, berlin)}, false},
		{ckDateTime, "tomorrow", jira.FieldValue{}, true},
		{ckURL, "https://example.invalid/x", jira.FieldValue{Kind: jira.KindText, Text: "https://example.invalid/x"}, false},
		{ckURL, "example.invalid", jira.FieldValue{}, true},
		{ckLabels, "a, b c", jira.FieldValue{Kind: jira.KindOptions, Options: []jira.Option{{Label: "a"}, {Label: "b-c"}}}, false},
		{ckText, "hello", jira.FieldValue{Kind: jira.KindText, Text: "hello"}, false},
	}
	for _, tc := range cases {
		row := fieldRow{custom: tc.kind, loc: berlin}
		got, problem := row.parseTyped(tc.text)
		if (problem != "") != tc.problem {
			t.Errorf("%d %q: problem %q", tc.kind, tc.text, problem)
			continue
		}
		if tc.problem {
			continue
		}
		if got.Kind != tc.want.Kind || got.Text != tc.want.Text || got.Number != tc.want.Number ||
			got.Date != tc.want.Date || !got.Time.Equal(tc.want.Time) || pickedText(got.Options) != pickedText(tc.want.Options) {
			t.Errorf("%d %q: got %+v, want %+v", tc.kind, tc.text, got, tc.want)
		}
	}
}

func TestCustom_NumberFieldIsEditedInPlaceAndSaved(t *testing.T) {
	t.Parallel()

	f := newCustomFake(t)
	rec := record(f)
	p := openCustom(t, f, rec, []jira.FieldMeta{screenField(t, f, pointsID)}, withDrafts(tempDrafts(t)))

	rowAt(t, p, pointsID)
	p.keys("enter")
	if p.editor().stage != sideTyping {
		t.Fatalf("enter on a number field did not open its input: stage %v", p.editor().stage)
	}
	p.editor().input.SetValue("5")
	p.keys("enter", "s")

	patch := rec.lastPatch(t)
	if got := patchFieldNames(patch); !slices.Equal(got, []string{pointsID}) {
		t.Fatalf("the patch sends %v, want only the number field", got)
	}
	if v, _ := fieldValue(t, f, pointsID); v.Kind != jira.KindNumber || v.Number != 5 {
		t.Fatalf("the site holds %+v, want 5", v)
	}
}

func TestCustom_AValueThisFieldCannotHoldIsNeverSent(t *testing.T) {
	t.Parallel()

	f := newCustomFake(t)
	rec := record(f)
	p := openCustom(t, f, rec, []jira.FieldMeta{screenField(t, f, pointsID)}, withDrafts(tempDrafts(t)))

	rowAt(t, p, pointsID)
	p.keys("enter")
	p.editor().input.SetValue("lots")
	p.keys("enter")
	row := p.editor().rowByID(pointsID)
	if row.problem == "" || row.value != "lots" {
		t.Fatalf("problem %q, value %q: want the typed text kept and flagged", row.problem, row.value)
	}
	p.keys("s")
	if rec.writes() != 0 {
		t.Fatal("a value that is not a number was written")
	}
	if !strings.Contains(p.frame(), "write a number") {
		t.Errorf("the row does not say what is wrong:\n%s", p.frame())
	}
}

func TestCustom_SelectChoosesFromTheScreensOwnValues(t *testing.T) {
	t.Parallel()

	f := newCustomFake(t)
	rec := record(f)
	p := openCustom(t, f, rec, []jira.FieldMeta{screenField(t, f, phaseID, phaseValues...)}, withDrafts(tempDrafts(t)))

	rowAt(t, p, phaseID)
	p.keys("enter")
	pick := p.editor().pick
	if pick == nil || pick.kind != rkField || len(pick.ranked) != 3 {
		t.Fatalf("the list is %+v, want None and the two allowed values", pick)
	}
	pick.cursor = pickOptionByID(t, pick.ranked, "20002")
	p.keys("enter", "s")

	v, _ := fieldValue(t, f, phaseID)
	if v.Kind != jira.KindOption || len(v.Options) != 1 || v.Options[0].ID != "20002" {
		t.Fatalf("the site holds %+v, want option 20002 by id", v)
	}
	if calls := f.Calls(); slices.Contains(calls, "FindPeople") {
		t.Errorf("a select asked for people: %v", calls)
	}
}

func TestCustom_MultiSelectTogglesSeveralBeforeClosing(t *testing.T) {
	t.Parallel()

	f := newCustomFake(t)
	seed(t, f, map[string]jira.FieldValue{checklistID: {Kind: jira.KindOptions, Options: []jira.Option{checkValues[0]}}})
	rec := record(f)
	p := openCustom(t, f, rec, []jira.FieldMeta{screenField(t, f, checklistID, checkValues...)}, withDrafts(tempDrafts(t)))

	rowAt(t, p, checklistID)
	p.keys("enter")
	pick := p.editor().pick
	pick.cursor = pickOptionByID(t, pick.ranked, "20011")
	p.keys("enter")
	pick.cursor = pickOptionByID(t, pick.ranked, "20013")
	p.keys("enter")
	if p.editor().stage != sidePicking {
		t.Fatal("toggling one value closed the list")
	}
	if !strings.Contains(p.frame(), "[x] Ops") || !strings.Contains(p.frame(), "[ ] Docs") {
		t.Errorf("the list does not show what is ticked:\n%s", p.frame())
	}
	p.keys("esc", "s")

	v, _ := fieldValue(t, f, checklistID)
	if v.Kind != jira.KindOptions || len(v.Options) != 1 || v.Options[0].ID != "20013" {
		t.Fatalf("the site holds %+v, want only Ops", v)
	}
}

func TestCustom_CascadeWritesTheParentAndTheChild(t *testing.T) {
	t.Parallel()

	f := newCustomFake(t)
	rec := record(f)
	p := openCustom(t, f, rec, []jira.FieldMeta{screenField(t, f, scopeID, scopeValues...)}, withDrafts(tempDrafts(t)))

	rowAt(t, p, scopeID)
	p.keys("enter")
	pick := p.editor().pick
	pick.cursor = pickOptionByID(t, pick.ranked, "20021/20032")
	p.keys("enter")
	if got := p.editor().rowByID(scopeID).display(); got != "Region / South" {
		t.Fatalf("the row reads %q", got)
	}
	p.keys("s")

	patch := rec.lastPatch(t)
	v, _ := patch.Fields.ByID(scopeID)
	if len(v.Options) != 1 || v.Options[0].ID != "20021" || len(v.Options[0].Children) != 1 || v.Options[0].Children[0].ID != "20032" {
		t.Fatalf("the patch carries %+v, want parent 20021 with child 20032", v)
	}
}

func TestCustom_PeopleFieldIsSearchedOnTheSite(t *testing.T) {
	t.Parallel()

	f := newCustomFake(t)
	rec := record(f)
	p := openCustom(t, f, rec, []jira.FieldMeta{screenField(t, f, reviewersID)}, withDrafts(tempDrafts(t)))

	rowAt(t, p, reviewersID)
	p.keys("enter")
	p.typed("gr")
	pick := p.editor().pick
	if pick == nil || !pick.people {
		t.Fatal("a people field did not open a person search")
	}
	pick.cursor = pickOptionByID(t, pick.ranked, "acct-grace")
	p.keys("enter", "esc", "s")

	v, _ := fieldValue(t, f, reviewersID)
	if v.Kind != jira.KindUsers || len(v.Users) != 1 || v.Users[0].AccountID != "acct-grace" {
		t.Fatalf("the site holds %+v, want Grace by account id", v)
	}
}

func TestCustom_EmptyingARowClearsTheField(t *testing.T) {
	t.Parallel()

	f := newCustomFake(t)
	seed(t, f, map[string]jira.FieldValue{noteID: {Kind: jira.KindText, Text: "was here"}})
	rec := record(f)
	p := openCustom(t, f, rec, []jira.FieldMeta{screenField(t, f, noteID)}, withDrafts(tempDrafts(t)))

	rowAt(t, p, noteID)
	p.keys("enter")
	p.editor().input.SetValue("")
	p.keys("enter", "s")
	if got := patchFieldNames(rec.lastPatch(t)); !slices.Equal(got, []string{noteID + " (emptied)"}) {
		t.Fatalf("the patch sends %v", got)
	}
}

func TestCustom_ARequiredFieldCannotBeEmptied(t *testing.T) {
	t.Parallel()

	f := newCustomFake(t)
	seed(t, f, map[string]jira.FieldValue{noteID: {Kind: jira.KindText, Text: "was here"}})
	rec := record(f)
	meta := screenField(t, f, noteID)
	meta.Required = true
	p := openCustom(t, f, rec, []jira.FieldMeta{meta}, withDrafts(tempDrafts(t)))

	rowAt(t, p, noteID)
	p.keys("enter")
	p.editor().input.SetValue("")
	p.keys("enter", "s")
	if rec.writes() != 0 {
		t.Fatal("a required field was emptied")
	}
	if p.editor().rowByID(noteID).problem == "" {
		t.Error("the row does not say why")
	}
}

// refusedWrites writes nothing and answers every UpdateIssue with one error.
type refusedWrites struct {
	jira.Client
	err error
}

func (r refusedWrites) UpdateIssue(context.Context, string, jira.IssuePatch) error { return r.err }

func TestCustom_ARefusedSaveKeepsTheEditAndSaysWhere(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name string
		err  error
		row  bool
	}{
		{"Jira refusedWrites the value", &jira.ValidationError{Fields: []jira.FieldError{{Field: pointsID, Message: "Effort must be under 100"}}}, true},
		{"a capability refusal", &jira.CapabilityError{Reason: "needs Edit issues"}, false},
		{"a rate limit", &jira.RateLimitError{RetryAfter: time.Second}, false},
		{"a transport failure", &jira.TransportError{Op: "PUT issue", Err: errors.New("connection reset")}, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			f := newCustomFake(t)
			drafts := tempDrafts(t)
			p := openCustom(t, f, refusedWrites{Client: f, err: tc.err}, []jira.FieldMeta{screenField(t, f, pointsID)}, withDrafts(drafts))
			rowAt(t, p, pointsID)
			p.keys("enter")
			p.editor().input.SetValue("500")
			p.keys("enter", "s")

			row := p.editor().rowByID(pointsID)
			if !row.dirty() || row.value != "500" {
				t.Fatalf("the edit is gone: %+v", row)
			}
			if tc.row && row.problem != "Effort must be under 100" {
				t.Errorf("the row says %q, want Jira's own words", row.problem)
			}
			if d, ok, _ := drafts.load("example.atlassian.net", "PROJ-1"); !ok || d.Values[pointsID] != "500" {
				t.Errorf("the draft holds %+v, want the edit", d)
			}
		})
	}
}

// A draft outlives the screen read: the rows for custom fields only exist once
// editmeta answers, so what a draft holds for them waits for that.
func TestCustom_ADraftWaitsForTheScreenToList(t *testing.T) {
	t.Parallel()

	f := newCustomFake(t)
	drafts := tempDrafts(t)
	if err := drafts.save(draft{
		Key: "PROJ-1", Site: "example.atlassian.net",
		Picks: map[string][]draftOption{phaseID: {{ID: "20001", Label: "Pilot"}}},
	}); err != nil {
		t.Fatal(err)
	}
	p := openCustom(t, f, record(f), []jira.FieldMeta{screenField(t, f, phaseID, phaseValues...)}, withDrafts(drafts))

	row := p.editor().rowByID(phaseID)
	if row == nil || !row.dirty() || row.display() != "Pilot" {
		t.Fatalf("the drafted choice was not put back: %+v", row)
	}
	if !p.editor().held.isEmpty() {
		t.Errorf("the draft is still held: %+v", p.editor().held)
	}
}

func TestCustom_AFieldThatFailsToListIsNotLostFromTheDraft(t *testing.T) {
	t.Parallel()

	f := newCustomFake(t)
	drafts := tempDrafts(t)
	if err := drafts.save(draft{
		Key: "PROJ-1", Site: "example.atlassian.net",
		Values: map[string]string{pointsID: "8"},
	}); err != nil {
		t.Fatal(err)
	}
	p := openCustom(t, f, record(f), []jira.FieldMeta{{Field: jira.FieldRef{ID: "summary"}}}, withDrafts(drafts))
	editSummary(t, p, "another summary")

	d, ok, err := drafts.load("example.atlassian.net", "PROJ-1")
	if err != nil || !ok || d.Values[pointsID] != "8" || d.Values["summary"] != "another summary" {
		t.Fatalf("the draft holds %+v, want both edits", d)
	}
}

func TestCustom_ParagraphFieldOpensTheTextareaWithMentions(t *testing.T) {
	t.Parallel()

	f := newCustomFake(t)
	rec := record(f)
	meta := screenField(t, f, notesDocID)
	p := openCustom(t, f, rec, []jira.FieldMeta{meta}, withDrafts(tempDrafts(t)), withAfter(rightAway))

	rowAt(t, p, notesDocID)
	p.keys("enter")
	if p.editor().stage != sideDocEdit || p.editor().docRow != notesDocID {
		t.Fatalf("stage %v on %q", p.editor().stage, p.editor().docRow)
	}
	p.typed("ask @gr")
	if !strings.Contains(p.frame(), "@Grace Hopper") {
		t.Fatalf("no suggestion is drawn:\n%s", p.frame())
	}
	p.keys("enter", "ctrl+s", "s")

	v, ok := rec.lastPatch(t).Fields.ByID(notesDocID)
	if !ok || v.Kind != jira.KindDoc {
		t.Fatalf("the patch carries %+v", v)
	}
	body, _ := adf.Marshal(v.Doc)
	if !strings.Contains(string(body), `"type":"mention"`) || !strings.Contains(string(body), "acct-grace") {
		t.Fatalf("the document carries no mention of Grace: %s", body)
	}
}

func TestDescriptionEditor_AtOffersPeopleAndWritesAMention(t *testing.T) {
	t.Parallel()

	f := newCustomFake(t)
	p, rec := openEditable(t, f, "PROJ-1", withDrafts(tempDrafts(t)), withAfter(rightAway))
	p.editor().focus = regionDesc
	p.keys("e")
	p.editor().docArea.SetValue("")
	p.typed("cc @ad")
	if !p.editor().mention.Open() {
		t.Fatal("@ did not open suggestions")
	}
	p.keys("enter", "ctrl+s", "s")

	body, _ := adf.Marshal(*rec.lastPatch(t).Description)
	if !strings.Contains(string(body), `"type":"mention"`) || !strings.Contains(string(body), "acct-ada") {
		t.Fatalf("the description carries no mention: %s", body)
	}
}

func TestCustom_SidebarGolden(t *testing.T) {
	t.Parallel()

	f := newCustomFake(t)
	seed(t, f, map[string]jira.FieldValue{
		pointsID: {Kind: jira.KindNumber, Number: 3},
		phaseID:  {Kind: jira.KindOption, Options: []jira.Option{phaseValues[0]}},
	})
	p := openCustom(t, f, record(f), []jira.FieldMeta{
		screenField(t, f, pointsID),
		screenField(t, f, phaseID, phaseValues...),
		screenField(t, f, checklistID, checkValues...),
	}, withDrafts(tempDrafts(t)))
	rowAt(t, p, pointsID)
	p.keys("enter")
	p.editor().input.SetValue("5")
	p.keys("enter")
	golden(t, "custom_fields_sidebar_100x40.golden", p.frame()+"\n")
}
