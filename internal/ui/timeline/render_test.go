package timeline

import (
	"strings"
	"testing"
	"time"

	"github.com/charmbracelet/x/ansi"

	"github.com/varijkapil13/saral/internal/ui/kernel"
	"github.com/varijkapil13/saral/pkg/jira"
)

func TestTimeline_Golden(t *testing.T) {
	t.Parallel()

	for name, tc := range map[string]struct {
		width, height int
		build         func(*testing.T) *driver
		keys          []string
		golden        string
	}{
		"a chart at a comfortable width": {
			width: 140, height: 24, golden: "chart_140x24.golden",
		},
		"a narrow terminal, where the source column gives way": {
			width: 80, height: 20, golden: "chart_80x20.golden",
		},
		"a wide terminal": {
			width: 200, height: 30, golden: "chart_200x30.golden",
		},
		"the day zoom": {
			width: 140, height: 24, keys: []string{"+", "+"}, golden: "day_140x24.golden",
		},
		"the quarter zoom": {
			width: 140, height: 24, keys: []string{"-", "-"}, golden: "quarter_140x24.golden",
		},
		"the notes pane": {
			width: 140, height: 24, keys: []string{"n"}, golden: "notes_140x24.golden",
		},
		"nothing the cascade could date": {
			width: 140, height: 20, golden: "undated_140x20.golden",
			build: func(t *testing.T) *driver {
				return newDriver(t, testDeps(customFake([]jira.Issue{
					issueIn("PROJ-1", "One"), issueIn("PROJ-2", "Two"), issueIn("PROJ-3", "Three"),
				})), 140, 20)
			},
		},
		"a search that matched nothing": {
			width: 140, height: 20, golden: "empty_140x20.golden",
			build: func(t *testing.T) *driver {
				return newDriver(t, testDeps(customFake(nil)), 140, 20)
			},
		},
		"a read the site refused": {
			width: 140, height: 20, golden: "refused_140x20.golden",
			build: func(t *testing.T) *driver {
				f := newFake(10)
				f.FailNextN(4, &jira.CapabilityError{
					Capability: jira.CapBoards,
					Reason:     "you need Browse Projects on PROJ to search it",
				})
				return newDriver(t, testDeps(f), 140, 20)
			},
		},
		"no connection in this session": {
			width: 140, height: 20, golden: "unconnected_140x20.golden",
			build: func(t *testing.T) *driver {
				return newDriver(t, testDeps(nil), 140, 20)
			},
		},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			dr := tc.build
			if dr == nil {
				dr = func(t *testing.T) *driver {
					return newDriver(t, testDeps(newFake(24)), tc.width, tc.height)
				}
			}
			d := dr(t)
			d.key(tc.keys...)
			golden(t, tc.golden, d.view())
		})
	}
}

// Every line is exactly as wide as the box, so a selected row's highlight
// reaches the edge and nothing wraps.
func TestTimeline_EveryRowFillsTheWidth(t *testing.T) {
	t.Parallel()

	for _, size := range [...][2]int{{80, 20}, {100, 24}, {140, 24}, {200, 60}} {
		dr := newDriver(t, testDeps(newFake(40)), size[0], size[1])
		for at, line := range strings.Split(dr.view(), "\n") {
			if got := ansi.StringWidth(line); got > size[0] {
				t.Errorf("at %dx%d line %d is %d columns wide:\n%q", size[0], size[1], at, got, line)
			}
		}
		// A bar row is padded to the full width, so a selected row's highlight
		// reaches the edge. The chrome lines around them are not.
		bars := 0
		for at, line := range strings.Split(dr.view(), "\n") {
			if !isBarRow(line) {
				continue
			}
			bars++
			if got := ansi.StringWidth(line); got != size[0] {
				t.Errorf("at %dx%d row %d is %d columns wide, want %d:\n%q", size[0], size[1], at, got, size[0], line)
			}
		}
		if bars == 0 {
			t.Fatalf("at %dx%d no bar row reached the frame", size[0], size[1])
		}
	}
}

func TestTimeline_FitsTheBoxItIsGiven(t *testing.T) {
	t.Parallel()

	for _, size := range [...][2]int{{40, 10}, {80, 20}, {120, 30}, {200, 60}} {
		for name, dr := range map[string]*driver{
			"with bars":  newDriver(t, testDeps(newFake(80)), size[0], size[1]),
			"with none":  newDriver(t, testDeps(customFake(nil)), size[0], size[1]),
			"with notes": newDriver(t, testDeps(newFake(8)), size[0], size[1]),
		} {
			if name == "with notes" {
				dr.key("n")
			}
			got := len(strings.Split(dr.view(), "\n"))
			if got != size[1] {
				t.Errorf("%s at %dx%d drew %d lines, want %d", name, size[0], size[1], got, size[1])
			}
		}
	}
}

// Only the rows that fit are built. A chart of eight hundred issues costs a
// frame what a chart of eight costs.
func TestTimeline_BuildsOnlyTheRowsThatFit(t *testing.T) {
	t.Parallel()

	dr := newDriver(t, testDeps(newFake(800)), 120, 24)
	drawn := 0
	for _, line := range strings.Split(dr.view(), "\n") {
		if isBarRow(line) {
			drawn++
		}
	}
	if drawn > dr.m.rowsHeight()+1 {
		t.Errorf("%d rows reached the frame and only %d fit", drawn, dr.m.rowsHeight())
	}
	if drawn == 0 {
		t.Fatal("no row reached the frame")
	}
}

// A bar outside the window is left out of it, not drawn against its edge. That
// is what horizontal virtualization means for a row, and the window has to be
// somewhere other than column zero for the question to mean anything.
func TestTimeline_LeavesABarOutsideTheWindowOutOfIt(t *testing.T) {
	t.Parallel()

	early := issueIn("PROJ-1", "Long finished")
	early.Due = day(2016, time.March, 20)
	inside := issueIn("PROJ-2", "Happening now")
	inside.Due = day(2026, time.March, 6)
	late := issueIn("PROJ-3", "Years off")
	late.Due = day(2036, time.March, 20)

	dr := newDriver(t, testDeps(customFake([]jira.Issue{early, inside, late})), 120, 24)
	dr.key("+", "+", "T")
	if dr.m.left == 0 {
		t.Fatalf("the window is at column zero over a span of %d columns, so nothing is off it", dr.m.ax.cols)
	}

	milestone := dr.m.styles.glyphs.milestone
	if got := chartOf(t, dr, "PROJ-2"); !strings.Contains(got, milestone) {
		t.Errorf("the bar inside the window was not drawn: %q", got)
	}
	for _, key := range []string{"PROJ-1", "PROJ-3"} {
		if got := chartOf(t, dr, key); strings.Contains(got, milestone) {
			at := dr.m.ax.col(rowFor(t, dr, key).rng.Start)
			t.Errorf("%s is in column %d and the window is [%d, %d), and it was drawn anyway: %q",
				key, at, dr.m.left, dr.m.left+dr.m.lay.chart, got)
		}
	}
}

// The today line and the today mark on the ruler are drawn from the same column,
// or the chart says two different things about the same day.
func TestTimeline_TheTodayLineAndTheRulerAgree(t *testing.T) {
	t.Parallel()

	dr := newDriver(t, testDeps(newFake(24)), 140, 24)
	frame := dr.view()
	lines := strings.Split(frame, "\n")
	ruler := lines[2]
	at := strings.Index(ruler, dr.m.styles.glyphs.mark)
	if at < 0 {
		t.Fatalf("the ruler carries no today mark:\n%s", ruler)
	}
	row := lineFor(t, frame, "PROJ-")
	if at >= len(row) || string(row[at]) != dr.m.styles.glyphs.today {
		t.Errorf("the ruler marks today at column %d and the row there holds %q:\n%s\n%s",
			at, safeAt(row, at), ruler, row)
	}
}

// A theme switch rebuilds the styles and drops the memo, or every row on screen
// keeps the colours of the theme it was drawn in.
func TestTimeline_ATh0emeSwitchRedrawsEveryRow(t *testing.T) {
	t.Parallel()

	dr := newDriver(t, testDeps(newFake(20)), 120, 24)
	before := dr.m.styles.gen
	dr.send(kernel.ThemeMsg{Theme: kernel.NewTheme(kernel.ThemeDark, true, kernel.UnicodeGlyphs())})
	if dr.m.styles.gen == before {
		t.Error("the styles were not rebuilt")
	}
	if len(dr.m.memo.rows) != 0 {
		t.Errorf("%d rows survived the theme switch in the memo", len(dr.m.memo.rows))
	}
	if !strings.Contains(dr.m.View(), "\x1b") {
		t.Error("the dark theme drew no colour at all")
	}
}

// isBarRow tells a bar's line from the detail line under the chart, which names
// the same issue and is deliberately not padded.
func isBarRow(line string) bool {
	return strings.Contains(line, "PROJ-") &&
		(strings.HasPrefix(line, "  ") || strings.HasPrefix(line, "> "))
}

func lineFor(t *testing.T, frame, want string) string {
	t.Helper()
	for _, line := range strings.Split(frame, "\n") {
		if strings.Contains(line, want) {
			return line
		}
	}
	t.Fatalf("no line contains %q:\n%s", want, frame)
	return ""
}

func safeAt(s string, at int) string {
	if at < 0 || at >= len(s) {
		return ""
	}
	return string(s[at])
}
