package backlog

import (
	"testing"

	"github.com/varijkapil13/saral/internal/ui/filter"
	"github.com/varijkapil13/saral/internal/ui/kernel"
	"github.com/varijkapil13/saral/internal/ui/uitest"
)

func TestBacklog_EscClearsTheTermsWhileAnyAreInForce(t *testing.T) {
	t.Parallel()
	dr := newDriver(t, testDeps(newFake(6)), 120, 20)
	if dr.m.WantsBack() {
		t.Fatal("a backlog with nothing narrowing it claims esc")
	}
	dr.send(filter.ChosenMsg{Term: filter.Term{Facet: filter.FacetStatus, ID: dr.m.issues[0].Status.ID, Label: "x"}})
	if !dr.m.WantsBack() {
		t.Fatal("a narrowed backlog does not claim esc")
	}

	dr.key("esc")

	if len(dr.m.terms) != 0 {
		t.Errorf("terms after esc = %+v, want none", dr.m.terms)
	}
}

func TestBacklog_RowsAreMarkedAgainWhenTheMouseComesBack(t *testing.T) {
	t.Parallel()
	d := testDeps(newFake(6))
	d.Zones.SetEnabled(false)
	dr := newDriver(t, d, 120, 20)
	_ = dr.m.View()
	if len(dr.m.rows) < 2 {
		t.Fatal("nothing to point at")
	}

	d.Zones.SetEnabled(true)
	dr.send(kernel.SetMouseMsg{Enabled: true})

	uitest.Zone(t, d.Zones, dr.m.View, dr.m.zones.ID(dr.m.zoneOf(1)))
}
