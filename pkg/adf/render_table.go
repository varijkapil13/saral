package adf

import (
	"bytes"
	"strings"

	"github.com/charmbracelet/x/ansi"
)

// minCell is the narrowest a column is squeezed to before the table is allowed
// to overflow the width it was given.
const minCell = 3

// table lays a table out as a padded pipe grid. Markdown's table syntax only
// reads as a table once the columns line up, so the columns are measured in
// terminal cells rather than in bytes or runes: a cell holding CJK or an emoji
// is two cells wide per character, and counting either of the other two puts
// every row after it out by a column.
//
// A cell's blocks are folded onto one line, because a pipe row cannot hold a
// second one. A table whose first row is not a header row gets no delimiter
// row, which is not GitHub-flavoured markdown but is the only honest thing a
// pager can show.
func (w *writer) table(n Node) {
	rows, header := w.tableCells(n)
	if len(rows) == 0 {
		return
	}
	widths := columnWidths(rows, w.opt.TableWidth)

	for i := range rows {
		if i > 0 {
			w.separate(false)
		}
		w.startLine()
		w.writeRow(rows[i], widths)
		w.endLine()
		if i == 0 && header {
			w.separate(false)
			w.startLine()
			w.writeRow(nil, widths)
			w.endLine()
		}
	}
}

// tableCells folds the table into rectangular rows of rendered cells, and
// reports whether the first row is a header row.
func (w *writer) tableCells(n Node) (rows [][]string, header bool) {
	rows = make([][]string, 0, len(n.Content))
	scratch := writer{opt: w.opt, gl: w.gl}
	columns := 0
	for i := range n.Content {
		row := n.Content[i]
		if row.Type != "tableRow" {
			continue
		}
		cells := make([]string, 0, len(row.Content))
		headers := 0
		for j := range row.Content {
			if row.Content[j].Type == "tableHeader" {
				headers++
			}
			cells = append(cells, scratch.cellText(row.Content[j]))
		}
		if len(rows) == 0 {
			header = headers > 0 && headers == len(cells)
		}
		if len(cells) > columns {
			columns = len(cells)
		}
		rows = append(rows, cells)
	}
	if columns == 0 {
		return nil, false
	}
	for i := range rows {
		for len(rows[i]) < columns {
			rows[i] = append(rows[i], "")
		}
	}
	return rows, header
}

// cellText renders one cell into the scratch writer it is called on, which is
// reused for every cell of the table.
func (w *writer) cellText(n Node) string {
	buf := w.buf[:0]
	*w = writer{buf: buf, opt: w.opt, gl: w.gl}
	w.blocks(n.Content, false)
	w.endLine()
	return foldCell(w.buf)
}

// foldCell puts a cell's lines on one line and escapes the character the grid
// is drawn with.
func foldCell(b []byte) string {
	var out strings.Builder
	out.Grow(len(b))
	for len(b) > 0 {
		line := b
		if i := bytes.IndexByte(b, '\n'); i >= 0 {
			line, b = b[:i], b[i+1:]
		} else {
			b = nil
		}
		line = bytes.Trim(line, " \t")
		if len(line) == 0 {
			continue
		}
		if out.Len() > 0 {
			out.WriteByte(' ')
		}
		if bytes.IndexByte(line, '|') < 0 {
			out.Write(line)
			continue
		}
		for _, c := range line {
			if c == '|' {
				out.WriteByte('\\')
			}
			out.WriteByte(c)
		}
	}
	return out.String()
}

// columnWidths measures every column and, when a width was given, squeezes the
// widest ones until the grid fits inside it.
func columnWidths(rows [][]string, limit int) []int {
	widths := make([]int, len(rows[0]))
	for j := range widths {
		widths[j] = minCell
	}
	for i := range rows {
		for j := range rows[i] {
			if n := ansi.StringWidth(rows[i][j]); n > widths[j] {
				widths[j] = n
			}
		}
	}
	if limit <= 0 {
		return widths
	}
	for gridWidth(widths) > limit {
		widest := 0
		for j := range widths {
			if widths[j] > widths[widest] {
				widest = j
			}
		}
		if widths[widest] <= minCell {
			break
		}
		widths[widest]--
	}
	return widths
}

// gridWidth is what a row costs on screen: a bar at each end and between every
// pair of columns, and a space either side of every cell.
func gridWidth(widths []int) int {
	total := 1
	for _, width := range widths {
		total += width + 3
	}
	return total
}

// writeRow appends one row of the grid, padding or truncating every cell to
// its column. A nil cells is the delimiter row under a header.
func (w *writer) writeRow(cells []string, widths []int) {
	w.buf = append(w.buf, '|')
	for j := range widths {
		w.buf = append(w.buf, ' ')
		filled := widths[j]
		if cells == nil {
			w.buf = appendRepeat(w.buf, '-', widths[j])
		} else {
			filled = w.appendCell(cells[j], widths[j])
		}
		w.buf = appendRepeat(w.buf, ' ', widths[j]-filled)
		w.buf = append(w.buf, ' ', '|')
	}
}

// appendCell writes one cell and returns how many terminal cells it took.
func (w *writer) appendCell(s string, width int) int {
	n := ansi.StringWidth(s)
	if n > width {
		s = ansi.Truncate(s, width, w.gl.ellipsis)
		n = ansi.StringWidth(s)
	}
	w.buf = append(w.buf, s...)
	return n
}

func appendRepeat(dst []byte, c byte, n int) []byte {
	for range n {
		dst = append(dst, c)
	}
	return dst
}
