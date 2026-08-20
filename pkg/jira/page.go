package jira

import (
	"context"
	"errors"
	"slices"
)

// ErrNoMorePages is returned by Page.Next when the page is already the last.
var ErrNoMorePages = errors.New("jira: no more pages")

// Page is one page of results from either of Jira's two pagination models: the
// platform API's opaque cursor, which reports no total, and the Agile API's
// startAt/total offsets, which does. Widgets consume both without branching.
//
// The zero Page is a valid empty, exhausted page.
type Page[T any] struct {
	Items []T

	// ApproxTotal is the server's idea of how many items exist in total. It is
	// nil for cursor-paginated endpoints, which never report one, and it is
	// approximate everywhere else — render it as "142+", not "142".
	ApproxTotal *int

	next func(context.Context) (Page[T], error)
}

// HasMore reports whether another page can be fetched.
func (p Page[T]) HasMore() bool { return p.next != nil }

// Next fetches the page after this one. It returns ErrNoMorePages when this
// page is the last, so a caller that forgot to check HasMore cannot loop.
func (p Page[T]) Next(ctx context.Context) (Page[T], error) {
	if p.next == nil {
		return Page[T]{}, ErrNoMorePages
	}
	return p.next(ctx)
}

// Count returns the approximate total and whether the endpoint reported one.
func (p Page[T]) Count() (int, bool) {
	if p.ApproxTotal == nil {
		return 0, false
	}
	return *p.ApproxTotal, true
}

// NewPage builds a single, exhausted page from items already in hand.
//
// There is deliberately no constructor that takes a successor function: every
// multi-page walk goes through Cursor or Offset, which is what makes the loop
// guards below impossible for an adapter to skip.
func NewPage[T any](items []T, approxTotal *int) Page[T] {
	return Page[T]{Items: items, ApproxTotal: approxTotal}
}

// CursorFetch fetches one page of a cursor-paginated endpoint. It returns the
// items and the token for the following page, or "" when there is none.
type CursorFetch[T any] func(ctx context.Context, token string) (items []T, nextToken string, err error)

// Cursor drives a cursor-paginated endpoint, starting from the first page.
//
// A token that already appears on the path taken to reach this page is treated
// as exhaustion rather than followed: Jira has been observed returning a token
// that loops back to page one, and following it never terminates.
//
// The guard is per-path, not per-walk, because a Page is a value a caller may
// keep and walk more than once — a view re-reading its pages after a redraw
// must get the same answer as the first time.
func Cursor[T any](ctx context.Context, fetch CursorFetch[T]) (Page[T], error) {
	return cursorPage(ctx, fetch, nil, "")
}

func cursorPage[T any](ctx context.Context, fetch CursorFetch[T], path []string, token string) (Page[T], error) {
	items, nextToken, err := fetch(ctx, token)
	if err != nil {
		return Page[T]{}, err
	}
	p := Page[T]{Items: items}
	if nextToken != "" && !slices.Contains(path, nextToken) {
		taken := make([]string, len(path), len(path)+1)
		copy(taken, path)
		taken = append(taken, nextToken)
		p.next = func(ctx context.Context) (Page[T], error) {
			return cursorPage(ctx, fetch, taken, nextToken)
		}
	}
	return p, nil
}

// OffsetFetch fetches one page of an offset-paginated endpoint. total is the
// server's count, or a negative number when it did not report one; isLast is
// the server's own end-of-results flag where it sends one.
type OffsetFetch[T any] func(ctx context.Context, startAt int) (items []T, total int, isLast bool, err error)

// Offset drives an offset-paginated endpoint, starting at zero.
//
// A page that comes back empty ends the walk even when the server claims there
// is more: the Agile API silently truncates against an instance limit that is
// not readable from the response, and without this guard that truncation is an
// infinite loop.
func Offset[T any](ctx context.Context, fetch OffsetFetch[T]) (Page[T], error) {
	return offsetPage(ctx, fetch, 0)
}

func offsetPage[T any](ctx context.Context, fetch OffsetFetch[T], startAt int) (Page[T], error) {
	items, total, isLast, err := fetch(ctx, startAt)
	if err != nil {
		return Page[T]{}, err
	}
	p := Page[T]{Items: items}
	if total >= 0 {
		p.ApproxTotal = &total
	}
	nextStart := startAt + len(items)
	done := isLast || len(items) == 0 || (total >= 0 && nextStart >= total)
	if !done {
		p.next = func(ctx context.Context) (Page[T], error) {
			return offsetPage(ctx, fetch, nextStart)
		}
	}
	return p, nil
}

// Collect walks pages from p and returns their items. A limit above zero stops
// the walk once that many items are in hand; a limit of zero or less walks to
// exhaustion, which is only safe when the result set is known to be small.
func Collect[T any](ctx context.Context, p Page[T], limit int) ([]T, error) {
	out := append([]T(nil), p.Items...)
	for p.HasMore() {
		if limit > 0 && len(out) >= limit {
			break
		}
		if err := ctx.Err(); err != nil {
			return out, err
		}
		var err error
		if p, err = p.Next(ctx); err != nil {
			return out, err
		}
		out = append(out, p.Items...)
	}
	if limit > 0 && len(out) > limit {
		out = out[:limit]
	}
	return out, nil
}
