package kernel

import (
	"strconv"
	"strings"
	"testing"

	"charm.land/lipgloss/v2"
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
	} {
		for _, r := range g {
			if r > 127 {
				t.Errorf("the ascii %s glyph %q is not ascii", name, g)
			}
		}
	}
	if GlyphsFor("").Bullet != UnicodeGlyphs().Bullet {
		t.Error("the default glyph set should be unicode")
	}
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
