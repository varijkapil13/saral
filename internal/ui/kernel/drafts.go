package kernel

import (
	"path/filepath"

	"github.com/varijkapil13/saral/internal/config"
)

// DraftRoot is the directory every view keeps its unsent text under, each in a
// subdirectory of its own. It lives beside the profile rather than in the cache
// directory: a cache is something a user is entitled to delete.
func (d Deps) DraftRoot() (string, error) {
	if d.DraftsDir != "" {
		return d.DraftsDir, nil
	}
	dir, err := config.Dir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "drafts"), nil
}
