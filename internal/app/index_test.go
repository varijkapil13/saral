package app

import (
	"errors"
	"fmt"
	"slices"
	"testing"
	"time"

	"github.com/varijkapil13/saral/pkg/jira"
)

var indexStored = time.Date(2026, time.March, 1, 8, 30, 0, 0, time.UTC)

// stubCorpus is an IssueCorpus over a slice. It counts its walks, so a test can
// assert that a rebuild did not happen.
type stubCorpus struct {
	issues []jira.Issue
	stored time.Time
	gen    uint64
	walks  int
	drops  int
	fail   error
}

func (c *stubCorpus) EachIssue(fn func(jira.Issue, time.Time) bool) (int, error) {
	c.walks++
	for i := range c.issues {
		if !fn(c.issues[i], c.stored) {
			break
		}
	}
	return c.drops, c.fail
}

func (c *stubCorpus) Generation() uint64 { return c.gen }

func newStubCorpus(issues ...jira.Issue) *stubCorpus {
	return &stubCorpus{issues: issues, stored: indexStored, gen: 1}
}

// titledIssue is an issue stored by a read that asked for its title.
func titledIssue(key, summary string) jira.Issue {
	return jira.Issue{
		Key: key, Summary: summary,
		Requested: jira.NewFieldMask(ListProjection().IDs),
	}
}

// untitledIssue is an issue stored by a read so narrow it never asked for the
// title, which is not the same as an issue with no title.
func untitledIssue(key string) jira.Issue {
	return jira.Issue{Key: key, Requested: jira.NewFieldMask([]string{"status"})}
}

func hitKeys(hits []Hit) []string {
	out := make([]string, len(hits))
	for i, h := range hits {
		out[i] = h.Key
	}
	return out
}

func searched(t *testing.T, ix *Index, text string, limit int) []Hit {
	t.Helper()

	hits, err := ix.Search(text, limit)
	if err != nil {
		t.Fatalf("searching for %q: %v", text, err)
	}
	return hits
}

func TestIndex_FindsAnIssueByItsKeyAndByWordsOfItsTitle(t *testing.T) {
	t.Parallel()

	ix := NewIndex(newStubCorpus(
		titledIssue("PROJ-1", "Fix the login flow"),
		titledIssue("PROJ-2", "Speed up the nightly export"),
		titledIssue("OPS-14", "Rework webhook retries"),
	))

	cases := []struct {
		name string
		text string
		want []string
	}{
		{name: "a whole key", text: "PROJ-2", want: []string{"PROJ-2"}},
		{name: "a key typed in lower case", text: "ops-14", want: []string{"OPS-14"}},
		{name: "the digits of a key", text: "14", want: []string{"OPS-14"}},
		{name: "a word of a title", text: "login", want: []string{"PROJ-1"}},
		{name: "two words of a title", text: "nightly export", want: []string{"PROJ-2"}},
		{name: "letters scattered through a title", text: "wbhk", want: []string{"OPS-14"}},
		{name: "a word two titles share", text: "the", want: []string{"PROJ-1", "PROJ-2"}},
		{name: "nothing any issue has", text: "kubernetes", want: nil},
		{name: "a pattern longer than every key and title", text: "a pattern nothing here is remotely as long as", want: nil},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := hitKeys(searched(t, ix, tc.text, 10)); !slices.Equal(got, tc.want) {
				t.Errorf("searching for %q found %v, want %v", tc.text, got, tc.want)
			}
		})
	}
}

func TestIndex_RanksAKeyPrefixAboveATitleWordAboveAScatteredMatch(t *testing.T) {
	t.Parallel()

	ix := NewIndex(newStubCorpus(
		titledIssue("PROJ-14", "Investigate session expiry"),
		titledIssue("PROJ-142", "Add the release checklist"),
		titledIssue("OPS-7", "Rework the PROJ-14 handover"),
		titledIssue("OPS-8", "Prepare the report on old jobs"),
		titledIssue("OPS-9", "Retire the audit trail"),
	))

	want := []string{
		"PROJ-14",  // the key, from its first rune
		"PROJ-142", // the same, on a longer key
		"OPS-7",    // a word of the title
		"OPS-8",    // scattered through the title
	}
	if got := hitKeys(searched(t, ix, "proj", 10)); !slices.Equal(got, want) {
		t.Errorf("searching for %q ranked %v, want %v", "proj", got, want)
	}
}

func TestIndex_RanksAnIssueByWhicheverOfItsKeyAndTitleMatchesBetter(t *testing.T) {
	t.Parallel()

	ix := NewIndex(newStubCorpus(
		titledIssue("PROJ-9", "export"),
		titledIssue("EXPORT-1", "Fix the nightly job"),
	))

	hits := searched(t, ix, "export", 10)
	if got := hitKeys(hits); !slices.Equal(got, []string{"PROJ-9", "EXPORT-1"}) {
		t.Errorf("searching for %q ranked %v; the title that is the whole pattern beats a key that only starts with it",
			"export", got)
	}
}

func TestIndex_MatchesOnTheKeyAloneWhenNothingAskedForTheTitle(t *testing.T) {
	t.Parallel()

	ix := NewIndex(newStubCorpus(
		untitledIssue("PROJ-1"),
		titledIssue("PROJ-2", "Retire the audit trail"),
	))

	hits := searched(t, ix, "proj", 10)
	if got := hitKeys(hits); !slices.Equal(got, []string{"PROJ-1", "PROJ-2"}) {
		t.Errorf("searching for %q found %v, want both keys", "proj", got)
	}
	if hits[0].HasSummary || hits[0].Summary != "" {
		t.Errorf("%s reports a title of %q; nothing asked the site for one", hits[0].Key, hits[0].Summary)
	}
	if !hits[1].HasSummary {
		t.Errorf("%s reports no title, but the read that stored it asked for one", hits[1].Key)
	}
	if got := hitKeys(searched(t, ix, "audit", 10)); !slices.Equal(got, []string{"PROJ-2"}) {
		t.Errorf("searching for %q found %v; an issue with no title cannot match a word of one", "audit", got)
	}
}

func TestIndex_KeepsATitleItWasGivenEvenWhenNoReadClaimsIt(t *testing.T) {
	t.Parallel()

	ix := NewIndex(newStubCorpus(jira.Issue{Key: "PROJ-3", Summary: "Document the release checklist"}))

	hits := searched(t, ix, "release", 10)
	if len(hits) != 1 || !hits[0].HasSummary {
		t.Fatalf("an issue carrying a title was indexed as %+v; a title that is there is an answer", hits)
	}
}

func TestIndex_FindsATranslatedTitle(t *testing.T) {
	t.Parallel()

	ix := NewIndex(newStubCorpus(
		titledIssue("PROJ-1", "会議のサポート体制を見直す"),
		titledIssue("PROJ-2", "Überprüfung der Rechnungsrundung"),
		titledIssue("PROJ-3", "Corriger le café du matin"),
	))

	cases := []struct {
		text string
		want []string
	}{
		{text: "サポート", want: []string{"PROJ-1"}},
		{text: "überprüfung", want: []string{"PROJ-2"}},
		{text: "RECHNUNG", want: []string{"PROJ-2"}},
		{text: "café", want: []string{"PROJ-3"}},
	}
	for _, tc := range cases {
		t.Run(tc.text, func(t *testing.T) {
			if got := hitKeys(searched(t, ix, tc.text, 10)); !slices.Equal(got, tc.want) {
				t.Errorf("searching for %q found %v, want %v", tc.text, got, tc.want)
			}
		})
	}
}

func TestIndex_DoesNotWalkTheCorpusAgainWhileItsGenerationStandsStill(t *testing.T) {
	t.Parallel()

	corpus := newStubCorpus(titledIssue("PROJ-1", "Fix the login flow"))
	ix := NewIndex(corpus)

	for _, text := range []string{"l", "lo", "log", "logi", "login"} {
		searched(t, ix, text, 10)
	}
	if corpus.walks != 1 {
		t.Errorf("five keystrokes walked the corpus %d times, want the generation to have saved all but the first", corpus.walks)
	}
	if walked, err := ix.Refresh(); walked || err != nil {
		t.Errorf("Refresh reported walked=%v err=%v with nothing changed, want false and no error", walked, err)
	}
}

func TestIndex_WalksAgainOnceTheCorpusHasMoved(t *testing.T) {
	t.Parallel()

	corpus := newStubCorpus(titledIssue("PROJ-1", "Fix the login flow"))
	ix := NewIndex(corpus)
	searched(t, ix, "login", 10)

	corpus.issues = append(corpus.issues, titledIssue("PROJ-2", "Retire the login banner"))
	corpus.gen++

	hits := searched(t, ix, "login", 10)
	if corpus.walks != 2 {
		t.Errorf("the corpus was walked %d times across a change to it, want twice", corpus.walks)
	}
	if got := hitKeys(hits); !slices.Equal(got, []string{"PROJ-1", "PROJ-2"}) {
		t.Errorf("after the corpus grew, searching found %v, want both issues", got)
	}
	if ix.Len() != 2 {
		t.Errorf("the index holds %d issues, want 2", ix.Len())
	}
}

func TestIndex_ReportsAnIssueItCouldNotReadWithoutRetryingItEveryKeystroke(t *testing.T) {
	t.Parallel()

	corpus := newStubCorpus(titledIssue("PROJ-1", "Fix the login flow"))
	corpus.fail = errors.New("the cached copy of PROJ-2 cannot be read")
	ix := NewIndex(corpus)

	hits, err := ix.Search("login", 10)
	if err == nil {
		t.Fatal("a corpus that could not be read entirely reported no error")
	}
	if got := hitKeys(hits); !slices.Equal(got, []string{"PROJ-1"}) {
		t.Errorf("a failed walk answered with %v, want the issues it did read", got)
	}

	if _, err := ix.Search("login", 10); err != nil {
		t.Errorf("the second keystroke reported %v; a failed walk is not retried until the corpus moves", err)
	}
	if corpus.walks != 1 {
		t.Errorf("a corpus that failed was walked %d times over two keystrokes, want once", corpus.walks)
	}
}

func TestIndex_FindsNothingAndSaysNothingWithNowhereToCache(t *testing.T) {
	t.Parallel()

	var absent Cache
	for _, tc := range []struct {
		name string
		ix   *Index
	}{
		{name: "a session with no cache at all", ix: NewIndex(nil)},
		{name: "a session holding a nil cache", ix: NewIndex(absent)},
		{name: "a nil index", ix: nil},
		{name: "an index nobody built", ix: new(Index)},
	} {
		t.Run(tc.name, func(t *testing.T) {
			hits, err := tc.ix.Search("login", 10)
			if err != nil || len(hits) != 0 {
				t.Errorf("searching found %v, %v; want nothing and no error", hits, err)
			}
			if walked, err := tc.ix.Refresh(); walked || err != nil {
				t.Errorf("Refresh reported walked=%v err=%v, want false and no error", walked, err)
			}
			if tc.ix.Len() != 0 {
				t.Errorf("the index holds %d issues, want none", tc.ix.Len())
			}
		})
	}
}

func TestIndex_BoundsWhatItHandsBackToWhatWasAskedFor(t *testing.T) {
	t.Parallel()

	issues := make([]jira.Issue, 0, 40)
	for i := 1; i <= 40; i++ {
		issues = append(issues, titledIssue(fmt.Sprintf("PROJ-%d", i), "Fix the login flow"))
	}
	ix := NewIndex(newStubCorpus(issues...))

	if got := len(searched(t, ix, "login", 5)); got != 5 {
		t.Errorf("a search bounded at 5 returned %d hits", got)
	}
	if got := len(searched(t, ix, "login", 500)); got != 40 {
		t.Errorf("a search bounded above the corpus returned %d hits, want all 40", got)
	}
	if got := len(searched(t, ix, "login", 0)); got != 40 {
		t.Errorf("an unbounded search returned %d hits, want all 40", got)
	}
}

func TestIndex_RanksTheSameWayBoundedAsUnbounded(t *testing.T) {
	t.Parallel()

	issues := make([]jira.Issue, 0, 60)
	for i := 1; i <= 60; i++ {
		issues = append(issues, titledIssue(fmt.Sprintf("PROJ-%d", i), fmt.Sprintf("Fix the login flow of stage %d", i)))
	}
	ix := NewIndex(newStubCorpus(issues...))

	all := hitKeys(searched(t, ix, "log", 0))
	few := hitKeys(searched(t, ix, "log", 8))
	if !slices.Equal(all[:len(few)], few) {
		t.Errorf("the first %d of an unbounded search are %v, and a bounded one returned %v", len(few), all[:len(few)], few)
	}
}

func TestIndex_AnEmptyPatternIsEveryIssueInKeyOrder(t *testing.T) {
	t.Parallel()

	ix := NewIndex(newStubCorpus(
		titledIssue("PROJ-1", "Fix the login flow"),
		titledIssue("PROJ-2", "Retire the audit trail"),
		untitledIssue("PROJ-3"),
	))

	want := []string{"PROJ-1", "PROJ-2", "PROJ-3"}
	if got := hitKeys(searched(t, ix, "", 10)); !slices.Equal(got, want) {
		t.Errorf("an empty pattern found %v, want %v", got, want)
	}
}

func TestIndex_SkipsAnIssueWithNoKeyToOpenItBy(t *testing.T) {
	t.Parallel()

	ix := NewIndex(newStubCorpus(
		jira.Issue{Summary: "Fix the login flow"},
		titledIssue("PROJ-2", "Fix the login banner"),
	))

	if got := hitKeys(searched(t, ix, "login", 10)); !slices.Equal(got, []string{"PROJ-2"}) {
		t.Errorf("searching found %v; an issue with no key is one nothing can open", got)
	}
}

func TestIndex_SaysWhenTheCopyItAnsweredFromWasStored(t *testing.T) {
	t.Parallel()

	ix := NewIndex(newStubCorpus(titledIssue("PROJ-1", "Fix the login flow")))

	hits := searched(t, ix, "login", 10)
	if len(hits) != 1 {
		t.Fatalf("searching found %d hits, want 1", len(hits))
	}
	if !hits[0].StoredAt.Equal(indexStored) {
		t.Errorf("the hit was stored at %s, want %s; a caller badges staleness against its own clock",
			hits[0].StoredAt, indexStored)
	}
}

func TestIndex_HandsBackASliceTheCallerKeeps(t *testing.T) {
	t.Parallel()

	ix := NewIndex(newStubCorpus(
		titledIssue("PROJ-1", "Fix the login flow"),
		titledIssue("PROJ-2", "Retire the login banner"),
	))

	held := searched(t, ix, "login", 10)
	searched(t, ix, "retire", 10)

	if got := hitKeys(held); !slices.Equal(got, []string{"PROJ-1", "PROJ-2"}) {
		t.Errorf("hits held across a second search became %v; the answer is the caller's own", got)
	}
}

// The stub above is a slice. This one is the cache a session actually holds:
// bbolt keys the issues, DiskCache decodes them, and the generation moves
// because rows were written.
func TestIndex_SearchesTheIssuesTheDiskCacheHolds(t *testing.T) {
	t.Parallel()

	cache, _ := newTestCache(t)
	rows := listRows(30)
	if err := cache.PutRows(cacheJQL, rows, false); err != nil {
		t.Fatalf("storing rows: %v", err)
	}

	ix := NewIndex(cache)
	if got := hitKeys(searched(t, ix, "PROJ-7", 5)); len(got) == 0 || got[0] != "PROJ-7" {
		t.Errorf("searching the disk cache for %q ranked %v", "PROJ-7", got)
	}
	if ix.Len() != len(rows) {
		t.Errorf("the index holds %d of the %d issues stored", ix.Len(), len(rows))
	}

	hits := searched(t, ix, rows[3].Summary, 5)
	if len(hits) == 0 || hits[0].Summary != rows[3].Summary {
		t.Errorf("searching for a stored title ranked %v", hitKeys(hits))
	}

	before := ix.Len()
	if err := cache.PutRows(`project = "OPS" ORDER BY key`, listRows(4), false); err != nil {
		t.Fatalf("storing a second search: %v", err)
	}
	searched(t, ix, "", 1)
	if ix.Len() != before {
		t.Errorf("the index holds %d issues after a second search stored the same keys, want %d", ix.Len(), before)
	}
}

// The count a walk skipped is carried to whoever draws the index: it is the only
// account there is of a corpus holding less than it looks, because a skipped
// record leaves no error behind.
func TestDropped_CarriesTheCountTheLastWalkSkipped(t *testing.T) {
	t.Parallel()

	corpus := newStubCorpus(listRows(3)...)
	corpus.drops = 2
	ix := NewIndex(corpus)
	if got := ix.Dropped(); got != 0 {
		t.Errorf("an index that has not walked reports %d dropped, want 0", got)
	}

	if walked, err := ix.Refresh(); !walked || err != nil {
		t.Fatalf("Refresh walked=%v: %v", walked, err)
	}
	if got := ix.Dropped(); got != 2 {
		t.Errorf("the index reports %d dropped records, want 2", got)
	}
	if got := ix.Len(); got != 3 {
		t.Errorf("the index holds %d issues, want the 3 the walk could read", got)
	}

	corpus.drops, corpus.gen = 0, corpus.gen+1
	if walked, err := ix.Refresh(); !walked || err != nil {
		t.Fatalf("the second Refresh walked=%v: %v", walked, err)
	}
	if got := ix.Dropped(); got != 0 {
		t.Errorf("a walk that skipped nothing still reports %d dropped, so the count is never cleared", got)
	}
}

func TestDropped_OverANilIndexIsZeroRatherThanAPanic(t *testing.T) {
	t.Parallel()

	var ix *Index
	if got := ix.Dropped(); got != 0 {
		t.Errorf("a nil index reports %d dropped", got)
	}
}

// indexOf10k is the corpus the budgets in docs/PERFORMANCE.md are written
// against: the cache's own bound is 5,000 issues, so ten thousand is twice the
// worst case a session can reach.
func indexOf10k(tb testing.TB) *Index {
	tb.Helper()

	ix := NewIndex(newStubCorpus(listRows(10000)...))
	if _, err := ix.Refresh(); err != nil {
		tb.Fatalf("building the index: %v", err)
	}
	return ix
}

// BenchmarkIndexSearch10k is the keystroke: a pattern typed into a screenful of
// results over ten thousand cached issues.
func BenchmarkIndexSearch10k(b *testing.B) {
	ix := indexOf10k(b)
	b.ReportAllocs()
	b.ResetTimer()
	for range b.N {
		if _, err := ix.Search("log", 20); err != nil {
			b.Fatalf("Search: %v", err)
		}
	}
}

// BenchmarkIndexSearchOneRune10k is the worst keystroke there is: the first
// letter typed, which nearly every issue matches, so the ranking cannot skip
// anything.
func BenchmarkIndexSearchOneRune10k(b *testing.B) {
	ix := indexOf10k(b)
	b.ReportAllocs()
	b.ResetTimer()
	for range b.N {
		if _, err := ix.Search("e", 20); err != nil {
			b.Fatalf("Search: %v", err)
		}
	}
}

// BenchmarkIndexSearchUnbounded10k is what asking for every hit costs, which is
// a sort over the corpus rather than a compare against the worst hit held.
func BenchmarkIndexSearchUnbounded10k(b *testing.B) {
	ix := indexOf10k(b)
	b.ReportAllocs()
	b.ResetTimer()
	for range b.N {
		if _, err := ix.Search("log", 0); err != nil {
			b.Fatalf("Search: %v", err)
		}
	}
}

// BenchmarkIndexRebuild10k is the cost the generation counter is there to avoid
// paying on every keystroke.
func BenchmarkIndexRebuild10k(b *testing.B) {
	corpus := newStubCorpus(listRows(10000)...)
	ix := NewIndex(corpus)
	b.ReportAllocs()
	b.ResetTimer()
	for i := range b.N {
		corpus.gen = uint64(i + 1)
		if _, err := ix.Refresh(); err != nil {
			b.Fatalf("Refresh: %v", err)
		}
	}
}
