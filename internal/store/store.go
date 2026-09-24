// Package store holds Saral's cache file. This package owns the file itself —
// opening it, closing it, and naming the buckets inside it so that two profiles
// cannot read each other's rows. What goes in the buckets, and for how long, is
// the cache's business and not this file's.
package store

import (
	"bytes"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"go.etcd.io/bbolt"
	bolterrors "go.etcd.io/bbolt/errors"
)

const (
	dirPerm  fs.FileMode = 0o700
	filePerm fs.FileMode = 0o600

	// A second copy of Saral is told at once and runs uncached, rather than
	// holding its first frame for as long as the lock wait lasts.
	defaultLockTimeout = 100 * time.Millisecond
)

// ErrLocked reports that another process has the cache open. bbolt holds an
// exclusive lock on the file for as long as it is open, so a second copy of
// Saral gets this rather than a second cache.
var ErrLocked = errors.New("the cache is open in another process")

// DB is an open cache file.
type DB struct {
	bolt      *bbolt.DB
	recovered string

	// counts is how many keys each bucket holds, learnt once and kept current
	// by every write through this DB. It is exact because bbolt holds the file
	// exclusively, so nothing else writes to it while it is open.
	mu     sync.Mutex
	counts map[string]int
}

// Option adjusts how Open opens the file.
type Option func(*bbolt.Options)

// WithLockTimeout sets how long Open waits for another process to let go of the
// file before giving up with ErrLocked. Zero waits for as long as it takes.
func WithLockTimeout(d time.Duration) Option {
	return func(o *bbolt.Options) { o.Timeout = d }
}

// Open opens the cache at path, creating the file and the directory holding it
// if they are not there yet. It reports ErrLocked when another process holds the
// file. The caller closes what it opens.
//
// A file bbolt cannot read as a database is moved aside to
// path.corrupt-<timestamp> and a fresh one created in its place; Recovered names
// where it went. A cache is a copy of what the site holds, so starting empty
// loses nothing a refetch will not bring back, while keeping the broken file
// would fail the same way on every launch.
func Open(path string, opts ...Option) (*DB, error) {
	if err := os.MkdirAll(filepath.Dir(path), dirPerm); err != nil {
		return nil, fmt.Errorf("making the directory for %s: %w", path, err)
	}

	options := bbolt.Options{Timeout: defaultLockTimeout}
	for _, opt := range opts {
		opt(&options)
	}

	bolt, err := bbolt.Open(path, filePerm, &options)
	recovered := ""
	if err != nil && unreadable(err) {
		aside := fmt.Sprintf("%s.corrupt-%s", path, time.Now().UTC().Format("20060102T150405.000000000Z"))
		if mvErr := os.Rename(path, aside); mvErr != nil {
			return nil, fmt.Errorf("opening %s: %w; moving it aside: %w", path, err, mvErr)
		}
		recovered = aside
		bolt, err = bbolt.Open(path, filePerm, &options)
	}
	if err != nil {
		if errors.Is(err, bolterrors.ErrTimeout) {
			return nil, fmt.Errorf("opening %s: %w", path, ErrLocked)
		}
		return nil, fmt.Errorf("opening %s: %w", path, err)
	}
	return &DB{bolt: bolt, recovered: recovered, counts: map[string]int{}}, nil
}

// unreadable is an error that says the file is not a database this bbolt can
// read, as against one that says it could not be reached or is held.
func unreadable(err error) bool {
	return errors.Is(err, bolterrors.ErrInvalid) ||
		errors.Is(err, bolterrors.ErrChecksum) ||
		errors.Is(err, bolterrors.ErrVersionMismatch)
}

// Recovered is where Open moved an unreadable file before starting afresh, or
// empty when the file opened as it was.
func (db *DB) Recovered() string { return db.recovered }

// Close releases the file and the lock on it.
func (db *DB) Close() error {
	if err := db.bolt.Close(); err != nil {
		return fmt.Errorf("closing %s: %w", db.bolt.Path(), err)
	}
	return nil
}

// Scope is whose cache a bucket holds: one account on one site. Two accounts on
// one site, and one account on two sites, are different scopes.
type Scope struct {
	Site    string
	Account string
}

// Bucket names the bucket holding one kind of cached data for this scope. The
// three parts are joined with a NUL, which no hostname, account ID or kind of
// ours contains, so no two scopes can name the same bucket.
func (s Scope) Bucket(kind string) []byte {
	return []byte(s.Site + "\x00" + s.Account + "\x00" + kind)
}

// scopeOf reads a bucket name back into the scope it was made for. A name not
// spelt by Bucket is not a scope's and is reported as such.
func scopeOf(name []byte) (Scope, bool) {
	parts := strings.SplitN(string(name), "\x00", 3)
	if len(parts) != 3 {
		return Scope{}, false
	}
	return Scope{Site: parts[0], Account: parts[1]}, true
}

// Scopes lists every scope the file holds anything for, in the order their
// buckets sort.
func (db *DB) Scopes() ([]Scope, error) {
	var out []Scope
	err := db.bolt.View(func(tx *bbolt.Tx) error {
		return tx.ForEach(func(name []byte, _ *bbolt.Bucket) error {
			s, ok := scopeOf(name)
			if ok && (len(out) == 0 || out[len(out)-1] != s) {
				out = append(out, s)
			}
			return nil
		})
	})
	if err != nil {
		return nil, fmt.Errorf("listing the cache's profiles: %w", err)
	}
	return out, nil
}

// DropScope deletes every bucket a scope holds, which is everything one
// profile has ever cached.
func (db *DB) DropScope(s Scope) error {
	prefix := []byte(s.Site + "\x00" + s.Account + "\x00")
	var dropped [][]byte
	err := db.bolt.Update(func(tx *bbolt.Tx) error {
		var names [][]byte
		if err := tx.ForEach(func(name []byte, _ *bbolt.Bucket) error {
			if bytes.HasPrefix(name, prefix) {
				names = append(names, bytes.Clone(name))
			}
			return nil
		}); err != nil {
			return err
		}
		for _, name := range names {
			if err := tx.DeleteBucket(name); err != nil {
				return err
			}
		}
		dropped = names
		return nil
	})
	if err != nil {
		return fmt.Errorf("dropping the cache of %s on %s: %w", s.Account, s.Site, err)
	}
	db.mu.Lock()
	for _, name := range dropped {
		delete(db.counts, string(name))
	}
	db.mu.Unlock()
	return nil
}

// Len is how many records one kind holds. The first ask walks the keys; every
// later one is answered from the count the writes keep.
func (db *DB) Len(s Scope, kind string) (int, error) {
	name := s.Bucket(kind)
	db.mu.Lock()
	n, known := db.counts[string(name)]
	db.mu.Unlock()
	if known {
		return n, nil
	}
	n = 0
	err := db.bolt.View(func(tx *bbolt.Tx) error {
		bucket := tx.Bucket(name)
		if bucket == nil {
			return nil
		}
		cursor := bucket.Cursor()
		for k, _ := cursor.First(); k != nil; k, _ = cursor.Next() {
			n++
		}
		return nil
	})
	if err != nil {
		return 0, fmt.Errorf("counting %s: %w", kind, err)
	}
	db.mu.Lock()
	db.counts[string(name)] = n
	db.mu.Unlock()
	return n, nil
}

// adjust moves a bucket's known count after a committed write. An unknown
// count stays unknown: Len learns it whole the first time it is asked.
func (db *DB) adjust(name []byte, delta int) {
	if delta == 0 {
		return
	}
	db.mu.Lock()
	if n, known := db.counts[string(name)]; known {
		db.counts[string(name)] = max(n+delta, 0)
	}
	db.mu.Unlock()
}

func (db *DB) setCount(name []byte, n int) {
	db.mu.Lock()
	db.counts[string(name)] = n
	db.mu.Unlock()
}
