package sprint

import (
	"slices"

	appcache "github.com/varijkapil13/saral/internal/app/cache"
)

// Cache is the stored copy of a project's sprints, which draws a first frame
// before the site has said anything. It is inert over a cache that does not
// keep sprints, or none.
type Cache struct {
	held appcache.SprintsCache
}

// NewCache takes the sprint half of c, if it has one.
func NewCache(c appcache.Cache) Cache {
	held, _ := c.(appcache.SprintsCache)
	return Cache{held: held}
}

// Stored is a listing read back from the cache. Stale is one past its TTL.
type Stored struct {
	Listing
	Stale bool
}

// Load is the listing last kept for project, sorted, without its closed
// sprints unless closed asks for them.
func (c Cache) Load(project string, closed bool) (Stored, bool) {
	if c.held == nil {
		return Stored{}, false
	}
	snap, ok := c.held.Sprints(project)
	if !ok {
		return Stored{}, false
	}
	sprints := slices.Clone(snap.Sprints)
	if !closed {
		sprints = DropClosed(sprints)
	}
	return Stored{
		Listing: Listing{Boards: snap.Boards, More: snap.More, Sprints: Sort(sprints)},
		Stale:   snap.Stale,
	}, true
}

// Keep stores a listing for project; closed says whether the closed sprints
// were asked for.
func (c Cache) Keep(project string, l Listing, closed bool) error {
	if c.held == nil {
		return nil
	}
	return c.held.PutSprints(project, appcache.SprintsSnapshot{
		Boards: l.Boards, More: l.More, Sprints: l.Sprints, Closed: closed,
	})
}

// Forget drops what is stored for project.
func (c Cache) Forget(project string) error {
	if c.held == nil {
		return nil
	}
	return c.held.ForgetSprints(project)
}
