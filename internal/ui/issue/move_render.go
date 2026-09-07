package issue

import (
	"strings"

	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/varijkapil13/saral/internal/ui/kernel"
)

// editLabelWidth is the field-name column, in the transition screen and in the
// sidebar's own row editing alike. Wide enough for the longest label either
// draws, so the values line up whatever is on screen.
const editLabelWidth = 13

// editMarker is the cells in front of a row that say which one is selected. It
// is wide enough for the ASCII marker as well as the Unicode one, so a row does
// not shift sideways when it is picked.
const editMarker = 3

// editStyles is shared between the transition picker and the sidebar's row
// editing: both draw a list of labelled rows, a selected one, and a question
// waiting for y or n.
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

func pickerLine(label string, t *kernel.Theme) string {
	if t.Glyphs.Ellipsis == kernel.ASCIIGlyphs().Ellipsis {
		return "< " + label + " >"
	}
	return "‹ " + label + " ›"
}

func padTo(s string, width int) string {
	if got := ansi.StringWidth(s); got < width {
		return s + strings.Repeat(" ", width-got)
	}
	return s + " "
}

// indentWrap lays a sentence out under a list of fields, carrying the indent
// onto every line it takes.
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

// fit makes a frame exactly as tall as the box it was given: a pane that draws
// fewer lines than it was allotted leaves the previous frame's rows on screen,
// and one that draws more pushes the footer off it.
func fit(lines []string, height int) []string {
	flat := flatten(lines)
	if len(flat) > height {
		return flat[:height]
	}
	for len(flat) < height {
		flat = append(flat, "")
	}
	return flat
}

// flatten splits out every embedded newline, so that a wrapped sentence counts
// as the rows it will actually take up rather than as one.
func flatten(lines []string) []string {
	flat := make([]string, 0, len(lines)+4)
	for _, line := range lines {
		flat = append(flat, strings.Split(line, "\n")...)
	}
	return flat
}
