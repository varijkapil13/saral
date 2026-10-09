package board

import (
	"errors"
	"slices"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"

	appcache "github.com/varijkapil13/saral/internal/app/cache"
	uistate "github.com/varijkapil13/saral/internal/config"
	"github.com/varijkapil13/saral/internal/ui/kernel"
	"github.com/varijkapil13/saral/internal/ui/widget/card"
	"github.com/varijkapil13/saral/pkg/jira"
	"github.com/varijkapil13/saral/pkg/jira/jiratest"
)

var cardLooks = []card.Look{card.Roomy, card.Compact}

func (d *driver) look(l card.Look) {
	d.t.Helper()
	d.send(card.LookMsg{Look: l})
	if d.m.look != l {
		d.t.Fatalf("the board is drawn %s after a LookMsg for %s", d.m.look.Word(), l.Word())
	}
}

func (d *driver) lookBroadcasts() []card.Look {
	var out []card.Look
	for _, msg := range d.broadcasts {
		if l, ok := msg.(card.LookMsg); ok {
			out = append(out, l.Look)
		}
	}
	return out
}

func TestBoardCards_Golden(t *testing.T) {
	t.Parallel()
	for _, look := range cardLooks {
		for name, tc := range map[string]struct {
			width, height int
			build         func(*testing.T, int, int) *driver
			keys          []string
			golden        string
		}{
			"a board at 120":         {width: 120, height: 30, golden: "120x30"},
			"a board at 80":          {width: 80, height: 20, golden: "80x20"},
			"a card in hand":         {width: 120, height: 30, keys: []string{"t", "l"}, golden: "held_120x30"},
			"a search under way":     {width: 120, height: 30, keys: []string{"/", "1", "2"}, golden: "find_120x30"},
			"lanes by assignee":      {width: 120, height: 24, build: lanedCards, keys: []string{"v"}, golden: "lanes_assignee_120x24"},
			"a folded lane":          {width: 120, height: 24, build: lanedCards, keys: []string{"v", "z"}, golden: "lanes_folded_120x24"},
			"asking which status":    {width: 120, height: 30, build: choosingCards, keys: []string{"t", "l", "l", "enter", "right"}, golden: "choosing_120x30"},
			"two cards picked":       {width: 120, height: 30, build: pickedCards, golden: "picked_120x30"},
			"a picked set in hand":   {width: 120, height: 30, build: pickedCards, keys: []string{"t", "l"}, golden: "picked_held_120x30"},
			"a board in a short box": {width: 120, height: 9, golden: "short_120x9"},
		} {
			t.Run(look.Word()+"/"+name, func(t *testing.T) {
				t.Parallel()
				build := tc.build
				if build == nil {
					build = func(t *testing.T, w, h int) *driver { return newDriver(t, testDeps(newFake(24)), w, h) }
				}
				dr := build(t, tc.width, tc.height)
				dr.look(look)
				dr.key(tc.keys...)
				frame := dr.view()
				lines := strings.Split(frame, "\n")
				if len(lines) != tc.height {
					t.Errorf("the board drew %d lines into a box of %d", len(lines), tc.height)
				}
				golden(t, "board_cards_"+look.Word()+"_"+tc.golden+".golden", frame)
			})
		}
	}
}

func lanedCards(t *testing.T, w, h int) *driver {
	_, dr := stocked(t, laneConfig(), laneCards(), w, h)
	return dr
}

func choosingCards(t *testing.T, w, h int) *driver {
	return newDriver(t, testDeps(newTwoInDone(9)), w, h)
}

func pickedCards(t *testing.T, w, h int) *driver {
	dr := newDriver(t, testDeps(newFake(9)), w, h)
	dr.pickFirst(0, 2)
	return dr
}

// Every line of every card is exactly the column's width, whatever the look,
// so the columns to the right of it stay where they are.
func TestBoardCards_EveryLineOfACardFillsItsColumn(t *testing.T) {
	t.Parallel()
	for _, look := range cardLooks {
		dr := newDriver(t, testDeps(newFake(24)), 120, 30)
		dr.look(look)
		for col := range dr.m.cols {
			for row := range dr.m.columnLen(col) {
				lines := dr.m.cardCell(col, row)
				if len(lines) != look.Lines() {
					t.Fatalf("%s: the card at %d,%d is %d lines, want %d", look.Word(), col, row, len(lines), look.Lines())
				}
				for _, line := range lines {
					if got := widthOf(line); got != dr.m.lay.cell {
						t.Fatalf("%s: a line of the card at %d,%d is %d cells wide, want %d", look.Word(), col, row, got, dr.m.lay.cell)
					}
				}
			}
		}
	}
}

// A machine that never chose a look draws roomy cards and asks for what they
// draw. It cannot run beside anything: it points the cache directory, where
// ui.toml lives, at one of its own.
func TestBoardCards_ABoardWithNoStoredLookIsRoomy(t *testing.T) {
	t.Setenv("SARAL_CACHE_DIR", t.TempDir())
	card.ResetRecall()
	was := recallLook
	recallLook = card.Recall
	t.Cleanup(func() {
		recallLook = was
		card.ResetRecall()
	})

	m, ok := New(testDeps(nil)).(*Model)
	if !ok {
		t.Fatal("New did not return a *Model")
	}
	if m.look != card.Roomy {
		t.Fatalf("a fresh board is drawn %s, want roomy", m.look.Word())
	}
	asked := m.plan.projectionFor(m.look).IDs
	for _, id := range card.RoomyFields {
		if !slices.Contains(asked, id) {
			t.Errorf("a roomy board asks for %v, without %s", asked, id)
		}
	}
	if n := len(asked); n != len(slices.Compact(slices.Sorted(slices.Values(asked)))) {
		t.Errorf("a roomy board asks for a field twice: %v", asked)
	}
	for _, look := range []card.Look{card.Compact, card.Lines} {
		if got := m.plan.projectionFor(look).IDs; slices.Contains(got, "duedate") {
			t.Errorf("a %s board asks for %v, which is what only a roomy card draws", look.Word(), got)
		}
	}

	if err := uistate.SaveLook(card.Compact.Word()); err != nil {
		t.Fatal(err)
	}
	card.ResetRecall()
	chose, ok := New(testDeps(nil)).(*Model)
	if !ok {
		t.Fatal("New did not return a *Model")
	}
	if chose.look != card.Compact {
		t.Errorf("a board on a machine that chose compact is drawn %s", chose.look.Word())
	}
}

// A board stored before cards asked for the roomy fields is drawn at once and
// read again behind that first paint, once; one stored with them is not. It
// swaps the look recalled at New, so it cannot run beside anything.
func TestBoardCards_ARoomyBoardFromTheCacheReadsWhatItLacksOnce(t *testing.T) {
	boardID, cfg, qf, issues := primed(t, testDeps(newFake(6)))
	was := recallLook
	recallLook = func() card.Look { return card.Roomy }
	t.Cleanup(func() { recallLook = was })
	fresh := func(f *jiratest.Fake) kernel.Deps {
		d := testDeps(f)
		d.Now = func() time.Time { return cacheStoredAt.Add(time.Second) }
		return d
	}

	cache := newFakeCache()
	cache.hold("PROJ", boardID, appcache.BoardSnapshot{Config: cfg, QuickFilters: qf, Issues: issues}, false)
	fake := newFake(6)
	dr := newDriver(t, withCache(fresh(fake), cache), 120, 30)
	if got := countCalls(fake, "SprintIssues"); got != 1 {
		t.Errorf("a roomy board over stored cards without its fields read them %d times, want once", got)
	}
	if got := countCalls(fake, "Boards"); got != 0 {
		t.Errorf("a board stored within its TTL asked which boards there are %d times", got)
	}
	if dr.m.lacksRoomy() {
		t.Error("the cards read again still lack what a roomy card draws")
	}

	again := newFake(6)
	cache.hold("PROJ", boardID, appcache.BoardSnapshot{Config: cfg, QuickFilters: qf, Issues: dr.m.issues}, false)
	_ = newDriver(t, withCache(fresh(again), cache), 120, 30)
	if calls := viewCalls(again); len(calls) != 0 {
		t.Errorf("a roomy board stored with its fields asked the site for %v", calls)
	}
}

func TestBoardCards_VCyclesEveryLook(t *testing.T) {
	t.Parallel()
	dr := newDriver(t, testDeps(newFake(9)), 120, 30)
	for _, want := range []card.Look{card.Roomy, card.Compact, card.Lines} {
		dr.broadcasts = nil
		dr.key("V")
		got := dr.lookBroadcasts()
		if len(got) != 1 || got[0] != want {
			t.Fatalf("V broadcast %v, want %s", got, want.Word())
		}
		dr.look(got[0])
	}
}

// V reaches the board through the kernel, and the look it broadcasts comes
// back to the board as it does to every other view.
func TestBoardCards_VThroughTheKernelRedrawsTheBoardInCards(t *testing.T) {
	t.Parallel()
	m, err := kernel.New(testDeps(newFake(24)), kernel.WithSize(140, 30), kernel.WithInitialView(ViewID))
	if err != nil {
		t.Fatal(err)
	}
	m = settle(t, m, m.Init(), 0)
	next, cmd := m.Update(tea.WindowSizeMsg{Width: 140, Height: 30})
	m = settle(t, next.(kernel.Model), cmd, 0)
	mustContain(t, ansi.Strip(m.Frame()), "PROJ-3 Investigate the onboarding wizard")

	next, cmd = m.Update(keyPress("V"))
	m = settle(t, next.(kernel.Model), cmd, 0)
	frame := ansi.Strip(m.Frame())
	mustNotContain(t, frame, "PROJ-3 Investigate the onboarding wizard")
	mustContain(t, frame, "PROJ-3", "| Investigate the onboarding wizard")
}

// A gesture that holds the keyboard keeps V for itself: typing it into a
// search or a name, or refusing it while a card is in hand.
func TestBoardCards_VIsNotTheLookKeyWhileAGestureHoldsTheKeyboard(t *testing.T) {
	t.Parallel()
	for name, tc := range map[string]struct {
		build func(*testing.T) *driver
		keys  []string
		check func(*testing.T, *driver)
	}{
		"a card in hand": {keys: []string{"t"}},
		"asking which status": {
			build: func(t *testing.T) *driver { return choosingCards(t, 120, 30) },
			keys:  []string{"t", "l", "l", "enter"},
		},
		"a search under way": {
			keys: []string{"/"},
			check: func(t *testing.T, dr *driver) {
				if dr.m.find.Value() != "V" {
					t.Errorf("the search holds %q, want the V typed into it", dr.m.find.Value())
				}
			},
		},
		"asking who the picked cards go to": {
			build: func(t *testing.T) *driver { return pickedCards(t, 120, 30) },
			keys:  []string{"@"},
		},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			dr := newDriver(t, testDeps(newFake(9)), 120, 30)
			if tc.build != nil {
				dr = tc.build(t)
			}
			dr.key(tc.keys...)
			dr.broadcasts = nil
			dr.key("V")
			if got := dr.lookBroadcasts(); len(got) != 0 {
				t.Errorf("V changed the look to %v", got)
			}
			if tc.check != nil {
				tc.check(t, dr)
			}
		})
	}
}

func TestBoardCards_TheCursorAndEachColumnsTopSurviveALookChange(t *testing.T) {
	t.Parallel()
	issues := append(genIssues("PROJ-B", "10201", "Backlog", 40), genIssues("PROJ-D", "10202", "Done", 40)...)
	_, dr := stocked(t, twoColumnBoard(), issues, 120, 40)
	dr.moveTo(1, 39)
	dr.moveTo(0, 2)
	key, tops := dr.m.selectedKey(), dr.tops()
	if tops[1] == "PROJ-D1" {
		t.Fatal("setup: the second column did not scroll")
	}
	for _, look := range []card.Look{card.Compact, card.Roomy, card.Lines, card.Roomy} {
		dr.look(look)
		if got := dr.m.selectedKey(); got != key {
			t.Errorf("%s: the cursor sits on %s, want %s", look.Word(), got, key)
		}
		if got := dr.tops(); !slices.Equal(got, tops) {
			t.Errorf("%s: the columns open on %v, want %v", look.Word(), got, tops)
		}
		mustContain(t, dr.view(), key, tops[1])
	}
}

func TestBoardCards_PagesAndTheWheelMoveByCards(t *testing.T) {
	t.Parallel()
	for _, look := range cardLooks {
		t.Run(look.Word(), func(t *testing.T) {
			t.Parallel()
			_, dr := stocked(t, twoColumnBoard(), genIssues("PROJ-B", "10201", "Backlog", 60), 120, 33)
			dr.look(look)
			page := dr.m.rowsHeight() / look.Lines()
			if page < 2 {
				t.Fatalf("setup: a page is %d cards", page)
			}
			dr.key("pgdown")
			if dr.m.curRow != page {
				t.Errorf("pgdn moved the cursor to card %d, want %d, a page of cards", dr.m.curRow, page)
			}
			dr.key("pgup")
			if dr.m.curRow != 0 {
				t.Errorf("pgup moved the cursor to card %d, want 0", dr.m.curRow)
			}
			dr.send(tea.MouseWheelMsg{Button: tea.MouseWheelDown})
			if got := dr.m.rowTopAt(0); got != 1 {
				t.Errorf("a notch of the wheel scrolled %d cards, want one", got)
			}
			if dr.m.curRow != 0 {
				t.Errorf("the wheel moved the cursor to card %d", dr.m.curRow)
			}
			dr.send(tea.MouseWheelMsg{Button: tea.MouseWheelUp})
			if got := dr.m.rowTopAt(0); got != 0 {
				t.Errorf("the column is at card %d after a notch down and a notch up, want 0", got)
			}
		})
	}
}

// Every line of a card is its zone, so a click on its last line selects it as
// surely as one on its key.
func TestBoardCards_ClickingTheSecondCardInAColumnSelectsItAndADoubleClickOpensIt(t *testing.T) {
	t.Parallel()
	for _, look := range cardLooks {
		t.Run(look.Word(), func(t *testing.T) {
			t.Parallel()
			d := testDeps(newFake(24))
			dr := newDriver(t, d, 120, 30)
			dr.look(look)
			want := dr.column(0)[1]
			at := zoneOf(t, d, dr, cardZone(want))
			if got := at.EndY - at.StartY + 1; got != look.Lines() {
				t.Fatalf("the card's zone is %d lines tall, want %d", got, look.Lines())
			}
			dr.send(tea.MouseClickMsg{X: at.StartX + 2, Y: at.EndY, Button: tea.MouseLeft})
			if got := dr.m.selectedKey(); got != want {
				t.Fatalf("the click selected %s, want %s", got, want)
			}
			if len(dr.pushes) != 0 {
				t.Fatal("one click opened the issue")
			}
			pressOn(t, d, dr, cardZone(want))
			if len(dr.pushes) != 1 || dr.pushes[0].Title != want {
				t.Errorf("a double-click pushed %v, want %s's pane", dr.pushes, want)
			}
		})
	}
}

func TestBoardCards_ADragToAnotherColumnMovesTheCard(t *testing.T) {
	t.Parallel()
	for _, look := range cardLooks {
		t.Run(look.Word(), func(t *testing.T) {
			t.Parallel()
			fake := newFake(9)
			d := testDeps(fake)
			dr := newDriver(t, d, 120, 30)
			dr.look(look)
			key := dr.column(0)[0]
			from := zoneOf(t, d, dr, cardZone(key))
			onto := zoneOf(t, d, dr, colZone(1))
			dr.send(tea.MouseClickMsg{X: from.StartX, Y: from.EndY, Button: tea.MouseLeft})
			dr.send(tea.MouseMotionMsg{X: onto.StartX, Y: onto.EndY, Button: tea.MouseLeft})
			dr.send(tea.MouseReleaseMsg{X: onto.StartX, Y: onto.EndY, Button: tea.MouseLeft})

			if n := countCalls(fake, "Transition"); n != 1 {
				t.Fatalf("%d transitions were applied, want one", n)
			}
			if got := dr.column(1); !slices.Contains(got, key) {
				t.Errorf("the second column holds %v, want %s in it", got, key)
			}
		})
	}
}

// In lanes the window is in lines and a card is several of them: walking the
// cursor through every lane keeps the whole of its card on screen.
func TestBoardCards_TheLaneWindowKeepsTheWholeCardUnderTheCursor(t *testing.T) {
	t.Parallel()
	for _, look := range cardLooks {
		t.Run(look.Word(), func(t *testing.T) {
			t.Parallel()
			dr := lanedCards(t, 120, 16)
			dr.key("v")
			dr.look(look)
			dr.moveTo(0, 0)
			seen := map[int]bool{}
			for range dr.m.columnLen(0) {
				line, _, ok := dr.m.cursorLine()
				if !ok {
					t.Fatal("the cursor is on no card")
				}
				seen[dr.m.laneAt(dr.m.curCol, dr.m.curRow)] = true
				if h := dr.m.rowsHeight(); line < dr.m.laneTop || line+look.Lines() > dr.m.laneTop+h {
					t.Fatalf("the cursor's card is lines %d to %d and the window shows %d to %d",
						line, line+look.Lines(), dr.m.laneTop, dr.m.laneTop+h)
				}
				mustContain(t, dr.view(), dr.m.selectedKey())
				dr.key("j")
			}
			if len(seen) < 2 {
				t.Fatalf("the walk stayed in %d lane", len(seen))
			}
		})
	}
}

func TestBoardCards_ACardInTheSecondLaneIsClickable(t *testing.T) {
	t.Parallel()
	for _, look := range cardLooks {
		t.Run(look.Word(), func(t *testing.T) {
			t.Parallel()
			d, dr := stocked(t, laneConfig(), laneCards(), 120, 60)
			dr.key("v")
			dr.look(look)
			second := &dr.m.lanes[1]
			if second.n[1] == 0 {
				t.Fatal("setup: the second lane has no card in the second column")
			}
			want := dr.m.issueAt(1, second.at[1]).Key
			at := zoneOf(t, d, dr, cardZone(want))
			dr.send(tea.MouseClickMsg{X: at.StartX + 1, Y: at.EndY, Button: tea.MouseLeft})
			if got := dr.m.selectedKey(); got != want {
				t.Errorf("the click selected %s, want %s", got, want)
			}
			if dr.m.laneAt(dr.m.curCol, dr.m.curRow) != 1 {
				t.Errorf("the cursor is in lane %d, want the second", dr.m.laneAt(dr.m.curCol, dr.m.curRow))
			}
		})
	}
}

// Moving to roomy over cards read without its fields reads them again once,
// through the staged swap, and the reader keeps their place throughout.
// Moving away from roomy reads nothing.
func TestBoardCards_RoomyReadsTheCardsAgainOnceWithWhatItDraws(t *testing.T) {
	t.Parallel()
	fake := newLongFake()
	dr := newDriver(t, testDeps(fake), 160, 40)
	key := dr.readDeep()
	if !dr.m.lacksRoomy() {
		t.Fatal("setup: the cards were read with the roomy fields already")
	}
	before := countCalls(fake, "SprintIssues")

	dr.park, dr.holdPages = isFirstPage, true
	dr.look(card.Roomy)
	tops := dr.tops()
	dr.walkHeld(key)
	assertKeptPlace(t, dr, key, tops)

	pages := countCalls(fake, "SprintIssues") - before
	if want := (longBoard + pageSize - 1) / pageSize; pages != want {
		t.Errorf("moving to roomy read %d pages, want the %d of one walk", pages, want)
	}
	for i := range dr.m.issues {
		for _, id := range card.RoomyFields {
			if !dr.m.issues[i].Requested.Has(id) {
				t.Fatalf("%s was read again without %s", dr.m.issues[i].Key, id)
			}
		}
	}

	calls := len(viewCalls(fake))
	dr.look(card.Compact)
	dr.look(card.Lines)
	dr.look(card.Roomy)
	if got := viewCalls(fake)[calls:]; len(got) != 0 {
		t.Errorf("cycling the look over cards that hold the roomy fields asked the site for %v", got)
	}
}

// A look changed while the cards are being read restarts the read under a
// new generation rather than changing what it asks for part way.
func TestBoardCards_RoomyMidWalkRestartsTheWalk(t *testing.T) {
	t.Parallel()
	fake := newLongFake()
	dr := newDriver(t, testDeps(fake), 160, 40)
	dr.holdPages = true
	dr.send(kernel.RefreshMsg{})
	if len(dr.heldPages) == 0 || dr.m.next == nil {
		t.Fatal("setup: the refresh is not part way through its walk")
	}
	gen := dr.m.gen

	dr.look(card.Roomy)
	if dr.m.gen == gen {
		t.Error("the walk in flight was not replaced")
	}
	dr.release()

	if dr.m.loading || dr.m.next != nil {
		t.Fatal("the restarted walk never finished")
	}
	if got := len(dr.m.issues); got != longBoard {
		t.Errorf("the board holds %d cards, want %d", got, longBoard)
	}
	for i := range dr.m.issues {
		if !dr.m.issues[i].Requested.Has("duedate") {
			t.Fatalf("%s came from the walk the look change replaced", dr.m.issues[i].Key)
		}
	}
}

func TestBoardCards_ARefusedRoomyReadKeepsTheCardsAndBadgesThem(t *testing.T) {
	t.Parallel()
	for name, failure := range map[string]error{
		"a 403":               &jira.CapabilityError{Reason: "you need Browse Projects in this project"},
		"a rate limit":        &jira.RateLimitError{RetryAfter: 30 * time.Second},
		"a transport failure": &jira.TransportError{Op: "GET /board", Err: errors.New("connection reset")},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			fake := newFake(24)
			dr := newDriver(t, testDeps(fake), 120, 30)
			key, was := dr.m.selectedKey(), dr.onScreen()

			fake.FailNext(failure)
			dr.look(card.Roomy)

			if dr.m.loading || dr.m.next != nil {
				t.Error("the refused read is still held as in flight")
			}
			if got := dr.onScreen(); got != was {
				t.Errorf("the board shows %d cards after the refusal, want the %d it had", got, was)
			}
			if got := dr.m.selectedKey(); got != key {
				t.Errorf("the cursor sits on %s, want %s", got, key)
			}
			frame := dr.view()
			mustContain(t, frame, staleLabel, key)
			reason, _ := jira.Reason(failure)
			if got := dr.lastStatus(); got.Level != kernel.LevelError || got.Text != reason {
				t.Errorf("status = %+v, want the refusal in its own words", got)
			}
		})
	}
}

func widthOf(line string) int { return ansi.StringWidth(ansi.Strip(line)) }
