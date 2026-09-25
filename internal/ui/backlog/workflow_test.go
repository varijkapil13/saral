package backlog

import (
	"errors"
	"slices"
	"strconv"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/varijkapil13/saral/internal/ui/filter"
	"github.com/varijkapil13/saral/internal/ui/kernel"
	"github.com/varijkapil13/saral/pkg/jira"
	"github.com/varijkapil13/saral/pkg/jira/jiratest"
)

var ada = jira.User{AccountID: "acct-ada", DisplayName: "Ada Lovelace", Active: true}

func refusals() map[string]error {
	return map[string]error{
		"a 403":               &jira.CapabilityError{Reason: "you need Schedule Issues in this project"},
		"a rate limit":        &jira.RateLimitError{RetryAfter: 30 * time.Second},
		"a transport failure": &jira.TransportError{Op: "PUT /issue/rank", Err: errors.New("connection reset")},
	}
}

// section is the keys of one section, in the order drawn.
func (d *driver) section(name string) []string {
	d.t.Helper()
	for g := range d.m.groups {
		if d.m.groups[g].name != name {
			continue
		}
		out := make([]string, 0, len(d.m.groups[g].issues))
		for _, at := range d.m.groups[g].issues {
			out = append(out, d.m.issues[at].Key)
		}
		return out
	}
	d.t.Fatalf("no section is called %s", name)
	return nil
}

func (d *driver) under() string {
	if iss := d.m.issueAt(d.m.cursor); iss != nil {
		return iss.Key
	}
	return ""
}

func TestRank_EveryDirectionLandsWhereItSaysAndTheSiteAgrees(t *testing.T) {
	t.Parallel()
	for name, tc := range map[string]struct {
		at   int
		key  string
		want func([]string) []string
	}{
		"K moves it above the one before": {at: 2, key: "K", want: func(c []string) []string {
			return append([]string{c[0], c[2], c[1]}, c[3:]...)
		}},
		"J moves it below the one after": {at: 0, key: "J", want: func(c []string) []string {
			return append([]string{c[1], c[0]}, c[2:]...)
		}},
		"{ puts it first": {at: 3, key: "{", want: func(c []string) []string {
			return append([]string{c[3], c[0], c[1], c[2]}, c[4:]...)
		}},
		"} puts it last": {at: 0, key: "}", want: func(c []string) []string {
			return append(slices.Clone(c[1:]), c[0])
		}},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			fake := newFake(12)
			dr := newDriver(t, testDeps(fake), 120, 24)
			before := dr.section(backlogName)
			dr.cursorTo("row:" + before[tc.at])

			dr.key(tc.key)

			want := tc.want(before)
			if got := dr.section(backlogName); !slices.Equal(got, want) {
				t.Errorf("the backlog reads %v, want %v", got, want)
			}
			if got := dr.under(); got != before[tc.at] {
				t.Errorf("the cursor is on %s, want it to stay on %s", got, before[tc.at])
			}
			if got := countCalls(fake, "RankIssues"); got != 1 {
				t.Errorf("%s made %d rank calls, want 1", tc.key, got)
			}
			dr.send(kernel.RefreshMsg{})
			if got := dr.section(backlogName); !slices.Equal(got, want) {
				t.Errorf("a re-read draws %v, want the ranked order %v", got, want)
			}
		})
	}
}

func TestRank_ARefusalPutsTheIssueBack(t *testing.T) {
	t.Parallel()
	errs := refusals()
	errs["a 207 that refused the issue"] = &jira.PartialRankError{
		Failed: []jira.RankFailure{{Key: "PROJ-4", Reason: "You cannot rank this issue."}},
	}
	for name, err := range errs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			fake := newFake(12)
			dr := newDriver(t, testDeps(fake), 120, 24)
			before := dr.section(backlogName)
			dr.cursorTo("row:" + before[2])
			fake.FailNext(err)
			dr.key("K")
			if got := dr.section(backlogName); !slices.Equal(got, before) {
				t.Errorf("the backlog reads %v after a refused rank, want %v", got, before)
			}
			if got := dr.under(); got != before[2] {
				t.Errorf("the cursor is on %s, want it on %s", got, before[2])
			}
			if got := dr.lastStatus().Level; got != kernel.LevelError {
				t.Errorf("the refusal was reported at level %v", got)
			}
		})
	}
}

func TestRank_StepsTakenWhileOneIsOutAreSentInOrder(t *testing.T) {
	t.Parallel()
	fake := newFake(12)
	dr := newDriver(t, testDeps(fake), 120, 24)
	before := dr.section(backlogName)
	dr.cursorTo("row:" + before[3])

	first := dr.hold(keyPress("K"))
	if second := dr.hold(keyPress("K")); second != nil {
		t.Fatal("a second step was sent while the first was still out")
	}
	dr.run(first)

	want := append([]string{before[0], before[3], before[1], before[2]}, before[4:]...)
	if got := dr.section(backlogName); !slices.Equal(got, want) {
		t.Errorf("the backlog reads %v, want %v", got, want)
	}
	if got := countCalls(fake, "RankIssues"); got != 2 {
		t.Errorf("two steps made %d rank calls, want 2", got)
	}
	dr.send(kernel.RefreshMsg{})
	if got := dr.section(backlogName); !slices.Equal(got, want) {
		t.Errorf("the site holds %v, want %v", got, want)
	}
}

func TestRank_RefusedWhereItCannotShow(t *testing.T) {
	t.Parallel()
	sorted := newDriver(t, testDeps(newFake(8)), 120, 24)
	sorted.m.sort = sortChoice{field: "key"}
	sorted.m.regroup()
	sorted.cursorTo("row:" + sorted.section(backlogName)[1])
	sorted.key("K")
	mustContain(t, sorted.lastStatus().Text, "sorted by key")

	kanban := jiratest.New(jiratest.WithProject("PROJ", jiratest.Kanban), jiratest.WithIssues(jiratest.Gen(8)))
	plain := newDriver(t, testDeps(kanban), 120, 24)
	plain.cursorTo("row:" + plain.section(backlogName)[1])
	plain.key("K")
	mustContain(t, plain.lastStatus().Text, "no rank field")
	if got := countCalls(kanban, "RankIssues"); got != 0 {
		t.Errorf("a board with no rank field made %d rank calls", got)
	}
}

func TestRank_ADragWithinASectionRanksTheIssue(t *testing.T) {
	t.Parallel()
	fake := newFake(12)
	d := testDeps(fake)
	dr := newDriver(t, d, 120, 24)
	before := dr.section(backlogName)

	pressOn(t, d, dr, "row:"+before[0])
	toX, toY := at(t, d, dr, "row:"+before[2])
	dr.send(tea.MouseMotionMsg{X: toX, Y: toY, Button: tea.MouseLeft})
	dr.send(tea.MouseReleaseMsg{X: toX, Y: toY, Button: tea.MouseLeft})

	if dr.m.mode != browsing {
		t.Fatalf("a drag within a section opened mode %d", dr.m.mode)
	}
	want := append([]string{before[1], before[2], before[0]}, before[3:]...)
	if got := dr.section(backlogName); !slices.Equal(got, want) {
		t.Errorf("the backlog reads %v after the drag, want %v", got, want)
	}
	if got := countCalls(fake, "RankIssues"); got != 1 {
		t.Errorf("the drag made %d rank calls, want 1", got)
	}
}

func TestMine_OToggleIsAnAssigneeTermTheBarNames(t *testing.T) {
	t.Parallel()
	fake := newFake(12, jiratest.WithMe(ada))
	dr := newDriver(t, testDeps(fake), 120, 24)

	dr.key("o")
	want := filter.Term{Facet: filter.FacetAssignee, ID: ada.AccountID}
	if !dr.m.terms.Has(want) || len(dr.m.terms) != 1 {
		t.Fatalf("o put %v in force, want only Ada", dr.m.terms)
	}
	for g := range dr.m.groups {
		for _, at := range dr.m.groups[g].issues {
			if a := dr.m.issues[at].Assignee; a == nil || a.AccountID != ada.AccountID {
				t.Errorf("%s is shown under only-mine", dr.m.issues[at].Key)
			}
		}
	}
	mustContain(t, dr.view(), "Ada Lovelace x")
	dr.key("o")
	if len(dr.m.terms) != 0 {
		t.Errorf("a second o left %v in force", dr.m.terms)
	}
	if got := countCalls(fake, "Me"); got != 1 {
		t.Errorf("two toggles asked who this is %d times", got)
	}
}

func TestMine_ARefusalIsReported(t *testing.T) {
	t.Parallel()
	for name, err := range refusals() {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			fake := newFake(6, jiratest.WithMe(ada))
			dr := newDriver(t, testDeps(fake), 120, 24)
			fake.FailNext(err)
			dr.key("o")
			if len(dr.m.terms) != 0 {
				t.Errorf("a refused Me put %v in force", dr.m.terms)
			}
			if got := dr.lastStatus().Level; got != kernel.LevelError {
				t.Errorf("the refusal was reported at level %v", got)
			}
		})
	}
}

func typeInto(dr *driver, s string) {
	for _, r := range s {
		dr.send(tea.KeyPressMsg{Code: r, Text: string(r)})
	}
}

func TestFind_TypingMovesTheCursorAndNWalksTheMatches(t *testing.T) {
	t.Parallel()
	dr := newDriver(t, testDeps(newFake(30)), 120, 24)
	dr.loadAll()
	dr.m.moveTo(0)

	dr.key("/")
	if !dr.m.WantsRawKeys() {
		t.Fatal("the search prompt does not take the keyboard")
	}
	typeInto(dr, "export")
	if got := dr.m.issueAt(dr.m.cursor); got == nil || !containsFold(got.Summary, "export") {
		t.Fatalf("typing export left the cursor on row %d", dr.m.cursor)
	}
	golden(t, "find_120x24.golden", dr.view())
	dr.key("enter")
	if dr.m.WantsRawKeys() {
		t.Fatal("enter left the prompt open")
	}

	want := 0
	for i := range dr.m.rows {
		if matchesNeedle(dr.m.issueAt(i), "export") {
			want++
		}
	}
	seen := map[int]bool{dr.m.cursor: true}
	for range want * 2 {
		dr.key("n")
		if !matchesNeedle(dr.m.issueAt(dr.m.cursor), "export") {
			t.Fatalf("n landed on row %d, which does not match", dr.m.cursor)
		}
		seen[dr.m.cursor] = true
	}
	if len(seen) != want {
		t.Errorf("n visited %d rows, want all %d that match", len(seen), want)
	}
	at := dr.m.cursor
	dr.key("n", "N")
	if dr.m.cursor != at {
		t.Errorf("n then N landed on row %d, want %d", dr.m.cursor, at)
	}
}

func TestFind_EscGoesBackAndAMissSaysSo(t *testing.T) {
	t.Parallel()
	dr := newDriver(t, testDeps(newFake(12)), 120, 24)
	start := dr.m.cursor
	dr.key("/")
	typeInto(dr, "qqqq")
	mustContain(t, dr.view(), "no issue matches")
	dr.key("esc")
	if dr.m.mode != browsing || dr.m.needle != "" || dr.m.cursor != start {
		t.Errorf("esc left mode %d, needle %q, cursor %d; want browsing, none, %d", dr.m.mode, dr.m.needle, dr.m.cursor, start)
	}
	dr.key("n")
	mustContain(t, dr.lastStatus().Text, "nothing is being searched for")
}

// A section's head carries the board's estimate summed over what it draws, in
// the estimation field's own name, and a board that does not estimate carries
// none.
func TestPoints_EachSectionSumsTheBoardsEstimate(t *testing.T) {
	t.Parallel()
	dr := newDriver(t, testDeps(newFake(12)), 120, 24)
	if dr.m.estimate.ID == "" {
		t.Fatal("the scrum fixture does not estimate, so this test proves nothing")
	}
	var want float64
	g := dr.m.groups[len(dr.m.groups)-1]
	for _, at := range g.issues {
		if n, ok := dr.m.issues[at].Fields.Number(dr.m.estimate); ok {
			want += n
		}
	}
	if !g.pointed || g.points != want || want == 0 {
		t.Errorf("the backlog section sums %v (pointed %v), want %v", g.points, g.pointed, want)
	}
	mustContain(t, dr.view(), trimPoints(want)+" "+dr.m.estimate.Name)

	kanban := jiratest.New(jiratest.WithProject("PROJ", jiratest.Kanban), jiratest.WithIssues(jiratest.Gen(8)))
	plain := newDriver(t, testDeps(kanban), 120, 24)
	mustNotContain(t, plain.view(), "Story Points")
}

func trimPoints(n float64) string { return strconv.FormatFloat(n, 'f', -1, 64) }
