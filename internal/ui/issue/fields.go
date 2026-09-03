package issue

import (
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/charmbracelet/x/ansi"

	"github.com/varijkapil13/saral/internal/ui/richtext"
	"github.com/varijkapil13/saral/pkg/jira"
)

// absent is what a field says when the read never asked for it. That is not the
// same answer as a field the site had nothing to send, which is what
// Issue.Requested exists to tell apart.
const absent = "not asked for"

// detail names one platform field the sidebar lists, beside the field ID it
// arrives under so that a read which did not ask for it can say so.
type detail struct {
	label string
	id    string
	value func(*Model) string
}

// platform is the built-in fields, in reading order. The identity header already
// carries the type, the status, the priority, the assignee and when the issue
// was last touched, so none of those is repeated here.
var platform = []detail{
	{"Project", "project", func(m *Model) string { return projectName(m.issue.Project) }},
	{"Reporter", "reporter", func(m *Model) string { return userName(m.issue.Reporter) }},
	{"Resolution", "resolution", func(m *Model) string { return resolutionName(&m.issue) }},
	{"Resolved", "resolutiondate", func(m *Model) string { return m.at(m.issue.Resolved) }},
	{"Due", "duedate", func(m *Model) string { return m.issue.Due.String() }},
	{"Created", "created", func(m *Model) string { return formatWhen(m.issue.Created, m.location()) }},
	{"Labels", "labels", func(m *Model) string { return strings.Join(m.issue.Labels, ", ") }},
	{"Components", "components", func(m *Model) string { return componentNames(m.issue.Components) }},
	{"Fix versions", "fixVersions", func(m *Model) string { return versionNames(m.issue.FixVersions) }},
	{"Time", "timetracking", func(m *Model) string { return timeTracking(m.issue.TimeTracking) }},
}

// related are the three fields that carry other issues. They are drawn as a line
// per issue rather than as a comma-joined list of bare keys: an IssueRef already
// carries the summary and the status, and a key on its own says nothing about
// what is blocking what.
var related = []detail{
	{label: "Parent", id: "parent"},
	{label: "Subtasks", id: "subtasks"},
	{label: "Links", id: "issuelinks"},
}

// platformIDs is every field the rows above draw themselves, so that a site
// sending one of them in the field set as well cannot have it listed twice.
var platformIDs = func() []string {
	out := make([]string, 0, len(platform)+len(related))
	for _, d := range platform {
		out = append(out, d.id)
	}
	for _, d := range related {
		out = append(out, d.id)
	}
	slices.Sort(out)
	return out
}()

// refGroup is a heading and the issues under it: "Subtasks", or the phrasing a
// link arrived with.
type refGroup struct {
	label string
	refs  []jira.IssueRef
}

// rows builds the sidebar's lines at one width, measuring each as it goes so
// that drawing a frame never measures anything.
type rows struct {
	m     *Model
	width int
	out   content
}

func (r *rows) line(s string) {
	s = clip(s, r.width, r.m.deps.Theme.Glyphs.Ellipsis)
	got := ansi.StringWidth(s)
	r.out.lines = append(r.out.lines, s)
	r.out.widths = append(r.out.widths, got)
	r.out.widest = max(r.out.widest, got)
}

// detailContent is the fields region's whole content: the platform fields, the
// related issues, and every field this site defines that the issue carries a
// value for.
func (m *Model) detailContent(width int) content {
	r := &rows{m: m, width: width, out: content{
		lines:  make([]string, 0, 32),
		widths: make([]int, 0, 32),
	}}
	r.heading("Details")
	if !m.loadedIssue {
		r.note("Reading the issue" + m.deps.Theme.Glyphs.Ellipsis)
	}
	for _, d := range platform {
		if m.read(d.id) {
			r.field(d.label, d.value(m))
			continue
		}
		r.missing(d.label)
	}
	r.related()
	r.custom()
	return r.out
}

// read reports whether the issue in hand can answer for a field. Before the full
// read arrives the seed answers for what it carries and says nothing about the
// rest, because a screenful of "not asked for" is not a first paint.
func (m *Model) read(id string) bool { return !m.loadedIssue || m.issue.Requested.Has(id) }

// field draws one label and its value, and draws nothing at all when there is no
// value: a column of dashes reads as data.
func (r *rows) field(label, value string) {
	if strings.TrimSpace(value) == "" {
		return
	}
	r.line("  " + r.m.styles.label.Render(label) + column(label, labelWidth) + value)
}

// missing says the read never asked for a field, in the space its value would
// have had.
func (r *rows) missing(label string) {
	r.line("  " + r.m.styles.label.Render(label) + column(label, labelWidth) +
		r.m.styles.muted.Render(absent))
}

func (r *rows) heading(text string) { r.line(r.m.styles.section.Render(text)) }

func (r *rows) note(text string) { r.line("  " + r.m.styles.muted.Render(text)) }

// related draws the parent, the subtasks and the links, each group under what
// relates them and each issue on a line of its own.
func (r *rows) related() {
	for _, d := range related {
		if !r.m.read(d.id) {
			r.missing(d.label)
		}
	}
	groups := r.m.refGroups()
	keyW, statusW := 0, 0
	for i := range groups {
		for j := range groups[i].refs {
			keyW = max(keyW, ansi.StringWidth(groups[i].refs[j].Key))
			statusW = max(statusW, ansi.StringWidth(groups[i].refs[j].Status.Name))
		}
	}
	for i := range groups {
		r.heading(groups[i].label)
		for j := range groups[i].refs {
			r.ref(&groups[i].refs[j], keyW, statusW)
		}
	}
}

// ref is one related issue: its key, what state it is in, and what it is about.
func (r *rows) ref(ref *jira.IssueRef, keyW, statusW int) {
	st := r.m.styles
	line := "    " + st.key.Render(ref.Key) + column(ref.Key, keyW+2)
	if statusW > 0 {
		name := ref.Status.Name
		line += st.category(ref.Status.Category).Render(name) + column(name, statusW+2)
	}
	r.line(line + ref.Summary)
}

// custom lists the site's own fields, sorted by the name this site displays, and
// then says how many more came back with nothing in them — which is the answer
// to "what else is on this issue" that an empty row cannot give.
func (r *rows) custom() {
	values, empty := r.m.customFields(r.valueRoom())
	if len(values) > 0 {
		r.heading("Fields")
	}
	for _, v := range values {
		r.field(v.label, v.text)
	}
	if empty > 0 {
		r.note(strconv.Itoa(empty) + " more, all empty")
	}
}

// valueRoom is how many cells a value has once the label column has its own.
func (r *rows) valueRoom() int { return max(r.width-labelWidth-2, 8) }

// named is one field's display name and the text of its value.
type named struct{ label, text string }

// customFields is every field this site defines that the issue carries a value
// for, named the way the site spells it, plus how many of the ones the read
// asked for came back empty.
//
// The name comes from the answer the values arrived with. A custom field's ID
// differs on every site and its name is translated, so neither can be written
// down here; an ID the catalogue could not name shows as the ID, because a value
// nobody can label is still a value somebody put there.
func (m *Model) customFields(room int) (values []named, empty int) {
	ids := m.issue.Fields.IDs()
	values = make([]named, 0, len(ids))
	for _, id := range ids {
		if _, drawn := slices.BinarySearch(platformIDs, id); drawn {
			continue
		}
		ref, known := m.labels.Field(id)
		if !known {
			ref = jira.FieldRef{ID: id}
		}
		if text := m.fieldText(ref, room); text != "" {
			values = append(values, named{label: firstNonEmpty(ref.Name, id), text: text})
		}
	}
	slices.SortFunc(values, func(a, b named) int { return strings.Compare(a.label, b.label) })
	for _, id := range m.labels.IDs() {
		ref, known := m.labels.Field(id)
		switch {
		case !known, ref.Schema.Custom == "", !m.read(id):
			continue
		}
		if _, has := m.issue.Fields.ByID(id); !has {
			empty++
		}
	}
	return values, empty
}

// fieldText renders one field value as a line of text. The kind decides what to
// do with it, never the field: the only thing that can be read off a custom
// field without knowing the site is the shape of what arrived in it.
func (m *Model) fieldText(ref jira.FieldRef, room int) string {
	v, ok := m.issue.Fields.ByID(ref.ID)
	if !ok {
		return ""
	}
	switch v.Kind {
	case jira.KindText:
		return strings.TrimSpace(v.Text)
	case jira.KindUnknown:
		return unmodelledText(v)
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
		return formatWhen(v.Time, m.location())
	case jira.KindDoc:
		return richtext.Summary(v.Doc, room)
	case jira.KindOption, jira.KindOptions:
		return optionLabels(v.Options)
	case jira.KindUser, jira.KindUsers:
		return userNames(v.Users)
	case jira.KindEmpty:
		return ""
	default:
		return ""
	}
}

// unmodelledText draws a value whose shape this client has no slot for. The
// sprint field is the one everybody meets: its schema says `array` of `json`,
// the adapter keeps the bytes rather than guessing at them, and drawing those
// bytes put `[{"id":42,"name":"Sprint 14","state":"active",…` in a column forty
// cells wide.
//
// A shape with nothing to label is counted rather than drawn, because the value
// is on the issue whether or not this client can read it and a row that
// disappears says it is not there.
func unmodelledText(v jira.FieldValue) string {
	if names := v.Names(); len(names) > 0 {
		return strings.Join(names, ", ")
	}
	switch n := v.Count(); n {
	case 0:
		return ""
	case 1:
		return unreadableOne
	default:
		return strconv.Itoa(n) + " " + unreadableMany
	}
}

// What a value this client cannot read says in the space its value would have
// had. It names the shape as the reason rather than the field, because the
// field is fine and it is the shape that has no slot.
const (
	unreadableOne  = "a value this client cannot read"
	unreadableMany = "values this client cannot read"
)

// refGroups is the related issues, gathered under what relates them and in the
// order the site sent them.
func (m *Model) refGroups() []refGroup {
	out := make([]refGroup, 0, 4)
	if m.issue.Parent != nil {
		out = append(out, refGroup{label: "Parent", refs: []jira.IssueRef{*m.issue.Parent}})
	}
	if len(m.issue.Subtasks) > 0 {
		out = append(out, refGroup{label: "Subtasks", refs: m.issue.Subtasks})
	}
	for i := range m.issue.Links {
		link := &m.issue.Links[i]
		label := firstNonEmpty(link.Label, link.Type, "Links")
		at := slices.IndexFunc(out, func(g refGroup) bool { return g.label == label })
		if at < 0 {
			out = append(out, refGroup{label: label})
			at = len(out) - 1
		}
		out[at].refs = append(out[at].refs, link.Other)
	}
	return out
}

// at renders an instant a field may not carry at all.
func (m *Model) at(t *time.Time) string {
	if t == nil {
		return ""
	}
	return formatWhen(*t, m.location())
}

func optionLabels(in []jira.Option) string {
	out := make([]string, 0, len(in))
	for i := range in {
		label := firstNonEmpty(in[i].Label, in[i].ID)
		for j := range in[i].Children {
			label += " " + firstNonEmpty(in[i].Children[j].Label, in[i].Children[j].ID)
		}
		out = append(out, label)
	}
	return strings.Join(out, ", ")
}

func userNames(in []jira.User) string {
	out := make([]string, 0, len(in))
	for i := range in {
		if name := strings.TrimSpace(in[i].DisplayName); name != "" {
			out = append(out, name)
		}
	}
	return strings.Join(out, ", ")
}

func firstNonEmpty(values ...string) string {
	for _, v := range values {
		if strings.TrimSpace(v) != "" {
			return v
		}
	}
	return ""
}

// column is the spaces that carry a value to its column, measured off what is
// already there. It pads outside the style rather than inside it, so a label
// drawn faint does not carry the emphasis across the gap.
func column(filled string, width int) string {
	gap := width - ansi.StringWidth(filled)
	if gap < 1 {
		gap = 1
	}
	return strings.Repeat(" ", gap)
}
