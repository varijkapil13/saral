package onboarding

import (
	"strconv"
	"strings"

	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/varijkapil13/saral/pkg/jira"
)

const (
	inputIndent = 4
	labelWidth  = 10
	// railMinHeight is the shortest box the list of steps is drawn in. Below it
	// the step being worked on gets the rows instead.
	railMinHeight = 24
)

// styles are built once per theme generation. Constructing a lipgloss.Style is
// the expensive half of rendering, and none of these may be built in a loop.
type styles struct {
	title    lipgloss.Style
	subtitle lipgloss.Style
	label    lipgloss.Style
	value    lipgloss.Style
	muted    lipgloss.Style
	prompt   lipgloss.Style
	problem  lipgloss.Style
	yes      lipgloss.Style
	no       lipgloss.Style
	chosen   lipgloss.Style
	done     lipgloss.Style
	here     lipgloss.Style
	pending  lipgloss.Style
}

func (m *Model) restyle() {
	t := m.deps.Theme
	m.styles = styles{
		title:    t.Title,
		subtitle: t.Muted,
		label:    t.Muted,
		value:    t.Base,
		muted:    t.Muted,
		prompt:   t.Accent,
		problem:  t.Danger,
		yes:      t.Success,
		no:       t.Warning,
		chosen:   t.Selected,
		done:     t.Success,
		here:     t.Accent,
		pending:  t.Muted,
	}
}

// renderCache holds the parts of the frame that do not change per keystroke:
// the heading and the block of prose under the field. They are rebuilt when the
// step, the width or the theme changes and not otherwise.
type renderCache struct {
	key     cacheKey
	heading []string
	explain []string
	pane    []string
	valid   bool
}

type cacheKey struct {
	step   step
	store  storeKind
	width  int
	height int
	theme  int
	offset int
}

func (c *renderCache) reset() {
	if c != nil {
		c.valid = false
	}
}

// View draws the whole body the kernel gave this view, in exactly the number of
// rows it was given.
func (m Model) View() string {
	if m.width <= 0 || m.height <= 0 {
		return ""
	}
	heading, explain, pane := m.chrome()
	body := m.section(pane)
	rail := []string(nil)
	if m.showRail() {
		rail = m.rail()
	}

	out := make([]string, 0, m.height)
	out = append(out, heading...)
	if m.showRail() {
		out = append(out, rail...)
		out = append(out, "")
	}
	out = append(out, body...)
	if len(explain) > 0 && len(out)+1+len(explain) <= m.height {
		out = append(out, "")
		out = append(out, explain...)
	}
	return m.frame(out)
}

// showRail decides whether there is room for the list of steps beside the one
// being worked on. It answers from the height alone rather than from whether
// the rest happens to fit, so that the layout does not move as a step grows.
func (m Model) showRail() bool { return m.height >= railMinHeight }

// paneHeight is what is left for the scrolling summary once the heading, the
// rail, the prompt and two rows for messages are taken out.
func (m Model) paneHeight() int {
	used := len(m.heading()) + 2 + 2
	if m.showRail() {
		used += steps + 1
	}
	return max(m.height-used, 3)
}

// chrome returns the heading and the prose, rebuilt only when the step, the
// width or the theme has changed. Everything else on the screen answers to a
// keystroke and is built fresh.
func (m Model) chrome() (heading, explain, pane []string) {
	if m.cache == nil {
		return m.heading(), m.explain(), m.paneLines()
	}
	key := cacheKey{
		step: m.step, store: m.store, theme: m.deps.Theme.Gen,
		width: m.width, height: m.height, offset: m.pane.YOffset(),
	}
	if !m.cache.valid || m.cache.key != key {
		*m.cache = renderCache{
			key: key, valid: true,
			heading: m.heading(), explain: m.explain(), pane: m.paneLines(),
		}
	}
	return m.cache.heading, m.cache.explain, m.cache.pane
}

// paneLines renders the scrolling summary. It is in the cache because the
// widget re-wraps and re-pads its whole content on every call, and the screen
// it draws answers to nothing but a scroll.
func (m Model) paneLines() []string {
	switch m.step {
	case stepReview, stepDone:
		return strings.Split(m.pane.View(), "\n")
	default:
		return nil
	}
}

// frame clamps every row to the width and the whole thing to the height. A row
// that wraps costs the footer its line, and the long rows here are the ones
// carrying an error message.
func (m Model) frame(rows []string) string {
	ellipsis := m.deps.Theme.Glyphs.Ellipsis
	var b strings.Builder
	b.Grow(m.width * min(len(rows), m.height))
	for i, row := range rows {
		if i >= m.height {
			break
		}
		if i > 0 {
			b.WriteByte('\n')
		}
		if lipgloss.Width(row) > m.width {
			row = ansi.Truncate(row, m.width, ellipsis)
		}
		b.WriteString(strings.TrimRight(row, " "))
	}
	return b.String()
}

func (m Model) heading() []string {
	title := "Set up Saral"
	if m.step == stepDone {
		title = "Saral is set up"
	}
	if m.step >= stepDone {
		return []string{m.styles.title.Render(title), ""}
	}
	counter := "step " + strconv.Itoa(int(m.step)+1) + " of " + strconv.Itoa(steps)
	return []string{m.spread(m.styles.title.Render(title), m.styles.muted.Render(counter)), ""}
}

// spread puts left at the margin and right at the far edge, dropping the right
// half rather than letting the row wrap.
func (m Model) spread(left, right string) string {
	gap := m.width - lipgloss.Width(left) - lipgloss.Width(right)
	if gap < 1 {
		return left
	}
	return left + strings.Repeat(" ", gap) + right
}

// rail is the list of steps down the side, with what has been entered beside
// the ones that are behind. Each one behind is clickable.
func (m Model) rail() []string {
	out := make([]string, 0, steps)
	for s := stepSite; s < stepDone; s++ {
		var mark, label string
		switch {
		case s < m.step:
			mark, label = m.deps.Theme.Glyphs.Check, m.styles.done.Render(s.title())
		case s == m.step:
			mark, label = m.deps.Theme.Glyphs.Arrow, m.styles.here.Render(s.title())
		default:
			mark, label = " ", m.styles.pending.Render(s.title())
		}
		row := "  " + cell(mark, 3) + label
		if entered := m.entered(s); entered != "" {
			row = "  " + cell(mark, 3) + pad(label, labelWidth+5) + m.styles.muted.Render(entered)
		}
		if s < m.step {
			row = m.mark("step:"+s.String(), row)
		}
		out = append(out, row)
	}
	return out
}

// entered is what the user put into a step, echoed beside it. The token is the
// one that never is.
func (m Model) entered(s step) string {
	if s >= m.step {
		return ""
	}
	switch s {
	case stepSite:
		return m.value(fieldSite)
	case stepEmail:
		return m.value(fieldEmail)
	case stepToken:
		if m.account.DisplayName != "" {
			return "verified as " + m.account.DisplayName
		}
		return "verified"
	case stepStorage:
		return m.store.title()
	case stepProject:
		if key := m.value(fieldProject); key != "" {
			return key
		}
		return "none"
	case stepReview, stepDone:
	}
	return ""
}

// section is the step's own content: the prompt, whatever it is asking for, and
// the line that says what went wrong.
func (m Model) section(pane []string) []string {
	out := make([]string, 0, 8)
	out = append(out, indent(m.styles.subtitle.Render(m.step.prompt())))

	switch m.step {
	case stepStorage:
		out = append(out, "")
		out = append(out, m.choices()...)
		out = append(out, "", m.fieldRow(m.store.label()))
	case stepProject:
		out = append(out, "", m.fieldRow("Project key"))
		out = append(out, m.suggestions()...)
	case stepReview, stepDone:
		out = append(out, "")
		out = append(out, pane...)
	case stepSite, stepEmail, stepToken:
		out = append(out, "", m.fieldRow(""))
	}
	return append(out, m.messages()...)
}

func (m Model) fieldRow(label string) string {
	f := m.step.field()
	if f == fieldNone {
		return ""
	}
	column := ""
	if label != "" {
		column = m.styles.label.Render(pad(label, labelWidth+4))
	}
	return indent(column + m.styles.prompt.Render(m.deps.Theme.Glyphs.Arrow+" ") + m.input[f].View())
}

// messages is what sits under the field: what went wrong, what is in flight, or
// what is worth knowing. There is always a row for it, so that the prose below
// does not move as messages come and go.
func (m Model) messages() []string {
	rows := make([]string, 0, 2)
	switch {
	case m.problem != "":
		rows = append(rows, indent(m.styles.problem.Render(m.deps.Theme.Glyphs.Cross+" "+m.problem)))
	case m.busy != busyNone:
		rows = append(rows, indent(m.spin.View()+" "+m.styles.muted.Render(m.busy.String()+m.deps.Theme.Glyphs.Ellipsis)))
	case m.step != stepProject:
	case m.looking:
		rows = append(rows, indent(m.styles.muted.Render("Looking for the projects you have been working in"+m.deps.Theme.Glyphs.Ellipsis)))
	case m.lookup != "":
		rows = append(rows, indent(m.styles.muted.Render(m.lookup)))
	}
	if m.note != "" {
		rows = append(rows, indent(m.styles.muted.Render(m.note)))
	}
	if len(rows) == 0 {
		rows = append(rows, "")
	}
	return rows
}

func (m Model) choices() []string {
	out := make([]string, 0, storeCount)
	for kind := storeKind(0); kind < storeCount; kind++ {
		mark, style := " ", m.styles.value
		if kind == m.store {
			mark, style = m.deps.Theme.Glyphs.Diamond, m.styles.chosen
		}
		row := indent(style.Render(cell(mark, 2) + " " + kind.title()))
		out = append(out, m.mark("store:"+kind.String(), row))
	}
	return out
}

func (m Model) suggestions() []string {
	if len(m.suggested) == 0 {
		return nil
	}
	rows := make([]string, 0, len(m.suggested)+1)
	rows = append(rows, "", indent(m.styles.muted.Render("Recently worked in")))
	picked := m.value(fieldProject)
	for _, key := range m.suggested {
		mark, style := " ", m.styles.value
		if key == picked {
			mark, style = m.deps.Theme.Glyphs.Diamond, m.styles.chosen
		}
		rows = append(rows, m.mark("project:"+key, indent(style.Render(cell(mark, 2)+" "+key))))
	}
	return rows
}

// summary is the review screen: everything that will be written, then what the
// probe found, in the probe's own words.
func (m Model) summary() []string {
	rows := make([]string, 0, 16)
	rows = append(rows,
		m.pair("Site", m.value(fieldSite)),
		m.pair("Account", m.accountLine()),
		m.pair("Profile", m.profileName()+m.styles.muted.Render(" in "+m.cfgPath)),
		m.pair("Token", m.tokenLine()),
		m.pair("Project", m.projectLine()),
	)
	if !m.probed {
		return rows
	}
	rows = append(rows, "", indent(m.styles.subtitle.Render(m.capsHeading())))
	for _, c := range capabilityList {
		rows = append(rows, m.capabilityRow(c.key, c.label))
	}
	return append(rows, "",
		m.pair("Dates in", m.caps.Location().String()),
		m.pair("Images", m.caps.Graphics.String()))
}

func (m Model) capsHeading() string {
	if m.project == "" {
		return "What this token can do on this site"
	}
	return "What this token can do in " + m.project
}

var capabilityList = []struct {
	key   jira.CapabilityKey
	label string
}{
	{jira.CapBoards, "Boards"},
	{jira.CapBulkMove, "Move between projects"},
	{jira.CapDeleteIssues, "Delete issues"},
	{jira.CapAttachments, "Attachments"},
	{jira.CapPlans, "Plans"},
}

func (m Model) capabilityRow(key jira.CapabilityKey, label string) string {
	got := m.caps.Capability(key)
	if got.OK {
		return indent(m.styles.yes.Render(cell(m.deps.Theme.Glyphs.Check, 2)) + m.styles.value.Render(label))
	}
	return indent(m.styles.no.Render(cell(m.deps.Theme.Glyphs.Cross, 2)) +
		m.styles.value.Render(pad(label, labelWidth+12)) + m.styles.muted.Render(got.Reason))
}

func (m Model) accountLine() string {
	if m.account.DisplayName == "" {
		return m.value(fieldEmail)
	}
	return m.account.DisplayName + m.styles.muted.Render(" · "+m.value(fieldEmail))
}

func (m Model) tokenLine() string {
	source, err := m.tokenSource()
	if err != nil {
		return m.styles.problem.Render(err.Error())
	}
	return source.String()
}

func (m Model) projectLine() string {
	if key := m.value(fieldProject); key != "" {
		return key
	}
	return m.styles.muted.Render("none, so Jira's per-project answers stay unknown")
}

func (m Model) finished() []string {
	rows := []string{
		m.pair("Profile", m.name),
		m.pair("Written to", m.savedTo),
		m.pair("Token", m.stored),
	}
	if m.deps.Jira == nil {
		return append(rows, "", indent(m.styles.value.Render("Press enter to leave, then run saral again to open "+m.value(fieldSite)+".")))
	}
	return append(rows, "", indent(m.styles.value.Render("Press enter to carry on.")))
}

func (m Model) pair(label, value string) string {
	return indent(m.styles.label.Render(pad(label, labelWidth+4)) + value)
}

// explain is the prose under the field. It is cached because wrapping it is the
// most expensive thing on this screen and it changes only with the step.
func (m Model) explain() []string {
	text := ""
	switch m.step {
	case stepSite:
		text = "The host you open in a browser, with or without the https:// in front. " +
			"Saral talks to it over https and nothing else."
	case stepEmail:
		text = "Jira Cloud sends the email and the API token together as basic auth, so a token " +
			"paired with the wrong address is refused exactly the way a wrong token is."
	case stepToken:
		text = "Create one at id.atlassian.com/manage-profile/security/api-tokens. It is checked " +
			"against the site before anything at all is written, and it never goes into the config file."
	case stepStorage:
		text = m.store.explain() + " The config file only ever names the source, so it stays safe to read, " +
			"copy between machines and keep in a dotfiles repository."
	case stepProject:
		text = "Jira grants Move, Create and Delete per project, and a board belongs to one, so the " +
			"same token answers differently in two projects on one site. Saral remembers the one you " +
			"pick, and naming a project on the command line overrides it for that run."
	case stepReview, stepDone:
		return nil
	}
	if text == "" {
		return nil
	}
	width := m.width - 2*inputIndent
	if width < 20 {
		return nil
	}
	wrapped := strings.Split(ansi.Wordwrap(text, width, ""), "\n")
	out := make([]string, 0, len(wrapped))
	for _, line := range wrapped {
		out = append(out, indent(m.styles.muted.Render(line)))
	}
	return out
}

func (m Model) mark(id, row string) string {
	if m.zone == "" || m.deps.Zones == nil {
		return row
	}
	return m.deps.Zones.Mark(m.zone+id, row)
}

func indent(s string) string { return strings.Repeat(" ", inputIndent) + s }

// pad widens a label to a column, always leaving at least one space after it.
func pad(s string, width int) string { return cell(s, width) + " " }

// cell widens a string to exactly width columns, measuring what will be on the
// screen rather than the styling around it.
func cell(s string, width int) string {
	if gap := width - lipgloss.Width(s); gap > 0 {
		return s + strings.Repeat(" ", gap)
	}
	return s
}
