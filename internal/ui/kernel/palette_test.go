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
	board := &stubView{id: "board"}
	RegisterView(spec("board", 1, "", board))
	RegisterView(spec(PaletteViewID, 0, "", &stubView{id: PaletteViewID}))

	m := newAt(t, testDeps(), 120, 30)
	m, _ = press(m, "ctrl+k")
	board.seen = nil

	if _, _ = m.Update(BroadcastMsg{Msg: RefreshMsg{Purge: true}}); !saw(board, "refresh:purge") {
		t.Errorf("a broadcast sent from the palette never reached the view it was for: %v", board.seen)
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
