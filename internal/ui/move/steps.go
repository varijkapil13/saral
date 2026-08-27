package move

import (
	"strings"

	tea "charm.land/bubbletea/v2"

	"github.com/varijkapil13/saral/internal/ui/kernel"
	"github.com/varijkapil13/saral/internal/ui/widget"
	"github.com/varijkapil13/saral/pkg/jira"
)

func (m *Model) key(msg tea.KeyPressMsg) tea.Cmd {
	if m.step == stepTyping {
		return m.typedKey(msg)
	}
	switch m.acts[msg.String()] {
	case actUp:
		m.moveTo(m.cursor - 1)
	case actDown:
		m.moveTo(m.cursor + 1)
	case actPageUp:
		m.moveTo(m.cursor - m.rowsHeight())
	case actPageDown:
		m.moveTo(m.cursor + m.rowsHeight())
	case actTop:
		m.moveTo(0)
	case actBottom:
		m.moveTo(m.rowCount() - 1)
	case actPrev:
		m.cycle(-1)
	case actNext:
		m.cycle(1)
	case actAct:
		return m.act()
	case actType:
		return m.startTyping()
	case actBack:
		return m.back()
	case actYes:
		return m.confirmed()
	case actNotify:
		m.toggleNotify()
	case actNone, actCancel:
	}
	return nil
}

// typedKey takes the keys while a project key is being typed. Everything the
// table does not claim is text, which is why the wizard claims raw keys here.
func (m *Model) typedKey(msg tea.KeyPressMsg) tea.Cmd {
	switch m.typing[msg.String()] {
	case actAct:
		return m.lookUp(m.input.Value())
	case actCancel:
		m.step = stepTarget
		m.input.Blur()
		m.warned = ""
		m.head, m.tail = nil, nil
		return nil
	case actNone, actUp, actDown, actPageUp, actPageDown, actTop, actBottom,
		actPrev, actNext, actType, actBack, actYes, actNotify:
	}
	// The input's own command is a cursor blink, which is a timer this view would
	// then own for as long as it is up. Dropping it costs a blinking block and
	// keeps every frame reproducible.
	m.input, _ = m.input.Update(msg)
	m.head, m.tail = nil, nil
	return nil
}

// act is what enter does, which is a different thing at every step.
func (m *Model) act() tea.Cmd {
	if m.loading {
		return nil
	}
	switch m.step {
	case stepTarget:
		return m.chooseCandidate()
	case stepType:
		return m.chooseType()
	case stepStatus:
		return m.leaveStatuses()
	case stepFields:
		return m.leaveFields()
	case stepDone:
		return kernel.Pop()
	case stepTyping, stepConfirm, stepRunning:
	}
	return nil
}

func (m *Model) startTyping() tea.Cmd {
	if m.step != stepTarget {
		return nil
	}
	m.step, m.warned = stepTyping, ""
	m.input.Reset()
	m.input.SetWidth(max(m.width-inputChrome, 8))
	_ = m.input.Focus()
	m.head, m.tail = nil, nil
	return nil
}

func (m *Model) chooseCandidate() tea.Cmd {
	if m.cursor < 0 || m.cursor >= len(m.found) {
		return nil
	}
	return m.lookUp(m.found[m.cursor])
}

// lookUp asks the site about a project key, which is the only way to find out
// whether it exists and what it will take: there is no project-list method on
// the port, so a key is validated by the answer to the question that follows it.
func (m *Model) lookUp(key string) tea.Cmd {
	key = strings.ToUpper(strings.TrimSpace(key))
	switch {
	case m.reason != "":
		m.warned = m.reason
		return kernel.Warn(m.reason)
	case key == "":
		m.warned = "a project key is needed: statuses and issue types are per project"
		return kernel.Warn(m.warned)
	}
	m.target, m.warned = key, ""
	ctx, gen := m.begin()
	return m.reply(vocabulary(ctx, m.deps.Jira, key, gen))
}

func (m *Model) chooseType() tea.Cmd {
	if m.cursor < 0 || m.cursor >= len(m.vocab) {
		return nil
	}
	// A subtask type in the target needs a parent over there, and a bulk move
	// carries no way to name one. Subtasks of the issues being moved travel with
	// them and are retyped by the site; that is a different thing from making
	// these issues subtasks.
	if m.vocab[m.cursor].Type.Subtask {
		m.warned = m.vocab[m.cursor].Type.Name + " is a subtask type, and a move cannot give these issues " +
			"a parent in " + m.target + "; the subtasks they already have travel with them anyway"
		return kernel.Warn(m.warned)
	}
	m.typeAt = m.cursor
	m.remaps = defaultRemap(sourceStatuses(m.issues), m.targetStatuses())
	m.step, m.cursor, m.top = stepStatus, 0, 0
	m.schema, m.fields, m.warned = false, nil, ""
	m.planGen++
	m.forget()
	ctx, gen := m.begin()
	return m.reply(schemaOf(ctx, m.deps.Jira, m.target, m.targetType().ID, gen))
}

// leaveStatuses will not pass a mapping the target cannot take. A source status
// with nowhere to land is a move Jira refuses, and finding that out from a 400
// after the queue has taken it is worse than being stopped here.
func (m *Model) leaveStatuses() tea.Cmd {
	if reason, blocked := m.unmapped(); blocked {
		m.warned = reason
		return kernel.Warn(reason)
	}
	if !m.schema {
		m.warned = "still asking " + m.target + " what it insists on"
		return kernel.Warn(m.warned)
	}
	m.warned = ""
	if len(m.fields) == 0 {
		return m.toConfirm()
	}
	m.step, m.cursor, m.top = stepFields, 0, 0
	m.forget()
	return nil
}

func (m *Model) unmapped() (string, bool) {
	targets := m.targetStatuses()
	if len(targets) == 0 {
		return m.targetType().Name + " in " + m.target + " reaches no status this move could land on", true
	}
	for i := range m.remaps {
		if m.remaps[i].to < 0 || m.remaps[i].to >= len(targets) {
			return m.remaps[i].from.Name + " has nothing to become in " + m.target, true
		}
	}
	return "", false
}

func (m *Model) leaveFields() tea.Cmd {
	if reason, blocked := halfAnswered(m.fields); blocked {
		m.warned = reason
		return kernel.Warn(reason)
	}
	m.warned = ""
	return m.toConfirm()
}

// toConfirm is the only door to the confirm screen, and the confirm screen is the
// only door to a submit. Nothing skips it: a move is irreversible from here
// without a second one, and the screen is where the whole resolved mapping is
// read back before anybody agrees to it.
func (m *Model) toConfirm() tea.Cmd {
	m.step, m.cursor, m.top = stepConfirm, 0, 0
	m.forget()
	if reason, over := tooMany(len(m.issues)); over {
		m.warned = reason
		return kernel.Warn(reason)
	}
	return nil
}

func (m *Model) back() tea.Cmd {
	m.warned = ""
	switch m.step {
	case stepType:
		m.step = stepTarget
		m.cursor = indexOf(m.found, m.target)
	case stepStatus:
		m.step, m.cursor = stepType, m.typeAt
	case stepFields:
		m.step, m.cursor = stepStatus, 0
	case stepConfirm:
		m.step, m.cursor = stepStatus, 0
		if len(m.fields) > 0 {
			m.step = stepFields
		}
	case stepTarget, stepTyping, stepRunning, stepDone:
		return nil
	}
	m.top = 0
	m.forget()
	m.clampScroll()
	return nil
}

func indexOf(keys []string, want string) int {
	for i, key := range keys {
		if key == want {
			return i
		}
	}
	return 0
}

func (m *Model) toggleNotify() {
	if m.step != stepConfirm {
		return
	}
	m.notify = !m.notify
	m.planGen++
	m.head, m.tail = nil, nil
}

// cycle moves one row's value, which is the target status on the remap step and
// the chosen option on the fields step.
func (m *Model) cycle(by int) {
	switch m.step {
	case stepStatus:
		targets := m.targetStatuses()
		if m.cursor < 0 || m.cursor >= len(m.remaps) || len(targets) == 0 {
			return
		}
		row := &m.remaps[m.cursor]
		row.to = (max(row.to, 0) + by + len(targets)) % len(targets)
	case stepFields:
		if m.cursor < 0 || m.cursor >= len(m.fields) {
			return
		}
		field := &m.fields[m.cursor]
		if !field.fillable() {
			return
		}
		// The values run from -1, which keeps what the source issue holds, so a
		// field can always be put back to being left alone.
		field.chosen = (field.chosen+by+2+len(field.options))%(len(field.options)+1) - 1
	case stepTarget, stepTyping, stepType, stepConfirm, stepRunning, stepDone:
		return
	}
	m.planGen++
	m.head, m.tail = nil, nil
}

// confirmed submits the move. It is reachable from the confirm screen and from
// nowhere else, and it refuses a selection the endpoint would refuse rather than
// sending the first thousand of it.
func (m *Model) confirmed() tea.Cmd {
	if m.step != stepConfirm {
		return nil
	}
	switch {
	case m.reason != "":
		m.warned = m.reason
		return kernel.Warn(m.reason)
	case len(m.issues) == 0:
		m.warned = "there is nothing here to move"
		return kernel.Warn(m.warned)
	}
	if reason, over := tooMany(len(m.issues)); over {
		m.warned = reason
		return kernel.Warn(reason)
	}
	if reason, blocked := m.unmapped(); blocked {
		m.warned = reason
		return kernel.Warn(reason)
	}
	if reason, blocked := halfAnswered(m.fields); blocked {
		m.warned = reason
		return kernel.Warn(reason)
	}
	if m.targetType().ID == "" {
		m.warned = "no issue type has been chosen in " + m.target
		return kernel.Warn(m.warned)
	}
	in := m.request()
	m.step, m.warned, m.failed = stepRunning, "", nil
	m.state, m.percent, m.paused = jira.TaskEnqueued, 0, 0
	m.cursor, m.top = 0, 0
	m.forget()
	ctx, gen := m.begin()
	return m.reply(submit(ctx, m.deps.Jira, in, gen))
}

// --- the plan on screen -----------------------------------------------------

func (m *Model) targetType() jira.IssueType {
	if m.typeAt < 0 || m.typeAt >= len(m.vocab) {
		return jira.IssueType{}
	}
	return m.vocab[m.typeAt].Type
}

// targetStatuses is the workflow the chosen issue type runs in the target
// project, looked up by type id because the same project answers differently per
// type.
func (m *Model) targetStatuses() []jira.Status {
	return statusesFor(m.vocab, m.targetType().ID)
}

func (m *Model) landing(at int) (jira.Status, bool) {
	targets := m.targetStatuses()
	if at < 0 || at >= len(m.remaps) {
		return jira.Status{}, false
	}
	to := m.remaps[at].to
	if to < 0 || to >= len(targets) {
		return jira.Status{}, false
	}
	return targets[to], true
}

// --- selection --------------------------------------------------------------

func (m *Model) rowCount() int {
	switch m.step {
	case stepTarget:
		return len(m.found)
	case stepType:
		return len(m.vocab)
	case stepStatus:
		return len(m.remaps)
	case stepFields:
		return len(m.fields)
	case stepConfirm:
		return len(m.issues)
	case stepDone:
		return len(m.failed)
	case stepTyping, stepRunning:
	}
	return 0
}

// selectable reports whether the rows on this step carry a cursor. The confirm
// screen and the list of keys a move could not shift are read, not chosen from.
func (m *Model) selectable() bool {
	switch m.step {
	case stepTarget, stepType, stepStatus, stepFields:
		return true
	case stepTyping, stepConfirm, stepRunning, stepDone:
	}
	return false
}

func (m *Model) moveTo(at int) {
	n := m.rowCount()
	if n == 0 {
		m.cursor, m.top = 0, 0
		return
	}
	if !m.selectable() {
		m.top = min(max(at, 0), max(n-m.rowsHeight(), 0))
		return
	}
	m.cursor = min(max(at, 0), n-1)
	m.warned = ""
	m.scrollToCursor()
}

func (m *Model) scrollToCursor() {
	h := m.rowsHeight()
	if m.cursor < m.top {
		m.top = m.cursor
	}
	if m.cursor >= m.top+h {
		m.top = m.cursor - h + 1
	}
	m.clampScroll()
}

func (m *Model) clampScroll() {
	m.top = min(max(m.top, 0), max(m.rowCount()-m.rowsHeight(), 0))
}

// --- mouse ------------------------------------------------------------------

// click puts the cursor on the row under the pointer, and a real double-click
// does what enter does. The pair is timed rather than read as "a second click on
// the row already selected", which cannot tell one gesture from two a minute
// apart.
func (m *Model) click(msg tea.MouseClickMsg) tea.Cmd {
	if msg.Button != tea.MouseLeft || !m.selectable() {
		return nil
	}
	for i := m.top; i < min(m.top+m.rowsHeight(), m.rowCount()); i++ {
		name := m.zoneOf(i)
		if name == "" || !m.zones.Hit(name, msg) {
			continue
		}
		if m.clicks.Double(name) {
			m.moveTo(i)
			cmd := m.act()
			// What was under the pointer is not what is there now, so the next
			// click on the same cell is a single one.
			m.clicks.Forget()
			return cmd
		}
		m.moveTo(i)
		return nil
	}
	return nil
}

// wheel scrolls the rows without moving the selection, which is what a wheel
// does everywhere else.
func (m *Model) wheel(msg tea.MouseWheelMsg) {
	switch msg.Button {
	case tea.MouseWheelUp:
		m.top -= widget.WheelStep
	case tea.MouseWheelDown:
		m.top += widget.WheelStep
	default:
		return
	}
	m.clampScroll()
}
