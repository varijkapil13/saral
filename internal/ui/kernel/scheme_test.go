package kernel

import (
	"os"
	"strings"
	"testing"

	"github.com/varijkapil13/saral/internal/config"
)

func TestParseScheme(t *testing.T) {
	t.Parallel()
	for in, want := range map[string]string{
		"": "default", "default": "default", "Nord": "nord", "dracula": "dracula",
		"SOLARIZED": "solarized", "gruvbox": "gruvbox",
	} {
		got, err := ParseScheme(in)
		if err != nil {
			t.Errorf("%q: %v", in, err)
		}
		if got.ID() != want {
			t.Errorf("%q: got %v want %v", in, got.ID(), want)
		}
	}
	if got, err := ParseScheme("  nord\t"); err != nil || got.ID() != "nord" {
		t.Errorf("surrounding whitespace was not trimmed: %v %v", got, err)
	}
	if _, err := ParseScheme("monokai"); err == nil {
		t.Error("an unknown scheme name was accepted")
	}
}

// Every scheme this build offers has both a light and a dark half, or a
// scheme picked in one mode would silently fall back to the zero colour.
func TestSchemes_EveryOneHasBothLightAndDark(t *testing.T) {
	t.Parallel()
	for _, sc := range Schemes {
		for _, dark := range []bool{true, false} {
			c := sc.colors(dark)
			if c.fg == nil || c.accent == nil || c.danger == nil || c.warning == nil ||
				c.success == nil || c.muted == nil || c.surface == nil || c.selected == nil || c.onAccent == nil {
				t.Errorf("%s (dark=%v) leaves a role with no colour: %+v", sc.id, dark, c)
			}
		}
	}
}

func TestNewTheme_SchemesDiffer(t *testing.T) {
	t.Parallel()
	def := NewTheme(ThemeDark, true, UnicodeGlyphs())
	nord := NewTheme(ThemeDark, true, UnicodeGlyphs(), WithScheme(nordScheme))
	if def.Accent.Render("x") == nord.Accent.Render("x") {
		t.Error("the default scheme and Nord render the accent identically")
	}
	if !hasColour(nord.Accent.Render("x")) {
		t.Error("a coloured theme with a scheme set emitted no colour")
	}
	if nord.Scheme.ID() != "nord" {
		t.Errorf("the theme reports scheme %q, want nord", nord.Scheme.ID())
	}
	if def.Scheme.ID() != DefaultScheme.ID() {
		t.Errorf("a theme built with no WithScheme reports %q, want the default", def.Scheme.ID())
	}
}

// TestSchemeIsASettingNotACommand is what replaced the five scheme.* commands:
// appearance.scheme is a Setting offering every scheme, drawn in its own
// colours through Option.Style, and nothing named scheme.* is a command any
// more.
func TestSchemeIsASettingNotACommand(t *testing.T) {
	resetRegistry()
	t.Cleanup(resetRegistry)
	RegisterSetting(schemeSetting())

	for _, cmd := range Commands() {
		if strings.HasPrefix(cmd.ID, "scheme.") {
			t.Errorf("%s is still a command; schemes moved to the appearance.scheme setting", cmd.ID)
		}
	}

	s, ok := lookupSetting(t, "appearance.scheme")
	if !ok {
		t.Fatal("appearance.scheme is not registered")
	}
	want := make(map[string]bool, len(Schemes))
	for _, sc := range Schemes {
		want[sc.id] = false
	}
	for _, opt := range s.Options(Deps{}) {
		if _, ours := want[opt.ID]; !ours {
			t.Errorf("appearance.scheme offers an option nothing names: %q", opt.ID)
			continue
		}
		want[opt.ID] = true
		if opt.Label == "" {
			t.Errorf("option %q has no label", opt.ID)
		}
		if opt.Style == nil {
			t.Errorf("option %q has no preview style", opt.ID)
		}
	}
	for id, found := range want {
		if !found {
			t.Errorf("appearance.scheme does not offer %q", id)
		}
	}
}

func TestSwitchScheme_RebuildsTheStylesAndKeepsModeAndGlyphs(t *testing.T) {
	colourfulEnv(t)
	writeConfig(t, profileWithEverything)

	d := Deps{Theme: NewTheme(ThemeLight, false, ASCIIGlyphs()), Site: "example.atlassian.net"}
	msg := firstMsgOfType[ThemeMsg](t, SwitchScheme(d, nordScheme))
	switch {
	case msg.Theme == nil:
		t.Fatal("no theme came back")
	case msg.Theme.Scheme.ID() != "nord":
		t.Errorf("got scheme %q want nord", msg.Theme.Scheme.ID())
	case msg.Theme.Mode != ThemeLight:
		t.Errorf("the mode changed to %v; a scheme switch answers a question about colour, not about light or dark", msg.Theme.Mode)
	case msg.Theme.Dark:
		t.Error("a light theme reports itself dark after only its scheme changed")
	case msg.Theme.Glyphs.Bullet != ASCIIGlyphs().Bullet:
		t.Error("the glyph set was replaced; a scheme switch answers a question about colour, not about the font")
	case !hasColour(msg.Theme.Accent.Render("x")):
		t.Error("switching scheme left the styles colourless")
	}
}

func TestWriteScheme_ChangesTheSchemeAndNothingElseInTheProfile(t *testing.T) {
	path := writeConfig(t, profileWithEverything)

	if err := writeScheme("example.atlassian.net", nordScheme); err != nil {
		t.Fatalf("writeScheme: %v", err)
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
	case got.Scheme != "nord":
		t.Errorf("scheme is %q, want nord", got.Scheme)
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

func TestWriteScheme_WritesDefaultAsNoSchemeAtAll(t *testing.T) {
	path := writeConfig(t, strings.Replace(profileWithEverything, `glyphs = "ascii"`, `scheme = "nord"`, 1))

	if err := writeScheme("example.atlassian.net", DefaultScheme); err != nil {
		t.Fatalf("writeScheme: %v", err)
	}
	body, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(body), "scheme") {
		t.Errorf("default was written as a value rather than as an absence:\n%s", body)
	}
}

func TestWriteScheme_RefusesWhenTheSessionIsNotOnTheActiveProfilesSite(t *testing.T) {
	writeConfig(t, profileWithEverything)

	err := writeScheme("other.atlassian.net", nordScheme)
	if err == nil {
		t.Fatal("the scheme was written onto a profile this session is not running as")
	}
	if !strings.Contains(err.Error(), "other.atlassian.net") || !strings.Contains(err.Error(), "work") {
		t.Errorf("the refusal does not say which session and which profile: %v", err)
	}
}

func TestSaveScheme_SaysSoWhenThereIsNoProfileToSaveTo(t *testing.T) {
	t.Setenv("SARAL_CONFIG_DIR", t.TempDir())

	status := firstMsgOfType[StatusMsg](t, saveScheme("example.atlassian.net", nordScheme))
	if !strings.Contains(status.Text, "no profile") {
		t.Errorf("unhelpful message with nowhere to save: %q", status.Text)
	}
	if status.Level != LevelInfo {
		t.Errorf("a first run with no profile is reported at level %v, which reads as a failure", status.Level)
	}
}
