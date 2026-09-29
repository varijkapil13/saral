package card

import (
	"flag"
	"os"
	"path/filepath"
	"testing"

	"github.com/varijkapil13/saral/internal/testsupport"
	"github.com/varijkapil13/saral/internal/ui/kernel"
)

func TestMain(m *testing.M) { os.Exit(testsupport.IsolateDirs(m)) }

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
		t.Fatalf("%v — run: go test ./internal/ui/widget/card -update", err)
	}
	if string(want) != got {
		t.Errorf("cards differ from %s\n--- want ---\n%s\n--- got ---\n%s", path, want, got)
	}
}

func listFacts(g kernel.Glyphs) Facts {
	return Facts{
		Key: "EX-1234", Summary: "Checkout fails when the basket is empty and the coupon field is still open",
		TypeGlyph: g.TypeBug, TypeName: "Bug", StatusGlyph: g.CategoryInProgress, StatusName: "In Review",
		Assignee: "Ada Lovelace", Priority: "High", Updated: "3d",
		Labels: []string{"checkout", "payments"}, FixVersions: []string{"2026.4"},
		Due: "2026-10-02", Subtasks: "2/5", Category: 2,
	}
}

func boardFacts(g kernel.Glyphs) Facts {
	return Facts{
		Key: "EX-87", Summary: "Cache the field catalogue between runs",
		TypeGlyph: g.TypeTask, Assignee: "Ben Adams", Priority: "Medium", Estimate: "5",
		ParentKey: "EX-12", ParentSummary: "Faster first paint", Category: 1,
	}
}

var states = []struct {
	name string
	st   State
}{
	{"resting", State{}},
	{"selected", State{Selected: true}},
	{"picked", State{Picked: true}},
	{"held", State{Held: true}},
}
