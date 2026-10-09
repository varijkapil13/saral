package draft

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
)

// Store is a directory of drafts: text a person typed that has not reached the
// site. docs/UX.md principle 6 asks that it survive a failed request, a
// conflict and a crash, so it lives on disk, beside the profile rather than in
// the cache a user is entitled to delete. The zero Store keeps nothing, which
// is what a session with nowhere to write gets.
type Store struct{ dir string }

// Open keeps drafts under dir.
func Open(dir string) Store { return Store{dir: dir} }

// Available reports a store that keeps anything.
func (s Store) Available() bool { return s.dir != "" }

// Path is where the draft named by segments lives, "" for a store that keeps
// nothing. Each segment is used as given, so a caller passes SafeName output.
func (s Store) Path(segments ...string) string {
	if !s.Available() {
		return ""
	}
	return filepath.Join(append([]string{s.dir}, segments...)...)
}

// Read returns the file at path, and false with no error when there is none.
func (s Store) Read(path string) (body []byte, ok bool, err error) {
	if path == "" {
		return nil, false, nil
	}
	body, err = os.ReadFile(path) //nolint:gosec // the path is built from SafeName segments under the store's directory
	if errors.Is(err, fs.ErrNotExist) {
		return nil, false, nil
	}
	if err != nil {
		return nil, false, err
	}
	return body, true, nil
}

// Write replaces the file at path. It is written beside its target and renamed
// over it, so a crash half way through leaves the previous draft rather than a
// truncated one.
func (s Store) Write(path string, body []byte) error {
	if path == "" {
		return nil
	}
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(dir, ".draft-*")
	if err != nil {
		return err
	}
	name := tmp.Name()
	defer func() { _ = os.Remove(name) }()
	if err := tmp.Chmod(0o600); err != nil {
		_ = tmp.Close()
		return err
	}
	if _, err := tmp.Write(body); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(name, path)
}

// Remove forgets the file at path; one already gone is not an error.
func (s Store) Remove(path string) error {
	if path == "" {
		return nil
	}
	if err := os.Remove(path); err != nil && !errors.Is(err, fs.ErrNotExist) {
		return err
	}
	return nil
}

// SafeName keeps a path segment to ASCII letters, digits, '-', '_' and the
// runes in also, everything else becoming '_', so a site host, an issue key or
// an id can never reach outside the store. A segment left empty is "unnamed".
func SafeName(s, also string) string {
	out := strings.Map(func(r rune) rune {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9', r == '-', r == '_':
			return r
		case strings.ContainsRune(also, r):
			return r
		default:
			return '_'
		}
	}, s)
	if out == "" {
		return "unnamed"
	}
	return out
}

// Migrate moves every draft with the extension ext kept one directory deep
// under from into the same place under to, then removes what it emptied. A
// draft already at the destination is newer than the one left behind, so it
// wins and the old one stays where it was rather than being lost.
func Migrate(from, to, ext string) {
	groups, err := os.ReadDir(from)
	if err != nil {
		return
	}
	for _, group := range groups {
		if !group.IsDir() {
			continue
		}
		files, err := os.ReadDir(filepath.Join(from, group.Name()))
		if err != nil {
			continue
		}
		for _, f := range files {
			if f.IsDir() || !strings.HasSuffix(f.Name(), ext) {
				continue
			}
			move(filepath.Join(from, group.Name(), f.Name()), filepath.Join(to, group.Name(), f.Name()))
		}
		_ = os.Remove(filepath.Join(from, group.Name()))
	}
	_ = os.Remove(from)
}

func move(src, dst string) {
	if _, err := os.Lstat(dst); err == nil {
		return
	}
	if err := os.MkdirAll(filepath.Dir(dst), 0o700); err != nil {
		return
	}
	if os.Rename(src, dst) == nil {
		return
	}
	body, err := os.ReadFile(src) //nolint:gosec // src is a directory entry under the legacy drafts directory
	if err != nil {
		return
	}
	if os.WriteFile(dst, body, 0o600) == nil {
		_ = os.Remove(src)
	}
}
