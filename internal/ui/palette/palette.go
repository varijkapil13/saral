// Package palette is the command palette: ctrl+k, a fuzzy filter over every
// command the build registered — ranked by what this machine actually runs — and
// over every issue the cache already holds.
package palette

import (
	"slices"
	"strings"
	"time"

	"charm.land/bubbles/v2/textinput"
	tea "charm.land/bubbletea/v2"

	"github.com/varijkapil13/saral/internal/app"
	"github.com/varijkapil13/saral/internal/ui/issue"
	"github.com/varijkapil13/saral/internal/ui/kernel"
	"github.com/varijkapil13/saral/internal/ui/widget"
	"github.com/varijkapil13/saral/pkg/jira"
)

// hintAfter is how many times an action is reached from here before the status
// line names the key that reaches it directly. docs/UX.md says three.
const hintAfter = 3

// scoreTier is app.Pattern's ranking step, which it does not export: a whole
// candidate, a prefix, a word start and a match inside a word are that far
// apart.
const scoreTier = 256

// fieldPenalty is what finding a command by something other than its title
// costs, and it is calibrated rather than picked. A command ID is dotted, so
// almost any short pattern is a prefix of one, and a prefix is worth twice what
// a word start further into a title is. Nine tiers puts a title match above a
// prefix of an ID or a group, and still leaves a whole word found in an ID above
// a title the letters are only scattered through — "mine" is issues.mine.
const fieldPenalty = 9 * scoreTier

// zoneRow and zoneHit prefix the click target on a row. A command ID and an
// issue key are both strings and are kept apart.
const (
	zoneRow = "row:"
	zoneHit = "hit:"
)

var (
	_ kernel.View        = (*Model)(nil)
	_ kernel.KeyCapturer = (*Model)(nil)
)

// row is one command as the palette holds it: the key that reaches it without
// the palette, and why the site does not allow it.
type row struct {
	cmd    kernel.Command
	keys   string
	reason string
}

func (r *row) offered() bool { return r.reason == "" }

// match is the best of the three ways a command can be found, each answering
// for itself so that a title match is never beaten by a prefix of an ID nobody
// can see.
func (r *row) match(p app.Pattern) (int, bool) {
	best, ok := p.Score(r.cmd.Title)
	for _, other := range [...]string{r.cmd.Group, r.cmd.ID} {
		if score, hit := p.Score(other); hit && (!ok || score-fieldPenalty > best) {
			best, ok = score-fieldPenalty, true
		}
	}
	return best, ok
}

// ranked is one offered command's place in the order.
type ranked struct {
	at    int
	score int
	freq  float64
}

// entry is one row on offer: a command the filter left, or an issue the cache
// answered with. at indexes rows or hits accordingly.
type entry struct {
	issue bool
	at    int
}

// mark names the row the selection is on, so that a list rebuilt for a reason
// other than typing can land on it again.
type mark struct {
	issue bool
	id    string
}

// Model is the palette. It is built fresh on every ctrl+k, so everything it
// remembers between opens lives in the frecency table rather than in here.
type Model struct {
	deps kernel.Deps
	keys keyMap
	acts map[string]action

	input textinput.Model
	freq  *table
	query string

	rows  []row
	index *app.Index
	hits  []hit
	shown []entry
	ranks []ranked
	// said is the drop count already reported, so a cache holding less than it
	// looks is said once rather than on every keystroke over the same walk.
	said int
	// refused are the commands the filter matched that this site does not allow.
	// They are not offered, and their reason is what the palette says instead of
	// "nothing matches".
	refused []int

	cursor, top   int
	width, height int

	styles   *styles
	memo     *rowCache
	lay      layout
	keyWidth int

	head       string
	headAt     headKey
	lines      []string
	zonePrefix string
}

// New builds the palette over everything registered. It is the registry's
// constructor: the kernel calls it on every ctrl+k, so the commands offered and
// the session they are offered against are both as of the keypress.
func New(d kernel.Deps) kernel.View { return build(d, kernel.Commands(), sharedTable()) }

func build(d kernel.Deps, cmds []kernel.Command, freq *table) *Model {
	m := &Model{
		deps:  d,
		keys:  defaultKeys(),
		input: newInput(d.Cache != nil),
		freq:  freq,
		index: app.NewIndex(d.Cache),
		memo:  newRowCache(rowMemoLimit),
	}
	if m.deps.Theme == nil {
		m.deps.Theme = kernel.NewTheme(kernel.ThemeAuto, true, kernel.UnicodeGlyphs())
	}
	if m.deps.Now == nil {
		m.deps.Now = time.Now
	}
	if d.Zones != nil {
		m.zonePrefix = d.Zones.NewPrefix()
	}
	m.acts = m.keys.table()
	m.styles = newStyles(m.deps.Theme)
	m.rows = m.buildRows(cmds)
	m.keyWidth = widestKey(m.rows)
	m.lay = planLayout(m.width, m.keyWidth)
	_ = m.input.Focus()
	_ = m.refilter(mark{})
	return m
}

// newInput advertises the cache only when there is one. A session with nowhere
// to keep issues is normal, and offering to search them there would name
// something that cannot work.
func newInput(cached bool) textinput.Model {
	ti := widget.NewInput()
	ti.Prompt = "> "
	ti.Placeholder = "what do you want to do?"
	if cached {
		ti.Placeholder = "what do you want to do, or which issue?"
	}
	return ti
}

// buildRows prepares each command for searching, and asks the capability it
// names whether this site allows it. The registry deliberately does no
// filtering — it would be its own client — so the palette does it, in the
// probe's own words.
func (m *Model) buildRows(cmds []kernel.Command) []row {
	out := make([]row, 0, len(cmds))
	for _, cmd := range cmds {
		out = append(out, row{
			cmd:    cmd,
			keys:   strings.Join(cmd.Keys, " / "),
			reason: m.refusal(cmd),
		})
	}
	return out
}

// refusal is why a command cannot be run here, and "" when it can. A capability
// with nothing to say — a probe that has not answered yet — still cannot be run,
// and saying so beats offering it and having the kernel refuse it.
func (m *Model) refusal(cmd kernel.Command) string {
	if cmd.Requires == "" || m.deps.Caps.Allows(cmd.Requires) {
		return ""
	}
	if reason := m.deps.Caps.Capability(cmd.Requires).Reason; reason != "" {
		return reason
	}
	return cmd.Title + " is not available on this site"
}

func (m *Model) now() time.Time { return m.deps.Now() }

// WantsRawKeys is always true: the filter has the keyboard for as long as the
// palette is up. Without it the kernel matches its own bindings first and the
// letters q, r and R, the digits and esc never reach the filter.
func (m *Model) WantsRawKeys() bool { return true }

// Init has nothing to fetch. Everything the palette shows is already in the
// registry and in the frecency table.
func (m *Model) Init() tea.Cmd { return nil }

// Update handles one message.
func (m *Model) Update(msg tea.Msg) (kernel.View, tea.Cmd) {
	var cmd tea.Cmd
	switch msg := msg.(type) {
	case kernel.SizeMsg:
		m.resize(msg.Width, msg.Height)

	case kernel.FocusMsg:
		m.focus(msg.Focused)

	case kernel.ThemeMsg:
		m.deps.Theme = msg.Theme
		m.styles = newStyles(msg.Theme)
		m.memo.reset()
		m.head = ""

	case kernel.CapabilitiesMsg:
		m.deps.Caps = msg.Caps
		cmd = m.recheck()

	case kernel.CommandRanMsg:
		cmd = m.ran(msg)

	case tea.KeyPressMsg:
		cmd = m.key(msg)

	case tea.MouseClickMsg:
		cmd = m.click(msg)

	case tea.MouseWheelMsg:
		m.wheel(msg)
	}
	return m, cmd
}

func (m *Model) resize(w, h int) {
	if w == m.width && h == m.height {
		return
	}
	m.width, m.height = w, h
	m.lay = planLayout(w, m.keyWidth)
	m.input.SetWidth(max(w-inputPrompt, 8))
	m.memo.reset()
	m.head = ""
	m.clampScroll()
}

// focus keeps the filter's cursor out of a pane nobody is looking at.
func (m *Model) focus(on bool) {
	if on {
		_ = m.input.Focus()
		return
	}
	m.input.Blur()
}

// recheck answers the capability questions again after a probe, keeping the
// selection on the command it was on.
func (m *Model) recheck() tea.Cmd {
	for i := range m.rows {
		m.rows[i].reason = m.refusal(m.rows[i].cmd)
	}
	m.memo.reset()
	return m.refilter(m.selection())
}

// ran counts the command that just ran and, on the third time, notes the key
// that reaches it without the palette. The count and the ranking are one table:
// a second counter would be a second answer to the same question.
func (m *Model) ran(msg kernel.CommandRanMsg) tea.Cmd {
	count := m.freq.ran(msg.ID, m.now())
	if count != hintAfter || len(msg.Keys) == 0 {
		return nil
	}
	title, ok := m.titleOf(msg.ID)
	if !ok {
		return nil
	}
	return kernel.Status(strings.Join(msg.Keys, " / ") + " runs " + title + " without the palette")
}

func (m *Model) titleOf(id string) (string, bool) {
	for i := range m.rows {
		if m.rows[i].cmd.ID == id {
			return m.rows[i].cmd.Title, true
		}
	}
	return "", false
}

func (m *Model) key(msg tea.KeyPressMsg) tea.Cmd {
	switch m.acts[msg.String()] {
	case actUp:
		m.moveTo(m.cursor - 1)
		return nil
	case actDown:
		m.moveTo(m.cursor + 1)
		return nil
	case actPageUp:
		m.moveTo(m.cursor - m.rowsHeight())
		return nil
	case actPageDown:
		m.moveTo(m.cursor + m.rowsHeight())
		return nil
	case actRun:
		return m.activate()
	case actClose:
		return kernel.Pop()
	case actNone:
	}
	// The input's own command is a cursor blink, which is a timer this view
	// would then own for as long as it is up. Dropping it costs a blinking block
	// and keeps every frame reproducible.
	m.input, _ = m.input.Update(msg)
	if q := m.input.Value(); q != m.query {
		m.query = q
		return m.refilter(mark{})
	}
	return nil
}

// activate does what the row under the cursor is for: a command is named to the
// kernel, and an issue is opened.
//
// Nothing calls a command's Run here: Deps is a value copied when the palette
// was built, and a search that narrows its JQL with Deps.Project would run
// against whichever project the session was on then, which is a valid query
// over the wrong project.
func (m *Model) activate() tea.Cmd {
	if len(m.shown) == 0 {
		return nil
	}
	at := m.shown[m.cursor]
	if at.issue {
		return m.open(&m.hits[at.at])
	}
	return kernel.RunCommand(m.rows[at.at].cmd.ID)
}

// open puts the detail pane over what the palette was opened from rather than
// over the palette, so that esc from the issue goes back to the view the user
// was in and not to a filter they have finished with.
func (m *Model) open(h *hit) tea.Cmd {
	seed := jira.Issue{Key: h.key, Summary: h.summary}
	return tea.Sequence(kernel.Pop(), kernel.Push(issue.ViewID, h.key, issue.New(m.deps, seed)))
}

func (m *Model) click(msg tea.MouseClickMsg) tea.Cmd {
	if msg.Button != tea.MouseLeft || m.deps.Zones == nil {
		return nil
	}
	for i := m.top; i < min(m.top+m.rowsHeight(), len(m.shown)); i++ {
		if !m.deps.Zones.Get(m.zone(m.shown[i])).InBounds(msg) {
			continue
		}
		if i == m.cursor {
			return m.activate()
		}
		m.moveTo(i)
		return nil
	}
	return nil
}

// zone is the click target a row is marked with.
func (m *Model) zone(at entry) string {
	if at.issue {
		return m.zonePrefix + zoneHit + m.hits[at.at].key
	}
	return m.zonePrefix + zoneRow + m.rows[at.at].cmd.ID
}

// wheel scrolls the rows without moving the selection, which is what a wheel
// does everywhere else.
func (m *Model) wheel(msg tea.MouseWheelMsg) {
	switch msg.Button {
	case tea.MouseWheelUp:
		m.top -= 3
	case tea.MouseWheelDown:
		m.top += 3
	default:
		return
	}
	m.clampScroll()
}

// refilter recomputes what the filter leaves and in what order: the commands
// this build registered, then the issues the cache already holds. keep names the
// row to stay on when the list is rebuilt for a reason other than typing; typing
// lands on the best match, which is the point of typing.
func (m *Model) refilter(keep mark) tea.Cmd {
	m.shown, m.refused, m.ranks = m.shown[:0], m.refused[:0], m.ranks[:0]
	text := strings.TrimSpace(m.query)
	pattern := app.NewPattern(text)
	now := m.now()
	for i := range m.rows {
		score, ok := m.rows[i].match(pattern)
		if !ok {
			continue
		}
		if !m.rows[i].offered() {
			m.refused = append(m.refused, i)
			continue
		}
		m.ranks = append(m.ranks, ranked{at: i, score: score, freq: m.freq.score(m.rows[i].cmd.ID, now)})
	}
	// The filter decides which commands and frecency orders the equals, so a
	// habit never demotes a better match: the query is the later intent.
	slices.SortFunc(m.ranks, func(a, b ranked) int {
		switch {
		case a.score != b.score:
			return b.score - a.score
		case a.freq > b.freq:
			return -1
		case a.freq < b.freq:
			return 1
		default:
			return a.at - b.at
		}
	})
	for _, rk := range m.ranks {
		m.shown = append(m.shown, entry{at: rk.at})
	}
	cmd := m.search(text, now)
	m.cursor = 0
	if keep.id != "" {
		if at := slices.IndexFunc(m.shown, func(e entry) bool { return m.markOf(e) == keep }); at >= 0 {
			m.cursor = at
		}
	}
	m.scrollToCursor()
	return cmd
}

// markOf names one row, whichever half of the list it came from.
func (m *Model) markOf(at entry) mark {
	if at.issue {
		return mark{issue: true, id: m.hits[at.at].key}
	}
	return mark{id: m.rows[at.at].cmd.ID}
}

func (m *Model) selection() mark {
	if len(m.shown) == 0 || m.cursor >= len(m.shown) {
		return mark{}
	}
	return m.markOf(m.shown[m.cursor])
}

// selectedID is the command under the cursor, and "" when the cursor is on an
// issue or on nothing.
func (m *Model) selectedID() string {
	if at := m.selection(); !at.issue {
		return at.id
	}
	return ""
}

// onIssue reports whether the row under the cursor is a cached issue, which is
// what enter does something different with.
func (m *Model) onIssue() bool {
	return m.cursor < len(m.shown) && m.shown[m.cursor].issue
}

func (m *Model) moveTo(at int) {
	if len(m.shown) == 0 {
		m.cursor, m.top = 0, 0
		return
	}
	m.cursor = min(max(at, 0), len(m.shown)-1)
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
	h := m.rowsHeight()
	m.top = min(m.top, max(len(m.shown)-h, 0))
	m.top = max(m.top, 0)
}

// rowsHeight is how many commands fit under the filter line and its rule.
func (m *Model) rowsHeight() int { return max(m.height-headHeight, 1) }
