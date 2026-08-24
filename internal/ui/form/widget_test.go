package form

import (
	"bytes"
	"strings"
	"testing"
	"time"

	"github.com/varijkapil13/saral/pkg/adf"
	"github.com/varijkapil13/saral/pkg/jira"
)

func meta(id, name string, schema jira.FieldSchema, allowed ...jira.Option) jira.FieldMeta {
	return jira.FieldMeta{
		Field:         jira.FieldRef{ID: id, Name: name, Schema: schema},
		Name:          name,
		Operations:    []string{"set"},
		AllowedValues: allowed,
	}
}

func option(id, label string, children ...jira.Option) jira.Option {
	return jira.Option{ID: id, Label: label, Children: children}
}

func TestWidgetFor_PicksTheEditorTheSchemaEarns(t *testing.T) {
	t.Parallel()

	options := []jira.Option{option("1", "One"), option("2", "Two")}
	tests := []struct {
		name string
		meta jira.FieldMeta
		want kind
	}{
		{"a single line of text", meta("summary", "Summary", jira.FieldSchema{Type: "string", System: "summary"}), kindText},
		{"a description, which v3 stores as a document", meta("description", "Description", jira.FieldSchema{Type: "string", System: "description"}), kindDoc},
		{"an environment, which is a document too", meta("environment", "Environment", jira.FieldSchema{Type: "string", System: "environment"}), kindDoc},
		{"a multi-line custom field, by its type URI", meta("customfield_1", "Notes", jira.FieldSchema{Type: "string", Custom: "com.atlassian.jira.plugin.system.customfieldtypes:textarea"}), kindDoc},
		{"a field the site already types as a document", meta("customfield_2", "Notes", jira.FieldSchema{Type: "doc"}), kindDoc},
		{"a number", meta("customfield_3", "Points", jira.FieldSchema{Type: "number", Custom: "x:float"}), kindNumber},
		{"a date", meta("duedate", "Due date", jira.FieldSchema{Type: "date", System: "duedate"}), kindDate},
		{"a date and time", meta("customfield_4", "Cutover", jira.FieldSchema{Type: "datetime", Custom: "x:datetime"}), kindDateTime},
		{"a single select", meta("customfield_5", "Phase", jira.FieldSchema{Type: "option", Custom: "x:select"}, options...), kindSelect},
		{"a priority, which is a select of Jira's own", meta("priority", "Priority", jira.FieldSchema{Type: "priority", System: "priority"}, options...), kindSelect},
		{"a cascading select", meta("customfield_6", "Scope", jira.FieldSchema{Type: "option-with-child", Custom: "x:cascadingselect"}, options...), kindCascade},
		{"a multi select", meta("customfield_7", "Checks", jira.FieldSchema{Type: "array", Items: "option", Custom: "x:multicheckboxes"}, options...), kindMultiSelect},
		{"fix versions, which is a multi select of versions", meta("fixVersions", "Fix versions", jira.FieldSchema{Type: "array", Items: "version", System: "fixVersions"}, options...), kindMultiSelect},
		{"a person", meta("assignee", "Assignee", jira.FieldSchema{Type: "user", System: "assignee"}), kindUser},
		{"several people", meta("customfield_8", "Reviewers", jira.FieldSchema{Type: "array", Items: "user", Custom: "x:people"}), kindUsers},
		{"labels", meta("labels", "Labels", jira.FieldSchema{Type: "array", Items: "string", System: "labels"}), kindLabels},
		{"a parent, which is an issue", meta("parent", "Parent", jira.FieldSchema{Type: "issuelink", System: "parent"}), kindIssueKey},
		{"a select with no values stated", meta("customfield_9", "Phase", jira.FieldSchema{Type: "option", Custom: "x:select"}), kindOther},
		{"an array of something this form has no editor for", meta("customfield_10", "Sprint", jira.FieldSchema{Type: "array", Items: "json", Custom: "x:gh-sprint"}), kindOther},
		{"a field whose plugin declared no type at all", meta("customfield_11", "Rank", jira.FieldSchema{Type: "any", Custom: "x:gh-lexo-rank"}), kindOther},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			if got := widgetFor(tt.meta); got != tt.want {
				t.Errorf("%s got a %v, want a %v", tt.meta.Field.ID, got, tt.want)
			}
		})
	}
}

func TestWidgetFor_IgnoresTheDisplayNameEntirely(t *testing.T) {
	t.Parallel()

	// The same field on a German site. Nothing about the widget may change.
	english := meta("customfield_5", "Release Level", jira.FieldSchema{Type: "option", Custom: "x:select"}, option("1", "One"))
	german := meta("customfield_5", "Freigabestufe", jira.FieldSchema{Type: "option", Custom: "x:select"}, option("1", "Eins"))

	if widgetFor(english) != widgetFor(german) {
		t.Errorf("the widget changed with the language: %v and %v", widgetFor(english), widgetFor(german))
	}
}

func TestOffer_KeepsAFieldOffTheFormWithAReason(t *testing.T) {
	t.Parallel()

	typ := jira.IssueType{ID: "10001", Name: "Task"}
	tests := []struct {
		name  string
		meta  jira.FieldMeta
		want  bool
		about string
	}{
		{
			name: "a field with no operations at all",
			meta: jira.FieldMeta{Field: jira.FieldRef{ID: "issuekey"}, Name: "Key", Operations: nil},
			want: false, about: "set while an issue is being created",
		},
		{
			name: "a field that may only be copied",
			meta: jira.FieldMeta{Field: jira.FieldRef{ID: "customfield_1"}, Name: "Reported", Operations: []string{"copy"}},
			want: false, about: "set while an issue is being created",
		},
		{
			name: "the project, which the form was opened for",
			meta: meta("project", "Project", jira.FieldSchema{Type: "project", System: "project"}),
			want: false, about: "PROJ",
		},
		{
			name: "the issue type, which the form was opened for",
			meta: meta("issuetype", "Issue Type", jira.FieldSchema{Type: "issuetype", System: "issuetype"}),
			want: false, about: "Task",
		},
		{
			name: "attachments",
			meta: meta("attachment", "Attachment", jira.FieldSchema{Type: "array", Items: "attachment", System: "attachment"}),
			want: false, about: "once the issue exists",
		},
		{
			name: "issue links",
			meta: jira.FieldMeta{
				Field:      jira.FieldRef{ID: "issuelinks", Schema: jira.FieldSchema{Type: "array", Items: "issuelinks", System: "issuelinks"}},
				Name:       "Linked Issues",
				Operations: []string{"add", "copy"},
			},
			want: false, about: "from the issue itself",
		},
		{
			name: "an ordinary field",
			meta: meta("summary", "Summary", jira.FieldSchema{Type: "string", System: "summary"}),
			want: true,
		},
		{
			name: "an array a value can only be added to",
			meta: jira.FieldMeta{
				Field:      jira.FieldRef{ID: "labels", Schema: jira.FieldSchema{Type: "array", Items: "string", System: "labels"}},
				Name:       "Labels",
				Operations: []string{"add"},
			},
			want: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			got, reason := offer(tt.meta, "PROJ", typ)
			if got != tt.want {
				t.Fatalf("offer = %v with reason %q, want %v", got, reason, tt.want)
			}
			if tt.want {
				return
			}
			if !strings.Contains(reason, tt.about) {
				t.Errorf("the reason reads %q, want it to mention %q", reason, tt.about)
			}
		})
	}
}

func TestField_TurnsWhatWasTypedIntoTheValueThePortCarries(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name  string
		build func() *field
		want  jira.FieldKind
		check func(*testing.T, jira.FieldValue)
	}{
		{
			name: "text",
			build: func() *field {
				f := newField(meta("summary", "Summary", jira.FieldSchema{Type: "string"}), time.UTC)
				f.text = " a summary "
				return f
			},
			want: jira.KindText,
			check: func(t *testing.T, v jira.FieldValue) {
				if v.Text != "a summary" {
					t.Errorf("the text reads %q, want it trimmed", v.Text)
				}
			},
		},
		{
			name: "a number",
			build: func() *field {
				f := newField(meta("c", "Points", jira.FieldSchema{Type: "number"}), time.UTC)
				f.text = "3.5"
				return f
			},
			want: jira.KindNumber,
			check: func(t *testing.T, v jira.FieldValue) {
				if v.Number != 3.5 {
					t.Errorf("the number reads %v", v.Number)
				}
			},
		},
		{
			name: "a date",
			build: func() *field {
				f := newField(meta("d", "Due", jira.FieldSchema{Type: "date"}), time.UTC)
				f.text = "2026-03-27"
				return f
			},
			want: jira.KindDate,
			check: func(t *testing.T, v jira.FieldValue) {
				if v.Date.String() != "2026-03-27" {
					t.Errorf("the date reads %q", v.Date)
				}
			},
		},
		{
			name: "a date and time, in the account timezone",
			build: func() *field {
				loc := time.FixedZone("test", 2*60*60)
				f := newField(meta("d", "Cutover", jira.FieldSchema{Type: "datetime"}), loc)
				f.text = "2026-03-27 09:30"
				return f
			},
			want: jira.KindTime,
			check: func(t *testing.T, v jira.FieldValue) {
				if v.Time.UTC().Format("15:04") != "07:30" {
					t.Errorf("the instant reads %s, want the typed time read in the account zone", v.Time.UTC())
				}
			},
		},
		{
			name: "labels, which are values and not a sentence",
			build: func() *field {
				f := newField(meta("labels", "Labels", jira.FieldSchema{Type: "array", Items: "string", System: "labels"}), time.UTC)
				f.text = "infra, flaky  infra"
				return f
			},
			want: jira.KindOptions,
			check: func(t *testing.T, v jira.FieldValue) {
				if len(v.Options) != 2 || v.Options[0].Label != "infra" || v.Options[1].Label != "flaky" {
					t.Errorf("the labels read %+v, want two, deduplicated", v.Options)
				}
			},
		},
		{
			name: "a cascading select, which carries both levels",
			build: func() *field {
				f := newField(meta("c", "Scope", jira.FieldSchema{Type: "option-with-child"}, option("1", "Tier One", option("11", "Pilot"))), time.UTC)
				f.picked = []jira.Option{option("1", "Tier One", option("11", "Pilot"))}
				return f
			},
			want: jira.KindOption,
			check: func(t *testing.T, v jira.FieldValue) {
				if len(v.Options) != 1 || len(v.Options[0].Children) != 1 || v.Options[0].Children[0].ID != "11" {
					t.Errorf("the value reads %+v, want the second level under the first", v.Options)
				}
			},
		},
		{
			name: "people, by account id",
			build: func() *field {
				f := newField(meta("c", "Reviewers", jira.FieldSchema{Type: "array", Items: "user"}), time.UTC)
				f.picked = []jira.Option{option("acc-1", "Someone"), option("acc-2", "Someone Else")}
				return f
			},
			want: jira.KindUsers,
			check: func(t *testing.T, v jira.FieldValue) {
				if len(v.Users) != 2 || v.Users[0].AccountID != "acc-1" {
					t.Errorf("the people read %+v, want them by account id", v.Users)
				}
			},
		},
		{
			name: "a shape this form does not understand",
			build: func() *field {
				f := newField(meta("c", "Rank", jira.FieldSchema{Type: "any", Custom: "x:gh-lexo-rank"}), time.UTC)
				f.text = "0|i0004j:"
				return f
			},
			want: jira.KindUnknown,
			check: func(t *testing.T, v jira.FieldValue) {
				if v.Text != "0|i0004j:" {
					t.Errorf("the value reads %q; what was typed was neither kept nor understood", v.Text)
				}
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			got, ok := tt.build().value()
			if !ok {
				t.Fatal("the field reports nothing in it")
			}
			if got.Kind != tt.want {
				t.Fatalf("the value is a %v, want a %v", got.Kind, tt.want)
			}
			tt.check(t, got)
		})
	}
}

func TestField_ReportsNothingForAFieldNobodyFilledIn(t *testing.T) {
	t.Parallel()

	f := newField(meta("summary", "Summary", jira.FieldSchema{Type: "string"}), time.UTC)
	if _, ok := f.value(); ok {
		t.Error("an empty field produced a value, which would write an empty summary")
	}
	f.text = "   "
	if _, ok := f.value(); ok {
		t.Error("a field holding only spaces produced a value")
	}
}

// richDoc is a document carrying the two things markdown has no spelling for:
// a mention's account id and a lozenge's colour.
func richDoc() adf.Doc {
	return adf.NewDoc(
		adf.NewNode("paragraph",
			adf.NewText("Assigned to "),
			adf.NewNode("mention").WithAttrs(adf.Attrs{"id": "acc-42", "text": "@Someone"}),
		),
		adf.NewNode("paragraph", adf.NewText("The second paragraph.")),
	)
}

func TestField_KeepsADocumentNobodyEditedByteForByte(t *testing.T) {
	t.Parallel()

	original := richDoc()
	f := newField(meta("description", "Description", jira.FieldSchema{Type: "string", System: "description"}), time.UTC)
	f.original = original
	f.text = adf.MarkdownWith(original, adf.Options{})

	got, err := f.document()
	if err != nil {
		t.Fatalf("reading the markdown back: %v", err)
	}
	before, err := adf.Marshal(original)
	if err != nil {
		t.Fatal(err)
	}
	after, err := adf.Marshal(got)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(before, after) {
		t.Errorf("a document nobody edited came back changed:\n%s\n%s", before, after)
	}
}

func TestField_KeepsWhatMarkdownCannotCarryInBlocksNobodyTouched(t *testing.T) {
	t.Parallel()

	original := richDoc()
	f := newField(meta("description", "Description", jira.FieldSchema{Type: "string", System: "description"}), time.UTC)
	f.original = original
	f.text = strings.Replace(
		adf.MarkdownWith(original, adf.Options{}),
		"The second paragraph.", "The second paragraph, rewritten.", 1)

	got, err := f.document()
	if err != nil {
		t.Fatalf("reading the markdown back: %v", err)
	}
	if !hasMention(got, "acc-42") {
		t.Error("the account id behind the mention was lost by an edit to another paragraph")
	}
	if !strings.Contains(adf.MarkdownWith(got, adf.Options{}), "rewritten") {
		t.Error("the edit itself did not survive")
	}

	// Without the original there is nothing to restore it from, which is why
	// this field parses into the document it was rendered from.
	loose, err := adf.ParseMarkdown(f.text)
	if err != nil {
		t.Fatal(err)
	}
	if hasMention(loose, "acc-42") {
		t.Fatal("markdown carried an account id, so nothing here is being tested")
	}
}

func TestField_NamesWhatAnEditWouldCostBeforeItIsMade(t *testing.T) {
	t.Parallel()

	f := newField(meta("description", "Description", jira.FieldSchema{Type: "string", System: "description"}), time.UTC)
	if got := f.oneWay(); len(got) != 0 {
		t.Errorf("an empty document warns about %v", got)
	}

	f.original = richDoc()
	warnings := f.oneWay()
	if len(warnings) == 0 {
		t.Fatal("a document with a mention in it warns about nothing")
	}
	for _, warning := range warnings {
		node, _, _ := strings.Cut(warning, ":")
		if node != "mention" {
			t.Errorf("the warning mentions %q, which this document does not have", node)
		}
	}
}

func hasMention(d adf.Doc, id string) bool {
	found := false
	d.Walk(func(n adf.Node) bool {
		if n.Type == "mention" && n.Attrs["id"] == id {
			found = true
		}
		return true
	})
	return found
}
