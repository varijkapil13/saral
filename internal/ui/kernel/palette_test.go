package kernel

import (
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"
)

func TestPalette_CtrlKPushesOverTheViewYouWereInRatherThanReplacingIt(t *testing.T) {
	resetRegistry()
	t.Cleanup(resetRegistry)
	RegisterView(spec("board", 1, "", &stubView{id: "board"}))
	RegisterView(spec(PaletteViewID, 0, "", &stubView{id: PaletteViewID}))

	m := newAt(t, testDeps(), 120, 30)
	next, _ := m.Update(PushMsg{View: &stubView{id: "detail", content: "PROJ-1 detail"}, ID: "issue", Title: "PROJ-1"})
	m = next.(Model)

	m, _ = press(m, "ctrl+k")
	if got := ansi.Strip(m.Frame()); !strings.Contains(got, "palette body") {
		t.Fatalf("ctrl+k did not open the palette:\n%s", got)
	}
	if len(m.stack) != 3 {
		t.Errorf("the stack is %d deep after ctrl+k, want 3: opening the palette threw away what was under it", len(m.stack))
	}

	m, _ = press(m, "esc")
	if got := ansi.Strip(m.Frame()); !strings.Contains(got, "PROJ-1 detail") {
		t.Errorf("esc did not put the palette away and land back where it opened from:\n%s", got)
	}
}

func TestPalette_IsBuiltFreshOnEveryCtrlK(t *testing.T) {
	resetRegistry()
	t.Cleanup(resetRegistry)
	RegisterView(spec("board", 1, "", &stubView{id: "board"}))
	var scopes []string
	RegisterView(ViewSpec{ID: PaletteViewID, Title: "Commands", New: func(d Deps) View {
		scopes = append(scopes, d.Project)
		return &stubView{id: PaletteViewID}
	}})

	m := newAt(t, testDeps(), 120, 30)
	m, _ = press(m, "ctrl+k")
	m, _ = press(m, "esc")
	next, _ := m.Update(ProjectMsg{Project: "PROJ"})
	_, _ = press(next.(Model), "ctrl+k")

	if len(scopes) != 2 {
		t.Fatalf("the palette was built %d times for two ctrl+k, want 2", len(scopes))
	}
	if scopes[1] != "PROJ" {
		t.Errorf("the second palette was built against project %q, want PROJ: an instance kept from the first ctrl+k is frozen at whatever the session was then", scopes[1])
	}
}

func TestPalette_OpensOverAViewHoldingSomethingUnsaved(t *testing.T) {
	resetRegistry()
	t.Cleanup(resetRegistry)
	RegisterView(spec("board", 1, "", &stubView{id: "board"}))
	RegisterView(spec(PaletteViewID, 0, "", &stubView{id: PaletteViewID}))

	m := newAt(t, testDeps(), 120, 30)
	next, _ := m.Update(PushMsg{
		View:  &stubView{id: "editor", content: "editor body", blocks: "PROJ-1 has unsaved changes"},
		ID:    "issue.edit",
		Title: "PROJ-1",
	})
	m = next.(Model)

	m, _ = press(m, "ctrl+k")
	got := ansi.Strip(m.Frame())
	if !strings.Contains(got, "palette body") {
		t.Errorf("a draft refused the palette, which discards nothing:\n%s", got)
	}
	if strings.Contains(got, "unsaved changes") {
		t.Errorf("ctrl+k answered with the draft's refusal instead of opening:\n%s", got)
	}
}

func TestPalette_ACommandThatBroadcastsStillReachesTheViewUnderneath(t *testing.T) {
	resetRegistry()
	t.Cleanup(resetRegistry)
	RegisterView(spec("board", 1, "", &stubView{id: "board"}))
	RegisterView(spec(PaletteViewID, 0, "", &stubView{id: PaletteViewID}))

	// The view underneath is a pushed one, not the root. A root the palette
	// replaced is still live and still hears a broadcast, so it says nothing
	// about whether the stack survived.
	detail := &stubView{id: "detail", content: "PROJ-1 detail"}
	m := newAt(t, testDeps(), 120, 30)
	next, _ := m.Update(PushMsg{View: detail, ID: "issue", Title: "PROJ-1"})
	m = next.(Model)

	m, _ = press(m, "ctrl+k")
	detail.seen = nil

	if _, _ = m.Update(BroadcastMsg{Msg: RefreshMsg{Purge: true}}); !saw(detail, "refresh:purge") {
		t.Errorf("a broadcast sent from the palette never reached the view it was for: %v", detail.seen)
	}
}

func TestPalette_CtrlKTwiceLeavesOnePaletteAndOneEscape(t *testing.T) {
	resetRegistry()
	t.Cleanup(resetRegistry)
	RegisterView(spec("board", 1, "", &stubView{id: "board"}))
	RegisterView(spec(PaletteViewID, 0, "", &stubView{id: PaletteViewID}))

	m := newAt(t, testDeps(), 120, 30)
	m, _ = press(m, "ctrl+k", "ctrl+k", "ctrl+k")
	if len(m.stack) != 2 {
		t.Fatalf("three ctrl+k left a stack %d deep, want 2: each one stacked another palette to escape", len(m.stack))
	}

	m, _ = press(m, "esc")
	if got := ansi.Strip(m.Frame()); !strings.Contains(got, "board body") {
		t.Errorf("one esc did not put the palette away:\n%s", got)
	}
}

func TestPalette_OpensFromAViewThatIsTakingTyping(t *testing.T) {
	resetRegistry()
	t.Cleanup(resetRegistry)
	typing := &stubView{id: "board", capturing: true}
	RegisterView(spec("board", 1, "", typing))
	RegisterView(spec(PaletteViewID, 0, "", &stubView{id: PaletteViewID}))

	m := newAt(t, testDeps(), 120, 30)
	typing.seen = nil
	m, _ = press(m, "ctrl+k")

	if got := ansi.Strip(m.Frame()); !strings.Contains(got, "palette body") {
		t.Errorf("ctrl+k did nothing from a filter, a form field or an editor, which is most of the program:\n%s", got)
	}
	if saw(typing, "key:ctrl+k") {
		t.Errorf("the view swallowed the palette key: %v", typing.seen)
	}
}

func TestPalette_ADraftUnderItStillRefusesAViewSwitch(t *testing.T) {
	resetRegistry()
	t.Cleanup(resetRegistry)
	RegisterView(spec("board", 1, "", &stubView{id: "board"}))
	RegisterView(spec("backlog", 2, "", &stubView{id: "backlog"}))
	RegisterView(spec(PaletteViewID, 0, "", &stubView{id: PaletteViewID}))

	m := newAt(t, testDeps(), 120, 30)
	next, _ := m.Update(PushMsg{
		View:  &stubView{id: "editor", content: "editor body", blocks: "PROJ-1 has unsaved changes"},
		ID:    "issue.edit",
		Title: "PROJ-1",
	})
	m = next.(Model)
	m, _ = press(m, "ctrl+k")

	after, _ := m.Update(OpenMsg{ID: "backlog"})
	frame := ansi.Strip(after.(Model).Frame())
	if !strings.Contains(frame, "unsaved changes") {
		t.Errorf("a switch away threw the draft out without a word, because it asked the palette on top of it:\n%s", frame)
	}
	if strings.Contains(frame, "backlog body") {
		t.Errorf("the switch happened anyway:\n%s", frame)
	}
}

// The footer names one destination and spends the rest of its row on actions, so
// the gesture is now taught by the overlay and by the palette row carrying it.
// What still has to hold is that the gesture works and reaches the view whose
// slot it was built from — a command teaching g3 must not open something else.
func TestSlotGesture_ReachesTheViewWhoseSlotItWasBuiltFrom(t *testing.T) {
	resetRegistry()
	t.Cleanup(resetRegistry)
	RegisterView(spec("board", 3, "", &stubView{id: "board"}))
	RegisterView(spec("backlog", 4, "", &stubView{id: "backlog"}))

	gesture := SlotGesture(3)
	if gesture != "g3" {
		t.Fatalf("SlotGesture(3) is %q; the keymap the kernel runs spells it g3", gesture)
	}

	m := newAt(t, testDeps(), 120, 30)
	m, _ = press(m, "g", "4")
	if got := ansi.Strip(m.Frame()); !strings.Contains(got, "backlog body") {
		t.Fatalf("g4 did not reach slot 4:\n%s", got)
	}
	m, _ = press(m, gesture[:1], gesture[1:])
	if got := ansi.Strip(m.Frame()); !strings.Contains(got, "board body") {
		t.Errorf("%q does not reach the view holding slot 3:\n%s", gesture, got)
	}
	m, _ = press(m, "?")
	if got := ansi.Strip(m.Frame()); !strings.Contains(got, "switch view") {
		t.Errorf("the overlay does not teach the digits the footer stopped spelling out:\n%s", got)
	}
}

func TestCommand_CarriesTheKeyItsRegistrarNamedAndNeverGuessesOne(t *testing.T) {
	resetRegistry()
	t.Cleanup(resetRegistry)
	run := func(Deps) tea.Cmd { return nil }
	// issue.edit is both a command ID and a view ID, and the view's keys are the
	// editor pane's. Anything deriving a key from an ID hands those out here.
	RegisterCommand(Command{ID: "issue.edit", Title: "Edit this issue", Group: "Issue", Keys: []string{"e"}, Run: run})
	RegisterCommand(Command{ID: "issue.create", Title: "Create an issue", Group: "Issue", Run: run})
	RegisterKeys("issue.edit", KeySet{Short: []Binding{Bind([]string{"ctrl+s"}, "ctrl+s", "save")}})

	keys := make(map[string][]string, 2)
	for _, cmd := range Commands() {
		keys[cmd.ID] = cmd.Keys
	}
	if got := keys["issue.edit"]; len(got) != 1 || got[0] != "e" {
		t.Errorf("issue.edit carries %v, want [e]", got)
	}
	if got := keys["issue.create"]; len(got) != 0 {
		t.Errorf("a command nothing binds carries %v; an empty set is the palette showing no key at all", got)
	}
}

func TestFooter_StillOffersThePaletteToAViewThatIsTakingTyping(t *testing.T) {
	resetRegistry()
	t.Cleanup(resetRegistry)
	typing := &stubView{id: "board", capturing: true}
	RegisterView(spec("board", 1, "", typing))
	RegisterView(spec(PaletteViewID, 0, "", &stubView{id: PaletteViewID}))
	RegisterKeys("board", KeySet{Short: []Binding{Bind([]string{"ctrl+g"}, "ctrl+g", "clear filter")}})

	m := newAt(t, testDeps(), 140, 30)
	got := lastLine(ansi.Strip(m.Frame()))
	if !strings.HasSuffix(got, "ctrl+k") {
		t.Errorf("the one global that still works while typing is not offered, so nobody finds it there:\n%s", got)
	}
	if strings.Contains(got, "? ctrl+k") || strings.HasSuffix(got, "q") {
		t.Errorf("the footer advertises globals the view is swallowing:\n%s", got)
	}
}
