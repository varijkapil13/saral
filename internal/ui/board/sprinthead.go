package board

import (
	"math"
	"strconv"
	"strings"
	"time"

	"github.com/varijkapil13/saral/internal/ui/widget"
)

// progressCells is how wide the sprint's progress bar is drawn.
const progressCells = 10

// sprintKey is everything the sprint line is built from. day is the calendar
// day "days left" is counted from, so the line turns over at midnight and not
// on every frame.
type sprintKey struct {
	sprint  int64
	width   int
	gen     int
	dataGen int
	day     int
	more    bool
}

// sprintLines is how many lines the sprint's own header takes: one while a
// running sprint is on screen, none otherwise.
func (m *Model) sprintLines() int {
	if m.ready && m.sprint.ID != 0 {
		return 1
	}
	return 0
}

// sprintLine names the sprint's goal, how long it has left and how much of it
// is done. Done is the board's last mapped column, never a status category: a
// board whose last column is not the done-category one disagrees with it
// (docs/API-NOTES.md), and this is the board's own sprint.
func (m *Model) sprintLine() string {
	today := m.now().In(m.deps.Caps.Location())
	key := sprintKey{
		sprint: m.sprint.ID, width: m.width, gen: m.styles.gen, dataGen: m.dataGen,
		day: today.Year()*1000 + today.YearDay(), more: m.more,
	}
	if m.sprintHead != "" && key == m.sprintAt {
		return m.sprintHead
	}
	left := "  No goal set for this sprint"
	if goal := strings.TrimSpace(widget.Sanitize(m.sprint.Goal)); goal != "" {
		left = "  Goal: " + goal
	}
	parts := make([]string, 0, 2)
	if left := m.daysLeft(today); left != "" {
		parts = append(parts, left)
	}
	parts = append(parts, m.progress())
	right := strings.Join(parts, " "+m.deps.Theme.Glyphs.Separator+" ") + "  "
	m.sprintHead = m.twoCells(left, right, m.styles.base, m.styles.muted)
	m.sprintAt = key
	return m.sprintHead
}

// daysLeft counts calendar days in the site's time zone, which is the zone the
// sprint's end was set in.
func (m *Model) daysLeft(today time.Time) string {
	if m.sprint.End == nil {
		return ""
	}
	end := m.sprint.End.In(today.Location())
	days := dayNumber(end) - dayNumber(today)
	switch {
	case days > 1:
		return strconv.Itoa(days) + " days left"
	case days == 1:
		return "1 day left"
	case days == 0:
		return "ends today"
	case days == -1:
		return "ended yesterday"
	default:
		return "ended " + strconv.Itoa(-days) + " days ago"
	}
}

func dayNumber(t time.Time) int {
	y, mo, d := t.Date()
	return int(time.Date(y, mo, d, 0, 0, 0, 0, time.UTC).Unix() / 86400)
}

// progress is the bar and the numbers behind it: the board's estimate where it
// estimates and any card carries one, the count of cards otherwise.
func (m *Model) progress() string {
	done, total, unit := m.sprintDone()
	g := m.deps.Theme.Glyphs
	filled := 0
	if total > 0 {
		filled = int(math.Round(progressCells * done / total))
	}
	filled = min(max(filled, 0), progressCells)
	bar := strings.Repeat(g.ProgressOn, filled) + strings.Repeat(g.ProgressNo, progressCells-filled)
	of := trimNumber(total)
	if m.more {
		of += "+"
	}
	return bar + " " + trimNumber(done) + " of " + of + " " + unit + " done"
}

func (m *Model) sprintDone() (done, total float64, unit string) {
	last := -1
	for c := len(m.plan.columns) - 1; c >= 0; c-- {
		if len(m.plan.columns[c].statuses) > 0 {
			last = c
			break
		}
	}
	var cards, doneCards int
	var points, donePoints float64
	for i := range m.issues {
		at, mapped := m.plan.columnOf(m.issues[i].Status.ID)
		if !mapped {
			continue
		}
		cards++
		if at == last {
			doneCards++
		}
		if !m.plan.estimates {
			continue
		}
		if n, ok := m.issues[i].Fields.Number(m.plan.estimate); ok {
			points += n
			if at == last {
				donePoints += n
			}
		}
	}
	if points > 0 {
		unit = strings.TrimSpace(widget.Sanitize(m.plan.estimate.Name))
		if unit == "" {
			unit = "estimated"
		}
		return donePoints, points, unit
	}
	unit = "issues"
	if cards == 1 {
		unit = "issue"
	}
	return float64(doneCards), float64(cards), unit
}
