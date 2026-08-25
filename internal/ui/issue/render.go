package issue

import (
	"strconv"
	"strings"
	"time"

	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/varijkapil13/saral/internal/ui/kernel"
	"github.com/varijkapil13/saral/internal/ui/richtext"
	"github.com/varijkapil13/saral/pkg/jira"
)

const labelWidth = 13

// styles are the detail view's styles, built once per theme generation. The
// display renderer's styles are built here too: richtext holds no theme, so the
// pane hands it tokens.
type styles struct {
	gen        int
	key        lipgloss.Style
	title      lipgloss.Style
	muted      lipgloss.Style
	label      lipgloss.Style
	section    lipgloss.Style
	rule       lipgloss.Style
	categories [4]lipgloss.Style

	rich    richtext.Styles
	markers richtext.Markers

	// The gutter's two cells, in both states, rendered once: a rail is a cell
	// per row per region, and asking a style to render one is the expensive half
	// of drawing it.
	railOn  [2]string
	railOff [2]string
}

func newStyles(t *kernel.Theme) *styles {
	s := &styles{
		gen:     t.Gen,
		key:     t.Accent,
		title:   t.Title,
		muted:   t.Muted,
		label:   t.Muted,
		section: t.Title,
		rule:    t.Muted,
		rich: richtext.NewStyles(richtext.Palette{
			Base: t.Base, Muted: t.Muted, Title: t.Title, Accent: t.Accent,
			Danger: t.Danger, Warning: t.Warning, Success: t.Success, Badge: t.Badge,
			Color: t.Color,
		}),
		markers: richtext.UnicodeMarkers(),
	}
	if asciiMode(t) {
		s.markers = richtext.ASCIIMarkers()
	}
	s.categories = [4]lipgloss.Style{
		jira.CategoryUnknown:    t.Muted,
		jira.CategoryToDo:       t.Base,
		jira.CategoryInProgress: t.Accent,
		jira.CategoryDone:       t.Success,
	}
	for at, glyph := range [2]string{railTrack: t.Glyphs.VLine, railThumb: t.Glyphs.ProgressOn} {
		s.railOn[at] = t.Accent.Render(glyph)
		s.railOff[at] = t.Muted.Render(glyph)
	}
	return s
}

// category is the colour a status of that category is drawn in, which is what
// keeps a site's own status names legible without knowing any of them.
func (s *styles) category(c jira.StatusCategory) lipgloss.Style {
	if int(c) < 0 || int(c) >= len(s.categories) {
		return s.categories[jira.CategoryUnknown]
	}
	return s.categories[c]
}

// asciiMode reports whether the theme's glyph set is the ASCII fallback, which
// is what the renderer needs to know to pick its own markers. The theme carries
// the glyphs rather than the name of the set they came from, so the set is
// identified by one of its members.
func asciiMode(t *kernel.Theme) bool {
	return t.Glyphs.Ellipsis == kernel.ASCIIGlyphs().Ellipsis
}

// contentKey is everything a region's lines depend on. A memo held under it
// cannot survive a resize, a theme switch, an expand being opened or a fresh
// read of the issue, which is the whole reason it is a key rather than a flag
// somebody has to remember to clear.
type contentKey struct {
	width int
	theme int
	data  int
	folds int
}

// content is one region's lines at one width, with the width of each measured
// while it was built so that drawing a frame never measures anything. widest is
// how far the region can be panned: code is never wrapped and a table is never
// cut, so those two are the lines that reach past the box.
type content struct {
	key    contentKey
	built  bool
	lines  []string
	widths []int
	widest int
}

// header is the identity of the issue: the key and the summary, the facts line
// and a rule. It stays put while the regions under it scroll.
func (m *Model) header() string {
	t := m.deps.Theme
	ell := t.Glyphs.Ellipsis
	sep := " " + t.Glyphs.Separator + " "

	key := m.styles.key.Render(m.issue.Key)
	room := max(m.width-ansi.StringWidth(m.issue.Key)-2, 1)
	title := m.styles.title.Render(ansi.Truncate(m.issue.Summary, room, ell))

	facts := make([]string, 0, 5)
	for _, s := range [...]string{
		m.issue.Type.Name,
		statusLabel(m.issue.Status),
		priorityName(m.issue),
		assigneeName(m.issue, "unassigned"),
	} {
		if s != "" {
			facts = append(facts, s)
		}
	}
	if when := formatWhen(m.issue.Updated, m.location()); when != "" {
		facts = append(facts, "updated "+when)
	}
	meta := m.styles.muted.Render(ansi.Truncate(strings.Join(facts, sep), m.width, ell))

	return key + "  " + title + "\n" + meta + "\n" +
		m.styles.rule.Render(strings.Repeat(t.Glyphs.HLine, max(m.width, 1)))
}

// statusLabel names the status and, where it adds something, the category it
// belongs to. A site's own status names carry no board position — "Building"
// says nothing about whether that counts as started — and the category is the
// half that means the same thing on every site.
func statusLabel(s jira.Status) string {
	if s.Name == "" {
		return ""
	}
	category := s.Category.String()
	if s.Category == jira.CategoryUnknown || strings.EqualFold(category, s.Name) {
		return s.Name
	}
	return s.Name + " (" + strings.ToLower(category) + ")"
}

// View draws the identity header and the regions under it.
func (m *Model) View() string {
	if m.width <= 0 || m.height <= 0 {
		return ""
	}
	m.build()
	m.readThread()

	m.buf = append(m.buf[:0], m.head...)
	buf := m.buf
	for row := range m.lay.paneH {
		buf = append(buf, '\n')
		if !m.lay.wide {
			buf = m.appendRegion(buf, m.focus, row)
			continue
		}
		buf = m.appendRegion(buf, regionDesc, row)
		buf = append(buf, ' ')
		if at := row - m.lay.boxes[regionDetails].h; at >= 0 {
			buf = m.appendRegion(buf, regionComments, at)
			continue
		}
		buf = m.appendRegion(buf, regionDetails, row)
	}
	m.buf = buf
	return string(buf)
}

// descLines renders the description through the display renderer. The markdown
// pkg/adf writes is a serialisation for editing — it backs the $EDITOR handoff
// and does not escape prose — so putting it on screen puts ## and ** there too.
func (m *Model) descLines(width int) content {
	if !m.loadedIssue && len(m.issue.Description.Content) == 0 {
		return m.oneLine(width, "Reading the issue"+m.deps.Theme.Glyphs.Ellipsis)
	}
	r := richtext.Render(m.issue.Description, richtext.Options{
		Width:    width,
		Location: m.location(),
		Styles:   m.styles.rich,
		Markers:  m.styles.markers,
		Open:     m.open,
	})
	m.folds = r.Folds
	if len(r.Lines) == 0 {
		return m.oneLine(width, "No description.")
	}
	m.markFolds(r)
	return content{lines: r.Lines, widths: r.Widths, widest: r.Width()}
}

// markFolds makes each expand's own line clickable. The marker is written into
// the line the renderer reported for the fold, and its id is the fold's index,
// so redrawing reuses it and the manager's map stays as small as the document.
func (m *Model) markFolds(r richtext.Rendered) {
	if !m.zones.Enabled() {
		return
	}
	for _, f := range r.Folds {
		if f.Line < 0 || f.Line >= len(r.Lines) {
			continue
		}
		r.Lines[f.Line] = m.zones.Mark(foldZone(f.Index), r.Lines[f.Line])
	}
}

func foldZone(index int) string { return "fold:" + strconv.Itoa(index) }

// oneLine is a region whose whole content is one muted sentence.
func (m *Model) oneLine(width int, text string) content {
	line := clip(m.styles.muted.Render(text), width, m.deps.Theme.Glyphs.Ellipsis)
	got := ansi.StringWidth(line)
	return content{lines: []string{line}, widths: []int{got}, widest: got}
}

// clip keeps a line inside the box it is drawn in. A code block and a table are
// the two constructs the renderer lets past the width, on the grounds that
// wrapping them loses more than cutting them does.
func clip(s string, width int, ell string) string {
	if ansi.StringWidth(s) <= width {
		return s
	}
	return ansi.Truncate(s, width, ell)
}

func priorityName(iss jira.Issue) string {
	if iss.Priority == nil {
		return ""
	}
	return iss.Priority.Name
}

func resolutionName(iss *jira.Issue) string {
	if iss.Resolution == nil {
		return ""
	}
	return iss.Resolution.Name
}

func assigneeName(iss jira.Issue, fallback string) string {
	if iss.Assignee == nil || strings.TrimSpace(iss.Assignee.DisplayName) == "" {
		return fallback
	}
	return iss.Assignee.DisplayName
}

func userName(u *jira.User) string {
	if u == nil {
		return ""
	}
	return u.DisplayName
}

func projectName(p jira.ProjectRef) string {
	switch {
	case p.Key == "":
		return p.Name
	case p.Name == "":
		return p.Key
	default:
		return p.Key + " " + p.Name
	}
}

func componentNames(in []jira.Component) string {
	names := make([]string, 0, len(in))
	for _, c := range in {
		names = append(names, c.Name)
	}
	return strings.Join(names, ", ")
}

func versionNames(in []jira.Version) string {
	names := make([]string, 0, len(in))
	for i := range in {
		names = append(names, in[i].Name)
	}
	return strings.Join(names, ", ")
}

func timeTracking(t *jira.TimeTracking) string {
	if t == nil {
		return ""
	}
	parts := make([]string, 0, 3)
	for _, p := range [...]struct {
		label string
		secs  int64
	}{{"estimated", t.OriginalEstimate}, {"remaining", t.RemainingEstimate}, {"spent", t.TimeSpent}} {
		if p.secs > 0 {
			parts = append(parts, duration(p.secs)+" "+p.label)
		}
	}
	return strings.Join(parts, ", ")
}

func duration(secs int64) string {
	d := time.Duration(secs) * time.Second
	switch {
	case d >= time.Hour:
		if rem := d % time.Hour; rem != 0 {
			return strconv.FormatInt(int64(d/time.Hour), 10) + "h" + strconv.FormatInt(int64(rem/time.Minute), 10) + "m"
		}
		return strconv.FormatInt(int64(d/time.Hour), 10) + "h"
	case d >= time.Minute:
		return strconv.FormatInt(int64(d/time.Minute), 10) + "m"
	default:
		return strconv.FormatInt(int64(d/time.Second), 10) + "s"
	}
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
