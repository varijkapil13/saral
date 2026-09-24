package app

import (
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/varijkapil13/saral/internal/store"
	"github.com/varijkapil13/saral/pkg/jira"
)

// SprintsSnapshot is a project's sprints as the sprints view last read them:
// the boards it asked, how many past its cap it did not, and the sprints on
// them. Closed says whether the read included the closed ones, which a view
// that does not want them filters out rather than trusting the list to lack
// them.
type SprintsSnapshot struct {
	Boards   []jira.Board
	More     int
	Sprints  []jira.Sprint
	Closed   bool
	StoredAt time.Time
	Stale    bool
}

// SprintsCache keeps a project's sprint list, so the sprints view draws its
// first frame from it rather than from a read of every board.
type SprintsCache interface {
	Sprints(project string) (SprintsSnapshot, bool)
	PutSprints(project string, snap SprintsSnapshot) error
	ForgetSprints(project string) error
}

// VersionsSnapshot is a project's versions as the release list last read them.
// Every Unresolved is nil: a count is the one thing a release decision turns on
// and is never served from disk.
type VersionsSnapshot struct {
	Versions []jira.Version
	StoredAt time.Time
	Stale    bool
}

// VersionsCache keeps a project's versions, so the release list draws its first
// frame from it.
type VersionsCache interface {
	Versions(project string) (VersionsSnapshot, bool)
	PutVersions(project string, versions []jira.Version) error
	ForgetVersions(project string) error
}

type wireSprints struct {
	Boards  []jira.Board  `json:"boards,omitempty"`
	More    int           `json:"more,omitempty"`
	Sprints []jira.Sprint `json:"sprints,omitempty"`
	Closed  bool          `json:"closed,omitempty"`
}

type wireVersions struct {
	Versions []jira.Version `json:"versions"`
}

func projectKey(project string) (string, bool) {
	key := strings.TrimSpace(project)
	return key, key != ""
}

// Sprints implements SprintsCache.
func (c *DiskCache) Sprints(project string) (SprintsSnapshot, bool) {
	key, ok := projectKey(project)
	if c == nil || c.db == nil || !ok {
		return SprintsSnapshot{}, false
	}
	rec, ok, err := c.db.Get(c.scope, string(KindSprints), key)
	if err != nil || !ok {
		return SprintsSnapshot{}, false
	}
	var entry wireSprints
	if err := json.Unmarshal(rec.Value, &entry); err != nil {
		return SprintsSnapshot{}, false
	}
	return SprintsSnapshot{
		Boards: entry.Boards, More: entry.More, Sprints: entry.Sprints, Closed: entry.Closed,
		StoredAt: rec.StoredAt, Stale: c.now().Sub(rec.StoredAt) > KindSprints.TTL(),
	}, true
}

// PutSprints implements SprintsCache.
func (c *DiskCache) PutSprints(project string, snap SprintsSnapshot) error {
	key, ok := projectKey(project)
	if c == nil || c.db == nil || !ok {
		return nil
	}
	value, err := json.Marshal(wireSprints{Boards: snap.Boards, More: snap.More, Sprints: snap.Sprints, Closed: snap.Closed})
	if err != nil {
		return fmt.Errorf("encoding a project's sprints: %w", err)
	}
	return c.putOne(KindSprints, key, value)
}

// ForgetSprints implements SprintsCache.
func (c *DiskCache) ForgetSprints(project string) error {
	return c.forgetOne(KindSprints, project)
}

// Versions implements VersionsCache.
func (c *DiskCache) Versions(project string) (VersionsSnapshot, bool) {
	key, ok := projectKey(project)
	if c == nil || c.db == nil || !ok {
		return VersionsSnapshot{}, false
	}
	rec, ok, err := c.db.Get(c.scope, string(KindVersions), key)
	if err != nil || !ok {
		return VersionsSnapshot{}, false
	}
	var entry wireVersions
	if err := json.Unmarshal(rec.Value, &entry); err != nil {
		return VersionsSnapshot{}, false
	}
	for i := range entry.Versions {
		entry.Versions[i].Unresolved = nil
	}
	return VersionsSnapshot{
		Versions: entry.Versions, StoredAt: rec.StoredAt,
		Stale: c.now().Sub(rec.StoredAt) > KindVersions.TTL(),
	}, true
}

// PutVersions implements VersionsCache.
func (c *DiskCache) PutVersions(project string, versions []jira.Version) error {
	key, ok := projectKey(project)
	if c == nil || c.db == nil || !ok {
		return nil
	}
	held := make([]jira.Version, len(versions))
	for i := range versions {
		held[i] = versions[i]
		held[i].Unresolved = nil
	}
	value, err := json.Marshal(wireVersions{Versions: held})
	if err != nil {
		return fmt.Errorf("encoding a project's versions: %w", err)
	}
	return c.putOne(KindVersions, key, value)
}

// ForgetVersions implements VersionsCache.
func (c *DiskCache) ForgetVersions(project string) error {
	return c.forgetOne(KindVersions, project)
}

func (c *DiskCache) putOne(kind Kind, key string, value []byte) error {
	if err := c.db.Put(c.scope, string(kind), store.Record{Key: key, Value: value, StoredAt: c.now()}); err != nil {
		return fmt.Errorf("writing %s %s: %w", kind, key, err)
	}
	if err := c.enforce(kind); err != nil {
		return err
	}
	c.gen.Add(1)
	return nil
}

func (c *DiskCache) forgetOne(kind Kind, project string) error {
	key, ok := projectKey(project)
	if c == nil || c.db == nil || !ok {
		return nil
	}
	if err := c.db.Delete(c.scope, string(kind), key); err != nil {
		return err
	}
	c.gen.Add(1)
	return nil
}
