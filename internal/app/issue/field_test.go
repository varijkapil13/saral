package issue

import (
	"errors"
	"testing"
	"time"

	"github.com/varijkapil13/saral/pkg/jira"
)

func TestShapeOf_PicksTheEditorTheSchemaEarns(t *testing.T) {
	t.Parallel()

	options := []jira.Option{option("1", "One"), option("2", "Two")}
	tests := []struct {
		name string
		meta jira.FieldMeta
		want Shape
	}{
		{"a single line of text", meta("summary", "Summary", jira.FieldSchema{Type: "string", System: "summary"}), ShapeText},
		{"a description, which v3 stores as a document", meta("description", "Description", jira.FieldSchema{Type: "string", System: "description"}), ShapeDoc},
		{"an environment, which is a document too", meta("environment", "Environment", jira.FieldSchema{Type: "string", System: "environment"}), ShapeDoc},
		{"a multi-line custom field, by its type URI", meta("customfield_1", "Notes", jira.FieldSchema{Type: "string", Custom: "com.atlassian.jira.plugin.system.customfieldtypes:textarea"}), ShapeDoc},
		{"a field the site already types as a document", meta("customfield_2", "Notes", jira.FieldSchema{Type: "doc"}), ShapeDoc},
		{"a number", meta("customfield_3", "Points", jira.FieldSchema{Type: "number", Custom: "x:float"}), ShapeNumber},
		{"a date", meta("duedate", "Due date", jira.FieldSchema{Type: "date", System: "duedate"}), ShapeDate},
		{"a date and time", meta("customfield_4", "Cutover", jira.FieldSchema{Type: "datetime", Custom: "x:datetime"}), ShapeDateTime},
		{"a single select", meta("customfield_5", "Phase", jira.FieldSchema{Type: "option", Custom: "x:select"}, options...), ShapeSelect},
		{"a priority, which is a select of Jira's own", meta("priority", "Priority", jira.FieldSchema{Type: "priority", System: "priority"}, options...), ShapeSelect},
		{"a cascading select", meta("customfield_6", "Scope", jira.FieldSchema{Type: "option-with-child", Custom: "x:cascadingselect"}, options...), ShapeCascade},
		{"a multi select", meta("customfield_7", "Checks", jira.FieldSchema{Type: "array", Items: "option", Custom: "x:multicheckboxes"}, options...), ShapeMultiSelect},
		{"fix versions, which is a multi select of versions", meta("fixVersions", "Fix versions", jira.FieldSchema{Type: "array", Items: "version", System: "fixVersions"}, options...), ShapeMultiSelect},
		{"a person", meta("assignee", "Assignee", jira.FieldSchema{Type: "user", System: "assignee"}), ShapeUser},
		{"several people", meta("customfield_8", "Reviewers", jira.FieldSchema{Type: "array", Items: "user", Custom: "x:people"}), ShapeUsers},
		{"labels", meta("labels", "Labels", jira.FieldSchema{Type: "array", Items: "string", System: "labels"}), ShapeLabels},
		{"a parent, which is an issue", meta("parent", "Parent", jira.FieldSchema{Type: "issuelink", System: "parent"}), ShapeIssueKey},
		{"a select with no values stated", meta("customfield_9", "Phase", jira.FieldSchema{Type: "option", Custom: "x:select"}), ShapeOther},
		{"an array of something this form has no editor for", meta("customfield_10", "Sprint", jira.FieldSchema{Type: "array", Items: "json", Custom: "x:gh-sprint"}), ShapeOther},
		{"a field whose plugin declared no type at all", meta("customfield_11", "Rank", jira.FieldSchema{Type: "any", Custom: "x:gh-lexo-rank"}), ShapeOther},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			if got := ShapeOf(tt.meta); got != tt.want {
				t.Errorf("%s got a %v, want a %v", tt.meta.Field.ID, got, tt.want)
			}
		})
	}
}

func TestShapeOf_IgnoresTheDisplayNameEntirely(t *testing.T) {
	t.Parallel()

	// The same field on a German site. Nothing about the widget may change.
	english := meta("customfield_5", "Release Level", jira.FieldSchema{Type: "option", Custom: "x:select"}, option("1", "One"))
	german := meta("customfield_5", "Freigabestufe", jira.FieldSchema{Type: "option", Custom: "x:select"}, option("1", "Eins"))

	if ShapeOf(english) != ShapeOf(german) {
		t.Errorf("the widget changed with the language: %v and %v", ShapeOf(english), ShapeOf(german))
	}
}

func TestParseDateTime_ReadsEveryFormAFieldAccepts(t *testing.T) {
	t.Parallel()

	east := time.FixedZone("east", 5*3600)
	tests := []struct {
		text string
		utc  string
	}{
		{"2026-03-27T09:30:00.000+0000", "2026-03-27 09:30"},
		{"2026-03-27T09:30:00Z", "2026-03-27 09:30"},
		{"2026-03-27 09:30", "2026-03-27 04:30"},
		{"2026-03-27 09:30:15", "2026-03-27 04:30"},
		{"2026-03-27T09:30", "2026-03-27 04:30"},
	}

	for _, tt := range tests {
		t.Run(tt.text, func(t *testing.T) {
			t.Parallel()

			at, err := ParseDateTime(tt.text, east)
			if err != nil {
				t.Fatalf("reading %q: %v", tt.text, err)
			}
			if got := at.UTC().Format("2006-01-02 15:04"); got != tt.utc {
				t.Errorf("%q reads as %s in UTC, want %s", tt.text, got, tt.utc)
			}
		})
	}
}

func TestCustomOf(t *testing.T) {
	t.Parallel()

	set := []string{"set"}
	opts := []jira.Option{{ID: "1", Label: "one"}}
	meta := func(s jira.FieldSchema, allowed ...jira.Option) jira.FieldMeta {
		return jira.FieldMeta{Field: jira.FieldRef{ID: "customfield_1", Schema: s}, Operations: set, AllowedValues: allowed}
	}
	cases := []struct {
		name string
		meta jira.FieldMeta
		want Custom
	}{
		{"text", meta(jira.FieldSchema{Type: "string", Custom: "x:textfield"}), CustomText},
		{"paragraph", meta(jira.FieldSchema{Type: "string", Custom: "x:textarea"}), CustomDoc},
		{"url", meta(jira.FieldSchema{Type: "string", Custom: "x:url"}), CustomURL},
		{"number", meta(jira.FieldSchema{Type: "number", Custom: "x:float"}), CustomNumber},
		{"date", meta(jira.FieldSchema{Type: "date", Custom: "x:datepicker"}), CustomDate},
		{"datetime", meta(jira.FieldSchema{Type: "datetime", Custom: "x:datetime"}), CustomDateTime},
		{"labels", meta(jira.FieldSchema{Type: "array", Items: "string", Custom: "x:labels"}), CustomLabels},
		{"select", meta(jira.FieldSchema{Type: "option", Custom: "x:select"}, opts...), CustomSelect},
		{"select with no values", meta(jira.FieldSchema{Type: "option", Custom: "x:select"}), CustomNone},
		{"multi-select", meta(jira.FieldSchema{Type: "array", Items: "option", Custom: "x:multiselect"}, opts...), CustomMulti},
		{"cascade", meta(jira.FieldSchema{Type: "option-with-child", Custom: "x:cascadingselect"}, opts...), CustomCascade},
		{"user", meta(jira.FieldSchema{Type: "user", Custom: "x:userpicker"}), CustomUser},
		{"users", meta(jira.FieldSchema{Type: "array", Items: "user", Custom: "x:people"}), CustomUsers},
		{"a json array", meta(jira.FieldSchema{Type: "array", Items: "json", Custom: "x:sprint"}), CustomNone},
		{"a system field", meta(jira.FieldSchema{Type: "string", System: "environment"}), CustomNone},
		{"no set operation", jira.FieldMeta{Field: jira.FieldRef{Schema: jira.FieldSchema{Type: "string", Custom: "x:textfield"}}}, CustomNone},
	}
	for _, tc := range cases {
		if got := CustomOf(tc.meta); got != tc.want {
			t.Errorf("%s: got %d, want %d", tc.name, got, tc.want)
		}
	}
}

func TestEntry_CheckNamesWhatIsWrong(t *testing.T) {
	t.Parallel()
	req := meta("summary", "Summary", jira.FieldSchema{Type: "string"})
	req.Required = true
	choice := meta("c", "Phase", jira.FieldSchema{Type: "option"}, option("1", "One"))
	tests := []struct {
		name  string
		entry Entry
		want  ProblemKind
	}{
		{"required", newEntry(req), ProblemRequired},
		{"number", Entry{Meta: meta("n", "N", jira.FieldSchema{Type: "number"}), Shape: ShapeNumber, Text: "NaN"}, ProblemNotNumber},
		{"date", Entry{Shape: ShapeDate, Text: "soon"}, ProblemNotDate},
		{"date and time", Entry{Shape: ShapeDateTime, Text: "soon"}, ProblemNotDateTime},
		{"issue key", Entry{Shape: ShapeIssueKey, Text: "nope"}, ProblemNotIssueKey},
		{"no account", Entry{Shape: ShapeUser, Picked: []jira.Option{{Label: "Ada"}}}, ProblemNoAccount},
		{"not allowed", Entry{Meta: choice, Shape: ShapeSelect, Picked: []jira.Option{{ID: "9"}}}, ProblemNotAllowed},
		{"fine", Entry{Meta: choice, Shape: ShapeSelect, Picked: []jira.Option{{ID: "1"}}}, ProblemNone},
	}
	for _, tt := range tests {
		if got := tt.entry.Check(); got.Kind != tt.want {
			t.Errorf("%s: %+v, want kind %d", tt.name, got, tt.want)
		}
	}
}

func TestOffer_WithholdsWithAReason(t *testing.T) {
	t.Parallel()
	for name, tc := range map[string]struct {
		meta jira.FieldMeta
		want Withheld
	}{
		"project":    {meta("project", "Project", jira.FieldSchema{System: "project"}), WithheldProject},
		"issue type": {meta("issuetype", "Type", jira.FieldSchema{System: "issuetype"}), WithheldIssueType},
		"read only":  {jira.FieldMeta{Field: jira.FieldRef{ID: "x"}}, WithheldNotSettable},
		"files":      {meta("attachment", "Files", jira.FieldSchema{Type: "array", Items: "attachment"}), WithheldAttachment},
		"links":      {meta("issuelinks", "Links", jira.FieldSchema{Type: "array", Items: "issuelinks"}), WithheldLinks},
		"offered":    {meta("summary", "Summary", jira.FieldSchema{Type: "string"}), WithheldNot},
	} {
		if got := Offer(tc.meta); got != tc.want {
			t.Errorf("%s: %d, want %d", name, got, tc.want)
		}
	}
}

func TestParseTyped_RefusalsAreTheirOwnErrors(t *testing.T) {
	t.Parallel()
	for k, want := range map[Custom]error{CustomNumber: ErrTypedNumber, CustomDate: ErrTypedDate, CustomDateTime: ErrTypedTime, CustomURL: ErrTypedURL} {
		if _, err := ParseTyped(k, "nonsense", nil); !errors.Is(err, want) {
			t.Errorf("%d: %v, want %v", k, err, want)
		}
	}
	v, err := ParseTyped(CustomDateTime, "2026-03-01 09:30", nil)
	if err != nil || !v.Time.Equal(time.Date(2026, 3, 1, 9, 30, 0, 0, time.UTC)) {
		t.Errorf("a time with no zone read as %v, %v", v.Time, err)
	}
}

func TestLabelsAndPicks(t *testing.T) {
	t.Parallel()
	add, remove := LabelDiff([]string{"a", "b"}, SplitLabels("b, c d"))
	if len(add) != 1 || add[0] != "c-d" || len(remove) != 1 || remove[0] != "a" {
		t.Errorf("add %v remove %v", add, remove)
	}
	x, y := jira.Option{ID: "1"}, jira.Option{ID: "1", Children: []jira.Option{{ID: "2"}}}
	if SamePicks([]jira.Option{x}, []jira.Option{y}, false) {
		t.Error("a cascade's second level was ignored")
	}
	if !SamePicks([]jira.Option{x, y}, []jira.Option{y, x}, true) || SamePicks([]jira.Option{x, y}, []jira.Option{y, x}, false) {
		t.Error("order counted where it should not, or not where it should")
	}
}
