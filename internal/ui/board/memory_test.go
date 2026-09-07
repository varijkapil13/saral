package board

import (
	"encoding/json"
	"testing"

	"github.com/varijkapil13/saral/internal/app"
	"github.com/varijkapil13/saral/internal/ui/filter"
	"github.com/varijkapil13/saral/internal/ui/kernel"
	"github.com/varijkapil13/saral/pkg/jira"
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

// A remembered term is put in force before New reads the cache, so cards
// already stored under a project's last board are drawn already narrowed.
func TestNew_RestoresARememberedTermBeforeReadingTheCache(t *testing.T) {
	t.Parallel()

	boardID, cfg, qf, issues := primed(t, testDeps(newFake(6)))
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

	cache := newFakeCache()
	cache.hold("PROJ", boardID, app.BoardSnapshot{Config: cfg, QuickFilters: qf, Issues: issues}, false)

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
	mustContain(t, frame, issues[0].Key)
	mustNotContain(t, frame, issues[other].Key)
}

func TestNew_WithNoRememberedTermOpensUnfiltered(t *testing.T) {
	t.Parallel()

	d := withMemory(testDeps(newFake(6)), newFakeMemory())
	dr := newDriver(t, d, 120, 20)
	if len(dr.m.terms) != 0 {
		t.Errorf("terms = %+v, want none", dr.m.terms)
	}
}

// Toggling a term keeps it in this profile's memory too, the same Toggle the
// keyboard, the chips and the picker already agree is the one true way a
// term changes.
func TestApplyFilterTerm_KeepsItInMemory(t *testing.T) {
	t.Parallel()

	mem := newFakeMemory()
	dr := newDriver(t, withMemory(testDeps(newFake(6)), mem), 120, 20)

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

// A quick filter this board's live list still offers, and that was toggled on
// last time, comes back on once the live list is known — and the cards on
// screen are asked for again under it, since the very first read already ran
// without it.
func TestTookQuickFilters_ReappliesARememberedSelectionAndReloadsCards(t *testing.T) {
	t.Parallel()

	qfs := []jira.QuickFilter{{ID: 10, Name: "Mine", JQL: "assignee = currentUser()"}, {ID: 20, Name: "Bugs"}}
	mem := newFakeMemory()
	ids, err := json.Marshal([]int64{20})
	if err != nil {
		t.Fatal(err)
	}
	mem.state[ViewID+"."+quickFiltersMemoryKey] = string(ids)

	fake := newFake(6)
	dr := newDriver(t, withMemory(testDeps(fake), mem), 120, 20)
	before := countCalls(fake, "BoardIssues")

	dr.send(quickFiltersMsg{gen: dr.m.gen, filters: qfs})

	if !dr.m.qfOn[20] {
		t.Errorf("qfOn = %+v, want 20 turned on from memory", dr.m.qfOn)
	}
	if dr.m.qfOn[10] {
		t.Error("a quick filter never remembered came on by itself")
	}
	if got := countCalls(fake, "BoardIssues") - before; got == 0 {
		t.Error("the cards were not re-read once a remembered quick filter came on")
	}
}

// A quick filter id remembered from a different board, or one this board has
// since removed, must not wedge anything: it is simply never among the ones
// turned on.
func TestTookQuickFilters_AnUnknownRememberedIDIsIgnored(t *testing.T) {
	t.Parallel()

	mem := newFakeMemory()
	ids, err := json.Marshal([]int64{999})
	if err != nil {
		t.Fatal(err)
	}
	mem.state[ViewID+"."+quickFiltersMemoryKey] = string(ids)

	dr := newDriver(t, withMemory(testDeps(newFake(6)), mem), 120, 20)
	dr.send(quickFiltersMsg{gen: dr.m.gen, filters: []jira.QuickFilter{{ID: 10, Name: "Mine"}}})

	if len(dr.m.qfOn) != 0 {
		t.Errorf("qfOn = %+v, want nothing turned on", dr.m.qfOn)
	}
}

// Toggling a quick filter by hand keeps the selection in memory, in the
// board's own display order, the same way the terms picker's gesture does.
func TestToggleQuickFilter_KeepsTheSelectionInMemory(t *testing.T) {
	t.Parallel()

	mem := newFakeMemory()
	dr := newDriver(t, withMemory(testDeps(newFake(6)), mem), 120, 20)
	dr.send(quickFiltersMsg{gen: dr.m.gen, filters: []jira.QuickFilter{{ID: 10, Name: "Mine"}, {ID: 20, Name: "Bugs"}}})

	if !dr.m.toggleQuickFilter(2) {
		t.Fatal("toggleQuickFilter(2) reported nothing bound to it")
	}

	got, ok := mem.Recall(ViewID, quickFiltersMemoryKey)
	if !ok {
		t.Fatal("nothing was kept after toggling a quick filter")
	}
	var ids []int64
	if err := json.Unmarshal([]byte(got), &ids); err != nil || len(ids) != 1 || ids[0] != 20 {
		t.Errorf("kept %q, want just [20]", got)
	}

	if !dr.m.toggleQuickFilter(2) {
		t.Fatal("toggleQuickFilter(2) reported nothing bound to it on the way back off")
	}
	if _, ok := mem.Recall(ViewID, quickFiltersMemoryKey); ok {
		t.Error("turning the last quick filter back off left something kept")
	}
}
