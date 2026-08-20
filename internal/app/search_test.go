package app

import (
	"context"
	"errors"
	"runtime"
	"slices"
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

	var entered atomic.Int64
	results := make(chan error, callers)
	for range callers {
		go func() {
			entered.Add(1)
			_, err := s.Run(t.Context(), Request{JQL: testJQL, Projection: ListProjection()})
			results <- err
		}()
	}

	<-blocking.arrived
	waitFor(t, "every caller to have reached the search", func() bool {
		return entered.Load() == callers
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
