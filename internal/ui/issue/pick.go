package issue

import (
	"context"
	"errors"
	"slices"
	"strings"

	"charm.land/bubbles/v2/textinput"
	tea "charm.land/bubbletea/v2"

	"github.com/varijkapil13/saral/internal/app"
	"github.com/varijkapil13/saral/internal/ui/kernel"
	"github.com/varijkapil13/saral/internal/ui/widget"
	"github.com/varijkapil13/saral/pkg/jira"
)

// peoplePickLimit bounds one assignee search, the same shape filter.go's own
// picker asks for — a ceiling this pane never pages past, since a person is
// found by typing more rather than by paging.
const peoplePickLimit = 20

// pickOption is one candidate an inline list offers: priority and status read
// id and label alike from the site, and an assignee's id is an account id that
// is never what a person reads — see commitAs.
type pickOption struct {
	id, label string
	// commit is what a chosen option writes to the row, when that differs from
	// the label the list shows it under — "Me — Alice" is how the assignee
	// picker's own shortcut is found, and "Alice" is what the row then reads,
	// exactly as choosing her out of a search would have left it.
	commit string
}

func (o pickOption) commitAs() string {
	if o.commit != "" {
		return o.commit
	}
	return o.label
}

// picker is the inline list open beneath a choice, person or status row — the
// same thing moveModel used to push as its own screen, drawn in place instead.
// Only one is ever open at a time, on the row id kept.
type picker struct {
	id   string
	kind rowKind

	input textinput.Model

	// all is every candidate currently held, in the order it arrived — the
	// site's for priority and status, this pane's own synthetic rows plus
	// whatever the site last answered for the assignee. ranked is all filtered
	// and ordered against what has been typed, recomputed on every keystroke.
	all    []pickOption
	ranked []pickOption
	cursor int
	top    int

	// currentID is the row's own value when the picker opened, so the
	// candidate already in force reads "(current)" rather than looking like
	// every other row.
	currentID string

	loading bool
	asked   map[string]bool
	fail    string

	// The status picker's own sub-state once a transition has been chosen:
	// moves is every transition offered, kept so a chosen id can be resolved
	// back to the jira.Transition requiredFields needs; move, fields, field and
	// confirming are exactly moveModel's own required-fields screen and
	// confirmation, drawn beneath the row instead of on a pushed pane.
	moves      []jira.Transition
	move       *jira.Transition
	fields     []moveField
	field      int
	confirming bool
}

// newPickInput is the inline list's own filter, seeded empty: dropping the
// cursor blink is the same reason newSideInput does, and the same reason the
// description's own textarea does.
func newPickInput() textinput.Model {
	ti := widget.NewInput()
	ti.Prompt = ""
	ti.SetVirtualCursor(false)
	return ti
}

// beginPicking opens the inline list for one row. Every opener starts here so
// that draftRestored and a stale save refusal never survive into a fresh
// gesture, exactly as actOnCursor already clears them for a text row.
func (m *Model) beginPicking(id string, kind rowKind) {
	m.stage = sidePicking
	m.pick = &picker{id: id, kind: kind, input: newPickInput(), asked: map[string]bool{}}
	m.draftRestored = false
	m.saveFail = ""
	m.editGen++
}

// closePicker puts the sidebar back to browsing and cancels whatever the
// picker still had in flight — a person search, a transitions read, or a move
// being applied.
func (m *Model) closePicker() {
	if m.pickCancel != nil {
		m.pickCancel()
		m.pickCancel = nil
	}
	m.pick = nil
	m.stage = sideBrowse
	m.editGen++
}

// beginPick cancels whatever this picker last asked the site for and opens a
// context for its replacement — the person search re-issued on every
// keystroke, a transitions read, or a move landing.
func (m *Model) beginPick() (ctx context.Context, gen int) {
	if m.pickCancel != nil {
		m.pickCancel()
	}
	m.pickGen++
	ctx, cancel := context.WithCancel(context.Background())
	m.pickCancel = cancel
	return ctx, m.pickGen
}

func (m *Model) currentPick(gen int) bool { return m.pick != nil && gen == m.pickGen }

// sideRowIndex finds a row's place on the sidebar's own cursor, for the
// gestures that reach a row from outside it — @ and t both work from wherever
// the cursor already is, and the row they open has to become the one under it.
func (m *Model) sideRowIndex(id string) int {
	for i, cr := range m.sideRows {
		if cr.id == id {
			return i
		}
	}
	return -1
}

// --- priority: a single choice with editmeta's own allowed values -----------

// openChoicePicker opens the inline list for priority, or any other rkChoice
// row a later packet adds. editmeta already carries the allowed values — the
// same read fetch() already asked for — so this needs no read of its own.
func (m *Model) openChoicePicker(row *fieldRow) tea.Cmd {
	idx, ok := m.edit.Order(row.id)
	if !ok {
		return kernel.Warn("read-only")
	}
	all := make([]pickOption, len(m.edit.Fields[idx].AllowedValues))
	for i, o := range m.edit.Fields[idx].AllowedValues {
		all[i] = pickOption{id: o.ID, label: firstNonEmpty(o.Label, o.ID)}
	}
	m.beginPicking(row.id, rkChoice)
	m.pick.currentID, m.pick.all = row.chosenID, all
	m.rerankPick("", row.chosenID)
	return m.pick.input.Focus()
}

// --- assignee: a person, searched on the site ------------------------------

// openPersonPicker opens the assignee's inline list: "me" and "unassigned"
// before anything is typed, and whatever the site answers once it does.
func (m *Model) openPersonPicker(row *fieldRow) tea.Cmd {
	if reason, blocked := m.peopleBlocked(); blocked {
		return kernel.Warn(reason)
	}
	m.beginPicking(row.id, rkPerson)
	m.pick.currentID = row.chosenID
	m.pick.all = m.personSeed()
	m.rerankPick("", row.chosenID)
	return join(m.pick.input.Focus(), join(m.fetchPeople(""), m.fetchMe()))
}

// openAssigneePicker is @ and the palette's "Assign to…": the assignee picker
// reached from wherever the cursor already is, rather than only from its own
// row.
func (m *Model) openAssigneePicker() tea.Cmd {
	row := m.rowByID("assignee")
	if row == nil {
		return nil
	}
	if reason, blocked := row.blocked(); blocked {
		return kernel.Warn(reason)
	}
	if at := m.sideRowIndex("assignee"); at >= 0 {
		m.cursor = at
	}
	m.focus = regionDetails
	return m.openPersonPicker(row)
}

// peopleBlocked is why the assignee cannot be looked up at all — no
// connection, or a token lacking Browse users and groups — the one place this
// pane says so rather than opening an empty list.
func (m *Model) peopleBlocked() (string, bool) {
	if m.deps.Jira == nil {
		return "there is no Jira connection in this session", true
	}
	got := m.deps.Caps.Capability(jira.CapPeople)
	if got.OK {
		return "", false
	}
	if got.Reason != "" {
		return got.Reason, true
	}
	return "this token cannot look accounts up", true
}

// personSeed is "me" and "unassigned", offered before anything is typed and
// before the site has answered anything: "me" is silent until fetchMe lands,
// since asking is who this session even is.
func (m *Model) personSeed() []pickOption {
	out := make([]pickOption, 0, 2)
	if m.me != nil {
		out = append(out, pickOption{id: m.me.AccountID, label: "Me — " + m.me.DisplayName, commit: m.me.DisplayName})
	}
	out = append(out, pickOption{id: "", label: "Unassigned", commit: "unassigned"})
	return out
}

// fetchPeople asks the site for accounts matching needle, cancelling whatever
// this picker last asked for — every keystroke re-issues the search rather
// than narrowing what is held, because PeopleQuery's own matching cannot be
// reproduced locally.
func (m *Model) fetchPeople(needle string) tea.Cmd {
	if m.pick == nil || m.deps.Jira == nil {
		return nil
	}
	m.pick.asked[needle] = true
	m.pick.loading = len(m.pick.all) <= len(m.personSeed())
	ctx, gen := m.beginPick()
	project := strings.TrimSpace(m.deps.Project)
	return kernel.Reply(findAssignees(ctx, m.deps.Jira, project, needle, peoplePickLimit, gen), m.addr)
}

func (m *Model) peopleFound(msg peopleFoundMsg) {
	if m.pick == nil || m.pick.kind != rkPerson || !m.currentPick(msg.gen) {
		return
	}
	m.pick.loading = false
	site := make([]pickOption, len(msg.people))
	for i, u := range msg.people {
		site[i] = pickOption{id: u.AccountID, label: u.DisplayName}
	}
	under := m.pickUnderCursor()
	m.pick.all = append(m.personSeed(), site...)
	m.rerankPick(m.pick.input.Value(), under)
	m.editGen++
}

// fetchMe reads this session's own account once and keeps it for as long as
// this pane is open. It is fired alongside opening the assignee picker and by
// "Assign to me" alike, and only the first caller's request actually reaches
// the site.
func (m *Model) fetchMe() tea.Cmd {
	if m.deps.Jira == nil || m.meAsked {
		return nil
	}
	m.meAsked = true
	ctx, cancel := context.WithCancel(context.Background())
	m.meCancel = cancel
	return kernel.Reply(fetchMeCmd(ctx, m.deps.Jira), m.addr)
}

func (m *Model) meLoaded(msg meLoadedMsg) tea.Cmd {
	if msg.err != nil {
		if m.pendingAssignSelf {
			m.pendingAssignSelf = false
			return kernel.Fail(msg.err)
		}
		return nil
	}
	me := msg.me
	m.me = &me
	if m.pick != nil && m.pick.kind == rkPerson {
		under := m.pickUnderCursor()
		m.pick.all = append(m.personSeed(), withoutMe(m.pick.all, me.AccountID)...)
		m.rerankPick(m.pick.input.Value(), under)
		m.editGen++
	}
	if m.pendingAssignSelf {
		m.pendingAssignSelf = false
		row := m.rowByID("assignee")
		if row == nil {
			return nil
		}
		return m.setAssignee(row, me.AccountID, me.DisplayName)
	}
	return nil
}

// withoutMe drops the seed's own placeholder for the account fetchMe just
// named, so the site's own row for that person — once one arrives — is not
// drawn twice under two different labels.
func withoutMe(all []pickOption, meID string) []pickOption {
	out := make([]pickOption, 0, len(all))
	for _, o := range all {
		if o.id == meID {
			continue
		}
		out = append(out, o)
	}
	return out
}

// assignToMe is the palette's "Assign to me": the same commit a person picked
// from the list would make, without opening one.
func (m *Model) assignToMe() tea.Cmd {
	row := m.rowByID("assignee")
	if row == nil {
		return nil
	}
	if reason, blocked := row.blocked(); blocked {
		return kernel.Warn(reason)
	}
	if m.me != nil {
		return m.setAssignee(row, m.me.AccountID, m.me.DisplayName)
	}
	if m.deps.Jira == nil {
		return kernel.Warn("there is no Jira connection in this session")
	}
	m.pendingAssignSelf = true
	return m.fetchMe()
}

// unassign is the palette's "Unassign": an empty account id, exactly what
// choosing "Unassigned" out of the list would have set.
func (m *Model) unassign() tea.Cmd {
	row := m.rowByID("assignee")
	if row == nil {
		return nil
	}
	if reason, blocked := row.blocked(); blocked {
		return kernel.Warn(reason)
	}
	return m.setAssignee(row, "", "unassigned")
}

func (m *Model) setAssignee(row *fieldRow, accountID, label string) tea.Cmd {
	row.chosenID, row.value, row.problem = accountID, label, ""
	m.draftRestored, m.saveFail = false, ""
	return m.keepDraft()
}

// --- status: the issue's own transitions ------------------------------------

// openStatusPicker opens the inline transition list — t, the palette's
// "Change this issue's status", and Enter on the Status row all reach it, and
// it works from wherever the cursor already is.
func (m *Model) openStatusPicker() tea.Cmd {
	if m.issue.Key == "" {
		return nil
	}
	if m.deps.Jira == nil {
		return kernel.Warn("there is no Jira connection in this session")
	}
	if at := m.sideRowIndex("status"); at >= 0 {
		m.cursor = at
	}
	m.focus = regionDetails
	m.beginPicking("status", rkStatus)
	m.pick.loading = true
	ctx, gen := m.beginPick()
	return kernel.Reply(loadMoves(ctx, m.deps.Jira, m.issue.Key, gen), m.addr)
}

func (m *Model) movesLoaded(msg movesLoadedMsg) {
	if m.pick == nil || m.pick.kind != rkStatus || !m.currentPick(msg.gen) {
		return
	}
	m.pick.loading = false
	m.pick.moves = msg.moves
	t := m.deps.Theme
	all := make([]pickOption, len(msg.moves))
	for i, mv := range msg.moves {
		all[i] = pickOption{id: mv.ID, label: mv.Name + "  " + t.Glyphs.Arrow + "  " + mv.To.Name}
	}
	under := m.pickUnderCursor()
	m.pick.all = all
	m.rerankPick(m.pick.input.Value(), under)
	m.editGen++
}

// chooseTransition picks one move off the list: straight to the confirmation
// when nothing required stands in the way, or the screen for what does.
func (m *Model) chooseTransition(id string) {
	p := m.pick
	at := slices.IndexFunc(p.moves, func(tr jira.Transition) bool { return tr.ID == id })
	if at < 0 {
		return
	}
	move := p.moves[at]
	p.move = &move
	p.fields = requiredFields(move)
	p.field, p.fail = 0, ""
	p.confirming = len(p.fields) == 0
	m.editGen++
}

// confirmTransitionQuestion is the named confirmation the design asks for:
// what is about to move, and how many other changes ride along with it.
func (m *Model) confirmTransitionQuestion() string {
	p := m.pick
	q := "Move " + m.issue.Key + " to " + p.move.To.Name
	for i := range p.fields {
		f := &p.fields[i]
		if !f.fillable() {
			continue
		}
		q += ". " + f.name() + " will be " + f.value().Label
	}
	if n := m.dirtyCount(); n > 0 {
		q += " and save " + pluralChanges(n)
	}
	return q + "?"
}

// applyTransition sends the dirty set through the transition endpoint
// alongside the move, since Transition takes fields exactly as UpdateIssue
// does — one request rather than two, and a status change that could not
// otherwise carry the edits sitting beside it in the dirty set.
func (m *Model) applyTransition() tea.Cmd {
	p := m.pick
	if p.move == nil {
		return nil
	}
	patch, err := m.buildPatch()
	if err != nil {
		p.fail, _ = jira.Reason(err)
		return kernel.Fail(err)
	}
	if screen := screenPatchFields(p.fields); screen.Fields.Len() > 0 {
		patch.Fields = screen.Fields
	}
	m.stage = sideSaving
	ctx, gen := m.beginPick()
	return kernel.Reply(applyMove(ctx, m.deps.Jira, m.issue.Key, p.move.ID, patch, gen), m.addr)
}

func (m *Model) moveDone(msg moveDoneMsg) tea.Cmd {
	if !m.currentPick(msg.gen) {
		return nil
	}
	to := ""
	if m.pick.move != nil {
		to = m.pick.move.To.Name
	}
	n := m.dirtyCount()
	discardCmd := m.discardCmd()
	key := m.issue.Key
	m.closePicker()
	status := key + " is now " + to
	if n > 0 {
		status += ", " + pluralChanges(n) + " saved"
	}
	return join(discardCmd, join(m.fetch(), kernel.Status(status)))
}

// pickFailed is every way a priority's already-held list never needed but a
// person search, a transitions read or a move applying can still fail. A
// conflict rereads and rebases exactly as a dirty-set save does; anything else
// is said on the picker itself, in the site's own words.
func (m *Model) pickFailed(msg editFailedMsg) tea.Cmd {
	if !m.currentPick(msg.gen) {
		return nil
	}
	var conflict *jira.ConflictError
	if errors.As(msg.err, &conflict) {
		m.closePicker()
		m.saveFail = "changed on the site while you edited; your changes are kept, review and save again"
		return m.fetch()
	}
	m.pick.loading, m.stage = false, sidePicking
	if m.pick.kind == rkStatus {
		m.pick.move, m.pick.fields, m.pick.confirming = nil, nil, false
	}
	m.pick.fail, _ = jira.Reason(msg.err)
	m.editGen++
	return kernel.Fail(msg.err)
}

// --- ranking, shared by all three -------------------------------------------

// rerankPick reorders what is held against what has been typed and puts the
// cursor back on under — a candidate id, not a row number, the same as
// filter.Model's own rerank — so a typed answer never bounces the reader back
// to the row that was already in force.
func (m *Model) rerankPick(query, under string) {
	p := m.pick
	p.ranked = rankPickOptions(p.all, query)
	p.cursor, p.top = 0, 0
	for i, opt := range p.ranked {
		if opt.id == under {
			p.cursor = i
			break
		}
	}
}

// pickUnderCursor is the candidate the cursor sits on right now, so a rerank
// that follows can put it back there rather than resetting to the top match.
func (m *Model) pickUnderCursor() string {
	p := m.pick
	if p.cursor >= 0 && p.cursor < len(p.ranked) {
		return p.ranked[p.cursor].id
	}
	return p.currentID
}

type scoredOption struct {
	opt   pickOption
	score int
	at    int
}

// rankPickOptions orders what is on offer against what has been typed, the
// same arithmetic the palette and the filter picker already rank with: the
// pattern decides and the candidates' own order settles a tie, so the site's
// order is never presented as a ranking of this pane's own.
func rankPickOptions(all []pickOption, query string) []pickOption {
	q := strings.TrimSpace(query)
	if q == "" {
		return append([]pickOption(nil), all...)
	}
	p := app.NewPattern(q)
	scored := make([]scoredOption, 0, len(all))
	for i, o := range all {
		if score, ok := p.Score(o.label); ok {
			scored = append(scored, scoredOption{opt: o, score: score, at: i})
		}
	}
	slices.SortFunc(scored, func(a, b scoredOption) int {
		if a.score != b.score {
			return b.score - a.score
		}
		return a.at - b.at
	})
	out := make([]pickOption, len(scored))
	for i, s := range scored {
		out[i] = s.opt
	}
	return out
}

// --- keys, clicks and the wheel ---------------------------------------------

// pickKey answers a key while the inline list, or the screen a chosen status
// move needs, has the keyboard.
func (m *Model) pickKey(msg tea.KeyPressMsg) tea.Cmd {
	if m.pick == nil {
		m.stage = sideBrowse
		return nil
	}
	if m.pick.kind == rkStatus && m.pick.move != nil {
		if m.pick.confirming {
			return m.pickConfirmKey(msg)
		}
		return m.pickFieldsKey(msg)
	}
	return m.pickListKey(msg)
}

// pickListKey moves the cursor over the candidates or takes a keystroke into
// the filter, the same split typingKey makes for a text row.
func (m *Model) pickListKey(msg tea.KeyPressMsg) tea.Cmd {
	p := m.pick
	switch msg.String() {
	case "enter":
		return m.choosePick()
	case "esc":
		m.closePicker()
		return nil
	case "up":
		p.cursor = max(p.cursor-1, 0)
		m.editGen++
		return nil
	case "down":
		p.cursor = min(p.cursor+1, max(len(p.ranked)-1, 0))
		m.editGen++
		return nil
	}
	under := m.pickUnderCursor()
	var cmd tea.Cmd
	p.input, cmd = p.input.Update(msg)
	m.editGen++
	m.rerankPick(p.input.Value(), under)
	if p.kind != rkPerson {
		return cmd
	}
	needle := strings.TrimSpace(p.input.Value())
	if needle == "" || p.asked[needle] {
		return cmd
	}
	return join(cmd, m.fetchPeople(needle))
}

func (m *Model) choosePick() tea.Cmd {
	p := m.pick
	if p.cursor < 0 || p.cursor >= len(p.ranked) {
		return nil
	}
	opt := p.ranked[p.cursor]
	if p.kind == rkStatus {
		m.chooseTransition(opt.id)
		return nil
	}
	row := m.rowByID(p.id)
	if row == nil {
		m.closePicker()
		return nil
	}
	row.chosenID, row.value, row.problem = opt.id, opt.commitAs(), ""
	m.closePicker()
	m.draftRestored, m.saveFail = false, ""
	return m.keepDraft()
}

func (m *Model) pickFieldsKey(msg tea.KeyPressMsg) tea.Cmd {
	p := m.pick
	switch msg.String() {
	case "esc":
		p.move, p.fields, p.field = nil, nil, 0
		m.editGen++
		return nil
	case "up":
		p.field = max(p.field-1, 0)
	case "down":
		p.field = min(p.field+1, max(len(p.fields)-1, 0))
	case "left":
		cyclePickField(p.fields, p.field, -1)
	case "right":
		cyclePickField(p.fields, p.field, 1)
	case "enter":
		if reason, blocked := unfillablePickField(p.fields); blocked {
			p.fail = reason
			m.editGen++
			return kernel.Warn(reason)
		}
		p.confirming, p.fail = true, ""
	default:
		return nil
	}
	m.editGen++
	return nil
}

func (m *Model) pickConfirmKey(msg tea.KeyPressMsg) tea.Cmd {
	p := m.pick
	if msg.String() != "y" {
		p.confirming = false
		if len(p.fields) == 0 {
			p.move, p.fields = nil, nil
		}
		m.editGen++
		return nil
	}
	return m.applyTransition()
}

// cyclePickField and unfillablePickField are moveModel's own cycleField and
// unfillable, kept apart rather than shared: both act on a []moveField that
// moveModel and this picker each hold their own copy of.
func cyclePickField(fields []moveField, at, by int) {
	if at < 0 || at >= len(fields) {
		return
	}
	f := &fields[at]
	if !f.fillable() {
		return
	}
	f.chosen = (f.chosen + by + len(f.options)) % len(f.options)
}

func unfillablePickField(fields []moveField) (string, bool) {
	for i := range fields {
		if fields[i].fillable() {
			continue
		}
		return "this move needs " + fields[i].name() +
			", and the site offered no values for it; make this one in the browser", true
	}
	return "", false
}

// screenPatchFields is moveModel's own screenPatch, over a []moveField rather
// than a receiver, so applyTransition can merge it into the dirty set's own
// patch rather than sending it alone.
func screenPatchFields(fields []moveField) jira.IssuePatch {
	values := make(map[string]jira.FieldValue, len(fields))
	for i := range fields {
		f := &fields[i]
		if !f.fillable() {
			continue
		}
		values[f.meta.Field.ID] = jira.FieldValue{Kind: jira.KindOption, Options: []jira.Option{f.value()}}
	}
	if len(values) == 0 {
		return jira.IssuePatch{}
	}
	return jira.IssuePatch{Fields: jira.NewFieldSet(values)}
}

// clickPicker answers a click while the inline list, or a chosen status
// move's screen, is open. The screen itself marks no zones, the same as
// moveModel's own never did.
func (m *Model) clickPicker(msg tea.MouseClickMsg) tea.Cmd {
	p := m.pick
	if p == nil || (p.kind == rkStatus && p.move != nil) {
		return nil
	}
	for i, opt := range p.ranked {
		if !m.zones.Hit(pickZone(p.id, opt.id), msg) {
			continue
		}
		p.cursor = i
		return m.choosePick()
	}
	return nil
}

// wheelPicker scrolls the candidates rather than the sidebar behind them,
// while the list itself is what is open.
func (m *Model) wheelPicker(msg tea.MouseWheelMsg) bool {
	p := m.pick
	if p == nil || (p.kind == rkStatus && p.move != nil) {
		return false
	}
	switch msg.Button {
	case tea.MouseWheelUp:
		p.top = max(p.top-widget.WheelStep, 0)
	case tea.MouseWheelDown:
		p.top += widget.WheelStep
	default:
		return true
	}
	m.editGen++
	return true
}
