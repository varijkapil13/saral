package timeline

import (
	"context"
	"errors"
	"slices"
	"strings"
	"testing"
	"time"

	appcache "github.com/varijkapil13/saral/internal/app/cache"
	"github.com/varijkapil13/saral/internal/app/cache/cachetest"
	appterm "github.com/varijkapil13/saral/internal/app/term"
	"github.com/varijkapil13/saral/pkg/jira"
	"github.com/varijkapil13/saral/pkg/jira/jiratest"
)

const loadJQL = `project = "PROJ" ORDER BY created ASC`

var loadFailures = []struct {
	name string
	err  error
	is   func(error) bool
}{
	{"a 403", &jira.CapabilityError{Capability: jira.CapBoards, Reason: "not for this token"}, func(err error) bool {
		var target *jira.CapabilityError
		return errors.As(err, &target)
	}},
	{"a 429", &jira.RateLimitError{RetryAfter: time.Second}, func(err error) bool {
		var target *jira.RateLimitError
		return errors.As(err, &target)
	}},
	{"a transport failure", &jira.TransportError{Op: "search", Status: 503}, func(err error) bool {
		var target *jira.TransportError
		return errors.As(err, &target)
	}},
}

func loadFake(n int, opts ...jiratest.Option) *jiratest.Fake {
	return jiratest.New(append([]jiratest.Option{
		jiratest.WithProject("PROJ", jiratest.Scrum), jiratest.WithIssues(jiratest.Gen(n)),
	}, opts...)...)
}

func capsOf(t *testing.T, f *jiratest.Fake) jira.Capabilities {
	t.Helper()

	caps, err := f.Capabilities(t.Context(), "PROJ")
	if err != nil {
		t.Fatalf("reading the fake's capabilities: %v", err)
	}
	return caps
}

func configFor(t *testing.T, f *jiratest.Fake) Config {
	t.Helper()
	return Config{Caps: capsOf(t, f), Now: testClock()}
}

type brokenCache struct{ appcache.Cache }

var errDiskFull = errors.New("disk full")

func (brokenCache) PutRows(string, []jira.Issue, bool) error { return errDiskFull }

func (brokenCache) Forget(string) error { return errDiskFull }

func TestLoader_LoadResolvesEveryIssueAndStoresThem(t *testing.T) {
	t.Parallel()

	f := loadFake(12)
	cache := cachetest.Open(t)
	l := NewLoader(f, cache)

	got, err := l.Load(t.Context(), loadJQL, configFor(t, f))
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if len(got.Issues) != 12 || got.Resolution.Len() != 12 || got.Truncated || got.Stored != nil {
		t.Fatalf("Load brought %d issues, %d resolved (truncated=%v, stored=%v), want 12 of each and no error",
			len(got.Issues), got.Resolution.Len(), got.Truncated, got.Stored)
	}
	if len(got.Fields.IDs()) == 0 {
		t.Error("Load resolved no date fields against the fake's catalogue")
	}
	snap, ok := cache.Rows(loadJQL)
	if !ok || len(snap.Issues) != 12 {
		t.Errorf("the cache holds %d rows (ok=%v) after a load, want 12", len(snap.Issues), ok)
	}
}

func TestLoader_LoadAsksForWhatABarDrawsAndATermMatches(t *testing.T) {
	t.Parallel()

	ids := projection(ResolveDateFields(nil, nil, nil)).IDs
	for _, want := range []string{"summary", "status", "parent", "assignee", "reporter", "priority", "labels", "duedate"} {
		if !slices.Contains(ids, want) {
			t.Errorf("the timeline projection %v does not ask for %s", ids, want)
		}
	}
}

func TestLoader_LoadStopsAtTheCapAndSaysSo(t *testing.T) {
	t.Parallel()

	f := loadFake(MaxIssues + 20)
	got, err := NewLoader(f, nil).Load(t.Context(), loadJQL, configFor(t, f))
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if len(got.Issues) != MaxIssues || !got.Truncated {
		t.Errorf("Load brought %d issues (truncated=%v), want the cap of %d and truncated", len(got.Issues), got.Truncated, MaxIssues)
	}
}

func TestLoader_LoadKeepsTheRowsWhenTheCacheWillNotTakeThem(t *testing.T) {
	t.Parallel()

	f := loadFake(3)
	got, err := NewLoader(f, brokenCache{}).Load(t.Context(), loadJQL, configFor(t, f))
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if len(got.Issues) != 3 || !errors.Is(got.Stored, errDiskFull) {
		t.Errorf("Load brought %d issues and stored=%v, want 3 and the disk error beside them", len(got.Issues), got.Stored)
	}
}

func TestLoader_LoadPassesAFailureThroughWhole(t *testing.T) {
	t.Parallel()

	for _, step := range []struct {
		name  string
		after int
	}{{"reading the field catalogue", 0}, {"searching", 1}} {
		for _, tc := range loadFailures {
			t.Run(step.name+"/"+tc.name, func(t *testing.T) {
				t.Parallel()

				f := loadFake(3)
				l := NewLoader(f, nil)
				cfg := configFor(t, f)
				if step.after > 0 {
					if _, err := l.search.Fields(t.Context()); err != nil {
						t.Fatalf("warming the catalogue: %v", err)
					}
				}
				f.FailNext(tc.err)
				_, err := l.Load(t.Context(), loadJQL, cfg)
				if !tc.is(err) || err.Error() != tc.err.Error() {
					t.Errorf("Load returned %v, want %v unwrapped", err, tc.err)
				}
			})
		}
	}
}

func TestLoader_LoadReadsNoSprintWhereBoardsAreOff(t *testing.T) {
	t.Parallel()

	f := loadFake(4, jiratest.WithCapabilities(jiratest.CapReason(jira.CapBoards, "no boards here")))
	l := NewLoader(f, nil)
	if l.sprints(capsOf(t, f)) != nil {
		t.Error("a session without boards was handed a sprint reader")
	}
	if l.sprints(capsOf(t, loadFake(1))) == nil {
		t.Error("a session with boards was handed no sprint reader")
	}
}

func TestLoader_StoredResolvesWhatTheLastSessionLeft(t *testing.T) {
	t.Parallel()

	f := loadFake(1)
	cache := cachetest.Open(t)
	l := NewLoader(nil, cache)
	if l.Live() {
		t.Fatal("a loader with no client says it can ask a site")
	}
	if _, ok := l.Stored(loadJQL, configFor(t, f)); ok {
		t.Fatal("an empty cache produced a snapshot")
	}
	if err := cache.PutRows(loadJQL, jiratest.Gen(5), false); err != nil {
		t.Fatalf("storing rows: %v", err)
	}
	snap, ok := l.Stored(loadJQL, configFor(t, f))
	if !ok || len(snap.Issues) != 5 || snap.Resolution.Len() != 5 || snap.StoredAt.IsZero() {
		t.Errorf("Stored = %d issues, %d resolved, at %v (ok=%v), want 5 resolved with a time",
			len(snap.Issues), snap.Resolution.Len(), snap.StoredAt, ok)
	}
	if len(snap.Resolution.Warnings()) != 0 {
		t.Errorf("a pass with no catalogue warned %v, want nothing", snap.Resolution.Warnings())
	}
}

func TestLoader_PurgeForgetsTheStoredRows(t *testing.T) {
	t.Parallel()

	cache := cachetest.Open(t)
	if err := cache.PutRows(loadJQL, jiratest.Gen(2), false); err != nil {
		t.Fatalf("storing rows: %v", err)
	}
	if err := NewLoader(loadFake(2), cache).Purge(loadJQL); err != nil {
		t.Fatalf("Purge: %v", err)
	}
	if _, ok := cache.Rows(loadJQL); ok {
		t.Error("the rows are still stored after a purge")
	}
	if err := NewLoader(loadFake(2), brokenCache{}).Purge(loadJQL); !errors.Is(err, errDiskFull) {
		t.Errorf("Purge over a broken cache returned %v, want the disk error", err)
	}
	if err := NewLoader(nil, nil).Purge(loadJQL); err != nil {
		t.Errorf("Purge with nothing to purge returned %v", err)
	}
}

func TestLoader_MarkersReadVersionsAndEveryBoardsSprints(t *testing.T) {
	t.Parallel()

	f := loadFake(2)
	got, err := NewLoader(f, nil).Markers(t.Context(), "PROJ", capsOf(t, f))
	if err != nil {
		t.Fatalf("Markers: %v", err)
	}
	if len(got.Versions) == 0 || len(got.Sprints) == 0 || len(got.Notes) != 0 {
		t.Errorf("Markers = %d versions, %d sprints, notes %v; want some of each and no notes",
			len(got.Versions), len(got.Sprints), got.Notes)
	}
}

func TestLoader_MarkersTurnEachFailureIntoANote(t *testing.T) {
	t.Parallel()

	for _, tc := range loadFailures {
		t.Run("versions/"+tc.name, func(t *testing.T) {
			t.Parallel()

			f := loadFake(2)
			caps := capsOf(t, f)
			f.FailNext(tc.err)
			got, err := NewLoader(f, nil).Markers(t.Context(), "PROJ", caps)
			if err != nil {
				t.Fatalf("Markers: %v", err)
			}
			if len(got.Notes) != 1 || !strings.HasPrefix(got.Notes[0], "no version markers: ") || len(got.Sprints) == 0 {
				t.Errorf("Markers = notes %v and %d sprints, want one version note and the sprints regardless", got.Notes, len(got.Sprints))
			}
		})
		t.Run("boards/"+tc.name, func(t *testing.T) {
			t.Parallel()

			f := loadFake(2)
			caps := capsOf(t, f)
			l := NewLoader(f, nil)
			failing := &failOn{Fake: f, method: "Boards", err: tc.err}
			l.client = failing
			got, err := l.Markers(t.Context(), "PROJ", caps)
			if err != nil {
				t.Fatalf("Markers: %v", err)
			}
			if len(got.Notes) != 1 || !strings.HasPrefix(got.Notes[0], "no sprint markers: ") || len(got.Versions) == 0 {
				t.Errorf("Markers = notes %v and %d versions, want one sprint note and the versions regardless", got.Notes, len(got.Versions))
			}
		})
		t.Run("sprints/"+tc.name, func(t *testing.T) {
			t.Parallel()

			f := loadFake(2)
			caps := capsOf(t, f)
			l := NewLoader(f, nil)
			l.client = &failOn{Fake: f, method: "Sprints", err: tc.err}
			got, err := l.Markers(t.Context(), "PROJ", caps)
			if err != nil {
				t.Fatalf("Markers: %v", err)
			}
			if len(got.Notes) == 0 || !strings.HasPrefix(got.Notes[0], "no sprint markers from ") || len(got.Sprints) != 0 {
				t.Errorf("Markers = notes %v and %d sprints, want a note per board and no sprints", got.Notes, len(got.Sprints))
			}
		})
	}
}

func TestLoader_MarkersSayWhyThereAreNoSprintsWhereBoardsAreOff(t *testing.T) {
	t.Parallel()

	f := loadFake(2, jiratest.WithCapabilities(jiratest.CapReason(jira.CapBoards, "no boards here")))
	got, err := NewLoader(f, nil).Markers(t.Context(), "PROJ", capsOf(t, f))
	if err != nil {
		t.Fatalf("Markers: %v", err)
	}
	if !slices.Equal(got.Notes, []string{"no sprint markers: no boards here"}) || slices.Contains(f.Calls(), "Boards") {
		t.Errorf("Markers = notes %v after calls %v, want the capability's reason and no board read", got.Notes, f.Calls())
	}
}

func TestLoader_MarkersStopWhenTheCallerGoesAway(t *testing.T) {
	t.Parallel()

	f := loadFake(2)
	caps := capsOf(t, f)
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if _, err := NewLoader(f, nil).Markers(ctx, "PROJ", caps); !errors.Is(err, context.Canceled) {
		t.Errorf("Markers on a cancelled context returned %v, want context.Canceled", err)
	}
}

// failOn fails every call to one method with err and passes the rest through.
type failOn struct {
	*jiratest.Fake
	method string
	err    error
}

func (f *failOn) Boards(ctx context.Context, project string) ([]jira.Board, error) {
	if f.method == "Boards" {
		return nil, f.err
	}
	return f.Fake.Boards(ctx, project)
}

func (f *failOn) Sprints(ctx context.Context, boardID int64, states ...jira.SprintState) (jira.Page[jira.Sprint], error) {
	if f.method == "Sprints" {
		return jira.Page[jira.Sprint]{}, f.err
	}
	return f.Fake.Sprints(ctx, boardID, states...)
}

func TestMarkers_ReleasesLeaveOutTheUndatedAndTheArchived(t *testing.T) {
	t.Parallel()

	m := Markers{Versions: []jira.Version{
		{ID: "v1", Name: "live", ReleaseDate: day(2026, time.April, 2)},
		{ID: "v2", Name: "archived", ReleaseDate: day(2026, time.April, 9), Archived: true},
		{ID: "v3", Name: "undated"},
	}}
	if got := m.Releases(); !slices.Equal(got, []Release{{Name: "live", On: day(2026, time.April, 2)}}) {
		t.Errorf("Releases = %v, want only the live dated version", got)
	}
}

func TestMarkers_SprintSpansBucketBoundariesInTheZoneGiven(t *testing.T) {
	t.Parallel()

	start := time.Date(2026, time.March, 1, 23, 30, 0, 0, time.UTC)
	end := time.Date(2026, time.March, 14, 23, 30, 0, 0, time.UTC)
	m := Markers{Sprints: []jira.Sprint{
		{ID: 1, Name: "dated", Start: &start, End: &end},
		{ID: 2, Name: "future"},
	}}
	east := time.FixedZone("east", 2*60*60)
	got := m.SprintSpans(east)
	want := []SprintSpan{{Name: "dated", From: day(2026, time.March, 2), To: day(2026, time.March, 15)}}
	if !slices.Equal(got, want) {
		t.Errorf("SprintSpans = %v, want %v", got, want)
	}
}

func TestMatchesTerms_IsAndAcrossFacetsAndOrWithinOne(t *testing.T) {
	t.Parallel()

	iss := &jira.Issue{
		Key:      "PROJ-1",
		Assignee: &jira.User{AccountID: "ada"},
		Status:   jira.Status{ID: "3"},
		Type:     jira.IssueType{ID: "10"},
		Labels:   []string{"ui"},
	}
	assignee := func(id string) appterm.Term { return appterm.Term{Facet: appterm.FacetAssignee, ID: id} }
	status := func(id string) appterm.Term { return appterm.Term{Facet: appterm.FacetStatus, ID: id} }
	tests := []struct {
		name  string
		terms appterm.Terms
		want  bool
	}{
		{"no terms", nil, true},
		{"one value that matches", appterm.Terms{assignee("ada")}, true},
		{"either of two values", appterm.Terms{assignee("bob"), assignee("ada")}, true},
		{"neither of two values", appterm.Terms{assignee("bob"), assignee("cy")}, false},
		{"two facets that both match", appterm.Terms{assignee("ada"), status("3")}, true},
		{"two facets where one does not", appterm.Terms{assignee("ada"), status("4")}, false},
		{"unassigned on an assigned issue", appterm.Terms{assignee("")}, false},
		{"no priority on an issue with none", appterm.Terms{{Facet: appterm.FacetPriority}}, true},
		{"a label it carries", appterm.Terms{{Facet: appterm.FacetLabel, ID: "ui"}}, true},
		{"a type it is not", appterm.Terms{{Facet: appterm.FacetType, ID: "11"}}, false},
		{"no reporter on an issue with none", appterm.Terms{{Facet: appterm.FacetReporter}}, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			if got := MatchesTerms(iss, tt.terms); got != tt.want {
				t.Errorf("MatchesTerms = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestCompareRanges_PutsTheEarliestFirstAndTheUndatedLast(t *testing.T) {
	t.Parallel()

	early := Range{Start: day(2026, time.March, 1), End: day(2026, time.March, 2), From: FromStartAndDue}
	late := Range{Start: day(2026, time.March, 9), End: day(2026, time.March, 9), From: FromOneDate}
	none := Range{}
	ranges := []Range{none, late, early}
	slices.SortStableFunc(ranges, CompareRanges)
	if ranges[0] != early || ranges[1] != late || ranges[2] != none {
		t.Errorf("sorted to %v, want early, late, undated", ranges)
	}
	if CompareRanges(early, early) != 0 || CompareRanges(none, none) != 0 {
		t.Error("a range does not compare equal to itself")
	}
}
