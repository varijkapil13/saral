package issue

import (
	"slices"
	"strconv"
	"strings"

	"github.com/varijkapil13/saral/pkg/adf"
	"github.com/varijkapil13/saral/pkg/jira"
)

// editKind is how one field is changed, which decides what a key does on its
// row and what shape its value takes in a patch.
type editKind uint8

const (
	editText editKind = iota
	editLabels
	editDate
	editDoc
	editPick
)

// noneOption is the first entry of every picker, so that a field which has a
// value can be given none. Its empty ID is what says the field is to be nulled.
var noneOption = jira.Option{Label: "none"}

// editRow is one field of the editor.
//
// fetched is the load-bearing one. It carries jira.Issue.Requested's answer for
// this field: false means the read never asked about it, so the value on screen
// is this client having nothing rather than Jira having nothing, and writing it
// back would empty a field nobody touched.
type editRow struct {
	id      string
	label   string
	kind    editKind
	fetched bool
	// reason is why the field could not be read, when the pane tried and was
	// refused. It replaces the generic wording with the site's own.
	reason string

	original string
	// originalLabel is what the value the site holds is called, for a picker
	// whose allowed values have not been read yet: the id alone reads as
	// nothing, and the name is on the issue already.
	originalLabel string
	value         string

	doc     adf.Doc
	edited  *adf.Doc
	cleared bool

	options     []jira.Option
	chosen, was int

	// problem is the message Jira attached to this field when it refused the
	// write, so a rejection annotates the row rather than a status line.
	problem string
}

func newEditRow(id string, iss jira.Issue) editRow {
	row := editRow{id: id, fetched: iss.Requested.Has(id)}
	switch id {
	case "summary":
		row.label, row.kind, row.original = "Summary", editText, iss.Summary
	case "description":
		row.label, row.kind, row.doc = "Description", editDoc, iss.Description
	case "priority":
		row.label, row.kind = "Priority", editPick
		if iss.Priority != nil {
			row.original, row.originalLabel = iss.Priority.ID, iss.Priority.Name
		}
	case "labels":
		row.label, row.kind, row.original = "Labels", editLabels, strings.Join(iss.Labels, ", ")
	case "duedate":
		row.label, row.kind, row.original = "Due", editDate, iss.Due.String()
	}
	row.value = row.original
	return row
}

// setOptions gives a picker the values this site allows, resolved at runtime
// from the create screen. The current value is matched by id, never by name: a
// localised site sends every priority's name translated and its id unchanged.
func (r *editRow) setOptions(allowed []jira.Option) {
	r.options = append([]jira.Option{noneOption}, allowed...)
	r.was = 0
	for i, option := range r.options {
		if i > 0 && option.ID == r.original {
			r.was = i
			break
		}
	}
	r.chosen = r.was
}

// blocked reports why this row cannot be changed, and is what every gesture on
// it is checked against.
func (r *editRow) blocked() (string, bool) {
	if r.fetched {
		return "", false
	}
	if r.reason != "" {
		return r.label + " could not be read: " + r.reason, true
	}
	return r.label + " was not read with this issue, so changing it here would empty what is really there", true
}

func (r *editRow) dirty() bool {
	switch r.kind {
	case editDoc:
		return r.edited != nil || (r.cleared && !r.doc.IsEmpty())
	case editPick:
		return len(r.options) > 0 && r.chosen != r.was
	case editText, editLabels, editDate:
		return r.value != r.original
	}
	return false
}

// editedValue is the row's value in the form a draft keeps it: an option by id,
// everything else as it was typed.
func (r *editRow) editedValue() string {
	if r.kind == editPick {
		if r.chosen <= 0 || r.chosen >= len(r.options) {
			return ""
		}
		return r.options[r.chosen].ID
	}
	return r.value
}

// setEdited puts a value back on the row, which is how a draft and a
// reload-and-reapply both restore what the user had.
func (r *editRow) setEdited(value string) {
	if r.kind != editPick {
		r.value = value
		return
	}
	r.chosen = 0
	for i, option := range r.options {
		if i > 0 && option.ID == value {
			r.chosen = i
			return
		}
	}
}

// documentNow is what the editor is handed: whatever the author has already
// produced, or the document the site holds.
func (r *editRow) documentNow() adf.Doc {
	switch {
	case r.edited != nil:
		return *r.edited
	case r.cleared:
		return adf.Doc{}
	default:
		return r.doc
	}
}

// display is the value shown on the row.
func (r *editRow) display() string {
	switch r.kind {
	case editDoc:
		return describeDoc(r.documentNow())
	case editPick:
		if len(r.options) == 0 {
			return r.originalLabel
		}
		return r.options[r.chosen].Label
	case editText, editLabels, editDate:
		return r.value
	}
	return ""
}

// was renders the value the site holds, for the confirmation that says what is
// about to change.
func (r *editRow) before() string {
	switch r.kind {
	case editDoc:
		return describeDoc(r.doc)
	case editPick:
		if len(r.options) == 0 || r.was >= len(r.options) {
			return r.originalLabel
		}
		return r.options[r.was].Label
	case editText, editLabels, editDate:
		return r.original
	}
	return ""
}

func describeDoc(d adf.Doc) string {
	if d.IsZero() || d.IsEmpty() {
		return "empty"
	}
	lines := strings.Count(strings.TrimRight(adf.Markdown(d), "\n"), "\n") + 1
	if lines == 1 {
		return "1 line"
	}
	return strconv.Itoa(lines) + " lines"
}

// into adds this row's change to a patch, in the shape the port takes it.
func (r *editRow) into(out *jira.IssuePatch) error {
	switch r.kind {
	case editText:
		value := strings.TrimSpace(r.value)
		if value == "" {
			return fieldProblem(r.id, r.label+" cannot be emptied")
		}
		out.Summary = &value
	case editLabels:
		labels := splitLabels(r.value)
		out.Labels = &labels
	case editDate:
		if strings.TrimSpace(r.value) == "" {
			out.Clear = append(out.Clear, jira.FieldRef{ID: r.id})
			return nil
		}
		due, err := jira.ParseDate(strings.TrimSpace(r.value))
		if err != nil {
			return fieldProblem(r.id, "write the date as 2006-01-02")
		}
		out.Due = &due
	case editDoc:
		if r.edited == nil {
			out.Clear = append(out.Clear, jira.FieldRef{ID: r.id})
			return nil
		}
		out.Description = r.edited
	case editPick:
		id := r.editedValue()
		if id == "" {
			out.Clear = append(out.Clear, jira.FieldRef{ID: r.id})
			return nil
		}
		out.PriorityID = &id
	}
	return nil
}

func fieldProblem(id, message string) error {
	return &jira.ValidationError{Fields: []jira.FieldError{{Field: id, Message: message}}}
}

// splitLabels reads the comma-separated form a label list is typed in. Jira
// refuses a label with a space in it, so the separator is unambiguous.
func splitLabels(s string) []string {
	out := make([]string, 0, strings.Count(s, ",")+1)
	for _, part := range strings.Split(s, ",") {
		label := strings.Join(strings.Fields(part), "-")
		if label != "" && !slices.Contains(out, label) {
			out = append(out, label)
		}
	}
	return out
}
