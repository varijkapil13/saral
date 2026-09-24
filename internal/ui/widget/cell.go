package widget

import (
	"strings"

	"github.com/charmbracelet/x/ansi"
)

// PadTruncate makes s exactly width columns wide, counting grapheme clusters
// rather than bytes so an emoji or a CJK summary does not shift every column
// to its right. It does not sanitize: a cell built from Jira text calls
// Sanitize on the raw field before styling it, since a card colours its key
// before truncating it and stripping escapes here would erase that colour
// along with whatever it was guarding against.
func PadTruncate(s string, width int, ellipsis string) string {
	if width <= 0 {
		return ""
	}
	return padTruncate(s, width, ellipsis)
}

// PadLeft right-aligns s within width, falling back to PadTruncate when s
// does not fit.
func PadLeft(s string, width int, ellipsis string) string {
	if width <= 0 {
		return ""
	}
	if got := ansi.StringWidth(s); got < width {
		return strings.Repeat(" ", width-got) + s
	}
	return padTruncate(s, width, ellipsis)
}

func padTruncate(s string, width int, ellipsis string) string {
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

// Sanitize drops what a terminal would act on rather than display: a tab
// (which would misalign a padded column) becomes a single space, ANSI escape
// sequences — CSI, OSC and the rest, including one an unterminated C1
// introducer swallows to the end of the string — are stripped along with
// whatever byte introduced them, and the bidi override/isolate characters
// (U+202A-202E, U+2066-2069) that could reorder a cell are dropped outright.
// Jira text is written by anyone with a login, and a column built from it
// must not be able to repaint or reshape the row it sits in.
func Sanitize(s string) string {
	if !needsSanitize(s) {
		return s
	}
	s = ansi.Strip(s)
	var b strings.Builder
	b.Grow(len(s))
	for _, r := range s {
		switch {
		case r == '\t':
			b.WriteByte(' ')
		case isUnsafe(r):
			continue
		default:
			b.WriteRune(r)
		}
	}
	return b.String()
}

func needsSanitize(s string) bool {
	for _, r := range s {
		if isUnsafe(r) {
			return true
		}
	}
	return false
}

func isUnsafe(r rune) bool {
	switch {
	case r < 0x20 || r == 0x7f:
		return true
	case r >= 0x80 && r <= 0x9f:
		return true
	case r >= 0x202a && r <= 0x202e:
		return true
	case r >= 0x2066 && r <= 0x2069:
		return true
	default:
		return false
	}
}
