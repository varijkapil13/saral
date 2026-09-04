package kernel

import (
	"os"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/varijkapil13/saral/internal/config"
	"github.com/varijkapil13/saral/pkg/jira"
)

func TestParseThemeMode(t *testing.T) {
	t.Parallel()
	for in, want := range map[string]ThemeMode{
		"": ThemeAuto, "auto": ThemeAuto, "Dark": ThemeDark, "light": ThemeLight,
		"no-color": ThemeNoColor, "nocolor": ThemeNoColor,
	} {
		got, err := ParseThemeMode(in)
		if err != nil {
			t.Errorf("%q: %v", in, err)
		}
		if got != want {
			t.Errorf("%q: got %v want %v", in, got, want)
		}
	}
	if got, err := ParseThemeMode("  mono\t"); err != nil || got != ThemeNoColor {
		t.Errorf("surrounding whitespace was not trimmed: %v %v", got, err)
	}
	if _, err := ParseThemeMode("solarized"); err == nil {
		t.Error("an unknown theme name was accepted")
	}
}

func TestThemeModeFromEnv(t *testing.T) {
	t.Parallel()
	for name, tc := range map[string]struct {
		env        []string
		configured string
		want       ThemeMode
	}{
		"NO_COLOR beats configuration": {[]string{"NO_COLOR=1"}, "dark", ThemeNoColor},
		"an empty NO_COLOR is not set": {[]string{"NO_COLOR="}, "dark", ThemeDark},
		"a dumb terminal has no color": {[]string{"TERM=dumb"}, "light", ThemeNoColor},
		"configuration otherwise wins": {[]string{"TERM=xterm-256color"}, "light", ThemeLight},
		"nonsense falls back to auto":  {nil, "solarized", ThemeAuto},
		"nothing set is auto":          {nil, "", ThemeAuto},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			if got := ThemeModeFromEnv(tc.env, tc.configured); got != tc.want {
				t.Errorf("got %v want %v", got, tc.want)
			}
		})
	}
}

func TestNewTheme_NoColorModeEmitsNoColour(t *testing.T) {
	t.Parallel()
	th := NewTheme(ThemeNoColor, true, UnicodeGlyphs())
	for name, style := range map[string]lipgloss.Style{
		"base": th.Base, "accent": th.Accent, "danger": th.Danger, "selected": th.Selected,
		"slot on": th.SlotOn, "footer": th.Footer, "header": th.Header, "overlay": th.Overlay,
	} {
		if out := style.Render("x"); hasColour(out) {
			t.Errorf("%s emitted colour in no-color mode: %q", name, out)
		}
	}
	if th.Color {
		t.Error("the no-color theme claims to have colour")
	}
}

func TestNewTheme_ColourModesDiffer(t *testing.T) {
	t.Parallel()
	dark := NewTheme(ThemeDark, false, UnicodeGlyphs())
	light := NewTheme(ThemeLight, true, UnicodeGlyphs())
	if !dark.Dark {
		t.Error("an explicit dark theme is not dark")
	}
	if light.Dark {
		t.Error("an explicit light theme is dark")
	}
	if dark.Base.Render("x") == light.Base.Render("x") {
		t.Error("light and dark render identically")
	}
	if !hasColour(dark.Accent.Render("x")) {
		t.Error("a colour theme emitted no colour")
	}
}

func TestNewTheme_GenerationChangesOnEveryBuild(t *testing.T) {
	t.Parallel()
	a := NewTheme(ThemeDark, true, UnicodeGlyphs())
	b := NewTheme(ThemeDark, true, UnicodeGlyphs())
	if a.Gen == b.Gen {
		t.Error("two themes share a generation, so memoized rows would never be rebuilt")
	}
}

func TestGlyphs_ASCIIFallbackIsPlain(t *testing.T) {
	t.Parallel()
	ascii := GlyphsFor("ascii")
	for name, g := range map[string]string{
		"bullet": ascii.Bullet, "arrow": ascii.Arrow, "check": ascii.Check,
		"vline": ascii.VLine, "separator": ascii.Separator, "diamond": ascii.Diamond,
		"type epic": ascii.TypeEpic, "type bug": ascii.TypeBug, "category done": ascii.CategoryDone,
	} {
		for _, r := range g {
			if r > 127 {
				t.Errorf("the ascii %s glyph %q is not ascii", name, g)
			}
		}
	}
}

func TestGlyphsFor_DefaultsToNerd(t *testing.T) {
	t.Parallel()
	for _, in := range []string{"", "  ", "nerd", "NERD", "something unrecognised"} {
		if got := GlyphsFor(in); got.Tier() != "nerd" {
			t.Errorf("GlyphsFor(%q).Tier() = %q, want nerd", in, got.Tier())
		}
	}
	if GlyphsFor("unicode").Tier() != "unicode" {
		t.Error("GlyphsFor(\"unicode\") did not resolve to the unicode tier")
	}
	if GlyphsFor("ascii").Tier() != "ascii" {
		t.Error("GlyphsFor(\"ascii\") did not resolve to the ascii tier")
	}
}

// TestGlyphs_EveryTierDefinesEveryField walks every field of Glyphs by
// reflection, over all three tiers, and fails on the first one any tier left
// at its zero value — a missing nerd icon must draw as a letter or a fallback
// rather than as an empty cell, and reflection is what lets a field added
// later be caught here without the test being told its name.
func TestGlyphs_EveryTierDefinesEveryField(t *testing.T) {
	t.Parallel()
	tiers := map[string]Glyphs{"nerd": NerdGlyphs(), "unicode": UnicodeGlyphs(), "ascii": ASCIIGlyphs()}
	scanned := 0
	rt := reflect.TypeOf(Glyphs{})
	for _, tier := range []string{"nerd", "unicode", "ascii"} {
		g := reflect.ValueOf(tiers[tier])
		for i := 0; i < rt.NumField(); i++ {
			field := rt.Field(i)
			v := g.Field(i)
			scanned++
			switch v.Kind() {
			case reflect.String:
				if v.String() == "" {
					t.Errorf("%s tier: field %s is empty", tier, field.Name)
				}
			case reflect.Slice:
				if v.Len() == 0 {
					t.Errorf("%s tier: field %s is empty", tier, field.Name)
				}
			default:
				t.Fatalf("unhandled Glyphs field kind for %s: %v", field.Name, v.Kind())
			}
		}
	}
	if scanned == 0 {
		t.Fatal("the scan visited no fields at all, so this test proves nothing")
	}
}

func TestGlyphs_TypeGlyphFallsBackToTheLetterForAnUnresolvedType(t *testing.T) {
	t.Parallel()
	g := UnicodeGlyphs()
	if got := g.TypeGlyph(jira.IssueType{Subtask: true}); got != g.TypeSubtask {
		t.Errorf("a subtask got %q, want the subtask icon %q", got, g.TypeSubtask)
	}
	for name, want := range map[string]string{
		"Bug": "B", "Story": "S", "Epic": "E", "Task": "T",
		"böcker": "B", "": "?",
	} {
		if got := g.TypeGlyph(jira.IssueType{Name: name}); got != want {
			t.Errorf("TypeGlyph(%q) = %q, want %q — pkg/jira.IssueType carries no hierarchy level, "+
				"so nothing here may resolve by matching the name", name, got, want)
		}
	}
}

func TestGlyphs_CategoryGlyphIsKeyedByCategoryNotName(t *testing.T) {
	t.Parallel()
	g := NerdGlyphs()
	for cat, want := range map[jira.StatusCategory]string{
		jira.CategoryToDo:       g.CategoryToDo,
		jira.CategoryInProgress: g.CategoryInProgress,
		jira.CategoryDone:       g.CategoryDone,
		jira.CategoryUnknown:    g.CategoryUnknown,
		jira.StatusCategory(99): g.CategoryUnknown,
	} {
		if got := g.CategoryGlyph(cat); got != want {
			t.Errorf("CategoryGlyph(%v) = %q, want %q", cat, got, want)
		}
	}
}

// colourfulEnv puts the environment back to one that allows colour, so that a
// machine with TERM unset or NO_COLOR exported does not turn a test about
// switching themes into a test about refusing to.
func colourfulEnv(t *testing.T) {
	t.Helper()
	t.Setenv("TERM", "xterm-256color")
	t.Setenv("NO_COLOR", "")
}

// writeConfig puts a config file where config.Path will find it and returns the
// path.
func writeConfig(t *testing.T, body string) string {
	t.Helper()
	dir := t.TempDir()
	t.Setenv("SARAL_CONFIG_DIR", dir)
	path := filepath.Join(dir, "config.toml")
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

const profileWithEverything = `active = "work"
mouse = true

[profiles.work]
site   = "example.atlassian.net"
email  = "you@example.com"
glyphs = "ascii"
token  = { env = "JIRA_TOKEN" }

[profiles.work.timeline]
start = ["Target start"]
end   = ["Target end"]

[[profiles.work.queries]]
name = "Blockers"
jql  = "resolution = EMPTY ORDER BY updated DESC"
key  = 2
`

// TestThemeIsASettingNotACommand is what replaced the four theme.* commands:
// docs/SETTINGS.md moves state out of the palette, so appearance.theme is a
// Setting offering all four modes and nothing named theme.* is a command any
// more.
func TestThemeIsASettingNotACommand(t *testing.T) {
	resetRegistry()
	t.Cleanup(resetRegistry)
	RegisterSetting(themeSetting())

	for _, cmd := range Commands() {
		if strings.HasPrefix(cmd.ID, "theme.") {
			t.Errorf("%s is still a command; theme modes moved to the appearance.theme setting", cmd.ID)
		}
	}

	s, ok := lookupSetting(t, "appearance.theme")
	if !ok {
		t.Fatal("appearance.theme is not registered")
	}
	want := map[string]bool{"auto": false, "dark": false, "light": false, "no-color": false}
	for _, opt := range s.Options(Deps{}) {
		if _, ours := want[opt.ID]; !ours {
			t.Errorf("appearance.theme offers an option nothing names: %q", opt.ID)
			continue
		}
		want[opt.ID] = true
		if opt.Label == "" {
			t.Errorf("option %q has no label", opt.ID)
		}
	}
	for id, found := range want {
		if !found {
			t.Errorf("appearance.theme does not offer %q", id)
		}
	}
}

// lookupSetting finds one registered setting by ID, the way LookupView and
// LookupCommand already do for their own registries.
func lookupSetting(t *testing.T, id string) (Setting, bool) {
	t.Helper()
	all := Settings()
	for i := range all {
		if all[i].ID == id {
			return all[i], true
		}
	}
	return Setting{}, false
}

func TestSwitchTheme_RebuildsTheStylesAndKeepsTheGlyphSet(t *testing.T) {
	colourfulEnv(t)
	writeConfig(t, profileWithEverything)

	d := Deps{Theme: NewTheme(ThemeNoColor, true, ASCIIGlyphs()), Site: "example.atlassian.net"}
	msg := firstMsgOfType[ThemeMsg](t, SwitchTheme(d, ThemeLight))
	switch {
	case msg.Theme == nil:
		t.Fatal("no theme came back")
	case msg.Theme.Mode != ThemeLight:
		t.Errorf("got mode %v want light", msg.Theme.Mode)
	case msg.Theme.Dark:
		t.Error("the light theme reports itself dark")
	case msg.Theme.Glyphs.Bullet != ASCIIGlyphs().Bullet:
		t.Error("the glyph set was replaced; it answers a question about the font, not about colour")
	case !hasColour(msg.Theme.Accent.Render("x")):
		t.Error("switching away from no-color left the styles colourless")
	}
}

func TestSwitchTheme_AsksTheTerminalAgainWhenItIsToldToFollowIt(t *testing.T) {
	colourfulEnv(t)
	writeConfig(t, profileWithEverything)

	d := Deps{Theme: NewTheme(ThemeDark, true, UnicodeGlyphs()), Site: "example.atlassian.net"}
	if !asksBackgroundColour(SwitchTheme(d, ThemeAuto)) {
		t.Error("auto did not ask the terminal for its background, so it would follow the old answer")
	}
	if asksBackgroundColour(SwitchTheme(d, ThemeLight)) {
		t.Error("an explicit theme asked the terminal for its background, which cannot change it")
	}
}

func TestSwitchTheme_RefusesColourWhenTheEnvironmentAlreadySaidNo(t *testing.T) {
	for name, env := range map[string][2]string{
		"NO_COLOR is set":         {"NO_COLOR", "1"},
		"the terminal is dumb":    {"TERM", "dumb"},
		"there is no TERM at all": {"TERM", ""},
	} {
		t.Run(name, func(t *testing.T) {
			t.Setenv("TERM", "xterm-256color")
			t.Setenv("NO_COLOR", "")
			t.Setenv(env[0], env[1])

			d := Deps{Theme: NewTheme(ThemeNoColor, true, UnicodeGlyphs())}
			status := firstMsgOfType[StatusMsg](t, SwitchTheme(d, ThemeDark))
			if !strings.Contains(status.Text, "no colour") {
				t.Errorf("unhelpful refusal: %q", status.Text)
			}
			if got := collect(SwitchTheme(d, ThemeDark)); len(got) != 1 {
				t.Errorf("the refusal still did other things: %#v", got)
			}
			if _, ok := firstOfType[ThemeMsg](collect(SwitchTheme(d, ThemeNoColor))); !ok {
				t.Error("no-color was refused in an environment that asked for exactly that")
			}
		})
	}
}

func TestWriteTheme_ChangesTheThemeAndNothingElseInTheProfile(t *testing.T) {
	path := writeConfig(t, profileWithEverything)

	if err := writeTheme("example.atlassian.net", ThemeDark); err != nil {
		t.Fatalf("writeTheme: %v", err)
	}
	cfg, err := config.LoadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	got, err := cfg.Get("work")
	if err != nil {
		t.Fatal(err)
	}
	switch {
	case got.Theme != "dark":
		t.Errorf("theme is %q, want dark", got.Theme)
	case got.Glyphs != "ascii":
		t.Errorf("the glyph set went from ascii to %q", got.Glyphs)
	case len(got.Queries) != 1 || got.Queries[0].Slot != 2:
		t.Errorf("the saved queries did not survive: %+v", got.Queries)
	case len(got.Timeline.Start) != 1 || len(got.Timeline.End) != 1:
		t.Errorf("the timeline fields did not survive: %+v", got.Timeline)
	case got.Token.Env != "JIRA_TOKEN":
		t.Errorf("the token source changed to %+v", got.Token)
	case cfg.Active != "work" || !cfg.Mouse:
		t.Errorf("the file's own settings changed: active=%q mouse=%v", cfg.Active, cfg.Mouse)
	}
}

func TestWriteTheme_WritesAutoAsNoThemeAtAll(t *testing.T) {
	path := writeConfig(t, strings.Replace(profileWithEverything, `glyphs = "ascii"`, `theme = "dark"`, 1))

	if err := writeTheme("example.atlassian.net", ThemeAuto); err != nil {
		t.Fatalf("writeTheme: %v", err)
	}
	body, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(body), "theme") {
		t.Errorf("auto was written as a value rather than as an absence:\n%s", body)
	}
}

func TestWriteTheme_RefusesWhenTheSessionIsNotOnTheActiveProfilesSite(t *testing.T) {
	writeConfig(t, profileWithEverything)

	err := writeTheme("other.atlassian.net", ThemeDark)
	if err == nil {
		t.Fatal("the theme was written onto a profile this session is not running as")
	}
	if !strings.Contains(err.Error(), "other.atlassian.net") || !strings.Contains(err.Error(), "work") {
		t.Errorf("the refusal does not say which session and which profile: %v", err)
	}
}

func TestSaveTheme_SaysSoWhenThereIsNoProfileToSaveTo(t *testing.T) {
	t.Setenv("SARAL_CONFIG_DIR", t.TempDir())

	status := firstMsgOfType[StatusMsg](t, saveTheme("example.atlassian.net", ThemeDark))
	if !strings.Contains(status.Text, "no profile") {
		t.Errorf("unhelpful message with nowhere to save: %q", status.Text)
	}
	if status.Level != LevelInfo {
		t.Errorf("a first run with no profile is reported at level %v, which reads as a failure", status.Level)
	}
}

func TestThemeSetting_SetReachesTheFrameAndTheProfile(t *testing.T) {
	colourfulEnv(t)
	path := writeConfig(t, profileWithEverything)
	resetRegistry()
	t.Cleanup(resetRegistry)
	RegisterView(spec("board", 1, "", &stubView{id: "board"}))

	d := testDeps()
	d.Site = "example.atlassian.net"
	m := newAt(t, d, 120, 30)
	if hasColour(m.Frame()) {
		t.Fatal("the test session did not start colourless, so this proves nothing")
	}

	cmd := themeSetting().Set(m.deps, "dark")
	theme, ok := firstOfType[ThemeMsg](collect(cmd))
	if !ok {
		t.Fatal("Set produced no theme")
	}
	next, _ := m.Update(theme)
	m = next.(Model)

	if m.deps.Theme.Mode != ThemeDark {
		t.Errorf("the kernel is on %v after switching to dark", m.deps.Theme.Mode)
	}
	if !hasColour(m.Frame()) {
		t.Error("the frame is still colourless after switching to the dark theme")
	}
	if saw := ansi.Strip(m.Frame()); !strings.Contains(saw, "board body") {
		t.Errorf("the view stopped drawing after a theme switch:\n%s", saw)
	}

	eventually(t, func() bool {
		cfg, err := config.LoadFile(path)
		if err != nil {
			return false
		}
		p, err := cfg.Get("work")
		return err == nil && p.Theme == "dark"
	})
}

// collect runs a command and flattens whatever it produced, because tea.Batch
// hands back a message holding more commands rather than a list of messages.
func collect(cmd tea.Cmd) []tea.Msg {
	if cmd == nil {
		return nil
	}
	switch msg := cmd().(type) {
	case nil:
		return nil
	case tea.BatchMsg:
		var out []tea.Msg
		for _, c := range msg {
			out = append(out, collect(c)...)
		}
		return out
	default:
		return []tea.Msg{msg}
	}
}

func firstOfType[T tea.Msg](msgs []tea.Msg) (T, bool) {
	for _, msg := range msgs {
		if want, ok := msg.(T); ok {
			return want, true
		}
	}
	var zero T
	return zero, false
}

func firstMsgOfType[T tea.Msg](t *testing.T, cmd tea.Cmd) T {
	t.Helper()
	got, ok := firstOfType[T](collect(cmd))
	if !ok {
		var zero T
		t.Fatalf("no %T came back", zero)
	}
	return got
}

// asksBackgroundColour reports whether the command asks the terminal what colour
// it is. bubbletea keeps the request message unexported, so it is recognised by
// comparing against what the request command itself produces.
func asksBackgroundColour(cmd tea.Cmd) bool {
	want := tea.RequestBackgroundColor()
	for _, msg := range collect(cmd) {
		if msg == want {
			return true
		}
	}
	return false
}

// hasColour reports whether a rendered string carries an SGR colour parameter.
// Bold, faint and reverse are not colour and are allowed in no-color mode.
func hasColour(s string) bool {
	for _, seq := range strings.Split(s, "\x1b[") {
		body, _, ok := strings.Cut(seq, "m")
		if !ok {
			continue
		}
		for _, param := range strings.Split(body, ";") {
			n, err := strconv.Atoi(param)
			if err != nil {
				continue
			}
			switch {
			case n >= 30 && n <= 38, n >= 40 && n <= 48,
				n >= 90 && n <= 97, n >= 100 && n <= 107:
				return true
			}
		}
	}
	return false
}
