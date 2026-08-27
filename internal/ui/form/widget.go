package form

import (
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/varijkapil13/saral/pkg/adf"
	"github.com/varijkapil13/saral/pkg/jira"
)

// kind is the widget one field earns. It is decided from the schema the site
// sent — the type, the element type of an array, the system name of a built-in
// field and the URI of a custom field type — and never from a display name,
// which on a German site is a German word.
type kind uint8

// The widgets. kindOther is the honest answer for a shape this form has no
// editor for: it keeps whatever was typed and says it does not understand it.
const (
	kindText kind = iota
	kindDoc
	kindNumber
	kindDate
	kindDateTime
	kindSelect
	kindMultiSelect
	kindCascade
	kindUser
	kindUsers
	kindLabels
	kindIssueKey
	kindOther
)

func (k kind) String() string {
	switch k {
	case kindText:
		return "text"
	case kindDoc:
		return "rich text"
	case kindNumber:
		return "number"
	case kindDate:
		return "date"
	case kindDateTime:
		return "date and time"
	case kindSelect:
		return "choice"
	case kindMultiSelect:
		return "choices"
	case kindCascade:
		return "choice"
	case kindUser:
		return "person"
	case kindUsers:
		return "people"
	case kindLabels:
		return "labels"
	case kindIssueKey:
		return "issue"
	default:
		return "unrecognised"
	}
}

// pane is the editor a widget opens in.
func (k kind) pane() editor {
	switch k {
	case kindDoc:
		return editDoc
	case kindSelect, kindMultiSelect, kindCascade, kindUser, kindUsers:
		return editChoose
	default:
		return editText
	}
}

// chooses reports whether the widget is filled from a list rather than typed.
func (k kind) chooses() bool { return k.pane() == editChoose }

// multiple reports whether the widget holds more than one value.
func (k kind) multiple() bool { return k == kindMultiSelect || k == kindUsers }

// selectable are the schema types whose values come from a list the site
// states. They are Jira's own type names, which are not translated.
var selectable = []string{
	"option", "option-with-child", "priority", "issuetype", "resolution",
	"project", "version", "component", "group", "securitylevel", "status",
}

// widgetFor picks the editor a field's schema earns.
func widgetFor(meta jira.FieldMeta) kind {
	schema := meta.Field.Schema
	if isDocument(schema) {
		return kindDoc
	}
	if schema.System == "parent" {
		return kindIssueKey
	}
	switch schema.Type {
	case "string":
		return kindText
	case "number":
		return kindNumber
	case "date":
		return kindDate
	case "datetime":
		return kindDateTime
	case "user":
		return kindUser
	case "option-with-child":
		if len(meta.AllowedValues) > 0 {
			return kindCascade
		}
		return kindOther
	case "array":
		return arrayWidget(meta)
	default:
		if slices.Contains(selectable, schema.Type) && len(meta.AllowedValues) > 0 {
			return kindSelect
		}
		return kindOther
	}
}

func arrayWidget(meta jira.FieldMeta) kind {
	items := meta.Field.Schema.Items
	switch items {
	case "string":
		return kindLabels
	case "user":
		return kindUsers
	default:
		if slices.Contains(selectable, items) && len(meta.AllowedValues) > 0 {
			return kindMultiSelect
		}
		return kindOther
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

// canSet reports whether the create screen lets this field be given a value at
// all. Jira states the operations per field per issue type, and a field with
// none is on the screen to be read.
func canSet(operations []string) bool {
	return slices.Contains(operations, "set") || slices.Contains(operations, "add")
}

// offer decides whether a field is put in front of the user, and says why not
// when it is not. The reason is the answer, in the same shape a capability
// gives one: a field silently missing from a form is indistinguishable from a
// form that forgot it.
func offer(meta jira.FieldMeta, project string, issueType jira.IssueType) (offered bool, reason string) {
	schema := meta.Field.Schema
	switch schema.System {
	case "project":
		return false, "this form creates the issue in " + project + ", the project it was opened for"
	case "issuetype":
		return false, "this form creates a " + issueType.Name + ", the issue type it was opened for"
	}
	if !canSet(meta.Operations) {
		return false, "Jira does not let this field be set while an issue is being created"
	}
	switch schema.Items {
	case "attachment":
		return false, "files are attached once the issue exists"
	case "issuelinks":
		return false, "links to other issues are made from the issue itself"
	}
	return true, ""
}

// field is one editable field: what the site said about it, which editor that
// earned, and what has been put in it so far.
type field struct {
	meta jira.FieldMeta
	kind kind

	// text carries what was typed into a typed widget, and the markdown of a
	// document widget.
	text string
	// picked carries what was chosen in a widget that chooses. A cascading
	// select stores the second level under Children, which is the shape the
	// same field's stored value arrives in.
	picked []jira.Option
	// original is the document this field started from. An edit is reconciled
	// against it rather than rebuilt from markdown, which is the only way the
	// parts nobody touched come back as they arrived.
	original adf.Doc
	// loc is the account timezone a date and time is read in.
	loc *time.Location

	// problem is what is wrong with the value: this form's own rules before a
	// write, and Jira's own words about the field after one.
	problem string
	// rev counts changes, so that a rendered row can be memoized.
	rev int
}

// newField builds the editor one field of a create screen earns.
func newField(meta jira.FieldMeta, loc *time.Location) *field {
	f := &field{meta: meta, kind: widgetFor(meta), loc: loc}
	if f.kind == kindDoc {
		f.text = adf.MarkdownWith(f.original, adf.Options{})
	}
	return f
}

func (f *field) id() string { return f.meta.Field.ID }

// empty reports whether nothing has been put in the field.
func (f *field) empty() bool {
	if f.kind.chooses() {
		return len(f.picked) == 0
	}
	return strings.TrimSpace(f.text) == ""
}

func (f *field) clear() {
	f.text, f.picked, f.problem = "", nil, ""
	f.rev++
}

// stated is what Jira says it will put in this field if the request leaves it
// out, and whether it says anything at all. It is shown and never put in the
// widget: seeding the widget would make the field non-empty, which puts the
// value in the FieldSet and sends it, and a default the client sends explicitly
// is a default the project can no longer change.
func (f *field) stated() (string, bool) {
	if !f.meta.HasDefault {
		return "", false
	}
	return defaultText(f.meta.Default), true
}

// defaultText spells a stated default. The empty string is the answer for a
// screen that says a field has a default without naming it — the reporter comes
// from the credential — and for a shape with no short form to show.
func defaultText(v jira.FieldValue) string {
	switch v.Kind {
	case jira.KindText, jira.KindUnknown:
		return strings.TrimSpace(v.Text)
	case jira.KindNumber:
		return strconv.FormatFloat(v.Number, 'f', -1, 64)
	case jira.KindBool:
		if v.Bool {
			return "yes"
		}
		return "no"
	case jira.KindDate:
		return v.Date.String()
	case jira.KindTime:
		return v.Time.Format("2006-01-02 15:04")
	case jira.KindOption, jira.KindOptions:
		labels := make([]string, 0, len(v.Options))
		for _, option := range v.Options {
			labels = append(labels, cascadeLabel(option))
		}
		return strings.Join(labels, ", ")
	case jira.KindUser, jira.KindUsers:
		names := make([]string, 0, len(v.Users))
		for _, user := range v.Users {
			names = append(names, userOption(user).Label)
		}
		return strings.Join(names, ", ")
	default:
		return ""
	}
}

// display is the value as the field list shows it.
func (f *field) display() string {
	if f.kind.chooses() {
		labels := make([]string, 0, len(f.picked))
		for _, option := range f.picked {
			labels = append(labels, cascadeLabel(option))
		}
		return strings.Join(labels, ", ")
	}
	return strings.ReplaceAll(strings.TrimSpace(f.text), "\n", " ")
}

// cascadeLabel spells a chosen value, following the second level of a cascading
// select into the label a user picked.
func cascadeLabel(option jira.Option) string {
	label := option.Label
	for _, child := range option.Children {
		label += " / " + child.Label
	}
	return label
}

// labels splits a labels field the way Jira stores one: separate values, never
// a sentence. A label cannot contain whitespace, so whitespace and commas both
// separate.
func (f *field) labels() []string {
	out := make([]string, 0, 4)
	for _, token := range strings.FieldsFunc(f.text, func(r rune) bool {
		return r == ',' || r == ' ' || r == '\t' || r == '\n'
	}) {
		if token != "" && !slices.Contains(out, token) {
			out = append(out, token)
		}
	}
	return out
}

// document reconciles the edited markdown against the document this field
// started from. ParseMarkdownInto reuses the original node for every block that
// was not touched, which is what keeps a mention's account id, a lozenge's
// colour and an unknown node's attributes — none of which markdown carries.
func (f *field) document() (adf.Doc, error) {
	return adf.ParseMarkdownInto(f.original, f.text, adf.Options{})
}

// oneWay names the constructs in this field's original document that a markdown
// round trip cannot rebuild, so the editor can say so before an edit rather
// than after it. A block nobody touches is restored whole, so the warning is
// about what an edit costs, not about opening the editor.
func (f *field) oneWay() []string {
	if f.original.IsEmpty() {
		return nil
	}
	present := f.original.NodeTypes()
	out := make([]string, 0, 4)
	for _, entry := range adf.ParseMarkdownDropsOnly() {
		node, _, ok := strings.Cut(entry, ":")
		// text and marks describe prose rather than a node type, and every
		// document has text, so naming them would warn about every document.
		if !ok || node == "text" || node == "marks" || present[node] == 0 {
			continue
		}
		out = append(out, entry)
	}
	return out
}

// value turns what is in the field into the tagged value the port carries. The
// second result is false for a field holding nothing, which is not the same as
// a field holding an empty value.
func (f *field) value() (jira.FieldValue, bool) {
	if f.empty() {
		return jira.FieldValue{}, false
	}
	text := strings.TrimSpace(f.text)
	switch f.kind {
	case kindNumber:
		number, err := strconv.ParseFloat(text, 64)
		if err != nil {
			return jira.FieldValue{}, false
		}
		return jira.FieldValue{Kind: jira.KindNumber, Number: number}, true
	case kindDate:
		date, err := jira.ParseDate(text)
		if err != nil {
			return jira.FieldValue{}, false
		}
		return jira.FieldValue{Kind: jira.KindDate, Date: date}, true
	case kindDateTime:
		at, err := parseDateTime(text, f.loc)
		if err != nil {
			return jira.FieldValue{}, false
		}
		return jira.FieldValue{Kind: jira.KindTime, Time: at}, true
	case kindDoc:
		doc, err := f.document()
		if err != nil {
			return jira.FieldValue{}, false
		}
		return jira.FieldValue{Kind: jira.KindDoc, Doc: doc}, true
	case kindLabels:
		options := make([]jira.Option, 0, 4)
		for _, label := range f.labels() {
			options = append(options, jira.Option{Label: label})
		}
		return jira.FieldValue{Kind: jira.KindOptions, Options: options}, true
	case kindSelect, kindCascade:
		return jira.FieldValue{Kind: jira.KindOption, Options: slices.Clone(f.picked)}, true
	case kindMultiSelect:
		return jira.FieldValue{Kind: jira.KindOptions, Options: slices.Clone(f.picked)}, true
	case kindUser:
		return jira.FieldValue{Kind: jira.KindUser, Users: f.users()}, true
	case kindUsers:
		return jira.FieldValue{Kind: jira.KindUsers, Users: f.users()}, true
	case kindOther:
		// Kept rather than guessed at: the value goes back as the text it was
		// typed as, marked as a shape this client does not model.
		return jira.FieldValue{Kind: jira.KindUnknown, Text: text}, true
	default:
		return jira.FieldValue{Kind: jira.KindText, Text: text}, true
	}
}

// users reads a person picker's choices back as accounts. The option id is the
// account id, which is the only identifier Jira accepts on a write.
func (f *field) users() []jira.User {
	out := make([]jira.User, 0, len(f.picked))
	for _, option := range f.picked {
		out = append(out, jira.User{AccountID: option.ID, DisplayName: option.Label})
	}
	return out
}

// userOption is how an account is offered in a picker: the account id is the
// value, because a display name is neither unique nor writable.
func userOption(user jira.User) jira.Option {
	label := strings.TrimSpace(user.DisplayName)
	if label == "" {
		label = user.AccountID
	}
	return jira.Option{ID: user.AccountID, Label: label}
}
