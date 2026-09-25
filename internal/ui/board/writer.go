package board

import "sync"

// writer drops a write from a read that has since been replaced: a late page
// of the old read would otherwise append to the new one's card order.
type writer struct {
	mu   sync.Mutex
	read int
}

func (w *writer) put(gen int, first bool, write func() error) func() error {
	return func() error {
		w.mu.Lock()
		defer w.mu.Unlock()
		if gen < w.read {
			return nil
		}
		if first {
			w.read = gen
		}
		return write()
	}
}
