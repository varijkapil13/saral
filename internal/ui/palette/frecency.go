package palette

import (
	"path/filepath"
	"strings"
	"sync"

	tea "charm.land/bubbletea/v2"

	appsearch "github.com/varijkapil13/saral/internal/app/search"
	"github.com/varijkapil13/saral/internal/config"
)

// Where the tables are kept. They are the palette's own files under the cache
// directory rather than the profile: docs/ARCHITECTURE.md asks that config.toml
// stay safe to share, and what a person runs most is not.
const (
	usageDir     = "palette"
	usageFile    = "usage.json"
	projectFile  = "projects.json"
	commandsPart = "commands"
	projectsPart = "projects"
)

type table = appsearch.Frecency

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
// one: no home directory, an unwritable cache.
func usagePath(file string) string {
	dir, err := config.CacheDir()
	if err != nil || strings.TrimSpace(dir) == "" {
		return ""
	}
	return filepath.Join(dir, usageDir, file)
}

func openTable(path, part string) *table { return appsearch.OpenFrecency(path, part) }

// save writes whatever Ran has changed off the event loop, and is nil when there
// is nothing to write.
func save(t *table) tea.Cmd {
	snapshot, ok := t.Pending()
	if !ok {
		return nil
	}
	return func() tea.Msg {
		t.Write(snapshot)
		return nil
	}
}
