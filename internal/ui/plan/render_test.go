package plan

import (
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"

	appplan "github.com/varijkapil13/saral/internal/app/plan"
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

func TestPlans_BoardSourcesGolden(t *testing.T) {
	t.Parallel()

	for name, tc := range map[string]struct {
		width  int
		golden string
	}{
		"wide":   {width: 120, golden: "site_boards_120x20.golden"},
		"narrow": {width: 80, golden: "site_boards_80x20.golden"},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			dr := newDriver(t, testDeps(newFake(5)), tc.width, 20)
			dr.send(plansMsg{gen: dr.m.gen, plans: []jira.Plan{{
				ID: "42", Name: "Delivery", Status: "Active",
				Sources: []jira.PlanSource{
					{Type: jira.PlanSourceBoard, Value: "17"},
					{Type: jira.PlanSourceBoard, Value: "18"},
					{Type: jira.PlanSourceBoard, Value: "19"},
				},
			}}})
			dr.key("enter")
			dr.send(releasesMsg{gen: dr.m.gen, plan: "42", got: appplan.Releases{
				Versions: []jira.Version{{ID: "1", Name: "1.0", Released: true}, {ID: "2", Name: "2.0"}},
				Owners:   []string{"10000", "10001"},
				Refused: []appplan.Refusal{
					{Kind: appplan.RefusedBoard, Ref: "18", Err: appplan.ErrBoardEmpty},
					{Kind: appplan.RefusedBoard, Ref: "19", Err: &jira.NotFoundError{Kind: "board", ID: "19", Detail: "The requested board cannot be viewed because it either does not exist or you do not have permission to view it."}},
				},
				Names:  map[string]string{"10000": "EX", "10001": "OPS"},
				Boards: map[string][]string{"17": {"EX", "OPS"}},
				Read:   []string{"10000", "10001"},
				Detail: &jira.PlanDetail{
					CrossProjectReleases: []jira.CrossProjectRelease{{Name: "Spring launch", VersionIDs: []string{"1", "2"}}},
					ExcludedVersionIDs:   []string{"2"},
				},
			}})
			golden(t, tc.golden, dr.view())
		})
	}
}

func TestPlans_CrossSpaceGolden(t *testing.T) {
	t.Parallel()

	dr := newDriver(t, testDeps(newFake(5)), 120, 20)
	dr.send(plansMsg{gen: dr.m.gen, plans: []jira.Plan{{
		ID: "42", Name: "Delivery", Status: "Active",
		Sources: projectSources("10000", "10001", "10002"),
	}}})
	dr.key("enter")
	versions := []jira.Version{
		{ID: "1", Name: "2.4.0"}, {ID: "2", Name: "2.5.0"}, {ID: "3", Name: "ops-2026.3"},
		{ID: "4", Name: "ops-2026.4"}, {ID: "5", Name: "web-9"}, {ID: "6", Name: "1.9.x-hotfix"},
	}
	dr.send(releasesMsg{gen: dr.m.gen, plan: "42", got: appplan.Releases{
		Versions: versions,
		Owners:   []string{"10000", "10000", "10001", "10001", "10002", "10002"},
		Names:    map[string]string{"10000": "EX", "10001": "OPS", "10002": "WEB"},
		Read:     []string{"10000", "10001", "10002"},
		Detail: &jira.PlanDetail{
			CrossProjectReleases: []jira.CrossProjectRelease{
				{Name: "Spring launch", VersionIDs: []string{"1", "3", "5"}},
				{Name: "Summer launch", VersionIDs: []string{"2", "4"}},
			},
			ExcludedVersionIDs: []string{"6", "99"},
		},
	}})
	golden(t, "open_cross_space_120x20.golden", dr.view())
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
