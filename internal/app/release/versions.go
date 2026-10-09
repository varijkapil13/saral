// Package release is a project's versions: reading and saving them, the
// facets they are sorted and filtered by, the flow that ships one, and the
// bulk assignment that puts one on the issues a query matches.
package release

import (
	"context"

	appcache "github.com/varijkapil13/saral/internal/app/cache"
	"github.com/varijkapil13/saral/pkg/jira"
)

// Load reads a project's versions. Every version in the answer has a nil
// Unresolved, which says nobody has counted what is open on it rather than
// that nothing is.
func Load(ctx context.Context, r jira.VersionReader, project string) ([]jira.Version, error) {
	return r.Versions(ctx, project)
}

// Save creates a version or updates one.
func Save(ctx context.Context, w jira.Releaser, in jira.VersionInput) (jira.Version, error) {
	return w.SaveVersion(ctx, in)
}

// CountOpen reads how many issues are still open on one version. No version
// read reports the number, so it is a request of its own.
func CountOpen(ctx context.Context, r jira.VersionReader, id string) (int, error) {
	return r.UnresolvedCount(ctx, id)
}

// UpdateOf is the input that changes one thing about a version and nothing
// else.
//
// Every field has to be sent. The endpoint empties a name, a description or a
// date it is not given, so an input built to archive a version and nothing more
// still carries the four values the version already had — otherwise archiving
// 2.0 also forgets when it was meant to ship.
func UpdateOf(v jira.Version) jira.VersionInput {
	return jira.VersionInput{
		ID:          v.ID,
		Name:        v.Name,
		Description: v.Description,
		StartDate:   v.StartDate,
		ReleaseDate: v.ReleaseDate,
	}
}

// Archiving is the input that archives v, or unarchives it, and changes
// nothing else.
func Archiving(v jira.Version, archived bool) jira.VersionInput {
	in := UpdateOf(v)
	in.Archived = &archived
	return in
}

// Put replaces a version by id, or appends it. A create comes back with an id
// the list has never seen and belongs at the end, which is where the project
// put it.
func Put(versions []jira.Version, v jira.Version) []jira.Version {
	for i := range versions {
		if versions[i].ID == v.ID {
			// A count already read stays read: the write that came back knows
			// nothing about what is open, and nil would say nobody had asked.
			if v.Unresolved == nil {
				v.Unresolved = versions[i].Unresolved
			}
			versions[i] = v
			return versions
		}
	}
	return append(versions, v)
}

// ByID finds a version by id.
func ByID(versions []jira.Version, id string) (jira.Version, bool) {
	for i := range versions {
		if versions[i].ID == id {
			return versions[i], true
		}
	}
	return jira.Version{}, false
}

// MoveTargets are the versions the open issues on one version could move to:
// the ones that are neither this one, nor already released, nor archived.
// Moving open work onto a version that has shipped is not somewhere to put it.
// With ownProject, only versions of from's own project are offered, which is
// what a list spanning several projects needs.
func MoveTargets(versions []jira.Version, from jira.Version, ownProject bool) []jira.Version {
	out := make([]jira.Version, 0, len(versions))
	for i := range versions {
		v := versions[i]
		if v.ID == from.ID || v.Released || v.Archived {
			continue
		}
		if ownProject && v.ProjectID != from.ProjectID {
			continue
		}
		out = append(out, v)
	}
	return out
}

// Cache is a project's versions as the release list last read them. A session
// whose cache does not keep versions makes every read a miss and every write a
// no-op.
type Cache struct{ held appcache.VersionsCache }

// NewCache wraps whatever cache the session holds.
func NewCache(c any) Cache {
	held, ok := c.(appcache.VersionsCache)
	if !ok || held == nil {
		return Cache{}
	}
	return Cache{held: held}
}

// Held is whether the session's cache keeps versions at all.
func (c Cache) Held() bool { return c.held != nil }

// Recall is what the project last had. The open counts are never stored, so
// every version in it says nobody has counted.
func (c Cache) Recall(project string) (appcache.VersionsSnapshot, bool) {
	if c.held == nil {
		return appcache.VersionsSnapshot{}, false
	}
	return c.held.Versions(project)
}

// Keep stores the project's versions for next time.
func (c Cache) Keep(project string, versions []jira.Version) error {
	if c.held == nil {
		return nil
	}
	return c.held.PutVersions(project, versions)
}

// Forget drops the stored copy of the project's versions.
func (c Cache) Forget(project string) error {
	if c.held == nil {
		return nil
	}
	return c.held.ForgetVersions(project)
}
