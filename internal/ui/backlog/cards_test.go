package backlog

import (
	"errors"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/varijkapil13/saral/internal/config"
	"github.com/varijkapil13/saral/internal/ui/kernel"
	"github.com/varijkapil13/saral/internal/ui/widget/card"
	"github.com/varijkapil13/saral/pkg/jira"
	"github.com/varijkapil13/saral/pkg/jira/jiratest"
)

// ownUIState rules out t.Parallel: t.Setenv.
func ownUIState(t *testing.T) {
	t.Helper()
	t.Setenv("SARAL_CACHE_DIR", t.TempDir())
}

func lookDriver(t *testing.T, d kernel.Deps, look card.Look, w, h int) *driver {
	t.Helper()
	dr := &driver{t: t, m: inLines(t, New(d))}
	dr.send(card.LookMsg{Look: look})
	dr.send(kernel.SizeMsg{Width: w, Height: h})
	dr.send(kernel.FocusMsg{Focused: true})
	dr.run(dr.m.Init())
	if dr.m.look != look {
		t.Fatalf("the view is in %s, want %s", dr.m.look.Word(), look.Word())
	}
	return dr
}

func carded(t *testing.T) *jiratest.Fake {
	t.Helper()
	issues := jiratest.Gen(12)
	done := jira.Status{ID: "done", Name: "Shipped", Category: jira.CategoryDone}
	open := jira.Status{ID: "open", Name: "Waiting", Category: jira.CategoryToDo}
	for i := range issues {
		switch issues[i].Key {
		case "PROJ-1":
			issues[i].Subtasks = []jira.IssueRef{
				{Key: "PROJ-101", Status: done}, {Key: "PROJ-102", Status: open}, {Key: "PROJ-103", Status: open},
			}
			issues[i].Due = jira.Date{Year: 2026, Month: time.March, Day: 1}
		case "PROJ-3":
			issues[i].Due = jira.Date{Year: 2027, Month: time.January, Day: 15}
		}
	}
	f := jiratest.New(jiratest.WithProject("PROJ", jiratest.Scrum), jiratest.WithIssues(issues))
	active, _ := sprintIDs(t, f)
	if err := f.MoveToSprint(t.Context(), active, []string{"PROJ-3", "PROJ-4"}); err != nil {
		t.Fatalf("seeding the sprint: %v", err)
	}
	return f
}

func TestCards_Golden(t *testing.T) {
	t.Parallel()
	for _, look := range []card.Look{card.Roomy, card.Compact} {
		for _, size := range [][2]int{{80, 20}, {120, 24}} {
			for _, state := range []string{"heads", "picked"} {
				name := "cards_" + look.Word() + "_" + state + "_" + strconv.Itoa(size[0]) + "x" + strconv.Itoa(size[1]) + ".golden"
				t.Run(name, func(t *testing.T) {
					t.Parallel()
					dr := lookDriver(t, testDeps(carded(t)), look, size[0], size[1])
					if state == "picked" {
						dr.cursorTo("row:PROJ-3")
						dr.key("space")
						if !dr.m.picked["PROJ-3"] || dr.m.underKey() != "PROJ-4" {
							t.Fatalf("space left %v picked and the cursor on %q", dr.m.picked, dr.m.underKey())
						}
					}
					frame := dr.view()
					if got := strings.Count(frame, "\n") + 1; got != size[1] {
						t.Errorf("the frame is %d lines tall, want %d", got, size[1])
					}
					golden(t, name, frame)
				})
			}
		}
	}
}

func TestCards_ARoomyCardDrawsWhatItWasReadFor(t *testing.T) {
	t.Parallel()
	dr := lookDriver(t, testDeps(carded(t)), card.Roomy, 120, 40)
	frame := dr.view()
	mustContain(t, frame, "1/3", "01 Mar", "15 Jan 2027", "Story Points")
	mustNotContain(t, frame, "unassigned")
	due, overdue := dr.m.dueText(jira.Date{Year: 2026, Month: time.March, Day: 1})
	if due != "01 Mar" || !overdue {
		t.Errorf("a date four days gone reads %q, overdue %v", due, overdue)
	}
	if _, overdue := dr.m.dueText(jira.Date{Year: 2026, Month: time.March, Day: 5}); overdue {
		t.Error("a date due today is drawn as overdue")
	}
}

func TestCards_AFreshModelWithNoStoredLookIsRoomy(t *testing.T) {
	ownUIState(t)
	f := newFake(12)
	dr := &driver{t: t}
	view, ok := New(testDeps(f)).(*Model)
	if !ok {
		t.Fatal("New did not return a *Model")
	}
	dr.m = view
	if dr.m.look != card.Roomy {
		t.Fatalf("a machine that never chose a look opens in %s", dr.m.look.Word())
	}
	dr.send(kernel.SizeMsg{Width: 120, Height: 24})
	dr.run(dr.m.Init())
	for _, id := range card.RoomyFields {
		if !slices.Contains(dr.m.fieldIDs, id) {
			t.Errorf("the first read asked for %v, without %q", dr.m.fieldIDs, id)
		}
	}
}

func TestCards_VCyclesThroughEveryLook(t *testing.T) {
	ownUIState(t)
	dr := newDriver(t, testDeps(newFake(12)), 120, 24)
	for _, want := range []card.Look{card.Roomy, card.Compact, card.Lines, card.Roomy} {
		dr.broadcasts = nil
		dr.key("V")
		at := slices.IndexFunc(dr.broadcasts, func(m tea.Msg) bool { _, ok := m.(card.LookMsg); return ok })
		if at < 0 {
			t.Fatalf("V broadcast %v and no look", dr.broadcasts)
		}
		dr.send(dr.broadcasts[at])
		if dr.m.look != want {
			t.Errorf("V moved to %s, want %s", dr.m.look.Word(), want.Word())
		}
		if got := config.LoadUIState().Look(); got != want.Word() {
			t.Errorf("ui.toml holds %q after V, want %q", got, want.Word())
		}
	}
}

func TestCards_TheCursorAndTheScrollSurviveALookChange(t *testing.T) {
	t.Parallel()
	dr := newDriver(t, testDeps(seededN(t, 60)), 100, 20)
	dr.cursorTo("row:PROJ-10")
	dr.m.top = dr.m.cursor
	dr.m.clampScroll()
	key, top := dr.m.underKey(), dr.m.zoneOf(dr.m.top)

	for _, look := range []card.Look{card.Compact, card.Roomy, card.Lines} {
		dr.send(card.LookMsg{Look: look})
		if got := dr.m.underKey(); got != key {
			t.Errorf("in %s the cursor is on %q, want %q", look.Word(), got, key)
		}
		if got := dr.m.zoneOf(dr.m.top); got != top {
			t.Errorf("in %s the screen starts at %q, want %q", look.Word(), got, top)
		}
		if !dr.m.fits(dr.m.cursor) {
			t.Errorf("in %s the cursor's card is not drawn whole", look.Word())
		}
		mustContain(t, dr.view(), key)
	}
}

func seededN(t *testing.T, n int) *jiratest.Fake {
	t.Helper()
	f := newFake(n)
	active, _ := sprintIDs(t, f)
	if err := f.MoveToSprint(t.Context(), active, []string{"PROJ-3", "PROJ-4"}); err != nil {
		t.Fatalf("seeding the sprint: %v", err)
	}
	return f
}

func TestCards_PagingTheWheelAndTheEndLandOnWholeRowsBetweenHeads(t *testing.T) {
	t.Parallel()
	for _, look := range []card.Look{card.Compact, card.Roomy} {
		t.Run(look.Word(), func(t *testing.T) {
			t.Parallel()
			dr := lookDriver(t, testDeps(seededN(t, 60)), look, 100, 20)
			dr.loadAll()
			dr.key("home")
			off, h := dr.m.offsets(), dr.m.rowsHeight()
			if !dr.m.rows[0].head || dr.m.rows[1].head || !dr.m.rows[3].head {
				t.Fatalf("the fixture does not interleave heads and cards: %v", dr.rowNames()[:5])
			}

			for range 4 {
				from := dr.m.cursor
				dr.key("pgdown")
				moved := off[dr.m.cursor] - off[from]
				if moved > h || moved <= h-look.Lines() {
					t.Errorf("page down moved %d lines, want about %d", moved, h)
				}
				if !dr.m.fits(dr.m.cursor) {
					t.Errorf("page down left the cursor's card cut at row %d", dr.m.cursor)
				}
			}
			dr.key("pgup")
			if !dr.m.fits(dr.m.cursor) {
				t.Error("page up left the cursor's card cut")
			}

			dr.m.top = 0
			for range 3 {
				from := dr.m.top
				dr.send(tea.MouseWheelMsg{Button: tea.MouseWheelDown})
				if dr.m.top <= from || off[dr.m.top]-off[from] > 3+look.Lines() {
					t.Errorf("a notch moved the screen from row %d to %d", from, dr.m.top)
				}
			}
			for range 400 {
				dr.send(tea.MouseWheelMsg{Button: tea.MouseWheelDown})
			}
			total := off[len(dr.m.rows)]
			if total-off[dr.m.top] > h || total-off[dr.m.top-1] <= h {
				t.Errorf("the wheel stopped at row %d, %d lines from the end of %d, on a screen of %d",
					dr.m.top, total-off[dr.m.top], total, h)
			}

			dr.key("G")
			last := len(dr.m.rows) - 1
			if dr.m.cursor != last || !dr.m.fits(last) {
				t.Errorf("G left the cursor on %d of %d, drawn whole %v", dr.m.cursor, last, dr.m.fits(last))
			}
			frame := dr.view()
			if got := strings.Count(frame, "\n") + 1; got != 20 {
				t.Errorf("the frame is %d lines tall, want 20", got)
			}
			mustContain(t, frame, dr.m.underKey())
		})
	}
}

func TestCards_ClickingACardOrAHeadSelectsIt(t *testing.T) {
	t.Parallel()
	d := testDeps(carded(t))
	dr := lookDriver(t, d, card.Roomy, 120, 30)

	x, y := at(t, d, dr, "row:PROJ-4")
	dr.send(tea.MouseClickMsg{X: x + 4, Y: y + 3, Button: tea.MouseLeft})
	if got := dr.m.underKey(); got != "PROJ-4" {
		t.Errorf("a click on the fourth line of PROJ-4 put the cursor on %q", got)
	}
	pressOn(t, d, dr, "head:2")
	if got := dr.m.zoneOf(dr.m.cursor); got != "head:2" {
		t.Errorf("a click on the backlog's head put the cursor on %q", got)
	}
}

func TestCards_ADragWithinASectionRanksTheIssue(t *testing.T) {
	t.Parallel()
	fake := newFake(12)
	d := testDeps(fake)
	dr := lookDriver(t, d, card.Compact, 120, 40)
	before := dr.section(backlogName)

	pressOn(t, d, dr, "row:"+before[0])
	toX, toY := at(t, d, dr, "row:"+before[2])
	dr.send(tea.MouseMotionMsg{X: toX, Y: toY + 2, Button: tea.MouseLeft})
	dr.send(tea.MouseReleaseMsg{X: toX, Y: toY + 2, Button: tea.MouseLeft})

	want := append([]string{before[1], before[2], before[0]}, before[3:]...)
	if got := dr.section(backlogName); !slices.Equal(got, want) {
		t.Errorf("the backlog reads %v after the drag, want %v", got, want)
	}
	if got := countCalls(fake, "RankIssues"); got != 1 {
		t.Errorf("the drag made %d rank calls, want 1", got)
	}
}

func TestCards_SpacePicksTheCardAndMarksIt(t *testing.T) {
	t.Parallel()
	dr := lookDriver(t, testDeps(seeded(t)), card.Compact, 120, 24)
	dr.cursorTo("row:PROJ-1")
	dr.key("space")
	if !dr.m.picked["PROJ-1"] {
		t.Fatal("space did not pick the card under the cursor")
	}
	if got := dr.m.underKey(); got != "PROJ-6" {
		t.Errorf("space left the cursor on %q, want the next card", got)
	}
	g := dr.m.deps.Theme.Glyphs
	mustContain(t, dr.view(), g.CornerTL+" "+g.Check+" PROJ-1", "1 issue picked")
}

func TestCards_MovingToRoomyReadsItsFieldsOnceAndKeepsThePlace(t *testing.T) {
	t.Parallel()
	f := newFake(40)
	dr := newDriver(t, testDeps(f), 100, 20)
	dr.cursorTo("row:PROJ-9")
	for _, id := range card.RoomyFields[:3] {
		if slices.Contains(dr.m.fieldIDs, id) {
			t.Fatalf("the lines look already asked for %q", id)
		}
	}

	before := countCalls(f, "BoardIssues")
	read := dr.hold(card.LookMsg{Look: card.Roomy})
	if read == nil {
		t.Fatal("moving to roomy over rows without its fields read nothing")
	}
	key, top := dr.m.underKey(), dr.m.zoneOf(dr.m.top)
	dr.run(read)
	if got := countCalls(f, "BoardIssues") - before; got != 1 {
		t.Errorf("moving to roomy made %d reads of the board's issues, want 1", got)
	}
	for _, id := range card.RoomyFields {
		if !slices.Contains(dr.m.fieldIDs, id) {
			t.Errorf("the read asked for %v, without %q", dr.m.fieldIDs, id)
		}
	}
	if dr.m.underKey() != key || dr.m.zoneOf(dr.m.top) != top {
		t.Errorf("the cursor is on %q under %q, want %q under %q", dr.m.underKey(), dr.m.zoneOf(dr.m.top), key, top)
	}

	settled := len(f.Calls())
	dr.send(card.LookMsg{Look: card.Compact})
	dr.send(card.LookMsg{Look: card.Lines})
	dr.send(card.LookMsg{Look: card.Roomy})
	if got := f.Calls()[settled:]; len(got) != 0 {
		t.Errorf("moving between looks over rows that have the fields made %v", got)
	}
}

func TestCards_ARoomyReadThatFailsKeepsTheRowsBadged(t *testing.T) {
	t.Parallel()
	for name, err := range map[string]error{
		"a refusal":           &jira.CapabilityError{Capability: jira.CapBoards, Reason: "you need Browse Projects on PROJ"},
		"a rate limit":        &jira.RateLimitError{RetryAfter: 30 * time.Second, Endpoint: "/board"},
		"a transport failure": &jira.TransportError{Op: "GET /board", Status: 502, Err: errors.New("bad gateway")},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			f := newFake(12)
			dr := newDriver(t, testDeps(f), 120, 24)
			dr.cursorTo("row:PROJ-6")
			rows := dr.rowNames()

			f.FailNextN(20, err)
			dr.send(card.LookMsg{Look: card.Roomy})
			if !slices.Equal(dr.rowNames(), rows) {
				t.Errorf("the failed read left %v, want %v", dr.rowNames(), rows)
			}
			if !dr.m.stale || dr.m.failure != nil {
				t.Errorf("stale %v, failure %v; want the rows badged and no refusal in their place", dr.m.stale, dr.m.failure)
			}
			if got := dr.m.underKey(); got != "PROJ-6" {
				t.Errorf("the cursor is on %q, want PROJ-6", got)
			}
			if dr.lastStatus().Level != kernel.LevelError {
				t.Errorf("the failure was not reported: %+v", dr.lastStatus())
			}
			mustContain(t, dr.view(), staleLabel, "PROJ-6")
		})
	}
}

func TestCards_ALookChangeMidWalkRestartsTheWalk(t *testing.T) {
	t.Parallel()
	f := newFake(40)
	dr := lookDriver(t, testDeps(f), card.Lines, 100, 20)
	gen := dr.m.gen
	cmd := dr.hold(kernel.RefreshMsg{})
	if !dr.m.loading {
		t.Fatal("a refresh is not in flight, so this proves nothing")
	}
	dr.send(card.LookMsg{Look: card.Roomy})
	if dr.m.gen <= gen+1 {
		t.Errorf("the walk kept generation %d", dr.m.gen)
	}
	stale := dr.m.gen
	dr.run(cmd)
	if dr.m.gen != stale {
		t.Error("the walk started under the old look landed")
	}
	for _, id := range card.RoomyFields {
		if !slices.Contains(dr.m.fieldIDs, id) {
			t.Errorf("the restarted walk asked for %v, without %q", dr.m.fieldIDs, id)
		}
	}
}
