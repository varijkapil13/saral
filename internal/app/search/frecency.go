package search

import (
	"encoding/json"
	"maps"
	"math"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

// freqHalfLife is how long it takes for a use to count for half of what it did.
// docs/UX.md puts frecency in the first week, so a week is what a habit is
// measured against: yesterday's ten runs still beat last month's twenty.
const freqHalfLife = 7 * 24 * time.Hour

// freqBound is how many items the table remembers. The registry is smaller than
// this, so what the bound actually catches is IDs from older builds piling up
// behind renames.
const freqBound = 200

type freqUse struct {
	Count int       `json:"count"`
	Last  time.Time `json:"last"`
}

// Frecency is the table docs/UX.md describes: a plain local table of
// (item, count, lastUsed) scored count * decay(lastUsed). Nothing leaves the
// machine: what it holds is command IDs from this build and integers.
//
// A session with nowhere to write keeps it in memory for as long as it runs, so
// ranking degrades to the registry's own order across restarts rather than
// failing or refusing to draw.
type Frecency struct {
	mu sync.Mutex
	// part is the object the entries sit under in the file, so that a table read
	// as commands can never be written back as projects.
	part string
	path string
	uses map[string]freqUse
	// dirty is a run Pending has not yet handed out.
	dirty bool
	// saving is a write already in flight, so Ran landing while one is going
	// only marks the table dirty again rather than starting a second write.
	saving bool
	// stopped records a write that failed. Ranking carries on in memory; there
	// is nothing to be gained from retrying a path that just refused.
	stopped bool
	// failure is the first error a write hit, and warned is whether Warning has
	// already handed it to a caller: said once, not on every keystroke after.
	failure error
	warned  bool
}

// FrecencySnapshot is what Pending took under the lock, for Write to put on disk.
type FrecencySnapshot struct {
	uses map[string]freqUse
}

// OpenFrecency loads the table at path under part; an empty path is a table kept in memory only.
func OpenFrecency(path, part string) *Frecency {
	f := &Frecency{part: part, path: path, uses: make(map[string]freqUse, 32)}
	f.load()
	return f
}

// Score is count * decay(lastUsed), and zero for an item never run. An entry
// dated in the future decays by nothing rather than by a negative amount: a
// clock that moved backwards must not invent a ranking.
func (f *Frecency) Score(id string, now time.Time) float64 {
	f.mu.Lock()
	defer f.mu.Unlock()
	held, ok := f.uses[id]
	if !ok || held.Count <= 0 {
		return 0
	}
	return float64(held.Count) * freqDecay(now.Sub(held.Last))
}

func freqDecay(age time.Duration) float64 {
	if age <= 0 {
		return 1
	}
	return math.Exp2(-age.Hours() / freqHalfLife.Hours())
}

// Ran records one run and returns how many there have now been. The mutation is
// a map write and stays synchronous; the disk write it earns is Pending and
// Write's job, off the event loop.
func (f *Frecency) Ran(id string, now time.Time) int {
	if strings.TrimSpace(id) == "" {
		return 0
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	held := f.uses[id]
	held.Count++
	held.Last = now
	f.uses[id] = held
	f.trim(now)
	f.dirty = true
	return held.Count
}

// trim drops whatever ranks lowest until the table is bounded. The caller holds the lock.
func (f *Frecency) trim(now time.Time) {
	for len(f.uses) > freqBound {
		worst, at := "", math.Inf(1)
		for id, held := range f.uses {
			if s := float64(held.Count) * freqDecay(now.Sub(held.Last)); s < at || (s == at && id < worst) {
				worst, at = id, s
			}
		}
		delete(f.uses, worst)
	}
}

func (f *Frecency) load() {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.path == "" {
		return
	}
	raw, err := os.ReadFile(f.path) //nolint:gosec // the path is this program's own file under the cache directory
	if err != nil {
		return
	}
	var stored map[string]map[string]freqUse
	if err := json.Unmarshal(raw, &stored); err != nil {
		return
	}
	for id, held := range stored[f.part] {
		if strings.TrimSpace(id) == "" || held.Count <= 0 {
			continue
		}
		f.uses[id] = held
	}
}

// Pending claims whatever Ran has changed since the last write, and reports false
// when there is nothing to write, a write is already going or a previous failure
// has stopped this table from trying. A claimed snapshot must be given to Write:
// the map keeps moving on the event loop, so the copy is what gets marshalled.
func (f *Frecency) Pending() (FrecencySnapshot, bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.path == "" || f.stopped || f.saving || !f.dirty {
		return FrecencySnapshot{}, false
	}
	f.saving, f.dirty = true, false
	return FrecencySnapshot{uses: maps.Clone(f.uses)}, true
}

// Write puts a snapshot from Pending on disk and records a failure for Warning.
func (f *Frecency) Write(s FrecencySnapshot) {
	err := f.write(s.uses)
	f.mu.Lock()
	defer f.mu.Unlock()
	f.saving = false
	if err != nil {
		f.stopped = true
		if f.failure == nil {
			f.failure = err
		}
	}
}

// Warning is the first write failure this table hit, reported once.
func (f *Frecency) Warning() (string, bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.failure == nil || f.warned {
		return "", false
	}
	f.warned = true
	return "what gets used here won't be remembered next time: " + f.failure.Error(), true
}

// Reset empties the table and clears its write state, for tests sharing a process-wide table.
func (f *Frecency) Reset() {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.uses = make(map[string]freqUse, 8)
	f.dirty, f.saving, f.stopped, f.warned = false, false, false, false
	f.failure = nil
}

// write saves the table beside its target and renames it over the top, so that
// a crash half way through leaves the previous table rather than a truncated
// one.
func (f *Frecency) write(uses map[string]freqUse) error {
	raw, err := json.Marshal(map[string]map[string]freqUse{f.part: uses})
	if err != nil {
		return err
	}
	dir := filepath.Dir(f.path)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(dir, ".usage-*")
	if err != nil {
		return err
	}
	name := tmp.Name()
	defer func() { _ = os.Remove(name) }()
	if err := tmp.Chmod(0o600); err != nil {
		_ = tmp.Close()
		return err
	}
	if _, err := tmp.Write(raw); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(name, f.path)
}
