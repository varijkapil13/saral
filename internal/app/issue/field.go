package issue

import (
	"errors"
	"fmt"
	"math"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/varijkapil13/saral/pkg/adf"
	"github.com/varijkapil13/saral/pkg/jira"
)

// Shape is what a field on a create screen holds, and so how it is filled in.
// It is decided from the schema the site sent — the type, the element type of
// an array, the system name of a built-in field and the URI of a custom field
// type — and never from a display name, which on a German site is a German word.
type Shape uint8

// The shapes. ShapeOther is the honest answer for a shape this client has no
// editor for: it keeps whatever was typed and says it does not understand it.
const (
	ShapeText Shape = iota
	ShapeDoc
	ShapeNumber
	ShapeDate
	ShapeDateTime
	ShapeSelect
	ShapeMultiSelect
	ShapeCascade
	ShapeUser
	ShapeUsers
	ShapeLabels
	ShapeIssueKey
	ShapeOther
)

// Chooses reports a shape filled from a list rather than typed.
func (k Shape) Chooses() bool {
	switch k {
	case ShapeSelect, ShapeMultiSelect, ShapeCascade, ShapeUser, ShapeUsers:
		return true
	default:
		return false
	}
}

// Multiple reports a shape that holds more than one value.
func (k Shape) Multiple() bool { return k == ShapeMultiSelect || k == ShapeUsers }

// People reports a shape that holds accounts.
func (k Shape) People() bool { return k == ShapeUser || k == ShapeUsers }

// selectable are the schema types whose values come from a list the site
// states. They are Jira's own type names, which are not translated.
var selectable = []string{
	"option", "option-with-child", "priority", "issuetype", "resolution",
	"project", "version", "component", "group", "securitylevel", "status",
}

// ShapeOf is the shape a field's schema earns.
func ShapeOf(meta jira.FieldMeta) Shape {
	schema := meta.Field.Schema
	if isDocument(schema) {
		return ShapeDoc
	}
	if schema.System == "parent" {
		return ShapeIssueKey
	}
	switch schema.Type {
	case "string":
		return ShapeText
	case "number":
		return ShapeNumber
	case "date":
		return ShapeDate
	case "datetime":
		return ShapeDateTime
	case "user":
		return ShapeUser
	case "option-with-child":
		if len(meta.AllowedValues) > 0 {
			return ShapeCascade
		}
		return ShapeOther
	case "array":
		return arrayShape(meta)
	default:
		if slices.Contains(selectable, schema.Type) && len(meta.AllowedValues) > 0 {
			return ShapeSelect
		}
		return ShapeOther
	}
}

func arrayShape(meta jira.FieldMeta) Shape {
	items := meta.Field.Schema.Items
	switch items {
	case "string":
		return ShapeLabels
	case "user":
		return ShapeUsers
	default:
		if slices.Contains(selectable, items) && len(meta.AllowedValues) > 0 {
			return ShapeMultiSelect
		}
		return ShapeOther
	}
}

// isDocument reports a field whose value is an ADF document. The create screen
// declares description and environment as plain strings and a multi-line custom
// field by its own type URI, and v3 stores all three as documents.
func isDocument(schema jira.FieldSchema) bool {
	switch {
	case schema.Type == "doc":
		return true
	case schema.System == "description", schema.System == "environment":
		return true
	default:
		return strings.HasSuffix(schema.Custom, ":textarea")
	}
}

// Withheld is why a create screen's field is not offered.
type Withheld uint8

// The reasons. WithheldNot is a field that is offered.
const (
	WithheldNot Withheld = iota
	WithheldProject
	WithheldIssueType
	WithheldNotSettable
	WithheldAttachment
	WithheldLinks
)

// Offer decides whether a create screen's field is put in front of the user.
// A field silently missing from a form is indistinguishable from a form that
// forgot it, so the answer is a reason, never just no.
func Offer(meta jira.FieldMeta) Withheld {
	schema := meta.Field.Schema
	switch schema.System {
	case "project":
		return WithheldProject
	case "issuetype":
		return WithheldIssueType
	}
	if !canSet(meta.Operations) {
		return WithheldNotSettable
	}
	switch schema.Items {
	case "attachment":
		return WithheldAttachment
	case "issuelinks":
		return WithheldLinks
	}
	return WithheldNot
}

// canSet reports whether the create screen lets this field be given a value at
// all. Jira states the operations per field per issue type, and a field with
// none is on the screen to be read.
func canSet(operations []string) bool {
	return slices.Contains(operations, "set") || slices.Contains(operations, "add")
}

// Entry is one field of a create screen and what has been put in it.
type Entry struct {
	Meta  jira.FieldMeta
	Shape Shape
	// Text is what was typed into a typed field, and the markdown of a document.
	Text string
	// Picked is what was chosen in a field that chooses. A cascading select
	// stores the second level under Children, which is the shape the same
	// field's stored value arrives in.
	Picked []jira.Option
	// Original is the document this field started from. An edit is reconciled
	// against it rather than rebuilt from markdown, which is the only way the
	// parts nobody touched come back as they arrived.
	Original adf.Doc
	// Loc is the account timezone a date and time is read in.
	Loc *time.Location
}

// ID is the field's id.
func (e *Entry) ID() string { return e.Meta.Field.ID }

// Empty reports whether nothing has been put in the field.
func (e *Entry) Empty() bool {
	if e.Shape.Chooses() {
		return len(e.Picked) == 0
	}
	return strings.TrimSpace(e.Text) == ""
}

// Labels splits a labels field the way Jira stores one: separate values, never
// a sentence. A label cannot contain whitespace, so whitespace and commas both
// separate.
func (e *Entry) Labels() []string {
	out := make([]string, 0, 4)
	for _, token := range strings.FieldsFunc(e.Text, func(r rune) bool {
		return r == ',' || r == ' ' || r == '\t' || r == '\n'
	}) {
		if token != "" && !slices.Contains(out, token) {
			out = append(out, token)
		}
	}
	return out
}

// Document reconciles the edited markdown against the document this field
// started from. ParseMarkdownInto reuses the original node for every block that
// was not touched, which is what keeps a mention's account id, a lozenge's
// colour and an unknown node's attributes — none of which markdown carries.
func (e *Entry) Document() (adf.Doc, error) {
	return adf.ParseMarkdownInto(e.Original, e.Text, adf.Options{})
}

// Value turns what is in the field into the tagged value the port carries. The
// second result is false for a field holding nothing, which is not the same as
// a field holding an empty value.
func (e *Entry) Value() (jira.FieldValue, bool) {
	if e.Empty() {
		return jira.FieldValue{}, false
	}
	text := strings.TrimSpace(e.Text)
	switch e.Shape {
	case ShapeNumber:
		number, err := ParseNumber(text)
		if err != nil {
			return jira.FieldValue{}, false
		}
		return jira.FieldValue{Kind: jira.KindNumber, Number: number}, true
	case ShapeDate:
		date, err := jira.ParseDate(text)
		if err != nil {
			return jira.FieldValue{}, false
		}
		return jira.FieldValue{Kind: jira.KindDate, Date: date}, true
	case ShapeDateTime:
		at, err := ParseDateTime(text, e.Loc)
		if err != nil {
			return jira.FieldValue{}, false
		}
		return jira.FieldValue{Kind: jira.KindTime, Time: at}, true
	case ShapeDoc:
		doc, err := e.Document()
		if err != nil {
			return jira.FieldValue{}, false
		}
		return jira.FieldValue{Kind: jira.KindDoc, Doc: doc}, true
	case ShapeLabels:
		options := make([]jira.Option, 0, 4)
		for _, label := range e.Labels() {
			options = append(options, jira.Option{Label: label})
		}
		return jira.FieldValue{Kind: jira.KindOptions, Options: options}, true
	case ShapeSelect, ShapeCascade:
		return jira.FieldValue{Kind: jira.KindOption, Options: slices.Clone(e.Picked)}, true
	case ShapeMultiSelect:
		return jira.FieldValue{Kind: jira.KindOptions, Options: slices.Clone(e.Picked)}, true
	case ShapeUser:
		return jira.FieldValue{Kind: jira.KindUser, Users: e.users()}, true
	case ShapeUsers:
		return jira.FieldValue{Kind: jira.KindUsers, Users: e.users()}, true
	case ShapeOther:
		// Kept rather than guessed at: the value goes back as the text it was
		// typed as, marked as a shape this client does not model.
		return jira.FieldValue{Kind: jira.KindUnknown, Text: text}, true
	default:
		return jira.FieldValue{Kind: jira.KindText, Text: text}, true
	}
}

// users reads a person picker's choices back as accounts. The option id is the
// account id, which is the only identifier Jira accepts on a write.
func (e *Entry) users() []jira.User {
	out := make([]jira.User, 0, len(e.Picked))
	for _, option := range e.Picked {
		out = append(out, jira.User{AccountID: option.ID, DisplayName: option.Label})
	}
	return out
}

// ProblemKind is what is wrong with a field's value.
type ProblemKind uint8

// The problems. ProblemNone is a value with nothing wrong with it.
const (
	ProblemNone ProblemKind = iota
	ProblemRequired
	ProblemNotNumber
	ProblemNotDate
	ProblemNotDateTime
	ProblemNotIssueKey
	ProblemDocument
	ProblemNoAccount
	ProblemNotAllowed
)

// Problem is what is wrong with a field's value: the text that is not what
// the field holds, the option that is not allowed or has no account, or the
// error a document would not parse with.
type Problem struct {
	Kind   ProblemKind
	Text   string
	Option jira.Option
	Err    error
}

// issueKey is the shape every Jira issue key has: a project key, a hyphen and a
// number. It is not a list of the site's projects, which no endpoint on the
// port answers.
var issueKey = regexp.MustCompile(`^[A-Za-z][A-Za-z0-9_]*-\d+$`)

// Check answers what is wrong with this field's value. Every rule comes from
// what the site said about the field: whether it is required, what it holds,
// and which values it allows.
func (e *Entry) Check() Problem {
	if e.Empty() {
		if e.Meta.Required && !e.Meta.HasDefault {
			return Problem{Kind: ProblemRequired}
		}
		return Problem{}
	}
	text := strings.TrimSpace(e.Text)
	switch e.Shape {
	case ShapeNumber:
		if _, err := ParseNumber(text); err != nil {
			return Problem{Kind: ProblemNotNumber, Text: text}
		}
	case ShapeDate:
		if _, err := jira.ParseDate(text); err != nil {
			return Problem{Kind: ProblemNotDate, Text: text}
		}
	case ShapeDateTime:
		if _, err := ParseDateTime(text, e.Loc); err != nil {
			return Problem{Kind: ProblemNotDateTime, Text: text}
		}
	case ShapeIssueKey:
		if !issueKey.MatchString(text) {
			return Problem{Kind: ProblemNotIssueKey, Text: text}
		}
	case ShapeDoc:
		if _, err := e.Document(); err != nil {
			return Problem{Kind: ProblemDocument, Err: err}
		}
	case ShapeUser, ShapeUsers:
		for _, option := range e.Picked {
			if option.ID == "" {
				return Problem{Kind: ProblemNoAccount, Option: option}
			}
		}
		return e.outsideAllowed()
	case ShapeSelect, ShapeMultiSelect, ShapeCascade:
		return e.outsideAllowed()
	case ShapeText, ShapeLabels, ShapeOther:
	}
	return Problem{}
}

// outsideAllowed reports a chosen value the site does not allow for this field.
// A picker only ever offers what the schema stated, so this catches a value
// that arrived some other way — a restored draft against a screen that has
// since changed, or a person picker with no list to check against.
func (e *Entry) outsideAllowed() Problem {
	if len(e.Meta.AllowedValues) == 0 {
		return Problem{}
	}
	for _, option := range e.Picked {
		if !allows(e.Meta.AllowedValues, option) {
			return Problem{Kind: ProblemNotAllowed, Option: option}
		}
	}
	return Problem{}
}

// allows reports whether one chosen value is among those stated, following the
// second level of a cascading select.
func allows(allowed []jira.Option, option jira.Option) bool {
	for _, candidate := range allowed {
		if candidate.ID != option.ID {
			continue
		}
		if len(option.Children) == 0 {
			return true
		}
		for _, child := range candidate.Children {
			if child.ID == option.Children[0].ID {
				return true
			}
		}
		return false
	}
	return false
}

// dateTimeLayouts are what a date-and-time field accepts, in the order they are
// tried. The first two carry their own offset; the rest are read in the account
// timezone, because a time typed with no offset is the one on the user's clock.
var dateTimeLayouts = []string{
	"2006-01-02T15:04:05.000-0700",
	time.RFC3339,
	"2006-01-02 15:04:05",
	"2006-01-02 15:04",
	"2006-01-02T15:04:05",
	"2006-01-02T15:04",
}

// ParseDateTime reads a date and time in any form a field accepts.
func ParseDateTime(text string, loc *time.Location) (time.Time, error) {
	if loc == nil {
		loc = time.UTC
	}
	trimmed := strings.TrimSpace(text)
	for i, layout := range dateTimeLayouts {
		var (
			at  time.Time
			err error
		)
		if i < 2 {
			at, err = time.Parse(layout, trimmed)
		} else {
			at, err = time.ParseInLocation(layout, trimmed, loc)
		}
		if err == nil {
			return at, nil
		}
	}
	return time.Time{}, fmt.Errorf("form: %q is not a date and time", trimmed)
}

var errNotFinite = errors.New("not a finite number")

// ParseNumber reads a number JSON can carry: ParseFloat also takes NaN and Inf.
func ParseNumber(text string) (float64, error) {
	number, err := strconv.ParseFloat(text, 64)
	if err != nil {
		return 0, err
	}
	if math.IsNaN(number) || math.IsInf(number, 0) {
		return 0, errNotFinite
	}
	return number, nil
}

// CreateInput assembles what will be created. The fields the port carries in
// their own right are read out by the system name the site gave them;
// everything else travels by field id in the FieldSet.
func CreateInput(project, issueTypeID string, entries []Entry) jira.IssueInput {
	in := jira.IssueInput{ProjectKey: project, IssueTypeID: issueTypeID}
	values := make(map[string]jira.FieldValue, len(entries))
	for i := range entries {
		e := &entries[i]
		if e.Empty() {
			continue
		}
		if assign(&in, e) {
			continue
		}
		if value, ok := e.Value(); ok {
			values[e.ID()] = value
		}
	}
	if len(values) > 0 {
		in.Fields = jira.NewFieldSet(values)
	}
	return in
}

// assign puts a field on the input itself where the port has a slot for it, and
// reports whether it did.
func assign(in *jira.IssueInput, e *Entry) bool {
	switch e.Meta.Field.Schema.System {
	case "summary":
		in.Summary = strings.TrimSpace(e.Text)
	case "description":
		doc, err := e.Document()
		if err != nil {
			return false
		}
		in.Description = doc
	case "parent":
		in.ParentKey = strings.TrimSpace(e.Text)
	case "labels":
		in.Labels = e.Labels()
	case "assignee":
		if len(e.Picked) == 0 {
			return false
		}
		in.Assignee = e.Picked[0].ID
	default:
		return false
	}
	return true
}
