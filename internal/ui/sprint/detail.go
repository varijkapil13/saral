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

// detailLines describe the sprint under the cursor: its name, its dates in the
// account's zone and how long it has left, its goal, and for a running sprint
// how much of it is done.
func (m *Model) detailLines() []string {
	sp := m.selected()
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
	if sp.State == jira.SprintActive && sp.End != nil && m.deps.Now != nil {
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

// standing is the third line: progress for a running sprint, and what state
// the others are in.
func (m *Model) standing(sp jira.Sprint) string {
	switch sp.State {
	case jira.SprintActive:
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
	case jira.SprintFuture:
		return "Planned, and not started."
	case jira.SprintClosed:
		if sp.Complete != nil {
			return "Closed on " + writeDate(sp.Complete, m.deps.Caps.Location()) + "."
		}
		return "Closed."
	}
	return "In a state this build has no word for: " + stateWord(sp.State) + "."
}
