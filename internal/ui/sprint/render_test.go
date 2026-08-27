package sprint

import (
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/varijkapil13/saral/internal/ui/kernel"
	"github.com/varijkapil13/saral/pkg/jira"
	"github.com/varijkapil13/saral/pkg/jira/jiratest"
)

// stock is a view already holding sprints, without a site behind it: they
// arrive as the message a read would have produced, which is what keeps a
// render test off the network.
func stock(t *testing.T, w, h, n int) *driver {
	t.Helper()
	dr := newDriver(t, testDeps(nil), w, h)
	dr.send(loadedMsg{gen: dr.m.gen, boards: []jira.Board{{ID: 1, Name: "PROJ board"}}, sprints: many(n)})
	return dr
}

// many is a board's history: one running sprint, one planned, and the rest
// closed, dated backwards from a fixed day.
func many(n int) []jira.Sprint {
	day := time.Date(2026, time.March, 2, 0, 0, 0, 0, time.UTC)
	out := make([]jira.Sprint, 0, n)
	for i := range n {
		start := day.AddDate(0, 0, -14*i)
		end := start.AddDate(0, 0, 14)
		sp := jira.Sprint{
			ID: int64(1000 + i), BoardID: 1,
			Name:  "Sprint " + itoa(n-i),
			Goal:  "goal number " + itoa(n-i),
			State: jira.SprintClosed, Start: &start, End: &end, Complete: &end,
		}
		switch i {
		case 0:
			sp.State = jira.SprintActive
			sp.Complete = nil
		case 1:
			sp.State = jira.SprintFuture
			sp.Complete = nil
		}
		out = append(out, sp)
	}
	return sortSprints(out)
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var digits [20]byte
	at := len(digits)
	for n > 0 {
		at--
		digits[at] = byte('0' + n%10)
		n /= 10
	}
	return string(digits[at:])
}

func TestSprints_Golden(t *testing.T) {
	t.Parallel()

	for name, tc := range map[string]struct {
		width, height int
		build         func(t *testing.T, w, h int) *driver
		golden        string
	}{
		"a board's sprints": {
			width: 120, height: 20, golden: "rows_120x20.golden",
			build: func(t *testing.T, w, h int) *driver { return stock(t, w, h, 8) },
		},
		"a narrow terminal": {
			width: 80, height: 20, golden: "rows_80x20.golden",
			build: func(t *testing.T, w, h int) *driver { return stock(t, w, h, 8) },
		},
		"a board with no sprint on it": {
			width: 100, height: 14, golden: "empty_100x14.golden",
			build: func(t *testing.T, w, h int) *driver {
				return newDriver(t, testDeps(jiratest.New(jiratest.WithProject("PROJ", jiratest.Kanban))), w, h)
			},
		},
		"no connection in this session": {
			width: 100, height: 14, golden: "noconnection_100x14.golden",
			build: func(t *testing.T, w, h int) *driver { return newDriver(t, testDeps(nil), w, h) },
		},
		"a read the site refused": {
			width: 100, height: 14, golden: "refused_100x14.golden",
			build: func(t *testing.T, w, h int) *driver {
				f := newFake()
				f.FailNext(&jira.CapabilityError{Capability: jira.CapBoards, Reason: "needs the Browse Projects permission on PROJ"})
				return newDriver(t, testDeps(f), w, h)
			},
		},
		"filling a sprint in": {
			width: 100, height: 20, golden: "form_100x20.golden",
			build: func(t *testing.T, w, h int) *driver {
				dr := newDriver(t, testDeps(newFake()), w, h)
				dr.key("n")
				dr.setField(fieldName, "Sprint 4")
				dr.key("tab")
				dr.typeText("finish the port")
				dr.key("tab")
				dr.typeText("2026-04-01")
				dr.key("tab")
				dr.typeText("2026-03-01")
				dr.key("ctrl+s")
				return dr
			},
		},
		"the confirm in front of a start": {
			width: 100, height: 16, golden: "start_100x16.golden",
			build: func(t *testing.T, w, h int) *driver {
				dr := newDriver(t, testDeps(newFake()), w, h)
				dr.onSprint("Sprint 3")
				at := time.Date(2026, time.April, 1, 0, 0, 0, 0, time.UTC)
				to := at.AddDate(0, 0, 13)
				dr.m.sprints[dr.m.cursor].Start, dr.m.sprints[dr.m.cursor].End = &at, &to
				dr.key("s")
				return dr
			},
		},
		"the confirm in front of a completion": {
			width: 100, height: 16, golden: "complete_100x16.golden",
			build: func(t *testing.T, w, h int) *driver {
				dr := newDriver(t, testDeps(newFake()), w, h)
				dr.onSprint("Sprint 2")
				dr.key("c")
				return dr
			},
		},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			dr := tc.build(t, tc.width, tc.height)
			golden(t, tc.golden, dr.view())
		})
	}
}

// Every row reaches the right edge, or a selected row's highlight stops short
// of it.
func TestSprints_EveryRowFillsTheWidth(t *testing.T) {
	t.Parallel()

	for _, width := range []int{80, 100, 132, 200} {
		dr := stock(t, width, 20, 12)
		for i := range min(dr.m.rowsHeight(), dr.m.rowCount()) {
			if got := ansi.StringWidth(ansi.Strip(dr.m.row(i))); got != width {
				t.Errorf("at width %d row %d is %d columns wide", width, i, got)
			}
		}
	}
}

// The frame is exactly the box the kernel gave it, in every state: a line more
// pushes the footer off the screen and a line fewer leaves a hole in it.
func TestSprints_FitsTheBoxItIsGiven(t *testing.T) {
	t.Parallel()

	for _, size := range []struct{ w, h int }{{40, 10}, {80, 20}, {120, 30}, {200, 60}} {
		for name, open := range map[string]func(dr *driver){
			"the list":    func(dr *driver) {},
			"the form":    func(dr *driver) { dr.key("n") },
			"the confirm": func(dr *driver) { dr.onSprint("Sprint 2"); dr.key("c") },
			"a refusal": func(dr *driver) {
				dr.send(failedMsg{gen: dr.m.gen, op: opRead, err: &jira.TransportError{
					Op: "GET /rest/agile/1.0/board/1/sprint", Err: errNoRoute,
				}})
			},
		} {
			dr := newDriver(t, testDeps(newFake()), size.w, size.h)
			open(dr)
			frame := dr.view()
			lines := strings.Split(frame, "\n")
			if len(lines) != size.h {
				t.Errorf("%s at %dx%d drew %d lines", name, size.w, size.h, len(lines))
			}
			for i, line := range lines {
				if got := ansi.StringWidth(line); got > size.w {
					t.Errorf("%s at %dx%d: line %d is %d columns wide: %q", name, size.w, size.h, i, got, line)
				}
			}
		}
	}
}

var errNoRoute = &noRoute{}

type noRoute struct{}

func (*noRoute) Error() string { return "dial tcp 10.0.0.1:443: no route to host" }

// Only the rows that fit are built. A board with two hundred sprints on it
// costs what one with twelve costs per frame.
func TestSprints_OnlyTheRowsThatFitAreRendered(t *testing.T) {
	t.Parallel()

	dr := stock(t, 120, 12, 200)
	_ = dr.m.View()
	if got, want := len(dr.m.memo.rows), dr.m.rowsHeight(); got > want {
		t.Errorf("a frame rendered %d rows into the memo with room for %d", got, want)
	}
	frame := dr.view()
	if drawn := strings.Count(frame, "Sprint "); drawn > dr.m.rowsHeight() {
		t.Errorf("the frame names %d sprints with room for %d", drawn, dr.m.rowsHeight())
	}
}

// A memoized row is thrown away by the two messages that change what a row is
// drawn from without changing the row: a theme, whose styles and glyphs are
// inside the rendered string, and a capability answer, which carries the
// timezone the dates are written in.
func TestSprints_TheMemoIsEmptiedByWhatChangesHowARowLooks(t *testing.T) {
	t.Parallel()

	for name, msg := range map[string]tea.Msg{
		"a new theme":             kernel.ThemeMsg{Theme: kernel.NewTheme(kernel.ThemeDark, true, kernel.UnicodeGlyphs())},
		"a capability answer":     kernel.CapabilitiesMsg{Caps: fullCaps()},
		"a resize":                kernel.SizeMsg{Width: 100, Height: 18},
		"an answer from the site": loadedMsg{},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			dr := stock(t, 120, 20, 12)
			_ = dr.m.View()
			if len(dr.m.memo.rows) == 0 {
				t.Fatal("nothing was memoized at all")
			}
			if held, ok := msg.(loadedMsg); ok {
				held.gen = dr.m.gen
				msg = held
			}
			dr.send(msg)
			if got := len(dr.m.memo.rows); got != 0 {
				t.Errorf("%d rows survived %s, so a frame after it is drawn from the old one", got, name)
			}
		})
	}
}

// A sprint with no dates says so rather than drawing an epoch, and the dates it
// does have are in the account's timezone rather than the machine's.
func TestSprints_DatesAreTheAccountsAndAnAbsentOneSaysSo(t *testing.T) {
	t.Parallel()

	kolkata, err := time.LoadLocation("Asia/Kolkata")
	if err != nil {
		t.Skipf("this machine has no timezone database: %v", err)
	}
	d := testDeps(nil)
	d.Caps.TimeZone = kolkata
	dr := newDriver(t, d, 120, 12)
	// Half past six in the evening UTC is the small hours of the next day in
	// Kolkata, which is the whole point of asking the account.
	at := time.Date(2026, time.April, 1, 18, 30, 0, 0, time.UTC)
	dr.send(loadedMsg{
		gen:    dr.m.gen,
		boards: []jira.Board{{ID: 1, Name: "PROJ board"}},
		sprints: []jira.Sprint{
			{ID: 1, BoardID: 1, Name: "dated", State: jira.SprintActive, Start: &at},
			{ID: 2, BoardID: 1, Name: "undated", State: jira.SprintFuture},
		},
	})

	frame := dr.view()
	mustContain(t, frame, "from 2026-04-02", "no dates yet")
	mustNotContain(t, frame, "from 2026-04-01")
}
