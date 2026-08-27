package app

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/varijkapil13/saral/pkg/jira"
)

// The field IDs a site might have minted for the cascade's fields. They live in
// the test and not in the code: every one of them is reached by resolving a name
// against this catalogue, which is what a site-specific ID being here and
// nowhere else proves.
const (
	testTargetStartID = "customfield_20001"
	testTargetEndID   = "customfield_20002"
	testStartDateID   = "customfield_20003"
	testSprintID      = "customfield_20004"
	testKickoffID     = "customfield_20005"
	testHandoverID    = "customfield_20006"
)

var datesNow = time.Date(2026, time.March, 5, 9, 30, 0, 0, time.UTC)

func testClock() func() time.Time { return func() time.Time { return datesNow } }

func day(year int, month time.Month, d int) jira.Date {
	return jira.Date{Year: year, Month: month, Day: d}
}

func customDateField(id, name string) jira.Field {
	return jira.Field{
		ID: id, Key: id, Name: name, UntranslatedName: name,
		Custom: true, Navigable: true, Searchable: true, Orderable: true,
		ClauseNames: []string{name},
		Schema: jira.FieldSchema{
			Type:   "date",
			Custom: "com.atlassian.jira.plugin.system.customfieldtypes:datepicker",
		},
	}
}

// siteFields is a catalogue with everything the cascade looks for on it, plus
// the two system fields it reads and a pair somebody configured.
func siteFields() []jira.Field {
	return []jira.Field{
		{ID: dueDateFieldID, Key: dueDateFieldID, Name: "Due date", Navigable: true,
			Schema: jira.FieldSchema{Type: "date", System: dueDateFieldID}},
		{ID: createdFieldID, Key: createdFieldID, Name: "Created", Navigable: true,
			Schema: jira.FieldSchema{Type: "datetime", System: createdFieldID}},
		customDateField(testTargetStartID, targetStartName),
		customDateField(testTargetEndID, targetEndName),
		customDateField(testStartDateID, startDateName),
		{ID: testSprintID, Key: testSprintID, Name: sprintFieldName, UntranslatedName: sprintFieldName,
			Custom: true, Navigable: true, ClauseNames: []string{"Sprint"},
			Schema: jira.FieldSchema{Type: "array", Items: "json",
				Custom: "com.pyxis.greenhopper.jira:gh-sprint"}},
		customDateField(testKickoffID, "Kickoff"),
		customDateField(testHandoverID, "Handover"),
	}
}

// ref is the reference a value is stored under. Only the ID identifies a field,
// which is why a test can build one from an ID alone.
func ref(id string) jira.FieldRef { return jira.FieldRef{ID: id} }

type issueOpt func(*jira.Issue)

// anIssue is an issue read the way a timeline reads one: with every field the
// cascade can use asked for, so that an unresolved bar means the issue is empty
// rather than that nothing asked.
func anIssue(key string, opts ...issueOpt) jira.Issue {
	iss := jira.Issue{
		ID:        "10" + strings.TrimPrefix(key, "TL-"),
		Key:       key,
		Requested: jira.NewFieldMask(ResolveDateFields(siteFields(), nil, nil).IDs()),
	}
	for _, o := range opts {
		o(&iss)
	}
	return iss
}

func withDate(id string, date jira.Date) issueOpt {
	return func(iss *jira.Issue) {
		iss.Fields = iss.Fields.With(ref(id), jira.FieldValue{Kind: jira.KindDate, Date: date})
	}
}

func withInstant(id string, at time.Time) issueOpt {
	return func(iss *jira.Issue) {
		iss.Fields = iss.Fields.With(ref(id), jira.FieldValue{Kind: jira.KindTime, Time: at})
	}
}

func withText(id, text string) issueOpt {
	return func(iss *jira.Issue) {
		iss.Fields = iss.Fields.With(ref(id), jira.FieldValue{Kind: jira.KindText, Text: text})
	}
}

// withSprintOptions is the sprint value as a read that sent no schema block
// decodes it: an array of objects with an id and a name, inferred as options.
func withSprintOptions(options ...jira.Option) issueOpt {
	return func(iss *jira.Issue) {
		iss.Fields = iss.Fields.With(ref(testSprintID), jira.FieldValue{Kind: jira.KindOptions, Options: options})
	}
}

// withSprintJSON is the sprint value as a read that did send a schema block
// decodes it: the field is an array of json, which the client has no slot for,
// so the bytes are kept verbatim.
func withSprintJSON(raw string) issueOpt {
	return func(iss *jira.Issue) {
		iss.Fields = iss.Fields.With(ref(testSprintID), jira.FieldValue{Kind: jira.KindUnknown, Text: raw})
	}
}

func inSprint(id int64, name string) issueOpt {
	return withSprintOptions(jira.Option{ID: strconv.FormatInt(id, 10), Label: name})
}

func due(date jira.Date) issueOpt {
	return func(iss *jira.Issue) { iss.Due = date }
}

func created(at time.Time) issueOpt {
	return func(iss *jira.Issue) { iss.Created = at }
}

func fixVersion(name string, release jira.Date) issueOpt {
	return func(iss *jira.Issue) {
		iss.FixVersions = append(iss.FixVersions, jira.Version{ID: "v" + name, Name: name, ReleaseDate: release})
	}
}

func childOf(parent string) issueOpt {
	return func(iss *jira.Issue) { iss.Parent = &jira.IssueRef{Key: parent} }
}

func withSubtasks(keys ...string) issueOpt {
	return func(iss *jira.Issue) {
		for _, key := range keys {
			iss.Subtasks = append(iss.Subtasks, jira.IssueRef{Key: key})
		}
	}
}

// askedFor narrows the mask to exactly these IDs, which is how a read that did
// not ask for a date field is told from one that asked and got nothing.
func askedFor(ids ...string) issueOpt {
	return func(iss *jira.Issue) { iss.Requested = jira.NewFieldMask(ids) }
}

// fakeSprints is a sprint reader: what each sprint's dates are, what reading one
// fails with, and what was asked for. It is deliberately the whole dependency
// rule 4 has.
type fakeSprints struct {
	mu    sync.Mutex
	asked []int64

	dates map[int64]jira.Sprint
	fail  map[int64]error

	// arrived and release park a read so that two passes can be proved to share
	// one call. A parked handler nothing releases is a hang rather than a
	// failure, so release is always closed by the test that sets it.
	arrived chan struct{}
	release chan struct{}
	// cancel runs on the first read, which is how a caller that goes away
	// mid-pass is tested without a sleep.
	cancel func()
}

func (f *fakeSprints) Sprint(ctx context.Context, id int64) (jira.Sprint, error) {
	f.mu.Lock()
	f.asked = append(f.asked, id)
	first := len(f.asked) == 1
	f.mu.Unlock()

	if f.cancel != nil && first {
		f.cancel()
	}
	if f.arrived != nil {
		f.arrived <- struct{}{}
		select {
		case <-f.release:
		case <-ctx.Done():
			return jira.Sprint{}, ctx.Err()
		}
	}
	if err := ctx.Err(); err != nil {
		return jira.Sprint{}, err
	}
	if err := f.fail[id]; err != nil {
		return jira.Sprint{}, err
	}
	sprint, ok := f.dates[id]
	if !ok {
		return jira.Sprint{}, &jira.NotFoundError{Kind: "sprint", ID: strconv.FormatInt(id, 10)}
	}
	return sprint, nil
}

func (f *fakeSprints) calls() []int64 {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]int64(nil), f.asked...)
}

func sprintOn(id int64, name string, start, end time.Time) jira.Sprint {
	out := jira.Sprint{ID: id, Name: name, State: jira.SprintActive}
	if !start.IsZero() {
		out.Start = &start
	}
	if !end.IsZero() {
		out.End = &end
	}
	return out
}

// sprints is the reader with two sprints on it: one with both dates and one
// with only a start, which is a future sprint nobody has scheduled yet.
func sprints() *fakeSprints {
	return &fakeSprints{
		dates: map[int64]jira.Sprint{
			41: sprintOn(41, "Sprint 41",
				time.Date(2026, time.February, 2, 9, 0, 0, 0, time.UTC),
				time.Date(2026, time.February, 16, 9, 0, 0, 0, time.UTC)),
			42: sprintOn(42, "Sprint 42",
				time.Date(2026, time.March, 2, 9, 0, 0, 0, time.UTC),
				time.Date(2026, time.March, 16, 9, 0, 0, 0, time.UTC)),
			43: sprintOn(43, "Sprint 43",
				time.Date(2026, time.April, 1, 9, 0, 0, 0, time.UTC),
				time.Time{}),
		},
	}
}

// resolveOne runs the cascade over one issue and hands back its range.
func resolveOne(t *testing.T, d *Dates, iss jira.Issue) (Range, Resolution) {
	t.Helper()

	res, err := d.Resolve(t.Context(), []jira.Issue{iss})
	if err != nil {
		t.Fatalf("resolving %s: %v", iss.Key, err)
	}
	got, ok := res.Range(iss.Key)
	if !ok {
		t.Fatalf("the resolution holds no range for %s at all", iss.Key)
	}
	return got, res
}

func TestResolveDateFields_ResolvesEveryFieldTheCascadeReadsByName(t *testing.T) {
	t.Parallel()

	fields := ResolveDateFields(siteFields(), []string{"Kickoff"}, []string{"Handover"})

	if problems := fields.Problems(); len(problems) != 0 {
		t.Errorf("a catalogue with every field on it produced %d problem(s): %v", len(problems), problems)
	}
	want := []string{
		testKickoffID, testHandoverID,
		testTargetStartID, testTargetEndID, testStartDateID, testSprintID,
		dueDateFieldID, createdFieldID, fixVersionsFieldID,
	}
	if got := fields.IDs(); !equalStrings(got, want) {
		t.Errorf("the fields to fetch are %v, want %v", got, want)
	}
	projection := fields.Projection()
	if projection.Name != "timeline" || !equalStrings(projection.IDs, want) {
		t.Errorf("the projection is %+v, want the timeline's own narrow field set", projection)
	}
	if projection.Custom {
		t.Error("the timeline projection asks for every custom field on the site; it needs six")
	}
}

func TestResolveDateFields_SaysWhichNamesThisSiteCannotResolveAndWhy(t *testing.T) {
	t.Parallel()

	// A site with no Advanced Roadmaps, no Start date field, and two fields
	// somebody has called the same thing.
	catalogue := []jira.Field{
		{ID: dueDateFieldID, Key: dueDateFieldID, Name: "Due date"},
		customDateField("customfield_30001", "Delivery date"),
		customDateField("customfield_30002", "Delivery date"),
		customDateField("customfield_30003", "Kickoff"),
	}
	fields := ResolveDateFields(catalogue, []string{"Kickoff", "Delivery date", "Go live"}, nil)

	if got := fields.Missing(); !equalStrings(got, []string{"Go live"}) {
		t.Errorf("the names this site has no field for are %v, want only the one nobody minted", got)
	}
	byName := map[string]FieldProblem{}
	for _, problem := range fields.Problems() {
		byName[problem.Name] = problem
	}
	for _, tt := range []struct {
		name          string
		configured    bool
		wantAmbiguous bool
	}{
		{name: "Delivery date", configured: true, wantAmbiguous: true},
		{name: "Go live", configured: true},
		{name: targetStartName},
		{name: targetEndName},
		{name: startDateName},
		{name: sprintFieldName},
	} {
		problem, ok := byName[tt.name]
		if !ok {
			t.Errorf("%q did not resolve and no problem says so", tt.name)
			continue
		}
		if problem.Configured != tt.configured {
			t.Errorf("%q is reported as configured=%t, want %t", tt.name, problem.Configured, tt.configured)
		}
		if problem.Ambiguous() != tt.wantAmbiguous {
			t.Errorf("%q is reported as ambiguous=%t, want %t: %v", tt.name, problem.Ambiguous(), tt.wantAmbiguous, problem.Err)
		}
		if problem.String() == "" {
			t.Errorf("%q produced a problem with nothing to show for it", tt.name)
		}
	}
	// A name that resolved to two fields is not a name to correct, so it must
	// not be reported as one this site does not have.
	if got := fields.IDs(); len(got) != 4 {
		t.Errorf("the fields to fetch are %v, want the one configured field that resolved and the three platform IDs", got)
	}
}

// A display name copied off an English site onto a translated one is the case
// jira.ResolveField's folded pass exists for, and the cascade must go through it
// rather than comparing names itself.
func TestResolveDateFields_ResolvesAConfiguredNameOnATranslatedSite(t *testing.T) {
	t.Parallel()

	german := []jira.Field{
		{ID: "customfield_40001", Key: "customfield_40001", Name: "Startdatum",
			UntranslatedName: "TargetStart", Custom: true},
		{ID: "customfield_40002", Key: "customfield_40002", Name: "Enddatum",
			UntranslatedName: "TargetEnd", Custom: true},
	}
	fields := ResolveDateFields(german, nil, nil)

	if fields.targetStart.ID != "customfield_40001" || fields.targetEnd.ID != "customfield_40002" {
		t.Fatalf("Target start and Target end resolved to %q and %q on a German site, want the folded spelling to carry them",
			fields.targetStart.ID, fields.targetEnd.ID)
	}
	iss := anIssue("TL-1",
		withDate("customfield_40001", day(2026, time.March, 2)),
		withDate("customfield_40002", day(2026, time.March, 20)))
	got, _ := resolveOne(t, NewDates(fields, WithNow(testClock())), iss)
	if got.From != FromTargetDates {
		t.Errorf("the range came from %v, want rule 2 through the folded name", got.From)
	}
	if got.Source != "Startdatum to Enddatum" {
		t.Errorf("the source reads %q, want the names this site actually shows", got.Source)
	}
}

// The cascade is first-match-wins, and a rule only matches when it has both
// ends. Every rule, every fall-through and every absence is here, because the
// timeline view is built on top of this and has no other way to find out.
func TestResolve_RunsTheCascadeInOrderAndSaysWhichRuleFired(t *testing.T) {
	t.Parallel()

	var (
		kickoff  = day(2026, time.January, 5)
		handover = day(2026, time.January, 30)
		target   = day(2026, time.February, 3)
		targetTo = day(2026, time.February, 27)
		start    = day(2026, time.March, 2)
		dueOn    = day(2026, time.March, 27)
		madeAt   = time.Date(2026, time.January, 2, 11, 0, 0, 0, time.UTC)
		release  = day(2026, time.June, 30)
	)
	// An issue with every date a site could carry, which is what says the order
	// is an order and not a coincidence.
	everything := []issueOpt{
		withDate(testKickoffID, kickoff),
		withDate(testHandoverID, handover),
		withDate(testTargetStartID, target),
		withDate(testTargetEndID, targetTo),
		withDate(testStartDateID, start),
		due(dueOn),
		inSprint(42, "Sprint 42"),
		created(madeAt),
		fixVersion("1.4", release),
	}

	tests := []struct {
		name string
		opts []issueOpt
		want Range
	}{
		{
			name: "rule 1: the fields this profile names beat every other date on the issue",
			opts: everything,
			want: Range{Start: kickoff, End: handover, From: FromConfiguredFields, Source: "Kickoff to Handover"},
		},
		{
			name: "rule 1 with only one of its fields filled does not fire, and rule 2 does",
			opts: append([]issueOpt{withDate(testKickoffID, kickoff)}, everything[2:]...),
			want: Range{Start: target, End: targetTo, From: FromTargetDates, Source: targetStartName + " to " + targetEndName},
		},
		{
			name: "rule 2: target start and target end where Advanced Roadmaps is on",
			opts: everything[2:],
			want: Range{Start: target, End: targetTo, From: FromTargetDates, Source: targetStartName + " to " + targetEndName},
		},
		{
			name: "rule 2 with a target start and no target end falls through to rule 3",
			opts: []issueOpt{withDate(testTargetStartID, target), withDate(testStartDateID, start), due(dueOn)},
			want: Range{Start: start, End: dueOn, From: FromStartAndDue, Source: startDateName + " to " + dueDateFieldID},
		},
		{
			name: "rule 3: a Start date field against the platform's due date",
			opts: []issueOpt{withDate(testStartDateID, start), due(dueOn), inSprint(42, "Sprint 42"), created(madeAt), fixVersion("1.4", release)},
			want: Range{Start: start, End: dueOn, From: FromStartAndDue, Source: startDateName + " to " + dueDateFieldID},
		},
		{
			name: "rule 3 with a start date and no due date falls through to the sprint",
			opts: []issueOpt{withDate(testStartDateID, start), inSprint(42, "Sprint 42")},
			want: Range{Start: day(2026, time.March, 2), End: day(2026, time.March, 16), From: FromSprint, Source: "Sprint 42 to Sprint 42"},
		},
		{
			name: "rule 4: the dates of the sprint the issue is in",
			opts: []issueOpt{inSprint(42, "Sprint 42"), created(madeAt), fixVersion("1.4", release)},
			want: Range{Start: day(2026, time.March, 2), End: day(2026, time.March, 16), From: FromSprint, Source: "Sprint 42 to Sprint 42"},
		},
		{
			name: "rule 4 takes the sprint the issue is in now, which is the last one it joined",
			opts: []issueOpt{withSprintOptions(
				jira.Option{ID: "41", Label: "Sprint 41"},
				jira.Option{ID: "42", Label: "Sprint 42"})},
			want: Range{Start: day(2026, time.March, 2), End: day(2026, time.March, 16), From: FromSprint, Source: "Sprint 42 to Sprint 42"},
		},
		{
			name: "rule 4 walks back to a sprint with both dates when the current one has only a start",
			opts: []issueOpt{withSprintOptions(
				jira.Option{ID: "41", Label: "Sprint 41"},
				jira.Option{ID: "43", Label: "Sprint 43"})},
			want: Range{Start: day(2026, time.February, 2), End: day(2026, time.February, 16), From: FromSprint, Source: "Sprint 41 to Sprint 41"},
		},
		{
			name: "rule 4 with a sprint that has only a start falls through to rule 5",
			opts: []issueOpt{inSprint(43, "Sprint 43"), created(madeAt), fixVersion("1.4", release)},
			want: Range{Start: day(2026, time.January, 2), End: release, From: FromCreatedAndRelease, Source: createdFieldID + " to 1.4"},
		},
		{
			name: "rule 5: created against the release it is on, which draws faded",
			opts: []issueOpt{created(madeAt), fixVersion("1.4", release)},
			want: Range{Start: day(2026, time.January, 2), End: release, From: FromCreatedAndRelease, Source: createdFieldID + " to 1.4"},
		},
		{
			name: "rule 5 ends at the first release it is committed to, whichever order they arrive in",
			opts: []issueOpt{created(madeAt),
				fixVersion("2.0", day(2026, time.September, 1)),
				fixVersion("1.4", release),
				fixVersion("next", jira.Date{})},
			want: Range{Start: day(2026, time.January, 2), End: release, From: FromCreatedAndRelease, Source: createdFieldID + " to 1.4"},
		},
		{
			name: "rule 5 with the first release named first, which a last-one-wins reading would get wrong",
			opts: []issueOpt{created(madeAt),
				fixVersion("1.4", release),
				fixVersion("2.0", day(2026, time.September, 1))},
			want: Range{Start: day(2026, time.January, 2), End: release, From: FromCreatedAndRelease, Source: createdFieldID + " to 1.4"},
		},
		{
			name: "rule 6: a due date and nothing to pair it with is a milestone on the due date, not on the day it was filed",
			opts: []issueOpt{due(dueOn), created(madeAt)},
			want: Range{Start: dueOn, End: dueOn, From: FromOneDate, Source: dueDateFieldID},
		},
		{
			name: "rule 6 takes the first lone date the cascade saw, which is the configured one",
			opts: []issueOpt{withDate(testKickoffID, kickoff), withDate(testStartDateID, start), created(madeAt)},
			want: Range{Start: kickoff, End: kickoff, From: FromOneDate, Source: "Kickoff"},
		},
		{
			name: "rule 6 on an issue that has only ever been created",
			opts: []issueOpt{created(madeAt)},
			want: Range{Start: day(2026, time.January, 2), End: day(2026, time.January, 2), From: FromOneDate, Source: createdFieldID},
		},
		{
			name: "rule 6 on an issue that has only a release date",
			opts: []issueOpt{fixVersion("1.4", release)},
			want: Range{Start: release, End: release, From: FromOneDate, Source: "1.4"},
		},
		{
			name: "an issue the read asked about and that carries no date at all",
			opts: nil,
			want: Range{From: FromNothing, Absent: AbsentEmpty},
		},
		{
			name: "an issue read without a single field a date could come from",
			opts: []issueOpt{askedFor("summary", "status", "assignee")},
			want: Range{From: FromNothing, Absent: AbsentNotAsked},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			fields := ResolveDateFields(siteFields(), []string{"Kickoff"}, []string{"Handover"})
			d := NewDates(fields, WithNow(testClock()), WithSprints(sprints()))
			got, res := resolveOne(t, d, anIssue("TL-1", tt.opts...))
			if got != tt.want {
				t.Errorf("resolved %+v, want %+v", got, tt.want)
			}
			if got.OK() != (tt.want.From != FromNothing) {
				t.Errorf("OK() is %t on %+v", got.OK(), got)
			}
			if !got.OK() && got.Absent.String() == "" {
				t.Error("a range that resolved nothing says nothing about why")
			}
			if res.Resolved() != map[bool]int{true: 1, false: 0}[got.OK()] {
				t.Errorf("the pass counted %d resolved of %d", res.Resolved(), res.Len())
			}
		})
	}
}

// A date is not an instant. The same sprint boundary and the same created
// timestamp fall on different days in different zones, and the zone that decides
// is the account's rather than this machine's.
func TestResolve_BucketsAnInstantInTheZoneItWasGiven(t *testing.T) {
	t.Parallel()

	sprint := &fakeSprints{dates: map[int64]jira.Sprint{
		42: sprintOn(42, "Sprint 42",
			time.Date(2026, time.March, 2, 22, 30, 0, 0, time.UTC),
			time.Date(2026, time.March, 16, 22, 30, 0, 0, time.UTC)),
	}}

	// The same instant, at 22:30 UTC, is the next day thirteen hours east and
	// still the same day five hours west. Every instant the cascade reads is on
	// it: a sprint boundary, a created timestamp and a datetime custom field.
	const late = 22*time.Hour + 30*time.Minute
	filed := time.Date(2026, time.March, 2, 0, 0, 0, 0, time.UTC).Add(late)

	tests := []struct {
		name      string
		zone      string
		offset    int
		wantStart jira.Date
		wantEnd   jira.Date
		wantToday jira.Date
	}{
		{
			name: "the account's own zone, east of the boundary",
			zone: "east", offset: 13 * 60 * 60,
			wantStart: day(2026, time.March, 3), wantEnd: day(2026, time.March, 17),
			wantToday: day(2026, time.March, 5),
		},
		{
			name: "the account's own zone, west of it",
			zone: "west", offset: -5 * 60 * 60,
			wantStart: day(2026, time.March, 2), wantEnd: day(2026, time.March, 16),
			wantToday: day(2026, time.March, 5),
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			zone := time.FixedZone(tt.zone, tt.offset)
			d := NewDates(ResolveDateFields(siteFields(), nil, nil),
				WithNow(testClock()), WithSprints(sprint), WithZone(zone, ""))
			res, err := d.Resolve(t.Context(), []jira.Issue{
				anIssue("TL-1", inSprint(42, "Sprint 42")),
				anIssue("TL-2", created(filed), fixVersion("1.4", day(2026, time.June, 30))),
				anIssue("TL-3", withInstant(testStartDateID, filed), due(day(2026, time.June, 30))),
			})
			if err != nil {
				t.Fatalf("resolving: %v", err)
			}

			sprintBar, _ := res.Range("TL-1")
			if sprintBar.Start != tt.wantStart || sprintBar.End != tt.wantEnd {
				t.Errorf("the sprint bar runs %s to %s in %s, want %s to %s",
					sprintBar.Start, sprintBar.End, tt.zone, tt.wantStart, tt.wantEnd)
			}
			for _, key := range []string{"TL-2", "TL-3"} {
				got, _ := res.Range(key)
				if got.Start != tt.wantStart {
					t.Errorf("%s starts on %s in %s, want %s: %s", key, got.Start, tt.zone, tt.wantStart, got.Source)
				}
			}
			if res.Today() != tt.wantToday {
				t.Errorf("today is %s in %s, want %s", res.Today(), tt.zone, tt.wantToday)
			}
			if at, _ := res.Zone(); at != zone {
				t.Errorf("the resolution reports zone %s, want %s", at, zone)
			}
			if res.At() != datesNow {
				t.Errorf("the pass is dated %s, want the clock it was given", res.At())
			}
		})
	}
}

// A timeZone on the account is not a promise this machine can load it. That
// failure is local, so the cascade falls back to UTC and carries the reason
// where a view can show it — which is exactly the pair jira.Capabilities.Zone
// hands over.
func TestResolve_FallsBackToUTCAndRepeatsTheReasonItWasGiven(t *testing.T) {
	t.Parallel()

	caps := jira.Capabilities{
		TimeZoneReason: "this machine has no timezone database entry for Pacific/Auckland, so dates are shown in UTC",
	}
	d := NewDates(ResolveDateFields(siteFields(), nil, nil), WithNow(testClock()), WithZone(caps.Zone()))

	iss := anIssue("TL-1", created(time.Date(2026, time.March, 1, 23, 45, 0, 0, time.UTC)), fixVersion("1.4", day(2026, time.April, 1)))
	got, res := resolveOne(t, d, iss)
	if got.Start != day(2026, time.March, 1) {
		t.Errorf("created bucketed to %s, want the UTC day", got.Start)
	}
	zone, reason := res.Zone()
	if zone != time.UTC || reason != caps.TimeZoneReason {
		t.Errorf("the resolution reports %s / %q, want UTC and the probe's own sentence", zone, reason)
	}
	if !hasWarning(res, caps.TimeZoneReason) {
		t.Errorf("the reason the zone is UTC is in no warning: %v", res.Warnings())
	}
}

// An issue's sprint value arrives in one of two shapes, depending on whether the
// read that fetched it sent a schema block, and neither of them carries a date.
func TestResolve_ReadsASprintValueInBothShapesItArrivesIn(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		opt  issueOpt
		want Range
	}{
		{
			name: "as options, which is what a read with no schema block infers",
			opt:  inSprint(42, "Sprint 42"),
			want: Range{Start: day(2026, time.March, 2), End: day(2026, time.March, 16), From: FromSprint, Source: "Sprint 42 to Sprint 42"},
		},
		{
			name: "as the bytes it arrived as, which is what a read with one keeps",
			opt:  withSprintJSON(`[{"id":42,"name":"Sprint 42","state":"active","boardId":7}]`),
			want: Range{Start: day(2026, time.March, 2), End: day(2026, time.March, 16), From: FromSprint, Source: "Sprint 42 to Sprint 42"},
		},
		{
			name: "as bytes naming several sprints, the last of which is where it is now",
			opt:  withSprintJSON(`[{"id":41,"name":"Sprint 41"},{"id":42,"name":"Sprint 42"}]`),
			want: Range{Start: day(2026, time.March, 2), End: day(2026, time.March, 16), From: FromSprint, Source: "Sprint 42 to Sprint 42"},
		},
		{
			name: "as bytes nothing can be read out of, which is a fall-through and not a crash",
			opt:  withSprintJSON(`[{"id":`),
			want: Range{Start: day(2026, time.January, 2), End: day(2026, time.January, 2), From: FromOneDate, Source: createdFieldID},
		},
		{
			name: "as a value in a shape no sprint ever had",
			opt:  withText(testSprintID, "Sprint 42"),
			want: Range{Start: day(2026, time.January, 2), End: day(2026, time.January, 2), From: FromOneDate, Source: createdFieldID},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			d := NewDates(ResolveDateFields(siteFields(), nil, nil), WithNow(testClock()), WithSprints(sprints()))
			iss := anIssue("TL-1", tt.opt, created(time.Date(2026, time.January, 2, 11, 0, 0, 0, time.UTC)))
			if got, _ := resolveOne(t, d, iss); got != tt.want {
				t.Errorf("resolved %+v, want %+v", got, tt.want)
			}
		})
	}
}

// A date custom field read from an endpoint that sent no schema arrives as
// whatever its text parsed as, so the cascade reads three shapes of the same
// value and refuses anything that is not one.
func TestResolve_ReadsADateFieldInEveryShapeAValueArrivesIn(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name  string
		start issueOpt
		want  jira.Date
	}{
		{
			name:  "a date, which is already a calendar date",
			start: withDate(testStartDateID, day(2026, time.March, 2)),
			want:  day(2026, time.March, 2),
		},
		{
			name:  "a datetime late in the day, bucketed in the zone the pass was given",
			start: withInstant(testStartDateID, time.Date(2026, time.March, 2, 23, 30, 0, 0, time.UTC)),
			want:  day(2026, time.March, 3),
		},
		{
			name:  "text a schemaless read left as text",
			start: withText(testStartDateID, "2026-03-02"),
			want:  day(2026, time.March, 2),
		},
		{
			name:  "text holding an instant",
			start: withText(testStartDateID, "2026-03-02T23:30:00.000+0000"),
			want:  jira.Date{},
		},
		{
			name:  "text holding an instant Go can read",
			start: withText(testStartDateID, "2026-03-02T23:30:00Z"),
			want:  day(2026, time.March, 3),
		},
		{
			name:  "text that is not a date at all",
			start: withText(testStartDateID, "next quarter"),
			want:  jira.Date{},
		},
		{
			name:  "a zero date, which is a field with nothing in it",
			start: withDate(testStartDateID, jira.Date{}),
			want:  jira.Date{},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			d := NewDates(ResolveDateFields(siteFields(), nil, nil),
				WithNow(testClock()), WithZone(time.FixedZone("east", 13*60*60), ""))
			iss := anIssue("TL-1", tt.start, due(day(2026, time.March, 27)))
			got, _ := resolveOne(t, d, iss)
			switch {
			case tt.want.IsZero() && got.From != FromOneDate:
				t.Errorf("a start nothing can be read out of resolved %+v, want the due date alone", got)
			case !tt.want.IsZero() && got.From != FromStartAndDue:
				t.Errorf("resolved from %v, want rule 3", got.From)
			case !tt.want.IsZero() && got.Start != tt.want:
				t.Errorf("the bar starts on %s, want %s", got.Start, tt.want)
			}
		})
	}
}

func TestResolve_ReadsEachSprintOnceHoweverManyIssuesAreInIt(t *testing.T) {
	t.Parallel()

	sprint := sprints()
	d := NewDates(ResolveDateFields(siteFields(), nil, nil), WithNow(testClock()), WithSprints(sprint))

	issues := make([]jira.Issue, 0, 60)
	for i := range 60 {
		id := int64(41 + i%2)
		issues = append(issues, anIssue(fmt.Sprintf("TL-%d", i+1), inSprint(id, "")))
	}
	res, err := d.Resolve(t.Context(), issues)
	if err != nil {
		t.Fatalf("resolving sixty issues: %v", err)
	}
	if got := sprint.calls(); len(got) != 2 || got[0] != 41 || got[1] != 42 {
		t.Errorf("the pass made %d sprint call(s) (%v), want one per distinct sprint in ascending order", len(got), got)
	}
	if res.Resolved() != 60 {
		t.Errorf("%d of 60 issues got a range", res.Resolved())
	}
	for _, key := range res.Keys() {
		got, _ := res.Range(key)
		if got.From != FromSprint {
			t.Fatalf("%s resolved from %v, want the sprint", key, got.From)
		}
	}
}

func TestResolve_CollapsesTwoPassesThatWantTheSameSprintAtOnce(t *testing.T) {
	t.Parallel()

	const callers = 4
	sprint := sprints()
	sprint.arrived = make(chan struct{}, callers)
	sprint.release = make(chan struct{})
	d := NewDates(ResolveDateFields(siteFields(), nil, nil), WithNow(testClock()), WithSprints(sprint))

	// Joining the flight is the barrier: a caller merely started would
	// legitimately begin a second read once this one had finished.
	var joined atomic.Int64
	wanted := sprintFlightKey(42)
	d.flight.joined = func(key string) {
		if key == wanted {
			joined.Add(1)
		}
	}

	results := make(chan error, callers)
	for i := range callers {
		go func() {
			_, err := d.Resolve(t.Context(), []jira.Issue{anIssue(fmt.Sprintf("TL-%d", i+1), inSprint(42, "Sprint 42"))})
			results <- err
		}()
	}

	<-sprint.arrived
	waitFor(t, "every pass to have joined the sprint read in flight", func() bool {
		return joined.Load() == callers
	})
	close(sprint.release)
	for range callers {
		if err := <-results; err != nil {
			t.Fatalf("a pass sharing the sprint read failed: %v", err)
		}
	}
	if got := sprint.calls(); len(got) != 1 {
		t.Errorf("the reader was asked %d times (%v), want once: four views drawing the same sprint is one request", len(got), got)
	}
}

// A sprint that cannot be read is an answer about that sprint. The issues in it
// fall through to the next rule and the pass says what happened, because one
// board a token cannot see must not empty a timeline.
func TestResolve_FallsThroughAndSaysSoWhenASprintCannotBeRead(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		fail error
		want string
	}{
		{
			name: "the token cannot see the board",
			fail: &jira.CapabilityError{Capability: jira.CapBoards, Reason: "You need the Browse Projects permission to see this board"},
			want: "Browse Projects",
		},
		{
			name: "the site is rate limiting",
			fail: &jira.RateLimitError{RetryAfter: 30 * time.Second, Endpoint: "GET /rest/agile/1.0/sprint/42"},
			want: "retry in 30s",
		},
		{
			name: "the request produced no answer at all",
			fail: &jira.TransportError{Op: "GET /rest/agile/1.0/sprint/42", Err: errors.New("no route to host")},
			want: "no route to host",
		},
		{
			name: "the sprint has been deleted",
			fail: &jira.NotFoundError{Kind: "sprint", ID: "42"},
			want: "does not exist",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			sprint := sprints()
			sprint.fail = map[int64]error{42: tt.fail}
			d := NewDates(ResolveDateFields(siteFields(), nil, nil), WithNow(testClock()), WithSprints(sprint))

			iss := anIssue("TL-1", inSprint(42, "Sprint 42"),
				created(time.Date(2026, time.January, 2, 11, 0, 0, 0, time.UTC)),
				fixVersion("1.4", day(2026, time.June, 30)))
			got, res := resolveOne(t, d, iss)
			if got.From != FromCreatedAndRelease {
				t.Errorf("the issue resolved from %v, want the rule below the sprint", got.From)
			}
			if !hasWarning(res, tt.want) {
				t.Errorf("no warning says why the sprint was not used; got %v", res.Warnings())
			}
			if !hasWarning(res, "sprint 42") {
				t.Errorf("the warnings do not name the sprint: %v", res.Warnings())
			}
		})
	}
}

func TestResolve_SaysSoWhenItHasNoWayToReadASprintAtAll(t *testing.T) {
	t.Parallel()

	d := NewDates(ResolveDateFields(siteFields(), nil, nil), WithNow(testClock()))
	got, res := resolveOne(t, d, anIssue("TL-1", inSprint(42, "Sprint 42"), due(day(2026, time.March, 27))))
	if got.From != FromOneDate {
		t.Errorf("the issue resolved from %v, want the due date alone", got.From)
	}
	if !hasWarning(res, "nothing to read a sprint with") {
		t.Errorf("a session with no sprint reader says nothing about rule 4: %v", res.Warnings())
	}
}

func TestResolve_StopsWhenTheCallerGoesAway(t *testing.T) {
	t.Parallel()

	t.Run("before the pass starts", func(t *testing.T) {
		t.Parallel()

		ctx, cancel := context.WithCancel(t.Context())
		cancel()
		sprint := sprints()
		d := NewDates(ResolveDateFields(siteFields(), nil, nil), WithNow(testClock()), WithSprints(sprint))
		if _, err := d.Resolve(ctx, []jira.Issue{anIssue("TL-1", inSprint(42, ""))}); !errors.Is(err, context.Canceled) {
			t.Fatalf("got %v, want the context's own error", err)
		}
		if got := sprint.calls(); len(got) != 0 {
			t.Errorf("the reader was asked %v for a pass the caller had already given up on", got)
		}
	})

	t.Run("part way through reading the sprints", func(t *testing.T) {
		t.Parallel()

		ctx, cancel := context.WithCancel(t.Context())
		sprint := sprints()
		sprint.cancel = cancel
		d := NewDates(ResolveDateFields(siteFields(), nil, nil), WithNow(testClock()), WithSprints(sprint))

		issues := []jira.Issue{
			anIssue("TL-1", inSprint(41, "")),
			anIssue("TL-2", inSprint(42, "")),
		}
		if _, err := d.Resolve(ctx, issues); !errors.Is(err, context.Canceled) {
			t.Fatalf("got %v, want the context's own error rather than a half-drawn timeline", err)
		}
	})
}

// A parent with no dates of its own spans the children that have some, and the
// rollup is a provenance of its own so that the view can draw it as something
// nobody set.
func TestResolve_RollsAParentUpToItsChildren(t *testing.T) {
	t.Parallel()

	d := NewDates(ResolveDateFields(siteFields(), []string{"Kickoff"}, []string{"Handover"}), WithNow(testClock()))
	issues := []jira.Issue{
		anIssue("TL-1"),
		anIssue("TL-2", childOf("TL-1"), withDate(testStartDateID, day(2026, time.March, 2)), due(day(2026, time.March, 20))),
		anIssue("TL-3", childOf("TL-1"), withDate(testStartDateID, day(2026, time.February, 10)), due(day(2026, time.March, 10))),
		// The latest day of all is an end and not a start, and the earliest is
		// the end of a child whose two dates are the wrong way round — so a
		// rollup that reads only one end of each child gets both wrong.
		anIssue("TL-4", childOf("TL-1"), withDate(testStartDateID, day(2026, time.March, 1)), due(day(2026, time.May, 30))),
		anIssue("TL-5", childOf("TL-1"), due(day(2026, time.April, 30))),
		anIssue("TL-6", childOf("TL-1"),
			withDate(testKickoffID, day(2026, time.January, 20)),
			withDate(testHandoverID, day(2026, time.January, 5))),
		anIssue("TL-7", childOf("TL-1")),
	}
	res, err := d.Resolve(t.Context(), issues)
	if err != nil {
		t.Fatalf("resolving: %v", err)
	}

	got, _ := res.Range("TL-1")
	want := Range{
		Start:  day(2026, time.January, 5),
		End:    day(2026, time.May, 30),
		From:   FromChildren,
		Source: "5 of its children",
	}
	if got != want {
		t.Errorf("the parent resolved %+v, want %+v", got, want)
	}
	if !got.From.Rollup() || got.From.Bar() != true {
		t.Errorf("a rollup reports Rollup()=%t Bar()=%t", got.From.Rollup(), got.From.Bar())
	}
	if child, _ := res.Range("TL-7"); child.OK() {
		t.Errorf("the childless child was given %+v; a rollup runs one way only", child)
	}
	if start, end := res.Span(); start != want.Start || end != want.End {
		t.Errorf("the pass spans %s to %s, want %s to %s", start, end, want.Start, want.End)
	}
}

func TestResolve_RollsUpThroughAParentThatIsItselfARollup(t *testing.T) {
	t.Parallel()

	d := NewDates(ResolveDateFields(siteFields(), nil, nil), WithNow(testClock()))
	// A grandparent, a parent with no dates, and a leaf that has some. The
	// grandparent's range can only come from the leaf, through the middle.
	issues := []jira.Issue{
		anIssue("TL-1", withSubtasks("TL-2")),
		anIssue("TL-2", childOf("TL-1")),
		anIssue("TL-3", childOf("TL-2"), withDate(testStartDateID, day(2026, time.March, 2)), due(day(2026, time.March, 20))),
	}
	res, err := d.Resolve(t.Context(), issues)
	if err != nil {
		t.Fatalf("resolving: %v", err)
	}
	for _, key := range []string{"TL-1", "TL-2"} {
		got, _ := res.Range(key)
		if got.From != FromChildren || got.Start != day(2026, time.March, 2) || got.End != day(2026, time.March, 20) {
			t.Errorf("%s resolved %+v, want the leaf's range rolled up", key, got)
		}
	}
}

func TestResolve_DoesNotRollUpOntoAParentThatHasItsOwnDates(t *testing.T) {
	t.Parallel()

	d := NewDates(ResolveDateFields(siteFields(), nil, nil), WithNow(testClock()))
	// Three levels, and the middle one has dates of its own. It must keep them
	// even though its own child has some too, and the top must span the middle
	// rather than reaching past it.
	issues := []jira.Issue{
		anIssue("TL-1"),
		anIssue("TL-2", childOf("TL-1"),
			withDate(testStartDateID, day(2026, time.March, 2)), due(day(2026, time.March, 10))),
		anIssue("TL-3", childOf("TL-2"),
			withDate(testStartDateID, day(2026, time.January, 1)), due(day(2026, time.December, 31))),
	}
	res, err := d.Resolve(t.Context(), issues)
	if err != nil {
		t.Fatalf("resolving: %v", err)
	}
	middle, _ := res.Range("TL-2")
	if middle.From != FromStartAndDue || middle.Start != day(2026, time.March, 2) || middle.End != day(2026, time.March, 10) {
		t.Errorf("the middle issue resolved %+v, want its own dates kept: a rollup is for an issue that has none", middle)
	}
	top, _ := res.Range("TL-1")
	want := Range{Start: day(2026, time.March, 2), End: day(2026, time.March, 10), From: FromChildren, Source: "1 of its children"}
	if top != want {
		t.Errorf("the top issue resolved %+v, want %+v: it spans its child, not its grandchild", top, want)
	}
}

// Jira will not make a parent its own ancestor. A cache holding two halves of a
// reparenting can, and a walk that recurses on it never returns.
func TestResolve_RefusesToWalkAParentChainThatLoops(t *testing.T) {
	t.Parallel()

	d := NewDates(ResolveDateFields(siteFields(), nil, nil), WithNow(testClock()))
	issues := []jira.Issue{
		anIssue("TL-1", childOf("TL-2")),
		anIssue("TL-2", childOf("TL-3")),
		anIssue("TL-3", childOf("TL-1")),
		anIssue("TL-4", childOf("TL-4")),
	}
	res, err := d.Resolve(t.Context(), issues)
	if err != nil {
		t.Fatalf("resolving a loop: %v", err)
	}
	for _, key := range res.Keys() {
		if got, _ := res.Range(key); got.OK() {
			t.Errorf("%s resolved %+v out of a loop with no dates anywhere in it", key, got)
		}
	}
	if !hasWarning(res, "leads back to itself") {
		t.Errorf("the loop is in no warning: %v", res.Warnings())
	}
}

func TestResolve_NamesTheIssuesWhoseDatesAreTheWrongWayRound(t *testing.T) {
	t.Parallel()

	d := NewDates(ResolveDateFields(siteFields(), []string{"Kickoff"}, []string{"Handover"}), WithNow(testClock()))
	iss := anIssue("TL-1",
		withDate(testKickoffID, day(2026, time.March, 20)),
		withDate(testHandoverID, day(2026, time.March, 2)))
	got, res := resolveOne(t, d, iss)
	if !got.Backwards() {
		t.Errorf("%+v does not report itself as backwards", got)
	}
	if !hasWarning(res, "TL-1") {
		t.Errorf("nothing names the issue whose dates are reversed: %v", res.Warnings())
	}
}

func TestResolve_CoversEveryIssueItWasGivenAndNothingElse(t *testing.T) {
	t.Parallel()

	d := NewDates(ResolveDateFields(siteFields(), nil, nil), WithNow(testClock()))
	issues := []jira.Issue{
		anIssue("TL-2", due(day(2026, time.March, 2))),
		anIssue("TL-1", due(day(2026, time.March, 3))),
		anIssue("", due(day(2026, time.March, 4))),
		anIssue("TL-1", due(day(2026, time.March, 5))),
	}
	res, err := d.Resolve(t.Context(), issues)
	if err != nil {
		t.Fatalf("resolving: %v", err)
	}
	if got := res.Keys(); !equalStrings(got, []string{"TL-1", "TL-2"}) {
		t.Errorf("the pass covers %v, want the two keys it was given, sorted", got)
	}
	if res.Len() != 2 {
		t.Errorf("the pass holds %d ranges, want 2", res.Len())
	}
	if got, _ := res.Range("TL-1"); got.Start != day(2026, time.March, 5) {
		t.Errorf("the repeated key resolved to %s, want the last read of it", got.Start)
	}
	if _, ok := res.Range("TL-9"); ok {
		t.Error("the resolution answers for an issue it never saw")
	}
	if _, err := d.Resolve(t.Context(), nil); err != nil {
		t.Errorf("resolving nothing failed: %v", err)
	}
}

func TestResolve_HandsOutCopiesRatherThanItsOwnState(t *testing.T) {
	t.Parallel()

	d := NewDates(ResolveDateFields(siteFields(), []string{"Kickoff"}, nil), WithNow(testClock()))
	_, res := resolveOne(t, d, anIssue("TL-1", due(day(2026, time.March, 2))))

	keys := res.Keys()
	keys[0] = "written over"
	if got := res.Keys(); got[0] != "TL-1" {
		t.Error("a caller writing into the keys it was handed rewrote the resolution")
	}
	if warnings := res.Warnings(); len(warnings) > 0 {
		warnings[0] = "written over"
		if res.Warnings()[0] == "written over" {
			t.Error("a caller writing into the warnings it was handed rewrote the resolution")
		}
	}
	ids := d.Fields().IDs()
	ids[0] = "written over"
	if got := d.Fields().IDs(); got[0] == "written over" {
		t.Error("a caller writing into the field IDs it was handed rewrote the cascade")
	}
}

func TestProvenance_SaysWhichRuleFiredAndHowToDrawIt(t *testing.T) {
	t.Parallel()

	tests := []struct {
		from                              Provenance
		rule                              int
		ok, bar, faded, milestone, rollup bool
	}{
		{from: FromNothing, rule: 0},
		{from: FromConfiguredFields, rule: 1, ok: true, bar: true},
		{from: FromTargetDates, rule: 2, ok: true, bar: true},
		{from: FromStartAndDue, rule: 3, ok: true, bar: true},
		{from: FromSprint, rule: 4, ok: true, bar: true},
		{from: FromCreatedAndRelease, rule: 5, ok: true, bar: true, faded: true},
		{from: FromOneDate, rule: 6, ok: true, milestone: true},
		{from: FromChildren, rule: 7, ok: true, bar: true, rollup: true},
	}
	if len(tests) != int(FromChildren)+1 {
		t.Fatalf("the table covers %d provenances and there are %d", len(tests), FromChildren+1)
	}
	for _, tt := range tests {
		t.Run(fmt.Sprintf("rule %d", tt.rule), func(t *testing.T) {
			t.Parallel()

			if got := tt.from.Rule(); got != tt.rule {
				t.Errorf("Rule() = %d, want %d", got, tt.rule)
			}
			for _, check := range []struct {
				what string
				got  bool
				want bool
			}{
				{"OK", tt.from.OK(), tt.ok},
				{"Bar", tt.from.Bar(), tt.bar},
				{"Faded", tt.from.Faded(), tt.faded},
				{"Milestone", tt.from.Milestone(), tt.milestone},
				{"Rollup", tt.from.Rollup(), tt.rollup},
			} {
				if check.got != check.want {
					t.Errorf("%s() = %t, want %t", check.what, check.got, check.want)
				}
			}
			if tt.from.String() == "" {
				t.Error("String() says nothing, so a footer showing provenance shows a blank")
			}
		})
	}
}

func TestAbsence_TellsAFieldNobodyAskedForFromOneThatIsEmpty(t *testing.T) {
	t.Parallel()

	if AbsentNothing.String() != "" {
		t.Errorf("a resolved range reports the absence %q", AbsentNothing.String())
	}
	if got := AbsentEmpty.String(); !strings.Contains(got, "requested and absent") {
		t.Errorf("AbsentEmpty reads %q; a mask cannot tell an empty field from a missing one, and saying "+
			"\"no dates\" instead is how an afternoon goes", got)
	}
	if got := AbsentNotAsked.String(); got == "" || strings.Contains(got, "requested and absent") {
		t.Errorf("AbsentNotAsked reads %q, want it to say the read never asked", got)
	}
}

func hasWarning(res Resolution, want string) bool {
	for _, line := range res.Warnings() {
		if strings.Contains(line, want) {
			return true
		}
	}
	return false
}

func equalStrings(got, want []string) bool {
	if len(got) != len(want) {
		return false
	}
	for i := range got {
		if got[i] != want[i] {
			return false
		}
	}
	return true
}

// fixedSprints is the sprint reader a benchmark needs: no locking and no
// bookkeeping, so what is measured is the cascade.
type fixedSprints map[int64]jira.Sprint

func (f fixedSprints) Sprint(_ context.Context, id int64) (jira.Sprint, error) {
	sprint, ok := f[id]
	if !ok {
		return jira.Sprint{}, &jira.NotFoundError{Kind: "sprint", ID: strconv.FormatInt(id, 10)}
	}
	return sprint, nil
}

// timelineOfIssues is a timeline's worth of issues in the mix a real project
// produces: a fifth on each of the first three rules, a fifth in one of twenty
// sprints, a fifth with nothing but the day it was filed and a release, and one
// parent in ten rolling up over the rest.
//
// The sprint values are the shape a real read produces — the bytes, not the
// inferred options — because that is the shape the site sends once a field list
// names a custom field, and it is the expensive one.
func timelineOfIssues(n int) []jira.Issue {
	issues := make([]jira.Issue, 0, n)
	mask := jira.NewFieldMask(ResolveDateFields(siteFields(), []string{"Kickoff"}, []string{"Handover"}).IDs())
	base := time.Date(2026, time.January, 6, 9, 0, 0, 0, time.UTC)
	for i := range n {
		iss := jira.Issue{ID: strconv.Itoa(100000 + i), Key: "TL-" + strconv.Itoa(i+1), Requested: mask}
		start := jira.DateOf(base.AddDate(0, 0, i%180))
		end := jira.DateOf(base.AddDate(0, 0, i%180+14))
		switch i % 5 {
		case 0:
			iss.Fields = iss.Fields.With(ref(testKickoffID), jira.FieldValue{Kind: jira.KindDate, Date: start})
			iss.Fields = iss.Fields.With(ref(testHandoverID), jira.FieldValue{Kind: jira.KindDate, Date: end})
		case 1:
			iss.Fields = iss.Fields.With(ref(testTargetStartID), jira.FieldValue{Kind: jira.KindDate, Date: start})
			iss.Fields = iss.Fields.With(ref(testTargetEndID), jira.FieldValue{Kind: jira.KindTime, Time: end.In(time.UTC)})
		case 2:
			iss.Fields = iss.Fields.With(ref(testStartDateID), jira.FieldValue{Kind: jira.KindDate, Date: start})
			iss.Due = end
		case 3:
			iss.Fields = iss.Fields.With(ref(testSprintID), jira.FieldValue{
				Kind: jira.KindUnknown,
				Text: fmt.Sprintf(`[{"id":%d,"name":"Sprint %d","state":"closed","boardId":7}]`, 100+i%20, 100+i%20),
			})
		case 4:
			iss.Created = base.AddDate(0, 0, i%180)
			iss.FixVersions = []jira.Version{{ID: "v1", Name: "1.4", ReleaseDate: end}}
		}
		if i%10 == 0 && i > 0 {
			iss.Parent = &jira.IssueRef{Key: "TL-" + strconv.Itoa(i)}
		}
		issues = append(issues, iss)
	}
	return issues
}

func benchmarkResolveDates(b *testing.B, n int) {
	b.Helper()

	reader := fixedSprints{}
	for id := int64(100); id < 120; id++ {
		start := time.Date(2026, time.January, 6, 9, 0, 0, 0, time.UTC).AddDate(0, 0, int(id-100)*14)
		end := start.AddDate(0, 0, 14)
		reader[id] = sprintOn(id, "Sprint "+strconv.FormatInt(id, 10), start, end)
	}
	d := NewDates(ResolveDateFields(siteFields(), []string{"Kickoff"}, []string{"Handover"}),
		WithNow(testClock()), WithSprints(reader), WithZone(time.UTC, ""))
	issues := timelineOfIssues(n)
	ctx := context.Background()

	b.ReportAllocs()
	b.ResetTimer()
	for range b.N {
		res, err := d.Resolve(ctx, issues)
		if err != nil {
			b.Fatalf("resolving: %v", err)
		}
		if res.Resolved() != n {
			b.Fatalf("%d of %d issues resolved; the benchmark is measuring the wrong thing", res.Resolved(), n)
		}
	}
}

// BenchmarkResolveDates2k is a timeline's worth of issues: the pass a view runs
// when a page lands, sprint reads included.
func BenchmarkResolveDates2k(b *testing.B) { benchmarkResolveDates(b, 2000) }

// BenchmarkResolveDates10k is the same pass at the size docs/PERFORMANCE.md
// measures a list at, which is what says the cost is linear rather than that 2k
// happens to fit.
func BenchmarkResolveDates10k(b *testing.B) { benchmarkResolveDates(b, 10000) }
