package issue

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

const labelWidth = 13

// styles are the detail view's styles, built once per theme generation.
type styles struct {
	gen     int
	key     lipgloss.Style
	title   lipgloss.Style
	muted   lipgloss.Style
	section lipgloss.Style
	author  lipgloss.Style
	rule    lipgloss.Style
}

func newStyles(t *kernel.Theme) *styles {
	return &styles{
		gen:     t.Gen,
		key:     t.Accent,
		title:   t.Title,
		muted:   t.Muted,
		section: t.Title,
		author:  t.Accent,
		rule:    t.Muted,
	}
}

// asciiMode reports whether the theme's glyph set is the ASCII fallback, which
// is what pkg/adf needs to know to pick its own markers. The theme carries the
// glyphs rather than the name of the set they came from, so the set is
// identified by one of its members.
func asciiMode(t *kernel.Theme) bool {
	return t.Glyphs.Ellipsis == kernel.ASCIIGlyphs().Ellipsis
}

// header is the two fixed lines above the pager plus a rule: the identity of
// the issue stays put while its body scrolls.
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
		m.issue.Status.Name,
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

	return key + "  " + title + "\n" + meta + "\n" + m.styles.rule.Render(strings.Repeat(t.Glyphs.HLine, max(m.width, 1)))
}

// body is what the pager scrolls: the description, the fields worth reading and
// the comment thread. It is built when the data or the width changes, never per
// frame.
func (m *Model) body(width int) string {
	t := m.deps.Theme
	var b strings.Builder

	desc := strings.TrimRight(adf.MarkdownWith(m.issue.Description, adf.Options{
		TableWidth: width,
		ASCII:      asciiMode(t),
		Location:   m.location(),
	}), "\n")
	if desc == "" {
		desc = m.styles.muted.Render("No description.")
	}
	b.WriteString(desc)

	if details := m.details(); details != "" {
		b.WriteString("\n\n")
		b.WriteString(m.styles.section.Render("Details"))
		b.WriteString("\n")
		b.WriteString(details)
	}

	b.WriteString("\n\n")
	b.WriteString(m.styles.section.Render(m.commentHeading()))
	b.WriteString("\n")
	b.WriteString(m.thread(width))
	return b.String()
}

func (m *Model) commentHeading() string {
	switch {
	case !m.loadedComments:
		return "Comments"
	case len(m.comments) == commentCap:
		return "Comments (" + strconv.Itoa(commentCap) + "+)"
	default:
		return "Comments (" + strconv.Itoa(len(m.comments)) + ")"
	}
}

func (m *Model) thread(width int) string {
	if !m.loadedComments {
		return m.styles.muted.Render("  Reading the thread" + m.deps.Theme.Glyphs.Ellipsis)
	}
	if len(m.comments) == 0 {
		return m.styles.muted.Render("  Nobody has commented.")
	}
	var b strings.Builder
	for i := range m.comments {
		c := &m.comments[i]
		if i > 0 {
			b.WriteString("\n")
		}
		b.WriteString("  ")
		b.WriteString(m.styles.author.Render(authorName(c)))
		b.WriteString(m.styles.muted.Render(" " + m.deps.Theme.Glyphs.Separator + " " + formatWhen(c.Created, m.location())))
		if c.Visibility != nil {
			b.WriteString(m.styles.muted.Render(" " + m.deps.Theme.Glyphs.Separator + " " + c.Visibility.Type + " " + c.Visibility.Value))
		}
		b.WriteString("\n")
		b.WriteString(indent(adf.MarkdownWith(c.Body, adf.Options{
			TableWidth: max(width-4, 8),
			ASCII:      asciiMode(m.deps.Theme),
			Location:   m.location(),
		}), "    "))
		b.WriteString("\n")
	}
	return strings.TrimRight(b.String(), "\n")
}

// details lists the fields a reader actually looks for, and nothing that is
// empty: a column of dashes reads as data.
func (m *Model) details() string {
	iss := &m.issue
	rows := make([][2]string, 0, 12)
	add := func(label, value string) {
		if strings.TrimSpace(value) != "" {
			rows = append(rows, [2]string{label, value})
		}
	}
	add("Reporter", userName(iss.Reporter))
	add("Project", projectName(iss.Project))
	add("Resolution", resolutionName(iss))
	add("Labels", strings.Join(iss.Labels, ", "))
	add("Components", componentNames(iss.Components))
	add("Fix versions", versionNames(iss.FixVersions))
	add("Due", iss.Due.String())
	add("Created", formatWhen(iss.Created, m.location()))
	if iss.Parent != nil {
		add("Parent", iss.Parent.Key+" "+iss.Parent.Summary)
	}
	add("Subtasks", refList(iss.Subtasks))
	add("Links", linkList(iss.Links))
	add("Time", timeTracking(iss.TimeTracking))
	if len(rows) == 0 {
		return ""
	}

	var b strings.Builder
	for i, row := range rows {
		if i > 0 {
			b.WriteString("\n")
		}
		b.WriteString("  ")
		b.WriteString(m.styles.muted.Render(pad(row[0], labelWidth)))
		b.WriteString(row[1])
	}
	return b.String()
}

func indent(s, prefix string) string {
	if s == "" {
		return ""
	}
	lines := strings.Split(strings.TrimRight(s, "\n"), "\n")
	for i, line := range lines {
		if line == "" {
			continue
		}
		lines[i] = prefix + line
	}
	return strings.Join(lines, "\n")
}

func pad(s string, width int) string {
	if got := ansi.StringWidth(s); got < width {
		return s + strings.Repeat(" ", width-got)
	}
	return s + " "
}

func authorName(c *jira.Comment) string {
	if strings.TrimSpace(c.Author.DisplayName) == "" {
		return "Someone"
	}
	return c.Author.DisplayName
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

func refList(in []jira.IssueRef) string {
	out := make([]string, 0, len(in))
	for i := range in {
		out = append(out, in[i].Key)
	}
	return strings.Join(out, ", ")
}

func linkList(in []jira.IssueLink) string {
	out := make([]string, 0, len(in))
	for i := range in {
		label := in[i].Label
		if label == "" {
			label = in[i].Type
		}
		out = append(out, label+" "+in[i].Other.Key)
	}
	return strings.Join(out, ", ")
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
