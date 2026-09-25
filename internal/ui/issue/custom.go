package issue

import (
	"net/url"
	"slices"
	"strconv"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/varijkapil13/saral/internal/app"
	"github.com/varijkapil13/saral/internal/ui/kernel"
	"github.com/varijkapil13/saral/internal/ui/widget"
	"github.com/varijkapil13/saral/pkg/adf"
	"github.com/varijkapil13/saral/pkg/jira"
)

// customKind is the editor a custom field on the issue's own screen earns. It
// is read off the schema editmeta sent — the type, an array's element type and
// the plugin key — and never off a field's id or name, which differ per site.
type customKind uint8

const (
	ckNone customKind = iota
	ckText
	ckURL
	ckDoc
	ckNumber
	ckDate
	ckDateTime
	ckLabels
	ckSelect
	ckMulti
	ckCascade
	ckUser
	ckUsers
)

func (k customKind) chooses() bool {
	switch k {
	case ckSelect, ckMulti, ckCascade, ckUser, ckUsers:
		return true
	default:
		return false
	}
}

func (k customKind) multiple() bool { return k == ckMulti || k == ckUsers }

func (k customKind) people() bool { return k == ckUser || k == ckUsers }

// choosable are the schema types whose values are a list the screen states.
var choosable = []string{"option", "version", "component", "group"}

// dateTimeLayout is how a date-and-time field is typed and shown, in the
// account's own zone.
const dateTimeLayout = "2006-01-02 15:04"

// customKindOf decides whether a screen field gets an editor here at all. A
// field the screen does not let be set, or a shape with no editor, is ckNone
// and stays a read-only line.
func customKindOf(meta jira.FieldMeta) customKind {
	s := meta.Field.Schema
	if s.Custom == "" || !slices.Contains(meta.Operations, "set") {
		return ckNone
	}
	switch {
	case s.Type == "doc", strings.HasSuffix(s.Custom, ":textarea"):
		return ckDoc
	case strings.HasSuffix(s.Custom, ":url"):
		return ckURL
	}
	allowed := len(meta.AllowedValues) > 0
	switch s.Type {
	case "string":
		return ckText
	case "number":
		return ckNumber
	case "date":
		return ckDate
	case "datetime":
		return ckDateTime
	case "user":
		return ckUser
	case "option-with-child":
		if allowed {
			return ckCascade
		}
	case "array":
		switch {
		case s.Items == "string":
			return ckLabels
		case s.Items == "user":
			return ckUsers
		case slices.Contains(choosable, s.Items) && allowed:
			return ckMulti
		}
	default:
		if slices.Contains(choosable, s.Type) && allowed {
			return ckSelect
		}
	}
	return ckNone
}

// valueFits reports whether the value the issue carries is in the shape the
// editor writes back. One that is not — a document field the site sent as a
// plain string — is left read-only rather than overwritten from a guess.
func valueFits(k customKind, v jira.FieldValue, present bool) bool {
	if !present || v.Kind == jira.KindEmpty {
		return true
	}
	switch k {
	case ckText, ckURL:
		return v.Kind == jira.KindText
	case ckDoc:
		return v.Kind == jira.KindDoc
	case ckNumber:
		return v.Kind == jira.KindNumber
	case ckDate:
		return v.Kind == jira.KindDate
	case ckDateTime:
		return v.Kind == jira.KindTime
	case ckLabels, ckMulti:
		return v.Kind == jira.KindOptions || v.Kind == jira.KindOption
	case ckSelect, ckCascade:
		return v.Kind == jira.KindOption
	case ckUser:
		return v.Kind == jira.KindUser
	case ckUsers:
		return v.Kind == jira.KindUsers || v.Kind == jira.KindUser
	default:
		return false
	}
}

// customRows are the rows the issue's own screen earns beyond the seven fixed
// ones, in the order the screen lists them. have skips ids that already have a
// row, which is how an editmeta answer landing after the read adds rows
// without rebuilding the ones somebody is typing in.
func (m *Model) customRows(have func(id string) bool) []fieldRow {
	var out []fieldRow
	for i := range m.edit.Fields {
		meta := m.edit.Fields[i]
		id := meta.Field.ID
		if have != nil && have(id) {
			continue
		}
		ref, known := m.labels.Field(id)
		if meta.Field.Schema.Custom == "" && known {
			meta.Field.Schema = ref.Schema
		}
		if isBookkeeping(meta.Field.Schema.Custom) {
			continue
		}
		kind := customKindOf(meta)
		if kind == ckNone {
			continue
		}
		label := widget.Sanitize(firstNonEmpty(meta.Name, meta.Field.Name, ref.Name, id))
		if row, ok := newCustomRow(meta, kind, label, m.issue, m.location()); ok {
			out = append(out, row)
		}
	}
	return out
}

func newCustomRow(meta jira.FieldMeta, kind customKind, label string, iss jira.Issue, loc *time.Location) (fieldRow, bool) {
	id := meta.Field.ID
	v, present := iss.Fields.ByID(id)
	if !valueFits(kind, v, present) {
		return fieldRow{}, false
	}
	row := fieldRow{
		id: id, label: label, kind: rkField, custom: kind, meta: meta, loc: loc,
		fetched: iss.Requested.Has(id), listed: true,
	}
	switch kind {
	case ckText, ckURL:
		row.original = v.Text
	case ckDoc:
		row.doc = v.Doc
	case ckNumber:
		if v.Kind == jira.KindNumber {
			row.original = strconv.FormatFloat(v.Number, 'f', -1, 64)
		}
	case ckDate:
		if v.Kind == jira.KindDate {
			row.original = v.Date.String()
		}
	case ckDateTime:
		if v.Kind == jira.KindTime {
			row.original = v.Time.In(loc).Format(dateTimeLayout)
		}
	case ckLabels:
		labels := make([]string, 0, len(v.Options))
		for _, o := range v.Options {
			labels = append(labels, firstNonEmpty(o.Label, o.ID))
		}
		row.original = strings.Join(labels, ", ")
	case ckUser, ckUsers:
		for _, u := range v.Users {
			row.originalPicked = append(row.originalPicked, personOption(u))
		}
	case ckSelect, ckMulti, ckCascade:
		row.originalPicked = sanitizeOptions(v.Options)
	}
	if kind.chooses() {
		row.picked = slices.Clone(row.originalPicked)
		row.original = pickedText(row.originalPicked)
	}
	row.value = row.original
	row.base = app.Fingerprint(iss, id)
	return row, true
}

func personOption(u jira.User) jira.Option {
	return jira.Option{ID: u.AccountID, Label: widget.Sanitize(firstNonEmpty(u.DisplayName, u.AccountID))}
}

func sanitizeOptions(in []jira.Option) []jira.Option {
	out := make([]jira.Option, len(in))
	for i, o := range in {
		out[i] = jira.Option{ID: o.ID, Label: widget.Sanitize(o.Label), Children: sanitizeOptions(o.Children)}
	}
	return out
}

// pickedText is a choice row's value as a person reads it, a cascade's second
// level after its first.
func pickedText(in []jira.Option) string {
	parts := make([]string, 0, len(in))
	for _, o := range in {
		label := firstNonEmpty(o.Label, o.ID)
		for _, c := range o.Children {
			label += " / " + firstNonEmpty(c.Label, c.ID)
		}
		parts = append(parts, label)
	}
	return strings.Join(parts, ", ")
}

// optionKey identifies a chosen option, its second level included.
func optionKey(o jira.Option) string {
	if len(o.Children) > 0 {
		return o.ID + "/" + o.Children[0].ID
	}
	return o.ID
}

func samePicks(a, b []jira.Option, asSet bool) bool {
	if len(a) != len(b) {
		return false
	}
	ka, kb := make([]string, len(a)), make([]string, len(b))
	for i := range a {
		ka[i], kb[i] = optionKey(a[i]), optionKey(b[i])
	}
	if asSet {
		slices.Sort(ka)
		slices.Sort(kb)
	}
	return slices.Equal(ka, kb)
}

func (r *fieldRow) isDoc() bool { return r.kind == rkDoc || (r.kind == rkField && r.custom == ckDoc) }

func (r *fieldRow) customDirty() bool {
	switch {
	case r.custom == ckDoc:
		return r.edited != nil || (r.cleared && !r.doc.IsEmpty())
	case r.custom.chooses():
		return !samePicks(r.picked, r.originalPicked, r.custom.multiple())
	default:
		return r.value != r.original
	}
}

// parseTyped reads what was typed into a typed custom row as the value it
// writes, or says in one clause what is wrong with it.
func (r *fieldRow) parseTyped(text string) (value jira.FieldValue, problem string) {
	switch r.custom {
	case ckNumber:
		n, err := strconv.ParseFloat(strings.ReplaceAll(text, ",", "."), 64)
		if err != nil {
			return jira.FieldValue{}, "write a number, like 3 or 2.5"
		}
		return jira.FieldValue{Kind: jira.KindNumber, Number: n}, ""
	case ckDate:
		d, err := jira.ParseDate(text)
		if err != nil {
			return jira.FieldValue{}, "write the date as 2006-01-02"
		}
		return jira.FieldValue{Kind: jira.KindDate, Date: d}, ""
	case ckDateTime:
		loc := r.loc
		if loc == nil {
			loc = time.UTC
		}
		at, err := time.ParseInLocation(dateTimeLayout, text, loc)
		if err != nil {
			if at, err = time.Parse(time.RFC3339, text); err != nil {
				return jira.FieldValue{}, "write the time as 2006-01-02 15:04"
			}
		}
		return jira.FieldValue{Kind: jira.KindTime, Time: at}, ""
	case ckURL:
		u, err := url.Parse(text)
		if err != nil || u.Scheme == "" || u.Host == "" {
			return jira.FieldValue{}, "write a whole address, starting https://"
		}
		return jira.FieldValue{Kind: jira.KindText, Text: text}, ""
	case ckLabels:
		labels := splitLabels(text)
		opts := make([]jira.Option, len(labels))
		for i, l := range labels {
			opts[i] = jira.Option{Label: l}
		}
		return jira.FieldValue{Kind: jira.KindOptions, Options: opts}, ""
	default:
		return jira.FieldValue{Kind: jira.KindText, Text: text}, ""
	}
}

// check is what is wrong with a typed custom row's value, "" when nothing is.
func (r *fieldRow) check() string {
	text := strings.TrimSpace(r.value)
	if text == "" {
		if r.meta.Required {
			return r.label + " cannot be emptied"
		}
		return ""
	}
	_, problem := r.parseTyped(text)
	return problem
}

// customInto adds a custom row's change to a patch: a value under the site's
// own field id, or the id named in Clear when the row was emptied.
func (r *fieldRow) customInto(out *jira.IssuePatch) error {
	ref := jira.FieldRef{ID: r.id}
	empty := func() error {
		if r.meta.Required {
			return fieldProblem(r.id, r.label+" cannot be emptied")
		}
		out.Clear = append(out.Clear, ref)
		return nil
	}
	set := func(v jira.FieldValue) error {
		out.Fields = out.Fields.With(ref, v)
		return nil
	}
	switch r.custom {
	case ckDoc:
		if r.edited == nil {
			return empty()
		}
		return set(jira.FieldValue{Kind: jira.KindDoc, Doc: *r.edited})
	case ckSelect, ckCascade:
		if len(r.picked) == 0 {
			return empty()
		}
		return set(jira.FieldValue{Kind: jira.KindOption, Options: slices.Clone(r.picked[:1])})
	case ckMulti:
		if len(r.picked) == 0 {
			return empty()
		}
		return set(jira.FieldValue{Kind: jira.KindOptions, Options: slices.Clone(r.picked)})
	case ckUser, ckUsers:
		if len(r.picked) == 0 {
			return empty()
		}
		users := make([]jira.User, len(r.picked))
		for i, o := range r.picked {
			users[i] = jira.User{AccountID: o.ID, DisplayName: o.Label}
		}
		kind := jira.KindUsers
		if r.custom == ckUser {
			kind, users = jira.KindUser, users[:1]
		}
		return set(jira.FieldValue{Kind: kind, Users: users})
	}
	text := strings.TrimSpace(r.value)
	if text == "" {
		return empty()
	}
	v, problem := r.parseTyped(text)
	if problem != "" {
		return fieldProblem(r.id, problem)
	}
	return set(v)
}

// startCustomEdit opens a custom row's editor: the description's own textarea
// for a document, an inline list for a choice or a person, and the row's own
// input for everything typed. ok is false for a typed row, which actOnCursor
// opens the way it opens every other typed row.
func (m *Model) startCustomEdit(row *fieldRow) (tea.Cmd, bool) {
	switch {
	case row.custom == ckDoc:
		return m.startDocEdit(row), true
	case row.custom.people():
		return m.openCustomPeople(row), true
	case row.custom.chooses():
		return m.openCustomChoice(row), true
	}
	return nil, false
}

// finishTyped checks a typed custom row once enter keeps it. A value this row
// cannot write stays on the row and in the draft, flagged, rather than being
// thrown away.
func (m *Model) finishTyped(row *fieldRow) tea.Cmd {
	if row.kind != rkField {
		return nil
	}
	if problem := row.check(); problem != "" {
		row.problem = problem
		return kernel.Warn(row.label + ": " + problem)
	}
	return nil
}

func pickKeyOf(picked []jira.Option) string {
	if len(picked) == 0 {
		return ""
	}
	return optionKey(picked[0])
}

// customChoices is every value a choice row's list offers: "None" first where
// the field may be emptied, then the screen's own allowed values, a cascade's
// second level each under its first.
func customChoices(row *fieldRow) []pickOption {
	out := make([]pickOption, 0, len(row.meta.AllowedValues)+1)
	if !row.custom.multiple() && !row.meta.Required {
		out = append(out, pickOption{id: "", label: "None"})
	}
	for _, o := range sanitizeOptions(row.meta.AllowedValues) {
		label := firstNonEmpty(o.Label, o.ID)
		out = append(out, pickOption{id: o.ID, label: label, option: jira.Option{ID: o.ID, Label: o.Label}})
		if row.custom != ckCascade {
			continue
		}
		for _, c := range o.Children {
			child := jira.Option{ID: o.ID, Label: o.Label, Children: []jira.Option{{ID: c.ID, Label: c.Label}}}
			out = append(out, pickOption{id: optionKey(child), label: label + " / " + firstNonEmpty(c.Label, c.ID), option: child})
		}
	}
	return out
}

func (m *Model) openCustomChoice(row *fieldRow) tea.Cmd {
	m.beginPicking(row.id, rkField)
	m.pick.multi = row.custom.multiple()
	m.pick.all = customChoices(row)
	m.markPicked(row)
	m.rerankPick("", m.pick.currentID)
	return m.pick.input.Focus()
}

func (m *Model) openCustomPeople(row *fieldRow) tea.Cmd {
	if reason, blocked := m.peopleBlocked(); blocked {
		return kernel.Warn(reason)
	}
	m.beginPicking(row.id, rkField)
	m.pick.multi, m.pick.people = row.custom.multiple(), true
	m.pick.all = m.customPeopleSeed(row, nil)
	m.markPicked(row)
	m.rerankPick("", m.pick.currentID)
	return join(m.pick.input.Focus(), join(m.fetchPeople(""), m.fetchMe()))
}

func (m *Model) markPicked(row *fieldRow) {
	m.pick.on = make(map[string]bool, len(row.picked))
	for _, o := range row.picked {
		m.pick.on[optionKey(o)] = true
	}
	if !m.pick.multi {
		m.pick.currentID = pickKeyOf(row.picked)
	}
}

// customPeopleSeed is what a person row's list offers before and beside the
// site's answer: "None" where the field may be emptied, whoever is already
// chosen, and this session's own account.
func (m *Model) customPeopleSeed(row *fieldRow, found []jira.User) []pickOption {
	out := make([]pickOption, 0, len(row.picked)+len(found)+2)
	seen := make(map[string]bool, cap(out))
	add := func(o jira.Option) {
		if o.ID == "" || seen[o.ID] {
			return
		}
		seen[o.ID] = true
		out = append(out, pickOption{id: o.ID, label: o.Label, option: o})
	}
	if !row.custom.multiple() && !row.meta.Required {
		out = append(out, pickOption{id: "", label: "None"})
	}
	for _, o := range row.picked {
		add(o)
	}
	if m.me != nil {
		add(personOption(*m.me))
	}
	for _, u := range found {
		add(personOption(u))
	}
	return out
}

// chooseCustom writes one chosen value to a custom row. A single choice closes
// the list; a multiple one toggles and stays open, so several can be picked
// before esc.
func (m *Model) chooseCustom(opt pickOption) tea.Cmd {
	row := m.rowByID(m.pick.id)
	if row == nil {
		m.closePicker()
		return nil
	}
	row.problem = ""
	m.draftRestored, m.saveFail = false, ""
	if m.pick.multi {
		at := slices.IndexFunc(row.picked, func(o jira.Option) bool { return optionKey(o) == opt.id })
		if at >= 0 {
			row.picked = slices.Delete(slices.Clone(row.picked), at, at+1)
		} else {
			row.picked = append(slices.Clone(row.picked), opt.option)
		}
		m.pick.on[opt.id] = at < 0
		row.value = pickedText(row.picked)
		m.editGen++
		return m.keepDraft()
	}
	row.picked = nil
	if opt.id != "" {
		row.picked = []jira.Option{opt.option}
	}
	row.value = pickedText(row.picked)
	m.closePicker()
	return m.keepDraft()
}

// resetRow is a row as the site holds it, with the listing it already had.
func (m *Model) resetRow(row *fieldRow) fieldRow {
	fresh := *row
	if row.kind == rkField {
		if built, ok := newCustomRow(row.meta, row.custom, row.label, m.issue, m.location()); ok {
			fresh = built
		}
	} else {
		fresh = newFieldRow(row.id, row.label, row.kind, m.issue)
	}
	fresh.listed, fresh.problem = row.listed, ""
	return fresh
}

// placeHeld puts back the edits a draft held for rows that did not exist yet —
// a custom row only exists once the screen has been read — and keeps the rest
// for the next time rows are added.
func (m *Model) placeHeld() {
	if m.held.isEmpty() {
		return
	}
	held := m.held
	m.held = draft{}
	m.applyEdits(held)
	if m.loadedIssue {
		m.moved += m.flagMoved()
	}
}

// heldFor copies out of d the edits for fields that have no row, so they can
// wait for one.
func (m *Model) heldFor(d draft) draft {
	missing := func(id string) bool { return m.rowByID(id) == nil }
	out := draft{Key: d.Key, Site: d.Site}
	for id, v := range d.Values {
		if missing(id) {
			out.Values = setIn(out.Values, id, v)
		}
	}
	for id, v := range d.Choices {
		if missing(id) {
			out.Choices = setIn(out.Choices, id, v)
		}
	}
	for id, v := range d.Picks {
		if missing(id) {
			out.Picks = setIn(out.Picks, id, v)
		}
	}
	for id, v := range d.Docs {
		if missing(id) {
			out.Docs = setIn(out.Docs, id, v)
		}
	}
	for id, v := range d.Pending {
		if missing(id) {
			out.Pending = setIn(out.Pending, id, v)
		}
	}
	for id, v := range d.Base.Fields {
		if missing(id) {
			out.Base.Fields = setIn(out.Base.Fields, id, v)
		}
	}
	return out
}

// withHeld adds to d the edits still waiting for a row, without overwriting
// anything d already says about a field.
func withHeld(d, held draft) draft {
	for id, v := range held.Values {
		if _, ok := d.Values[id]; !ok {
			d.Values = setIn(d.Values, id, v)
		}
	}
	for id, v := range held.Choices {
		if _, ok := d.Choices[id]; !ok {
			d.Choices = setIn(d.Choices, id, v)
		}
	}
	for id, v := range held.Picks {
		if _, ok := d.Picks[id]; !ok {
			d.Picks = setIn(d.Picks, id, v)
		}
	}
	for id, v := range held.Docs {
		if _, ok := d.Docs[id]; !ok {
			d.Docs = setIn(d.Docs, id, v)
		}
	}
	for id, v := range held.Pending {
		if _, ok := d.Pending[id]; !ok {
			d.Pending = setIn(d.Pending, id, v)
		}
	}
	for id, v := range held.Base.Fields {
		if _, ok := d.Base.Fields[id]; !ok {
			d.Base.Fields = setIn(d.Base.Fields, id, v)
		}
	}
	return d
}

func setIn[V any](m map[string]V, id string, v V) map[string]V {
	if m == nil {
		m = map[string]V{}
	}
	m[id] = v
	return m
}

func toDraftOptions(in []jira.Option) []draftOption {
	out := make([]draftOption, len(in))
	for i, o := range in {
		out[i] = draftOption{ID: o.ID, Label: o.Label, Children: toDraftOptions(o.Children)}
	}
	return out
}

func fromDraftOptions(in []draftOption) []jira.Option {
	if len(in) == 0 {
		return nil
	}
	out := make([]jira.Option, len(in))
	for i, o := range in {
		out[i] = jira.Option{ID: o.ID, Label: o.Label, Children: fromDraftOptions(o.Children)}
	}
	return out
}

func unmarshalDoc(body []byte) (adf.Doc, bool) {
	doc, err := adf.Unmarshal(body)
	return doc, err == nil
}
