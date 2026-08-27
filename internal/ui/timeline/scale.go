package timeline

import (
	"strconv"
	"time"

	"github.com/varijkapil13/saral/pkg/jira"
)

// Zoom is how much calendar one column of the chart covers.
type Zoom int

// The zoom levels, finest first. Stepping past either end stays where it is.
const (
	ZoomDay Zoom = iota
	ZoomWeek
	ZoomMonth
	ZoomQuarter
	zoomCount
)

// String names the zoom the way the footer and the summary line spell it.
func (z Zoom) String() string {
	switch z {
	case ZoomWeek:
		return "week"
	case ZoomMonth:
		return "month"
	case ZoomQuarter:
		return "quarter"
	default:
		return "day"
	}
}

func (z Zoom) in() Zoom  { return max(z-1, ZoomDay) }
func (z Zoom) out() Zoom { return min(z+1, zoomCount-1) }

// axis maps a calendar date to a chart column and back. It is comparable, so a
// row memoized under one is invalidated by a zoom or a change of span.
type axis struct {
	zoom   Zoom
	origin jira.Date
	cols   int
}

// newAxis covers from to to at the given zoom, snapping the origin down to the
// start of the period so that a column boundary is a calendar boundary.
func newAxis(zoom Zoom, from, to jira.Date) axis {
	if from.IsZero() || to.IsZero() {
		return axis{zoom: zoom}
	}
	if to.Before(from) {
		from, to = to, from
	}
	a := axis{zoom: zoom, origin: snap(from, zoom)}
	a.cols = a.col(to) + 1
	return a
}

func (a axis) empty() bool { return a.cols <= 0 }

// col is the column a date falls in. It is not clamped: a caller asking about a
// date outside the span needs to know which side it went off.
func (a axis) col(d jira.Date) int {
	if a.origin.IsZero() || d.IsZero() {
		return 0
	}
	switch a.zoom {
	case ZoomWeek:
		return floorDiv(daysBetween(a.origin, d), 7)
	case ZoomMonth:
		return monthsBetween(a.origin, d)
	case ZoomQuarter:
		return floorDiv(monthsBetween(a.origin, d), 3)
	default:
		return daysBetween(a.origin, d)
	}
}

func (a axis) start(col int) jira.Date {
	if a.origin.IsZero() {
		return jira.Date{}
	}
	at := a.origin.In(time.UTC)
	switch a.zoom {
	case ZoomWeek:
		return jira.DateOf(at.AddDate(0, 0, col*7))
	case ZoomMonth:
		return jira.DateOf(at.AddDate(0, col, 0))
	case ZoomQuarter:
		return jira.DateOf(at.AddDate(0, col*3, 0))
	default:
		return jira.DateOf(at.AddDate(0, 0, col))
	}
}

// heads reports whether a column begins the coarser period the heading line
// names — a month under the day and week zooms, a year under the other two.
func (a axis) heads(col int) bool {
	if col == 0 {
		return true
	}
	now, before := a.start(col), a.start(col-1)
	switch a.zoom {
	case ZoomMonth, ZoomQuarter:
		return now.Year != before.Year
	default:
		return now.Month != before.Month || now.Year != before.Year
	}
}

// heading is what the heading line writes at a column that heads a period. It
// is short enough to fit between two of them: a month is four or five columns
// wide at the week zoom, so the year goes on the summary line rather than here.
func (a axis) heading(col int) string {
	at := a.start(col)
	switch a.zoom {
	case ZoomMonth, ZoomQuarter:
		return strconv.Itoa(at.Year)
	case ZoomWeek:
		return at.In(time.UTC).Format("Jan")
	default:
		return at.In(time.UTC).Format("Jan 2006")
	}
}

// snap moves a date back to the first day of the period it is in, which is what
// makes a week column start on a Monday and a quarter column in January, April,
// July or October wherever the span happens to begin.
func snap(d jira.Date, z Zoom) jira.Date {
	switch z {
	case ZoomWeek:
		at := d.In(time.UTC)
		back := (int(at.Weekday()) + 6) % 7
		return jira.DateOf(at.AddDate(0, 0, -back))
	case ZoomMonth:
		return jira.Date{Year: d.Year, Month: d.Month, Day: 1}
	case ZoomQuarter:
		return jira.Date{Year: d.Year, Month: time.Month((int(d.Month)-1)/3*3 + 1), Day: 1}
	default:
		return d
	}
}

func daysBetween(a, b jira.Date) int { return dayNumber(b) - dayNumber(a) }

// dayNumber is a date's day count from a fixed epoch, computed from the calendar
// rather than by subtracting two instants: a time.Duration is 64 bits of
// nanoseconds and overflows at about 292 years, so a span longer than that came
// back as a number that put every bar in the wrong column or in none.
func dayNumber(d jira.Date) int {
	y, m := d.Year, int(d.Month)
	if m <= 2 {
		y--
	}
	era := floorDiv(y, 400)
	yoe := y - era*400
	mp := (m + 9) % 12
	doy := (153*mp+2)/5 + d.Day - 1
	doe := yoe*365 + yoe/4 - yoe/100 + doy
	return era*146097 + doe - 719468
}

func monthsBetween(a, b jira.Date) int {
	return (b.Year-a.Year)*12 + int(b.Month) - int(a.Month)
}

// floorDiv rounds towards minus infinity, so a date before the origin lands in a
// negative column rather than in column zero with everything else.
func floorDiv(n, d int) int {
	q := n / d
	if n%d != 0 && (n < 0) != (d < 0) {
		q--
	}
	return q
}
