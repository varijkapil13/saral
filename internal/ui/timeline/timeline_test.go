package timeline

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/varijkapil13/saral/internal/ui/kernel"
	"github.com/varijkapil13/saral/pkg/jira"
	"github.com/varijkapil13/saral/pkg/jira/jiratest"
)

// The rule a bar came from is what makes a bar in the wrong place diagnosable,
// so every rule of the cascade has to reach the screen as its own shape and its
// own word — and an issue no rule could date has to say that instead of drawing
// nothing.
func TestTimeline_SaysWhichRuleOfTheCascadeDrewEachBar(t *testing.T) {
	t.Parallel()

	catalogue := defaultCatalogue(t)
	withStartDate := append(slices.Clone(catalogue), jira.Field{
		ID: "customfield_13501", Key: "customfield_13501", Name: "Start date", Custom: true,
		Navigable: true, Searchable: true, Orderable: true,
		ClauseNames: []string{"customfield_13501", "Start date"},
		Schema:      jira.FieldSchema{Type: "date", Custom: "com.atlassian.jira.plugin.system.customfieldtypes:datepicker", CustomID: 13501},
	})

	for name, tc := range map[string]struct {
		build func(*testing.T) *jiratest.Fake
		key   string
		why   string
		dates string
	}{
		"advanced roadmaps target dates": {
			build: func(t *testing.T) *jiratest.Fake {
				f := customFake(nil)
				iss := issueIn("PROJ-1", "Ship the thing")
				iss = withDate(iss, fieldRef(t, f, "Target start"), day(2026, time.March, 2))
				iss = withDate(iss, fieldRef(t, f, "Target end"), day(2026, time.March, 20))
				return customFake([]jira.Issue{iss})
			},
			key: "PROJ-1", why: "target", dates: "2026-03-02 -> 2026-03-20",
		},
		"a start date custom field and the platform due date": {
			build: func(t *testing.T) *jiratest.Fake {
				f := customFake(nil, jiratest.WithFields(withStartDate))
				iss := issueIn("PROJ-1", "Ship the thing")
				iss.Due = day(2026, time.March, 20)
				iss = withDate(iss, fieldRef(t, f, "Start date"), day(2026, time.March, 2))
				return customFake([]jira.Issue{iss}, jiratest.WithFields(withStartDate))
			},
			key: "PROJ-1", why: "start/due", dates: "2026-03-02 -> 2026-03-20",
		},
		"the dates of the sprint the issue is in": {
			build: func(t *testing.T) *jiratest.Fake {
				f := customFake(nil)
				sprint := activeSprint(t, f)
				iss := issueIn("PROJ-1", "Ship the thing")
				iss.Fields = iss.Fields.With(fieldRef(t, f, "Sprint"), jira.FieldValue{
					Kind:    jira.KindOptions,
					Options: []jira.Option{{ID: sprintID(sprint), Label: sprint.Name}},
				})
				return customFake([]jira.Issue{iss})
			},
			key: "PROJ-1", why: "sprint", dates: "",
		},
		"created and a release date, which is a guess": {
			build: func(t *testing.T) *jiratest.Fake {
				iss := issueIn("PROJ-1", "Ship the thing")
				iss.Created = time.Date(2026, time.January, 5, 9, 0, 0, 0, time.UTC)
				iss.FixVersions = []jira.Version{{ID: "v1", Name: "2.0", ReleaseDate: day(2026, time.April, 2)}}
				return customFake([]jira.Issue{iss})
			},
			key: "PROJ-1", why: "guessed", dates: "2026-01-05 -> 2026-04-02",
		},
		"one date and nothing to pair it with": {
			build: func(t *testing.T) *jiratest.Fake {
				iss := issueIn("PROJ-1", "Ship the thing")
				iss.Due = day(2026, time.March, 20)
				return customFake([]jira.Issue{iss})
			},
			key: "PROJ-1", why: "one date", dates: "2026-03-20",
		},
		"a parent spanning the children that have dates": {
			build: func(t *testing.T) *jiratest.Fake {
				parent := issueIn("PROJ-1", "The epic")
				kid := issueIn("PROJ-2", "A child")
				kid.Due = day(2026, time.March, 20)
				kid.Parent = &jira.IssueRef{ID: "9PROJ-1", Key: "PROJ-1"}
				other := issueIn("PROJ-3", "Another child")
				other.Due = day(2026, time.April, 10)
				other.Parent = &jira.IssueRef{ID: "9PROJ-1", Key: "PROJ-1"}
				return customFake([]jira.Issue{parent, kid, other})
			},
			key: "PROJ-1", why: "children", dates: "2026-03-20 -> 2026-04-10",
		},
		"no date at all": {
			build: func(t *testing.T) *jiratest.Fake {
				return customFake([]jira.Issue{issueIn("PROJ-1", "Ship the thing")})
			},
			key: "PROJ-1", why: "no dates", dates: "no dates",
		},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			dr := newDriver(t, testDeps(tc.build(t)), 140, 24)
			row := rowFor(t, dr, tc.key)
			if got := whyLabel(row.rng); got != tc.why {
				t.Errorf("%s is drawn as %q, want %q (rule %d, source %q)",
					tc.key, got, tc.why, row.rng.From.Rule(), row.rng.Source)
			}
			frame := dr.view()
			if !strings.Contains(frame, tc.why) {
				t.Errorf("the frame never says %q:\n%s", tc.why, frame)
			}
			if tc.dates != "" && !strings.Contains(frame, tc.dates) {
				t.Errorf("the detail line never says %q:\n%s", tc.dates, frame)
			}
		})
	}
}

// A bar's shape carries the same fact its word does, because a chart is read by
// shape before it is read by word.
func TestTimeline_DrawsEachProvenanceAsItsOwnShape(t *testing.T) {
	t.Parallel()

	guessed := issueIn("PROJ-1", "Guessed")
	guessed.Created = time.Date(2026, time.March, 2, 9, 0, 0, 0, time.UTC)
	guessed.FixVersions = []jira.Version{{ID: "v1", Name: "2.0", ReleaseDate: day(2026, time.March, 20)}}

	real := issueIn("PROJ-2", "Set by somebody")
	milestone := issueIn("PROJ-3", "One date")
	milestone.Due = day(2026, time.March, 10)

	parent := issueIn("PROJ-4", "Rolled up")
	kid := issueIn("PROJ-5", "A child")
	kid.Due = day(2026, time.March, 12)
	kid.Parent = &jira.IssueRef{ID: "9PROJ-4", Key: "PROJ-4"}

	f := customFake(nil)
	real = withDate(real, fieldRef(t, f, "Target start"), day(2026, time.March, 2))
	real = withDate(real, fieldRef(t, f, "Target end"), day(2026, time.March, 20))

	dr := newDriver(t, testDeps(customFake([]jira.Issue{guessed, real, milestone, parent, kid})), 140, 24)
	g := dr.m.styles.glyphs
	for key, want := range map[string]string{
		"PROJ-1": g.faded,
		"PROJ-2": g.bar,
		"PROJ-3": g.milestone,
		"PROJ-4": g.rollup,
	} {
		row := rowFor(t, dr, key)
		if got := chartOf(t, dr, key); !strings.Contains(got, want) {
			t.Errorf("%s (%s) draws %q and never %q", key, whyLabel(row.rng), got, want)
		}
	}
	if g.faded == g.bar || g.bar == g.rollup || g.rollup == g.milestone || g.faded == g.rollup {
		t.Errorf("two provenances share a glyph: %+v", g)
	}
}

// A read that failed says what the site said, keeps saying it, and names the key
// that tries again — for each of the three refusals a real site produces.
func TestTimeline_AFailedReadSaysWhatTheSiteSaid(t *testing.T) {
	t.Parallel()

	for name, tc := range map[string]struct {
		err  error
		says string
	}{
		"a capability the token has not": {
			err:  &jira.CapabilityError{Capability: jira.CapBoards, Reason: "you need Browse Projects on PROJ"},
			says: "you need Browse Projects on PROJ",
		},
		"a rate limit": {
			err:  &jira.RateLimitError{RetryAfter: 30 * time.Second},
			says: "rate limited by Jira",
		},
		"a transport failure": {
			err:  &jira.TransportError{Op: "search", Err: errors.New("dial tcp: no route to host")},
			says: "no route to host",
		},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			f := newFake(10)
			f.FailNextN(4, tc.err)
			dr := newDriver(t, testDeps(f), 120, 20)

			frame := dr.view()
			mustContain(t, frame, "The timeline could not be read.", retryHint)
			if !strings.Contains(strings.ToLower(frame), strings.ToLower(tc.says)) {
				t.Errorf("the pane never says %q:\n%s", tc.says, frame)
			}
			if dr.lastStatus().Level != kernel.LevelError {
				t.Errorf("the status line is level %d, want an error", dr.lastStatus().Level)
			}
			// The status line goes with the next keypress; the pane must not.
			dr.key("j")
			mustContain(t, dr.view(), "The timeline could not be read.")
		})
	}
}

// A refusal over bars already drawn badges them rather than clearing them: the
// last true answer this session had beats an empty chart.
func TestTimeline_ARefusalAfterAChartIsDrawnBadgesItRatherThanEmptyingIt(t *testing.T) {
	t.Parallel()

	f := newFake(10)
	dr := newDriver(t, testDeps(f), 120, 20)
	before := len(dr.m.rows)
	if before == 0 {
		t.Fatal("nothing was drawn to badge")
	}

	f.FailNextN(4, &jira.RateLimitError{RetryAfter: time.Minute})
	dr.send(kernel.RefreshMsg{})

	if len(dr.m.rows) != before {
		t.Errorf("the chart went from %d bars to %d; a failed refresh must keep what is on screen", before, len(dr.m.rows))
	}
	mustContain(t, dr.view(), "stale")
}

// An answer to a question the user has already changed is dropped rather than
// drawn over the answer to the question they are now asking.
func TestTimeline_AnAnswerToASupersededReadIsDropped(t *testing.T) {
	t.Parallel()

	dr := newDriver(t, testDeps(newFake(10)), 120, 20)
	was := len(dr.m.rows)
	dr.send(loadedMsg{gen: dr.m.gen - 1, issues: []jira.Issue{issueIn("OTHER-1", "Not this")}})
	if len(dr.m.rows) != was {
		t.Errorf("a stale answer replaced the chart: %d bars, want %d", len(dr.m.rows), was)
	}
	if dr.m.rows[0].key == "OTHER-1" {
		t.Error("the stale answer's issue reached the chart")
	}
}

// Losing the keyboard is not being closed. A palette opened over a loading chart
// must not cancel the load.
func TestTimeline_KeepsItsReadWhenItLosesTheKeys(t *testing.T) {
	t.Parallel()

	view, ok := New(testDeps(newFake(10))).(*Model)
	if !ok {
		t.Fatal("New did not return a *Model")
	}
	view.resize(120, 20)
	cmd := view.Init()
	next, _ := view.Update(kernel.FocusMsg{Focused: false})
	view = mustModel(t, next)
	if view.cancel == nil {
		t.Fatal("the read was let go of on a blur")
	}

	msg := answer(cmd)
	if _, failed := msg.(failedMsg); failed {
		t.Fatalf("the read came back as a failure: %v", msg)
	}
	if _, ok := msg.(loadedMsg); !ok {
		t.Fatalf("the read came back as %T, want a chart", msg)
	}
}

// The read asks for the fields the cascade needs and nothing else, and it asks
// for the custom ones by the id this site gave them rather than by a
// customfield_NNNNN written down here.
func TestTimeline_AsksForTheNarrowFieldSetTheCascadeNeeds(t *testing.T) {
	t.Parallel()

	f := newFake(10)
	client := newSpy(f)
	dr := newDriver(t, testDeps(client), 120, 20)
	_ = dr

	query := client.lastQuery(t)
	target := fieldRef(t, f, "Target start")
	for _, want := range []string{"summary", "duedate", "created", "fixVersions", "parent", target.ID} {
		if !slices.Contains(query.Fields, want) {
			t.Errorf("the search did not ask for %q; it asked for %v", want, query.Fields)
		}
	}
	if strings.HasPrefix(target.ID, "customfield_") && strings.Contains(strings.Join(query.Fields, ","), "customfield_13405") {
		t.Log("the fake happens to allocate 13405 for Target start; the id is still resolved rather than written down")
	}
	for _, unwanted := range []string{"description", "*all", "issuelinks", "timetracking"} {
		if slices.Contains(query.Fields, unwanted) {
			t.Errorf("the search asked for %q, which no bar is drawn from: %v", unwanted, query.Fields)
		}
	}
}

// A day is a question about a timezone. A created instant bucketed in the wrong
// zone puts the bar on the wrong day.
func TestTimeline_BucketsADayInTheAccountsZoneAndNotTheMachines(t *testing.T) {
	t.Parallel()

	iss := issueIn("PROJ-1", "Filed late in the day")
	iss.Created = time.Date(2026, time.March, 4, 20, 0, 0, 0, time.UTC)
	iss.FixVersions = []jira.Version{{ID: "v1", Name: "2.0", ReleaseDate: day(2026, time.April, 2)}}

	for name, tc := range map[string]struct {
		zone *time.Location
		want jira.Date
	}{
		"utc":               {time.UTC, day(2026, time.March, 4)},
		"thirteen hours on": {time.FixedZone("Pacific/Testing", 13*3600), day(2026, time.March, 5)},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			deps := testDeps(customFake([]jira.Issue{iss}))
			deps.Caps.TimeZone = tc.zone
			dr := newDriver(t, deps, 140, 20)
			row := rowFor(t, dr, "PROJ-1")
			if row.rng.Start != tc.want {
				t.Errorf("the bar starts on %s, want %s", row.rng.Start, tc.want)
			}
			mustContain(t, dr.view(), tc.want.String())
		})
	}
}

// A cascade field this site does not have is normal — Target start exists only
// with Advanced Roadmaps — and it still has to be said, or a bar drawn from a
// later rule looks like a bar drawn from this one.
func TestTimeline_NamesACascadeFieldTheSiteDoesNotHave(t *testing.T) {
	t.Parallel()

	thin := slices.DeleteFunc(defaultCatalogue(t), func(f jira.Field) bool {
		return f.Name == "Target start" || f.Name == "Target end"
	})
	dr := newDriver(t, testDeps(newFake(10, jiratest.WithFields(thin))), 120, 24)

	notes := strings.Join(dr.m.noteLines, "\n")
	mustContain(t, notes, "Target start", "Target end")
	mustContain(t, dr.view(), notesHint)

	dr.key("n")
	mustContain(t, dr.view(), "Target start")
}

// Sprint markers need boards. A token that cannot see them gets the site's own
// reason rather than a chart quietly missing its boundaries.
func TestTimeline_SaysWhySprintMarkersAreMissingWhenBoardsAreRefused(t *testing.T) {
	t.Parallel()

	f := newFake(10, jiratest.WithCapabilities(jiratest.NoBoards))
	dr := newDriver(t, testDeps(f), 120, 24)
	deps := testDeps(f)
	deps.Caps.Boards = jira.Capability{Reason: "this project has no board"}
	dr = newDriver(t, deps, 120, 24)

	notes := strings.Join(dr.m.noteLines, "\n")
	mustContain(t, notes, "no sprint markers", "this project has no board")
	if len(dr.m.sprintMarks) > 0 {
		t.Errorf("%d sprint markers were drawn without the capability to read them", len(dr.m.sprintMarks))
	}
	if countCalls(f, "Sprints") > 0 {
		t.Error("the sprints were read anyway")
	}
}

// An issue in a sprint falls through to a later rule when the sprint cannot be
// read, and the pass says so rather than losing the bar without comment.
func TestTimeline_ASprintItCannotReadIsANoteAndNotAnEmptyChart(t *testing.T) {
	t.Parallel()

	f := customFake(nil)
	iss := issueIn("PROJ-1", "In a sprint")
	iss.Created = time.Date(2026, time.January, 5, 9, 0, 0, 0, time.UTC)
	iss.FixVersions = []jira.Version{{ID: "v1", Name: "2.0", ReleaseDate: day(2026, time.April, 2)}}
	iss.Fields = iss.Fields.With(fieldRef(t, f, "Sprint"), jira.FieldValue{
		Kind:    jira.KindOptions,
		Options: []jira.Option{{ID: "999999", Label: "A sprint nobody can see"}},
	})

	dr := newDriver(t, testDeps(customFake([]jira.Issue{iss})), 140, 24)
	row := rowFor(t, dr, "PROJ-1")
	if got := whyLabel(row.rng); got != "guessed" {
		t.Errorf("the bar is drawn as %q, want the rule below the sprint", got)
	}
	mustContain(t, strings.Join(dr.m.noteLines, "\n"), "sprint 999999 could not be read")
}

// A search that came back with issues and no dates at all is the state this view
// exists to make diagnosable, and it is a different screen from a search that
// came back with nothing.
func TestTimeline_SaysWhenNothingResolvedAtAll(t *testing.T) {
	t.Parallel()

	dr := newDriver(t, testDeps(customFake([]jira.Issue{
		issueIn("PROJ-1", "One"), issueIn("PROJ-2", "Two"),
	})), 120, 20)

	if dr.m.res.Resolved() != 0 {
		t.Fatalf("%d of these resolved, so this is not the state under test", dr.m.res.Resolved())
	}
	mustContain(t, dr.view(), "none of these 2 issues carries a date", "no dates")
	mustNotContain(t, dr.view(), "Nothing matches this search")
}

func TestTimeline_TellsTheKindsOfEmptyApart(t *testing.T) {
	t.Parallel()

	for name, tc := range map[string]struct {
		build func(*testing.T) *driver
		says  string
	}{
		"no connection at all": {
			build: func(t *testing.T) *driver { return newDriver(t, testDeps(nil), 120, 20) },
			says:  "No Jira connection in this session yet.",
		},
		"a search that matched nothing": {
			build: func(t *testing.T) *driver { return newDriver(t, testDeps(customFake(nil)), 120, 20) },
			says:  "Nothing matches this search.",
		},
		"a read still out with the site": {
			build: func(t *testing.T) *driver {
				view, ok := New(testDeps(newFake(5))).(*Model)
				if !ok {
					t.Fatal("New did not return a *Model")
				}
				view.resize(120, 20)
				_ = view.load()
				return &driver{t: t, m: view}
			},
			says: "Reading the fields these dates come from",
		},
		"nothing asked for yet": {
			build: func(t *testing.T) *driver {
				view, ok := New(testDeps(newFake(5))).(*Model)
				if !ok {
					t.Fatal("New did not return a *Model")
				}
				view.resize(120, 20)
				return &driver{t: t, m: view}
			},
			says: "Nothing has been asked of Jira yet.",
		},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			mustContain(t, tc.build(t).view(), tc.says)
		})
	}
}

// Zooming is a change of detail and not a change of place: the day in the middle
// of the chart stays in the middle of the chart. It is only a question worth
// asking where the span is wider than the chart, so the span here is twenty
// years — wider than a 64-column chart even at the quarter zoom.
func TestTimeline_ZoomKeepsTheDayInTheMiddleInTheMiddle(t *testing.T) {
	t.Parallel()

	var wide []jira.Issue
	for year := 2016; year <= 2036; year++ {
		iss := issueIn("PROJ-"+strconv.Itoa(year), "Due in "+strconv.Itoa(year))
		iss.Due = day(year, time.June, 15)
		wide = append(wide, iss)
	}
	dr := newDriver(t, testDeps(customFake(wide)), 120, 24)
	middle := func() jira.Date {
		return dr.m.ax.start(min(dr.m.left+dr.m.lay.chart/2, dr.m.ax.cols-1))
	}

	for _, step := range []string{"+", "+", "-", "-", "-"} {
		was, wasZoom := middle(), dr.m.zoom
		dr.key(step)
		if dr.m.zoom == wasZoom {
			continue
		}
		if dr.m.ax.cols <= dr.m.lay.chart {
			t.Fatalf("the %s zoom fits the whole span in the chart, so there is no middle to keep", dr.m.zoom)
		}
		// One column of the coarser of the two scales is the tolerance: the
		// middle can only be named to the precision the new scale has.
		if drift := daysBetween(was, middle()); drift > 92 || drift < -92 {
			t.Errorf("%s moved the middle of the chart from %s to %s (%d days)", step, was, middle(), drift)
		}
	}
	if dr.m.zoom != ZoomQuarter {
		t.Errorf("the walk left the zoom at %s", dr.m.zoom)
	}
}

// A span narrower than the chart has nothing to pan, so a zoom leaves the window
// at the left edge rather than pushing the bars off it.
func TestTimeline_AZoomOverASpanThatFitsLeavesTheWindowAlone(t *testing.T) {
	t.Parallel()

	iss := issueIn("PROJ-1", "A fortnight")
	iss.Due = day(2026, time.March, 20)
	dr := newDriver(t, testDeps(customFake([]jira.Issue{iss})), 120, 24)
	for _, step := range []string{"-", "-", "+", "+", "+"} {
		dr.key(step)
		if dr.m.ax.cols > dr.m.lay.chart {
			continue
		}
		if dr.m.left != 0 {
			t.Fatalf("the %s zoom put the window at column %d over a span of %d columns in a chart of %d",
				dr.m.zoom, dr.m.left, dr.m.ax.cols, dr.m.lay.chart)
		}
	}
}

// A key that answers with nothing and a key that is not bound look the same, so
// the end of the scale says so.
func TestTimeline_ZoomStopsAtBothEndsAndSaysSo(t *testing.T) {
	t.Parallel()

	dr := newDriver(t, testDeps(newFake(20)), 120, 20)
	dr.key("+")
	dr.key("+")
	if dr.m.zoom != ZoomDay {
		t.Fatalf("the zoom is %s, want the finest", dr.m.zoom)
	}
	dr.key("+")
	mustContain(t, dr.lastStatus().Text, "finest")

	for range 4 {
		dr.key("-")
	}
	if dr.m.zoom != ZoomQuarter {
		t.Fatalf("the zoom is %s, want the coarsest", dr.m.zoom)
	}
	dr.key("-")
	mustContain(t, dr.lastStatus().Text, "coarsest")
}

func TestTimeline_CentresOnTodayOnDemand(t *testing.T) {
	t.Parallel()

	dr := newDriver(t, testDeps(newFake(200)), 120, 24)
	dr.key("+", "+") // the day zoom, where the whole span cannot fit
	if dr.m.ax.cols <= dr.m.lay.chart {
		t.Fatalf("the span is %d columns and the chart is %d, so there is nothing to pan", dr.m.ax.cols, dr.m.lay.chart)
	}
	dr.key("home")
	for range 40 {
		dr.key("h")
	}
	if dr.m.left == 0 {
		t.Log("the window was already at the left edge")
	}
	dr.key("T")
	today := dr.m.ax.col(dr.m.today())
	if today < dr.m.left || today >= dr.m.left+dr.m.lay.chart {
		t.Errorf("today is column %d and the window is [%d, %d)", today, dr.m.left, dr.m.left+dr.m.lay.chart)
	}
}

// Panning cannot leave the calendar: the window stops at both ends of the span.
func TestTimeline_PanningStopsAtBothEndsOfTheSpan(t *testing.T) {
	t.Parallel()

	dr := newDriver(t, testDeps(newFake(200)), 120, 24)
	dr.key("+", "+")
	for range 400 {
		dr.key("h")
	}
	if dr.m.left != 0 {
		t.Errorf("panning left stopped at column %d, want 0", dr.m.left)
	}
	for range 800 {
		dr.key("l")
	}
	if want := max(dr.m.ax.cols-dr.m.lay.chart, 0); dr.m.left != want {
		t.Errorf("panning right stopped at column %d, want %d", dr.m.left, want)
	}
}

func TestTimeline_OpensTheIssueUnderTheCursor(t *testing.T) {
	t.Parallel()

	dr := newDriver(t, testDeps(newFake(20)), 120, 24)
	dr.key("j")
	want := dr.m.selectedKey()
	dr.key("enter")

	if len(dr.pushes) != 1 {
		t.Fatalf("enter pushed %d views, want one", len(dr.pushes))
	}
	if dr.pushes[0].Title != want {
		t.Errorf("the pane pushed is titled %q, want %q", dr.pushes[0].Title, want)
	}
}

// The whole point of the cascade's provenance is that it survives a look at the
// chart, so the row's rule is on screen and stays there after a keypress clears
// the status line.
func TestTimeline_TheSelectedBarsProvenanceStaysOnScreen(t *testing.T) {
	t.Parallel()

	dr := newDriver(t, testDeps(newFake(20)), 140, 24)
	row := dr.m.selected()
	if row == nil {
		t.Fatal("nothing is selected")
	}
	mustContain(t, dr.view(), row.key, row.rng.From.String(), row.rng.Source)
	dr.key("j")
	next := dr.m.selected()
	mustContain(t, dr.view(), next.key, next.rng.From.String())
}

// A project switch retargets the chart, because a chart of somewhere else under
// a header naming here is worse than an empty one.
func TestTimeline_FollowsAProjectSwitch(t *testing.T) {
	t.Parallel()

	f := jiratest.New(
		jiratest.WithProject("PROJ", jiratest.Scrum),
		jiratest.WithProject("OTHER", jiratest.Scrum),
		jiratest.WithIssues(jiratest.Gen(10)),
		jiratest.WithIssues(jiratest.GenFor("OTHER", 6)),
	)
	dr := newDriver(t, testDeps(f), 120, 24)
	mustContain(t, dr.view(), "Timeline of PROJ")

	dr.send(kernel.ProjectMsg{Project: "OTHER"})
	mustContain(t, dr.view(), "Timeline of OTHER")
	for _, row := range dr.m.rows {
		if !strings.HasPrefix(row.key, "OTHER-") {
			t.Fatalf("%s is still on the chart after the switch", row.key)
		}
	}
}

// The first frame is drawn from what the last session left on disk, before
// anything at all is asked of the site.
func TestTimeline_FirstPaintComesFromTheCacheBeforeAnythingIsAsked(t *testing.T) {
	t.Parallel()

	f := newFake(10)
	deps := testDeps(f)
	cache := newMemCache()
	deps.Cache = cache

	stored := issueIn("PROJ-77", "Stored last time")
	stored.Due = day(2026, time.March, 12)
	jql, _ := defaultQuery("PROJ")
	if err := cache.PutRows(jql, []jira.Issue{stored}, false); err != nil {
		t.Fatal(err)
	}

	view, ok := New(deps).(*Model)
	if !ok {
		t.Fatal("New did not return a *Model")
	}
	view.resize(120, 20)
	frame := view.View()
	mustContain(t, frame, "PROJ-77", "stored")
	if got := len(f.Calls()); got != 0 {
		t.Errorf("the first frame made %d calls to the site: %v", got, f.Calls())
	}
	if view.badge != "stored" {
		t.Errorf("the badge is %q; rows dated without the site's field list have to say so", view.badge)
	}
}

// Rows off disk were dated by a cascade with no field catalogue behind it, so the
// badge comes off only when the real read lands.
func TestTimeline_TheStoredBadgeComesOffWhenTheSiteAnswers(t *testing.T) {
	t.Parallel()

	deps := testDeps(newFake(10))
	cache := newMemCache()
	deps.Cache = cache
	jql, _ := defaultQuery("PROJ")
	stored := issueIn("PROJ-77", "Stored last time")
	stored.Due = day(2026, time.March, 12)
	if err := cache.PutRows(jql, []jira.Issue{stored}, false); err != nil {
		t.Fatal(err)
	}

	dr := newDriver(t, deps, 120, 20)
	if dr.m.badge != "" {
		t.Errorf("the badge is still %q after the site answered", dr.m.badge)
	}
	mustNotContain(t, dr.view(), "PROJ-77")
}

// Explicit configuration wins over every rule under it, which is the whole point
// of having it.
func TestTimeline_ConfiguredFieldsWinOverTheCascadesOwn(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("SARAL_CONFIG_DIR", dir)
	write := `active = "work"

[profiles.work]
site  = "example.atlassian.net"
email = "sam@example.invalid"

[profiles.work.token]
env = "SARAL_TOKEN"

[profiles.work.timeline]
start = ["Target end"]
end   = ["Target end"]
`
	if err := os.WriteFile(filepath.Join(dir, "config.toml"), []byte(write), 0o600); err != nil {
		t.Fatal(err)
	}

	f := customFake(nil)
	iss := issueIn("PROJ-1", "Ship the thing")
	iss = withDate(iss, fieldRef(t, f, "Target start"), day(2026, time.March, 2))
	iss = withDate(iss, fieldRef(t, f, "Target end"), day(2026, time.March, 20))

	dr := newDriver(t, testDeps(customFake([]jira.Issue{iss})), 140, 24)
	row := rowFor(t, dr, "PROJ-1")
	if got := whyLabel(row.rng); got != "config" {
		t.Fatalf("the bar came from %q, want the fields the profile names", got)
	}
	// Both ends were configured to Target end, so the range is a point on it and
	// not the target pair the rule below would have produced.
	if row.rng.Start != day(2026, time.March, 20) || row.rng.End != day(2026, time.March, 20) {
		t.Errorf("the bar spans %s to %s, want the configured field at both ends", row.rng.Start, row.rng.End)
	}
}

// A profile on another site must not lend this session its field names: a
// --profile run would otherwise date one account's issues by another's fields.
func TestTimeline_IgnoresAProfileForAnotherSite(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("SARAL_CONFIG_DIR", dir)
	write := `active = "elsewhere"

[profiles.elsewhere]
site  = "somewhere-else.atlassian.net"
email = "sam@example.invalid"

[profiles.elsewhere.token]
env = "SARAL_TOKEN"

[profiles.elsewhere.timeline]
start = ["Target end"]
end   = ["Target end"]
`
	if err := os.WriteFile(filepath.Join(dir, "config.toml"), []byte(write), 0o600); err != nil {
		t.Fatal(err)
	}

	start, end := configuredFields("example.atlassian.net")
	if len(start) > 0 || len(end) > 0 {
		t.Errorf("a profile on another site handed over %v and %v", start, end)
	}
	if start, end = configuredFields("somewhere-else.atlassian.net"); len(start) != 1 || len(end) != 1 {
		t.Errorf("the profile for this site handed over %v and %v", start, end)
	}
}

// A search bigger than a chart can hold is truncated, and the truncation is said
// rather than silently applied.
func TestTimeline_SaysWhenItIsShowingOnlyPartOfTheSearch(t *testing.T) {
	t.Parallel()

	dr := newDriver(t, testDeps(newFake(maxIssues+40)), 120, 24)
	if len(dr.m.rows) != maxIssues {
		t.Fatalf("the chart holds %d bars, want the cap of %d", len(dr.m.rows), maxIssues)
	}
	if !dr.m.truncated {
		t.Fatal("the chart does not know it is showing part of the answer")
	}
	mustContain(t, strings.Join(dr.m.noteLines, "\n"), "holds the first "+strconv.Itoa(maxIssues))
}

// --- helpers ----------------------------------------------------------------

func itoa(n int) string {
	return strings.TrimSpace(strings.Join([]string{"", ""}, "")) + intToString(n)
}

func intToString(n int) string {
	if n == 0 {
		return "0"
	}
	var digits []byte
	for n > 0 {
		digits = append([]byte{byte('0' + n%10)}, digits...)
		n /= 10
	}
	return string(digits)
}

func mustModel(t *testing.T, view kernel.View) *Model {
	t.Helper()
	m, ok := view.(*Model)
	if !ok {
		t.Fatal("Update did not return a *Model")
	}
	return m
}

// answer is what the kernel hands a view: the command's own reply with the
// envelope the kernel addresses it by taken off.
func answer(cmd tea.Cmd) tea.Msg {
	msg := cmd()
	if cmds, ok := unwrapCmds(msg); ok {
		for _, one := range cmds {
			if got := answer(one); got != nil {
				if _, isLoad := got.(loadedMsg); isLoad {
					return got
				}
				if _, isFail := got.(failedMsg); isFail {
					return got
				}
			}
		}
		return nil
	}
	if reply, addressed := msg.(kernel.ReplyMsg); addressed {
		return reply.Msg
	}
	return msg
}

func rowFor(t *testing.T, dr *driver, key string) barRow {
	t.Helper()
	for i := range dr.m.rows {
		if dr.m.rows[i].key == key {
			dr.m.cursor = i
			dr.m.scrollToCursor()
			return dr.m.rows[i]
		}
	}
	t.Fatalf("no row for %s; the chart holds %d", key, len(dr.m.rows))
	return barRow{}
}

// chartOf is the chart half of one row's line, with the styling taken off.
func chartOf(t *testing.T, dr *driver, key string) string {
	t.Helper()
	row := rowFor(t, dr, key)
	_ = row
	for _, line := range strings.Split(dr.view(), "\n") {
		if strings.Contains(line, key+" ") || strings.HasPrefix(strings.TrimSpace(line), key) {
			if len(line) > dr.m.lay.prefix() {
				return line[dr.m.lay.prefix():]
			}
			return ""
		}
	}
	t.Fatalf("no line for %s in:\n%s", key, dr.view())
	return ""
}

func defaultCatalogue(t *testing.T) []jira.Field {
	t.Helper()
	fields, err := jiratest.New().Fields(context.Background())
	if err != nil {
		t.Fatalf("reading the field catalogue: %v", err)
	}
	return fields
}

func activeSprint(t *testing.T, f *jiratest.Fake) jira.Sprint {
	t.Helper()
	boards, err := f.Boards(context.Background(), "PROJ")
	if err != nil || len(boards) == 0 {
		t.Fatalf("the fake has no board on PROJ: %v", err)
	}
	page, err := f.Sprints(context.Background(), boards[0].ID, jira.SprintActive)
	if err != nil || len(page.Items) == 0 {
		t.Fatalf("the fake's board has no active sprint: %v", err)
	}
	return page.Items[0]
}

func sprintID(s jira.Sprint) string { return strconv.FormatInt(s.ID, 10) }

// An issue's sprint value arrives as JSON text on the read a timeline makes: a
// timeline asks for a custom field by name, which is what makes the site send a
// schema, and the sprint field's schema is an array of json that this client has
// no slot for. The typed shape is what an endpoint that sent no schema produces.
func TestTimeline_ReadsASprintValueInEitherShapeItArrivesIn(t *testing.T) {
	t.Parallel()

	f := customFake(nil)
	sprint := activeSprint(t, f)
	ref := fieldRef(t, f, "Sprint")

	for name, value := range map[string]jira.FieldValue{
		"as options, from a read with no schema": {
			Kind:    jira.KindOptions,
			Options: []jira.Option{{ID: sprintID(sprint), Label: sprint.Name}},
		},
		"as text, from a read that asked by name": {
			Kind: jira.KindText,
			Text: `[{"id":` + sprintID(sprint) + `,"name":"` + sprint.Name + `","state":"active"}]`,
		},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			iss := issueIn("PROJ-1", "In a sprint")
			iss.Fields = iss.Fields.With(ref, value)
			dr := newDriver(t, testDeps(customFake([]jira.Issue{iss})), 140, 24)
			row := rowFor(t, dr, "PROJ-1")
			if got := whyLabel(row.rng); got != "sprint" {
				t.Errorf("the bar came from %q, want the sprint's own dates (source %q)", got, row.rng.Source)
			}
			if !strings.Contains(row.rng.Source, sprint.Name) {
				t.Errorf("the bar names %q as its source, which does not mention %q", row.rng.Source, sprint.Name)
			}
			mustContain(t, dr.view(), sprint.Name)
		})
	}
}

// An archived version is not a date anybody is working towards, and the fake
// never produces one, so the filter is held here rather than through a read.
func TestTimeline_LeavesAnArchivedVersionOffTheChart(t *testing.T) {
	t.Parallel()

	dr := newDriver(t, testDeps(newFake(10)), 140, 24)
	dr.send(markersMsg{
		gen: dr.m.gen,
		versions: []jira.Version{
			{ID: "v1", Name: "live", ReleaseDate: day(2026, time.April, 2)},
			{ID: "v2", Name: "archived", ReleaseDate: day(2026, time.April, 9), Archived: true},
			{ID: "v3", Name: "undated"},
		},
	})

	var names []string
	for _, v := range dr.m.versionMarks {
		names = append(names, v.name)
	}
	if len(names) != 1 || names[0] != "live" {
		t.Errorf("the chart marks %v, want only the live dated version", names)
	}
}
