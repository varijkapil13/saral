package timeline

import (
	"context"
	"fmt"
	"time"

	appcache "github.com/varijkapil13/saral/internal/app/cache"
	appquery "github.com/varijkapil13/saral/internal/app/query"
	"github.com/varijkapil13/saral/pkg/jira"
)

// pageSize is how many issues one request asks for, and MaxIssues is how many
// pages of them a timeline will walk. A chart is not readable at ten thousand
// bars and the walk has to end somewhere the user can be told about, so the cap
// is named on screen rather than silently applied.
const (
	pageSize  = 50
	MaxIssues = 500
)

// maxSprints bounds the walk over one board's sprints. A board with years of
// closed sprints behind it would otherwise page through all of them for
// boundaries nobody can see on a chart of the current quarter.
const maxSprints = 200

// Client is what a timeline reads with: the search and the field catalogue,
// the one sprint rule 4 needs, and the versions and boards the markers come
// from.
type Client interface {
	appquery.SearchClient
	jira.SprintReader
	jira.VersionReader
	jira.BoardReader
}

// Loader reads a timeline's issues, resolves their dates and keeps them in the
// cache, and reads the version and sprint markers drawn above them.
type Loader struct {
	client Client
	search *appquery.Search
	cache  appcache.Cache
}

// NewLoader builds a Loader. A nil client is a session with no site to ask, and
// a nil cache is one with nowhere to keep rows.
func NewLoader(client Client, cache appcache.Cache) *Loader {
	l := &Loader{client: client, cache: cache}
	if client != nil {
		l.search = appquery.NewSearch(client)
	}
	return l
}

// Live reports whether there is a site to ask.
func (l *Loader) Live() bool { return l.search != nil }

// Config is what the cascade is built with: the field names this profile chose,
// the capabilities that say whether a sprint can be read and which zone days
// are bucketed in, and the clock.
type Config struct {
	Start []string
	End   []string
	Caps  jira.Capabilities
	Now   func() time.Time
}

// Loaded is what one read brought back. Stored is the error from writing it to
// the cache: the read worked, and a cache that could not be written is worth a
// warning and nothing more.
type Loaded struct {
	Fields     DateFields
	Issues     []jira.Issue
	Missing    []string
	Resolution Resolution
	// Truncated is a search with more issues in it than MaxIssues.
	Truncated bool
	Stored    error
}

// projection is the date cascade's own fields plus what a bar draws and what a
// local filter matches by. Assignee, reporter, priority and labels are in it
// because MatchesTerms reads them off this read's own issues, and none of the
// four is otherwise asked for.
func projection(fields DateFields) appquery.Projection {
	return fields.Projection().With(
		"summary", "issuetype", "status", "parent", "subtasks",
		"assignee", "reporter", "priority", "labels",
	)
}

// Load reads the site's field catalogue, every issue jql finds up to MaxIssues,
// and resolves the cascade over them.
func (l *Loader) Load(ctx context.Context, jql string, cfg Config) (Loaded, error) {
	catalogue, err := l.search.Fields(ctx)
	if err != nil {
		return Loaded{}, err
	}
	fields := ResolveDateFields(catalogue, cfg.Start, cfg.End)
	res, err := l.search.Run(ctx, appquery.Request{JQL: jql, Projection: projection(fields), MaxResults: pageSize})
	if err != nil {
		return Loaded{}, err
	}
	issues, err := jira.Collect(ctx, res.Page, MaxIssues)
	if err != nil {
		return Loaded{}, err
	}
	zone, reason := cfg.Caps.Zone()
	dates := NewDates(fields,
		WithSprints(l.sprints(cfg.Caps)),
		WithZone(zone, reason),
		WithNow(cfg.Now))
	resolution, err := dates.Resolve(ctx, issues)
	if err != nil {
		return Loaded{}, err
	}
	return Loaded{
		Fields: fields, Issues: issues, Missing: res.Missing,
		Resolution: resolution,
		Truncated:  len(issues) >= MaxIssues && res.Page.HasMore(),
		Stored:     l.keep(jql, issues, res.Page.HasMore()),
	}, nil
}

// sprints is what rule 4 reads a sprint's own dates with. A session with no
// boards has none, and the cascade says so rather than dating an issue off a
// sprint it could not read.
func (l *Loader) sprints(caps jira.Capabilities) SprintDates {
	if l.client == nil || !caps.Allows(jira.CapBoards) {
		return nil
	}
	return l.client
}

func (l *Loader) keep(jql string, issues []jira.Issue, more bool) error {
	if l.cache == nil {
		return nil
	}
	return l.cache.PutRows(jql, issues, more)
}

// Snapshot is a chart drawn from what the last session left on disk.
type Snapshot struct {
	Issues     []jira.Issue
	Resolution Resolution
	StoredAt   time.Time
}

// Stored resolves the rows the last session left on disk for jql, before
// anything is asked of the site.
//
// The cascade it runs has no field catalogue behind it, so it reaches only the
// platform fields — a due date, a created stamp, a release date — and a bar it
// produces can move a rule when the real read lands. The field problems this
// pass would report are dropped with the catalogue: a name that did not resolve
// against a catalogue nobody read is not news.
func (l *Loader) Stored(jql string, cfg Config) (Snapshot, bool) {
	if l.cache == nil {
		return Snapshot{}, false
	}
	snap, ok := l.cache.Rows(jql)
	if !ok || len(snap.Issues) == 0 {
		return Snapshot{}, false
	}
	zone, reason := cfg.Caps.Zone()
	dates := NewDates(ResolveDateFields(nil, nil, nil), WithZone(zone, reason), WithNow(cfg.Now))
	res, err := dates.Resolve(context.Background(), snap.Issues)
	if err != nil {
		return Snapshot{}, false
	}
	return Snapshot{Issues: snap.Issues, Resolution: res, StoredAt: snap.StoredAt}, true
}

// Purge drops the field catalogue held in memory and the rows stored for jql,
// so that the next Load asks the site for both.
func (l *Loader) Purge(jql string) error {
	if l.search != nil {
		l.search.Invalidate()
	}
	if l.cache == nil {
		return nil
	}
	return l.cache.Forget(jql)
}

// Markers are the version release dates and sprint boundaries drawn above the
// bars. Notes says which of them could not be had and why.
type Markers struct {
	Versions []jira.Version
	Sprints  []jira.Sprint
	Notes    []string
}

// Markers reads the version release dates and the sprint boundaries. Every
// failure is a note rather than an error: a board a token cannot see must not
// empty a chart that has nothing to do with it. The one error is a caller that
// went away.
func (l *Loader) Markers(ctx context.Context, project string, caps jira.Capabilities) (Markers, error) {
	var out Markers
	versions, err := l.client.Versions(ctx, project)
	if err != nil {
		if ctxErr := ctx.Err(); ctxErr != nil {
			return Markers{}, ctxErr
		}
		reason, _ := jira.Reason(err)
		out.Notes = append(out.Notes, "no version markers: "+reason)
	}
	out.Versions = versions
	if !caps.Allows(jira.CapBoards) {
		out.Notes = append(out.Notes, "no sprint markers: "+caps.Capability(jira.CapBoards).Reason)
		return out, nil
	}
	found, err := l.client.Boards(ctx, project)
	if err != nil {
		if ctxErr := ctx.Err(); ctxErr != nil {
			return Markers{}, ctxErr
		}
		reason, _ := jira.Reason(err)
		out.Notes = append(out.Notes, "no sprint markers: "+reason)
		return out, nil
	}
	for _, board := range found {
		page, err := l.client.Sprints(ctx, board.ID, jira.SprintActive, jira.SprintFuture, jira.SprintClosed)
		if err != nil {
			reason, _ := jira.Reason(err)
			out.Notes = append(out.Notes, fmt.Sprintf("no sprint markers from %s: %s", board.Name, reason))
			continue
		}
		sprints, err := jira.Collect(ctx, page, maxSprints)
		if err != nil {
			reason, _ := jira.Reason(err)
			out.Notes = append(out.Notes, fmt.Sprintf("no sprint markers from %s: %s", board.Name, reason))
			continue
		}
		out.Sprints = append(out.Sprints, sprints...)
	}
	return out, nil
}

// Release is a version's release date.
type Release struct {
	Name string
	On   jira.Date
}

// Releases are the versions worth a marker: released or due on a date, and not
// archived.
func (m Markers) Releases() []Release {
	out := make([]Release, 0, len(m.Versions))
	for i := range m.Versions {
		v := &m.Versions[i]
		if v.ReleaseDate.IsZero() || v.Archived {
			continue
		}
		out = append(out, Release{Name: v.Name, On: v.ReleaseDate})
	}
	return out
}

// SprintSpan is a sprint's two boundaries as days.
type SprintSpan struct {
	Name string
	From jira.Date
	To   jira.Date
}

// SprintSpans are the sprints with both boundaries, each bucketed into a day in
// zone.
func (m Markers) SprintSpans(zone *time.Location) []SprintSpan {
	out := make([]SprintSpan, 0, len(m.Sprints))
	for _, s := range m.Sprints {
		if s.Start == nil || s.End == nil || s.Start.IsZero() || s.End.IsZero() {
			continue
		}
		out = append(out, SprintSpan{
			Name: s.Name,
			From: jira.DateOf(s.Start.In(zone)),
			To:   jira.DateOf(s.End.In(zone)),
		})
	}
	return out
}
