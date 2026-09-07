package kernel

import (
	"testing"

	"github.com/varijkapil13/saral/pkg/jira"
)

// fakeMemory is a Memory in a map, the same in-process fake pattern every
// package's own fakeCache already is: the real one is a profile-scoped file
// below internal/config, which this package may not import to build one.
type fakeMemory struct {
	state map[string]string
	kept  []string // view+"."+key, in the order Keep was called, for call-count assertions
}

func newFakeMemory() *fakeMemory { return &fakeMemory{state: map[string]string{}} }

func (f *fakeMemory) Recall(view, key string) (string, bool) {
	v, ok := f.state[view+"."+key]
	return v, ok
}

func (f *fakeMemory) Keep(view, key, value string) {
	f.kept = append(f.kept, view+"."+key)
	k := view + "." + key
	if value == "" {
		delete(f.state, k)
		return
	}
	f.state[k] = value
}

func (f *fakeMemory) Forget() { f.state = map[string]string{} }

func TestStartView_PrefersTheRecalledRootWhenItIsStillAvailable(t *testing.T) {
	resetRegistry()
	t.Cleanup(resetRegistry)
	RegisterView(spec("board", 1, "", &stubView{id: "board"}))
	RegisterView(spec("backlog", 2, "", &stubView{id: "backlog"}))

	mem := newFakeMemory()
	mem.state["kernel.view"] = "backlog"
	d := testDeps()
	d.Memory = mem

	m := newAt(t, d, 100, 24)
	if got := m.top().spec.ID; got != "backlog" {
		t.Errorf("opened on %q, want the recalled root backlog", got)
	}
}

// A capability the token no longer has must not wedge the session on a view
// it cannot see: the first available slotted root is what a build with no
// memory at all opens on, and a remembered root that has stopped being
// available falls back to exactly that.
func TestStartView_FallsBackWhenTheRecalledRootIsNoLongerAvailable(t *testing.T) {
	resetRegistry()
	t.Cleanup(resetRegistry)
	RegisterView(spec("board", 1, "", &stubView{id: "board"}))
	RegisterView(spec("plans", 2, jira.CapPlans, &stubView{id: "plans"}))

	mem := newFakeMemory()
	mem.state["kernel.view"] = "plans"
	d := testDeps()
	d.Memory = mem
	d.Caps = jira.Capabilities{} // no Plans

	m := newAt(t, d, 100, 24)
	if got := m.top().spec.ID; got != "board" {
		t.Errorf("opened on %q, want the fallback board", got)
	}
}

// A remembered id nothing in this build registers — an older build's view, or
// a file edited by hand — is read as no preference rather than refused.
func TestStartView_FallsBackWhenTheRecalledRootIsNotRegistered(t *testing.T) {
	resetRegistry()
	t.Cleanup(resetRegistry)
	RegisterView(spec("board", 1, "", &stubView{id: "board"}))

	mem := newFakeMemory()
	mem.state["kernel.view"] = "sometimeslong-gone-view"
	d := testDeps()
	d.Memory = mem

	m := newAt(t, d, 100, 24)
	if got := m.top().spec.ID; got != "board" {
		t.Errorf("opened on %q, want the fallback board", got)
	}
}

// Only a root may be landed on this way: a view reachable only by being
// pushed — the filter picker, the palette — has nothing to be a starting
// point, and a stale or hand-edited record naming one must not open it as
// the root of the session.
func TestStartView_NeverLandsOnAPushOnlyView(t *testing.T) {
	resetRegistry()
	t.Cleanup(resetRegistry)
	RegisterView(spec("board", 1, "", &stubView{id: "board"}))
	RegisterView(spec("filter", 0, "", &stubView{id: "filter"}))

	mem := newFakeMemory()
	mem.state["kernel.view"] = "filter"
	d := testDeps()
	d.Memory = mem

	m := newAt(t, d, 100, 24)
	if got := m.top().spec.ID; got != "board" {
		t.Errorf("opened on %q, want the fallback board", got)
	}
}

// An explicit view — a CLI argument, onboarding's WithInitialView — keeps
// winning over anything remembered: the session was told, in this run, to
// open somewhere specific.
func TestStartView_ExplicitInitialViewWinsOverTheRecalledRoot(t *testing.T) {
	resetRegistry()
	t.Cleanup(resetRegistry)
	RegisterView(spec("board", 1, "", &stubView{id: "board"}))
	RegisterView(spec("backlog", 2, "", &stubView{id: "backlog"}))

	mem := newFakeMemory()
	mem.state["kernel.view"] = "backlog"
	d := testDeps()
	d.Memory = mem

	m := newAt(t, d, 100, 24, WithInitialView("board"))
	if got := m.top().spec.ID; got != "board" {
		t.Errorf("opened on %q, want the explicit board", got)
	}
}

func TestOpen_RemembersTheRootItSwitchedTo(t *testing.T) {
	resetRegistry()
	t.Cleanup(resetRegistry)
	RegisterView(spec("board", 1, "", &stubView{id: "board"}))
	RegisterView(spec("backlog", 2, "", &stubView{id: "backlog"}))

	mem := newFakeMemory()
	d := testDeps()
	d.Memory = mem

	m := newAt(t, d, 100, 24)
	if _, _ = m.Update(OpenMsg{ID: "backlog"}); mem.state["kernel.view"] != "backlog" {
		t.Errorf("kernel.view = %q, want backlog", mem.state["kernel.view"])
	}
}

// New itself opens a root — the first slotted one, absent any preference —
// and that is remembered too, so a session that never switches still has
// something to reopen on next time.
func TestNew_RemembersTheRootItOpenedOn(t *testing.T) {
	resetRegistry()
	t.Cleanup(resetRegistry)
	RegisterView(spec("board", 1, "", &stubView{id: "board"}))

	mem := newFakeMemory()
	d := testDeps()
	d.Memory = mem

	newAt(t, d, 100, 24)
	if mem.state["kernel.view"] != "board" {
		t.Errorf("kernel.view = %q, want board", mem.state["kernel.view"])
	}
}

func TestForgetMemorySetting_ClearsWhatWasKept(t *testing.T) {
	resetRegistry()
	t.Cleanup(resetRegistry)
	RegisterSetting(forgetMemorySetting())

	mem := newFakeMemory()
	mem.state["kernel.view"] = "board"
	mem.state["list.terms"] = `[{"facet":"status","id":"1","label":"Done"}]`
	d := Deps{Memory: mem}

	setting, ok := lookupSetting(t, "session.memory")
	if !ok {
		t.Fatal("session.memory is not registered")
	}
	if setting.Run == nil {
		t.Fatal("session.memory has no Run")
	}
	firstMsgOfType[StatusMsg](t, setting.Run(d))
	if len(mem.state) != 0 {
		t.Errorf("memory still holds %v after Forget", mem.state)
	}
}

func TestForgetMemorySetting_WarnsRatherThanPanicsWithNoMemory(t *testing.T) {
	resetRegistry()
	t.Cleanup(resetRegistry)
	RegisterSetting(forgetMemorySetting())

	setting, ok := lookupSetting(t, "session.memory")
	if !ok {
		t.Fatal("session.memory is not registered")
	}
	msg := firstMsgOfType[StatusMsg](t, setting.Run(Deps{}))
	if msg.Level != LevelWarn {
		t.Errorf("level = %v, want a warning with no memory to forget", msg.Level)
	}
}
