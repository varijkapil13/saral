package list

import (
	"testing"

	"github.com/varijkapil13/saral/internal/ui/kernel"
)

func TestList_EscClearsAKeptFilterThroughTheKernel(t *testing.T) {
	t.Parallel()

	m := startAll(t, testDeps(newFake(40)), 120, 30)
	m = keys(t, m, "/")
	for _, r := range "login" {
		m = send(t, m, keyPress(string(r)))
	}
	m = keys(t, m, "enter")
	lm := m.Top().(*Model)
	if lm.query == "" || len(lm.view) == len(lm.issues) {
		t.Fatalf("the filter was not kept (%q, %d of %d rows), so this proves nothing", lm.query, len(lm.view), len(lm.issues))
	}
	if !lm.WantsBack() {
		t.Fatal("a narrowed list does not claim esc")
	}

	m = keys(t, m, "esc")
	lm = m.Top().(*Model)
	if lm.query != "" || len(lm.view) != len(lm.issues) {
		t.Errorf("esc left the filter %q on, %d of %d rows", lm.query, len(lm.view), len(lm.issues))
	}
	if lm.WantsBack() {
		t.Error("a list with nothing narrowing it still claims esc")
	}
	mustNotContain(t, frame(m), "only rows matching")
}

func TestList_AtRestLeavesEscToTheKernel(t *testing.T) {
	t.Parallel()

	dr := openAll(t, testDeps(newFake(10)), 120, 30)
	if dr.m.WantsBack() {
		t.Error("a list with nothing narrowing it claims esc")
	}
}

func TestCommands_OpenTheListThenDeliverRatherThanBroadcast(t *testing.T) {
	t.Parallel()

	for id, want := range map[string]any{
		"issues.filter-by":    OpenFilterMsg{},
		"issues.edit-query":   EditQueryMsg{},
		"issues.sort":         SortMsg{},
		"issues.save-query":   SaveQueryMsg{},
		"issues.clear-filter": ClearFilterMsg{},
	} {
		command, ok := kernel.LookupCommand(id)
		if !ok {
			t.Fatalf("%s is not registered", id)
		}
		open, ok := command.Run(testDeps(newFake(0)))().(kernel.OpenMsg)
		if !ok || open.ID != ViewID || open.Then != want {
			t.Errorf("%s runs %#v, want an open of %s carrying %T", id, open, ViewID, want)
		}
	}
}
