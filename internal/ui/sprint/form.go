package sprint

import (
	"errors"
	"strings"
	"time"

	"charm.land/bubbles/v2/textinput"
	tea "charm.land/bubbletea/v2"

	appsprint "github.com/varijkapil13/saral/internal/app/sprint"
	"github.com/varijkapil13/saral/internal/ui/kernel"
	"github.com/varijkapil13/saral/internal/ui/widget"
	"github.com/varijkapil13/saral/pkg/jira"
)

// dateShape is the layout as a reader sees it, which is what a placeholder and
// a complaint about a bad date both have to say.
const dateShape = "YYYY-MM-DD"

type field = appsprint.Field

const (
	fieldName  = appsprint.FieldName
	fieldGoal  = appsprint.FieldGoal
	fieldStart = appsprint.FieldStart
	fieldEnd   = appsprint.FieldEnd
	fieldCount = appsprint.FieldCount
)

func label(f field) string {
	switch f {
	case fieldName:
		return "name"
	case fieldGoal:
		return "goal"
	case fieldStart:
		return "start"
	case fieldEnd:
		return "end"
	case fieldCount:
	}
	return ""
}

type formMode uint8

const (
	formCreate formMode = iota
	formEdit
)

// form is the create-and-edit screen. It holds what was typed and what the
// sprint said before any of it was, because an update sends the fields that
// changed and nothing else: the endpoint underneath is a full replace, so a
// field that goes as an empty string is a field that has been emptied.
type form struct {
	open   bool
	mode   formMode
	board  jira.Board
	sprint jira.Sprint

	inputs   [fieldCount]textinput.Model
	was      [fieldCount]string
	problems [fieldCount]string
	at       field
	notice   string
}

func newForm() form {
	var f form
	for i := range f.inputs {
		in := widget.NewInput()
		in.Prompt = ""
		f.inputs[i] = in
	}
	f.inputs[fieldStart].Placeholder = dateShape
	f.inputs[fieldEnd].Placeholder = dateShape
	return f
}

// locked reports that the dates cannot be touched, which is a closed sprint:
// the port takes only its name and its goal, and a field that would be refused
// is not one to let somebody type into.
func (f *form) locked() bool {
	return f.mode == formEdit && appsprint.RankOf(f.sprint.State) == appsprint.RankClosed
}

func (f *form) value(at field) string { return f.inputs[at].Value() }

func (f *form) dirty() bool {
	for i := range f.inputs {
		if f.inputs[i].Value() != f.was[i] {
			return true
		}
	}
	return false
}

// resize gives every field the room left after the label, the indent and the
// cell the cursor sits in past the last rune.
func (f *form) resize(width int) {
	room := max(width-formLabel-formGutter-marker-1, 8)
	for i := range f.inputs {
		f.inputs[i].SetWidth(room)
	}
}

func (f *form) focus() {
	for i := range f.inputs {
		f.inputs[i].Blur()
	}
	_ = f.inputs[f.at].Focus()
}

func (f *form) blur() {
	for i := range f.inputs {
		f.inputs[i].Blur()
	}
}

func (f *form) close() {
	f.open = false
	f.blur()
	f.notice = ""
	f.problems = [fieldCount]string{}
}

// move steps to the next field, skipping the ones a closed sprint refuses.
func (f *form) move(by int) {
	for range int(fieldCount) {
		next := int(f.at) + by
		switch {
		case next < 0:
			next = int(fieldCount) - 1
		case next >= int(fieldCount):
			next = 0
		}
		f.at = field(next)
		if !f.locked() || (f.at != fieldStart && f.at != fieldEnd) {
			break
		}
	}
	f.focus()
}

// typeKey gives the keystroke to the field that has the cursor. Its own command
// is a cursor blink, which is a timer this view would then own for as long as
// the form is up; dropping it costs a blinking block and keeps every frame
// reproducible.
func (f *form) typeKey(msg tea.KeyPressMsg) {
	f.inputs[f.at], _ = f.inputs[f.at].Update(msg)
}

// openCreate opens the form on a sprint that does not exist yet, on a board of
// the project. A sprint is created on a board and nowhere else, so a project
// with none is told so rather than shown a form nothing can send.
func (m *Model) openCreate() tea.Cmd {
	if len(m.boards) == 0 {
		return kernel.Warn("this project has no board this session can read, and a sprint is planned on a board")
	}
	board := m.boards[0]
	if sp := m.selected(); sp.ID != 0 {
		if at := boardAt(m.boards, sp.BoardID); at >= 0 {
			board = m.boards[at]
		}
	}
	f := newForm()
	f.open, f.mode, f.board = true, formCreate, board
	m.form = f
	m.form.resize(m.width)
	m.state = filling
	m.form.at = fieldName
	m.form.focus()
	m.clicks.Forget()
	m.chrome = [2]string{}
	return nil
}

// openEdit opens the form on the sprint under the cursor, filled in with what
// the site last said about it.
func (m *Model) openEdit() tea.Cmd {
	sp := m.selected()
	if sp.ID == 0 {
		return nil
	}
	loc := m.deps.Caps.Location()
	f := newForm()
	f.open, f.mode, f.sprint = true, formEdit, sp
	if at := boardAt(m.boards, sp.BoardID); at >= 0 {
		f.board = m.boards[at]
	}
	f.inputs[fieldName].SetValue(sp.Name)
	f.inputs[fieldGoal].SetValue(sp.Goal)
	f.inputs[fieldStart].SetValue(writeDate(sp.Start, loc))
	f.inputs[fieldEnd].SetValue(writeDate(sp.End, loc))
	for i := range f.inputs {
		f.was[i] = f.inputs[i].Value()
	}
	if appsprint.RankOf(sp.State) == appsprint.RankClosed {
		f.notice = "a closed sprint takes only its name and its goal"
	}
	m.form = f
	m.form.resize(m.width)
	m.state = filling
	m.form.at = fieldName
	m.form.focus()
	m.clicks.Forget()
	m.chrome = [2]string{}
	return nil
}

func boardAt(boards []jira.Board, id int64) int {
	for i := range boards {
		if boards[i].ID == id {
			return i
		}
	}
	return -1
}

func writeDate(at *time.Time, loc *time.Location) string {
	if at == nil {
		return ""
	}
	return at.In(loc).Format(appsprint.DateLayout)
}

func (m *Model) formKey(msg tea.KeyPressMsg) tea.Cmd {
	switch m.inForm[msg.String()] {
	case actNextField:
		m.form.move(1)
		return nil
	case actPrevField:
		m.form.move(-1)
		return nil
	case actSave:
		return m.save()
	case actDiscard:
		return m.discard()
	default:
		if m.form.locked() && (m.form.at == fieldStart || m.form.at == fieldEnd) {
			return nil
		}
		m.form.typeKey(msg)
		return nil
	}
}

// formClick puts the cursor in the field that was clicked. Nothing is arithmetic
// on coordinates: each field's line is marked where it is drawn.
func (m *Model) formClick(msg tea.MouseClickMsg) tea.Cmd {
	for at := field(0); at < fieldCount; at++ {
		if !m.zones.Hit(fieldZone(at), msg) {
			continue
		}
		if m.form.locked() && (at == fieldStart || at == fieldEnd) {
			return kernel.Warn(m.form.notice)
		}
		m.form.at = at
		m.form.focus()
		return nil
	}
	switch {
	case m.zones.Hit(zoneSend, msg):
		return m.save()
	case m.zones.Hit(zoneCancel, msg):
		return m.discard()
	}
	return nil
}

func (m *Model) discard() tea.Cmd {
	m.state = browsing
	m.form.close()
	m.chrome = [2]string{}
	return kernel.Status("the sprint is as it was")
}

// save sends what the form holds, and only what it holds. What cannot be sent
// is said on the field it belongs to rather than as one sentence about the whole
// screen, because a form annotates the widget that is wrong.
func (m *Model) save() tea.Cmd {
	loc := m.deps.Caps.Location()
	if !m.form.validate(loc) {
		return kernel.Warn(m.form.firstProblem())
	}
	if m.deps.Jira == nil {
		return kernel.Warn("there is no Jira connection in this session")
	}

	if m.form.mode == formCreate {
		in := appsprint.Input(m.form.board.ID, m.form.typed(), loc)
		ctx, gen := m.begin()
		m.inflight = opCreate
		m.chrome = [2]string{}
		return m.reply(createSprint(ctx, m.deps.Jira, in, gen))
	}

	patch, named := appsprint.Patch(m.form.typed(), appsprint.Draft(m.form.was), m.form.locked(), loc)
	if !named {
		m.form.notice = "nothing on this screen has changed"
		return kernel.Warn(m.form.notice)
	}
	ctx, gen := m.begin()
	m.inflight = opUpdate
	m.chrome = [2]string{}
	return m.reply(updateSprint(ctx, m.deps.Jira, m.form.sprint.ID, patch, gen))
}

func (f *form) typed() appsprint.Draft {
	var out appsprint.Draft
	for i := range f.inputs {
		out[i] = f.inputs[i].Value()
	}
	return out
}

// validate fills in the problems this program can see without asking, which are
// the ones the port would refuse locally anyway. It reports whether the form can
// be sent at all.
func (f *form) validate(loc *time.Location) bool {
	found := appsprint.Validate(f.typed(), appsprint.Draft(f.was), loc)
	for i, p := range found {
		f.problems[i] = problemWords(p)
	}
	return f.firstProblem() == ""
}

func problemWords(p appsprint.Problem) string {
	switch p {
	case appsprint.NoName:
		return "a sprint needs a name"
	case appsprint.BadDate:
		return "a date is written " + dateShape
	case appsprint.EndsBeforeStart:
		return "a sprint cannot end before it starts"
	case appsprint.Cleared:
		return "a date that is set cannot be cleared from here"
	case appsprint.Fine:
	}
	return ""
}

func (f *form) firstProblem() string {
	for i := range f.problems {
		if f.problems[i] != "" {
			return f.problems[i]
		}
	}
	return ""
}

// annotate puts a refusal back on the fields it names. The port validates
// locally, so this arrives without a round trip, and the field names are the
// API's own — which is why they are mapped rather than printed.
func (f *form) annotate(err error) {
	var ve *jira.ValidationError
	if !errors.As(err, &ve) {
		return
	}
	var loose []string
	for _, fe := range ve.Fields {
		if at, ok := appsprint.FieldOf(fe.Field); ok {
			f.problems[at] = fe.Message
			continue
		}
		loose = append(loose, fe.Message)
	}
	loose = append(loose, ve.Messages...)
	if len(loose) > 0 {
		f.notice = strings.Join(loose, "; ")
	}
}
