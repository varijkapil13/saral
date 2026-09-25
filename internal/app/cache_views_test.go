package app

import (
	"reflect"
	"testing"
	"time"

	"github.com/varijkapil13/saral/pkg/jira"
	"github.com/varijkapil13/saral/pkg/jira/jiratest"
)

func testSprints() SprintsSnapshot {
	start := testNow.Add(-72 * time.Hour).UTC()
	end := testNow.Add(240 * time.Hour).UTC()
	return SprintsSnapshot{
		Boards: []jira.Board{{ID: 7, Name: "Board", Type: jira.BoardScrum, ProjectKey: "PROJ"}},
		More:   2,
		Sprints: []jira.Sprint{
			{ID: 70, BoardID: 7, Name: "Sprint 1", Goal: "ship it", State: jira.SprintActive, Start: &start, End: &end},
			{ID: 71, BoardID: 7, Name: "Sprint 2", State: jira.SprintFuture},
		},
		Closed: true,
	}
}

func TestSprints_ComeBackAsTheyWereStoredAndAgeIntoStale(t *testing.T) {
	t.Parallel()

	cache, clk := newTestCache(t)
	stored := testSprints()
	if err := cache.PutSprints(" PROJ ", stored); err != nil {
		t.Fatalf("PutSprints: %v", err)
	}
	got, ok := cache.Sprints("PROJ")
	if !ok {
		t.Fatal("the sprints just stored are not there")
	}
	if got.Stale || !got.StoredAt.Equal(testNow) {
		t.Errorf("Stale=%t StoredAt=%s, want a current entry stored at %s", got.Stale, got.StoredAt, testNow)
	}
	got.StoredAt, got.Stale = time.Time{}, false
	if !reflect.DeepEqual(got, stored) {
		t.Errorf("the sprints came back as\n%+v\nwant\n%+v", got, stored)
	}
	if _, ok := cache.Sprints("OTHER"); ok {
		t.Error("another project's key answered with this one's sprints")
	}

	clk.at = testNow.Add(KindSprints.TTL() + time.Second)
	if got, ok := cache.Sprints("PROJ"); !ok || !got.Stale {
		t.Errorf("past the TTL ok=%t stale=%t, want the entry served and badged", ok, got.Stale)
	}

	if err := cache.ForgetSprints("PROJ"); err != nil {
		t.Fatalf("ForgetSprints: %v", err)
	}
	if _, ok := cache.Sprints("PROJ"); ok {
		t.Error("the sprints are still there after ForgetSprints")
	}
}

func TestVersions_ComeBackWithoutACountAndAgeIntoStale(t *testing.T) {
	t.Parallel()

	cache, clk := newTestCache(t)
	stored := jiratest.VersionsFor("PROJ")
	open := 4
	stored[1].Unresolved = &open
	if err := cache.PutVersions("PROJ", stored); err != nil {
		t.Fatalf("PutVersions: %v", err)
	}
	if stored[1].Unresolved == nil {
		t.Error("PutVersions cleared the caller's count; it must strip only its own copy")
	}
	got, ok := cache.Versions("PROJ")
	if !ok || got.Stale {
		t.Fatalf("ok=%t stale=%t, want the versions just stored, current", ok, got.Stale)
	}
	if len(got.Versions) != len(stored) {
		t.Fatalf("%d versions came back, want %d", len(got.Versions), len(stored))
	}
	for i := range got.Versions {
		if got.Versions[i].Unresolved != nil {
			t.Errorf("%s came back with a stored count; an open count is never served from disk", got.Versions[i].Name)
		}
		want := stored[i]
		want.Unresolved = nil
		if !reflect.DeepEqual(got.Versions[i], want) {
			t.Errorf("version %d came back as %+v, want %+v", i, got.Versions[i], want)
		}
	}

	clk.at = testNow.Add(KindVersions.TTL() + time.Second)
	if got, ok := cache.Versions("PROJ"); !ok || !got.Stale {
		t.Errorf("past the TTL ok=%t stale=%t, want the entry served and badged", ok, got.Stale)
	}
	if err := cache.ForgetVersions("PROJ"); err != nil {
		t.Fatalf("ForgetVersions: %v", err)
	}
	if _, ok := cache.Versions("PROJ"); ok {
		t.Error("the versions are still there after ForgetVersions")
	}
}

func TestSprintsAndVersions_ANilOrUnscopedCacheHoldsNothing(t *testing.T) {
	t.Parallel()

	var nothing *DiskCache
	if err := nothing.PutSprints("PROJ", testSprints()); err != nil {
		t.Errorf("a nil cache refused a write: %v", err)
	}
	if _, ok := nothing.Sprints("PROJ"); ok {
		t.Error("a nil cache answered")
	}
	if _, ok := nothing.Versions("PROJ"); ok {
		t.Error("a nil cache answered")
	}

	cache, _ := newTestCache(t)
	if err := cache.PutVersions("  ", jiratest.VersionsFor("PROJ")); err != nil {
		t.Fatalf("PutVersions with no project: %v", err)
	}
	if _, ok := cache.Versions(""); ok {
		t.Error("a session with no project read versions back; a version belongs to one project")
	}
}
