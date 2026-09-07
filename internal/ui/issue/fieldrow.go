package issue

import (
	"slices"
	"strconv"
	"strings"

	"github.com/varijkapil13/saral/pkg/adf"
	"github.com/varijkapil13/saral/pkg/jira"
)

// rowKind is how one sidebar row is changed, which decides what editing it in
// place does and what shape its value takes in a patch.
//
// rkStatic covers every field this build has no editor for at all — a
// platform fact, a related issue, a custom field nothing here has a kind for.
// rkChoice, rkPerson and rkStatus are priority, assignee and status: this
// build's whole editable set is {rkText, rkLabels, rkDate, rkDoc, rkChoice,
// rkPerson, rkStatus}. A future option or user-typed custom field discovered
// from FieldSchema rather than named by a fixed id is still out of scope — see
// docs/FIELDS.md P8.
type rowKind uint8

const (
	rkStatic rowKind = iota
	rkText
	rkLabels
	rkDate
	rkDoc
	rkChoice
	rkPerson
	rkStatus
)

// editableKind reports whether this build can edit a row of this kind at all.
// A kind failing this is not "editmeta did not list it" — it is "nothing here
// knows how", which reads the same to the row's blocked() but is a different
// fact and is why this is spelled out rather than folded into fetched alone.
func (k rowKind) editableKind() bool {
	switch k {
	case rkText, rkLabels, rkDate, rkDoc, rkChoice, rkPerson, rkStatus:
		return true
	default:
		return false
	}
}

// fieldRow is one field this build knows how to edit in place: the summary,
// the description, the labels and the due date. Every other field the sidebar
// draws is a static line with no row of its own — see cursorRow.
//
// fetched is the load-bearing one. It carries jira.Issue.Requested's answer for
// this field: false means the read never asked about it, so the value on
// screen is this client having nothing rather than Jira having nothing, and
// writing it back would empty a field nobody touched.
type fieldRow struct {
	id    string
	label string
	kind  rowKind

	fetched bool
	// listed is editmeta's own answer for this field: true once a read of it has
	// arrived and named this id. A row that is fetched but never listed is not
	// editable — the screen this issue is on right now does not offer it — and a
	// row not yet fetched has this false regardless, since there has been no
	// screen read to ask.
	listed bool

	original string
	value    string

	// chosenID and originalID are the underlying identifier rkChoice and
	// rkPerson change: an option id for priority, an account id ("" for
	// unassigned) for the assignee. original/value above stay the display
	// labels a person reads; these two are what dirty() compares and into()
	// sends, because two options can share a label and an account's display
	// name is not what the patch takes.
	chosenID   string
	originalID string

	doc     adf.Doc
	edited  *adf.Doc
	cleared bool

	// problem is the message Jira attached to this field when it refused the
	// write, so a rejection is shown in the field's own words.
	problem string
}

func newFieldRow(id, label string, kind rowKind, iss jira.Issue) fieldRow {
	row := fieldRow{id: id, label: label, kind: kind, fetched: iss.Requested.Has(id)}
	switch id {
	case "summary":
		row.original = iss.Summary
	case "description":
		row.doc = iss.Description
	case "labels":
		row.original = strings.Join(iss.Labels, ", ")
	case "duedate":
		row.original = iss.Due.String()
	case "status":
		row.original = iss.Status.Name
	case "priority":
		if iss.Priority != nil {
			row.original, row.chosenID = iss.Priority.Name, iss.Priority.ID
		}
	case "assignee":
		switch {
		case iss.Assignee != nil:
			row.original, row.chosenID = iss.Assignee.DisplayName, iss.Assignee.AccountID
		default:
			row.original = "unassigned"
		}
	}
	row.originalID = row.chosenID
	row.value = row.original
	return row
}

// editable reports whether this row can be changed right now. Status is a
// workflow action rather than a field editmeta lists, so it answers for
// itself — see docs/FIELDS.md P8 — and every other row needs the site's own
// screen to name it as well as a kind and a fetch this build knows what to do
// with. A row failing either test is read-only, and blocked says why.
func (r *fieldRow) editable() bool {
	if r.kind == rkStatus {
		return true
	}
	return r.kind.editableKind() && r.fetched && r.listed
}

// blocked reports why this row cannot be changed, in one clause: the shared
// wording every read-only row in the sidebar answers Enter with.
func (r *fieldRow) blocked() (string, bool) {
	if r.editable() {
		return "", false
	}
	return "read-only", true
}

func (r *fieldRow) dirty() bool {
	switch r.kind {
	case rkDoc:
		return r.edited != nil || (r.cleared && !r.doc.IsEmpty())
	case rkText, rkLabels, rkDate:
		return r.value != r.original
	case rkChoice, rkPerson:
		return r.chosenID != r.originalID
	default:
		// rkStatus never joins the dirty set: a status change is a workflow
		// action applied through the transition endpoint the moment it is
		// chosen, never a value waiting on s — see saveTransition.
		return false
	}
}

// setEdited puts a value back on the row, which is how a draft and a rebase
// after a reload both restore what the user had typed.
func (r *fieldRow) setEdited(value string) { r.value = value }

// documentNow is what the $EDITOR handoff and the inline textarea are seeded
// with: whatever the author has already produced, or the document the site
// holds.
func (r *fieldRow) documentNow() adf.Doc {
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
func (r *fieldRow) display() string {
	if r.kind == rkDoc {
		return describeDoc(r.documentNow())
	}
	return r.value
}

// before renders the value the site holds, for the "was …" a dirty row shows
// beside its new value when there is room.
func (r *fieldRow) before() string {
	if r.kind == rkDoc {
		return describeDoc(r.doc)
	}
	return r.original
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
func (r *fieldRow) into(out *jira.IssuePatch) error {
	switch r.kind {
	case rkText:
		value := strings.TrimSpace(r.value)
		if value == "" {
			return fieldProblem(r.id, r.label+" cannot be emptied")
		}
		out.Summary = &value
	case rkLabels:
		labels := splitLabels(r.value)
		out.Labels = &labels
	case rkDate:
		if strings.TrimSpace(r.value) == "" {
			out.Clear = append(out.Clear, jira.FieldRef{ID: r.id})
			return nil
		}
		due, err := jira.ParseDate(strings.TrimSpace(r.value))
		if err != nil {
			return fieldProblem(r.id, "write the date as 2006-01-02")
		}
		out.Due = &due
	case rkDoc:
		if r.edited == nil {
			out.Clear = append(out.Clear, jira.FieldRef{ID: r.id})
			return nil
		}
		out.Description = r.edited
	case rkChoice:
		id := r.chosenID
		out.PriorityID = &id
	case rkPerson:
		id := r.chosenID
		out.Assignee = &id
	}
	return nil
}

func fieldProblem(id, message string) error {
	return &jira.ValidationError{Fields: []jira.FieldError{{Field: id, Message: message}}}
}

func notRead(row *fieldRow) error {
	return &jira.ValidationError{Fields: []jira.FieldError{{
		Field:   row.id,
		Message: row.label + " was not read with this issue, so writing it would empty whatever is really there",
	}}}
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

// editableRowSpecs is the fields this build offers a row for, in the order
// they are built. Every other field the sidebar draws — every platform fact
// beyond these seven, every related issue, every custom field — is a cursorRow
// with no fieldRow behind it: navigable, and read-only.
var editableRowSpecs = []struct {
	id, label string
	kind      rowKind
}{
	{"summary", "Summary", rkText},
	{"description", "Description", rkDoc},
	{"status", "Status", rkStatus},
	{"priority", "Priority", rkChoice},
	{"assignee", "Assignee", rkPerson},
	{"labels", "Labels", rkLabels},
	{"duedate", "Due", rkDate},
}

func buildFieldRows(iss jira.Issue) []fieldRow {
	rows := make([]fieldRow, 0, len(editableRowSpecs))
	for _, spec := range editableRowSpecs {
		rows = append(rows, newFieldRow(spec.id, spec.label, spec.kind, iss))
	}
	return rows
}
