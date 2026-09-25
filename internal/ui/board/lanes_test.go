package board

import (
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/varijkapil13/saral/internal/ui/filter"
	"github.com/varijkapil13/saral/internal/ui/kernel"
	"github.com/varijkapil13/saral/internal/ui/uitest"
	"github.com/varijkapil13/saral/pkg/jira"
)

var (
	grace   = jira.User{AccountID: "acct-grace", DisplayName: "Grace Hopper", Active: true}
	epicOne = jira.IssueRef{Key: "PROJ-900", Summary: "Nightly export"}
	epicTwo = jira.IssueRef{Key: "PROJ-901", Summary: "Billing rework"}
)

func laneConfig() jira.BoardConfig {
	return jira.BoardConfig{
		BoardID: 42, Name: "Delivery", Type: jira.BoardScrum, RankFieldID: "customfield_13404",
		Columns: []jira.Column{
			{Name: "To do", StatusIDs: []string{"10201"}},
			{Name: "Doing", StatusIDs: []string{"10202"}},
			{Name: "Done", StatusIDs: []string{"10203"}},
		},
	}
}

// laneCards is a board where who does what and which parent it hangs off
// both vary across the columns, so every lane has a different shape.
func laneCards() []jira.Issue {
	statuses := []jira.Status{
		{ID: "10201", Name: "To do", Category: jira.CategoryToDo},
		{ID: "10202", Name: "Doing", Category: jira.CategoryInProgress},
		{ID: "10203", Name: "Done", Category: jira.CategoryDone},
	}
	people := []*jira.User{&grace, &ada, nil}
	parents := []*jira.IssueRef{&epicOne, nil, &epicTwo, &epicOne}
	base := time.Date(2026, time.February, 1, 9, 0, 0, 0, time.UTC)
	out := make([]jira.Issue, 0, 12)
	for i := range 12 {
		iss := jira.Issue{
			ID: strconv.Itoa(30000 + i), Key: "PROJ-" + strconv.Itoa(i+1),
			Summary: "Card number " + strconv.Itoa(i+1),
			Status:  statuses[i%3], Type: jira.IssueType{ID: "10001", Name: "Story"},
			Updated: base.Add(time.Duration(i) * time.Minute),
		}
		if who := people[(i/2)%3]; who != nil {
			u := *who
			iss.Assignee = &u
		}
		if parent := parents[i%4]; parent != nil {
			p := *parent
			iss.Parent = &p
		}
		out = append(out, iss)
	}
	return out
}

func laned(t *testing.T, w, h int, keys ...string) (kernel.Deps, *driver) {
	t.Helper()
	d, dr := stocked(t, laneConfig(), laneCards(), w, h)
	dr.key(keys...)
	return d, dr
}

func TestLanes_Golden(t *testing.T) {
	t.Parallel()
	for name, tc := range map[string]struct {
		width, height int
		keys          []string
		golden        string
	}{
		"by assignee at 80":           {width: 80, height: 24, keys: []string{"w"}, golden: "lanes_assignee_80x24.golden"},
		"by assignee at 120":          {width: 120, height: 24, keys: []string{"w"}, golden: "lanes_assignee_120x24.golden"},
		"by assignee at 160":          {width: 160, height: 30, keys: []string{"w"}, golden: "lanes_assignee_160x30.golden"},
		"by parent at 120":            {width: 120, height: 24, keys: []string{"w", "w"}, golden: "lanes_parent_120x24.golden"},
		"a folded lane at 120":        {width: 120, height: 24, keys: []string{"w", "z"}, golden: "lanes_folded_120x24.golden"},
		"every lane folded at 120":    {width: 120, height: 24, keys: []string{"w", "Z"}, golden: "lanes_all_folded_120x24.golden"},
		"lanes in a short box at 120": {width: 120, height: 10, keys: []string{"w"}, golden: "lanes_short_120x10.golden"},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			_, dr := laned(t, tc.width, tc.height, tc.keys...)
			golden(t, tc.golden, dr.view())
		})
	}
}

func TestLanes_WCyclesNoneAssigneeParentAndBack(t *testing.T) {
	t.Parallel()
	_, dr := laned(t, 120, 24)
	for _, want := range []laneMode{lanesByAssignee, lanesByParent, lanesOff} {
		dr.key("w")
		if dr.m.laneMode != want {
			t.Fatalf("w left the board in mode %v, want %v", dr.m.laneMode, want)
		}
	}
	if len(dr.m.lanes) != 0 {
		t.Errorf("lanes off still holds %d lanes", len(dr.m.lanes))
	}
}

// Every card is in exactly one lane, each lane counts what it holds, people
// are ordered by name and nobody's cards come last.
func TestLanes_ByAssigneeGroupsEveryCardOnce(t *testing.T) {
	t.Parallel()
	_, dr := laned(t, 120, 24, "w")
	labels := make([]string, 0, len(dr.m.lanes))
	total := 0
	for _, l := range dr.m.lanes {
		labels = append(labels, l.label)
		total += l.cards
	}
	if want := []string{"Ada Lovelace", "Grace Hopper", "Unassigned"}; !slices.Equal(labels, want) {
		t.Errorf("the lanes are %v, want %v", labels, want)
	}
	if total != len(dr.m.issues) {
		t.Errorf("the lanes count %d cards between them, want all %d", total, len(dr.m.issues))
	}
	for c := range dr.m.cols {
		prev := -1
		for r := range dr.m.cols[c] {
			L := dr.m.laneAt(c, r)
			if L < prev {
				t.Errorf("column %d goes back from lane %d to lane %d at row %d", c, prev, L, r)
			}
			prev = L
		}
	}
}

func TestLanes_ByParentReadsTheParentFieldAndPutsOrphansLast(t *testing.T) {
	t.Parallel()
	_, dr := laned(t, 120, 24, "w", "w")
	keys := make([]string, 0, len(dr.m.lanes))
	for _, l := range dr.m.lanes {
		keys = append(keys, l.key)
	}
	if want := []string{epicOne.Key, epicTwo.Key, ""}; !slices.Equal(keys, want) {
		t.Errorf("the lanes are %v, want %v: parents in the order the board ranks their first card, then none", keys, want)
	}
	mustContain(t, dr.view(), "PROJ-900 Nightly export", "No parent")
}

// The choice is kept per board, so a board opened again is drawn the way it was
// left and another board is not.
func TestLanes_TheChoiceIsRememberedPerBoard(t *testing.T) {
	t.Parallel()
	mem := newFakeMemory()
	d := withMemory(testDeps(nil), mem)
	dr := newDriver(t, d, 120, 24)
	dr.send(boardsMsg{gen: dr.m.gen, boards: []jira.Board{{ID: 42, Name: "Delivery"}}})
	dr.send(configMsg{gen: dr.m.gen, cfg: laneConfig()})
	dr.send(firstPage(dr.m.gen, laneCards()))
	dr.key("w", "w")
	if got := mem.state[ViewID+"."+laneMemoryKey(42)]; got != "parent" {
		t.Fatalf("the memory holds %q for board 42, want parent", got)
	}

	again := newDriver(t, d, 120, 24)
	again.send(boardsMsg{gen: again.m.gen, boards: []jira.Board{{ID: 42, Name: "Delivery"}}})
	again.send(configMsg{gen: again.m.gen, cfg: laneConfig()})
	again.send(firstPage(again.m.gen, laneCards()))
	if again.m.laneMode != lanesByParent {
		t.Errorf("board 42 opened again in mode %v, want the parent lanes it was left in", again.m.laneMode)
	}

	other := laneConfig()
	other.BoardID = 43
	elsewhere := newDriver(t, d, 120, 24)
	elsewhere.send(boardsMsg{gen: elsewhere.m.gen, boards: []jira.Board{{ID: 43, Name: "Other"}}})
	elsewhere.send(configMsg{gen: elsewhere.m.gen, cfg: other})
	elsewhere.send(firstPage(elsewhere.m.gen, laneCards()))
	if elsewhere.m.laneMode != lanesOff {
		t.Errorf("board 43 opened in mode %v, want no lanes: the choice belongs to board 42", elsewhere.m.laneMode)
	}
}

// Folding takes a lane's cards out of what the cursor walks and keeps them in
// every count.
func TestLanes_FoldingKeepsTheCountsAndSkipsTheCards(t *testing.T) {
	t.Parallel()
	_, dr := laned(t, 120, 24, "w")
	before := dr.m.counts()
	L := dr.m.laneAt(dr.m.curCol, dr.m.curRow)
	first := dr.m.lanes[L].key
	dr.key("z")
	if !dr.m.lanes[L].folded {
		t.Fatal("z did not fold the lane under the cursor")
	}
	if got := dr.m.counts(); got != before {
		t.Errorf("the count line reads %q folded, want %q as it was", got, before)
	}
	for c := range dr.m.cols {
		for r := range dr.m.cols[c] {
			if dr.m.lanes[dr.m.laneAt(c, r)].key == first {
				t.Fatalf("a folded lane's card is still walkable at column %d row %d", c, r)
			}
		}
	}
	if caption := strings.Join(strings.Fields(strings.Split(dr.view(), "\n")[1]), " "); !strings.HasPrefix(caption, "To do 4 ") {
		t.Errorf("the captions read %q, want the first column still counting the folded lane's cards", caption)
	}
	dr.key("Z")
	dr.key("Z")
	for _, l := range dr.m.lanes {
		if l.folded {
			t.Errorf("Z twice left %s folded", l.label)
		}
	}
}

// A lane whose every card a term hides is not drawn at all, rather than drawn
// with nothing under it.
func TestLanes_ALaneTheFilterEmptiesIsNotDrawn(t *testing.T) {
	t.Parallel()
	_, dr := laned(t, 120, 24, "w")
	dr.send(filter.ChosenMsg{Term: filter.Term{Facet: filter.FacetAssignee, ID: ada.AccountID, Label: ada.DisplayName}})
	if len(dr.m.lanes) != 1 || dr.m.lanes[0].key != ada.AccountID {
		t.Fatalf("the lanes under a term naming Ada are %+v, want hers alone", dr.m.lanes)
	}
	mustNotContain(t, dr.view(), "Grace Hopper", "Unassigned")
}

func TestLanes_TheCursorWalksFromOneLaneIntoTheNext(t *testing.T) {
	t.Parallel()
	_, dr := laned(t, 120, 24, "w")
	start := dr.m.laneAt(0, 0)
	for range dr.m.columnLen(0) {
		if dr.m.laneAt(dr.m.curCol, dr.m.curRow) != start {
			return
		}
		dr.key("j")
	}
	t.Error("j never left the first lane of the first column")
}

// A rank stays inside the lane: the first card of a lane is first there, even
// with a card of another lane above it in the column.
func TestLanes_ARankStaysWithinTheLane(t *testing.T) {
	t.Parallel()
	_, dr := laned(t, 120, 24, "w")
	L := 1
	row := dr.m.lanes[L].at[0]
	dr.moveTo(0, row)
	dr.key("K")
	mustContain(t, dr.lastStatus().Text, "already first in To do in this lane")
}

func TestLanes_ClickingAHeaderFoldsItsLane(t *testing.T) {
	t.Parallel()
	d, dr := laned(t, 120, 24, "w")
	key := dr.m.lanes[1].key
	pressOn(t, d, dr, laneZone(lanesByAssignee, key))
	if !dr.m.lanes[1].folded {
		t.Fatalf("a click on %s's header did not fold it", dr.m.lanes[1].label)
	}
	pressOn(t, d, dr, laneZone(lanesByAssignee, key))
	if dr.m.lanes[1].folded {
		t.Error("a second click did not open it again")
	}
}

// Each lane's header is a zone of its own and each card in it keeps the card's
// own zone, so a click on a card in a lane selects that card.
func TestLanes_ACardInALaneIsClickable(t *testing.T) {
	t.Parallel()
	d, dr := laned(t, 120, 24, "w")
	L := len(dr.m.lanes) - 1
	want := dr.m.issueAt(0, dr.m.lanes[L].at[0]).Key
	at := uitest.Zone(t, d.Zones, dr.m.View, dr.m.zones.ID(cardZone(want)))
	dr.send(tea.MouseClickMsg{X: at.StartX, Y: at.StartY, Button: tea.MouseLeft})
	if got := dr.m.selectedKey(); got != want {
		t.Errorf("the click selected %s, want %s", got, want)
	}
}

// Past the bottom of the box the run of lanes scrolls with the cursor, header
// and all.
func TestLanes_TheWindowFollowsTheCursorDownTheLanes(t *testing.T) {
	t.Parallel()
	_, dr := laned(t, 120, 10, "w")
	for range 8 {
		dr.key("j")
	}
	line, _, ok := dr.m.cursorLine()
	if !ok {
		t.Fatal("the cursor is on no card")
	}
	if h := dr.m.rowsHeight(); line < dr.m.laneTop || line >= dr.m.laneTop+h {
		t.Errorf("the cursor is on line %d and the window shows %d to %d", line, dr.m.laneTop, dr.m.laneTop+h)
	}
	if !strings.Contains(dr.view(), dr.m.selectedKey()) {
		t.Errorf("the card under the cursor, %s, is not drawn", dr.m.selectedKey())
	}
}
