package settings

import (
	"runtime"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/varijkapil13/saral/internal/ui/kernel"
	"github.com/varijkapil13/saral/pkg/jira"
)

func TestSettings_RadiosApplyImmediatelyOnArrowsAndOnEnter(t *testing.T) {
	t.Parallel()
	st := &fakeState{theme: "dark", scheme: "default", mouse: true}
	all, sections := sampleSettings(st)
	p := fly(t, settingsDeps(defaultTheme()), all, sections, 100, 30)

	p.press("right")
	if st.theme != "light" {
		t.Fatalf("→ on the theme row left it at %q, want light", st.theme)
	}
	p.press("left")
	if st.theme != "dark" {
		t.Fatalf("← back left it at %q, want dark", st.theme)
	}
	p.press("enter")
	if got := st.setCalls[len(st.setCalls)-1]; got != "appearance.theme=dark" {
		t.Errorf("enter on a radio row did not re-apply the value under the cursor: %v", st.setCalls)
	}
}

func TestSettings_ArrowsAtTheEdgeOfARadioRowDoNothing(t *testing.T) {
	t.Parallel()
	st := &fakeState{theme: "auto"}
	all, sections := sampleSettings(st)
	p := fly(t, settingsDeps(defaultTheme()), all, sections, 100, 30)

	before := len(st.setCalls)
	p.press("left")
	if len(st.setCalls) != before {
		t.Errorf("← at the first option called Set: %v", st.setCalls)
	}
}

func TestSettings_ToggleFlipsOnEnterSpaceOrEitherArrow(t *testing.T) {
	t.Parallel()
	for _, key := range []string{"enter", "space", "left", "right"} {
		st := &fakeState{mouse: true}
		all, sections := sampleSettings(st)
		p := fly(t, settingsDeps(defaultTheme()), all, sections, 100, 30)
		p.press("down", "down") // theme -> scheme -> mouse
		p.press(key)
		if st.mouse {
			t.Errorf("%s on the mouse row did not flip it off", key)
		}
	}
}

func TestSettings_PickerRowOpensOnEnterAndOnRightNotLeft(t *testing.T) {
	t.Parallel()
	st := &fakeState{theme: "dark", scheme: "default"}
	all, sections := sampleSettings(st)
	p := fly(t, settingsDeps(defaultTheme()), all, sections, 100, 30)
	p.press("down") // theme -> scheme, a picker row

	p.press("left")
	if len(p.pushed()) != 0 {
		t.Fatalf("← on a picker row opened something: %v", p.pushed())
	}
	p.press("right")
	if len(p.pushed()) != 1 {
		t.Fatalf("→ on a picker row did not open it: %v", p.msgs)
	}
	p2 := fly(t, settingsDeps(defaultTheme()), all, sections, 100, 30)
	p2.press("down", "enter")
	if len(p2.pushed()) != 1 {
		t.Fatalf("enter on a picker row did not open it: %v", p2.msgs)
	}
}

func TestSettings_ActionRunsOnEnter(t *testing.T) {
	t.Parallel()
	st := &fakeState{}
	all, sections := sampleSettings(st)
	p := fly(t, settingsDeps(defaultTheme()), all, sections, 100, 30)
	p.press("down", "down", "down", "down") // theme, scheme, mouse, profile, action
	p.press("enter")
	if len(st.runCalls) != 1 {
		t.Fatalf("enter on the action row did not run it: %v", st.runCalls)
	}
}

func TestSettings_InfoRowWithNoRunSaysItsSummaryOnEnter(t *testing.T) {
	t.Parallel()
	st := &fakeState{}
	all, sections := sampleSettings(st)
	p := fly(t, settingsDeps(defaultTheme()), all, sections, 100, 30)
	p.press("down", "down", "down") // theme, scheme, mouse, profile
	p.press("enter")
	mustContain(t, strings.Join(p.statuses(), " | "), "site, account and where the token comes from")
}

// A row whose Requires the capability probe refuses is hidden with the
// probe's own words, exactly as palette.refusal does it.
func TestSettings_ARefusedCapabilityHidesTheRowEntirely(t *testing.T) {
	t.Parallel()
	st := &fakeState{}
	all, sections := sampleSettings(st)
	all = append(all, kernel.Setting{
		ID: "gated.thing", Section: "Session", Order: 2, Title: "Gated thing",
		Summary: "needs a capability", Kind: kernel.KindAction, Scope: kernel.ScopeSession,
		Requires: jira.CapBoards,
		Run:      func(kernel.Deps) tea.Cmd { return nil },
	})
	caps := jira.Capabilities{Boards: jira.Capability{Reason: "you need the Manage Sprints permission"}}
	d := settingsDeps(defaultTheme())
	d.Caps = caps
	p := fly(t, d, all, sections, 100, 30)
	mustNotContain(t, p.frame(), "Gated thing")

	allowed := d
	allowed.Caps = fullCaps()
	allowed.Caps.Boards = jira.Capability{OK: true}
	p2 := fly(t, allowed, all, sections, 100, 30)
	mustContain(t, p2.frame(), "Gated thing")
}

// A row whose Unavailable answers is drawn with its value and its reason,
// never hidden — the two are different questions.
func TestSettings_AnUnavailableRowIsDrawnWithItsReasonNotHidden(t *testing.T) {
	t.Parallel()
	st := &fakeState{theme: "no-color", scheme: "nord"}
	all, sections := sampleSettings(st)
	p := fly(t, settingsDeps(noColorTheme()), all, sections, 100, 30)
	mustContain(t, p.frame(), "Colour scheme", "Nord", "colour is off, so a scheme would change nothing you can see")
}

func TestSettings_EmptyRegistryRendersWithNoBrokenFrame(t *testing.T) {
	t.Parallel()
	p := fly(t, settingsDeps(defaultTheme()), nil, nil, 100, 30)
	mustContain(t, p.frame(), "Nothing is registered")
}

// A registry whose every setting is refused is a real state too, and neither
// it nor an empty one may draw a broken frame.
func TestSettings_EveryRowRefusedRendersWithNoBrokenFrame(t *testing.T) {
	t.Parallel()
	all := []kernel.Setting{
		{
			ID: "gated.one", Section: "Session", Order: 0, Title: "One",
			Kind: kernel.KindAction, Requires: jira.CapBoards,
			Run: func(kernel.Deps) tea.Cmd { return nil },
		},
	}
	d := settingsDeps(defaultTheme())
	d.Caps = jira.Capabilities{Boards: jira.Capability{Reason: "no"}}
	p := fly(t, d, all, []string{"Session"}, 100, 30)
	mustContain(t, p.frame(), "available on this site")
	mustNotContain(t, p.frame(), "One")
}

func TestSettings_MouseClickOnARadioOptionAppliesThatExactValue(t *testing.T) {
	t.Parallel()
	st := &fakeState{theme: "auto"}
	all, sections := sampleSettings(st)
	d := settingsDeps(defaultTheme())
	p := fly(t, d, all, sections, 100, 30)

	_ = d.Zones.Scan(p.m.View())
	id := p.m.optZone("appearance.theme", "light")
	deadline := time.Now().Add(5 * time.Second)
	for d.Zones.Get(id).IsZero() {
		if time.Now().After(deadline) {
			t.Fatal("the light option drew with no zone of its own")
		}
		runtime.Gosched()
	}
	info := d.Zones.Get(id)
	p.send(tea.MouseClickMsg{X: info.StartX, Y: info.StartY, Button: tea.MouseLeft})
	if st.theme != "light" {
		t.Errorf("clicking the light option left the theme at %q", st.theme)
	}
}

func TestSettings_TheProjectRowOpensPaletteSOwnPicker(t *testing.T) {
	t.Parallel()
	all := []kernel.Setting{
		{
			ID: "session.project", Section: "Session", Order: 0, Title: "Project",
			Kind: kernel.KindChoice, Scope: kernel.ScopeSession,
			Options: func(d kernel.Deps) []kernel.SettingOption {
				return []kernel.SettingOption{{ID: d.Project, Label: "PROJ"}}
			},
			Value: func(d kernel.Deps) string { return d.Project },
			Set:   func(_ kernel.Deps, id string) tea.Cmd { return kernel.SetProject(id) },
		},
	}
	d := settingsDeps(defaultTheme())
	d.Project = "PROJ"
	p := fly(t, d, all, []string{"Session"}, 100, 30)
	p.press("enter")
	pushed := p.pushed()
	if len(pushed) != 1 || !strings.HasPrefix(pushed[0], "palette.project:") {
		t.Fatalf("session.project did not open palette's own picker: %v", pushed)
	}
}
