package search

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	appcache "github.com/varijkapil13/saral/internal/app/cache"
	"github.com/varijkapil13/saral/internal/app/cache/cachetest"
	"github.com/varijkapil13/saral/pkg/jira"
	"github.com/varijkapil13/saral/pkg/jira/jiratest"
)

const listJQL = `project = "PROJ" ORDER BY key`

var listFailures = []struct {
	name string
	err  error
	is   func(error) bool
}{
	{"a 403", &jira.CapabilityError{Capability: jira.CapPeople, Reason: "not for this token"}, func(err error) bool {
		var target *jira.CapabilityError
		return errors.As(err, &target)
	}},
	{"a 429", &jira.RateLimitError{RetryAfter: time.Second}, func(err error) bool {
		var target *jira.RateLimitError
		return errors.As(err, &target)
	}},
	{"a transport failure", &jira.TransportError{Op: "search", Status: 503}, func(err error) bool {
		var target *jira.TransportError
		return errors.As(err, &target)
	}},
}

func listFake(n int, opts ...jiratest.Option) *jiratest.Fake {
	return jiratest.New(append([]jiratest.Option{
		jiratest.WithProject("PROJ", jiratest.Scrum), jiratest.WithIssues(jiratest.Gen(n)),
	}, opts...)...)
}

// listRecorder keeps every query a search was asked.
type listRecorder struct {
	*jiratest.Fake
	mu  sync.Mutex
	got []jira.Query
}

func (r *listRecorder) Search(ctx context.Context, q jira.Query) (jira.Page[jira.Issue], error) {
	r.mu.Lock()
	r.got = append(r.got, q)
	r.mu.Unlock()
	return r.Fake.Search(ctx, q)
}

// listBrokenCache is a cache that will not take a write.
type listBrokenCache struct{ appcache.Cache }

var errListDiskFull = errors.New("disk full")

func (listBrokenCache) PutRows(string, []jira.Issue, bool) error { return errListDiskFull }

func (listBrokenCache) Forget(string) error { return errListDiskFull }

func TestLister_FirstReadsOnePageAndStoresIt(t *testing.T) {
	t.Parallel()

	rec := &listRecorder{Fake: listFake(120)}
	cache := cachetest.Open(t)
	l := NewLister(rec, cache, rec)

	got, err := l.First(t.Context(), listJQL, ListProjection())
	if err != nil {
		t.Fatalf("First: %v", err)
	}
	if len(got.Page.Items) != listPageSize || !got.Page.HasMore() || got.Stored != nil {
		t.Fatalf("First read %d rows (more=%v, stored=%v), want %d and more", len(got.Page.Items), got.Page.HasMore(), got.Stored, listPageSize)
	}
	if rec.got[0].MaxResults != listPageSize {
		t.Errorf("asked for %d rows a page, want %d", rec.got[0].MaxResults, listPageSize)
	}
	snap, ok := l.Stored(listJQL)
	if !ok || len(snap.Issues) != listPageSize || !snap.More {
		t.Errorf("stored %d rows (ok=%v, more=%v), want the page", len(snap.Issues), ok, snap.More)
	}
}

func TestLister_ACacheThatCannotBeWrittenStillHandsBackTheRows(t *testing.T) {
	t.Parallel()

	f := listFake(10)
	l := NewLister(f, listBrokenCache{cachetest.Open(t)}, f)

	got, err := l.First(t.Context(), listJQL, ListProjection())
	if err != nil {
		t.Fatalf("First: %v", err)
	}
	if !errors.Is(got.Stored, errListDiskFull) {
		t.Errorf("the failed write came back as %v, want it beside the rows", got.Stored)
	}
	if len(got.Page.Items) != 10 {
		t.Errorf("got %d rows, want all 10 despite the cache", len(got.Page.Items))
	}
}

func TestLister_NextStoresTheWholeOfWhatWasScrolled(t *testing.T) {
	t.Parallel()

	f := listFake(120)
	l := NewLister(f, cachetest.Open(t), f)
	first, err := l.First(t.Context(), listJQL, ListProjection())
	if err != nil {
		t.Fatalf("First: %v", err)
	}

	next, err := l.Next(t.Context(), listJQL, first.Page.Items, first.Page)
	if err != nil {
		t.Fatalf("Next: %v", err)
	}
	if len(next.Page.Items) != listPageSize || next.Page.Items[0].Key == first.Page.Items[0].Key {
		t.Fatalf("Next did not read the page after the first")
	}
	snap, _ := l.Stored(listJQL)
	if len(snap.Issues) != 2*listPageSize {
		t.Errorf("stored %d rows, want both pages", len(snap.Issues))
	}
}

func TestLister_ReloadWalksUntilItHasWhatWasAskedFor(t *testing.T) {
	t.Parallel()

	f := listFake(120, jiratest.WithPageSize(20))
	l := NewLister(f, nil, f)

	got, err := l.Reload(t.Context(), listJQL, ListProjection(), 50)
	if err != nil {
		t.Fatalf("Reload: %v", err)
	}
	if len(got.Issues) != 60 || !got.Page.HasMore() {
		t.Errorf("reloaded %d rows (more=%v), want three pages of 20 and more", len(got.Issues), got.Page.HasMore())
	}
	if got.Stored != nil {
		t.Errorf("nowhere to store reported %v", got.Stored)
	}

	on, err := l.PageOn(t.Context(), listJQL, ListProjection(), 60)
	if err != nil {
		t.Fatalf("PageOn: %v", err)
	}
	if len(on.Issues) != 120 {
		t.Errorf("paging on from 60 stored rows read %d, want at least a page more", len(on.Issues))
	}
}

func TestLister_HasAssignedAsksForOneNarrowRow(t *testing.T) {
	t.Parallel()

	rec := &listRecorder{Fake: listFake(5)}
	l := NewLister(rec, nil, rec)

	has, err := l.HasAssigned(t.Context(), listJQL)
	if err != nil || !has {
		t.Fatalf("HasAssigned = %v, %v, want true", has, err)
	}
	if rec.got[0].MaxResults != 1 {
		t.Errorf("the probe asked for %d rows, want 1", rec.got[0].MaxResults)
	}

	none, err := l.HasAssigned(t.Context(), `project = "NOPE"`)
	if err != nil || none {
		t.Errorf("HasAssigned over nothing = %v, %v, want false", none, err)
	}
}

func TestLister_RevalidateReadsTheIssueByTheFieldsItWasDrawnWith(t *testing.T) {
	t.Parallel()

	f := listFake(3)
	l := NewLister(f, nil, f)
	iss, err := l.Revalidate(t.Context(), "PROJ-1", []string{"summary", "status"})
	if err != nil {
		t.Fatalf("Revalidate: %v", err)
	}
	if iss.Key != "PROJ-1" || !iss.Requested.Has("status") {
		t.Errorf("revalidated %s with %v, want PROJ-1 with its status", iss.Key, iss.Requested.IDs())
	}
}

func TestLister_PassesEveryFailureThroughAsTheSiteSaidIt(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name string
		// run fails the next port call with err and makes the use case's call.
		run func(ctx context.Context, l *Lister, f *jiratest.Fake, err error) error
	}{
		{"First", func(ctx context.Context, l *Lister, f *jiratest.Fake, fail error) error {
			f.FailNext(fail)
			_, err := l.First(ctx, listJQL, ListProjection())
			return err
		}},
		{"Next", func(ctx context.Context, l *Lister, f *jiratest.Fake, fail error) error {
			first, err := l.First(ctx, `project = "PROJ" ORDER BY created`, ListProjection())
			if err != nil {
				return err
			}
			f.FailNext(fail)
			_, err = l.Next(ctx, listJQL, first.Page.Items, first.Page)
			return err
		}},
		{"Reload", func(ctx context.Context, l *Lister, f *jiratest.Fake, fail error) error {
			f.FailNext(fail)
			_, err := l.Reload(ctx, listJQL, ListProjection(), 1)
			return err
		}},
		{"HasAssigned", func(ctx context.Context, l *Lister, f *jiratest.Fake, fail error) error {
			f.FailNext(fail)
			_, err := l.HasAssigned(ctx, listJQL)
			return err
		}},
		{"Revalidate", func(ctx context.Context, l *Lister, f *jiratest.Fake, fail error) error {
			f.FailNext(fail)
			_, err := l.Revalidate(ctx, "PROJ-1", []string{"summary"})
			return err
		}},
	}
	for _, uc := range cases {
		for _, fail := range listFailures {
			t.Run(uc.name+" meets "+fail.name, func(t *testing.T) {
				t.Parallel()

				f := listFake(120)
				l := NewLister(f, cachetest.Open(t), f)
				if err := uc.run(t.Context(), l, f, fail.err); !fail.is(err) {
					t.Errorf("failed with %v, want the site's own %T", err, fail.err)
				}
				if _, ok := l.Stored(listJQL); ok {
					t.Error("a failed read stored rows anyway")
				}
			})
		}
	}
}

func TestLister_PurgeForgetsTheStoredRowsAndSaysWhenItCannot(t *testing.T) {
	t.Parallel()

	f := listFake(5)
	l := NewLister(f, cachetest.Open(t), f)
	if _, err := l.First(t.Context(), listJQL, ListProjection()); err != nil {
		t.Fatalf("First: %v", err)
	}
	if err := l.Purge(listJQL); err != nil {
		t.Fatalf("Purge: %v", err)
	}
	if _, ok := l.Stored(listJQL); ok {
		t.Error("the purged rows are still stored")
	}

	broken := NewLister(f, listBrokenCache{cachetest.Open(t)}, f)
	if err := broken.Purge(listJQL); !errors.Is(err, errListDiskFull) {
		t.Errorf("Purge over a broken cache = %v, want its error", err)
	}
}

func TestLister_WithNoSiteAndNoCacheIsQuiet(t *testing.T) {
	t.Parallel()

	l := NewLister(nil, nil, nil)
	if l.Live() {
		t.Error("a lister with no client claims a site to ask")
	}
	if _, ok := l.Stored(listJQL); ok {
		t.Error("nowhere to cache produced stored rows")
	}
	if err := l.Store(listJQL, jiratest.Gen(1), false); err != nil {
		t.Errorf("storing nowhere failed: %v", err)
	}
	if err := l.Purge(listJQL); err != nil {
		t.Errorf("purging nowhere failed: %v", err)
	}
}

func TestDiffRows_CountsByKeyAndByWhenEachWasTouched(t *testing.T) {
	t.Parallel()

	at := func(key string, mins int) jira.Issue {
		return jira.Issue{Key: key, Updated: time.Date(2026, 3, 1, 8, mins, 0, 0, time.UTC)}
	}
	tests := []struct {
		name          string
		before, after []jira.Issue
		want          RowChange
	}{
		{"nothing at all", nil, nil, RowChange{}},
		{"the same rows", []jira.Issue{at("A-1", 0)}, []jira.Issue{at("A-1", 0)}, RowChange{}},
		{"one new", nil, []jira.Issue{at("A-1", 0)}, RowChange{Added: 1}},
		{"one gone", []jira.Issue{at("A-1", 0)}, nil, RowChange{Gone: 1}},
		{"one touched", []jira.Issue{at("A-1", 0)}, []jira.Issue{at("A-1", 5)}, RowChange{Updated: 1}},
		{"all three", []jira.Issue{at("A-1", 0), at("A-2", 0)}, []jira.Issue{at("A-1", 5), at("A-3", 0)}, RowChange{Added: 1, Gone: 1, Updated: 1}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got := DiffRows(tc.before, tc.after)
			if got != tc.want {
				t.Errorf("DiffRows = %+v, want %+v", got, tc.want)
			}
			if got.Any() != (tc.want != RowChange{}) {
				t.Errorf("Any() = %v for %+v", got.Any(), got)
			}
		})
	}
}
