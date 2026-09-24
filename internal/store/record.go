package store

import (
	"encoding/binary"
	"fmt"
	"slices"
	"time"

	"go.etcd.io/bbolt"
)

// stampSize is the width of the write time each value is prefixed with.
const stampSize = 8

// Record is one value in a bucket together with the moment it was written.
//
// The time is stored beside the value rather than derived from the file, because
// a TTL is per entry and bbolt knows nothing about either. Nothing here reads a
// clock: the caller stamps what it writes, which is what lets a cache expiry be
// tested with an injected clock rather than a sleep.
type Record struct {
	Key      string
	Value    []byte
	StoredAt time.Time
}

// Get reads one record. A key with nothing under it, and a kind never written
// to, are both a false rather than an error.
func (db *DB) Get(s Scope, kind, key string) (Record, bool, error) {
	var out Record
	found := false
	err := db.bolt.View(func(tx *bbolt.Tx) error {
		bucket := tx.Bucket(s.Bucket(kind))
		if bucket == nil {
			return nil
		}
		rec, ok, err := read(bucket, key)
		out, found = rec, ok
		return err
	})
	if err != nil {
		return Record{}, false, fmt.Errorf("reading %s/%s: %w", kind, key, err)
	}
	return out, found, nil
}

// GetAll reads several keys in one transaction, in the order they were asked
// for. A key with nothing under it is left out, so the result is shorter than
// the request rather than carrying a hole.
//
// It is one transaction because this is the read a first paint waits on: fifty
// rows fetched one transaction apiece is fifty times the work for the same
// bytes.
func (db *DB) GetAll(s Scope, kind string, keys []string) ([]Record, error) {
	out := make([]Record, 0, len(keys))
	err := db.bolt.View(func(tx *bbolt.Tx) error {
		bucket := tx.Bucket(s.Bucket(kind))
		if bucket == nil {
			return nil
		}
		for _, key := range keys {
			rec, ok, err := read(bucket, key)
			if err != nil {
				return err
			}
			if ok {
				out = append(out, rec)
			}
		}
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("reading %d key(s) of %s: %w", len(keys), kind, err)
	}
	return out, nil
}

// Put writes records, replacing whatever their keys already held and creating
// the bucket on first use.
func (db *DB) Put(s Scope, kind string, recs ...Record) error {
	if len(recs) == 0 {
		return nil
	}
	if err := db.PutAll(s, Write{Kind: kind, Records: recs}); err != nil {
		return fmt.Errorf("writing %d record(s) of %s: %w", len(recs), kind, err)
	}
	return nil
}

// Write is the records of one kind that a PutAll stores.
type Write struct {
	Kind    string
	Records []Record
}

// PutAll stores the records of several kinds in one transaction, so a reader
// sees all of them or none, and the file is synced once rather than once a
// kind.
func (db *DB) PutAll(s Scope, writes ...Write) error {
	added := make([]int, len(writes))
	err := db.bolt.Update(func(tx *bbolt.Tx) error {
		for i, w := range writes {
			added[i] = 0
			if len(w.Records) == 0 {
				continue
			}
			bucket, err := tx.CreateBucketIfNotExists(s.Bucket(w.Kind))
			if err != nil {
				return err
			}
			for _, rec := range w.Records {
				if rec.Key == "" {
					return fmt.Errorf("a record of %s with no key cannot be stored", w.Kind)
				}
				if bucket.Get([]byte(rec.Key)) == nil {
					added[i]++
				}
				if err := bucket.Put([]byte(rec.Key), stamp(rec)); err != nil {
					return err
				}
			}
		}
		return nil
	})
	if err != nil {
		return err
	}
	for i, w := range writes {
		db.adjust(s.Bucket(w.Kind), added[i])
	}
	return nil
}

// Delete removes keys. One that is not there is not an error: the caller wanted
// it gone and it is.
func (db *DB) Delete(s Scope, kind string, keys ...string) error {
	if len(keys) == 0 {
		return nil
	}
	name := s.Bucket(kind)
	removed := 0
	err := db.bolt.Update(func(tx *bbolt.Tx) error {
		removed = 0
		bucket := tx.Bucket(name)
		if bucket == nil {
			return nil
		}
		for _, key := range keys {
			if bucket.Get([]byte(key)) != nil {
				removed++
			}
			if err := bucket.Delete([]byte(key)); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		return fmt.Errorf("deleting %d key(s) of %s: %w", len(keys), kind, err)
	}
	db.adjust(name, -removed)
	return nil
}

// Each visits every record of one kind in key order, stopping early when fn
// returns false. The value handed to fn is the caller's own copy.
//
// A record whose stored bytes cannot be read is skipped and its key returned
// rather than ending the walk. Keys sort, so stopping at the first unreadable
// one would hide every record after it, and a short answer with no error is the
// one failure a caller cannot see.
func (db *DB) Each(s Scope, kind string, fn func(Record) bool) ([]string, error) {
	var unreadable []string
	err := db.bolt.View(func(tx *bbolt.Tx) error {
		bucket := tx.Bucket(s.Bucket(kind))
		if bucket == nil {
			return nil
		}
		cursor := bucket.Cursor()
		for k, v := cursor.First(); k != nil; k, v = cursor.Next() {
			rec, err := decode(string(k), v)
			if err != nil {
				unreadable = append(unreadable, string(k))
				continue
			}
			if !fn(rec) {
				return nil
			}
		}
		return nil
	})
	if err != nil {
		return unreadable, fmt.Errorf("walking %s: %w", kind, err)
	}
	return unreadable, nil
}

// Trim keeps the keep most recently written records of one kind and deletes the
// rest, reporting how many went. A keep of zero or less empties the kind. A
// record too short to say when it was written is deleted whatever the bound:
// it cannot be read, and left in place it would be walked over on every trim.
//
// A kind already known to be within the bound costs a map lookup and no
// transaction at all, so a caller may trim after every write.
//
// It walks and deletes inside one write transaction, so a reader either sees the
// bucket before the trim or after it, and the walk cannot be overtaken by a
// write it is about to act on.
func (db *DB) Trim(s Scope, kind string, keep int) (int, error) {
	keep = max(keep, 0)
	if n, err := db.Len(s, kind); err == nil && n <= keep {
		return 0, nil
	}
	name := s.Bucket(kind)
	removed, left := 0, 0
	err := db.bolt.Update(func(tx *bbolt.Tx) error {
		removed, left = 0, 0
		bucket := tx.Bucket(name)
		if bucket == nil {
			return nil
		}
		var held []Record
		var broken []string
		cursor := bucket.Cursor()
		for k, v := cursor.First(); k != nil; k, v = cursor.Next() {
			stored, err := stampOf(v)
			if err != nil {
				broken = append(broken, string(k))
				continue
			}
			held = append(held, Record{Key: string(k), StoredAt: stored})
		}
		doomed := broken
		if len(held) > keep {
			// Oldest first, key order breaking a tie so that two records written in
			// the same nanosecond do not evict in map order.
			slices.SortFunc(held, func(a, b Record) int {
				if !a.StoredAt.Equal(b.StoredAt) {
					return a.StoredAt.Compare(b.StoredAt)
				}
				if a.Key < b.Key {
					return -1
				}
				return 1
			})
			for _, rec := range held[:len(held)-keep] {
				doomed = append(doomed, rec.Key)
			}
			left = keep
		} else {
			left = len(held)
		}
		for _, key := range doomed {
			if err := bucket.Delete([]byte(key)); err != nil {
				return err
			}
		}
		removed = len(doomed)
		return nil
	})
	if err != nil {
		return 0, fmt.Errorf("trimming %s to %d: %w", kind, keep, err)
	}
	db.setCount(name, left)
	return removed, nil
}

// Expire deletes every record of one kind written before cutoff, and every one
// too short to say when it was written, reporting how many went. It looks
// before it writes, so a kind with nothing old in it costs no write
// transaction.
func (db *DB) Expire(s Scope, kind string, cutoff time.Time) (int, error) {
	name := s.Bucket(kind)
	var doomed []string
	err := db.bolt.View(func(tx *bbolt.Tx) error {
		bucket := tx.Bucket(name)
		if bucket == nil {
			return nil
		}
		cursor := bucket.Cursor()
		for k, v := cursor.First(); k != nil; k, v = cursor.Next() {
			stored, err := stampOf(v)
			if err != nil || stored.Before(cutoff) {
				doomed = append(doomed, string(k))
			}
		}
		return nil
	})
	if err != nil {
		return 0, fmt.Errorf("expiring %s: %w", kind, err)
	}
	if len(doomed) == 0 {
		return 0, nil
	}
	removed := 0
	err = db.bolt.Update(func(tx *bbolt.Tx) error {
		removed = 0
		bucket := tx.Bucket(name)
		if bucket == nil {
			return nil
		}
		for _, key := range doomed {
			v := bucket.Get([]byte(key))
			if v == nil {
				continue
			}
			// Rewritten since the look, and so no longer old.
			if stored, err := stampOf(v); err == nil && !stored.Before(cutoff) {
				continue
			}
			if err := bucket.Delete([]byte(key)); err != nil {
				return err
			}
			removed++
		}
		return nil
	})
	if err != nil {
		return 0, fmt.Errorf("expiring %s: %w", kind, err)
	}
	db.adjust(name, -removed)
	return removed, nil
}

func read(bucket *bbolt.Bucket, key string) (Record, bool, error) {
	raw := bucket.Get([]byte(key))
	if raw == nil {
		return Record{}, false, nil
	}
	rec, err := decode(key, raw)
	if err != nil {
		return Record{}, false, err
	}
	return rec, true, nil
}

func stamp(rec Record) []byte {
	out := make([]byte, stampSize+len(rec.Value))
	binary.BigEndian.PutUint64(out, bitsOf(rec.StoredAt))
	copy(out[stampSize:], rec.Value)
	return out
}

// bitsOf encodes a write time. A zero time is written as zero rather than as
// UnixNano's answer for it, which is outside the range time.Unix can invert.
func bitsOf(t time.Time) uint64 {
	if t.IsZero() {
		return 0
	}
	return uint64(t.UnixNano()) //nolint:gosec // inverted by stampOf below
}

// decode copies the value out of the transaction. bbolt hands back a window
// into the mapped file, which is only valid until the transaction ends.
func decode(key string, raw []byte) (Record, error) {
	stored, err := stampOf(raw)
	if err != nil {
		return Record{}, fmt.Errorf("%s: %w", key, err)
	}
	value := make([]byte, len(raw)-stampSize)
	copy(value, raw[stampSize:])
	return Record{Key: key, Value: value, StoredAt: stored}, nil
}

func stampOf(raw []byte) (time.Time, error) {
	if len(raw) < stampSize {
		return time.Time{}, fmt.Errorf("a stored value is %d bytes, too short to carry the time it was written", len(raw))
	}
	bits := binary.BigEndian.Uint64(raw)
	if bits == 0 {
		return time.Time{}, nil
	}
	return time.Unix(0, int64(bits)), nil //nolint:gosec // the inverse of bitsOf
}
