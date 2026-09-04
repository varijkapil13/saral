package issue

import (
	"cmp"
	"math"
	"slices"
	"strconv"
	"strings"
	"sync/atomic"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/varijkapil13/saral/internal/ui/kernel"
	"github.com/varijkapil13/saral/internal/ui/richtext"
	"github.com/varijkapil13/saral/pkg/jira"
)

// absent is what a field says when the read never asked for it. That is not the
// same answer as a field the site had nothing to send, which is what
// Issue.Requested exists to tell apart.
const absent = "not asked for"

// bookkeepingNote is what a count of hidden fields is drawn with, the way
// empty fields are drawn with "all empty": it names what happened to the
// value rather than the field, since the field is not wrong — this program is
// choosing not to draw it.
const bookkeepingNote = "more, hidden as Jira's own bookkeeping"

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

// bookkeepingField is one plugin field type a Jira Cloud site mints for its own
// UI — never written by a person — that the sidebar hides by default. The
// match is Key against jira.FieldSchema.Custom, the plugin's own field type
// URI: the same string on every Jira Cloud site, unlike a field id or a field
// name, which are both per-site (see docs/API-NOTES.md). Matching on Name
// instead would be matching on the one thing here that differs per site and is
// translated besides.
//
// Seen marks a key this repository's own fixtures already carry —
// gh-lexo-rank is minted by pkg/jira/jiratest/gen.go. An unseen key is this
// program's best knowledge of Jira's plugin field types, entered here (and in
// docs/FIELDS.md's own table) to be checked against a real site's
// GET /rest/api/3/field the first time somebody can run scripts/capture.sh,
// which has not happened yet. A wrong key is inert: it matches nothing on any
// site, so it hides nothing that should have been drawn, which is the
// direction a mistake in this table is safe to fall in.
type bookkeepingField struct {
	Key  string
	Name string // documents the row; never compared against
	Seen bool
}

// bookkeepingFields is the denylist. Add a row to extend it.
var bookkeepingFields = []bookkeepingField{
	{Key: "com.pyxis.greenhopper.jira:gh-lexo-rank", Name: "Rank", Seen: true},
	{Key: "com.pyxis.greenhopper.jira:gh-epic-color", Name: "Epic Colour", Seen: false},
	{Key: "com.pyxis.greenhopper.jira:jsw-issue-color", Name: "Issue colour", Seen: false},
	{Key: "com.pyxis.greenhopper.jira:gh-epic-status", Name: "Epic Status", Seen: false},
	{Key: "com.atlassian.jira.ext.charting:timeinstatus", Name: "Time in Status", Seen: false},
	{
		Key:  "com.atlassian.jira.plugins.jira-development-integration-plugin:devsummarycf",
		Name: "Development", Seen: false,
	},
	{Key: "com.atlassian.servicedesk:vp-origin", Name: "(internal, Service Management)", Seen: false},
}

// isBookkeeping reports whether a plugin key names one of bookkeepingFields. A
// system field and a custom field this site's catalogue never named both carry
// "", which matches nothing: no entry above is empty.
func isBookkeeping(pluginKey string) bool {
	if pluginKey == "" {
		return false
	}
	for _, f := range bookkeepingFields {
		if f.Key == pluginKey {
			return true
		}
	}
	return false
}

// showBookkeeping is whether the sidebar draws the fields bookkeepingFields
// names instead of hiding and counting them. It is kernel.ScopeSession: this
// packet owns no file that outlives a restart (config.toml and the cache
// directory's ui.toml both belong to other packets), so the setting's state
// lives here rather than on disk.
//
// Flipping it does not by itself repaint a pane already on screen:
// detailContent's caller memoizes on a contentKey (internal/ui/issue/issue.go,
// outside this file) that this flag is not part of, so an open pane catches up
// on its next real change — a resize, a theme switch, the issue reloading —
// rather than the frame the setting changes on.
var showBookkeeping atomic.Bool

func init() { kernel.RegisterSetting(bookkeepingSetting()) }

// bookkeepingSetting turns showBookkeeping on, so it draws every field
// bookkeepingFields would otherwise hide and count.
func bookkeepingSetting() kernel.Setting {
	return kernel.Setting{
		ID:      "issue.bookkeeping",
		Section: "Issue",
		Title:   "Plugin fields",
		Summary: "rank, epic colour and the rest of what a Jira Software project mints for its own board and epic UI",
		Kind:    kernel.KindToggle,
		Scope:   kernel.ScopeSession,
		Options: func(kernel.Deps) []kernel.SettingOption {
			return []kernel.SettingOption{{ID: "on", Label: "on"}, {ID: "off", Label: "off"}}
		},
		Value: func(kernel.Deps) string {
			if showBookkeeping.Load() {
				return "on"
			}
			return "off"
		},
		Set: func(_ kernel.Deps, id string) tea.Cmd {
			showBookkeeping.Store(id == "on")
			return kernel.Status("plugin fields are now " + id + ", for this session")
		},
	}
}

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

// custom lists the site's own fields, sorted by the name this site displays,
// then says how many more came back with nothing in them and how many carry a
// value this program is choosing not to draw — two different answers to "what
// else is on this issue" that a row disappearing without a count cannot give.
func (r *rows) custom() {
	values, hidden, empty := r.m.customFields(r.valueRoom())
	if len(values) > 0 {
		r.heading("Fields")
	}
	for _, v := range values {
		r.field(v.label, v.text)
	}
	if empty > 0 {
		r.note(strconv.Itoa(empty) + " more, all empty")
	}
	if hidden > 0 {
		r.note(strconv.Itoa(hidden) + " " + bookkeepingNote)
	}
}

// valueRoom is how many cells a value has once the label column has its own.
func (r *rows) valueRoom() int { return max(r.width-labelWidth-2, 8) }

// named is one field's display name, the text of its value, and where it sits
// on the issue's screen right now.
type named struct {
	label, text string
	order       int
}

// noScreenOrder marks a field editmeta did not name, which is most of them on
// a site this build has never read a screen from: it sorts every such field
// after every one editmeta did, and among themselves they keep sorting by
// label exactly as they did before this program could ask.
const noScreenOrder = math.MaxInt

// customFields is every field this site defines that the issue carries a value
// for, named the way the site spells it, minus the ones bookkeepingFields
// hides, plus how many of those were hidden and how many of the ones the read
// asked for came back empty.
//
// The name comes from the answer the values arrived with. A custom field's ID
// differs on every site and its name is translated, so neither can be written
// down here; an ID the catalogue could not name shows as the ID, because a value
// nobody can label is still a value somebody put there.
//
// Ordering is the site's own screen first, in the order it put its fields in,
// and everything editmeta did not name below that alphabetically — never the
// other way, and never a reason to leave a field out: editmeta answers with
// editable fields, so a field this issue carries a value for and the current
// screen does not offer is still drawn, just last.
func (m *Model) customFields(room int) (values []named, hidden, empty int) {
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
		text := m.fieldText(ref, room)
		if text == "" {
			continue
		}
		if !showBookkeeping.Load() && isBookkeeping(ref.Schema.Custom) {
			hidden++
			continue
		}
		order := noScreenOrder
		if at, on := m.edit.Order(id); on {
			order = at
		}
		values = append(values, named{label: firstNonEmpty(ref.Name, id), text: text, order: order})
	}
	slices.SortFunc(values, func(a, b named) int {
		if a.order != b.order {
			return cmp.Compare(a.order, b.order)
		}
		return strings.Compare(a.label, b.label)
	})
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
	return values, hidden, empty
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
