// Package app holds Saral's use cases: the orchestration between a view and the
// jira.Client port. Nothing in it knows how Jira is reached — it is handed the
// port, and in tests that is the in-memory fake — and nothing in it knows what a
// view looks like.
package app

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

// Projection is the narrow set of fields one view needs.
//
// It is split in two because only half of it can be written down. IDs are the
// platform's own field identifiers, which are the same on every site; Names are
// display names resolved against the site's field catalogue at runtime, which
// is the only way to ask for a custom field without writing a customfield_NNNNN
// into the source. A name the site has no field for is reported, never guessed.
type Projection struct {
	// Name describes the projection in an error a user might read.
	Name  string
	IDs   []string
	Names []string
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

// ListProjection is what a row in a list needs and nothing else. Six fields,
// not sixty: a bare issue read returns every field on the site, and on a site
// with ninety custom fields that is ninety nulls per row.
func ListProjection() Projection {
	return Projection{
		Name: "issue list",
		IDs:  []string{"summary", "status", "assignee", "priority", "updated", "issuetype"},
	}
}

// DetailProjection is what one open issue needs. It is wider than a list row
// and still narrower than everything.
func DetailProjection() Projection {
	return Projection{
		Name: "issue detail",
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
}

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
}

// Search is the use case behind every issue list.
//
// It owns three things a view should not: which fields a view actually needs,
// the site's field catalogue that resolves a name to one of them, and the
// collapsing of identical searches that a cursor moving down a list produces.
// A Search is safe to share between goroutines.
type Search struct {
	client jira.Client
	saved  SavedQueries

	flight singleflight.Group

	mu        sync.Mutex
	catalogue []jira.Field
	loaded    bool
}

// Option configures a Search at construction.
type Option func(*Search)

// WithSavedQueries seeds the saved queries, which is how the ones read out of a
// profile get here.
func WithSavedQueries(q SavedQueries) Option {
	return func(s *Search) { s.saved = q }
}

// NewSearch builds the search use case over a Jira client.
func NewSearch(client jira.Client, opts ...Option) *Search {
	s := &Search{client: client}
	for _, o := range opts {
		if o != nil {
			o(s)
		}
	}
	return s
}

var errNoClient = errors.New("app: this search has no Jira client to run against")

// Fields returns the site's field catalogue, fetched once and kept until
// Invalidate drops it. Resolving a field name is the only reason anything above
// the adapter needs it, and it changes about as often as the site's
// configuration does.
func (s *Search) Fields(ctx context.Context) ([]jira.Field, error) {
	if s.client == nil {
		return nil, errNoClient
	}
	if cached, ok := s.cached(); ok {
		return cached, nil
	}
	fields, err := coalesce(ctx, &s.flight, "fields", func(ctx context.Context) ([]jira.Field, error) {
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
	return cloneFields(fields), nil
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
	return cloneFields(s.catalogue), true
}

// Resolve turns a projection into the field IDs to ask for.
//
// The catalogue is fetched only when the projection names a field, so the list
// view — six platform field IDs and no names — resolves without touching the
// network at all.
func (s *Search) Resolve(ctx context.Context, p Projection) (Resolved, error) {
	out := Resolved{IDs: make([]string, 0, len(p.IDs)+len(p.Names))}
	for _, id := range p.IDs {
		out.IDs = appendUnique(out.IDs, id)
	}
	names := trimmed(p.Names)
	if len(names) == 0 {
		return out, nil
	}
	catalogue, err := s.Fields(ctx)
	if err != nil {
		return Resolved{}, err
	}
	for _, name := range names {
		field, ok := jira.FieldByName(catalogue, name)
		if !ok {
			out.Missing = appendUnique(out.Missing, name)
			continue
		}
		out.IDs = appendUnique(out.IDs, field.ID)
	}
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
	page, err := coalesce(ctx, &s.flight, searchKey(query), func(ctx context.Context) (jira.Page[jira.Issue], error) {
		return s.client.Search(ctx, query)
	})
	if err != nil {
		return Result{}, err
	}
	return Result{Page: page, Missing: resolved.Missing}, nil
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
	count, err = coalesce(ctx, &s.flight, "count\x00"+query, func(ctx context.Context) (int, error) {
		return counter.ApproximateCount(ctx, query)
	})
	if err != nil {
		return 0, false, err
	}
	return count, true, nil
}

// Saved returns the saved queries.
func (s *Search) Saved() SavedQueries { return s.saved }

// RunSaved runs a saved query by name.
func (s *Search) RunSaved(ctx context.Context, name string) (Result, error) {
	query, ok := s.saved.ByName(name)
	if !ok {
		return Result{}, fmt.Errorf("app: there is no saved query called %q", name)
	}
	return s.Run(ctx, Request{JQL: query.JQL, Projection: query.projection()})
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

// coalesce runs one call for however many callers ask for it at the same
// moment.
//
// The call runs on the context of whichever caller started it, so cancelling
// the only caller really does cancel the work. When that caller leaves while
// others are still waiting, one of them starts the call again rather than
// inheriting a cancellation it did not ask for.
func coalesce[T any](ctx context.Context, group *singleflight.Group, key string, fn func(context.Context) (T, error)) (T, error) {
	var zero T
	for range coalesceAttempts {
		if err := ctx.Err(); err != nil {
			return zero, err
		}
		shared := group.DoChan(key, func() (any, error) {
			out, err := fn(ctx)
			if ctx.Err() != nil {
				return nil, errLeaderLeft
			}
			return out, err
		})
		select {
		case <-ctx.Done():
			group.Forget(key)
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

func appendUnique(out []string, value string) []string {
	trimmedValue := strings.TrimSpace(value)
	if trimmedValue == "" || slices.Contains(out, trimmedValue) {
		return out
	}
	return append(out, trimmedValue)
}

func trimmed(in []string) []string {
	out := make([]string, 0, len(in))
	for _, s := range in {
		out = appendUnique(out, s)
	}
	return out
}

// MaxSavedSlot is the highest number key a saved query can be bound to.
const MaxSavedSlot = 9

// SavedQuery is a query a user keeps, optionally bound to a number key.
type SavedQuery struct {
	Name string
	JQL  string
	// Slot is the number key that runs this query, 1 to MaxSavedSlot, or zero
	// when it is only reachable by name.
	Slot int
	// Projection is the field set to fetch for it. One that asks for nothing
	// means the list projection, which is what a saved query is opened into.
	Projection Projection
}

func (q SavedQuery) projection() Projection {
	if len(q.Projection.IDs) == 0 && len(q.Projection.Names) == 0 {
		return ListProjection()
	}
	return q.Projection
}

// SavedQueries is an ordered set of saved queries.
//
// It is immutable the way jira.FieldSet is: Add and Remove return a new set. A
// set travels by value into the search use case and out to whatever renders the
// list of them, and a shared slice behind a value type would mean that binding
// a key in one place rebinds it everywhere.
type SavedQueries struct {
	items []SavedQuery
}

// NewSavedQueries builds a set, adding the queries in order.
func NewSavedQueries(in ...SavedQuery) (SavedQueries, error) {
	var out SavedQueries
	for _, q := range in {
		var err error
		if out, err = out.Add(q); err != nil {
			return SavedQueries{}, err
		}
	}
	return out, nil
}

// Add returns a copy of the set carrying one more query.
//
// A query whose name is already in the set replaces it and keeps its position.
// A slot already bound elsewhere moves to the new query rather than being
// refused: binding a key is a user taking it, and answering "that key is taken"
// would only make them go and unbind the old one first.
func (q SavedQueries) Add(in SavedQuery) (SavedQueries, error) {
	in.Name = strings.TrimSpace(in.Name)
	in.JQL = strings.TrimSpace(in.JQL)
	switch {
	case in.Name == "":
		return q, errors.New("app: a saved query needs a name")
	case in.JQL == "":
		return q, fmt.Errorf("app: the saved query %q has no JQL to run", in.Name)
	case in.Slot < 0 || in.Slot > MaxSavedSlot:
		return q, fmt.Errorf("app: the saved query %q asks for key %d; the keys are 1 to %d, or 0 for none", in.Name, in.Slot, MaxSavedSlot)
	}

	items := slices.Clone(q.items)
	for i := range items {
		if in.Slot > 0 && items[i].Slot == in.Slot {
			items[i].Slot = 0
		}
	}
	if at := q.indexOf(in.Name); at >= 0 {
		items[at] = in
		return SavedQueries{items: items}, nil
	}
	return SavedQueries{items: append(items, in)}, nil
}

// Remove returns a copy of the set without the named query.
func (q SavedQueries) Remove(name string) SavedQueries {
	at := q.indexOf(name)
	if at < 0 {
		return q
	}
	return SavedQueries{items: slices.Delete(slices.Clone(q.items), at, at+1)}
}

// All returns the queries in the order they were added.
func (q SavedQueries) All() []SavedQuery { return slices.Clone(q.items) }

// Len reports how many queries are saved.
func (q SavedQueries) Len() int { return len(q.items) }

// ByName finds a query by name, case-insensitively.
func (q SavedQueries) ByName(name string) (SavedQuery, bool) {
	if at := q.indexOf(name); at >= 0 {
		return q.items[at], true
	}
	return SavedQuery{}, false
}

// BySlot finds the query bound to a number key.
func (q SavedQueries) BySlot(slot int) (SavedQuery, bool) {
	if slot <= 0 {
		return SavedQuery{}, false
	}
	for _, item := range q.items {
		if item.Slot == slot {
			return item, true
		}
	}
	return SavedQuery{}, false
}

func (q SavedQueries) indexOf(name string) int {
	wanted := strings.TrimSpace(name)
	for i := range q.items {
		if strings.EqualFold(q.items[i].Name, wanted) {
			return i
		}
	}
	return -1
}
