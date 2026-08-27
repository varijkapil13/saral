package release

import (
	"errors"
	"testing"

	"github.com/varijkapil13/saral/pkg/jira"
)

func TestFlow_Golden(t *testing.T) {
	t.Parallel()

	for name, tc := range map[string]struct {
		width, height int
		open          int
		targets       []jira.Version
		after         func(*driver)
		golden        string
	}{
		"the three choices": {
			width: 100, height: 14, open: 12, targets: []jira.Version{threeOhVersion()},
			golden: "flow_choices_100x14.golden",
		},
		"a narrow terminal": {
			width: 80, height: 14, open: 12, targets: []jira.Version{threeOhVersion()},
			golden: "flow_choices_80x14.golden",
		},
		"nowhere to move the open issues": {
			width: 100, height: 14, open: 12,
			golden: "flow_nowhere_100x14.golden",
		},
		"where the open issues move to": {
			width: 100, height: 14, open: 12, targets: dated(),
			after:  func(dr *driver) { dr.key("j", "enter") },
			golden: "flow_targets_100x14.golden",
		},
		"the confirm for a move": {
			width: 100, height: 14, open: 12, targets: dated(),
			after:  func(dr *driver) { dr.key("j", "enter", "enter") },
			golden: "flow_confirm_move_100x14.golden",
		},
		"the confirm for a version with nothing open": {
			width: 100, height: 14,
			golden: "flow_confirm_clean_100x14.golden",
		},
		"a release in flight": {
			width: 100, height: 14, open: 3,
			after:  func(dr *driver) { dr.flow().state = flowWorking },
			golden: "flow_working_100x14.golden",
		},
		"a release that did not happen": {
			width: 100, height: 14, open: 3,
			after: func(dr *driver) {
				f := dr.flow()
				f.state = flowStuck
				f.failure = &jira.TransportError{
					Op: "PUT /rest/api/3/version/10001", Status: 503,
					Err: errors.New("the service is unavailable"),
				}
			},
			golden: "flow_stuck_100x14.golden",
		},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			dr := flowOf(t, testDeps(newFake(4)), twoOhVersion(), tc.open, tc.targets, tc.width, tc.height)
			if tc.after != nil {
				tc.after(dr)
			}
			golden(t, tc.golden, dr.view())
		})
	}
}

func dated() []jira.Version {
	three := threeOhVersion()
	three.ReleaseDate = jira.Date{Year: 2026, Month: 6, Day: 1}
	return []jira.Version{three, {ID: "ver-PROJ-9", Name: "4.0"}}
}
