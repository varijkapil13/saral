package sprint

import (
	"errors"
	"testing"

	appcache "github.com/varijkapil13/saral/internal/app/cache"
	"github.com/varijkapil13/saral/pkg/jira"
)

type memCache struct {
	appcache.Cache
	held map[string]appcache.SprintsSnapshot
	fail error
}

func (c *memCache) Sprints(project string) (appcache.SprintsSnapshot, bool) {
	snap, ok := c.held[project]
	return snap, ok
}

func (c *memCache) PutSprints(project string, snap appcache.SprintsSnapshot) error {
	if c.fail != nil {
		return c.fail
	}
	c.held[project] = snap
	return nil
}

func (c *memCache) ForgetSprints(project string) error {
	if c.fail != nil {
		return c.fail
	}
	delete(c.held, project)
	return nil
}

func TestCache_KeepsALoadsSortedAndDropsClosedUnlessAsked(t *testing.T) {
	t.Parallel()

	mem := &memCache{held: map[string]appcache.SprintsSnapshot{}}
	c := NewCache(mem)
	l := Listing{
		Boards: []jira.Board{{ID: 1}},
		More:   2,
		Sprints: []jira.Sprint{
			{ID: 1, State: jira.SprintClosed}, {ID: 2, State: jira.SprintFuture}, {ID: 3, State: jira.SprintActive},
		},
	}
	if err := c.Keep("PROJ", l, true); err != nil {
		t.Fatalf("Keep: %v", err)
	}
	if !mem.held["PROJ"].Closed {
		t.Error("a listing with the closed sprints was stored as one without")
	}
	got, ok := c.Load("PROJ", false)
	if !ok || got.More != 2 || len(got.Sprints) != 2 || got.Sprints[0].ID != 3 {
		t.Errorf("loaded %+v, %v; want the running then the planned sprint", got, ok)
	}
	if len(mem.held["PROJ"].Sprints) != 3 {
		t.Error("loading changed what is stored")
	}
	if err := c.Forget("PROJ"); err != nil {
		t.Fatalf("Forget: %v", err)
	}
	if _, ok := c.Load("PROJ", true); ok {
		t.Error("a forgotten listing loaded")
	}
}

func TestCache_PassesAStoreErrorThroughAndIsInertWithoutOne(t *testing.T) {
	t.Parallel()

	want := errors.New("disk full")
	c := NewCache(&memCache{held: map[string]appcache.SprintsSnapshot{}, fail: want})
	if err := c.Keep("PROJ", Listing{}, false); !unchanged(err, want) {
		t.Errorf("Keep returned %v, want %v", err, want)
	}
	if err := c.Forget("PROJ"); !unchanged(err, want) {
		t.Errorf("Forget returned %v, want %v", err, want)
	}
	none := NewCache(nil)
	if _, ok := none.Load("PROJ", true); ok {
		t.Error("no cache loaded something")
	}
	if none.Keep("PROJ", Listing{}, true) != nil || none.Forget("PROJ") != nil {
		t.Error("no cache failed")
	}
}
