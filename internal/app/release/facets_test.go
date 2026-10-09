package release

import (
	"errors"
	"testing"

	"github.com/varijkapil13/saral/pkg/jira"
)

// A version's state comes from the port's own flags and dates, never from a
// word the site could rename.
func TestStateOf_ComesFromItsFlagsAndItsDates(t *testing.T) {
	t.Parallel()

	today := jira.Date{Year: 2026, Month: 3, Day: 5}
	for name, tc := range map[string]struct {
		version jira.Version
		want    State
	}{
		"released":                {version: jira.Version{Released: true}, want: Released},
		"archived beats released": {version: jira.Version{Released: true, Archived: true}, want: Archived},
		"a release date in the past": {
			version: jira.Version{ReleaseDate: jira.Date{Year: 2026, Month: 3, Day: 4}}, want: Overdue,
		},
		"a release date today is not late yet": {version: jira.Version{ReleaseDate: today}, want: Unreleased},
		"no dates at all":                      {version: jira.Version{}, want: Unreleased},
		"released and overdue is released": {
			version: jira.Version{Released: true, ReleaseDate: jira.Date{Year: 2020, Month: 1, Day: 1}}, want: Released,
		},
		"no date to be late against": {
			version: jira.Version{ReleaseDate: jira.Date{Year: 2020, Month: 1, Day: 1}}, want: Unreleased,
		},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			day := today
			if name == "no date to be late against" {
				day = jira.Date{}
			}
			if got := StateOf(tc.version, day); got != tc.want {
				t.Errorf("state %d, want %d", got, tc.want)
			}
		})
	}
}

func TestCompareStates_PutsWhatNeedsDoingFirst(t *testing.T) {
	t.Parallel()
	order := []State{Overdue, Unreleased, Released, Archived}
	for i := 1; i < len(order); i++ {
		if CompareStates(order[i-1], order[i]) >= 0 {
			t.Errorf("%d does not sort before %d", order[i-1], order[i])
		}
	}
}

func TestCompareDated_PutsTheUndatedLastBothWays(t *testing.T) {
	t.Parallel()
	early, late := jira.Date{Year: 2026, Month: 1, Day: 1}, jira.Date{Year: 2026, Month: 2, Day: 1}
	for _, desc := range []bool{false, true} {
		if CompareDated(jira.Date{}, early, desc) <= 0 || CompareDated(early, jira.Date{}, desc) >= 0 {
			t.Errorf("desc=%v: an undated version is not last", desc)
		}
		if CompareDated(jira.Date{}, jira.Date{}, desc) != 0 {
			t.Errorf("desc=%v: two undated versions are not equal", desc)
		}
	}
	if CompareDated(early, late, false) >= 0 || CompareDated(early, late, true) <= 0 {
		t.Error("the direction does not turn the dated ones round")
	}
	if CompareNames("alpha", "Beta") >= 0 || CompareNames("ALPHA", "alpha") != 0 {
		t.Error("names are not compared regardless of case")
	}
}

func TestFilter_KeepsTheRightStatesAndCycles(t *testing.T) {
	t.Parallel()
	keeps := map[Filter][]State{
		FilterAll:        {Overdue, Unreleased, Released, Archived},
		FilterUnreleased: {Overdue, Unreleased},
		FilterReleased:   {Released},
		FilterArchived:   {Archived},
	}
	for f, want := range keeps {
		for s := Overdue; s <= Archived; s++ {
			kept := false
			for _, w := range want {
				kept = kept || w == s
			}
			if f.Keeps(s) != kept {
				t.Errorf("%s keeps state %d: %v, want %v", f.Name(), s, f.Keeps(s), kept)
			}
		}
	}
	if FilterArchived.Next() != FilterAll || FilterAll.Step(-1) != FilterArchived || FilterAll.Step(1) != FilterUnreleased {
		t.Error("the filters do not cycle round")
	}
	for f := range FilterCount {
		if got, ok := FilterNamed(f.Name()); !ok || got != f {
			t.Errorf("%s does not read back as itself", f.Name())
		}
	}
	if got, ok := FilterNamed("shipped-by-a-later-build"); ok || got != FilterAll {
		t.Errorf("an unknown word reads back as %s", got.Name())
	}
}

func TestFold_FindsByNameAndGroupButNotAcrossThem(t *testing.T) {
	t.Parallel()
	fold := Fold("Alpha", "Spring\x00")
	for needle, want := range map[string]bool{"": true, "alpha": true, "spring": true, "haspr": false} {
		if got := Found(fold, Needle(needle)); got != want {
			t.Errorf("%q found %v, want %v", needle, got, want)
		}
	}
	if Fold("Alpha") != "alpha" || Needle("  AL ") != "al" {
		t.Error("folding is not lower case and trimmed")
	}
}

func TestDraft_BuildsTheInputOrSaysWhy(t *testing.T) {
	t.Parallel()
	in, err := Draft{Name: " 2.0 ", Description: " d ", Start: "2026-01-01", Release: "2026-02-01"}.Input("PROJ")
	if err != nil || in.Name != "2.0" || in.Description != "d" || in.ProjectKey != "PROJ" || in.ReleaseDate.IsZero() {
		t.Errorf("a new version built %+v, %v", in, err)
	}
	in, err = Draft{ID: "9", Name: "2.0"}.Input("PROJ")
	if err != nil || in.ProjectKey != "" || in.ID != "9" {
		t.Errorf("an edit built %+v, %v", in, err)
	}
	if _, err := (Draft{Name: "  "}).Input("PROJ"); !errors.Is(err, ErrNoName) {
		t.Errorf("a blank name said %v", err)
	}
	if _, err := (Draft{Name: "x", Start: "2026-02-01", Release: "2026-01-01"}).Input("PROJ"); !errors.Is(err, ErrReleasedBeforeStart) {
		t.Errorf("a release before the start said %v", err)
	}
	var bad *DateError
	if _, err := (Draft{Name: "x", Release: "soon"}).Input("PROJ"); !errors.As(err, &bad) || bad.Field != ReleaseDate || bad.Typed != "soon" {
		t.Errorf("a bad release date said %v", err)
	}
	if _, err := (Draft{Name: "x", Start: "soon"}).Input("PROJ"); !errors.As(err, &bad) || bad.Field != StartDate {
		t.Errorf("a bad start date said %v", err)
	}
}
