package filterbar

import (
	"flag"
	"os"
	"path/filepath"
	"testing"

	tea "charm.land/bubbletea/v2"
	zone "github.com/lrstanley/bubblezone/v2"

	"github.com/varijkapil13/saral/internal/ui/uitest"
	"github.com/varijkapil13/saral/internal/ui/widget"
)

var update = flag.Bool("update", false, "rewrite the golden files")

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
		t.Fatalf("%v — run: go test ./internal/ui/widget/filterbar -update", err)
	}
	if string(want) != got {
		t.Errorf("line differs from %s\n--- want ---\n%q\n--- got ---\n%q", path, want, got)
	}
}

// markedBar is a bar backed by a real zone manager, the shape a running
// program always draws with.
func markedBar(tb testing.TB) (*Bar, widget.Zoner, *zone.Manager) {
	tb.Helper()
	mgr := zone.New()
	tb.Cleanup(mgr.Close)
	z := widget.NewZoner(mgr)
	return New(z), z, mgr
}

// pressOn scans the frame drawn now for one of its zones and clicks the first
// cell of it.
func pressOn(t *testing.T, mgr *zone.Manager, z widget.Zoner, frame func() string, name string) tea.MouseClickMsg {
	t.Helper()
	at := uitest.Zone(t, mgr, frame, z.ID(name))
	return tea.MouseClickMsg{X: at.StartX, Y: at.StartY, Button: tea.MouseLeft}
}
