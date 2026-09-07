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

	"github.com/varijkapil13/saral/internal/config"
	"github.com/varijkapil13/saral/internal/ui/kernel"
	"github.com/varijkapil13/saral/internal/ui/richtext"
	"github.com/varijkapil13/saral/internal/ui/widget"
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

// platform is the built-in fields, in reading order. Status, priority and
// assignee are rows here as well as header facts, since a row is what Enter
// and a click act on; their value funcs are the fallback when no fieldRow
// exists for them — see editableField.
var platform = []detail{
	{"Status", "status", func(m *Model) string { return m.issue.Status.Name }},
	{"Priority", "priority", func(m *Model) string { return priorityName(m.issue) }},
	{"Assignee", "assignee", func(m *Model) string { return assigneeName(m.issue, "") }},
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
// Seen marks a key a real site's GET /rest/api/3/field has been observed to
// carry. Every key here is one, checked against a site running Jira Software,
// Service Management, Product Discovery, Advanced Roadmaps and ProForma; the
// answer to that call names only plugin identifiers, which are the same
// everywhere, so nothing about that instance is written down here.
//
// A key that is wrong is inert — it matches nothing on any site, so it hides
// nothing that should have been drawn — which is the direction a mistake in
// this table is safe to fall in. Adding a key on a guess is still not the way
// to grow it: the same call answers for any site.
type bookkeepingField struct {
	Key  string
	Name string // documents the row; never compared against
	Seen bool
}

// bookkeepingFields is the denylist. Add a row to extend it.
var bookkeepingFields = []bookkeepingField{
	{Key: "com.pyxis.greenhopper.jira:gh-lexo-rank", Name: "Rank", Seen: true},
	{Key: "com.pyxis.greenhopper.jira:gh-epic-color", Name: "Epic Colour", Seen: true},
	{Key: "com.pyxis.greenhopper.jira:jsw-issue-color", Name: "Issue colour", Seen: true},
	{Key: "com.pyxis.greenhopper.jira:gh-epic-status", Name: "Epic Status", Seen: true},
	{Key: "com.atlassian.jira.ext.charting:timeinstatus", Name: "Time in Status", Seen: true},
	{
		Key:  "com.atlassian.jira.plugins.jira-development-integration-plugin:devsummarycf",
		Name: "Development", Seen: true,
	},
	{Key: "com.atlassian.servicedesk:vp-origin", Name: "(internal, Service Management)", Seen: true},
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
// names instead of hiding and counting them. It lasts a run and no longer,
// which is the shape the choice has: it is turned on to answer a question
// about one issue rather than to change how the program looks.
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

// cursorRow is one row the sidebar's own cursor can land on: every field-value
// line the region draws, cursorable and read-only alike. A row that is not one
// of the four this build knows how to edit still gets one, because Enter has to
// answer *something* on it — "read-only" — rather than nothing at all.
//
// fieldRow carries the persistent state for the four this build can actually
// change; cursorRow is the transient, rebuilt-every-frame index over every row
// the region drew, which is what a cursor position and a click both resolve
// through.
type cursorRow struct {
	id       string
	label    string
	kind     rowKind
	editable bool
	// lineAt is this row's own line in content.lines, which is what keeps the
	// cursor's line in view as it moves and what a click resolves a coordinate
	// back into a row through.
	lineAt int
}

// rows builds the sidebar's lines at one width, measuring each as it goes so
// that drawing a frame never measures anything.
type rows struct {
	m     *Model
	width int
	out   content
	curs  []cursorRow
}

func (r *rows) line(s string) {
	s = clip(s, r.width, r.m.deps.Theme.Glyphs.Ellipsis)
	got := ansi.StringWidth(s)
	r.out.lines = append(r.out.lines, s)
	r.out.widths = append(r.out.widths, got)
	r.out.widest = max(r.out.widest, got)
}

// fieldRowZone is the click target for one sidebar row, marked into the line
// itself the way a description's fold markers are.
func fieldRowZone(id string) string { return "row:" + id }

// mark registers the line just drawn as one the sidebar cursor can land on,
// and marks it as that row's own click zone.
func (r *rows) mark(id, label string, kind rowKind, editable bool) {
	at := len(r.out.lines) - 1
	if at >= 0 && r.m.zones.Enabled() {
		r.out.lines[at] = r.m.zones.Mark(fieldRowZone(id), r.out.lines[at])
	}
	r.curs = append(r.curs, cursorRow{id: id, label: label, kind: kind, editable: editable, lineAt: at})
}

// detailContent is the fields region's whole content: the summary and the
// description this build can edit in place, the platform fields, the related
// issues, and every field this site defines that the issue carries a value
// for. It also rebuilds the sidebar's own cursor list, which is why it runs
// again on every frame the cursor moves on, the row under it is being typed
// into, or the description textarea is open — see contentKey.
func (m *Model) detailContent(width int) content {
	r := &rows{m: m, width: width, out: content{
		lines:  make([]string, 0, 32),
		widths: make([]int, 0, 32),
	}, curs: make([]cursorRow, 0, 32)}
	r.heading("Details")
	if !m.loadedIssue {
		r.note("Reading the issue" + m.deps.Theme.Glyphs.Ellipsis)
	}
	if row := m.rowByID("summary"); row != nil {
		r.editableField(row)
	}
	if row := m.rowByID("description"); row != nil {
		r.editableField(row)
	}
	for _, d := range platform {
		if row := m.rowByID(d.id); row != nil {
			r.editableField(row)
			continue
		}
		if m.read(d.id) {
			r.field(d.id, d.label, d.value(m))
			continue
		}
		r.missing(d.id, d.label)
	}
	r.related()
	r.custom()
	m.sideRows = r.curs
	return r.out
}

// read reports whether the issue in hand can answer for a field. Before the full
// read arrives the seed answers for what it carries and says nothing about the
// rest, because a screenful of "not asked for" is not a first paint.
func (m *Model) read(id string) bool { return !m.loadedIssue || m.issue.Requested.Has(id) }

// field draws one label and its value, and draws nothing at all when there is no
// value: a column of dashes reads as data. editableField draws the rows that
// can be edited.
func (r *rows) field(id, label, value string) {
	if strings.TrimSpace(value) == "" {
		return
	}
	r.line("  " + r.m.styles.label.Render(label) + column(label, labelWidth) + value)
	r.mark(id, label, rkStatic, false)
}

// missing says the read never asked for a field, in the space its value would
// have had.
func (r *rows) missing(id, label string) {
	r.line("  " + r.m.styles.label.Render(label) + column(label, labelWidth) +
		r.m.styles.muted.Render(absent))
	r.mark(id, label, rkStatic, false)
}

// editableField draws one of the four rows this build can change in place: the
// cursor's own arrow when it is the one selected, the typed value while it is
// open, a dirty row's glyph and — clipped away like anything else too wide for
// the sidebar — the value the site still holds beside it.
func (r *rows) editableField(row *fieldRow) {
	selected := len(r.curs) == r.m.cursor
	prefix := "  "
	if selected {
		prefix = r.m.arrowPrefix()
	}
	label := r.m.styles.label.Render(row.label) + column(row.label, labelWidth)

	picking := selected && r.m.stage == sidePicking && r.m.pick != nil && r.m.pick.id == row.id
	var value string
	switch {
	case !row.fetched && row.kind != rkStatus:
		value = r.m.styles.muted.Render(absent)
	case selected && r.m.stage == sideTyping:
		value = r.m.input.View()
	case selected && r.m.stage == sideDocEdit:
		value = r.m.styles.muted.Render("editing below" + r.m.deps.Theme.Glyphs.Ellipsis)
	case picking:
		value = r.m.pick.input.View()
	default:
		shown := row.display()
		if strings.TrimSpace(shown) == "" {
			shown = "not set"
		}
		style := r.m.styles.value
		if row.problem != "" {
			style = r.m.styles.fail
		}
		value = style.Render(shown)
		if row.dirty() {
			value = r.m.styles.selected.Render(r.m.deps.Theme.Glyphs.Bullet) + " " + value
			if was := row.before(); strings.TrimSpace(was) != "" {
				value += r.m.styles.muted.Render("  (was " + was + ")")
			}
		}
	}
	r.line(prefix + label + value)
	r.mark(row.id, row.label, row.kind, row.editable())
	if row.problem != "" {
		r.line("  " + r.m.styles.fail.Render(row.problem))
	}
	if picking {
		r.pickerLines()
	}
}

// maxPickRows bounds the inline list a choice, person or status row opens
// beneath itself: it scrolls with its own top rather than growing without
// limit, the same way moveModel's own list used to.
const maxPickRows = 8

// pickZone is the click target for one candidate in an inline list, namespaced
// by which row opened it so that an empty id — unassigned — never collides
// with another row's own empty or repeated ids.
func pickZone(pickID, optionID string) string { return "pick:" + pickID + ":" + optionID }

// pickLine draws one line already clipped to the box and then marks it,
// mirroring mark(): a zone is wrapped onto a line only after clipping has
// already settled its width, never the other way round.
func (r *rows) pickLine(s, zoneID string) {
	r.line(s)
	if zoneID == "" || !r.m.zones.Enabled() {
		return
	}
	at := len(r.out.lines) - 1
	r.out.lines[at] = r.m.zones.Mark(zoneID, r.out.lines[at])
}

// pickerLines draws the inline list a choice, person or status row has open
// beneath it: the candidates ranked against what has been typed, or — once a
// status row's candidate is a transition with a screen — the screen and the
// confirmation moveModel used to draw as a pushed pane.
func (r *rows) pickerLines() {
	p := r.m.pick
	t := r.m.deps.Theme
	switch {
	case p.kind == rkStatus && p.move != nil:
		r.pickerMoveLines()
		return
	case p.loading:
		r.note("reading" + t.Glyphs.Ellipsis)
		return
	case len(p.ranked) == 0:
		r.note("nothing matches")
	}
	labels := make([]string, len(p.ranked))
	for i, opt := range p.ranked {
		prefix := "    "
		style := r.m.styles.value
		if i == p.cursor {
			prefix = "  " + r.m.styles.selected.Render(t.Glyphs.Arrow) + " "
			style = r.m.styles.selected
		}
		text := opt.label
		if opt.id == p.currentID {
			text += "  (current)"
		}
		labels[i] = prefix + style.Render(clip(text, max(r.width-4, 8), t.Glyphs.Ellipsis))
	}
	window, top := widget.Window(labels, p.top, maxPickRows, p.cursor)
	p.top = top
	for i, line := range window {
		r.pickLine(line, pickZone(p.id, p.ranked[top+i].id))
	}
	if p.fail != "" {
		r.note(p.fail)
	}
}

// pickerMoveLines draws the transition's own screen and confirmation, the same
// content moveModel drew full-screen, now directly beneath the status row.
func (r *rows) pickerMoveLines() {
	p := r.m.pick
	t := r.m.deps.Theme
	move := *p.move
	r.note(move.Name + " " + t.Glyphs.Arrow + " " + move.To.Name)
	for i := range p.fields {
		r.moveFieldLine(i)
	}
	switch {
	case p.confirming:
		r.line("    " + r.m.styles.title.Render(clip(r.m.confirmTransitionQuestion(), max(r.width-4, 8), t.Glyphs.Ellipsis)))
		r.note("y moves it, any other key goes back")
	default:
		r.note("left and right choose a value, enter continues, esc goes back")
	}
	if p.fail != "" {
		r.note(p.fail)
	}
}

func (r *rows) moveFieldLine(at int) {
	p := r.m.pick
	t := r.m.deps.Theme
	f := &p.fields[at]
	prefix := "    "
	if at == p.field && !p.confirming {
		prefix = "  " + r.m.styles.selected.Render(t.Glyphs.Arrow) + " "
	}
	label := r.m.styles.label.Render(padTo(f.name(), max(labelWidth-2, 4)))
	if !f.fillable() {
		r.line(prefix + label + r.m.styles.fail.Render("nothing to choose from"))
		r.note("this move cannot be completed here: the site offered no values for " + f.name())
		return
	}
	room := max(r.width-labelWidth-4, 8)
	r.line(prefix + label + r.m.styles.value.Render(clip(pickerLine(f.value().Label, t), room, t.Glyphs.Ellipsis)))
}

func (r *rows) heading(text string) { r.line(r.m.styles.section.Render(text)) }

func (r *rows) note(text string) { r.line("  " + r.m.styles.muted.Render(text)) }

// related draws the parent, the subtasks and the links, each group under what
// relates them and each issue on a line of its own. The issues themselves are
// not on the sidebar's cursor: they are not a single value to edit, and what
// can be done to one already has its own gesture — opening it.
func (r *rows) related() {
	for _, d := range related {
		if !r.m.read(d.id) {
			r.missing(d.id, d.label)
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

// custom lists the site's own fields: this profile's pinned ones first, under
// their own heading and in the order they were pinned, then the rest sorted by
// the name this site displays. It ends with how many more came back with
// nothing in them and how many carry a value this program is choosing not to
// draw — two different answers to "what else is on this issue" that a row
// disappearing without a count cannot give.
func (r *rows) custom() {
	pinned, rest, hidden, empty := r.m.customFields(r.valueRoom())
	if len(pinned) > 0 {
		r.heading("Pinned")
		for _, v := range pinned {
			r.field(v.id, v.label, v.text)
		}
	}
	if len(rest) > 0 {
		r.heading("Fields")
		for _, v := range rest {
			r.field(v.id, v.label, v.text)
		}
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

// named is one field's id, its display name, the text of its value, and where
// it sits on the issue's screen right now.
type named struct {
	id, label, text string
	order           int
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
// pinned holds this profile's own picks, in the order they were pinned; rest is
// everything else, ordered the site's own screen first, in the order it put its
// fields in, and everything editmeta did not name below that alphabetically —
// never the other way, and never a reason to leave a field out: editmeta
// answers with editable fields, so a field this issue carries a value for and
// the current screen does not offer is still drawn, just last. A pinned id this
// issue carries no value for — the site no longer has it, or nobody has filled
// it in — is in neither slice, which is what leaves it off the drawing without
// touching the profile that still names it.
func (m *Model) customFields(room int) (pinned, rest []named, hidden, empty int) {
	ids := m.issue.Fields.IDs()
	values := make([]named, 0, len(ids))
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
		values = append(values, named{id: id, label: firstNonEmpty(ref.Name, id), text: text, order: order})
	}
	slices.SortFunc(values, func(a, b named) int {
		if a.order != b.order {
			return cmp.Compare(a.order, b.order)
		}
		return strings.Compare(a.label, b.label)
	})
	pinned, rest = splitPinned(values, pinnedFieldIDs(m.deps.Site))
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
	return pinned, rest, hidden, empty
}

// splitPinned pulls the pinned ids out of values, in the order pinnedIDs names
// them, and leaves everything else in the order values already had. A pinned
// id absent from values — the issue carries no value for it — is simply not in
// either slice.
func splitPinned(values []named, pinnedIDs []string) (pinned, rest []named) {
	if len(pinnedIDs) == 0 {
		return nil, values
	}
	byID := make(map[string]named, len(values))
	for _, v := range values {
		byID[v.id] = v
	}
	pinned = make([]named, 0, len(pinnedIDs))
	for _, id := range pinnedIDs {
		if v, ok := byID[id]; ok {
			pinned = append(pinned, v)
		}
	}
	rest = make([]named, 0, len(values))
	for _, v := range values {
		if !slices.Contains(pinnedIDs, v.id) {
			rest = append(rest, v)
		}
	}
	return pinned, rest
}

// pinnedFieldIDs is the field ids this profile draws first, in the order they
// were pinned — config.toml's own answer, read fresh rather than cached on the
// pane, since a pin list a settings row just changed has to be seen the next
// time a sidebar is built rather than after a restart.
//
// A profile whose site does not match the one this session is on is never the
// source, the same guard writeTheme applies before touching a profile a
// --profile flag chose instead of the active one: a session on one site must
// never draw the pins another site's profile happens to have.
func pinnedFieldIDs(site string) []string {
	cfg, err := config.Load()
	if err != nil {
		return nil
	}
	profile, err := cfg.Current()
	if err != nil || profile.Site != site {
		return nil
	}
	return profile.Pinned
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
