package backlog

import (
	"testing"

	"github.com/varijkapil13/saral/internal/ui/filter"
	"github.com/varijkapil13/saral/internal/ui/kernel"
)

// fakeMemory is a kernel.Memory in a map — the real one is a profile-scoped
// file below internal/config, which a view may not import.
type fakeMemory struct{ state map[string]string }

func newFakeMemory() *fakeMemory { return &fakeMemory{state: map[string]string{}} }

func (f *fakeMemory) Recall(view, key string) (string, bool) {
	v, ok := f.state[view+"."+key]
	return v, ok
}

func (f *fakeMemory) Keep(view, key, value string) {
	k := view + "." + key
	if value == "" {
		delete(f.state, k)
		return
	}
	f.state[k] = value
}

func (f *fakeMemory) Forget() { f.state = map[string]string{} }

func withMemory(d kernel.Deps, mem kernel.Memory) kernel.Deps {
	d.Memory = mem
	return d
}

// A remembered term is put in force before New reads the cache, so issues
// already stored under a project's last board are drawn already narrowed.
func TestNew_RestoresARememberedTermBeforeReadingTheCache(t *testing.T) {
	t.Parallel()

	boardID, snap := primed(t, testDeps(newFake(6)))
	if len(snap.Issues) < 2 {
		t.Fatalf("primed only %d issues, need at least two with differing status", len(snap.Issues))
	}
	term := filter.Term{Facet: filter.FacetStatus, ID: snap.Issues[0].Status.ID, Label: snap.Issues[0].Status.Name}
	other := -1
	for i, iss := range snap.Issues {
		if iss.Status.ID != term.ID {
			other = i
			break
		}
	}
	if other < 0 {
		t.Fatal("every primed issue shares one status, so this proves nothing about narrowing")
	}

	cache := newFakeCache()
	cache.hold("PROJ", boardID, snap, false)

	mem := newFakeMemory()
	mem.state[ViewID+"."+termsMemoryKey] = (filter.Terms{term}).Encode()

	d := withMemory(withCache(testDeps(refusing(6)), cache), mem)
	view, ok := New(d).(*Model)
	if !ok {
		t.Fatal("New did not return a *Model")
	}
	if len(view.terms) != 1 || !view.terms.Has(term) {
		t.Fatalf("terms after New = %+v, want just %+v", view.terms, term)
	}
	next, _ := view.Update(kernel.SizeMsg{Width: 120, Height: 20})
	m := next.(*Model)
	frame := m.View()
	mustContain(t, frame, snap.Issues[0].Key)
	mustNotContain(t, frame, snap.Issues[other].Key)
}

func TestNew_WithNoRememberedTermOpensUnfiltered(t *testing.T) {
	t.Parallel()

	d := withMemory(testDeps(newFake(6)), newFakeMemory())
	dr := newDriver(t, d, 120, 20)
	if len(dr.m.terms) != 0 {
		t.Errorf("terms = %+v, want none", dr.m.terms)
	}
}

func TestApplyFilterTerm_KeepsItInMemory(t *testing.T) {
	t.Parallel()

	mem := newFakeMemory()
	dr := newDriver(t, withMemory(testDeps(newFake(6)), mem), 120, 20)
	if len(dr.m.issues) == 0 {
		t.Fatal("nothing to build a term from")
	}
	term := filter.Term{Facet: filter.FacetStatus, ID: dr.m.issues[0].Status.ID, Label: dr.m.issues[0].Status.Name}
	dr.send(filter.ChosenMsg{Term: term})

	got, ok := mem.Recall(ViewID, termsMemoryKey)
	if !ok {
		t.Fatal("nothing was kept after applying a term")
	}
	terms, ok := filter.DecodeTerms(got)
	if !ok || len(terms) != 1 || !terms.Has(term) {
		t.Errorf("kept %q, does not decode to just %+v", got, term)
	}
}

func TestClearFilter_ForgetsItInMemory(t *testing.T) {
	t.Parallel()

	mem := newFakeMemory()
	dr := newDriver(t, withMemory(testDeps(newFake(6)), mem), 120, 20)
	if len(dr.m.issues) == 0 {
		t.Fatal("nothing to build a term from")
	}
	term := filter.Term{Facet: filter.FacetStatus, ID: dr.m.issues[0].Status.ID, Label: dr.m.issues[0].Status.Name}
	dr.send(filter.ChosenMsg{Term: term})
	if _, ok := mem.Recall(ViewID, termsMemoryKey); !ok {
		t.Fatal("setup: the term was not kept")
	}

	dr.send(ClearFilterMsg{})
	if _, ok := mem.Recall(ViewID, termsMemoryKey); ok {
		t.Error("the cleared filter is still kept in memory")
	}
}
