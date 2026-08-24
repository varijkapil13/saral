package jira_test

import (
	"bytes"
	"errors"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"testing"
	"time"
	_ "time/tzdata"

	"github.com/varijkapil13/saral/pkg/adf"
	"github.com/varijkapil13/saral/pkg/jira"
)

func ptr[T any](v T) *T { return &v }

func ref(id, schemaType string) jira.FieldRef {
	return jira.FieldRef{ID: id, Name: id, Schema: jira.FieldSchema{Type: schemaType}}
}

func location(t *testing.T, name string) *time.Location {
	t.Helper()

	loc, err := time.LoadLocation(name)
	if err != nil {
		t.Fatalf("loading location %s: %v", name, err)
	}
	return loc
}

func TestParseDate_ReadsJirasFormatAndRejectsEverythingElse(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		input   string
		want    jira.Date
		wantErr bool
	}{
		{name: "a due date", input: "2026-08-20", want: jira.Date{Year: 2026, Month: time.August, Day: 20}},
		{name: "the first of a month", input: "2026-01-01", want: jira.Date{Year: 2026, Month: time.January, Day: 1}},
		{name: "a leap day", input: "2024-02-29", want: jira.Date{Year: 2024, Month: time.February, Day: 29}},
		{name: "the empty string", input: "", wantErr: true},
		{name: "an unpadded month", input: "2026-8-20", wantErr: true},
		{name: "a day-first date", input: "20-08-2026", wantErr: true},
		{name: "a month that does not exist", input: "2026-13-01", wantErr: true},
		{name: "a day that does not exist", input: "2026-02-30", wantErr: true},
		{name: "a timestamp", input: "2026-08-20T09:30:00.000+0200", wantErr: true},
		{name: "prose", input: "tomorrow", wantErr: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			got, err := jira.ParseDate(tt.input)
			if tt.wantErr {
				if err == nil {
					t.Fatalf("ParseDate(%q) = %v, want an error", tt.input, got)
				}
				if !got.IsZero() {
					t.Errorf("ParseDate(%q) returned %v alongside its error, want the zero date", tt.input, got)
				}
				return
			}
			if err != nil {
				t.Fatalf("ParseDate(%q): %v", tt.input, err)
			}
			if got != tt.want {
				t.Errorf("ParseDate(%q) = %v, want %v", tt.input, got, tt.want)
			}
			if round := got.String(); round != tt.input {
				t.Errorf("String() = %q, want the %q it was parsed from", round, tt.input)
			}
		})
	}
}

func TestDate_StringPadsAndEmptiesTheZeroDate(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		date jira.Date
		want string
	}{
		{name: "a single-digit month and day", date: jira.Date{Year: 2026, Month: time.March, Day: 5}, want: "2026-03-05"},
		{name: "the last day of a year", date: jira.Date{Year: 2026, Month: time.December, Day: 31}, want: "2026-12-31"},
		{name: "the unset date", date: jira.Date{}, want: ""},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			if got := tt.date.String(); got != tt.want {
				t.Errorf("String() = %q, want %q", got, tt.want)
			}
			if got := tt.date.IsZero(); got != (tt.want == "") {
				t.Errorf("IsZero() = %t for %v", got, tt.date)
			}
		})
	}
}

func TestDate_RendersTheSameCalendarDayInEveryZone(t *testing.T) {
	t.Parallel()

	due := jira.Date{Year: 2026, Month: time.August, Day: 20}
	zones := []string{
		"UTC",
		"Pacific/Kiritimati",  // UTC+14
		"Pacific/Pago_Pago",   // UTC-11
		"Asia/Kolkata",        // UTC+5:30
		"America/Los_Angeles", // UTC-7 in August
		"Australia/Lord_Howe", // UTC+10:30 in August
	}

	for _, zone := range zones {
		t.Run(zone, func(t *testing.T) {
			t.Parallel()

			loc := location(t, zone)
			midnight := due.In(loc)
			if got := midnight.Format(time.DateOnly); got != due.String() {
				t.Errorf("In(%s) renders as %s, want %s", zone, got, due)
			}
			if got := jira.DateOf(midnight); got != due {
				t.Errorf("DateOf(In(%s)) = %v, want %v", zone, got, due)
			}
		})
	}

	t.Run("a nil location means UTC", func(t *testing.T) {
		t.Parallel()

		got := due.In(nil)
		if got.Location() != time.UTC {
			t.Errorf("In(nil) is in %s, want UTC", got.Location())
		}
		if want := due.In(time.UTC); !got.Equal(want) {
			t.Errorf("In(nil) = %s, want %s", got, want)
		}
	})
}

func TestDate_IsWhyADueDateIsNotHeldAsATime(t *testing.T) {
	t.Parallel()

	due := jira.Date{Year: 2026, Month: time.August, Day: 20}
	asTime := time.Date(2026, time.August, 20, 0, 0, 0, 0, time.UTC)

	for _, zone := range []string{"Pacific/Pago_Pago", "America/Los_Angeles"} {
		t.Run(zone, func(t *testing.T) {
			t.Parallel()

			loc := location(t, zone)
			if slipped := jira.DateOf(asTime.In(loc)); slipped == due {
				t.Fatalf("midnight UTC still reads as %v in %s, so this test no longer shows the bug Date prevents", slipped, zone)
			}
			if held := jira.DateOf(due.In(loc)); held != due {
				t.Errorf("Date slipped to %v in %s, want %v", held, zone, due)
			}
		})
	}
}

func TestDate_BeforeOrdersByCalendarDay(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name  string
		left  jira.Date
		right jira.Date
		want  bool
	}{
		{
			name:  "the day before",
			left:  jira.Date{Year: 2026, Month: time.August, Day: 19},
			right: jira.Date{Year: 2026, Month: time.August, Day: 20},
			want:  true,
		},
		{
			name:  "the same day",
			left:  jira.Date{Year: 2026, Month: time.August, Day: 20},
			right: jira.Date{Year: 2026, Month: time.August, Day: 20},
			want:  false,
		},
		{
			name:  "the day after",
			left:  jira.Date{Year: 2026, Month: time.August, Day: 21},
			right: jira.Date{Year: 2026, Month: time.August, Day: 20},
			want:  false,
		},
		{
			name:  "across a month boundary",
			left:  jira.Date{Year: 2026, Month: time.August, Day: 31},
			right: jira.Date{Year: 2026, Month: time.September, Day: 1},
			want:  true,
		},
		{
			name:  "across a year boundary",
			left:  jira.Date{Year: 2026, Month: time.December, Day: 31},
			right: jira.Date{Year: 2027, Month: time.January, Day: 1},
			want:  true,
		},
		{
			name:  "the unset date sorts first",
			left:  jira.Date{},
			right: jira.Date{Year: 2026, Month: time.August, Day: 20},
			want:  true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			if got := tt.left.Before(tt.right); got != tt.want {
				t.Errorf("%v.Before(%v) = %t, want %t", tt.left, tt.right, got, tt.want)
			}
		})
	}
}

func TestParseStatusCategory_MapsEveryKeyJiraSends(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		key  string
		want jira.StatusCategory
	}{
		{name: "the API key for to do", key: "new", want: jira.CategoryToDo},
		{name: "the API key for in progress", key: "indeterminate", want: jira.CategoryInProgress},
		{name: "the API key for done", key: "done", want: jira.CategoryDone},
		{name: "the API key for uncategorised", key: "undefined", want: jira.CategoryUnknown},
		{name: "the display name of to do", key: "To Do", want: jira.CategoryToDo},
		{name: "the display name of in progress", key: "In Progress", want: jira.CategoryInProgress},
		{name: "the display name of done", key: "Done", want: jira.CategoryDone},
		{name: "a shouted key", key: "INDETERMINATE", want: jira.CategoryInProgress},
		{name: "the spelling without a space", key: "todo", want: jira.CategoryToDo},
		{name: "the other spelling of done", key: "complete", want: jira.CategoryDone},
		{name: "a category this client has never heard of", key: "escalated", want: jira.CategoryUnknown},
		{name: "no key at all", key: "", want: jira.CategoryUnknown},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			if got := jira.ParseStatusCategory(tt.key); got != tt.want {
				t.Errorf("ParseStatusCategory(%q) = %v, want %v", tt.key, got, tt.want)
			}
		})
	}
}

func TestStatusCategory_StringNamesEveryCategory(t *testing.T) {
	t.Parallel()

	tests := []struct {
		category jira.StatusCategory
		want     string
	}{
		{category: jira.CategoryToDo, want: "To Do"},
		{category: jira.CategoryInProgress, want: "In Progress"},
		{category: jira.CategoryDone, want: "Done"},
		{category: jira.CategoryUnknown, want: "Unknown"},
		{category: jira.StatusCategory(99), want: "Unknown"},
	}

	for _, tt := range tests {
		t.Run(tt.want, func(t *testing.T) {
			t.Parallel()

			if got := tt.category.String(); got != tt.want {
				t.Errorf("String() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestFieldSet_StoresAndReturnsEveryKindOfValue(t *testing.T) {
	t.Parallel()

	var (
		text    = ref("customfield_10001", "string")
		number  = ref("customfield_10016", "number")
		flag    = ref("customfield_10002", "option")
		date    = ref("duedate", "date")
		stamp   = ref("customfield_10003", "datetime")
		doc     = ref("description", "doc")
		option  = ref("customfield_10004", "option")
		options = ref("customfield_10005", "array")
		user    = ref("assignee", "user")
		users   = ref("customfield_10006", "array")
	)
	when := time.Date(2026, time.August, 20, 9, 30, 0, 0, time.UTC)
	body := adf.NewDoc(adf.NewNode("paragraph", adf.NewText("a description")))

	var set jira.FieldSet
	set = set.With(text, jira.FieldValue{Kind: jira.KindText, Text: "a summary"})
	set = set.With(number, jira.FieldValue{Kind: jira.KindNumber, Number: 3.5})
	set = set.With(flag, jira.FieldValue{Kind: jira.KindBool, Bool: true})
	set = set.With(date, jira.FieldValue{Kind: jira.KindDate, Date: jira.Date{Year: 2026, Month: time.August, Day: 20}})
	set = set.With(stamp, jira.FieldValue{Kind: jira.KindTime, Time: when})
	set = set.With(doc, jira.FieldValue{Kind: jira.KindDoc, Doc: body})
	set = set.With(option, jira.FieldValue{Kind: jira.KindOption, Options: []jira.Option{{ID: "1", Label: "High"}}})
	set = set.With(options, jira.FieldValue{Kind: jira.KindOptions, Options: []jira.Option{{ID: "1", Label: "api"}, {ID: "2", Label: "ui"}}})
	set = set.With(user, jira.FieldValue{Kind: jira.KindUser, Users: []jira.User{{AccountID: "acc-1"}}})
	set = set.With(users, jira.FieldValue{Kind: jira.KindUsers, Users: []jira.User{{AccountID: "acc-1"}, {AccountID: "acc-2"}}})

	if got := set.Len(); got != 10 {
		t.Fatalf("Len() = %d, want 10", got)
	}

	if got, ok := set.Text(text); !ok || got != "a summary" {
		t.Errorf("Text() = (%q, %t), want (\"a summary\", true)", got, ok)
	}
	if got, ok := set.Number(number); !ok || got != 3.5 {
		t.Errorf("Number() = (%v, %t), want (3.5, true)", got, ok)
	}
	if got, ok := set.Get(flag); !ok || !got.Bool {
		t.Errorf("Get() = (%+v, %t), want a true boolean", got, ok)
	}
	if got, ok := set.Date(date); !ok || got.String() != "2026-08-20" {
		t.Errorf("Date() = (%v, %t), want (2026-08-20, true)", got, ok)
	}
	if got, ok := set.Time(stamp); !ok || !got.Equal(when) {
		t.Errorf("Time() = (%s, %t), want (%s, true)", got, ok, when)
	}
	if got, ok := set.Doc(doc); !ok || !sameDoc(t, got, body) {
		t.Errorf("Doc() = (%+v, %t), want the document that was stored", got, ok)
	}
	if got, ok := set.Options(option); !ok || len(got) != 1 || got[0].Label != "High" {
		t.Errorf("Options() = (%+v, %t), want the single stored option", got, ok)
	}
	if got, ok := set.Options(options); !ok || len(got) != 2 {
		t.Errorf("Options() = (%+v, %t), want both stored options", got, ok)
	}
	if got, ok := set.Users(user); !ok || len(got) != 1 || got[0].AccountID != "acc-1" {
		t.Errorf("Users() = (%+v, %t), want the single stored account", got, ok)
	}
	if got, ok := set.Users(users); !ok || len(got) != 2 {
		t.Errorf("Users() = (%+v, %t), want both stored accounts", got, ok)
	}
	if got, ok := set.ByID(number.ID); !ok || got.Number != 3.5 {
		t.Errorf("ByID(%q) = (%+v, %t), want the value stored under that ID", number.ID, got, ok)
	}
}

func TestFieldSet_TypedGetterRefusesTheWrongKind(t *testing.T) {
	t.Parallel()

	var (
		text    = ref("customfield_10001", "string")
		number  = ref("customfield_10016", "number")
		date    = ref("duedate", "date")
		option  = ref("customfield_10004", "option")
		user    = ref("assignee", "user")
		missing = ref("customfield_99999", "string")
	)

	var set jira.FieldSet
	set = set.With(text, jira.FieldValue{Kind: jira.KindText, Text: "a summary"})
	set = set.With(number, jira.FieldValue{Kind: jira.KindNumber, Number: 3.5})
	set = set.With(date, jira.FieldValue{Kind: jira.KindDate, Date: jira.Date{Year: 2026, Month: time.August, Day: 20}})
	set = set.With(option, jira.FieldValue{Kind: jira.KindOption, Options: []jira.Option{{ID: "1", Label: "High"}}})
	set = set.With(user, jira.FieldValue{Kind: jira.KindUser, Users: []jira.User{{AccountID: "acc-1"}}})

	tests := []struct {
		name string
		get  func() bool
	}{
		{"a number read as text", func() bool { _, ok := set.Text(number); return ok }},
		{"text read as a number", func() bool { _, ok := set.Number(text); return ok }},
		{"text read as a document", func() bool { _, ok := set.Doc(text); return ok }},
		{"text read as a date", func() bool { _, ok := set.Date(text); return ok }},
		{"a date read as a timestamp", func() bool { _, ok := set.Time(date); return ok }},
		{"an option read as a user", func() bool { _, ok := set.Users(option); return ok }},
		{"a user read as an option", func() bool { _, ok := set.Options(user); return ok }},
		{"a field that was never set", func() bool { _, ok := set.Get(missing); return ok }},
		{"a field that was never set, read as text", func() bool { _, ok := set.Text(missing); return ok }},
		{"an ID that was never set", func() bool { _, ok := set.ByID("customfield_00000"); return ok }},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			if tt.get() {
				t.Error("the getter reported a value, want false for the wrong kind")
			}
		})
	}
}

func TestFieldSet_ConvertsOnlyWhereTheKindsOverlap(t *testing.T) {
	t.Parallel()

	stamp := ref("customfield_10003", "datetime")
	unknown := ref("customfield_10007", "sd-approvals")

	var set jira.FieldSet
	set = set.With(stamp, jira.FieldValue{Kind: jira.KindTime, Time: time.Date(2026, time.August, 20, 23, 45, 0, 0, time.UTC)})
	set = set.With(unknown, jira.FieldValue{Kind: jira.KindUnknown, Text: "2 approvals"})

	if got, ok := set.Date(stamp); !ok || got.String() != "2026-08-20" {
		t.Errorf("Date() on a timestamp = (%v, %t), want (2026-08-20, true)", got, ok)
	}
	if got, ok := set.Text(unknown); !ok || got != "2 approvals" {
		t.Errorf("Text() on an unmodelled field = (%q, %t), want its display form", got, ok)
	}
}

func TestFieldSet_IDsAreSortedSoIterationIsStable(t *testing.T) {
	t.Parallel()

	var set jira.FieldSet
	for _, id := range []string{"summary", "customfield_10016", "assignee", "customfield_10001", "duedate"} {
		set = set.With(ref(id, "string"), jira.FieldValue{Kind: jira.KindText, Text: id})
	}

	want := []string{"assignee", "customfield_10001", "customfield_10016", "duedate", "summary"}
	if got := set.IDs(); !slices.Equal(got, want) {
		t.Errorf("IDs() = %q, want %q", got, want)
	}
}

func TestFieldSet_IsImmutableSoACopyCannotWriteBackIntoItsSource(t *testing.T) {
	t.Parallel()

	kept := ref("summary", "string")
	added := ref("duedate", "date")

	// A cached issue, as the store or the adapter would hand it back.
	cached := jira.Issue{Key: "PROJ-1"}
	cached.Fields = cached.Fields.With(kept, jira.FieldValue{Kind: jira.KindText, Text: "before"})

	// Seeding an edit form from it the obvious way must not touch the cache.
	patch := jira.IssuePatch{Fields: cached.Fields}
	patch.Fields = patch.Fields.With(kept, jira.FieldValue{Kind: jira.KindText, Text: "after"})
	patch.Fields = patch.Fields.With(added, jira.FieldValue{Kind: jira.KindDate, Date: jira.Date{Year: 2026, Month: time.August, Day: 20}})

	if got, _ := cached.Fields.Text(kept); got != "before" {
		t.Errorf("the cached issue now reads %q, want it untouched at \"before\"", got)
	}
	if _, ok := cached.Fields.Get(added); ok {
		t.Error("a field added to the patch appeared on the cached issue")
	}
	if cached.Fields.Len() != 1 || patch.Fields.Len() != 2 {
		t.Errorf("Len() = %d cached and %d patched, want 1 and 2", cached.Fields.Len(), patch.Fields.Len())
	}

	cached.Fields = cached.Fields.With(kept, jira.FieldValue{Kind: jira.KindText, Text: "changed later"})
	if got, _ := patch.Fields.Text(kept); got != "after" {
		t.Errorf("the patch now reads %q, want it untouched at \"after\"", got)
	}
}

func TestFieldSet_WithoutRemovesOnlyWhatItNames(t *testing.T) {
	t.Parallel()

	kept := ref("summary", "string")
	gone := ref("duedate", "date")

	var set jira.FieldSet
	set = set.With(kept, jira.FieldValue{Kind: jira.KindText, Text: "a summary"})
	set = set.With(gone, jira.FieldValue{Kind: jira.KindDate, Date: jira.Date{Year: 2026, Month: time.August, Day: 20}})

	trimmed := set.Without(gone)
	if _, ok := trimmed.Get(gone); ok {
		t.Error("the removed field is still there")
	}
	if _, ok := trimmed.Get(kept); !ok {
		t.Error("Without removed a field it was not given")
	}
	if _, ok := set.Get(gone); !ok {
		t.Error("Without changed the set it was called on")
	}
	var zero jira.FieldSet
	if got := zero.Without(kept); got.Len() != 0 {
		t.Errorf("removing from the zero set gave %d values, want none", got.Len())
	}
}

func TestNewFieldSet_CopiesTheMapItIsGiven(t *testing.T) {
	t.Parallel()

	field := ref("summary", "string")
	in := map[string]jira.FieldValue{field.ID: {Kind: jira.KindText, Text: "before"}}
	set := jira.NewFieldSet(in)

	in[field.ID] = jira.FieldValue{Kind: jira.KindText, Text: "after"}
	if got, _ := set.Text(field); got != "before" {
		t.Errorf("the set reads %q; it kept a reference to the caller's map", got)
	}
	if jira.NewFieldSet(nil).Len() != 0 {
		t.Error("a set built from nil is not empty")
	}
}

func TestFieldSet_ZeroValueIsReadable(t *testing.T) {
	t.Parallel()

	var set jira.FieldSet
	field := ref("summary", "string")

	if set.Len() != 0 {
		t.Errorf("Len() = %d, want 0", set.Len())
	}
	if got := set.IDs(); len(got) != 0 {
		t.Errorf("IDs() = %q, want none", got)
	}
	if _, ok := set.Get(field); ok {
		t.Error("Get() found a value in the zero set")
	}
	if _, ok := set.Text(field); ok {
		t.Error("Text() found a value in the zero set")
	}

	set = set.With(field, jira.FieldValue{Kind: jira.KindText, Text: "a summary"})
	if got, ok := set.Text(field); !ok || got != "a summary" {
		t.Errorf("Text() = (%q, %t) after the first Set, want (\"a summary\", true)", got, ok)
	}
}

func capsWithout(t *testing.T, absent jira.CapabilityKey, reason string) jira.Capabilities {
	t.Helper()

	caps := jira.Capabilities{
		Plans:        jira.Capability{OK: true},
		BulkMove:     jira.Capability{OK: true},
		Boards:       jira.Capability{OK: true},
		Attachments:  jira.Capability{OK: true},
		DeleteIssues: jira.Capability{OK: true},
	}
	missing := jira.Capability{Reason: reason}
	switch absent {
	case jira.CapPlans:
		caps.Plans = missing
	case jira.CapBulkMove:
		caps.BulkMove = missing
	case jira.CapBoards:
		caps.Boards = missing
	case jira.CapAttachments:
		caps.Attachments = missing
	case jira.CapDeleteIssues:
		caps.DeleteIssues = missing
	default:
		t.Fatalf("capsWithout does not know the capability %q", absent)
	}
	return caps
}

func TestCapabilities_AnswerForEveryProbedKey(t *testing.T) {
	t.Parallel()

	keys := []jira.CapabilityKey{
		jira.CapPlans,
		jira.CapBulkMove,
		jira.CapBoards,
		jira.CapAttachments,
		jira.CapDeleteIssues,
	}

	for _, absent := range keys {
		t.Run(string(absent), func(t *testing.T) {
			t.Parallel()

			reason := "the token lacks the permission behind " + string(absent)
			caps := capsWithout(t, absent, reason)

			if got := caps.Capability(absent); got.OK || got.Reason != reason {
				t.Errorf("Capability(%q) = %+v, want it absent with the probe's reason", absent, got)
			}
			if caps.Allows(absent) {
				t.Errorf("Allows(%q) = true, want false", absent)
			}

			err := caps.Require(absent)
			var capErr *jira.CapabilityError
			if !errors.As(err, &capErr) {
				t.Fatalf("Require(%q) = %v, want a *jira.CapabilityError", absent, err)
			}
			if capErr.Capability != absent || capErr.Reason != reason {
				t.Errorf("Require(%q) carried %+v, want the key and the probe's reason", absent, capErr)
			}
			if capErr.Error() != reason {
				t.Errorf("Error() = %q, want the probe's reason %q", capErr.Error(), reason)
			}

			for _, other := range keys {
				if other == absent {
					continue
				}
				if !caps.Allows(other) {
					t.Errorf("Allows(%q) = false, want the other capabilities left alone", other)
				}
				if err := caps.Require(other); err != nil {
					t.Errorf("Require(%q) = %v, want nil", other, err)
				}
			}
		})
	}
}

func TestCapabilities_TreatAnUnknownKeyAsUnavailable(t *testing.T) {
	t.Parallel()

	caps := capsWithout(t, jira.CapPlans, "needs Administer Jira")
	const typo jira.CapabilityKey = "bulkmove"

	got := caps.Capability(typo)
	if got.OK {
		t.Error("Capability() reported an unknown key as available; a typo must never unlock a view")
	}
	if want := `unknown capability "bulkmove"`; got.Reason != want {
		t.Errorf("Reason = %q, want %q", got.Reason, want)
	}
	if caps.Allows(typo) {
		t.Error("Allows() reported an unknown key as available")
	}

	var capErr *jira.CapabilityError
	if err := caps.Require(typo); !errors.As(err, &capErr) {
		t.Fatalf("Require() = %v, want a *jira.CapabilityError", err)
	}
	if capErr.Capability != typo {
		t.Errorf("Require() carried the key %q, want %q", capErr.Capability, typo)
	}
}

func TestCapabilities_LocationFallsBackToUTC(t *testing.T) {
	t.Parallel()

	if got := (jira.Capabilities{}).Location(); got != time.UTC {
		t.Errorf("Location() = %s on an unprobed timezone, want UTC", got)
	}

	loc := location(t, "Europe/Berlin")
	if got := (jira.Capabilities{TimeZone: loc}).Location(); got != loc {
		t.Errorf("Location() = %s, want the account's %s", got, loc)
	}
}

func TestCapabilities_ZoneCarriesTheReasonItIsNotTheAccounts(t *testing.T) {
	t.Parallel()

	berlin := location(t, "Europe/Berlin")

	tests := []struct {
		name       string
		caps       jira.Capabilities
		wantZone   *time.Location
		wantReason string
	}{
		{
			name:     "the account's own zone comes back with nothing to explain",
			caps:     jira.Capabilities{TimeZone: berlin, TimeZoneReason: "ignored while there is a zone"},
			wantZone: berlin,
		},
		{
			name:       "a probe that failed says so beside UTC",
			caps:       jira.Capabilities{TimeZoneReason: "Jira did not answer what timezone this account is in"},
			wantZone:   time.UTC,
			wantReason: "Jira did not answer what timezone this account is in",
		},
		{
			name:     "an unprobed value is UTC with nothing claimed about it",
			caps:     jira.Capabilities{},
			wantZone: time.UTC,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			gotZone, gotReason := tt.caps.Zone()
			if gotZone != tt.wantZone {
				t.Errorf("Zone() = %s, want %s", gotZone, tt.wantZone)
			}
			if gotReason != tt.wantReason {
				t.Errorf("Zone() reason = %q, want %q", gotReason, tt.wantReason)
			}
			if gotZone != tt.caps.Location() {
				t.Errorf("Zone() and Location() disagree: %s and %s", gotZone, tt.caps.Location())
			}
		})
	}
}

// TestCapabilities_StaysComparable pins what keeps the whole value object usable
// as a probe result: the adapter compares it against the zero value to say that
// a rejected credential produced no answer at all.
func TestCapabilities_StaysComparable(t *testing.T) {
	t.Parallel()

	var unprobed, alsoUnprobed jira.Capabilities
	if unprobed != alsoUnprobed {
		t.Fatal("two unprobed values compare unequal")
	}
	withReason := jira.Capabilities{TimeZoneReason: "Jira did not say what timezone this account is in"}
	if withReason == unprobed {
		t.Error("a value carrying a timezone reason compares equal to an unprobed one")
	}
}

func TestNewFieldMask_DropsBlanksAndRepeatsAndSorts(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		in       []string
		want     []string
		wantWide bool
	}{
		{name: "nothing asked for", in: nil, want: []string{}},
		{name: "blanks are not fields", in: []string{"", "   ", "\t"}, want: []string{}},
		{name: "sorted so two equal reads compare equal", in: []string{"status", "assignee", "summary"}, want: []string{"assignee", "status", "summary"}},
		{name: "a repeat was still asked for once", in: []string{"summary", "summary"}, want: []string{"summary"}},
		{name: "surrounding space is not part of an ID", in: []string{" summary ", "customfield_11101"}, want: []string{"customfield_11101", "summary"}},
		{name: "*all is every field there is", in: []string{"summary", jira.FieldsAll}, want: []string{}, wantWide: true},
		{name: "*navigable is wide too", in: []string{jira.FieldsNavigable}, want: []string{}, wantWide: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			got := jira.NewFieldMask(tt.in)
			if !slices.Equal(got.IDs(), tt.want) {
				t.Errorf("IDs() = %q, want %q", got.IDs(), tt.want)
			}
			if got.Wide() != tt.wantWide {
				t.Errorf("Wide() = %v, want %v", got.Wide(), tt.wantWide)
			}
			if got.Len() != len(tt.want) {
				t.Errorf("Len() = %d, want %d", got.Len(), len(tt.want))
			}
		})
	}
}

func TestFieldMask_HasAnswersForWhatWasAskedFor(t *testing.T) {
	t.Parallel()

	narrow := jira.NewFieldMask([]string{"summary", "status"})
	if !narrow.Has("summary") || !narrow.Has("status") {
		t.Error("a field the read named is not in the mask")
	}
	if narrow.Has("assignee") {
		t.Error("a field the read never named is in the mask, so a nil assignee reads as unassigned")
	}

	wide := jira.AllFields()
	if !wide.Has("assignee") || !wide.Has("a-field-nothing-has-enumerated") {
		t.Error("a wide mask must answer for every field, including ones no client can enumerate")
	}
	if wide.Len() != 0 || len(wide.IDs()) != 0 {
		t.Errorf("a wide mask names %d fields; the list is the site's, not the caller's", wide.Len())
	}

	var unread jira.FieldMask
	if unread.Has("summary") || unread.Wide() {
		t.Error("the zero mask asked for something; an issue that came from no read must refuse a write")
	}
}

func TestFieldMask_IsImmutableSoAReaderCannotWidenIt(t *testing.T) {
	t.Parallel()

	mask := jira.NewFieldMask([]string{"summary", "assignee"})
	ids := mask.IDs()
	ids[0] = "labels"

	if mask.Has("labels") {
		t.Error("writing into the slice IDs() handed back changed the mask, so a cached issue can be told it read a field it did not")
	}
	if !slices.Equal(mask.IDs(), []string{"assignee", "summary"}) {
		t.Errorf("IDs() = %q after a caller wrote into an earlier copy", mask.IDs())
	}
}

// TestFieldMask_CostsNothingToCarryOnAnIssue is why the mask is a sorted slice
// behind a value type rather than a map or a per-issue copy: an adapter builds
// one per read and hands the same one to every issue on the page, so a page of
// ten thousand rows pays for it once.
func TestFieldMask_CostsNothingToCarryOnAnIssue(t *testing.T) {
	mask := jira.NewFieldMask([]string{"summary", "status", "assignee", "priority", "updated", "issuetype"})
	issues := make([]jira.Issue, 512)

	carry := testing.AllocsPerRun(50, func() {
		for i := range issues {
			issues[i].Requested = mask
		}
	})
	if carry != 0 {
		t.Errorf("carrying the mask onto %d issues cost %.0f allocations, want none", len(issues), carry)
	}

	read := testing.AllocsPerRun(50, func() {
		for i := range issues {
			if !issues[i].Requested.Has("assignee") || issues[i].Requested.Has("labels") {
				t.Fatal("the mask lost what it was asked for")
			}
		}
	})
	if read != 0 {
		t.Errorf("reading the mask %d times cost %.0f allocations, want none", len(issues), read)
	}
}

func TestIssuePatch_IsEmptyOnlyWhenItWouldChangeNothing(t *testing.T) {
	t.Parallel()

	var withField jira.FieldSet
	withField = withField.With(ref("customfield_10016", "number"), jira.FieldValue{Kind: jira.KindNumber, Number: 5})

	tests := []struct {
		name  string
		patch jira.IssuePatch
		want  bool
	}{
		{name: "an untouched patch", patch: jira.IssuePatch{}, want: true},
		{name: "a patch that only asks not to notify", patch: jira.IssuePatch{Notify: ptr(false)}, want: true},
		{name: "a new summary", patch: jira.IssuePatch{Summary: ptr("a summary")}},
		{name: "an emptied summary", patch: jira.IssuePatch{Summary: ptr("")}},
		{name: "a new description", patch: jira.IssuePatch{Description: ptr(adf.NewDoc())}},
		{name: "a new assignee", patch: jira.IssuePatch{Assignee: ptr("acc-1")}},
		{name: "an unassignment", patch: jira.IssuePatch{Assignee: ptr("")}},
		{name: "emptied labels", patch: jira.IssuePatch{Labels: ptr([]string{})}},
		{name: "a new priority", patch: jira.IssuePatch{PriorityID: ptr("3")}},
		{name: "a new due date", patch: jira.IssuePatch{Due: ptr(jira.Date{Year: 2026, Month: time.August, Day: 20})}},
		{name: "a custom field", patch: jira.IssuePatch{Fields: withField}},
		{name: "a field to clear", patch: jira.IssuePatch{Clear: []jira.FieldRef{ref("duedate", "date")}}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			if got := tt.patch.IsEmpty(); got != tt.want {
				t.Errorf("IsEmpty() = %t, want %t", got, tt.want)
			}
		})
	}
}

func TestSchema_RequiredKeepsOnlyTheMandatoryFieldsInOrder(t *testing.T) {
	t.Parallel()

	schema := jira.Schema{
		Project:   jira.ProjectRef{Key: "PROJ"},
		IssueType: jira.IssueType{ID: "10001", Name: "Story"},
		Fields: []jira.FieldMeta{
			{Name: "Summary", Required: true},
			{Name: "Description"},
			{Name: "Story Points"},
			{Name: "Team", Required: true},
		},
	}

	got := schema.Required()
	want := []string{"Summary", "Team"}
	names := make([]string, 0, len(got))
	for i := range got {
		names = append(names, got[i].Name)
	}
	if !slices.Equal(names, want) {
		t.Errorf("Required() = %q, want %q in catalogue order", names, want)
	}

	if none := (jira.Schema{Fields: []jira.FieldMeta{{Name: "Summary"}}}).Required(); len(none) != 0 {
		t.Errorf("Required() = %d fields, want none when nothing is mandatory", len(none))
	}
	if empty := (jira.Schema{}).Required(); len(empty) != 0 {
		t.Errorf("Required() = %d fields, want none for an empty schema", len(empty))
	}
}

func TestTaskState_DoneOnlyWhenTheTaskHasStopped(t *testing.T) {
	t.Parallel()

	tests := []struct {
		state jira.TaskState
		want  bool
	}{
		{state: jira.TaskEnqueued, want: false},
		{state: jira.TaskRunning, want: false},
		{state: jira.TaskComplete, want: true},
		{state: jira.TaskFailed, want: true},
		{state: jira.TaskCancelled, want: true},
		{state: jira.TaskDead, want: true},
		{state: jira.TaskState(""), want: false},
		{state: jira.TaskState("PAUSED"), want: false},
	}

	for _, tt := range tests {
		t.Run(string(tt.state), func(t *testing.T) {
			t.Parallel()

			if got := tt.state.Done(); got != tt.want {
				t.Errorf("Done() = %t, want %t", got, tt.want)
			}
		})
	}
}

func TestGraphicsMode_StringNamesEveryMode(t *testing.T) {
	t.Parallel()

	tests := []struct {
		mode jira.GraphicsMode
		want string
	}{
		{mode: jira.GraphicsKitty, want: "kitty"},
		{mode: jira.GraphicsITerm2, want: "iterm2"},
		{mode: jira.GraphicsHalfBlocks, want: "halfblocks"},
		{mode: jira.GraphicsNone, want: "none"},
		{mode: jira.GraphicsMode(42), want: "none"},
	}

	for _, tt := range tests {
		t.Run(tt.want, func(t *testing.T) {
			t.Parallel()

			if got := tt.mode.String(); got != tt.want {
				t.Errorf("String() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestFieldByName_IsCaseInsensitiveAndTakesTheFirstMatch(t *testing.T) {
	t.Parallel()

	catalogue := []jira.Field{
		{ID: "summary", Name: "Summary"},
		{ID: "customfield_10016", Name: "Story Points", Custom: true, Schema: jira.FieldSchema{Type: "number", CustomID: 10016}},
		{ID: "customfield_10020", Name: "Story points", Custom: true, Schema: jira.FieldSchema{Type: "number", CustomID: 10020}},
	}

	tests := []struct {
		name   string
		lookup string
		wantID string
		found  bool
	}{
		{name: "an exact name", lookup: "Summary", wantID: "summary", found: true},
		{name: "a name in the wrong case", lookup: "sUmMaRy", wantID: "summary", found: true},
		{name: "a name two fields share", lookup: "story points", wantID: "customfield_10016", found: true},
		{name: "a name no field has", lookup: "Sprint", found: false},
		{name: "no name at all", lookup: "", found: false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			got, ok := jira.FieldByName(catalogue, tt.lookup)
			if ok != tt.found {
				t.Fatalf("FieldByName(%q) found = %t, want %t", tt.lookup, ok, tt.found)
			}
			if !tt.found {
				if got.ID != "" {
					t.Errorf("FieldByName(%q) = %+v, want the zero Field", tt.lookup, got)
				}
				return
			}
			if got.ID != tt.wantID {
				t.Errorf("FieldByName(%q) = %q, want %q", tt.lookup, got.ID, tt.wantID)
			}
		})
	}

	if _, ok := jira.FieldByName(nil, "Summary"); ok {
		t.Error("FieldByName found a field in an empty catalogue")
	}
}

func TestField_RefCarriesTheIDNameAndSchema(t *testing.T) {
	t.Parallel()

	field := jira.Field{
		ID:     "customfield_10016",
		Key:    "customfield_10016",
		Name:   "Story Points",
		Custom: true,
		Schema: jira.FieldSchema{Type: "number", Custom: "com.pyxis.greenhopper.jira:gh-sprint", CustomID: 10016},
	}

	want := jira.FieldRef{ID: field.ID, Name: field.Name, Schema: field.Schema}
	if got := field.Ref(); got != want {
		t.Errorf("Ref() = %+v, want %+v", got, want)
	}
}

func TestFileFromPath_DescribesTheFileAndReopensItPerAttempt(t *testing.T) {
	t.Parallel()

	const content = "an attachment\n"
	path := filepath.Join(t.TempDir(), "notes.txt")
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatalf("writing the fixture: %v", err)
	}

	file, err := jira.FileFromPath(path)
	if err != nil {
		t.Fatalf("FileFromPath: %v", err)
	}
	if file.Name != "notes.txt" {
		t.Errorf("Name = %q, want the base name", file.Name)
	}
	if file.Size != int64(len(content)) {
		t.Errorf("Size = %d, want %d", file.Size, len(content))
	}
	for attempt := range 2 {
		if got := readAll(t, file); got != content {
			t.Errorf("attempt %d read %q, want %q", attempt+1, got, content)
		}
	}
}

func TestFileFromPath_RefusesAPathThatIsNotThere(t *testing.T) {
	t.Parallel()

	path := filepath.Join(t.TempDir(), "missing.txt")

	file, err := jira.FileFromPath(path)
	if err == nil {
		t.Fatalf("FileFromPath(%q) = %+v, want an error", path, file)
	}
	if !errors.Is(err, fs.ErrNotExist) {
		t.Errorf("FileFromPath(%q) = %v, want it to wrap fs.ErrNotExist", path, err)
	}
	if file.Open != nil || file.Name != "" {
		t.Errorf("FileFromPath(%q) = %+v alongside its error, want the zero FileRef", path, file)
	}
}

func readAll(t *testing.T, file jira.FileRef) string {
	t.Helper()

	reader, err := file.Open()
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer func() {
		if err := reader.Close(); err != nil {
			t.Errorf("Close: %v", err)
		}
	}()
	content, err := io.ReadAll(reader)
	if err != nil {
		t.Fatalf("reading %s: %v", file.Name, err)
	}
	return string(content)
}

func sameDoc(t *testing.T, got, want adf.Doc) bool {
	t.Helper()

	gotJSON, err := adf.Marshal(got)
	if err != nil {
		t.Fatalf("marshalling the document that was read back: %v", err)
	}
	wantJSON, err := adf.Marshal(want)
	if err != nil {
		t.Fatalf("marshalling the document that was stored: %v", err)
	}
	return bytes.Equal(gotJSON, wantJSON)
}

func TestFieldSet_HandsOutCopiesOfTheSlicesInsideAValue(t *testing.T) {
	t.Parallel()

	labels := ref("customfield_13500", "array")
	stored := jira.FieldValue{Kind: jira.KindOptions, Options: []jira.Option{{ID: "1", Label: "api"}}}

	var set jira.FieldSet
	set = set.With(labels, stored)

	// Writing through the value that was stored must not reach the set.
	stored.Options[0].Label = "written through the input"
	if got, _ := set.Options(labels); got[0].Label != "api" {
		t.Errorf("the set now reads %q; With kept the caller's slice", got[0].Label)
	}

	// Nor may writing through a value the set handed back.
	got, _ := set.Options(labels)
	got[0].Label = "written through the output"
	if again, _ := set.Options(labels); again[0].Label != "api" {
		t.Errorf("the set now reads %q; a reader wrote into it", again[0].Label)
	}
}

func TestFieldSet_HandsOutACopyOfARichTextValue(t *testing.T) {
	t.Parallel()

	body := ref("customfield_13600", "doc")
	doc, err := adf.Unmarshal([]byte(`{"version":1,"type":"doc","content":[{"type":"paragraph","content":[{"type":"text","text":"original"}]}]}`))
	if err != nil {
		t.Fatalf("unmarshal: %v", err)
	}

	var set jira.FieldSet
	set = set.With(body, jira.FieldValue{Kind: jira.KindDoc, Doc: doc})

	// Writing through the document that was stored must not reach the set.
	doc.Content[0].Content[0].Text = "written through the input"
	got, _ := set.Doc(body)
	if got.Content[0].Content[0].Text != "original" {
		t.Errorf("the set reads %q; With kept the caller's document", got.Content[0].Content[0].Text)
	}

	// Nor may writing through a document the set handed back.
	got.Content[0].Content[0].Text = "written through the output"
	again, _ := set.Doc(body)
	if again.Content[0].Content[0].Text != "original" {
		t.Errorf("the set reads %q; a reader wrote into it", again.Content[0].Content[0].Text)
	}
}

func TestFieldByName_PrefersTheUntranslatedNameBecauseNameIsLocalised(t *testing.T) {
	t.Parallel()

	// What a German site sends: the display name is translated, the untranslated
	// one is not, and configuration naming "Story Points" has to keep working.
	fields := []jira.Field{
		{ID: "summary", Name: "Zusammenfassung"},
		{ID: "customfield_10032", Name: "Story-Punkte", UntranslatedName: "Story Points", Custom: true},
		{ID: "customfield_10041", Name: "Zieltermin Start", UntranslatedName: "Target start", Custom: true},
	}

	for name, want := range map[string]string{
		"Story Points":    "customfield_10032",
		"story points":    "customfield_10032",
		"Target start":    "customfield_10041",
		"Zusammenfassung": "summary",
		"Story-Punkte":    "customfield_10032",
	} {
		got, ok := FieldByNameHelper(t, fields, name)
		if !ok || got.ID != want {
			t.Errorf("FieldByName(%q) = %q, %t; want %q", name, got.ID, ok, want)
		}
	}

	if _, ok := jira.FieldByName(fields, ""); ok {
		t.Error("an empty name matched a field; every system field has an empty untranslated name")
	}
	if _, ok := jira.FieldByName(fields, "   "); ok {
		t.Error("a blank name matched a field")
	}
	if _, ok := jira.FieldByName(fields, "Sprint"); ok {
		t.Error("a name nothing carries matched anyway")
	}
}

// FieldByNameHelper keeps the table above readable.
func FieldByNameHelper(t *testing.T, fields []jira.Field, name string) (jira.Field, bool) {
	t.Helper()
	return jira.FieldByName(fields, name)
}
