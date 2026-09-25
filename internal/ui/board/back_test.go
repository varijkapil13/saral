package board

import (
	"testing"

	"github.com/varijkapil13/saral/internal/ui/filter"
	"github.com/varijkapil13/saral/internal/ui/kernel"
	"github.com/varijkapil13/saral/internal/ui/uitest"
)

func TestBoard_EscClearsTheTermsWhileAnyAreInForce(t *testing.T) {
	t.Parallel()
	dr := newDriver(t, testDeps(newFake(6)), 120, 20)
	if dr.m.WantsBack() {
		t.Fatal("a board with nothing narrowing it claims esc")
	}
	dr.send(filter.ChosenMsg{Term: filter.Term{Facet: filter.FacetStatus, ID: dr.m.issues[0].Status.ID, Label: "x"}})
	if !dr.m.WantsBack() {
		t.Fatal("a narrowed board does not claim esc")
	}

	dr.key("esc")

	if len(dr.m.terms) != 0 {
		t.Errorf("terms after esc = %+v, want none", dr.m.terms)
	}
	if dr.m.WantsBack() {
		t.Error("the board still claims esc with nothing left to clear")
	}
}

func TestBoard_CardsAreMarkedAgainWhenTheMouseComesBack(t *testing.T) {
	t.Parallel()
	d := testDeps(newFake(6))
	d.Zones.SetEnabled(false)
	dr := newDriver(t, d, 120, 20)
	_ = dr.m.View()

	d.Zones.SetEnabled(true)
	dr.send(kernel.SetMouseMsg{Enabled: true})

	uitest.Zone(t, d.Zones, dr.m.View, dr.m.zones.ID(cardZone("PROJ-3")), dr.m.zones.ID(colZone(0)))
}
