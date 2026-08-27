package timeline

import (
	"testing"
	"time"

	"github.com/varijkapil13/saral/pkg/jira"
)

func TestAxis_MapsADateToAColumnAndBack(t *testing.T) {
	t.Parallel()

	for name, tc := range map[string]struct {
		zoom   Zoom
		from   jira.Date
		to     jira.Date
		origin jira.Date
		cols   int
		at     jira.Date
		col    int
	}{
		"a day is a column": {
			zoom: ZoomDay, from: day(2026, time.March, 2), to: day(2026, time.March, 11),
			origin: day(2026, time.March, 2), cols: 10,
			at: day(2026, time.March, 5), col: 3,
		},
		"a week starts on the monday before the span": {
			// 2026-03-05 is a Thursday; the column it is in starts on the 2nd.
			zoom: ZoomWeek, from: day(2026, time.March, 5), to: day(2026, time.April, 2),
			origin: day(2026, time.March, 2), cols: 5,
			at: day(2026, time.March, 8), col: 0,
		},
		"a week column ends on the sunday": {
			zoom: ZoomWeek, from: day(2026, time.March, 5), to: day(2026, time.April, 2),
			origin: day(2026, time.March, 2), cols: 5,
			at: day(2026, time.March, 9), col: 1,
		},
		"a month is a calendar month and not thirty days": {
			zoom: ZoomMonth, from: day(2026, time.February, 17), to: day(2026, time.May, 1),
			origin: day(2026, time.February, 1), cols: 4,
			at: day(2026, time.April, 30), col: 2,
		},
		"a quarter starts in january, april, july or october": {
			zoom: ZoomQuarter, from: day(2026, time.February, 17), to: day(2026, time.December, 31),
			origin: day(2026, time.January, 1), cols: 4,
			at: day(2026, time.August, 9), col: 2,
		},
		"a span given backwards is the same span": {
			zoom: ZoomDay, from: day(2026, time.March, 11), to: day(2026, time.March, 2),
			origin: day(2026, time.March, 2), cols: 10,
			at: day(2026, time.March, 2), col: 0,
		},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			ax := newAxis(tc.zoom, tc.from, tc.to)
			if ax.origin != tc.origin {
				t.Errorf("origin is %s, want %s", ax.origin, tc.origin)
			}
			if ax.cols != tc.cols {
				t.Errorf("the axis is %d columns wide, want %d", ax.cols, tc.cols)
			}
			if got := ax.col(tc.at); got != tc.col {
				t.Errorf("%s is in column %d, want %d", tc.at, got, tc.col)
			}
			if got := ax.col(ax.start(tc.col)); got != tc.col {
				t.Errorf("column %d starts on %s, which is column %d", tc.col, ax.start(tc.col), got)
			}
		})
	}
}

// A date before the span is in a negative column, so a bar that starts off the
// left of the window is clipped rather than drawn against column zero with
// everything else.
func TestAxis_ADateBeforeTheSpanIsInANegativeColumn(t *testing.T) {
	t.Parallel()

	for name, tc := range map[string]struct {
		zoom Zoom
		at   jira.Date
		want int
	}{
		"a day before":       {ZoomDay, day(2026, time.March, 1), -1},
		"a week before":      {ZoomWeek, day(2026, time.February, 24), -1},
		"two weeks before":   {ZoomWeek, day(2026, time.February, 20), -2},
		"a month before":     {ZoomMonth, day(2026, time.February, 28), -1},
		"a quarter before":   {ZoomQuarter, day(2025, time.December, 31), -1},
		"four months before": {ZoomQuarter, day(2025, time.November, 1), -1},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			ax := newAxis(tc.zoom, day(2026, time.March, 2), day(2026, time.June, 2))
			if got := ax.col(tc.at); got != tc.want {
				t.Errorf("%s is in column %d, want %d", tc.at, got, tc.want)
			}
		})
	}
}

func TestAxis_HeadsTheColumnThatStartsAPeriod(t *testing.T) {
	t.Parallel()

	for name, tc := range map[string]struct {
		zoom  Zoom
		from  jira.Date
		to    jira.Date
		heads []int
	}{
		"the day zoom heads the first of the month": {
			zoom: ZoomDay, from: day(2026, time.March, 30), to: day(2026, time.April, 3),
			heads: []int{0, 2},
		},
		"the month zoom heads january": {
			zoom: ZoomMonth, from: day(2026, time.November, 1), to: day(2027, time.February, 1),
			heads: []int{0, 2},
		},
		"the quarter zoom heads the first quarter": {
			zoom: ZoomQuarter, from: day(2026, time.July, 1), to: day(2027, time.April, 1),
			heads: []int{0, 2},
		},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			ax := newAxis(tc.zoom, tc.from, tc.to)
			var got []int
			for col := 0; col < ax.cols; col++ {
				if ax.heads(col) {
					got = append(got, col)
				}
			}
			if len(got) != len(tc.heads) {
				t.Fatalf("columns %v head a period, want %v", got, tc.heads)
			}
			for i := range got {
				if got[i] != tc.heads[i] {
					t.Fatalf("columns %v head a period, want %v", got, tc.heads)
				}
			}
		})
	}
}

func TestAxis_AnEmptySpanHasNoColumns(t *testing.T) {
	t.Parallel()

	for name, ax := range map[string]axis{
		"no dates at all": newAxis(ZoomWeek, jira.Date{}, jira.Date{}),
		"no end":          newAxis(ZoomWeek, day(2026, time.March, 2), jira.Date{}),
		"no start":        newAxis(ZoomWeek, jira.Date{}, day(2026, time.March, 2)),
		"the zero value":  {},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			if !ax.empty() {
				t.Errorf("the axis reports %d columns", ax.cols)
			}
			if got := ax.start(0); !got.IsZero() {
				t.Errorf("column zero starts on %s", got)
			}
		})
	}
}

func TestZoom_StepsStopAtBothEnds(t *testing.T) {
	t.Parallel()

	if got := ZoomDay.in(); got != ZoomDay {
		t.Errorf("zooming in past the day zoom gives %s", got)
	}
	if got := ZoomQuarter.out(); got != ZoomQuarter {
		t.Errorf("zooming out past the quarter zoom gives %s", got)
	}
	want := []Zoom{ZoomWeek, ZoomMonth, ZoomQuarter, ZoomQuarter}
	at := ZoomDay
	for i, expect := range want {
		at = at.out()
		if at != expect {
			t.Fatalf("step %d out of the day zoom is %s, want %s", i+1, at, expect)
		}
	}
}

func TestFloorDiv_RoundsTowardsMinusInfinity(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct{ n, d, want int }{
		{7, 7, 1}, {6, 7, 0}, {0, 7, 0}, {-1, 7, -1}, {-7, 7, -1}, {-8, 7, -2},
	} {
		if got := floorDiv(tc.n, tc.d); got != tc.want {
			t.Errorf("floorDiv(%d, %d) = %d, want %d", tc.n, tc.d, got, tc.want)
		}
	}
}

// A span longer than about 292 years used to come back as nonsense: the
// difference was taken as a time.Duration, which is 64 bits of nanoseconds.
func TestAxis_SurvivesASpanLongerThanADurationHolds(t *testing.T) {
	t.Parallel()

	from, to := day(1526, time.March, 2), day(2526, time.March, 2)
	for _, tc := range []struct {
		zoom Zoom
		cols int
	}{
		{ZoomDay, 365244},
		{ZoomWeek, 52178},
		{ZoomMonth, 12001},
		{ZoomQuarter, 4001},
	} {
		ax := newAxis(tc.zoom, from, to)
		if ax.empty() {
			t.Errorf("a thousand years at the %s zoom is an empty axis", tc.zoom)
			continue
		}
		if ax.cols != tc.cols {
			t.Errorf("a thousand years at the %s zoom is %d columns, want %d", tc.zoom, ax.cols, tc.cols)
		}
		if got := ax.col(to); got != ax.cols-1 {
			t.Errorf("the last day is in column %d of %d at the %s zoom", got, ax.cols, tc.zoom)
		}
	}
}

// dayNumber has to agree with the calendar it is standing in for, including over
// the leap rules a naive count gets wrong.
func TestDayNumber_AgreesWithTheCalendar(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		a, b jira.Date
		want int
	}{
		{day(2026, time.March, 2), day(2026, time.March, 3), 1},
		{day(2026, time.February, 28), day(2026, time.March, 1), 1},
		{day(2024, time.February, 28), day(2024, time.March, 1), 2},
		{day(1900, time.February, 28), day(1900, time.March, 1), 1},
		{day(2000, time.February, 28), day(2000, time.March, 1), 2},
		{day(2026, time.January, 1), day(2027, time.January, 1), 365},
		{day(2024, time.January, 1), day(2025, time.January, 1), 366},
		{day(2026, time.March, 3), day(2026, time.March, 2), -1},
	} {
		if got := daysBetween(tc.a, tc.b); got != tc.want {
			t.Errorf("%s to %s is %d days, want %d", tc.a, tc.b, got, tc.want)
		}
		if got := tc.b.In(time.UTC).Sub(tc.a.In(time.UTC)) / (24 * time.Hour); int(got) != tc.want {
			t.Errorf("%s to %s: the calendar count and the instant count disagree (%d against %d)",
				tc.a, tc.b, tc.want, int(got))
		}
	}
}
