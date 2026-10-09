// Package query is the coalescing search runner and the projections every
// context reads with.
package query

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
	"sync"

	"golang.org/x/sync/singleflight"

	"github.com/varijkapil13/saral/pkg/jira"
)

// Counter is the part of an adapter that can say how many issues a query
// matches. It is not on the jira.Client port because not every adapter can
// answer it: the platform search endpoint reports no total, so a count is a
// second call to a second endpoint, and a client that cannot make it should not
// have to pretend.
//
// Callers reach it through Search.Count, which reports the absence rather than
// failing on it.
type Counter interface {
	ApproximateCount(ctx context.Context, jql string) (int, error)
}

// SearchClient is what a search runs on: JQL, and the field catalogue that turns
// a field named in a profile into the ID to ask this site for. It is the pair of
// port roles this use case needs, and asking for the pair rather than for the
// whole port is what lets an adapter run a list before it can do anything else.
type SearchClient interface {
	jira.Searcher
	jira.FieldCatalogue
}

// Projection is the narrow set of fields one view needs.
//
// It is in three parts because only one of them can be written down. IDs are
// the platform's own field identifiers, which are the same on every site; Names
// and Custom are resolved against the site's field catalogue at runtime, which
// is the only way to ask for a custom field without writing a customfield_NNNNN
// into the source. A name the site has no field for is reported, never guessed.
type Projection struct {
	// Name describes the projection in an error a user might read.
	Name  string
	IDs   []string
	Names []string
	// Custom asks for every custom field this site defines, by taking their IDs
	// from the catalogue Search already holds. It is how story points, the
	// sprint, the epic link and the acceptance criteria — most of what an issue
	// is on a configured site — arrive without a customfield_NNNNN anywhere in
	// the source and without a name having to be written down first.
	//
	// It belongs on a read of one issue. A site with two hundred custom fields
	// turns a page of fifty rows into ten thousand values nothing renders, so a
	// list projection leaves it off.
	Custom bool
}

// With returns a copy of the projection asking for more field IDs, which is how
// a caller adds something it resolved elsewhere — a board's estimation field,
// say, which comes from the board configuration rather than from a name.
func (p Projection) With(ids ...string) Projection {
	p.IDs = append(slices.Clone(p.IDs), ids...)
	return p
}

// WithNames returns a copy of the projection asking for more fields by name.
func (p Projection) WithNames(names ...string) Projection {
	p.Names = append(slices.Clone(p.Names), names...)
	return p
}

// WithCustom returns a copy of the projection asking for this site's custom
// fields as well.
func (p Projection) WithCustom() Projection {
	p.Custom = true
	return p
}

// ListProjection is what a row in a list needs and nothing else. Six fields,
// not sixty: a bare issue read returns every field on the site, and on a site
// with ninety custom fields that is ninety nulls per row.
func ListProjection() Projection {
	return Projection{
		Name: "issue list",
		IDs:  []string{"summary", "status", "assignee", "priority", "updated", "issuetype"},
	}
}

// DetailProjection is what one open issue needs: the platform fields an issue
// screen draws, plus whatever custom fields this site defines. It is wider than
// a list row and still narrower than a wildcard, whose values arrive with
// nothing to label them by.
func DetailProjection() Projection {
	return Projection{
		Name:   "issue detail",
		Custom: true,
		IDs: []string{
			"summary", "status", "assignee", "priority", "updated", "issuetype",
			"description", "project", "reporter", "labels", "components",
			"fixVersions", "parent", "subtasks", "issuelinks", "duedate",
			"created", "resolution", "resolutiondate", "timetracking",
		},
	}
}

// Resolved is a projection turned into the field IDs to ask Jira for.
type Resolved struct {
	IDs []string
	// Missing names the fields this site has none of, so that a view can say so
	// instead of rendering an empty column.
	Missing []string
	// Labels is what this site calls the fields that were asked for. It is
	// filled whenever resolving needed the catalogue and empty otherwise, so
	// that a list of six platform IDs still resolves without a fetch.
	Labels FieldLabels
}

// FieldLabels is what a site calls the fields one read asked for, keyed by
// field ID.
//
// The two halves are not interchangeable. An ID identifies a field: it is what
// a value arrives under, what the cache is keyed by and what a write names. A
// name only displays one, and customfield_13401 is not a name anyone can read.
// A name is also translated, is not the spelling the same field answers to in
// JQL, and has been seen changing between two reads in one session — so it
// travels beside the answer it was read with and is never written down as
// though it identified anything.
//
// It is immutable for the reason jira.FieldSet is: it travels by value out of a
// search that several callers may be sharing.
type FieldLabels struct {
	refs map[string]jira.FieldRef
}

// NewFieldLabels labels the field IDs a read asked for, taking each name from
// the catalogue as this site spells it now. An ID the catalogue has no field
// for is left unlabelled rather than handed its own ID as a name: whether to
// show a raw ID is the caller's decision.
func NewFieldLabels(catalogue []jira.Field, ids []string) FieldLabels {
	if len(catalogue) == 0 || len(ids) == 0 {
		return FieldLabels{}
	}
	refs := make(map[string]jira.FieldRef, len(ids))
	for i := range catalogue {
		if slices.Contains(ids, catalogue[i].ID) {
			refs[catalogue[i].ID] = catalogue[i].Ref()
		}
	}
	if len(refs) == 0 {
		return FieldLabels{}
	}
	return FieldLabels{refs: refs}
}

// Name returns what this site calls a field, or the empty string when the read
// carried no label for it.
func (l FieldLabels) Name(id string) string { return l.refs[id].Name }

// Field returns the reference for a field ID: the name to put on screen and the
// schema that says what its value is.
func (l FieldLabels) Field(id string) (jira.FieldRef, bool) {
	ref, ok := l.refs[id]
	return ref, ok
}

// IDs returns the labelled field IDs, sorted, so that iteration is stable.
func (l FieldLabels) IDs() []string {
	out := make([]string, 0, len(l.refs))
	for id := range l.refs {
		out = append(out, id)
	}
	slices.Sort(out)
	return out
}

// Len reports how many fields are labelled.
func (l FieldLabels) Len() int { return len(l.refs) }

// Request is one search to run.
type Request struct {
	JQL        string
	Projection Projection
	// MaxResults is how many issues to ask for per page. Zero leaves the size
	// to Jira.
	MaxResults int
}

// Result is a search's first page plus what could not be asked for.
//
// The page may be shared with another caller that asked for the same search at
// the same moment, so its issues are to be read and not written to.
type Result struct {
	Page    jira.Page[jira.Issue]
	Missing []string
	// Labels names the fields the page's issues carry values for, for the
	// reason Resolved carries them: a value keyed by customfield_13401 cannot
	// be rendered on its own.
	Labels FieldLabels
}

// Search is the use case behind every issue list.
//
// It owns three things a view should not: which fields a view actually needs,
// the site's field catalogue that resolves a name to one of them, and the
// collapsing of identical searches that a cursor moving down a list produces.
// A Search is safe to share between goroutines.
type Search struct {
	client SearchClient

	flight Flights

	mu        sync.Mutex
	catalogue []jira.Field
	loaded    bool
}

// NewSearch builds the search use case over a Jira client.
func NewSearch(client SearchClient) *Search { return &Search{client: client} }

var errNoClient = errors.New("app: this search has no Jira client to run against")

// Fields returns the site's field catalogue, fetched once and kept until
// Invalidate drops it. Resolving a field is the only reason anything above the
// adapter needs it — a name into the ID to ask for, an ID into the name to show
// — and it changes about as often as the site's configuration does.
func (s *Search) Fields(ctx context.Context) ([]jira.Field, error) {
	fields, err := s.fields(ctx)
	if err != nil {
		return nil, err
	}
	return cloneFields(fields), nil
}

// fields is the catalogue itself rather than a copy of it, for the readers
// inside this package that only read. It stays unexported because a caller that
// wrote to what it returns would be writing into the cache — and copying a
// hundred field definitions to resolve one projection is a cost every issue a
// user opens would pay.
func (s *Search) fields(ctx context.Context) ([]jira.Field, error) {
	if s.client == nil {
		return nil, errNoClient
	}
	if cached, ok := s.cached(); ok {
		return cached, nil
	}
	fields, err := Coalesce(ctx, &s.flight, "fields", func(ctx context.Context) ([]jira.Field, error) {
		got, err := s.client.Fields(ctx)
		if err != nil {
			return nil, err
		}
		return cloneFields(got), nil
	})
	if err != nil {
		return nil, err
	}
	s.mu.Lock()
	s.catalogue, s.loaded = fields, true
	s.mu.Unlock()
	return fields, nil
}

// Invalidate drops the cached field catalogue, which is what a refresh that
// purges rather than reloads has to do.
func (s *Search) Invalidate() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.catalogue, s.loaded = nil, false
}

func (s *Search) cached() ([]jira.Field, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.loaded {
		return nil, false
	}
	return s.catalogue, true
}

// Resolve turns a projection into the field IDs to ask for, and into what this
// site calls each of them.
//
// The catalogue is fetched only when the projection names a field or asks for
// the custom ones, so the list view — six platform field IDs and no names —
// resolves without touching the network at all.
//
// Custom fields are taken from the catalogue by ID rather than asked for with
// jira.FieldsNavigable. The wildcard is both wider and less useful: it returns
// a value per field per issue, and it returns them keyed by IDs the answer
// carries no names for, which is a screen of customfield_13401.
func (s *Search) Resolve(ctx context.Context, p Projection) (Resolved, error) {
	out := Resolved{IDs: make([]string, 0, len(p.IDs)+len(p.Names))}
	for _, id := range p.IDs {
		out.IDs = AppendUnique(out.IDs, id)
	}
	names := trimmed(p.Names)
	if len(names) == 0 && !p.Custom {
		return out, nil
	}
	catalogue, err := s.fields(ctx)
	if err != nil {
		return Resolved{}, err
	}
	for _, name := range names {
		field, ok := jira.FieldByName(catalogue, name)
		if !ok {
			out.Missing = AppendUnique(out.Missing, name)
			continue
		}
		out.IDs = AppendUnique(out.IDs, field.ID)
	}
	if p.Custom {
		for i := range catalogue {
			if catalogue[i].Custom {
				out.IDs = AppendUnique(out.IDs, catalogue[i].ID)
			}
		}
	}
	out.Labels = NewFieldLabels(catalogue, out.IDs)
	return out, nil
}

// Run searches, asking for the projection's fields and nothing else.
//
// Two identical searches issued while one is still in the air become one call:
// a cursor moving down a list must not fan out a fetch per keystroke.
func (s *Search) Run(ctx context.Context, r Request) (Result, error) {
	if s.client == nil {
		return Result{}, errNoClient
	}
	resolved, err := s.Resolve(ctx, r.Projection)
	if err != nil {
		return Result{}, err
	}
	if len(resolved.IDs) == 0 {
		return Result{}, fmt.Errorf("app: the %s field set resolved to no field on this site, so there is nothing to ask for", projectionName(r.Projection))
	}
	query := jira.Query{
		JQL:        strings.TrimSpace(r.JQL),
		Fields:     resolved.IDs,
		MaxResults: r.MaxResults,
	}
	page, err := Coalesce(ctx, &s.flight, searchKey(query), func(ctx context.Context) (jira.Page[jira.Issue], error) {
		return s.client.Search(ctx, query)
	})
	if err != nil {
		return Result{}, err
	}
	return Result{Page: page, Missing: resolved.Missing, Labels: resolved.Labels}, nil
}

// Count reports roughly how many issues a query matches, and whether this
// adapter can answer at all. It is a second request to a second endpoint, so it
// is worth making only where a number is genuinely needed: a list that pages as
// the user scrolls already knows how to render "142+" without one.
func (s *Search) Count(ctx context.Context, jql string) (count int, counted bool, err error) {
	counter, ok := s.client.(Counter)
	if !ok {
		return 0, false, nil
	}
	query := strings.TrimSpace(jql)
	count, err = Coalesce(ctx, &s.flight, "count\x00"+query, func(ctx context.Context) (int, error) {
		return counter.ApproximateCount(ctx, query)
	})
	if err != nil {
		return 0, false, err
	}
	return count, true, nil
}

// searchKey is what makes two searches the same search. The page token is not
// part of it and never can be: it is not opaque, it embeds the JQL it was
// issued for, and nothing above the adapter may hold on to one.
func searchKey(q jira.Query) string {
	var b strings.Builder
	b.WriteString("search\x00")
	b.WriteString(q.JQL)
	b.WriteByte(0)
	b.WriteString(strings.Join(q.Fields, ","))
	b.WriteByte(0)
	fmt.Fprintf(&b, "%d", q.MaxResults)
	return b.String()
}

func projectionName(p Projection) string {
	if p.Name == "" {
		return "requested"
	}
	return p.Name
}

// coalesceAttempts bounds how many times a waiter restarts a call that
// successive callers keep abandoning.
const coalesceAttempts = 3

// errLeaderLeft marks a shared call that ended because the caller who started
// it walked away. It is never returned to anyone: it is how a waiter tells that
// case apart from a request that genuinely failed, which the error value cannot
// say on its own.
var errLeaderLeft = errors.New("app: the caller that started this request left")

// Flights is the group identical calls collapse into.
type Flights struct {
	group singleflight.Group
	// Joined fires on a caller's own goroutine the moment that caller is
	// registered in the group, which is the first instant it is certain to share
	// a call rather than start one. Nothing outside a test sets it.
	Joined func(key string)
}

// Coalesce runs one call for however many callers ask for it at the same
// moment.
//
// The call runs on the context of whichever caller started it, so cancelling
// the only caller really does cancel the work. When that caller leaves while
// others are still waiting, one of them starts the call again rather than
// inheriting a cancellation it did not ask for.
func Coalesce[T any](ctx context.Context, in *Flights, key string, fn func(context.Context) (T, error)) (T, error) {
	var zero T
	for range coalesceAttempts {
		if err := ctx.Err(); err != nil {
			return zero, err
		}
		shared := in.group.DoChan(key, func() (any, error) {
			out, err := fn(ctx)
			if ctx.Err() != nil {
				return nil, errLeaderLeft
			}
			return out, err
		})
		if in.Joined != nil {
			in.Joined(key)
		}
		select {
		case <-ctx.Done():
			in.group.Forget(key)
			return zero, ctx.Err()
		case res := <-shared:
			if errors.Is(res.Err, errLeaderLeft) {
				continue
			}
			if res.Err != nil {
				return zero, res.Err
			}
			out, _ := res.Val.(T)
			return out, nil
		}
	}
	return zero, errors.New("app: this request was restarted too many times because each caller left before it finished")
}

func cloneFields(in []jira.Field) []jira.Field {
	out := make([]jira.Field, len(in))
	for i := range in {
		out[i] = in[i]
		out[i].ClauseNames = slices.Clone(in[i].ClauseNames)
	}
	return out
}

// AppendUnique appends value, trimmed, unless it is blank or already there.
func AppendUnique(out []string, value string) []string {
	trimmedValue := strings.TrimSpace(value)
	if trimmedValue == "" || slices.Contains(out, trimmedValue) {
		return out
	}
	return append(out, trimmedValue)
}

func trimmed(in []string) []string {
	out := make([]string, 0, len(in))
	for _, s := range in {
		out = AppendUnique(out, s)
	}
	return out
}

// ReadIssue reads through the issue endpoint because search's index trails a
// write by seconds, and the read after a save has to see the save.
func (s *Search) ReadIssue(ctx context.Context, reader jira.IssueReader, key string, p Projection) (jira.Issue, FieldLabels, error) {
	if reader == nil {
		return jira.Issue{}, FieldLabels{}, errNoClient
	}
	resolved, err := s.Resolve(ctx, p)
	if err != nil {
		return jira.Issue{}, FieldLabels{}, err
	}
	if len(resolved.IDs) == 0 {
		return jira.Issue{}, FieldLabels{}, errors.New("app: the " + projectionName(p) + " field set resolved to no field on this site, so there is nothing to ask for")
	}
	iss, err := reader.IssueFields(ctx, key, resolved.IDs)
	if err != nil {
		return jira.Issue{}, FieldLabels{}, err
	}
	return iss, resolved.Labels, nil
}
