package palette

import (
	"encoding/json"
	"maps"
	"math"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	tea "charm.land/bubbletea/v2"

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

// Where the tables are kept. They are the palette's own files under the cache
// directory rather than the profile: docs/ARCHITECTURE.md asks that config.toml
// stay safe to share, and what a person runs most is not.
//
// Commands and projects are one shape and two answers, so they are one type over
// two files rather than one map keyed by both.
const (
	usageDir     = "palette"
	usageFile    = "usage.json"
	projectFile  = "projects.json"
	commandsPart = "commands"
	projectsPart = "projects"
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
	mu sync.Mutex
	// part is the object the entries sit under in the file, so that a table read
	// as commands can never be written back as projects.
	part string
	path string
	uses map[string]use
	// dirty is a run Save has not yet written.
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

// shared is the table the running program uses. The palette is built fresh on
// every ctrl+k, so anything counted has to outlive the instance that counted it.
var (
	sharedOnce sync.Once
	shared     *table

	projectOnce sync.Once
	sharedProj  *table
)

func sharedTable() *table {
	sharedOnce.Do(func() { shared = openTable(usagePath(usageFile), commandsPart) })
	return shared
}

// sharedProjectTable is the projects' own table. The picker is built fresh on
// every "Switch project", so what it counts has to outlive the instance.
func sharedProjectTable() *table {
	projectOnce.Do(func() { sharedProj = openTable(usagePath(projectFile), projectsPart) })
	return sharedProj
}

// usagePath is where a table lives, and "" for a session with nowhere to keep
// one — no home directory, an unwritable cache.
func usagePath(file string) string {
	dir, err := config.CacheDir()
	if err != nil || strings.TrimSpace(dir) == "" {
		return ""
	}
	return filepath.Join(dir, usageDir, file)
}

func openTable(path, part string) *table {
	t := &table{part: part, path: path, uses: make(map[string]use, 32)}
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
// from here. The mutation is a map write and stays synchronous; the disk write
// it earns is Save's job, off the event loop.
func (t *table) ran(id string, now time.Time) int {
	if strings.TrimSpace(id) == "" {
		return 0
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	held := t.uses[id]
	held.Count++
	held.Last = now
	t.uses[id] = held
	t.trim(now)
	t.dirty = true
	return held.Count
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
	var stored map[string]map[string]use
	if err := json.Unmarshal(raw, &stored); err != nil {
		return
	}
	for id, held := range stored[t.part] {
		if strings.TrimSpace(id) == "" || held.Count <= 0 {
			continue
		}
		t.uses[id] = held
	}
}

// Save schedules a write of whatever Ran has changed since the last one, off
// the event loop: CreateTemp/write/rename on every run put a command's disk
// I/O on the goroutine that has to draw the next frame. It is coalesced
// against a write already going — Ran landing while one is in flight marks the
// table dirty again rather than this starting a second write, and flush picks
// that up once it is back — and nil when there is nothing to write or a
// previous failure has already stopped this table from trying.
func (t *table) Save() tea.Cmd {
	t.mu.Lock()
	if t.path == "" || t.stopped || t.saving || !t.dirty {
		t.mu.Unlock()
		return nil
	}
	t.saving, t.dirty = true, false
	snapshot := maps.Clone(t.uses)
	t.mu.Unlock()
	return func() tea.Msg {
		t.flush(snapshot)
		return nil
	}
}

// flush is Save's write, run off the event loop against the snapshot Save
// took under the lock: the map itself keeps moving on the event loop while
// this runs, and marshalling it here without a copy would race that.
func (t *table) flush(snapshot map[string]use) {
	err := t.write(snapshot)
	t.mu.Lock()
	defer t.mu.Unlock()
	t.saving = false
	if err != nil {
		t.stopped = true
		if t.failure == nil {
			t.failure = err
		}
	}
}

// Warning is the first save failure this table hit, reported once: a caller
// puts it on the status line, and nothing hands it back again after.
func (t *table) Warning() (string, bool) {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.failure == nil || t.warned {
		return "", false
	}
	t.warned = true
	return "what gets used here won't be remembered next time: " + t.failure.Error(), true
}

// write saves the table beside its target and renames it over the top, so that
// a crash half way through leaves the previous table rather than a truncated
// one.
func (t *table) write(uses map[string]use) error {
	raw, err := json.Marshal(map[string]map[string]use{t.part: uses})
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
