package config

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"github.com/BurntSushi/toml"
)

// uiStateFile is where the arrangement of the panes is kept, under the cache
// directory rather than beside config.toml.
const uiStateFile = "ui.toml"

// SplitScale is what a stored split is a share of: a sidebar taking a third of
// its pane is 333.
const SplitScale = 1000

// uiWrite serialises the read-merge-write below, so two views choosing a split
// at once cannot each write a file missing the other's.
var uiWrite sync.Mutex

// UIState is what this machine remembers about how a view is arranged.
//
// It is deliberately not part of a Profile, for three reasons. A pane width
// belongs to the terminal it was chosen in and not to a Jira account, so two
// profiles on one machine want one answer and a config.toml copied to a laptop
// should not carry a desktop's proportions. Onboarding rebuilds a profile from a
// zero value and drops every field it did not collect, so a number kept there is
// lost the next time somebody re-checks their token. And config.toml is a file
// people hand-edit and hand to each other, which a number a drag rewrites is not.
type UIState struct {
	// Splits maps a view id to the share of that view's pane its sidebar takes,
	// out of SplitScale. A share and not a column count, because the same
	// terminal is not always the same width.
	Splits map[string]int `toml:"splits"`
}

// UIStatePath is the file the arrangement is kept in.
func UIStatePath() (string, error) {
	dir, err := CacheDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, uiStateFile), nil
}

// LoadUIState reads what this machine remembers, and remembers nothing when it
// cannot: a first run, no home directory, an unreadable cache, or a file edited
// into something TOML will not parse. This is the program's own record of how a
// pane was left rather than anything a person wrote, and none of those is worth
// refusing to open an issue over — the answer a comment draft gives a read it
// cannot satisfy.
func LoadUIState() UIState {
	path, err := UIStatePath()
	if err != nil {
		return UIState{}
	}
	data, err := os.ReadFile(path) //nolint:gosec // the path is the cache directory's own file
	if err != nil {
		return UIState{}
	}
	var state UIState
	if _, err := toml.Decode(string(data), &state); err != nil {
		return UIState{}
	}
	return state
}

// Split is the share the named view keeps, and whether it keeps one at all. A
// share outside the scale is a file somebody has edited into something no
// gesture could have produced, and is read as no choice rather than obeyed.
func (s UIState) Split(view string) (share int, ok bool) {
	share, kept := s.Splits[view]
	if !kept || share <= 0 || share >= SplitScale {
		return 0, false
	}
	return share, true
}

// SaveSplit records one view's share of its pane and writes the file, keeping
// every other view's. A share of zero is a view that has gone back to the answer
// its width alone gives, and is removed rather than written as a number nothing
// would produce.
func SaveSplit(view string, share int) error {
	path, err := UIStatePath()
	if err != nil {
		return err
	}
	uiWrite.Lock()
	defer uiWrite.Unlock()

	state := LoadUIState()
	if state.Splits == nil {
		state.Splits = make(map[string]int, 1)
	}
	if share <= 0 || share >= SplitScale {
		delete(state.Splits, view)
	} else {
		state.Splits[view] = share
	}
	var b strings.Builder
	if err := toml.NewEncoder(&b).Encode(state); err != nil {
		return fmt.Errorf("encoding %s: %w", path, err)
	}
	return writeAtomic(path, []byte(b.String()))
}
