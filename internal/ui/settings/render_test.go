package settings

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/varijkapil13/saral/internal/ui/kernel"
)

// goldenSizes are the two widths docs/SETTINGS.md's definition of done asks
// for, at a height generous enough to show every sample setting without
// scrolling.
var goldenSizes = []struct {
	name string
	w, h int
}{
	{"80x24", 80, 24},
	{"120x30", 120, 30},
}

func golden(t *testing.T, name, got string) {
	t.Helper()
	path := filepath.Join("testdata", name)
	if *update {
		if err := os.MkdirAll("testdata", 0o750); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(got), 0o600); err != nil {
			t.Fatal(err)
		}
		return
	}
	want, err := os.ReadFile(path) //nolint:gosec // the path is a literal under testdata
	if err != nil {
		t.Fatalf("%v — run: go test ./internal/ui/settings -update", err)
	}
	if string(want) != got {
		t.Errorf("frame differs from %s\n--- want ---\n%s\n--- got ---\n%s", path, want, got)
	}
}

// TestGolden_TheScreenAtEveryWidthEveryThemeAndTwoCursors is
// docs/SETTINGS.md's definition of done: 80 and 120 columns, the default and
// the no-colour theme, with the cursor on a radio row and on a picker row.
func TestGolden_TheScreenAtEveryWidthEveryThemeAndTwoCursors(t *testing.T) {
	for _, size := range goldenSizes {
		for _, theme := range []struct {
			name  string
			build func() *kernel.Theme
		}{
			{"default", defaultTheme},
			{"nocolor", noColorTheme},
		} {
			for _, cursor := range []struct {
				name  string
				downs int
			}{
				{"radio", 0},  // the theme row
				{"picker", 1}, // the colour scheme row
			} {
				t.Run(size.name+"_"+theme.name+"_"+cursor.name, func(t *testing.T) {
					st := &fakeState{theme: "dark", scheme: "default", mouse: true}
					all, sections := sampleSettings(st)
					p := fly(t, settingsDeps(theme.build()), all, sections, size.w, size.h)
					p.press(repeat("down", cursor.downs)...)
					golden(t, "settings_"+size.name+"_"+theme.name+"_"+cursor.name+".golden", p.frame())
				})
			}
		}
	}
}

func repeat(s string, n int) []string {
	out := make([]string, n)
	for i := range out {
		out[i] = s
	}
	return out
}
