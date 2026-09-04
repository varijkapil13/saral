package board

import (
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"
	zone "github.com/lrstanley/bubblezone/v2"

	"github.com/varijkapil13/saral/internal/ui/filter"
	"github.com/varijkapil13/saral/internal/ui/kernel"
	"github.com/varijkapil13/saral/internal/ui/widget/filterbar"
	"github.com/varijkapil13/saral/pkg/jira"
	"github.com/varijkapil13/saral/pkg/jira/jiratest"
)

func TestBoardRender_Golden(t *testing.T) {
	t.Parallel()
	for name, tc := range map[string]struct {
		width, height int
		keys          []string
		golden        string
		build         func(*testing.T, kernel.Deps, int, int) *driver
	}{
		"a board at a comfortable width": {
			width: 120, height: 20, golden: "board_120x20.golden",
		},
		"a narrow terminal": {
			width: 80, height: 20, golden: "board_80x20.golden",
		},
		"a wide terminal": {
			width: 160, height: 30, golden: "board_160x30.golden",
		},
		"a card in hand, aimed at the next column": {
			width: 120, height: 20, keys: []string{"m", "l"}, golden: "held_120x20.golden",
		},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			dr := newDriver(t, testDeps(newFake(24)), tc.width, tc.height)
			dr.key(tc.keys...)
			golden(t, tc.golden, dr.view())
		})
	}
}

// The board holds a filter.Terms since it grew them, and the bar under the
// grid is the only thing that ever said so. One golden with one facet in
// force, one with two, at the widths docs/FILTERS.md asks for.
func TestBoardRender_FilterBarGolden(t *testing.T) {
	t.Parallel()
	waiting := filter.Term{Facet: filter.FacetStatus, ID: "10201", Label: "Triage"}
	bug := filter.Term{Facet: filter.FacetType, ID: "3", Label: "Bug"}
	for name, tc := range map[string]struct {
		width, height int
		terms         []filter.Term
		golden        string
	}{
		"one facet at 80": {
			width: 80, height: 20, terms: []filter.Term{waiting}, golden: "board_term_80x20.golden",
		},
		"one facet at 120": {
			width: 120, height: 20, terms: []filter.Term{waiting}, golden: "board_term_120x20.golden",
		},
		"two facets at 120": {
			width: 120, height: 20, terms: []filter.Term{waiting, bug}, golden: "board_two_terms_120x20.golden",
		},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			dr := newDriver(t, testDeps(newFake(16)), tc.width, tc.height)
			for _, term := range tc.terms {
				dr.send(filter.ChosenMsg{Term: term})
			}
			golden(t, tc.golden, dr.view())
		})
	}
}

// A term in force clears with ctrl+g, the same key that puts a picked-up card
// back — the two never overlap, because ctrl+g only clears while nothing is
// in hand.
func TestBoard_CtrlGClearsATermInForce(t *testing.T) {
	t.Parallel()
	waiting := filter.Term{Facet: filter.FacetStatus, ID: "10201", Label: "Triage"}
	dr := newDriver(t, testDeps(newFake(16)), 120, 20)
	before := boardShown(dr)
	dr.send(filter.ChosenMsg{Term: waiting})
	if got := boardShown(dr); got >= before {
		t.Fatalf("choosing a term left %d cards, want fewer than the %d before", got, before)
	}
	dr.key("ctrl+g")
	if len(dr.m.terms) != 0 {
		t.Errorf("ctrl+g left %v in force", dr.m.terms)
	}
	if got := boardShown(dr); got != before {
		t.Errorf("ctrl+g left %d cards, want the original %d back", got, before)
	}
	if strings.Contains(dr.view(), "clears everything") {
		t.Error("the bar is still drawn after ctrl+g cleared the only term")
	}
}

// Clicking a chip's × drops the whole facet, and clicking a value inside one
// drops just that value — both through the same widget the issue list uses.
func TestBoard_ClickingTheBarDropsAFacetOrAValue(t *testing.T) {
	t.Parallel()
	waiting := filter.Term{Facet: filter.FacetStatus, ID: "10201", Label: "Triage"}
	bug := filter.Term{Facet: filter.FacetType, ID: "3", Label: "Bug"}
	d := testDeps(newFake(16))
	dr := newDriver(t, d, 120, 20)
	dr.send(filter.ChosenMsg{Term: waiting})
	dr.send(filter.ChosenMsg{Term: bug})

	pressOn(t, d, dr, filterbar.FacetZone(filter.FacetType))
	if got := dr.m.terms; len(got) != 1 || got[0].Facet != filter.FacetStatus {
		t.Fatalf("clicking the type chip's x left %v, want only the status term", got)
	}

	pressOn(t, d, dr, filterbar.ValueZone(waiting))
	if len(dr.m.terms) != 0 {
		t.Errorf("clicking the status value left %v, want none", dr.m.terms)
	}
}

func TestBoardRender_EmptyStatesGolden(t *testing.T) {
	t.Parallel()
	for name, tc := range map[string]struct {
		deps   func(*testing.T) kernel.Deps
		golden string
	}{
		"no Jira in this session": {
			deps:   func(*testing.T) kernel.Deps { return testDeps(nil) },
			golden: "empty_noclient_100x16.golden",
		},
		"a token that may not read boards": {
			deps: func(*testing.T) kernel.Deps {
				d := testDeps(newFake(9))
				d.Caps.Boards = jira.Capability{Reason: "you need the Browse Projects permission on PROJ"}
				return d
			},
			golden: "empty_nocap_100x16.golden",
		},
		"a project with no board on it": {
			deps: func(*testing.T) kernel.Deps {
				return testDeps(jiratest.New(jiratest.WithProject("PROJ", jiratest.NoBoard)))
			},
			golden: "empty_noboard_100x16.golden",
		},
		"a board whose columns nothing is in": {
			deps: func(*testing.T) kernel.Deps {
				return testDeps(jiratest.New(jiratest.WithProject("PROJ", jiratest.Scrum)))
			},
			golden: "empty_nocards_100x16.golden",
		},
		"a search the site refused": {
			deps: func(*testing.T) kernel.Deps {
				fake := newFake(9)
				fake.FailNextN(4, &jira.CapabilityError{
					Capability: jira.CapBoards,
					Reason:     "your token cannot see the boards on this project",
				})
				return testDeps(fake)
			},
			golden: "failed_100x16.golden",
		},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			dr := newDriver(t, tc.deps(t), 100, 16)
			golden(t, tc.golden, dr.view())
		})
	}
}

// The frame is exactly the box the kernel gave it, at every size, in every
// state. A view a line too tall pushes the footer off the screen.
func TestBoardRender_FitsTheBoxItIsGiven(t *testing.T) {
	t.Parallel()
	for _, size := range [][2]int{{40, 10}, {80, 20}, {120, 30}, {200, 60}} {
		for name, keys := range map[string][]string{
			"looking at it":  nil,
			"a card in hand": {"m", "l"},
		} {
			t.Run(name, func(t *testing.T) {
				t.Parallel()
				dr := newDriver(t, testDeps(newFake(40)), size[0], size[1])
				dr.key(keys...)
				lines := strings.Split(dr.view(), "\n")
				if len(lines) != size[1] {
					t.Fatalf("at %dx%d the board drew %d lines", size[0], size[1], len(lines))
				}
				for i, line := range lines {
					if got := ansi.StringWidth(line); got > size[0] {
						t.Errorf("at %dx%d line %d is %d cells wide", size[0], size[1], i, got)
					}
				}
			})
		}
	}
}

// Every card fills its column, so that a selected one's highlight reaches the
// column's edge rather than stopping at the end of its summary.
func TestBoardRender_EveryCardFillsItsColumn(t *testing.T) {
	t.Parallel()
	dr := newDriver(t, testDeps(newFake(24)), 120, 20)
	cell := dr.m.lay.cell
	if cell <= 0 {
		t.Fatal("the layout gave the columns no width")
	}
	for col := range dr.m.cols {
		for row := range dr.m.columnLen(col) {
			got := ansi.StringWidth(ansi.Strip(dr.m.cell(col, row)))
			if got != cell {
				t.Fatalf("the card at column %d row %d is %d cells wide, want %d", col, row, got, cell)
			}
		}
	}
}

// A column that holds more cards than the board says it should draws its count
// in the warning style rather than silently. Min and Max are pointers because a
// column may have neither.
func TestBoardRender_ABreachedColumnLimitIsDrawnDifferently(t *testing.T) {
	t.Parallel()
	two := 2
	cfg := jira.BoardConfig{BoardID: 1, Name: "Ledger", Columns: []jira.Column{
		{Name: "Waiting", StatusIDs: []string{"10201"}},
		{Name: "Under way", StatusIDs: []string{"10202"}, Max: &two},
	}}
	issues := []jira.Issue{
		{Key: "PROJ-1", Summary: "one", Status: jira.Status{ID: "10202"}},
		{Key: "PROJ-2", Summary: "two", Status: jira.Status{ID: "10202"}},
		{Key: "PROJ-3", Summary: "three", Status: jira.Status{ID: "10202"}},
	}
	under := jira.BoardConfig{BoardID: 1, Name: "Ledger", Columns: []jira.Column{
		{Name: "Waiting", StatusIDs: []string{"10201"}},
		{Name: "Under way", StatusIDs: []string{"10202"}},
	}}

	breachedFrame := colouredFrame(t, cfg, issues)
	plainFrame := colouredFrame(t, under, issues)
	if breachedFrame == plainFrame {
		t.Error("a column holding three cards against its own limit of two is drawn exactly as one with no limit")
	}
	if !breached(planColumn{max: &two}, 3) {
		t.Error("three cards against a maximum of two is not reported as a breach")
	}
	if breached(planColumn{}, 3) {
		t.Error("a column with neither limit was reported as breaching one")
	}
}

// colouredFrame draws a board with a theme that has colour in it, because what a
// breached limit changes is a style and not a word.
func colouredFrame(t *testing.T, cfg jira.BoardConfig, issues []jira.Issue) string {
	t.Helper()
	d := testDeps(nil)
	d.Theme = kernel.NewTheme(kernel.ThemeDark, true, kernel.ASCIIGlyphs())
	d.Zones = zone.New()
	view, ok := New(d).(*Model)
	if !ok {
		t.Fatal("New did not return a *Model")
	}
	dr := &driver{t: t, m: view}
	dr.send(kernel.SizeMsg{Width: 100, Height: 16})
	dr.send(boardsMsg{gen: dr.m.gen, boards: []jira.Board{{ID: cfg.BoardID, Name: cfg.Name}}})
	dr.send(configMsg{gen: dr.m.gen, cfg: cfg})
	dr.send(issuesMsg{gen: dr.m.gen, issues: issues})
	return dr.m.View()
}

// A board that does not estimate draws no estimate anywhere, which is different
// from drawing a zero.
func TestBoardRender_ABoardThatDoesNotEstimateDrawsNoNumbers(t *testing.T) {
	t.Parallel()
	cfg := jira.BoardConfig{BoardID: 1, Name: "Ledger", Type: jira.BoardKanban, Columns: []jira.Column{
		{Name: "Waiting", StatusIDs: []string{"10201"}},
		{Name: "Under way", StatusIDs: []string{"10202"}},
	}}
	points := jira.FieldRef{ID: "customfield_13401", Name: "Story Points"}
	iss := jira.Issue{Key: "PROJ-1", Summary: "one", Status: jira.Status{ID: "10201"}}
	iss.Fields = iss.Fields.With(points, jira.FieldValue{Kind: jira.KindNumber, Number: 8})

	_, plain := stocked(t, cfg, []jira.Issue{iss}, 100, 16)
	withEstimates := cfg
	withEstimates.Estimation = &jira.Estimation{Type: jira.EstimationField, Field: points}
	_, counted := stocked(t, withEstimates, []jira.Issue{iss}, 100, 16)

	mustNotContain(t, plain.view(), "8")
	mustContain(t, counted.view(), "8")
}

// A resting card's key carries its status category's colour — a column
// already says which status something is in, so this is which kind of
// status — and selecting or holding the card, which inverts the whole thing,
// does not layer a second colour on top of that.
func TestRenderCard_RestingMarkAcrossGlyphTiers(t *testing.T) {
	iss := jira.Issue{
		Key:     "PROJ-42",
		Summary: "A resting card",
		Type:    jira.IssueType{Name: "Defect"},
		Status:  jira.Status{Category: jira.CategoryInProgress},
	}

	for _, tier := range []struct {
		name   string
		glyphs kernel.Glyphs
	}{
		{"nerd", kernel.NerdGlyphs()},
		{"unicode", kernel.UnicodeGlyphs()},
		{"ascii", kernel.ASCIIGlyphs()},
	} {
		t.Run(tier.name, func(t *testing.T) {
			theme := kernel.NewTheme(kernel.ThemeNoColor, true, tier.glyphs)
			st := newStyles(theme)
			got := ansi.Strip(renderCard(&iss, 30, false, false, st, theme, plan{}))
			golden(t, "card_mark_"+tier.name+".golden", got+"\n")
		})
	}
}

// A subtask is the one type TypeGlyph can actually tell apart from the rest.
func TestRenderCard_SubtaskMarkAcrossGlyphTiers(t *testing.T) {
	iss := jira.Issue{
		Key:     "PROJ-43",
		Summary: "A resting subtask card",
		Type:    jira.IssueType{Name: "Offshoot", Subtask: true},
		Status:  jira.Status{Category: jira.CategoryToDo},
	}

	for _, tier := range []struct {
		name   string
		glyphs kernel.Glyphs
	}{
		{"nerd", kernel.NerdGlyphs()},
		{"unicode", kernel.UnicodeGlyphs()},
		{"ascii", kernel.ASCIIGlyphs()},
	} {
		t.Run(tier.name, func(t *testing.T) {
			theme := kernel.NewTheme(kernel.ThemeNoColor, true, tier.glyphs)
			st := newStyles(theme)
			got := ansi.Strip(renderCard(&iss, 30, false, false, st, theme, plan{}))
			golden(t, "card_mark_subtask_"+tier.name+".golden", got+"\n")
		})
	}
}

func TestRenderCard_TheKeyCarriesItsStatusCategorysColourWhileResting(t *testing.T) {
	t.Parallel()
	th := kernel.NewTheme(kernel.ThemeDark, true, kernel.UnicodeGlyphs())
	st := newStyles(th)
	p := plan{}

	// Same key and summary, only the category differs, so any difference in
	// what renderCard answers with is the category's colour and nothing else.
	toDo := &jira.Issue{Key: "PROJ-1", Summary: "one", Status: jira.Status{Category: jira.CategoryToDo}}
	done := &jira.Issue{Key: "PROJ-1", Summary: "one", Status: jira.Status{Category: jira.CategoryDone}}

	restingToDo := renderCard(toDo, 30, false, false, st, th, p)
	if restingToDo == ansi.Strip(restingToDo) {
		t.Fatal("a resting card built from a colour theme carries no colour at all, so this test proves nothing")
	}
	if restingDone := renderCard(done, 30, false, false, st, th, p); restingDone == restingToDo {
		t.Error("two resting cards in different categories rendered identically")
	}
	if sameAgain := renderCard(toDo, 30, false, false, st, th, p); sameAgain != restingToDo {
		t.Errorf("the same card rendered twice differs: %q vs %q", sameAgain, restingToDo)
	}

	if selected := renderCard(toDo, 30, true, false, st, th, p); selected != renderCard(done, 30, true, false, st, th, p) {
		t.Error("two selected cards still differ by category, so a second colour is fighting the selection style")
	}
	if held := renderCard(toDo, 30, false, true, st, th, p); held != renderCard(done, 30, false, true, st, th, p) {
		t.Error("two held cards still differ by category, so a second colour is fighting the held style")
	}
}
