package issue

import (
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"

	"github.com/varijkapil13/saral/internal/ui/kernel"
	"github.com/varijkapil13/saral/pkg/jira"
)

// The status fact in the header carries its category's colour, the way a
// resting board card's key already does, rather than the one uniform muted
// colour every other fact still uses.
func TestHeader_TheStatusFactCarriesItsCategorysColour(t *testing.T) {
	t.Parallel()
	d := testDeps(nil)
	d.Theme = kernel.NewTheme(kernel.ThemeDark, true, kernel.UnicodeGlyphs())

	seed := jira.Issue{Key: "PROJ-1", Summary: "one", Status: jira.Status{Name: "Done", Category: jira.CategoryDone}}
	dr := newDriver(t, d, seed, 80, 24)

	raw := dr.m.header()
	if raw == ansi.Strip(raw) {
		t.Fatal("a header built from a colour theme carries no colour at all, so this test proves nothing")
	}

	want := dr.m.styles.category(jira.CategoryDone).Render(statusLabel(seed.Status))
	if !strings.Contains(raw, want) {
		t.Errorf("header %q does not contain the status rendered in its category colour %q", raw, want)
	}

	if mutedStatus := dr.m.styles.muted.Render(statusLabel(seed.Status)); mutedStatus != want && strings.Contains(raw, mutedStatus) {
		t.Error("the status is still rendered in the muted colour rather than its category's")
	}
}
