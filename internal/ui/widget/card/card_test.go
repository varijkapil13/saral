package card

import (
	"fmt"
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"
	zone "github.com/lrstanley/bubblezone/v2"

	"github.com/varijkapil13/saral/internal/ui/kernel"
	"github.com/varijkapil13/saral/internal/ui/uitest"
	"github.com/varijkapil13/saral/internal/ui/widget"
)

var goldenWidths = []int{16, 19, 24, 29, 40, 80, 120}

var tiers = []struct {
	name   string
	glyphs kernel.Glyphs
}{
	{"unicode", kernel.UnicodeGlyphs()},
	{"ascii", kernel.ASCIIGlyphs()},
}

func draw(f Facts, st State, t *kernel.Theme, look Look, width int) []string {
	return Render(nil, f, st, NewStyles(t), Frame{Width: width, Look: look, Glyphs: t.Glyphs})
}

func block(lines []string) string {
	var b strings.Builder
	for _, l := range lines {
		b.WriteString(ansi.Strip(l))
		b.WriteString("|\n")
	}
	return b.String()
}

func TestRender_Golden(t *testing.T) {
	t.Parallel()

	for _, look := range []Look{Roomy, Compact} {
		for _, tier := range tiers {
			for _, width := range goldenWidths {
				name := fmt.Sprintf("%s_%s_%d.golden", look.Word(), tier.name, width)
				t.Run(name, func(t *testing.T) {
					t.Parallel()

					theme := kernel.NewTheme(kernel.ThemeDark, true, tier.glyphs)
					var b strings.Builder
					for _, facts := range []struct {
						name string
						f    Facts
					}{{"list", listFacts(tier.glyphs)}, {"board", boardFacts(tier.glyphs)}} {
						for _, s := range states {
							fmt.Fprintf(&b, "-- %s, %s --\n", facts.name, s.name)
							b.WriteString(block(draw(facts.f, s.st, theme, look, width)))
						}
					}
					golden(t, name, b.String())
				})
			}
		}
	}
}

func TestRender_GoldenEmptyAndOverdue(t *testing.T) {
	t.Parallel()

	for _, tier := range tiers {
		t.Run(tier.name, func(t *testing.T) {
			t.Parallel()

			theme := kernel.NewTheme(kernel.ThemeDark, true, tier.glyphs)
			overdue := listFacts(tier.glyphs)
			overdue.Overdue = true
			var b strings.Builder
			for _, look := range []Look{Roomy, Compact} {
				for _, width := range []int{24, 80} {
					fmt.Fprintf(&b, "-- nothing but a key, %s, %d --\n", look.Word(), width)
					b.WriteString(block(draw(Facts{Key: "EX-5"}, State{}, theme, look, width)))
					fmt.Fprintf(&b, "-- overdue, %s, %d --\n", look.Word(), width)
					b.WriteString(block(draw(overdue, State{}, theme, look, width)))
				}
			}
			golden(t, "empty_and_overdue_"+tier.name+".golden", b.String())
		})
	}
}

func hostile() Facts {
	return Facts{
		Key: "EX-\x1b[31m9\x1b[0m", Summary: "日本語のサマリー 🚀🚀 with a Supercalifragilisticexpialidocious\tword\nand\u202eevil",
		TypeGlyph: "◆", TypeName: "Épopée", StatusGlyph: "◐", StatusName: "進行中",
		Assignee: "Zoë 山田 \x1b]8;;http://x\x07link", Priority: "Höchste", Updated: "12mo", Estimate: "13.5",
		Labels: []string{"a\x1b[1mb", "界"}, FixVersions: []string{"v1\n2"},
		ParentKey: "EX-1", ParentSummary: "親 parent 🚀", Due: "2026-01-01", Subtasks: "10/12",
		Overdue: true, Category: 9, TypeZone: "t", StatusZone: "s", WhoZone: "w",
	}
}

func TestRender_EveryLineIsExactlyTheWidthAndTheCardExactlyItsHeight(t *testing.T) {
	t.Parallel()

	mgr := zone.New()
	t.Cleanup(mgr.Close)
	zones := widget.NewZoner(mgr)
	for _, tier := range tiers {
		theme := kernel.NewTheme(kernel.ThemeDark, true, tier.glyphs)
		sty := NewStyles(theme)
		for _, f := range []Facts{listFacts(tier.glyphs), boardFacts(tier.glyphs), hostile(), {}} {
			for _, look := range []Look{Roomy, Compact} {
				for _, s := range states {
					for width := 0; width <= 130; width++ {
						for _, z := range []widget.Zoner{{}, zones} {
							lines := Render(nil, f, s.st, sty, Frame{Width: width, Look: look, Glyphs: tier.glyphs, Zones: z})
							if len(lines) != look.Lines() {
								t.Fatalf("%s %s at %d drew %d lines, want %d", look.Word(), s.name, width, len(lines), look.Lines())
							}
							for i, l := range lines {
								if got := ansi.StringWidth(l); got != width {
									t.Fatalf("%s %s %s at %d: line %d is %d cells: %q", tier.name, look.Word(), s.name, width, i, got, ansi.Strip(l))
								}
							}
						}
					}
				}
			}
		}
	}
}

func TestRender_AppendsToWhatItIsGiven(t *testing.T) {
	t.Parallel()

	theme := kernel.NewTheme(kernel.ThemeDark, true, kernel.UnicodeGlyphs())
	dst := make([]string, 1, 16)
	dst[0] = "before"
	dst = Render(dst, listFacts(theme.Glyphs), State{}, NewStyles(theme), Frame{Width: 40, Look: Compact, Glyphs: theme.Glyphs})
	if len(dst) != 4 || dst[0] != "before" {
		t.Errorf("Render gave back %d lines starting %q, want the one it was given and three more", len(dst), dst[0])
	}
}

func TestRender_LinesIsNotACardAndAppendsNothing(t *testing.T) {
	t.Parallel()

	theme := kernel.NewTheme(kernel.ThemeDark, true, kernel.UnicodeGlyphs())
	dst := []string{"row"}
	got := Render(dst, listFacts(theme.Glyphs), State{}, NewStyles(theme), Frame{Width: 80, Look: Lines, Glyphs: theme.Glyphs})
	if len(got) != 1 || got[0] != "row" {
		t.Errorf("Render in the lines look gave back %q, want what it was given", got)
	}
}

func TestRender_SanitizesEveryField(t *testing.T) {
	t.Parallel()

	theme := kernel.NewTheme(kernel.ThemeNoColor, false, kernel.UnicodeGlyphs())
	for _, look := range []Look{Roomy, Compact} {
		out := strings.Join(draw(hostile(), State{}, theme, look, 200), "\n")
		for _, bad := range []string{"\x1b[31m", "\x1b]8", "\x1b[1mb", "\u202e", "\t", "v1\n2"} {
			if strings.Contains(out, bad) {
				t.Errorf("the %s card carries %q:\n%q", look.Word(), bad, out)
			}
		}
	}
}

func TestRender_ColoursTheBracketByCategoryAndAnOverdueDateInWarning(t *testing.T) {
	t.Parallel()

	theme := kernel.NewTheme(kernel.ThemeDark, true, kernel.UnicodeGlyphs())
	sty := NewStyles(theme)
	g := theme.Glyphs
	f := listFacts(g)
	f.Overdue = true
	lines := Render(nil, f, State{}, sty, Frame{Width: 120, Look: Roomy, Glyphs: g})
	for i, glyph := range []string{g.CornerTL, g.VLine, g.VLine, g.VLine, g.CornerBL} {
		if want := sty.Categories[2].Render(glyph); !strings.HasPrefix(lines[i], want) {
			t.Errorf("line %d starts %q, want the bracket in the in-progress colour %q", i, lines[i], want)
		}
	}
	if !strings.Contains(lines[3], sty.Warning.Render(f.Due)) {
		t.Errorf("an overdue date is not drawn in Warning: %q", lines[3])
	}
	f.Overdue = false
	lines = Render(nil, f, State{}, sty, Frame{Width: 120, Look: Roomy, Glyphs: g})
	if strings.Contains(lines[3], sty.Warning.Render(f.Due)) {
		t.Errorf("a date not yet due is drawn in Warning: %q", lines[3])
	}
	if !strings.Contains(lines[0], sty.Key.Render(f.Key)) {
		t.Errorf("the key is not in the accent: %q", lines[0])
	}
	if strings.Contains(lines[3], sty.Categories[2].Render(f.Priority)) || !strings.Contains(lines[3], f.Priority) {
		t.Errorf("the priority is drawn in a colour, or not at all: %q", lines[3])
	}
}

func TestRender_SelectedInvertsTheWholeCardAndHeldIsInWarning(t *testing.T) {
	t.Parallel()

	theme := kernel.NewTheme(kernel.ThemeDark, true, kernel.UnicodeGlyphs())
	sty := NewStyles(theme)
	g := theme.Glyphs
	for _, tc := range []struct {
		st    State
		style func(string) string
		mark  string
	}{
		{State{Selected: true}, func(s string) string { return sty.Selected.Render(s) }, g.Collapsed},
		{State{Held: true, Selected: true}, func(s string) string { return sty.Held.Render(s) }, g.Diamond},
		{State{Picked: true, Selected: true}, func(s string) string { return sty.Selected.Render(s) }, g.Check},
	} {
		lines := Render(nil, listFacts(g), tc.st, sty, Frame{Width: 60, Look: Compact, Glyphs: g})
		for i, l := range lines {
			if want := tc.style(ansi.Strip(l)); l != want {
				t.Errorf("%+v line %d is %q, want it drawn whole in one style: %q", tc.st, i, l, want)
			}
		}
		if head := ansi.Strip(lines[0]); !strings.HasPrefix(head, g.CornerTL+" "+tc.mark+" EX-1234") {
			t.Errorf("%+v head is %q, want the mark %q", tc.st, head, tc.mark)
		}
	}
}

func TestRender_TheMarkCellFollowsTheBoardsRule(t *testing.T) {
	t.Parallel()

	theme := kernel.NewTheme(kernel.ThemeNoColor, false, kernel.UnicodeGlyphs())
	g := theme.Glyphs
	for _, tc := range []struct {
		st   State
		want string
	}{
		{State{}, " "},
		{State{Mark: "x"}, "x"},
		{State{Selected: true}, g.Collapsed},
		{State{Picked: true}, g.Check},
		{State{Picked: true, Selected: true}, g.Check},
		{State{Held: true, Picked: true}, g.Diamond},
	} {
		head := ansi.Strip(draw(listFacts(g), tc.st, theme, Compact, 80)[0])
		if !strings.HasPrefix(head, g.CornerTL+" "+tc.want+" ") {
			t.Errorf("%+v draws %q, want the mark %q", tc.st, head, tc.want)
		}
	}
	board := ansi.Strip(draw(boardFacts(g), State{}, theme, Compact, 80)[0])
	if !strings.HasPrefix(board, g.CornerTL+" "+g.TypeTask+" EX-87 ") {
		t.Errorf("a card with no type name draws %q, want the type glyph as its mark", board)
	}
}

func TestRender_TheTypeGlyphIsDrawnOnceAndNeverLost(t *testing.T) {
	t.Parallel()

	for _, tier := range tiers {
		theme := kernel.NewTheme(kernel.ThemeNoColor, false, tier.glyphs)
		g := theme.Glyphs
		blankMark := g.CornerTL + strings.Repeat(" ", ansi.StringWidth(g.TypeBug)+2) + "EX-1234"
		glyphMark := g.CornerTL + " " + g.TypeBug + " EX-1234"
		seen := map[string]bool{}
		for width := 80; width >= 12; width-- {
			head := ansi.Strip(draw(listFacts(g), State{}, theme, Roomy, width)[0])
			after, ok := strings.CutPrefix(head, blankMark)
			switch {
			case ok && strings.HasPrefix(after, " "+g.TypeBug+" Bug"):
				seen["type cell whole"] = true
			case ok && strings.HasPrefix(after, " "+g.TypeBug+" "):
				seen["type cell down to its glyph"] = true
			case strings.HasPrefix(head, glyphMark) && !strings.Contains(strings.TrimPrefix(head, glyphMark), g.TypeBug):
				seen["type cell gone, glyph in the mark"] = true
			default:
				t.Fatalf("%s at %d draws %q: the type glyph is doubled or lost", tier.name, width, head)
			}
		}
		for _, c := range []string{"type cell whole", "type cell down to its glyph", "type cell gone, glyph in the mark"} {
			if !seen[c] {
				t.Errorf("%s: no width between 12 and 80 drew the case %q", tier.name, c)
			}
		}
	}
}

func TestRender_GivesUpCellsInTheSpecifiedOrder(t *testing.T) {
	t.Parallel()

	theme := kernel.NewTheme(kernel.ThemeNoColor, false, kernel.UnicodeGlyphs())
	g := theme.Glyphs
	f := listFacts(g)
	f.Labels, f.FixVersions = []string{"checkout"}, nil
	metaAt := func(width int) string { return ansi.Strip(draw(f, State{}, theme, Compact, width)[2]) }
	headAt := func(width int) string { return ansi.Strip(draw(f, State{}, theme, Compact, width)[0]) }

	full := metaAt(80)
	for _, want := range []string{"Ada Lovelace", "High", "2026-10-02", "2/5", "checkout"} {
		if !strings.Contains(full, want) {
			t.Fatalf("at 80 the meta line %q lacks %q", full, want)
		}
	}
	gone := func(line string, cells ...string) bool {
		for _, c := range cells {
			if strings.Contains(line, c) {
				return false
			}
		}
		return true
	}
	var seenLabelsGo, seenPriorityGo bool
	for width := 80; width >= 16; width-- {
		line := metaAt(width)
		if gone(line, "checkout") {
			seenLabelsGo = true
		}
		if gone(line, "High") {
			if !seenLabelsGo {
				t.Fatalf("at %d the priority went before the labels: %q", width, line)
			}
			seenPriorityGo = true
		}
		if !strings.Contains(line, "Ada Lovelace") {
			if !seenPriorityGo {
				t.Fatalf("at %d the assignee was cut before the priority went: %q", width, line)
			}
		}
		if who := strings.Fields(strings.TrimPrefix(line, g.CornerBL))[0]; strings.HasSuffix(who, g.Ellipsis) &&
			ansi.StringWidth(who) < minWho && !gone(line, "2/5", "2026-10-02") {
			t.Fatalf("at %d the assignee was cut below %d cells while the date or the subtasks stayed: %q", width, minWho, line)
		}
	}

	var seenTypeName bool
	for width := 80; width >= 16; width-- {
		line := headAt(width)
		if !strings.Contains(line, "EX-1234") && width >= 13 {
			t.Fatalf("at %d the key is cut: %q", width, line)
		}
		if gone(line, "Bug") {
			seenTypeName = true
		}
		if gone(line, "3d") && !seenTypeName {
			t.Fatalf("at %d updated went before the type name: %q", width, line)
		}
		if !gone(line, "Bug") && !strings.Contains(line, g.TypeBug+" Bug") {
			t.Fatalf("at %d the type name is drawn without its glyph: %q", width, line)
		}
	}
}

func TestRender_WrapsTheRoomySummaryByWord(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		s             string
		width         int
		first, second string
	}{
		{"short", 20, "short", ""},
		{"one two three four", 9, "one two", "three fo…"},
		{"one two three", 7, "one two", "three"},
		{"Supercalifragilistic word", 10, "Supercalif", "ragilisti…"},
		{"日本語のサマリー", 6, "日本語", "のサ…"},
		{"a b", 0, "", ""},
	} {
		first, second := wrap(tc.s, tc.width, "…")
		if first != tc.first || second != tc.second {
			t.Errorf("wrap(%q, %d) = %q / %q, want %q / %q", tc.s, tc.width, first, second, tc.first, tc.second)
		}
	}
}

func TestRender_MarksTheFacetCellsItIsGivenZonesFor(t *testing.T) {
	t.Parallel()

	for _, st := range []State{{}, {Selected: true}} {
		mgr := zone.New()
		t.Cleanup(mgr.Close)
		z := widget.NewZoner(mgr)
		theme := kernel.NewTheme(kernel.ThemeDark, true, kernel.UnicodeGlyphs())
		f := listFacts(theme.Glyphs)
		f.TypeZone, f.StatusZone, f.WhoZone = "type", "status", "who"
		frame := func() string {
			return strings.Join(Render(nil, f, st, NewStyles(theme), Frame{Width: 80, Look: Compact, Glyphs: theme.Glyphs, Zones: z}), "\n")
		}
		lines := strings.Split(ansi.Strip(frame()), "\n")
		for name, want := range map[string]struct {
			row  int
			text string
		}{
			"type":   {0, theme.Glyphs.TypeBug + " Bug"},
			"status": {0, theme.Glyphs.CategoryInProgress + " In Review"},
			"who":    {2, "Ada Lovelace"},
		} {
			at := uitest.Zone(t, mgr, frame, z.ID(name))
			before, _, found := strings.Cut(lines[want.row], want.text)
			if !found {
				t.Fatalf("row %d %q does not draw %q", want.row, lines[want.row], want.text)
			}
			col := ansi.StringWidth(before)
			if at.StartY != want.row || at.StartX != col || at.EndX != col+ansi.StringWidth(want.text)-1 {
				t.Errorf("%+v: zone %s spans (%d,%d)-(%d,%d), want row %d from %d over %q",
					st, name, at.StartX, at.StartY, at.EndX, at.EndY, want.row, col, want.text)
			}
		}
	}

	theme := kernel.NewTheme(kernel.ThemeDark, true, kernel.UnicodeGlyphs())
	mgr := zone.New()
	t.Cleanup(mgr.Close)
	plain := Render(nil, listFacts(theme.Glyphs), State{}, NewStyles(theme),
		Frame{Width: 80, Look: Compact, Glyphs: theme.Glyphs, Zones: widget.NewZoner(mgr)})
	if strings.Join(plain, "") != strings.Join(draw(listFacts(theme.Glyphs), State{}, theme, Compact, 80), "") {
		t.Error("a card with no zone names is marked anyway")
	}
}
