package richtext

import (
	"strings"

	"github.com/varijkapil13/saral/pkg/adf"
)

// cellFloor is the narrowest a column is squeezed to. Past it the grid is
// allowed to be wider than the pane: a table that does not fit is panned, not
// cut, and a column of two cells holds nothing worth reading anyway.
const cellFloor = 8

// cell is one grid position. A blank one is the tail of a span: the columns a
// merged cell covers are held so that every value below it stays under the
// heading it belongs to.
type cell struct {
	nodes  []adf.Node
	header bool
	lines  []string
	widths []int
	nat    int
}

// table lays a table out as a grid whose columns line up in terminal cells.
//
// A cell wraps inside its column rather than being cut off: squeezing a column
// and truncating what is in it loses data silently, which is the one thing a
// reader cannot recover from. A row is therefore as tall as its tallest cell.
// The pixel colwidth the browser editor stores is ignored — it says nothing
// about a terminal, and what a column needs is measurable from what is in it.
func (f *frame) table(n adf.Node) {
	rows, header := f.grid(n)
	if len(rows) == 0 {
		return
	}
	widths := f.columns(rows)
	for i := range rows {
		folds := len(*f.folds)
		for j := range rows[i] {
			c := &rows[i][j]
			if len(c.nodes) == 0 {
				continue
			}
			c.lines, c.widths = f.cellLines(c.nodes, widths[j], c.header, f.folds, f.nextFold)
		}
		// A blank line owed from the block above is written first, so that the
		// line a fold in this row reports is the line the row is really on.
		f.settle()
		at := len(f.lines)
		f.row(rows[i], widths)
		if i == 0 && header {
			f.divider(widths)
		}
		f.patchFolds(folds, at)
	}
}

// grid folds the table into rectangular rows, expanding a merged cell into the
// positions it covers. Emitting one column for a cell that spans two puts every
// value after it under the wrong heading, which is not a cosmetic problem — it
// is the wrong answer.
func (f *frame) grid(n adf.Node) (rows [][]cell, header bool) {
	rows = make([][]cell, 0, len(n.Content))
	var carry []int // per column, the rows a span from above still covers
	columns := 0
	for i := range n.Content {
		row := n.Content[i]
		if row.Type != "tableRow" {
			continue
		}
		out := make([]cell, 0, len(row.Content))
		headers, at := 0, 0
		for j := range row.Content {
			for at < len(carry) && carry[at] > 0 {
				carry[at]--
				at++
				out = append(out, cell{})
			}
			src := row.Content[j]
			if src.Type == "tableHeader" {
				headers++
			}
			span, _ := attrInt(src.Attrs, "colspan")
			down, _ := attrInt(src.Attrs, "rowspan")
			for k := range max(span, 1) {
				held := cell{}
				if k == 0 {
					held = cell{nodes: src.Content, header: src.Type == "tableHeader"}
				}
				out = append(out, held)
				for len(carry) <= at {
					carry = append(carry, 0)
				}
				carry[at] = max(down, 1) - 1
				at++
			}
		}
		for ; at < len(carry); at++ {
			if carry[at] > 0 {
				carry[at]--
				out = append(out, cell{})
			}
		}
		if len(rows) == 0 {
			header = headers > 0 && headers == len(out)
		}
		columns = max(columns, len(out))
		rows = append(rows, out)
	}
	if columns == 0 {
		return nil, false
	}
	for i := range rows {
		for len(rows[i]) < columns {
			rows[i] = append(rows[i], cell{})
		}
	}
	return rows, header
}

// columns measures every column against what is in it and then squeezes the
// widest ones until the grid fits.
func (f *frame) columns(rows [][]cell) []int {
	widths := make([]int, len(rows[0]))
	// The measuring pass renders into fold storage of its own: counting the same
	// fold twice would put two entries in the answer, and advancing the shared
	// counter would renumber every fold after it.
	var scratch []Fold
	count := *f.nextFold
	sub := f.cellFrame(1<<20, false, &scratch, &count)
	sub.dry = true
	for i := range rows {
		for j := range rows[i] {
			c := &rows[i][j]
			if len(c.nodes) == 0 {
				continue
			}
			for _, n := range sub.remeasure(c.nodes, c.header) {
				c.nat = max(c.nat, n)
			}
			widths[j] = max(widths[j], c.nat)
		}
	}
	avail := f.avail()
	for gridWidth(widths) > avail {
		widest := -1
		for j := range widths {
			if widths[j] > cellFloor && (widest < 0 || widths[j] > widths[widest]) {
				widest = j
			}
		}
		if widest < 0 {
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
	for _, w := range widths {
		total += w + 3
	}
	return total
}

// remeasure asks how wide a cell wants to be, which is the width it takes when
// nothing wraps it. The frame is reused across the cells of one table: what is
// wanted is the widths, and the lines a dry frame builds are empty.
func (f *frame) remeasure(nodes []adf.Node, header bool) []int {
	f.lines, f.widths, f.rails, f.owe = f.lines[:0], f.widths[:0], f.rails[:0], false
	ctx := &f.sty.TableCell
	if header {
		ctx = &f.sty.TableHeader
	}
	f.setCtx(ctx)
	f.blocks(nodes, false)
	return f.widths
}

// cellLines renders one cell in its own column.
func (f *frame) cellLines(nodes []adf.Node, width int, header bool, folds *[]Fold, next *int) (lines []string, widths []int) {
	sub := f.cellFrame(width, header, folds, next)
	sub.blocks(nodes, false)
	return sub.lines, sub.widths
}

func (f *frame) cellFrame(width int, header bool, folds *[]Fold, next *int) *frame {
	sub := f.sub(width)
	sub.folds, sub.nextFold, sub.floor, sub.unplaced = folds, next, 1, true
	if header {
		sub.setCtx(&f.sty.TableHeader)
	} else {
		sub.setCtx(&f.sty.TableCell)
	}
	return sub
}

// row emits one row of the grid, as tall as its tallest cell.
func (f *frame) row(cells []cell, widths []int) {
	height := 1
	for i := range cells {
		height = max(height, len(cells[i].lines))
	}
	for line := range height {
		f.grid1(cells, widths, line)
	}
}

func (f *frame) grid1(cells []cell, widths []int, line int) {
	border := f.paint(&f.sty.TableBorder)
	f.rowSpans = f.rowSpans[:0]
	for j := range widths {
		text, w := "", 0
		if j < len(cells) && line < len(cells[j].lines) {
			text, w = cells[j].lines[line], cells[j].widths[line]
		}
		f.rowSpans = append(f.rowSpans,
			span{text: f.mk.VLine, p: border, w: 1},
			span{text: " ", w: 1},
			span{text: text, w: w},
			span{text: pad(widths[j] - w), w: max(widths[j]-w, 0)},
			span{text: " ", w: 1})
	}
	f.rowSpans = append(f.rowSpans, span{text: f.mk.VLine, p: border, w: 1})
	f.out(f.rowSpans)
}

// divider is the line under a header row.
func (f *frame) divider(widths []int) {
	border := f.paint(&f.sty.TableBorder)
	f.rowSpans = f.rowSpans[:0]
	for j := range widths {
		f.rowSpans = append(f.rowSpans,
			span{text: f.mk.VLine, p: border, w: 1},
			span{text: strings.Repeat(f.mk.HLine, widths[j]+2), p: border, w: widths[j] + 2})
	}
	f.rowSpans = append(f.rowSpans, span{text: f.mk.VLine, p: border, w: 1})
	f.out(f.rowSpans)
}

// patchFolds points the folds a row's cells registered at the row itself. A
// fold's line is what a pane hit-tests against, and a line inside a cell is not
// one of the document's own.
func (f *frame) patchFolds(from, at int) {
	for i := from; i < len(*f.folds); i++ {
		if (*f.folds)[i].Line < 0 {
			(*f.folds)[i].Line = at
		}
	}
}

func pad(n int) string {
	if n <= 0 {
		return ""
	}
	if n <= len(spaces) {
		return spaces[:n]
	}
	return strings.Repeat(" ", n)
}
