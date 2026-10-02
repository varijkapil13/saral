package release

import (
	"slices"
	"testing"

	"github.com/varijkapil13/saral/internal/ui/kernel"
)

func TestList_FindNarrowsVersionsByName(t *testing.T) {
	t.Parallel()

	dr := listOf(t, testDeps(nil), 120, 16)
	stock(dr, mixedVersions())
	m := dr.list()

	dr.key("/")
	if !m.WantsRawKeys() {
		t.Fatal("the find does not claim raw keys, so q quits and a digit switches view")
	}
	dr.typeText("TA")
	if got, want := drawnIDs(m), []string{"1", "4"}; !slices.Equal(got, want) {
		t.Errorf("a capitalised find draws %v, want the versions named beta and delta %v", got, want)
	}
	mustContain(t, dr.view(), `matching "TA"`, "2 of 5 versions")

	dr.key("enter")
	if m.WantsRawKeys() {
		t.Error("enter kept the text and the list still claims every key")
	}
	if got, want := drawnIDs(m), []string{"1", "4"}; !slices.Equal(got, want) {
		t.Errorf("enter lost the find: %v", got)
	}
}

func TestList_FindEscClearsAndRestores(t *testing.T) {
	t.Parallel()

	dr := listOf(t, testDeps(newFake(4)), 120, 16)
	stock(dr, mixedVersions())
	m := dr.list()

	dr.key("/")
	dr.typeText("zzz")
	if len(m.order) != 0 {
		t.Fatalf("a find nothing matches still draws %v", drawnIDs(m))
	}
	mustContain(t, dr.view(), "No version matches what is narrowing the list.", `text "zzz"`)

	dr.key("esc")
	if m.find.needle != "" || m.find.rawNeedle != "" || m.find.input.Value() != "" {
		t.Errorf("esc kept the text %q", m.find.rawNeedle)
	}
	if m.WantsRawKeys() {
		t.Error("esc left the find open")
	}
	if got := len(drawnIDs(m)); got != 5 {
		t.Errorf("esc did not bring every version back: %d drawn", got)
	}
	mustNotContain(t, dr.view(), "matching")
}

func TestList_FindKeepsCursorOnItsVersion(t *testing.T) {
	t.Parallel()

	dr := listOf(t, testDeps(nil), 120, 16)
	stock(dr, mixedVersions())
	dr.moveTo("4")

	dr.key("/")
	dr.typeText("ta")
	if got := dr.list().selectedID(); got != "4" {
		t.Errorf("narrowing to the versions that hold %q moved the cursor to %q, want 4", "ta", got)
	}
	dr.key("backspace", "backspace")
	if got := dr.list().selectedID(); got != "4" {
		t.Errorf("widening the find moved the cursor to %q, want 4", got)
	}
}

func TestList_FindComposesWithStateFilterAndSort(t *testing.T) {
	t.Parallel()

	dr := listOf(t, testDeps(nil), 120, 16)
	stock(dr, mixedVersions())
	m := dr.list()

	dr.key("f")
	m.sort = sortChoice{field: "name", desc: true}
	m.reorder()
	if got, want := drawnIDs(m), []string{"3", "5", "1"}; !slices.Equal(got, want) {
		t.Fatalf("unreleased by name, descending, draws %v, want %v", got, want)
	}

	dr.key("/")
	dr.typeText("a")
	if got, want := drawnIDs(m), []string{"3", "1"}; !slices.Equal(got, want) {
		t.Errorf("the find over the state filter and the sort draws %v, want %v", got, want)
	}
	mustContain(t, dr.view(), `unreleased · matching "a" · 2 of 5 versions`)

	dr.key("esc")
	if got, want := drawnIDs(m), []string{"3", "5", "1"}; !slices.Equal(got, want) {
		t.Errorf("clearing the find drew %v, want the filtered and sorted %v", got, want)
	}
}

func TestList_FindTypesQAndDigits(t *testing.T) {
	t.Parallel()

	dr := listOf(t, testDeps(nil), 120, 16)
	stock(dr, mixedVersions())
	m := dr.list()

	dr.key("j", "j")
	dr.key("/")
	dr.typeText("q1jk2g")
	if got := m.find.input.Value(); got != "q1jk2g" {
		t.Errorf("q, 1, j, k, 2 and g typed into the find left %q", got)
	}
	if dr.pops != 0 || len(dr.pushes) != 0 || len(dr.statuses) != 0 {
		t.Errorf("typing into the find asked the kernel for something: %d pops, %d pushes, %d statuses",
			dr.pops, len(dr.pushes), len(dr.statuses))
	}
	mustContain(t, dr.view(), "/ q1jk2g")
	if _, blocked := m.BlocksClose(); blocked {
		t.Error("a find being typed blocks closing the list")
	}
}

func TestList_FindPaletteCommandOpensIt(t *testing.T) {
	t.Parallel()

	cmd, ok := kernel.LookupCommand("releases.find")
	if !ok {
		t.Fatal("no command releases.find is registered")
	}
	broadcast, ok := cmd.Run(kernel.Deps{})().(kernel.BroadcastMsg)
	if !ok {
		t.Fatal("the find command no longer broadcasts to the views on the stack")
	}

	dr := listOf(t, testDeps(nil), 120, 16)
	stock(dr, mixedVersions())
	dr.send(broadcast.Msg)
	if m := dr.list(); m.mode != finding || !m.WantsRawKeys() {
		t.Errorf("the palette command left the list in mode %d", m.mode)
	}
}

func TestList_FindIsNotOpenedUnderAScreenPushedOverIt(t *testing.T) {
	t.Parallel()

	dr := listOf(t, testDeps(nil), 120, 16)
	stock(dr, mixedVersions())
	dr.send(kernel.FocusMsg{})
	dr.send(FindMsg{})
	if m := dr.list(); m.mode == finding {
		t.Error("a find was opened on a list that another screen covers")
	}
	dr.send(kernel.FocusMsg{Focused: true})
	dr.send(FindMsg{})
	if m := dr.list(); m.mode != finding {
		t.Error("a find was not opened on the list once it had the keys back")
	}
}

func TestList_FindFollowsAProjectSwitchWithNothingTyped(t *testing.T) {
	t.Parallel()

	dr := listOf(t, testDeps(nil), 120, 16)
	stock(dr, mixedVersions())
	dr.key("/")
	dr.typeText("ta")
	dr.send(kernel.ProjectMsg{Project: "OTHER"})
	m := dr.list()
	if m.find.rawNeedle != "" || m.mode != browsing {
		t.Errorf("a project switch kept the find %q in mode %d", m.find.rawNeedle, m.mode)
	}
}
