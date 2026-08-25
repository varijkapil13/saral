package app

import "time"

// Entry is one cached value and the moment it was written. Age is left for the
// caller to work out against its own clock, so that a TTL can be tested without
// waiting for one to pass.
type Entry struct {
	Data    []byte
	Written time.Time
}

// Cache is the local copy of what a site has already answered: bytes under a
// kind and a key, plus when they were written.
//
// It is an interface, and it lives here rather than in the kernel, because
// internal/ui takes what it needs as interfaces and internal/store sits below
// internal/app — the same reasoning Counter is written with. A session that has
// nowhere to cache carries a nil one, and every caller has to cope with that: a
// first run has nothing on disk, another copy of Saral may hold the file, and a
// home directory is not always writable.
//
// Nothing here takes a context. A cached read is what the first frame is drawn
// from, so it happens on the event loop and has to be quick enough that
// cancelling it would mean nothing; the fetch it saves is what takes a context.
type Cache interface {
	// Get reads what is stored under a kind and a key. A miss is a false bool,
	// not an error.
	Get(kind, key string) (Entry, bool, error)
	// Put writes an entry, replacing whatever the key already held.
	Put(kind, key string, data []byte) error
	// Each visits every entry of one kind in key order, stopping at the first
	// error fn returns and returning it. It is how an index is built from what
	// is already on disk without reading a whole kind into memory first.
	Each(kind string, fn func(key string, e Entry) error) error
	// Purge drops every entry of one kind, which is what R asks for beyond a
	// refetch.
	Purge(kind string) error
}
