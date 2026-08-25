package kernel

import (
	"strconv"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"

	"github.com/varijkapil13/saral/pkg/jira"
)

type stubView struct {
	id        string
	width     int
	height    int
	seen      []string
	content   string
	blocks    string
	capturing bool
}

func (s *stubView) Init() tea.Cmd { return nil }

func (s *stubView) Update(msg tea.Msg) (View, tea.Cmd) {
	if m, ok := msg.(SizeMsg); ok {
		s.width, s.height = m.Width, m.Height
	}
	s.seen = append(s.seen, msgName(msg))
	return s, nil
}

func (s *stubView) View() string {
	if s.content != "" {
		return s.content
	}
	return s.id + " body"
}

func (s *stubView) WantsRawKeys() bool { return s.capturing }

func (s *stubView) BlocksClose() (string, bool) {
	if s.blocks == "" {
		return "", false
	}
	return s.blocks, true
}

func msgName(msg tea.Msg) string {
	switch m := msg.(type) {
	case SizeMsg:
		return "size"
	case RefreshMsg:
		if m.Purge {
			return "refresh:purge"
		}
		return "refresh"
	case ThemeMsg:
		return "theme"
	case CapabilitiesMsg:
		return "caps"
	case FocusMsg:
		if m.Focused {
			return "focus"
		}
		return "blur"
	case RunQueryMsg:
		return "query:" + m.JQL
	case SavedQueriesMsg:
		return "saved:" + strconv.Itoa(m.Queries.Len())
	case tea.KeyPressMsg:
		return "key:" + m.String()
	case CommandRanMsg:
		return "ran:" + m.ID + ":" + strings.Join(m.Keys, "/")
	case tea.MouseClickMsg:
		return "click"
	case tea.MouseWheelMsg:
		return "wheel"
	case tea.MouseMotionMsg:
		return "motion"
	case tea.MouseReleaseMsg:
		return "release"
	default:
		return "other"
	}
}

func spec(id string, slot int, requires jira.CapabilityKey, v *stubView) ViewSpec {
	return ViewSpec{
		ID:       id,
		Title:    strings.ToUpper(id[:1]) + id[1:],
		Slot:     slot,
		Requires: requires,
		New:      func(Deps) View { return v },
	}
}

func TestRegisterView_OrdersBySlotThenID(t *testing.T) {
	resetRegistry()
	t.Cleanup(resetRegistry)

	RegisterView(spec("zulu", 2, "", &stubView{id: "zulu"}))
	RegisterView(spec("alpha", 1, "", &stubView{id: "alpha"}))
	RegisterView(spec("detail", 0, "", &stubView{id: "detail"}))
	RegisterView(spec("aside", 0, "", &stubView{id: "aside"}))

	views := Views()
	got := make([]string, 0, len(views))
	for _, s := range views {
		got = append(got, s.ID)
	}
	want := []string{"alpha", "zulu", "aside", "detail"}
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Errorf("got %v, want %v", got, want)
	}
	if errs := RegistrationErrors(); len(errs) != 0 {
		t.Errorf("unexpected registration errors: %v", errs)
	}
}

func TestRegisterView_RecordsBadRegistrationsInsteadOfPanicking(t *testing.T) {
	for name, tc := range map[string]struct {
		register func()
		want     string
	}{
		"no ID":             {func() { RegisterView(ViewSpec{New: func(Deps) View { return nil }}) }, "no ID"},
		"no constructor":    {func() { RegisterView(ViewSpec{ID: "x"}) }, "no constructor"},
		"impossible slot":   {func() { RegisterView(spec("x", 12, "", nil)) }, "not 0-9"},
		"duplicate ID":      {func() { RegisterView(spec("x", 1, "", nil)); RegisterView(spec("x", 2, "", nil)) }, "registered twice"},
		"contested slot":    {func() { RegisterView(spec("x", 1, "", nil)); RegisterView(spec("y", 1, "", nil)) }, "both claim footer slot 1"},
		"keys with no ID":   {func() { RegisterKeys("", KeySet{}) }, "no view ID"},
		"duplicate keys":    {func() { RegisterKeys("x", KeySet{}); RegisterKeys("x", KeySet{}) }, "registered twice"},
		"command no ID":     {func() { RegisterCommand(Command{Run: func(Deps) tea.Cmd { return nil }}) }, "no ID"},
		"command no runner": {func() { RegisterCommand(Command{ID: "c"}) }, "nothing to run"},
	} {
		t.Run(name, func(t *testing.T) {
			resetRegistry()
			t.Cleanup(resetRegistry)
			tc.register()
			errs := RegistrationErrors()
			if len(errs) == 0 {
				t.Fatal("expected a recorded error")
			}
			if !strings.Contains(errs[0].Error(), tc.want) {
				t.Errorf("error %q does not mention %q", errs[0], tc.want)
			}
		})
	}
}

func TestNew_RefusesToStartWithABadRegistration(t *testing.T) {
	resetRegistry()
	t.Cleanup(resetRegistry)
	RegisterView(ViewSpec{ID: "broken"})

	if _, err := New(Deps{}); err == nil {
		t.Fatal("New accepted a registry that failed to register a view")
	}
}

func TestCommands_OrderedByGroupThenTitle(t *testing.T) {
	resetRegistry()
	t.Cleanup(resetRegistry)
	run := func(Deps) tea.Cmd { return nil }
	RegisterCommand(Command{ID: "b", Group: "issue", Title: "Assign", Run: run})
	RegisterCommand(Command{ID: "a", Group: "board", Title: "Move", Run: run})
	RegisterCommand(Command{ID: "c", Group: "issue", Title: "Add comment", Run: run})

	cmds := Commands()
	got := make([]string, 0, len(cmds))
	for _, c := range cmds {
		got = append(got, c.ID)
	}
	if strings.Join(got, ",") != "a,c,b" {
		t.Errorf("got %v, want [a c b]", got)
	}
}

func TestKeysFor_ReturnsAnEmptySetForAnUnknownView(t *testing.T) {
	resetRegistry()
	t.Cleanup(resetRegistry)
	if !KeysFor("nobody").IsZero() {
		t.Error("an unregistered view should have no keys")
	}
}

func black() colorRGBA { return colorRGBA{} }

type colorRGBA struct{}

func (colorRGBA) RGBA() (r, g, b, a uint32) { return 0, 0, 0, 0xffff }
