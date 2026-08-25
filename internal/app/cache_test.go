package app

import (
	"fmt"
	"path/filepath"
	"slices"
	"testing"
	"time"

	"github.com/varijkapil13/saral/internal/store"
	"github.com/varijkapil13/saral/pkg/adf"
	"github.com/varijkapil13/saral/pkg/jira"
	"github.com/varijkapil13/saral/pkg/jira/jiratest"
)

const cacheJQL = `project = "PROJ" ORDER BY key`

var (
	testScope = store.Scope{Site: "example.atlassian.net", Account: "you@example.com"}
	testNow   = time.Date(2026, time.March, 2, 9, 0, 0, 0, time.UTC)
)

// clock is a hand-wound clock. docs/TESTING.md forbids a sleep, and every TTL in
// this file is checked by moving this instead.
type clock struct{ at time.Time }

func (c *clock) now() time.Time { return c.at }

func openDB(t testing.TB) *store.DB {
	t.Helper()

	db, err := store.Open(filepath.Join(t.TempDir(), "cache.db"))
	if err != nil {
		t.Fatalf("opening the cache: %v", err)
	}
	t.Cleanup(func() {
		if err := db.Close(); err != nil {
			t.Errorf("closing the cache: %v", err)
		}
	})
	return db
}

func newTestCache(t testing.TB, opts ...CacheOption) (*DiskCache, *clock) {
	t.Helper()

	c := &clock{at: testNow}
	return NewCache(openDB(t), testScope, append([]CacheOption{WithClock(c.now)}, opts...)...), c
}

// listRows is what the list view stores: the six fields of ListProjection and a
// mask saying so.
func listRows(n int) []jira.Issue {
	mask := jira.NewFieldMask(ListProjection().IDs)
	out := jiratest.Gen(n)
	for i := range out {
		out[i].Requested = mask
		out[i].Description = adf.Doc{}
	}
	return out
}

func keysOf(issues []jira.Issue) []string {
	out := make([]string, 0, len(issues))
	for i := range issues {
		out = append(out, issues[i].Key)
	}
	return out
}

func TestKindTTL_IsTheTableTheProjectPublishes(t *testing.T) {
	t.Parallel()

	want := map[Kind]time.Duration{
		KindFields:      24 * time.Hour,
		KindCreateMeta:  24 * time.Hour,
		KindBoardConfig: time.Hour,
		KindVersions:    10 * time.Minute,
		KindIssue:       60 * time.Second,
		KindSearch:      30 * time.Second,
	}
	for kind, ttl := range want {
		if got := kind.TTL(); got != ttl {
			t.Errorf("%s lives for %s, want %s", kind, got, ttl)
		}
	}
	if got := Kind("something else").TTL(); got != 0 {
		t.Errorf("an unknown kind claims a TTL of %s; nothing should be kept under a name nobody defined", got)
	}
}

func TestRows_ComeBackAsTheyWereStored(t *testing.T) {
	t.Parallel()

	cache, _ := newTestCache(t)
	stored := listRows(3)
	if err := cache.PutRows(cacheJQL, stored, true); err != nil {
		t.Fatalf("PutRows: %v", err)
	}

	got, ok := cache.Rows(cacheJQL)
	if !ok {
		t.Fatal("the rows just stored are not there")
	}
	if !slices.Equal(keysOf(got.Issues), keysOf(stored)) {
		t.Errorf("the rows came back as %v, want %v in that order", keysOf(got.Issues), keysOf(stored))
	}
	if got.Issues[0].Summary != stored[0].Summary {
		t.Errorf("the first row came back summarised as %q", got.Issues[0].Summary)
	}
	if !got.More {
		t.Error("a search stored with another page behind it came back looking complete")
	}
	if got.Stale {
		t.Error("rows stored this instant came back stale")
	}
	if !got.StoredAt.Equal(testNow) {
		t.Errorf("the rows say they were stored at %s, want %s", got.StoredAt, testNow)
	}
}

func TestRows_AreAMissWhenNothingWasStoredForThatSearch(t *testing.T) {
	t.Parallel()

	cache, _ := newTestCache(t)
	if err := cache.PutRows(cacheJQL, listRows(2), false); err != nil {
		t.Fatalf("PutRows: %v", err)
	}
	for _, jql := range []string{"", "   ", `project = "OTHER"`, cacheJQL + " AND assignee IS EMPTY"} {
		if _, ok := cache.Rows(jql); ok {
			t.Errorf("%q found rows stored under another question", jql)
		}
	}
	if _, ok := cache.Rows("  " + cacheJQL + "\n"); !ok {
		t.Error("the same question with whitespace around it missed; a key is the trimmed JQL")
	}
}

func TestRows_TurnStaleOnceTheSearchTTLHasPassed(t *testing.T) {
	t.Parallel()

	cache, clk := newTestCache(t)
	if err := cache.PutRows(cacheJQL, listRows(2), false); err != nil {
		t.Fatalf("PutRows: %v", err)
	}

	clk.at = testNow.Add(KindSearch.TTL())
	if got, _ := cache.Rows(cacheJQL); got.Stale {
		t.Error("rows exactly at their TTL are still current")
	}
	clk.at = testNow.Add(KindSearch.TTL() + time.Second)
	got, ok := cache.Rows(cacheJQL)
	if !ok {
		t.Fatal("stale rows were dropped rather than badged; seeing yesterday's rows beats seeing none")
	}
	if !got.Stale {
		t.Error("rows past their TTL did not come back stale")
	}
	if len(got.Issues) != 2 {
		t.Errorf("stale rows came back %d long, want the 2 that were stored", len(got.Issues))
	}
}

func TestRows_StayInsideTheProfileThatStoredThem(t *testing.T) {
	t.Parallel()

	db := openDB(t)
	clk := &clock{at: testNow}
	mine := NewCache(db, testScope, WithClock(clk.now))
	theirs := NewCache(db, store.Scope{Site: testScope.Site, Account: "someone.else@example.com"}, WithClock(clk.now))

	if err := mine.PutRows(cacheJQL, listRows(3), false); err != nil {
		t.Fatalf("PutRows: %v", err)
	}
	if _, ok := theirs.Rows(cacheJQL); ok {
		t.Error("one account read another's rows off the same file")
	}
	seen := 0
	if err := theirs.EachIssue(func(jira.Issue, time.Time) bool { seen++; return true }); err != nil {
		t.Fatalf("EachIssue: %v", err)
	}
	if seen != 0 {
		t.Errorf("another account's cache holds %d of this one's issues", seen)
	}

	otherSite := NewCache(db, store.Scope{Site: "other.atlassian.net", Account: testScope.Account}, WithClock(clk.now))
	if _, ok := otherSite.Rows(cacheJQL); ok {
		t.Error("one account on two sites shares one cache; the two sites are different Jiras")
	}
}

func TestPutRows_DropsTheIssuesStoredLongestAgoOnceItIsOverTheBound(t *testing.T) {
	t.Parallel()

	cache, clk := newTestCache(t, WithIssueBound(4))
	first := listRows(6)[:3]
	if err := cache.PutRows(`project = "PROJ" AND key >= "PROJ-1"`, first, false); err != nil {
		t.Fatalf("PutRows: %v", err)
	}

	clk.at = testNow.Add(time.Minute)
	second := jiratest.GenFor("OTHER", 3)
	mask := jira.NewFieldMask(ListProjection().IDs)
	for i := range second {
		second[i].Requested = mask
	}
	if err := cache.PutRows(`project = "OTHER"`, second, false); err != nil {
		t.Fatalf("PutRows: %v", err)
	}

	var held []string
	if err := cache.EachIssue(func(iss jira.Issue, _ time.Time) bool {
		held = append(held, iss.Key)
		return true
	}); err != nil {
		t.Fatalf("EachIssue: %v", err)
	}
	if len(held) != 4 {
		t.Fatalf("the cache holds %d issues (%v), want the bound of 4", len(held), held)
	}
	for _, key := range keysOf(second) {
		if !slices.Contains(held, key) {
			t.Errorf("%s was evicted although it was stored last", key)
		}
	}
	if slices.Contains(held, "PROJ-1") {
		t.Error("PROJ-1 survived; the bound drops what was stored longest ago")
	}
}

func TestForget_DropsASearchAndKeepsTheIssuesItMatched(t *testing.T) {
	t.Parallel()

	cache, _ := newTestCache(t)
	stored := listRows(3)
	if err := cache.PutRows(cacheJQL, stored, false); err != nil {
		t.Fatalf("PutRows: %v", err)
	}
	if err := cache.Forget(cacheJQL); err != nil {
		t.Fatalf("Forget: %v", err)
	}
	if _, ok := cache.Rows(cacheJQL); ok {
		t.Error("the search survived being forgotten")
	}

	held := 0
	if err := cache.EachIssue(func(jira.Issue, time.Time) bool { held++; return true }); err != nil {
		t.Fatalf("EachIssue: %v", err)
	}
	if held != len(stored) {
		t.Errorf("forgetting one search left %d of %d issues; the issues are shared with every other search",
			held, len(stored))
	}
}

func TestEachIssue_VisitsEveryIssueInKeyOrderAndStopsWhenAsked(t *testing.T) {
	t.Parallel()

	cache, _ := newTestCache(t)
	if err := cache.PutRows(cacheJQL, listRows(4), false); err != nil {
		t.Fatalf("PutRows: %v", err)
	}

	var seen []string
	if err := cache.EachIssue(func(iss jira.Issue, storedAt time.Time) bool {
		if !storedAt.Equal(testNow) {
			t.Errorf("%s says it was stored at %s", iss.Key, storedAt)
		}
		seen = append(seen, iss.Key)
		return true
	}); err != nil {
		t.Fatalf("EachIssue: %v", err)
	}
	want := []string{"PROJ-1", "PROJ-2", "PROJ-3", "PROJ-4"}
	if !slices.Equal(seen, want) {
		t.Errorf("EachIssue visited %v, want %v", seen, want)
	}

	seen = nil
	if err := cache.EachIssue(func(iss jira.Issue, _ time.Time) bool {
		seen = append(seen, iss.Key)
		return false
	}); err != nil {
		t.Fatalf("EachIssue: %v", err)
	}
	if len(seen) != 1 {
		t.Errorf("a walk told to stop visited %d issues", len(seen))
	}
}

// TestGeneration_TellsAnIndexBuiltFromTheCacheThatItIsBehind is the shape P3.4
// reads this cache with: walk it once, then notice a write without walking again.
func TestGeneration_TellsAnIndexBuiltFromTheCacheThatItIsBehind(t *testing.T) {
	t.Parallel()

	cache, _ := newTestCache(t)
	if err := cache.PutRows(cacheJQL, listRows(2), false); err != nil {
		t.Fatalf("PutRows: %v", err)
	}

	indexed := cache.Generation()
	var index []string
	if err := cache.EachIssue(func(iss jira.Issue, _ time.Time) bool {
		index = append(index, iss.Key)
		return true
	}); err != nil {
		t.Fatalf("EachIssue: %v", err)
	}
	if cache.Generation() != indexed {
		t.Error("reading the cache moved its generation; only a write may")
	}

	if err := cache.PutRows(`project = "OTHER"`, listRows(3), false); err != nil {
		t.Fatalf("second PutRows: %v", err)
	}
	if cache.Generation() == indexed {
		t.Fatal("a write left the generation where it was, so an index built before it has no way to know")
	}
	if cache.Generation() < indexed {
		t.Error("the generation went backwards")
	}
	if err := cache.Forget(cacheJQL); err != nil {
		t.Fatalf("Forget: %v", err)
	}
	if cache.Generation() == indexed+1 {
		t.Error("dropping a search left the generation where the write before it put it")
	}
	_ = index
}

func TestNilCache_AnswersEveryCallWithoutPanicking(t *testing.T) {
	t.Parallel()

	var cache *DiskCache
	if _, ok := cache.Rows(cacheJQL); ok {
		t.Error("a cache that does not exist found rows")
	}
	if err := cache.PutRows(cacheJQL, listRows(1), false); err != nil {
		t.Errorf("storing into a cache that does not exist failed: %v", err)
	}
	if err := cache.Forget(cacheJQL); err != nil {
		t.Errorf("forgetting in a cache that does not exist failed: %v", err)
	}
	if err := cache.EachIssue(func(jira.Issue, time.Time) bool { return true }); err != nil {
		t.Errorf("walking a cache that does not exist failed: %v", err)
	}
	if got := cache.Generation(); got != 0 {
		t.Errorf("a cache that does not exist is at generation %d", got)
	}
}

// TestPutRows_LeavesAloneTheFieldsANarrowRefreshNeverAskedAbout is the case PC.1
// built Issue.Requested for: a list row asks for six fields, and storing one must
// not throw away what a wider read put on the same issue.
func TestPutRows_LeavesAloneTheFieldsANarrowRefreshNeverAskedAbout(t *testing.T) {
	t.Parallel()

	cache, clk := newTestCache(t)
	wide := jiratest.Gen(1)[0]
	wide.Requested = jira.AllFields()
	if wide.Assignee == nil || wide.Description.IsZero() || len(wide.Labels) == 0 {
		t.Fatalf("the fixture is not wide enough to prove anything: %+v", wide)
	}
	if err := cache.PutRows(`key = "PROJ-1"`, []jira.Issue{wide}, false); err != nil {
		t.Fatalf("storing the wide read: %v", err)
	}

	narrow := jira.Issue{
		ID:        wide.ID,
		Key:       wide.Key,
		Summary:   "a summary the refresh did bring",
		Status:    wide.Status,
		Requested: jira.NewFieldMask([]string{"summary", "status"}),
	}
	clk.at = testNow.Add(time.Minute)
	if err := cache.PutRows(cacheJQL, []jira.Issue{narrow}, false); err != nil {
		t.Fatalf("storing the narrow read: %v", err)
	}

	got, ok := cache.Rows(cacheJQL)
	if !ok || len(got.Issues) != 1 {
		t.Fatalf("the narrow read did not come back: found %t, %d rows", ok, len(got.Issues))
	}
	merged := got.Issues[0]
	if merged.Summary != narrow.Summary {
		t.Errorf("the refresh's own summary was lost: %q", merged.Summary)
	}
	if merged.Assignee == nil {
		t.Error("a refresh that never mentioned the assignee unassigned the issue")
	}
	if merged.Description.IsZero() {
		t.Error("a refresh that never mentioned the description emptied it")
	}
	if len(merged.Labels) == 0 {
		t.Error("a refresh that never mentioned the labels dropped them")
	}
	if !merged.Requested.Wide() {
		t.Errorf("the merged issue was read with %v, want the wide read it was merged into to stay wide",
			merged.Requested.IDs())
	}
}

func TestMergeIssue(t *testing.T) {
	t.Parallel()

	points := jira.FieldRef{ID: "customfield_20001"}
	rank := jira.FieldRef{ID: "customfield_20002"}
	ada := jira.User{AccountID: "acct-ada", DisplayName: "Ada Lovelace"}

	base := jira.Issue{
		ID: "20001", Key: "PROJ-1",
		Summary:  "as it was",
		Assignee: &ada,
		Labels:   []string{"cache"},
		Fields: jira.NewFieldSet(map[string]jira.FieldValue{
			points.ID: {Kind: jira.KindNumber, Number: 5},
			rank.ID:   {Kind: jira.KindText, Text: "0|i00001:"},
		}),
		Requested: jira.NewFieldMask([]string{"summary", "assignee", "labels", points.ID, rank.ID}),
	}

	tests := []struct {
		name  string
		fresh jira.Issue
		check func(t *testing.T, got jira.Issue)
	}{
		{
			name: "a narrow read keeps what it did not ask about",
			fresh: jira.Issue{
				Key: "PROJ-1", Summary: "as it is now",
				Requested: jira.NewFieldMask([]string{"summary"}),
			},
			check: func(t *testing.T, got jira.Issue) {
				if got.Summary != "as it is now" {
					t.Errorf("summary is %q", got.Summary)
				}
				if got.Assignee == nil || len(got.Labels) != 1 {
					t.Error("a field outside the mask was cleared")
				}
			},
		},
		{
			name: "a read that asked for a field Jira had nothing for clears it",
			fresh: jira.Issue{
				Key: "PROJ-1", Requested: jira.NewFieldMask([]string{"assignee"}),
			},
			check: func(t *testing.T, got jira.Issue) {
				if got.Assignee != nil {
					t.Error("a read that asked about the assignee and came back with none left the old one in place")
				}
				if got.Summary != base.Summary {
					t.Errorf("summary is %q, and nothing asked about it", got.Summary)
				}
			},
		},
		{
			name: "a custom field outside the mask survives",
			fresh: jira.Issue{
				Key:       "PROJ-1",
				Fields:    jira.NewFieldSet(map[string]jira.FieldValue{points.ID: {Kind: jira.KindNumber, Number: 8}}),
				Requested: jira.NewFieldMask([]string{points.ID}),
			},
			check: func(t *testing.T, got jira.Issue) {
				if n, ok := got.Fields.Number(points); !ok || n != 8 {
					t.Errorf("the field the read asked for is %v (found %t)", n, ok)
				}
				if _, ok := got.Fields.Text(rank); !ok {
					t.Error("a custom field the read never named was dropped")
				}
			},
		},
		{
			name: "a custom field the read asked for and Jira had nothing for goes",
			fresh: jira.Issue{
				Key:       "PROJ-1",
				Requested: jira.NewFieldMask([]string{rank.ID}),
			},
			check: func(t *testing.T, got jira.Issue) {
				if _, ok := got.Fields.Text(rank); ok {
					t.Error("a field that was asked for and came back empty kept its old value")
				}
				if _, ok := got.Fields.Number(points); !ok {
					t.Error("a field nobody asked about went with it")
				}
			},
		},
		{
			name: "a wide read replaces everything",
			fresh: jira.Issue{
				Key: "PROJ-1", Summary: "everything", Requested: jira.AllFields(),
			},
			check: func(t *testing.T, got jira.Issue) {
				if got.Assignee != nil || len(got.Labels) != 0 {
					t.Error("a read that asked for every field left something from the old copy behind")
				}
				if !got.Requested.Wide() {
					t.Error("the result of a wide read is not itself wide")
				}
			},
		},
		{
			name:  "a read that asked for nothing changes nothing",
			fresh: jira.Issue{Key: "PROJ-1", Summary: ""},
			check: func(t *testing.T, got jira.Issue) {
				if got.Summary != base.Summary || got.Assignee == nil {
					t.Error("an issue that did not come from a read overwrote one that did")
				}
			},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			tc.check(t, MergeIssue(base, tc.fresh))
		})
	}
}

func TestMergeIssue_TakesTheFreshCopyWholeWhenThereIsNothingToMergeInto(t *testing.T) {
	t.Parallel()

	fresh := jira.Issue{Key: "PROJ-1", Summary: "new", Requested: jira.NewFieldMask([]string{"summary"})}
	if got := MergeIssue(jira.Issue{}, fresh); got.Summary != "new" || got.Key != "PROJ-1" {
		t.Errorf("merging into nothing gave %+v", got)
	}
}

func TestStoredIssue_CarriesEverythingBackThatItTookIn(t *testing.T) {
	t.Parallel()

	cache, _ := newTestCache(t)
	loc, err := time.LoadLocation("Europe/Berlin")
	if err != nil {
		t.Skipf("this machine has no zoneinfo entry for Europe/Berlin: %v", err)
	}
	doc, err := adf.Unmarshal([]byte(`{"version":1,"type":"doc","content":[{"type":"paragraph","content":[{"type":"text","text":"a description"}]}]}`))
	if err != nil {
		t.Fatalf("parsing the description: %v", err)
	}

	want := jira.Issue{
		ID: "20001", Key: "PROJ-1",
		Project:     jira.ProjectRef{ID: "10000", Key: "PROJ", Name: "Project"},
		Summary:     "everything an issue carries",
		Description: doc,
		Type:        jira.IssueType{ID: "10301", Name: "Story"},
		Status:      jira.Status{ID: "10202", Name: "Building", Category: jira.CategoryInProgress},
		Priority:    &jira.Priority{ID: "10402", Name: "Normal"},
		Resolution:  &jira.Resolution{ID: "10501", Name: "Delivered"},
		Assignee: &jira.User{
			AccountID: "acct-ada", DisplayName: "Ada Lovelace",
			Email: "ada@example.com", TimeZone: loc, Active: true,
		},
		Reporter:    &jira.User{AccountID: "acct-grace", DisplayName: "Grace Hopper"},
		Labels:      []string{"cache", "offline"},
		Components:  []jira.Component{{ID: "10600", Name: "Storage"}},
		FixVersions: []jira.Version{{ID: "10700", Name: "1.2.0", ReleaseDate: jira.Date{Year: 2026, Month: time.April, Day: 1}}},
		Parent:      &jira.IssueRef{ID: "20000", Key: "PROJ-0", Summary: "an epic"},
		Subtasks:    []jira.IssueRef{{ID: "20002", Key: "PROJ-2"}},
		Links: []jira.IssueLink{{
			ID: "30001", Type: "Blocks", Label: "is blocked by",
			Direction: jira.LinkInward, Other: jira.IssueRef{Key: "PROJ-3"},
		}},
		Due:          jira.Date{Year: 2026, Month: time.March, Day: 20},
		Created:      testNow.Add(-72 * time.Hour),
		Updated:      testNow.Add(-time.Hour),
		Resolved:     ptr(testNow.Add(-30 * time.Minute)),
		TimeTracking: &jira.TimeTracking{OriginalEstimate: 3600, RemainingEstimate: 1800, TimeSpent: 1800},
		Fields: jira.NewFieldSet(map[string]jira.FieldValue{
			"customfield_20001": {Kind: jira.KindNumber, Number: 8},
			"customfield_20002": {Kind: jira.KindText, Text: "0|i00001:"},
			"customfield_20003": {Kind: jira.KindDoc, Doc: doc},
			"customfield_20004": {Kind: jira.KindOptions, Options: []jira.Option{{ID: "1", Label: "one"}}},
			"customfield_20005": {Kind: jira.KindUsers, Users: []jira.User{{AccountID: "acct-alan", TimeZone: loc}}},
			"customfield_20006": {Kind: jira.KindDate, Date: jira.Date{Year: 2026, Month: time.May, Day: 4}},
			"customfield_20007": {Kind: jira.KindBool, Bool: true},
		}),
		Requested: jira.AllFields(),
	}
	if err := cache.PutRows(cacheJQL, []jira.Issue{want}, false); err != nil {
		t.Fatalf("PutRows: %v", err)
	}

	got, ok := cache.Rows(cacheJQL)
	if !ok || len(got.Issues) != 1 {
		t.Fatalf("found %t with %d rows", ok, len(got.Issues))
	}
	back := got.Issues[0]

	if !back.Requested.Wide() {
		t.Error("a wide read came back narrow, so a later merge would refuse to overwrite anything")
	}
	if back.Assignee.TimeZone == nil || back.Assignee.TimeZone.String() != loc.String() {
		t.Errorf("the assignee's timezone came back as %v", back.Assignee.TimeZone)
	}
	if back.Description.IsZero() || len(back.Description.Content) != 1 {
		t.Errorf("the description came back as %+v", back.Description)
	}
	if !slices.Equal(back.Fields.IDs(), want.Fields.IDs()) {
		t.Errorf("the custom fields came back as %v, want %v", back.Fields.IDs(), want.Fields.IDs())
	}
	if n, _ := back.Fields.Number(jira.FieldRef{ID: "customfield_20001"}); n != 8 {
		t.Errorf("a numeric custom field came back as %v", n)
	}
	if users, _ := back.Fields.Users(jira.FieldRef{ID: "customfield_20005"}); len(users) != 1 || users[0].TimeZone == nil {
		t.Errorf("a user-valued custom field came back as %+v", users)
	}
	for _, field := range []struct {
		name       string
		want, back any
	}{
		{"id", want.ID, back.ID},
		{"key", want.Key, back.Key},
		{"project", want.Project, back.Project},
		{"summary", want.Summary, back.Summary},
		{"type", want.Type, back.Type},
		{"status", want.Status, back.Status},
		{"priority", *want.Priority, *back.Priority},
		{"resolution", *want.Resolution, *back.Resolution},
		{"labels", fmt.Sprint(want.Labels), fmt.Sprint(back.Labels)},
		{"components", fmt.Sprint(want.Components), fmt.Sprint(back.Components)},
		{"fix versions", fmt.Sprint(want.FixVersions), fmt.Sprint(back.FixVersions)},
		{"parent", *want.Parent, *back.Parent},
		{"subtasks", fmt.Sprint(want.Subtasks), fmt.Sprint(back.Subtasks)},
		{"links", fmt.Sprint(want.Links), fmt.Sprint(back.Links)},
		{"due", want.Due, back.Due},
		{"time tracking", *want.TimeTracking, *back.TimeTracking},
	} {
		if fmt.Sprint(field.want) != fmt.Sprint(field.back) {
			t.Errorf("%s came back as %v, want %v", field.name, field.back, field.want)
		}
	}
	for _, when := range []struct {
		name       string
		want, back time.Time
	}{
		{"created", want.Created, back.Created},
		{"updated", want.Updated, back.Updated},
		{"resolved", *want.Resolved, *back.Resolved},
	} {
		if !when.want.Equal(when.back) {
			t.Errorf("%s came back as %s, want %s", when.name, when.back, when.want)
		}
	}
}

func ptr[T any](v T) *T { return &v }

// BenchmarkCacheReadFirstPaint measures the budget in docs/PERFORMANCE.md that
// this packet owns: the cache read a view's first frame waits on.
func BenchmarkCacheReadFirstPaint(b *testing.B) {
	cache, _ := newTestCache(b)
	if err := cache.PutRows(cacheJQL, listRows(50), true); err != nil {
		b.Fatalf("PutRows: %v", err)
	}
	b.ReportAllocs()
	b.ResetTimer()
	for range b.N {
		if _, ok := cache.Rows(cacheJQL); !ok {
			b.Fatal("the rows went away")
		}
	}
}
