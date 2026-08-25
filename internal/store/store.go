// Package store holds Saral's cache file. This package owns the file itself —
// opening it, closing it, and naming the buckets inside it so that two profiles
// cannot read each other's rows. What goes in the buckets, and for how long, is
// the cache's business and not this file's.
package store

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"time"

	"go.etcd.io/bbolt"
	bolterrors "go.etcd.io/bbolt/errors"
)

const (
	dirPerm  fs.FileMode = 0o700
	filePerm fs.FileMode = 0o600

	defaultLockTimeout = 2 * time.Second
)

// ErrLocked reports that another process has the cache open. bbolt holds an
// exclusive lock on the file for as long as it is open, so a second copy of
// Saral gets this rather than a second cache.
var ErrLocked = errors.New("the cache is open in another process")

// DB is an open cache file.
type DB struct {
	bolt *bbolt.DB
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
func Open(path string, opts ...Option) (*DB, error) {
	if err := os.MkdirAll(filepath.Dir(path), dirPerm); err != nil {
		return nil, fmt.Errorf("making the directory for %s: %w", path, err)
	}

	options := bbolt.Options{Timeout: defaultLockTimeout}
	for _, opt := range opts {
		opt(&options)
	}

	bolt, err := bbolt.Open(path, filePerm, &options)
	if err != nil {
		if errors.Is(err, bolterrors.ErrTimeout) {
			return nil, fmt.Errorf("opening %s: %w", path, ErrLocked)
		}
		return nil, fmt.Errorf("opening %s: %w", path, err)
	}
	return &DB{bolt: bolt}, nil
}

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
