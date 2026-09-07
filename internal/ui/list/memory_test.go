package list

import (
	"strings"
	"testing"

	"github.com/varijkapil13/saral/internal/ui/filter"
	"github.com/varijkapil13/saral/internal/ui/kernel"
	"github.com/varijkapil13/saral/pkg/jira"
)

// fakeMemory is a kernel.Memory in a map. The real one is a profile-scoped
// file below internal/config, which a view may not import — the same reason
// fakeCache is a map instead of the real bbolt-backed one.
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

// A remembered filter is what the search on screen is built from before
// anything is asked of the site, so the first frame narrows exactly as the
// last session left it and the filter bar says so.
func TestNew_RestoresARememberedFilterAndDrawsItsChip(t *testing.T) {
	t.Parallel()

	mem := newFakeMemory()
	mem.state[ViewID+"."+termsMemoryKey] = filter.Terms{shipped}.Encode()
	d := withMemory(testDeps(newFake(5)), mem)

	dr := newDriver(t, d, 120, 30)

	if len(dr.m.terms) != 1 || !dr.m.terms.Has(shipped) {
		t.Fatalf("terms after New = %+v, want just %+v", dr.m.terms, shipped)
	}
	if !strings.Contains(dr.m.jql, `status = "10203"`) {
		t.Errorf("jql = %q, does not narrow by the remembered status", dr.m.jql)
	}
	if !strings.Contains(dr.view(), "Shipped") {
		t.Errorf("the filter bar does not draw the remembered chip:\n%s", dr.view())
	}
}

// A session with nothing remembered opens exactly as it always has.
func TestNew_WithNoRememberedFilterOpensOnTheDefault(t *testing.T) {
	t.Parallel()

	d := withMemory(testDeps(newFake(5)), newFakeMemory())
	dr := newDriver(t, d, 120, 30)

	if len(dr.m.terms) != 0 {
		t.Errorf("terms = %+v, want none", dr.m.terms)
	}
	if !dr.m.defaulted {
		t.Error("defaulted is false with nothing remembered to have turned it off")
	}
}

// fromCache is what draws the first frame, and it has to be asked for the
// rows of the exact query the remembered terms build — not the plain
// default — or a cache hit for the filtered search is missed on every start.
func TestNew_RestoresTheFilterBeforeReadingTheCache(t *testing.T) {
	t.Parallel()

	mem := newFakeMemory()
	mem.state[ViewID+"."+termsMemoryKey] = filter.Terms{shipped}.Encode()
	d := testDeps(nil)
	d.Memory = mem
	jql, _ := termQuery(d.Project, filter.Terms{shipped})
	jql = applySort(jql, sortChoice{})

	cache := newFakeCache()
	cache.hold(jql, []jira.Issue{{Key: "PROJ-9"}}, false, false)
	d.Cache = cache

	dr := newDriver(t, d, 120, 30)
	if !dr.m.loaded {
		t.Fatal("the cached rows for the filtered query were not painted")
	}
	if len(dr.m.issues) != 1 || dr.m.issues[0].Key != "PROJ-9" {
		t.Errorf("issues = %+v, want the row cached under the filtered query", dr.m.issues)
	}
}

// Toggling a term through the picker's gesture keeps it in memory too, not
// just on screen — the same Toggle the keyboard, the chips and the picker
// all already agree is the one true way a term changes.
func TestApplyTerm_KeepsItInMemory(t *testing.T) {
	t.Parallel()

	mem := newFakeMemory()
	dr := openAll(t, withMemory(testDeps(newFake(5)), mem), 120, 30)

	dr.send(filter.ChosenMsg{Term: shipped})

	got, ok := mem.Recall(ViewID, termsMemoryKey)
	if !ok {
		t.Fatal("nothing was kept after applying a term")
	}
	terms, ok := filter.DecodeTerms(got)
	if !ok || len(terms) != 1 || !terms.Has(shipped) {
		t.Errorf("kept %q, does not decode to just %+v", got, shipped)
	}
}

// Dropping the last term is remembering nothing, the same "no choice a
// gesture could have produced" reading a zero split or a blank sort already
// gets — so the next session must not restore a filter this one cleared.
func TestClearFilter_ForgetsItInMemory(t *testing.T) {
	t.Parallel()

	mem := newFakeMemory()
	mem.state[ViewID+"."+termsMemoryKey] = filter.Terms{shipped}.Encode()
	dr := newDriver(t, withMemory(testDeps(newFake(5)), mem), 120, 30)

	dr.send(ClearFilterMsg{})

	if _, ok := mem.Recall(ViewID, termsMemoryKey); ok {
		t.Error("the cleared filter is still kept in memory")
	}
}

// An id a remembered term names that this site no longer knows is found out
// by asking, and the answer is a 400 the fake stands in for with FailNext.
// The view has to recover on its own: drop the terms, remember that they are
// gone, and fall back to the default search rather than sitting on a refusal
// nobody chose.
func TestRecalledTerms_DroppedWhenTheSiteRefusesThem(t *testing.T) {
	t.Parallel()

	mem := newFakeMemory()
	mem.state[ViewID+"."+termsMemoryKey] = filter.Terms{shipped}.Encode()
	f := newFake(0)
	f.FailNext(&jira.ValidationError{Fields: []jira.FieldError{
		{Field: "jql", Message: `the value '10203' does not exist for the field 'status'`},
	}})
	d := withMemory(testDeps(f), mem)

	dr := newDriver(t, d, 120, 30)

	if len(dr.m.terms) != 0 {
		t.Errorf("terms after the refusal = %+v, want none", dr.m.terms)
	}
	if _, ok := mem.Recall(ViewID, termsMemoryKey); ok {
		t.Error("the refused filter is still kept in memory")
	}
	if !dr.said("no longer work here") {
		t.Errorf("nothing told the user the remembered filter was dropped; statuses: %+v", dr.statuses)
	}
	if dr.m.failure != nil {
		t.Errorf("the pane is still showing a refusal after falling back: %v", dr.m.failure)
	}
}

// A failure that has nothing to do with the remembered ids — a host that is
// not answering — must not be read as a verdict on them, or a term this site
// still knows perfectly well would be dropped over an ordinary network blip.
func TestRecalledTerms_KeptWhenTheFailureIsNotAValidationError(t *testing.T) {
	t.Parallel()

	mem := newFakeMemory()
	want := (filter.Terms{shipped}).Encode()
	mem.state[ViewID+"."+termsMemoryKey] = want
	f := newFake(0)
	f.FailNext(refusedConnection())
	d := withMemory(testDeps(f), mem)

	dr := newDriver(t, d, 120, 30)

	if len(dr.m.terms) != 1 || !dr.m.terms.Has(shipped) {
		t.Errorf("terms after a connection failure = %+v, want the remembered term kept", dr.m.terms)
	}
	if got, ok := mem.Recall(ViewID, termsMemoryKey); !ok || got != want {
		t.Errorf("memory = %q, ok=%v, want the term still kept", got, ok)
	}
}

// A second failure — a poll, a later retry — is an ordinary failure and must
// not be re-examined as a verdict on ids remembered from last time: that
// check happens once, on the first answer, and never again.
func TestRecalledTerms_ASecondFailureIsOrdinary(t *testing.T) {
	t.Parallel()

	mem := newFakeMemory()
	mem.state[ViewID+"."+termsMemoryKey] = (filter.Terms{shipped}).Encode()
	f := newFake(3)
	d := withMemory(testDeps(f), mem)

	dr := newDriver(t, d, 120, 30)
	if len(dr.m.terms) != 1 {
		t.Fatalf("the first (successful) load did not keep the recalled term: %+v", dr.m.terms)
	}

	f.FailNext(&jira.ValidationError{Fields: []jira.FieldError{{Field: "jql", Message: "boom"}}})
	dr.send(kernel.RefreshMsg{})

	if len(dr.m.terms) != 1 || !dr.m.terms.Has(shipped) {
		t.Errorf("a later validation failure dropped a term already confirmed valid: %+v", dr.m.terms)
	}
}
