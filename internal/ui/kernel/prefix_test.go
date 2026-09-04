package kernel

import (
	"reflect"
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"
)

// prefixModel is a kernel with a slotted view and the two views the prefix
// gestures open, so every row of the overlay is reachable rather than dimmed.
func prefixModel(t *testing.T) Model {
	t.Helper()
	resetRegistry()
	t.Cleanup(resetRegistry)
	RegisterView(spec("board", 1, "", &stubView{id: "board"}))
	RegisterView(ViewSpec{ID: PaletteViewID, Title: "Commands", New: func(Deps) View {
		return &stubView{id: PaletteViewID}
	}})
	RegisterView(ViewSpec{ID: SettingsViewID, Title: "Settings", New: func(Deps) View {
		return &stubView{id: SettingsViewID}
	}})
	return newAt(t, testDeps(), 120, 38)
}

// The check that keeps the table honest from the outside: a global whose label
// spells a gesture on the go-to prefix has to be in prefixGestures, or it is a
// key that works and is taught nowhere. g s was exactly that — dispatched by
// resolvePrefix, absent from the overlay and from the footer — and nothing
// failed, because each of the three was written out by hand.
//
// The digits are the one exemption: they are a range rather than a gesture, and
// destinations() draws a row per view that claims one.
func TestPrefixGestures_CoverEveryGlobalOnThePrefix(t *testing.T) {
	m := prefixModel(t)
	lead := m.keys.Go.Help().Key + " "

	covered := make(map[string]bool, len(m.prefixGestures()))
	for _, g := range m.prefixGestures() {
		gesture, ok := gestureIn(g.key, lead)
		if !ok {
			t.Errorf("prefixGestures holds %q, whose label %q spells no gesture on %q",
				g.name, g.key.Help().Key, lead)
			continue
		}
		covered[gesture] = true
	}

	keys := reflect.ValueOf(m.keys)
	scanned, found := 0, 0
	for i := range keys.NumField() {
		field, ok := keys.Field(i).Interface().(Binding)
		if !ok {
			continue
		}
		scanned++
		name := keys.Type().Field(i).Name
		gesture, spells := gestureIn(field, lead)
		if !spells || name == "Slot" {
			continue
		}
		found++
		if !covered[gesture] {
			t.Errorf("GlobalKeys.%s spells %q but is not in prefixGestures, so the key works and "+
				"neither the overlay nor the footer says it exists", name, gesture)
		}
	}
	if scanned == 0 {
		t.Fatal("no binding was read off GlobalKeys, so this test proved nothing")
	}
	if found == 0 {
		t.Fatalf("no global spells a gesture on %q among %d bindings, so this test proved nothing", lead, scanned)
	}
}

// Dispatching one and showing it are the same edit now, so the overlay is held
// to the table rather than to a list written beside it.
func TestPrefixGestures_AreDrawnInTheOverlay(t *testing.T) {
	m := prefixModel(t)
	next, _ := press(m, "g")
	m = next

	body := ansi.Strip(m.Frame())
	gestures := m.prefixGestures()
	if len(gestures) == 0 {
		t.Fatal("no prefix gesture is registered, so this test proved nothing")
	}
	for _, g := range gestures {
		gesture, _ := gestureIn(g.key, m.keys.Go.Help().Key+" ")
		if !strings.Contains(body, gesture) {
			t.Errorf("%q is not on the overlay:\n%s", gesture, body)
		}
		if !strings.Contains(body, g.name) {
			t.Errorf("%q is not named on the overlay:\n%s", g.name, body)
		}
	}
}

// The footer under the latched prefix says what the box answers to, and a
// gesture missing from it is one nobody at 80 columns is told about.
func TestPrefixGestures_AreAdvertisedInTheFooter(t *testing.T) {
	m := prefixModel(t)
	acts := m.destFooterActs()
	if len(m.prefixGestures()) == 0 {
		t.Fatal("no prefix gesture is registered, so this test proved nothing")
	}
	for _, g := range m.prefixGestures() {
		found := false
		for _, act := range acts {
			if act.Help().Desc == g.key.Help().Desc {
				found = true
			}
		}
		if !found {
			t.Errorf("%q is not in the footer under the prefix: %v", g.key.Help().Desc, acts)
		}
	}
}

// And each one actually goes where the overlay says. A row drawn but not
// dispatched is the same failure the other way round.
func TestPrefixGestures_EachOneOpensWhatItNames(t *testing.T) {
	for _, tc := range []struct{ stroke, view string }{
		{"i", PaletteViewID},
		{"s", SettingsViewID},
	} {
		t.Run(tc.stroke, func(t *testing.T) {
			m := prefixModel(t)
			next, _ := press(m, "g", tc.stroke)
			if got := ansi.Strip(next.Frame()); !strings.Contains(got, tc.view+" body") {
				t.Errorf("g %s did not open %s:\n%s", tc.stroke, tc.view, got)
			}
		})
	}
}

// Arrowing onto a gesture and pressing enter is the third way in, and it is
// what the mouse resolves through as well: every row carries its own zone.
func TestPrefixGestures_AreReachableWithTheArrowsAndEnter(t *testing.T) {
	m := prefixModel(t)
	dests := m.destinations()
	at := -1
	for i := range dests {
		if dests[i].open != nil {
			at = i
			break
		}
	}
	if at < 0 {
		t.Fatal("no destination is a gesture, so this test proved nothing")
	}
	if dests[at].zone == "" {
		t.Errorf("the gesture row %q carries no click zone", dests[at].key)
	}

	next, _ := press(m, "g")
	m = next
	for range at {
		m, _ = press(m, "down")
	}
	m, _ = press(m, "enter")
	if got := ansi.Strip(m.Frame()); strings.Contains(got, "Where g goes") {
		t.Errorf("enter on %q left the overlay up:\n%s", dests[at].key, got)
	}
}
