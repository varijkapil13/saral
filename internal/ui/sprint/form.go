package sprint

import (
	"errors"
	"strings"
	"time"

	"charm.land/bubbles/v2/textinput"
	tea "charm.land/bubbletea/v2"

	"github.com/varijkapil13/saral/internal/ui/kernel"
	"github.com/varijkapil13/saral/pkg/jira"
)

// dateLayout is how a date is typed and drawn here. It is the one format that
// is the same in every locale, which is the point: the site's own dates arrive
// as instants and go back out in the layout the adapter writes.
const dateLayout = "2006-01-02"

// dateShape is the layout as a reader sees it, which is what a placeholder and
// a complaint about a bad date both have to say.
const dateShape = "YYYY-MM-DD"

type field uint8

const (
	fieldName field = iota
	fieldGoal
	fieldStart
	fieldEnd
	fieldCount
)

func (f field) label() string {
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
		in := textinput.New()
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
	return f.mode == formEdit && rankState(f.sprint.State) == rankClosed
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
	if rankState(sp.State) == rankClosed {
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
	return at.In(loc).Format(dateLayout)
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
	start, _ := parseDate(m.form.value(fieldStart), loc)
	end, _ := parseDate(m.form.value(fieldEnd), loc)

	if m.form.mode == formCreate {
		in := jira.SprintInput{
			BoardID: m.form.board.ID,
			Name:    strings.TrimSpace(m.form.value(fieldName)),
			Goal:    strings.TrimSpace(m.form.value(fieldGoal)),
			Start:   start,
			End:     end,
		}
		ctx, gen := m.begin()
		m.inflight = opCreate
		m.chrome = [2]string{}
		return m.reply(createSprint(ctx, m.deps.Jira, in, gen))
	}

	patch, named := m.form.patch(loc)
	if !named {
		m.form.notice = "nothing on this screen has changed"
		return kernel.Warn(m.form.notice)
	}
	ctx, gen := m.begin()
	m.inflight = opUpdate
	m.chrome = [2]string{}
	return m.reply(updateSprint(ctx, m.deps.Jira, m.form.sprint.ID, patch, gen))
}

// validate fills in the problems this program can see without asking, which are
// the ones the port would refuse locally anyway. It reports whether the form can
// be sent at all.
func (f *form) validate(loc *time.Location) bool {
	f.problems = [fieldCount]string{}
	if strings.TrimSpace(f.value(fieldName)) == "" {
		f.problems[fieldName] = "a sprint needs a name"
	}
	start, startErr := parseDate(f.value(fieldStart), loc)
	end, endErr := parseDate(f.value(fieldEnd), loc)
	if startErr != nil {
		f.problems[fieldStart] = startErr.Error()
	}
	if endErr != nil {
		f.problems[fieldEnd] = endErr.Error()
	}
	if start != nil && end != nil && end.Before(*start) {
		f.problems[fieldEnd] = "a sprint cannot end before it starts"
	}
	// A date that was set and has been emptied is a request to unset one, and
	// the port has no way to send that: a nil field in the patch means leave it
	// alone, and an empty string would be a date of nothing.
	if f.mode == formEdit {
		for _, at := range [...]field{fieldStart, fieldEnd} {
			if f.was[at] != "" && strings.TrimSpace(f.value(at)) == "" {
				f.problems[at] = "a date that is set cannot be cleared from here"
			}
		}
	}
	return f.firstProblem() == ""
}

func (f *form) firstProblem() string {
	for i := range f.problems {
		if f.problems[i] != "" {
			return f.problems[i]
		}
	}
	return ""
}

// patch is the fields that have actually changed, each as a pointer, and
// reports whether any has. Everything nil is what leaves a field alone; the
// endpoint underneath nulls whatever it is not sent.
func (f *form) patch(loc *time.Location) (jira.SprintPatch, bool) {
	var out jira.SprintPatch
	named := false
	if name := strings.TrimSpace(f.value(fieldName)); name != strings.TrimSpace(f.was[fieldName]) {
		out.Name, named = &name, true
	}
	if goal := f.value(fieldGoal); goal != f.was[fieldGoal] {
		out.Goal, named = &goal, true
	}
	if f.locked() {
		return out, named
	}
	if f.value(fieldStart) != f.was[fieldStart] {
		if at, err := parseDate(f.value(fieldStart), loc); err == nil && at != nil {
			out.Start, named = at, true
		}
	}
	if f.value(fieldEnd) != f.was[fieldEnd] {
		if at, err := parseDate(f.value(fieldEnd), loc); err == nil && at != nil {
			out.End, named = at, true
		}
	}
	return out, named
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
		if at, ok := fieldOf(fe.Field); ok {
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

// fieldOf maps the API's own field names onto the fields on this screen. A name
// that is not one of them is drawn as a sentence instead of being dropped.
func fieldOf(name string) (field, bool) {
	switch name {
	case "name":
		return fieldName, true
	case "goal":
		return fieldGoal, true
	case "startDate":
		return fieldStart, true
	case "endDate":
		return fieldEnd, true
	}
	return fieldCount, false
}

// parseDate reads a date as it is typed. An empty field is not a bad date: it
// is a date that has not been given, which is what a planned sprint has.
func parseDate(s string, loc *time.Location) (*time.Time, error) {
	trimmed := strings.TrimSpace(s)
	if trimmed == "" {
		return nil, nil
	}
	at, err := time.ParseInLocation(dateLayout, trimmed, loc)
	if err != nil {
		return nil, errors.New("a date is written " + dateShape)
	}
	return &at, nil
}
