package board

import (
	"testing"

	appcache "github.com/varijkapil13/saral/internal/app/cache"
	"github.com/varijkapil13/saral/internal/app/cache/cachetest"
	"github.com/varijkapil13/saral/pkg/jira"
	"github.com/varijkapil13/saral/pkg/jira/jiratest"
)

var columns = []jira.Column{{Name: "Waiting", StatusIDs: []string{"1"}}}

func boardSnap(issues []jira.Issue) func() appcache.BoardSnapshot {
	return func() appcache.BoardSnapshot {
		return appcache.BoardSnapshot{Config: jira.BoardConfig{BoardID: 7, Name: "Ledger", Columns: columns}, Issues: issues}
	}
}

// rowsOnly is a cache with neither a board nor a backlog half.
type rowsOnly struct{ appcache.Cache }

func TestShelf_WithNowhereToKeepABoardWritesNothing(t *testing.T) {
	t.Parallel()
	s := NewBoardShelf(rowsOnly{})
	if s.Held() {
		t.Fatal("a cache of rows alone holds boards")
	}
	if _, _, ok := s.Last("PROJ"); ok {
		t.Error("a shelf with nowhere to keep a board remembered one")
	}
	if put := s.Write(1, 7, true, false, boardSnap(nil), boardSnap(nil)); put != nil {
		t.Error("a shelf with nowhere to keep a board has a write")
	}
	if s.Forget(7) != nil || s.Remember("PROJ", 7) != nil {
		t.Error("forgetting or remembering on no cache failed")
	}
	var none *Shelf[appcache.BoardSnapshot]
	if none.Write(1, 7, true, false, boardSnap(nil), boardSnap(nil)) != nil {
		t.Error("a nil shelf has a write")
	}
}

func TestShelf_KeepsAndForgetsTheLastBoard(t *testing.T) {
	t.Parallel()
	s := NewBoardShelf(cachetest.Open(t))
	issues := jiratest.Gen(3)
	if err := s.Write(1, 7, true, false, boardSnap(issues), boardSnap(issues))(); err != nil {
		t.Fatal(err)
	}
	if err := s.Remember("PROJ", 7); err != nil {
		t.Fatal(err)
	}
	id, snap, ok := s.Last("PROJ")
	if !ok || id != 7 || len(snap.Issues) != 3 {
		t.Fatalf("Last = %d, %d issues, %v", id, len(snap.Issues), ok)
	}
	if err := s.Forget(7); err != nil {
		t.Fatal(err)
	}
	if _, ok := s.Get(7); ok {
		t.Error("a forgotten board is still kept")
	}
}

// A write from a read that has since been replaced is dropped, so a late page
// of the old read cannot append to the new one's order.
func TestShelf_DropsAWriteFromAReplacedRead(t *testing.T) {
	t.Parallel()
	s := NewBacklogShelf(cachetest.Open(t))
	snap := func(n int) func() appcache.BacklogSnapshot {
		return func() appcache.BacklogSnapshot {
			return appcache.BacklogSnapshot{Config: jira.BoardConfig{BoardID: 7, Columns: columns}, Issues: jiratest.Gen(n)}
		}
	}
	late := s.Write(1, 7, false, false, snap(1), snap(1))
	if err := s.Write(2, 7, true, true, snap(2), snap(2))(); err != nil {
		t.Fatal(err)
	}
	if err := late(); err != nil {
		t.Fatal(err)
	}
	got, ok := s.Get(7)
	if !ok || len(got.Issues) != 2 {
		t.Errorf("the backlog keeps %d issues, want the newer read's 2", len(got.Issues))
	}
}
