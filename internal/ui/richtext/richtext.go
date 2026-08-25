// Package richtext renders an ADF document as styled terminal lines.
//
// It exists because pkg/adf's markdown is a serialisation for editing: it backs
// the $EDITOR handoff and a byte-stable round trip, it deliberately does not
// escape prose, and it is public API that must not grow a dependency on a UI
// library. Displaying it puts ## and ** and [text](url) on screen, and feeding
// it to a markdown renderer would re-parse text that was never markdown — so
// *not emphasis* would become emphasis — after the information a display needs
// has already been thrown away: by then an error panel and a plain quote are
// the same node.
//
// So the walk happens once, over the document, straight to lines:
//
//	r := richtext.Render(doc, richtext.Options{
//		Width:   pane - gutter,
//		Styles:  richtext.NewStyles(palette),
//		Markers: richtext.UnicodeMarkers(),
//	})
//
// The caller hands in the theme. This package holds none, which keeps the
// golden files a property of the document rather than of whichever theme was
// loaded, lets the issue and the comment views share one renderer, and
// forecloses an import cycle: nothing here imports internal/ui/kernel.
//
// Every line stands on its own. A line is built by breaking first and styling
// after, because ansi.Wrap does not re-open an SGR sequence on the lines it
// makes, so a pane showing a window into the middle of a wrapped run would
// otherwise draw it unstyled or open it with a stray reset.
//
// Lines are not padded to Width: padding belongs to the pane, which owns the
// gutter and the mouse zone. Widths is there so a pane can clamp panning
// without measuring every line on every frame, and a line may be wider than
// Width where the alternative is losing data — code is never wrapped and a
// table is never cut.
package richtext

import (
	"strings"
	"time"

	"github.com/charmbracelet/x/ansi"

	"github.com/varijkapil13/saral/pkg/adf"
)

// defaultWidth is what a caller who asked for no width gets, rather than a
// document rendered one word per line.
const defaultWidth = 80

// Options tunes one rendering.
type Options struct {
	// Width is how many cells a line has, gutters included.
	Width int

	// Location renders the instant a date node carries. Nil means UTC. No clock
	// is read, so a document always renders to the same lines.
	Location *time.Location

	// Styles is the theme, already built. Nothing here constructs a style per
	// line.
	Styles Styles

	// Markers are the glyphs. The zero value is the Unicode set.
	Markers Markers

	// Open holds the folds the reader has opened, by Fold.Index. Everything
	// else is closed, which is how Jira shows an expand too.
	Open map[int]bool
}

// Fold is one expand in the document, whether or not it is open.
type Fold struct {
	// Index is the fold's position in document order, and the key Options.Open
	// is read with. It does not move when another fold is opened or closed.
	Index int

	// Line is where the fold's own line landed in Lines. A fold inside a table
	// cell reports the line the row starts on, because that is the line a pane
	// can hit-test.
	Line int

	Open  bool
	Title string
}

// Rendered is a document laid out at one width.
type Rendered struct {
	// Lines are the styled lines, unpadded.
	Lines []string

	// Widths is the width of each line in terminal cells, measured while it was
	// built. A pane clamps panning against these rather than measuring every
	// line on every frame.
	Widths []int

	// Folds are every expand in the document, in document order.
	Folds []Fold
}

// Width is the widest line, which is how far a pane can pan.
func (r Rendered) Width() int {
	n := 0
	for _, w := range r.Widths {
		n = max(n, w)
	}
	return n
}

// Render lays a document out at one width.
func Render(d adf.Doc, opt Options) Rendered {
	opt.Markers = opt.Markers.withDefaults()
	if opt.Width <= 0 {
		opt.Width = defaultWidth
	}
	var folds []Fold
	var memo []painted
	var marked []markPainted
	next := 0
	f := &frame{
		opt: &opt, sty: &opt.Styles, mk: &opt.Markers, memo: &memo, marked: &marked,
		width: opt.Width, floor: minAvail,
		folds: &folds, nextFold: &next,
	}
	f.ctx, f.pctx, f.pcont = opt.Styles.Body, f.paint(&f.sty.Body), f.paint(&f.sty.Cont)
	f.blocks(d.Content, false)
	return Rendered{Lines: f.lines, Widths: f.widths, Folds: folds}
}

// Summary flattens a document onto one unstyled line, for a list row or a
// palette entry. Structure is dropped rather than spelled: a row has no space
// to say that something was a heading.
func Summary(d adf.Doc, width int) string {
	if width <= 0 {
		return ""
	}
	fl := flat{limit: width}
	fl.b.Grow(width * 2)
	fl.nodes(d.Content)
	return ansi.Truncate(fl.b.String(), width, "…")
}

// flat collapses a document's text into one line, one space between words, and
// stops once it has more than the caller can show.
type flat struct {
	b     strings.Builder
	limit int
	w     int
	gap   bool
}

// full reports whether there is already more than the width asked for, with a
// cell in hand for the ellipsis.
func (fl *flat) full() bool { return fl.w > fl.limit }

func (fl *flat) nodes(nodes []adf.Node) {
	for i := range nodes {
		if fl.full() {
			return
		}
		n := &nodes[i]
		switch n.Type {
		case "text":
			fl.add(n.Text)
		case "mention":
			fl.add(mentionText(n.Attrs))
		case "status":
			text, _ := statusParts(n.Attrs)
			fl.add(text)
		case "emoji":
			fl.add(emojiText(n.Attrs))
		case "hardBreak", "rule":
			fl.gap = true
		default:
			fl.nodes(n.Content)
		}
		if !isInline(n.Type) {
			fl.gap = true
		}
	}
}

func (fl *flat) add(s string) {
	for at := 0; at < len(s) && !fl.full(); {
		if isSpace(s[at]) {
			fl.gap = true
			at++
			continue
		}
		end := at
		for end < len(s) && !isSpace(s[end]) {
			end++
		}
		if fl.gap && fl.b.Len() > 0 {
			fl.b.WriteByte(' ')
			fl.w++
		}
		fl.gap = false
		word := sanitize(s[at:end])
		fl.b.WriteString(word)
		fl.w += ansi.StringWidth(word)
		at = end
	}
}

func isSpace(c byte) bool {
	return c == ' ' || c == '\t' || c == '\n' || c == '\r' || c == '\v' || c == '\f'
}
