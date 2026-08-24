package adf

import (
	"cmp"
	"errors"
	"fmt"
	"maps"
	"strconv"
	"strings"

	"github.com/charmbracelet/x/ansi"
)

// ParseMarkdown turns markdown back into an ADF document.
//
// Its input language is exactly what [Markdown] emits, which is markdown by
// shape rather than by standard, so this is the inverse of that function and
// not a general CommonMark implementation. Anything it cannot map to a node it
// keeps as prose, so text is never silently dropped.
//
// The conversion ADF → markdown → ADF is not lossless on its own: markdown has
// no spelling for an account id, a lozenge colour or the attributes of a node
// type nobody has published yet. Use [ParseMarkdownInto] whenever the document
// the markdown was rendered from is still to hand — it restores every block the
// author did not touch from the original bytes, which is the only way an
// untouched document survives byte for byte. [ParseMarkdownDropsOnly] lists
// what a parse without the original loses.
func ParseMarkdown(md string) (Doc, error) { return ParseMarkdownWith(md, Options{}) }

// ParseMarkdownWith parses markdown with explicit options.
//
// Only [Options.ASCII] changes how the markdown is read: it selects the marker
// set a panel, a decision item and an expand are recognised by, and it has to
// match the option the markdown was rendered with. TableWidth and Location are
// carried for [ParseMarkdownInto], which re-renders the original document to
// find out which blocks are untouched.
func ParseMarkdownWith(md string, opt Options) (Doc, error) {
	p := newParser(opt)
	nodes, err := p.blocks(splitLines(md), "doc")
	if err != nil {
		return Doc{}, err
	}
	return NewDoc(nodes...), nil
}

// ParseMarkdownInto parses markdown that was rendered from d, and reuses d's
// own nodes for every top-level block the author left alone.
//
// This is what makes an edit safe. A reused node keeps the bytes it was parsed
// from, so it re-encodes exactly as it arrived — attributes this package does
// not model, node types it has never heard of, the account id behind a mention,
// the colour of a lozenge and the key order Jira happened to use. Markdown
// carries none of those; the original document does.
//
//	md := adf.Markdown(d)          // hand to $EDITOR
//	out, err := adf.ParseMarkdownInto(d, edited, adf.Options{})
//
// When edited is byte-identical to md, adf.Marshal(out) is byte-identical to
// adf.Marshal(d). Blocks the author did change are parsed from the markdown and
// lose whatever markdown could not carry.
//
// opt must be the options d was rendered with, or nothing will match. Leave
// TableWidth at zero for markdown somebody is going to edit: a bounded render
// truncates a table's cells with an ellipsis, and an edit anywhere in that
// table writes the truncation back.
func ParseMarkdownInto(d Doc, md string, opt Options) (Doc, error) {
	nodes, err := newParser(opt).reconcile(splitLines(md), d.Content)
	if err != nil {
		return Doc{}, err
	}
	// A field Jira holds as null arrives as the zero document and has to go back
	// as null, not as an empty one.
	if len(nodes) == 0 && d.IsZero() {
		return d, nil
	}
	out := d
	out.Version, out.Type = max(d.Version, 1), cmp.Or(d.Type, "doc")
	out.Content = nodes
	out.extra = maps.Clone(d.extra)
	return out, nil
}

// ParseMarkdownDropsOnly names, in document order, every construct that ADF →
// markdown → ADF cannot reproduce without the original document. It is here so
// that a caller can tell a user what an edit will cost rather than finding out
// afterwards, and so that the list is maintained beside the code that causes
// it.
//
// [ParseMarkdownInto] restores all of these for a block the author did not
// touch. Nothing in this list is dropped as text: a mention still reads
// "@Someone" and a lozenge still reads "[Done]", they are simply prose again.
func ParseMarkdownDropsOnly() []string {
	return []string{
		"mention: the account id, which markdown has no room for, so a mention becomes its own display text",
		"status: the lozenge colour, so a lozenge becomes bracketed text",
		"date: the instant, which renders as a day in one timezone and cannot be read back as an epoch",
		"emoji: the shortName and id behind the character",
		"media: the collection, dimensions and layout of an attachment",
		"table: colspan, rowspan, cell background, layout and the number column; a cell's blocks are folded to one line",
		"table: every cell of a table rendered with a TableWidth, which is truncated to fit the width",
		"panel: the case of a panelType this package does not know, which renders uppercased",
		"heading: a heading with no content, which renders as nothing at all",
		"hardBreak: one that opens a block, or sits in a heading, or is followed by a line that reads as a marker",
		"text: prose that begins a line with a marker, because the renderer does not escape what an author typed",
		"text: trailing whitespace on a line, and the control characters the renderer strips",
		"marks: the order marks arrived in, which is meaningless but byte-significant",
		"marks: neighbouring runs whose marks overlap, which spell the same characters as one run inside another",
		"any node type ADF gained after this package was written, which renders as an [unsupported: …] marker",
	}
}

// ParseError reports markdown this package will not turn into ADF, and where.
type ParseError struct {
	// Line is 1-based, and counts the lines of the markdown as it was given.
	Line int
	// Text is the line the parser stopped on.
	Text string
	Err  error
}

func (e *ParseError) Error() string {
	return fmt.Sprintf("adf: line %d: %v: %q", e.Line, e.Err, e.Text)
}

func (e *ParseError) Unwrap() error { return e.Err }

var (
	// ErrUnsupportedMarker reports an "[unsupported: …]" marker that could not
	// be matched to a node in the document the markdown came from. The marker
	// stands for a node whose attributes markdown never carried, so rebuilding
	// it would either invent a node Jira rejects or quietly delete one.
	ErrUnsupportedMarker = errors.New("this stands for a node markdown cannot rebuild, and no original was given to restore it from")

	// ErrNesting reports a nesting ADF has no shape for, such as a table inside
	// a quote or a heading inside a list item.
	ErrNesting = errors.New("ADF cannot nest these")
)

// line is one line of the input with the number it had before any prefix was
// stripped from it, so that an error points at the line the author sees.
type line struct {
	text string
	no   int
}

func (l line) blank() bool { return strings.TrimRight(l.text, " \t") == "" }

func (l line) indent() int {
	for i := range len(l.text) {
		if l.text[i] != ' ' {
			return i
		}
	}
	return len(l.text)
}

// undent removes up to n leading spaces, which is how a continuation line is
// taken out of the block that indented it.
func (l line) undent(n int) line {
	i := 0
	for i < n && i < len(l.text) && l.text[i] == ' ' {
		i++
	}
	l.text = l.text[i:]
	return l
}

func splitLines(md string) []line {
	md = strings.TrimPrefix(md, "\ufeff")
	if strings.ContainsRune(md, '\r') {
		md = strings.ReplaceAll(md, "\r\n", "\n")
		md = strings.ReplaceAll(md, "\r", "\n")
	}
	parts := strings.Split(md, "\n")
	out := make([]line, len(parts))
	for i := range parts {
		out[i] = line{text: parts[i], no: i + 1}
	}
	return out
}

type parser struct {
	opt Options
	gl  glyphs
	// scratch is reused by every render the parser does of its own work, which
	// is one per line that holds a marker.
	scratch []byte
}

func newParser(opt Options) *parser { return &parser{opt: opt, gl: glyphsFor(opt.ASCII)} }

func (p *parser) fail(l line, err error) error {
	return &ParseError{Line: l.no, Text: l.text, Err: err}
}

// forbidden is the part of ADF's content model that markdown can ask this
// parser to break. It is deliberately short: only nestings ADF has no shape at
// all for are refused, because guessing wider rejects documents Jira itself
// stores.
var forbidden = map[string]map[string]bool{
	"blockquote": {"table": true, "heading": true, "rule": true},
	"listItem":   {"table": true, "heading": true, "rule": true},
	"panel":      {"table": true, "panel": true},
}

func (p *parser) blocks(ls []line, parent string) ([]Node, error) {
	var out []Node
	for i := 0; i < len(ls); {
		if ls[i].blank() {
			i++
			continue
		}
		n, next, err := p.block(ls, i, parent)
		if err != nil {
			return nil, err
		}
		if forbidden[parent][n.Type] {
			return nil, p.fail(ls[i], fmt.Errorf("%w: a %s inside a %s", ErrNesting, n.Type, parent))
		}
		out = append(out, n)
		i = next
	}
	return out, nil
}

// block reads one block starting at a non-blank line. The index it returns is
// where the next block starts and is valid even when the error is not nil, so
// that a caller reconciling against an original document can still measure the
// block it could not build.
func (p *parser) block(ls []line, i int, parent string) (Node, int, error) {
	l := ls[i]
	switch {
	case isFence(l.text):
		return p.codeBlock(ls, i)
	case strings.HasPrefix(l.text, ">"):
		return p.quoted(ls, i)
	case strings.HasPrefix(l.text, "|"):
		return p.table(ls, i)
	case isRule(l.text):
		return NewNode("rule"), i + 1, nil
	case headingLevel(l.text) > 0:
		return p.heading(ls, i)
	case p.expandTitle(l.text) != nil:
		return p.expand(ls, i, parent)
	case isMarker(l.text, p.gl):
		return p.list(ls, i)
	case isImageLine(l.text):
		return p.media(ls, i)
	}
	return p.paragraph(ls, i)
}

// startsBlock reports whether a line would begin a new block, which is what
// ends the paragraph above it. Prose is never wrapped by the renderer, so the
// only reason a paragraph runs to a second line is a hard break.
func (p *parser) startsBlock(l line) bool {
	return isFence(l.text) || strings.HasPrefix(l.text, ">") || strings.HasPrefix(l.text, "|") ||
		isRule(l.text) || headingLevel(l.text) > 0 || p.expandTitle(l.text) != nil ||
		isMarker(l.text, p.gl) || isImageLine(l.text)
}

func (p *parser) paragraph(ls []line, i int) (Node, int, error) {
	end := i + 1
	for end < len(ls) && !ls[end].blank() && !p.startsBlock(ls[end]) {
		end++
	}
	content, err := p.inline(ls[i:end])
	if err != nil {
		return Node{}, end, err
	}
	return NewNode("paragraph").WithContent(content...), end, nil
}

// headingLevel reports the level of an ATX heading, and zero for a line that is
// not one. A row of hashes with nothing after it is prose: the renderer writes
// nothing at all for a heading with no content, so a line that says only "#"
// was typed rather than rendered, and reading it as a heading would delete it.
func headingLevel(text string) int {
	n := 0
	for n < len(text) && text[n] == '#' {
		n++
	}
	if n == 0 || n > 6 || n == len(text) || text[n] != ' ' {
		return 0
	}
	if strings.TrimSpace(text[n:]) == "" {
		return 0
	}
	return n
}

func (p *parser) heading(ls []line, i int) (Node, int, error) {
	level := headingLevel(ls[i].text)
	rest := line{text: strings.TrimSpace(ls[i].text[level:]), no: ls[i].no}
	content, err := p.inline([]line{rest})
	if err != nil {
		return Node{}, i + 1, err
	}
	n := NewNode("heading").WithAttrs(Attrs{"level": level}).WithContent(content...)
	return n, i + 1, nil
}

// isRule matches a thematic break. The renderer only ever writes "---", but a
// document that has been through an editor can carry any of the three spellings.
func isRule(text string) bool {
	t := strings.TrimRight(text, " \t")
	if len(t) < 3 {
		return false
	}
	c := t[0]
	if c != '-' && c != '*' && c != '_' {
		return false
	}
	n := 0
	for i := range len(t) {
		switch t[i] {
		case c:
			n++
		case ' ', '\t':
		default:
			return false
		}
	}
	return n >= 3
}

func isFence(text string) bool { _, _, ok := fenceAt(text); return ok }

// fenceAt reads a code fence, returning the character it is drawn with and how
// long it is. A fence closes on a run of the same character at least as long.
func fenceAt(text string) (c byte, n int, ok bool) {
	if text == "" || (text[0] != '`' && text[0] != '~') {
		return 0, 0, false
	}
	c = text[0]
	for n < len(text) && text[n] == c {
		n++
	}
	if n < 3 {
		return 0, 0, false
	}
	if c == '`' && strings.ContainsRune(text[n:], '`') {
		return 0, 0, false
	}
	return c, n, true
}

func (p *parser) codeBlock(ls []line, i int) (Node, int, error) {
	c, n, _ := fenceAt(ls[i].text)
	language := strings.TrimSpace(ls[i].text[n:])
	end := i + 1
	for end < len(ls) {
		if cc, nn, ok := fenceAt(strings.TrimRight(ls[end].text, " \t")); ok && cc == c && nn >= n &&
			strings.TrimRight(ls[end].text, " \t") == strings.Repeat(string(c), nn) {
			break
		}
		end++
	}
	code := make([]string, 0, end-i-1)
	for _, l := range ls[i+1 : end] {
		code = append(code, l.text)
	}
	if end < len(ls) {
		end++
	}

	node := NewNode("codeBlock")
	if language != "" {
		node = node.WithAttrs(Attrs{"language": language})
	}
	if text := sanitize(strings.Join(code, "\n")); text != "" {
		node = node.WithContent(NewText(text))
	}
	return node, end, nil
}

// quoted reads the block a run of "> " lines carries. Three different nodes
// come out of that prefix — a quote, a panel, and the marker the renderer
// leaves where a node type it does not know used to be — so the prefix is
// stripped first and the block identified from what is under it.
func (p *parser) quoted(ls []line, i int) (Node, int, error) {
	end := i
	inner := make([]line, 0, 4)
	for end < len(ls) && strings.HasPrefix(ls[end].text, ">") {
		l := ls[end]
		l.text = strings.TrimPrefix(l.text[1:], " ")
		inner = append(inner, l)
		end++
	}

	if typ, ok := unsupportedMarker(inner[0].text); ok {
		return Node{}, end, p.fail(inner[0], fmt.Errorf("%w: %s", ErrUnsupportedMarker, typ))
	}
	if attrs, ok := p.panelAttrs(inner[0].text); ok {
		content, err := p.blocks(inner[1:], "panel")
		if err != nil {
			return Node{}, end, err
		}
		return NewNode("panel").WithAttrs(attrs).WithContent(filled(content)...), end, nil
	}
	content, err := p.blocks(inner, "blockquote")
	if err != nil {
		return Node{}, end, err
	}
	return NewNode("blockquote").WithContent(filled(content)...), end, nil
}

// panelAttrs inverts the marker and label the renderer puts on a panel's first
// line. An unknown panelType is rendered uppercased and comes back lowercased,
// because ADF's own types are lower case and the original spelling is gone.
func (p *parser) panelAttrs(text string) (Attrs, bool) {
	marker, label, ok := strings.Cut(text, " ")
	if !ok || !isLabel(label) {
		return nil, false
	}
	switch {
	case marker == p.gl.info && label == "INFO":
		return Attrs{"panelType": "info"}, true
	case marker == p.gl.note && label == "NOTE":
		return Attrs{"panelType": "note"}, true
	case marker == p.gl.success && (label == "SUCCESS" || label == "TIP"):
		return Attrs{"panelType": strings.ToLower(label)}, true
	case marker == p.gl.warning && label == "WARNING":
		return Attrs{"panelType": "warning"}, true
	case marker == p.gl.failure && label == "ERROR":
		return Attrs{"panelType": "error"}, true
	case marker == p.gl.custom && label == "PANEL":
		return Attrs{"panelType": "custom"}, true
	case marker == p.gl.custom:
		return Attrs{"panelType": strings.ToLower(label)}, true
	case label == "PANEL":
		return Attrs{"panelType": "custom", "panelIconText": marker}, true
	}
	return nil, false
}

// isLabel reports whether a word is the shouted label the renderer writes
// beside a panel marker, which is what tells a panel from a quote whose first
// line happens to start with a glyph.
func isLabel(s string) bool {
	if s == "" {
		return false
	}
	for _, r := range s {
		switch {
		case r >= 'A' && r <= 'Z', r >= '0' && r <= '9', r == ' ', r == '-', r == '_', r == '/':
		default:
			return false
		}
	}
	return true
}

// unsupportedMarker reads the marker the renderer leaves in place of a node
// type it does not know, and reports the type it stood for.
func unsupportedMarker(text string) (string, bool) {
	const open = "[unsupported: "
	if !strings.HasPrefix(text, open) || !strings.HasSuffix(text, "]") {
		return "", false
	}
	body := text[len(open) : len(text)-1]
	typ, _, _ := strings.Cut(body, ":")
	if typ == "" {
		return "", false
	}
	for i, r := range typ {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z':
		case i > 0 && r >= '0' && r <= '9':
		default:
			return "", false
		}
	}
	return typ, true
}

func (p *parser) expandTitle(text string) *string {
	if text != p.gl.expand && !strings.HasPrefix(text, p.gl.expand+" ") {
		return nil
	}
	title := strings.TrimSpace(text[len(p.gl.expand):])
	return &title
}

func (p *parser) expand(ls []line, i int, parent string) (Node, int, error) {
	title := *p.expandTitle(ls[i].text)
	end, inner := gather(ls, i+1, 2)

	typ := "expand"
	if parent == "expand" || parent == "nestedExpand" {
		typ = "nestedExpand"
	}
	content, err := p.blocks(inner, typ)
	if err != nil {
		return Node{}, end, err
	}
	n := NewNode(typ).WithContent(filled(content)...)
	if title != "" {
		n = n.WithAttrs(Attrs{"title": title})
	}
	return n, end, nil
}

// gather collects the lines indented under a block that opened at i-1, undented
// by width, and returns where the block ends. A blank line only continues the
// block when something indented follows it.
func gather(ls []line, i, width int) (end int, inner []line) {
	for i < len(ls) {
		if !ls[i].blank() {
			if ls[i].indent() < width {
				break
			}
			inner = append(inner, ls[i].undent(width))
			i++
			continue
		}
		next := i
		for next < len(ls) && ls[next].blank() {
			next++
		}
		if next == len(ls) || ls[next].indent() < width {
			break
		}
		for ; i < next; i++ {
			inner = append(inner, line{no: ls[i].no})
		}
	}
	return i, inner
}

// marker is one list item's bullet, number or checkbox, and the width its
// continuation lines are indented by.
type marker struct {
	list  string // bulletList, orderedList, taskList or decisionList
	item  string // listItem, taskItem or decisionItem
	width int
	rest  string
	attrs Attrs
}

func isMarker(text string, gl glyphs) bool { _, ok := markerAt(text, gl); return ok }

func markerAt(text string, gl glyphs) (marker, bool) {
	bullet, rest, ok := cutMarker(text)
	if !ok {
		return marker{}, false
	}
	if isDigit(bullet[0]) {
		number, err := strconv.Atoi(bullet[:len(bullet)-2])
		if err != nil {
			return marker{}, false
		}
		return marker{
			list: "orderedList", item: "listItem", rest: rest,
			// ADF numbers a list from one. A list written to start at zero is
			// rendered from one, so it is read back from one too.
			width: ansi.StringWidth(bullet), attrs: Attrs{"order": max(number, 1)},
		}, true
	}
	if box, tail, ok := cutInner(rest, "[ ]", "[x]", "[X]"); ok {
		state := "TODO"
		if box != "[ ]" {
			state = "DONE"
		}
		return marker{
			list: "taskList", item: "taskItem", rest: tail,
			width: ansi.StringWidth(bullet) + len(box) + 1, attrs: Attrs{"state": state},
		}, true
	}
	if _, tail, ok := cutInner(rest, gl.decision); ok {
		return marker{
			list: "decisionList", item: "decisionItem", rest: tail,
			width: ansi.StringWidth(bullet+gl.decision) + 1, attrs: Attrs{"state": "DECIDED"},
		}, true
	}
	return marker{list: "bulletList", item: "listItem", rest: rest, width: ansi.StringWidth(bullet)}, true
}

// cutInner takes the checkbox or the lozenge that follows a bullet off the rest
// of the line. An item with nothing in it has the marker and no space after it,
// because the renderer trims the trailing space off every line it writes.
func cutInner(rest string, want ...string) (mark, tail string, ok bool) {
	for _, w := range want {
		switch {
		case rest == w:
			return w, "", true
		case strings.HasPrefix(rest, w+" "):
			return w, rest[len(w)+1:], true
		}
	}
	return "", "", false
}

// cutMarker takes the marker off the front of a line: a bullet, an ordered
// number, or the checkbox or lozenge that follows one. The marker keeps the
// single space after it, because that space is part of the width the
// continuation lines are indented by.
func cutMarker(text string) (mark, rest string, ok bool) {
	end := 0
	switch {
	case text == "":
		return "", "", false
	case text[0] == '-' || text[0] == '*' || text[0] == '+':
		end = 1
	default:
		for end < len(text) && end < 9 && isDigit(text[end]) {
			end++
		}
		if end == 0 || end == len(text) || (text[end] != '.' && text[end] != ')') {
			return "", "", false
		}
		end++
	}
	switch {
	case end == len(text):
		// A line that is only a marker is an item with nothing in it, and the
		// renderer writes those with a hyphen or a number. A lone star is the
		// character an author typed.
		if text[0] == '*' || text[0] == '+' {
			return "", "", false
		}
		return text + " ", "", true
	case text[end] == ' ':
		return text[:end+1], text[end+1:], true
	}
	return "", "", false
}

func isDigit(c byte) bool { return c >= '0' && c <= '9' }

// list reads one list. A blank line ends it: the renderer never puts one
// between the items of a single list, so a blank line means the author started
// a second one.
func (p *parser) list(ls []line, i int) (Node, int, error) {
	first, _ := markerAt(ls[i].text, p.gl)
	node := NewNode(first.list)
	if first.list == "orderedList" {
		if order, _ := attrInt(first.attrs, "order"); order != 1 {
			node = node.WithAttrs(Attrs{"order": order})
		}
	}

	// A list runs to the end of its items even when one of them will not parse,
	// so that a caller reconciling against an original document is told how far
	// the list reached and can reuse the node it already had.
	var failed error
	for i < len(ls) {
		// ADF's content model for a task or decision list is (item | list)+:
		// indenting an action item in the editor stores a sibling list inside
		// its parent rather than inside the item above it, and the renderer
		// indents that sibling by two rather than by a marker's width.
		if checklist(first.list) && !ls[i].blank() && ls[i].indent() > 0 {
			end, inner := gather(ls, i, 2)
			if end == i {
				break
			}
			nested, err := p.blocks(inner, first.list)
			failed = cmp.Or(failed, err)
			node.Content = append(node.Content, nested...)
			i = end
			continue
		}

		m, ok := markerAt(ls[i].text, p.gl)
		if !ok || m.list != first.list {
			break
		}
		end, inner := gather(ls, i+1, m.width)
		inner = append([]line{{text: m.rest, no: ls[i].no}}, inner...)

		content, err := p.blocks(inner, m.item)
		failed = cmp.Or(failed, err)
		item := NewNode(m.item).WithContent(content...)
		if m.item == "listItem" {
			item = item.WithContent(filled(content)...)
		} else {
			item = item.WithAttrs(m.attrs)
			// A task or decision item holds inline content where a list item
			// holds blocks, so the paragraph the line parsed into is unwrapped.
			if len(content) == 1 && content[0].Type == "paragraph" {
				item = item.WithContent(content[0].Content...)
			}
		}
		node.Content = append(node.Content, item)
		i = end
	}
	return node, i, failed
}

func checklist(list string) bool { return list == "taskList" || list == "decisionList" }

// filled gives a container that must hold at least one block an empty
// paragraph to hold. An empty list item, quote or cell is something an author
// can write, and ADF has no shape for a container with nothing in it.
func filled(content []Node) []Node {
	if len(content) > 0 {
		return content
	}
	return []Node{NewNode("paragraph")}
}

// media reads a run of image lines, and the caption line the renderer puts
// under a single one. Several images with no blank line between them are one
// group, which is how ADF stores an editor's row of attachments.
func (p *parser) media(ls []line, i int) (Node, int, error) {
	end := i
	var images []Node
	for end < len(ls) && isImageLine(ls[end].text) {
		n, _, ok := imageNode(ls[end].text, "media")
		if !ok {
			break
		}
		images = append(images, n)
		end++
	}
	switch {
	case len(images) == 0:
		return p.paragraph(ls, i)
	case len(images) > 1:
		return NewNode("mediaGroup").WithContent(images...), end, nil
	}

	single := NewNode("mediaSingle").WithContent(images...)
	if end < len(ls) && !ls[end].blank() && !p.startsBlock(ls[end]) {
		caption, err := p.inline(ls[end : end+1])
		if err != nil {
			return Node{}, end + 1, err
		}
		single = single.WithContent(images[0],
			NewNode("caption").WithContent(NewNode("paragraph").WithContent(caption...)))
		end++
	}
	return single, end, nil
}

func isImageLine(text string) bool {
	_, n, ok := imageNode(text, "media")
	return ok && n == len(text)
}
