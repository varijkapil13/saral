package jira

import (
	"fmt"
	"io"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strings"
	"time"

	"github.com/varijkapil13/saral/pkg/adf"
)

// Date is a calendar date with no time and no zone, which is what Jira's due
// date, sprint boundaries and version dates actually are. Holding them as a
// time.Time is how a release slips a day for anyone east of the server.
type Date struct {
	Year  int
	Month time.Month
	Day   int
}

// ParseDate reads Jira's date format, "2006-01-02".
func ParseDate(s string) (Date, error) {
	t, err := time.Parse(time.DateOnly, s)
	if err != nil {
		return Date{}, fmt.Errorf("jira: parsing date %q: %w", s, err)
	}
	return DateOf(t), nil
}

// DateOf takes the calendar date out of a time, in that time's own zone.
func DateOf(t time.Time) Date {
	y, m, d := t.Date()
	return Date{Year: y, Month: m, Day: d}
}

// IsZero reports whether the date is unset.
func (d Date) IsZero() bool { return d == Date{} }

// String renders the date the way Jira writes it, or "" when unset.
func (d Date) String() string {
	if d.IsZero() {
		return ""
	}
	return fmt.Sprintf("%04d-%02d-%02d", d.Year, int(d.Month), d.Day)
}

// In returns midnight on this date in the given location.
func (d Date) In(loc *time.Location) time.Time {
	if loc == nil {
		loc = time.UTC
	}
	return time.Date(d.Year, d.Month, d.Day, 0, 0, 0, 0, loc)
}

// Before reports whether d falls before other.
func (d Date) Before(other Date) bool { return d.In(time.UTC).Before(other.In(time.UTC)) }

// User is a Jira account. Email is often absent — Jira hides it unless the
// account's privacy settings allow it, so nothing may depend on having one.
type User struct {
	AccountID   string
	DisplayName string
	Email       string
	TimeZone    *time.Location
	Active      bool
	AvatarURL   string
}

// ProjectRef identifies a project.
type ProjectRef struct {
	ID   string
	Key  string
	Name string
}

// StatusCategory is Jira's three-valued grouping of statuses. It is the only
// status property that is the same on every site, which makes it the only safe
// thing to group board columns or filter by.
type StatusCategory int

// The status categories, in board order.
const (
	CategoryUnknown StatusCategory = iota
	CategoryToDo
	CategoryInProgress
	CategoryDone
)

// ParseStatusCategory maps the API's category key to a StatusCategory.
func ParseStatusCategory(key string) StatusCategory {
	switch strings.ToLower(key) {
	case "new", "to do", "todo":
		return CategoryToDo
	case "indeterminate", "in progress":
		return CategoryInProgress
	case "done", "complete":
		return CategoryDone
	default:
		return CategoryUnknown
	}
}

// String returns the category's display name.
func (c StatusCategory) String() string {
	switch c {
	case CategoryToDo:
		return "To Do"
	case CategoryInProgress:
		return "In Progress"
	case CategoryDone:
		return "Done"
	default:
		return "Unknown"
	}
}

// Status is a workflow status. Name varies per site; Category does not.
type Status struct {
	ID       string
	Name     string
	Category StatusCategory
}

// IssueType is a project's issue type. IDs are per-site.
type IssueType struct {
	ID      string
	Name    string
	Subtask bool
	IconURL string
}

// Priority is an issue priority.
type Priority struct {
	ID   string
	Name string
}

// Resolution is an issue resolution.
type Resolution struct {
	ID   string
	Name string
}

// Component is a project component.
type Component struct {
	ID   string
	Name string
}

// Option is one allowed value of a select-like field.
type Option struct {
	ID    string
	Value string
}

// FieldSchema describes what a field holds, as Jira reports it.
type FieldSchema struct {
	Type     string // "string", "number", "date", "datetime", "option", "array", "user", ...
	Items    string // element type when Type is "array"
	System   string // set for built-in fields
	Custom   string // the custom field type URI, set for custom fields
	CustomID int
}

// Field is a field definition from the site's field catalogue. Custom field IDs
// differ on every site, so a field is always looked up by Name and then used by
// ID — never written into code as a customfield_NNNNN literal.
type Field struct {
	ID          string
	Key         string
	Name        string
	Custom      bool
	Navigable   bool
	Searchable  bool
	Orderable   bool
	ClauseNames []string
	Schema      FieldSchema
}

// Ref returns the reference used to read and write this field's values.
func (f Field) Ref() FieldRef { return FieldRef{ID: f.ID, Name: f.Name, Schema: f.Schema} }

// FieldRef is a resolved field: the site-specific ID plus enough about the
// field to interpret its value.
type FieldRef struct {
	ID     string
	Name   string
	Schema FieldSchema
}

// FieldByName finds a field by its display name, case-insensitively. Names are
// not unique on every site, so the first match in catalogue order wins and the
// caller may want to disambiguate on Schema.
func FieldByName(fields []Field, name string) (Field, bool) {
	for i := range fields {
		if strings.EqualFold(fields[i].Name, name) {
			return fields[i], true
		}
	}
	return Field{}, false
}

// FieldKind is which slot of a FieldValue carries the value.
type FieldKind int

// The field value kinds.
const (
	KindEmpty FieldKind = iota
	KindText
	KindNumber
	KindBool
	KindDate
	KindTime
	KindDoc
	KindOption
	KindOptions
	KindUser
	KindUsers
	KindUnknown // a schema type this client does not model; Text holds a display form
)

// FieldValue is one field's value. It is a tagged union rather than an any so
// that nothing above the adapter has to type-assert its way through Jira's
// field zoo.
type FieldValue struct {
	Kind    FieldKind
	Text    string
	Number  float64
	Bool    bool
	Date    Date
	Time    time.Time
	Doc     adf.Doc
	Options []Option
	Users   []User
}

// clone detaches a value's slices so that neither the caller who stored it nor
// the caller who reads it can write into the other's copy.
func (v FieldValue) clone() FieldValue {
	v.Options = slices.Clone(v.Options)
	v.Users = slices.Clone(v.Users)
	return v
}

// FieldSet holds the field values carried on an issue, keyed by field ID.
//
// It is immutable: With and Without return a new set instead of changing this
// one. A FieldSet travels by value — into an Issue, into an IssuePatch, out of
// the cache — and a mutable map behind a value type would mean that seeding an
// edit form from a cached issue rewrites the cached issue.
type FieldSet struct {
	values map[string]FieldValue
}

// NewFieldSet builds a set from field IDs to values, which is how an adapter
// assembles one without a chain of copies.
func NewFieldSet(values map[string]FieldValue) FieldSet {
	if len(values) == 0 {
		return FieldSet{}
	}
	out := make(map[string]FieldValue, len(values))
	for id := range values {
		out[id] = values[id].clone()
	}
	return FieldSet{values: out}
}

// With returns a copy of the set carrying one more value.
func (s FieldSet) With(ref FieldRef, v FieldValue) FieldSet {
	out := make(map[string]FieldValue, len(s.values)+1)
	maps.Copy(out, s.values)
	out[ref.ID] = v.clone()
	return FieldSet{values: out}
}

// Without returns a copy of the set with a field removed.
func (s FieldSet) Without(ref FieldRef) FieldSet {
	if _, ok := s.values[ref.ID]; !ok {
		return s
	}
	out := maps.Clone(s.values)
	delete(out, ref.ID)
	return FieldSet{values: out}
}

// Get returns the value stored for a field.
func (s FieldSet) Get(ref FieldRef) (FieldValue, bool) {
	v, ok := s.values[ref.ID]
	return v.clone(), ok
}

// Text returns a string-valued field.
func (s FieldSet) Text(ref FieldRef) (string, bool) {
	v, ok := s.values[ref.ID]
	if !ok || (v.Kind != KindText && v.Kind != KindUnknown) {
		return "", false
	}
	return v.Text, true
}

// Number returns a numeric field, which is how story points arrive.
func (s FieldSet) Number(ref FieldRef) (float64, bool) {
	v, ok := s.values[ref.ID]
	if !ok || v.Kind != KindNumber {
		return 0, false
	}
	return v.Number, true
}

// Date returns a date-valued field.
func (s FieldSet) Date(ref FieldRef) (Date, bool) {
	v, ok := s.values[ref.ID]
	if !ok {
		return Date{}, false
	}
	switch v.Kind {
	case KindDate:
		return v.Date, true
	case KindTime:
		return DateOf(v.Time), true
	default:
		return Date{}, false
	}
}

// Time returns a datetime-valued field.
func (s FieldSet) Time(ref FieldRef) (time.Time, bool) {
	v, ok := s.values[ref.ID]
	if !ok || v.Kind != KindTime {
		return time.Time{}, false
	}
	return v.Time, true
}

// Doc returns a rich-text field.
func (s FieldSet) Doc(ref FieldRef) (adf.Doc, bool) {
	v, ok := s.values[ref.ID]
	if !ok || v.Kind != KindDoc {
		return adf.Doc{}, false
	}
	return v.Doc, true
}

// Options returns a select-like field's chosen values.
func (s FieldSet) Options(ref FieldRef) ([]Option, bool) {
	v, ok := s.values[ref.ID]
	if !ok || (v.Kind != KindOption && v.Kind != KindOptions) {
		return nil, false
	}
	return slices.Clone(v.Options), true
}

// Users returns a user-valued field's accounts.
func (s FieldSet) Users(ref FieldRef) ([]User, bool) {
	v, ok := s.values[ref.ID]
	if !ok || (v.Kind != KindUser && v.Kind != KindUsers) {
		return nil, false
	}
	return slices.Clone(v.Users), true
}

// Len reports how many fields carry a value.
func (s FieldSet) Len() int { return len(s.values) }

// IDs returns the field IDs present, sorted, so that iteration is stable.
func (s FieldSet) IDs() []string {
	ids := make([]string, 0, len(s.values))
	for id := range s.values {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	return ids
}

// ByID returns the value stored under a raw field ID, for the cases where only
// the ID is known.
func (s FieldSet) ByID(id string) (FieldValue, bool) {
	v, ok := s.values[id]
	return v.clone(), ok
}

// IssueRef identifies an issue without carrying its whole body.
type IssueRef struct {
	ID      string
	Key     string
	Summary string
	Status  Status
	Type    IssueType
}

// LinkDirection says which end of a link the issue at hand is on.
type LinkDirection int

// The two ends of an issue link.
const (
	LinkOutward LinkDirection = iota
	LinkInward
)

// IssueLink is a typed relationship to another issue.
type IssueLink struct {
	ID        string
	Type      string // the link type name, e.g. "Blocks"
	Label     string // the directional phrasing, e.g. "is blocked by"
	Direction LinkDirection
	Other     IssueRef
}

// TimeTracking holds an issue's estimates, in seconds.
type TimeTracking struct {
	OriginalEstimate  int64
	RemainingEstimate int64
	TimeSpent         int64
}

// Issue is a Jira issue. Fields carries everything outside this struct,
// including every custom field, keyed by the site's own field IDs.
type Issue struct {
	ID           string
	Key          string
	Project      ProjectRef
	Summary      string
	Description  adf.Doc
	Type         IssueType
	Status       Status
	Priority     *Priority
	Resolution   *Resolution
	Assignee     *User
	Reporter     *User
	Labels       []string
	Components   []Component
	FixVersions  []Version
	Parent       *IssueRef
	Subtasks     []IssueRef
	Links        []IssueLink
	Due          Date
	Created      time.Time
	Updated      time.Time
	Resolved     *time.Time
	TimeTracking *TimeTracking
	Fields       FieldSet
}

// IssueInput is a new issue. Anything beyond the fields named here goes in
// Fields, which is what the schema-driven create form populates.
type IssueInput struct {
	ProjectKey  string
	IssueTypeID string
	Summary     string
	Description adf.Doc
	ParentKey   string
	Labels      []string
	Assignee    string // account ID
	Fields      FieldSet
}

// IssuePatch is a sparse update: a nil pointer leaves the field alone, and a
// field named in Clear is set to null. The two are separate because "leave it"
// and "empty it" are different requests and a single zero value cannot say
// which one was meant.
type IssuePatch struct {
	Summary     *string
	Description *adf.Doc
	Assignee    *string
	Labels      *[]string
	PriorityID  *string
	Due         *Date
	Fields      FieldSet
	Clear       []FieldRef
	Notify      *bool
}

// IsEmpty reports whether the patch would change nothing.
func (p IssuePatch) IsEmpty() bool {
	return p.Summary == nil && p.Description == nil && p.Assignee == nil &&
		p.Labels == nil && p.PriorityID == nil && p.Due == nil &&
		p.Fields.Len() == 0 && len(p.Clear) == 0
}

// FieldMeta describes one field on a create, edit or transition screen.
type FieldMeta struct {
	Field           FieldRef
	Name            string
	Required        bool
	HasDefault      bool
	Operations      []string
	AllowedValues   []Option
	AutoCompleteURL string
}

// Schema is what a project and issue type require in order to create an issue.
type Schema struct {
	Project   ProjectRef
	IssueType IssueType
	Fields    []FieldMeta
}

// Required returns the fields that must be filled in.
func (s Schema) Required() []FieldMeta {
	out := make([]FieldMeta, 0, len(s.Fields))
	for i := range s.Fields {
		if s.Fields[i].Required {
			out = append(out, s.Fields[i])
		}
	}
	return out
}

// Transition is a workflow move available on one issue right now. Status is not
// writable on Jira, so this is the only way an issue changes state.
type Transition struct {
	ID        string
	Name      string
	To        Status
	HasScreen bool
	Fields    []FieldMeta
}

// Visibility restricts a comment to a role or group.
type Visibility struct {
	Type  string // "role" or "group"
	Value string
}

// Comment is a comment on an issue.
type Comment struct {
	ID           string
	Author       User
	UpdateAuthor *User
	Body         adf.Doc
	Created      time.Time
	Updated      time.Time
	Visibility   *Visibility
}

// Attachment is a file attached to an issue.
type Attachment struct {
	ID           string
	Filename     string
	MimeType     string
	Size         int64
	Created      time.Time
	Author       User
	ContentURL   string
	ThumbnailURL string
}

// FileRef is a file to upload. Open is called once per attempt, so a retry
// re-reads the file rather than buffering it in memory.
type FileRef struct {
	Name string
	Size int64
	Open func() (io.ReadCloser, error)
}

// FileFromPath makes a FileRef for a file on disk.
func FileFromPath(path string) (FileRef, error) {
	info, err := os.Stat(path)
	if err != nil {
		return FileRef{}, fmt.Errorf("jira: reading %s: %w", path, err)
	}
	return FileRef{
		Name: filepath.Base(path),
		Size: info.Size(),
		Open: func() (io.ReadCloser, error) { return os.Open(path) },
	}, nil
}

// Version is a project version, which is what a release is before it ships.
type Version struct {
	ID          string
	ProjectID   string
	Name        string
	Description string
	Archived    bool
	Released    bool
	StartDate   Date
	ReleaseDate Date

	// Unresolved is how many issues on the version are still open. It is nil
	// unless the caller asked for it: releasing a version with open issues is
	// a decision the user has to make, and Jira will not make it for them.
	Unresolved *int
}

// VersionInput creates or updates a version. An empty ID creates.
type VersionInput struct {
	ID          string
	ProjectKey  string
	Name        string
	Description string
	StartDate   Date
	ReleaseDate Date
	Archived    *bool
}

// UnresolvedPolicy is what to do about issues still open on a version being
// released. Jira's own API silently releases with open issues, so the choice is
// made here, in front of the user.
type UnresolvedPolicy int

// What to do with the issues left open on a version at release time.
const (
	// ReleaseAnyway leaves the open issues on the version.
	ReleaseAnyway UnresolvedPolicy = iota
	// MoveUnresolved moves them to another version.
	MoveUnresolved
	// StripUnresolved removes the version from them.
	StripUnresolved
)

// ReleaseInput releases a version.
type ReleaseInput struct {
	ReleaseDate     Date
	Unresolved      UnresolvedPolicy
	MoveToVersionID string
}

// BoardType is how a board behaves.
type BoardType string

// The board types the Agile API reports.
const (
	BoardScrum  BoardType = "scrum"
	BoardKanban BoardType = "kanban"
	BoardSimple BoardType = "simple"
)

// Board is an Agile board.
type Board struct {
	ID         int64
	Name       string
	Type       BoardType
	ProjectKey string
}

// Column is one column of a board, defined by the statuses mapped into it.
type Column struct {
	Name      string
	StatusIDs []string
	Min       *int
	Max       *int
}

// EstimationType is how a board measures issues.
type EstimationType string

// The estimation types a board configuration reports.
const (
	EstimationNone       EstimationType = "none"
	EstimationField      EstimationType = "field"
	EstimationIssueCount EstimationType = "issueCount"
)

// Estimation is a board's estimation setting. Field is meaningless unless Type
// is EstimationField, and its ID differs on every site.
type Estimation struct {
	Type  EstimationType
	Field FieldRef
}

// BoardConfig is everything about a board that must be read rather than
// assumed: which statuses make up which column, what the board estimates in,
// and which custom field holds rank.
type BoardConfig struct {
	BoardID     int64
	Name        string
	Type        BoardType
	Columns     []Column
	Estimation  Estimation
	RankFieldID string
	FilterID    string
}

// SprintState is where a sprint is in its lifecycle. The only legal moves are
// future to active to closed.
type SprintState string

// The sprint states.
const (
	SprintFuture SprintState = "future"
	SprintActive SprintState = "active"
	SprintClosed SprintState = "closed"
)

// Sprint is a sprint on a board.
type Sprint struct {
	ID       int64
	BoardID  int64
	Name     string
	Goal     string
	State    SprintState
	Start    *time.Time
	End      *time.Time
	Complete *time.Time
}

// SprintInput creates a sprint.
type SprintInput struct {
	BoardID int64
	Name    string
	Goal    string
	Start   *time.Time
	End     *time.Time
}

// SprintPatch updates the parts of a sprint the caller names. Every field is a
// pointer because the underlying API's full-replace PUT nulls anything omitted,
// and the port must never let that reach a caller.
type SprintPatch struct {
	Name  *string
	Goal  *string
	Start *time.Time
	End   *time.Time
}

// StatusMapping remaps one status to another when an issue changes project.
type StatusMapping struct {
	FromStatusID string
	ToStatusID   string
}

// MoveRequest moves issues to another project, which Jira only allows through
// its asynchronous bulk endpoint.
type MoveRequest struct {
	Keys              []string
	TargetProjectKey  string
	TargetIssueTypeID string
	StatusMap         []StatusMapping
	Fields            FieldSet
	Notify            bool
}

// TaskRef identifies a long-running Jira task. The submit response carries only
// an ID, so URL is the progress endpoint the adapter built for it — for a bulk
// move that is /rest/api/3/bulk/queue/{id}, not the generic task endpoint.
type TaskRef struct {
	ID  string
	URL string
}

// TaskState is where an asynchronous task has got to.
type TaskState string

// The task states Jira reports.
const (
	TaskEnqueued  TaskState = "ENQUEUED"
	TaskRunning   TaskState = "RUNNING"
	TaskComplete  TaskState = "COMPLETE"
	TaskFailed    TaskState = "FAILED"
	TaskCancelled TaskState = "CANCELLED"
	TaskDead      TaskState = "DEAD"
)

// Done reports whether the task has stopped, however it ended.
func (s TaskState) Done() bool {
	switch s {
	case TaskComplete, TaskFailed, TaskCancelled, TaskDead:
		return true
	default:
		return false
	}
}

// TaskStatus is a snapshot of a long-running task.
type TaskStatus struct {
	Ref      TaskRef
	State    TaskState
	Progress int // percent, 0-100
	Message  string
	Failed   []string // keys the task could not move, when it reports them
}

// PlanSourceType is where a plan draws its issues from.
type PlanSourceType string

// The plan issue-source types.
const (
	PlanSourceProject PlanSourceType = "project"
	PlanSourceBoard   PlanSourceType = "board"
	PlanSourceFilter  PlanSourceType = "filter"
)

// PlanSource is one issue source of a plan.
type PlanSource struct {
	Type  PlanSourceType
	Value string
}

// Plan is an Advanced Roadmaps plan, or a locally defined stand-in for one.
// Local is true for the fallback plans this client builds from config when the
// Plans API is out of reach, which is the normal case: it needs Administer
// Jira, and per-plan permissions in the web UI do not grant it.
type Plan struct {
	ID      string
	Name    string
	Status  string
	Sources []PlanSource
	Local   bool
}

// Query is a search. Fields must be an explicit, narrow list of field IDs:
// /search/jql returns almost nothing without one, and asking for everything is
// the single most expensive mistake available.
type Query struct {
	JQL        string
	Fields     []string
	Expand     []string
	Properties []string
	MaxResults int
}

// GraphicsMode is how, if at all, this terminal can show an image.
type GraphicsMode int

// The terminal graphics modes, best first.
const (
	GraphicsNone GraphicsMode = iota
	GraphicsHalfBlocks
	GraphicsITerm2
	GraphicsKitty
)

// String names the graphics mode.
func (g GraphicsMode) String() string {
	switch g {
	case GraphicsKitty:
		return "kitty"
	case GraphicsITerm2:
		return "iterm2"
	case GraphicsHalfBlocks:
		return "halfblocks"
	default:
		return "none"
	}
}

// CapabilityKey names one thing a site and token may or may not be able to do.
type CapabilityKey string

// The capabilities probed at connect time.
const (
	CapPlans        CapabilityKey = "plans"
	CapBulkMove     CapabilityKey = "bulk-move"
	CapBoards       CapabilityKey = "boards"
	CapAttachments  CapabilityKey = "attachments"
	CapDeleteIssues CapabilityKey = "delete-issues"
)

// Capability is one probe result. A negative is an answer with a reason
// attached, not an error: the reason is what the UI shows instead of the view.
type Capability struct {
	OK     bool
	Reason string
}

// Capabilities is what this site and token can do, probed once per site and
// cached. Views read it; nothing re-probes on its own.
type Capabilities struct {
	Plans        Capability
	BulkMove     Capability
	Boards       Capability
	Attachments  Capability
	DeleteIssues Capability
	Graphics     GraphicsMode
	TimeZone     *time.Location
}

// Capability returns the probe result for a key. An unknown key comes back as
// unavailable rather than as available, so a typo can never unlock a view.
func (c Capabilities) Capability(k CapabilityKey) Capability {
	switch k {
	case CapPlans:
		return c.Plans
	case CapBulkMove:
		return c.BulkMove
	case CapBoards:
		return c.Boards
	case CapAttachments:
		return c.Attachments
	case CapDeleteIssues:
		return c.DeleteIssues
	default:
		return Capability{Reason: fmt.Sprintf("unknown capability %q", k)}
	}
}

// Allows reports whether the capability is available.
func (c Capabilities) Allows(k CapabilityKey) bool { return c.Capability(k).OK }

// Require returns a *CapabilityError when the capability is absent, so that a
// caller can refuse the action with the probe's own wording.
func (c Capabilities) Require(k CapabilityKey) error {
	if got := c.Capability(k); !got.OK {
		return &CapabilityError{Capability: k, Reason: got.Reason}
	}
	return nil
}

// Location returns the timezone to render dates in, which is the Jira account's
// timezone and not the machine's.
func (c Capabilities) Location() *time.Location {
	if c.TimeZone == nil {
		return time.UTC
	}
	return c.TimeZone
}
