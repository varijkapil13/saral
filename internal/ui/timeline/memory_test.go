package timeline

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

func withCache(d kernel.Deps, c *memCache) kernel.Deps {
	d.Cache = c
	return d
}

// A remembered term is put in force before New reads the cache, so issues
// already stored under this project's search are drawn already narrowed.
func TestNew_RestoresARememberedTermBeforeReadingTheCache(t *testing.T) {
	t.Parallel()

	primed := newDriver(t, testDeps(newFake(6)), 120, 30)
	issues := primed.m.issues
	if len(issues) < 2 {
		t.Fatalf("primed only %d issues, need at least two with differing status", len(issues))
	}
	term := filter.Term{Facet: filter.FacetStatus, ID: issues[0].Status.ID, Label: issues[0].Status.Name}
	other := -1
	for i, iss := range issues {
		if iss.Status.ID != term.ID {
			other = i
			break
		}
	}
	if other < 0 {
		t.Fatal("every primed issue shares one status, so this proves nothing about narrowing")
	}

	jql, _ := defaultQuery("PROJ")
	cache := newMemCache()
	if err := cache.PutRows(jql, issues, false); err != nil {
		t.Fatalf("PutRows: %v", err)
	}

	mem := newFakeMemory()
	mem.state[ViewID+"."+termsMemoryKey] = (filter.Terms{term}).Encode()

	d := withMemory(withCache(testDeps(newFake(0)), cache), mem)
	view, ok := New(d).(*Model)
	if !ok {
		t.Fatal("New did not return a *Model")
	}
	if len(view.terms) != 1 || !view.terms.Has(term) {
		t.Fatalf("terms after New = %+v, want just %+v", view.terms, term)
	}
	if view.filteredOut == 0 {
		t.Error("nothing was filtered out, so the remembered term was not applied to the cached rows")
	}
}

func TestNew_WithNoRememberedTermOpensUnfiltered(t *testing.T) {
	t.Parallel()

	d := withMemory(testDeps(newFake(6)), newFakeMemory())
	dr := newDriver(t, d, 120, 30)
	if len(dr.m.terms) != 0 {
		t.Errorf("terms = %+v, want none", dr.m.terms)
	}
}

func TestApplyFilterTerm_KeepsItInMemory(t *testing.T) {
	t.Parallel()

	mem := newFakeMemory()
	dr := newDriver(t, withMemory(testDeps(newFake(6)), mem), 120, 30)
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
	dr := newDriver(t, withMemory(testDeps(newFake(6)), mem), 120, 30)
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
