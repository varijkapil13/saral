package adf

import (
	"bytes"
	"fmt"
	"strings"
)

// markSet is the emphasis in force at a point in the scan. The renderer wraps
// text in a fixed order — link outermost, then strong, em, strike, underline,
// and code innermost — so reading that order back is a matter of recursing
// once per delimiter rather than of pairing delimiters after the fact.
type markSet struct {
	strong, em, strike, underline, code bool
	linked                              bool
	plain                               bool // emphasis markers are characters, not markers
	href                                string
}

// marks builds the mark list in ProseMirror rank order, which is the order the
// Jira editor writes and therefore the order a document that has been opened in
// a browser comes back in.
func (m markSet) marks() []Mark {
	var out []Mark
	if m.linked {
		out = append(out, NewMark("link", Attrs{"href": m.href}))
	}
	if m.em {
		out = append(out, NewMark("em", nil))
	}
	if m.strong {
		out = append(out, NewMark("strong", nil))
	}
	if m.strike {
		out = append(out, NewMark("strike", nil))
	}
	if m.underline {
		out = append(out, NewMark("underline", nil))
	}
	if m.code {
		out = append(out, NewMark("code", nil))
	}
	return out
}

// inline turns the lines of one paragraph into inline nodes. A paragraph only
// runs to a second line because the author put a hard break there: the renderer
// never wraps prose, so that a viewport can reflow it.
//
// What it reads is checked against what it would render, and a reading that
// does not reproduce the line it came from is thrown away for one that treats
// the markers as the characters they are. Star soup — adjacent runs of emphasis
// whose marks overlap — has no single reading, and picking one anyway is how a
// paragraph grows a marker every time somebody saves it. The last reading is
// the line itself as prose, which always renders back to what it was, so this
// never answers something that would come out different next time.
func (p *parser) inline(ls []line) ([]Node, error) { return p.inlineIn(ls, false) }

// inlineIn with noBlocks reads a heading or a cell, where the renderer escapes
// nothing at a line's start and so nothing there is unescaped.
func (p *parser) inlineIn(ls []line, noBlocks bool) ([]Node, error) {
	out, err := p.inlineWith(ls, markSet{}, !noBlocks)
	if err != nil {
		return nil, err
	}
	if !ambiguous(ls) || p.rendersBack(out, ls, noBlocks) {
		return out, nil
	}
	if plain, err := p.inlineWith(ls, markSet{plain: true}, !noBlocks); err == nil && p.rendersBack(plain, ls, noBlocks) {
		return plain, nil
	}
	return p.prose(ls, !noBlocks), nil
}

// prose is the reading of last resort: every line as the characters it holds.
func (p *parser) prose(ls []line, start bool) []Node {
	var out []Node
	for i := range ls {
		if i > 0 {
			out = append(out, NewNode("hardBreak"))
		}
		if text := unescape(sanitize(strings.TrimRight(ls[i].text, " \t")), start, p.gl); text != "" {
			out = append(out, NewText(text))
		}
	}
	return out
}

func (p *parser) inlineWith(ls []line, ms markSet, start bool) ([]Node, error) {
	var out []Node
	for i := range ls {
		if i > 0 {
			out = append(out, NewNode("hardBreak"))
		}
		nodes, err := p.scan(sanitize(strings.TrimRight(ls[i].text, " \t")), ls[i], ms, start)
		if err != nil {
			return nil, err
		}
		out = append(out, nodes...)
	}
	return merge(out), nil
}

// ambiguous reports whether a paragraph is worth checking, which is only when
// it holds a character a marker can start with. Prose that holds none of them
// is its own rendering.
func ambiguous(ls []line) bool {
	for i := range ls {
		if strings.ContainsAny(ls[i].text, "*_~`[<!@\\") {
			return true
		}
	}
	return false
}

// rendersBack also accepts a reading that differs from the lines only in their
// escapes, such as an author's needless "\.": every marker it paired is still
// where the line has one.
func (p *parser) rendersBack(nodes []Node, ls []line, noBlocks bool) bool {
	w := writer{buf: p.scratch[:0], opt: p.opt, gl: p.gl, noBlocks: noBlocks}
	w.blocks([]Node{NewNode("paragraph").WithContent(nodes...)}, false)
	w.endLine()
	p.scratch = w.buf
	if sameLines(p.scratch, ls, nil) {
		return true
	}
	if bytes.IndexByte(p.scratch, '\\') < 0 && !anyEscape(ls) {
		return false
	}
	loose := func(s string) string { return unescape(s, false, p.gl) }
	return sameLines([]byte(loose(string(p.scratch))), ls, loose)
}

func anyEscape(ls []line) bool {
	for i := range ls {
		if strings.IndexByte(ls[i].text, '\\') >= 0 {
			return true
		}
	}
	return false
}

func sameLines(rendered []byte, ls []line, normalise func(string) string) bool {
	at := 0
	for i := range ls {
		want := sanitize(strings.TrimRight(ls[i].text, " \t"))
		if normalise != nil {
			want = normalise(want)
		}
		if i > 0 {
			if at >= len(rendered) || rendered[at] != '\n' {
				return false
			}
			at++
		}
		if len(rendered)-at < len(want) || string(rendered[at:at+len(want)]) != want {
			return false
		}
		at += len(want)
	}
	return at == len(rendered)
}

// scan's atStart says s begins its line, the only place a backslash before an
// expand or decision marker is an escape.
func (p *parser) scan(s string, ln line, ms markSet, atStart bool) ([]Node, error) {
	var out []Node
	start := 0
	// lit holds the run before an escape, without its backslash.
	var lit strings.Builder
	flush := func(end int) {
		if lit.Len() > 0 {
			lit.WriteString(s[start:end])
			out = append(out, NewText(lit.String(), ms.marks()...))
			lit.Reset()
			return
		}
		if end > start {
			out = append(out, NewText(s[start:end], ms.marks()...))
		}
	}
	// emphasis reads a delimited run. A marker with a space inside it is not
	// emphasis — the renderer keeps the spaces outside the markers precisely so
	// that "*a* b" cannot be read as one run — and an underscore that continues
	// a word is a name.
	emphasis := func(i int, delim string, inner markSet) (int, error) {
		body := i + len(delim)
		if body >= len(s) || spaceByte(s[body]) {
			return 0, nil
		}
		// Strong wraps em, so "***both***" opens with two stars and one, and the
		// closing run has to give its last two back to the strong. A plain
		// "**a****b**" is two runs of two and closes at the front of each.
		tail := delim == "**" && runLen(s, i, '*') > 2

		for at := findClose(s, delim, body); at >= body; at = findClose(s, delim, at+1) {
			end := at
			if tail {
				end = at + runLen(s, at, '*') - 2
			}
			switch {
			case end <= body || spaceByte(s[end-1]):
			case delim == "_" && end+1 < len(s) && wordByte(s[end+1]):
			default:
				flush(i)
				nodes, err := p.scan(s[body:end], ln, inner, false)
				if err != nil {
					return 0, err
				}
				out = append(out, nodes...)
				return end + len(delim), nil
			}
		}
		return 0, nil
	}

	for i := 0; i < len(s); {
		if s[i] == '\\' && i+1 < len(s) {
			switch {
			case isPunct(s[i+1]):
				lit.WriteString(s[start:i])
				start, i = i+1, i+2
				continue
			case atStart && i == 0 && glyphMarker(s[1:], p.gl) > 0:
				start, i = 1, 1
				continue
			}
		}
		var next int
		var err error
		switch {
		case s[i] == '@' && !ms.linked && strings.HasPrefix(s[i+1:], "["):
			if node, n, ok := mentionAt(s[i:], p.gl); ok {
				flush(i)
				out = append(out, node)
				next = i + n
			}
		case s[i] == '`' && !ms.code:
			if content, n, ok := codeSpanAt(s[i:]); ok {
				flush(i)
				coded := ms
				coded.code = true
				if content != "" {
					out = append(out, NewText(content, coded.marks()...))
				}
				next = i + n
			}
		case strings.HasPrefix(s[i:], "!["):
			if node, n, ok := imageNode(s[i:], "mediaInline"); ok {
				flush(i)
				out = append(out, node)
				next = i + n
			}
		case s[i] == '[':
			if typ, ok := unsupportedMarker(markerSpan(s[i:])); ok {
				return nil, p.fail(ln, fmt.Errorf("%w: %s", ErrUnsupportedMarker, typ))
			}
			if !ms.linked {
				if text, dest, n, ok := linkAt(s[i:]); ok {
					linked := ms
					linked.linked, linked.href = true, dest
					flush(i)
					nodes, scanErr := p.scan(text, ln, linked, false)
					if scanErr != nil {
						return nil, scanErr
					}
					out = append(out, nodes...)
					next = i + n
				}
			}
		case s[i] == '<' && !ms.linked:
			if url, n, ok := autolinkAt(s[i:]); ok {
				flush(i)
				linked := ms
				linked.linked, linked.href = true, url
				out = append(out, NewText(url, linked.marks()...))
				next = i + n
			}
		case strings.HasPrefix(s[i:], "**") && !ms.strong && !ms.plain:
			strong := ms
			strong.strong = true
			next, err = emphasis(i, "**", strong)
		case strings.HasPrefix(s[i:], "~~") && !ms.strike && !ms.plain:
			strike := ms
			strike.strike = true
			next, err = emphasis(i, "~~", strike)
		case s[i] == '*' && !ms.em && !ms.plain:
			em := ms
			em.em = true
			next, err = emphasis(i, "*", em)
		case s[i] == '_' && !ms.underline && !ms.plain && (i == 0 || !wordByte(s[i-1])):
			underline := ms
			underline.underline = true
			next, err = emphasis(i, "_", underline)
		}
		if err != nil {
			return nil, err
		}
		if next > i {
			i, start = next, next
			continue
		}
		i++
	}
	flush(len(s))
	return out, nil
}

// merge joins neighbouring runs of unmarked text, so that a paragraph does not
// come out of the parser split at every delimiter that turned out not to be
// one. Marked runs are left alone: two links in a row are two links, and
// joining them turns a pair of bare addresses into one link with both of them
// as its text.
func merge(in []Node) []Node {
	out := make([]Node, 0, len(in))
	for i := range in {
		last := len(out) - 1
		if last >= 0 && plainText(in[i]) && plainText(out[last]) {
			out[last].Text += in[i].Text
			continue
		}
		out = append(out, in[i])
	}
	return out
}

func plainText(n Node) bool { return n.Type == "text" && len(n.Marks) == 0 }

// codeSpanAt reads a backtick span, undoing the padding the renderer adds when
// the code itself starts or ends with a backtick.
func codeSpanAt(s string) (content string, n int, ok bool) {
	fence := 0
	for fence < len(s) && s[fence] == '`' {
		fence++
	}
	rest := s[fence:]
	for i := 0; i < len(rest); {
		if rest[i] != '`' {
			i++
			continue
		}
		run := i
		for run < len(rest) && rest[run] == '`' {
			run++
		}
		if run-i == fence {
			return unpad(rest[:i]), fence + run, true
		}
		i = run
	}
	return "", 0, false
}

func unpad(s string) string {
	if len(s) >= 2 && s[0] == ' ' && s[len(s)-1] == ' ' && strings.Trim(s, " ") != "" {
		return s[1 : len(s)-1]
	}
	return s
}

// linkAt reads "[text](dest)". The renderer leaves a pair of brackets in the
// text alone, so a bracket inside it is matched by depth rather than by the
// first "]".
func linkAt(s string) (text, dest string, n int, ok bool) {
	if s == "" || s[0] != '[' {
		return "", "", 0, false
	}
	shut := closeBracket(s)
	if shut < 0 || shut+1 >= len(s) || s[shut+1] != '(' {
		return "", "", 0, false
	}
	text, i := s[1:shut], shut+2

	if i < len(s) && s[i] == '<' {
		end := strings.IndexByte(s[i:], '>')
		if end < 0 || i+end+1 >= len(s) || s[i+end+1] != ')' {
			return "", "", 0, false
		}
		return text, unangle(s[i+1 : i+end]), i + end + 2, true
	}
	depth := 1
	for j := i; j < len(s); j++ {
		switch s[j] {
		case '(':
			depth++
		case ')':
			depth--
			if depth == 0 {
				return text, s[i:j], j + 1, true
			}
		}
	}
	return "", "", 0, false
}

func closeBracket(s string) int {
	depth := 0
	for i := 0; i < len(s); {
		if s[i] == '\\' && i+1 < len(s) && isPunct(s[i+1]) {
			i += 2
			continue
		}
		if s[i] == '`' {
			if _, n, ok := codeSpanAt(s[i:]); ok {
				i += n
				continue
			}
		}
		switch s[i] {
		case '[':
			depth++
		case ']':
			depth--
			if depth == 0 {
				return i
			}
		}
		i++
	}
	return -1
}

// markerSpan returns the bracketed run starting at s, which is what an
// "[unsupported: …]" marker is tested against.
func markerSpan(s string) string {
	if shut := closeBracket(s); shut > 0 {
		return s[:shut+1]
	}
	return ""
}

// angles undoes what the renderer had to do to put an address inside angle
// brackets. It only ever runs on a destination that arrived in that form, which
// is one the renderer only writes for an address holding a space, a bracket or
// a parenthesis.
var unangles = strings.NewReplacer("%3C", "<", "%3E", ">")

func unangle(s string) string { return unangles.Replace(s) }

func autolinkAt(s string) (url string, n int, ok bool) {
	if s == "" || s[0] != '<' {
		return "", 0, false
	}
	end := strings.IndexByte(s, '>')
	if end < 2 {
		return "", 0, false
	}
	url = s[1:end]
	if strings.ContainsAny(url, " \t<") || !hasScheme(url) {
		return "", 0, false
	}
	return url, end + 1, true
}

func hasScheme(s string) bool {
	if s == "" || !letterByte(s[0]) {
		return false
	}
	for i := range len(s) {
		switch c := s[i]; {
		case letterByte(c) || isDigit(c) || c == '+' || c == '-' || c == '.':
		case c == ':':
			return i+1 < len(s)
		default:
			return false
		}
	}
	return false
}

// findClose finds the delimiter that closes an emphasis run, stepping over the
// spans that are not prose: a closing marker inside a code span or inside a
// link's address is not a closing marker.
//
// A doubled star met while looking for the end of an em run is the ambiguity of
// this dialect. "*a**b*" is two italic runs the renderer wrote side by side
// because their other marks differ, and "*a **b** c*" is one italic sentence
// with a bold word in it. What tells them apart is where the spaces are: a run
// with a space in front of it cannot end anything, so it opens a strong that a
// later run has to close, and that later run is spoken for.
func findClose(s, delim string, from int) int {
	strong := 0
	for i := from; i < len(s); {
		if s[i] == '\\' && i+1 < len(s) && isPunct(s[i+1]) {
			i += 2
			continue
		}
		if s[i] == '`' {
			if _, n, ok := codeSpanAt(s[i:]); ok {
				i += n
				continue
			}
		}
		if s[i] == '[' || strings.HasPrefix(s[i:], "![") {
			at := i
			if s[i] == '!' {
				at++
			}
			if _, _, n, ok := linkAt(s[at:]); ok {
				i = at + n
				continue
			}
		}
		if delim == "*" && strings.HasPrefix(s[i:], "**") {
			n := runLen(s, i, '*')
			opens := i+n < len(s) && !spaceByte(s[i+n])
			switch closes := i > from && !spaceByte(s[i-1]); {
			case closes && strong == 0:
				return i
			case closes && strong > 0:
				strong--
			case opens:
				strong++
			}
			i += n
			continue
		}
		if strings.HasPrefix(s[i:], delim) {
			return i
		}
		i++
	}
	return -1
}

func runLen(s string, at int, c byte) int {
	n := 0
	for at+n < len(s) && s[at+n] == c {
		n++
	}
	return n
}

func mentionAt(s string, gl glyphs) (Node, int, bool) {
	text, dest, n, ok := linkAt(s[1:])
	if !ok {
		return Node{}, 0, false
	}
	id, found := strings.CutPrefix(dest, mentionScheme)
	if !found || strings.TrimSpace(id) == "" {
		return Node{}, 0, false
	}
	name := strings.TrimPrefix(strings.TrimSpace(unescape(text, false, gl)), "@")
	return NewNode("mention").WithAttrs(Attrs{"id": id, "text": "@" + name}), n + 1, true
}

// imageNode reads "![alt](dest)" into a media node of the given type.
func imageNode(s, typ string) (Node, int, bool) {
	if !strings.HasPrefix(s, "![") {
		return Node{}, 0, false
	}
	alt, dest, n, ok := linkAt(s[1:])
	if !ok || dest == "" {
		return Node{}, 0, false
	}
	attrs := Attrs{}
	if id, found := strings.CutPrefix(dest, "media:"); found && id != "" {
		attrs["id"], attrs["type"] = id, "file"
	} else {
		attrs["url"], attrs["type"] = dest, "external"
	}
	// The renderer spells a missing alt "media", so that is what it means here.
	if alt != "" && alt != "media" {
		attrs["alt"] = alt
	}
	return NewNode(typ).WithAttrs(attrs), n + 1, true
}

func letterByte(c byte) bool { return (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') }

func spaceByte(c byte) bool { return c == ' ' || c == '\t' }

// wordByte reports whether a byte continues a word, which is what stops
// snake_case from reading as underlined text.
func wordByte(c byte) bool { return letterByte(c) || isDigit(c) || c >= 0x80 }
