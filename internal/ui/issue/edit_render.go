package issue

import (
	"strings"

	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/varijkapil13/saral/internal/ui/kernel"
)

// editLabelWidth is the field-name column. Wide enough for the longest label
// this pane draws, so the values line up whatever is on screen.
const editLabelWidth = 13

// editMarker is the cells in front of a row that say which one is selected. It
// is wide enough for the ASCII marker as well as the Unicode one, so a row does
// not shift sideways when it is picked.
const editMarker = 3

type editStyles struct {
	title    lipgloss.Style
	label    lipgloss.Style
	value    lipgloss.Style
	muted    lipgloss.Style
	warn     lipgloss.Style
	fail     lipgloss.Style
	selected lipgloss.Style
}

func newEditStyles(t *kernel.Theme) *editStyles {
	return &editStyles{
		title:    t.Title,
		label:    t.Muted,
		value:    t.Base,
		muted:    t.Muted,
		warn:     t.Warning,
		fail:     t.Danger,
		selected: t.Accent,
	}
}

// View draws the fields, then whatever the pane is waiting for an answer about.
func (m *editModel) View() string {
	if m.width <= 0 || m.height <= 0 {
		return ""
	}
	lines := make([]string, 0, m.height)
	lines = append(lines, m.editTitle())
	for i := range m.rows {
		lines = append(lines, m.rowLines(i)...)
	}
	lines = append(lines, "")
	lines = append(lines, m.footerLines()...)
	return strings.Join(fit(lines, m.height), "\n")
}

func (m *editModel) editTitle() string {
	ell := m.deps.Theme.Glyphs.Ellipsis
	title := "Edit " + m.issue.Key
	if m.dirty() {
		title += "  " + m.deps.Theme.Glyphs.Bullet + " unsaved"
	}
	return m.styles.title.Render(ansi.Truncate(title, max(m.width, 1), ell))
}

// rowLines draws one field: the row itself, and the sentences underneath it
// that say why it cannot be changed or why Jira refused it.
func (m *editModel) rowLines(at int) []string {
	row := &m.rows[at]
	t := m.deps.Theme
	selected := at == m.cursor && m.stage != stageConfirm && m.stage != stageConflict

	prefix := strings.Repeat(" ", editMarker)
	if selected {
		prefix = m.styles.selected.Render(t.Glyphs.Arrow) + strings.Repeat(" ", editMarker-ansi.StringWidth(t.Glyphs.Arrow))
	}
	label := m.styles.label.Render(padTo(row.label, editLabelWidth))

	room := max(m.width-editMarker-editLabelWidth, 8)
	var value string
	switch {
	case selected && m.stage == stageTyping && row.kind != editPick && row.kind != editDoc:
		value = m.input.View()
	case row.kind == editPick && len(row.options) > 0:
		value = m.styles.value.Render(ansi.Truncate(pickerLine(row.display(), t), room, t.Glyphs.Ellipsis))
	default:
		value = m.styles.value.Render(ansi.Truncate(shownValue(row, t), room, t.Glyphs.Ellipsis))
	}

	line := prefix + label + value
	if m.deps.Zones != nil && m.zonePrefix != "" {
		line = m.deps.Zones.Mark(m.zonePrefix+"row:"+row.id, line)
	}
	out := []string{line}
	if reason, blocked := row.blocked(); blocked {
		out = append(out, m.styles.warn.Render(indentWrap(reason, m.width)))
	}
	if row.problem != "" {
		out = append(out, m.styles.fail.Render(indentWrap(row.problem, m.width)))
	}
	return out
}

// shownValue is what a row reads as, with a word standing in for a field that
// holds nothing so that a column of blanks does not read as data. A field the
// issue was never read with says so instead: this client having nothing and
// Jira having nothing are different answers.
func shownValue(row *editRow, t *kernel.Theme) string {
	if !row.fetched {
		return "not read"
	}
	got := row.display()
	if strings.TrimSpace(got) == "" {
		got = "not set"
	}
	if row.dirty() {
		got += "  " + t.Glyphs.Bullet + " changed"
	}
	return got
}

func pickerLine(label string, t *kernel.Theme) string {
	if t.Glyphs.Ellipsis == kernel.ASCIIGlyphs().Ellipsis {
		return "< " + label + " >"
	}
	return "‹ " + label + " ›"
}

// footerLines are the note, the failure and whichever question the pane is
// waiting on. Nothing destructive happens without one of them on screen.
func (m *editModel) footerLines() []string {
	switch m.stage {
	case stageConfirm:
		return m.confirmLines()
	case stageConflict:
		return m.conflictLines()
	case stageSaving:
		return []string{m.styles.muted.Render("saving " + m.issue.Key + m.deps.Theme.Glyphs.Ellipsis)}
	case stageTyping:
		return m.messageLines(m.styles.muted.Render("enter keeps this value, esc leaves it alone"))
	case stageBrowse:
	}
	return m.messageLines(m.styles.muted.Render(m.browseHint()))
}

func (m *editModel) browseHint() string {
	if m.dirty() {
		return "ctrl+s saves, X throws the changes away"
	}
	return "enter changes the field under the cursor"
}

func (m *editModel) messageLines(hint string) []string {
	out := make([]string, 0, 3)
	if m.fail != "" {
		out = append(out, m.styles.fail.Render(wrapped(m.fail, m.width)))
	}
	if m.note != "" {
		out = append(out, m.styles.muted.Render(wrapped(m.note, m.width)))
	}
	return append(out, hint)
}

// confirmLines say what is about to change, in words. docs/UX.md principle 4:
// nothing destructive happens without a named confirmation, and somebody else's
// ticket is somebody else's ticket.
func (m *editModel) confirmLines() []string {
	arrow := m.deps.Theme.Glyphs.Arrow
	out := []string{m.styles.title.Render("Save these changes to " + m.issue.Key + "?")}
	for i := range m.rows {
		row := &m.rows[i]
		if !row.dirty() {
			continue
		}
		change := padTo(row.label, editLabelWidth) + quoteValue(row.before()) + " " + arrow + " " + quoteValue(row.display())
		out = append(out, m.styles.value.Render(indentLine(change, m.width, m.deps.Theme.Glyphs.Ellipsis)))
	}
	if costs := m.editCosts(); len(costs) > 0 {
		out = append(out, m.styles.warn.Render(indentWrap("the parts of the description you changed lose:", m.width)))
		for _, cost := range costs {
			out = append(out, m.styles.warn.Render(indentWrap(m.deps.Theme.Glyphs.Bullet+" "+cost, m.width)))
		}
	}
	return append(out, m.styles.muted.Render("y saves, any other key goes back"))
}

// editCosts names what an edited description cannot carry back, and only when
// the description is one of the things being written.
func (m *editModel) editCosts() []string {
	row := m.rowByID("description")
	if row == nil || !row.dirty() || row.edited == nil {
		return nil
	}
	return riskyEdits(row.doc)
}

func (m *editModel) conflictLines() []string {
	return []string{
		m.styles.fail.Render(wrapped(m.fail, m.width)),
		m.styles.muted.Render(wrapped(
			"Your changes are still here and still on disk. y re-reads "+m.issue.Key+
				" and puts them back on top of it; any other key leaves this alone.", m.width)),
	}
}

func quoteValue(s string) string {
	if strings.TrimSpace(s) == "" {
		return "nothing"
	}
	return `"` + s + `"`
}

func padTo(s string, width int) string {
	if got := ansi.StringWidth(s); got < width {
		return s + strings.Repeat(" ", width-got)
	}
	return s + " "
}

// indentLine puts a structured row under the fields. It is truncated rather
// than wrapped: its columns are what make it readable and a wrap folds them
// into prose.
func indentLine(s string, width int, ell string) string {
	pad := strings.Repeat(" ", editMarker)
	return pad + ansi.Truncate(s, max(width-editMarker, 8), ell)
}

// indentWrap lays a sentence out under the fields, carrying the indent onto
// every line it takes.
func indentWrap(s string, width int) string {
	pad := strings.Repeat(" ", editMarker)
	lines := wrapTo(s, max(width-editMarker, 12))
	for i := range lines {
		lines[i] = pad + lines[i]
	}
	return strings.Join(lines, "\n")
}

// wrapped is wrapTo as one string, which is the form a lipgloss style renders:
// Render joins its arguments with a space rather than a newline.
func wrapped(s string, width int) string { return strings.Join(wrapTo(s, width), "\n") }

// wrapTo breaks a sentence at the terminal's width, so that a long reason from
// Jira is readable rather than a line that runs off the side.
func wrapTo(s string, width int) []string {
	limit := max(width, 20)
	words := strings.Fields(s)
	if len(words) == 0 {
		return []string{""}
	}
	out := make([]string, 0, 2)
	line := words[0]
	for _, word := range words[1:] {
		if ansi.StringWidth(line)+1+ansi.StringWidth(word) > limit {
			out = append(out, line)
			line = word
			continue
		}
		line += " " + word
	}
	return append(out, line)
}

// fit makes the frame exactly as tall as the box it was given: a pane that
// draws fewer lines than it was allotted leaves the previous frame's rows on
// screen, and one that draws more pushes the footer off it.
func fit(lines []string, height int) []string {
	flat := make([]string, 0, height)
	for _, line := range lines {
		flat = append(flat, strings.Split(line, "\n")...)
	}
	if len(flat) > height {
		return flat[:height]
	}
	for len(flat) < height {
		flat = append(flat, "")
	}
	return flat
}
