package issue

import (
	"context"
	"errors"
	"strconv"
	"strings"

	"charm.land/bubbles/v2/textarea"
	tea "charm.land/bubbletea/v2"

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

// UndoAllMsg is the palette's way to U: throw every edit away.
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
		Title: "Undo all changes",
		Group: "Issue",
		Keys:  []string{undoAllBinding().Help().Key},
		Run:   func(kernel.Deps) tea.Cmd { return kernel.Broadcast(UndoAllMsg{}) },
	})
}

// dirtyMsg answers the palette's way into editing the row under the cursor,
// saving and undoing everything — the same gestures e/enter, s and U reach.
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
// It answers later by sending kernel.Pop() itself, once the prompt is
// resolved — see leavingKey.
func (m *Model) AskClose() tea.Cmd {
	if !m.anyDirty() {
		return kernel.Pop()
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

// edits is what the user has changed, in the form a draft keeps it.
func (m *Model) edits() draft {
	out := draft{Key: m.issue.Key, Site: m.deps.Site, Values: map[string]string{}}
	for i := range m.rows {
		row := &m.rows[i]
		if !row.dirty() {
			continue
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
		default:
			out.Values[row.id] = row.value
		}
	}
	if len(out.Values) == 0 {
		out.Values = nil
	}
	return out
}

// applyEdits puts a draft's edits back onto the rows, skipping anything the
// issue was not read with — a draft outlives a session, and the field list of
// the read that rebuilt these rows is not the one that produced it.
func (m *Model) applyEdits(d draft) {
	for id, value := range d.Values {
		row := m.rowByID(id)
		if row == nil || !row.fetched {
			continue
		}
		if row.kind == rkDoc {
			row.cleared, row.edited = true, nil
			continue
		}
		row.setEdited(value)
	}
	for id, choice := range d.Choices {
		row := m.rowByID(id)
		if row == nil || !row.fetched {
			continue
		}
		row.chosenID, row.value = choice.ID, choice.Label
	}
	if len(d.Description) == 0 {
		return
	}
	row := m.rowByID("description")
	if row == nil || !row.fetched {
		return
	}
	doc, err := adf.Unmarshal(d.Description)
	if err != nil {
		return
	}
	row.edited, row.cleared = &doc, false
}

// rebaseRows rebuilds the editable rows around the issue currently in hand,
// puts whatever the user had already typed back on top, and — only while
// nothing is dirty in memory — tries a persisted draft too. It runs every time
// the issue reloads: on the first open, on r and R, and after a conflict, which
// is what makes conflict handling here nothing more than "reload and rebase".
func (m *Model) rebaseRows() {
	kept := m.edits()
	fresh := buildFieldRows(m.issue)
	for i := range fresh {
		_, listed := m.edit.Order(fresh[i].id)
		fresh[i].listed = listed
	}
	m.rows = fresh
	m.applyEdits(kept)
	if !m.anyDirty() {
		if d, ok, err := m.drafts.load(m.deps.Site, m.issue.Key); err == nil && ok {
			m.applyEdits(d)
			m.draftRestored = true
		}
	}
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
}

func (m *Model) keepDraft() tea.Cmd {
	if err := m.drafts.save(m.edits()); err != nil {
		return kernel.Warn(err.Error())
	}
	return nil
}

// discardAll puts every row back to what the site holds and removes the draft.
func (m *Model) discardAll() tea.Cmd {
	m.rows = buildFieldRows(m.issue)
	for i := range m.rows {
		_, listed := m.edit.Order(m.rows[i].id)
		m.rows[i].listed = listed
	}
	m.draftRestored = false
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
		m.saveFail, _ = jira.Reason(err)
		return kernel.Fail(err)
	}
	if m.deps.Jira == nil {
		return kernel.Warn("there is no Jira connection in this session")
	}
	m.stage, m.saveFail = sideSaving, ""
	ctx, gen := m.beginSave()
	return kernel.Reply(saveDirtyPatch(ctx, m.deps.Jira, m.issue.Key, patch, gen), m.addr)
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
		return tea.Sequence(discardCmd, kernel.Pop(), kernel.Broadcast(kernel.RefreshMsg{}), kernel.Status(key+" saved"))
	}
	return join(discardCmd, join(m.fetch(), kernel.Status(key+" saved")))
}

func (m *Model) discardCmd() tea.Cmd {
	if err := m.drafts.discard(m.deps.Site, m.issue.Key); err != nil {
		return kernel.Warn(err.Error())
	}
	return nil
}

// saveFailed keeps every edit on screen and on disk. docs/UX.md principle 6 is
// that a refused write never costs the user their text, and a 409 is the case
// that principle was written for: it is answered by rereading the issue and
// rebasing the edits back on top, which rebaseRows already does for every
// reload.
func (m *Model) saveFailed(err error) tea.Cmd {
	m.stage = sideBrowse
	m.leaving = false
	m.markFieldProblems(err)
	var conflict *jira.ConflictError
	if errors.As(err, &conflict) {
		m.saveFail = "changed on the site while you edited; your changes are kept, review and save again"
		return m.fetch()
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

// undoRow puts the row under the cursor back to what the site holds.
func (m *Model) undoRow() tea.Cmd {
	cr := m.currentCursorRow()
	if cr == nil {
		return nil
	}
	row := m.rowByID(cr.id)
	if row == nil || !row.dirty() {
		return kernel.Warn("nothing to undo here")
	}
	fresh := newFieldRow(row.id, row.label, row.kind, m.issue)
	fresh.listed, fresh.problem = row.listed, ""
	*row = fresh
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
		return tea.Sequence(cmd, kernel.Pop(), kernel.Status(m.issue.Key+": the changes were thrown away"))
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
		return m.keepDraft()
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
	m.docArea = newDocArea()
	m.docArea.SetValue(adf.Markdown(row.documentNow()))
	cmd := m.docArea.Focus()
	m.stage = sideDocEdit
	m.tops[regionDesc] = 0
	return cmd
}

func (m *Model) docEditKey(msg tea.KeyPressMsg) tea.Cmd {
	switch msg.String() {
	case "ctrl+s":
		return m.commitDocEdit()
	case "esc":
		m.stage = sideBrowse
		m.docArea.Blur()
		return nil
	}
	var cmd tea.Cmd
	m.docArea, cmd = m.docArea.Update(msg)
	return cmd
}

func (m *Model) commitDocEdit() tea.Cmd {
	row := m.rowByID("description")
	if row == nil {
		m.stage = sideBrowse
		m.docArea.Blur()
		return nil
	}
	text := m.docArea.Value()
	if strings.TrimSpace(text) == "" {
		row.edited, row.cleared = nil, true
		m.stage = sideBrowse
		m.docArea.Blur()
		return m.keepDraft()
	}
	doc, err := adf.ParseMarkdownInto(row.doc, text, adf.Options{})
	if err != nil {
		return kernel.Warn(parseMarkdownProblem(err))
	}
	row.edited, row.cleared = &doc, false
	m.stage = sideBrowse
	m.docArea.Blur()
	if costs := riskyEdits(row.doc); len(costs) > 0 {
		return join(m.keepDraft(), kernel.Warn("editing this as markdown loses: "+strings.Join(costs, ", ")))
	}
	return m.keepDraft()
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
	return handOffToEditor(m.launch, m.addr, m.docGen, m.issue.Key, row.documentNow())
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
		row.edited, row.cleared = nil, true
	default:
		row.edited, row.cleared = msg.doc, false
	}
	return join(m.keepDraft(), kernel.Status(msg.note))
}
