package richtext

import (
	"flag"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/varijkapil13/saral/pkg/adf"
)

var update = flag.Bool("update", false, "rewrite the golden files")

// load reads one of the stored documents. kitchen.json is what a real site
// stored when it was sent every node type it accepts; edges.json is the shapes
// a site keeps but will not accept, which is how an app's own nodes arrive.
func load(t testing.TB, name string) adf.Doc {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join("testdata", name)) //nolint:gosec // a literal under testdata
	if err != nil {
		t.Fatal(err)
	}
	d, err := adf.Unmarshal(raw)
	if err != nil {
		t.Fatal(err)
	}
	return d
}

// plainPalette is the theme the layout goldens are written under: attributes
// only, no colour, so that a golden diff is about the document and never about
// a hex value somebody changed in the kernel.
func plainPalette() Palette {
	base := lipgloss.NewStyle()
	return Palette{
		Base:    base,
		Muted:   base.Faint(true),
		Title:   base.Bold(true),
		Accent:  base.Bold(true),
		Danger:  base.Bold(true),
		Warning: base.Bold(true),
		Success: base.Bold(true),
		Badge:   base.Reverse(true),
		Color:   false,
	}
}

// colourPalette is the theme the styling assertions use. Each token is a
// different colour so that a test can say which one a run was rendered in.
func colourPalette() Palette {
	return Palette{
		Base:    lipgloss.NewStyle().Foreground(lipgloss.Color("#101010")),
		Muted:   lipgloss.NewStyle().Foreground(lipgloss.Color("#202020")),
		Title:   lipgloss.NewStyle().Foreground(lipgloss.Color("#303030")),
		Accent:  lipgloss.NewStyle().Foreground(lipgloss.Color("#404040")),
		Danger:  lipgloss.NewStyle().Foreground(lipgloss.Color("#505050")),
		Warning: lipgloss.NewStyle().Foreground(lipgloss.Color("#606060")),
		Success: lipgloss.NewStyle().Foreground(lipgloss.Color("#707070")),
		Badge:   lipgloss.NewStyle().Foreground(lipgloss.Color("#808080")),
		Color:   true,
	}
}

func options(width int) Options {
	return Options{
		Width:    width,
		Location: time.UTC,
		Styles:   NewStyles(plainPalette()),
		Markers:  UnicodeMarkers(),
	}
}

// stripped is what a golden file holds: the layout, with the styling taken off,
// which is the convention the other views' goldens follow.
func stripped(r Rendered) string {
	var b strings.Builder
	for i, line := range r.Lines {
		b.WriteString(ansi.Strip(line))
		if i < len(r.Lines)-1 {
			b.WriteString("\n")
		}
	}
	return b.String()
}

func golden(t *testing.T, name, got string) {
	t.Helper()
	path := filepath.Join("testdata", name)
	if *update {
		if err := os.WriteFile(path, []byte(got), 0o600); err != nil {
			t.Fatal(err)
		}
		return
	}
	want, err := os.ReadFile(path) //nolint:gosec // a literal under testdata
	if err != nil {
		t.Fatalf("%v — run: go test ./internal/ui/richtext -update", err)
	}
	if string(want) != got {
		t.Errorf("output differs from %s\n--- want ---\n%s\n--- got ---\n%s", path, want, got)
	}
}

func doc(nodes ...adf.Node) adf.Doc { return adf.NewDoc(nodes...) }

func para(text string) adf.Node {
	return adf.NewNode("paragraph", adf.NewText(text))
}
