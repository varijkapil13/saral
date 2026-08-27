package store

import (
	"fmt"
	"path/filepath"
	"testing"
	"time"

	"go.etcd.io/bbolt"
)

const kind = "issue"

var scope = Scope{Site: "example.atlassian.net", Account: "you@example.com"}

func openTemp(t *testing.T) *DB {
	t.Helper()

	db, err := Open(filepath.Join(t.TempDir(), "cache.db"))
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() {
		if err := db.Close(); err != nil {
			t.Errorf("Close: %v", err)
		}
	})
	return db
}

func TestGet_ReadsBackWhatPutWrote(t *testing.T) {
	t.Parallel()

	db := openTemp(t)
	written := time.Date(2025, time.March, 5, 9, 0, 0, 0, time.UTC)
	if err := db.Put(scope, kind, Record{Key: "PROJ-1", Value: []byte("rows"), StoredAt: written}); err != nil {
		t.Fatalf("Put: %v", err)
	}

	got, ok, err := db.Get(scope, kind, "PROJ-1")
	if err != nil || !ok {
		t.Fatalf("Get: %v, found %t", err, ok)
	}
	if string(got.Value) != "rows" {
		t.Errorf("the value came back as %q", got.Value)
	}
	if !got.StoredAt.Equal(written) {
		t.Errorf("the write time came back as %s, want %s", got.StoredAt, written)
	}
}

func TestGet_ReportsAMissRatherThanAnError(t *testing.T) {
	t.Parallel()

	db := openTemp(t)
	if _, ok, err := db.Get(scope, kind, "PROJ-1"); err != nil || ok {
		t.Errorf("a kind never written to gave %v, found %t; want a plain miss", err, ok)
	}
	if err := db.Put(scope, kind, Record{Key: "PROJ-1", Value: []byte("x"), StoredAt: time.Now()}); err != nil {
		t.Fatalf("Put: %v", err)
	}
	if _, ok, err := db.Get(scope, kind, "PROJ-2"); err != nil || ok {
		t.Errorf("a key never written gave %v, found %t; want a plain miss", err, ok)
	}
}

func TestGetAll_KeepsTheOrderAskedForAndSkipsWhatIsNotThere(t *testing.T) {
	t.Parallel()

	db := openTemp(t)
	now := time.Now()
	for _, key := range []string{"PROJ-1", "PROJ-2", "PROJ-3"} {
		if err := db.Put(scope, kind, Record{Key: key, Value: []byte(key), StoredAt: now}); err != nil {
			t.Fatalf("Put %s: %v", key, err)
		}
	}

	got, err := db.GetAll(scope, kind, []string{"PROJ-3", "PROJ-9", "PROJ-1"})
	if err != nil {
		t.Fatalf("GetAll: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("GetAll returned %d records, want the two that exist", len(got))
	}
	if got[0].Key != "PROJ-3" || got[1].Key != "PROJ-1" {
		t.Errorf("GetAll returned %s then %s; the order asked for is what a list draws",
			got[0].Key, got[1].Key)
	}
}

func TestPut_ReplacesWhatAKeyHeld(t *testing.T) {
	t.Parallel()

	db := openTemp(t)
	first := time.Date(2025, time.March, 5, 9, 0, 0, 0, time.UTC)
	second := first.Add(time.Hour)
	if err := db.Put(scope, kind, Record{Key: "PROJ-1", Value: []byte("old"), StoredAt: first}); err != nil {
		t.Fatalf("first Put: %v", err)
	}
	if err := db.Put(scope, kind, Record{Key: "PROJ-1", Value: []byte("new"), StoredAt: second}); err != nil {
		t.Fatalf("second Put: %v", err)
	}

	got, _, err := db.Get(scope, kind, "PROJ-1")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if string(got.Value) != "new" || !got.StoredAt.Equal(second) {
		t.Errorf("the key still holds %q from %s", got.Value, got.StoredAt)
	}
}

func TestPut_RefusesARecordWithNoKey(t *testing.T) {
	t.Parallel()

	db := openTemp(t)
	if err := db.Put(scope, kind, Record{Value: []byte("x"), StoredAt: time.Now()}); err == nil {
		t.Error("a record with no key was stored under one")
	}
}

func TestPut_KeepsAZeroWriteTimeZero(t *testing.T) {
	t.Parallel()

	db := openTemp(t)
	if err := db.Put(scope, kind, Record{Key: "PROJ-1", Value: []byte("x")}); err != nil {
		t.Fatalf("Put: %v", err)
	}
	got, _, err := db.Get(scope, kind, "PROJ-1")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if !got.StoredAt.IsZero() {
		t.Errorf("an unstamped record came back written at %s", got.StoredAt)
	}
}

func TestDelete_RemovesAKeyAndForgivesOneThatIsNotThere(t *testing.T) {
	t.Parallel()

	db := openTemp(t)
	if err := db.Put(scope, kind, Record{Key: "PROJ-1", Value: []byte("x"), StoredAt: time.Now()}); err != nil {
		t.Fatalf("Put: %v", err)
	}
	if err := db.Delete(scope, kind, "PROJ-1", "PROJ-404"); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	if _, ok, _ := db.Get(scope, kind, "PROJ-1"); ok {
		t.Error("the key survived being deleted")
	}
}

func TestEach_WalksInKeyOrderAndStopsWhenAsked(t *testing.T) {
	t.Parallel()

	db := openTemp(t)
	now := time.Now()
	for _, key := range []string{"PROJ-3", "PROJ-1", "PROJ-2"} {
		if err := db.Put(scope, kind, Record{Key: key, Value: []byte(key), StoredAt: now}); err != nil {
			t.Fatalf("Put %s: %v", key, err)
		}
	}

	var seen []string
	if _, err := db.Each(scope, kind, func(rec Record) bool {
		seen = append(seen, rec.Key)
		return true
	}); err != nil {
		t.Fatalf("Each: %v", err)
	}
	want := []string{"PROJ-1", "PROJ-2", "PROJ-3"}
	if fmt.Sprint(seen) != fmt.Sprint(want) {
		t.Errorf("Each walked %v, want %v", seen, want)
	}

	seen = nil
	if _, err := db.Each(scope, kind, func(rec Record) bool {
		seen = append(seen, rec.Key)
		return false
	}); err != nil {
		t.Fatalf("Each: %v", err)
	}
	if len(seen) != 1 {
		t.Errorf("a walk told to stop visited %d records", len(seen))
	}
}

func TestEach_OverAKindNeverWrittenToVisitsNothing(t *testing.T) {
	t.Parallel()

	db := openTemp(t)
	visited := 0
	if _, err := db.Each(scope, "search", func(Record) bool {
		visited++
		return true
	}); err != nil {
		t.Fatalf("Each: %v", err)
	}
	if visited != 0 {
		t.Errorf("a walk over nothing visited %d records", visited)
	}
}

func TestTrim_KeepsTheMostRecentlyWrittenAndDropsTheRest(t *testing.T) {
	t.Parallel()

	db := openTemp(t)
	base := time.Date(2025, time.March, 5, 9, 0, 0, 0, time.UTC)
	for i := range 10 {
		key := fmt.Sprintf("PROJ-%d", i)
		rec := Record{Key: key, Value: []byte(key), StoredAt: base.Add(time.Duration(i) * time.Minute)}
		if err := db.Put(scope, kind, rec); err != nil {
			t.Fatalf("Put %s: %v", key, err)
		}
	}

	removed, err := db.Trim(scope, kind, 4)
	if err != nil {
		t.Fatalf("Trim: %v", err)
	}
	if removed != 6 {
		t.Errorf("Trim removed %d records, want 6", removed)
	}
	for i := range 10 {
		key := fmt.Sprintf("PROJ-%d", i)
		_, ok, err := db.Get(scope, kind, key)
		if err != nil {
			t.Fatalf("Get %s: %v", key, err)
		}
		if want := i >= 6; ok != want {
			t.Errorf("%s present = %t, want %t: the oldest writes are what a bound drops", key, ok, want)
		}
	}
}

func TestTrim_LeavesAKindThatIsAlreadyUnderTheBoundAlone(t *testing.T) {
	t.Parallel()

	db := openTemp(t)
	if err := db.Put(scope, kind, Record{Key: "PROJ-1", Value: []byte("x"), StoredAt: time.Now()}); err != nil {
		t.Fatalf("Put: %v", err)
	}
	removed, err := db.Trim(scope, kind, 10)
	if err != nil {
		t.Fatalf("Trim: %v", err)
	}
	if removed != 0 {
		t.Errorf("Trim removed %d records from a kind under the bound", removed)
	}
	if _, ok, _ := db.Get(scope, kind, "PROJ-1"); !ok {
		t.Error("the only record was dropped by a trim that had nothing to do")
	}
}

func TestRecords_StayInsideTheirOwnScope(t *testing.T) {
	t.Parallel()

	db := openTemp(t)
	other := Scope{Site: scope.Site, Account: "someone.else@example.com"}
	now := time.Now()
	if err := db.Put(scope, kind, Record{Key: "PROJ-1", Value: []byte("mine"), StoredAt: now}); err != nil {
		t.Fatalf("Put: %v", err)
	}
	if err := db.Put(other, kind, Record{Key: "PROJ-1", Value: []byte("theirs"), StoredAt: now}); err != nil {
		t.Fatalf("Put for the other account: %v", err)
	}

	got, _, err := db.Get(scope, kind, "PROJ-1")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if string(got.Value) != "mine" {
		t.Errorf("one account read %q out of another's bucket", got.Value)
	}
	if _, err := db.Trim(other, kind, 0); err != nil {
		t.Fatalf("Trim the other account: %v", err)
	}
	if _, ok, _ := db.Get(scope, kind, "PROJ-1"); !ok {
		t.Error("emptying one account's cache emptied another's")
	}
}

// A value too short to carry the time it was written used to end the walk, and
// keys sort, so one truncated record hid every record after it.
func TestEach_SkipsARecordItCannotDecodeAndNamesIt(t *testing.T) {
	t.Parallel()

	db := openTemp(t)
	now := time.Now()
	for _, key := range []string{"PROJ-1", "PROJ-2", "PROJ-3"} {
		if err := db.Put(scope, kind, Record{Key: key, Value: []byte(key), StoredAt: now}); err != nil {
			t.Fatalf("Put %s: %v", key, err)
		}
	}
	// Written past Put, which always stamps what it stores: a half-written value
	// is what a file truncated under a crash leaves behind.
	if err := db.bolt.Update(func(tx *bbolt.Tx) error {
		return tx.Bucket(scope.Bucket(kind)).Put([]byte("PROJ-2"), []byte{1, 2, 3})
	}); err != nil {
		t.Fatalf("writing a value too short to carry a stamp: %v", err)
	}

	var seen []string
	unreadable, err := db.Each(scope, kind, func(rec Record) bool {
		seen = append(seen, rec.Key)
		return true
	})
	if err != nil {
		t.Fatalf("Each: %v", err)
	}
	if want := []string{"PROJ-1", "PROJ-3"}; fmt.Sprint(seen) != fmt.Sprint(want) {
		t.Errorf("the walk visited %v, want %v: one record it could not decode hid the ones after it", seen, want)
	}
	if want := []string{"PROJ-2"}; fmt.Sprint(unreadable) != fmt.Sprint(want) {
		t.Errorf("the walk reported %v as unreadable, want %v; a caller cannot heal or count what it is not told about", unreadable, want)
	}
}
