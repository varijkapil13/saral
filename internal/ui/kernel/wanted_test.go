package kernel

import (
	"strings"
	"testing"

	"github.com/varijkapil13/saral/pkg/jira"
)

// wantedRegistry is a build whose first slot is ungated and whose board needs a
// capability nothing has been asked about yet.
func wantedRegistry(t *testing.T) (issues, board *stubView) {
	t.Helper()
	resetRegistry()
	t.Cleanup(resetRegistry)
	issues, board = &stubView{id: "issues"}, &stubView{id: "board"}
	RegisterView(spec("issues", 1, "", issues))
	RegisterView(spec("board", 2, jira.CapBoards, board))
	RegisterView(spec("backlog", 3, "", &stubView{id: "backlog"}))
	return issues, board
}

func unprobedDeps(mem *fakeMemory) Deps {
	d := testDeps()
	d.Caps = jira.Capabilities{}
	d.Memory = mem
	return d
}

func probed(m Model, caps jira.Capabilities) Model {
	next, _ := m.Update(capsProbedMsg{seq: m.capsSeq, caps: caps})
	return next.(Model)
}

var boardsAllowed = jira.Capabilities{Boards: jira.Capability{OK: true}}

func TestWanted_AViewNamedBeforeTheProbeOpensOnceItAnswers(t *testing.T) {
	issues, _ := wantedRegistry(t)
	mem := newFakeMemory()

	m := newAt(t, unprobedDeps(mem), 120, 30, WithInitialView("board"))
	if got := m.top().spec.ID; got != "issues" {
		t.Fatalf("opened on %q before the probe, want the fallback", got)
	}
	if _, kept := mem.state["kernel.view"]; kept {
		t.Errorf("the fallback was remembered as a choice: %q", mem.state["kernel.view"])
	}

	m = probed(m, boardsAllowed)
	if got := m.top().spec.ID; got != "board" || len(m.stack) != 1 {
		t.Fatalf("after the probe the stack is %d deep on %q, want board", len(m.stack), got)
	}
	if mem.state["kernel.view"] != "board" {
		t.Errorf("remembered %q, want board", mem.state["kernel.view"])
	}
	if issues.closed != 1 {
		t.Errorf("the fallback was closed %d times, want once", issues.closed)
	}
	if _, parked := m.live["issues"]; parked {
		t.Error("the fallback is still parked as though somebody chose it")
	}
}

func TestWanted_AViewTheProbeRefusesSaysWhyAndKeepsTheFallback(t *testing.T) {
	wantedRegistry(t)
	mem := newFakeMemory()

	m := newAt(t, unprobedDeps(mem), 120, 30, WithInitialView("board"))
	m = probed(m, jira.Capabilities{})
	if got := m.top().spec.ID; got != "issues" {
		t.Fatalf("on %q, want the fallback", got)
	}
	want := "Board needs boards on this site; opened Issues"
	if m.status != want || m.statusLevel != LevelWarn {
		t.Errorf("the status line reads %q at level %d, want %q", m.status, m.statusLevel, want)
	}
}

func TestWanted_ARootTheUserSwitchedToIsLeftAlone(t *testing.T) {
	wantedRegistry(t)
	m := newAt(t, unprobedDeps(newFakeMemory()), 120, 30, WithInitialView("board"))
	m, _ = press(m, "g", "3")

	m = probed(m, boardsAllowed)
	if got := m.top().spec.ID; got != "backlog" {
		t.Errorf("the probe took the user off the root they chose, onto %q", got)
	}
}

// Whatever was pushed over the fallback at startup stays where it is; only the
// root under it is replaced.
func TestWanted_ThePushOverTheFallbackSurvives(t *testing.T) {
	wantedRegistry(t)
	m := newAt(t, unprobedDeps(newFakeMemory()), 120, 30, WithInitialView("board"))
	pane := &stubView{id: "PROJ-1"}
	m = pushed(t, m, PushMsg{View: pane, ID: "issue"})

	m = probed(m, boardsAllowed)
	if len(m.stack) != 2 || m.stack[0].spec.ID != "board" || m.top().view != View(pane) {
		t.Fatalf("stack is %d deep on %q with %q on top", len(m.stack), m.stack[0].spec.ID, m.top().spec.ID)
	}
	if pane.closed != 0 {
		t.Error("the pushed pane was discarded")
	}
}

// The remembered root is the same promise as a named one: a first frame drawn
// before the probe must not overwrite it with the fallback.
func TestWanted_TheRememberedRootIsNotOverwrittenByTheFallback(t *testing.T) {
	wantedRegistry(t)
	mem := newFakeMemory()
	mem.state["kernel.view"] = "board"

	m := newAt(t, unprobedDeps(mem), 120, 30)
	if mem.state["kernel.view"] != "board" {
		t.Fatalf("the fallback overwrote the remembered root: %q", mem.state["kernel.view"])
	}
	m = probed(m, boardsAllowed)
	if got := m.top().spec.ID; got != "board" {
		t.Errorf("on %q after the probe, want the remembered board", got)
	}

	refused := newFakeMemory()
	refused.state["kernel.view"] = "board"
	m = probed(newAt(t, unprobedDeps(refused), 120, 30), jira.Capabilities{})
	if strings.Contains(m.status, "needs") {
		t.Errorf("a remembered root nobody named on this run explained itself: %q", m.status)
	}
}
