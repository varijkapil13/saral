package app

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/varijkapil13/saral/pkg/jira"
)

// The names the cascade looks a field up by. They are names and not IDs on
// purpose: a custom field's ID differs on every site, so it is resolved against
// the site's catalogue at runtime and none of these is ever written down as a
// customfield_NNNNN. Two of them belong to Advanced Roadmaps and exist on no
// site without it, which is a fall-through and not a failure.
const (
	targetStartName = "Target start"
	targetEndName   = "Target end"
	startDateName   = "Start date"
	sprintFieldName = "Sprint"
)

// The platform field IDs the cascade reads. Unlike a custom field's, these are
// the same on every site, so they can be named here.
const (
	dueDateFieldID     = "duedate"
	createdFieldID     = "created"
	fixVersionsFieldID = "fixVersions"
)

// The platform field IDs the rollup reads. They carry no date, so a read that
// asked for these and nothing else is a read that asked for no date at all —
// but a read that does not name them comes back with Parent nil and Subtasks
// empty however the issues are related, so rule 7 needs them in the field list.
const (
	parentFieldID   = "parent"
	subtasksFieldID = "subtasks"
)

// Provenance is which rule of the date cascade produced a range.
//
// The timeline draws by it — rule 5 is a faded bar, a lone date is a diamond
// rather than a bar, a rollup is neither — and it is what makes a bar in the
// wrong place diagnosable without a second read of the issue.
type Provenance int

// The ways a range comes about, in the order the cascade tries them. Rules 1 to
// 6 are the table in docs/ROADMAP.md; the rollup is the seventh, and is a range
// no field on the issue carries.
const (
	// FromNothing is an issue no rule found a date for. Range.Absent says which
	// kind of nothing it was.
	FromNothing Provenance = iota
	// FromConfiguredFields is rule 1: the fields named in [profiles.x.timeline].
	FromConfiguredFields
	// FromTargetDates is rule 2: Advanced Roadmaps' Target start and Target end.
	FromTargetDates
	// FromStartAndDue is rule 3: the Start date custom field and duedate, which
	// is the shape of a site without Advanced Roadmaps.
	FromStartAndDue
	// FromSprint is rule 4: the dates of the sprint the issue is in now.
	FromSprint
	// FromCreatedAndRelease is rule 5: created, to the release date of a fix
	// version. Neither is a date anybody set for this issue, which is why a bar
	// drawn from them is faded.
	FromCreatedAndRelease
	// FromOneDate is rule 6: one date with nothing to pair it with, which is a
	// milestone and not a bar.
	FromOneDate
	// FromChildren is a parent with no dates of its own, spanning the children
	// that have some.
	FromChildren
)

// Rule is the cascade rule that fired, numbered as docs/ROADMAP.md numbers
// them. A rollup is 7, being the seventh way a range comes about, and an
// unresolved issue is 0.
func (p Provenance) Rule() int { return int(p) }

// String says where the dates came from, in words a footer can show.
func (p Provenance) String() string {
	switch p {
	case FromConfiguredFields:
		return "the fields this profile names"
	case FromTargetDates:
		return "target start and target end"
	case FromStartAndDue:
		return "the start date and the due date"
	case FromSprint:
		return "the sprint's own dates"
	case FromCreatedAndRelease:
		return "created and a release date"
	case FromOneDate:
		return "the one date there is"
	case FromChildren:
		return "its children"
	default:
		return "nothing"
	}
}

// OK reports whether the cascade resolved anything at all.
func (p Provenance) OK() bool { return p != FromNothing }

// Bar reports whether this provenance draws as a bar between two dates.
func (p Provenance) Bar() bool { return p.OK() && p != FromOneDate }

// Faded reports whether the bar is a guess rather than dates somebody set, which
// rule 5 is: it pairs the day the issue was filed with the day a release is due.
func (p Provenance) Faded() bool { return p == FromCreatedAndRelease }

// Milestone reports whether the range is a single date, which draws as a diamond
// and not as a bar of one day.
func (p Provenance) Milestone() bool { return p == FromOneDate }

// Rollup reports whether the range came from the issue's children rather than
// from the issue, which is drawn distinctly because moving it moves nothing.
func (p Provenance) Rollup() bool { return p == FromChildren }

// Absence is why the cascade found no date, which is not one answer.
//
// A field nothing asked for, a field spelled wrongly and a field this site does
// not have are indistinguishable on the issue that comes back — jira.FieldMask
// records what was asked for, never what the site had. So the most a bar with no
// dates can say is which side of that line it is on, and saying "no dates" when
// the read never asked for any is how an hour goes missing.
type Absence int

// The kinds of nothing.
const (
	// AbsentNothing is a range that resolved.
	AbsentNothing Absence = iota
	// AbsentNotAsked is a read that asked for no field the cascade can date an
	// issue from, so the issue has nothing to say either way.
	AbsentNotAsked
	// AbsentEmpty is a read that asked and got nothing back.
	AbsentEmpty
)

// String says which kind of nothing this is, in words a footer can show.
func (a Absence) String() string {
	switch a {
	case AbsentNotAsked:
		return "this read asked for none of the fields a date could come from"
	case AbsentEmpty:
		return "requested and absent: the date fields were asked for and this issue carries none of them"
	default:
		return ""
	}
}

// Range is one issue's place on the timeline, and where that came from.
//
// A milestone carries the same date at both ends, so that a caller laying out a
// bar needs no special case for it; From.Milestone is what says to draw a
// diamond instead. Both dates are calendar dates rather than instants, because a
// day is what a timeline is ruled in and an instant bucketed in the wrong zone
// lands on the wrong day.
type Range struct {
	Start jira.Date
	End   jira.Date
	// From is the rule that produced this range.
	From Provenance
	// Source names the things the dates came out of — the fields, the sprint,
	// the version, the count of children — so that a bar in the wrong place can
	// be traced back without reading the issue again.
	Source string
	// Absent says why nothing resolved, and is AbsentNothing on a range that
	// did.
	Absent Absence
}

// OK reports whether the cascade resolved this range.
func (r Range) OK() bool { return r.From.OK() }

// Point reports whether the range covers a single day, which every milestone
// does and a one-day bar also can.
func (r Range) Point() bool { return !r.Start.IsZero() && r.Start == r.End }

// Backwards reports whether the range ends before it starts, which is data
// nobody can draw: two configured fields filled in the wrong order, or a sprint
// somebody moved. A caller draws it as a point or refuses to draw it, and
// Resolution.Warnings names the issue either way.
func (r Range) Backwards() bool {
	return r.OK() && !r.Start.IsZero() && !r.End.IsZero() && r.End.Before(r.Start)
}

// FieldProblem is a field name the cascade could not turn into one field on this
// site.
type FieldProblem struct {
	// Name is the name as it was written down.
	Name string
	// Configured is true when the name came out of a profile rather than out of
	// the cascade's own list, which is the difference between somebody's typo
	// and a field this site simply does not have.
	Configured bool
	// Err is the port's own account of it, always a *jira.FieldNameError.
	Err error
}

// Ambiguous reports whether the name belongs to more than one field, which is a
// name to say differently rather than a name to correct.
func (p FieldProblem) Ambiguous() bool {
	var unresolved *jira.FieldNameError
	return errors.As(p.Err, &unresolved) && unresolved.Ambiguous()
}

// String is the problem in one line, saying whether the name was configured
// here or is one of the cascade's own.
func (p FieldProblem) String() string {
	if p.Err == nil {
		return p.Name
	}
	if p.Configured {
		return "this profile's timeline: " + p.Err.Error()
	}
	return p.Err.Error()
}

// DateFields are the fields the cascade reads, resolved against one site's
// catalogue once.
//
// Resolving is separate from resolving dates because it is per site and not per
// issue, and because what it produces is also the field list to fetch with:
// Projection asks for exactly these and nothing else.
//
// A name that resolves to no field is normal — Target start exists only with
// Advanced Roadmaps, Start date only where somebody added it — and a name that
// resolves to two is not, because reading either of them reads a field nobody
// named. Both are kept in Problems; neither stops the cascade, which falls
// through to the next rule.
type DateFields struct {
	starts      []jira.FieldRef
	ends        []jira.FieldRef
	targetStart jira.FieldRef
	targetEnd   jira.FieldRef
	startDate   jira.FieldRef
	sprint      jira.FieldRef
	ids         []string
	dated       []string
	problems    []FieldProblem
}

// ResolveDateFields resolves every field the cascade reads against the site's
// catalogue: the ones this profile named for rule 1, and the cascade's own for
// rules 2 to 4.
//
// Names are matched by jira.ResolveField, which compares a folded, separator-
// stripped spelling as well as the two the site sends. That is what carries a
// name copied off an English site onto a translated one — and it is still not
// portable, because untranslatedName is a third spelling of a field's name and
// only custom fields have one. Configuration that holds an ID resolved once
// travels; a name does not.
func ResolveDateFields(catalogue []jira.Field, configuredStart, configuredEnd []string) DateFields {
	var out DateFields
	out.starts = out.resolveAll(catalogue, configuredStart)
	out.ends = out.resolveAll(catalogue, configuredEnd)
	out.targetStart = out.resolveOne(catalogue, targetStartName)
	out.targetEnd = out.resolveOne(catalogue, targetEndName)
	out.startDate = out.resolveOne(catalogue, startDateName)
	out.sprint = out.resolveOne(catalogue, sprintFieldName)

	ids := make([]string, 0, len(out.starts)+len(out.ends)+8)
	for _, ref := range out.starts {
		ids = appendUnique(ids, ref.ID)
	}
	for _, ref := range out.ends {
		ids = appendUnique(ids, ref.ID)
	}
	for _, ref := range []jira.FieldRef{out.targetStart, out.targetEnd, out.startDate, out.sprint} {
		ids = appendUnique(ids, ref.ID)
	}
	// A profile can name one of these: end = "Due date" is the likeliest rule 1.
	for _, id := range []string{dueDateFieldID, createdFieldID, fixVersionsFieldID} {
		ids = appendUnique(ids, id)
	}
	out.dated = slices.Clone(ids)
	for _, id := range []string{parentFieldID, subtasksFieldID} {
		ids = appendUnique(ids, id)
	}
	out.ids = ids
	return out
}

func (f *DateFields) resolveAll(catalogue []jira.Field, names []string) []jira.FieldRef {
	out := make([]jira.FieldRef, 0, len(names))
	for _, name := range names {
		if strings.TrimSpace(name) == "" {
			continue
		}
		field, err := jira.ResolveField(catalogue, name)
		if err != nil {
			f.problems = append(f.problems, FieldProblem{Name: name, Configured: true, Err: err})
			continue
		}
		out = append(out, field.Ref())
	}
	return out
}

func (f *DateFields) resolveOne(catalogue []jira.Field, name string) jira.FieldRef {
	field, err := jira.ResolveField(catalogue, name)
	if err != nil {
		f.problems = append(f.problems, FieldProblem{Name: name, Err: err})
		return jira.FieldRef{}
	}
	return field.Ref()
}

// IDs are the field IDs a read has to ask for so the cascade has something to
// work on, in a stable order. A field this site does not have is not in it.
//
// parent and subtasks are in it and carry no date: rule 7 rolls a parent up over
// its children, and an issue read without them names neither.
func (f DateFields) IDs() []string { return slices.Clone(f.ids) }

// Projection is the narrow field set a timeline fetches with. Asking for these
// and nothing else is the difference between six values a row and sixty.
func (f DateFields) Projection() Projection {
	return Projection{Name: "timeline", IDs: f.IDs()}
}

// Problems are the names that did not resolve to one field on this site, in the
// order they were resolved.
func (f DateFields) Problems() []FieldProblem { return slices.Clone(f.problems) }

// Missing names the fields this profile asked for that this site has none of, so
// that a view can say so rather than draw an empty timeline. A name that matched
// several fields is not missing — see Problems.
func (f DateFields) Missing() []string {
	out := make([]string, 0, len(f.problems))
	for _, problem := range f.problems {
		if problem.Configured && !problem.Ambiguous() {
			out = append(out, problem.Name)
		}
	}
	return out
}

// unusable is the problems a resolution warns about, which is not all of them.
// A cascade name this site has no field for is the ordinary shape of a site
// without Advanced Roadmaps and would warn on every pass. A name belonging to
// two fields is not: the rule that reads it silently never fires, on a site
// where a plugin has added a second field called Sprint or Start date, and the
// emptiest timeline is the one with nothing to say about why.
func (f DateFields) unusable() []string {
	var out []string
	for _, problem := range f.problems {
		if !problem.Configured && !problem.Ambiguous() {
			continue
		}
		out = append(out, problem.String()+"; the cascade fell through the rule that reads it")
	}
	return out
}

// SprintDates is the one thing rule 4 needs from Jira: a sprint's own start and
// end, which the sprint value on an issue does not carry.
//
// It is one method rather than the whole jira.SprintReader role because it is
// all the cascade calls, and a test needs a dozen lines to stand one up. Any
// jira.SprintReader satisfies it.
type SprintDates interface {
	Sprint(ctx context.Context, id int64) (jira.Sprint, error)
}

var _ SprintDates = jira.SprintReader(nil)

// Dates resolves the timeline cascade: given the fields resolved for this site
// and a set of issues, which start and end each issue draws between, and which
// rule said so.
//
// It is pure apart from the sprint reader it is handed. Nothing else about it
// reaches anywhere: the clock and the timezone are given, the field catalogue is
// resolved before it is built, and the issues are the ones the caller already
// has. A Dates is safe to share between goroutines.
type Dates struct {
	fields  DateFields
	sprints SprintDates
	zone    *time.Location
	reason  string
	now     func() time.Time

	flight flights
}

// DatesOption configures the cascade at construction.
type DatesOption func(*Dates)

// WithSprints hands the cascade the reader rule 4 needs. Without one, an issue
// in a sprint falls through to rule 5 and the resolution says so.
func WithSprints(sprints SprintDates) DatesOption {
	return func(d *Dates) { d.sprints = sprints }
}

// WithZone sets the timezone instants are bucketed into days in, and the reason
// it is not the account's own when it is not — which is exactly the pair
// jira.Capabilities.Zone returns, so a caller writes WithZone(caps.Zone()).
//
// A sprint boundary arrives as an instant normalised to UTC and a created
// timestamp in the site's own offset. Bucketing either in the wrong zone moves
// its bar by a day.
func WithZone(zone *time.Location, reason string) DatesOption {
	return func(d *Dates) { d.zone, d.reason = zone, reason }
}

// WithNow sets the clock the pass is dated by, which is what makes the today
// marker and the bars agree and what makes the whole thing testable.
func WithNow(now func() time.Time) DatesOption {
	return func(d *Dates) { d.now = now }
}

// NewDates builds the cascade over the fields resolved for one site.
func NewDates(fields DateFields, opts ...DatesOption) *Dates {
	d := &Dates{fields: fields, zone: time.UTC, now: time.Now}
	for _, o := range opts {
		if o != nil {
			o(d)
		}
	}
	if d.zone == nil {
		d.zone = time.UTC
	}
	if d.now == nil {
		d.now = time.Now
	}
	return d
}

// Fields returns the resolved fields the cascade reads, which is also the field
// list to fetch with.
func (d *Dates) Fields() DateFields { return d.fields }

// Resolution is one pass of the cascade: a range per issue, the zone and the
// day the pass ran, and whatever could not be worked out.
//
// It is immutable and travels by value, for the reason jira.FieldSet does: the
// view that draws it and the use case that made it are looking at the same
// ranges.
type Resolution struct {
	ranges     map[string]Range
	keys       []string
	at         time.Time
	today      jira.Date
	zone       *time.Location
	reason     string
	warnings   []string
	spanStart  jira.Date
	spanEnd    jira.Date
	resolvedNo int
}

// Range returns the range resolved for an issue key.
func (r Resolution) Range(key string) (Range, bool) {
	got, ok := r.ranges[key]
	return got, ok
}

// Keys are the issue keys this pass covered, sorted, so that iteration over a
// resolution is stable.
func (r Resolution) Keys() []string { return slices.Clone(r.keys) }

// Len reports how many issues the pass covered.
func (r Resolution) Len() int { return len(r.ranges) }

// Resolved reports how many of them got a range.
func (r Resolution) Resolved() int { return r.resolvedNo }

// At is the instant the pass ran, from the clock it was given. A today marker
// drawn from anything else can disagree with the bars beside it.
func (r Resolution) At() time.Time { return r.at }

// Today is the day the pass ran, in the zone the dates are bucketed in.
func (r Resolution) Today() jira.Date { return r.today }

// Zone is the timezone the dates were bucketed in and, when that is not the
// account's own, the reason it is not. The reason is a local failure to load a
// zone, never Jira refusing anything.
func (r Resolution) Zone() (zone *time.Location, reason string) {
	if r.zone == nil {
		return time.UTC, r.reason
	}
	return r.zone, r.reason
}

// Span is the first and last day anything in this pass touches, which is the
// extent an axis has to cover. Both are zero when nothing resolved.
func (r Resolution) Span() (start, end jira.Date) { return r.spanStart, r.spanEnd }

// Warnings are the things that went wrong without stopping the pass, in a stable
// order: a zone that would not load, a field name that means two fields on this
// site, a sprint that could not be read, an issue whose dates are the wrong way
// round, a parent chain that loops. A view shows them; nothing in here depends
// on them.
func (r Resolution) Warnings() []string { return slices.Clone(r.warnings) }

// Resolve runs the cascade over a set of issues.
//
// Rules 1 to 5 are tried in order and the first that yields both a start and an
// end wins. A rule that yields only one date does not win, but the first lone
// date any rule found becomes a milestone under rule 6 — so an issue with a due
// date and nothing else diamonds on its due date rather than on the day it was
// created. A parent that got nothing of its own then spans its children.
//
// The only call it makes is one per distinct sprint, coalesced: resolving two
// hundred issues in the same sprint is one request. A sprint that cannot be read
// is a warning and a fall-through to the next rule, not a failed pass — one
// board a token cannot see must not empty a timeline. A cancelled context does
// stop it.
//
// An issue with no key cannot be drawn and is skipped; two issues under one key
// are one issue, the last of them.
func (d *Dates) Resolve(ctx context.Context, issues []jira.Issue) (Resolution, error) {
	if err := ctx.Err(); err != nil {
		return Resolution{}, err
	}
	now := d.now()
	out := Resolution{
		ranges: make(map[string]Range, len(issues)),
		keys:   make([]string, 0, len(issues)),
		at:     now,
		today:  jira.DateOf(now.In(d.zone)),
		zone:   d.zone,
		reason: d.reason,
	}
	if d.reason != "" {
		out.warnings = append(out.warnings, d.reason)
	}
	out.warnings = append(out.warnings, d.fields.unusable()...)

	run := &pass{
		fields:  d.fields,
		zone:    d.zone,
		dated:   d.fields.dated,
		out:     &out,
		onIssue: make(map[string][]sprintRef, len(issues)),
		parsed:  make(map[string][]sprintRef),
		sprints: make(map[int64]jira.Sprint),
	}

	ids := run.collect(issues)
	if err := d.readSprints(ctx, ids, run); err != nil {
		return Resolution{}, err
	}
	run.cascade(issues)
	run.rollups()
	run.finish()
	return out, nil
}

// readSprints fetches each distinct sprint once, in ascending id order so that
// the pass makes the same calls in the same order every time.
//
// Two passes running at once share a call rather than making two, which is what
// coalesce is for. A failure is recorded against the sprint and the pass carries
// on, because a 403 on one board is an answer about that board.
func (d *Dates) readSprints(ctx context.Context, ids []int64, run *pass) error {
	if len(ids) == 0 {
		return nil
	}
	if d.sprints == nil {
		run.warn(fmt.Sprintf("%d sprint(s) hold the dates for these issues and this session has nothing to read a sprint with", len(ids)))
		return nil
	}
	for _, id := range ids {
		sprint, err := coalesce(ctx, &d.flight, sprintFlightKey(id), func(ctx context.Context) (jira.Sprint, error) {
			return d.sprints.Sprint(ctx, id)
		})
		if err != nil {
			if ctxErr := ctx.Err(); ctxErr != nil {
				return ctxErr
			}
			reason, _ := jira.Reason(err)
			run.warn(fmt.Sprintf("sprint %d could not be read, so the issues in it fall back to a later rule: %s", id, reason))
			continue
		}
		run.sprints[id] = sprint
	}
	return nil
}

func sprintFlightKey(id int64) string {
	return "sprint\x00" + strconv.FormatInt(id, 10)
}

// sprintRef is a sprint as an issue names it. An issue's sprint value carries an
// id and a name and no dates at all, which is the whole reason rule 4 needs a
// reader.
type sprintRef struct {
	id   int64
	name string
}

// pass is the state of one resolution: what was read, what could not be, and
// the ranges as they are worked out.
type pass struct {
	fields  DateFields
	zone    *time.Location
	dated   []string
	out     *Resolution
	onIssue map[string][]sprintRef
	parsed  map[string][]sprintRef
	sprints map[int64]jira.Sprint
	kids    map[string][]string
	// looped is the keys already reported as being in a parent-chain cycle, so
	// that a three-issue cycle is one warning and not one per issue that leads
	// into it.
	looped map[string]bool
}

func (p *pass) warn(text string) { p.out.warnings = append(p.out.warnings, text) }

// collect reads the sprints named on every issue and returns the distinct ids,
// ascending. Reading them here rather than inside the cascade means an issue's
// sprint value is decoded once, and that the whole pass knows what it has to
// fetch before it fetches anything.
func (p *pass) collect(issues []jira.Issue) []int64 {
	var ids []int64
	for i := range issues {
		key := issues[i].Key
		if key == "" {
			continue
		}
		refs := p.sprintsOn(issues[i])
		if len(refs) == 0 {
			continue
		}
		p.onIssue[key] = refs
		for _, ref := range refs {
			if !slices.Contains(ids, ref.id) {
				ids = append(ids, ref.id)
			}
		}
	}
	slices.Sort(ids)
	return ids
}

// sprintsOn reads the sprints an issue is in.
//
// The value arrives in one of two shapes and the field's own type is neither. A
// read that sent no schema block decodes the array as options — an id and a name
// — and a read that did send one finds the sprint field declared as an array of
// json, which this client has no slot for, so the bytes are kept as text. A
// timeline asks for a custom field by name, which is what makes the site send a
// schema, so the second shape is the everyday one and both are read here.
func (p *pass) sprintsOn(iss jira.Issue) []sprintRef {
	ref := p.fields.sprint
	if ref.ID == "" {
		return nil
	}
	if options, ok := iss.Fields.Options(ref); ok {
		out := make([]sprintRef, 0, len(options))
		for _, option := range options {
			id, err := strconv.ParseInt(strings.TrimSpace(option.ID), 10, 64)
			if err != nil {
				continue
			}
			out = append(out, sprintRef{id: id, name: option.Label})
		}
		return out
	}
	text, ok := iss.Fields.Text(ref)
	if !ok || !strings.HasPrefix(strings.TrimSpace(text), "[") {
		return nil
	}
	if cached, ok := p.parsed[text]; ok {
		return cached
	}
	var wire []struct {
		ID   int64  `json:"id"`
		Name string `json:"name"`
	}
	var out []sprintRef
	if err := json.Unmarshal([]byte(text), &wire); err == nil {
		for _, one := range wire {
			if one.ID == 0 {
				continue
			}
			out = append(out, sprintRef{id: one.ID, name: one.Name})
		}
	}
	p.parsed[text] = out
	return out
}

// cascade resolves every issue against rules 1 to 6 and records the parent
// edges the rollup needs.
func (p *pass) cascade(issues []jira.Issue) {
	p.kids = make(map[string][]string, len(issues))
	for i := range issues {
		key := issues[i].Key
		if key == "" {
			continue
		}
		if _, seen := p.out.ranges[key]; !seen {
			p.out.keys = append(p.out.keys, key)
		}
		p.out.ranges[key] = p.rangeOf(issues[i])
		p.edges(issues[i])
	}
	slices.Sort(p.out.keys)
	for parent := range p.kids {
		slices.Sort(p.kids[parent])
	}
}

// edges records the parent-child links this pass can see, from both ends: an
// issue names its parent, and a parent read wide enough names its subtasks. A
// link to an issue outside the pass is not a link — there is nothing to roll up
// from.
func (p *pass) edges(iss jira.Issue) {
	if iss.Parent != nil {
		p.link(iss.Parent.Key, iss.Key)
	}
	for i := range iss.Subtasks {
		p.link(iss.Key, iss.Subtasks[i].Key)
	}
}

func (p *pass) link(parent, child string) {
	if parent == "" || child == "" || parent == child {
		return
	}
	if !slices.Contains(p.kids[parent], child) {
		p.kids[parent] = append(p.kids[parent], child)
	}
}

// edge is one end of a range and the thing it came out of.
type edge struct {
	date   jira.Date
	source string
}

func (e edge) set() bool { return !e.date.IsZero() }

// rangeOf runs the cascade over one issue: the first rule with both ends wins,
// and the first rule with one end supplies the milestone if none of them has
// both.
func (p *pass) rangeOf(iss jira.Issue) Range {
	var lone edge

	start, end := p.configured(iss)
	if out, ok := paired(&lone, FromConfiguredFields, start, end); ok {
		return out
	}
	start, end = p.target(iss)
	if out, ok := paired(&lone, FromTargetDates, start, end); ok {
		return out
	}
	start, end = p.startAndDue(iss)
	if out, ok := paired(&lone, FromStartAndDue, start, end); ok {
		return out
	}
	start, end = p.sprint(iss)
	if out, ok := paired(&lone, FromSprint, start, end); ok {
		return out
	}
	start, end = p.createdAndRelease(iss)
	if out, ok := paired(&lone, FromCreatedAndRelease, start, end); ok {
		return out
	}

	if lone.set() {
		return Range{Start: lone.date, End: lone.date, From: FromOneDate, Source: lone.source}
	}
	return Range{From: FromNothing, Absent: p.absence(iss)}
}

// paired reports whether one rule of the cascade produced a range, and keeps the
// first lone date any rule found for rule 6 when none of them does.
func paired(lone *edge, from Provenance, start, end edge) (Range, bool) {
	if start.set() && end.set() {
		return Range{
			Start:  start.date,
			End:    end.date,
			From:   from,
			Source: start.source + " to " + end.source,
		}, true
	}
	if !lone.set() {
		switch {
		case start.set():
			*lone = start
		case end.set():
			*lone = end
		}
	}
	return Range{}, false
}

// configured is rule 1: the fields this profile named, in the order it named
// them, first one with a value winning. Explicit configuration beats everything
// below it, which is the point of having it.
func (p *pass) configured(iss jira.Issue) (start, end edge) {
	return p.firstOf(iss, p.fields.starts), p.firstOf(iss, p.fields.ends)
}

func (p *pass) firstOf(iss jira.Issue, refs []jira.FieldRef) edge {
	for _, ref := range refs {
		if date, ok := p.dateOf(iss, ref); ok {
			return edge{date: date, source: sourceOf(ref)}
		}
	}
	return edge{}
}

// target is rule 2: Advanced Roadmaps' own two date fields, which exist on a
// Premium site and nowhere else.
func (p *pass) target(iss jira.Issue) (start, end edge) {
	return p.oneField(iss, p.fields.targetStart), p.oneField(iss, p.fields.targetEnd)
}

// startAndDue is rule 3: a Start date custom field somebody added, against the
// platform's own due date. It is the common shape of a site without Advanced
// Roadmaps.
func (p *pass) startAndDue(iss jira.Issue) (start, end edge) {
	start = p.oneField(iss, p.fields.startDate)
	if !iss.Due.IsZero() {
		end = edge{date: iss.Due, source: dueDateFieldID}
	}
	return start, end
}

// sprint is rule 4: the dates of the sprint the issue is in.
//
// An issue carries every sprint it has ever been in, in the order it joined
// them, so the last one is where it is now — and the walk runs backwards from
// there so that an issue dragged out of a dateless future sprint still draws
// against the sprint it was actually in.
func (p *pass) sprint(iss jira.Issue) (start, end edge) {
	refs := p.onIssue[iss.Key]
	for i := len(refs) - 1; i >= 0; i-- {
		sprint, ok := p.sprints[refs[i].id]
		if !ok {
			continue
		}
		name := sprintName(refs[i], sprint)
		from, to := edge{}, edge{}
		if sprint.Start != nil && !sprint.Start.IsZero() {
			from = edge{date: jira.DateOf(sprint.Start.In(p.zone)), source: name}
		}
		if sprint.End != nil && !sprint.End.IsZero() {
			to = edge{date: jira.DateOf(sprint.End.In(p.zone)), source: name}
		}
		if from.set() && to.set() {
			return from, to
		}
		if !start.set() && !end.set() {
			start, end = from, to
		}
	}
	return start, end
}

func sprintName(ref sprintRef, sprint jira.Sprint) string {
	switch {
	case sprint.Name != "":
		return sprint.Name
	case ref.name != "":
		return ref.name
	default:
		return "sprint " + strconv.FormatInt(ref.id, 10)
	}
}

// createdAndRelease is rule 5: the day the issue was filed, to the day the first
// release it is on is due. Neither is a date anybody chose for this issue, which
// is why the bar it makes is faded rather than solid.
func (p *pass) createdAndRelease(iss jira.Issue) (start, end edge) {
	if !iss.Created.IsZero() {
		start = edge{date: jira.DateOf(iss.Created.In(p.zone)), source: createdFieldID}
	}
	for i := range iss.FixVersions {
		date := iss.FixVersions[i].ReleaseDate
		if date.IsZero() || (end.set() && !date.Before(end.date)) {
			continue
		}
		end = edge{date: date, source: versionName(iss.FixVersions[i])}
	}
	return start, end
}

func versionName(version jira.Version) string {
	if version.Name != "" {
		return version.Name
	}
	return "a fix version"
}

func (p *pass) oneField(iss jira.Issue, ref jira.FieldRef) edge {
	if date, ok := p.dateOf(iss, ref); ok {
		return edge{date: date, source: sourceOf(ref)}
	}
	return edge{}
}

func sourceOf(ref jira.FieldRef) string {
	if ref.Name != "" {
		return ref.Name
	}
	return ref.ID
}

// dateOf reads a field's value as a calendar date.
//
// A date field already is one and is taken as it stands. A datetime is an
// instant, and which day an instant falls on is a question about a timezone, so
// it is bucketed in the account's zone and not this machine's. A field that
// arrived as text — one the site declares as a string, holding a date somebody
// types in — is parsed, because the alternative is a bar that is missing for no
// reason a user can see.
func (p *pass) dateOf(iss jira.Issue, ref jira.FieldRef) (jira.Date, bool) {
	if ref.ID == "" {
		return jira.Date{}, false
	}
	if at, ok := iss.Fields.Time(ref); ok {
		if at.IsZero() {
			return jira.Date{}, false
		}
		return jira.DateOf(at.In(p.zone)), true
	}
	if date, ok := iss.Fields.Date(ref); ok {
		return date, !date.IsZero()
	}
	if text, ok := iss.Fields.Text(ref); ok {
		return parseDateText(text, p.zone)
	}
	return p.platformDate(iss, ref.ID)
}

// platformDate reads a date that lands on the issue itself rather than in its
// field set. An adapter decodes duedate and created onto the struct, so a
// profile naming one of them for rule 1 — end = "Due date" is the likeliest
// rule 1 there is — finds nothing in the set and would fall through to a rule
// nobody configured.
func (p *pass) platformDate(iss jira.Issue, id string) (jira.Date, bool) {
	switch id {
	case dueDateFieldID:
		return iss.Due, !iss.Due.IsZero()
	case createdFieldID:
		if iss.Created.IsZero() {
			return jira.Date{}, false
		}
		return jira.DateOf(iss.Created.In(p.zone)), true
	default:
		return jira.Date{}, false
	}
}

// parseDateText reads a date out of a field that arrived as text. Anything that
// does not start like a date is not one, which keeps a whole sprint array off
// the parser.
func parseDateText(text string, zone *time.Location) (jira.Date, bool) {
	value := strings.TrimSpace(text)
	if len(value) < len(time.DateOnly) || value[0] < '0' || value[0] > '9' {
		return jira.Date{}, false
	}
	if len(value) == len(time.DateOnly) {
		date, err := jira.ParseDate(value)
		return date, err == nil
	}
	for _, layout := range dateTextLayouts {
		if at, err := time.Parse(layout, value); err == nil {
			return jira.DateOf(at.In(zone)), true
		}
	}
	return jira.Date{}, false
}

// The layouts a date-time reaches this package as text in. The first two are the
// platform's own and the Agile API's, neither of which is RFC 3339: the platform
// writes an offset with no colon. docs/API-NOTES.md has the third.
var dateTextLayouts = []string{
	"2006-01-02T15:04:05.000-0700",
	"2006-01-02T15:04:05.000-07:00",
	time.RFC3339,
}

// absence says which kind of nothing an unresolved issue is. The mask records
// what the read asked for and can say that much; it cannot tell a field that was
// empty from one spelled wrongly or one this site never had.
//
// Only the fields a date can come out of count: a read that asked for parent and
// nothing else asked for no date.
func (p *pass) absence(iss jira.Issue) Absence {
	for _, id := range p.dated {
		if iss.Requested.Has(id) {
			return AbsentEmpty
		}
	}
	return AbsentNotAsked
}

// rollups spans every issue that got nothing over the children that got
// something.
func (p *pass) rollups() {
	visiting := make(map[string]bool)
	for _, key := range p.out.keys {
		if p.out.ranges[key].OK() {
			continue
		}
		p.rollup(key, visiting)
	}
}

// rollup is the min and max of an issue's children, computed depth-first so that
// a parent whose children are themselves rollups spans its grandchildren.
//
// visiting is the chain being walked, so a parent that is somewhere in its own
// chain stops the walk with a warning instead of recursing until the stack ends.
// Jira will not make one; a cache holding two halves of a reparenting can.
func (p *pass) rollup(key string, visiting map[string]bool) Range {
	if got := p.out.ranges[key]; got.OK() {
		return got
	}
	if visiting[key] {
		if !p.looped[key] {
			p.warn(key + ": its parent chain leads back to itself, so nothing was rolled up onto it")
			if p.looped == nil {
				p.looped = make(map[string]bool, len(visiting)+1)
			}
			for inChain := range visiting {
				p.looped[inChain] = true
			}
		}
		return Range{}
	}
	visiting[key] = true
	defer delete(visiting, key)

	var (
		start, end jira.Date
		spanned    int
	)
	for _, child := range p.kids[key] {
		got := p.rollup(child, visiting)
		if !got.OK() {
			continue
		}
		spanned++
		start = earliest(earliest(start, got.Start), got.End)
		end = latest(latest(end, got.Start), got.End)
	}
	if spanned == 0 {
		return p.out.ranges[key]
	}
	out := Range{
		Start:  start,
		End:    end,
		From:   FromChildren,
		Source: fmt.Sprintf("%d of its children", spanned),
	}
	p.out.ranges[key] = out
	return out
}

// finish counts what resolved, measures the extent an axis has to cover, and
// names the issues whose dates are the wrong way round.
func (p *pass) finish() {
	for _, key := range p.out.keys {
		got := p.out.ranges[key]
		if !got.OK() {
			continue
		}
		p.out.resolvedNo++
		p.out.spanStart = earliest(earliest(p.out.spanStart, got.Start), got.End)
		p.out.spanEnd = latest(latest(p.out.spanEnd, got.Start), got.End)
		if got.Backwards() {
			p.warn(fmt.Sprintf("%s ends on %s and starts on %s, which is %s read in the order they are written", key, got.End, got.Start, got.Source))
		}
	}
}

func earliest(a, b jira.Date) jira.Date {
	switch {
	case b.IsZero():
		return a
	case a.IsZero() || b.Before(a):
		return b
	default:
		return a
	}
}

func latest(a, b jira.Date) jira.Date {
	switch {
	case b.IsZero():
		return a
	case a.IsZero() || a.Before(b):
		return b
	default:
		return a
	}
}
