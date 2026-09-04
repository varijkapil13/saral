package settings

import (
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	zone "github.com/lrstanley/bubblezone/v2"

	"github.com/varijkapil13/saral/internal/ui/kernel"
)

func sampleOptions() []kernel.SettingOption {
	return []kernel.SettingOption{
		{ID: "default", Label: "Use the default colours"},
		{ID: "nord", Label: "Use the Nord colour scheme", Note: "cool"},
		{ID: "dracula", Label: "Use the Dracula colour scheme"},
	}
}

// pilotP drives the generic picker the same way pilot drives the main
// screen — a second harness because pickerModel is its own kernel.View, built
// and typed independently of Model.
type pilotP struct {
	t    *testing.T
	m    *pickerModel
	msgs []tea.Msg
}

func flyPicker(t *testing.T, opts []kernel.SettingOption, current string, apply func(kernel.Deps, string) tea.Cmd, w, h int) *pilotP {
	t.Helper()
	d := kernel.Deps{Theme: defaultTheme(), Zones: zone.New(), Now: func() time.Time { return clockAt }}
	p := &pilotP{t: t, m: newPicker(d, opts, current, apply)}
	p.send(kernel.SizeMsg{Width: w, Height: h})
	p.run(p.m.Init())
	return p
}

func (p *pilotP) send(msg tea.Msg) {
	p.t.Helper()
	view, cmd := p.m.Update(msg)
	m, ok := view.(*pickerModel)
	if !ok {
		p.t.Fatal("Update did not return a *pickerModel")
	}
	p.m = m
	p.run(cmd)
}

func (p *pilotP) run(cmd tea.Cmd) {
	p.t.Helper()
	queue := []tea.Cmd{cmd}
	for steps := 0; len(queue) > 0; steps++ {
		if steps > 500 {
			p.t.Fatal("commands never settled")
		}
		next := queue[0]
		queue = queue[1:]
		if next == nil {
			continue
		}
		msg := next()
		if msg == nil {
			continue
		}
		if cmds, ok := unwrapCmds(msg); ok {
			queue = append(queue, cmds...)
			continue
		}
		p.msgs = append(p.msgs, msg)
	}
}

func (p *pilotP) press(keys ...string) {
	p.t.Helper()
	for _, k := range keys {
		p.send(stroke(k))
	}
}

func (p *pilotP) typeText(s string) {
	p.t.Helper()
	for _, r := range s {
		p.send(tea.KeyPressMsg{Code: r, Text: string(r)})
	}
}

func (p *pilotP) applied() []string {
	out := []string{}
	for _, msg := range p.msgs {
		if a, ok := msg.(appliedMsg); ok {
			out = append(out, a.id)
		}
	}
	return out
}

func (p *pilotP) popped() bool {
	for _, msg := range p.msgs {
		if _, ok := msg.(kernel.PopMsg); ok {
			return true
		}
	}
	return false
}

// appliedMsg is a test-only message so a test can see which option's ID the
// picker chose without depending on what a real Setting.Set happens to do.
type appliedMsg struct{ id string }

func recordingApply(t *testing.T) func(kernel.Deps, string) tea.Cmd {
	return func(_ kernel.Deps, id string) tea.Cmd {
		return func() tea.Msg { return appliedMsg{id: id} }
	}
}

func TestPicker_EnterChoosesTheHighlightedOption(t *testing.T) {
	t.Parallel()
	p := flyPicker(t, sampleOptions(), "default", recordingApply(t), 60, 12)
	p.press("down", "enter")
	if got := p.applied(); len(got) != 1 || got[0] != "nord" {
		t.Fatalf("enter on the second option applied %v, want nord", got)
	}
	if !p.popped() {
		t.Error("choosing an option did not pop the picker")
	}
}

func TestPicker_TypingFiltersAndRanksByScore(t *testing.T) {
	t.Parallel()
	p := flyPicker(t, sampleOptions(), "default", recordingApply(t), 60, 12)
	p.typeText("dracula")
	if len(p.m.shown) != 1 || p.m.opts[p.m.shown[0]].ID != "dracula" {
		t.Fatalf("filtering for dracula left %v", p.m.shown)
	}
}

func TestPicker_EscPopsWithoutApplying(t *testing.T) {
	t.Parallel()
	p := flyPicker(t, sampleOptions(), "default", recordingApply(t), 60, 12)
	p.press("esc")
	if len(p.applied()) != 0 {
		t.Errorf("esc applied something: %v", p.applied())
	}
	if !p.popped() {
		t.Error("esc did not pop the picker")
	}
}

func TestPicker_EachOptionDrawsInItsOwnStyleWhenGiven(t *testing.T) {
	t.Parallel()
	styled := false
	opts := []kernel.SettingOption{
		{ID: "a", Label: "Plain"},
		{ID: "b", Label: "Styled", Style: func(*kernel.Theme) lipgloss.Style {
			styled = true
			return lipgloss.NewStyle()
		}},
	}
	p := flyPicker(t, opts, "a", recordingApply(t), 60, 12)
	_ = p.m.View()
	if !styled {
		t.Error("an option's own Style was never called while drawing the picker")
	}
}

func TestPicker_NothingMatchesSaysSoAndDoesNotPanic(t *testing.T) {
	t.Parallel()
	p := flyPicker(t, sampleOptions(), "default", recordingApply(t), 60, 12)
	p.typeText("zzzzz")
	mustContain(t, strings.Join([]string{p.m.View()}, ""), "Nothing matches")
}
