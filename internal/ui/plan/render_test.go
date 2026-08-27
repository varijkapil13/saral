package plan

import (
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"

	"github.com/varijkapil13/saral/pkg/jira"
	"github.com/varijkapil13/saral/pkg/jira/jiratest"
)

func TestPlans_Golden(t *testing.T) {
	t.Parallel()

	for name, tc := range map[string]struct {
		width, height int
		refused       bool
		open          bool
		golden        string
	}{
		"the site's own plans": {
			width: 120, height: 20, golden: "site_120x20.golden",
		},
		"the profile's plans, with the reason the site's are not there": {
			width: 120, height: 20, refused: true, golden: "profile_120x20.golden",
		},
		"a plan opened on its sources and its releases": {
			width: 120, height: 20, refused: true, open: true, golden: "open_120x20.golden",
		},
		"a narrow terminal": {
			width: 80, height: 20, refused: true, open: true, golden: "open_80x20.golden",
		},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			d := testDeps(newFake(5))
			if tc.refused {
				d = refusedDeps(newFake(5))
			}
			dr := newDriver(t, d, tc.width, tc.height, WithDefined(defined()))
			if tc.open {
				dr.key("enter")
			}
			golden(t, tc.golden, dr.view())
		})
	}
}

func TestPlans_FailureGolden(t *testing.T) {
	t.Parallel()

	f := newFake(5)
	f.FailNext(&jira.TransportError{
		Op:  "GET /rest/api/3/plans/plan",
		Err: errNoHost{},
	})
	dr := newDriver(t, testDeps(f), 120, 20, WithDefined(defined()))

	golden(t, "failed_120x20.golden", dr.view())
}

type errNoHost struct{}

func (errNoHost) Error() string {
	return `dial tcp: lookup example.atlassian.net: no such host`
}

// The two empty screens the view can be on, each saying which kind of empty it
// is. They drew one sentence between them once, and a profile with nothing in it
// and a site with nothing on it are different things to do next.
func TestPlans_EmptyGolden(t *testing.T) {
	t.Parallel()

	for name, tc := range map[string]struct {
		refused bool
		golden  string
	}{
		"the site has no plans":                         {golden: "empty_site_120x20.golden"},
		"the profile defines none and the site refused": {refused: true, golden: "empty_profile_120x20.golden"},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			d := testDeps(jiratest.New())
			if tc.refused {
				d = refusedDeps(jiratest.New())
			}
			dr := newDriver(t, d, 120, 20, WithDefined(nil))
			golden(t, tc.golden, dr.view())
		})
	}
}

// Every row is exactly as wide as the pane, whatever is in it, or the selected
// row's highlight stops short of the edge.
func TestPlans_EveryRowFillsTheWidth(t *testing.T) {
	t.Parallel()

	for _, width := range []int{80, 100, 120, 200} {
		dr := newDriver(t, refusedDeps(newFake(5)), width, 20, WithDefined(defined()))
		dr.key("enter")
		lines := strings.Split(dr.view(), "\n")[headHeight:]
		for i := range dr.m.rows {
			if got := ansi.StringWidth(lines[i]); got != width {
				t.Errorf("at %d columns a row is %d wide: %q", width, got, lines[i])
			}
		}
	}
}

// The pane keeps to the box it was given at every width, including one too
// narrow for a second column.
func TestPlans_FitsTheBoxItIsGiven(t *testing.T) {
	t.Parallel()

	for _, size := range []struct{ w, h int }{{40, 10}, {80, 20}, {120, 30}, {200, 60}} {
		dr := newDriver(t, refusedDeps(newFake(5)), size.w, size.h, WithDefined(defined()))
		dr.key("enter")
		lines := strings.Split(dr.m.View(), "\n")
		if len(lines) != size.h {
			t.Errorf("at %dx%d the frame is %d lines", size.w, size.h, len(lines))
		}
		for _, line := range lines {
			if got := ansi.StringWidth(line); got > size.w {
				t.Errorf("at %dx%d a line is %d columns: %q", size.w, size.h, got, line)
			}
		}
	}
}

// The reason the plans are the profile's is not a status line: it stays in the
// pane, under the rows, whatever else has happened since.
func TestPlans_TheReasonStaysUnderTheRows(t *testing.T) {
	t.Parallel()

	dr := newDriver(t, refusedDeps(newFake(5)), 120, 20, WithDefined(defined()))
	dr.key("j", "enter", "k")

	lines := strings.Split(dr.view(), "\n")
	if got := lines[len(lines)-1]; !strings.Contains(got, "the Plans API needs Administer Jira") {
		t.Errorf("the bottom line is %q, and it should be the reason these are the profile's plans", got)
	}
}
