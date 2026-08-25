package richtext

import (
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"
)

func folds(t *testing.T, name string, open map[int]bool) (r Rendered, text string) {
	t.Helper()
	opt := options(80)
	opt.Open = open
	r = Render(load(t, name), opt)
	return r, stripped(r)
}

func TestFolds_AreClosedUntilTheReaderOpensOne(t *testing.T) {
	t.Parallel()
	const inside = "Expands may hold a table"
	_, closed := folds(t, "kitchen.json", nil)
	if strings.Contains(closed, inside) {
		t.Errorf("a closed fold showed what is inside it:\n%s", closed)
	}
	if !strings.Contains(closed, UnicodeMarkers().Folded+" an expand") {
		t.Errorf("a closed fold does not say it can be opened:\n%s", closed)
	}
	_, opened := folds(t, "kitchen.json", map[int]bool{0: true})
	if !strings.Contains(opened, inside) {
		t.Errorf("an opened fold did not show what is inside it:\n%s", opened)
	}
	if !strings.Contains(opened, UnicodeMarkers().Unfolded+" an expand") {
		t.Errorf("an opened fold still shows as closed:\n%s", opened)
	}
}

// TestFolds_IndexIsDocumentOrder pins the key Options.Open is read with. A
// localId would have been the obvious choice and is the wrong one: it is
// optional on the wire, absent on every expand in the stored kitchen sink, and
// no more unique than the editor that wrote it made it.
func TestFolds_IndexIsDocumentOrder(t *testing.T) {
	t.Parallel()
	r, _ := folds(t, "edges.json", nil)
	want := []struct {
		index int
		title string
	}{
		{0, "in a cell"}, // the table comes first, and a cell may hold a fold
		{1, "a fold holding another fold"},
	}
	if len(r.Folds) != len(want) {
		t.Fatalf("expected %d folds, got %+v", len(want), r.Folds)
	}
	for i, w := range want {
		if r.Folds[i].Index != w.index || r.Folds[i].Title != w.title {
			t.Errorf("fold %d is %+v, wanted index %d titled %q", i, r.Folds[i], w.index, w.title)
		}
	}
}

// TestFolds_IndicesDoNotMoveWhenOneOpens is what makes the index a key rather
// than a position: the fold inside a closed one is counted, so opening its
// parent does not renumber the folds after it.
func TestFolds_IndicesDoNotMoveWhenOneOpens(t *testing.T) {
	t.Parallel()
	before, _ := folds(t, "edges.json", nil)
	after, _ := folds(t, "edges.json", map[int]bool{1: true})

	for i := range before.Folds {
		if before.Folds[i].Index != after.Folds[i].Index || before.Folds[i].Title != after.Folds[i].Title {
			t.Errorf("opening a fold renumbered the others: %+v became %+v", before.Folds[i], after.Folds[i])
		}
	}
	if len(after.Folds) != len(before.Folds)+1 {
		t.Fatalf("opening a fold did not reveal the one inside it: %+v", after.Folds)
	}
	inner := after.Folds[len(after.Folds)-1]
	if inner.Index != 2 {
		t.Errorf("the fold inside a fold is keyed %d, and the count reserved 2: %+v", inner.Index, after.Folds)
	}
	if inner.Title != "details" {
		t.Errorf("a fold with no title of its own is named %q", inner.Title)
	}
	if !after.Folds[1].Open || before.Folds[1].Open {
		t.Errorf("a fold does not report whether it is open: %+v then %+v", before.Folds[1], after.Folds[1])
	}
}

// TestFolds_LineIsWhereTheFoldIs is the half a pane hit-tests against.
func TestFolds_LineIsWhereTheFoldIs(t *testing.T) {
	t.Parallel()
	for _, open := range []map[int]bool{nil, {0: true}, {1: true}, {0: true, 1: true, 2: true}} {
		r, _ := folds(t, "edges.json", open)
		for _, f := range r.Folds {
			if f.Line < 0 || f.Line >= len(r.Lines) {
				t.Fatalf("fold %+v points at line %d of %d", f, f.Line, len(r.Lines))
			}
			// A fold in a table cell reports the line its row starts on, which is
			// the only line of the document's own that a grid position has.
			row := ansi.Strip(r.Lines[f.Line])
			if !strings.Contains(row, f.Title) {
				t.Errorf("fold %+v points at %q, which does not hold its title", f, row)
			}
		}
	}
}

// TestFolds_InATableCellOpensInsideTheRow covers the fold ADF allows in a grid
// position, which is why a fold's line is patched to the row it landed in.
func TestFolds_InATableCellOpensInsideTheRow(t *testing.T) {
	t.Parallel()
	_, closed := folds(t, "edges.json", nil)
	if strings.Contains(closed, "a fold inside") {
		t.Errorf("a closed fold in a cell showed what is inside it:\n%s", closed)
	}
	opened, text := folds(t, "edges.json", map[int]bool{0: true})
	if !strings.Contains(text, "a fold inside") {
		t.Errorf("a fold opened inside a cell did not show what is in it:\n%s", text)
	}
	row := opened.Folds[0].Line
	if !strings.Contains(ansi.Strip(opened.Lines[row]), "in a cell") {
		t.Errorf("a fold in a cell points at %q rather than at its row", ansi.Strip(opened.Lines[row]))
	}
	if !strings.Contains(ansi.Strip(opened.Lines[row+1]), "a fold inside") {
		t.Errorf("the row a fold opened in did not grow under it: %q", ansi.Strip(opened.Lines[row+1]))
	}
}
