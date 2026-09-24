package widget

// RowCache is a bounded memo of rendered rows, keyed by whatever a view
// invalidates a row on and holding whatever a view renders one into. Past its
// limit it is emptied rather than evicted one at a time, because a scroll or
// a resize invalidates a screenful at once anyway and clearing keeps the
// map's capacity.
type RowCache[K comparable, V any] struct {
	rows  map[K]V
	limit int
}

// NewRowCache returns a RowCache holding at most limit rows before a
// put clears it.
func NewRowCache[K comparable, V any](limit int) *RowCache[K, V] {
	return &RowCache[K, V]{rows: make(map[K]V, limit), limit: limit}
}

// Get returns the row memoized under k, if any.
func (c *RowCache[K, V]) Get(k K) (V, bool) {
	v, ok := c.rows[k]
	return v, ok
}

// Put memoizes v under k, clearing the memo first if it is already at limit.
func (c *RowCache[K, V]) Put(k K, v V) {
	if len(c.rows) >= c.limit {
		clear(c.rows)
	}
	c.rows[k] = v
}

// Reset forgets every memoized row.
func (c *RowCache[K, V]) Reset() { clear(c.rows) }

// Len is how many rows are currently memoized, for a test asserting a
// selection or theme change invalidated the rows it should have.
func (c *RowCache[K, V]) Len() int { return len(c.rows) }
