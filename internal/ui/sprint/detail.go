package sprint

import (
	"strings"

	"github.com/charmbracelet/x/ansi"

	"github.com/varijkapil13/saral/internal/ui/widget"
	"github.com/varijkapil13/saral/pkg/jira"
)

// detailKey is everything the detail block is drawn from. The day is in it
// because days left moves at midnight whether or not anything else does.
type detailKey struct {
	id                   int64
	name, goal           string
	state                jira.SprintState
	start, end, complete int64
	width, gen, pver     int
	day                  jira.Date
	counting             bool
}

func (m *Model) detailKey(sp jira.Sprint) detailKey {
	day := jira.Date{}
	if m.deps.Now != nil {
		day = jira.DateOf(m.deps.Now().In(m.deps.Caps.Location()))
	}
	return detailKey{
		id: sp.ID, name: sp.Name, goal: sp.Goal, state: sp.State,
		start: unixOr(sp.Start), end: unixOr(sp.End), complete: unixOr(sp.Complete),
		width: m.width, gen: m.styles.gen, pver: m.pver, day: day, counting: m.counting,
	}
}

// detailSprint is the running sprint the block describes: the one under the
// cursor when it is running, else the first running one on the list. Keying it
// on the running sprint rather than the cursor keeps a scroll from rebuilding it.
func (m *Model) detailSprint() (jira.Sprint, bool) {
	if sp := m.selected(); sp.State == jira.SprintActive {
		return sp, true
	}
	for i := range m.sprints {
		if m.sprints[i].State == jira.SprintActive {
			return m.sprints[i], true
		}
	}
	return jira.Sprint{}, false
}

// detailLines describe a running sprint: its name, its dates in the account's
// zone and how long it has left, its goal, and how much of it is done.
func (m *Model) detailLines() []string {
	sp, _ := m.detailSprint()
	key := m.detailKey(sp)
	if held, ok := m.details.Get(key); ok {
		return held
	}
	room := max(m.width, 8)
	ell := m.deps.Theme.Glyphs.Ellipsis
	sep := " " + m.deps.Theme.Glyphs.Separator + " "
	fit := func(s string) string { return ansi.Truncate(s, room, ell) }

	zone, _ := m.deps.Caps.Zone()
	var b strings.Builder
	b.WriteString("  ")
	b.WriteString(widget.Sanitize(nameOr(sp.Name)))
	b.WriteString(sep)
	b.WriteString(m.datesOf(sp))
	b.WriteString(" (")
	b.WriteString(zone.String())
	b.WriteString(")")
	if sp.End != nil && m.deps.Now != nil {
		b.WriteString(sep)
		b.WriteString(daysLeft(*sp.End, m.deps.Now(), m.deps.Caps.Location()))
	}

	goal := "  No goal."
	if g := strings.TrimSpace(widget.Sanitize(sp.Goal)); g != "" {
		goal = "  Goal: " + g
	}

	lines := []string{
		m.styles.accent.Render(fit(b.String())),
		m.styles.muted.Render(fit(goal)),
		m.styles.muted.Render(fit("  " + m.standing(sp))),
	}
	m.details.Put(key, lines)
	return lines
}

// standing is the third line: how much of the sprint is done.
func (m *Model) standing(sp jira.Sprint) string {
	p, ok := m.progress[sp.ID]
	switch {
	case ok && p.err != nil:
		reason, _ := jira.Reason(p.err)
		return "The issues in it could not be counted: " + reason
	case ok:
		return p.words()
	case m.counting:
		return "Counting the issues in it" + m.deps.Theme.Glyphs.Ellipsis
	}
	return "Its issues have not been counted."
}
