package jira_test

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"sync"
	"testing"

	"github.com/varijkapil13/saral/pkg/jira"
)

var errFetch = errors.New("jira_test: the fetch failed")

type cursorStep struct {
	items []string
	next  string
}

type cursorScript struct {
	steps map[string]cursorStep
	calls int
}

func (s *cursorScript) fetch(_ context.Context, token string) (items []string, nextToken string, err error) {
	s.calls++
	step, ok := s.steps[token]
	if !ok {
		return nil, "", fmt.Errorf("%w: unscripted token %q", errFetch, token)
	}
	return step.items, step.next, nil
}

type offsetStep struct {
	items  []string
	total  int
	isLast bool
}

type offsetScript struct {
	steps map[int]offsetStep
	calls int
}

func (s *offsetScript) fetch(_ context.Context, startAt int) (items []string, total int, isLast bool, err error) {
	s.calls++
	step, ok := s.steps[startAt]
	if !ok {
		return nil, 0, false, fmt.Errorf("%w: unscripted startAt %d", errFetch, startAt)
	}
	return step.items, step.total, step.isLast, nil
}

func TestCursor_WalksEveryPageAndThenRefusesToWalkFurther(t *testing.T) {
	t.Parallel()

	script := &cursorScript{steps: map[string]cursorStep{
		"":   {items: []string{"a", "b"}, next: "t1"},
		"t1": {items: []string{"c", "d"}, next: "t2"},
		"t2": {items: []string{"e"}},
	}}
	ctx := context.Background()

	page, err := jira.Cursor(ctx, script.fetch)
	if err != nil {
		t.Fatalf("Cursor: %v", err)
	}

	var got []string
	pages := 0
	for {
		got = append(got, page.Items...)
		pages++
		if !page.HasMore() {
			break
		}
		if page, err = page.Next(ctx); err != nil {
			t.Fatalf("Next after %d pages: %v", pages, err)
		}
	}

	if want := []string{"a", "b", "c", "d", "e"}; !slices.Equal(got, want) {
		t.Errorf("items = %q, want %q", got, want)
	}
	if pages != 3 || script.calls != 3 {
		t.Errorf("walked %d pages in %d fetches, want 3 and 3", pages, script.calls)
	}
	if _, err := page.Next(ctx); !errors.Is(err, jira.ErrNoMorePages) {
		t.Errorf("Next on the last page = %v, want ErrNoMorePages", err)
	}
	if script.calls != 3 {
		t.Errorf("Next on the last page fetched again: %d fetches, want 3", script.calls)
	}
}

func TestCursor_ReportsNoApproximateTotal(t *testing.T) {
	t.Parallel()

	script := &cursorScript{steps: map[string]cursorStep{
		"":   {items: []string{"a"}, next: "t1"},
		"t1": {items: []string{"b"}},
	}}
	ctx := context.Background()

	page, err := jira.Cursor(ctx, script.fetch)
	if err != nil {
		t.Fatalf("Cursor: %v", err)
	}
	for {
		if page.ApproxTotal != nil {
			t.Errorf("ApproxTotal = %d, want nil on a cursor page", *page.ApproxTotal)
		}
		if total, ok := page.Count(); ok {
			t.Errorf("Count() = (%d, true), want (0, false) on a cursor page", total)
		}
		if !page.HasMore() {
			return
		}
		if page, err = page.Next(ctx); err != nil {
			t.Fatalf("Next: %v", err)
		}
	}
}

func TestCursor_TreatsATokenItHasAlreadySeenAsExhaustion(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name      string
		steps     map[string]cursorStep
		want      []string
		wantCalls int
	}{
		{
			name: "a token that loops straight back to the page it came from",
			steps: map[string]cursorStep{
				"":   {items: []string{"a"}, next: "t1"},
				"t1": {items: []string{"b"}, next: "t1"},
			},
			want:      []string{"a", "b"},
			wantCalls: 2,
		},
		{
			name: "a token that loops back after three distinct pages",
			steps: map[string]cursorStep{
				"":   {items: []string{"a"}, next: "t1"},
				"t1": {items: []string{"b"}, next: "t2"},
				"t2": {items: []string{"c"}, next: "t3"},
				"t3": {items: []string{"d"}, next: "t1"},
			},
			want:      []string{"a", "b", "c", "d"},
			wantCalls: 4,
		},
		{
			name: "a token that loops back to the middle of the walk",
			steps: map[string]cursorStep{
				"":   {items: []string{"a"}, next: "t1"},
				"t1": {items: []string{"b"}, next: "t2"},
				"t2": {items: []string{"c"}, next: "t2"},
			},
			want:      []string{"a", "b", "c"},
			wantCalls: 3,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			script := &cursorScript{steps: tt.steps}
			ctx := context.Background()

			page, err := jira.Cursor(ctx, script.fetch)
			if err != nil {
				t.Fatalf("Cursor: %v", err)
			}
			got, err := jira.Collect(ctx, page, 0)
			if err != nil {
				t.Fatalf("Collect: %v", err)
			}
			if !slices.Equal(got, tt.want) {
				t.Errorf("items = %q, want %q: the looped page must not be delivered", got, tt.want)
			}
			if script.calls != tt.wantCalls {
				t.Errorf("fetched %d times, want %d: the repeated token must not be followed", script.calls, tt.wantCalls)
			}
		})
	}
}

func TestOffset_TerminatesOnEveryEndOfResultsSignalTheAgileAPISends(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name         string
		steps        map[int]offsetStep
		want         []string
		wantTotal    int
		wantHasTotal bool
		wantCalls    int
	}{
		{
			name: "the server sets isLast on the final page",
			steps: map[int]offsetStep{
				0: {items: []string{"a", "b"}, total: 5},
				2: {items: []string{"c", "d"}, total: 5},
				4: {items: []string{"e"}, total: 5, isLast: true},
			},
			want:         []string{"a", "b", "c", "d", "e"},
			wantTotal:    5,
			wantHasTotal: true,
			wantCalls:    3,
		},
		{
			name: "the server promises more than it will hand over",
			steps: map[int]offsetStep{
				0: {items: []string{"a", "b"}, total: 100},
				2: {items: nil, total: 100},
			},
			want:         []string{"a", "b"},
			wantTotal:    100,
			wantHasTotal: true,
			wantCalls:    2,
		},
		{
			name: "the server never sets isLast but the offset reaches the total",
			steps: map[int]offsetStep{
				0: {items: []string{"a", "b", "c"}, total: 6},
				3: {items: []string{"d", "e", "f"}, total: 6},
			},
			want:         []string{"a", "b", "c", "d", "e", "f"},
			wantTotal:    6,
			wantHasTotal: true,
			wantCalls:    2,
		},
		{
			name: "the server reports no total at all",
			steps: map[int]offsetStep{
				0: {items: []string{"a"}, total: -1, isLast: true},
			},
			want:         []string{"a"},
			wantHasTotal: false,
			wantCalls:    1,
		},
		{
			name:         "the first page is already empty",
			steps:        map[int]offsetStep{0: {items: nil, total: 0}},
			want:         nil,
			wantTotal:    0,
			wantHasTotal: true,
			wantCalls:    1,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			script := &offsetScript{steps: tt.steps}
			ctx := context.Background()

			page, err := jira.Offset(ctx, script.fetch)
			if err != nil {
				t.Fatalf("Offset: %v", err)
			}
			total, hasTotal := page.Count()
			if hasTotal != tt.wantHasTotal || (hasTotal && total != tt.wantTotal) {
				t.Errorf("Count() = (%d, %t), want (%d, %t)", total, hasTotal, tt.wantTotal, tt.wantHasTotal)
			}
			if !tt.wantHasTotal && page.ApproxTotal != nil {
				t.Errorf("ApproxTotal = %d, want nil when the server reports no total", *page.ApproxTotal)
			}

			got, err := jira.Collect(ctx, page, 0)
			if err != nil {
				t.Fatalf("Collect: %v", err)
			}
			if !slices.Equal(got, tt.want) {
				t.Errorf("items = %q, want %q", got, tt.want)
			}
			if script.calls != tt.wantCalls {
				t.Errorf("fetched %d times, want %d", script.calls, tt.wantCalls)
			}
		})
	}
}

func TestPage_PropagatesTheFetchErrorUntouched(t *testing.T) {
	t.Parallel()

	ctx := context.Background()

	t.Run("the first cursor fetch fails", func(t *testing.T) {
		t.Parallel()

		page, err := jira.Cursor(ctx, func(context.Context, string) ([]string, string, error) {
			return nil, "", errFetch
		})
		if !errors.Is(err, errFetch) {
			t.Errorf("Cursor = %v, want errFetch", err)
		}
		if page.HasMore() || len(page.Items) != 0 {
			t.Errorf("a failed Cursor returned %d items, HasMore=%t, want an empty exhausted page", len(page.Items), page.HasMore())
		}
	})

	t.Run("a later cursor fetch fails", func(t *testing.T) {
		t.Parallel()

		script := &cursorScript{steps: map[string]cursorStep{
			"": {items: []string{"a"}, next: "gone"},
		}}
		page, err := jira.Cursor(ctx, script.fetch)
		if err != nil {
			t.Fatalf("Cursor: %v", err)
		}
		next, err := page.Next(ctx)
		if !errors.Is(err, errFetch) {
			t.Errorf("Next = %v, want errFetch", err)
		}
		if next.HasMore() || len(next.Items) != 0 {
			t.Errorf("a failed Next returned %d items, HasMore=%t, want an empty exhausted page", len(next.Items), next.HasMore())
		}
	})

	t.Run("an offset fetch fails", func(t *testing.T) {
		t.Parallel()

		script := &offsetScript{steps: map[int]offsetStep{
			0: {items: []string{"a"}, total: 10},
		}}
		page, err := jira.Offset(ctx, script.fetch)
		if err != nil {
			t.Fatalf("Offset: %v", err)
		}
		if _, err := page.Next(ctx); !errors.Is(err, errFetch) {
			t.Errorf("Next = %v, want errFetch", err)
		}
	})

	t.Run("Collect hands back what it had when the fetch failed", func(t *testing.T) {
		t.Parallel()

		script := &cursorScript{steps: map[string]cursorStep{
			"":   {items: []string{"a", "b"}, next: "t1"},
			"t1": {items: []string{"c"}, next: "gone"},
		}}
		page, err := jira.Cursor(ctx, script.fetch)
		if err != nil {
			t.Fatalf("Cursor: %v", err)
		}
		got, err := jira.Collect(ctx, page, 0)
		if !errors.Is(err, errFetch) {
			t.Errorf("Collect = %v, want errFetch", err)
		}
		if want := []string{"a", "b", "c"}; !slices.Equal(got, want) {
			t.Errorf("items = %q, want the %q it collected before the failure", got, want)
		}
	})
}

func TestCollect_StopsAtTheLimitAndTruncatesExactly(t *testing.T) {
	t.Parallel()

	steps := map[string]cursorStep{
		"":   {items: []string{"a", "b", "c"}, next: "t1"},
		"t1": {items: []string{"d", "e", "f"}, next: "t2"},
		"t2": {items: []string{"g"}},
	}

	tests := []struct {
		name      string
		limit     int
		want      []string
		wantCalls int
	}{
		{name: "a limit inside the first page", limit: 2, want: []string{"a", "b"}, wantCalls: 1},
		{name: "a limit exactly on a page boundary", limit: 3, want: []string{"a", "b", "c"}, wantCalls: 1},
		{name: "a limit part way into the second page", limit: 4, want: []string{"a", "b", "c", "d"}, wantCalls: 2},
		{name: "a limit of zero walks everything", limit: 0, want: []string{"a", "b", "c", "d", "e", "f", "g"}, wantCalls: 3},
		{name: "a negative limit walks everything", limit: -1, want: []string{"a", "b", "c", "d", "e", "f", "g"}, wantCalls: 3},
		{name: "a limit beyond the result set", limit: 99, want: []string{"a", "b", "c", "d", "e", "f", "g"}, wantCalls: 3},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			script := &cursorScript{steps: steps}
			ctx := context.Background()

			page, err := jira.Cursor(ctx, script.fetch)
			if err != nil {
				t.Fatalf("Cursor: %v", err)
			}
			got, err := jira.Collect(ctx, page, tt.limit)
			if err != nil {
				t.Fatalf("Collect: %v", err)
			}
			if !slices.Equal(got, tt.want) {
				t.Errorf("items = %q, want %q", got, tt.want)
			}
			if script.calls != tt.wantCalls {
				t.Errorf("fetched %d times, want %d", script.calls, tt.wantCalls)
			}
		})
	}
}

func TestCollect_ReturnsTheContextErrorWhenTheWalkIsCanceled(t *testing.T) {
	t.Parallel()

	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)

	calls := 0
	fetch := func(_ context.Context, _ string) ([]string, string, error) {
		calls++
		if calls > 4 {
			return nil, "", fmt.Errorf("%w: the walk ignored the canceled context", errFetch)
		}
		if calls == 2 {
			cancel()
		}
		return []string{fmt.Sprintf("item-%d", calls)}, fmt.Sprintf("token-%d", calls), nil
	}

	page, err := jira.Cursor(ctx, fetch)
	if err != nil {
		t.Fatalf("Cursor: %v", err)
	}
	got, err := jira.Collect(ctx, page, 0)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("Collect = %v, want context.Canceled", err)
	}
	if want := []string{"item-1", "item-2"}; !slices.Equal(got, want) {
		t.Errorf("items = %q, want the %q collected before the cancellation", got, want)
	}
	if calls != 2 {
		t.Errorf("fetched %d times, want 2: the walk must stop at the first canceled check", calls)
	}
}

func TestPage_NextIsSafeToCallFromTwoGoroutinesAtOnce(t *testing.T) {
	t.Parallel()

	// The script is only read, so the fetch itself adds no shared state to the race under test.
	steps := map[string]cursorStep{
		"":   {items: []string{"a"}, next: "t1"},
		"t1": {items: []string{"b"}, next: "t2"},
		"t2": {items: []string{"c"}},
	}
	fetch := func(_ context.Context, token string) ([]string, string, error) {
		step, ok := steps[token]
		if !ok {
			return nil, "", fmt.Errorf("%w: unscripted token %q", errFetch, token)
		}
		return step.items, step.next, nil
	}
	ctx := context.Background()

	page, err := jira.Cursor(ctx, fetch)
	if err != nil {
		t.Fatalf("Cursor: %v", err)
	}

	const goroutines = 2
	items := make([][]string, goroutines)
	errs := make([]error, goroutines)

	var wg sync.WaitGroup
	for i := range goroutines {
		wg.Add(1)
		go func() {
			defer wg.Done()
			next, err := page.Next(ctx)
			items[i], errs[i] = next.Items, err
		}()
	}
	wg.Wait()

	for i, err := range errs {
		if err != nil {
			t.Errorf("goroutine %d: Next: %v", i, err)
		}
		if want := []string{"b"}; !slices.Equal(items[i], want) {
			t.Errorf("goroutine %d: items = %q, want %q", i, items[i], want)
		}
	}
}

func TestZeroPage_IsAnEmptyExhaustedPage(t *testing.T) {
	t.Parallel()

	var page jira.Page[string]
	ctx := context.Background()

	if page.HasMore() {
		t.Error("HasMore() = true, want false on the zero page")
	}
	if total, ok := page.Count(); ok {
		t.Errorf("Count() = (%d, true), want (0, false) on the zero page", total)
	}
	if _, err := page.Next(ctx); !errors.Is(err, jira.ErrNoMorePages) {
		t.Errorf("Next = %v, want ErrNoMorePages", err)
	}
	got, err := jira.Collect(ctx, page, 0)
	if err != nil {
		t.Fatalf("Collect: %v", err)
	}
	if len(got) != 0 {
		t.Errorf("Collect = %q, want no items", got)
	}
}

func TestNewPage_CarriesItsItemsAndTotalAndCannotBeChained(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	total := 7
	page := jira.NewPage([]string{"a", "b"}, &total)

	if got, ok := page.Count(); !ok || got != total {
		t.Errorf("Count() = (%d, %t), want (%d, true)", got, ok, total)
	}
	if page.HasMore() {
		t.Error("HasMore() = true; a page built from items in hand is always the last one")
	}
	got, err := jira.Collect(ctx, page, 0)
	if err != nil {
		t.Fatalf("Collect: %v", err)
	}
	if want := []string{"a", "b"}; !slices.Equal(got, want) {
		t.Errorf("items = %q, want %q", got, want)
	}

	if total, ok := jira.NewPage([]string{"a"}, nil).Count(); ok {
		t.Errorf("Count() = (%d, true), want (0, false) when no total was given", total)
	}
}

func TestCursor_WalkingTheSamePageTwiceGivesTheSameAnswer(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	pages := map[string]struct {
		items []string
		next  string
	}{
		"":   {[]string{"a"}, "t1"},
		"t1": {[]string{"b"}, "t2"},
		"t2": {[]string{"c"}, ""},
	}
	fetch := func(_ context.Context, token string) ([]string, string, error) {
		p, ok := pages[token]
		if !ok {
			return nil, "", fmt.Errorf("no page for token %q", token)
		}
		return p.items, p.next, nil
	}

	first, err := jira.Cursor(ctx, fetch)
	if err != nil {
		t.Fatalf("Cursor: %v", err)
	}
	for attempt := 1; attempt <= 3; attempt++ {
		got, err := jira.Collect(ctx, first, 0)
		if err != nil {
			t.Fatalf("walk %d: %v", attempt, err)
		}
		if want := []string{"a", "b", "c"}; !slices.Equal(got, want) {
			t.Errorf("walk %d collected %q, want %q — a retained page must be re-walkable", attempt, got, want)
		}
	}
}
