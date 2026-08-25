package issue

import (
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"

	"github.com/varijkapil13/saral/internal/ui/kernel"
	"github.com/varijkapil13/saral/internal/ui/richtext"
	"github.com/varijkapil13/saral/pkg/adf"
)

// The table in the packet brief, held against the code that has to produce it.
func TestLayout_TheBreakpointIsWhereTheSidebarStartsFitting(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		w          int
		wide       bool
		desc, side int
	}{
		{w: 80, wide: false, desc: 79, side: 79},
		{w: 89, wide: false, desc: 88, side: 88},
		{w: 90, wide: true, desc: 53, side: 34},
		{w: 100, wide: true, desc: 63, side: 34},
		{w: 120, wide: true, desc: 78, side: 39},
		{w: 200, wide: true, desc: 153, side: 44},
	} {
		m := &Model{width: tc.w}
		desc, side := m.contentWidths()
		lay := newLayout(tc.w, 28, 4, regionDesc)
		switch {
		case lay.wide != tc.wide:
			t.Errorf("%d columns is wide=%v, want %v", tc.w, lay.wide, tc.wide)
		case desc != tc.desc:
			t.Errorf("%d columns gives the description %d cells, want %d", tc.w, desc, tc.desc)
		case side != tc.side:
			t.Errorf("%d columns gives the sidebar %d cells, want %d", tc.w, side, tc.side)
		}
		if !lay.wide {
			continue
		}
		if got := lay.boxes[regionDesc].w + divider + lay.boxes[regionDetails].w; got != tc.w {
			t.Errorf("%d columns lays out %d of them", tc.w, got)
		}
	}
}

// The floors are measurements, so they are measured here rather than trusted.
// Below 53 the description loses about two words a line for every ten cells, and
// below 34 the sidebar cannot fit its label column and a value beside it.
func TestLayout_TheFloorsAreWhatRealContentNeeds(t *testing.T) {
	t.Parallel()

	th := kernel.NewTheme(kernel.ThemeNoColor, true, kernel.ASCIIGlyphs())
	st := newStyles(th)
	doc := adf.NewDoc(adf.NewNode("paragraph", adf.NewText(
		"The nightly export dies whenever a tenant has more than one active contract, "+
			"because the query joins on the contract table without a group by and the writer "+
			"then sees the same invoice twice.")))

	density := func(w int) float64 {
		r := richtext.Render(doc, richtext.Options{Width: w, Styles: st.rich, Markers: st.markers})
		words, lines := 0, 0
		for _, l := range r.Lines {
			if s := strings.TrimSpace(ansi.Strip(l)); s != "" {
				lines++
				words += len(strings.Fields(s))
			}
		}
		return float64(words) / float64(max(lines, 1))
	}
	atFloor, below := density(53), density(34)
	if atFloor < 8 {
		t.Errorf("prose runs at %.1f words a line at the description's floor of 53 cells, want at least 8", atFloor)
	}
	if below >= atFloor-1 {
		t.Errorf("prose runs at %.1f words a line at 34 cells and %.1f at 53, so the floor buys nothing", below, atFloor)
	}

	// The sidebar's floor: the widest label this pane draws, plus room for a
	// value that is not one word.
	widest := 0
	for _, d := range platform {
		widest = max(widest, ansi.StringWidth(d.label))
	}
	if widest >= labelWidth {
		t.Errorf("the widest label is %d cells and the column is %d, so it runs into its own value", widest, labelWidth)
	}
	if room := sideMin - gutter - labelWidth - 2; room < 15 {
		t.Errorf("the narrowest sidebar leaves %d cells for a value, which is not a readable one", room)
	}
}

// A code line wider than the pane is truncated rather than wrapped, because the
// renderer never wraps code — so panning is what reaches the rest of it. A
// realistic Go signature is around eighty cells and the widest description box
// the wide mode gives is seventy-eight, so this is not an exotic case.
func TestLayout_PanningReachesACodeLineWiderThanThePane(t *testing.T) {
	t.Parallel()

	f := newFake(8)
	full := readIssue(t, f, "PROJ-2")
	full.Description = codeDoc()
	dr := newDriver(t, testDeps(f), seedOf(t, f, "PROJ-2"), 120, 24)
	dr.send(loadedMsg{gen: dr.m.gen, issue: full})

	mustContain(t, dr.view(), "func (c *Client) Export(ctx")
	mustNotContain(t, dr.view(), "(Report, error) {")
	if dr.m.panes[regionDesc].widest <= dr.m.lay.boxes[regionDesc].content() {
		t.Fatal("the description fits, so this proves nothing")
	}

	for range 6 {
		dr.key("l")
	}
	mustContain(t, dr.view(), "(Report, error) {")
	if dr.m.pans[regionDesc] == 0 {
		t.Error("six strokes of l panned nothing")
	}

	dr.key("g", "g")
	if dr.m.pans[regionDesc] != 0 {
		t.Error("g g did not come back to the left edge as well as the top")
	}
}

func codeDoc() adf.Doc {
	block := adf.NewNode("codeBlock", adf.NewText(
		"func (c *Client) Export(ctx context.Context, tenant string) (Report, error) {\n"+
			"\treturn c.do(ctx, http.MethodPost, \"/export/\"+tenant, nil)\n}"))
	block.Attrs = adf.Attrs{"language": "go"}
	return adf.NewDoc(adf.NewNode("paragraph", adf.NewText("The client is below.")), block)
}

// The gutter is the scrollbar as well as the focus rail: a thumb where the
// content does not fit, and none where it does.
func TestRail_TheThumbSaysWhereYouAreAndIsAbsentWhenEverythingFits(t *testing.T) {
	t.Parallel()

	if got := railFor(10, 8, 0, true); got.from != -1 {
		t.Errorf("eight lines in ten rows drew a thumb at %d", got.from)
	}
	top := railFor(10, 100, 0, true)
	switch {
	case top.from != 0:
		t.Errorf("the thumb at the top starts at row %d, want 0", top.from)
	case top.to-top.from != 1:
		t.Errorf("a hundred lines in ten rows sized the thumb %d rows, want one", top.to-top.from)
	}
	if bottom := railFor(10, 100, 90, true); bottom.from != 9 {
		t.Errorf("the thumb at the end starts at row %d, want the last one", bottom.from)
	}
	// A thumb never leaves the rail, whatever the proportions.
	for _, total := range []int{11, 13, 40, 999} {
		for top := 0; top <= total; top++ {
			r := railFor(12, total, min(top, total-12), true)
			if r.from < 0 {
				continue
			}
			if r.from < 0 || r.to > 12 {
				t.Fatalf("%d lines at offset %d put the thumb at rows %d..%d of twelve", total, top, r.from, r.to)
			}
		}
	}
}

// Which region has the keyboard is said by the rail's colour, and in the
// no-colour theme by the emphasis that survives it.
func TestRail_SaysWhichRegionHasTheKeyboard(t *testing.T) {
	t.Parallel()

	for _, mode := range []kernel.ThemeMode{kernel.ThemeDark, kernel.ThemeLight, kernel.ThemeNoColor} {
		st := newStyles(kernel.NewTheme(mode, true, kernel.UnicodeGlyphs()))
		on := railFor(4, 4, 0, true).cell(st, 0)
		off := railFor(4, 4, 0, false).cell(st, 0)
		if on == off {
			t.Errorf("%v draws the focused and unfocused rail identically as %q", mode, on)
		}
		if ansi.Strip(on) != ansi.Strip(off) {
			t.Errorf("%v tells the two apart by the glyph rather than by emphasis: %q against %q",
				mode, ansi.Strip(on), ansi.Strip(off))
		}
		run := railFor(4, 40, 0, false)
		if track, thumb := ansi.Strip(run.cell(st, 3)), ansi.Strip(run.cell(st, 0)); track == thumb {
			t.Errorf("%v draws the thumb and the track with the same glyph %q, so an unfocused "+
				"region cannot say there is more below", mode, thumb)
		}
	}
}

// Below the breakpoint one region is on screen at a time, and tab is what walks
// them. The footer says the same thing at every width, because one key set
// answers for the whole pane.
func TestIssue_NarrowModeShowsOneRegionAtATime(t *testing.T) {
	t.Parallel()

	f := newFake(8)
	addComment(t, f, "PROJ-4", "Only visible once the thread has the screen.")
	dr := newDriver(t, testDeps(f), seedOf(t, f, "PROJ-4"), 80, 20)

	mustContain(t, dr.view(), "Document CSV import.")
	mustNotContain(t, dr.view(), "Details", "Only visible once")

	dr.key("tab")
	mustContain(t, dr.view(), "Details", "Reporter")
	mustNotContain(t, dr.view(), "Document CSV import.", "Only visible once")

	dr.key("tab")
	mustContain(t, dr.view(), "Only visible once the thread")
	mustNotContain(t, dr.view(), "Document CSV import.", "Reporter")

	dr.key("tab")
	if dr.m.focus != regionDesc {
		t.Errorf("three tabs left the keyboard on region %d, want back at the description", dr.m.focus)
	}
	dr.key("shift+tab")
	if dr.m.focus != regionComments {
		t.Errorf("shift+tab from the description went to region %d, want round the other way", dr.m.focus)
	}
}

// Crossing the breakpoint in either direction keeps the keyboard where it was
// and leaves the scroll somewhere the content actually reaches.
func TestIssue_AResizeAcrossTheBreakpointKeepsFocusAndScrollSane(t *testing.T) {
	t.Parallel()

	f := newFake(8)
	full := readIssue(t, f, "PROJ-3")
	full.Description = longDoc(60)
	dr := newDriver(t, testDeps(f), seedOf(t, f, "PROJ-3"), 120, 30)
	dr.send(loadedMsg{gen: dr.m.gen, issue: full})
	dr.key("tab")
	dr.key("G")

	wide := dr.m.tops[regionDetails]
	if dr.m.focus != regionDetails {
		t.Fatalf("tab left the keyboard on region %d", dr.m.focus)
	}

	dr.send(kernel.SizeMsg{Width: 80, Height: 20})
	if dr.m.focus != regionDetails {
		t.Errorf("going narrow moved the keyboard to region %d", dr.m.focus)
	}
	sane(t, dr, "narrow")

	dr.send(kernel.SizeMsg{Width: 120, Height: 30})
	if dr.m.focus != regionDetails {
		t.Errorf("going wide again moved the keyboard to region %d", dr.m.focus)
	}
	sane(t, dr, "wide again")
	if got := dr.m.tops[regionDetails]; got > wide {
		t.Errorf("the fields came back scrolled to %d, past the %d they were at", got, wide)
	}
}

// sane holds every region's offset inside what its content and its box allow. An
// offset past the end draws a box of blanks and looks like a pane that lost its
// data.
func sane(t *testing.T, dr *driver, when string) {
	t.Helper()

	for r := range regionCount {
		if r == regionComments {
			continue
		}
		b := dr.m.lay.boxes[r]
		if !b.drawn() {
			continue
		}
		if want := max(len(dr.m.panes[r].lines)-b.h, 0); dr.m.tops[r] > want {
			t.Errorf("%s: region %d is scrolled to line %d of %d in a box %d tall",
				when, r, dr.m.tops[r], len(dr.m.panes[r].lines), b.h)
		}
		if want := max(dr.m.panes[r].widest-b.content(), 0); dr.m.pans[r] > want {
			t.Errorf("%s: region %d is panned to cell %d, past the %d there is to see",
				when, r, dr.m.pans[r], want)
		}
	}
}

// Every memo is keyed rather than flagged, so this walks the four things that
// have to move it and checks the lines actually changed.
func TestIssue_NoMemoSurvivesAResizeAThemeSwitchAFoldOrAProjectSwitch(t *testing.T) {
	t.Parallel()

	f := newFake(8)
	full := readIssue(t, f, "PROJ-6")
	full.Description = foldDoc()
	dr := newDriver(t, testDeps(f), seedOf(t, f, "PROJ-6"), 120, 30)
	dr.send(loadedMsg{gen: dr.m.gen, issue: full})

	for _, tc := range []struct {
		name string
		do   func()
	}{
		{"a resize", func() { dr.send(kernel.SizeMsg{Width: 100, Height: 30}) }},
		{"a theme switch", func() {
			dr.send(kernel.ThemeMsg{Theme: kernel.NewTheme(kernel.ThemeDark, true, kernel.UnicodeGlyphs())})
		}},
		{"a fold", func() { dr.key("z") }},
		{"a project switch", func() { dr.send(kernel.ProjectMsg{Project: "OTHER"}) }},
		{"a fresh read", func() { dr.send(loadedMsg{gen: dr.m.gen, issue: full}) }},
	} {
		before := dr.m.panes[regionDesc].key
		beforeDetails := dr.m.panes[regionDetails].key
		tc.do()
		_ = dr.view()
		if dr.m.panes[regionDesc].key == before {
			t.Errorf("%s left the description's memo key untouched, so a stale render survives it", tc.name)
		}
		if dr.m.panes[regionDetails].key == beforeDetails {
			t.Errorf("%s left the fields' memo key untouched", tc.name)
		}
	}
}

// An expand is closed until the reader opens one, which is how Jira shows it
// too — so the pane owes them a gesture, and the title is on screen either way.
func TestIssue_AnExpandIsClosedUntilZOpensIt(t *testing.T) {
	t.Parallel()

	f := newFake(8)
	full := readIssue(t, f, "PROJ-6")
	full.Description = foldDoc()
	dr := newDriver(t, testDeps(f), seedOf(t, f, "PROJ-6"), 120, 30)
	dr.send(loadedMsg{gen: dr.m.gen, issue: full})

	mustContain(t, dr.view(), "How we tested it")
	mustNotContain(t, dr.view(), "Twice on staging")

	dr.key("z")
	mustContain(t, dr.view(), "How we tested it", "Twice on staging")

	dr.key("z")
	mustNotContain(t, dr.view(), "Twice on staging")
}

func foldDoc() adf.Doc {
	expand := adf.NewNode("expand", adf.NewNode("paragraph", adf.NewText("Twice on staging, once in production.")))
	expand.Attrs = adf.Attrs{"title": "How we tested it"}
	return adf.NewDoc(adf.NewNode("paragraph", adf.NewText("The export is wrong.")), expand)
}

// And the rail is actually drawn: the leftmost column of each region carries the
// track, the thumb where there is more to see, and nothing else.
func TestRail_IsTheLeftmostColumnOfEveryRegion(t *testing.T) {
	t.Parallel()

	f := newFake(8)
	full := readIssue(t, f, "PROJ-3")
	full.Description = longDoc(60)
	dr := newDriver(t, testDeps(f), seedOf(t, f, "PROJ-3"), 120, 24)
	dr.send(loadedMsg{gen: dr.m.gen, issue: full})

	glyphs := kernel.ASCIIGlyphs()
	rows := strings.Split(dr.view(), "\n")[headerHeight:]
	descAt, sideAt := 0, dr.m.lay.boxes[regionDetails].x
	thumbs := 0
	for i, row := range rows {
		for _, at := range []int{descAt, sideAt} {
			cell := string([]rune(row)[at])
			switch cell {
			case glyphs.VLine:
			case glyphs.ProgressOn:
				thumbs++
			default:
				t.Fatalf("row %d column %d is %q, want the rail", i, at, cell)
			}
		}
	}
	if thumbs == 0 {
		t.Error("sixty paragraphs in twenty-one rows drew no thumb at all")
	}
	if thumbs >= len(rows) {
		t.Errorf("the thumb covers %d of %d rows, so it says nothing about where the pane is", thumbs, len(rows))
	}

	// And it goes away when everything fits.
	short := readIssue(t, f, "PROJ-3")
	short.Description = longDoc(1)
	fits := newDriver(t, testDeps(f), seedOf(t, f, "PROJ-3"), 120, 40)
	fits.send(loadedMsg{gen: fits.m.gen, issue: short})
	if strings.Contains(fits.view(), glyphs.ProgressOn) {
		t.Errorf("a pane whose content fits still drew a thumb:\n%s", fits.view())
	}
}
