package card

import (
	"strings"

	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/varijkapil13/saral/internal/ui/kernel"
	"github.com/varijkapil13/saral/internal/ui/widget"
)

// Facts is what a card shows, already read off the issue and formatted by the
// view. A blank field draws nothing, never a placeholder, so a view leaves out
// what it does not show: the board fills no status, since its column says it.
// The type cell beside the key is drawn only with a TypeName; TypeGlyph alone
// is the resting mark.
type Facts struct {
	Key, Summary, TypeGlyph, TypeName, StatusGlyph, StatusName string
	Assignee, Priority, Updated, Estimate                      string
	Labels, FixVersions                                        []string
	ParentKey, ParentSummary, Due, Subtasks                    string
	Overdue                                                    bool
	// Category indexes Styles.Categories: jira.StatusCategory's own value.
	Category                      int
	TypeZone, StatusZone, WhoZone string
}

// State is how the card sits in its view. Mark is the resting mark cell, the
// type glyph when blank; Held, Picked and Selected take the cell over, in that
// order.
type State struct {
	Selected, Picked, Held bool
	Mark                   string
}

// Styles are a view's card styles, built once per theme generation with
// NewStyles. Categories colours the bracket and the status by status category.
type Styles struct {
	Base, Muted, Key, Selected, Held, Warning lipgloss.Style
	Categories                                [4]lipgloss.Style
	Gen                                       int

	paints [toneCategory + 4]paint
}

// NewStyles builds the card styles from a theme: the board's category colours,
// the accent key, Warning for a card in hand and for an overdue date.
func NewStyles(t *kernel.Theme) *Styles {
	s := &Styles{
		Base: t.Base, Muted: t.Muted, Key: t.Accent, Selected: t.Selected, Held: t.Warning, Warning: t.Warning,
		Categories: [4]lipgloss.Style{t.Muted, t.Base, t.Accent, t.Success},
		Gen:        t.Gen,
	}
	for at := range s.paints {
		s.paints[at] = newPaint(s.style(tone(at)))
	}
	return s
}

func (s *Styles) style(t tone) lipgloss.Style {
	switch t {
	case toneMuted:
		return s.Muted
	case toneKey:
		return s.Key
	case toneWarn:
		return s.Warning
	case toneBase:
		return s.Base
	}
	return s.Categories[t-toneCategory]
}

// paint is a style's opening and closing sequences, split once so a card
// writes them around its text instead of calling Render for every cell.
type paint struct {
	open, close string
	ok          bool
}

func newPaint(s lipgloss.Style) paint {
	const probe = "probe text"
	open, closing, found := strings.Cut(s.Render(probe), probe)
	if !found || strings.Contains(closing, probe) || open+"a b"+closing != s.Render("a b") {
		return paint{}
	}
	return paint{open: open, close: closing, ok: true}
}

// Frame is where a card is drawn: its width in cells, its look, the glyph tier
// and the view's zones.
type Frame struct {
	Width  int
	Look   Look
	Glyphs kernel.Glyphs
	Zones  widget.Zoner
}

// Render appends the card to dst: exactly fr.Look.Lines() lines, each exactly
// fr.Width cells wide. Lines is not a card — each view keeps its own row for
// it — so Render with Lines appends nothing.
func Render(dst []string, f Facts, st State, sty *Styles, fr Frame) []string {
	if !fr.Look.Cards() {
		return dst
	}
	n := fr.Look.Lines()
	if fr.Width <= 0 {
		for range n {
			dst = append(dst, "")
		}
		return dst
	}
	r := renderer{f: sanitized(f), st: st, sty: sty, fr: fr, width: max(fr.Width-2, 0), ell: fr.Glyphs.Ellipsis}
	if r.f.Category < 0 || r.f.Category >= len(sty.Categories) {
		r.f.Category = 0
	}
	r.whole = st.Selected || st.Held

	dst = append(dst, r.line(0, n, r.head()))
	if fr.Look == Roomy {
		first, second := wrap(r.f.Summary, r.width, r.ell)
		return append(dst,
			r.line(1, n, r.one(first, toneBase)),
			r.line(2, n, r.one(second, toneBase)),
			r.line(3, n, r.meta(false)),
			r.line(4, n, r.extra()))
	}
	return append(dst, r.line(1, n, r.one(r.f.Summary, toneBase)), r.line(2, n, r.meta(true)))
}

type tone uint8

// toneCategory is last: the four category tones follow it.
const (
	toneBase tone = iota
	toneMuted
	toneKey
	toneWarn
	toneCategory
)

type seg struct {
	text string
	tone tone
	zone string
	pad  int
}

func (s seg) width() int { return ansi.StringWidth(s.text) + s.pad }

func segWidth(segs []seg) int {
	w := 0
	for _, s := range segs {
		w += s.width()
	}
	return w
}

type renderer struct {
	f     Facts
	st    State
	sty   *Styles
	fr    Frame
	width int
	ell   string
	whole bool
	buf   [16]seg
	right [8]seg
}

func (r *renderer) one(text string, t tone) []seg {
	r.buf[0] = seg{text: text, tone: t}
	return r.buf[:1]
}

func (r *renderer) cat() tone { return toneCategory + tone(r.f.Category) }

func (r *renderer) mark() (string, tone) {
	g := r.fr.Glyphs
	switch {
	case r.st.Held:
		return g.Diamond, toneBase
	case r.st.Picked:
		return g.Check, toneKey
	case r.st.Selected:
		return g.Collapsed, toneBase
	case r.st.Mark != "":
		return r.st.Mark, toneMuted
	}
	return r.f.TypeGlyph, toneMuted
}

// head is the key's line. What it gives up as the width shrinks, in order:
// the type name, updated, the status name, the estimate, then both glyphs; the
// key is truncated only once nothing else is left beside it.
func (r *renderer) head() []seg {
	f := r.f
	typeFull, statusFull := cell(f.TypeGlyph, f.TypeName), cell(f.StatusGlyph, f.StatusName)
	typ, updated, status, estimate := typeFull, f.Updated, statusFull, f.Estimate
	if f.TypeName == "" {
		typ = ""
	}
	for step := 0; ; step++ {
		left, right := r.headCells(typ, updated, status, estimate)
		lw, rw := segWidth(left), segWidth(right)
		switch {
		case rw == 0 && lw <= r.width:
			return left
		case rw > 0 && lw+2+rw <= r.width:
			left = append(left, seg{pad: r.width - lw - rw})
			return append(left, right...)
		}
		switch step {
		case 0:
			if typ != "" {
				typ = f.TypeGlyph
			}
		case 1:
			updated = ""
		case 2:
			if status != "" {
				status = f.StatusGlyph
			}
		case 3:
			estimate = ""
		case 4:
			typ, status = "", ""
		default:
			return fit(left, r.width, r.ell)
		}
	}
}

func (r *renderer) headCells(typ, updated, status, estimate string) (left, right []seg) {
	f := r.f
	left = r.buf[:0]
	right = r.right[:0]
	if m, t := r.mark(); m != "" {
		left = append(left, seg{text: m, tone: t}, seg{pad: 1})
	}
	left = append(left, seg{text: f.Key, tone: toneKey})
	if typ != "" {
		left = append(left, seg{pad: 1, tone: toneMuted}, seg{text: typ, tone: toneMuted, zone: f.TypeZone})
	}
	if status != "" {
		right = append(right, seg{text: status, tone: r.cat(), zone: f.StatusZone})
	}
	for _, s := range [...]string{updated, estimate} {
		if s == "" {
			continue
		}
		if len(right) > 0 {
			right = append(right, seg{pad: 2, tone: toneMuted})
		}
		right = append(right, seg{text: s, tone: toneMuted})
	}
	return left, right
}

func cell(glyph, name string) string {
	switch {
	case glyph == "":
		return name
	case name == "":
		return glyph
	}
	return glyph + " " + name
}

// minWho is how narrow the assignee is truncated before the due date and the
// subtasks are given up for it.
const minWho = 6

// meta is the assignee's line. It gives up the labels and versions first, then
// the priority, then truncates the assignee; below minWho it drops the
// subtasks, then the due date, and truncates what is left.
func (r *renderer) meta(tail bool) []seg {
	f := r.f
	labels := ""
	if tail {
		labels = r.labelText()
	}
	priority, subtasks, due := f.Priority, f.Subtasks, f.Due
	for step := 0; ; step++ {
		segs := r.metaCells(f.Assignee, priority, due, subtasks, labels)
		w := segWidth(segs)
		if w <= r.width {
			return segs
		}
		switch step {
		case 0:
			labels = ""
		case 1:
			priority = ""
		default:
			if f.Assignee != "" {
				who := ansi.StringWidth(f.Assignee)
				if room := r.width - (w - who); room >= min(minWho, who) || (subtasks == "" && due == "") {
					return r.metaCells(ansi.Truncate(f.Assignee, max(room, 0), r.ell), priority, due, subtasks, labels)
				}
			}
			switch {
			case subtasks != "":
				subtasks = ""
			case due != "":
				due = ""
			default:
				return fit(segs, r.width, r.ell)
			}
		}
	}
}

func (r *renderer) metaCells(who, priority, due, subtasks, labels string) []seg {
	segs := r.buf[:0]
	sep := r.fr.Glyphs.Separator
	dueTone := toneMuted
	if r.f.Overdue {
		dueTone = toneWarn
	}
	for _, s := range [...]seg{
		{text: who, zone: r.f.WhoZone},
		{text: priority},
		{text: due, tone: dueTone},
		{text: subtasks, tone: toneMuted},
		{text: labels, tone: toneMuted},
	} {
		if s.text == "" {
			continue
		}
		if len(segs) > 0 {
			segs = append(segs, seg{pad: 1, tone: toneMuted}, seg{text: sep, tone: toneMuted}, seg{pad: 1, tone: toneMuted})
		}
		segs = append(segs, s)
	}
	return segs
}

func (r *renderer) labelText() string {
	labels := strings.Join(r.f.Labels, " ")
	versions := strings.Join(r.f.FixVersions, ", ")
	switch {
	case labels == "":
		return versions
	case versions == "":
		return labels
	}
	return labels + " " + r.fr.Glyphs.Separator + " " + versions
}

func (r *renderer) extra() []seg {
	if r.f.ParentKey != "" {
		r.buf[0] = seg{text: r.f.ParentKey, tone: toneMuted}
		r.buf[1] = seg{pad: 1, tone: toneMuted}
		r.buf[2] = seg{text: r.f.ParentSummary, tone: toneMuted}
		return r.buf[:3]
	}
	return r.one(r.labelText(), toneMuted)
}

func fit(segs []seg, width int, ell string) []seg {
	w := segWidth(segs)
	for len(segs) > 0 && w > width {
		last := &segs[len(segs)-1]
		lw := last.width()
		room := width - (w - lw)
		if last.pad == 0 && room > 0 && (room > ansi.StringWidth(ell) || len(segs) == 1) {
			last.text = ansi.Truncate(last.text, room, ell)
			return segs
		}
		segs = segs[:len(segs)-1]
		w -= lw
	}
	return segs
}

// wrap splits a summary over two lines at a space where it can, and cuts a
// word only when it is wider than the line. The second line ends in the
// ellipsis when the summary does not fit in two.
func wrap(s string, width int, ell string) (first, second string) {
	if width <= 0 {
		return "", ""
	}
	head := ansi.Truncate(s, width, "")
	if len(head) == len(s) {
		return s, ""
	}
	cut := len(head)
	if s[cut] != ' ' {
		if at := strings.LastIndexByte(head, ' '); at > 0 {
			cut = at
		}
	}
	first = strings.TrimRight(s[:cut], " ")
	second = ansi.Truncate(strings.TrimLeft(s[cut:], " "), width, ell)
	return first, second
}

const spaces = "                                                                                "

func writePad(b *strings.Builder, n int) {
	for n > 0 {
		k := min(n, len(spaces))
		b.WriteString(spaces[:k])
		n -= k
	}
}

func (r *renderer) line(at, n int, segs []seg) string {
	edge := r.fr.Glyphs.VLine
	switch at {
	case 0:
		edge = r.fr.Glyphs.CornerTL
	case n - 1:
		edge = r.fr.Glyphs.CornerBL
	}
	var b strings.Builder
	b.Grow(r.fr.Width*4 + 64)
	if r.whole {
		b.WriteString(edge)
	} else {
		r.write(&b, r.cat(), edge)
	}
	if r.fr.Width == 1 {
		return r.finish(b.String())
	}
	b.WriteByte(' ')
	segs = fit(segs, r.width, r.ell)
	w := 0
	for i := 0; i < len(segs); {
		s := segs[i]
		if s.zone != "" {
			w += s.width()
			text := s.text
			if !r.whole {
				text = r.painted(s.tone, text)
			}
			b.WriteString(r.fr.Zones.Mark(s.zone, text))
			i++
			continue
		}
		j := i
		for j < len(segs) && segs[j].zone == "" && (r.whole || segs[j].tone == s.tone) {
			j++
		}
		p := r.sty.paints[s.tone]
		switch {
		case r.whole || s.tone == toneBase:
			for _, run := range segs[i:j] {
				w += run.width()
				b.WriteString(run.text)
				writePad(&b, run.pad)
			}
		case p.ok:
			b.WriteString(p.open)
			for _, run := range segs[i:j] {
				w += run.width()
				b.WriteString(run.text)
				writePad(&b, run.pad)
			}
			b.WriteString(p.close)
		default:
			var plain strings.Builder
			for _, run := range segs[i:j] {
				w += run.width()
				plain.WriteString(run.text)
				writePad(&plain, run.pad)
			}
			b.WriteString(r.sty.style(s.tone).Render(plain.String()))
		}
		i = j
	}
	writePad(&b, r.width-w)
	return r.finish(b.String())
}

func (r *renderer) write(b *strings.Builder, t tone, text string) {
	if p := r.sty.paints[t]; p.ok {
		b.WriteString(p.open)
		b.WriteString(text)
		b.WriteString(p.close)
		return
	}
	b.WriteString(r.sty.style(t).Render(text))
}

func (r *renderer) painted(t tone, text string) string {
	if p := r.sty.paints[t]; p.ok {
		return p.open + text + p.close
	}
	return r.sty.style(t).Render(text)
}

func (r *renderer) finish(s string) string {
	switch {
	case r.st.Held:
		return r.sty.Held.Render(s)
	case r.st.Selected:
		return r.sty.Selected.Render(s)
	}
	return s
}

func sanitized(f Facts) Facts {
	for _, p := range [...]*string{
		&f.Key, &f.Summary, &f.TypeGlyph, &f.TypeName, &f.StatusGlyph, &f.StatusName,
		&f.Assignee, &f.Priority, &f.Updated, &f.Estimate,
		&f.ParentKey, &f.ParentSummary, &f.Due, &f.Subtasks,
	} {
		*p = oneLine(widget.Sanitize(*p))
	}
	if needsCopy(f.Labels) {
		f.Labels = cleanAll(f.Labels)
	}
	if needsCopy(f.FixVersions) {
		f.FixVersions = cleanAll(f.FixVersions)
	}
	return f
}

func oneLine(s string) string {
	if !strings.ContainsAny(s, "\r\n\v\f") {
		return strings.TrimSpace(s)
	}
	return strings.Join(strings.Fields(s), " ")
}

func needsCopy(in []string) bool {
	for _, s := range in {
		if oneLine(widget.Sanitize(s)) != s {
			return true
		}
	}
	return false
}

func cleanAll(in []string) []string {
	out := make([]string, len(in))
	for i, s := range in {
		out[i] = oneLine(widget.Sanitize(s))
	}
	return out
}
