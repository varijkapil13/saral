package palette

import (
	"encoding/json"
	"math"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/varijkapil13/saral/internal/config"
)

// halfLife is how long it takes for a use to count for half of what it did.
// docs/UX.md puts frecency in the first week, so a week is what a habit is
// measured against: yesterday's ten runs still beat last month's twenty.
const halfLife = 7 * 24 * time.Hour

// bound is how many commands the table remembers. The registry is smaller than
// this, so what the bound actually catches is IDs from older builds piling up
// behind renames.
const bound = 200

// Where the table is kept. It is the palette's own file under the cache
// directory rather than the profile: docs/ARCHITECTURE.md asks that config.toml
// stay safe to share, and what a person runs most is not.
const (
	usageDir  = "palette"
	usageFile = "usage.json"
)

// use is one command's history: how often it has been reached from the palette
// and when that last happened.
type use struct {
	Count int       `json:"count"`
	Last  time.Time `json:"last"`
}

// table is the frecency table docs/UX.md describes — a plain local table of
// (item, count, lastUsed) scored count * decay(lastUsed). Nothing leaves the
// machine: what it holds is command IDs from this build and integers.
//
// A session with nowhere to write keeps it in memory for as long as it runs, so
// ranking degrades to the registry's own order across restarts rather than
// failing or refusing to draw.
type table struct {
	mu   sync.Mutex
	path string
	uses map[string]use
	// stopped records a write that failed. Ranking carries on in memory; there
	// is nothing to be gained from retrying a path that just refused.
	stopped bool
}

// shared is the table the running program uses. The palette is built fresh on
// every ctrl+k, so anything counted has to outlive the instance that counted it.
var (
	sharedOnce sync.Once
	shared     *table
)

func sharedTable() *table {
	sharedOnce.Do(func() { shared = openTable(usagePath()) })
	return shared
}

// usagePath is where the table lives, and "" for a session with nowhere to keep
// one — no home directory, an unwritable cache.
func usagePath() string {
	dir, err := config.CacheDir()
	if err != nil || strings.TrimSpace(dir) == "" {
		return ""
	}
	return filepath.Join(dir, usageDir, usageFile)
}

func openTable(path string) *table {
	t := &table{path: path, uses: make(map[string]use, 32)}
	t.load()
	return t
}

// score is count * decay(lastUsed), and zero for a command never run. An entry
// dated in the future decays by nothing rather than by a negative amount: a
// clock that moved backwards must not invent a ranking.
func (t *table) score(id string, now time.Time) float64 {
	t.mu.Lock()
	defer t.mu.Unlock()
	held, ok := t.uses[id]
	if !ok || held.Count <= 0 {
		return 0
	}
	return float64(held.Count) * decay(now.Sub(held.Last))
}

func decay(age time.Duration) float64 {
	if age <= 0 {
		return 1
	}
	return math.Exp2(-age.Hours() / halfLife.Hours())
}

// ran records one run and returns how many there have now been, which is what
// the hint counts: docs/UX.md notes an action's key the third time it is reached
// from here.
func (t *table) ran(id string, now time.Time) int {
	if strings.TrimSpace(id) == "" {
		return 0
	}
	t.mu.Lock()
	held := t.uses[id]
	held.Count++
	held.Last = now
	t.uses[id] = held
	t.trim(now)
	count := held.Count
	t.mu.Unlock()

	t.save()
	return count
}

// trim keeps the table bounded by dropping whatever ranks lowest. The caller
// holds the lock.
func (t *table) trim(now time.Time) {
	for len(t.uses) > bound {
		worst, at := "", math.Inf(1)
		for id, held := range t.uses {
			if s := float64(held.Count) * decay(now.Sub(held.Last)); s < at || (s == at && id < worst) {
				worst, at = id, s
			}
		}
		delete(t.uses, worst)
	}
}

func (t *table) load() {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.path == "" {
		return
	}
	raw, err := os.ReadFile(t.path) //nolint:gosec // the path is this program's own file under the cache directory
	if err != nil {
		return
	}
	var stored struct {
		Commands map[string]use `json:"commands"`
	}
	if err := json.Unmarshal(raw, &stored); err != nil {
		return
	}
	for id, held := range stored.Commands {
		if strings.TrimSpace(id) == "" || held.Count <= 0 {
			continue
		}
		t.uses[id] = held
	}
}

// save writes the table beside its target and renames it over the top, so that
// a crash half way through leaves the previous table rather than a truncated
// one.
func (t *table) save() {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.path == "" || t.stopped {
		return
	}
	if err := t.write(); err != nil {
		t.stopped = true
	}
}

func (t *table) write() error {
	raw, err := json.Marshal(struct {
		Commands map[string]use `json:"commands"`
	}{Commands: t.uses})
	if err != nil {
		return err
	}
	dir := filepath.Dir(t.path)
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
	return os.Rename(name, t.path)
}
