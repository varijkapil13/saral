package move

import (
	"errors"
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"

	"github.com/varijkapil13/saral/pkg/jira"
	"github.com/varijkapil13/saral/pkg/jira/jiratest"
)

func TestMove_Golden(t *testing.T) {
	t.Parallel()

	for name, tc := range map[string]struct {
		width, height int
		golden        string
		drive         func(t *testing.T, dr *driver)
	}{
		"the target project": {
			width: 120, height: 20, golden: "target_120x20.golden",
		},
		"typing a project key": {
			width: 120, height: 20, golden: "typing_120x20.golden",
			drive: func(_ *testing.T, dr *driver) { dr.key("i"); dr.typeText("OTH") },
		},
		"the issue type": {
			width: 120, height: 20, golden: "type_120x20.golden",
			drive: func(_ *testing.T, dr *driver) { dr.typeKey("OTHER") },
		},
		"the status remap": {
			width: 120, height: 20, golden: "statuses_120x20.golden",
			drive: func(_ *testing.T, dr *driver) { dr.typeKey("OTHER"); dr.key("enter") },
		},
		"what the target insists on": {
			width: 120, height: 20, golden: "fields_120x20.golden",
			drive: func(_ *testing.T, dr *driver) {
				dr.typeKey("OTHER")
				dr.key("enter")
				ref := func(id, name string) jira.FieldRef { return jira.FieldRef{ID: id, Name: name} }
				dr.send(schemaMsg{gen: dr.m.gen, schema: jira.Schema{Fields: []jira.FieldMeta{
					{Field: ref("customfield_1", "Erfassungsart"), Name: "Erfassungsart", Required: true,
						AllowedValues: []jira.Option{{ID: "1", Label: "Eins"}, {ID: "2", Label: "Zwei"}}},
					{Field: ref("customfield_2", "Kostenstelle"), Name: "Kostenstelle", Required: true},
				}}})
				dr.key("enter")
				dr.key("right")
			},
		},
		"the confirm screen": {
			width: 120, height: 20, golden: "confirm_120x20.golden",
			drive: func(_ *testing.T, dr *driver) { dr.walkTo("OTHER") },
		},
		"the confirm screen in a narrow terminal": {
			width: 80, height: 20, golden: "confirm_80x20.golden",
			drive: func(_ *testing.T, dr *driver) { dr.walkTo("OTHER") },
		},
		"a move the queue is running": {
			width: 120, height: 20, golden: "running_120x20.golden",
			drive: func(_ *testing.T, dr *driver) {
				dr.walkTo("OTHER")
				dr.running()
				dr.once(taskMsg{gen: dr.m.gen, status: jira.TaskStatus{State: jira.TaskRunning, Progress: 50}})
			},
		},
		"a move that left issues behind": {
			width: 120, height: 20, golden: "partial_120x20.golden",
			drive: func(_ *testing.T, dr *driver) {
				dr.walkTo("OTHER")
				dr.running()
				dr.send(taskMsg{gen: dr.m.gen, status: jira.TaskStatus{
					State: jira.TaskComplete, Progress: 100, Failed: []string{"PROJ-2", "PROJ-3"},
				}})
			},
		},
		"a read the site refused": {
			width: 120, height: 20, golden: "refused_120x20.golden",
			drive: func(_ *testing.T, dr *driver) {
				dr.m.failure = &jira.CapabilityError{
					Capability: jira.CapBulkMove,
					Reason:     "You need the Bulk Change permission to move issues between projects",
				}
				dr.m.looked = true
				dr.m.found = nil
				dr.m.forget()
			},
		},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			f, iss := twoProjects(t)
			w := &immediate{}
			dr := newDriver(t, testDeps(f), tc.width, tc.height, WithIssues(iss), withWaiter(w.wait))
			if tc.drive != nil {
				tc.drive(t, dr)
			}
			golden(t, tc.golden, dr.view())
		})
	}
}

// A token that may not move issues at all is the normal answer here, so it has a
// frame of its own.
func TestMove_GoldenWhenTheTokenMayNotMoveIssues(t *testing.T) {
	t.Parallel()
	f, iss := twoProjects(t)
	d := testDeps(f)
	d.Caps = noMoveCaps()
	dr := newDriver(t, d, 120, 20, WithIssues(iss))
	golden(t, "norights_120x20.golden", dr.view())
}

func TestMove_FitsTheBoxItIsGiven(t *testing.T) {
	t.Parallel()
	for _, size := range []struct{ w, h int }{{40, 10}, {80, 20}, {120, 30}, {200, 60}} {
		f, iss := twoProjects(t)
		dr := newDriver(t, testDeps(f), size.w, size.h, WithIssues(iss))
		for _, at := range []string{"the projects", "the issue type", "the remap", "the confirm screen"} {
			frame := dr.view()
			if got := strings.Count(frame, "\n") + 1; got != size.h {
				t.Errorf("%s at %dx%d draws %d lines", at, size.w, size.h, got)
			}
			for i, line := range strings.Split(frame, "\n") {
				if got := ansi.StringWidth(line); got > size.w {
					t.Errorf("%s at %dx%d draws a %d-column line %d: %q", at, size.w, size.h, got, i, line)
				}
			}
			switch at {
			case "the projects":
				dr.typeKey("OTHER")
			case "the issue type", "the remap":
				dr.key("enter")
			}
		}
	}
}

func TestMove_EveryRowFillsTheWidth(t *testing.T) {
	t.Parallel()
	f, iss := twoProjects(t)
	dr := newDriver(t, testDeps(f), 100, 24, WithIssues(iss))
	dr.typeKey("OTHER")

	for step, name := range map[step]string{stepType: "the issue types", stepStatus: "the remap"} {
		if dr.m.step != step {
			dr.key("enter")
		}
		for i := range dr.m.rowCount() {
			row := ansi.Strip(dr.m.row(i))
			if got := ansi.StringWidth(row); got != dr.m.lay.width {
				t.Errorf("%s row %d is %d columns wide against a plan of %d: %q", name, i, got, dr.m.lay.width, row)
			}
		}
	}
}

func TestMove_AFailedReadKeepsTheSitesOwnWordsAndTheKeyThatTriesAgain(t *testing.T) {
	t.Parallel()
	f, iss := twoProjects(t)
	dr := newDriver(t, testDeps(f), 100, 20, WithIssues(iss))
	f.FailNext(&jira.TransportError{Op: "read the issue types", Err: errors.New("dial tcp 10.0.0.1:443: i/o timeout")})
	dr.typeKey("OTHER")
	dr.key("esc")

	mustContain(t, dr.view(), "i/o timeout")
	if !strings.Contains(dr.view(), retryHint) {
		t.Errorf("the pane does not say which key tries again (%q):\n%s", retryHint, dr.view())
	}
}

// The five kinds of empty are five sentences. All of them drawing "Asking the
// site" is how a missing connection, a question never asked and a real absence
// became one screen that looked like a hang.
func TestMove_SaysWhichKindOfEmptyItIs(t *testing.T) {
	t.Parallel()
	f, iss := twoProjects(t)

	noSite := newDriver(t, testDeps(nil), 100, 20, WithIssues(iss))
	mustContain(t, noSite.view(), "no Jira connection")

	unasked := newDriver(t, testDeps(f), 100, 20, WithIssues(iss))
	unasked.m.looked, unasked.m.found, unasked.m.loading = false, nil, false
	unasked.m.forget()
	mustContain(t, unasked.view(), "Nothing has been asked of Jira yet.")

	loading := newDriver(t, testDeps(f), 100, 20, WithIssues(iss))
	loading.m.loading, loading.m.found = true, nil
	loading.m.forget()
	mustContain(t, loading.view(), "Asking the site")

	failed := newDriver(t, testDeps(f), 100, 20, WithIssues(iss))
	failed.m.found, failed.m.looked = nil, true
	failed.m.failure = &jira.RateLimitError{RetryAfter: 0}
	failed.m.forget()
	mustContain(t, failed.view(), "The site would not say.", "rate limited by Jira")

	empty := newDriver(t, testDeps(f), 100, 20, WithIssues(iss))
	empty.m.found, empty.m.looked, empty.m.loading = nil, true, false
	empty.m.forget()
	mustContain(t, empty.view(), "No other project came back", "i types a key")
}

// A project with nothing to move to says so rather than drawing an empty list
// that looks like a slow one.
func TestMove_ATargetWithNoIssueTypeSaysSo(t *testing.T) {
	t.Parallel()
	f := newFake(2, jiratest.WithIssues(jiratest.GenFor("OTHER", 2)))
	iss := seeded(t, f, "PROJ-1")
	dr := newDriver(t, testDeps(f), 100, 20, WithIssues(iss))
	dr.typeKey("OTHER")
	dr.send(vocabularyMsg{gen: dr.m.gen, project: "OTHER", types: nil})

	mustContain(t, dr.view(), "can create no issue type in OTHER")
}
