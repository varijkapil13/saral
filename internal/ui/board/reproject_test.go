package board

import (
	"strings"
	"testing"

	"github.com/varijkapil13/saral/internal/ui/filter"
	"github.com/varijkapil13/saral/internal/ui/kernel"
	"github.com/varijkapil13/saral/pkg/jira/jiratest"
)

// A status is minted per project, so a term naming one cannot follow a switch
// to another project: it comes off, the switch says so, and it is still there
// for the project it was about when the session comes back to it.
func TestReproject_TermsComeOffAndStayWithTheProjectTheyWereAbout(t *testing.T) {
	t.Parallel()
	mem := newFakeMemory()
	f := newFake(6, jiratest.WithProject("OPS", jiratest.Scrum))
	dr := newDriver(t, withMemory(testDeps(f), mem), 120, 20)
	if len(dr.m.issues) == 0 {
		t.Fatal("nothing loaded to narrow")
	}
	term := filter.Term{Facet: filter.FacetStatus, ID: dr.m.issues[0].Status.ID, Label: dr.m.issues[0].Status.Name}
	dr.send(filter.ChosenMsg{Term: term})
	if len(dr.m.terms) != 1 {
		t.Fatalf("setup: terms = %+v", dr.m.terms)
	}

	dr.send(kernel.ProjectMsg{Project: "OPS"})

	if len(dr.m.terms) != 0 {
		t.Errorf("terms after the switch = %+v, want none: they named a status of PROJ", dr.m.terms)
	}
	mustContain(t, dr.lastStatus().Text, "the filters were about PROJ, so they came off with it")
	if _, kept := mem.Recall(ViewID, "terms:OPS"); kept {
		t.Error("the terms about PROJ were kept for OPS")
	}
	enc, kept := mem.Recall(ViewID, "terms:PROJ")
	if back, ok := filter.DecodeTerms(enc); !kept || !ok || !back.Has(term) {
		t.Errorf("PROJ's terms were not kept for it: %q", enc)
	}
	mustNotContain(t, dr.view(), "status: "+term.Label)
	golden(t, "post_switch_120x20.golden", dr.view())

	dr.statuses = nil
	dr.send(kernel.ProjectMsg{Project: "PROJ"})

	if len(dr.m.terms) != 1 || !dr.m.terms.Has(term) {
		t.Errorf("terms back on PROJ = %+v, want %+v again", dr.m.terms, term)
	}
	for _, said := range dr.statuses {
		if strings.Contains(said.Text, "came off") {
			t.Errorf("switching from a project with no terms said %q", said.Text)
		}
	}
}
