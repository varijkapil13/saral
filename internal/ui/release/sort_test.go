package release

import (
	"context"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/varijkapil13/saral/internal/config"
	"github.com/varijkapil13/saral/internal/ui/kernel"
	"github.com/varijkapil13/saral/pkg/jira"
)

// ownSortCache points this test's saved sort at a directory of its own.
// t.Setenv rules out t.Parallel, which is why a test here that chooses an
// order through the picker does not run in parallel.
func ownSortCache(t *testing.T) {
	t.Helper()
	t.Setenv("SARAL_CACHE_DIR", t.TempDir())
}

func day(y int, m time.Month, d int) jira.Date { return jira.Date{Year: y, Month: m, Day: d} }

// mixedVersions is one version in every state, with ties on the start date
// and versions with no dates at all. Today is 2026-03-05 in testDeps.
func mixedVersions() []jira.Version {
	return []jira.Version{
		{ID: "1", Name: "beta", StartDate: day(2026, time.January, 1), ReleaseDate: day(2026, time.April, 1)},
		{ID: "2", Name: "Alpha", Released: true, StartDate: day(2026, time.February, 1)},
		{ID: "3", Name: "gamma", ReleaseDate: day(2026, time.February, 1)},
		{
			ID: "4", Name: "delta", Archived: true,
			StartDate: day(2026, time.January, 1), ReleaseDate: day(2026, time.May, 1),
		},
		{ID: "5", Name: "epsilon"},
	}
}

func drawnIDs(m *Model) []string {
	out := make([]string, 0, len(m.order))
	for _, i := range m.order {
		out = append(out, m.versions[i].ID)
	}
	return out
}

func TestSort_EachFieldOrdersTheRowsAndTiesKeepTheProjectsOrder(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		field     string
		asc, desc []string
	}{
		{field: sortProject, asc: []string{"1", "2", "3", "4", "5"}, desc: []string{"5", "4", "3", "2", "1"}},
		{field: "name", asc: []string{"2", "1", "4", "5", "3"}, desc: []string{"3", "5", "4", "1", "2"}},
		{field: "release", asc: []string{"3", "1", "4", "2", "5"}, desc: []string{"4", "1", "3", "2", "5"}},
		{field: "start", asc: []string{"1", "4", "2", "3", "5"}, desc: []string{"2", "1", "4", "3", "5"}},
		{field: "state", asc: []string{"3", "1", "5", "2", "4"}, desc: []string{"4", "2", "1", "5", "3"}},
	} {
		t.Run(tc.field, func(t *testing.T) {
			t.Parallel()
			dr := listOf(t, testDeps(nil), 120, 16)
			stock(dr, mixedVersions())
			m := dr.list()
			for _, desc := range []bool{false, true} {
				m.sort = sortChoice{field: tc.field, desc: desc}
				m.reorder()
				want := tc.asc
				if desc {
					want = tc.desc
				}
				if got := drawnIDs(m); !slices.Equal(got, want) {
					t.Errorf("desc=%v: drawn %v, want %v", desc, got, want)
				}
			}
		})
	}
}

// The versions the cache keeps and the flow is offered stay in the project's
// own order whatever is drawn.
func TestSort_TheVersionsThemselvesStayInTheProjectsOrder(t *testing.T) {
	t.Parallel()

	cache := newMemCache()
	d := testDeps(nil)
	d.Cache = cache
	dr := listOf(t, d, 120, 16)
	stock(dr, mixedVersions())
	m := dr.list()
	m.sort, m.filter = sortChoice{field: "name", desc: true}, filterUnreleased
	m.reorder()
	dr.send(versionsMsg{gen: m.gen, versions: mixedVersions()})

	ids := make([]string, 0, len(cache.held["PROJ"].Versions))
	for _, v := range cache.held["PROJ"].Versions {
		ids = append(ids, v.ID)
	}
	if want := []string{"1", "2", "3", "4", "5"}; !slices.Equal(ids, want) {
		t.Errorf("the cache holds %v, want the site's own %v", ids, want)
	}
	if got := drawnIDs(m); !slices.Equal(got, []string{"3", "5", "1"}) {
		t.Errorf("drawn %v under unreleased by name descending", got)
	}
}

func TestSort_ThePickerChoosesFlipsCancelsAndIsRemembered(t *testing.T) {
	ownSortCache(t)

	dr := listOf(t, testDeps(nil), 120, 16)
	stock(dr, mixedVersions())
	m := dr.list()

	dr.key("s")
	if m.mode != sorting {
		t.Fatal("s did not open the picker")
	}
	mustContain(t, dr.view(), "sort by:", "[project order ^]")
	dr.key("l", "enter")
	if m.mode != browsing || m.sort != (sortChoice{field: "name"}) {
		t.Fatalf("choosing name left mode %d and sort %+v", m.mode, m.sort)
	}
	mustContain(t, dr.view(), "sort: name ^")
	if spec, ok := config.LoadUIState().Sort(ViewID); !ok || spec.Field != "name" || spec.Desc {
		t.Errorf("the choice was kept as %+v (%v)", spec, ok)
	}

	dr.key("s", "enter")
	if m.sort != (sortChoice{field: "name", desc: true}) {
		t.Errorf("choosing the field in force again left %+v, want it turned round", m.sort)
	}

	dr.key("s", "l", "l", "esc")
	if m.mode != browsing || m.sort != (sortChoice{field: "name", desc: true}) {
		t.Errorf("esc changed the order to %+v", m.sort)
	}

	again, ok := New(testDeps(nil)).(*Model)
	if !ok {
		t.Fatal("New did not build a *Model")
	}
	if again.sort != (sortChoice{field: "name", desc: true}) {
		t.Errorf("a new list opened on %+v, want the order last chosen", again.sort)
	}

	dr.key("s", "h", "enter")
	if m.sort.chosen() {
		t.Errorf("going back to the project's own order left %+v", m.sort)
	}
	if _, ok := config.LoadUIState().Sort(ViewID); ok {
		t.Error("the project's own order was kept as a choice")
	}
}

func TestSort_TheCursorStaysOnItsVersionWhateverTheOrder(t *testing.T) {
	ownSortCache(t)

	dr := listOf(t, testDeps(nil), 120, 16)
	stock(dr, mixedVersions())
	dr.moveTo("3")
	for _, keys := range [][]string{{"s", "l", "enter"}, {"s", "enter"}, {"s", "l", "l", "l", "enter"}} {
		dr.key(keys...)
		if got := dr.list().selectedID(); got != "3" {
			t.Fatalf("after %v the cursor is on %q, want 3", keys, got)
		}
	}
	dr.send(versionsMsg{gen: dr.list().gen, versions: mixedVersions()})
	if got := dr.list().selectedID(); got != "3" {
		t.Errorf("a refetch under a sort moved the cursor to %q", got)
	}
}

func TestSort_AFailedSaveWarnsOnceAndKeepsTheOrder(t *testing.T) {
	blocker := filepath.Join(t.TempDir(), "not-a-directory")
	if err := os.WriteFile(blocker, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("SARAL_CACHE_DIR", filepath.Join(blocker, "cache"))

	dr := listOf(t, testDeps(nil), 120, 16)
	stock(dr, mixedVersions())
	dr.key("s", "l", "enter")
	dr.key("s", "enter")
	dr.key("s", "l", "enter")

	warned := 0
	for _, s := range dr.statuses {
		if s.Level == kernel.LevelWarn && strings.Contains(s.Text, "will not survive a restart") {
			warned++
		}
	}
	if warned != 1 {
		t.Errorf("three failed saves warned %d times, want once", warned)
	}
	if got := dr.list().sort; got != (sortChoice{field: "release"}) {
		t.Errorf("the order on screen is %+v after the saves failed", got)
	}
}

func TestSort_ClickingTheLabelReopensThePicker(t *testing.T) {
	t.Parallel()

	d := testDeps(nil)
	dr := listOf(t, d, 120, 16)
	stock(dr, mixedVersions())
	m := dr.list()
	m.sort = sortChoice{field: "state"}
	m.reorder()
	m.sum = ""

	pressOn(t, d, dr, sortZone)
	if m.mode != sorting {
		t.Error("clicking the sort label did not open the picker")
	}
	if got := sortFields[m.sortCursor].id; got != "state" {
		t.Errorf("the picker opened on %q, want the field in force", got)
	}
}

// Every action works on the row drawn under the cursor, not on whichever
// version sits at that index in the site's own list.
func TestSort_EveryActionTakesTheVersionDrawnUnderTheCursor(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		key   string
		check func(t *testing.T, dr *driver, w *watcher, want jira.Version)
	}{
		{key: "enter", check: func(t *testing.T, dr *driver, _ *watcher, want jira.Version) {
			pushed, ok := dr.pushed()
			if !ok || pushed.Title != "Release "+want.Name {
				t.Errorf("enter pushed %q, want the release of %s", pushed.Title, want.Name)
			}
		}},
		{key: "e", check: func(t *testing.T, dr *driver, _ *watcher, want jira.Version) {
			if got := dr.list().form.id; got != want.ID {
				t.Errorf("e opened the editor on %q, want %q", got, want.ID)
			}
		}},
		{key: "A", check: func(t *testing.T, _ *driver, w *watcher, want jira.Version) {
			sent := w.saved()
			if len(sent) != 1 || sent[0].ID != want.ID {
				t.Errorf("A archived %+v, want %q", sent, want.ID)
			}
		}},
		{key: "b", check: func(t *testing.T, dr *driver, _ *watcher, want jira.Version) {
			pushed, ok := dr.pushed()
			if !ok || pushed.ID != BulkViewID || pushed.Title != "Issues on "+want.Name {
				t.Errorf("b pushed %q %q, want the assignment screen for %s", pushed.ID, pushed.Title, want.Name)
			}
		}},
	} {
		t.Run(tc.key, func(t *testing.T) {
			t.Parallel()
			fake := newFake(4)
			seedVersion(t, fake, jira.VersionInput{ProjectKey: "PROJ", Name: "2.5"})
			seedVersion(t, fake, jira.VersionInput{ProjectKey: "PROJ", Name: "9.9", Archived: ptr(true)})
			w := watching(fake)
			dr := listOf(t, testDeps(w), 120, 16)
			m := dr.list()
			m.sort, m.filter = sortChoice{field: "name", desc: true}, filterUnreleased
			m.reorder()
			m.moveTo(1)

			want, ok := m.selected()
			if !ok || want.Name != "2.5" {
				t.Fatalf("the cursor is on %+v; under unreleased by name descending row 1 is 2.5", want)
			}
			if m.versions[1].Name == want.Name {
				t.Fatal("row 1 is the same version in both orders, so this proves nothing")
			}
			dr.key(tc.key)
			tc.check(t, dr, w, want)
		})
	}
}

func seedVersion(t *testing.T, fake interface {
	SaveVersion(ctx context.Context, in jira.VersionInput) (jira.Version, error)
}, in jira.VersionInput,
) {
	t.Helper()
	if _, err := fake.SaveVersion(context.Background(), in); err != nil {
		t.Fatal(err)
	}
}

func ptr[T any](v T) *T { return &v }
