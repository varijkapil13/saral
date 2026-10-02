package release

import (
	"errors"
	"slices"
	"strings"
	"testing"

	"github.com/varijkapil13/saral/internal/ui/kernel"
	"github.com/varijkapil13/saral/pkg/jira"
)

// fakeMemory is a kernel.Memory in a map; the real one is a profile-scoped
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

func TestFilter_CyclesThroughEveryStateAndKeepsTheRightVersions(t *testing.T) {
	t.Parallel()

	dr := listOf(t, testDeps(nil), 120, 16)
	stock(dr, mixedVersions())
	m := dr.list()
	for _, step := range []struct {
		filter stateFilter
		want   []string
	}{
		{filterUnreleased, []string{"1", "3", "5"}},
		{filterReleased, []string{"2"}},
		{filterArchived, []string{"4"}},
		{filterAll, []string{"1", "2", "3", "4", "5"}},
	} {
		dr.key("f")
		if m.filter != step.filter {
			t.Fatalf("f moved the filter to %q, want %q", m.filter.name(), step.filter.name())
		}
		if got := drawnIDs(m); !slices.Equal(got, step.want) {
			t.Errorf("%s draws %v, want %v", step.filter.name(), got, step.want)
		}
	}
}

func TestFilter_TheSummarySaysWhatIsInForceAndHowManyItKeeps(t *testing.T) {
	t.Parallel()

	dr := listOf(t, testDeps(nil), 120, 16)
	stock(dr, mixedVersions())
	mustContain(t, dr.view(), "PROJ 5 versions · 1 released")
	dr.key("f")
	mustContain(t, dr.view(), "PROJ unreleased · 3 of 5 versions")
}

func TestFilter_IsRememberedForTheProfile(t *testing.T) {
	t.Parallel()

	mem := newFakeMemory()
	d := testDeps(nil)
	d.Memory = mem
	dr := listOf(t, d, 120, 16)
	dr.key("f", "f")
	if got := mem.state[ViewID+"."+filterMemoryKey]; got != "released" {
		t.Errorf("the memory holds %q, want released", got)
	}

	again, ok := New(d).(*Model)
	if !ok {
		t.Fatal("New did not build a *Model")
	}
	if again.filter != filterReleased {
		t.Errorf("a new list opened on %q, want the filter last left in force", again.filter.name())
	}

	dr.key("f", "f")
	if _, kept := mem.state[ViewID+"."+filterMemoryKey]; kept {
		t.Error("going back to every version left a filter in the memory")
	}

	mem.state[ViewID+"."+filterMemoryKey] = "shipped-by-a-later-build"
	if got := recallFilter(d, ViewID); got != filterAll {
		t.Errorf("a value this build does not know opened on %q, want all", got.name())
	}
}

// A version the filter hides leaves the cursor on the row that took its place.
func TestFilter_ACursorOnAHiddenVersionGoesToTheNearestRow(t *testing.T) {
	t.Parallel()

	dr := listOf(t, testDeps(nil), 120, 16)
	stock(dr, mixedVersions())
	dr.moveTo("4")
	dr.key("f")
	if got := dr.list().selectedID(); got != "5" {
		t.Errorf("hiding the archived version moved the cursor to %q, want the next row, 5", got)
	}
	dr.key("f", "f", "f")
	dr.moveTo("5")
	dr.key("f", "f")
	if got := dr.list().selectedID(); got != "2" {
		t.Errorf("the cursor is on %q, want the only released version", got)
	}
}

func TestFilter_AnEmptyResultSaysSoAndNamesTheWayOut(t *testing.T) {
	t.Parallel()

	dr := listOf(t, testDeps(newFake(4)), 120, 16)
	dr.key("f", "f", "f")
	mustContain(t, dr.view(), "PROJ has no archived versions.", "f shows every version.")
	if _, ok := dr.list().selected(); ok {
		t.Error("an empty result still has a version under the cursor")
	}
	dr.key("!", "e", "A", "B")
	if len(dr.pushes) != 0 || dr.list().mode != browsing {
		t.Error("an action on an empty result did something")
	}
}

// A read that fails while a filter is in force shows the failure, not the
// empty filtered line.
func TestFilter_AFailedReadUnderAFilterStillSaysWhatFailed(t *testing.T) {
	t.Parallel()

	fake := newFake(4)
	dr := listOf(t, testDeps(fake), 120, 16)
	dr.key("f", "f", "f")
	fake.FailNext(&jira.CapabilityError{Reason: "you need Browse Projects on PROJ to read its versions"})
	dr.send(kernel.RefreshMsg{})

	frame := dr.view()
	mustContain(t, frame, "you need Browse Projects on PROJ", "stale")
	mustNotContain(t, frame, "has no archived versions")
	if s := dr.lastStatus(); s.Level != kernel.LevelError {
		t.Errorf("the status line says %+v, want the refusal", s)
	}

	fresh := newFake(4)
	fresh.FailNext(errors.New("dial tcp: connection refused"))
	mem := newFakeMemory()
	mem.state[ViewID+"."+filterMemoryKey] = "unreleased"
	d := testDeps(fresh)
	d.Memory = mem
	cold := listOf(t, d, 120, 16)
	mustContain(t, cold.view(), "connection refused")
}

// A created version is always shown: a filter that would hide it gives way to
// every version, and the status line says so.
func TestFilter_ACreatedVersionIsNeverHidden(t *testing.T) {
	t.Parallel()

	dr := listOf(t, testDeps(newFake(4)), 120, 20)
	dr.key("f", "f")
	dr.key("c")
	dr.typeText("4.0")
	dr.key("ctrl+s")

	m := dr.list()
	if m.filter != filterAll {
		t.Errorf("the filter is %q after creating an unreleased version under released", m.filter.name())
	}
	if v, ok := m.selected(); !ok || v.Name != "4.0" {
		t.Errorf("the cursor is on %+v, want the version just created", v)
	}
	if got := dr.lastStatus().Text; !strings.Contains(got, "showing every version") {
		t.Errorf("the status line says %q, want it to say why the filter changed", got)
	}
}

// Archiving under the unreleased filter takes the version out of view; the
// cursor goes to the row that took its place and the status says why.
func TestFilter_ArchivingUnderUnreleasedTakesTheVersionOutOfView(t *testing.T) {
	t.Parallel()

	dr := listOf(t, testDeps(newFake(4)), 120, 16)
	dr.key("f")
	dr.moveTo(twoOh)
	dr.key("A")

	m := dr.list()
	if slices.Contains(drawnIDs(m), twoOh) {
		t.Error("the archived version is still drawn under unreleased")
	}
	if got := m.selectedID(); got != threeOh {
		t.Errorf("the cursor is on %q, want 3.0, the row that took its place", got)
	}
	if got := dr.lastStatus().Text; !strings.Contains(got, "unreleased filter hides") {
		t.Errorf("the status line says %q", got)
	}
	if v, _ := m.byID(twoOh); !v.Archived {
		t.Error("the version was not archived")
	}
}
