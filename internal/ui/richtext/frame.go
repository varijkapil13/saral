package richtext

import (
	"strings"

	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"
)

const (
	// minAvail is the narrowest column prose is wrapped into. A deeply nested
	// list in a split pane can otherwise ask for a negative width.
	minAvail = 8

	// tabWidth is what a tab is expanded to before anything is measured.
	// ansi.StringWidth counts a tab as one cell while a terminal jumps to the
	// next stop, so a line holding one would be measured wrong and a pane would
	// pan past the end of it.
	tabWidth = 4
)

// span is a run of text carrying one style. A line is a concatenation of spans
// painted independently, which is what makes it self-contained: see the package
// comment for what wrapping styled text does instead.
type span struct {
	text string
	p    paint
	w    int  // the width, where it is already known: a rendered table cell
	brk  bool // a hard break: end the line here
}

// paint is the pair of sequences a style puts either side of a run, read from
// the style once instead of per run.
//
// lipgloss re-styles every rune of an underlined or struck-through run, which is
// one escape pair per letter and an escape sequence inside any grapheme cluster
// the run holds — an emoji built from joiners comes apart. Asking a style what
// it puts around one rune leaves lipgloss owning the sequences and hands the
// terminal one pair.
type paint struct {
	open, close string
	pad         int // cells the style adds of its own, if a caller's token pads
}

// painted is one style's sequences, remembered by where the style lives. The
// tokens all sit in one Styles for the whole of a render, so their address is a
// key, and asking a style for its sequences costs a render of its own.
type painted struct {
	at *lipgloss.Style
	p  paint
}

// markPainted is the same for a run of marked text, which is the one style
// nothing could have built in advance. A document repeats its marks — every
// bold run in it asks for the same style — so the key is the marks and the
// block style they were applied over.
type markPainted struct {
	m   marks
	ctx string
	p   paint
}

// paintOf reads a style's sequences off a probe it cannot mistake for content.
func paintOf(st lipgloss.Style) paint {
	const probe = "\x01"
	out := st.Render(probe)
	before, after, ok := strings.Cut(out, probe)
	if !ok {
		return paint{}
	}
	return paint{open: before, close: after, pad: ansi.StringWidth(out)}
}

func (p paint) put(dst []byte, text string) []byte {
	dst = append(dst, p.open...)
	dst = append(dst, text...)
	return append(dst, p.close...)
}

// piece is the range of one span that landed on the line being built.
type piece struct {
	src      int
	from, to int
	w        int
}

// rail is one level of the gutter: a quote's bar, a list item's marker and the
// indent under it, a panel's edge.
type rail struct {
	first, cont   string
	firstW, contW int
	p             paint
	used          bool
}

// align is what an alignment mark asks of a paragraph.
type align int

const (
	alignLeft align = iota
	alignCenter
	alignRight
)

// frame accumulates the lines of one rendering target. The document has one;
// every table cell gets another, because a cell is laid out in its own column
// and then padded into the grid.
type frame struct {
	opt *Options
	sty *Styles
	mk  *Markers

	width  int
	floor  int // the narrowest the content of a line may be squeezed to
	lines  []string
	widths []int

	folds    *[]Fold
	nextFold *int

	// unplaced marks a frame whose lines are not the document's own — a table
	// cell — so that a fold inside it is given its row's line instead.
	unplaced bool

	rails []rail
	ctx   lipgloss.Style // the style prose takes in the block being rendered
	pctx  paint
	pcont paint

	memo     *[]painted
	marked   *[]markPainted
	spans    []span
	rowSpans []span
	pieces   []piece
	pend     []piece
	word     []piece
	cont     int // index of the continuation-marker span, -1 until one is needed
	tmp      [4]span

	// raw is the line under construction. strings.Builder is not used here
	// because its Reset drops the buffer, so reusing one costs an allocation and
	// a regrow per line.
	raw []byte

	// owed holds a separating blank line that is only written if something
	// follows it, so that a block rendering nothing does not leave a gap.
	owed  string
	owedW int
	owe   bool

	// dry is a frame being rendered only to be measured — a table column asking
	// how wide its content wants to be — which needs the widths and not the
	// lines.
	dry bool
}

// paint is the sequences a token puts around a run, taken from the token once
// per render however many runs it paints.
func (f *frame) paint(at *lipgloss.Style) paint {
	for i := range *f.memo {
		if (*f.memo)[i].at == at {
			return (*f.memo)[i].p
		}
	}
	p := paintOf(*at)
	*f.memo = append(*f.memo, painted{at: at, p: p})
	return p
}

// markPaint is the sequences a run of marked text takes over the style of the
// block it sits in.
func (f *frame) markPaint(m marks) paint {
	for i := range *f.marked {
		if (*f.marked)[i].m == m && (*f.marked)[i].ctx == f.pctx.open {
			return (*f.marked)[i].p
		}
	}
	p := paintOf(f.sty.apply(f.ctx, m))
	*f.marked = append(*f.marked, markPainted{m: m, ctx: f.pctx.open, p: p})
	return p
}

func (f *frame) sub(width int) *frame {
	return &frame{
		opt: f.opt, sty: f.sty, mk: f.mk, memo: f.memo, marked: f.marked,
		width: width, floor: f.floor, folds: f.folds, nextFold: f.nextFold,
		ctx: f.ctx, pctx: f.pctx, pcont: f.pcont,
	}
}

// push adds a gutter level. first is what the next line carries and cont what
// the ones after it do; they are padded to the same width so that the room
// available to the content does not depend on which line it is.
func (f *frame) push(first, cont string, style *lipgloss.Style) {
	f.pushPaint(first, cont, f.paint(style))
}

func (f *frame) pushPaint(first, cont string, p paint) {
	fw, cw := ansi.StringWidth(first), ansi.StringWidth(cont)
	for cw < fw {
		cont += " "
		cw++
	}
	for fw < cw {
		first += " "
		fw++
	}
	f.rails = append(f.rails, rail{first: first, cont: cont, firstW: fw, contW: cw, p: p})
}

func (f *frame) pop() { f.rails = f.rails[:len(f.rails)-1] }

func (f *frame) gutter() int {
	n := 0
	for i := range f.rails {
		n += f.rails[i].contW
	}
	return n
}

// avail is how many cells the content of a line has.
func (f *frame) avail() int {
	return max(f.width-f.gutter(), f.floor)
}

// railText is what rail i puts on the line being written.
func (f *frame) railText(i int, advance bool) (text string, width int) {
	r := &f.rails[i]
	if r.used {
		return r.cont, r.contW
	}
	if advance {
		r.used = true
	}
	return r.first, r.firstW
}

// writeGutter puts the current gutter on the line being built and reports its
// width. trim drops the trailing whitespace of a line with nothing after the
// gutter, so that a blank line inside a quote is the bar and nothing else.
func (f *frame) writeGutter(trim, advance bool) int {
	last := len(f.rails)
	if trim {
		for last > 0 {
			text, _ := f.railText(last-1, false)
			if strings.TrimRight(text, " ") != "" {
				break
			}
			last--
		}
	}
	width := 0
	for i := range f.rails {
		text, w := f.railText(i, advance)
		if i >= last {
			continue
		}
		if trim && i == last-1 {
			cut := strings.TrimRight(text, " ")
			w -= len(text) - len(cut)
			text = cut
		}
		if text == "" {
			continue
		}
		f.raw = f.rails[i].p.put(f.raw, text)
		width += w
	}
	return width
}

// keep appends the line under construction, writing out any separating blank
// line that was waiting to see whether anything followed it. A dry frame keeps
// only the width, which is all it was built to answer.
func (f *frame) keep(width int) {
	f.settle()
	text := ""
	if !f.dry {
		text = string(f.raw)
	}
	f.lines = append(f.lines, text)
	f.widths = append(f.widths, width)
}

// settle writes a blank line that is owed, for a block that is about to record
// which line it started on and cannot have that line move under it.
func (f *frame) settle() {
	if !f.owe {
		return
	}
	f.owe = false
	f.lines = append(f.lines, f.owed)
	f.widths = append(f.widths, f.owedW)
}

// separate owes a blank line between two blocks. The gutter it carries is the
// one in force now — the blank between two of a panel's paragraphs belongs to
// the panel — and it never consumes a marker, which belongs to a line with
// something on it.
func (f *frame) separate() {
	if len(f.lines) == 0 && !f.owe {
		return
	}
	f.raw = f.raw[:0]
	f.owedW = f.writeGutter(true, false)
	f.owed, f.owe = string(f.raw), true
}

// markerOnly emits a line that is only the gutter, for an item whose content
// rendered nothing: a list with a row missing is a list read wrong.
func (f *frame) markerOnly() {
	f.raw = f.raw[:0]
	f.keep(f.writeGutter(true, true))
}

// out emits one line from the spans given, exactly as they are. A code line, a
// grid row and a placeholder are all lines whose width is what it is; wrapping
// them would be a lie about the document.
func (f *frame) out(spans []span) {
	empty := true
	for i := range spans {
		if spans[i].text != "" {
			empty = false
			break
		}
	}
	f.raw = f.raw[:0]
	width := f.writeGutter(empty, true)
	for i := range spans {
		s := &spans[i]
		if s.text == "" {
			continue
		}
		f.raw = s.p.put(f.raw, s.text)
		width += s.p.pad
		if s.w > 0 {
			width += s.w
			continue
		}
		width += ansi.StringWidth(s.text)
	}
	f.keep(width)
}

// line emits one unwrapped line of a single style.
func (f *frame) line(text string, style *lipgloss.Style) {
	f.tmp[0] = span{text: text, p: f.paint(style)}
	f.out(f.tmp[:1])
}

// setCtx changes the style prose takes in the block being rendered and hands
// back what it was, for the caller to put back. A closure would be tidier and
// would cost an allocation per block: it captures a whole lipgloss.Style.
func (f *frame) setCtx(style *lipgloss.Style) (lipgloss.Style, paint) {
	ctx, pctx := f.ctx, f.pctx
	f.ctx, f.pctx = *style, f.paint(style)
	return ctx, pctx
}

func (f *frame) restoreCtx(ctx lipgloss.Style, p paint) { f.ctx, f.pctx = ctx, p }

// emit flushes the line under construction.
func (f *frame) emit(a align, lineW int) {
	f.raw = f.raw[:0]
	width := f.writeGutter(len(f.pieces) == 0, true)
	if len(f.pieces) > 0 {
		if lead := f.lead(a, lineW); lead > 0 {
			for range lead {
				f.raw = append(f.raw, ' ')
			}
			width += lead
		}
	}
	for _, p := range f.pieces {
		s := &f.spans[p.src]
		f.raw = s.p.put(f.raw, s.text[p.from:p.to])
		width += p.w + s.p.pad
	}
	f.keep(width)
	f.pieces = f.pieces[:0]
	f.pend = f.pend[:0]
}

// lead is how far a centred or right-aligned line is pushed across. Only the
// leading spaces are written: the pane owns the padding on the other side,
// along with the gutter and the mouse zone.
func (f *frame) lead(a align, lineW int) int {
	room := f.avail() - lineW
	if room <= 0 {
		return 0
	}
	switch a {
	case alignCenter:
		return room / 2
	case alignRight:
		return room
	default:
		return 0
	}
}

// fill wraps the spans collected in f.spans into lines. Words are placed whole
// where they fit; a word longer than the whole column is broken, and the break
// is marked, because a reader cannot otherwise tell a break in the layout from
// one in the text.
//
// A word is a run of non-space text and may cross a span boundary: the sub
// mark in H₂O and a comma after a code span are each their own span, and
// breaking a line between two spans that no space separates puts a break where
// the document has none.
func (f *frame) fill(a align) {
	avail, lineW, at := f.avail(), 0, len(f.lines)
	f.pieces, f.pend, f.word, f.cont = f.pieces[:0], f.pend[:0], f.word[:0], -1

	for i := range len(f.spans) {
		if f.spans[i].brk {
			lineW = f.place(a, lineW, avail)
			f.emit(a, lineW)
			lineW = 0
			continue
		}
		text := f.spans[i].text
		for pos := 0; pos < len(text); {
			if text[pos] == ' ' {
				lineW = f.place(a, lineW, avail)
				end := pos
				for end < len(text) && text[end] == ' ' {
					end++
				}
				if lineW > 0 {
					f.pend = append(f.pend, piece{src: i, from: pos, to: end, w: end - pos})
				}
				pos = end
				continue
			}
			end := pos
			for end < len(text) && text[end] != ' ' {
				end++
			}
			f.word = append(f.word, piece{src: i, from: pos, to: end, w: ansi.StringWidth(text[pos:end])})
			pos = end
		}
	}
	lineW = f.place(a, lineW, avail)
	if len(f.pieces) > 0 || len(f.lines) == at {
		f.emit(a, lineW)
	}
}

// place puts the word collected so far on the line, breaking first if it does
// not fit and splitting it if it cannot fit on a line of its own.
func (f *frame) place(a align, lineW, avail int) int {
	if len(f.word) == 0 {
		return lineW
	}
	total, pendW := 0, 0
	for _, p := range f.word {
		total += p.w
	}
	for _, p := range f.pend {
		pendW += p.w
	}
	if lineW > 0 && lineW+pendW+total > avail {
		f.emit(a, lineW)
		lineW, pendW = 0, 0
	}
	if lineW == 0 && total > avail {
		lineW = f.split(a, avail)
		f.word = f.word[:0]
		return lineW
	}
	if lineW > 0 {
		for _, p := range f.pend {
			f.appendPiece(p)
		}
		lineW += pendW
	}
	f.pend = f.pend[:0]
	for _, p := range f.word {
		f.appendPiece(p)
	}
	f.word = f.word[:0]
	return lineW + total
}

// split breaks one word that is wider than the column. The pieces are cut at
// grapheme boundaries, so a CJK cell or an emoji is never halved.
func (f *frame) split(a align, avail int) int {
	marked := true
	for _, p := range f.word {
		if hasWide(f.spans[p.src].text[p.from:p.to]) {
			marked = false
			break
		}
	}
	lineW, broken := 0, false
	for _, p := range f.word {
		for at := p.from; at < p.to; {
			if lineW == 0 && broken && marked {
				lineW = f.appendCont()
			}
			room := avail - lineW
			if room < 1 {
				f.emit(a, lineW)
				lineW = 0
				continue
			}
			rest := f.spans[p.src].text[at:p.to]
			if w := ansi.StringWidth(rest); w <= room {
				f.appendPiece(piece{src: p.src, from: at, to: p.to, w: w})
				lineW += w
				break
			}
			head, _ := splitWidth(rest, room)
			headW := ansi.StringWidth(head)
			f.appendPiece(piece{src: p.src, from: at, to: at + len(head), w: headW})
			f.emit(a, lineW+headW)
			lineW, broken = 0, true
			at += len(head)
		}
	}
	return lineW
}

// appendCont heads a continuation line with the marker that says the renderer
// broke the word, not the author.
func (f *frame) appendCont() int {
	if f.cont < 0 {
		f.spans = append(f.spans, span{text: f.mk.Cont + " ", p: f.pcont})
		f.cont = len(f.spans) - 1
	}
	text := f.spans[f.cont].text
	w := ansi.StringWidth(text)
	f.pieces = append(f.pieces, piece{src: f.cont, from: 0, to: len(text), w: w})
	return w
}

// appendPiece extends the piece under construction when the new range carries
// straight on from it, so that a run of words in one style costs one Render
// call per line rather than one per word.
func (f *frame) appendPiece(p piece) {
	if n := len(f.pieces); n > 0 {
		last := &f.pieces[n-1]
		if last.src == p.src && last.to == p.from {
			last.to, last.w = p.to, last.w+p.w
			return
		}
	}
	f.pieces = append(f.pieces, p)
}

// splitWidth cuts a string at the last grapheme boundary that fits in n cells,
// returning the two halves. ansi does the cutting: a cluster wider than the
// room left would otherwise come back empty and the caller would not advance.
func splitWidth(s string, n int) (head, tail string) {
	for room := n; room <= n+4; room++ {
		if head = ansi.Truncate(s, room, ""); head != "" {
			return head, ansi.TruncateLeft(s, room, "")
		}
	}
	return s, ""
}

// hasWide reports whether a run holds anything wider than one cell, which is
// how a run with no break opportunity in it is told from CJK. Breaking CJK
// between two ideographs is what a terminal is supposed to do, and marking
// every one of those lines as a broken word would be noise.
func hasWide(s string) bool {
	for _, r := range s {
		if ansi.StringWidth(string(r)) > 1 {
			return true
		}
	}
	return false
}

// expandTabs replaces tabs with the spaces to the next stop, which is what a
// terminal does and what ansi.StringWidth does not.
func expandTabs(s string, col int) string {
	if !strings.ContainsRune(s, '\t') {
		return s
	}
	var b strings.Builder
	b.Grow(len(s) + tabWidth)
	for _, r := range s {
		if r != '\t' {
			b.WriteRune(r)
			col += ansi.StringWidth(string(r))
			continue
		}
		pad := tabWidth - col%tabWidth
		for range pad {
			b.WriteByte(' ')
		}
		col += pad
	}
	return b.String()
}

// sanitize drops the control characters a terminal would act on. Issue text is
// written by anyone with a Jira login, and an escape sequence in a description
// must not be able to repaint the screen it is displayed in.
func sanitize(s string) string {
	if !hasControl(s) {
		return s
	}
	var b strings.Builder
	b.Grow(len(s))
	for _, r := range s {
		if isControl(r) {
			continue
		}
		b.WriteRune(r)
	}
	return b.String()
}

func hasControl(s string) bool {
	for _, r := range s {
		if isControl(r) {
			return true
		}
	}
	return false
}

func isControl(r rune) bool {
	switch {
	case r == '\t':
		return false
	case r < 0x20 || r == 0x7f:
		return true
	case r >= 0x80 && r <= 0x9f:
		return true
	default:
		return false
	}
}
