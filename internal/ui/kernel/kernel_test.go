package kernel

import (
	"flag"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/varijkapil13/saral/pkg/jira"
)

var update = flag.Bool("update", false, "rewrite the golden files")

func fullCaps() jira.Capabilities {
	ok := jira.Capability{OK: true}
	return jira.Capabilities{Plans: ok, BulkMove: ok, Boards: ok, Attachments: ok, DeleteIssues: ok}
}

func testDeps() Deps {
	return Deps{
		Caps:  fullCaps(),
		Theme: NewTheme(ThemeNoColor, true, ASCIIGlyphs()),
		Site:  "example.atlassian.net",
		Now:   func() time.Time { return time.Date(2026, 8, 20, 9, 0, 0, 0, time.UTC) },
	}
}

func newAt(t *testing.T, d Deps, w, h int, opts ...Option) Model {
	t.Helper()
	m, err := New(d, append([]Option{WithSize(w, h)}, opts...)...)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	next, _ := m.Update(tea.WindowSizeMsg{Width: w, Height: h})
	return next.(Model)
}

// press sends one key, or a gesture spelt out one key at a time.
func press(m Model, keys ...string) (Model, tea.Cmd) {
	var cmd tea.Cmd
	for _, k := range keys {
		var next tea.Model
		next, cmd = m.Update(keyPress(k))
		m = next.(Model)
	}
	return m, cmd
}

func keyPress(s string) tea.KeyPressMsg {
	switch s {
	case "esc":
		return tea.KeyPressMsg{Code: tea.KeyEsc}
	case "ctrl+c":
		return tea.KeyPressMsg{Code: 'c', Mod: tea.ModCtrl}
	case "ctrl+k":
		return tea.KeyPressMsg{Code: 'k', Mod: tea.ModCtrl}
	default:
		r, _ := utf8.DecodeRuneInString(s)
		return tea.KeyPressMsg{Code: r, Text: s}
	}
}

func TestFrame_SaysSoWhenTheTerminalIsTooSmall(t *testing.T) {
	resetRegistry()
	t.Cleanup(resetRegistry)
	RegisterView(spec("board", 1, "", &stubView{id: "board"}))

	m := newAt(t, testDeps(), 60, 12)
	got := ansi.Strip(m.Frame())
	if !strings.Contains(got, "at least 80×20") || !strings.Contains(got, "60×12") {
		t.Errorf("unhelpful small-terminal frame:\n%s", got)
	}
}

func TestFrame_ExplainsItselfWhenNoViewIsRegistered(t *testing.T) {
	resetRegistry()
	t.Cleanup(resetRegistry)

	m := newAt(t, testDeps(), 100, 24)
	if got := ansi.Strip(m.Frame()); !strings.Contains(got, "No views are registered") {
		t.Errorf("frame does not explain the empty build:\n%s", got)
	}
}

func TestFooter_HidesAViewWhoseCapabilityIsAbsentAndExplainsItOnItsKey(t *testing.T) {
	resetRegistry()
	t.Cleanup(resetRegistry)
	RegisterView(spec("board", 1, "", &stubView{id: "board"}))
	RegisterView(spec("plans", 2, jira.CapPlans, &stubView{id: "plans"}))

	d := testDeps()
	d.Caps.Plans = jira.Capability{Reason: "Plans need Administer Jira, which this token does not have"}
	m := newAt(t, d, 120, 30)

	frame := ansi.Strip(m.Frame())
	if strings.Contains(frame, "Plans") {
		t.Errorf("an unavailable view is in the footer:\n%s", frame)
	}
	m, _ = press(m, "g", "2")
	if got := ansi.Strip(m.Frame()); !strings.Contains(got, "Administer Jira") {
		t.Errorf("pressing an unavailable view's key did not show the reason:\n%s", got)
	}
}

func TestKeys_GoThenADigitSwitchesRootView(t *testing.T) {
	resetRegistry()
	t.Cleanup(resetRegistry)
	RegisterView(spec("board", 1, "", &stubView{id: "board"}))
	RegisterView(spec("backlog", 2, "", &stubView{id: "backlog"}))

	m := newAt(t, testDeps(), 120, 30)
	if got := ansi.Strip(m.Frame()); !strings.Contains(got, "board body") {
		t.Fatalf("did not start on the first slot:\n%s", got)
	}
	m, _ = press(m, "g", "2")
	if got := ansi.Strip(m.Frame()); !strings.Contains(got, "backlog body") {
		t.Errorf("g 2 did not switch view:\n%s", got)
	}
}

func TestKeys_UnboundSlotSaysSoRatherThanDoingNothing(t *testing.T) {
	resetRegistry()
	t.Cleanup(resetRegistry)
	RegisterView(spec("board", 1, "", &stubView{id: "board"}))

	m := newAt(t, testDeps(), 120, 30)
	m, _ = press(m, "g", "7")
	if got := ansi.Strip(m.Frame()); !strings.Contains(got, "nothing is bound to 7") {
		t.Errorf("silent no-op on an unbound slot:\n%s", got)
	}
}

func TestKeys_CtrlKSaysSoWhenThePaletteIsNotInTheBuild(t *testing.T) {
	resetRegistry()
	t.Cleanup(resetRegistry)
	RegisterView(spec("board", 1, "", &stubView{id: "board"}))

	m := newAt(t, testDeps(), 120, 30)
	m, _ = press(m, "ctrl+k")
	if got := ansi.Strip(m.Frame()); !strings.Contains(got, "palette is not available") {
		t.Errorf("ctrl+k was a silent no-op:\n%s", got)
	}
}

func TestStack_EscPopsAndQuitOnlyLeavesFromARoot(t *testing.T) {
	resetRegistry()
	t.Cleanup(resetRegistry)
	RegisterView(spec("board", 1, "", &stubView{id: "board"}))

	m := newAt(t, testDeps(), 120, 30)
	next, _ := m.Update(PushMsg{View: &stubView{id: "detail"}, ID: "issue", Title: "PROJ-1"})
	m = next.(Model)
	if got := ansi.Strip(m.Frame()); !strings.Contains(got, "detail body") {
		t.Fatalf("push did not take effect:\n%s", got)
	}

	m, cmd := press(m, "q")
	if cmd != nil && isQuit(cmd) {
		t.Error("q quit from a pushed view")
	}
	if got := ansi.Strip(m.Frame()); !strings.Contains(got, "board body") {
		t.Errorf("q did not pop back to the root:\n%s", got)
	}

	_, cmd = press(m, "q")
	if !isQuit(cmd) {
		t.Error("q at the root did not quit")
	}
}

func TestStack_CtrlCAlwaysQuits(t *testing.T) {
	resetRegistry()
	t.Cleanup(resetRegistry)
	RegisterView(spec("board", 1, "", &stubView{id: "board"}))

	m := newAt(t, testDeps(), 120, 30)
	next, _ := m.Update(PushMsg{View: &stubView{id: "detail"}})
	_, cmd := press(next.(Model), "ctrl+c")
	if !isQuit(cmd) {
		t.Error("ctrl+c did not quit from a pushed view")
	}
}

func TestQuit_IsRefusedWhileAViewIsHoldingSomething(t *testing.T) {
	resetRegistry()
	t.Cleanup(resetRegistry)
	RegisterView(spec("board", 1, "", &stubView{id: "board", blocks: "the comment you are writing is unsaved"}))

	m := newAt(t, testDeps(), 120, 30)
	m, cmd := press(m, "q")
	if isQuit(cmd) {
		t.Fatal("quit was not refused")
	}
	if got := ansi.Strip(m.Frame()); !strings.Contains(got, "unsaved") {
		t.Errorf("the reason was not shown:\n%s", got)
	}
}

func TestHelp_TogglesAndListsTheFocusedViewsKeysAndTheGlobalOnes(t *testing.T) {
	resetRegistry()
	t.Cleanup(resetRegistry)
	RegisterView(spec("board", 1, "", &stubView{id: "board"}))
	RegisterKeys("board", KeySet{
		Short: []Binding{Bind([]string{"m"}, "m", "move issue")},
		Full:  [][]Binding{{Bind([]string{"m"}, "m", "move issue")}},
	})

	m := newAt(t, testDeps(), 120, 30)
	m, _ = press(m, "?")
	help := ansi.Strip(m.Frame())
	if !strings.Contains(help, "move issue") || !strings.Contains(help, "quit") {
		t.Errorf("help overlay is missing keys:\n%s", help)
	}
	m, _ = press(m, "?")
	if got := ansi.Strip(m.Frame()); strings.Contains(got, "refetch everything") {
		t.Errorf("help overlay did not close:\n%s", got)
	}
}

func TestFooter_ShowsOnlyTheFocusedViewsHints(t *testing.T) {
	resetRegistry()
	t.Cleanup(resetRegistry)
	RegisterView(spec("board", 1, "", &stubView{id: "board"}))
	RegisterView(spec("backlog", 2, "", &stubView{id: "backlog"}))
	RegisterKeys("board", KeySet{Short: []Binding{Bind([]string{"m"}, "m", "move issue")}})
	RegisterKeys("backlog", KeySet{Short: []Binding{Bind([]string{"s"}, "s", "sprint")}})

	m := newAt(t, testDeps(), 140, 30)
	if got := ansi.Strip(m.Frame()); !strings.Contains(got, "move issue") || strings.Contains(got, "sprint") {
		t.Errorf("footer shows the wrong view's keys:\n%s", got)
	}
	m, _ = press(m, "g", "2")
	if got := ansi.Strip(m.Frame()); !strings.Contains(got, "sprint") || strings.Contains(got, "move issue") {
		t.Errorf("footer did not follow the focus:\n%s", got)
	}
}

func TestSize_GivesTheViewTheBoxLeftAfterTheChrome(t *testing.T) {
	resetRegistry()
	t.Cleanup(resetRegistry)
	view := &stubView{id: "board"}
	RegisterView(spec("board", 1, "", view))

	m := newAt(t, testDeps(), 120, 40)
	if view.width != 120 || view.height != 38 {
		t.Errorf("view got %dx%d, want 120x38 (header and footer removed)", view.width, view.height)
	}

	next, _ := m.Update(StatusMsg{Text: "rate limited, retrying in 30s", Level: LevelWarn})
	m = next.(Model)
	next, _ = m.Update(tea.WindowSizeMsg{Width: 120, Height: 40})
	m = next.(Model)
	if view.height != 37 {
		t.Errorf("a status line did not take a row from the view: got %d, want 37", view.height)
	}
	if got := ansi.Strip(m.Frame()); !strings.Contains(got, "rate limited") {
		t.Errorf("status line not rendered:\n%s", got)
	}
}

func TestBroadcast_ReachesEveryViewOnTheStack(t *testing.T) {
	resetRegistry()
	t.Cleanup(resetRegistry)
	root := &stubView{id: "board"}
	RegisterView(spec("board", 1, "", root))

	m := newAt(t, testDeps(), 120, 30)
	pushed := &stubView{id: "detail"}
	next, _ := m.Update(PushMsg{View: pushed, ID: "issue"})
	m = next.(Model)

	if _, _ = m.Update(BroadcastMsg{Msg: RefreshMsg{Purge: true}}); !saw(root, "refresh:purge") || !saw(pushed, "refresh:purge") {
		t.Errorf("broadcast did not reach both views: root=%v pushed=%v", root.seen, pushed.seen)
	}
}

func TestRefreshKeys_GoOnlyToTheFocusedView(t *testing.T) {
	resetRegistry()
	t.Cleanup(resetRegistry)
	root := &stubView{id: "board"}
	RegisterView(spec("board", 1, "", root))

	m := newAt(t, testDeps(), 120, 30)
	pushed := &stubView{id: "detail"}
	next, _ := m.Update(PushMsg{View: pushed, ID: "issue"})
	m = next.(Model)
	root.seen, pushed.seen = nil, nil

	if _, _ = press(m, "r"); saw(root, "refresh") || !saw(pushed, "refresh") {
		t.Errorf("r reached the wrong views: root=%v pushed=%v", root.seen, pushed.seen)
	}
}

func TestTheme_OnlyFollowsTheTerminalWhenTheModeIsAuto(t *testing.T) {
	resetRegistry()
	t.Cleanup(resetRegistry)
	view := &stubView{id: "board"}
	RegisterView(spec("board", 1, "", view))

	d := testDeps()
	d.Theme = NewTheme(ThemeAuto, false, UnicodeGlyphs())
	m := newAt(t, d, 120, 30)
	next, _ := m.Update(tea.BackgroundColorMsg{Color: black()})
	m = next.(Model)
	if !m.deps.Theme.Dark {
		t.Error("auto theme did not follow a dark terminal")
	}
	if !saw(view, "theme") {
		t.Error("views were not told the theme changed")
	}

	d = testDeps()
	d.Theme = NewTheme(ThemeLight, false, UnicodeGlyphs())
	m = newAt(t, d, 120, 30)
	next, _ = m.Update(tea.BackgroundColorMsg{Color: black()})
	if next.(Model).deps.Theme.Dark {
		t.Error("an explicitly light theme followed the terminal anyway")
	}
}

// The footer names one destination — the root the session is in — so a probe that
// grants a capability is held to the thing that actually changed: the view can be
// reached, where before the gesture that reaches it said why it could not.
func TestCapabilities_ARefreshedProbeMakesItsViewReachable(t *testing.T) {
	resetRegistry()
	t.Cleanup(resetRegistry)
	RegisterView(spec("board", 1, "", &stubView{id: "board"}))
	RegisterView(spec("plans", 2, jira.CapPlans, &stubView{id: "plans"}))

	d := testDeps()
	d.Caps.Plans = jira.Capability{Reason: "no"}
	m := newAt(t, d, 140, 30)
	m, _ = press(m, "g", "2")
	if got := ansi.Strip(m.Frame()); strings.Contains(got, "plans body") {
		t.Fatalf("Plans opened without the capability:\n%s", got)
	}

	next, _ := m.Update(CapabilitiesMsg{Caps: fullCaps()})
	m, _ = press(next.(Model), "g", "2")
	if got := ansi.Strip(m.Frame()); !strings.Contains(got, "plans body") {
		t.Errorf("a granted capability did not bring its view back:\n%s", got)
	}
}

// The root cell is where esc lands, and clicking it is the same gesture: the
// footer says which root a pushed view came from, so pointing at it goes back
// there rather than nowhere.
func TestMouse_ClickingTheRootCellGoesBackToTheRoot(t *testing.T) {
	resetRegistry()
	t.Cleanup(resetRegistry)
	RegisterView(spec("board", 1, "", &stubView{id: "board"}))

	m := newAt(t, testDeps(), 120, 30, WithMouse(true))
	next, _ := m.Update(PushMsg{ID: "detail", Title: "PROJ-1", View: &stubView{id: "detail"}})
	m = next.(Model)
	if got := ansi.Strip(m.Frame()); !strings.Contains(got, "detail body") {
		t.Fatalf("the pushed view is not on top:\n%s", got)
	}
	if got := lastLine(ansi.Strip(m.Frame())); !strings.Contains(got, "Board") {
		t.Fatalf("the footer does not name the root a click would go back to:\n%s", got)
	}

	prefix := m.zonePrefix
	eventually(t, func() bool { return !m.deps.Zones.Get(prefix + rootZone).IsZero() })

	info := m.deps.Zones.Get(prefix + rootZone)
	click := tea.MouseClickMsg{X: info.StartX + 1, Y: info.StartY, Button: tea.MouseLeft}
	next, _ = m.Update(click)
	if got := ansi.Strip(next.(Model).Frame()); !strings.Contains(got, "board body") {
		t.Errorf("clicking the root cell did not come back to the root:\n%s", got)
	}
}

func TestFirstPaint_RendersAFrameAndReportsHowLongItTook(t *testing.T) {
	resetRegistry()
	t.Cleanup(resetRegistry)
	RegisterView(spec("board", 1, "", &stubView{id: "board"}))

	took, frame, err := FirstPaint(testDeps(), 120, 40)
	if err != nil {
		t.Fatalf("FirstPaint: %v", err)
	}
	if took <= 0 {
		t.Error("no elapsed time reported")
	}
	if !strings.Contains(ansi.Strip(frame), "board body") {
		t.Errorf("first paint did not draw the view:\n%s", frame)
	}
}

func TestFrame_Golden(t *testing.T) {
	for name, tc := range map[string]struct{ w, h int }{
		"120x40": {120, 40},
		"80x20":  {80, 20},
	} {
		t.Run(name, func(t *testing.T) {
			resetRegistry()
			t.Cleanup(resetRegistry)
			RegisterView(spec("board", 1, "", &stubView{id: "board", content: "PROJ-1  Fix the thing\nPROJ-2  Fix the other thing"}))
			RegisterView(spec("backlog", 2, "", &stubView{id: "backlog"}))
			RegisterView(spec("plans", 3, jira.CapPlans, &stubView{id: "plans"}))
			RegisterKeys("board", KeySet{Short: []Binding{
				Bind([]string{"enter"}, "enter", "open"),
				Bind([]string{"/"}, "/", "filter"),
			}})

			d := testDeps()
			d.Caps.Plans = jira.Capability{Reason: "needs Administer Jira"}
			m := newAt(t, d, tc.w, tc.h)
			golden(t, "frame_"+name+".golden", ansi.Strip(m.Frame()))
		})
	}
}

func BenchmarkFrame(b *testing.B) {
	resetRegistry()
	b.Cleanup(resetRegistry)
	RegisterView(ViewSpec{ID: "board", Title: "Board", Slot: 1,
		New: func(Deps) View { return &stubView{id: "board", content: strings.Repeat("row\n", 40)} }})
	RegisterKeys("board", KeySet{Short: []Binding{Bind([]string{"enter"}, "enter", "open")}})

	m, err := New(testDeps(), WithSize(200, 60), WithMouse(false))
	if err != nil {
		b.Fatal(err)
	}
	next, _ := m.Update(tea.WindowSizeMsg{Width: 200, Height: 60})
	m = next.(Model)

	b.ReportAllocs()
	b.ResetTimer()
	for range b.N {
		_ = m.Frame()
	}
}

func BenchmarkKeyToFrame(b *testing.B) {
	resetRegistry()
	b.Cleanup(resetRegistry)
	RegisterView(ViewSpec{ID: "board", Title: "Board", Slot: 1,
		New: func(Deps) View { return &stubView{id: "board", content: strings.Repeat("row\n", 40)} }})

	m, err := New(testDeps(), WithSize(200, 60), WithMouse(false))
	if err != nil {
		b.Fatal(err)
	}
	next, _ := m.Update(tea.WindowSizeMsg{Width: 200, Height: 60})
	m = next.(Model)
	key := keyPress("j")

	b.ReportAllocs()
	b.ResetTimer()
	for range b.N {
		updated, _ := m.Update(key)
		_ = updated.(Model).Frame()
	}
}

func saw(v *stubView, name string) bool {
	for _, s := range v.seen {
		if s == name {
			return true
		}
	}
	return false
}

func isQuit(cmd tea.Cmd) bool {
	if cmd == nil {
		return false
	}
	switch msg := cmd().(type) {
	case tea.QuitMsg:
		return true
	case tea.BatchMsg:
		for _, c := range msg {
			if isQuit(c) {
				return true
			}
		}
	}
	return false
}

func eventually(t *testing.T, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for !cond() {
		if time.Now().After(deadline) {
			t.Fatal("condition never became true")
		}
		runtime.Gosched()
	}
}

func golden(t *testing.T, name, got string) {
	t.Helper()
	path := filepath.Join("testdata", name)
	if *update {
		if err := os.MkdirAll("testdata", 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(got), 0o644); err != nil {
			t.Fatal(err)
		}
		return
	}
	want, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("%v — run: go test ./internal/ui/kernel -update", err)
	}
	if string(want) != got {
		t.Errorf("frame differs from %s\n--- want ---\n%s\n--- got ---\n%s", path, want, got)
	}
}

func TestSwitchingRootViewsAndBackKeepsTheUsersPlace(t *testing.T) {
	resetRegistry()
	t.Cleanup(resetRegistry)
	built := 0
	board := &stubView{id: "board"}
	RegisterView(ViewSpec{ID: "board", Title: "Board", Slot: 1, New: func(Deps) View {
		built++
		return board
	}})
	RegisterView(spec("backlog", 2, "", &stubView{id: "backlog"}))

	m := newAt(t, testDeps(), 120, 30)
	board.content = "cursor is on row 42"
	m, _ = press(m, "g", "2")
	m, _ = press(m, "g", "1")

	if built != 1 {
		t.Errorf("the board was rebuilt %d times; switching away should not throw it away", built)
	}
	if got := ansi.Strip(m.Frame()); !strings.Contains(got, "row 42") {
		t.Errorf("coming back reset the view:\n%s", got)
	}
}

func TestOpen_WorksWhenNothingWasAvailableAtStartup(t *testing.T) {
	resetRegistry()
	t.Cleanup(resetRegistry)
	RegisterView(spec("plans", 1, jira.CapPlans, &stubView{id: "plans"}))

	d := testDeps()
	d.Caps = jira.Capabilities{Plans: jira.Capability{Reason: "needs Administer Jira"}}
	m := newAt(t, d, 120, 30)
	if len(m.stack) != 0 {
		t.Fatalf("expected an empty stack, got %d entries", len(m.stack))
	}

	next, _ := m.Update(CapabilitiesMsg{Caps: fullCaps()})
	m = next.(Model)
	m, _ = press(m, "g", "1")

	if got := ansi.Strip(m.Frame()); !strings.Contains(got, "plans body") {
		t.Errorf("opening a view that only just became available did not work:\n%s", got)
	}
}

func TestStatusLine_ResizesTheViewSoItsBottomRowIsNotClipped(t *testing.T) {
	resetRegistry()
	t.Cleanup(resetRegistry)
	view := &stubView{id: "board"}
	RegisterView(spec("board", 1, "", view))

	m := newAt(t, testDeps(), 120, 40)
	if view.height != 38 {
		t.Fatalf("view started at height %d, want 38", view.height)
	}

	next, _ := m.Update(StatusMsg{Text: "rate limited, retrying in 30s", Level: LevelWarn})
	m = next.(Model)
	if view.height != 37 {
		t.Errorf("the view was not told the status line took a row: height %d, want 37", view.height)
	}

	_, _ = m.Update(StatusMsg{})
	if view.height != 38 {
		t.Errorf("the view was not told the status line went away: height %d, want 38", view.height)
	}
}

func TestThemeChange_ReachesARootViewTheUserSwitchedAwayFrom(t *testing.T) {
	resetRegistry()
	t.Cleanup(resetRegistry)
	board := &stubView{id: "board"}
	RegisterView(spec("board", 1, "", board))
	RegisterView(spec("backlog", 2, "", &stubView{id: "backlog"}))

	d := testDeps()
	d.Theme = NewTheme(ThemeAuto, false, UnicodeGlyphs())
	m := newAt(t, d, 120, 30)
	m, _ = press(m, "g", "2")
	board.seen = nil

	next, _ := m.Update(tea.BackgroundColorMsg{Color: black()})
	m = next.(Model)
	if !saw(board, "theme") {
		t.Errorf("a parked root view was not told the theme changed: %v", board.seen)
	}

	if _, _ = m.Update(BroadcastMsg{Msg: RefreshMsg{Purge: true}}); !saw(board, "refresh:purge") {
		t.Errorf("a parked root view missed a broadcast: %v", board.seen)
	}
}

func TestHelpOverlay_FooterAdvertisesOnlyWhatStillWorks(t *testing.T) {
	resetRegistry()
	t.Cleanup(resetRegistry)
	RegisterView(spec("board", 1, "", &stubView{id: "board"}))
	RegisterKeys("board", KeySet{Short: []Binding{Bind([]string{"m"}, "m", "move issue")}})

	m := newAt(t, testDeps(), 140, 30)
	m, _ = press(m, "?")

	footer := lastLine(ansi.Strip(m.Frame()))
	if !strings.Contains(footer, "close help") {
		t.Errorf("the footer does not say how to close the overlay:\n%s", footer)
	}
	for _, gone := range []string{"move issue", "commands", "quit"} {
		if strings.Contains(footer, gone) {
			t.Errorf("the footer still advertises %q, which the overlay is swallowing:\n%s", gone, footer)
		}
	}
}

func TestFooter_DoesNotAdvertiseThePaletteWhenItIsNotInTheBuild(t *testing.T) {
	resetRegistry()
	t.Cleanup(resetRegistry)
	RegisterView(spec("board", 1, "", &stubView{id: "board"}))

	m := newAt(t, testDeps(), 140, 30)
	if got := lastLine(ansi.Strip(m.Frame())); strings.Contains(got, "ctrl+k") {
		t.Errorf("the palette is advertised but not registered:\n%s", got)
	}

	resetRegistry()
	RegisterView(spec("board", 1, "", &stubView{id: "board"}))
	RegisterView(spec(PaletteViewID, 0, "", &stubView{id: PaletteViewID}))
	m = newAt(t, testDeps(), 140, 30)
	if got := lastLine(ansi.Strip(m.Frame())); !strings.Contains(got, "ctrl+k") {
		t.Errorf("the palette is registered but not advertised:\n%s", got)
	}
}

func lastLine(s string) string {
	lines := strings.Split(strings.TrimRight(s, "\n"), "\n")
	return lines[len(lines)-1]
}

func TestFrame_IsNeverTallerThanTheTerminal(t *testing.T) {
	resetRegistry()
	t.Cleanup(resetRegistry)
	long := strings.Repeat("a very long view title ", 8)
	RegisterView(ViewSpec{ID: "board", Title: long, Slot: 1,
		New: func(Deps) View { return &stubView{id: "board"} }})

	m := newAt(t, testDeps(), 80, 24)
	for name, status := range map[string]string{
		"no status":       "",
		"a short one":     "saved",
		"exactly as wide": strings.Repeat("x", 80),
		"far too wide":    `search failed: Get "https://example.atlassian.net/rest/api/3/search/jql": dial tcp: lookup example.atlassian.net: no such host`,
		"with a newline":  "first line\nsecond line that should never become a row of its own",
	} {
		t.Run(name, func(t *testing.T) {
			next, _ := m.Update(StatusMsg{Text: status, Level: LevelError})
			frame := ansi.Strip(next.(Model).Frame())
			if got := strings.Count(frame, "\n") + 1; got != 24 {
				t.Errorf("frame is %d rows, want 24 — the footer is off the screen\n%s", got, frame)
			}
			for i, line := range strings.Split(frame, "\n") {
				if w := lipgloss.Width(line); w > 80 {
					t.Errorf("row %d is %d columns wide, want at most 80", i, w)
				}
			}
		})
	}
}

func TestBlocker_IsAskedBeforeAnythingThatWouldDiscardTheView(t *testing.T) {
	resetRegistry()
	t.Cleanup(resetRegistry)
	RegisterView(spec("board", 1, "", &stubView{id: "board"}))
	RegisterView(spec("backlog", 2, "", &stubView{id: "backlog"}))

	draft := &stubView{id: "editor", blocks: "the comment you are writing is unsaved"}
	for name, act := range map[string]func(Model) (tea.Model, tea.Cmd){
		"going back with esc": func(m Model) (tea.Model, tea.Cmd) { return m.Update(keyPress("esc")) },
		"switching view by key": func(m Model) (tea.Model, tea.Cmd) {
			next, _ := m.Update(keyPress("g"))
			return next.(Model).Update(keyPress("2"))
		},
		"switching view by name":   func(m Model) (tea.Model, tea.Cmd) { return m.Update(OpenMsg{ID: "backlog"}) },
		"popping programmatically": func(m Model) (tea.Model, tea.Cmd) { return m.Update(PopMsg{}) },
	} {
		t.Run(name, func(t *testing.T) {
			m := newAt(t, testDeps(), 120, 30)
			next, _ := m.Update(PushMsg{View: draft, ID: "editor", Title: "Editing"})
			after, _ := act(next.(Model))

			frame := ansi.Strip(after.(Model).Frame())
			if !strings.Contains(frame, "editor body") {
				t.Errorf("the view was discarded although it said it was holding something:\n%s", frame)
			}
			if !strings.Contains(frame, "unsaved") {
				t.Errorf("the reason was not shown:\n%s", frame)
			}
		})
	}
}

func TestCtrlC_QuitsEvenWithTheHelpOverlayOpen(t *testing.T) {
	resetRegistry()
	t.Cleanup(resetRegistry)
	RegisterView(spec("board", 1, "", &stubView{id: "board"}))

	m := newAt(t, testDeps(), 120, 30)
	m, _ = press(m, "?")
	if _, cmd := press(m, "ctrl+c"); !isQuit(cmd) {
		t.Error("ctrl+c was swallowed by the help overlay")
	}
}

func TestFocus_IsHandedOverWhenTheStackChanges(t *testing.T) {
	resetRegistry()
	t.Cleanup(resetRegistry)
	root := &stubView{id: "board"}
	RegisterView(spec("board", 1, "", root))

	m := newAt(t, testDeps(), 120, 30)
	root.seen = nil

	pushed := &stubView{id: "detail"}
	next, _ := m.Update(PushMsg{View: pushed, ID: "issue"})
	m = next.(Model)
	if !saw(root, "blur") {
		t.Errorf("the root was not told it lost focus: %v", root.seen)
	}
	if !saw(pushed, "focus") {
		t.Errorf("the pushed view was not told it has focus: %v", pushed.seen)
	}

	root.seen, pushed.seen = nil, nil
	if _, _ = m.Update(PopMsg{}); !saw(pushed, "blur") || !saw(root, "focus") {
		t.Errorf("focus was not handed back on pop: root=%v pushed=%v", root.seen, pushed.seen)
	}
}

func TestKeys_AViewTakingTypingGetsThemAllExceptCtrlC(t *testing.T) {
	resetRegistry()
	t.Cleanup(resetRegistry)
	view := &stubView{id: "board", capturing: true}
	RegisterView(spec("board", 1, "", view))
	RegisterView(spec("backlog", 2, "", &stubView{id: "backlog"}))

	m := newAt(t, testDeps(), 120, 30)
	view.seen = nil

	// Every one of these is a global binding. A filter or a form field that
	// cannot receive them is not one anybody can type into.
	for _, k := range []string{"q", "r", "R", "?", "esc", "2"} {
		m, _ = press(m, k)
		if !saw(view, "key:"+k) {
			t.Errorf("the view never received %q: %v", k, view.seen)
		}
	}
	if got := ansi.Strip(m.Frame()); !strings.Contains(got, "board body") {
		t.Errorf("a global key acted anyway:\n%s", got)
	}

	if _, cmd := press(m, "ctrl+c"); !isQuit(cmd) {
		t.Error("ctrl+c must work even while a view is taking typing")
	}
}

func TestFooter_SaysNothingAboutGlobalsAViewIsSwallowing(t *testing.T) {
	resetRegistry()
	t.Cleanup(resetRegistry)
	view := &stubView{id: "board"}
	RegisterView(spec("board", 1, "", view))
	RegisterKeys("board", KeySet{Short: []Binding{Bind([]string{"ctrl+g"}, "ctrl+g", "clear filter")}})

	m := newAt(t, testDeps(), 140, 30)
	if got := lastLine(ansi.Strip(m.Frame())); !strings.HasSuffix(got, "? q") {
		t.Fatalf("the globals should show while nothing is capturing:\n%s", got)
	}

	view.capturing = true
	got := lastLine(ansi.Strip(m.Frame()))
	if !strings.Contains(got, "clear filter") {
		t.Errorf("the view's own keys vanished:\n%s", got)
	}
	if strings.Contains(got, "? ") || strings.HasSuffix(got, "q") {
		t.Errorf("the footer advertises globals the view is swallowing:\n%s", got)
	}
}
