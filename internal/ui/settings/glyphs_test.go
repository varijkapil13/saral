package settings

import (
	"strings"
	"testing"

	"github.com/varijkapil13/saral/internal/ui/kernel"
)

func glyphSetting() kernel.Setting {
	return kernel.Setting{
		ID: glyphsSettingID, Section: "Appearance", Order: 2, Title: "Glyphs",
		Summary: "which icons are drawn", Kind: kernel.KindChoice, Scope: kernel.ScopeProfile,
		Options: func(kernel.Deps) []kernel.SettingOption {
			return []kernel.SettingOption{{ID: "nerd", Label: "nerd font"}, {ID: "unicode", Label: "unicode"}, {ID: "ascii", Label: "ascii"}}
		},
		Value: func(d kernel.Deps) string { return d.Theme.Glyphs.Tier() },
	}
}

func TestGlyphPreview_NamesEveryIconOfEachTier(t *testing.T) {
	t.Parallel()
	for _, tier := range []struct {
		name   string
		glyphs kernel.Glyphs
	}{
		{"nerd", kernel.NerdGlyphs()},
		{"unicode", kernel.UnicodeGlyphs()},
		{"ascii", kernel.ASCIIGlyphs()},
	} {
		t.Run(tier.name, func(t *testing.T) {
			t.Parallel()
			g := tier.glyphs
			got := glyphPreview(g)
			for _, icon := range append([]string{
				g.TypeEpic, g.TypeStory, g.TypeTask, g.TypeBug, g.TypeSubtask, g.TypeOther,
				g.CategoryToDo, g.CategoryInProgress, g.CategoryDone, g.CategoryUnknown,
			}, g.Spinner...) {
				if !strings.Contains(got, icon) {
					t.Errorf("the %s preview %q leaves out %q", tier.name, got, icon)
				}
			}
		})
	}
}

func TestSettings_TheGlyphsRowDrawsTheTierInForceBeneathIt(t *testing.T) {
	st := &fakeState{theme: "dark", scheme: "default", mouse: true}
	all, sections := sampleSettings(st)
	all = append(all, glyphSetting())
	p := fly(t, settingsDeps(noColorTheme()), all, sections, 120, 40)
	frame := p.frame()
	if !strings.Contains(frame, glyphPreview(kernel.ASCIIGlyphs())[:20]) {
		t.Fatalf("the screen does not preview the ASCII tier under its Glyphs row:\n%s", frame)
	}
	lines := strings.Split(frame, "\n")
	at := -1
	for i, line := range lines {
		if strings.Contains(line, "which icons are drawn") {
			at = i
		}
	}
	if at < 0 || at+1 >= len(lines) || !strings.Contains(lines[at+1], kernel.ASCIIGlyphs().TypeEpic) {
		t.Fatalf("the preview is not the line straight under the Glyphs row's summary:\n%s", frame)
	}
	golden(t, "settings_120x40_glyphs_preview.golden", frame)
}
