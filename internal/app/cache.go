package app

import (
	"encoding/json"
	"fmt"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/varijkapil13/saral/internal/store"
	"github.com/varijkapil13/saral/pkg/adf"
	"github.com/varijkapil13/saral/pkg/jira"
)

// Kind is what a cached entry holds. It names the bucket the entry lives in and
// the TTL it lives under.
//
// The set is closed and spelt here because the alternative is two packets
// agreeing out of band on a string constant, which is a cache with two spellings
// of one thing and no error anywhere.
type Kind string

// The kinds of thing worth keeping between runs.
const (
	KindIssue       Kind = "issue"
	KindSearch      Kind = "search"
	KindFields      Kind = "fields"
	KindCreateMeta  Kind = "createmeta"
	KindBoardConfig Kind = "boardconfig"
	KindVersions    Kind = "versions"
)

// TTL is how long an entry of this kind counts as current. Past it the entry is
// still served — seeing yesterday's rows beats seeing none — but it is badged
// and revalidated behind the frame it was drawn into.
//
// The numbers are how fast the underlying thing actually changes: a field
// catalogue is site configuration and moves about as often as an administrator
// does, while a search is a question whose answer somebody else can change while
// you are reading it.
func (k Kind) TTL() time.Duration {
	switch k {
	case KindFields, KindCreateMeta:
		return 24 * time.Hour
	case KindBoardConfig:
		return time.Hour
	case KindVersions:
		return 10 * time.Minute
	case KindIssue:
		return 60 * time.Second
	case KindSearch:
		return 30 * time.Second
	default:
		return 0
	}
}

// Snapshot is what a search left on disk the last time it ran: its rows, when
// they were written, whether that was long enough ago to badge, and whether the
// search had more pages than the rows account for.
type Snapshot struct {
	Issues   []jira.Issue
	StoredAt time.Time
	Stale    bool
	// More reports that the search had another page when it was stored. The rows
	// carry no cursor across a restart, so a caller that reaches the end of them
	// has to ask the search again rather than page on from here.
	More bool
}

// Cache is the copy on disk of what a site has already answered, so that the
// first frame is drawn from it instead of from a round trip (docs/UX.md
// principle 1).
//
// A nil Cache is a session with nowhere to keep one — a first run, a read-only
// home directory, another copy of Saral holding the file — and every caller has
// to work without it rather than refuse to draw.
//
// It is keyed by site and account below this interface, so a caller names a
// search and cannot reach another profile's rows even by accident.
type Cache interface {
	// Rows returns what a search last brought back, whatever its age. It takes
	// the JQL rather than a key so that nothing outside this package has to know
	// how a key is spelt.
	//
	// There is no error: this is the read a first paint is drawn from, so it runs
	// on the event loop and has nowhere to put one. A cache that cannot be read
	// is a cache with nothing in it, and the next fetch to land overwrites
	// whatever could not be understood.
	Rows(jql string) (Snapshot, bool)

	// PutRows stores what a search brought back.
	//
	// Each issue is merged into the copy already held, so a narrow read cannot
	// blank a field it never asked for: a list row asks for six fields, and
	// storing one must not unassign an issue whose assignee a wider read put
	// there.
	PutRows(jql string, issues []jira.Issue, more bool) error

	// Forget drops a search's rows, which is what a purging refresh asks for
	// beyond a refetch. The issues themselves stay: they are shared with every
	// other search that matched them, and the refetch overwrites what it asked
	// for anyway.
	Forget(jql string) error

	// EachIssue visits every issue held, in key order, stopping early when fn
	// returns false. It is how an index over what is already on disk is built
	// without a round trip.
	EachIssue(fn func(iss jira.Issue, storedAt time.Time) bool) error

	// Generation counts the changes this cache has made to what is on disk. It
	// only ever increases, and it moves for any write, so something holding a
	// derived copy — an index built by walking EachIssue — can tell that its
	// copy is behind by comparing one number.
	//
	// It over-reports rather than under-reports: a change that happens not to
	// affect a particular reader still moves it, which costs that reader a
	// rebuild and never leaves it holding a stale answer.
	Generation() uint64
}

// DefaultIssueBound is how many issues the cache keeps before it starts dropping
// the ones stored longest ago. A session that scrolls all day would otherwise
// grow the file without limit.
const DefaultIssueBound = 5000

// DiskCache is the bbolt-backed Cache, scoped to one site and account.
type DiskCache struct {
	db    *store.DB
	scope store.Scope
	now   func() time.Time
	bound int

	gen atomic.Uint64
}

var _ Cache = (*DiskCache)(nil)

// CacheOption adjusts a DiskCache at construction.
type CacheOption func(*DiskCache)

// WithClock replaces the clock the TTLs are measured against, which is what lets
// an expiry be tested without waiting for one.
func WithClock(now func() time.Time) CacheOption {
	return func(c *DiskCache) {
		if now != nil {
			c.now = now
		}
	}
}

// WithIssueBound sets how many issues are kept. Zero or less leaves the default.
func WithIssueBound(n int) CacheOption {
	return func(c *DiskCache) {
		if n > 0 {
			c.bound = n
		}
	}
}

// NewCache builds the cache over an open database. The scope is whose cache it
// is: two accounts on one site, and one account on two sites, never see each
// other's rows.
func NewCache(db *store.DB, scope store.Scope, opts ...CacheOption) *DiskCache {
	c := &DiskCache{db: db, scope: scope, now: time.Now, bound: DefaultIssueBound}
	for _, opt := range opts {
		if opt != nil {
			opt(c)
		}
	}
	return c
}

// Rows implements Cache.
func (c *DiskCache) Rows(jql string) (Snapshot, bool) {
	key := rowsKey(jql)
	if c == nil || c.db == nil || key == "" {
		return Snapshot{}, false
	}
	rec, ok, err := c.db.Get(c.scope, string(KindSearch), key)
	if err != nil || !ok {
		return Snapshot{}, false
	}
	var entry wireSearch
	if err := json.Unmarshal(rec.Value, &entry); err != nil || len(entry.Keys) == 0 {
		return Snapshot{}, false
	}
	held, err := c.db.GetAll(c.scope, string(KindIssue), entry.Keys)
	if err != nil {
		return Snapshot{}, false
	}
	issues := make([]jira.Issue, 0, len(held))
	for i := range held {
		iss, err := decodeIssue(held[i].Value)
		if err != nil {
			continue
		}
		issues = append(issues, iss)
	}
	if len(issues) == 0 {
		return Snapshot{}, false
	}
	return Snapshot{
		Issues:   issues,
		StoredAt: rec.StoredAt,
		Stale:    c.now().Sub(rec.StoredAt) > KindSearch.TTL(),
		More:     entry.More,
	}, true
}

// PutRows implements Cache.
func (c *DiskCache) PutRows(jql string, issues []jira.Issue, more bool) error {
	key := rowsKey(jql)
	if c == nil || c.db == nil || key == "" {
		return nil
	}
	keys := make([]string, 0, len(issues))
	for i := range issues {
		if issues[i].Key != "" {
			keys = append(keys, issues[i].Key)
		}
	}
	if err := c.mergeIssues(issues, keys); err != nil {
		return err
	}
	entry, err := json.Marshal(wireSearch{Keys: keys, More: more})
	if err != nil {
		return fmt.Errorf("encoding the rows of a search: %w", err)
	}
	if err := c.db.Put(c.scope, string(KindSearch), store.Record{
		Key: key, Value: entry, StoredAt: c.now(),
	}); err != nil {
		return err
	}
	c.gen.Add(1)
	return nil
}

// mergeIssues writes each issue over the copy already held, keeping every field
// the fresh read did not ask about, and then brings the issue count back to the
// bound.
func (c *DiskCache) mergeIssues(issues []jira.Issue, keys []string) error {
	if len(issues) == 0 {
		return nil
	}
	held, err := c.db.GetAll(c.scope, string(KindIssue), keys)
	if err != nil {
		return err
	}
	base := make(map[string]jira.Issue, len(held))
	for i := range held {
		iss, err := decodeIssue(held[i].Value)
		if err != nil {
			continue
		}
		base[held[i].Key] = iss
	}

	now := c.now()
	recs := make([]store.Record, 0, len(issues))
	for i := range issues {
		if issues[i].Key == "" {
			continue
		}
		value, err := encodeIssue(MergeIssue(base[issues[i].Key], issues[i]))
		if err != nil {
			return err
		}
		recs = append(recs, store.Record{Key: issues[i].Key, Value: value, StoredAt: now})
	}
	if err := c.db.Put(c.scope, string(KindIssue), recs...); err != nil {
		return err
	}
	if _, err := c.db.Trim(c.scope, string(KindIssue), c.bound); err != nil {
		return err
	}
	return nil
}

// Forget implements Cache.
func (c *DiskCache) Forget(jql string) error {
	key := rowsKey(jql)
	if c == nil || c.db == nil || key == "" {
		return nil
	}
	if err := c.db.Delete(c.scope, string(KindSearch), key); err != nil {
		return err
	}
	c.gen.Add(1)
	return nil
}

// EachIssue implements Cache.
func (c *DiskCache) EachIssue(fn func(jira.Issue, time.Time) bool) error {
	if c == nil || c.db == nil || fn == nil {
		return nil
	}
	var bad error
	err := c.db.Each(c.scope, string(KindIssue), func(rec store.Record) bool {
		iss, err := decodeIssue(rec.Value)
		if err != nil {
			bad = fmt.Errorf("the cached copy of %s cannot be read: %w", rec.Key, err)
			return false
		}
		return fn(iss, rec.StoredAt)
	})
	if err != nil {
		return err
	}
	return bad
}

// Generation implements Cache.
func (c *DiskCache) Generation() uint64 {
	if c == nil {
		return 0
	}
	return c.gen.Load()
}

// rowsKey is how a search is spelt on disk: the JQL itself, trimmed. It is the
// question rather than the answer's shape, so two callers asking the same
// question with different field sets share one entry — which is correct, because
// what the entry holds is which issues matched and in what order, while the
// fields live on the issues.
func rowsKey(jql string) string { return strings.TrimSpace(jql) }

// MergeIssue combines a fresh read with the copy already held.
//
// The fresh read wins for every field it asked for, and only for those: a field
// outside its Requested mask is absent because nothing asked, which is a
// different answer from Jira having nothing to send, and treating the two the
// same is how a list refresh that never mentions the assignee unassigns the row.
//
// A read that asked for nothing changes nothing, which is the safe direction for
// an issue that did not come from a read at all.
func MergeIssue(base, fresh jira.Issue) jira.Issue {
	switch {
	case base.Key == "" && base.ID == "":
		return fresh
	case fresh.Requested.Wide():
		return fresh
	case fresh.Requested.Len() == 0:
		return base
	}

	out := base
	if fresh.ID != "" {
		out.ID = fresh.ID
	}
	if fresh.Key != "" {
		out.Key = fresh.Key
	}
	for _, col := range issueColumns {
		if fresh.Requested.Has(col.id) {
			col.take(&out, &fresh)
		}
	}
	for _, id := range fresh.Requested.IDs() {
		if isColumn(id) {
			continue
		}
		ref := jira.FieldRef{ID: id}
		if v, ok := fresh.Fields.ByID(id); ok {
			out.Fields = out.Fields.With(ref, v)
			continue
		}
		out.Fields = out.Fields.Without(ref)
	}
	// A base that was read wide stays wide: it already carries every field the
	// site has, and a narrow refresh over it replaces some of them rather than
	// narrowing what is known.
	if base.Requested.Wide() {
		out.Requested = jira.AllFields()
		return out
	}
	out.Requested = jira.NewFieldMask(append(base.Requested.IDs(), fresh.Requested.IDs()...))
	return out
}

// issueColumns maps the platform field IDs an issue's own struct fields come
// from. These are the same on every site — the per-site IDs are the custom
// fields, which travel in Fields and are matched by the loop above.
var issueColumns = []struct {
	id   string
	take func(dst, src *jira.Issue)
}{
	{"summary", func(dst, src *jira.Issue) { dst.Summary = src.Summary }},
	{"description", func(dst, src *jira.Issue) { dst.Description = src.Description }},
	{"project", func(dst, src *jira.Issue) { dst.Project = src.Project }},
	{"issuetype", func(dst, src *jira.Issue) { dst.Type = src.Type }},
	{"status", func(dst, src *jira.Issue) { dst.Status = src.Status }},
	{"priority", func(dst, src *jira.Issue) { dst.Priority = src.Priority }},
	{"resolution", func(dst, src *jira.Issue) { dst.Resolution = src.Resolution }},
	{"assignee", func(dst, src *jira.Issue) { dst.Assignee = src.Assignee }},
	{"reporter", func(dst, src *jira.Issue) { dst.Reporter = src.Reporter }},
	{"labels", func(dst, src *jira.Issue) { dst.Labels = src.Labels }},
	{"components", func(dst, src *jira.Issue) { dst.Components = src.Components }},
	{"fixVersions", func(dst, src *jira.Issue) { dst.FixVersions = src.FixVersions }},
	{"parent", func(dst, src *jira.Issue) { dst.Parent = src.Parent }},
	{"subtasks", func(dst, src *jira.Issue) { dst.Subtasks = src.Subtasks }},
	{"issuelinks", func(dst, src *jira.Issue) { dst.Links = src.Links }},
	{"duedate", func(dst, src *jira.Issue) { dst.Due = src.Due }},
	{"created", func(dst, src *jira.Issue) { dst.Created = src.Created }},
	{"updated", func(dst, src *jira.Issue) { dst.Updated = src.Updated }},
	{"resolutiondate", func(dst, src *jira.Issue) { dst.Resolved = src.Resolved }},
	{"timetracking", func(dst, src *jira.Issue) { dst.TimeTracking = src.TimeTracking }},
}

func isColumn(id string) bool {
	for i := range issueColumns {
		if issueColumns[i].id == id {
			return true
		}
	}
	return false
}

// wireSearch is a search's answer: which issues matched, in the order they came
// back, and whether there were more pages behind them.
type wireSearch struct {
	Keys []string `json:"keys"`
	More bool     `json:"more,omitempty"`
}

// wireIssue is how an issue is encoded. It exists because three of the things an
// issue carries cannot round-trip through encoding/json on their own: FieldSet
// and FieldMask hold their contents unexported on purpose, and a user's timezone
// is a *time.Location, which encodes as an empty object and decodes as nothing.
type wireIssue struct {
	ID           string                    `json:"id,omitempty"`
	Key          string                    `json:"key,omitempty"`
	Project      jira.ProjectRef           `json:"project,omitzero"`
	Summary      string                    `json:"summary,omitempty"`
	Description  adf.Doc                   `json:"description,omitzero"`
	Type         jira.IssueType            `json:"type,omitzero"`
	Status       jira.Status               `json:"status,omitzero"`
	Priority     *jira.Priority            `json:"priority,omitempty"`
	Resolution   *jira.Resolution          `json:"resolution,omitempty"`
	Assignee     *wireUser                 `json:"assignee,omitempty"`
	Reporter     *wireUser                 `json:"reporter,omitempty"`
	Labels       []string                  `json:"labels,omitempty"`
	Components   []jira.Component          `json:"components,omitempty"`
	FixVersions  []jira.Version            `json:"fixVersions,omitempty"`
	Parent       *jira.IssueRef            `json:"parent,omitempty"`
	Subtasks     []jira.IssueRef           `json:"subtasks,omitempty"`
	Links        []jira.IssueLink          `json:"links,omitempty"`
	Due          jira.Date                 `json:"due,omitzero"`
	Created      time.Time                 `json:"created,omitzero"`
	Updated      time.Time                 `json:"updated,omitzero"`
	Resolved     *time.Time                `json:"resolved,omitempty"`
	TimeTracking *jira.TimeTracking        `json:"timeTracking,omitempty"`
	Fields       map[string]wireFieldValue `json:"fields,omitempty"`
	Requested    []string                  `json:"requested,omitempty"`
	Wide         bool                      `json:"wide,omitempty"`
}

type wireUser struct {
	AccountID   string `json:"accountId,omitempty"`
	DisplayName string `json:"displayName,omitempty"`
	Email       string `json:"email,omitempty"`
	TimeZone    string `json:"timeZone,omitempty"`
	Active      bool   `json:"active,omitempty"`
	AvatarURL   string `json:"avatarUrl,omitempty"`
}

type wireFieldValue struct {
	Kind    jira.FieldKind `json:"kind,omitempty"`
	Text    string         `json:"text,omitempty"`
	Number  float64        `json:"number,omitempty"`
	Bool    bool           `json:"bool,omitempty"`
	Date    jira.Date      `json:"date,omitzero"`
	Time    time.Time      `json:"time,omitzero"`
	Doc     adf.Doc        `json:"doc,omitzero"`
	Options []jira.Option  `json:"options,omitempty"`
	Users   []wireUser     `json:"users,omitempty"`
}

func encodeIssue(iss jira.Issue) ([]byte, error) {
	out := wireIssue{
		ID: iss.ID, Key: iss.Key, Project: iss.Project, Summary: iss.Summary,
		Description: iss.Description, Type: iss.Type, Status: iss.Status,
		Priority: iss.Priority, Resolution: iss.Resolution,
		Assignee: encodeUser(iss.Assignee), Reporter: encodeUser(iss.Reporter),
		Labels: iss.Labels, Components: iss.Components, FixVersions: iss.FixVersions,
		Parent: iss.Parent, Subtasks: iss.Subtasks, Links: iss.Links,
		Due: iss.Due, Created: iss.Created, Updated: iss.Updated, Resolved: iss.Resolved,
		TimeTracking: iss.TimeTracking,
		Requested:    iss.Requested.IDs(), Wide: iss.Requested.Wide(),
	}
	if ids := iss.Fields.IDs(); len(ids) > 0 {
		out.Fields = make(map[string]wireFieldValue, len(ids))
		for _, id := range ids {
			v, ok := iss.Fields.ByID(id)
			if !ok {
				continue
			}
			out.Fields[id] = wireFieldValue{
				Kind: v.Kind, Text: v.Text, Number: v.Number, Bool: v.Bool,
				Date: v.Date, Time: v.Time, Doc: v.Doc,
				Options: v.Options, Users: encodeUsers(v.Users),
			}
		}
	}
	data, err := json.Marshal(out)
	if err != nil {
		return nil, fmt.Errorf("encoding %s: %w", iss.Key, err)
	}
	return data, nil
}

func decodeIssue(data []byte) (jira.Issue, error) {
	var in wireIssue
	if err := json.Unmarshal(data, &in); err != nil {
		return jira.Issue{}, fmt.Errorf("decoding a cached issue: %w", err)
	}
	out := jira.Issue{
		ID: in.ID, Key: in.Key, Project: in.Project, Summary: in.Summary,
		Description: in.Description, Type: in.Type, Status: in.Status,
		Priority: in.Priority, Resolution: in.Resolution,
		Assignee: decodeUser(in.Assignee), Reporter: decodeUser(in.Reporter),
		Labels: in.Labels, Components: in.Components, FixVersions: in.FixVersions,
		Parent: in.Parent, Subtasks: in.Subtasks, Links: in.Links,
		Due: in.Due, Created: in.Created, Updated: in.Updated, Resolved: in.Resolved,
		TimeTracking: in.TimeTracking,
	}
	if in.Wide {
		out.Requested = jira.AllFields()
	} else {
		out.Requested = jira.NewFieldMask(in.Requested)
	}
	if len(in.Fields) > 0 {
		values := make(map[string]jira.FieldValue, len(in.Fields))
		for id := range in.Fields {
			v := in.Fields[id]
			values[id] = jira.FieldValue{
				Kind: v.Kind, Text: v.Text, Number: v.Number, Bool: v.Bool,
				Date: v.Date, Time: v.Time, Doc: v.Doc,
				Options: v.Options, Users: decodeUsers(v.Users),
			}
		}
		out.Fields = jira.NewFieldSet(values)
	}
	return out, nil
}

func encodeUser(u *jira.User) *wireUser {
	if u == nil {
		return nil
	}
	out := wireUser{
		AccountID: u.AccountID, DisplayName: u.DisplayName, Email: u.Email,
		Active: u.Active, AvatarURL: u.AvatarURL,
	}
	if u.TimeZone != nil {
		out.TimeZone = u.TimeZone.String()
	}
	return &out
}

func decodeUser(u *wireUser) *jira.User {
	if u == nil {
		return nil
	}
	out := jira.User{
		AccountID: u.AccountID, DisplayName: u.DisplayName, Email: u.Email,
		Active: u.Active, AvatarURL: u.AvatarURL, TimeZone: location(u.TimeZone),
	}
	return &out
}

func encodeUsers(in []jira.User) []wireUser {
	if len(in) == 0 {
		return nil
	}
	out := make([]wireUser, 0, len(in))
	for i := range in {
		out = append(out, *encodeUser(&in[i]))
	}
	return out
}

func decodeUsers(in []wireUser) []jira.User {
	if len(in) == 0 {
		return nil
	}
	out := make([]jira.User, 0, len(in))
	for i := range in {
		out = append(out, *decodeUser(&in[i]))
	}
	return out
}

// zones memoizes the zoneinfo lookups a decode does. Every row of a list can
// name the same timezone, and time.LoadLocation reads a file each time it is
// asked; a name this machine has no entry for stays absent rather than failing
// the decode of an issue that is otherwise fine.
var zones sync.Map

func location(name string) *time.Location {
	if name == "" {
		return nil
	}
	if held, ok := zones.Load(name); ok {
		loc, _ := held.(*time.Location)
		return loc
	}
	loc, err := time.LoadLocation(name)
	if err != nil {
		loc = nil
	}
	zones.Store(name, loc)
	return loc
}
