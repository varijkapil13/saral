package form

import (
	"slices"
	"sync"
	"time"

	"github.com/varijkapil13/saral/pkg/jira"
)

// schemaTTL is how long a create screen is kept. docs/ARCHITECTURE.md sets it:
// a project's screen does not change between two creates a minute apart, and a
// refresh throws it away on demand.
const schemaTTL = 24 * time.Hour

// screen identifies one create screen. Both halves matter — Jira states the
// fields per project and per issue type, and the same type in two projects is
// two different screens.
type screen struct {
	project   string
	issueType string
}

type cached struct {
	schema jira.Schema
	at     time.Time
}

// schemaCache keeps create screens for the session. It is shared rather than
// held on the view because a form is built afresh every time it is pushed, and
// re-reading two paginated endpoints to draw the same screen again is exactly
// the fetch docs/PERFORMANCE.md says to avoid.
type schemaCache struct {
	mu      sync.Mutex
	ttl     time.Duration
	now     func() time.Time
	entries map[screen]cached
}

func newSchemaCache(ttl time.Duration, now func() time.Time) *schemaCache {
	if now == nil {
		now = time.Now
	}
	return &schemaCache{ttl: ttl, now: now, entries: make(map[screen]cached, 4)}
}

func (c *schemaCache) get(key screen) (jira.Schema, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	entry, ok := c.entries[key]
	if !ok || c.now().Sub(entry.at) >= c.ttl {
		return jira.Schema{}, false
	}
	return cloneSchema(entry.schema), true
}

func (c *schemaCache) put(key screen, schema jira.Schema) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.entries[key] = cached{schema: cloneSchema(schema), at: c.now()}
}

func (c *schemaCache) purge() {
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

// draft is what was typed into one screen, kept by field id.
type draft map[string]draftValue

type draftValue struct {
	text   string
	picked []jira.Option
}

// draftStore keeps what a user typed after they leave the form.
//
// docs/UX.md asks that nothing typed is ever lost, and a create form is where
// that matters most: the alternative is a view that refuses to close, which
// leaves no way out at all. Closing the form keeps the values here, opening it
// again restores them, and a successful create is what clears them.
type draftStore struct {
	mu     sync.Mutex
	drafts map[screen]draft
}

func newDraftStore() *draftStore {
	return &draftStore{drafts: make(map[screen]draft, 2)}
}

func (s *draftStore) get(key screen) draft {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make(draft, len(s.drafts[key]))
	for id, value := range s.drafts[key] {
		out[id] = draftValue{text: value.text, picked: cloneOptions(value.picked)}
	}
	return out
}

func (s *draftStore) put(key screen, fields []*field) {
	kept := make(draft, len(fields))
	for _, f := range fields {
		if f.empty() {
			continue
		}
		kept[f.id()] = draftValue{text: f.text, picked: cloneOptions(f.picked)}
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if len(kept) == 0 {
		delete(s.drafts, key)
		return
	}
	s.drafts[key] = kept
}

func (s *draftStore) clear(key screen) {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.drafts, key)
}

// The stores every form in this session shares.
var (
	schemas = newSchemaCache(schemaTTL, time.Now)
	drafts  = newDraftStore()
)
