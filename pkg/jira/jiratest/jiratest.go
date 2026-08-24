// Package jiratest is the in-memory adapter for the jira port: a complete
// jira.Client backed by maps, so that everything above the adapter can be
// exercised with no Jira instance, no credentials and no network.
//
//	c := jiratest.New(
//		jiratest.WithProject("PROJ", jiratest.Scrum),
//		jiratest.WithIssues(jiratest.Gen(500)),
//		jiratest.WithCapabilities(jiratest.NoBulkMove, jiratest.NoPlans),
//	)
//
// The fake is deliberately opinionated about what it refuses. Its statuses,
// issue types and priorities are not the names a stock site ships with, and its
// custom field IDs are not the ones a stock site allocates, so code that
// hardcodes "In Progress" or customfield_10016 fails here rather than in front
// of a user. It validates the sprint state machine, the release policy and the
// create screen locally, for the same reason.
//
// It also misbehaves on demand, because the failure paths are the interesting
// ones: FailNext queues a typed error for whichever call comes next, Delay
// makes every call slow enough to cancel, and CursorLoop reproduces Jira's
// repeated-page-token bug.
package jiratest

import (
	"context"
	"slices"
	"strconv"
	"sync"
	"time"

	"github.com/varijkapil13/saral/pkg/jira"
)

// What the fake can be asked for.
//
// Both the port and the roles are named on purpose. A role restates a signature
// Client already carries, and one type cannot hold two methods of a name, so a
// role that drifts from the port makes these lines stop compiling. Nothing else
// catches that drift.
var (
	_ jira.Client = (*Fake)(nil)

	_ jira.Prober         = (*Fake)(nil)
	_ jira.Identifier     = (*Fake)(nil)
	_ jira.Searcher       = (*Fake)(nil)
	_ jira.FieldCatalogue = (*Fake)(nil)
	_ jira.SchemaReader   = (*Fake)(nil)
	_ jira.IssueWriter    = (*Fake)(nil)
	_ jira.Mover          = (*Fake)(nil)
	_ jira.CommentReader  = (*Fake)(nil)
	_ jira.Commenter      = (*Fake)(nil)
	_ jira.SessionClient  = (*Fake)(nil)
)

// BoardKind is what WithProject builds alongside a project.
type BoardKind int

// The board kinds a project can be given.
const (
	// Scrum gives the project a scrum board with a closed, an active and a
	// future sprint.
	Scrum BoardKind = iota
	// Kanban gives the project a kanban board and no sprints.
	Kanban
	// NoBoard gives the project no board at all.
	NoBoard
)

// Option configures a Fake at construction. Options are re-applied by Reset, so
// one must not depend on state built by a call made after New.
type Option func(*Fake)

// CapMod changes one probed capability. Anything the standard negatives do not
// cover can be written inline, since the whole value object is addressable.
type CapMod func(*jira.Capabilities)

// The standard capability negatives, worded the way a real probe words them.
var (
	// NoPlans turns off the Advanced Roadmaps plans capability.
	NoPlans = CapReason(jira.CapPlans, "the Plans API needs Administer Jira")
	// NoBulkMove turns off the cross-project bulk move capability.
	NoBulkMove = CapReason(jira.CapBulkMove, "needs Bulk Change permission")
	// NoBoards turns off the boards capability.
	NoBoards = CapReason(jira.CapBoards, "this project has no board")
	// NoAttachments turns off the attachments capability.
	NoAttachments = CapReason(jira.CapAttachments, "attachments are disabled on this site")
	// NoDeleteIssues turns off the delete-issues capability.
	NoDeleteIssues = CapReason(jira.CapDeleteIssues, "needs Delete Issues permission")
	// NoTimeZone leaves the account timezone unknown, which makes every date
	// render in UTC with the reason beside it.
	NoTimeZone = CapZone(nil, "Jira did not answer what timezone this account is in")
)

// CapReason turns a capability off with wording of the caller's own.
func CapReason(k jira.CapabilityKey, reason string) CapMod {
	return func(c *jira.Capabilities) {
		fakeSetCap(c, k, jira.Capability{Reason: reason})
	}
}

// CapZone sets the account timezone and, when there is none, why there is none.
// A location clears the reason: a probe never has both.
func CapZone(loc *time.Location, reason string) CapMod {
	return func(c *jira.Capabilities) {
		if loc != nil {
			reason = ""
		}
		c.TimeZone, c.TimeZoneReason = loc, reason
	}
}

// Fake is an in-memory jira.Client. Every method takes its own lock, so a Fake
// is safe to share between goroutines the way the real client is.
type Fake struct {
	mu   sync.Mutex
	opts []Option

	now      time.Time
	pageSize int
	caps     jira.Capabilities
	me       jira.User
	fields   []jira.Field

	projects    map[string]*fakeProject
	projectKeys []string

	issues    map[string]*jira.Issue
	issueKeys []string

	boards   map[int64]*jira.Board
	sprints  map[int64]*jira.Sprint
	versions map[string]*jira.Version

	comments    map[string][]jira.Comment
	attachments map[string][]jira.Attachment
	attachOwner map[string]string
	sprintOf    map[string]int64

	tasks map[string]*fakeTask

	failures   []error
	delay      time.Duration
	cursorLoop bool
	failTask   bool
	calls      []string

	seq int
}

type fakeProject struct {
	ref        jira.ProjectRef
	kind       BoardKind
	boardID    int64
	versionIDs []string
}

type fakeTask struct {
	ref   jira.TaskRef
	req   jira.MoveRequest
	polls int
	fails bool
}

// New builds a fake, applies the options in order and returns it ready to use.
func New(opts ...Option) *Fake {
	f := &Fake{opts: slices.Clone(opts)}
	f.fakeInit()
	return f
}

func (f *Fake) fakeInit() {
	f.now = fakeEpoch
	f.pageSize = 50
	f.caps = fakeDefaultCaps()
	f.me = fakeDefaultMe
	f.fields = fakeCloneFields(fakeDefaultFields)
	f.projects = make(map[string]*fakeProject)
	f.projectKeys = nil
	f.issues = make(map[string]*jira.Issue)
	f.issueKeys = nil
	f.boards = make(map[int64]*jira.Board)
	f.sprints = make(map[int64]*jira.Sprint)
	f.versions = make(map[string]*jira.Version)
	f.comments = make(map[string][]jira.Comment)
	f.attachments = make(map[string][]jira.Attachment)
	f.attachOwner = make(map[string]string)
	f.sprintOf = make(map[string]int64)
	f.tasks = make(map[string]*fakeTask)
	f.failures = nil
	f.delay = 0
	f.cursorLoop = false
	f.failTask = false
	f.calls = nil
	f.seq = 0
	for _, o := range f.opts {
		if o != nil {
			o(f)
		}
	}
}

func fakeDefaultCaps() jira.Capabilities {
	ok := jira.Capability{OK: true}
	return jira.Capabilities{
		Plans:        ok,
		BulkMove:     ok,
		Boards:       ok,
		Attachments:  ok,
		DeleteIssues: ok,
		Graphics:     jira.GraphicsHalfBlocks,
		TimeZone:     time.UTC,
	}
}

func fakeSetCap(c *jira.Capabilities, k jira.CapabilityKey, v jira.Capability) {
	switch k {
	case jira.CapPlans:
		c.Plans = v
	case jira.CapBulkMove:
		c.BulkMove = v
	case jira.CapBoards:
		c.Boards = v
	case jira.CapAttachments:
		c.Attachments = v
	case jira.CapDeleteIssues:
		c.DeleteIssues = v
	}
}

// WithProject adds a project, its versions and, unless the kind is NoBoard, a
// board. A scrum board also gets a closed, an active and a future sprint.
func WithProject(key string, kind BoardKind) Option {
	return func(f *Fake) { f.fakeAddProject(key, kind) }
}

// WithIssues loads issues into the fake, registering any project they name that
// is not there yet.
func WithIssues(issues []jira.Issue) Option {
	return func(f *Fake) {
		for i := range issues {
			f.fakePutIssue(&issues[i])
		}
	}
}

// WithCapabilities applies capability modifiers over the all-permitted default.
func WithCapabilities(mods ...CapMod) Option {
	return func(f *Fake) {
		for _, m := range mods {
			if m != nil {
				m(&f.caps)
			}
		}
	}
}

// WithFields replaces the site's field catalogue. Anything resolved by name —
// story points, rank, the sprint field — disappears with the entry that named
// it, which is how a site missing a field is modelled.
func WithFields(fields []jira.Field) Option {
	return func(f *Fake) { f.fields = fakeCloneFields(fields) }
}

// WithMe sets the authenticated account.
func WithMe(u jira.User) Option {
	return func(f *Fake) { f.me = u }
}

// WithPageSize sets how many items a page holds, for both paginators. The
// default is 50 and anything below 1 is clamped to 1.
func WithPageSize(n int) Option {
	return func(f *Fake) {
		if n < 1 {
			n = 1
		}
		f.pageSize = n
	}
}

// WithNow sets the clock the fake stamps writes with. Seeded data is derived
// from a fixed epoch either way, so that it does not move when the clock does.
func WithNow(t time.Time) Option {
	return func(f *Fake) { f.now = t }
}

// FailNext queues an error for the next call, whichever method that turns out
// to be. The value is returned as it was given, so errors.As sees through it.
func (f *Fake) FailNext(err error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.failures = append(f.failures, err)
}

// FailNextN queues the same error for each of the next n calls.
func (f *Fake) FailNextN(n int, err error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	for range n {
		f.failures = append(f.failures, err)
	}
}

// Delay makes every subsequent call wait, until it is set back to zero. The
// wait is cancellable: a call whose context is cancelled part-way through
// returns the context's error instead of sitting out the delay.
func (f *Fake) Delay(d time.Duration) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.delay = d
}

// CursorLoop makes search hand back a page token it has already given out,
// which is the shape of the Jira bug Page's cursor is expected to survive.
func (f *Fake) CursorLoop() {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.cursorLoop = true
}

// FailNextTask makes the next task BulkMove creates end in TaskFailed rather
// than TaskComplete.
func (f *Fake) FailNextTask() {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.failTask = true
}

// Calls returns the method names called so far, in order, including the ones
// that returned an error and one entry per page of a paginated walk.
func (f *Fake) Calls() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return slices.Clone(f.calls)
}

// Reset puts the fake back to how New left it, re-applying the same options.
func (f *Fake) Reset() {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.fakeInit()
}

// fakeBegin records the call, consumes any queued error and serves the delay.
// It runs before every method body and holds the lock only long enough to take
// what it needs, so that a slow call does not block the rest of the fake.
func (f *Fake) fakeBegin(ctx context.Context, name string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	f.mu.Lock()
	f.calls = append(f.calls, name)
	delay := f.delay
	var queued error
	if len(f.failures) > 0 {
		queued, f.failures = f.failures[0], f.failures[1:]
	}
	f.mu.Unlock()

	if delay > 0 {
		timer := time.NewTimer(delay)
		defer timer.Stop()
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-timer.C:
		}
	}
	return queued
}

func (f *Fake) fakeNextID(prefix string) string {
	f.seq++
	return prefix + "-" + strconv.Itoa(f.seq)
}

// fakeAddProject registers a project, or upgrades one that was auto-registered
// by WithIssues before WithProject named its board.
func (f *Fake) fakeAddProject(key string, kind BoardKind) *fakeProject {
	if p, ok := f.projects[key]; ok {
		if p.boardID == 0 && kind != NoBoard {
			p.kind = kind
			f.fakeAddBoard(p, kind)
		}
		return p
	}
	p := &fakeProject{ref: fakeProjectRef(key), kind: kind}
	seeded := fakeVersionsFor(key)
	for i := range seeded {
		f.versions[seeded[i].ID] = &seeded[i]
		p.versionIDs = append(p.versionIDs, seeded[i].ID)
	}
	f.projects[key] = p
	f.projectKeys = append(f.projectKeys, key)
	slices.Sort(f.projectKeys)
	f.fakeAddBoard(p, kind)
	return p
}

func (f *Fake) fakeAddBoard(p *fakeProject, kind BoardKind) {
	if kind == NoBoard {
		return
	}
	typ := jira.BoardScrum
	if kind == Kanban {
		typ = jira.BoardKanban
	}
	id := fakeBoardID(p.ref.Key)
	f.boards[id] = &jira.Board{ID: id, Name: p.ref.Key + " board", Type: typ, ProjectKey: p.ref.Key}
	p.boardID = id
	if kind != Scrum {
		return
	}
	for i, seed := range fakeSeedSprints(id) {
		sp := seed
		sp.ID = id*100 + int64(i)
		f.sprints[sp.ID] = &sp
	}
}

// fakeSeedSprints builds one sprint in each state, dated around the fixed epoch
// rather than around the fake's clock so the seed does not move with WithNow.
func fakeSeedSprints(boardID int64) []jira.Sprint {
	past := fakeEpoch.AddDate(0, 0, -28)
	mid := fakeEpoch.AddDate(0, 0, -14)
	soon := fakeEpoch.AddDate(0, 0, 7)
	return []jira.Sprint{
		{BoardID: boardID, Name: "Sprint 1", Goal: "Ship the skeleton", State: jira.SprintClosed, Start: &past, End: &mid, Complete: &mid},
		{BoardID: boardID, Name: "Sprint 2", Goal: "Make it usable", State: jira.SprintActive, Start: &mid, End: &soon},
		{BoardID: boardID, Name: "Sprint 3", State: jira.SprintFuture},
	}
}

func (f *Fake) fakePutIssue(in *jira.Issue) {
	if _, ok := f.projects[in.Project.Key]; !ok && in.Project.Key != "" {
		f.fakeAddProject(in.Project.Key, NoBoard)
	}
	stored := fakeCloneIssue(in)
	// The one funnel into the store, so a seeded issue is as honest as a fetched one.
	stored.Requested = jira.AllFields()
	if _, ok := f.issues[stored.Key]; !ok {
		f.issueKeys = append(f.issueKeys, stored.Key)
	}
	f.issues[stored.Key] = &stored
}

func fakeCloneFields(in []jira.Field) []jira.Field {
	out := make([]jira.Field, 0, len(in))
	for i := range in {
		field := in[i]
		field.ClauseNames = slices.Clone(field.ClauseNames)
		out = append(out, field)
	}
	return out
}

func fakeCloneIssue(in *jira.Issue) jira.Issue {
	out := *in
	out.Labels = slices.Clone(in.Labels)
	out.Components = slices.Clone(in.Components)
	out.FixVersions = slices.Clone(in.FixVersions)
	out.Subtasks = slices.Clone(in.Subtasks)
	out.Links = slices.Clone(in.Links)
	out.Fields = in.Fields
	out.Description = in.Description.Clone()
	out.Assignee = fakeClonePtr(in.Assignee)
	out.Reporter = fakeClonePtr(in.Reporter)
	out.Priority = fakeClonePtr(in.Priority)
	out.Resolution = fakeClonePtr(in.Resolution)
	out.Parent = fakeClonePtr(in.Parent)
	out.Resolved = fakeClonePtr(in.Resolved)
	out.TimeTracking = fakeClonePtr(in.TimeTracking)
	return out
}

func fakeCloneVersion(in *jira.Version) jira.Version {
	out := *in
	out.Unresolved = fakeClonePtr(in.Unresolved)
	return out
}

// fakeCloneSprint copies a sprint including the times behind its pointers, so
// a caller cannot move a sprint by writing through what it was handed.
func fakeCloneSprint(in jira.Sprint) jira.Sprint {
	out := in
	out.Start = fakeClonePtr(in.Start)
	out.End = fakeClonePtr(in.End)
	out.Complete = fakeClonePtr(in.Complete)
	return out
}

// fakeInStates narrows sprints to the states asked for; no state means all.
func fakeInStates(in []jira.Sprint, states []jira.SprintState) []jira.Sprint {
	if len(states) == 0 {
		return in
	}
	out := make([]jira.Sprint, 0, len(in))
	for i := range in {
		if slices.Contains(states, in[i].State) {
			out = append(out, in[i])
		}
	}
	return out
}

func fakeClonePtr[T any](p *T) *T {
	if p == nil {
		return nil
	}
	v := *p
	return &v
}

// fakeRetainFields copies a field set without the named IDs.
func fakeRetainFields(in jira.FieldSet, drop map[string]bool) jira.FieldSet {
	out := in
	for id := range drop {
		out = out.Without(jira.FieldRef{ID: id})
	}
	return out
}
