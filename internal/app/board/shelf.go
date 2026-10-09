package board

import (
	"sync"

	appcache "github.com/varijkapil13/saral/internal/app/cache"
)

// Shelf is where a board or a backlog keeps what it last drew, S being the
// snapshot's shape. Either half of the cache may be absent: a session with
// nowhere to keep one, or a cache that is only rows and issues, has neither,
// and a cache that keeps no pages is written whole.
//
// It also drops a write from a read that has since been replaced: a late page
// of the old read would otherwise append to the new one's order.
type Shelf[S any] struct {
	last     func(project string) (int64, bool)
	get      func(boardID int64) (S, bool)
	put      func(boardID int64, snap S) error
	forget   func(boardID int64) error
	remember func(project string, boardID int64) error
	page     func(boardID int64, snap S, first bool) error

	mu   sync.Mutex
	read int
}

// NewBoardShelf is the board-shaped half of c.
func NewBoardShelf(c appcache.Cache) *Shelf[appcache.BoardSnapshot] {
	s := &Shelf[appcache.BoardSnapshot]{}
	if held, ok := c.(appcache.BoardCache); ok && held != nil {
		s.last, s.get, s.put = held.LastBoard, held.Board, held.PutBoard
		s.forget, s.remember = held.ForgetBoard, held.PutLastBoard
	}
	if paged, ok := c.(appcache.BoardPageCache); ok && paged != nil {
		s.page = paged.PutBoardPage
	}
	return s
}

// NewBacklogShelf is the backlog-shaped half of c.
func NewBacklogShelf(c appcache.Cache) *Shelf[appcache.BacklogSnapshot] {
	s := &Shelf[appcache.BacklogSnapshot]{}
	if held, ok := c.(appcache.BacklogCache); ok && held != nil {
		s.last, s.get, s.put = held.LastBacklogBoard, held.Backlog, held.PutBacklog
		s.forget, s.remember = held.ForgetBacklog, held.PutLastBacklogBoard
	}
	if paged, ok := c.(appcache.BacklogPageCache); ok && paged != nil {
		s.page = paged.PutBacklogPage
	}
	return s
}

// Held reports whether there is anywhere to keep a whole snapshot.
func (s *Shelf[S]) Held() bool { return s != nil && s.put != nil }

// Last is the board a project was last drawing, and what was kept of it.
func (s *Shelf[S]) Last(project string) (boardID int64, snap S, ok bool) {
	if !s.Held() {
		return 0, snap, false
	}
	if boardID, ok = s.last(project); !ok {
		return 0, snap, false
	}
	if snap, ok = s.get(boardID); !ok {
		return 0, snap, false
	}
	return boardID, snap, true
}

// Get is what was kept of one board.
func (s *Shelf[S]) Get(boardID int64) (snap S, ok bool) {
	if !s.Held() {
		return snap, false
	}
	return s.get(boardID)
}

// Forget drops what was kept of one board. The issues themselves stay: they
// are shared with every other read that named them.
func (s *Shelf[S]) Forget(boardID int64) error {
	if !s.Held() || boardID == 0 {
		return nil
	}
	return s.forget(boardID)
}

// Remember writes which board a project is drawing, so a session opening cold
// knows which board's snapshot to read before the site has said which boards
// the project has.
func (s *Shelf[S]) Remember(project string, boardID int64) error {
	if !s.Held() {
		return nil
	}
	return s.remember(project, boardID)
}

// Write is the write of one page of read gen, to run off the update loop, or
// nil when there is nothing to write. A cache that keeps pages is given the
// page alone, which a first page starts afresh; one that keeps no pages is
// written whole, on the first page and once more is false only. paged and
// whole build the snapshot for either case and are called only when it is
// written.
func (s *Shelf[S]) Write(gen int, boardID int64, first, more bool, paged, whole func() S) func() error {
	if s == nil {
		return nil
	}
	if s.page != nil {
		snap := paged()
		return s.ordered(gen, first, func() error { return s.page(boardID, snap, first) })
	}
	if s.put == nil || (!first && more) {
		return nil
	}
	snap := whole()
	return s.ordered(gen, true, func() error { return s.put(boardID, snap) })
}

func (s *Shelf[S]) ordered(gen int, first bool, write func() error) func() error {
	return func() error {
		s.mu.Lock()
		defer s.mu.Unlock()
		if gen < s.read {
			return nil
		}
		if first {
			s.read = gen
		}
		return write()
	}
}
