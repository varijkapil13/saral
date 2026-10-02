package sortpick

import (
	"os"
	"testing"

	"github.com/varijkapil13/saral/internal/config"
	"github.com/varijkapil13/saral/internal/testsupport"
	"github.com/varijkapil13/saral/internal/ui/kernel"
)

func TestMain(m *testing.M) { os.Exit(testsupport.IsolateDirs(m)) }

var fields = []Field{
	{ID: "created", Label: "created", Desc: true},
	{ID: "key", Label: "key"},
	{ID: "summary", Label: "summary"},
}

func open(cur Choice) *Picker {
	p := &Picker{Fields: fields}
	p.Start(cur)
	return p
}

func TestSortpick_StartsOnTheFieldInForce(t *testing.T) {
	t.Parallel()
	if p := open(Choice{Field: "summary"}); p.Cursor != 2 || !p.Open {
		t.Errorf("cursor %d, open %v", p.Cursor, p.Open)
	}
	if p := open(Choice{Field: "gone"}); p.Cursor != 0 {
		t.Errorf("a field the picker does not offer put the cursor on %d", p.Cursor)
	}
}

func TestSortpick_EveryPickerKeyDoesItsAction(t *testing.T) {
	t.Parallel()
	keys := kernel.SortPickerKeys()
	if len(keys) != 4 {
		t.Fatalf("the vocabulary has %d picker keys, want 4", len(keys))
	}
	for _, stroke := range keys[0].Keys() {
		p := open(Choice{Field: "key"})
		if _, chose, handled := p.Key(stroke, Choice{}); chose || !handled || p.Cursor != 0 {
			t.Errorf("%q: chose %v, handled %v, cursor %d", stroke, chose, handled, p.Cursor)
		}
	}
	for _, stroke := range keys[1].Keys() {
		p := open(Choice{Field: "key"})
		if _, chose, handled := p.Key(stroke, Choice{}); chose || !handled || p.Cursor != 2 {
			t.Errorf("%q: chose %v, handled %v, cursor %d", stroke, chose, handled, p.Cursor)
		}
	}
	for _, stroke := range keys[3].Keys() {
		p := open(Choice{})
		if _, chose, handled := p.Key(stroke, Choice{}); chose || !handled || p.Open {
			t.Errorf("%q: chose %v, handled %v, open %v", stroke, chose, handled, p.Open)
		}
	}
	for _, stroke := range keys[2].Keys() {
		p := open(Choice{})
		if got, chose, _ := p.Key(stroke, Choice{}); !chose || got != (Choice{Field: "created", Desc: true}) || p.Open {
			t.Errorf("%q chose %+v (%v), open %v", stroke, got, chose, p.Open)
		}
	}
}

func TestSortpick_WrapsAtBothEnds(t *testing.T) {
	t.Parallel()
	p := open(Choice{Field: "created"})
	p.Key("left", Choice{})
	if p.Cursor != 2 {
		t.Errorf("left from the first field landed on %d", p.Cursor)
	}
	p.Key("right", Choice{})
	if p.Cursor != 0 {
		t.Errorf("right from the last field landed on %d", p.Cursor)
	}
}

func TestSortpick_ChoosingTheFieldInForceFlipsIt(t *testing.T) {
	t.Parallel()
	p := open(Choice{Field: "key"})
	got, chose, _ := p.Key("enter", Choice{Field: "key"})
	if !chose || got != (Choice{Field: "key", Desc: true}) {
		t.Errorf("chose %+v (%v)", got, chose)
	}
	p = open(Choice{Field: "key", Desc: true})
	if got, _, _ = p.Key("enter", Choice{Field: "key", Desc: true}); got.Desc {
		t.Error("a descending field did not flip back")
	}
}

func TestSortpick_ClosedPickerTakesNothingAndOpenOneTakesEverything(t *testing.T) {
	t.Parallel()
	closed := &Picker{Fields: fields}
	if _, _, handled := closed.Key("enter", Choice{}); handled {
		t.Error("a closed picker took a stroke")
	}
	p := open(Choice{})
	if _, chose, handled := p.Key("x", Choice{}); chose || !handled || !p.Open {
		t.Errorf("a stray stroke: chose %v, handled %v, open %v", chose, handled, p.Open)
	}
}

func TestSortpick_LineMarksCursorAndDirection(t *testing.T) {
	t.Parallel()
	got := Line(fields, Choice{Field: "key", Desc: true}, 2, kernel.UnicodeGlyphs())
	if want := "sort by:  created  key ↓  [summary]"; got != want {
		t.Errorf("line is %q, want %q", got, want)
	}
	got = Line(fields, Choice{Field: "created"}, 0, kernel.ASCIIGlyphs())
	if want := "sort by:  [created ^]  key  summary"; got != want {
		t.Errorf("ascii line is %q, want %q", got, want)
	}
}

func TestSortpick_SpecRoundTripsAndRefusesAStrangeField(t *testing.T) {
	t.Parallel()
	c := Choice{Field: "summary", Desc: true}
	back, ok := FromSpec(c.Spec(), fields)
	if !ok || back != c {
		t.Errorf("round trip gave %+v (%v)", back, ok)
	}
	if _, ok := FromSpec(config.SortSpec{Field: "ninth"}, fields); ok {
		t.Error("a field the picker does not offer was read as a choice")
	}
	if (Choice{}).Chosen() || !c.Chosen() {
		t.Error("Chosen is wrong")
	}
}
