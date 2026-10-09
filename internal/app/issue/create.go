package issue

import (
	"context"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	appquery "github.com/varijkapil13/saral/internal/app/query"
	"github.com/varijkapil13/saral/pkg/jira"
)

// typeSample is how many issues are read to find out which issue types a
// project actually uses. It is one page, not a walk: the port has no endpoint
// that lists a project's issue types, so the answer comes from the issues the
// account can see, the same way onboarding finds a project key.
const typeSample = 50

// Runner runs one search.
type Runner interface {
	Run(ctx context.Context, r appquery.Request) (appquery.Result, error)
}

// Types are the issue types in use in one project, newest first, which is
// the order worth offering.
func Types(ctx context.Context, search Runner, project string) ([]jira.IssueType, error) {
	result, err := search.Run(ctx, appquery.Request{
		JQL:        "project = " + quote(project) + " ORDER BY created DESC",
		Projection: appquery.Projection{Name: "issue type picker", IDs: []string{"issuetype"}},
		MaxResults: typeSample,
	})
	if err != nil {
		return nil, err
	}
	return distinctTypes(result.Page.Items), nil
}

func distinctTypes(issues []jira.Issue) []jira.IssueType {
	seen := make(map[string]bool, len(issues))
	out := make([]jira.IssueType, 0, 6)
	for i := range issues {
		typ := issues[i].Type
		if typ.ID == "" || seen[typ.ID] {
			continue
		}
		seen[typ.ID] = true
		out = append(out, typ)
	}
	return out
}

// quote writes a project key as JQL takes it. The key is whatever the session
// was opened against and nothing about it is written down here.
func quote(s string) string {
	return strconv.Quote(strings.ReplaceAll(s, `"`, ""))
}

// SchemaTTL is how long a create screen is kept: a project's screen does not
// change between two creates a minute apart, and a refresh throws it away on
// demand.
const SchemaTTL = 24 * time.Hour

// Screen identifies one create screen. Both halves matter — Jira states the
// fields per project and per issue type, and the same type in two projects is
// two different screens.
type Screen struct {
	Project   string
	IssueType string
}

type cachedSchema struct {
	schema jira.Schema
	at     time.Time
}

// Schemas keeps create screens for the session. It is shared rather than held
// on a form because a form is built afresh every time it is opened, and
// re-reading two paginated endpoints to draw the same screen again is exactly
// the fetch docs/PERFORMANCE.md says to avoid.
type Schemas struct {
	mu      sync.Mutex
	ttl     time.Duration
	now     func() time.Time
	entries map[Screen]cachedSchema
}

// NewSchemas keeps create screens for ttl by now's clock.
func NewSchemas(ttl time.Duration, now func() time.Time) *Schemas {
	if now == nil {
		now = time.Now
	}
	return &Schemas{ttl: ttl, now: now, entries: make(map[Screen]cachedSchema, 4)}
}

// Get is a screen still fresh, as a copy the caller may change.
func (c *Schemas) Get(key Screen) (jira.Schema, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	entry, ok := c.entries[key]
	if !ok || c.now().Sub(entry.at) >= c.ttl {
		return jira.Schema{}, false
	}
	return cloneSchema(entry.schema), true
}

// Put keeps a copy of a screen.
func (c *Schemas) Put(key Screen, schema jira.Schema) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.entries[key] = cachedSchema{schema: cloneSchema(schema), at: c.now()}
}

// Purge forgets every screen.
func (c *Schemas) Purge() {
	c.mu.Lock()
	defer c.mu.Unlock()
	clear(c.entries)
}

// cloneSchema detaches a screen from the copy in the cache, so that a form
// cannot write through into what the next form will be built from.
func cloneSchema(in jira.Schema) jira.Schema {
	out := in
	out.Fields = make([]jira.FieldMeta, len(in.Fields))
	for i := range in.Fields {
		meta := in.Fields[i]
		meta.Operations = slices.Clone(in.Fields[i].Operations)
		meta.AllowedValues = cloneOptions(in.Fields[i].AllowedValues)
		meta.Default.Options = cloneOptions(in.Fields[i].Default.Options)
		meta.Default.Users = slices.Clone(in.Fields[i].Default.Users)
		meta.Default.Doc = in.Fields[i].Default.Doc.Clone()
		out.Fields[i] = meta
	}
	return out
}

func cloneOptions(in []jira.Option) []jira.Option {
	if in == nil {
		return nil
	}
	out := make([]jira.Option, len(in))
	for i := range in {
		out[i] = in[i]
		out[i].Children = cloneOptions(in[i].Children)
	}
	return out
}

// CreateScreen reads a create screen, from the cache when it is still fresh.
func CreateScreen(ctx context.Context, reader jira.SchemaReader, cache *Schemas, key Screen) (jira.Schema, error) {
	if schema, ok := cache.Get(key); ok {
		return schema, nil
	}
	schema, err := reader.CreateMeta(ctx, key.Project, key.IssueType)
	if err != nil {
		return jira.Schema{}, err
	}
	cache.Put(key, schema)
	return schema, nil
}

// Create asks Jira to store the issue.
func Create(ctx context.Context, w jira.IssueWriter, in jira.IssueInput) (jira.Issue, error) {
	return w.CreateIssue(ctx, in)
}
