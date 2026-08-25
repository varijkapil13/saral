package ui

import (
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/varijkapil13/saral/internal/ui/kernel"
	"github.com/varijkapil13/saral/internal/ui/list"
)

var update = flag.Bool("update", false, "rewrite the golden files")

// This is the only place the whole program's chrome can be drawn: the kernel may
// not import a view, a view package sees only itself, and the row is shared by all
// of them. It is also the only build with the palette registered, so it is the
// only place ctrl+k is honestly on the row.
func viewsUnderTest() []string {
	scopes := append([]string(nil), kernel.KeyScopes()...)
	scopes = append(scopes, kernel.PaletteViewID)
	sort.Strings(scopes)
	return scopes
}

// build makes one view the way the program does. The two panes that are opened
// with an issue are given one; the rest are built from the registry.
func build(t *testing.T, id string) kernel.View {
	t.Helper()
	if construct, ok := keyReporters[id]; ok {
		return construct(depsFor(t))
	}
	if static, ok := staticKeys[id]; ok {
		return static.build(depsFor(t))
	}
	spec, ok := kernel.LookupView(id)
	if !ok || spec.New == nil {
		t.Fatalf("%s is registered with nothing that builds it", id)
	}
	return spec.New(depsFor(t))
}

// kernelWith is the program with one view on top, at one size. The root
// underneath is always the issue list, because that is where a session starts and
// what esc walks back to.
func kernelWith(t *testing.T, id string, w, h int) kernel.Model {
	t.Helper()
	m, err := kernel.New(depsFor(t), kernel.WithSize(w, h),
		kernel.WithInitialView(list.ViewID), kernel.WithMouse(false))
	if err != nil {
		t.Fatalf("kernel.New: %v", err)
	}
	next, _ := m.Update(tea.WindowSizeMsg{Width: w, Height: h})
	m = next.(kernel.Model)
	if id != list.ViewID {
		next, _ = m.Update(kernel.PushMsg{ID: id, Title: id, View: build(t, id)})
		m = next.(kernel.Model)
	}
	return m
}

func footerOf(t *testing.T, id string, w, h int) string {
	t.Helper()
	frame := ansi.Strip(kernelWith(t, id, w, h).Frame())
	lines := strings.Split(strings.TrimRight(frame, "\n"), "\n")
	return lines[len(lines)-1]
}

// Every view at the three sizes docs/UX.md cares about. Eighty is the documented
// minimum; a hundred is the width at which the help component overflows this row
// by exactly one column, which is where it used to be dropped whole.
func TestFooter_EveryViewAtEveryWidth(t *testing.T) {
	sweepEnv(t)
	for _, size := range []struct {
		label string
		w, h  int
	}{{"80x20", 80, 20}, {"100x28", 100, 28}, {"120x38", 120, 38}} {
		t.Run(size.label, func(t *testing.T) {
			var b strings.Builder
			for _, id := range viewsUnderTest() {
				fmt.Fprintf(&b, "%s\n%s\n", id, footerOf(t, id, size.w, size.h))
			}
			golden(t, "footer_"+size.label+".golden", b.String())
		})
	}
}

// The other half of the row: what ? says once the motions have moved into it.
// Every view's overlay leads with the actions the row shows, spelt out, and then
// the strokes the row has no space for. Nothing appears twice, which is what
// pushed the globals off the right of the screen while it did.
//
// A view taking typing is left out: ? is a character there, so it has no overlay.
func TestHelpOverlay_EveryView(t *testing.T) {
	sweepEnv(t)
	var b strings.Builder
	for _, id := range viewsUnderTest() {
		if c, ok := build(t, id).(kernel.KeyCapturer); ok && c.WantsRawKeys() {
			continue
		}
		fmt.Fprintf(&b, "%s\n%s\n\n", id, overlayOf(t, id, 120, 38))
	}
	golden(t, "overlay_120x38.golden", b.String())
}

// overlayOf is the body of the frame with the overlay up, trimmed of the blank
// rows below it so that a change in a view's height does not rewrite this.
func overlayOf(t *testing.T, id string, w, h int) string {
	t.Helper()
	m := kernelWith(t, id, w, h)
	next, _ := m.Update(tea.KeyPressMsg{Code: '?', Text: "?"})
	rows := strings.Split(ansi.Strip(next.(kernel.Model).Frame()), "\n")
	kept := make([]string, 0, len(rows))
	for _, row := range rows[1 : len(rows)-1] {
		if strings.TrimSpace(row) != "" {
			kept = append(kept, strings.TrimRight(row, " "))
		}
	}
	return strings.Join(kept, "\n")
}

// The regression, over the real views rather than a stub: at every width a
// terminal may be, every view still says how to get out. The globals are the row's
// last cell, so a row that lost them lost them off the right-hand end.
func TestFooter_EveryViewKeepsTheWayOutAtEveryWidth(t *testing.T) {
	sweepEnv(t)
	for _, id := range viewsUnderTest() {
		away := wayOut(t, id)
		for width := kernel.MinWidth; width <= 132; width++ {
			row := footerOf(t, id, width, 24)
			if !strings.HasSuffix(row, away) {
				t.Fatalf("%s at %d columns does not end in the way out:\n%q", id, width, row)
			}
			if got := ansi.StringWidth(row); got > width {
				t.Fatalf("%s at %d columns draws a %d-column row, which wraps:\n%q", id, width, got, row)
			}
		}
	}
}

// wayOut is the globals cell this view is entitled to. A view taking typing has
// swallowed every global but the one it is not allowed to, so its row honestly
// ends there; anything else offers help, the palette, and either back or quit.
func wayOut(t *testing.T, id string) string {
	t.Helper()
	if c, ok := build(t, id).(kernel.KeyCapturer); ok && c.WantsRawKeys() {
		return "ctrl+k"
	}
	if id == list.ViewID {
		return "? ctrl+k q"
	}
	return "? ctrl+k esc"
}

func golden(t *testing.T, name, got string) {
	t.Helper()
	path := filepath.Join("testdata", name)
	if *update {
		if err := os.MkdirAll("testdata", 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(got), 0o644); err != nil {
			t.Fatal(err)
		}
		return
	}
	want, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("%v — run: go test ./internal/ui -update", err)
	}
	if string(want) != got {
		t.Errorf("differs from %s\n--- want ---\n%s\n--- got ---\n%s", path, want, got)
	}
}
