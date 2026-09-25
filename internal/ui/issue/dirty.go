package issue

import (
	"context"
	"encoding/json"
	"errors"
	"slices"
	"strconv"
	"strings"
	"time"

	"charm.land/bubbles/v2/textarea"
	tea "charm.land/bubbletea/v2"

	"github.com/varijkapil13/saral/internal/app"
	"github.com/varijkapil13/saral/internal/ui/kernel"
	"github.com/varijkapil13/saral/internal/ui/widget"
	"github.com/varijkapil13/saral/pkg/adf"
	"github.com/varijkapil13/saral/pkg/jira"
)

// newDocArea is the description's inline editor: the same widget the comment
// composer uses, with its own cursor blink dropped for the same reason —
// keeping it would make this pane own a timer for as long as the description
// is open, and every frame reproducible is worth a solid cursor.
func newDocArea() textarea.Model {
	ta := widget.NewArea()
	ta.Prompt = ""
	ta.ShowLineNumbers = false
	ta.SetVirtualCursor(false)
	return ta
}

// sideStage is what the sidebar is doing right now, which decides both what a
// key means and whether the kernel's own keys are reachable while it is
// focused. Whether the pane is asking before it is closed is tracked
// separately (leaving): a save started from that prompt still passes through
// sideSaving, and the prompt has to survive the save failing.
type sideStage uint8

const (
	sideBrowse sideStage = iota
	sideTyping
	sideDocEdit
	sidePicking
	sideSaving
)

var (
	_ kernel.KeyCapturer = (*Model)(nil)
	_ kernel.Blocker     = (*Model)(nil)
	_ kernel.CloseAsker  = (*Model)(nil)
)

// EditFieldMsg is the palette's way to e/enter: edit the row under the sidebar
// cursor, or the description if that region has the keyboard. It is a
// broadcast because the palette never knows which issue is on screen.
type EditFieldMsg struct{}

// SaveChangesMsg is the palette's way to s: send the whole dirty set.
type SaveChangesMsg struct{}

// UndoAllMsg is the palette's way to X: throw every edit away.
type UndoAllMsg struct{}

func init() {
	kernel.RegisterCommand(kernel.Command{
		ID:    "issue.editField",
		Title: "Edit this field",
		Group: "Issue",
		Keys:  []string{editBinding().Help().Key},
		Run:   func(kernel.Deps) tea.Cmd { return kernel.Broadcast(EditFieldMsg{}) },
	})
	kernel.RegisterCommand(kernel.Command{
		ID:    "issue.save",
		Title: "Save changes",
		Group: "Issue",
		Keys:  []string{saveBinding().Help().Key},
		Run:   func(kernel.Deps) tea.Cmd { return kernel.Broadcast(SaveChangesMsg{}) },
	})
	kernel.RegisterCommand(kernel.Command{
		ID:    "issue.undoAll",
		Title: "Revert all changes",
		Group: "Issue",
		Keys:  []string{undoAllBinding().Help().Key},
		Run:   func(kernel.Deps) tea.Cmd { return kernel.Broadcast(UndoAllMsg{}) },
	})
}

// dirtyMsg answers the palette's way into editing the row under the cursor,
// saving and reverting everything — the same gestures e/enter, s and X reach.
func (m *Model) dirtyMsg(msg tea.Msg) tea.Cmd {
	switch msg.(type) {
	case EditFieldMsg:
		if m.focus == regionDesc {
			return m.startDescriptionEdit()
		}
		return m.actOnCursor()
	case SaveChangesMsg:
		return m.saveDirty()
	case UndoAllMsg:
		return m.undoAll()
	}
	return nil
}

// WantsRawKeys is true while a row is being typed into, the description
// textarea is open, a save is in flight, or the leave prompt is up. Without it
// q quits the program out from under a row being typed, and esc answers the
// leave prompt as "put it aside" instead of "stay".
func (m *Model) WantsRawKeys() bool { return m.leaving || m.stage != sideBrowse }

// BlocksClose refuses to let the pane be discarded while it holds edits nobody
// has saved. AskClose is what the kernel calls instead wherever it can, which
// is everywhere this pane is reached from; this stays as the fallback for a
// caller that only knows Blocker.
func (m *Model) BlocksClose() (string, bool) {
	if !m.anyDirty() {
		return "", false
	}
	return m.issue.Key + " has " + pluralChanges(m.dirtyCount()) + " unsaved", true
}

// AskClose puts the pane into the leave prompt instead of refusing outright.
// It answers later with kernel.Proceed(), once the prompt is resolved, so
// the kernel carries on with whichever gesture it asked about — see
// leavingKey.
func (m *Model) AskClose() tea.Cmd {
	if !m.anyDirty() {
		return kernel.Proceed()
	}
	m.leaving = true
	return nil
}

func pluralChanges(n int) string {
	if n == 1 {
		return "1 change"
	}
	return strconv.Itoa(n) + " changes"
}

// rowByID finds the persistent editable state for a field, or nil for every
// field this build has no row for at all.
func (m *Model) rowByID(id string) *fieldRow {
	for i := range m.rows {
		if m.rows[i].id == id {
			return &m.rows[i]
		}
	}
	return nil
}

func (m *Model) anyDirty() bool {
	for i := range m.rows {
		if m.rows[i].dirty() {
			return true
		}
	}
	return false
}

func (m *Model) dirtyCount() int {
	n := 0
	for i := range m.rows {
		if m.rows[i].dirty() {
			n++
		}
	}
	return n
}

// edits is what the user has changed, in the form a draft keeps it, with the
// base each edit was made against.
func (m *Model) edits() draft {
	out := draft{Key: m.issue.Key, Site: m.deps.Site, Values: map[string]string{}}
	out.Base.Updated = m.baseAt
	for i := range m.rows {
		row := &m.rows[i]
		if row.pending != nil {
			text := *row.pending
			if row.kind == rkDoc {
				out.DescriptionText = &text
			} else {
				out.Pending = setIn(out.Pending, row.id, text)
			}
		}
		if !row.dirty() {
			continue
		}
		switch row.kind {
		case rkLabels:
			was := slices.Clone(row.baseLabels)
			if was == nil {
				was = []string{}
			}
			out.LabelsBase = &was
		default:
			if out.Base.Fields == nil {
				out.Base.Fields = map[string]string{}
			}
			out.Base.Fields[row.id] = row.base
		}
		switch row.kind {
		case rkDoc:
			if row.cleared {
				out.Values[row.id] = ""
				continue
			}
			if body, err := adf.Marshal(*row.edited); err == nil {
				out.Description = body
			}
		case rkChoice, rkPerson:
			if out.Choices == nil {
				out.Choices = map[string]namedID{}
			}
			out.Choices[row.id] = namedID{ID: row.chosenID, Label: row.value}
		case rkField:
			m.customEdit(&out, row)
		default:
			out.Values[row.id] = row.value
		}
	}
	out = withHeld(out, m.held)
	if len(out.Values) == 0 {
		out.Values = nil
	}
	return out
}

func (m *Model) customEdit(out *draft, row *fieldRow) {
	switch {
	case row.custom == ckDoc && row.cleared:
		out.Values[row.id] = ""
	case row.custom == ckDoc:
		if body, err := adf.Marshal(*row.edited); err == nil {
			out.Docs = setIn(out.Docs, row.id, json.RawMessage(body))
		}
	case row.custom.chooses():
		out.Picks = setIn(out.Picks, row.id, toDraftOptions(row.picked))
	default:
		out.Values[row.id] = row.value
	}
}

func (m *Model) editBase() app.EditBase {
	return m.edits().Base
}

// applyEdits puts a draft's edits back onto the rows, skipping anything the
// issue was not read with — a draft outlives a session, and the field list of
// the read that rebuilt these rows is not the one that produced it. Each edit
// keeps the base it was made against; a draft written before bases were kept
// takes the fresh read's.
func (m *Model) applyEdits(d draft) {
	rebase := func(row *fieldRow) {
		if was, ok := d.Base.Fields[row.id]; ok && was != "" {
			row.base = was
		}
	}
	m.held = withHeld(m.heldFor(d), m.held)
	for id, value := range d.Values {
		row := m.rowByID(id)
		if row == nil || !row.fetched {
			continue
		}
		rebase(row)
		if row.isDoc() {
			row.cleared, row.edited = true, nil
			continue
		}
		if row.kind == rkLabels && d.LabelsBase != nil {
			row.baseLabels = slices.Clone(*d.LabelsBase)
		}
		row.setEdited(value)
	}
	for id, choice := range d.Choices {
		row := m.rowByID(id)
		if row == nil || !row.fetched {
			continue
		}
		rebase(row)
		row.chosenID, row.value = choice.ID, choice.Label
	}
	for id, picks := range d.Picks {
		row := m.rowByID(id)
		if row == nil || !row.fetched || row.kind != rkField {
			continue
		}
		rebase(row)
		row.picked = fromDraftOptions(picks)
		row.value = pickedText(row.picked)
	}
	for id, body := range d.Docs {
		row := m.rowByID(id)
		if row == nil || !row.fetched || !row.isDoc() {
			continue
		}
		if doc, ok := unmarshalDoc(body); ok {
			rebase(row)
			row.edited, row.cleared = &doc, false
		}
	}
	for id, text := range d.Pending {
		if row := m.rowByID(id); row != nil && row.fetched && row.isDoc() {
			held := text
			row.pending = &held
		}
	}
	if !d.Base.Updated.IsZero() {
		m.baseAt = d.Base.Updated
	}
	row := m.rowByID("description")
	if row == nil || !row.fetched {
		return
	}
	if d.DescriptionText != nil {
		text := *d.DescriptionText
		row.pending = &text
	}
	if len(d.Description) == 0 {
		return
	}
	doc, err := adf.Unmarshal(d.Description)
	if err != nil {
		return
	}
	rebase(row)
	row.edited, row.cleared = &doc, false
}

// rebaseRows rebuilds the editable rows around the issue currently in hand,
// puts whatever the user had already typed back on top, and — only while
// nothing is dirty in memory — tries a persisted draft too. It runs every time
// the issue reloads: on the first open, on r and R, and after a conflict, which
// is what makes conflict handling here "reload, rebase, and say which rows the
// site moved under".
func (m *Model) rebaseRows() {
	kept := m.edits()
	problems := map[string]string{}
	for i := range m.rows {
		if m.rows[i].dirty() && m.rows[i].problem != "" {
			problems[m.rows[i].id] = m.rows[i].problem
		}
	}
	m.rows = m.buildRows()
	m.held = draft{}
	m.applyEdits(kept)
	for i := range m.rows {
		if m.rows[i].dirty() {
			m.rows[i].problem = problems[m.rows[i].id]
		}
	}
	if !m.anyDirty() && !m.anyPending() {
		m.baseAt = m.issue.Updated
		if d, ok, err := m.drafts.load(m.deps.Site, m.issue.Key); err == nil && ok {
			m.applyEdits(d)
			m.draftRestored = true
		}
	}
	m.moved = m.flagMoved()
}

// flagMoved rebases a moved row onto the fresh read once it is marked: the user
// has been shown it. Only a full read counts; a seed or cache may just be old.
func (m *Model) flagMoved() int {
	if !m.loadedIssue {
		return 0
	}
	n := 0
	for i := range m.rows {
		row := &m.rows[i]
		if !row.dirty() || row.kind == rkLabels {
			continue
		}
		now := app.Fingerprint(m.issue, row.id)
		if row.base == now {
			continue
		}
		row.base = now
		row.problem = "changed on the site while you edited; review before saving"
		n++
	}
	if n > 0 {
		m.baseAt = m.issue.Updated
	}
	return n
}

func (m *Model) anyPending() bool {
	for i := range m.rows {
		if m.rows[i].pending != nil {
			return true
		}
	}
	return false
}

// buildRows is every row the issue in hand earns: the seven fixed ones and the
// custom fields its screen lists, each marked listed or not.
func (m *Model) buildRows() []fieldRow {
	rows := append(buildFieldRows(m.issue), m.customRows(nil)...)
	for i := range rows {
		_, listed := m.edit.Order(rows[i].id)
		rows[i].listed = listed
	}
	return rows
}

// relist refreshes which rows editmeta names, without touching the values a
// user has typed. It is what an editMetaMsg runs, since editmeta can answer
// after the issue already has: rebuilding the rows from scratch here would
// throw away typing that arrived in between.
func (m *Model) relist() {
	for i := range m.rows {
		_, listed := m.edit.Order(m.rows[i].id)
		m.rows[i].listed = listed
	}
	added := m.customRows(func(id string) bool { return m.rowByID(id) != nil })
	if len(added) == 0 {
		return
	}
	m.rows = append(m.rows, added...)
	m.placeHeld()
}

func (m *Model) keepDraft() tea.Cmd {
	if err := m.drafts.save(m.edits()); err != nil {
		return kernel.Warn(err.Error())
	}
	return nil
}

// discardAll puts every row back to what the site holds and removes the draft.
func (m *Model) discardAll() tea.Cmd {
	m.rows = m.buildRows()
	m.held = draft{}
	m.draftRestored, m.moved = false, 0
	if err := m.drafts.discard(m.deps.Site, m.issue.Key); err != nil {
		return kernel.Warn(err.Error())
	}
	return nil
}

// buildPatch turns the dirty rows into a sparse patch. Every row it reads is
// one the issue was actually read with, so nothing here can name a field the
// site was never asked about.
func (m *Model) buildPatch() (jira.IssuePatch, error) {
	var out jira.IssuePatch
	for i := range m.rows {
		row := &m.rows[i]
		if !row.dirty() {
			continue
		}
		if !row.fetched {
			return jira.IssuePatch{}, notRead(row)
		}
		if err := row.into(&out); err != nil {
			return jira.IssuePatch{}, err
		}
	}
	return out, nil
}

func (m *Model) beginSave() (ctx context.Context, gen int) {
	if m.saveCancel != nil {
		m.saveCancel()
	}
	m.saveGen++
	ctx, cancel := context.WithCancel(context.Background())
	m.saveCancel = cancel
	return ctx, m.saveGen
}

func (m *Model) currentSave(gen int) bool { return gen == m.saveGen }

// saveDirty sends every dirty row as one patch. It is what s, ctrl+s and the
// palette's "Save changes" all run, and what y runs from the leave prompt —
// the prompt itself is untouched by this and is resolved once the save lands.
func (m *Model) saveDirty() tea.Cmd {
	if m.stage == sideSaving {
		return nil
	}
	if !m.anyDirty() {
		return kernel.Warn("nothing has changed")
	}
	patch, err := m.buildPatch()
	if err != nil {
		m.markFieldProblems(err)
		m.saveFail, _ = jira.Reason(err)
		return kernel.Fail(err)
	}
	if patch.IsEmpty() {
		return kernel.Warn("nothing has changed")
	}
	if m.deps.Jira == nil {
		return kernel.Warn("there is no Jira connection in this session")
	}
	m.stage, m.saveFail = sideSaving, ""
	ctx, gen := m.beginSave()
	return kernel.Reply(saveDirtyPatch(ctx, m.deps.Jira, m.issue.Key, m.editBase(), patch, gen), m.addr)
}

func (m *Model) saveResult(msg savedMsg) tea.Cmd {
	if !m.currentSave(msg.gen) {
		return nil
	}
	if msg.err != nil {
		return m.saveFailed(msg.err)
	}
	m.stage = sideBrowse
	discardCmd := m.discardCmd()
	key := m.issue.Key
	if m.leaving {
		m.leaving = false
		return tea.Sequence(discardCmd, kernel.Proceed(), kernel.Broadcast(ChangedMsg{Key: key}), kernel.Status(key+" saved"))
	}
	return join(discardCmd, join(m.fetch(), join(kernel.Broadcast(ChangedMsg{Key: key}), kernel.Status(key+" saved"))))
}

// discardCmd drops the draft of a write that landed. Description text still
// open in the inline editor was never part of that write, so it stays.
func (m *Model) discardCmd() tea.Cmd {
	left := withHeld(draft{Key: m.issue.Key, Site: m.deps.Site}, m.held)
	for i := range m.rows {
		row := &m.rows[i]
		if row.pending == nil {
			continue
		}
		text := *row.pending
		if row.kind == rkDoc {
			left.DescriptionText = &text
		} else {
			left.Pending = setIn(left.Pending, row.id, text)
		}
	}
	if err := m.drafts.save(left); err != nil {
		return kernel.Warn(err.Error())
	}
	return nil
}

// saveFailed keeps every edit on screen and on disk. docs/UX.md principle 6 is
// that a refused write never costs the user their text. A conflict — the save's
// own re-read finding a field it writes moved — is answered by rereading the
// issue and rebasing the edits back on top, which is where flagMoved marks the
// rows the site changed.
func movedNote(n int) string {
	if n == 1 {
		return "1 field changed on the site while you edited; your change is kept, review and save again"
	}
	return strconv.Itoa(n) + " fields changed on the site while you edited; your changes are kept, review and save again"
}

const conflictNote = "changed on the site while you edited; your changes are kept, review and save again"

func (m *Model) saveFailed(err error) tea.Cmd {
	m.stage = sideBrowse
	m.leaving = false
	m.markFieldProblems(err)
	var conflict *jira.ConflictError
	if errors.As(err, &conflict) {
		m.saveFail = conflictNote
		return join(m.fetch(), kernel.Warn(m.saveFail))
	}
	m.saveFail, _ = jira.Reason(err)
	return kernel.Fail(err)
}

// markFieldProblems puts each of Jira's per-field messages on the row it is
// about, so a rejected write is shown in that field's own words.
func (m *Model) markFieldProblems(err error) {
	for i := range m.rows {
		m.rows[i].problem = ""
	}
	var invalid *jira.ValidationError
	if !errors.As(err, &invalid) {
		return
	}
	for _, field := range invalid.Fields {
		if row := m.rowByID(field.Field); row != nil {
			row.problem = field.Message
		}
	}
}

func (m *Model) undoRow() tea.Cmd {
	var row *fieldRow
	switch m.focus {
	case regionDesc:
		row = m.rowByID("description")
	case regionDetails:
		if cr := m.currentCursorRow(); cr != nil {
			row = m.rowByID(cr.id)
		}
	default:
		return kernel.Warn("nothing to revert here")
	}
	if row == nil || (!row.dirty() && row.pending == nil) {
		return kernel.Warn("nothing to revert here")
	}
	*row = m.resetRow(row)
	return m.keepDraft()
}

// undoAll drops every edit on this issue at once.
func (m *Model) undoAll() tea.Cmd {
	if !m.anyDirty() {
		return kernel.Warn("nothing has changed")
	}
	cmd := m.discardAll()
	return join(cmd, kernel.Status(m.issue.Key+": every change was thrown away"))
}

// leavingKey answers the leave prompt: y saves and then closes, n discards and
// closes right away, and esc puts the prompt away and stays.
func (m *Model) leavingKey(msg tea.KeyPressMsg) tea.Cmd {
	switch msg.String() {
	case "y":
		return m.saveDirty()
	case "n":
		m.leaving = false
		cmd := m.discardAll()
		return tea.Sequence(cmd, kernel.Proceed(), kernel.Status(m.issue.Key+": the changes were thrown away"))
	case "esc":
		m.leaving = false
		return nil
	}
	return nil
}

// currentCursorRow is the cursorRow under the sidebar cursor right now, or nil
// when the sidebar has nothing in it or the details region has never built a
// frame.
func (m *Model) currentCursorRow() *cursorRow {
	if m.cursor < 0 || m.cursor >= len(m.sideRows) {
		return nil
	}
	return &m.sideRows[m.cursor]
}

// actOnCursor is Enter or e on the details region: it edits the row under the
// cursor in place, or says the one word every read-only row answers with.
func (m *Model) actOnCursor() tea.Cmd {
	cr := m.currentCursorRow()
	if cr == nil {
		return nil
	}
	row := m.rowByID(cr.id)
	if row == nil || !row.editable() {
		return kernel.Warn("read-only")
	}
	m.draftRestored = false
	m.saveFail, row.problem = "", ""
	switch row.kind {
	case rkDoc:
		return m.startDocEdit(row)
	case rkChoice:
		return m.openChoicePicker(row)
	case rkPerson:
		return m.openPersonPicker(row)
	case rkStatus:
		return m.openStatusPicker()
	case rkField:
		if cmd, opened := m.startCustomEdit(row); opened {
			return cmd
		}
	}
	m.stage = sideTyping
	m.input.SetValue(row.value)
	m.input.CursorEnd()
	return m.input.Focus()
}

func (m *Model) typingKey(msg tea.KeyPressMsg) tea.Cmd {
	// Neither the cursor nor the stage moves while a key is only changing what
	// is typed, and both are already in the sidebar's memo key — this is the
	// one counter that tells it a keystroke happened anyway.
	m.editGen++
	switch msg.String() {
	case "enter":
		row := m.currentEditRow()
		m.stage = sideBrowse
		m.input.Blur()
		if row == nil {
			return nil
		}
		row.value = strings.TrimSpace(m.input.Value())
		return join(m.keepDraft(), m.finishTyped(row))
	case "esc":
		m.stage = sideBrowse
		m.input.Blur()
		return nil
	}
	var cmd tea.Cmd
	m.input, cmd = m.input.Update(msg)
	return cmd
}

// currentEditRow is the row a textinput or the description textarea is
// currently open on: whatever the cursor names, since neither stage lets the
// cursor move while it holds the keyboard.
func (m *Model) currentEditRow() *fieldRow {
	cr := m.currentCursorRow()
	if cr == nil {
		return nil
	}
	return m.rowByID(cr.id)
}

func (m *Model) startDocEdit(row *fieldRow) tea.Cmd {
	m.docRow = row.id
	m.mention.Close()
	m.docArea = newDocArea()
	m.docSeed = adf.Markdown(row.documentNow())
	if row.pending != nil {
		m.docArea.SetValue(*row.pending)
	} else {
		m.docArea.SetValue(m.docSeed)
	}
	m.docLosses = riskyEdits(row.doc)
	cmd := m.docArea.Focus()
	m.stage = sideDocEdit
	m.tops[regionDesc] = 0
	return cmd
}

const pendingSaveAfter = 400 * time.Millisecond

type pendingFlushMsg struct{ gen int }

func (m *Model) docEditKey(msg tea.KeyPressMsg) tea.Cmd {
	var cmd tea.Cmd
	switch {
	case msg.String() == "ctrl+s":
		m.mention.Close()
		return m.commitDocEdit()
	case m.mention.Key(msg, &m.docArea):
	case msg.String() == "esc":
		m.stage = sideBrowse
		m.docArea.Blur()
		m.holdPending()
		what := "the description text"
		if row := m.rowByID(m.docRow); row != nil && row.kind == rkField {
			what = "the " + row.label + " text"
		}
		return join(m.keepDraft(), kernel.Status(what+" is kept; e opens it again, x throws it away"))
	default:
		m.docArea, cmd = m.docArea.Update(msg)
	}
	cmd = join(cmd, kernel.Reply(m.mention.Track(&m.docArea, m.after), m.addr))
	m.holdPending()
	m.pendingGen++
	gen := m.pendingGen
	return join(cmd, m.after(pendingSaveAfter, func() tea.Msg { return kernel.ReplyTo(pendingFlushMsg{gen: gen}, m.addr) }))
}

func (m *Model) holdPending() {
	row := m.rowByID(m.docRow)
	if row == nil {
		return
	}
	text := m.docArea.Value()
	if text == m.docSeed {
		row.pending = nil
		return
	}
	row.pending = &text
}

func (m *Model) pendingFlush(msg pendingFlushMsg) tea.Cmd {
	if msg.gen != m.pendingGen {
		return nil
	}
	return m.keepDraft()
}

func (m *Model) commitDocEdit() tea.Cmd {
	row := m.rowByID(m.docRow)
	if row == nil {
		m.stage = sideBrowse
		m.docArea.Blur()
		return nil
	}
	text := m.docArea.Value()
	if strings.TrimSpace(text) == "" {
		row.edited, row.cleared, row.pending = nil, true, nil
		m.stage = sideBrowse
		m.docArea.Blur()
		return m.keepDraft()
	}
	doc, err := adf.ParseMarkdownInto(row.doc, text, adf.Options{})
	if err != nil {
		return kernel.Warn(parseMarkdownProblem(err))
	}
	row.edited, row.cleared, row.pending = &doc, false, nil
	m.stage = sideBrowse
	m.docArea.Blur()
	return join(m.keepDraft(), lossWarning(row.doc))
}

func lossWarning(d adf.Doc) tea.Cmd {
	if costs := riskyEdits(d); len(costs) > 0 {
		return kernel.Warn(lossSentence(costs))
	}
	return nil
}

func lossSentence(costs []string) string {
	return "editing this as markdown loses: " + strings.Join(costs, ", ")
}

func parseMarkdownProblem(err error) string {
	var parse *adf.ParseError
	if errors.As(err, &parse) {
		return "line " + strconv.Itoa(parse.Line) + ": " + parse.Err.Error()
	}
	return err.Error()
}

// handOffDescription is E on the description region: the existing $EDITOR
// launcher, reached from the row directly rather than through a pushed pane.
func (m *Model) handOffDescription() tea.Cmd {
	row := m.rowByID("description")
	if row == nil {
		return nil
	}
	if reason, blocked := row.blocked(); blocked {
		return kernel.Warn(reason)
	}
	m.docGen++
	return handOffToEditor(m.launch, m.addr, m.docGen, m.issue.Key, row.documentNow(), riskyEdits(row.doc))
}

// editedResult takes what the $EDITOR handoff produced.
func (m *Model) editedResult(msg editedMsg) tea.Cmd {
	if msg.gen != m.docGen {
		return nil
	}
	row := m.rowByID("description")
	switch {
	case msg.err != nil:
		m.saveFail = msg.err.Error()
		return kernel.Fail(msg.err)
	case row == nil, msg.doc == nil && !msg.cleared:
		return kernel.Status(msg.note)
	case msg.cleared:
		row.edited, row.cleared, row.pending = nil, true, nil
		return join(m.keepDraft(), kernel.Status(msg.note))
	default:
		row.edited, row.cleared, row.pending = msg.doc, false, nil
	}
	return join(m.keepDraft(), join(kernel.Status(msg.note), lossWarning(row.doc)))
}
