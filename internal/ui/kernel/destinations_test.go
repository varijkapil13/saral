package kernel

import (
	"strconv"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/varijkapil13/saral/pkg/jira"
)

// slotTitles is a view on every digit, which is the widest the overlay can ever
// be: RegisterView takes slots 1 to 9 and refuses anything else.
var slotTitles = []string{
	"Issues", "Board", "Backlog", "Sprints", "Releases", "Timeline", "Plans", "Reports", "Filters",
}

// destBoard is a view whose inventory carries a two-stroke gesture on the same
// prefix the overlay is drawn under, spelt the way the three views in this
// program spell it: the label says g g and the stroke it matches is home.
func destBoard() *actingView {
	return &actingView{set: KeySet{
		Acts: []Binding{Bind([]string{"enter"}, "enter", "open")},
		Full: [][]Binding{{
			Bind([]string{"home"}, "g g", "first row"),
			Bind([]string{"G", "end"}, "G", "last row"),
		}},
	}}
}

// destRegistry registers n slots, the first of them the view under test.
func destRegistry(t *testing.T, first View, n int) {
	t.Helper()
	resetRegistry()
	t.Cleanup(resetRegistry)
	for i := range n {
		id, slot := strings.ToLower(slotTitles[i]), i+1
		view := first
		if i > 0 {
			view = &stubView{id: id}
		}
		RegisterView(ViewSpec{ID: id, Title: slotTitles[i], Slot: slot,
			New: func(Deps) View { return view }})
	}
}

func latched(t *testing.T, d Deps, w, h, slots int, view View) Model {
	t.Helper()
	destRegistry(t, view, slots)
	m := newAt(t, d, w, h, WithMouse(true))
	m, _ = press(m, "g")
	if !m.prefixSet {
		t.Fatal("g did not latch the prefix")
	}
	return m
}

// The complaint this answers, from somebody using the built binary: nothing at
// rest said the other views existed. Pressing the prefix is the moment the
// question is being asked, and the program was already sitting there waiting.
func TestDestinations_TheLatchedPrefixNamesEverySlotAndItsDigit(t *testing.T) {
	m := latched(t, testDeps(), 120, 38, 9, destBoard())

	frame := ansi.Strip(m.Frame())
	for i, title := range slotTitles {
		if !strings.Contains(frame, title) {
			t.Errorf("the overlay does not name %q:\n%s", title, frame)
		}
		if gesture := "g" + strconv.Itoa(i+1); !strings.Contains(frame, gesture) {
			t.Errorf("the overlay does not say %q reaches %s:\n%s", gesture, title, frame)
		}
	}
}

// The registry is the source: a slot list written down here would be a second
// answer to a question RegisterView already refuses a duplicate on.
func TestDestinations_ComeFromTheRegistryAndNotFromAList(t *testing.T) {
	m := latched(t, testDeps(), 120, 38, 3, destBoard())

	frame := ansi.Strip(m.Frame())
	for _, gone := range slotTitles[3:] {
		if strings.Contains(frame, gone) {
			t.Errorf("the overlay offers %q, which nothing registered:\n%s", gone, frame)
		}
	}
}

// A negative capability is an answer with a reason attached, which is the rule
// everywhere else in this program: the row stays, and it carries the probe's own
// sentence rather than being left out.
func TestDestinations_AViewOutOfReachSaysWhyInTheProbesOwnWords(t *testing.T) {
	const reason = "Plans need Administer Jira, which this token does not have"
	d := testDeps()
	d.Caps.Plans = jira.Capability{Reason: reason}

	resetRegistry()
	t.Cleanup(resetRegistry)
	RegisterView(spec("board", 1, "", destBoard()))
	RegisterView(ViewSpec{ID: "plans", Title: "Plans", Slot: 7, Requires: jira.CapPlans,
		New: func(Deps) View { return &stubView{id: "plans"} }})

	m := newAt(t, d, 120, 38)
	m, _ = press(m, "g")
	if frame := ansi.Strip(m.Frame()); !strings.Contains(frame, reason) {
		t.Errorf("the overlay does not say why Plans is out of reach:\n%s", frame)
	}
}

// A row nobody can reach is not a place the cursor may rest, so no number of
// motions can put it under one and enter can never spend the gesture on it.
func TestDestinations_ARowNobodyCanReachIsNotSelectable(t *testing.T) {
	d := testDeps()
	d.Caps.Plans = jira.Capability{Reason: "Plans need Administer Jira"}

	resetRegistry()
	t.Cleanup(resetRegistry)
	RegisterView(spec("board", 1, "", destBoard()))
	RegisterView(ViewSpec{ID: "plans", Title: "Plans", Slot: 7, Requires: jira.CapPlans,
		New: func(Deps) View { return &stubView{id: "plans"} }})
	RegisterView(spec("backlog", 3, "", &stubView{id: "backlog"}))

	m := newAt(t, d, 120, 38)
	for _, key := range []string{"j", "j", "j", "k", "k", "k"} {
		m, _ = press(m, "g")
		next, _ := press(m, key)
		dests := next.destinations()
		if next.dest < 0 || next.dest >= len(dests) {
			t.Fatalf("%s put the cursor at %d over %d rows", key, next.dest, len(dests))
		}
		if !dests[next.dest].reachable {
			t.Fatalf("%s landed the cursor on %q, which nothing can reach", key, dests[next.dest].title)
		}
		m = next
		m, _ = press(m, "esc")
	}

	m, _ = press(m, "g", "j", "j", "enter")
	if got := ansi.Strip(m.Frame()); strings.Contains(got, "plans body") {
		t.Errorf("enter opened a view whose capability is absent:\n%s", got)
	}
}

// The overlay teaches its own gesture by example: the row for the view you are
// looking at is the one the cursor opens on, and it says so in words as well.
func TestDestinations_MarkTheViewYouAreIn(t *testing.T) {
	m := latched(t, testDeps(), 120, 38, 3, destBoard())

	if got := m.destinations()[m.dest].title; got != slotTitles[0] {
		t.Errorf("the cursor opened on %q, not on the view this session is in", got)
	}
	frame := ansi.Strip(m.Frame())
	if !strings.Contains(frame, destOn) {
		t.Errorf("nothing in the overlay says which view is up:\n%s", frame)
	}

	m, _ = press(m, "esc")
	m, _ = press(m, "g", "2")
	m, _ = press(m, "g")
	if got := m.destinations()[m.dest].title; got != slotTitles[1] {
		t.Errorf("after switching, the cursor opened on %q rather than on the view now up", got)
	}
}

// The prefix is two things at once: the kernel's slot gesture and the first
// stroke of the focused view's own. Naming both is what keeps the overlay from
// shadowing the half it does not own.
func TestDestinations_AlsoNameTheFocusedViewsOwnGestures(t *testing.T) {
	m := latched(t, testDeps(), 120, 38, 3, destBoard())

	frame := ansi.Strip(m.Frame())
	for _, want := range []string{destHere, "g g", "first row"} {
		if !strings.Contains(frame, want) {
			t.Errorf("the overlay does not teach %q, which the focused view answers:\n%s", want, frame)
		}
	}
	// The view's other motions are not the prefix's, and a box that listed them
	// would be answering a question nobody asked by pressing g.
	if strings.Contains(frame, "last row") {
		t.Errorf("the overlay lists a stroke that has nothing to do with the prefix:\n%s", frame)
	}
}

// The gestures are read off the focused view, so they follow the stack rather
// than the root: a pushed pane with its own g is the one being typed into.
func TestDestinations_TakeTheGesturesFromWhicheverViewHasTheKeyboard(t *testing.T) {
	m := latched(t, testDeps(), 120, 38, 3, destBoard())
	m, _ = press(m, "esc")

	pushed := &actingView{set: KeySet{
		Acts: []Binding{Bind([]string{"enter"}, "enter", "open")},
		Full: [][]Binding{{Bind([]string{"end"}, "g e", "the last comment")}},
	}}
	next, _ := m.Update(PushMsg{View: pushed, ID: "thread", Title: "Comments"})
	m, _ = press(next.(Model), "g")

	frame := ansi.Strip(m.Frame())
	if !strings.Contains(frame, "the last comment") {
		t.Errorf("the overlay names the root's gestures rather than the focused view's:\n%s", frame)
	}
	if strings.Contains(frame, "first row") {
		t.Errorf("the overlay still names the gesture of the view underneath:\n%s", frame)
	}
}

func TestDestinations_EnterGoesToTheRowUnderTheCursor(t *testing.T) {
	m := latched(t, testDeps(), 120, 38, 3, destBoard())

	m, _ = press(m, "j", "enter")
	if m.prefixSet {
		t.Error("the gesture is still latched after it was spent")
	}
	if got := ansi.Strip(m.Frame()); !strings.Contains(got, "board body") {
		t.Errorf("enter did not switch to the row under the cursor:\n%s", got)
	}
}

// The digit is unchanged, which is the whole point of making the wait visible
// rather than making it a step: g2 typed fast behaves as it always did.
func TestDestinations_ADigitStillSwitchesStraightAway(t *testing.T) {
	m := latched(t, testDeps(), 120, 38, 3, destBoard())

	m, _ = press(m, "3")
	if m.prefixSet {
		t.Error("a digit left the gesture latched")
	}
	if got := ansi.Strip(m.Frame()); !strings.Contains(got, "backlog body") {
		t.Errorf("g 3 did not open the third slot:\n%s", got)
	}
}

func TestDestinations_EscThrowsTheGestureAwayAndDrawsNothing(t *testing.T) {
	view := destBoard()
	m := latched(t, testDeps(), 120, 38, 3, view)

	m, _ = press(m, "esc")
	if m.prefixSet {
		t.Error("esc left the gesture latched")
	}
	frame := ansi.Strip(m.Frame())
	if strings.Contains(frame, m.destTitle()) {
		t.Errorf("the overlay is still drawn after esc:\n%s", frame)
	}
	if !strings.Contains(frame, "board body") {
		t.Errorf("the view did not come back:\n%s", frame)
	}
	if len(view.keys) != 0 {
		t.Errorf("cancelling the gesture sent the view keys: %v", view.keys)
	}
}

// The overlay is a hint over a pass-through and not a modal. Every key it does
// not answer resolves the way it did before it existed, which is what keeps the
// views' own two-stroke gestures working.
func TestDestinations_EveryOtherKeyStillReachesTheViewAsBothStrokes(t *testing.T) {
	for _, second := range []string{"g", "e", "r", "?"} {
		t.Run(second, func(t *testing.T) {
			view := &stubView{id: "issues"}
			destRegistry(t, view, 3)
			m := newAt(t, testDeps(), 120, 38)
			view.seen = nil

			m, _ = press(m, "g", second)
			if m.prefixSet {
				t.Errorf("%q left the gesture latched", second)
			}
			for _, want := range []string{"key:g", "key:" + second} {
				if !saw(view, want) {
					t.Errorf("the view was not handed %q: %v", want, view.seen)
				}
			}
			if !equalOrder(view.seen, []string{"key:g", "key:" + second}) {
				t.Errorf("the keys arrived as %v, want them in the order they were typed", view.seen)
			}
		})
	}
}

// docs/UX.md principle 2 over the one screen where the view's own keys are not
// the ones that work: the row says what this overlay answers to.
func TestDestinations_TheRowSaysWhatTheOverlayAnswersTo(t *testing.T) {
	m := latched(t, testDeps(), 120, 38, 3, destBoard())

	footer := lastLine(ansi.Strip(m.Frame()))
	for _, want := range []string{"1-9", "switch view", "up/down", "enter", "esc", "cancel"} {
		if !strings.Contains(footer, want) {
			t.Errorf("the row does not name %q while the overlay is up:\n%q", want, footer)
		}
	}
	if strings.Contains(footer, "open") {
		t.Errorf("the row still advertises the view's own keys while the gesture has them:\n%q", footer)
	}
}

// The third route to the same action, for somebody who has not learnt the
// prefix: the palette is where they are already looking.
func TestDestinations_ThePaletteEntryOpensTheSameOverlay(t *testing.T) {
	destRegistry(t, destBoard(), 3)
	registerDestinationCommand()
	m := newAt(t, testDeps(), 120, 38)

	next, cmd := m.Update(RunCommandMsg{ID: "views.switch"})
	m = deliver(t, next.(Model), cmd)
	if !m.prefixSet {
		t.Fatal("the palette entry did not latch the gesture")
	}
	if got := ansi.Strip(m.Frame()); !strings.Contains(got, m.destTitle()) {
		t.Errorf("the palette entry did not draw the destinations:\n%s", got)
	}
	// The stroke it latches is the keymap's own, so leaving the gesture hands the
	// view the key it would have pressed to start it.
	if got := m.prefix.String(); got != m.keys.Go.Keys()[0] {
		t.Errorf("the palette entry buffered %q rather than the prefix the keymap binds", got)
	}
}

func TestDestinations_AClickOnARowGoesThereAndAClickOffCancels(t *testing.T) {
	m := latched(t, testDeps(), 120, 38, 3, destBoard())
	_ = m.Frame()

	prefix := m.zonePrefix
	eventually(t, func() bool { return !m.deps.Zones.Get(prefix + destZone + "2").IsZero() })
	at := m.deps.Zones.Get(prefix + destZone + "2")

	next, _ := m.Update(tea.MouseClickMsg{X: at.StartX, Y: at.StartY, Button: tea.MouseLeft})
	clicked := next.(Model)
	if clicked.prefixSet {
		t.Error("a click on a row left the gesture latched")
	}
	if got := ansi.Strip(clicked.Frame()); !strings.Contains(got, "board body") {
		t.Errorf("clicking the second row did not open it:\n%s", got)
	}

	next, _ = m.Update(tea.MouseClickMsg{X: m.width - 1, Y: m.height - 4, Button: tea.MouseLeft})
	if off := next.(Model); off.prefixSet {
		t.Error("a click off the rows left the gesture latched")
	}
}

// docs/UX.md promises every entry on the row is clickable, and the row while this
// overlay is up is the overlay's own: a click there arrives as the key it names,
// which is how the key, the palette and the pointer stay one implementation.
func TestDestinations_TheRowStaysClickableWhileItIsUp(t *testing.T) {
	m := latched(t, testDeps(), 120, 38, 3, destBoard())
	_ = m.Frame()

	prefix, esc := m.zonePrefix, m.keys.Back.Help().Key
	eventually(t, func() bool { return !m.deps.Zones.Get(prefix + actZone + esc).IsZero() })
	at := m.deps.Zones.Get(prefix + actZone + esc)

	next, _ := m.Update(tea.MouseClickMsg{X: at.StartX, Y: at.StartY, Button: tea.MouseLeft})
	got := next.(Model)
	if got.prefixSet {
		t.Error("clicking the row's own cancel left the gesture latched")
	}
	if frame := ansi.Strip(got.Frame()); !strings.Contains(frame, "board body") {
		t.Errorf("the view did not come back:\n%s", frame)
	}
}

// A wheel or a drag reaching the view under the overlay scrolls something nobody
// can see, which is the rule the help overlay and the menu already follow.
func TestDestinations_NoWheelOrDragReachesTheViewWhileItIsUp(t *testing.T) {
	for name, msg := range map[string]tea.Msg{
		"wheel":   tea.MouseWheelMsg{Button: tea.MouseWheelDown, X: 4, Y: 4},
		"motion":  tea.MouseMotionMsg{Button: tea.MouseLeft, X: 5, Y: 5},
		"release": tea.MouseReleaseMsg{Button: tea.MouseLeft, X: 4, Y: 4},
	} {
		t.Run(name, func(t *testing.T) {
			view := &stubView{id: "issues"}
			destRegistry(t, view, 3)
			m := newAt(t, testDeps(), 120, 38, WithMouse(true))
			m, _ = press(m, "g")
			view.seen = nil

			next, _ := m.Update(msg)
			if got := next.(Model); !got.prefixSet {
				t.Errorf("a %s threw the gesture away", name)
			}
			if len(view.seen) != 0 {
				t.Errorf("a %s reached the view behind the overlay: %v", name, view.seen)
			}
		})
	}
}

// A right-click while the gesture is latched would put the menu over the
// overlay, and the menu spends the arrows and enter that this one is holding.
func TestDestinations_ARightClickDoesNotOpenTheMenuOverIt(t *testing.T) {
	m := latched(t, testDeps(), 120, 38, 3, destBoard())

	next, _ := m.Update(rightClick(10, 5))
	got := next.(Model)
	if got.menu.open {
		t.Error("the menu opened over the destination overlay")
	}
	if !got.prefixSet {
		t.Error("a right-click threw the gesture away")
	}
}

// The nine slots are what may not be given up, so the block that folds when the
// terminal is short is the one naming the view's own gestures.
func TestDestinations_KeepEveryDestinationWhenTheGesturesWillNotFit(t *testing.T) {
	crowded := &actingView{set: KeySet{
		Acts: []Binding{Bind([]string{"enter"}, "enter", "open")},
		Full: [][]Binding{{
			Bind([]string{"home"}, "g g", "first row"),
			Bind([]string{"end"}, "g e", "last row"),
			Bind([]string{"a"}, "g a", "the row you came from"),
			Bind([]string{"b"}, "g b", "the row after this one"),
			Bind([]string{"c"}, "g c", "the row before this one"),
			Bind([]string{"d"}, "g d", "the row in the middle"),
		}},
	}}
	m := latched(t, testDeps(), 80, 20, 9, crowded)

	frame := ansi.Strip(m.Frame())
	for _, title := range slotTitles {
		if !strings.Contains(frame, title) {
			t.Errorf("a destination was dropped to make room for a gesture: %q is missing\n%s", title, frame)
		}
	}
	if !strings.Contains(frame, "more") {
		t.Errorf("the gestures that did not fit went away without saying so:\n%s", frame)
	}
	if rows := strings.Count(frame, "\n") + 1; rows != 20 {
		t.Errorf("the frame is %d rows, so the overlay pushed the footer off the screen:\n%s", rows, frame)
	}
}

// The box is cut rather than wrapped, for the reason the menu's rows are: a
// wrapped chrome row pushes the footer off the bottom of the screen.
func TestDestinations_FitsTheBoxItIsGiven(t *testing.T) {
	long := "Boards need the Jira Software project this token cannot see, at some length, " +
		"and this is the sentence the site itself came back with"
	for width := MinWidth; width <= 140; width += 6 {
		for _, height := range []int{MinHeight, 24, 40} {
			d := testDeps()
			d.Caps.Boards = jira.Capability{Reason: long}

			resetRegistry()
			RegisterView(spec("issues", 1, "", destBoard()))
			for i := 1; i < len(slotTitles); i++ {
				id := strings.ToLower(slotTitles[i])
				RegisterView(ViewSpec{ID: id, Title: slotTitles[i], Slot: i + 1,
					Requires: jira.CapBoards, New: func(Deps) View { return &stubView{id: id} }})
			}
			m := newAt(t, d, width, height)
			m, _ = press(m, "g")

			frame := ansi.Strip(m.Frame())
			lines := strings.Split(frame, "\n")
			if len(lines) != height {
				t.Fatalf("at %dx%d the overlay made a frame of %d rows:\n%s", width, height, len(lines), frame)
			}
			for i, line := range lines {
				if got := ansi.StringWidth(line); got > width {
					t.Fatalf("at %dx%d row %d of the overlay is %d wide:\n%s", width, height, i, got, frame)
				}
			}
			resetRegistry()
		}
	}
}

// A build where no view sits on a digit still has to answer the gesture, because
// the views' own g strokes go through it: it says there is nowhere to go rather
// than drawing an empty box.
func TestDestinations_SayWhenNoViewSitsOnADigit(t *testing.T) {
	resetRegistry()
	t.Cleanup(resetRegistry)
	RegisterView(spec("form", 0, "", &stubView{id: "form"}))

	m := newAt(t, testDeps(), 120, 38)
	m, _ = press(m, "g")
	if got := ansi.Strip(m.Frame()); !strings.Contains(got, destNone) {
		t.Errorf("a build with no slots drew a box with nothing in it:\n%s", got)
	}
}

func TestDestinations_Golden(t *testing.T) {
	for _, size := range [][2]int{{120, 38}, {80, 20}} {
		t.Run(strconv.Itoa(size[0])+"x"+strconv.Itoa(size[1]), func(t *testing.T) {
			d := testDeps()
			d.Caps.Plans = jira.Capability{Reason: "Plans need Administer Jira, which this token does not have"}

			resetRegistry()
			t.Cleanup(resetRegistry)
			RegisterView(spec("issues", 1, "", destBoard()))
			RegisterView(spec("board", 2, "", &stubView{id: "board"}))
			RegisterView(spec("backlog", 3, "", &stubView{id: "backlog"}))
			RegisterView(ViewSpec{ID: "plans", Title: "Plans", Slot: 7, Requires: jira.CapPlans,
				New: func(Deps) View { return &stubView{id: "plans"} }})

			m := newAt(t, d, size[0], size[1])
			m, _ = press(m, "g")
			golden(t, "destinations_"+strconv.Itoa(size[0])+"x"+strconv.Itoa(size[1])+".golden",
				ansi.Strip(m.Frame()))
		})
	}
}

// BenchmarkFrameWithTheDestinationsUp is the frame drawn while the gesture is
// waiting. It is not a steady state — nothing repaints it until the next key —
// but it is a frame, and a ceiling on it is what says so out loud.
func BenchmarkFrameWithTheDestinationsUp(b *testing.B) {
	resetRegistry()
	b.Cleanup(resetRegistry)
	for i := range slotTitles {
		id, slot := strings.ToLower(slotTitles[i]), i+1
		RegisterView(ViewSpec{ID: id, Title: slotTitles[i], Slot: slot,
			New: func(Deps) View { return &stubView{id: id, content: strings.Repeat("row\n", 40)} }})
	}
	RegisterKeys("issues", KeySet{Full: [][]Binding{{Bind([]string{"home"}, "g g", "first row")}}})

	m, err := New(testDeps(), WithSize(200, 60), WithMouse(false))
	if err != nil {
		b.Fatal(err)
	}
	next, _ := m.Update(tea.WindowSizeMsg{Width: 200, Height: 60})
	next, _ = next.(Model).Update(keyPress("g"))
	m = next.(Model)

	b.ReportAllocs()
	b.ResetTimer()
	for range b.N {
		_ = m.Frame()
	}
}
