package widget

import "testing"

type rowCacheTestKey struct {
	id      string
	version int
}

func TestRowCache_RemembersAPutUntilItsKeyChanges(t *testing.T) {
	t.Parallel()

	c := NewRowCache[rowCacheTestKey, string](4)
	base := rowCacheTestKey{id: "PROJ-1", version: 1}
	c.Put(base, "first")

	if got, ok := c.Get(base); !ok || got != "first" {
		t.Fatalf("the row was not memoized: %q %t", got, ok)
	}

	changed := base
	changed.version = 2
	if _, ok := c.Get(changed); ok {
		t.Error("a row whose key changed still hit the memo")
	}
}

func TestRowCache_GetOnAMissReturnsTheZeroValue(t *testing.T) {
	t.Parallel()

	c := NewRowCache[rowCacheTestKey, string](4)
	got, ok := c.Get(rowCacheTestKey{id: "never put"})
	if ok || got != "" {
		t.Errorf("Get on a miss = %q, %t; want the zero value and false", got, ok)
	}
}

func TestRowCache_ClearsRatherThanEvictsPastItsLimit(t *testing.T) {
	t.Parallel()

	const limit = 4
	c := NewRowCache[rowCacheTestKey, string](limit)
	for i := range 20 {
		c.Put(rowCacheTestKey{id: "PROJ-1", version: i}, "row")
	}
	if len(c.rows) > limit {
		t.Errorf("the memo grew to %d entries with a limit of %d", len(c.rows), limit)
	}
}

func TestRowCache_ResetForgetsEveryRow(t *testing.T) {
	t.Parallel()

	c := NewRowCache[rowCacheTestKey, string](4)
	c.Put(rowCacheTestKey{id: "PROJ-1"}, "row")
	c.Reset()

	if _, ok := c.Get(rowCacheTestKey{id: "PROJ-1"}); ok {
		t.Error("a row survived Reset")
	}
}

func TestRowCache_HoldsAnyValueType(t *testing.T) {
	t.Parallel()

	type renderedRow struct {
		ctrl string
		body string
	}
	c := NewRowCache[rowCacheTestKey, renderedRow](4)
	c.Put(rowCacheTestKey{id: "PROJ-1"}, renderedRow{ctrl: "input", body: "row"})

	got, ok := c.Get(rowCacheTestKey{id: "PROJ-1"})
	if !ok || got.ctrl != "input" || got.body != "row" {
		t.Errorf("Get = %+v, %t; want the struct just put", got, ok)
	}
}
