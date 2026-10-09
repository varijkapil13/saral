package issue

import (
	"errors"
	"slices"
	"strconv"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"

	appissue "github.com/varijkapil13/saral/internal/app/issue"
	"github.com/varijkapil13/saral/internal/ui/kernel"
	"github.com/varijkapil13/saral/internal/ui/widget"
	"github.com/varijkapil13/saral/pkg/adf"
	"github.com/varijkapil13/saral/pkg/jira"
)

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
		kind := appissue.CustomOf(meta)
		if kind == appissue.CustomNone {
			continue
		}
		label := widget.Sanitize(firstNonEmpty(meta.Name, meta.Field.Name, ref.Name, id))
		if row, ok := newCustomRow(meta, kind, label, m.issue, m.location()); ok {
			out = append(out, row)
		}
	}
	return out
}

func newCustomRow(meta jira.FieldMeta, kind appissue.Custom, label string, iss jira.Issue, loc *time.Location) (fieldRow, bool) {
	id := meta.Field.ID
	v, present := iss.Fields.ByID(id)
	if !appissue.ValueFits(kind, v, present) {
		return fieldRow{}, false
	}
	row := fieldRow{
		id: id, label: label, kind: rkField, custom: kind, meta: meta, loc: loc,
		fetched: iss.Requested.Has(id), listed: true,
	}
	switch kind {
	case appissue.CustomText, appissue.CustomURL:
		row.original = v.Text
	case appissue.CustomDoc:
		row.doc = v.Doc
	case appissue.CustomNumber:
		if v.Kind == jira.KindNumber {
			row.original = strconv.FormatFloat(v.Number, 'f', -1, 64)
		}
	case appissue.CustomDate:
		if v.Kind == jira.KindDate {
			row.original = v.Date.String()
		}
	case appissue.CustomDateTime:
		if v.Kind == jira.KindTime {
			row.original = v.Time.In(loc).Format(appissue.DateTimeLayout)
		}
	case appissue.CustomLabels:
		labels := make([]string, 0, len(v.Options))
		for _, o := range v.Options {
			labels = append(labels, firstNonEmpty(o.Label, o.ID))
		}
		row.original = strings.Join(labels, ", ")
	case appissue.CustomUser, appissue.CustomUsers:
		for _, u := range v.Users {
			row.originalPicked = append(row.originalPicked, personOption(u))
		}
	case appissue.CustomSelect, appissue.CustomMulti, appissue.CustomCascade:
		row.originalPicked = sanitizeOptions(v.Options)
	}
	if kind.Chooses() {
		row.picked = slices.Clone(row.originalPicked)
		row.original = pickedText(row.originalPicked)
	}
	row.value = row.original
	row.base = appissue.Fingerprint(iss, id)
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

func (r *fieldRow) isDoc() bool {
	return r.kind == rkDoc || (r.kind == rkField && r.custom == appissue.CustomDoc)
}

func (r *fieldRow) customDirty() bool {
	switch {
	case r.custom == appissue.CustomDoc:
		return r.edited != nil || (r.cleared && !r.doc.IsEmpty())
	case r.custom.Chooses():
		return !appissue.SamePicks(r.picked, r.originalPicked, r.custom.Multiple())
	default:
		return r.value != r.original
	}
}

// parseTyped reads what was typed into a typed custom row as the value it
// writes, or says in one clause what is wrong with it.
func (r *fieldRow) parseTyped(text string) (value jira.FieldValue, problem string) {
	v, err := appissue.ParseTyped(r.custom, text, r.loc)
	switch {
	case err == nil:
		return v, ""
	case errors.Is(err, appissue.ErrTypedNumber):
		return v, "write a number, like 3 or 2.5"
	case errors.Is(err, appissue.ErrTypedDate):
		return v, "write the date as 2006-01-02"
	case errors.Is(err, appissue.ErrTypedTime):
		return v, "write the time as 2006-01-02 15:04"
	case errors.Is(err, appissue.ErrTypedURL):
		return v, "write a whole address, starting https://"
	default:
		return v, err.Error()
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
	case appissue.CustomDoc:
		if r.edited == nil {
			return empty()
		}
		return set(jira.FieldValue{Kind: jira.KindDoc, Doc: *r.edited})
	case appissue.CustomSelect, appissue.CustomCascade:
		if len(r.picked) == 0 {
			return empty()
		}
		return set(jira.FieldValue{Kind: jira.KindOption, Options: slices.Clone(r.picked[:1])})
	case appissue.CustomMulti:
		if len(r.picked) == 0 {
			return empty()
		}
		return set(jira.FieldValue{Kind: jira.KindOptions, Options: slices.Clone(r.picked)})
	case appissue.CustomUser, appissue.CustomUsers:
		if len(r.picked) == 0 {
			return empty()
		}
		users := make([]jira.User, len(r.picked))
		for i, o := range r.picked {
			users[i] = jira.User{AccountID: o.ID, DisplayName: o.Label}
		}
		kind := jira.KindUsers
		if r.custom == appissue.CustomUser {
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
	case row.custom == appissue.CustomDoc:
		return m.startDocEdit(row), true
	case row.custom.People():
		return m.openCustomPeople(row), true
	case row.custom.Chooses():
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
	return appissue.OptionKey(picked[0])
}

// customChoices is every value a choice row's list offers: "None" first where
// the field may be emptied, then the screen's own allowed values, a cascade's
// second level each under its first.
func customChoices(row *fieldRow) []pickOption {
	out := make([]pickOption, 0, len(row.meta.AllowedValues)+1)
	if !row.custom.Multiple() && !row.meta.Required {
		out = append(out, pickOption{id: "", label: "None"})
	}
	for _, o := range sanitizeOptions(row.meta.AllowedValues) {
		label := firstNonEmpty(o.Label, o.ID)
		out = append(out, pickOption{id: o.ID, label: label, option: jira.Option{ID: o.ID, Label: o.Label}})
		if row.custom != appissue.CustomCascade {
			continue
		}
		for _, c := range o.Children {
			child := jira.Option{ID: o.ID, Label: o.Label, Children: []jira.Option{{ID: c.ID, Label: c.Label}}}
			out = append(out, pickOption{id: appissue.OptionKey(child), label: label + " / " + firstNonEmpty(c.Label, c.ID), option: child})
		}
	}
	return out
}

func (m *Model) openCustomChoice(row *fieldRow) tea.Cmd {
	m.beginPicking(row.id, rkField)
	m.pick.multi = row.custom.Multiple()
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
	m.pick.multi, m.pick.people = row.custom.Multiple(), true
	m.pick.all = m.customPeopleSeed(row, nil)
	m.markPicked(row)
	m.rerankPick("", m.pick.currentID)
	return join(m.pick.input.Focus(), join(m.fetchPeople(""), m.fetchMe()))
}

func (m *Model) markPicked(row *fieldRow) {
	m.pick.on = make(map[string]bool, len(row.picked))
	for _, o := range row.picked {
		m.pick.on[appissue.OptionKey(o)] = true
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
	if !row.custom.Multiple() && !row.meta.Required {
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
		at := slices.IndexFunc(row.picked, func(o jira.Option) bool { return appissue.OptionKey(o) == opt.id })
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
	if m.held.IsEmpty() {
		return
	}
	held := m.held
	m.held = appissue.Draft{}
	m.applyEdits(held)
	if m.loadedIssue {
		m.moved += m.flagMoved()
	}
}

// heldFor copies out of d the edits for fields that have no row, so they can
// wait for one.
func (m *Model) heldFor(d appissue.Draft) appissue.Draft {
	return appissue.Held(d, func(id string) bool { return m.rowByID(id) == nil })
}

func setIn[V any](m map[string]V, id string, v V) map[string]V {
	if m == nil {
		m = map[string]V{}
	}
	m[id] = v
	return m
}

func unmarshalDoc(body []byte) (adf.Doc, bool) {
	doc, err := adf.Unmarshal(body)
	return doc, err == nil
}
