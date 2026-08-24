package comment

import (
	"strconv"
	"strings"
	"time"

	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/varijkapil13/saral/internal/ui/kernel"
	"github.com/varijkapil13/saral/pkg/adf"
	"github.com/varijkapil13/saral/pkg/jira"
)

const (
	// marker is the gutter the selected comment's arrow sits in.
	marker = 2
	// indent is how far a comment's own words sit in from the gutter.
	indent = 4
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
}

func newStyles(t *kernel.Theme) *styles {
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
	}
}

// asciiMode reports whether the theme's glyph set is the ASCII fallback, which
// is what pkg/adf needs in order to pick its own markers. The theme carries the
// glyphs rather than the name of the set they came from, so the set is
// identified by one of its members.
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
	made  map[blockKey][]string
	limit int
}

func newBlocks(limit int) *blocks {
	return &blocks{made: make(map[blockKey][]string, limit), limit: limit}
}

func (b *blocks) get(k blockKey) ([]string, bool) {
	lines, ok := b.made[k]
	return lines, ok
}

func (b *blocks) put(k blockKey, lines []string) {
	if len(b.made) >= b.limit {
		clear(b.made)
	}
	b.made[k] = lines
}

func (b *blocks) reset() { clear(b.made) }

// renderBlock draws one comment: who wrote it and when, whatever restricts it,
// and its body wrapped to the width it has been given. Every line is padded to
// the full width so that the mouse zone around the block is the rectangle the
// block occupies rather than the shape of its last line. The blank line that
// separates one comment from the next is added outside, so that it falls
// outside the zone.
func renderBlock(c *jira.Comment, width int, selected bool, st *styles, t *kernel.Theme, loc *time.Location) []string {
	body := adf.MarkdownWith(c.Body, adf.Options{
		TableWidth: max(width-indent, 8),
		ASCII:      asciiMode(t),
		Location:   loc,
	})
	lines := make([]string, 0, 8)

	head := metaLine(c, st, t, loc)
	if selected {
		head = st.selected.Render(pad(t.Glyphs.Collapsed+" "+head, width))
	} else {
		head = pad(strings.Repeat(" ", marker)+head, width)
	}
	lines = append(lines, head)

	room := max(width-indent, 8)
	for _, line := range strings.Split(strings.TrimRight(body, "\n"), "\n") {
		for _, wrapped := range strings.Split(ansi.Wrap(line, room, "-"), "\n") {
			lines = append(lines, pad(strings.Repeat(" ", indent)+wrapped, width))
		}
	}
	if len(lines) == 1 {
		lines = append(lines, pad(strings.Repeat(" ", indent)+st.muted.Render("(empty)"), width))
	}
	return lines
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
	loaded     bool
	more       bool
	selected   bool
}

func (m *Model) headKey() headKey {
	return headKey{
		issue: m.issue, width: m.width, gen: m.styles.gen,
		comments: len(m.comments), loaded: m.loaded,
		more: m.page.HasMore(), selected: m.selected() != nil,
	}
}

// header is the identity line above the thread and the rule under it. The
// actions on the right are mouse zones, because docs/UX.md asks that nothing be
// keyboard-only.
func (m *Model) header() string {
	key := m.headKey()
	if m.head != "" && key == m.headAt {
		return m.head
	}
	m.head, m.headAt = m.buildHeader(), key
	return m.head
}

func (m *Model) buildHeader() string {
	t := m.deps.Theme
	actions := m.actionBar()
	room := max(m.width-ansi.StringWidth(actions)-2, 8)

	label := m.issue
	if label == "" {
		label = "Comments"
	} else {
		label += " " + t.Glyphs.Separator + " " + m.countLabel()
	}
	left := m.styles.title.Render(ansi.Truncate(label, room, t.Glyphs.Ellipsis))
	gap := max(m.width-ansi.StringWidth(left)-ansi.StringWidth(actions), 1)
	rule := m.styles.rule.Render(strings.Repeat(t.Glyphs.HLine, max(m.width, 1)))
	return left + strings.Repeat(" ", gap) + actions + "\n" + rule
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

// deletePrompt is the confirmation docs/UX.md principle 4 asks for: it names
// the comment that is about to go, in words, and says what answers it. Nothing
// deletes a comment except the key this line names.
func (m *Model) deletePrompt() string {
	t := m.deps.Theme
	c := m.pending
	label := "delete " + authorName(&c) + "'s comment"
	if when := formatWhen(c.Created, m.location()); when != "" {
		label += " from " + when
	}
	if restricted := visibilityLabel(c.Visibility); restricted != "" {
		label += ", " + restricted
	}
	if first := firstWords(c.Body, 40); first != "" {
		label += ", " + strconv.Quote(first)
	}
	label += "?"
	hint := "  " + m.mark(zoneConfirm, "y deletes it") + ", " + m.mark(zoneRefuse, "any other key keeps it")
	room := max(m.width-ansi.StringWidth(hint), 8)
	return m.styles.danger.Render(ansi.Truncate(label, room, t.Glyphs.Ellipsis)) + m.styles.muted.Render(hint)
}

// firstWords is the opening of a comment, on one line, so that a confirmation
// names the comment the way the person who wrote it would recognise it.
func firstWords(d adf.Doc, width int) string {
	text := strings.TrimSpace(adf.Markdown(d))
	if text == "" {
		return ""
	}
	if at := strings.IndexByte(text, '\n'); at >= 0 {
		text = text[:at]
	}
	return ansi.Truncate(strings.TrimSpace(text), width, "…")
}

// editorCaption says which comment the editor is on, so that an edit and a new
// comment are never the same screen.
func (m *Model) editorCaption() string {
	if m.editing == "" {
		return m.styles.title.Render("A new comment on " + m.issue)
	}
	label := "Editing " + authorName(&m.pending) + "'s comment"
	if when := formatWhen(m.pending.Created, m.location()); when != "" {
		label += " from " + when
	}
	if restricted := visibilityLabel(m.pending.Visibility); restricted != "" {
		label += " " + m.deps.Theme.Glyphs.Separator + " " + restricted + ", and it stays that way"
	}
	return m.styles.title.Render(ansi.Truncate(label, max(m.width, 8), m.deps.Theme.Glyphs.Ellipsis))
}

// editorHint is the line under the editor. It names the keys that finish, since
// the footer cannot: the registry holds one key set per view, not one per mode.
func (m *Model) editorHint() string {
	if m.sending {
		return m.styles.muted.Render("  sending" + m.deps.Theme.Glyphs.Ellipsis)
	}
	return m.styles.muted.Render("  " + m.mark(zoneSend, "ctrl+s sends") + "  " +
		m.mark(zoneCancel, "esc keeps it as a draft"))
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
