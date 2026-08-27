package backlog

import (
	"errors"
	"strings"
	"testing"

	"github.com/varijkapil13/saral/internal/ui/kernel"
	"github.com/varijkapil13/saral/pkg/jira"
	"github.com/varijkapil13/saral/pkg/jira/jiratest"
)

// seeded is the fixture the goldens are drawn from: a scrum board with two of
// its issues already in the active sprint, so both kinds of section have rows.
func seeded(t *testing.T) *jiratest.Fake {
	t.Helper()
	f := newFake(12)
	active, _ := sprintIDs(t, f)
	if err := f.MoveToSprint(t.Context(), active, []string{"PROJ-3", "PROJ-4"}); err != nil {
		t.Fatalf("seeding the sprint: %v", err)
	}
	return f
}

func TestBacklog_Golden(t *testing.T) {
	t.Parallel()
	for name, tc := range map[string]struct {
		width, height int
		open          func(t *testing.T) *driver
		golden        string
	}{
		"the board": {
			width: 120, height: 24,
			open:   func(t *testing.T) *driver { return newDriver(t, testDeps(seeded(t)), 120, 24) },
			golden: "board_120x24.golden",
		},
		"a narrow terminal": {
			width: 80, height: 20,
			open:   func(t *testing.T) *driver { return newDriver(t, testDeps(seeded(t)), 80, 20) },
			golden: "board_80x20.golden",
		},
		"a wide terminal": {
			width: 160, height: 30,
			open:   func(t *testing.T) *driver { return newDriver(t, testDeps(seeded(t)), 160, 30) },
			golden: "board_160x30.golden",
		},
		"issues picked": {
			width: 120, height: 24,
			open: func(t *testing.T) *driver {
				dr := newDriver(t, testDeps(seeded(t)), 120, 24)
				dr.cursorTo("row:PROJ-1")
				dr.key("space", "space")
				return dr
			},
			golden: "picked_120x24.golden",
		},
		"choosing where they go": {
			width: 120, height: 24,
			open: func(t *testing.T) *driver {
				dr := newDriver(t, testDeps(seeded(t)), 120, 24)
				dr.cursorTo("row:PROJ-1")
				dr.key("space", "m")
				return dr
			},
			golden: "chooser_120x24.golden",
		},
		"the confirm": {
			width: 120, height: 24,
			open: func(t *testing.T) *driver {
				dr := newDriver(t, testDeps(seeded(t)), 120, 24)
				dr.cursorTo("row:PROJ-1")
				dr.key("space", "m", "enter")
				return dr
			},
			golden: "confirm_120x24.golden",
		},
		"a board with no rank field": {
			width: 120, height: 24,
			open: func(t *testing.T) *driver {
				return newDriver(t, testDeps(jiratest.New(
					jiratest.WithProject("PROJ", jiratest.Kanban),
					jiratest.WithIssues(jiratest.Gen(8)),
				)), 120, 24)
			},
			golden: "kanban_120x24.golden",
		},
		"a read the site refused": {
			width: 100, height: 20,
			open: func(t *testing.T) *driver {
				f := newFake(8)
				f.FailNext(&jira.CapabilityError{
					Capability: jira.CapBoards,
					Reason:     "you need the Browse Projects permission on PROJ",
				})
				return newDriver(t, testDeps(f), 100, 20)
			},
			golden: "refused_100x20.golden",
		},
		"a transport failure over a narrow window": {
			width: 80, height: 20,
			open: func(t *testing.T) *driver {
				f := newFake(8)
				f.FailNext(&jira.TransportError{
					Op:     "GET /rest/agile/1.0/board",
					Err:    errors.New("dial tcp 10.0.0.7:443: connect: connection refused"),
					Status: 0,
				})
				return newDriver(t, testDeps(f), 80, 20)
			},
			golden: "transport_80x20.golden",
		},
		"nothing waiting to be scheduled": {
			width: 100, height: 20,
			open: func(t *testing.T) *driver {
				return newDriver(t, testDeps(jiratest.New(
					jiratest.WithProject("PROJ", jiratest.Scrum),
					jiratest.WithIssues(allDone(t)),
				)), 100, 20)
			},
			golden: "empty_100x20.golden",
		},
		"a project with no board": {
			width: 100, height: 20,
			open: func(t *testing.T) *driver {
				return newDriver(t, testDeps(jiratest.New(
					jiratest.WithProject("PROJ", jiratest.NoBoard),
					jiratest.WithIssues(jiratest.Gen(4)),
				)), 100, 20)
			},
			golden: "noboard_100x20.golden",
		},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			dr := tc.open(t)
			got := dr.view()
			if lines := strings.Count(got, "\n") + 1; lines != tc.height {
				t.Errorf("the frame is %d lines, want %d", lines, tc.height)
			}
			golden(t, tc.golden, got)
		})
	}
}

func TestBacklog_GoldenOfAMoveInFlight(t *testing.T) {
	t.Parallel()
	c := newChunker(newFake(140))
	dr := newDriver(t, testDeps(c), 120, 24)
	dr.loadAll()
	dr.pickWholeBacklog()
	dr.key("m")
	dr.m.destAt = 0
	dr.key("enter")
	cmd := dr.hold(keyPress("y"))
	golden(t, "moving_120x24.golden", dr.view())
	dr.run(cmd)
}

// A section head is as wide as a row, so a selected one is highlighted to the
// edge of the pane rather than to the end of its words.
func TestBacklog_EverySectionHeadFillsTheWidth(t *testing.T) {
	t.Parallel()
	const width = 120
	dr := newDriver(t, testDeps(seeded(t)), width, 24)
	lines := strings.Split(dr.view(), "\n")
	heads := 0
	for i := range dr.m.rows {
		if !dr.m.rows[i].head {
			continue
		}
		heads++
		if got := len(lines[i+1]); got != width {
			t.Errorf("section head %q is %d cells wide: %q", dr.m.zoneOf(i), got, lines[i+1])
		}
	}
	if heads == 0 {
		t.Fatal("no section head was drawn, so this test proves nothing")
	}
}

// The theme is rebuilt on a switch and everything memoized under the old one
// goes with it.
func TestBacklog_RedrawsEverythingWhenTheThemeChanges(t *testing.T) {
	t.Parallel()
	dr := newDriver(t, testDeps(seeded(t)), 120, 24)
	before := dr.view()
	dr.send(kernel.ThemeMsg{Theme: kernel.NewTheme(kernel.ThemeDark, true, kernel.ASCIIGlyphs())})
	if dr.m.styles.gen == 0 && before == "" {
		t.Fatal("the fixture drew nothing")
	}
	after := dr.view()
	if strings.Contains(after, "▸") {
		t.Error("the frame still carries the unicode glyphs of the theme it was built with")
	}
	if after == "" {
		t.Error("the view drew nothing after the theme changed")
	}
}
