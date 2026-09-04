package filterbar

import (
	"flag"
	"os"
	"path/filepath"
	"runtime"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	zone "github.com/lrstanley/bubblezone/v2"

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

// pressOn scans a rendered line for one of its zones and clicks the first cell
// of it. The manager records a zone on its own goroutine, so the zone is
// waited for rather than assumed.
func pressOn(t *testing.T, mgr *zone.Manager, z widget.Zoner, line, name string) tea.MouseClickMsg {
	t.Helper()
	_ = mgr.Scan(line)
	id := z.ID(name)
	eventually(t, func() bool { return !mgr.Get(id).IsZero() })
	at := mgr.Get(id)
	return tea.MouseClickMsg{X: at.StartX, Y: at.StartY, Button: tea.MouseLeft}
}

func eventually(t *testing.T, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for !cond() {
		if time.Now().After(deadline) {
			t.Fatal("condition never became true")
		}
		runtime.Gosched()
	}
}
