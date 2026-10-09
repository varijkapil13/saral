package move

import (
	"slices"
	"testing"

	"github.com/varijkapil13/saral/pkg/jira"
)

func names(rows []Remap) []string {
	out := make([]string, 0, len(rows))
	for i := range rows {
		out = append(out, rows[i].From.Name)
	}
	return out
}

// The default has to come from the status category and never from the display
// name. Both are on offer here and they disagree: one measured site had four
// pairs of distinct status ids sharing a name, and a German site translates every
// name while the category key stays the same three words.
func TestDefaultRemap_ComesFromTheCategoryAndNotFromTheName(t *testing.T) {
	t.Parallel()
	source := []jira.Issue{{
		Key:    "PROJ-1",
		Status: jira.Status{ID: "10202", Name: "Building", Category: jira.CategoryInProgress},
	}}
	targets := []jira.Status{
		{ID: "20001", Name: "Building", Category: jira.CategoryDone},
		{ID: "20002", Name: "Werkstatt", Category: jira.CategoryInProgress},
	}

	rows := DefaultRemap(SourceStatuses(source), targets)
	if len(rows) != 1 {
		t.Fatalf("one source status became %d rows", len(rows))
	}
	got := targets[rows[0].To]
	if got.ID != "20002" {
		t.Errorf("Building was pointed at %s (%q, category %v); the only target in the source's category "+
			"is 20002, so this default came from the name", got.ID, got.Name, got.Category)
	}
}

// A target workflow with nothing in the source's category still has to offer
// something, and the row says so by being on screen for the user to change.
func TestDefaultRemap_FallsBackToTheFirstTargetWhenNoCategoryMatches(t *testing.T) {
	t.Parallel()
	source := []jira.Issue{{Status: jira.Status{ID: "1", Name: "Triage", Category: jira.CategoryToDo}}}
	targets := []jira.Status{{ID: "9", Name: "Shipped", Category: jira.CategoryDone}}

	rows := DefaultRemap(SourceStatuses(source), targets)
	if rows[0].To != 0 {
		t.Errorf("the fallback landed on %d, want the first target", rows[0].To)
	}

	none := DefaultRemap(SourceStatuses(source), nil)
	if none[0].To != -1 {
		t.Errorf("a target with no statuses left the row pointing at %d rather than at nothing", none[0].To)
	}
}

// Two statuses that share a display name are two rows, because they are two
// statuses. Collapsing them is how a remap silently moves half the selection to
// the wrong place.
func TestSourceStatuses_AreDistinctByIdAndNotByName(t *testing.T) {
	t.Parallel()
	issues := []jira.Issue{
		{Status: jira.Status{ID: "10202", Name: "Building", Category: jira.CategoryInProgress}},
		{Status: jira.Status{ID: "10204", Name: "Building", Category: jira.CategoryInProgress}},
		{Status: jira.Status{ID: "10202", Name: "Building", Category: jira.CategoryInProgress}},
	}
	rows := SourceStatuses(issues)
	if len(rows) != 2 {
		t.Fatalf("three issues on two status ids became %d rows: %v", len(rows), names(rows))
	}
	if rows[0].From.ID != "10202" || rows[0].Count != 2 {
		t.Errorf("the first row is %s with %d issues, want 10202 with 2", rows[0].From.ID, rows[0].Count)
	}
	if rows[1].From.ID != "10204" || rows[1].Count != 1 {
		t.Errorf("the second row is %s with %d issues, want 10204 with 1", rows[1].From.ID, rows[1].Count)
	}
}

// The workflow is per issue type, so the same project answers differently for two
// of them and the lookup has to be by type id.
func TestStatusesFor_AnswersPerIssueTypeAndNotPerProject(t *testing.T) {
	t.Parallel()
	vocab := []jira.IssueTypeStatuses{
		{Type: jira.IssueType{ID: "10301"}, Statuses: []jira.Status{{ID: "a"}}},
		{Type: jira.IssueType{ID: "10305"}, Statuses: []jira.Status{{ID: "b"}}},
	}
	if got := StatusesFor(vocab, "10305"); len(got) != 1 || got[0].ID != "b" {
		t.Errorf("the subtask type's workflow came back as %v", got)
	}
	if got := StatusesFor(vocab, "nosuch"); got != nil {
		t.Errorf("a type this project does not run answered with %v rather than nothing", got)
	}
}

func TestMandatory_IsWhateverTheTargetSaysAtRuntime(t *testing.T) {
	t.Parallel()
	ref := func(id, name string) jira.FieldRef { return jira.FieldRef{ID: id, Name: name} }
	schema := jira.Schema{
		Fields: []jira.FieldMeta{
			{Field: ref("summary", "Summary"), Name: "Summary", Required: true},
			{Field: ref("project", "Project"), Name: "Project", Required: true},
			{Field: ref("issuetype", "Issue Type"), Name: "Issue Type", Required: true},
			{Field: ref("status", "Status"), Name: "Status", Required: true},
			{Field: ref("customfield_1", "Erfassungsart"), Name: "Erfassungsart", Required: true,
				AllowedValues: []jira.Option{{ID: "1", Label: "Eins"}, {ID: "2", Label: "Zwei"}}},
			{Field: ref("customfield_2", "Kostenstelle"), Name: "Kostenstelle", Required: true, HasDefault: true},
			{Field: ref("customfield_3", "Notiz"), Name: "Notiz"},
		},
	}
	got := Mandatory(schema)
	if len(got) != 2 {
		t.Fatalf("the target insists on %d fields a move has to reckon with, want 2: %v", len(got), fieldNames(got))
	}
	if got[0].Meta.Field.ID != "customfield_1" || got[1].Meta.Field.ID != "customfield_2" {
		t.Errorf("the fields left over are %v", fieldNames(got))
	}
	for i := range got {
		if !got[i].Retains() {
			t.Errorf("%s starts out being written rather than kept from the source", got[i].Meta.Field.ID)
		}
	}
	if Written(got) {
		t.Error("a group nobody has touched reports that something is being written")
	}
}

func fieldNames(fields []Pending) []string {
	out := make([]string, 0, len(fields))
	for i := range fields {
		out = append(out, fields[i].Meta.Field.ID)
	}
	return out
}

func TestTooMany_IsAboveWhatOneMoveTakes(t *testing.T) {
	t.Parallel()
	if TooMany(MaxKeys) {
		t.Errorf("%d issues was refused and the endpoint takes it", MaxKeys)
	}
	if !TooMany(MaxKeys + 1) {
		t.Errorf("%d issues was accepted and the endpoint takes %d", MaxKeys+1, MaxKeys)
	}
}

func TestUnmapped_FindsTheFirstRowWithNowhereToLand(t *testing.T) {
	t.Parallel()
	targets := []jira.Status{{ID: "a"}, {ID: "b"}}
	if row, blocked := Unmapped([]Remap{{To: 0}}, nil); !blocked || row != -1 {
		t.Errorf("a target with no statuses answered row %d, blocked %v", row, blocked)
	}
	if _, blocked := Unmapped([]Remap{{To: 0}, {To: 1}}, targets); blocked {
		t.Error("a whole mapping was blocked")
	}
	for _, to := range []int{-1, 2} {
		if row, blocked := Unmapped([]Remap{{To: 0}, {To: to}}, targets); !blocked || row != 1 {
			t.Errorf("a row landing on %d answered row %d, blocked %v", to, row, blocked)
		}
	}
}

func TestHalfAnswered_IsAGroupWithOneValueAndOneKept(t *testing.T) {
	t.Parallel()
	opts := []jira.Option{{ID: "1", Label: "Eins"}}
	if _, blocked := HalfAnswered([]Pending{{Options: opts, Chosen: -1}, {Chosen: -1}}); blocked {
		t.Error("a group left alone was blocked")
	}
	if _, blocked := HalfAnswered([]Pending{{Options: opts, Chosen: 0}, {Options: opts, Chosen: 0}}); blocked {
		t.Error("a group answered whole was blocked")
	}
	row, blocked := HalfAnswered([]Pending{{Options: opts, Chosen: 0}, {Chosen: -1}})
	if !blocked || row != 1 {
		t.Errorf("a half-answered group answered row %d, blocked %v", row, blocked)
	}
}

func TestCycle_WrapsAndAFieldCanAlwaysBePutBackToBeingKept(t *testing.T) {
	t.Parallel()
	r := Remap{To: -1}
	r.Cycle(1, 3)
	if r.To != 1 {
		t.Errorf("an unmapped row stepped to %d", r.To)
	}
	r.Cycle(-2, 3)
	if r.To != 2 {
		t.Errorf("stepping back past the first wrapped to %d", r.To)
	}

	p := Pending{Options: []jira.Option{{ID: "1"}, {ID: "2"}}, Chosen: -1}
	seen := make([]int, 0, 3)
	for range 3 {
		p.Cycle(1)
		seen = append(seen, p.Chosen)
	}
	if !slices.Equal(seen, []int{0, 1, -1}) {
		t.Errorf("stepping through two values went %v", seen)
	}
	p.Cycle(-1)
	if p.Chosen != 1 {
		t.Errorf("stepping back from kept went to %d", p.Chosen)
	}
	empty := Pending{Chosen: -1}
	empty.Cycle(1)
	if !empty.Retains() {
		t.Error("a field with no values to offer was given one")
	}
}

func TestPlanRequest_IsTheWholeMappingAndEveryValueOrNone(t *testing.T) {
	t.Parallel()
	opts := []jira.Option{{ID: "1", Label: "Eins"}}
	p := Plan{
		Issues:  []jira.Issue{{Key: "PROJ-1"}, {Key: "PROJ-2"}},
		Target:  "OTHER",
		TypeID:  "10001",
		Targets: []jira.Status{{ID: "t1"}, {ID: "t2"}},
		Remaps:  []Remap{{From: jira.Status{ID: "s1"}, To: 1}, {From: jira.Status{ID: "s2"}, To: 0}},
		Fields: []Pending{
			{Meta: jira.FieldMeta{Field: jira.FieldRef{ID: "customfield_1"}}, Options: opts, Chosen: -1},
			{Meta: jira.FieldMeta{Field: jira.FieldRef{ID: "customfield_2"}}, Options: opts, Chosen: -1},
		},
		Notify: true,
	}
	in := p.Request()
	if !slices.Equal(in.Keys, []string{"PROJ-1", "PROJ-2"}) || in.TargetProjectKey != "OTHER" ||
		in.TargetIssueTypeID != "10001" || !in.Notify {
		t.Errorf("the request is %+v", in)
	}
	want := []jira.StatusMapping{{FromStatusID: "s1", ToStatusID: "t2"}, {FromStatusID: "s2", ToStatusID: "t1"}}
	if !slices.Equal(in.StatusMap, want) {
		t.Errorf("the status map is %v, want %v", in.StatusMap, want)
	}
	if in.Fields.Len() != 0 {
		t.Errorf("a group left alone sent %d values", in.Fields.Len())
	}

	p.Fields[0].Chosen = 0
	if got := p.Request().Fields.Len(); got != 2 {
		t.Errorf("one value named sent %d values; the group is sent whole", got)
	}
}
