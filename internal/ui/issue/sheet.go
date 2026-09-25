package issue

import (
	"context"
	"strconv"
	"strings"
	"time"

	"charm.land/bubbles/v2/textinput"
	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/varijkapil13/saral/internal/ui/kernel"
	"github.com/varijkapil13/saral/internal/ui/widget"
	"github.com/varijkapil13/saral/pkg/jira"
)

type sheet struct {
	deps kernel.Deps
	kind sheetKind
	key  string
	head string
	addr kernel.Addr

	note, fail string
	rows       []sheetRow
	cursor     int
	top        int
	busy       bool

	asking   bool
	label    string
	input    textinput.Model
	cands    []sheetRow
	pick     int
	needPick bool
	problem  string

	question string
	onYes    func() tea.Cmd
	onNo     func() tea.Cmd

	loads, looks fetchSlot
	wait         time.Duration

	width, height int
	frame         string
	dirty         bool
	zones         widget.Zoner
	clicks        *widget.Clicks
	lines         []string
}

type sheetRow struct {
	text string
	head bool
	id   string
	key  string
}

type sheetAct uint8

const (
	sheetNoAct sheetAct = iota
	sheetAdd
	sheetRemove
	sheetOpen
	sheetToggle
)

type sheetKind interface {
	load(s *sheet) tea.Cmd
	act(s *sheet, a sheetAct) tea.Cmd
	answered(s *sheet, text string, pick *sheetRow) tea.Cmd
	changed(s *sheet, text string) tea.Cmd
	keys() *sheetKeys
}

type fetchSlot struct {
	gen    int
	cancel context.CancelFunc
}

func (f *fetchSlot) stop() {
	if f.cancel != nil {
		f.cancel()
		f.cancel = nil
	}
}

type sheetMsg struct {
	slot  *fetchSlot
	gen   int
	apply func(*sheet) tea.Cmd
}

var sheetZoner widget.SharedZoner

var (
	_ kernel.View        = (*sheet)(nil)
	_ kernel.Closer      = (*sheet)(nil)
	_ kernel.Addressed   = (*sheet)(nil)
	_ kernel.KeyCapturer = (*sheet)(nil)
	_ kernel.KeyReporter = (*sheet)(nil)
)

func newSheet(d kernel.Deps, iss jira.Issue, kind sheetKind) *sheet {
	if d.Theme == nil {
		d.Theme = kernel.NewTheme(kernel.ThemeAuto, true, kernel.UnicodeGlyphs())
	}
	s := &sheet{
		deps:  d,
		kind:  kind,
		key:   iss.Key,
		head:  widget.Sanitize(strings.TrimSpace(iss.Key + "  " + iss.Summary)),
		addr:  kernel.NewAddr(),
		input: widget.NewInput(),
		wait:  250 * time.Millisecond,
		zones: sheetZoner.Get(d.Zones),
		dirty: true,
	}
	s.clicks = widget.NewClicks(d.Now)
	s.input.Prompt = "> "
	return s
}

func (s *sheet) Addr() kernel.Addr { return s.addr }

func (s *sheet) Init() tea.Cmd { return s.kind.load(s) }

func (s *sheet) Close() {
	s.loads.stop()
	s.looks.stop()
}

func (s *sheet) WantsRawKeys() bool { return s.asking || s.question != "" }

func (s *sheet) Update(msg tea.Msg) (kernel.View, tea.Cmd) {
	var cmd tea.Cmd
	switch msg := msg.(type) {
	case kernel.SizeMsg:
		s.width, s.height = msg.Width, msg.Height
		s.input.SetWidth(max(msg.Width-4, 8))
	case kernel.ThemeMsg:
		s.deps.Theme = msg.Theme
	case kernel.RefreshMsg:
		cmd = s.kind.load(s)
	case sheetMsg:
		if msg.slot == nil || msg.gen == msg.slot.gen {
			cmd = msg.apply(s)
		}
	case tea.KeyPressMsg:
		cmd = s.press(msg)
	case tea.MouseClickMsg:
		cmd = s.click(msg)
	case tea.MouseWheelMsg:
		switch msg.Button {
		case tea.MouseWheelUp:
			s.moveBy(-widget.WheelStep)
		case tea.MouseWheelDown:
			s.moveBy(widget.WheelStep)
		default:
		}
	default:
		return s, nil
	}
	s.dirty = true
	return s, cmd
}

func (s *sheet) read(slot *fetchSlot, work func(context.Context, jira.SessionClient) func(*sheet) tea.Cmd) tea.Cmd {
	c := s.deps.Jira
	if c == nil {
		return nil
	}
	slot.stop()
	slot.gen++
	gen := slot.gen
	ctx, cancel := context.WithCancel(context.Background())
	slot.cancel = cancel
	return kernel.Reply(func() tea.Msg {
		return sheetMsg{slot: slot, gen: gen, apply: work(ctx, c)}
	}, s.addr)
}

// write sends one change. It is never cancelled, closing the sheet included: a
// request already sent may have landed, and dropping its answer would leave
// nobody knowing whether it did.
func (s *sheet) write(work func(context.Context, jira.SessionClient) (func(*sheet) tea.Cmd, error)) tea.Cmd {
	c := s.deps.Jira
	if c == nil || s.busy {
		return nil
	}
	s.busy, s.fail = true, ""
	return kernel.Reply(func() tea.Msg {
		then, err := work(context.Background(), c)
		return sheetMsg{apply: func(s *sheet) tea.Cmd {
			s.busy = false
			if err != nil {
				s.fail, _ = jira.Reason(err)
				return kernel.Fail(err)
			}
			return then(s)
		}}
	}, s.addr)
}

func (s *sheet) failed(err error) tea.Cmd {
	s.fail, _ = jira.Reason(err)
	return kernel.Fail(err)
}

func (s *sheet) changedIssue() tea.Cmd { return kernel.Broadcast(ChangedMsg{Key: s.key}) }

func (s *sheet) setRows(rows []sheetRow) {
	s.rows, s.fail = rows, ""
	s.cursor = min(s.cursor, max(len(rows)-1, 0))
	s.moveBy(0)
}

func (s *sheet) ask(label, seed string, needPick bool) tea.Cmd {
	s.asking, s.label, s.needPick, s.problem = true, label, needPick, ""
	s.cands, s.pick = nil, 0
	s.input.SetValue(seed)
	s.input.CursorEnd()
	return tea.Batch(s.input.Focus(), s.kind.changed(s, seed))
}

func (s *sheet) endAsk() {
	s.asking, s.cands, s.problem = false, nil, ""
	s.looks.stop()
	s.input.Blur()
}

func (s *sheet) confirm(question string, yes, no func() tea.Cmd) {
	s.question, s.onYes, s.onNo = question, yes, no
}

func (s *sheet) debounced(run func(*sheet) tea.Cmd) tea.Cmd {
	s.looks.stop()
	s.looks.gen++
	slot, gen := &s.looks, s.looks.gen
	return kernel.Reply(tea.Tick(s.wait, func(time.Time) tea.Msg {
		return sheetMsg{slot: slot, gen: gen, apply: run}
	}), s.addr)
}

func (s *sheet) press(msg tea.KeyPressMsg) tea.Cmd {
	stroke := msg.String()
	switch {
	case s.question != "":
		return s.answer(stroke)
	case s.asking:
		return s.promptKey(msg, stroke)
	case s.busy:
		return nil
	}
	switch sheetMotions[stroke] {
	case 1:
		s.moveBy(1)
		return nil
	case -1:
		s.moveBy(-1)
		return nil
	}
	if a := s.kind.keys().acts[stroke]; a != sheetNoAct {
		return s.kind.act(s, a)
	}
	return nil
}

var sheetMotions = map[string]int{"j": 1, "down": 1, "k": -1, "up": -1}

func (s *sheet) answer(stroke string) tea.Cmd {
	var next func() tea.Cmd
	switch stroke {
	case "y":
		next = s.onYes
	case "n", "esc":
		next = s.onNo
	default:
		return nil
	}
	s.question, s.onYes, s.onNo = "", nil, nil
	if next == nil {
		return nil
	}
	return next()
}

func (s *sheet) promptKey(msg tea.KeyPressMsg, stroke string) tea.Cmd {
	switch stroke {
	case "esc":
		s.endAsk()
		return nil
	case "enter":
		return s.submit()
	case "up":
		s.pick = max(s.pick-1, 0)
		return nil
	case "down":
		s.pick = min(s.pick+1, max(len(s.cands)-1, 0))
		return nil
	}
	before := s.input.Value()
	var cmd tea.Cmd
	s.input, cmd = s.input.Update(msg)
	if after := s.input.Value(); after != before {
		s.problem, s.pick = "", 0
		cmd = tea.Batch(cmd, s.kind.changed(s, after))
	}
	return cmd
}

func (s *sheet) submit() tea.Cmd {
	var pick *sheetRow
	if s.pick < len(s.cands) {
		pick = &s.cands[s.pick]
	}
	if s.needPick && pick == nil {
		s.problem = "choose one from the list"
		return nil
	}
	return s.kind.answered(s, strings.TrimSpace(s.input.Value()), pick)
}

func (s *sheet) moveBy(d int) {
	if len(s.rows) == 0 {
		s.cursor = 0
		return
	}
	at := min(max(s.cursor+d, 0), len(s.rows)-1)
	step := 1
	if d < 0 {
		step = -1
	}
	for i := at; i >= 0 && i < len(s.rows); i += step {
		if !s.rows[i].head {
			s.cursor = i
			return
		}
	}
	for i := at; i >= 0 && i < len(s.rows); i -= step {
		if !s.rows[i].head {
			s.cursor = i
			return
		}
	}
}

func (s *sheet) current() *sheetRow {
	if s.cursor < len(s.rows) && !s.rows[s.cursor].head {
		return &s.rows[s.cursor]
	}
	return nil
}

func (s *sheet) click(msg tea.MouseClickMsg) tea.Cmd {
	if msg.Button != tea.MouseLeft || s.busy {
		return nil
	}
	if s.asking {
		for i := range s.cands {
			if s.zones.Hit(candZone(i), msg) {
				s.pick = i
				return s.submit()
			}
		}
		return nil
	}
	for i := s.top; i < len(s.rows); i++ {
		name := rowZone(i)
		if s.rows[i].head || !s.zones.Hit(name, msg) {
			continue
		}
		s.cursor = i
		if s.clicks.Double(name) {
			return s.kind.act(s, sheetOpen)
		}
		return nil
	}
	return nil
}

func rowZone(i int) string  { return "sheet:r" + strconv.Itoa(i) }
func candZone(i int) string { return "sheet:c" + strconv.Itoa(i) }

const maxCands = 6

func (s *sheet) View() string {
	if s.width <= 0 || s.height <= 0 {
		return ""
	}
	if !s.dirty {
		return s.frame
	}
	t := s.deps.Theme
	w, ell := s.width, t.Glyphs.Ellipsis
	fit := func(text string) string { return ansi.Truncate(text, w, ell) }
	lines := s.lines[:0]
	lines = append(lines, t.Title.Render(fit(s.head)))
	switch {
	case s.fail != "":
		lines = append(lines, t.Danger.Render(fit(s.fail)))
	default:
		lines = append(lines, t.Muted.Render(fit(s.note)))
	}
	lines = append(lines, t.Muted.Render(strings.Repeat(t.Glyphs.HLine, w)))

	var tail []string
	switch {
	case s.question != "":
		tail = append(tail, t.Warning.Render(fit(s.question)))
	case s.asking:
		label := s.label
		if s.problem != "" {
			label += "  " + s.problem
		}
		tail = append(tail, t.Accent.Render(fit(label)), s.input.View())
		for i := 0; i < len(s.cands) && i < maxCands; i++ {
			row := fit("  " + s.cands[i].text)
			if i == s.pick {
				row = t.Selected.Render(row)
			}
			tail = append(tail, s.zones.Mark(candZone(i), row))
		}
	}

	room := max(s.height-len(lines)-len(tail), 0)
	switch {
	case s.cursor < s.top:
		s.top = s.cursor
	case s.cursor >= s.top+room:
		s.top = s.cursor - room + 1
	}
	s.top = max(min(s.top, len(s.rows)-room), 0)
	for i := s.top; i < len(s.rows) && i < s.top+room; i++ {
		r := &s.rows[i]
		switch {
		case r.head:
			lines = append(lines, t.Accent.Render(fit(r.text)))
		case i == s.cursor && !s.asking:
			lines = append(lines, s.zones.Mark(rowZone(i), t.Selected.Render(widget.PadTruncate("  "+r.text, w, ell))))
		default:
			lines = append(lines, s.zones.Mark(rowZone(i), fit("  "+r.text)))
		}
	}
	for len(lines) < s.height-len(tail) {
		lines = append(lines, "")
	}
	lines = append(lines, tail...)
	s.lines = lines
	s.frame, s.dirty = strings.Join(lines, "\n"), false
	return s.frame
}

type sheetKeys struct {
	acts map[string]sheetAct
	sets [sheetStates]kernel.KeySet
	base int
}

type sheetState uint8

const (
	sheetBrowse sheetState = iota
	sheetBusy
	sheetAsking
	sheetConfirm
	sheetStates
)

var (
	sheetDown    = kernel.Bind([]string{"j", "down"}, "↓/j", "down")
	sheetUp      = kernel.Bind([]string{"k", "up"}, "↑/k", "up")
	sheetChoose  = kernel.Bind([]string{"enter"}, "enter", "go on")
	sheetCancel  = kernel.Bind([]string{"esc"}, "esc", "cancel")
	sheetCandUp  = kernel.Bind([]string{"up"}, "↑", "previous suggestion")
	sheetCandDn  = kernel.Bind([]string{"down"}, "↓", "next suggestion")
	sheetYes     = kernel.Bind([]string{"y"}, "y", "yes")
	sheetNo      = kernel.Bind([]string{"n", "esc"}, "n", "no")
	sheetKindSeq int
)

type sheetBind struct {
	b  kernel.Binding
	do sheetAct
}

func newSheetKeys(acts ...sheetBind) *sheetKeys {
	k := &sheetKeys{acts: map[string]sheetAct{}, base: sheetKindSeq * int(sheetStates)}
	sheetKindSeq++
	own := make([]kernel.Binding, 0, len(acts))
	for _, a := range acts {
		own = append(own, a.b)
		for _, stroke := range a.b.Keys() {
			k.acts[stroke] = a.do
		}
	}
	k.sets[sheetBrowse] = kernel.KeySet{Acts: own, Full: [][]kernel.Binding{own, {sheetDown, sheetUp}}}
	k.sets[sheetAsking] = kernel.KeySet{
		Acts: []kernel.Binding{sheetChoose, sheetCancel},
		Full: [][]kernel.Binding{{sheetChoose, sheetCancel}, {sheetCandDn, sheetCandUp}, {widget.KillLine}},
	}
	k.sets[sheetConfirm] = kernel.KeySet{Acts: []kernel.Binding{sheetYes, sheetNo}, Full: [][]kernel.Binding{{sheetYes, sheetNo}}}
	return k
}

func (s *sheet) LiveKeys() (set kernel.KeySet, gen int) {
	state := sheetBrowse
	switch {
	case s.question != "":
		state = sheetConfirm
	case s.asking:
		state = sheetAsking
	case s.busy:
		state = sheetBusy
	}
	k := s.kind.keys()
	return k.sets[state], k.base + int(state)
}
