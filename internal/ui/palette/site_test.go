package palette

import (
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/varijkapil13/saral/internal/ui/kernel"
	"github.com/varijkapil13/saral/internal/ui/search"
	"github.com/varijkapil13/saral/internal/ui/uitest"
)

func TestPalette_OffersASiteSearchUnderEverythingItFound(t *testing.T) {
	t.Parallel()

	d, _ := cachedDeps()
	p := fly(t, d, sample(), memoryTable(), 120, 24)
	p.typeText("login")

	last := p.m.shown[len(p.m.shown)-1]
	if !last.find {
		t.Fatalf("the last row is %+v, want the site search", last)
	}
	if len(p.keys()) == 0 {
		t.Fatal("the cache found nothing, so this proves nothing about being under it")
	}
	if p.m.onFind() {
		t.Error("the site search took the cursor from a better match")
	}
	frame := p.frame()
	mustContain(t, frame, `Search issues for "login"`)
	for _, line := range strings.Split(frame, "\n") {
		if strings.Contains(line, "Search issues for") && !strings.HasSuffix(strings.TrimRight(line, " "), "g /") {
			t.Errorf("the row does not carry the gesture that reaches the same view: %q", line)
		}
	}
}

func TestPalette_NothingCachedMatchesSoTheSiteSearchIsSelected(t *testing.T) {
	t.Parallel()

	p := fly(t, paletteDeps(), sample(), memoryTable(), 120, 24)
	p.typeText("zzzz")

	if len(p.m.shown) != 1 || !p.m.shown[0].find {
		t.Fatalf("rows %+v, want the site search alone", p.m.shown)
	}
	if !p.m.onFind() {
		t.Error("the cursor is not on the only row there is")
	}
}

func TestPalette_AnEmptyFilterOffersNoSiteSearch(t *testing.T) {
	t.Parallel()

	p := fly(t, paletteDeps(), sample(), memoryTable(), 120, 24)
	for _, at := range p.m.shown {
		if at.find {
			t.Fatal("the palette as it opens offers a search of nothing")
		}
	}
	p.typeText("zz")
	p.press("backspace", "backspace")
	for _, at := range p.m.shown {
		if at.find {
			t.Fatal("clearing the filter left the site search behind")
		}
	}
	p.typeText("   ")
	for _, at := range p.m.shown {
		if at.find {
			t.Fatal("a filter of spaces offers a search of nothing")
		}
	}
}

func TestPalette_EnterOnTheSiteSearchOpensItPrefilledOverWhatThePaletteWasOpenedFrom(t *testing.T) {
	t.Parallel()

	d, _ := cachedDeps()
	p := fly(t, d, sample(), memoryTable(), 120, 24)
	p.typeText("login")
	p.press("down")
	if !p.m.onFind() {
		t.Fatal("down did not reach the site search")
	}
	p.press("enter")

	var order []string
	var pushed kernel.PushMsg
	for _, msg := range p.msgs {
		switch msg := msg.(type) {
		case kernel.PopMsg:
			order = append(order, "pop")
		case kernel.PushMsg:
			order, pushed = append(order, "push"), msg
		case kernel.RunCommandMsg:
			t.Errorf("enter on the site search ran the command %q", msg.ID)
		}
	}
	if strings.Join(order, ",") != "pop,push" {
		t.Fatalf("enter did %v, want the palette put away and then the search pushed", order)
	}
	if pushed.ID != search.ViewID {
		t.Fatalf("pushed %q, want %q", pushed.ID, search.ViewID)
	}
	view, _ := pushed.View.Update(kernel.SizeMsg{Width: 100, Height: 20})
	mustContain(t, ansi.Strip(view.View()), "search login")
}

func TestPalette_TheSiteSearchRowIsClickable(t *testing.T) {
	t.Parallel()

	d, _ := cachedDeps()
	p := fly(t, d, sample(), memoryTable(), 120, 24)
	p.typeText("login")
	found := uitest.Zone(t, d.Zones, p.m.View, p.m.zones.ID(zoneFind))
	at := zoneBounds{StartX: found.StartX, StartY: found.StartY}
	click := tea.MouseClickMsg{X: at.StartX + 2, Y: at.StartY, Button: tea.MouseLeft}

	p.send(click)
	if !p.m.onFind() {
		t.Fatal("the first click did not select the site search")
	}
	if got := p.pushed(); len(got) != 0 {
		t.Fatalf("the first click pushed %v; a click selects", got)
	}
	p.send(click)
	if got := p.pushed(); len(got) != 1 || got[0] != search.ViewID+":Search" {
		t.Errorf("the second click pushed %v", got)
	}
}

func TestPalette_RefusalsAreStillNamedUnderTheSiteSearch(t *testing.T) {
	t.Parallel()

	p := fly(t, paletteDeps(), sample(), memoryTable(), 120, 24)
	p.typeText("move")

	frame := p.frame()
	mustContain(t, frame, `Search issues for "move"`, "Move issues between projects", noBulkMove)
	if strings.Index(frame, "Search issues for") > strings.Index(frame, noBulkMove) {
		t.Error("the refusal is above the row it explains the absence beside")
	}
}

func TestPalette_FooterNamesTheSiteSearch(t *testing.T) {
	t.Parallel()

	p := fly(t, paletteDeps(), sample(), memoryTable(), 120, 24)
	p.typeText("zzzz")
	set, gen := p.m.LiveKeys()
	if gen != int(keysFind) {
		t.Fatalf("the keys are in state %d, want the site search's", gen)
	}
	if labels := actsOf(set); !strings.Contains(labels, "enter search the site") || !strings.Contains(labels, "esc close") {
		t.Errorf("the footer says %q", labels)
	}
}

func TestView_GoldenWithTheSiteSearchAlone(t *testing.T) {
	t.Parallel()

	p := fly(t, paletteDeps(), sample(), memoryTable(), 80, 24)
	p.typeText("zebra")
	golden(t, "palette_find_row_80x24.golden", p.frame())
}
