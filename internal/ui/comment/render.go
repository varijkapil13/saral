package comment

import (
	"strconv"
	"strings"
	"time"

	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/varijkapil13/saral/internal/ui/kernel"
	"github.com/varijkapil13/saral/internal/ui/richtext"
	"github.com/varijkapil13/saral/internal/ui/widget"
	"github.com/varijkapil13/saral/pkg/adf"
	"github.com/varijkapil13/saral/pkg/jira"
)

const (
	// marker is the gutter the selected comment's arrow sits in.
	marker = 2
	// indent is how far a comment's own words sit in from the gutter.
	indent = 4
	// minBody is the narrowest column a comment's own words are laid out in. A
	// sidebar can be dragged narrower than this; the body then goes past the box
	// and is panned like any other line too wide for it.
	minBody = 8
	// promptWords is how much of a comment the delete confirmation quotes back.
	promptWords = 40
)

// styles are this view's styles, built once per theme generation because
// constructing a lipgloss.Style is the expensive half of drawing a line.
type styles struct {
	gen      int
	author   lipgloss.Style
	muted    lipgloss.Style
	title    lipgloss.Style
	selected lipgloss.Style
	danger   lipgloss.Style
	warning  lipgloss.Style
	action   lipgloss.Style
	rule     lipgloss.Style

	// body and marks are what a comment's words are drawn with. They are built
	// with the rest of the styles because NewStyles walks the whole palette, and
	// nothing may do that inside a render.
	body  richtext.Styles
	marks richtext.Markers
}

func newStyles(t *kernel.Theme) *styles {
	marks := richtext.UnicodeMarkers()
	if asciiMode(t) {
		marks = richtext.ASCIIMarkers()
	}
	return &styles{
		gen:      t.Gen,
		author:   t.Accent,
		muted:    t.Muted,
		title:    t.Title,
		selected: t.Selected,
		danger:   t.Danger,
		warning:  t.Warning,
		action:   t.HintKey,
		rule:     t.Muted,
		body: richtext.NewStyles(richtext.Palette{
			Base:    t.Base,
			Muted:   t.Muted,
			Title:   t.Title,
			Accent:  t.Accent,
			Danger:  t.Danger,
			Warning: t.Warning,
			Success: t.Success,
			// The theme pads a badge and the renderer measures the run rather
			// than the cells a token adds around it, so a padded one would lay
			// out a cell wider than it reports.
			Badge: t.Badge.UnsetPadding(),
			Color: t.Color,
		}),
		marks: marks,
	}
}

// asciiMode reports whether the theme's glyph set is the ASCII fallback, which
// is what pkg/adf and the display renderer need in order to pick their own
// markers. The theme carries the glyphs rather than the name of the set they
// came from, so the set is identified by one of its members.
func asciiMode(t *kernel.Theme) bool {
	return t.Glyphs.Ellipsis == kernel.ASCIIGlyphs().Ellipsis
}

// blockKey is everything one comment's rendering depends on, so that a block
// memoized under it is invalidated by any change that would redraw it.
type blockKey struct {
	id       string
	updated  int64
	width    int
	gen      int
	selected bool
}

// blocks is a bounded memo of rendered comments. Past its limit it is emptied
// rather than evicted one at a time: a scroll invalidates a screenful at once
// anyway, and clearing keeps the map's capacity.
type blocks struct {
	made  map[blockKey]*block
	limit int
}

func newBlocks(limit int) *blocks {
	return &blocks{made: make(map[blockKey]*block, limit), limit: limit}
}

func (b *blocks) get(k blockKey) (*block, bool) {
	made, ok := b.made[k]
	return made, ok
}

func (b *blocks) put(k blockKey, made *block) {
	if len(b.made) >= b.limit {
		clear(b.made)
	}
	b.made[k] = made
}

func (b *blocks) reset() { clear(b.made) }

// block is one comment laid out at one width: its lines, how wide each of them
// is, and the window last cut out of them.
//
// The widths are kept because the display renderer reports them while it builds
// each line, and a line wider than the box is the one thing the pane cannot draw
// as it stands. Re-measuring them per frame is what docs/PERFORMANCE.md names as
// the third cost in a program of this shape.
type block struct {
	lines  []string
	widths []int
	width  int
	wide   int

	// shown is lines with the mouse zone marked and every over-wide line cut to
	// the box, at the pan in panAt. Marking allocates, so it is done once per
	// pan rather than once per frame.
	shown []string
	panAt int
}

// pan is how far this block is actually panned. A block whose every line fits
// does not move when the pane is panned: the lines too wide for the box are the
// only reason to pan, and sliding the rest out of view would take the author and
// the date of the comment being read with them.
func (b *block) pan(want int) int {
	if b.wide <= b.width {
		return 0
	}
	return want
}

// window is the block as the box shows it, with the blank line that separates
// one comment from the next on the end of it — outside the zone, so that a click
// between two comments belongs to neither.
func (b *block) window(want int, ell string, z widget.Zoner, name string) []string {
	at := b.pan(want)
	if b.shown != nil && b.panAt == at {
		return b.shown
	}
	lines := b.lines
	if b.wide > b.width {
		cut := make([]string, len(b.lines))
		for i, line := range b.lines {
			if b.widths[i] <= b.width {
				cut[i] = line
				continue
			}
			cut[i] = window(line, at, b.width, ell)
		}
		lines = cut
	}
	marked := z.MarkLines(name, lines)
	shown := make([]string, 0, len(marked)+1)
	shown = append(shown, marked...)
	shown = append(shown, "")
	b.shown, b.panAt = shown, at
	return b.shown
}

// window is the part of one line the box shows, with a marker wherever something
// has been cut off. Nothing is cut silently: the display renderer leaves a code
// line and a grid row at their own width by design — wrapping code corrupts what
// a reader is about to copy — so the pane says where a line continues and the
// pan keys reach it.
func window(line string, from, width int, ell string) string {
	if from > 0 {
		line = ell + ansi.TruncateLeft(line, from+ansi.StringWidth(ell), "")
	}
	return pad(ansi.Truncate(line, width, ell), width)
}

// renderBlock lays one comment out: who wrote it and when, whatever restricts
// it, and its body drawn through the display renderer rather than written out as
// markdown, which is a serialisation for editing and puts ## and ** on screen.
//
// Every line that fits is padded to the full width so that the mouse zone around
// the block is the rectangle the block occupies rather than the shape of its last
// line. The blank line that separates one comment from the next is added
// outside, so that it falls outside the zone.
func renderBlock(c *jira.Comment, width int, selected bool, st *styles, t *kernel.Theme, loc *time.Location) *block {
	body := richtext.Render(c.Body, richtext.Options{
		Width:    max(width-indent, minBody),
		Location: loc,
		Styles:   st.body,
		Markers:  st.marks,
	})
	b := &block{
		lines:  make([]string, 0, len(body.Lines)+1),
		widths: make([]int, 0, len(body.Lines)+1),
		width:  width,
		wide:   width,
		panAt:  -1,
	}

	meta := metaLine(c, st, t, loc)
	head := strings.Repeat(" ", marker) + meta
	if selected {
		head = t.Glyphs.Collapsed + " " + meta
	}
	headW := ansi.StringWidth(head)
	head = pad(head, width)
	if selected {
		head = st.selected.Render(head)
	}
	b.add(head, headW)

	gutter := strings.Repeat(" ", indent)
	for i, line := range body.Lines {
		b.add(pad(gutter+line, width), indent+body.Widths[i])
	}
	if len(b.lines) == 1 {
		b.add(pad(gutter+st.muted.Render("(empty)"), width), width)
	}
	return b
}

func (b *block) add(line string, width int) {
	b.lines = append(b.lines, line)
	b.widths = append(b.widths, width)
	b.wide = max(b.wide, width)
}

// metaLine names the author, when the comment was written, whether it has been
// edited since, and what restricts it — a restriction is the one property of a
// comment that changes who else can read what is being written about them.
func metaLine(c *jira.Comment, st *styles, t *kernel.Theme, loc *time.Location) string {
	sep := " " + t.Glyphs.Separator + " "
	parts := []string{st.author.Render(authorName(c))}
	if when := formatWhen(c.Created, loc); when != "" {
		parts = append(parts, st.muted.Render(when))
	}
	if edited := formatWhen(c.Updated, loc); edited != "" && !c.Updated.Equal(c.Created) {
		parts = append(parts, st.muted.Render("edited "+edited))
	}
	if label := visibilityLabel(c.Visibility); label != "" {
		parts = append(parts, st.warning.Render(label))
	}
	return strings.Join(parts, sep)
}

// visibilityLabel says who can read a restricted comment, and "" for one
// anybody on the issue can read.
func visibilityLabel(v *jira.Visibility) string {
	if v == nil {
		return ""
	}
	switch {
	case v.Type == "" && v.Value == "":
		return "restricted"
	case v.Type == "":
		return "only " + v.Value
	case v.Value == "":
		return "only one " + v.Type
	default:
		return "only the " + v.Value + " " + v.Type
	}
}

func authorName(c *jira.Comment) string {
	if strings.TrimSpace(c.Author.DisplayName) == "" {
		return "Someone"
	}
	return c.Author.DisplayName
}

// formatWhen renders an instant in the Jira account's timezone rather than the
// machine's, which is what Capabilities.Location carries it for.
func formatWhen(t time.Time, loc *time.Location) string {
	if t.IsZero() {
		return ""
	}
	if loc == nil {
		loc = time.UTC
	}
	return t.In(loc).Format("02 Jan 2006 15:04")
}

func pad(s string, width int) string {
	if got := ansi.StringWidth(s); got < width {
		return s + strings.Repeat(" ", width-got)
	}
	return s
}

// headKey is everything the header is built from, so that the line is rebuilt
// when one of them moves and never once per frame.
type headKey struct {
	issue      string
	width, gen int
	comments   int
	rule       bool
	loaded     bool
	more       bool
	selected   bool
}

func (m *Model) headKey(rows int) headKey {
	return headKey{
		issue: m.issue, width: m.width, gen: m.styles.gen,
		comments: len(m.comments), rule: rows > 1, loaded: m.loaded,
		more: m.page.HasMore(), selected: m.selected() != nil,
	}
}

// headLines is the identity line above the thread and, where the box has a row
// to spare for it, the rule under it. The actions on the right are mouse zones,
// because docs/UX.md asks that nothing be keyboard-only.
func (m *Model) headLines(rows int) []string {
	key := m.headKey(rows)
	if m.head != nil && key == m.headAt {
		return m.head
	}
	m.head, m.headAt = m.buildHead(rows), key
	return m.head
}

func (m *Model) buildHead(rows int) []string {
	if rows <= 0 {
		return nil
	}
	t := m.deps.Theme
	label := m.issue
	if label == "" {
		label = "Comments"
	} else {
		label += " " + t.Glyphs.Separator + " " + m.countLabel()
	}

	actions := m.actionBar()
	actionsW := ansi.StringWidth(actions)
	// A box too narrow for both keeps the identity and drops the actions rather
	// than cutting the issue key out of the line: the footer advertises the same
	// three, and its row is clickable too.
	if m.width < ansi.StringWidth(label)+actionsW+2 {
		actions, actionsW = "", 0
	}
	room := m.width
	if actionsW > 0 {
		room = max(m.width-actionsW-2, minBody)
	}
	left := m.styles.title.Render(ansi.Truncate(label, room, t.Glyphs.Ellipsis))
	gap := max(m.width-ansi.StringWidth(left)-actionsW, 0)
	head := make([]string, 0, 2)
	head = append(head, left+strings.Repeat(" ", gap)+actions)
	if rows < 2 {
		return head
	}
	return append(head, m.styles.rule.Render(strings.Repeat(t.Glyphs.HLine, max(m.width, 1))))
}

func (m *Model) countLabel() string {
	switch {
	case !m.loaded:
		return "reading the thread" + m.deps.Theme.Glyphs.Ellipsis
	case len(m.comments) == 0:
		return "no comments"
	case m.page.HasMore():
		return strconv.Itoa(len(m.comments)) + "+ comments"
	case len(m.comments) == 1:
		return "1 comment"
	default:
		return strconv.Itoa(len(m.comments)) + " comments"
	}
}

// actionBar is the three actions, each of them a click target. An action that
// cannot be taken right now is not drawn, which is the same rule the footer
// follows.
func (m *Model) actionBar() string {
	labels := []struct {
		zone, text string
	}{{zoneWrite, "write"}}
	if m.selected() != nil {
		labels = append(labels,
			struct{ zone, text string }{zoneEdit, "edit"},
			struct{ zone, text string }{zoneDelete, "delete"},
		)
	}
	parts := make([]string, 0, len(labels))
	for _, l := range labels {
		parts = append(parts, m.mark(l.zone, m.styles.action.Render(l.text)))
	}
	return strings.Join(parts, "  ")
}

// hint is a pair of labels and the zones they answer to. Each form says the same
// thing in fewer cells than the one before it, so that a 34-column sidebar keeps
// the keys rather than losing the second half of the sentence naming them.
type hint struct{ left, right string }

// pick is the fullest form that fits the room, or the tersest one when none of
// them does. Measuring happens before marking: a zone marker is a private escape
// sequence, so it changes the bytes and not the cells.
func pick(room int, sep string, forms ...hint) hint {
	sepW := ansi.StringWidth(sep)
	for _, f := range forms {
		if ansi.StringWidth(f.left)+sepW+ansi.StringWidth(f.right) <= room {
			return f
		}
	}
	return forms[len(forms)-1]
}

// shortest is the first form that fits the room, or the last one. The forms are
// written longest first and each says the same thing in fewer cells, so a narrow
// box loses a detail rather than the end of a sentence.
func shortest(room int, forms ...string) string {
	for _, f := range forms {
		if ansi.StringWidth(f) <= room {
			return f
		}
	}
	return forms[len(forms)-1]
}

// fit picks the fullest pair that fits and marks each half as its own click
// target.
func (m *Model) fit(width int, sep, leftZone, rightZone string, forms ...hint) string {
	f := pick(max(width-2, 0), sep, forms...)
	line := "  " + m.mark(leftZone, f.left) + sep + m.mark(rightZone, f.right)
	return ansi.Truncate(line, max(width, 1), m.deps.Theme.Glyphs.Ellipsis)
}

// buildPrompt is the confirmation docs/UX.md principle 4 asks for: it names the
// comment that is about to go, in words, and says what answers it. Nothing
// deletes a comment except the key it names.
//
// It is the one thing here that may take a second line. Everything else in the
// box gives way to the width it has been given; a sentence saying what is about
// to be destroyed may not be cut short of saying it, so a box too narrow to hold
// the sentence and the keys side by side puts them one above the other, and only
// then starts giving up the parts of the sentence from the end.
func (m *Model) buildPrompt() []string {
	t := m.deps.Theme
	c := m.pending
	keys := m.fit(m.width, ", ", zoneConfirm, zoneRefuse,
		hint{"y deletes it", "any other key keeps it"},
		hint{"y deletes", "any key keeps"},
		hint{"y", "any"})
	keysW := ansi.StringWidth(keys)

	label := "delete " + authorName(&c) + "'s comment"
	parts := m.promptParts(&c)
	whole := label + strings.Join(parts, "") + "?"
	if room := m.width - keysW; ansi.StringWidth(whole) <= room {
		return []string{pad(m.styles.danger.Render(whole), room) + m.styles.muted.Render(keys)}
	}
	for _, part := range parts {
		if ansi.StringWidth(label+part)+1 <= m.width {
			label += part
		}
	}
	sentence := ansi.Truncate(label+"?", max(m.width, 1), t.Glyphs.Ellipsis)
	return []string{m.styles.danger.Render(sentence), m.styles.muted.Render(keys)}
}

// promptParts are what the confirmation adds to whose comment it is, in the
// order they earn their cells. The opening words come from the display
// renderer's summary rather than from markdown, so a heading is not quoted back
// with its hashes on.
func (m *Model) promptParts(c *jira.Comment) []string {
	parts := make([]string, 0, 3)
	if when := formatWhen(c.Created, m.location()); when != "" {
		parts = append(parts, " from "+when)
	}
	if restricted := visibilityLabel(c.Visibility); restricted != "" {
		parts = append(parts, ", "+restricted)
	}
	if opening := richtext.Summary(c.Body, promptWords); opening != "" {
		parts = append(parts, ", "+strconv.Quote(opening))
	}
	return parts
}

// composerChrome is the composer's one row: which comment is being written on
// the left, and the keys that finish it on the right.
//
// One row and not two, because the row it saves is a row of the draft — which is
// what makes composerHeight's 1+lines the draft's own lines rather than one short
// of them. The keys are the half that must survive a narrow box, so they are
// measured first and the caption takes what they leave.
// chromeKey is everything the composer's chrome row is built from, so the row is
// rebuilt when one of them moves rather than once per keystroke.
type chromeKey struct {
	issue      string
	editing    string
	width, gen int
	sending    bool
}

func (m *Model) chromeKey() chromeKey {
	return chromeKey{
		issue: m.issue, editing: m.editing, width: m.width,
		gen: m.styles.gen, sending: m.sending,
	}
}

func (m *Model) composerChrome() string {
	key := m.chromeKey()
	if m.chrome != "" && key == m.chromeAt {
		return m.chrome
	}
	m.chrome, m.chromeAt = m.buildChrome(), key
	return m.chrome
}

func (m *Model) buildChrome() string {
	keys := m.composerKeys()
	keysW := ansi.StringWidth(keys)
	room := max(m.width-keysW-1, 0)
	caption := ansi.Truncate(m.composerCaption(room), room, m.deps.Theme.Glyphs.Ellipsis)
	gap := max(m.width-ansi.StringWidth(caption)-keysW, 0)
	return m.styles.title.Render(caption) + strings.Repeat(" ", gap) + m.styles.muted.Render(keys)
}

// composerCaption says which comment the composer is on, so that an edit and a
// new comment are never the same pane, in the fullest form the room allows.
//
// A restriction is never one of the details dropped: it is the one property of a
// comment that decides who else can read what is being written about them, so it
// outlives whose comment it is and when they wrote it.
func (m *Model) composerCaption(room int) string {
	if m.editing == "" {
		return shortest(room, "A new comment on "+m.issue, "New on "+m.issue, m.issue)
	}
	who := "Editing " + authorName(&m.pending) + "'s comment"
	dated := who
	if when := formatWhen(m.pending.Created, m.location()); when != "" {
		dated = who + " from " + when
	}
	restricted := visibilityLabel(m.pending.Visibility)
	if restricted == "" {
		return shortest(room, dated, who, "Editing")
	}
	sep := " " + m.deps.Theme.Glyphs.Separator + " "
	stays := ", and it stays that way"
	return shortest(room,
		dated+sep+restricted+stays,
		who+sep+restricted+stays,
		who+sep+restricted,
		"Editing"+sep+restricted,
		restricted)
}

// composerKeys name what finishes the comment. The footer says the same two
// while this mode is live, and this row is where they are clickable.
func (m *Model) composerKeys() string {
	if m.sending {
		return "sending" + m.deps.Theme.Glyphs.Ellipsis
	}
	f := pick(max(m.width/2, 1), "  ",
		hint{"ctrl+s sends", "esc keeps it as a draft"},
		hint{"ctrl+s sends", "esc keeps it"},
		hint{"ctrl+s", "esc"})
	return m.mark(zoneSend, f.left) + "  " + m.mark(zoneCancel, f.right)
}

// alwaysPresent are the entries in pkg/adf's list that name prose details
// rather than a construct anybody would recognise on screen, and that every
// document has. Warning about them would mean warning about every comment.
var alwaysPresent = map[string]bool{"text": true, "marks": true}

// oneWay names the constructs in a document that markdown alone cannot carry,
// read out of pkg/adf's own list so that this view cannot end up warning about
// something the parser has since learned to keep.
func oneWay(d adf.Doc) []string {
	types := d.NodeTypes()
	if len(types) == 0 {
		return nil
	}
	seen := make(map[string]bool, len(types))
	out := make([]string, 0, 4)
	for _, entry := range adf.ParseMarkdownDropsOnly() {
		name, _, ok := strings.Cut(entry, ":")
		if !ok || seen[name] || alwaysPresent[name] || types[name] == 0 {
			continue
		}
		seen[name] = true
		out = append(out, name)
	}
	return out
}

// list joins names the way a sentence does.
func list(names []string) string {
	switch len(names) {
	case 0:
		return ""
	case 1:
		return names[0]
	default:
		return strings.Join(names[:len(names)-1], ", ") + " and " + names[len(names)-1]
	}
}
