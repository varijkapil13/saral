package adf

import (
	"strconv"
	"strings"
	"time"

	"github.com/charmbracelet/x/ansi"
)

// Options tunes how a document is rendered.
type Options struct {
	// TableWidth bounds the total width of a table, in terminal cells. It is
	// the only thing a width is needed for here: prose is never wrapped, so
	// that the viewport showing it can reflow on a resize without first having
	// to undo the wrapping this package chose. Zero lets a table be as wide as
	// its widest row.
	TableWidth int

	// ASCII swaps the Unicode markers for ASCII ones, for the terminals and
	// fonts docs/UX.md says never to assume anything about.
	ASCII bool

	// Location renders the instant carried by a date node. Nil means UTC. No
	// clock is read, so a document always renders to the same bytes.
	Location *time.Location
}

// Markdown renders a document as markdown a terminal can show.
//
// The output is plain text: no ANSI, no wrapping, and no trailing newline, so
// that a caller can wrap it, style it, or split it into lines without having
// to undo anything first. It is markdown by shape rather than by standard —
// tables are padded to align in a monospaced pager, a panel becomes a marked
// blockquote and a status lozenge becomes bracketed text, none of which have a
// markdown spelling that survives a pager. Prose is not backslash-escaped
// either, because a reader should see the characters the author typed.
//
// A node type this package does not know is rendered rather than dropped: its
// type is shown and its text content is rendered below it. ADF gains node
// types faster than any client follows them, and a reader who cannot see that
// something is there cannot go and look at it in a browser.
func Markdown(d Doc) string { return MarkdownWith(d, Options{}) }

// MarkdownWith renders a document as markdown with explicit options.
func MarkdownWith(d Doc, opt Options) string {
	return string(AppendMarkdown(make([]byte, 0, estimate(d.Content)), d, opt))
}

// AppendMarkdown appends the rendered document to dst and returns the extended
// buffer, so that a caller rendering many documents can reuse one allocation.
func AppendMarkdown(dst []byte, d Doc, opt Options) []byte {
	w := writer{buf: dst, opt: opt, gl: glyphsFor(opt.ASCII)}
	w.blocks(d.Content, false)
	w.endLine()
	return w.buf
}

// estimate sizes the output buffer from the text the document carries, which
// costs one walk and saves the growth copies on anything but a tiny document.
func estimate(nodes []Node) int {
	n := 0
	for i := range nodes {
		n += len(nodes[i].Text) + 8 + estimate(nodes[i].Content)
	}
	return n
}

// spaces is sliced to build an indent, so that indenting a list item does not
// allocate.
const spaces = "                                "

var headingMarks = [7]string{"", "# ", "## ", "### ", "#### ", "##### ", "###### "}

// writer renders into one buffer, tracking the prefix every line of the
// current block carries — "> " inside a quote, two spaces inside a list item —
// so that nesting costs a string concatenation per block rather than a nested
// buffer per block.
type writer struct {
	buf    []byte
	opt    Options
	gl     glyphs
	prefix string // carried by every line of the current block
	marker string // carried by the next line only, in place of prefix

	lineAt   int  // where the current line starts in buf
	open     bool // a line is being written
	written  bool // a line has been written
	blank    bool // a blank line is owed before the next line
	verbatim bool // keep trailing whitespace, for code

	// blankPrefix is the prefix that was in force when the blank line was
	// owed, which is the one it must carry: a blank line between two blocks of
	// a quote belongs to the quote, not to the block that follows it.
	blankPrefix string
}

func (w *writer) separate(on bool) {
	w.blank, w.blankPrefix = on, w.prefix
}

func (w *writer) startLine() {
	if w.open {
		return
	}
	if w.written {
		w.buf = append(w.buf, '\n')
		if w.blank {
			w.buf = append(w.buf, strings.TrimRight(w.blankPrefix, " ")...)
			w.buf = append(w.buf, '\n')
		}
	}
	w.written, w.open, w.blank = true, true, false
	w.lineAt = len(w.buf)
	if w.marker != "" {
		w.buf = append(w.buf, w.marker...)
		w.marker = ""
		return
	}
	w.buf = append(w.buf, w.prefix...)
}

func (w *writer) endLine() {
	if !w.open {
		return
	}
	if !w.verbatim {
		for len(w.buf) > w.lineAt {
			if c := w.buf[len(w.buf)-1]; c != ' ' && c != '\t' {
				break
			}
			w.buf = w.buf[:len(w.buf)-1]
		}
	}
	w.open = false
}

// emit writes text into the current line, starting one if none is open and
// breaking wherever the text does.
func (w *writer) emit(s string) {
	for {
		i := strings.IndexByte(s, '\n')
		if i < 0 {
			break
		}
		w.startLine()
		w.buf = append(w.buf, s[:i]...)
		w.endLine()
		s = s[i+1:]
	}
	w.startLine()
	w.buf = append(w.buf, s...)
}

func (w *writer) line(s string) {
	w.emit(s)
	w.endLine()
}

// push sets the prefix for the next line and for the ones after it, and
// returns the function that restores both. A marker that is still pending when
// the block ends is handed back to the caller, so that an outer list bullet
// whose item rendered nothing is not lost.
func (w *writer) push(first, cont string) func() {
	prefix, marker := w.prefix, w.marker
	base := marker
	if base == "" {
		base = prefix
	}
	pushed := base + first
	w.marker, w.prefix = pushed, prefix+cont
	return func() {
		if w.marker == pushed {
			w.marker = marker
		} else {
			w.marker = ""
		}
		w.prefix = prefix
	}
}

// indentFor returns an indent as wide on screen as the marker it continues.
func indentFor(marker string) string {
	n := ansi.StringWidth(marker)
	if n > len(spaces) {
		n = len(spaces)
	}
	return spaces[:n]
}

func (w *writer) blocks(nodes []Node, tight bool) {
	for i := range nodes {
		if i > 0 {
			w.separate(!tight || !isList(nodes[i].Type))
		}
		w.block(nodes[i])
	}
}

func (w *writer) block(n Node) {
	switch n.Type {
	case "paragraph":
		w.inline(n.Content)
		w.endLine()
	case "heading":
		w.heading(n)
	case "bulletList", "orderedList":
		w.list(n)
	case "taskList", "decisionList":
		w.checklist(n)
	case "blockquote":
		pop := w.push("> ", "> ")
		w.blocks(n.Content, false)
		pop()
	case "codeBlock":
		w.codeBlock(n)
	case "rule":
		w.line("---")
	case "panel":
		w.panel(n)
	case "table":
		w.table(n)
	case "mediaSingle", "mediaGroup":
		w.mediaBlock(n)
	case "expand", "nestedExpand":
		w.expand(n)
	case "layoutSection", "layoutColumn", "caption":
		w.blocks(n.Content, false)
	case "blockCard", "embedCard":
		w.line(w.cardText(n))
	default:
		if isInline(n.Type) {
			w.inlineNode(n)
			w.endLine()
			return
		}
		w.unknownBlock(n)
	}
}

func (w *writer) heading(n Node) {
	level, ok := attrInt(n.Attrs, "level")
	switch {
	case !ok || level < 1:
		level = 1
	case level > 6:
		level = 6
	}
	pop := w.push(headingMarks[level], "")
	w.inline(n.Content)
	w.endLine()
	pop()
}

func (w *writer) list(n Node) {
	ordered := n.Type == "orderedList"
	number := 1
	if ordered {
		if start, ok := attrInt(n.Attrs, "order"); ok && start > 0 {
			number = start
		}
	}
	for i := range n.Content {
		if i > 0 {
			w.separate(false)
		}
		marker := "- "
		if ordered {
			marker = strconv.Itoa(number) + ". "
			number++
		}
		w.item(n.Content[i], marker)
	}
}

func (w *writer) checklist(n Node) {
	for i := range n.Content {
		if i > 0 {
			w.separate(false)
		}
		child := n.Content[i]
		// ADF's content model for these lists is (item | list)+: indenting an
		// action item in the editor stores a sibling list inside its parent, and
		// treating that as an item renders every child as unsupported.
		if child.Type == "taskList" || child.Type == "decisionList" {
			done := w.push("  ", "  ")
			w.checklist(child)
			done()
			continue
		}
		marker := "- " + w.gl.decision + " "
		if child.Type == "taskItem" {
			marker = "- [ ] "
			if state, _ := attrString(child.Attrs, "state"); strings.EqualFold(state, "DONE") {
				marker = "- [x] "
			}
		}
		w.item(child, marker)
	}
}

// item renders one list, task or decision item under its marker, indenting
// everything below the first line to the width of that marker.
func (w *writer) item(n Node, marker string) {
	pop := w.push(marker, indentFor(marker))
	switch {
	case len(n.Content) == 0:
		w.line("")
	case inlineOnly(n.Content):
		w.inline(n.Content)
		w.endLine()
	default:
		w.blocks(n.Content, true)
	}
	if w.marker != "" {
		w.line("")
	}
	pop()
}

func (w *writer) codeBlock(n Node) {
	code := textOf(n.Content)
	fence := "```"
	if n := fenceLen(code); n > 3 {
		fence = strings.Repeat("`", n)
	}
	language, _ := attrString(n.Attrs, "language")
	w.line(fence + sanitize(language))
	w.verbatim = true
	w.line(strings.TrimRight(code, "\n"))
	w.verbatim = false
	w.line(fence)
}

// fenceLen returns a fence long enough to hold code that contains backticks.
func fenceLen(code string) int {
	longest, run := 0, 0
	for i := range len(code) {
		if code[i] != '`' {
			run = 0
			continue
		}
		run++
		if run > longest {
			longest = run
		}
	}
	if longest < 3 {
		return 3
	}
	return longest + 1
}

func (w *writer) panel(n Node) {
	pop := w.push("> ", "> ")
	marker, label := w.panelParts(n.Attrs)
	w.line(marker + " " + label)
	w.blocks(n.Content, false)
	pop()
}

// panelParts picks the marker and label for a panel. panelType is an enum
// rather than anything the site names, but an unknown one is still shown.
func (w *writer) panelParts(a Attrs) (marker, label string) {
	kind, _ := attrString(a, "panelType")
	switch kind {
	case "info":
		return w.gl.info, "INFO"
	case "note":
		return w.gl.note, "NOTE"
	case "tip", "success":
		return w.gl.success, strings.ToUpper(kind)
	case "warning":
		return w.gl.warning, "WARNING"
	case "error":
		return w.gl.failure, "ERROR"
	case "custom":
		if icon, ok := attrString(a, "panelIconText"); ok && icon != "" {
			return sanitize(icon), "PANEL"
		}
		return w.gl.custom, "PANEL"
	case "":
		return w.gl.custom, "PANEL"
	default:
		return w.gl.custom, strings.ToUpper(sanitize(kind))
	}
}

func (w *writer) mediaBlock(n Node) {
	for i := range n.Content {
		if i > 0 {
			w.separate(false)
		}
		if child := n.Content[i]; child.Type == "media" {
			w.line(w.mediaText(child))
			continue
		}
		w.block(n.Content[i])
	}
	if len(n.Content) == 0 {
		w.unknownBlock(n)
	}
}

func (w *writer) expand(n Node) {
	title, _ := attrString(n.Attrs, "title")
	if title != "" {
		title = " " + sanitize(title)
	}
	w.line(w.gl.expand + title)
	pop := w.push("  ", "  ")
	w.blocks(n.Content, false)
	pop()
}

// unknownBlock shows that a node is there, and what it holds, without
// pretending to know how it should look.
func (w *writer) unknownBlock(n Node) {
	pop := w.push("> ", "> ")
	w.line("[unsupported: " + originalType(n) + "]")
	switch {
	case len(n.Content) > 0 && inlineOnly(n.Content):
		w.inline(n.Content)
		w.endLine()
	case len(n.Content) > 0:
		w.blocks(n.Content, false)
	case n.Text != "":
		w.line(sanitize(n.Text))
	default:
		if text, ok := attrString(n.Attrs, "text"); ok && text != "" {
			w.line(sanitize(text))
		}
	}
	pop()
}

// originalType names an unsupportedBlock or unsupportedInline by the node it
// stands in for, which is the only interesting thing about it.
func originalType(n Node) string {
	if n.Type != "unsupportedBlock" && n.Type != "unsupportedInline" {
		return sanitize(n.Type)
	}
	original, ok := n.Attrs["originalValue"].(map[string]any)
	if !ok {
		return sanitize(n.Type)
	}
	if typ, ok := original["type"].(string); ok && typ != "" {
		return sanitize(typ)
	}
	return sanitize(n.Type)
}

func isList(typ string) bool {
	switch typ {
	case "bulletList", "orderedList", "taskList", "decisionList":
		return true
	default:
		return false
	}
}
