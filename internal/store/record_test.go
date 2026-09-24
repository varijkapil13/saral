package store

import (
	"fmt"
	"path/filepath"
	"slices"
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

func writeRaw(t *testing.T, db *DB, key string, raw []byte) {
	t.Helper()
	if err := db.bolt.Update(func(tx *bbolt.Tx) error {
		b, err := tx.CreateBucketIfNotExists(scope.Bucket(kind))
		if err != nil {
			return err
		}
		return b.Put([]byte(key), raw)
	}); err != nil {
		t.Fatalf("writing %s raw: %v", key, err)
	}
}

// A value too short to carry its stamp used to abort the trim, and the trim runs
// inside every write, so one such record failed every PutRows after it.
func TestTrim_DeletesARecordItCannotReadInsteadOfFailing(t *testing.T) {
	t.Parallel()

	db := openTemp(t)
	base := time.Date(2025, time.March, 5, 9, 0, 0, 0, time.UTC)
	writeRaw(t, db, "PROJ-0", []byte{1, 2, 3})
	for i := 1; i <= 5; i++ {
		key := fmt.Sprintf("PROJ-%d", i)
		if err := db.Put(scope, kind, Record{Key: key, Value: []byte(key), StoredAt: base.Add(time.Duration(i) * time.Minute)}); err != nil {
			t.Fatalf("Put %s: %v", key, err)
		}
	}

	removed, err := db.Trim(scope, kind, 3)
	if err != nil {
		t.Fatalf("Trim stopped at a record it could not read: %v", err)
	}
	if removed != 3 {
		t.Errorf("Trim removed %d, want the unreadable record and the two oldest", removed)
	}
	for i, want := range []bool{false, false, false, true, true, true} {
		key := fmt.Sprintf("PROJ-%d", i)
		present := false
		_ = db.bolt.View(func(tx *bbolt.Tx) error {
			present = tx.Bucket(scope.Bucket(kind)).Get([]byte(key)) != nil
			return nil
		})
		if present != want {
			t.Errorf("%s present = %t, want %t", key, present, want)
		}
	}
	if n, _ := db.Len(scope, kind); n != 3 {
		t.Errorf("Len = %d after the trim, want 3", n)
	}
}

func TestTrim_AnUnreadableRecordGoesEvenUnderTheBound(t *testing.T) {
	t.Parallel()

	db := openTemp(t)
	writeRaw(t, db, "PROJ-1", []byte{9})
	if err := db.Put(scope, kind, Record{Key: "PROJ-2", Value: []byte("x"), StoredAt: time.Now()}); err != nil {
		t.Fatal(err)
	}
	removed, err := db.Trim(scope, kind, 1)
	if err != nil {
		t.Fatalf("Trim: %v", err)
	}
	if removed != 1 {
		t.Errorf("Trim removed %d, want the unreadable one", removed)
	}
	if _, ok, err := db.Get(scope, kind, "PROJ-2"); err != nil || !ok {
		t.Errorf("the readable record went too (ok=%t, err=%v)", ok, err)
	}
}

func TestLen_FollowsEveryWrite(t *testing.T) {
	t.Parallel()

	db := openTemp(t)
	now := time.Date(2025, time.March, 5, 9, 0, 0, 0, time.UTC)
	check := func(step string, want int) {
		t.Helper()
		if n, err := db.Len(scope, kind); err != nil || n != want {
			t.Errorf("after %s Len = %d (err %v), want %d", step, n, err, want)
		}
	}
	check("nothing", 0)
	for i := range 6 {
		key := fmt.Sprintf("PROJ-%d", i)
		if err := db.Put(scope, kind, Record{Key: key, Value: []byte(key), StoredAt: now.Add(time.Duration(i) * time.Hour)}); err != nil {
			t.Fatal(err)
		}
	}
	check("six puts", 6)
	if err := db.Put(scope, kind, Record{Key: "PROJ-0", Value: []byte("again"), StoredAt: now}); err != nil {
		t.Fatal(err)
	}
	check("a rewrite of a held key", 6)
	if err := db.Delete(scope, kind, "PROJ-5", "PROJ-9"); err != nil {
		t.Fatal(err)
	}
	check("deleting one held and one absent key", 5)
	if _, err := db.Trim(scope, kind, 4); err != nil {
		t.Fatal(err)
	}
	check("a trim to four", 4)
	if _, err := db.Expire(scope, kind, now.Add(150*time.Minute)); err != nil {
		t.Fatal(err)
	}
	check("expiring what was written before 02:30 past", 2)
	if err := db.DropScope(scope); err != nil {
		t.Fatal(err)
	}
	check("dropping the scope", 0)
}

func TestExpire_DropsWhatWasWrittenBeforeTheCutoffAndWhatCannotBeRead(t *testing.T) {
	t.Parallel()

	db := openTemp(t)
	cutoff := time.Date(2025, time.March, 5, 9, 0, 0, 0, time.UTC)
	for key, at := range map[string]time.Time{
		"PROJ-1": cutoff.Add(-time.Hour),
		"PROJ-2": cutoff,
		"PROJ-3": cutoff.Add(time.Hour),
	} {
		if err := db.Put(scope, kind, Record{Key: key, Value: []byte(key), StoredAt: at}); err != nil {
			t.Fatal(err)
		}
	}
	writeRaw(t, db, "PROJ-4", []byte{1})

	removed, err := db.Expire(scope, kind, cutoff)
	if err != nil {
		t.Fatalf("Expire: %v", err)
	}
	if removed != 2 {
		t.Errorf("Expire removed %d, want PROJ-1 and the unreadable PROJ-4", removed)
	}
	var left []string
	if _, err := db.Each(scope, kind, func(r Record) bool { left = append(left, r.Key); return true }); err != nil {
		t.Fatal(err)
	}
	if fmt.Sprint(left) != "[PROJ-2 PROJ-3]" {
		t.Errorf("left %v, want [PROJ-2 PROJ-3]", left)
	}
	if n, err := db.Expire(scope, "never-written", cutoff); err != nil || n != 0 {
		t.Errorf("expiring a kind never written to: %d, %v", n, err)
	}
}

func TestScopes_ListsEveryProfileAndDropScopeRemovesOnlyOne(t *testing.T) {
	t.Parallel()

	db := openTemp(t)
	other := Scope{Site: scope.Site, Account: "someone.else@example.com"}
	third := Scope{Site: "other.atlassian.net", Account: scope.Account}
	now := time.Now()
	for _, s := range []Scope{scope, other, third} {
		for _, k := range []string{"issue", "search", "board"} {
			if err := db.Put(s, k, Record{Key: "K", Value: []byte("v"), StoredAt: now}); err != nil {
				t.Fatal(err)
			}
		}
	}
	held, err := db.Scopes()
	if err != nil {
		t.Fatalf("Scopes: %v", err)
	}
	if len(held) != 3 {
		t.Fatalf("Scopes = %v, want the three written", held)
	}

	if err := db.DropScope(other); err != nil {
		t.Fatalf("DropScope: %v", err)
	}
	held, _ = db.Scopes()
	if slices.Contains(held, other) || len(held) != 2 {
		t.Errorf("after dropping %v Scopes = %v", other, held)
	}
	for _, s := range []Scope{scope, third} {
		if _, ok, _ := db.Get(s, "search", "K"); !ok {
			t.Errorf("dropping one profile removed %v's search", s)
		}
	}
	if err := db.DropScope(Scope{Site: "nowhere", Account: "nobody"}); err != nil {
		t.Errorf("dropping a scope with nothing stored: %v", err)
	}
}
