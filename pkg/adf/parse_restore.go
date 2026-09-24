package adf

import (
	"maps"
	"strings"
)

// restore pairs the blocks parsed from an edited span with the originals that
// span replaced, in order and by type, and merges each pair.
func (p *parser) restore(fresh, gap []Node) []Node {
	next := 0
	for i := range fresh {
		for k := next; k < len(gap); k++ {
			if compatible(fresh[i].Type, gap[k].Type) {
				fresh[i] = p.merge(fresh[i], gap[k])
				next = k + 1
				break
			}
		}
	}
	return fresh
}

func compatible(a, b string) bool {
	return a == b || (isCell(a) && isCell(b))
}

func isCell(typ string) bool { return typ == "tableCell" || typ == "tableHeader" }

// merge returns the original when both render alike. Otherwise the parse wins
// on what markdown spells, the original keeps the rest, and the children are
// matched the same way.
func (p *parser) merge(n, o Node) Node {
	if !compatible(n.Type, o.Type) {
		return n
	}
	if p.key(n) == p.key(o) {
		out := o.Clone()
		// Only a full header row is drawn as one, so losing the header proves nothing.
		if n.Type == "tableHeader" {
			out.Type = n.Type
		}
		return out
	}
	if !isBlock(o.Type) {
		return n
	}
	out := n
	if n.Type != "tableHeader" {
		out.Type = o.Type
	}
	out.Attrs = p.mergeAttrs(n, o)
	if len(n.Marks) == 0 && len(o.Marks) > 0 {
		out.Marks = make([]Mark, len(o.Marks))
		for i := range o.Marks {
			out.Marks[i] = o.Marks[i].Clone()
		}
	}
	out.extra = maps.Clone(o.extra)
	switch o.Type {
	case "tableRow":
		out.Content = p.mergeCells(n.Content, o.Content)
	case "doc", "bulletList", "orderedList", "taskList", "decisionList", "listItem", "blockquote",
		"panel", "expand", "nestedExpand", "table", "tableCell", "tableHeader", "mediaSingle", "mediaGroup":
		out.Content = p.mergeChildren(n.Content, o.Content)
	}
	return out
}

func isBlock(typ string) bool {
	switch typ {
	case "media", "mediaInline", "caption":
		return false
	}
	return !isInline(typ)
}

// carried are the attributes markdown spells, which an edit decides.
var carried = map[string][]string{
	"heading":      {"level"},
	"orderedList":  {"order"},
	"taskItem":     {"state"},
	"codeBlock":    {"language"},
	"panel":        {"panelType", "panelIconText"},
	"expand":       {"title"},
	"nestedExpand": {"title"},
}

// mergeAttrs keeps the original's attributes whole when they render the parse
// the same way: an upper-cased panelType, a decision state nothing draws.
func (p *parser) mergeAttrs(n, o Node) Attrs {
	if len(o.Attrs) == 0 {
		return n.Attrs
	}
	candidate := n
	candidate.Attrs = o.Attrs
	if p.key(candidate) == p.key(n) {
		return cloneAttrs(o.Attrs)
	}
	out := cloneAttrs(o.Attrs)
	for _, k := range carried[o.Type] {
		delete(out, k)
		if v, ok := n.Attrs[k]; ok {
			out[k] = v
		}
	}
	return out
}

func (p *parser) mergeChildren(parsed, orig []Node) []Node {
	kp, ko := p.keys(parsed), p.keys(orig)
	out := make([]Node, 0, len(parsed))
	i, j := 0, 0
	for _, m := range lcs(kp, ko) {
		out = append(out, p.between(parsed[i:m[0]], orig[j:m[1]], ko[j:m[1]])...)
		out = append(out, orig[m[1]].Clone())
		i, j = m[0]+1, m[1]+1
	}
	return append(out, p.between(parsed[i:], orig[j:], ko[j:])...)
}

// between keeps an original child that renders to nothing, which nobody saw,
// unless something visible beside it was deleted.
func (p *parser) between(parsed, orig []Node, keys []string) []Node {
	pairedWith := make([]int, len(orig))
	next := 0
	for i := range parsed {
		for k := next; k < len(orig); k++ {
			if keys[k] != "" && compatible(parsed[i].Type, orig[k].Type) {
				parsed[i] = p.merge(parsed[i], orig[k])
				pairedWith[k], next = i+1, k+1
				break
			}
		}
	}
	for k := range orig {
		if keys[k] != "" && pairedWith[k] == 0 {
			return parsed
		}
	}
	out := make([]Node, 0, len(parsed)+len(orig))
	emitted := 0
	for k := range orig {
		if keys[k] == "" {
			out = append(out, orig[k].Clone())
			continue
		}
		out = append(out, parsed[emitted:pairedWith[k]]...)
		emitted = pairedWith[k]
	}
	return append(out, parsed[emitted:]...)
}

// mergeCells pairs cells by grid column, folding the empty columns a merged
// cell was drawn with back into its span.
func (p *parser) mergeCells(parsed, orig []Node) []Node {
	width := 0
	for i := range orig {
		width += span(orig[i])
	}
	if len(parsed) > width && allEmpty(p, parsed[width:]) {
		parsed = parsed[:width]
	}
	if len(parsed) != width {
		return p.mergeChildren(parsed, orig)
	}
	out := make([]Node, 0, len(orig))
	c := 0
	for i := range orig {
		n := span(orig[i])
		if n > 1 && !allEmpty(p, parsed[c+1:c+n]) {
			out = append(out, parsed[c:c+n]...)
		} else {
			out = append(out, p.merge(parsed[c], orig[i]))
		}
		c += n
	}
	return out
}

func span(cell Node) int {
	n, _ := attrInt(cell.Attrs, "colspan")
	return max(1, n)
}

func allEmpty(p *parser, cells []Node) bool {
	for i := range cells {
		if p.key(cells[i]) != "" {
			return false
		}
	}
	return true
}

func (p *parser) keys(nodes []Node) []string {
	out := make([]string, len(nodes))
	for i := range nodes {
		out[i] = p.key(nodes[i])
	}
	return out
}

// key is what a node renders to on its own. Cells and rows are keyed by text
// alone, because a grid's padding depends on the rest of the table.
func (p *parser) key(n Node) string {
	w := writer{buf: p.keyBuf[:0], opt: p.opt, gl: p.gl}
	switch n.Type {
	case "listItem":
		w.block(NewNode("bulletList", n))
	case "taskItem":
		w.block(NewNode("taskList", n))
	case "decisionItem":
		w.block(NewNode("decisionList", n))
	case "tableRow":
		return p.rowKey(n)
	case "tableCell", "tableHeader":
		return w.cellText(n)
	default:
		w.block(n)
	}
	w.endLine()
	p.keyBuf = w.buf
	return string(w.buf)
}

func (p *parser) rowKey(row Node) string {
	w := writer{opt: p.opt, gl: p.gl}
	var slots []string
	headers := 0
	for i := range row.Content {
		cell := row.Content[i]
		if cell.Type == "tableHeader" {
			headers++
		}
		slots = append(slots, w.cellText(cell))
		for range span(cell) - 1 {
			slots = append(slots, "")
		}
	}
	for len(slots) > 0 && slots[len(slots)-1] == "" {
		slots = slots[:len(slots)-1]
	}
	key := strings.Join(slots, "\x00")
	if headers > 0 && headers == len(row.Content) {
		key = "\x01" + key
	}
	return key
}

// lcs trims the common head and tail first, so the quadratic table only
// spans what changed.
func lcs(a, b []string) [][2]int {
	head := 0
	for head < len(a) && head < len(b) && a[head] == b[head] {
		head++
	}
	tail := 0
	for tail < len(a)-head && tail < len(b)-head && a[len(a)-1-tail] == b[len(b)-1-tail] {
		tail++
	}
	out := make([][2]int, 0, head+tail)
	for i := range head {
		out = append(out, [2]int{i, i})
	}
	ma, mb := a[head:len(a)-tail], b[head:len(b)-tail]
	if len(ma) > 0 && len(mb) > 0 {
		table := make([][]int, len(ma)+1)
		for i := range table {
			table[i] = make([]int, len(mb)+1)
		}
		for i := len(ma) - 1; i >= 0; i-- {
			for j := len(mb) - 1; j >= 0; j-- {
				if ma[i] == mb[j] {
					table[i][j] = table[i+1][j+1] + 1
				} else {
					table[i][j] = max(table[i+1][j], table[i][j+1])
				}
			}
		}
		for i, j := 0, 0; i < len(ma) && j < len(mb); {
			switch {
			case ma[i] == mb[j]:
				out = append(out, [2]int{head + i, head + j})
				i++
				j++
			case table[i+1][j] >= table[i][j+1]:
				i++
			default:
				j++
			}
		}
	}
	for k := tail; k > 0; k-- {
		out = append(out, [2]int{len(a) - k, len(b) - k})
	}
	return out
}
