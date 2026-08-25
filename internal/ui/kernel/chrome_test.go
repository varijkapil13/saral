package kernel

import (
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"
)

// reporterView is a view whose keys move with its state, which is what every
// real view in the program does. It stays capturing throughout, so a footer that
// follows it can only be following the generation.
type reporterView struct {
	id    string
	state int
	sets  []KeySet
}

func (r *reporterView) Init() tea.Cmd { return nil }

func (r *reporterView) Update(msg tea.Msg) (View, tea.Cmd) {
	if key, ok := msg.(tea.KeyPressMsg); ok && key.String() == "n" {
		r.state = (r.state + 1) % len(r.sets)
	}
	return r, nil
}

func (r *reporterView) View() string       { return r.id + " body" }
func (r *reporterView) WantsRawKeys() bool { return true }
func (r *reporterView) LiveKeys() (set KeySet, gen int) {
	return r.sets[r.state], r.state
}

func twoStateReporter(id string) *reporterView {
	return &reporterView{id: id, sets: []KeySet{
		{
			Short: []Binding{Bind([]string{"enter"}, "enter", "open"), Bind([]string{"/"}, "/", "filter")},
			Full:  [][]Binding{{Bind([]string{"enter"}, "enter", "open"), Bind([]string{"/"}, "/", "filter")}},
		},
		{
			Short: []Binding{Bind([]string{"enter"}, "enter", "keep filter"), Bind([]string{"esc"}, "esc", "clear filter")},
			Full:  [][]Binding{{Bind([]string{"enter"}, "enter", "keep filter"), Bind([]string{"esc"}, "esc", "clear filter")}},
		},
	}}
}

func reporterSpec(id string, slot int, v *reporterView) ViewSpec {
	return ViewSpec{ID: id, Title: strings.ToUpper(id[:1]) + id[1:], Slot: slot,
		New: func(Deps) View { return v }}
}

func TestFooter_ShowsWhatTheViewSaysWorksRatherThanWhatItRegistered(t *testing.T) {
	resetRegistry()
	t.Cleanup(resetRegistry)
	view := twoStateReporter("board")
	RegisterView(reporterSpec("board", 1, view))
	RegisterKeys("board", KeySet{Short: []Binding{Bind([]string{"x"}, "x", "registered at start-up")}})

	m := newAt(t, testDeps(), 120, 30)
	first := ansi.Strip(m.Frame())
	if !strings.Contains(first, "filter") {
		t.Errorf("the footer does not show what the view says works:\n%s", first)
	}
	if strings.Contains(first, "registered at start-up") {
		t.Errorf("the footer fell back to the registered set for a view that reports:\n%s", first)
	}

	m, _ = press(m, "n")
	second := ansi.Strip(m.Frame())
	if !strings.Contains(second, "keep filter") || !strings.Contains(second, "clear filter") {
		t.Errorf("the footer did not follow the view into its second state:\n%s", second)
	}
}

// The chrome is memoized on a comparable key and a KeySet holds slices, so the
// generation is the only thing that can tell it to repaint. This view captures
// keys in both states and never changes its title, depth, width or theme, so a
// footer that repaints here is repainting on the generation and nothing else.
func TestChrome_RepaintsWhenNothingButTheViewsKeysChanged(t *testing.T) {
	resetRegistry()
	t.Cleanup(resetRegistry)
	view := twoStateReporter("board")
	RegisterView(reporterSpec("board", 1, view))

	m := newAt(t, testDeps(), 120, 30)
	before := ansi.Strip(m.Frame())
	m, _ = press(m, "n")
	after := ansi.Strip(m.Frame())

	if before == after {
		t.Errorf("the footer is memoized past a change in the view's keys:\n%s", before)
	}
	if !m.capturing() {
		t.Fatal("the view stopped capturing, so this test proves nothing about the generation")
	}
}

func TestHelpOverlay_ListsWhatTheViewSaysWorksNow(t *testing.T) {
	resetRegistry()
	t.Cleanup(resetRegistry)
	view := &reporterView{id: "board", sets: []KeySet{{
		Short: []Binding{Bind([]string{"a"}, "a", "write a comment")},
		Full:  [][]Binding{{Bind([]string{"a"}, "a", "write a comment")}},
	}}}
	RegisterView(reporterSpec("board", 1, view))
	RegisterKeys("board", KeySet{Full: [][]Binding{{Bind([]string{"z"}, "z", "registered at start-up")}}})

	m := newAt(t, testDeps(), 120, 30)
	m.showHelp = true
	got := ansi.Strip(m.Frame())
	if !strings.Contains(got, "write a comment") {
		t.Errorf("the help overlay does not list the live keys:\n%s", got)
	}
	if strings.Contains(got, "registered at start-up") {
		t.Errorf("the help overlay listed the registered set for a view that reports:\n%s", got)
	}
}

func TestViewKeys_AnswersFromTheRegistryForAViewThatDoesNotReport(t *testing.T) {
	resetRegistry()
	t.Cleanup(resetRegistry)
	RegisterView(spec("board", 1, "", &stubView{id: "board"}))
	RegisterKeys("board", KeySet{Short: []Binding{Bind([]string{"enter"}, "enter", "open")}})

	m := newAt(t, testDeps(), 120, 30)
	set, gen := m.viewKeys()
	if len(set.Short) != 1 || set.Short[0].Help().Desc != "open" {
		t.Errorf("a view that does not report should get the registered set, got %+v", set)
	}
	if gen != 0 {
		t.Errorf("the registry answer carries generation %d, so the chrome would repaint for nothing", gen)
	}
	if !strings.Contains(ansi.Strip(m.Frame()), "open") {
		t.Error("the footer of a view that does not report lost its keys")
	}
}

// A golden per state as well as per width: the states are what this packet
// changed, and the narrow one is where the footer has to give something up.
func TestFrame_GoldenPerState(t *testing.T) {
	for name, state := range map[string]int{"resting": 0, "filtering": 1} {
		for _, size := range []struct {
			label string
			w, h  int
		}{{"120x30", 120, 30}, {"80x20", 80, 20}} {
			t.Run(name+" at "+size.label, func(t *testing.T) {
				resetRegistry()
				t.Cleanup(resetRegistry)
				view := twoStateReporter("board")
				view.state = state
				RegisterView(reporterSpec("board", 1, view))
				RegisterView(spec("backlog", 2, "", &stubView{id: "backlog"}))

				m := newAt(t, testDeps(), size.w, size.h)
				golden(t, "chrome_"+name+"_"+size.label+".golden", ansi.Strip(m.Frame()))
			})
		}
	}
}

func TestFrame_GoldenOfTheHelpOverlayPerState(t *testing.T) {
	for name, state := range map[string]int{"resting": 0, "filtering": 1} {
		t.Run(name, func(t *testing.T) {
			resetRegistry()
			t.Cleanup(resetRegistry)
			view := twoStateReporter("board")
			view.state = state
			RegisterView(reporterSpec("board", 1, view))

			m := newAt(t, testDeps(), 120, 30)
			m.showHelp = true
			golden(t, "help_"+name+".golden", ansi.Strip(m.Frame()))
		})
	}
}

// A KeySet built per call would put its allocations under every keystroke,
// because chromeFor asks for one on every frame. The comparison is against a
// view that answers from the registry rather than against a fixed number, so
// this measures the cost the interface added and nothing else.
func TestFrame_AskingAViewForItsLiveKeysCostsNothingPerFrame(t *testing.T) {
	allocs := func(build func() ViewSpec) float64 {
		resetRegistry()
		t.Cleanup(resetRegistry)
		RegisterView(build())
		RegisterKeys("board", KeySet{Short: []Binding{Bind([]string{"enter"}, "enter", "open")}})
		m, err := New(testDeps(), WithSize(200, 60), WithMouse(false))
		if err != nil {
			t.Fatal(err)
		}
		next, _ := m.Update(tea.WindowSizeMsg{Width: 200, Height: 60})
		model := next.(Model)
		return testing.AllocsPerRun(200, func() { _ = model.Frame() })
	}

	static := allocs(func() ViewSpec { return spec("board", 1, "", &stubView{id: "board"}) })
	live := allocs(func() ViewSpec { return reporterSpec("board", 1, twoStateReporter("board")) })
	if live > static+2 {
		t.Errorf("reporting live keys costs %.0f allocations a frame against %.0f for the registry answer; "+
			"a KeySet is being built per call rather than stored", live, static)
	}
}
