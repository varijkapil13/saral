package settings

import (
	"flag"
	"fmt"
	"os"
	"reflect"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"
	zone "github.com/lrstanley/bubblezone/v2"

	"github.com/varijkapil13/saral/internal/ui/kernel"
	"github.com/varijkapil13/saral/pkg/jira"
)

var update = flag.Bool("update", false, "rewrite the golden files")

var clockAt = time.Date(2026, time.September, 3, 9, 0, 0, 0, time.UTC)

// TestMain points every read this package's tests do at a directory of this
// run's own, so nothing here ever touches whoever is running the suite's real
// config.toml.
func TestMain(m *testing.M) {
	dir, err := os.MkdirTemp("", "saral-settings-config")
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	if err := os.Setenv("SARAL_CONFIG_DIR", dir); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	code := m.Run()
	_ = os.RemoveAll(dir)
	os.Exit(code)
}

// fakeState is the state sampleSettings's Value/Set/Run close over, so a test
// can assert what a keypress or a click actually changed without a real
// theme, a real profile or a real site behind any of it.
type fakeState struct {
	theme, scheme string
	mouse         bool
	setCalls      []string
	runCalls      []string
}

// sampleSettings is five settings across two sections — one of every kind —
// injected rather than read from the real registry, so these tests do not
// depend on which view packages happen to be linked into the test binary.
func sampleSettings(st *fakeState) (settings []kernel.Setting, sections []string) {
	settings = []kernel.Setting{
		{
			ID: "appearance.theme", Section: "Appearance", Order: 0, Title: "Theme",
			Summary: "how colours are chosen", Kind: kernel.KindChoice, Scope: kernel.ScopeProfile,
			Options: func(kernel.Deps) []kernel.SettingOption {
				return []kernel.SettingOption{
					{ID: "auto", Label: "auto"}, {ID: "dark", Label: "dark"},
					{ID: "light", Label: "light"}, {ID: "no-color", Label: "no colour"},
				}
			},
			Value: func(kernel.Deps) string { return st.theme },
			Set: func(_ kernel.Deps, id string) tea.Cmd {
				st.theme = id
				st.setCalls = append(st.setCalls, "appearance.theme="+id)
				return kernel.Status("theme set to " + id)
			},
		},
		{
			ID: "appearance.scheme", Section: "Appearance", Order: 1, Title: "Colour scheme",
			Summary: "which colours mean accent, danger and the rest", Kind: kernel.KindChoice, Scope: kernel.ScopeProfile,
			Options: func(kernel.Deps) []kernel.SettingOption {
				return []kernel.SettingOption{
					{ID: "default", Label: "Use the default colours"},
					{ID: "nord", Label: "Use the Nord colour scheme", Style: func(t *kernel.Theme) lipgloss.Style { return t.Accent }},
					{ID: "dracula", Label: "Use the Dracula colour scheme"},
					{ID: "solarized", Label: "Use the Solarized colour scheme"},
					{ID: "gruvbox", Label: "Use the Gruvbox colour scheme"},
				}
			},
			Value: func(kernel.Deps) string { return st.scheme },
			Set: func(_ kernel.Deps, id string) tea.Cmd {
				st.scheme = id
				st.setCalls = append(st.setCalls, "appearance.scheme="+id)
				return nil
			},
			Unavailable: func(kernel.Deps) string {
				if st.theme == "no-color" {
					return "colour is off, so a scheme would change nothing you can see"
				}
				return ""
			},
		},
		{
			ID: "appearance.mouse", Section: "Appearance", Order: 2, Title: "Mouse",
			Summary: "clicking, dragging the split, the right-click menu", Kind: kernel.KindToggle, Scope: kernel.ScopeFile,
			Options: func(kernel.Deps) []kernel.SettingOption {
				return []kernel.SettingOption{{ID: "on", Label: "on"}, {ID: "off", Label: "off"}}
			},
			Value: func(kernel.Deps) string {
				if st.mouse {
					return "on"
				}
				return "off"
			},
			Set: func(_ kernel.Deps, id string) tea.Cmd {
				st.mouse = id == "on"
				st.setCalls = append(st.setCalls, "appearance.mouse="+id)
				return nil
			},
		},
		{
			ID: "session.info", Section: "Session", Order: 0, Title: "Profile",
			Summary: "site, account and where the token comes from", Kind: kernel.KindInfo, Scope: kernel.ScopeFile,
			Value: func(kernel.Deps) string { return "work · example.atlassian.net" },
		},
		{
			ID: "session.action", Section: "Session", Order: 1, Title: "Set up a profile again",
			Summary: "re-runs the questions onboarding asks", Kind: kernel.KindAction, Scope: kernel.ScopeSession,
			Run: func(kernel.Deps) tea.Cmd {
				st.runCalls = append(st.runCalls, "session.action")
				return kernel.Status("ran")
			},
		},
	}
	return settings, []string{"Appearance", "Session"}
}

func settingsDeps(theme *kernel.Theme) kernel.Deps {
	return kernel.Deps{
		Theme: theme,
		Zones: zone.New(),
		Site:  "example.atlassian.net",
		Now:   func() time.Time { return clockAt },
	}
}

func defaultTheme() *kernel.Theme {
	return kernel.NewTheme(kernel.ThemeDark, true, kernel.UnicodeGlyphs())
}
func noColorTheme() *kernel.Theme {
	return kernel.NewTheme(kernel.ThemeNoColor, true, kernel.ASCIIGlyphs())
}

// pilot drives the settings screen the way the kernel would, keeping what a
// command produced instead of acting on it, so a test can assert that a
// keypress asked for a push or a status rather than that it happened to work.
type pilot struct {
	t    *testing.T
	m    *Model
	msgs []tea.Msg
}

func fly(t *testing.T, d kernel.Deps, all []kernel.Setting, sections []string, w, h int) *pilot {
	t.Helper()
	p := &pilot{t: t, m: build(d, all, sections)}
	p.send(kernel.SizeMsg{Width: w, Height: h})
	p.run(p.m.Init())
	return p
}

func (p *pilot) send(msg tea.Msg) {
	p.t.Helper()
	view, cmd := p.m.Update(msg)
	model, ok := view.(*Model)
	if !ok {
		p.t.Fatal("Update did not return a *Model")
	}
	p.m = model
	p.run(cmd)
}

func (p *pilot) run(cmd tea.Cmd) {
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

func (p *pilot) press(keys ...string) {
	p.t.Helper()
	for _, k := range keys {
		p.send(stroke(k))
	}
}

func (p *pilot) frame() string { return ansi.Strip(p.m.View()) }

func (p *pilot) pushed() []string {
	out := []string{}
	for _, msg := range p.msgs {
		if push, ok := msg.(kernel.PushMsg); ok {
			out = append(out, push.ID+":"+push.Title)
		}
	}
	return out
}

func (p *pilot) statuses() []string {
	out := []string{}
	for _, msg := range p.msgs {
		if status, ok := msg.(kernel.StatusMsg); ok {
			out = append(out, status.Text)
		}
	}
	return out
}

func unwrapCmds(msg tea.Msg) ([]tea.Cmd, bool) {
	v := reflect.ValueOf(msg)
	if v.Kind() != reflect.Slice || v.Type().Elem() != reflect.TypeOf(tea.Cmd(nil)) {
		return nil, false
	}
	out := make([]tea.Cmd, 0, v.Len())
	for i := range v.Len() {
		cmd, _ := v.Index(i).Interface().(tea.Cmd)
		out = append(out, cmd)
	}
	return out, true
}

func stroke(s string) tea.KeyPressMsg {
	switch s {
	case "enter":
		return tea.KeyPressMsg{Code: tea.KeyEnter}
	case "esc":
		return tea.KeyPressMsg{Code: tea.KeyEsc}
	case "up":
		return tea.KeyPressMsg{Code: tea.KeyUp}
	case "down":
		return tea.KeyPressMsg{Code: tea.KeyDown}
	case "left":
		return tea.KeyPressMsg{Code: tea.KeyLeft}
	case "right":
		return tea.KeyPressMsg{Code: tea.KeyRight}
	case "space":
		return tea.KeyPressMsg{Code: tea.KeySpace}
	default:
		r, _ := utf8.DecodeRuneInString(s)
		return tea.KeyPressMsg{Code: r, Text: s}
	}
}

func mustContain(t *testing.T, got string, want ...string) {
	t.Helper()
	for _, w := range want {
		if !strings.Contains(got, w) {
			t.Errorf("output does not contain %q:\n%s", w, got)
		}
	}
}

func mustNotContain(t *testing.T, got string, unwanted ...string) {
	t.Helper()
	for _, w := range unwanted {
		if strings.Contains(got, w) {
			t.Errorf("output still contains %q:\n%s", w, got)
		}
	}
}

func fullCaps() jira.Capabilities {
	ok := jira.Capability{OK: true}
	return jira.Capabilities{Plans: ok, Boards: ok, Attachments: ok, DeleteIssues: ok, BulkMove: ok, TimeZone: time.UTC}
}
