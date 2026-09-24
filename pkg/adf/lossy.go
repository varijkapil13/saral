package adf

import "strings"

// ParseMarkdownDropsOnly names every construct that ADF → markdown → ADF cannot
// reproduce without the original document, one "construct: what is lost" line
// each. It is here so that the list is maintained beside the code that causes
// it; [LossyConstructs] is the one to show a user, because it names only what
// a given document holds and only what an edit costs once
// [ParseMarkdownInto] has restored everything the author left alone.
//
// Nothing in this list is dropped as text: a lozenge still reads "[Done]" and a
// date still reads as its day, they are simply prose again.
func ParseMarkdownDropsOnly() []string {
	return []string{
		"mention: the account id of a mention stored without one, which becomes its display text; one with an id keeps it and nothing else",
		"status: the lozenge colour, so a lozenge becomes bracketed text",
		"date: the instant, which renders as a day in one timezone and cannot be read back as an epoch",
		"emoji: the shortName and id behind the character",
		"media: the collection, dimensions and layout of an attachment",
		"inlineCard: a smart link, which becomes a plain link; blockCard and embedCard too",
		"placeholder: the placeholder, which becomes the text it shows",
		"layoutSection: the columns, whose blocks are laid out one after another",
		"table: colspan, rowspan, cell background, layout and the number column; a cell's blocks are folded to one line",
		"table: every cell of a table rendered with a TableWidth, which is truncated to fit the width",
		"panel: the case of a panelType this package does not know, which renders uppercased",
		"blockquote: a quote whose first line reads like a panel's marker and label, which becomes a panel",
		"heading: a heading with no content, which renders as nothing at all",
		"hardBreak: one that opens or ends a block, follows another, or sits in a heading or a table cell",
		"text: trailing whitespace on a line, a line break inside a text node, and the control characters the renderer strips",
		"marks: every mark but strong, em, strike, underline, code and a link's href — textColor, subsup and the rest",
		"marks: the order marks arrived in, which is meaningless but byte-significant",
		"marks: runs of emphasis that touch their neighbours so that no reading of the markers gives them back",
		"any node type ADF gained after this package was written, which renders as an [unsupported: …] marker",
	}
}

// Loss is one construct in a document that editing the block holding it
// cannot carry through markdown.
type Loss struct {
	// Construct is the node or mark type the loss is about, or "text" for
	// something about prose itself.
	Construct string
	// Cost says what an edited block holding the construct loses.
	Cost string
}

func (l Loss) String() string { return l.Construct + ": " + l.Cost }

// LossyConstructs names, in document order, the constructs d holds that an
// edit through markdown will lose — narrowed to this document, and to what is
// still lost once [ParseMarkdownInto] has restored every block, list item and
// table cell the author left alone. A document it answers nothing for can be
// edited as markdown without losing anything markdown did not show.
//
// opt must be the options the markdown will be rendered with.
func LossyConstructs(d Doc, opt Options) []Loss {
	s := survey{p: newParser(opt), opt: opt, seen: map[Loss]bool{}}
	s.nodes(d.Content, "doc")
	return s.out
}

type survey struct {
	p    *parser
	opt  Options
	seen map[Loss]bool
	out  []Loss
	cell int // how many table cells the walk is inside
}

func (s *survey) add(construct, cost string) {
	l := Loss{Construct: construct, Cost: cost}
	if !s.seen[l] {
		s.seen[l] = true
		s.out = append(s.out, l)
	}
}

func (s *survey) nodes(nodes []Node, parent string) {
	for i := range nodes {
		s.node(nodes, i, parent)
	}
}

func (s *survey) node(siblings []Node, i int, parent string) {
	n := siblings[i]
	switch n.Type {
	case "status":
		s.add("status", "the lozenge colour, which comes back as bracketed text")
	case "date":
		s.add("date", "the instant, which comes back as the day it reads as")
	case "emoji":
		s.add("emoji", "the shortName and id behind the character")
	case "mediaInline":
		s.add("media", "the collection and size of an attachment inside an edited paragraph")
	case "mention":
		if id, _ := attrString(n.Attrs, "id"); strings.TrimSpace(id) == "" {
			s.add("mention", "one stored without an account id, which comes back as its display text")
		}
	case "inlineCard", "blockCard", "embedCard":
		s.add(n.Type, "the smart link, which comes back as a plain link")
	case "placeholder":
		s.add("placeholder", "the placeholder, which comes back as the text it shows")
	case "layoutSection":
		s.add("layoutSection", "the columns, whose blocks come back one after another")
	case "table":
		s.table(n)
	case "panel":
		if kind, _ := attrString(n.Attrs, "panelType"); !isLabel(strings.ToUpper(sanitize(kind))) && kind != "" {
			s.add("panel", "a panelType markdown cannot spell, which comes back as a quote")
		}
	case "blockquote":
		s.quote(n)
	case "heading":
		if len(n.Content) == 0 {
			s.add("heading", "one with nothing in it, which renders as nothing")
		}
	case "hardBreak":
		if s.cell > 0 {
			s.add("hardBreak", "one inside a table cell, which comes back as a space")
		}
		if parent == "heading" || i == 0 || i == len(siblings)-1 || siblings[i+1].Type == "hardBreak" {
			s.add("hardBreak", "one that opens or ends a block, follows another, or sits in a heading")
		}
	case "text":
		s.text(siblings, i)
	case "unsupportedBlock", "unsupportedInline":
		s.unknown(n)
	default:
		if !known(n.Type) {
			s.unknown(n)
		}
	}
	if emphasised(n) && !s.settles(n) {
		s.add("marks", "runs of emphasis that touch their neighbours, which come back as the characters they are drawn with")
	}
	if isCell(n.Type) {
		s.cell++
		defer func() { s.cell-- }()
	}
	s.nodes(n.Content, n.Type)
}

func (s *survey) unknown(n Node) {
	s.add(originalType(n), "a node type this client does not know, so a block holding it cannot be edited here")
}

func (s *survey) table(n Node) {
	if s.opt.TableWidth > 0 {
		s.add("table", "every cell, which is truncated to fit the width the table was drawn at")
	}
	for r := range n.Content {
		for c := range n.Content[r].Content {
			cell := &n.Content[r].Content[c]
			if len(cell.Content) > 1 || (len(cell.Content) == 1 && cell.Content[0].Type != "paragraph") {
				s.add("table", "a cell holding more than one paragraph, which an edit to it folds onto one line")
			}
			if rows, _ := attrInt(cell.Attrs, "rowspan"); rows > 1 {
				s.add("table", "a cell spanning rows, which markdown draws as a ragged row")
			}
		}
	}
}

func (s *survey) quote(n Node) {
	if len(n.Content) == 0 || n.Content[0].Type != "paragraph" {
		return
	}
	md := string(AppendMarkdown(nil, NewDoc(n.Content[0]), s.opt))
	first, _, _ := strings.Cut(md, "\n")
	if _, ok := s.p.panelAttrs(first); ok {
		s.add("blockquote", "one whose first line reads like a panel's marker and label, which comes back as a panel")
	}
}

func (s *survey) text(siblings []Node, i int) {
	n := siblings[i]
	if hasControl(n.Text) {
		s.add("text", "control characters, which are stripped")
	}
	if strings.IndexByte(n.Text, '\n') >= 0 {
		s.add("text", "a line break inside a text node, which comes back as a hard break")
	}
	last := i == len(siblings)-1 || siblings[i+1].Type == "hardBreak"
	if last && strings.TrimRight(n.Text, " \t") != n.Text && !hasMark(n.Marks, "code") {
		s.add("text", "whitespace at the end of a line, which is trimmed")
	}
	for _, m := range n.Marks {
		switch m.Type {
		case "strong", "em", "strike", "underline", "code", "link":
		default:
			s.add(m.Type, "the mark, which markdown has no spelling for")
		}
	}
}

// settles tries the round trip, because whether emphasis survives depends on
// its neighbours rather than its type.
func (s *survey) settles(n Node) bool {
	d := NewDoc(n)
	switch n.Type {
	case "taskItem":
		d = NewDoc(NewNode("taskList", n))
	case "decisionItem":
		d = NewDoc(NewNode("decisionList", n))
	}
	md := MarkdownWith(d, s.opt)
	out, err := ParseMarkdownWith(md, s.opt)
	return err != nil || MarkdownWith(out, s.opt) == md
}

func emphasised(n Node) bool {
	switch n.Type {
	case "paragraph", "heading", "taskItem", "decisionItem":
	default:
		return false
	}
	for i := range n.Content {
		for _, m := range n.Content[i].Marks {
			switch m.Type {
			case "strong", "em", "strike", "underline":
				return true
			}
		}
	}
	return false
}

func known(typ string) bool {
	switch typ {
	case "paragraph", "heading", "bulletList", "orderedList", "taskList", "decisionList", "listItem",
		"taskItem", "decisionItem", "blockquote", "codeBlock", "rule", "panel", "table", "tableRow",
		"tableCell", "tableHeader", "mediaSingle", "mediaGroup", "media", "caption", "expand",
		"nestedExpand", "layoutColumn", "hardBreak":
		return true
	}
	return isInline(typ) && typ != "inlineExtension"
}
