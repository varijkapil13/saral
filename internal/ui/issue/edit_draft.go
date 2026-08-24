package issue

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/varijkapil13/saral/internal/config"
)

// draft is an edit that has not reached Jira. docs/UX.md principle 6 is that
// nothing typed is ever lost, so an edit is written down as soon as a field is
// committed and stays on disk until the write it belongs to succeeds — through
// a rejected request, a 409 and a crash alike.
type draft struct {
	Key     string    `json:"key"`
	Site    string    `json:"site"`
	SavedAt time.Time `json:"savedAt"`
	// Values are the edited fields by field ID, in the form they were typed.
	Values map[string]string `json:"values,omitempty"`
	// Description is the document $EDITOR produced, kept as ADF because that is
	// what was reconciled against the original and re-rendering it as markdown
	// would put it through a second lossy trip.
	Description json.RawMessage `json:"description,omitempty"`
}

func (d draft) isEmpty() bool { return len(d.Values) == 0 && len(d.Description) == 0 }

// draftStore keeps drafts under one directory, one file per issue per site.
//
// They live beside the profile rather than in the cache directory: a cache is
// something a user is entitled to delete, and the one thing in here is text
// nobody else has a copy of.
type draftStore struct{ dir string }

// newDraftStore locates the draft directory. A store with no directory keeps
// nothing and says so, which is what a session with nowhere to write gets.
func newDraftStore() (draftStore, error) {
	dir, err := config.Dir()
	if err != nil {
		return draftStore{}, fmt.Errorf("locating the drafts directory: %w", err)
	}
	return draftStore{dir: filepath.Join(dir, "drafts")}, nil
}

func (s draftStore) available() bool { return s.dir != "" }

// path is where one issue's draft lives. The site is a directory so that two
// profiles on two sites cannot overwrite each other's draft of the same key.
func (s draftStore) path(site, key string) string {
	return filepath.Join(s.dir, safeName(site), safeName(key)+".json")
}

// safeName reduces a site host or an issue key to something that is a filename
// on every platform this runs on.
func safeName(s string) string {
	out := strings.Map(func(r rune) rune {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9':
			return r
		case r == '-', r == '.', r == '_':
			return r
		default:
			return '_'
		}
	}, strings.TrimSpace(s))
	if out == "" {
		return "unnamed"
	}
	return out
}

// load reads the draft kept for an issue, if there is one.
func (s draftStore) load(site, key string) (draft, bool, error) {
	if !s.available() {
		return draft{}, false, nil
	}
	body, err := os.ReadFile(s.path(site, key)) //nolint:gosec // the path is built from the store's own directory
	if errors.Is(err, fs.ErrNotExist) {
		return draft{}, false, nil
	}
	if err != nil {
		return draft{}, false, fmt.Errorf("reading the draft of %s: %w", key, err)
	}
	var kept draft
	if err := json.Unmarshal(body, &kept); err != nil {
		return draft{}, false, fmt.Errorf("reading the draft of %s: %w", key, err)
	}
	return kept, !kept.isEmpty(), nil
}

// save writes a draft, replacing whatever was there. It writes to a temporary
// file first, so that a crash halfway through leaves the previous draft rather
// than half of this one.
func (s draftStore) save(d draft) error {
	if !s.available() {
		return nil
	}
	if d.isEmpty() {
		return s.discard(d.Site, d.Key)
	}
	body, err := json.Marshal(d)
	if err != nil {
		return fmt.Errorf("writing the draft of %s: %w", d.Key, err)
	}
	final := s.path(d.Site, d.Key)
	if err := os.MkdirAll(filepath.Dir(final), 0o700); err != nil {
		return fmt.Errorf("writing the draft of %s: %w", d.Key, err)
	}
	temp, err := os.CreateTemp(filepath.Dir(final), ".draft-*")
	if err != nil {
		return fmt.Errorf("writing the draft of %s: %w", d.Key, err)
	}
	name := temp.Name()
	if _, err := temp.Write(body); err != nil {
		_ = temp.Close()
		_ = os.Remove(name)
		return fmt.Errorf("writing the draft of %s: %w", d.Key, err)
	}
	if err := temp.Close(); err != nil {
		_ = os.Remove(name)
		return fmt.Errorf("writing the draft of %s: %w", d.Key, err)
	}
	if err := os.Chmod(name, 0o600); err != nil {
		_ = os.Remove(name)
		return fmt.Errorf("writing the draft of %s: %w", d.Key, err)
	}
	if err := os.Rename(name, final); err != nil {
		_ = os.Remove(name)
		return fmt.Errorf("writing the draft of %s: %w", d.Key, err)
	}
	return nil
}

// discard removes an issue's draft, which is what a write that landed and an
// edit the user threw away both mean.
func (s draftStore) discard(site, key string) error {
	if !s.available() {
		return nil
	}
	if err := os.Remove(s.path(site, key)); err != nil && !errors.Is(err, fs.ErrNotExist) {
		return fmt.Errorf("removing the draft of %s: %w", key, err)
	}
	return nil
}
