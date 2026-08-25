package store

import (
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"
)

func TestOpen_CreatesTheFileAndTheDirectoryHoldingIt(t *testing.T) {
	t.Parallel()

	path := filepath.Join(t.TempDir(), "saral", "cache.db")
	db, err := Open(path)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() {
		if err := db.Close(); err != nil {
			t.Errorf("Close: %v", err)
		}
	})

	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("the database is not on disk: %v", err)
	}
	if info.Size() == 0 {
		t.Error("the database is empty, so bbolt never wrote its meta pages")
	}
	if perm := info.Mode().Perm(); perm != filePerm {
		t.Errorf("the cache is mode %v, want %v: it holds issues from a private site", perm, filePerm)
	}
}

func TestOpen_ReopensADatabaseItAlreadyWrote(t *testing.T) {
	t.Parallel()

	path := filepath.Join(t.TempDir(), "cache.db")
	first, err := Open(path)
	if err != nil {
		t.Fatalf("first Open: %v", err)
	}
	if err := first.Close(); err != nil {
		t.Fatalf("first Close: %v", err)
	}
	before, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat after the first close: %v", err)
	}

	second, err := Open(path)
	if err != nil {
		t.Fatalf("reopening: %v", err)
	}
	if err := second.Close(); err != nil {
		t.Fatalf("second Close: %v", err)
	}
	after, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat after the second close: %v", err)
	}
	if after.Size() != before.Size() {
		t.Errorf("reopening changed the file from %d to %d bytes, so it was not reused",
			before.Size(), after.Size())
	}
}

func TestOpen_FailsWhenThePathCannotBeCreated(t *testing.T) {
	t.Parallel()

	blocker := filepath.Join(t.TempDir(), "saral")
	if err := os.WriteFile(blocker, []byte("not a directory"), filePerm); err != nil {
		t.Fatalf("writing the blocking file: %v", err)
	}

	db, err := Open(filepath.Join(blocker, "cache.db"))
	if err == nil {
		t.Fatalf("Open succeeded below a regular file; closing: %v", db.Close())
	}
	if errors.Is(err, ErrLocked) {
		t.Errorf("a path that cannot be created was reported as another process holding it: %v", err)
	}
}

func TestOpen_FailsWhenTheFileIsNotADatabase(t *testing.T) {
	t.Parallel()

	path := filepath.Join(t.TempDir(), "cache.db")
	if err := os.WriteFile(path, []byte("this was never a bbolt file"), filePerm); err != nil {
		t.Fatalf("writing the impostor: %v", err)
	}

	db, err := Open(path, WithLockTimeout(time.Second))
	if err == nil {
		t.Fatalf("Open accepted a file that is not a database; closing: %v", db.Close())
	}
}

func TestOpen_ReportsThatAnotherCopyOfSaralHasTheFile(t *testing.T) {
	t.Parallel()

	path := filepath.Join(t.TempDir(), "cache.db")
	held, err := Open(path)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() {
		if err := held.Close(); err != nil {
			t.Errorf("Close: %v", err)
		}
	})

	second, err := Open(path, WithLockTimeout(100*time.Millisecond))
	if err == nil {
		t.Fatalf("a second opener took the locked file; closing: %v", second.Close())
	}
	if !errors.Is(err, ErrLocked) {
		t.Fatalf("a second opener got %v, want it to report ErrLocked", err)
	}
	if !strings.Contains(err.Error(), path) {
		t.Errorf("the error %q does not name the file the other copy is holding", err)
	}
}

func TestOpen_TakesTheFileOnceTheFirstOpenerHasClosed(t *testing.T) {
	t.Parallel()

	path := filepath.Join(t.TempDir(), "cache.db")
	first, err := Open(path)
	if err != nil {
		t.Fatalf("first Open: %v", err)
	}
	if err := first.Close(); err != nil {
		t.Fatalf("first Close: %v", err)
	}

	second, err := Open(path, WithLockTimeout(100*time.Millisecond))
	if err != nil {
		t.Fatalf("the lock outlived the close: %v", err)
	}
	if err := second.Close(); err != nil {
		t.Errorf("second Close: %v", err)
	}
}

func TestScopeBucket_KeepsProfilesAndKindsApart(t *testing.T) {
	t.Parallel()

	const (
		site    = "example.atlassian.net"
		account = "5b10a2844c20165700ede21g"
	)
	work := Scope{Site: site, Account: account}

	tests := map[string]struct {
		scope        Scope
		kind         string
		sameAsTheRef bool
	}{
		"the same site, account and kind": {
			scope:        Scope{Site: site, Account: account},
			kind:         "issue",
			sameAsTheRef: true,
		},
		"another kind for one account": {scope: work, kind: "search"},
		"another account on one site":  {scope: Scope{Site: site, Account: account + "h"}, kind: "issue"},
		"the same account on another site": {
			scope: Scope{Site: "other.atlassian.net", Account: account},
			kind:  "issue",
		},
		"a kind that continues where the account ends": {
			scope: Scope{Site: site, Account: account[:len(account)-1]},
			kind:  account[len(account)-1:] + "issue",
		},
	}

	reference := work.Bucket("issue")
	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			got := tc.scope.Bucket(tc.kind)
			if slices.Equal(got, reference) != tc.sameAsTheRef {
				t.Errorf("%+v under %q names %q against %q, want the same name = %t",
					tc.scope, tc.kind, got, reference, tc.sameAsTheRef)
			}
		})
	}
}
