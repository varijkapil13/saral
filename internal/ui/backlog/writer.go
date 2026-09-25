package backlog

import (
	"sync"

	tea "charm.land/bubbletea/v2"

	"github.com/varijkapil13/saral/internal/ui/kernel"
)

// writer drops a write from a read that has since been replaced: a late page
// of the old read would otherwise append to the new one's order.
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

func stored(put func() error) tea.Cmd {
	if put == nil {
		return nil
	}
	return func() tea.Msg {
		if err := put(); err != nil {
			return kernel.Warn("this backlog could not be stored for next time: " + err.Error())()
		}
		return nil
	}
}
