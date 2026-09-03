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
	"unicode"

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

// AccountKind is what sort of account this is. It labels; it does not filter.
// An app account is assigned work and reports issues exactly as a person does,
// so hiding one loses rows — but a site whose account list is ten robots and one
// human is unreadable without the distinction, which is what a picker sinks and
// badges by.
type AccountKind int

// The account kinds, in the order a picker prefers them.
const (
	// AccountUnknown is a read that did not say. It is not a fourth kind of
	// account: several endpoints answer a user with no accountType at all.
	AccountUnknown AccountKind = iota
	AccountPerson
	AccountApp
	AccountCustomer
)

// ParseAccountKind maps the API's accountType to an AccountKind. The values are
// an enum rather than display text, so unlike a status or a priority name they
// are the same on a site in any language.
func ParseAccountKind(accountType string) AccountKind {
	switch strings.ToLower(strings.TrimSpace(accountType)) {
	case "atlassian":
		return AccountPerson
	case "app":
		return AccountApp
	case "customer":
		return AccountCustomer
	default:
		return AccountUnknown
	}
}

// String returns the kind's name for a badge, and "" for a read that did not
// say, so that a picker drawing it puts nothing rather than the word "unknown"
// beside every row on an endpoint that omits the field.
func (k AccountKind) String() string {
	switch k {
	case AccountPerson:
		return "person"
	case AccountApp:
		return "app"
	case AccountCustomer:
		return "customer"
	default:
		return ""
	}
}

// User is a Jira account. Email is often absent — Jira hides it unless the
// account's privacy settings allow it, so nothing may depend on having one.
//
// Kind is filled wherever an account arrives: one decoder reads accountType, so
// an issue's assignee and reporter, a comment's author and update author,
// /myself and the people endpoints all carry a kind. It is AccountUnknown on a
// read that did not carry one, which means the answer was silent rather than
// that the account is odd.
type User struct {
	AccountID   string
	DisplayName string
	Email       string
	TimeZone    *time.Location
	Active      bool
	AvatarURL   string
	Kind        AccountKind
}

// PeopleQuery narrows a search for accounts.
//
// Match is handed to Jira, which is the whole difficulty. Its matching is
// neither substring nor fuzzy and is documented nowhere: measured on a real
// site, it takes a prefix of any word of the display name, the initials of it,
// and part of the email address, so a two-letter needle found an account by its
// initials and a different two-letter needle found nobody although a surname on
// the site contains it. Nothing local reproduces that, and no read says which
// rule fired.
//
// Two consequences for a caller. Type-ahead has to go back to the site rather
// than narrow what it already holds, because a longer needle can match what a
// shorter one did not. And the order the answer arrives in is Jira's, not a
// ranking: a picker ranks what comes back itself and never presents that order
// as its own.
//
// Project scopes the search to accounts that can be assigned work in one
// project. It is worth setting for a picker even where the search is not about
// assigning: the assignable endpoint drops app accounts for free, and on the
// measured site that was ten of eleven accounts.
//
// Limit is a ceiling, not a page size — the port does not page people, because a
// person is found by typing more rather than by paging. Zero asks for the
// adapter's own default.
type PeopleQuery struct {
	Match   string
	Project string
	Limit   int
}

// IssueTypeStatuses is the statuses one issue type can be in, in one project.
//
// It carries ids because neither name identifies anything: a status name is
// localised, and a team-managed project mints project-scoped statuses that reuse
// the stock names, so two distinct ids answer to one string on one site. Grouping
// or filtering by the name silently merges them.
//
// The statuses are per issue type and not per project — two types in one project
// can run different workflows — so a filter offering "every status here" is the
// union of these and has to say which types it came from.
type IssueTypeStatuses struct {
	Type     IssueType
	Statuses []Status
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
//
// Jira labels these two different ways depending on the field: a custom select
// option carries a value, while a priority, issue type, project or version
// carries a name. Label is whichever one arrived, so a picker never renders a
// column of empty strings.
type Option struct {
	ID    string
	Label string
	// Children are the second level of a cascading select, empty otherwise.
	Children []Option
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
// differ on every site, so a field is always looked up by name and then used by
// ID — never written into code as a customfield_NNNNN literal.
type Field struct {
	ID string
	// Key is the field's key, which for a custom field is customfield_NNNNN.
	Key string
	// Name is the display name, in the site's language. On a German site the
	// story point field is not called "Story Points".
	Name string
	// UntranslatedName is the name Jira gives the field regardless of site
	// language, and it is what appears in ClauseNames. Jira sends it for custom
	// fields only, so it is empty on every system field. It is not the field's
	// English display name either: a field displayed as "Release Windows" can be
	// "ReleaseWindows" here, on an English site and a German one alike.
	UntranslatedName string
	Custom           bool
	Navigable        bool
	Searchable       bool
	Orderable        bool
	// ClauseNames are the identifiers this field can be written as in JQL. They
	// follow UntranslatedName, not Name. A field may send none, which is the
	// site saying it cannot be named in JQL at all — not even as cf[NNNNN].
	ClauseNames []string
	// Schema is absent on a few system fields — parent, issuekey, thumbnail —
	// which is Jira saying they are not written through the generic field path.
	Schema FieldSchema
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

// FieldNameError reports that a name did not resolve to exactly one field on
// this site.
//
// Matches holds the candidates when the name was ambiguous and is empty when no
// field carried it at all. That is the distinction a caller has to be able to put
// in front of somebody: one is a name to correct, the other is a name that has to
// be said a different way.
type FieldNameError struct {
	Name    string
	Matches []Field
}

// Ambiguous reports whether the name belongs to more than one field.
func (e *FieldNameError) Ambiguous() bool { return len(e.Matches) > 1 }

func (e *FieldNameError) Error() string {
	if !e.Ambiguous() {
		return fmt.Sprintf("no field on this site is called %q", e.Name)
	}
	ids := make([]string, 0, len(e.Matches))
	for i := range e.Matches {
		ids = append(ids, e.Matches[i].ID)
	}
	return fmt.Sprintf("%q is the name of %d fields on this site (%s), so it does not say which one",
		e.Name, len(e.Matches), strings.Join(ids, ", "))
}

// ResolveField turns a field name written down somewhere — a profile, a saved
// query, a board's estimation setting — into the one field on this site that it
// means, or says why it cannot.
//
// A site spells one field's name up to three ways and shows only two of them at
// a time. UntranslatedName does not move with the site language but is sent for
// custom fields only, and it is not the English display name either: a field
// displayed as "Release Windows" in English can be "Freigabefenster" in German
// and "ReleaseWindows" in UntranslatedName on both. So a name is compared four
// ways, in this order, and the first way that matches anything decides:
//
//  1. UntranslatedName, ignoring case
//  2. Name, ignoring case
//  3. UntranslatedName, ignoring case and every separator
//  4. Name, ignoring case and every separator
//
// The last two are what carry a display name copied off an English site onto a
// translated one. They come last so that a name a field really displays always
// beats a name reconstructed from one.
//
// Two different fields matching at the same level is unresolvable rather than a
// coin toss. Display names are not unique — a catalogue can hold two fields
// called the same thing, and translation collapses distinct names into one — and
// answering with either of them reads or writes a field nobody named.
func ResolveField(fields []Field, name string) (Field, error) {
	wanted := strings.TrimSpace(name)
	if wanted == "" {
		return Field{}, &FieldNameError{Name: name}
	}
	folded := foldFieldName(wanted)
	for _, matches := range []func(Field) bool{
		func(f Field) bool { return f.UntranslatedName != "" && strings.EqualFold(f.UntranslatedName, wanted) },
		func(f Field) bool { return strings.EqualFold(f.Name, wanted) },
		func(f Field) bool { return folded != "" && foldFieldName(f.UntranslatedName) == folded },
		func(f Field) bool { return folded != "" && foldFieldName(f.Name) == folded },
	} {
		switch found := fieldsMatching(fields, matches); len(found) {
		case 0:
		case 1:
			return found[0], nil
		default:
			return Field{}, &FieldNameError{Name: wanted, Matches: found}
		}
	}
	return Field{}, &FieldNameError{Name: wanted}
}

// FieldByName finds the one field a name means, and reports whether exactly one
// field means it. Prefer ResolveField where the caller can say something useful
// about why a name did not resolve — an ambiguous name and an unknown one need
// different answers from the person who wrote it down.
func FieldByName(fields []Field, name string) (Field, bool) {
	field, err := ResolveField(fields, name)
	return field, err == nil
}

// fieldsMatching collects the distinct fields a rule matches. One field listed
// twice is one field; two ids are two candidates.
func fieldsMatching(fields []Field, matches func(Field) bool) []Field {
	var out []Field
	for i := range fields {
		if !matches(fields[i]) {
			continue
		}
		if slices.ContainsFunc(out, func(seen Field) bool { return seen.ID == fields[i].ID }) {
			continue
		}
		out = append(out, fields[i])
	}
	return out
}

// foldFieldName reduces a name to its letters and digits, lowercased, so that a
// display name matches the compressed spelling UntranslatedName uses for the
// same field.
func foldFieldName(s string) string {
	var b strings.Builder
	b.Grow(len(s))
	for _, r := range s {
		if unicode.IsLetter(r) || unicode.IsDigit(r) {
			b.WriteRune(unicode.ToLower(r))
		}
	}
	return b.String()
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
	KindUnknown // a schema type this client does not model; Text holds the bytes verbatim, and Names reads them
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
	v.Doc = v.Doc.Clone()
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
	return v.Doc.Clone(), true
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
	// Requested is the set of fields this issue was read with. A field outside
	// it is absent because nothing asked for it, which is a different answer
	// from Jira having nothing to send.
	Requested FieldMask
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
	Field    FieldRef
	Name     string
	Required bool
	// HasDefault is the site saying it will fill this field in when the request
	// omits it.
	HasDefault bool
	// Default is what it will fill it in with, and is meaningful only when
	// HasDefault. A screen may say there is a default and not say what it is —
	// Jira sends a null beside hasDefaultValue true for the reporter — and that
	// arrives here as KindEmpty, which is a different answer from no default.
	//
	// It is for showing, not for sending. A caller that seeds a write with it
	// turns a server-side default into an explicit value in the request, and the
	// project's default then stops applying to anything this client creates.
	Default         FieldValue
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

// DownloadOptions says how to fetch an attachment. From is where to start,
// which is what makes a resumed download resume rather than start again: the
// caller already has that many bytes on disk and the endpoint honours Range.
type DownloadOptions struct {
	From     int64
	Progress func(written int64)
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

// Ordering is how a board decides the order of the issues in a column.
type Ordering int

// The board orderings. A board that ranks has a rank field; one that does not
// is showing whatever its filter's ORDER BY produced.
const (
	// OrderFilter means the board has no rank field and the order is whatever
	// the board's saved filter sorts by — priority, created, anything.
	OrderFilter Ordering = iota
	// OrderRank means the board exposes a rank field and rows can be reordered.
	OrderRank
)

// BoardConfig is everything about a board that must be read rather than
// assumed: which statuses make up which column, what the board estimates in,
// and which custom field holds rank.
//
// Almost all of it is optional, and not in the "unset" sense — a Kanban board
// sends no estimation object at all, and a board that is ordered by priority
// sends no rank field. Every consumer feature-detects: estimation, ranking and
// a sub-query are things a board may simply not have.
type BoardConfig struct {
	BoardID int64
	Name    string
	Type    BoardType
	Columns []Column
	// Estimation is nil when the board does not estimate. That is different from
	// EstimationNone, which is a Scrum board that has turned estimation off.
	Estimation *Estimation
	// RankFieldID is empty when the board has no rank field, which means its
	// order comes from its filter and rows cannot be reordered.
	RankFieldID string
	// FilterID is the saved filter behind the board. When there is no rank
	// field, this filter's ORDER BY is the board's order, and reading it takes
	// a separate call.
	FilterID string
	// SubQuery is the Kanban-only extra condition that decides which resolved
	// issues still show. It is empty on a Scrum board.
	SubQuery string
}

// Ordering reports how this board's columns are ordered.
func (c BoardConfig) Ordering() Ordering {
	if c.RankFieldID != "" {
		return OrderRank
	}
	return OrderFilter
}

// Estimates reports whether the board measures issues at all.
func (c BoardConfig) Estimates() bool {
	return c.Estimation != nil && c.Estimation.Type == EstimationField && c.Estimation.Field.ID != ""
}

// BoardQuery narrows a read of a board's issues.
//
// Fields is not optional, for the reason Query.Fields is not: asked for no
// field, the Agile issue endpoints answer with every navigable and Agile field
// the site has, which on a site with ninety custom fields is ninety values per
// card that draws six. A read naming none is refused rather than sent.
//
// SubQuery is the board's own extra condition, taken from BoardConfig.SubQuery.
// It is on the query rather than applied inside the port because the caller has
// already read the configuration and the port would have to read it again; it is
// on the query at all because the endpoint does not apply it. A Kanban board
// whose sub-query hides long-resolved work answers more issues than the board
// draws, and the difference is silent. Anything other than that board's own
// sub-query narrows the board to a set no board shows.
//
// There is deliberately no further narrowing invented here. QuickFilters is not
// an exception to that: each entry is JQL a caller read from QuickFilters(ctx,
// boardID) rather than composed itself, so what it narrows to is a board state
// the site's own board draws too — toggling the same quick filter there. A
// caller that wants a subset of the board this port cannot name filters the
// rows it was given instead.
type BoardQuery struct {
	Fields   []string
	SubQuery string
	// QuickFilters are the JQL of whichever of the board's own quick filters are
	// toggled on, each one ANDed onto SubQuery the way the board's own UI ANDs
	// them. Every entry has to be a JQL string this call's QuickFilters read
	// back, never one composed by the caller — see the type doc.
	QuickFilters []string
	// MaxResults is how many issues one page asks for. Zero leaves the length
	// to the adapter.
	MaxResults int
}

// QuickFilter is one of a board's own quick filters: a JQL fragment the site
// offers to AND onto what the board already shows, toggled rather than
// replacing it — "Only My Issues", say, or "Recently Updated". It is read
// through QuickFilters(ctx, boardID) and passed back through
// BoardQuery.QuickFilters; nothing here is a claim it still exists, since a
// board's quick filters are edited independently of everything else about it.
type QuickFilter struct {
	ID   int64
	Name string
	// JQL is the fragment this quick filter ANDs onto a board read. It is
	// opaque: there is no engine in this package that could evaluate it, which
	// is why it goes back to the site rather than being matched locally.
	JQL string
	// Description is the site's own text for what the filter narrows to, and is
	// empty when nobody wrote one.
	Description string
	// Position is the order the board draws its quick filters in.
	Position int
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
	TaskEnqueued TaskState = "ENQUEUED"
	TaskRunning  TaskState = "RUNNING"
	TaskComplete TaskState = "COMPLETE"
	TaskFailed   TaskState = "FAILED"
	// Not a stopped task: a switch with no case for it reports a running task
	// as finished.
	TaskCancelRequested TaskState = "CANCEL_REQUESTED"
	TaskCancelled       TaskState = "CANCELLED"
	TaskDead            TaskState = "DEAD"
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
	// Failed identifies the issues the task could not move, when it reports
	// them. These are ids and not keys: the queue answers
	// failedAccessibleIssues keyed by numeric issue id and carries nothing that
	// turns one back into a key.
	Failed []string
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

// The two wildcards Query.Fields understands. Either asks for every field the
// site has, on every issue in the result set, which is the expensive mistake
// Query.Fields exists to avoid.
const (
	FieldsAll       = "*all"
	FieldsNavigable = "*navigable"
)

// FieldMask is the set of field IDs an issue was read with.
//
// A narrow read is otherwise indistinguishable from an empty one: Assignee is
// nil both for an unassigned issue and for a search whose Query.Fields never
// named the assignee. Anything that merges, caches or writes an issue back has
// to know which of those two it is holding, or a refresh that never asked about
// the assignee unassigns the issue.
//
// The zero mask asked for nothing, which is the honest answer for an issue that
// did not come from a read, and the answer that refuses a write rather than
// permitting one.
//
// It is immutable for the reason FieldSet is: a mask travels by value into an
// Issue, into the cache and back out, and a collection that could be written
// through would mean seeding an edit form from a cached issue rewrites the
// cached issue.
type FieldMask struct {
	ids []string
	all bool
}

// NewFieldMask builds a mask from the field IDs a read asked for, dropping
// blanks and repeats and sorting what is left. Either wildcard makes the whole
// mask wide, because that is what the endpoint does with it.
func NewFieldMask(ids []string) FieldMask {
	out := make([]string, 0, len(ids))
	for _, id := range ids {
		trimmed := strings.TrimSpace(id)
		switch {
		case trimmed == "":
		case trimmed == FieldsAll || trimmed == FieldsNavigable:
			return FieldMask{all: true}
		case !slices.Contains(out, trimmed):
			out = append(out, trimmed)
		}
	}
	if len(out) == 0 {
		return FieldMask{}
	}
	slices.Sort(out)
	return FieldMask{ids: out}
}

// AllFields is the mask of a read that asked for everything the site holds.
func AllFields() FieldMask { return FieldMask{all: true} }

// Has reports whether the read asked for a field. When it did, that field being
// absent from the issue means the site had nothing to send for it.
func (m FieldMask) Has(id string) bool { return m.all || slices.Contains(m.ids, id) }

// Wide reports whether the read asked for every field the site has.
func (m FieldMask) Wide() bool { return m.all }

// IDs returns the field IDs the read named, sorted. A wide mask names none:
// what it asked for is the site's list, which no client can enumerate.
func (m FieldMask) IDs() []string { return slices.Clone(m.ids) }

// Len reports how many field IDs the mask names. That is zero for a wide mask
// as well as for the zero one, so Wide is what tells those two apart.
func (m FieldMask) Len() int { return len(m.ids) }

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
	// CapPeople is whether this token may look accounts up at all. Browse users
	// and groups is a site-wide permission a perfectly ordinary token can lack,
	// and without it a person can only be named by an account id somebody
	// already has.
	CapPeople CapabilityKey = "people"
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
	People       Capability
	Graphics     GraphicsMode
	TimeZone     *time.Location
	// TimeZoneReason is why TimeZone is not the account's own, worded the way a
	// Capability.Reason is and empty exactly when TimeZone is set. Dates render
	// in UTC either way, so this sentence is the only thing that can tell a user
	// their timestamps are an hour out.
	TimeZoneReason string
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
	case CapPeople:
		return c.People
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

// Zone returns the timezone to render dates in and, when that is not the
// account's own, the reason it is not. The reason is empty exactly when the
// zone is the account's.
func (c Capabilities) Zone() (zone *time.Location, reason string) {
	if c.TimeZone == nil {
		return time.UTC, c.TimeZoneReason
	}
	return c.TimeZone, ""
}
