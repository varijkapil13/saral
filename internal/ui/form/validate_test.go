package form

import (
	"strings"
	"testing"
	"time"

	"github.com/varijkapil13/saral/pkg/jira"
)

func required(m jira.FieldMeta) jira.FieldMeta {
	m.Required = true
	return m
}

func TestValidate_AnswersWhatIsWrongWithAValue(t *testing.T) {
	t.Parallel()

	cascading := meta("c", "Scope", jira.FieldSchema{Type: "option-with-child"},
		option("1", "Tier One", option("11", "Pilot")),
		option("2", "Tier Two"),
	)
	choice := meta("c", "Phase", jira.FieldSchema{Type: "option"}, option("1", "One"), option("2", "Two"))

	tests := []struct {
		name  string
		build func() *field
		wants string
	}{
		{
			name: "a required field left empty",
			build: func() *field {
				return newField(required(meta("summary", "Summary", jira.FieldSchema{Type: "string"})), time.UTC)
			},
			wants: "this field is required",
		},
		{
			name: "a required field Jira fills in itself",
			build: func() *field {
				m := required(meta("reporter", "Reporter", jira.FieldSchema{Type: "user", System: "reporter"}))
				m.HasDefault = true
				return newField(m, time.UTC)
			},
			wants: "",
		},
		{
			name:  "an optional field left empty",
			build: func() *field { return newField(meta("summary", "Summary", jira.FieldSchema{Type: "string"}), time.UTC) },
			wants: "",
		},
		{
			name: "a number that is not one",
			build: func() *field {
				f := newField(meta("c", "Points", jira.FieldSchema{Type: "number"}), time.UTC)
				f.text = "quite a few"
				return f
			},
			wants: "is not a number",
		},
		{
			name: "a number that is one",
			build: func() *field {
				f := newField(meta("c", "Points", jira.FieldSchema{Type: "number"}), time.UTC)
				f.text = "13"
				return f
			},
			wants: "",
		},
		{
			name: "a date that is not one",
			build: func() *field {
				f := newField(meta("duedate", "Due", jira.FieldSchema{Type: "date"}), time.UTC)
				f.text = "next tuesday"
				return f
			},
			wants: "is not a date",
		},
		{
			name: "a date written the way Jira writes one",
			build: func() *field {
				f := newField(meta("duedate", "Due", jira.FieldSchema{Type: "date"}), time.UTC)
				f.text = "2026-03-27"
				return f
			},
			wants: "",
		},
		{
			name: "a date and time that is not one",
			build: func() *field {
				f := newField(meta("c", "Cutover", jira.FieldSchema{Type: "datetime"}), time.UTC)
				f.text = "half nine"
				return f
			},
			wants: "is not a date and time",
		},
		{
			name: "a date and time with no offset, read in the account zone",
			build: func() *field {
				f := newField(meta("c", "Cutover", jira.FieldSchema{Type: "datetime"}), time.FixedZone("test", 3600))
				f.text = "2026-03-27 09:30"
				return f
			},
			wants: "",
		},
		{
			name: "a value the field does not allow",
			build: func() *field {
				f := newField(choice, time.UTC)
				f.picked = []jira.Option{option("9", "Nine")}
				return f
			},
			wants: "is not one of the values this field allows",
		},
		{
			name: "a value the field does allow",
			build: func() *field {
				f := newField(choice, time.UTC)
				f.picked = []jira.Option{option("2", "Two")}
				return f
			},
			wants: "",
		},
		{
			name: "one bad value among several good ones",
			build: func() *field {
				m := meta("c", "Checks", jira.FieldSchema{Type: "array", Items: "option"}, option("1", "One"), option("2", "Two"))
				f := newField(m, time.UTC)
				f.picked = []jira.Option{option("1", "One"), option("9", "Nine")}
				return f
			},
			wants: "is not one of the values this field allows",
		},
		{
			name: "a second level that does not hang off the first",
			build: func() *field {
				f := newField(cascading, time.UTC)
				f.picked = []jira.Option{option("2", "Tier Two", option("11", "Pilot"))}
				return f
			},
			wants: "is not one of the values this field allows",
		},
		{
			name: "a second level that does",
			build: func() *field {
				f := newField(cascading, time.UTC)
				f.picked = []jira.Option{option("1", "Tier One", option("11", "Pilot"))}
				return f
			},
			wants: "",
		},
		{
			name: "an issue key that is not one",
			build: func() *field {
				f := newField(meta("parent", "Parent", jira.FieldSchema{Type: "issuelink", System: "parent"}), time.UTC)
				f.text = "the epic"
				return f
			},
			wants: "is not an issue key",
		},
		{
			name: "an issue key that is one",
			build: func() *field {
				f := newField(meta("parent", "Parent", jira.FieldSchema{Type: "issuelink", System: "parent"}), time.UTC)
				f.text = "PROJ-142"
				return f
			},
			wants: "",
		},
		{
			name: "a person with no account id behind them",
			build: func() *field {
				f := newField(meta("assignee", "Assignee", jira.FieldSchema{Type: "user", System: "assignee"}), time.UTC)
				f.picked = []jira.Option{{Label: "Someone"}}
				return f
			},
			wants: "has no account id",
		},
		{
			name: "markdown that is not a document",
			build: func() *field {
				f := newField(meta("description", "Description", jira.FieldSchema{Type: "doc"}), time.UTC)
				f.text = "> | a | table |\n> | - | - |\n> | in | a quote |"
				return f
			},
			wants: "ADF cannot nest these",
		},
		{
			name: "a shape this form does not understand, which is kept as typed",
			build: func() *field {
				f := newField(meta("c", "Rank", jira.FieldSchema{Type: "any"}), time.UTC)
				f.text = "0|i0004j:"
				return f
			},
			wants: "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			got := tt.build().validate()
			switch {
			case tt.wants == "" && got != "":
				t.Errorf("the value was refused: %s", got)
			case tt.wants != "" && !strings.Contains(got, tt.wants):
				t.Errorf("the field says %q, want it to say %q", got, tt.wants)
			}
		})
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

			at, err := parseDateTime(tt.text, east)
			if err != nil {
				t.Fatalf("reading %q: %v", tt.text, err)
			}
			if got := at.UTC().Format("2006-01-02 15:04"); got != tt.utc {
				t.Errorf("%q reads as %s in UTC, want %s", tt.text, got, tt.utc)
			}
		})
	}
}
