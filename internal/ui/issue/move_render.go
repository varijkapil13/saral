package issue

import (
	"strings"

	"github.com/charmbracelet/x/ansi"

	"github.com/varijkapil13/saral/internal/ui/kernel"
)

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
