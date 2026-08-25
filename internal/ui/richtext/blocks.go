package richtext

import (
	"strconv"
	"strings"

	"charm.land/lipgloss/v2"

	"github.com/varijkapil13/saral/pkg/adf"
)

// blocks renders a run of block nodes, separated by a blank line. tight is for
// the inside of a list item, where a nested list belongs to the item above it
// and a blank line between the two reads as a new list.
func (f *frame) blocks(nodes []adf.Node, tight bool) {
	for i := range nodes {
		if i > 0 && (!tight || !isList(nodes[i].Type)) {
			f.separate()
		}
		f.block(nodes[i])
	}
}

func (f *frame) block(n adf.Node) {
	switch n.Type {
	case "paragraph":
		f.paragraph(n)
	case "heading":
		f.heading(n)
	case "bulletList", "orderedList":
		f.list(n)
	case "taskList", "decisionList":
		f.checklist(n)
	case "blockquote":
		f.quote(n)
	case "codeBlock":
		f.codeBlock(n)
	case "rule":
		f.rule()
	case "panel":
		f.panel(n)
	case "table":
		f.table(n)
	case "mediaSingle", "mediaGroup":
		f.mediaBlock(n)
	case "expand", "nestedExpand":
		f.expand(n)
	case "blockCard", "embedCard":
		f.cardBlock(n)
	// A layout is two columns in a browser and one after another in a terminal:
	// eighty cells split in two holds no prose worth reading.
	case "layoutSection", "layoutColumn", "caption", "doc":
		f.blocks(n.Content, false)
	default:
		if isInline(n.Type) {
			f.nodeBlock(n, alignLeft)
			return
		}
		f.unknownBlock(n)
	}
}

// inlineBlock wraps a run of inline nodes into lines.
func (f *frame) inlineBlock(nodes []adf.Node, a align) {
	f.spans = f.spans[:0]
	f.inline(nodes)
	f.fill(a)
}

// nodeBlock is the same for one inline node standing where a block should be.
func (f *frame) nodeBlock(n adf.Node, a align) {
	f.spans = f.spans[:0]
	f.inlineNode(n)
	f.fill(a)
}

// textBlock is the same for text of one style: a label, a placeholder, a title.
func (f *frame) textBlock(text string, p paint, a align) {
	f.spans = f.spans[:0]
	f.addPaint(text, p)
	f.fill(a)
}

// cardBlock is the same for a smart link standing on a line of its own.
func (f *frame) cardBlock(n adf.Node) {
	f.spans = f.spans[:0]
	f.cardSpans(n)
	f.fill(alignLeft)
}

func (f *frame) paragraph(n adf.Node) {
	a, level := blockMarks(n.Marks)
	if level > 0 {
		pad := indent(level * 2)
		f.push(pad, pad, &f.sty.Body)
		defer f.pop()
	}
	f.inlineBlock(n.Content, a)
}

// heading renders a heading at the weight its level earns.
func (f *frame) heading(n adf.Node) {
	level, ok := attrInt(n.Attrs, "level")
	switch {
	case !ok || level < 1:
		level = 1
	case level > 6:
		level = 6
	}
	style := &f.sty.H3
	switch level {
	case 1:
		style = &f.sty.H1
	case 2:
		style = &f.sty.H2
	}
	// Levels one to three are told apart by weight, and level one keeps an
	// attribute rather than a colour so that it survives the no-colour theme.
	// Below three there is no weight left to spend, so the indent says it.
	if level > 3 {
		pad := indent((level - 3) * 2)
		f.push(pad, pad, &f.sty.Body)
		defer f.pop()
	}
	a, _ := blockMarks(n.Marks)
	ctx, p := f.setCtx(style)
	f.inlineBlock(n.Content, a)
	f.restoreCtx(ctx, p)
}

func (f *frame) list(n adf.Node) {
	ordered := n.Type == "orderedList"
	number := 1
	if ordered {
		if start, ok := attrInt(n.Attrs, "order"); ok && start > 0 {
			number = start
		}
	}
	bullet := f.mk.Bullet + " "
	for i := range n.Content {
		child := n.Content[i]
		if child.Type != "listItem" {
			f.block(child)
			continue
		}
		marker, style := bullet, &f.sty.Bullet
		if ordered {
			marker, style = strconv.Itoa(number)+". ", &f.sty.Number
			number++
		}
		f.item(child, marker, style)
	}
}

// checklist renders a task or decision list. ADF's content model for these is
// (item | list)+: indenting an action item in the editor stores a sibling list
// inside its parent, and treating that as an item renders every child as
// unsupported.
func (f *frame) checklist(n adf.Node) {
	for i := range n.Content {
		child := n.Content[i]
		if child.Type == "taskList" || child.Type == "decisionList" {
			f.push("  ", "  ", &f.sty.Body)
			f.checklist(child)
			f.pop()
			continue
		}
		marker, style := f.mk.Decision+" ", &f.sty.Decision
		if child.Type == "taskItem" {
			marker, style = f.mk.Task+" ", &f.sty.Task
			if state, _ := attrString(child.Attrs, "state"); strings.EqualFold(state, "DONE") {
				marker, style = f.mk.TaskDone+" ", &f.sty.TaskDone
			}
		}
		f.item(child, marker, style)
	}
}

// item renders one list, task or decision item under its marker, indenting
// everything below the first line to the width of that marker.
func (f *frame) item(n adf.Node, marker string, style *lipgloss.Style) {
	f.push(marker, indentFor(marker), style)
	switch {
	case len(n.Content) == 0:
		f.markerOnly()
	case inlineOnly(n.Content):
		f.inlineBlock(n.Content, alignLeft)
	default:
		f.blocks(n.Content, true)
	}
	// An item whose content rendered nothing would otherwise take its marker
	// with it, and a list with a row missing is a list read wrong.
	if !f.rails[len(f.rails)-1].used {
		f.markerOnly()
	}
	f.pop()
}

func (f *frame) quote(n adf.Node) {
	f.push(f.mk.VLine+" ", f.mk.VLine+" ", &f.sty.QuoteBar)
	ctx, p := f.setCtx(&f.sty.Quote)
	f.blocks(n.Content, false)
	f.restoreCtx(ctx, p)
	f.pop()
}

// panel renders one of the five stock panels or a custom one. A panel is not a
// blockquote: what the document said about a warning is lost the moment it
// renders as the bar an ordinary quote gets, so each kind keeps its own marker,
// its own label and its own style — and a custom panel keeps the colour the
// site chose.
func (f *frame) panel(n adf.Node) {
	kind, _ := attrString(n.Attrs, "panelType")
	style, marker, label := f.sty.panelStyle(kind, *f.mk)
	// The icon the author picked is used only where the glyph set says the
	// terminal can be trusted with a glyph nobody chose: ASCII mode exists
	// because it cannot, and a marker measured wrong takes the gutter with it.
	if icon, ok := attrString(n.Attrs, "panelIconText"); ok && icon != "" && !f.mk.ascii() {
		if clean := sanitize(strings.TrimSpace(icon)); clean != "" {
			marker = clean
		}
	}
	p := f.paint(style)
	if f.sty.Color {
		if colour, ok := attrString(n.Attrs, "panelColor"); ok {
			if c, ok := wireColor(colour); ok {
				p = paintOf(style.Foreground(c))
			}
		}
	}
	f.pushPaint(f.mk.VLine+" ", f.mk.VLine+" ", p)
	f.textBlock(marker+" "+label, p, alignLeft)
	f.blocks(n.Content, false)
	f.pop()
}

// codeBlock renders code as it was written. It is neither wrapped nor
// truncated: wrapping corrupts what a reader is going to copy, and truncating
// loses it without saying so. A line wider than the pane is left wide, its
// width recorded, and panning it is the pane's job.
func (f *frame) codeBlock(n adf.Node) {
	f.push(f.mk.VLine+" ", f.mk.VLine+" ", &f.sty.CodeBar)
	if language, ok := attrString(n.Attrs, "language"); ok && language != "" {
		f.line(sanitize(language), &f.sty.CodeLang)
	}
	code := strings.TrimRight(rawText(n.Content), "\n")
	for {
		line, rest, more := strings.Cut(code, "\n")
		f.line(expandTabs(sanitize(line), 0), &f.sty.CodeBlock)
		if !more {
			break
		}
		code = rest
	}
	f.pop()
}

func (f *frame) rule() {
	f.line(strings.Repeat(f.mk.HLine, f.avail()), &f.sty.Rule)
}

// expand renders a fold. It is closed unless the reader has opened it, and the
// key it is opened by is its position in document order: a localId is optional
// on the wire, is not unique when it is there, and says nothing about a
// document that was never edited in the browser. Document order is derivable
// from the document alone, which is what a pane holding a set of open folds
// needs it to be.
func (f *frame) expand(n adf.Node) {
	index := *f.nextFold
	*f.nextFold++
	open := f.opt.Open[index]

	title, _ := attrString(n.Attrs, "title")
	title = sanitize(strings.TrimSpace(title))
	if title == "" {
		title = "details"
	}
	marker := f.mk.Folded
	if open {
		marker = f.mk.Unfolded
	}
	f.settle()
	at := len(f.lines)
	f.push(marker+" ", indentFor(marker+" "), &f.sty.FoldMark)
	f.textBlock(title, f.paint(&f.sty.FoldTitle), alignLeft)
	f.pop()
	if f.unplaced {
		at = -1
	}
	*f.folds = append(*f.folds, Fold{Index: index, Line: at, Open: open, Title: title})

	if !open {
		*f.nextFold += countFolds(n.Content)
		return
	}
	f.push("  ", "  ", &f.sty.Body)
	f.blocks(n.Content, false)
	f.pop()
}

// mediaBlock renders an attachment or an image as a placeholder line that keeps
// the id or the URL: a placeholder naming nothing is a dead end for whatever
// comes to resolve it, and a reader cannot go and look at it either.
func (f *frame) mediaBlock(n adf.Node) {
	if len(n.Content) == 0 {
		f.unknownBlock(n)
		return
	}
	for i := range n.Content {
		if i > 0 {
			f.separate()
		}
		child := n.Content[i]
		if child.Type == "media" || child.Type == "mediaInline" {
			media := f.paint(&f.sty.Media)
			f.pushPaint(f.mk.Media+" ", indentFor(f.mk.Media+" "), media)
			f.textBlock(mediaTarget(child), media, alignLeft)
			f.pop()
			continue
		}
		f.block(child)
	}
}

// unknownBlock quarantines a node this build has never heard of behind its own
// bar, so that whatever it holds cannot be read as prose somebody wrote.
func (f *frame) unknownBlock(n adf.Node) {
	f.push(f.mk.VLine+" ", f.mk.VLine+" ", &f.sty.Unknown)
	f.line(f.mk.Unknown+" unsupported: "+nodeName(n), &f.sty.Unknown)
	switch {
	case inlineOnly(n.Content):
		f.inlineBlock(n.Content, alignLeft)
	case len(n.Content) > 0:
		f.blocks(n.Content, false)
	case n.Text != "":
		f.textBlock(n.Text, f.pctx, alignLeft)
	default:
		if text, ok := attrString(n.Attrs, "text"); ok && text != "" {
			f.textBlock(text, f.pctx, alignLeft)
		}
	}
	f.pop()
}
