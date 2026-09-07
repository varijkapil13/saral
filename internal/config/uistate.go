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

	// Sorts maps a view id to the order it was last left reading its rows in.
	// Beside Splits for the same reason: how this machine likes to look at a
	// view is not a Jira account's business.
	Sorts map[string]SortSpec `toml:"sorts"`

	// Remembered maps a site to an account on it to what that profile scope
	// was last left showing — see ProfileScope and Remembered's own doc for
	// why this half of the file is keyed by profile rather than by view alone.
	Remembered map[string]map[string]Remembered `toml:"remembered"`
}

// SortSpec is the order one view reads its rows in: a field it named for
// itself and the direction, never anything read off a site.
type SortSpec struct {
	Field string `toml:"field"`
	Desc  bool   `toml:"desc"`
}

// ProfileScope is whose remembered view and filters a Remembered entry is: the
// same site-and-account pair cmd/saral/main.go's openCache scopes the cache
// by. Unlike Splits and Sorts, a filter names a person, a status or an issue
// type by an id the site minted, which means nothing to any other site and
// can mean a different thing to another account's project visibility — so
// this half of UIState is kept by profile scope and never by view alone.
type ProfileScope struct {
	Site    string
	Account string
}

// Remembered is what one profile scope was left showing: state a view kept
// for itself under a key of its own naming — a filter's encoding, a board's
// active quick filters, the id of the root view a session last had open.
type Remembered struct {
	State map[string]string `toml:"state"`
}

// stateKey is how a view and a key of its own naming become one map key. A
// view never sees another's: the kernel scopes every read and write to the
// view name it was called with.
func stateKey(view, key string) string { return view + "." + key }

// remembered finds one profile scope's entry, and whether it has ever kept
// anything at all.
func (s UIState) remembered(scope ProfileScope) (Remembered, bool) {
	bySite, ok := s.Remembered[scope.Site]
	if !ok {
		return Remembered{}, false
	}
	r, ok := bySite[scope.Account]
	return r, ok
}

// RememberedState is what a view kept for itself under a key of its own
// naming, the last time this profile scope wrote one, and whether it ever
// did. A blank value is read as never having chosen one, the same "no choice
// a gesture could have produced" reading Split and Sort already give.
func (s UIState) RememberedState(scope ProfileScope, view, key string) (string, bool) {
	r, ok := s.remembered(scope)
	if !ok {
		return "", false
	}
	v, ok := r.State[stateKey(view, key)]
	if !ok || v == "" {
		return "", false
	}
	return v, true
}

// RememberedState reads one profile scope's kept value straight off disk,
// remembering nothing when it cannot read the file for any of the reasons
// LoadUIState already tolerates.
func RememberedState(scope ProfileScope, view, key string) (string, bool) {
	return LoadUIState().RememberedState(scope, view, key)
}

// RememberState writes one profile scope's kept value and the file, keeping
// every other scope's and every other key of this one's — the same
// read-merge-write SaveSplit and SaveSort already do. An empty value is a view
// that has gone back to having chosen nothing, and is removed rather than
// written as a choice nothing would produce.
func RememberState(scope ProfileScope, view, key, value string) error {
	return mutateRemembered(scope, func(r *Remembered) {
		k := stateKey(view, key)
		if strings.TrimSpace(value) == "" {
			delete(r.State, k)
			return
		}
		if r.State == nil {
			r.State = make(map[string]string, 1)
		}
		r.State[k] = value
	})
}

// ForgetRemembered drops everything one profile scope has ever kept — every
// view's state and the root view it last opened — which is what a session
// asking to forget its remembered view and filters means.
func ForgetRemembered(scope ProfileScope) error {
	return mutateRemembered(scope, func(r *Remembered) { *r = Remembered{} })
}

// mutateRemembered is RememberState's and ForgetRemembered's shared
// read-merge-write, pruning a scope back out of the file the moment it has
// nothing left to say — the same discipline SaveSplit and SaveSort hold a
// zero share and a blank field to.
func mutateRemembered(scope ProfileScope, fn func(*Remembered)) error {
	path, err := UIStatePath()
	if err != nil {
		return err
	}
	uiWrite.Lock()
	defer uiWrite.Unlock()

	state := LoadUIState()
	bySite := state.Remembered[scope.Site]
	r := bySite[scope.Account]
	fn(&r)

	if len(r.State) == 0 {
		delete(bySite, scope.Account)
	} else {
		if bySite == nil {
			bySite = make(map[string]Remembered, 1)
		}
		bySite[scope.Account] = r
	}
	if len(bySite) == 0 {
		if state.Remembered != nil {
			delete(state.Remembered, scope.Site)
		}
	} else {
		if state.Remembered == nil {
			state.Remembered = make(map[string]map[string]Remembered, 1)
		}
		state.Remembered[scope.Site] = bySite
	}

	var b strings.Builder
	if err := toml.NewEncoder(&b).Encode(state); err != nil {
		return fmt.Errorf("encoding %s: %w", path, err)
	}
	return writeAtomic(path, []byte(b.String()))
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

// Sort is the order the named view was last left reading its rows in, and
// whether it ever chose one at all. A field this program no longer names, from
// a file edited by hand or from a build that once offered a ninth field, is
// read as no choice rather than obeyed.
func (s UIState) Sort(view string) (SortSpec, bool) {
	spec, kept := s.Sorts[view]
	if !kept || strings.TrimSpace(spec.Field) == "" {
		return SortSpec{}, false
	}
	return spec, true
}

// SaveSort records one view's chosen order and writes the file, keeping every
// other view's — the same read-merge-write SaveSplit already does, so two
// views choosing at once cannot lose each other's. A blank field is a view
// that has gone back to its search's own order, and is removed rather than
// written as a choice nothing would produce.
func SaveSort(view string, spec SortSpec) error {
	path, err := UIStatePath()
	if err != nil {
		return err
	}
	uiWrite.Lock()
	defer uiWrite.Unlock()

	state := LoadUIState()
	if state.Sorts == nil {
		state.Sorts = make(map[string]SortSpec, 1)
	}
	if strings.TrimSpace(spec.Field) == "" {
		delete(state.Sorts, view)
	} else {
		state.Sorts[view] = spec
	}
	var b strings.Builder
	if err := toml.NewEncoder(&b).Encode(state); err != nil {
		return fmt.Errorf("encoding %s: %w", path, err)
	}
	return writeAtomic(path, []byte(b.String()))
}
