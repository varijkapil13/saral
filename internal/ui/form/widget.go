package form

import (
	"strconv"
	"strings"
	"time"

	appissue "github.com/varijkapil13/saral/internal/app/issue"
	"github.com/varijkapil13/saral/internal/ui/widget"
	"github.com/varijkapil13/saral/pkg/adf"
	"github.com/varijkapil13/saral/pkg/jira"
)

// shapeName is what a field's shape is called on screen.
func shapeName(k appissue.Shape) string {
	switch k {
	case appissue.ShapeText:
		return "text"
	case appissue.ShapeDoc:
		return "rich text"
	case appissue.ShapeNumber:
		return "number"
	case appissue.ShapeDate:
		return "date"
	case appissue.ShapeDateTime:
		return "date and time"
	case appissue.ShapeSelect:
		return "choice"
	case appissue.ShapeMultiSelect:
		return "choices"
	case appissue.ShapeCascade:
		return "choice"
	case appissue.ShapeUser:
		return "person"
	case appissue.ShapeUsers:
		return "people"
	case appissue.ShapeLabels:
		return "labels"
	case appissue.ShapeIssueKey:
		return "issue"
	default:
		return "unrecognised"
	}
}

// paneOf is the editor a field's shape opens in.
func paneOf(k appissue.Shape) editor {
	switch {
	case k == appissue.ShapeDoc:
		return editDoc
	case k.Chooses():
		return editChoose
	default:
		return editText
	}
}

// offer decides whether a field is put in front of the user, and says why not
// when it is not.
func offer(meta jira.FieldMeta, project string, issueType jira.IssueType) (offered bool, reason string) {
	switch appissue.Offer(meta) {
	case appissue.WithheldProject:
		return false, "this form creates the issue in " + project + ", the project it was opened for"
	case appissue.WithheldIssueType:
		return false, "this form creates a " + issueType.Name + ", the issue type it was opened for"
	case appissue.WithheldNotSettable:
		return false, "Jira does not let this field be set while an issue is being created"
	case appissue.WithheldAttachment:
		return false, "files are attached once the issue exists"
	case appissue.WithheldLinks:
		return false, "links to other issues are made from the issue itself"
	default:
		return true, ""
	}
}

// field is one editable field: what the site said about it, which editor that
// earned, and what has been put in it so far.
type field struct {
	meta jira.FieldMeta
	kind appissue.Shape

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
	f := &field{meta: meta, kind: appissue.ShapeOf(meta), loc: loc}
	if f.kind == appissue.ShapeDoc {
		f.text = adf.MarkdownWith(f.original, adf.Options{})
	}
	return f
}

func (f *field) id() string { return f.meta.Field.ID }

func (f *field) entry() appissue.Entry {
	return appissue.Entry{Meta: f.meta, Shape: f.kind, Text: f.text, Picked: f.picked, Original: f.original, Loc: f.loc}
}

// empty reports whether nothing has been put in the field.
func (f *field) empty() bool {
	e := f.entry()
	return e.Empty()
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
	if f.kind.Chooses() {
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

func (f *field) document() (adf.Doc, error) {
	e := f.entry()
	return e.Document()
}

// oneWay names the constructs in this field's original document that a markdown
// round trip cannot rebuild, so the editor can say so before an edit rather
// than after it. A block nobody touches is restored whole, so the warning is
// about what an edit costs, not about opening the editor.
func (f *field) oneWay() []string {
	losses := adf.LossyConstructs(f.original, adf.Options{})
	out := make([]string, 0, len(losses))
	for _, l := range losses {
		out = append(out, l.String())
	}
	return out
}

func (f *field) value() (jira.FieldValue, bool) {
	e := f.entry()
	return e.Value()
}

// userOption is how an account is offered in a picker: the account id is the
// value, because a display name is neither unique nor writable.
func userOption(user jira.User) jira.Option {
	label := strings.TrimSpace(widget.Sanitize(user.DisplayName))
	if label == "" {
		label = widget.Sanitize(user.AccountID)
	}
	return jira.Option{ID: user.AccountID, Label: label}
}
