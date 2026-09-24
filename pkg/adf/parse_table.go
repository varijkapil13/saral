package adf

import "strings"

// table reads a run of pipe rows back into a table.
//
// The grid the renderer draws is padded so that it lines up in a monospaced
// pager, and a cell holding more than one block was folded onto one line to fit
// in it. Neither survives: a cell comes back as one paragraph, and the widths
// are recomputed from the text the next time it is rendered. What a merged cell
// spanned is gone too — it was written out as one value and as many empty
// columns as it covered, and that is what comes back.
func (p *parser) table(ls []line, i int) (Node, int, error) {
	end := i
	for end < len(ls) && strings.HasPrefix(ls[end].text, "|") {
		end++
	}

	rows := make([][]string, 0, end-i)
	for _, l := range ls[i:end] {
		rows = append(rows, splitRow(l.text))
	}
	header := len(rows) > 1 && isDelimiterRow(rows[1]) && len(rows[1]) == len(rows[0])
	if header {
		rows = append(rows[:1], rows[2:]...)
	}

	node := NewNode("table")
	for r, cells := range rows {
		typ := "tableCell"
		if header && r == 0 {
			typ = "tableHeader"
		}
		row := NewNode("tableRow")
		for _, text := range cells {
			content, err := p.inlineIn([]line{{text: text, no: ls[i].no}}, true)
			if err != nil {
				return Node{}, end, err
			}
			cell := NewNode(typ, NewNode("paragraph").WithContent(content...))
			row.Content = append(row.Content, cell)
		}
		node.Content = append(node.Content, row)
	}
	return node, end, nil
}

// splitRow takes a pipe row apart, undoing foldCell's escaping.
func splitRow(text string) []string {
	text = strings.TrimSuffix(strings.TrimPrefix(strings.TrimRight(text, " \t"), "|"), "|")
	var (
		cells []string
		cell  strings.Builder
	)
	for i := 0; i < len(text); {
		run := 0
		for i+run < len(text) && text[i+run] == '\\' {
			run++
		}
		if i+run == len(text) || text[i+run] != '|' {
			n := max(run, 1)
			cell.WriteString(text[i : i+n])
			i += n
			continue
		}
		cell.WriteString(strings.Repeat("\\", run/2))
		if run%2 == 1 {
			cell.WriteByte('|')
		} else {
			cells = append(cells, strings.TrimSpace(cell.String()))
			cell.Reset()
		}
		i += run + 1
	}
	return append(cells, strings.TrimSpace(cell.String()))
}

// isDelimiterRow reports the row of dashes that says the row above it is a
// header. The renderer writes one only when every cell of the first row is a
// header cell, which is the only case a pager can honestly show.
func isDelimiterRow(cells []string) bool {
	for _, cell := range cells {
		if cell == "" {
			return false
		}
		for i := range len(cell) {
			if cell[i] != '-' {
				return false
			}
		}
	}
	return len(cells) > 0
}
