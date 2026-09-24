package adf

import "strings"

type escaping struct {
	lineStart bool // a hyphen, a hash or a number here would start a block
	glyph     bool // the parser only unescapes an expand or decision marker at a line's very start
	link      bool // inside a link's text, where an unpaired bracket ends the text early
}

// escapeText escapes only what the parser would read as markup. Bracket pairs
// stay bare unless they spell a link, a checkbox or an unsupported marker,
// because a status lozenge renders as "[Done]" and has to read back the same.
func escapeText(s string, e escaping, gl glyphs) string {
	lineStart, glyphStart := e.lineStart, e.glyph
	if !needsEscape(s, lineStart, glyphStart, gl) {
		return s
	}
	var brackets []bool
	if strings.ContainsAny(s, "[]") {
		brackets = bracketEscapes(s, lineStart, e.link)
	}
	var b strings.Builder
	b.Grow(len(s) + 8)
	start, glyph := lineStart, glyphStart
	for i := 0; i < len(s); i++ {
		atStart, atGlyph := start, glyph
		start, glyph = false, false
		if atStart {
			if n := glyphMarker(s[i:], gl); atGlyph && n > 0 {
				b.WriteByte('\\')
				b.WriteString(s[i : i+n])
				i += n - 1
				continue
			}
			if at := blockMarker(s[i:]); at >= 0 {
				b.WriteString(s[i : i+at])
				b.WriteByte('\\')
				b.WriteByte(s[i+at])
				i += at
				continue
			}
		}
		c := s[i]
		if c == '\n' {
			start, glyph = true, true
		}
		if escapes(s, i, brackets, atGlyph, gl) {
			b.WriteByte('\\')
		}
		b.WriteByte(c)
	}
	return b.String()
}

func needsEscape(s string, lineStart, glyphStart bool, gl glyphs) bool {
	switch {
	case strings.ContainsAny(s, "\\`*_~[]<\n"):
		return true
	case lineStart && blockMarker(s) >= 0:
		return true
	case glyphStart && glyphMarker(s, gl) > 0:
		return true
	}
	return false
}

func escapes(s string, i int, brackets []bool, glyphFollows bool, gl glyphs) bool {
	c := s[i]
	last := i+1 == len(s)
	switch c {
	case '\\':
		return last || isPunct(s[i+1]) || (glyphFollows && glyphMarker(s[i+1:], gl) > 0)
	case '`':
		return true
	case '*':
		return i == 0 || last || !spaceByte(s[i-1]) || !spaceByte(s[i+1])
	case '_':
		if i == 0 || last {
			return true
		}
		inWord := wordByte(s[i-1]) && wordByte(s[i+1])
		spaced := spaceByte(s[i-1]) && spaceByte(s[i+1])
		return !inWord && !spaced
	case '~':
		return i == 0 || last || s[i-1] == '~' || s[i+1] == '~'
	case '[', ']':
		return brackets[i]
	case '<':
		return last || letterByte(s[i+1])
	}
	return false
}

// bracketEscapes also escapes an unpaired "]", which could close a "[" left open
// by the run before.
func bracketEscapes(s string, lineStart, link bool) []bool {
	esc := make([]bool, len(s))
	var open []int
	for i := range len(s) {
		switch s[i] {
		case '[':
			open = append(open, i)
		case ']':
			if len(open) == 0 {
				esc[i] = true
				continue
			}
			o := open[len(open)-1]
			open = open[:len(open)-1]
			span := s[o : i+1]
			link := i+1 < len(s) && s[i+1] == '('
			box := lineStart && o == 0 && (span == "[ ]" || span == "[x]" || span == "[X]") &&
				(i+1 == len(s) || s[i+1] == ' ')
			if _, marker := unsupportedMarker(span); link || box || marker {
				esc[o], esc[i] = true, true
			}
		}
	}
	for _, o := range open {
		esc[o] = link
	}
	return esc
}

// blockMarker returns where a backslash stops s from starting a block, or -1.
// A number is escaped at its dot: a backslash before a digit is kept literally.
func blockMarker(s string) int {
	if s == "" {
		return -1
	}
	ends := func(at int) bool { return at == len(s) || s[at] == ' ' }
	switch c := s[0]; {
	case c == '>' || c == '|':
		return 0
	case c == '-':
		if ends(1) || s[1] == '-' {
			return 0
		}
	case c == '+':
		if ends(1) {
			return 0
		}
	case c == '#':
		if ends(runLen(s, 0, '#')) {
			return 0
		}
	case isDigit(c):
		n := 0
		for n < len(s) && n < 9 && isDigit(s[n]) {
			n++
		}
		if n < len(s) && (s[n] == '.' || s[n] == ')') && ends(n+1) {
			return n
		}
	}
	return -1
}

func glyphMarker(s string, gl glyphs) int {
	for _, g := range [...]string{gl.expand, gl.decision} {
		if strings.HasPrefix(s, g) && (len(s) == len(g) || s[len(g)] == ' ') {
			return len(g)
		}
	}
	return 0
}

func isPunct(c byte) bool {
	return (c >= '!' && c <= '/') || (c >= ':' && c <= '@') || (c >= '[' && c <= '`') || (c >= '{' && c <= '~')
}

func unescape(s string, start bool, gl glyphs) string {
	if strings.IndexByte(s, '\\') < 0 {
		return s
	}
	var b strings.Builder
	b.Grow(len(s))
	for i := 0; i < len(s); i++ {
		if s[i] == '\\' && i+1 < len(s) {
			if isPunct(s[i+1]) {
				i++
			} else if start && i == 0 && glyphMarker(s[1:], gl) > 0 {
				continue
			}
		}
		b.WriteByte(s[i])
	}
	return b.String()
}

// MentionMarkdown spells a mention the way [Markdown] renders one and the
// parser reads one back, so that an editor inserting a mention writes a real
// mention node rather than text that happens to start with "@".
//
//	@[Display Name](accountid:5b10ac8d82e05b22cc7d4ef5)
func MentionMarkdown(displayName, accountID string) string {
	name := sanitize(strings.TrimPrefix(strings.TrimSpace(displayName), "@"))
	if name == "" {
		name = accountID
	}
	name = escapeText(name, escaping{link: true}, glyphs{})
	id := sanitize(accountID)
	if strings.ContainsAny(id, " ()<>") {
		return "@" + link(name, mentionScheme+id)
	}
	return "@[" + name + "](" + mentionScheme + id + ")"
}

const mentionScheme = "accountid:"
