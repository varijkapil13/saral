package release

import (
	"errors"
	"testing"

	"github.com/varijkapil13/saral/pkg/jira"
)

func TestReleases_Golden(t *testing.T) {
	t.Parallel()

	for name, tc := range map[string]struct {
		width, height int
		after         func(*driver)
		golden        string
	}{
		"the versions of a project": {
			width: 120, height: 16, golden: "versions_120x16.golden",
		},
		"a narrow terminal": {
			width: 80, height: 16, golden: "versions_80x16.golden",
		},
		"a count that has been read": {
			width: 120, height: 16, golden: "counted_120x16.golden",
			after: func(dr *driver) {
				m := dr.list()
				v, _ := m.byID(twoOh)
				open := 12
				v.Unresolved = &open
				m.put(v)
				m.rebuildCells()
				m.sum = ""
			},
		},
		"typing a new version": {
			width: 120, height: 20, golden: "editing_120x20.golden",
			after: func(dr *driver) {
				dr.key("n")
				dr.typeText("4.0")
				dr.key("tab")
				dr.typeText("the one after next")
			},
		},
		"a date it will not accept": {
			width: 120, height: 20, golden: "refused_date_120x20.golden",
			after: func(dr *driver) {
				dr.key("n")
				dr.typeText("4.0")
				dr.key("tab", "tab")
				dr.typeText("soon")
				dr.key("ctrl+s")
			},
		},
		"a read the site refused": {
			width: 100, height: 14, golden: "refused_100x14.golden",
			after: func(dr *driver) {
				m := dr.list()
				m.versions, m.loaded = nil, false
				m.failure, m.what = &jira.CapabilityError{
					Reason: "you need Browse Projects on PROJ to read its versions",
				}, whatVersions
				m.rebuildCells()
				m.sum = ""
			},
		},
		"a project with no versions": {
			width: 100, height: 14, golden: "empty_100x14.golden",
			after: func(dr *driver) {
				m := dr.list()
				m.versions, m.loaded = nil, true
				m.rebuildCells()
				m.sum = ""
			},
		},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			dr := listOf(t, testDeps(newFake(8)), tc.width, tc.height)
			if tc.after != nil {
				tc.after(dr)
			}
			golden(t, tc.golden, dr.view())
		})
	}
}

// A version is drawn from the port's own flags and dates, never from a word the
// site could rename.
func TestReleases_TheStateOfAVersionComesFromItsFlagsAndItsDates(t *testing.T) {
	t.Parallel()

	today := jira.Date{Year: 2026, Month: 3, Day: 5}
	for name, tc := range map[string]struct {
		version jira.Version
		want    string
	}{
		"released": {
			version: jira.Version{Released: true},
			want:    stateReleased,
		},
		"archived beats released": {
			version: jira.Version{Released: true, Archived: true},
			want:    stateArchived,
		},
		"a release date in the past": {
			version: jira.Version{ReleaseDate: jira.Date{Year: 2026, Month: 3, Day: 4}},
			want:    stateOverdue,
		},
		"a release date today is not late yet": {
			version: jira.Version{ReleaseDate: today},
			want:    stateUnreleased,
		},
		"no dates at all": {
			version: jira.Version{},
			want:    stateUnreleased,
		},
		"released and overdue is released": {
			version: jira.Version{Released: true, ReleaseDate: jira.Date{Year: 2020, Month: 1, Day: 1}},
			want:    stateReleased,
		},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			if got := versionState(tc.version, today); got != tc.want {
				t.Errorf("the version is drawn as %q, want %q", got, tc.want)
			}
		})
	}
}

// A count nobody has read is not a count of zero.
func TestReleases_TheOpenColumnTellsNilFromZero(t *testing.T) {
	t.Parallel()

	none := 0
	if got := openLabel(jira.Version{}); got != unknownOpen {
		t.Errorf("an uncounted version draws %q in the open column", got)
	}
	if got := openLabel(jira.Version{Unresolved: &none}); got != "0" {
		t.Errorf("a version counted at zero draws %q, want the number", got)
	}
}

// A relayout is what invalidates a memoized row, so the plan has to be part of
// its key — and two widths must not plan the same columns.
func TestReleases_TheColumnPlanDropsFromTheRight(t *testing.T) {
	t.Parallel()

	wide := planLayout(200, 8)
	narrow := planLayout(80, 8)
	if wide == narrow {
		t.Fatal("two widths planned the same columns, so a resize would not invalidate a row")
	}
	if narrow.description >= wide.description {
		t.Errorf("the description is %d columns at 80 and %d at 200", narrow.description, wide.description)
	}
	tiny := planLayout(46, 8)
	switch {
	case tiny.description != 0:
		t.Errorf("at 46 columns the plan kept a description of %d", tiny.description)
	case tiny.release != 0:
		t.Error("at 46 columns the plan kept the release date and gave up nothing")
	case tiny.name < minName:
		t.Errorf("at 46 columns the name was squeezed to %d", tiny.name)
	}
}

// A failure is wrapped rather than cut: a transport failure names a host and a
// port before it says what is wrong with them.
func TestReleases_ALongRefusalIsWrappedRatherThanCut(t *testing.T) {
	t.Parallel()

	long := "the connection to example.atlassian.net:443 was refused after four attempts, " +
		"which usually means the host is behind a proxy this session does not know about"
	dr := listOf(t, testDeps(newFake(4)), 80, 16)
	m := dr.list()
	m.versions, m.loaded = nil, false
	m.failure, m.what = errors.New(long), whatVersions
	m.rebuildCells()
	m.sum = ""

	frame := dr.view()
	mustContain(t, frame,
		"the connection to example.atlassian.net:443 was refused",
		"which usually means the host is behind a proxy",
		"about",
	)
}
