package app

import (
	"context"
	"errors"
	"fmt"
	"runtime"
	"slices"
	"strconv"
	"sync/atomic"
	"testing"
	"time"

	"github.com/varijkapil13/saral/pkg/jira"
	"github.com/varijkapil13/saral/pkg/jira/jiratest"
)

const testJQL = "project = PROJ ORDER BY key"

func testFake(issues int) *jiratest.Fake {
	return jiratest.New(
		jiratest.WithProject("PROJ", jiratest.Scrum),
		jiratest.WithIssues(jiratest.Gen(issues)),
	)
}

// callsTo counts how many times the fake was asked for one thing.
func callsTo(f *jiratest.Fake, method string) int {
	n := 0
	for _, call := range f.Calls() {
		if call == method {
			n++
		}
	}
	return n
}

// waitFor spins until cond holds, so that a test waits on another goroutine
// reaching a state rather than on a duration.
func waitFor(t *testing.T, what string, cond func() bool) {
	t.Helper()

	deadline := time.Now().Add(10 * time.Second)
	for !cond() {
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for %s", what)
		}
		runtime.Gosched()
	}
}

// countingClient is an adapter that can answer an approximate count, which the
// port itself does not carry.
type countingClient struct {
	jira.Client

	asked atomic.Int64
	jql   atomic.Value
	count int
	err   error
}

func (c *countingClient) ApproximateCount(_ context.Context, jql string) (int, error) {
	c.asked.Add(1)
	c.jql.Store(jql)
	return c.count, c.err
}

// blockingClient holds a search open until the test lets it finish, so that
// several callers are provably in flight at once.
type blockingClient struct {
	jira.Client

	searches atomic.Int64
	arrived  chan struct{}
	release  chan struct{}
}

func (c *blockingClient) Search(ctx context.Context, q jira.Query) (jira.Page[jira.Issue], error) {
	c.searches.Add(1)
	c.arrived <- struct{}{}
	select {
	case <-c.release:
	case <-ctx.Done():
		return jira.Page[jira.Issue]{}, ctx.Err()
	}
	return c.Client.Search(ctx, q)
}

func TestListProjection_AsksForWhatARowNeedsAndNothingElse(t *testing.T) {
	t.Parallel()

	got := ListProjection()
	want := []string{"summary", "status", "assignee", "priority", "updated", "issuetype"}
	if !slices.Equal(got.IDs, want) {
		t.Errorf("the list field set is %v, want %v", got.IDs, want)
	}
	if len(got.Names) != 0 {
		t.Errorf("the list field set resolves %v by name, which costs a catalogue fetch on the first paint", got.Names)
	}
}

func TestProjection_WithLeavesTheOneItWasBuiltFromAlone(t *testing.T) {
	t.Parallel()

	base := ListProjection()
	wider := base.With("customfield_13401").WithNames("Story Points")

	if len(base.IDs) != 6 || len(base.Names) != 0 {
		t.Errorf("widening the list field set changed it: %+v", base)
	}
	if len(wider.IDs) != 7 || !slices.Contains(wider.Names, "Story Points") {
		t.Errorf("the widened field set is %+v", wider)
	}
}

func TestResolve_DoesNotFetchTheCatalogueWhenNothingIsNamed(t *testing.T) {
	t.Parallel()

	f := testFake(0)
	got, err := NewSearch(f).Resolve(t.Context(), ListProjection())
	if err != nil {
		t.Fatalf("resolving: %v", err)
	}
	if !slices.Equal(got.IDs, ListProjection().IDs) {
		t.Errorf("resolved to %v", got.IDs)
	}
	if n := callsTo(f, "Fields"); n != 0 {
		t.Errorf("the catalogue was fetched %d times; a field set of platform IDs resolves without it", n)
	}
}

func TestResolve_TurnsANameIntoThisSitesFieldIDAndReportsWhatIsMissing(t *testing.T) {
	t.Parallel()

	f := testFake(0)
	catalogue, err := f.Fields(t.Context())
	if err != nil {
		t.Fatalf("reading the catalogue: %v", err)
	}
	points, ok := jira.FieldByName(catalogue, "Story Points")
	if !ok {
		t.Fatal("the fake has no story point field to resolve")
	}

	got, err := NewSearch(f).Resolve(t.Context(), ListProjection().WithNames("Story Points", "Sprint", "Bandwidth Envelope"))
	if err != nil {
		t.Fatalf("resolving: %v", err)
	}
	if !slices.Contains(got.IDs, points.ID) {
		t.Errorf("resolved to %v, which does not include %s", got.IDs, points.ID)
	}
	if want := []string{"Bandwidth Envelope"}; !slices.Equal(got.Missing, want) {
		t.Errorf("the missing fields are %v, want %v: a site without a field says so rather than guessing", got.Missing, want)
	}
}

func TestFields_FetchesTheCatalogueOnceAndAgainOnlyAfterItIsInvalidated(t *testing.T) {
	t.Parallel()

	f := testFake(0)
	s := NewSearch(f)
	for range 3 {
		if _, err := s.Fields(t.Context()); err != nil {
			t.Fatalf("reading the catalogue: %v", err)
		}
	}
	if n := callsTo(f, "Fields"); n != 1 {
		t.Errorf("the catalogue was fetched %d times, want 1: it changes with the site's configuration, not with the view", n)
	}

	s.Invalidate()
	if _, err := s.Fields(t.Context()); err != nil {
		t.Fatalf("reading the catalogue after a purge: %v", err)
	}
	if n := callsTo(f, "Fields"); n != 2 {
		t.Errorf("the catalogue was fetched %d times after a purge, want 2", n)
	}
}

func TestFields_HandsOutACopyTheCallerCannotWriteThroughToTheCache(t *testing.T) {
	t.Parallel()

	s := NewSearch(testFake(0))
	first, err := s.Fields(t.Context())
	if err != nil {
		t.Fatalf("reading the catalogue: %v", err)
	}
	first[0].Name = "rewritten"

	second, err := s.Fields(t.Context())
	if err != nil {
		t.Fatalf("reading the catalogue again: %v", err)
	}
	if second[0].Name == "rewritten" {
		t.Error("writing to a returned catalogue changed the cached one")
	}
}

func TestRun_AsksForTheProjectionsFieldsAndNothingElse(t *testing.T) {
	t.Parallel()

	f := testFake(10)
	got, err := NewSearch(f).Run(t.Context(), Request{JQL: testJQL, Projection: ListProjection()})
	if err != nil {
		t.Fatalf("searching: %v", err)
	}
	if len(got.Page.Items) != 10 {
		t.Fatalf("the page carried %d issues, want 10", len(got.Page.Items))
	}
	if len(got.Missing) != 0 {
		t.Errorf("the search reported %v missing, want none", got.Missing)
	}

	issue := got.Page.Items[0]
	switch {
	case issue.Summary == "" || issue.Status.Name == "":
		t.Errorf("the projected fields are missing: %+v", issue)
	case !issue.Description.IsEmpty():
		t.Error("the issue carries a description, which a list row does not need")
	case issue.Labels != nil:
		t.Error("the issue carries labels, which a list row does not need")
	case issue.Fields.Len() != 0:
		t.Errorf("the issue carries %d custom field values, which a list row did not ask for", issue.Fields.Len())
	}
}

func TestRun_SearchesForANamedFieldThisSiteDoesHave(t *testing.T) {
	t.Parallel()

	f := testFake(6)
	got, err := NewSearch(f).Run(t.Context(), Request{
		JQL:        testJQL,
		Projection: ListProjection().WithNames("Story Points", "Bandwidth Envelope"),
	})
	if err != nil {
		t.Fatalf("searching: %v", err)
	}
	if want := []string{"Bandwidth Envelope"}; !slices.Equal(got.Missing, want) {
		t.Errorf("the search reported %v missing, want %v", got.Missing, want)
	}

	withPoints := 0
	for _, issue := range got.Page.Items {
		if issue.Fields.Len() > 0 {
			withPoints++
		}
	}
	if withPoints == 0 {
		t.Error("no issue came back with a story point value, so the resolved field was not asked for")
	}
}

func TestRun_RefusesAFieldSetThisSiteResolvesToNothing(t *testing.T) {
	t.Parallel()

	f := testFake(4)
	_, err := NewSearch(f).Run(t.Context(), Request{
		JQL:        testJQL,
		Projection: Projection{Name: "burn-up", Names: []string{"Bandwidth Envelope"}},
	})
	if err == nil {
		t.Fatal("the search ran with no fields at all")
	}
	if callsTo(f, "Search") != 0 {
		t.Error("a search with no fields reached the adapter, which is the request that returns nothing useful")
	}
}

func TestRun_ReturnsTheAdaptersOwnTypedFailure(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		fail error
		want func(error) bool
	}{
		{
			name: "the site rate limiting the query",
			fail: &jira.RateLimitError{RetryAfter: 30 * time.Second},
			want: func(err error) bool {
				var limited *jira.RateLimitError
				return errors.As(err, &limited) && limited.RetryAfter == 30*time.Second
			},
		},
		{
			name: "a project this token cannot browse",
			fail: &jira.CapabilityError{Reason: "you need Browse Projects"},
			want: func(err error) bool {
				var missing *jira.CapabilityError
				return errors.As(err, &missing)
			},
		},
		{
			name: "the site being unreachable",
			fail: &jira.TransportError{Op: "POST /rest/api/3/search/jql", Err: errors.New("no route to host")},
			want: func(err error) bool {
				var broken *jira.TransportError
				return errors.As(err, &broken)
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			f := testFake(4)
			f.FailNext(tt.fail)

			_, err := NewSearch(f).Run(t.Context(), Request{JQL: testJQL, Projection: ListProjection()})
			if !tt.want(err) {
				t.Fatalf("got %v, want the adapter's own typed failure to survive the use case", err)
			}
		})
	}
}

func TestRun_CollapsesIdenticalSearchesThatAreInFlightAtOnce(t *testing.T) {
	t.Parallel()

	const callers = 4
	blocking := &blockingClient{
		Client:  testFake(8),
		arrived: make(chan struct{}, callers),
		release: make(chan struct{}),
	}
	s := NewSearch(blocking)

	// Joining the flight is the barrier: a caller merely started would
	// legitimately begin a second search once this one had finished.
	wanted := searchKey(jira.Query{JQL: testJQL, Fields: ListProjection().IDs})
	var joined atomic.Int64
	s.flight.joined = func(key string) {
		if key == wanted {
			joined.Add(1)
		}
	}

	results := make(chan error, callers)
	for range callers {
		go func() {
			_, err := s.Run(t.Context(), Request{JQL: testJQL, Projection: ListProjection()})
			results <- err
		}()
	}

	<-blocking.arrived
	waitFor(t, "every caller to have joined the search in flight", func() bool {
		return joined.Load() == callers
	})
	close(blocking.release)
	for range callers {
		if err := <-results; err != nil {
			t.Fatalf("a caller sharing the search failed: %v", err)
		}
	}

	if got := blocking.searches.Load(); got != 1 {
		t.Errorf("the adapter ran %d searches, want 1: a cursor moving down a list must not fan out a fetch per keystroke", got)
	}
}

func TestRun_StartsAFreshSearchOnceTheSharedOneIsDone(t *testing.T) {
	t.Parallel()

	f := testFake(4)
	s := NewSearch(f)
	for range 2 {
		if _, err := s.Run(t.Context(), Request{JQL: testJQL, Projection: ListProjection()}); err != nil {
			t.Fatalf("searching: %v", err)
		}
	}
	if n := callsTo(f, "Search"); n != 2 {
		t.Errorf("the adapter ran %d searches, want 2: collapsing calls in flight is not caching finished ones", n)
	}
}

func TestRun_ReturnsTheCallersOwnErrorWhenItCancels(t *testing.T) {
	t.Parallel()

	ctx, cancel := context.WithCancel(t.Context())
	cancel()

	f := testFake(4)
	_, err := NewSearch(f).Run(ctx, Request{JQL: testJQL, Projection: ListProjection()})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("got %v, want the context's own error", err)
	}
	if callsTo(f, "Search") != 0 {
		t.Error("the adapter was asked for a search the caller had already given up on")
	}
}

func TestSearchKey_TellsTwoSearchesApartByEverythingThatChangesTheAnswer(t *testing.T) {
	t.Parallel()

	base := jira.Query{JQL: testJQL, Fields: []string{"summary", "status"}, MaxResults: 50}
	tests := []struct {
		name string
		with jira.Query
		same bool
	}{
		{name: "the same query twice", with: base, same: true},
		{name: "a different JQL", with: jira.Query{JQL: "project = OTHER", Fields: base.Fields, MaxResults: 50}},
		{name: "a wider field set", with: jira.Query{JQL: base.JQL, Fields: []string{"summary", "status", "assignee"}, MaxResults: 50}},
		{name: "a different page size", with: jira.Query{JQL: base.JQL, Fields: base.Fields, MaxResults: 100}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			if same := searchKey(base) == searchKey(tt.with); same != tt.same {
				t.Errorf("the two searches share a key: %t, want %t", same, tt.same)
			}
		})
	}
}

func TestCount_ReportsThatAnAdapterCannotCountRatherThanFailing(t *testing.T) {
	t.Parallel()

	count, ok, err := NewSearch(testFake(4)).Count(t.Context(), testJQL)
	if err != nil {
		t.Fatalf("counting: %v", err)
	}
	if ok || count != 0 {
		t.Errorf("the count is %d (%t); an adapter with no count endpoint answers that it has none", count, ok)
	}
}

func TestCount_AsksTheAdapterThatCanAnswerAndTrimsTheQuery(t *testing.T) {
	t.Parallel()

	counting := &countingClient{Client: testFake(4), count: 153}
	count, ok, err := NewSearch(counting).Count(t.Context(), "  "+testJQL+"  ")
	if err != nil {
		t.Fatalf("counting: %v", err)
	}
	if !ok || count != 153 {
		t.Errorf("the count is %d (%t), want 153", count, ok)
	}
	if got, _ := counting.jql.Load().(string); got != testJQL {
		t.Errorf("the count asked for %q, want the trimmed query", got)
	}
}

func TestCount_SurfacesWhatWentWrongWhenTheAdapterCanCountAndDidNot(t *testing.T) {
	t.Parallel()

	counting := &countingClient{Client: testFake(4), err: &jira.CapabilityError{Reason: "you need Browse Projects"}}
	_, ok, err := NewSearch(counting).Count(t.Context(), testJQL)

	var missing *jira.CapabilityError
	if !errors.As(err, &missing) {
		t.Fatalf("got %v, want the adapter's own failure", err)
	}
	if ok {
		t.Error("a failed count reported that it had an answer")
	}
}

func TestSavedQueries_RefusesTheOnesThatCouldNotBeRun(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name  string
		query SavedQuery
	}{
		{name: "no name to reach it by", query: SavedQuery{Name: "  ", JQL: testJQL}},
		{name: "nothing to run", query: SavedQuery{Name: "Mine", JQL: "   "}},
		{name: "a key that is not on the keyboard row", query: SavedQuery{Name: "Mine", JQL: testJQL, Slot: MaxSavedSlot + 1}},
		{name: "a negative key", query: SavedQuery{Name: "Mine", JQL: testJQL, Slot: -1}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			if _, err := NewSavedQueries(tt.query); err == nil {
				t.Fatalf("%+v was accepted", tt.query)
			}
		})
	}
}

func TestSavedQueries_KeepsTheOrderAddedAndFindsAQueryByNameOrKey(t *testing.T) {
	t.Parallel()

	saved, err := NewSavedQueries(
		SavedQuery{Name: "My open work", JQL: "assignee = currentUser()", Slot: 1},
		SavedQuery{Name: "Recently updated", JQL: testJQL, Slot: 2},
	)
	if err != nil {
		t.Fatalf("saving: %v", err)
	}

	if got := saved.All(); len(got) != 2 || got[0].Name != "My open work" {
		t.Errorf("the saved queries are %+v", got)
	}
	if got, ok := saved.ByName("my open WORK"); !ok || got.Slot != 1 {
		t.Errorf("looking a query up by name gave %+v (%t); a name is not case sensitive", got, ok)
	}
	if got, ok := saved.BySlot(2); !ok || got.Name != "Recently updated" {
		t.Errorf("key 2 runs %+v (%t)", got, ok)
	}
	if _, ok := saved.BySlot(0); ok {
		t.Error("an unbound query was reachable by key zero")
	}
	if saved.Remove("My open work").Len() != 1 || saved.Len() != 2 {
		t.Error("removing a query changed the set it was removed from")
	}
}

func TestSavedQueries_ListsTheKeysThatActuallyRunSomething(t *testing.T) {
	t.Parallel()

	saved, err := NewSavedQueries(
		SavedQuery{Name: "Third", JQL: testJQL, Slot: 3},
		SavedQuery{Name: "Unbound", JQL: testJQL},
		SavedQuery{Name: "First", JQL: testJQL, Slot: 1},
	)
	if err != nil {
		t.Fatalf("saving: %v", err)
	}
	if got := saved.Slots(); !slices.Equal(got, []int{1, 3}) {
		t.Errorf("the bound keys are %v, want [1 3] in that order", got)
	}
	if got := (SavedQueries{}).Slots(); len(got) != 0 {
		t.Errorf("an empty set reports keys %v", got)
	}
}

func TestSavedQueries_RebindingAKeyTakesItFromWhicheverQueryHadIt(t *testing.T) {
	t.Parallel()

	saved, err := NewSavedQueries(
		SavedQuery{Name: "My open work", JQL: "assignee = currentUser()", Slot: 1},
		SavedQuery{Name: "Recently updated", JQL: testJQL, Slot: 1},
	)
	if err != nil {
		t.Fatalf("saving: %v", err)
	}

	got, ok := saved.BySlot(1)
	if !ok || got.Name != "Recently updated" {
		t.Errorf("key 1 runs %+v (%t), want the query that took it", got, ok)
	}
	previous, ok := saved.ByName("My open work")
	if !ok || previous.Slot != 0 {
		t.Errorf("the query that held key 1 is now %+v; it should still be there, unbound", previous)
	}
}

func TestSavedQueries_ReplacingAQueryKeepsItsPlaceInTheList(t *testing.T) {
	t.Parallel()

	saved, err := NewSavedQueries(
		SavedQuery{Name: "First", JQL: "project = A"},
		SavedQuery{Name: "Second", JQL: "project = B"},
	)
	if err != nil {
		t.Fatalf("saving: %v", err)
	}
	saved, err = saved.Add(SavedQuery{Name: "first", JQL: "project = C"})
	if err != nil {
		t.Fatalf("replacing: %v", err)
	}

	all := saved.All()
	if len(all) != 2 {
		t.Fatalf("the set holds %d queries, want 2: a query with a name already there replaces it", len(all))
	}
	if all[0].JQL != "project = C" || all[1].Name != "Second" {
		t.Errorf("the set reads %+v", all)
	}
}

func TestRunSaved_RunsTheQueryBehindTheNameAndSaysSoWhenThereIsNone(t *testing.T) {
	t.Parallel()

	saved, err := NewSavedQueries(SavedQuery{Name: "Everything", JQL: testJQL, Slot: 1})
	if err != nil {
		t.Fatalf("saving: %v", err)
	}
	f := testFake(5)
	s := NewSearch(f, WithSavedQueries(saved))

	got, err := s.RunSaved(t.Context(), "everything")
	if err != nil {
		t.Fatalf("running a saved query: %v", err)
	}
	if len(got.Page.Items) != 5 {
		t.Errorf("the saved query returned %d issues, want 5", len(got.Page.Items))
	}
	if got.Page.Items[0].Summary == "" {
		t.Error("a saved query with no field set of its own fetched nothing to render")
	}
	if s.Saved().Len() != 1 {
		t.Errorf("the search reports %d saved queries, want 1", s.Saved().Len())
	}

	if _, err := s.RunSaved(t.Context(), "nothing by this name"); err == nil {
		t.Error("running a saved query nobody saved succeeded")
	}
}

func TestSearch_WithNoClientSaysSoRatherThanPanicking(t *testing.T) {
	t.Parallel()

	s := NewSearch(nil)
	if _, err := s.Run(t.Context(), Request{JQL: testJQL, Projection: ListProjection()}); err == nil {
		t.Error("a search with no client to run against succeeded")
	}
	if _, err := s.Fields(t.Context()); err == nil {
		t.Error("a catalogue read with no client to read from succeeded")
	}
}

const oneIssue = `key = "PROJ-1"`

// translatedFields is a catalogue the way a site that is not in English sends
// one: no untranslated name on a system field, a display name per custom field
// in the site's own language that its untranslated spelling does not match, and
// one custom field no JQL clause can name.
func translatedFields() []jira.Field {
	return []jira.Field{
		{
			ID: "summary", Key: "summary", Name: "Zusammenfassung",
			Navigable: true, Searchable: true, Orderable: true,
			ClauseNames: []string{"summary"},
			Schema:      jira.FieldSchema{Type: "string", System: "summary"},
		},
		{
			ID: "status", Key: "status", Name: "Bearbeitungsstand",
			Navigable: true, Searchable: true, Orderable: true,
			ClauseNames: []string{"status"},
			Schema:      jira.FieldSchema{Type: "status", System: "status"},
		},
		{
			ID: "customfield_20001", Key: "customfield_20001",
			Name: "Aufwandspunkte", UntranslatedName: "EffortPoints", Custom: true,
			Navigable: true, Searchable: true, Orderable: true,
			ClauseNames: []string{"cf[20001]", "EffortPoints"},
			Schema: jira.FieldSchema{
				Type:     "number",
				Custom:   "com.atlassian.jira.plugin.system.customfieldtypes:float",
				CustomID: 20001,
			},
		},
		{
			ID: "customfield_20002", Key: "customfield_20002",
			Name: "Abnahmekriterien", UntranslatedName: "AcceptanceCriteria", Custom: true,
			Navigable: true,
			Schema: jira.FieldSchema{
				Type:     "string",
				Custom:   "com.atlassian.jira.plugin.system.customfieldtypes:textarea",
				CustomID: 20002,
			},
		},
	}
}

func systemFieldsOnly() []jira.Field {
	all := translatedFields()
	out := make([]jira.Field, 0, len(all))
	for i := range all {
		if !all[i].Custom {
			out = append(out, all[i])
		}
	}
	return out
}

// translatedSite is one issue on a site whose fields are the ones above, with a
// value under each of the two custom field IDs.
func translatedSite(fields []jira.Field) *jiratest.Fake {
	return jiratest.New(
		jiratest.WithFields(fields),
		jiratest.WithIssues([]jira.Issue{{
			ID:      "30001",
			Key:     "PROJ-1",
			Project: jira.ProjectRef{ID: "10001", Key: "PROJ", Name: "Bestellwesen"},
			Summary: "Der Export läuft zweimal",
			Fields: jira.NewFieldSet(map[string]jira.FieldValue{
				"customfield_20001": {Kind: jira.KindNumber, Number: 5},
				"customfield_20002": {Kind: jira.KindText, Text: "Zwei Läufe, eine Datei"},
			}),
		}}),
	)
}

// fieldNamed is the ID this site holds a field under, which is the only way to
// get at one from a test without writing a customfield_NNNNN down.
func fieldNamed(t *testing.T, f *jiratest.Fake, name string) jira.Field {
	t.Helper()

	catalogue, err := f.Fields(t.Context())
	if err != nil {
		t.Fatalf("reading the catalogue: %v", err)
	}
	field, err := jira.ResolveField(catalogue, name)
	if err != nil {
		t.Fatalf("resolving %q on this site: %v", name, err)
	}
	return field
}

func TestDetailProjection_AsksForTheCustomFieldsAndTheListOneDoesNot(t *testing.T) {
	t.Parallel()

	if !DetailProjection().Custom {
		t.Error("an open issue does not ask for this site's custom fields, which are most of what an issue is")
	}
	if ListProjection().Custom {
		t.Error("a list row asks for every custom field, which is a hundred values per row nothing renders")
	}
}

func TestProjection_WithCustomLeavesTheOneItWasBuiltFromAlone(t *testing.T) {
	t.Parallel()

	base := ListProjection()
	wider := base.WithCustom()

	if base.Custom {
		t.Error("widening a field set to the custom fields changed the one it was built from")
	}
	if !wider.Custom {
		t.Error("the widened field set does not ask for the custom fields")
	}
	if !slices.Equal(base.IDs, wider.IDs) {
		t.Errorf("asking for the custom fields changed the platform IDs: %v against %v", wider.IDs, base.IDs)
	}
}

func TestRun_ADetailReadBringsBackTheCustomValuesAndWhatTheSiteCallsThem(t *testing.T) {
	t.Parallel()

	f := testFake(9)
	points := fieldNamed(t, f, "Story Points")

	got, err := NewSearch(f).Run(t.Context(), Request{
		JQL: oneIssue, Projection: DetailProjection(), MaxResults: 1,
	})
	if err != nil {
		t.Fatalf("reading one issue: %v", err)
	}
	if len(got.Page.Items) != 1 {
		t.Fatalf("the read came back with %d issues, want 1", len(got.Page.Items))
	}
	if len(got.Missing) != 0 {
		t.Errorf("the read reported %v missing; nothing was asked for by name", got.Missing)
	}

	issue := got.Page.Items[0]
	if issue.Fields.Len() == 0 {
		t.Fatal("the issue carries no custom field values, which is most of what an issue is on a configured site")
	}
	ref, ok := got.Labels.Field(points.ID)
	if !ok {
		t.Fatalf("%s came back with no label, so a view can only render its ID", points.ID)
	}
	if ref.Name != points.Name {
		t.Errorf("%s is labelled %q, want %q: the name is the one this site displays", points.ID, ref.Name, points.Name)
	}
	if value, ok := issue.Fields.Number(ref); !ok || value == 0 {
		t.Errorf("reading %s through its own label gave %v (%t)", points.ID, value, ok)
	}
	for _, id := range issue.Fields.IDs() {
		if got.Labels.Name(id) == "" {
			t.Errorf("the issue carries a value for %s that nothing can name", id)
		}
	}
}

func TestResolve_TakesEveryCustomFieldIDFromTheCatalogueRatherThanAWildcard(t *testing.T) {
	t.Parallel()

	f := testFake(0)
	catalogue, err := f.Fields(t.Context())
	if err != nil {
		t.Fatalf("reading the catalogue: %v", err)
	}

	got, err := NewSearch(f).Resolve(t.Context(), DetailProjection())
	if err != nil {
		t.Fatalf("resolving: %v", err)
	}

	custom := 0
	for i := range catalogue {
		if !catalogue[i].Custom {
			continue
		}
		custom++
		if !slices.Contains(got.IDs, catalogue[i].ID) {
			t.Errorf("%s is a custom field on this site and was not asked for", catalogue[i].ID)
		}
		if got.Labels.Name(catalogue[i].ID) != catalogue[i].Name {
			t.Errorf("%s resolved without the name to render it by", catalogue[i].ID)
		}
	}
	if custom == 0 {
		t.Fatal("this site has no custom fields, so the test proves nothing")
	}
	for _, wildcard := range []string{jira.FieldsAll, jira.FieldsNavigable} {
		if slices.Contains(got.IDs, wildcard) {
			t.Errorf("the read asks for %s, which returns a value per field per issue and no name for any of them", wildcard)
		}
	}
	if jira.NewFieldMask(got.IDs).Wide() {
		t.Error("a field set taken from the catalogue reports itself as a read of everything, which would let a narrow cached row look wide")
	}
}

func TestResolve_LabelsACustomFieldWithTheNameThisSiteShowsNotItsUntranslatedOne(t *testing.T) {
	t.Parallel()

	f := translatedSite(translatedFields())
	got, err := NewSearch(f).Resolve(t.Context(), DetailProjection())
	if err != nil {
		t.Fatalf("resolving: %v", err)
	}

	if name := got.Labels.Name("customfield_20001"); name != "Aufwandspunkte" {
		t.Errorf("the field is labelled %q, want the name this site displays", name)
	}
	if name := got.Labels.Name("summary"); name != "Zusammenfassung" {
		t.Errorf("the summary is labelled %q; a system field is translated too", name)
	}
	if len(got.Missing) != 0 {
		t.Errorf("the read reported %v missing: a custom field taken by ID never had to be named", got.Missing)
	}
}

func TestResolve_AsksForACustomFieldNoJQLClauseCanName(t *testing.T) {
	t.Parallel()

	f := translatedSite(translatedFields())
	criteria := fieldNamed(t, f, "AcceptanceCriteria")
	if len(criteria.ClauseNames) != 0 {
		t.Fatalf("%s has clause names %v, so the test is not about the field it means to be", criteria.ID, criteria.ClauseNames)
	}

	s := NewSearch(f)
	resolved, err := s.Resolve(t.Context(), DetailProjection())
	if err != nil {
		t.Fatalf("resolving: %v", err)
	}
	if !slices.Contains(resolved.IDs, criteria.ID) {
		t.Errorf("%s was not asked for; a field that cannot be named in JQL is still readable by ID", criteria.ID)
	}
	if name := resolved.Labels.Name(criteria.ID); name != criteria.Name {
		t.Errorf("%s is labelled %q, want %q", criteria.ID, name, criteria.Name)
	}

	got, err := s.Run(t.Context(), Request{JQL: oneIssue, Projection: DetailProjection(), MaxResults: 1})
	if err != nil {
		t.Fatalf("reading one issue: %v", err)
	}
	ref, ok := got.Labels.Field(criteria.ID)
	if !ok {
		t.Fatalf("%s came back unlabelled", criteria.ID)
	}
	if text, ok := got.Page.Items[0].Fields.Text(ref); !ok || text == "" {
		t.Errorf("%s came back with %q (%t)", criteria.ID, text, ok)
	}
}

func TestRun_ADetailReadOnASiteWithNoCustomFieldsAsksForTheSameFieldsAsBefore(t *testing.T) {
	t.Parallel()

	f := translatedSite(systemFieldsOnly())
	got, err := NewSearch(f).Run(t.Context(), Request{
		JQL: oneIssue, Projection: DetailProjection(), MaxResults: 1,
	})
	if err != nil {
		t.Fatalf("reading one issue on a site with no custom fields: %v", err)
	}
	if len(got.Page.Items) != 1 {
		t.Fatalf("the read came back with %d issues, want 1", len(got.Page.Items))
	}

	issue := got.Page.Items[0]
	if issue.Summary == "" {
		t.Error("the issue came back without its summary")
	}
	if issue.Fields.Len() != 0 {
		t.Errorf("the issue carries %d custom values on a site that defines none", issue.Fields.Len())
	}
	want := slices.Sorted(slices.Values(DetailProjection().IDs))
	if got := issue.Requested.IDs(); !slices.Equal(got, want) {
		t.Errorf("the read asked for %v, want the platform fields alone %v", got, want)
	}
	if got.Labels.Len() != len(systemFieldsOnly()) {
		t.Errorf("%d fields came back labelled, want the %d this site defines", got.Labels.Len(), len(systemFieldsOnly()))
	}
}

func TestRun_TheIssuesMaskNamesExactlyWhatTheProjectionAskedFor(t *testing.T) {
	t.Parallel()

	f := testFake(9)
	s := NewSearch(f)
	resolved, err := s.Resolve(t.Context(), DetailProjection())
	if err != nil {
		t.Fatalf("resolving: %v", err)
	}
	got, err := s.Run(t.Context(), Request{JQL: oneIssue, Projection: DetailProjection(), MaxResults: 1})
	if err != nil {
		t.Fatalf("reading one issue: %v", err)
	}

	issue := got.Page.Items[0]
	if issue.Requested.Wide() {
		t.Error("the issue reports a read of every field the site has, which is not what was asked for")
	}
	want := slices.Sorted(slices.Values(resolved.IDs))
	if got := issue.Requested.IDs(); !slices.Equal(got, want) {
		t.Errorf("the issue reports %v as asked for, want %v", got, want)
	}
	for _, id := range issue.Fields.IDs() {
		if !issue.Requested.Has(id) {
			t.Errorf("the issue carries a value for %s, which its own mask says nothing asked for", id)
		}
	}
}

func TestMergeIssue_ADetailReadWidensACachedRowWithoutMakingItLookWide(t *testing.T) {
	t.Parallel()

	f := testFake(9)
	points := fieldNamed(t, f, "Story Points")
	s := NewSearch(f)

	rows, err := s.Run(t.Context(), Request{JQL: testJQL, Projection: ListProjection()})
	if err != nil {
		t.Fatalf("reading the list: %v", err)
	}
	row := rows.Page.Items[0]
	detail, err := s.Run(t.Context(), Request{JQL: oneIssue, Projection: DetailProjection(), MaxResults: 1})
	if err != nil {
		t.Fatalf("reading one issue: %v", err)
	}
	if row.Key != detail.Page.Items[0].Key {
		t.Fatalf("the list row is %s and the detail read is %s", row.Key, detail.Page.Items[0].Key)
	}

	merged := MergeIssue(row, detail.Page.Items[0])
	if merged.Requested.Wide() {
		t.Error("merging a detail read over a cached row claims every field the site has")
	}
	if !merged.Requested.Has(points.ID) {
		t.Errorf("the merged issue does not report %s as read, so a write would treat its value as nothing anybody asked about", points.ID)
	}
	if _, ok := merged.Fields.ByID(points.ID); !ok {
		t.Errorf("the merged issue lost the %s value the detail read brought back", points.ID)
	}

	back := MergeIssue(merged, row)
	if back.Requested.Wide() {
		t.Error("a narrow list refresh over the merged issue made it look wide")
	}
	if _, ok := back.Fields.ByID(points.ID); !ok {
		t.Errorf("a list refresh dropped %s, which it never asked about", points.ID)
	}
	if !back.Requested.Has(points.ID) {
		t.Errorf("a list refresh stopped reporting %s as read while still carrying its value", points.ID)
	}
}

func TestRun_AListReadIsUnchangedByTheWiderDetailRead(t *testing.T) {
	t.Parallel()

	f := testFake(9)
	got, err := NewSearch(f).Run(t.Context(), Request{JQL: testJQL, Projection: ListProjection()})
	if err != nil {
		t.Fatalf("reading the list: %v", err)
	}
	if n := callsTo(f, "Fields"); n != 0 {
		t.Errorf("a list read fetched the catalogue %d times, which is a request in front of the first paint", n)
	}
	if got.Labels.Len() != 0 {
		t.Errorf("a list read came back with %d labels, which it could only have from a catalogue fetch", got.Labels.Len())
	}

	issue := got.Page.Items[0]
	if issue.Fields.Len() != 0 {
		t.Errorf("a list row carries %d custom values", issue.Fields.Len())
	}
	want := slices.Sorted(slices.Values(ListProjection().IDs))
	if got := issue.Requested.IDs(); !slices.Equal(got, want) {
		t.Errorf("a list row reports %v as asked for, want the six %v", got, want)
	}
}

func TestRun_FetchesTheCatalogueOnceAcrossTwoDetailReads(t *testing.T) {
	t.Parallel()

	f := testFake(9)
	s := NewSearch(f)
	for range 2 {
		if _, err := s.Run(t.Context(), Request{JQL: oneIssue, Projection: DetailProjection(), MaxResults: 1}); err != nil {
			t.Fatalf("reading one issue: %v", err)
		}
	}
	if n := callsTo(f, "Fields"); n != 1 {
		t.Errorf("the catalogue was fetched %d times for two reads, want 1: it changes with the site's configuration, not with the issue", n)
	}
	if n := callsTo(f, "Search"); n != 2 {
		t.Errorf("the adapter ran %d searches, want 2", n)
	}
}

func TestRun_ADetailReadSaysWhyTheCatalogueCouldNotBeRead(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		fail error
		want func(error) bool
	}{
		{
			name: "the site rate limiting the catalogue",
			fail: &jira.RateLimitError{RetryAfter: 30 * time.Second},
			want: func(err error) bool {
				var limited *jira.RateLimitError
				return errors.As(err, &limited) && limited.RetryAfter == 30*time.Second
			},
		},
		{
			name: "a token that cannot read the catalogue",
			fail: &jira.CapabilityError{Reason: "you need Browse Projects"},
			want: func(err error) bool {
				var missing *jira.CapabilityError
				return errors.As(err, &missing)
			},
		},
		{
			name: "the site being unreachable",
			fail: &jira.TransportError{Op: "GET /rest/api/3/field", Err: errors.New("no route to host")},
			want: func(err error) bool {
				var broken *jira.TransportError
				return errors.As(err, &broken)
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			f := testFake(9)
			f.FailNext(tt.fail)

			_, err := NewSearch(f).Run(t.Context(), Request{
				JQL: oneIssue, Projection: DetailProjection(), MaxResults: 1,
			})
			if !tt.want(err) {
				t.Fatalf("got %v, want the adapter's own typed failure", err)
			}
			if callsTo(f, "Search") != 0 {
				t.Error("the search went out anyway, so the issue would have opened missing every custom field and said nothing about it")
			}
		})
	}
}

func TestFieldLabels_LabelOnlyWhatWasAskedForAndCannotBeWrittenThrough(t *testing.T) {
	t.Parallel()

	catalogue := translatedFields()
	labels := NewFieldLabels(catalogue, []string{"customfield_20001", "customfield_99999"})

	if got := labels.IDs(); !slices.Equal(got, []string{"customfield_20001"}) {
		t.Errorf("the labels cover %v, want only the field the catalogue has", got)
	}
	if labels.Len() != 1 {
		t.Errorf("the labels count %d, want 1", labels.Len())
	}
	if _, ok := labels.Field("customfield_99999"); ok {
		t.Error("a field this site does not have came back labelled")
	}
	if name := labels.Name("customfield_99999"); name != "" {
		t.Errorf("an unlabelled field is named %q, want nothing: whether to show a raw ID is the caller's decision", name)
	}

	for i := range catalogue {
		catalogue[i].Name = "rewritten"
	}
	if name := labels.Name("customfield_20001"); name == "rewritten" {
		t.Error("writing to the catalogue afterwards changed the labels taken from it")
	}

	var unread FieldLabels
	if unread.Len() != 0 || len(unread.IDs()) != 0 || unread.Name("summary") != "" {
		t.Error("a read that carried no labels answers as though it had some")
	}
	if _, ok := unread.Field("summary"); ok {
		t.Error("a read that carried no labels resolved one")
	}
}

func TestSavedQuery_AFieldSetOfNothingButCustomFieldsIsNotTheListDefault(t *testing.T) {
	t.Parallel()

	q := SavedQuery{Name: "Estimates", JQL: testJQL, Projection: Projection{Name: "estimates", Custom: true}}
	got := q.projection()
	if !got.Custom {
		t.Error("a saved query asking only for the custom fields fell back to the list field set, which asks for none of them")
	}
	if len(got.IDs) != 0 {
		t.Errorf("the field set grew to %v", got.IDs)
	}

	empty := SavedQuery{Name: "Anything", JQL: testJQL}.projection()
	if len(empty.IDs) != len(ListProjection().IDs) || empty.Custom {
		t.Errorf("a saved query with no field set of its own resolved to %+v, want the list one", empty)
	}
}

// configuredSite is a catalogue the size of one a real site answered with: 101
// fields, 57 of them custom. It is what the detail read's cost has to be
// measured against, because a site with six custom fields cannot show it.
func configuredSite() []jira.Field {
	detail := DetailProjection().IDs
	out := make([]jira.Field, 0, 101)
	for i, id := range detail {
		out = append(out, jira.Field{
			ID: id, Key: id, Name: "Field " + strconv.Itoa(i),
			Navigable: true, Searchable: true, Orderable: true,
			ClauseNames: []string{id},
			Schema:      jira.FieldSchema{Type: "string", System: id},
		})
	}
	for i := len(detail); i < 44; i++ {
		id := "system_" + strconv.Itoa(i)
		out = append(out, jira.Field{
			ID: id, Key: id, Name: "Field " + strconv.Itoa(i),
			Navigable: true, Searchable: true, Orderable: true,
			ClauseNames: []string{id},
			Schema:      jira.FieldSchema{Type: "string", System: id},
		})
	}
	for i := range 57 {
		id := fmt.Sprintf("customfield_%d", 21000+i)
		out = append(out, jira.Field{
			ID: id, Key: id,
			Name: "Custom " + strconv.Itoa(i), UntranslatedName: "Custom" + strconv.Itoa(i),
			Custom: true, Navigable: true, Searchable: true, Orderable: true,
			ClauseNames: []string{id},
			Schema: jira.FieldSchema{
				Type:     "string",
				Custom:   "com.atlassian.jira.plugin.system.customfieldtypes:textfield",
				CustomID: 21000 + i,
			},
		})
	}
	return out
}

func resolverOn(tb testing.TB, fields []jira.Field) *Search {
	tb.Helper()

	s := NewSearch(jiratest.New(jiratest.WithFields(fields)))
	if _, err := s.Fields(tb.Context()); err != nil {
		tb.Fatalf("warming the catalogue: %v", err)
	}
	return s
}

// BenchmarkResolveList is the resolve a keystroke pays for: six platform IDs,
// no catalogue.
func BenchmarkResolveList(b *testing.B) {
	s := resolverOn(b, configuredSite())
	ctx := b.Context()
	p := ListProjection()
	b.ReportAllocs()
	b.ResetTimer()
	for range b.N {
		if _, err := s.Resolve(ctx, p); err != nil {
			b.Fatalf("Resolve: %v", err)
		}
	}
}

// BenchmarkResolveDetail is what opening an issue costs on a site with 57 custom
// fields, once the catalogue is held.
func BenchmarkResolveDetail(b *testing.B) {
	s := resolverOn(b, configuredSite())
	ctx := b.Context()
	p := DetailProjection()
	b.ReportAllocs()
	b.ResetTimer()
	for range b.N {
		if _, err := s.Resolve(ctx, p); err != nil {
			b.Fatalf("Resolve: %v", err)
		}
	}
}
