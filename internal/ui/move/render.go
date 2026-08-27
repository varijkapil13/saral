package move

import (
	"strconv"
	"strings"
	"time"

	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/varijkapil13/saral/internal/ui/kernel"
	"github.com/varijkapil13/saral/pkg/jira"
)

const (
	// marker is the gutter the cursor sits in.
	marker = 2
	gap    = 2
	// inputChrome is what the project-key line costs beyond the text itself: its
	// two-cell prompt and the cell the cursor sits in past the last rune.
	inputChrome = 3
	minName     = 12
	// maxName keeps the note beside the name rather than at the far edge of a
	// wide terminal.
	maxName   = 40
	noteWidth = 34
	// reasonLines is how many lines of a refusal the pane wraps before it stops.
	reasonLines = 4
	// barWidth is the progress bar, which carries a real percentage: the queue
	// reports one, and elapsed time is what a view draws when an API does not.
	barWidth = 20
	// rowMemoLimit holds the visible window and its overscan several relayouts
	// deep. Past it the map is cleared rather than evicted one row at a time,
	// because a scroll invalidates a screenful at once anyway.
	rowMemoLimit = 256
)

// styles are the wizard's own, built once per theme generation because
// constructing a lipgloss.Style is the expensive half of drawing a row.
type styles struct {
	gen      int
	title    lipgloss.Style
	selected lipgloss.Style
	name     lipgloss.Style
	note     lipgloss.Style
	muted    lipgloss.Style
	rule     lipgloss.Style
	accent   lipgloss.Style
	danger   lipgloss.Style
	warn     lipgloss.Style
	ok       lipgloss.Style
}

func newStyles(t *kernel.Theme) *styles {
	return &styles{
		gen:      t.Gen,
		title:    t.Title,
		selected: t.Selected,
		name:     t.Base,
		note:     t.Muted,
		muted:    t.Muted,
		rule:     t.Muted,
		accent:   t.Accent,
		danger:   t.Danger,
		warn:     t.Warning,
		ok:       t.Success,
	}
}

// layout is the column plan for one width. It is comparable so that a row
// memoized under it is invalidated by any relayout, not only by a resize.
type layout struct {
	width int
	name  int
	note  int
}

func planLayout(width int) layout {
	lay := layout{width: max(width, marker+minName), note: noteWidth}
	for {
		lay.name = lay.width - marker - optionalWidth(lay)
		if lay.name >= minName || lay.note == 0 {
			break
		}
		lay.note = 0
	}
	lay.name = max(min(lay.name, maxName), 1)
	if lay.note > 0 {
		lay.note = max(lay.width-marker-gap-lay.name, 0)
	}
	return lay
}

func optionalWidth(lay layout) int {
	if lay.note == 0 {
		return 0
	}
	return gap + lay.note
}

// rowKey is what makes two renderings of a row the same rendering.
type rowKey struct {
	step     step
	id       string
	name     string
	note     string
	lay      layout
	selected bool
	warn     bool
	gen      int
}

type rowCache struct {
	rows  map[rowKey]string
	limit int
}

func newRowCache(limit int) *rowCache {
	return &rowCache{rows: make(map[rowKey]string, limit), limit: limit}
}

func (c *rowCache) get(k rowKey) (string, bool) {
	s, ok := c.rows[k]
	return s, ok
}

func (c *rowCache) put(k rowKey, s string) {
	if len(c.rows) >= c.limit {
		clear(c.rows)
	}
	c.rows[k] = s
}

func (c *rowCache) reset() { clear(c.rows) }

// zoneOf is the click target one row is marked with. Every name is built from an
// id rather than a row number, so it is stable for the life of the wizard —
// bubblezone never frees an id.
func (m *Model) zoneOf(at int) string {
	if at < 0 || at >= m.rowCount() {
		return ""
	}
	switch m.step {
	case stepTarget:
		return "project:" + m.found[at]
	case stepType:
		return "type:" + m.vocab[at].Type.ID
	case stepStatus:
		return "status:" + m.remaps[at].from.ID
	case stepFields:
		return "field:" + m.fields[at].meta.Field.ID
	case stepTyping, stepConfirm, stepRunning, stepDone:
	}
	return ""
}

func (m *Model) row(at int) string {
	k := m.rowKeyAt(at)
	if s, ok := m.memo.get(k); ok {
		return s
	}
	s := renderRow(k, m.styles, m.deps.Theme)
	if name := m.zoneOf(at); name != "" {
		s = m.zones.Mark(name, s)
	}
	m.memo.put(k, s)
	return s
}

func (m *Model) rowKeyAt(at int) rowKey {
	k := rowKey{step: m.step, lay: m.lay, gen: m.styles.gen}
	k.selected = m.selectable() && at == m.cursor
	switch m.step {
	case stepTarget:
		k.id, k.name = m.found[at], m.found[at]
	case stepType:
		typ := m.vocab[at].Type
		k.id, k.name = typ.ID, typ.Name
		if typ.Subtask {
			k.note = "subtask type"
		}
		if len(m.vocab[at].Statuses) == 0 {
			k.note, k.warn = "no status this move could land on", true
		}
	case stepStatus:
		row := &m.remaps[at]
		k.id, k.name = row.from.ID, row.from.Name
		if to, ok := m.landing(at); ok {
			k.note = m.deps.Theme.Glyphs.Arrow + " " + to.Name + "  " + plural(row.count, "issue")
		} else {
			k.note, k.warn = "nothing to become in "+m.target, true
		}
	case stepFields:
		f := &m.fields[at]
		k.id, k.name = f.meta.Field.ID, f.name()+" *"
		switch {
		case !f.retains():
			k.note = m.deps.Theme.Glyphs.Arrow + " " + f.value().Label
		case f.fillable():
			k.note = "kept from the source issue"
		default:
			k.note = "kept from the source issue; this site offered no values to set instead"
		}
	case stepConfirm:
		iss := &m.issues[at]
		k.id, k.name, k.note = iss.Key, iss.Key, iss.Summary
	case stepDone:
		k.id, k.name, k.note, k.warn = m.failed[at], m.namedFailure(at), "did not move", true
	case stepTyping, stepRunning:
	}
	return k
}

// renderRow draws one row to exactly lay.width columns, so that a selected row's
// highlight reaches the edge.
func renderRow(k rowKey, st *styles, t *kernel.Theme) string {
	ell := t.Glyphs.Ellipsis
	var b strings.Builder
	b.Grow(k.lay.width + 32)
	if k.selected {
		b.WriteString(t.Glyphs.Arrow)
		b.WriteString(strings.Repeat(" ", max(marker-ansi.StringWidth(t.Glyphs.Arrow), 0)))
	} else {
		b.WriteString(strings.Repeat(" ", marker))
	}
	name := padTruncate(k.name, k.lay.name, ell)
	switch {
	case k.selected:
		b.WriteString(name)
	case k.warn:
		b.WriteString(st.warn.Render(name))
	default:
		b.WriteString(st.name.Render(name))
	}
	if k.lay.note > 0 {
		b.WriteString(strings.Repeat(" ", gap))
		note := padTruncate(k.note, k.lay.note, ell)
		switch {
		case k.selected:
			b.WriteString(note)
		case k.warn:
			b.WriteString(st.warn.Render(note))
		default:
			b.WriteString(st.note.Render(note))
		}
	}
	if k.selected {
		return st.selected.Render(b.String())
	}
	return b.String()
}

// headKey is everything the block above the rows is built from, so that it is
// rebuilt when one of them moves and never once per frame. The resolved mapping
// is held by a generation rather than by its own slices, which are not
// comparable.
type headKey struct {
	step    step
	width   int
	gen     int
	plan    int
	target  string
	typed   string
	at      int
	issues  int
	rows    int
	loading bool
	failed  bool
	state   jira.TaskState
	percent int
	paused  bool
}

func (m *Model) headKeyNow() headKey {
	return headKey{
		step: m.step, width: m.width, gen: m.styles.gen, plan: m.planGen,
		target: m.target, typed: m.input.Value(), at: m.input.Position(),
		issues: len(m.issues), rows: m.rowCount(),
		loading: m.loading, failed: m.failure != nil,
		state: m.state, percent: m.percent, paused: m.paused > 0,
	}
}

// headBlock is the title, the rule and whatever the step has to say above its
// rows. It is memoized whole: between two keystrokes it draws the same thing
// twice, and drawing it twice is most of what a frame costs once the rows are
// memoized.
func (m *Model) headBlock() []string {
	key := m.headKeyNow()
	if m.head != nil && key == m.headAt {
		return m.head
	}
	out := make([]string, 0, 8)
	out = append(out, m.titleLine(), m.ruleLine())
	switch m.step {
	case stepTyping:
		out = append(out, m.input.View())
	case stepType, stepStatus, stepFields:
		out = append(out, m.line(m.styles.muted, "into "+m.target+m.typeSuffix()))
	case stepConfirm:
		out = append(out, m.mappingLines()...)
	case stepRunning:
		out = append(out, m.runningLines()...)
	case stepDone:
		out = append(out, m.outcomeLines()...)
	case stepTarget:
	}
	m.head, m.headAt = out, key
	return out
}

func (m *Model) typeSuffix() string {
	if m.step == stepType {
		return ""
	}
	if name := m.targetType().Name; name != "" {
		return " as " + name
	}
	return ""
}

func (m *Model) titleLine() string {
	what := plural(len(m.issues), "issue")
	from := m.sourceProject()
	title := "Move " + what
	if from != "" {
		title += " out of " + from
	}
	return m.line(m.styles.title, title)
}

// sourceProject names where the issues are now, and says "several" rather than
// picking one when they are not all in the same place.
func (m *Model) sourceProject() string {
	key := ""
	for i := range m.issues {
		got := m.issues[i].Project.Key
		switch {
		case got == "":
			continue
		case key == "":
			key = got
		case key != got:
			return "several projects"
		}
	}
	return key
}

// ruleLine is the line under the title, with what the step is asking at its
// right end.
func (m *Model) ruleLine() string {
	label := m.stepLabel()
	dashes := max(m.width-ansi.StringWidth(label)-1, 0)
	return m.styles.rule.Render(strings.Repeat(m.deps.Theme.Glyphs.HLine, dashes)) +
		" " + m.styles.muted.Render(label)
}

func (m *Model) stepLabel() string {
	if m.loading && m.rereadable() {
		return "asking the site"
	}
	switch m.step {
	case stepTarget, stepTyping:
		return "which project"
	case stepType:
		return "which issue type"
	case stepStatus:
		return "what each status becomes"
	case stepFields:
		return "what " + m.target + " insists on"
	case stepConfirm:
		return "confirm"
	case stepRunning:
		return "moving"
	case stepDone:
		return "done"
	}
	return ""
}

// mappingLines are the whole resolved mapping, read back before anything is
// submitted: where the issues are going, as what, what each status becomes, what
// every mandatory field will be set to, and who gets an email about it.
func (m *Model) mappingLines() []string {
	t := m.deps.Theme
	out := make([]string, 0, len(m.remaps)+len(m.fields)+5)
	out = append(out, m.line(m.styles.accent, "into "+m.target+" as "+m.targetType().Name))
	for i := range m.remaps {
		row := &m.remaps[i]
		to, ok := m.landing(i)
		text := "  " + row.from.Name + " " + t.Glyphs.Arrow + " " + to.Name +
			"   " + plural(row.count, "issue")
		style := m.styles.name
		if !ok {
			text, style = "  "+row.from.Name+" has nothing to become", m.styles.danger
		}
		out = append(out, m.line(style, text))
	}
	for i := range m.fields {
		f := &m.fields[i]
		if f.retains() {
			out = append(out, m.line(m.styles.muted, "  "+f.name()+" "+t.Glyphs.Arrow+" kept from the source issue"))
			continue
		}
		out = append(out, m.line(m.styles.name, "  "+f.name()+" "+t.Glyphs.Arrow+" "+f.value().Label))
	}
	if written(m.fields) {
		for _, said := range m.indented("  ", "Every mandatory field above is being written: naming one value "+
			"on this endpoint stops the rest being kept from the source.") {
			out = append(out, m.line(m.styles.warn, said))
		}
	}
	out = append(out, m.line(m.styles.muted, "  watchers "+t.Glyphs.Arrow+" "+m.notifyWords()))
	for _, said := range m.indented("  ", "Subtasks travel with their parents and are retyped in "+m.target+
		". They count towards the "+strconv.Itoa(maxKeys)+" one move takes, so the site can still refuse this.") {
		out = append(out, m.line(m.styles.muted, said))
	}
	// Wrapped and not truncated: this is the sentence somebody has to read
	// before they agree, and a narrow window is exactly where it was being cut.
	for _, said := range m.wrapped("Once submitted the move runs on Jira whether this stays open or not. There is no undo.") {
		out = append(out, m.line(m.styles.warn, said))
	}
	return out
}

// namedFailure turns one entry of the queue's failure list into something a
// reader recognises. The body keys its failures by issue id while a fixture keys
// them by issue key, so both are looked for among the issues being moved and
// what came back is drawn only when neither matches.
func (m *Model) namedFailure(at int) string {
	if at < 0 || at >= len(m.failed) {
		return ""
	}
	got := m.failed[at]
	for i := range m.issues {
		if m.issues[i].Key == got || m.issues[i].ID == got {
			return m.issues[i].Key
		}
	}
	return got
}

func (m *Model) notifyWords() string {
	if m.notify {
		return "emailed"
	}
	return "not emailed"
}

// runningLines report progress from the number the queue gives, and from
// nothing else: a task's own message is sometimes an unresolved translation key
// and its description has reported zero issues for a run of sixty.
func (m *Model) runningLines() []string {
	out := make([]string, 0, 4)
	out = append(out, m.line(m.styles.muted, "task "+m.ref.ID))
	out = append(out, m.line(m.styles.accent, m.bar()+"  "+strconv.Itoa(m.percent)+"%  "+m.stateWords()))
	if m.paused > 0 {
		out = append(out, m.line(m.styles.warn,
			"Jira asked for a pause; asking again in "+m.paused.Round(time.Second).String()))
	}
	return out
}

func (m *Model) bar() string {
	t := m.deps.Theme
	on := min(max(m.percent, 0), 100) * barWidth / 100
	return strings.Repeat(t.Glyphs.ProgressOn, on) + strings.Repeat(t.Glyphs.ProgressNo, barWidth-on)
}

// stateWords are the queue's own state in words. CANCEL_REQUESTED is a task
// still running, so it is worded as one.
func (m *Model) stateWords() string {
	switch m.state {
	case jira.TaskEnqueued:
		return "queued"
	case jira.TaskRunning:
		return "moving them"
	case jira.TaskCancelRequested:
		return "cancelling, still running"
	case jira.TaskComplete:
		return "complete"
	case jira.TaskFailed:
		return "failed"
	case jira.TaskCancelled:
		return "cancelled"
	case jira.TaskDead:
		return "dead"
	}
	return "waiting"
}

// outcomeLines say what actually happened, including the half that did not.
func (m *Model) outcomeLines() []string {
	out := make([]string, 0, 4)
	moved := len(m.issues) - len(m.failed)
	switch {
	case m.failure != nil:
		reason, _ := jira.Reason(m.failure)
		out = append(out, m.line(m.styles.danger, "The move was submitted and this stopped being able to follow it."))
		for _, said := range m.wrapped(reason) {
			out = append(out, m.line(m.styles.muted, "  "+said))
		}
		return append(out, m.line(m.styles.muted, "task "+m.ref.ID+" is still Jira's to finish."))
	case len(m.failed) > 0:
		out = append(out, m.line(m.styles.warn, plural(moved, "issue")+" moved to "+m.target+
			", and "+plural(len(m.failed), "issue")+" did not:"))
	case m.state == jira.TaskComplete:
		out = append(out, m.line(m.styles.ok, plural(moved, "issue")+" moved to "+m.target+"."))
	default:
		out = append(out, m.line(m.styles.warn, "The move ended "+m.stateWords()+"."))
	}
	return out
}

// tailKey is everything below the rows, which is the refusal the pane keeps and
// the sentence naming what the step is for.
type tailKey struct {
	step   step
	width  int
	gen    int
	warned string
	reason string
	failed string
	rows   int
}

func (m *Model) tailKeyNow() tailKey {
	// The refusal goes below the rows wherever the pane is not empty enough to
	// have said it already: a step with rows still on it, and one that draws no
	// list at all.
	failed := ""
	if m.failure != nil && m.step != stepDone && (m.rowCount() > 0 || !m.listing()) {
		failed, _ = jira.Reason(m.failure)
	}
	return tailKey{
		step: m.step, width: m.width, gen: m.styles.gen,
		warned: m.warned, reason: m.reason, failed: failed, rows: m.rowCount(),
	}
}

func (m *Model) tailBlock() []string {
	key := m.tailKeyNow()
	if m.tail != nil && key == m.tailAt {
		return m.tail
	}
	out := make([]string, 0, 6)
	// A token that may not move issues at all says so on every step, because it
	// is the answer to every question the wizard asks and the status line that
	// said it first is gone by the next keypress.
	if m.reason != "" {
		out = append(out, "", m.line(m.styles.danger, m.reason))
	}
	if key.failed != "" {
		for _, said := range m.wrapped(key.failed) {
			out = append(out, m.line(m.styles.danger, said))
		}
		if m.rereadable() {
			out = append(out, m.line(m.styles.muted, retryHint))
		}
	}
	if m.warned != "" && m.warned != m.reason {
		for _, said := range m.wrapped(m.warned) {
			out = append(out, m.line(m.styles.warn, said))
		}
	}
	m.tail, m.tailAt = out, key
	return out
}

// --- the five kinds of empty ------------------------------------------------

// appendEmpty says which kind of empty this is. A pane that answered "searching"
// to a missing connection, a question never asked, a refusal and a real absence
// alike is a pane that looks wedged four different ways.
func (m *Model) appendEmpty(lines []string, h int) []string {
	at := len(lines)
	switch {
	case m.deps.Jira == nil:
		lines = append(lines, m.line(m.styles.muted, "  There is no Jira connection in this session."))
	case m.loading:
		lines = append(lines, m.line(m.styles.muted, "  Asking the site"+m.deps.Theme.Glyphs.Ellipsis))
	case m.failure != nil:
		lines = m.appendFailure(lines, h)
	case m.step == stepTarget && !m.looked:
		lines = append(lines, m.line(m.styles.muted, "  Nothing has been asked of Jira yet."))
	default:
		lines = append(lines, m.line(m.styles.muted, "  "+m.nothingHere()))
	}
	for len(lines)-at < h {
		lines = append(lines, "")
	}
	return lines[:at+h]
}

// listing reports whether this step draws a list at all. A field being typed
// into, a task being followed and a move that came back whole have nothing to
// list, and an empty-state sentence under them would answer a question nobody
// asked.
// rereadable reports whether the step on screen is drawn from something that can
// be asked for again, which is what decides whether naming the refresh key names
// one that works.
func (m *Model) rereadable() bool {
	switch m.step {
	case stepTarget, stepTyping, stepType, stepStatus, stepFields, stepConfirm:
		return true
	case stepRunning, stepDone:
	}
	return false
}

func (m *Model) listing() bool {
	switch m.step {
	case stepTarget, stepType, stepStatus, stepFields, stepConfirm:
		return true
	case stepDone:
		return len(m.failed) > 0
	case stepTyping, stepRunning:
	}
	return false
}

// nothingHere is the site answering with none of something, which is a different
// sentence per step because what to do about it is different every time.
func (m *Model) nothingHere() string {
	switch m.step {
	case stepTarget:
		return "No other project came back from your recent issues. i types a key."
	case stepType:
		return "This token can create no issue type in " + m.target + "."
	case stepStatus:
		return "These issues are all on one status already over there."
	case stepFields:
		return m.target + " insists on nothing this move has to answer."
	case stepConfirm:
		return "There is nothing here to move."
	case stepTyping, stepRunning, stepDone:
	}
	return ""
}

// appendFailure is the refusal in the words the site used, wrapped rather than
// cut: a transport failure names a host and a port before it says what is wrong
// with them.
func (m *Model) appendFailure(lines []string, h int) []string {
	reason, _ := jira.Reason(m.failure)
	lines = append(lines, m.line(m.styles.danger, "  The site would not say."))
	said := m.wrapped(reason)
	for _, line := range said[:min(len(said), max(min(h-2, reasonLines), 1))] {
		lines = append(lines, m.line(m.styles.muted, "  "+line))
	}
	return append(lines, m.line(m.styles.muted, "  "+retryHint))
}

// retryHint names the kernel's own refresh, which this view registers nothing
// for, spelt from the binding rather than written out.
var retryHint = kernel.DefaultGlobalKeys().Refresh.Help().Key + " tries again."

func (m *Model) wrapped(s string) []string {
	room := max(m.width-2, 8)
	return strings.Split(ansi.Wrap(s, room, ""), "\n")
}

// indented wraps a sentence that sits under a heading, keeping every line of it
// under the same margin. Without it a wrapped line starts at column zero and
// reads as a new item rather than the rest of the one above.
func (m *Model) indented(prefix, body string) []string {
	room := max(m.width-ansi.StringWidth(prefix)-1, 8)
	lines := strings.Split(ansi.Wrap(body, room, ""), "\n")
	for i := range lines {
		lines[i] = prefix + lines[i]
	}
	return lines
}

// line draws one styled line no wider than the pane.
func (m *Model) line(style lipgloss.Style, s string) string {
	return style.Render(ansi.Truncate(s, max(m.width, 1), m.deps.Theme.Glyphs.Ellipsis))
}

// rowsHeight is what is left for the rows once the block above them and the one
// below them have taken theirs.
func (m *Model) rowsHeight() int {
	if m.height <= 0 {
		return 1
	}
	return max(m.height-len(m.headBlock())-len(m.tailBlock()), 1)
}

// padTruncate makes a string exactly width columns wide, counting grapheme
// clusters rather than bytes: a project name, a status and a summary are all
// whatever somebody typed.
func padTruncate(s string, width int, ellipsis string) string {
	if width <= 0 {
		return ""
	}
	got := ansi.StringWidth(s)
	switch {
	case got == width:
		return s
	case got < width:
		return s + strings.Repeat(" ", width-got)
	}
	out := ansi.Truncate(s, width, ellipsis)
	if pad := width - ansi.StringWidth(out); pad > 0 {
		out += strings.Repeat(" ", pad)
	}
	return out
}

// View draws the block above, the window of rows, and the block below. Only the
// visible rows are built, so a move of a thousand issues costs what a move of
// twenty costs per frame.
func (m *Model) View() string {
	if m.width <= 0 || m.height <= 0 {
		return ""
	}
	head, tail := m.headBlock(), m.tailBlock()
	h := max(m.height-len(head)-len(tail), 1)
	lines := m.lines[:0]
	lines = append(lines, head...)
	n := m.rowCount()
	switch {
	case !m.listing():
		for range h {
			lines = append(lines, "")
		}
	case n == 0:
		lines = m.appendEmpty(lines, h)
	default:
		end := min(m.top+h, n)
		for i := m.top; i < end; i++ {
			lines = append(lines, m.row(i))
		}
		for i := end - m.top; i < h; i++ {
			lines = append(lines, "")
		}
	}
	lines = append(lines, tail...)
	m.lines = lines
	return strings.Join(fit(lines, m.height), "\n")
}

// fit makes the frame exactly as tall as the box, which is the invariant the
// kernel lays the chrome out against.
func fit(lines []string, height int) []string {
	for len(lines) < height {
		lines = append(lines, "")
	}
	return lines[:height]
}
