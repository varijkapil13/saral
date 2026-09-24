package comment

import (
	"os"
	"path/filepath"
	"strings"
	"sync"

	"github.com/varijkapil13/saral/internal/config"
	"github.com/varijkapil13/saral/internal/ui/kernel"
)

// draftDirName is where unsent comments live under the drafts directory. They
// are the user's own words rather than anything fetched, so nothing in the
// program deletes one except sending it or clearing it by hand.
const draftDirName = "comments"

// draftKey names one draft: a new comment on an issue, or an edit of one
// comment. The two are separate drafts, because abandoning an edit must not
// take the half-written new comment beside it with it.
type draftKey struct {
	site    string
	issue   string
	comment string
}

func (k draftKey) file() string {
	name := k.comment
	if name == "" {
		name = "new"
	}
	return safeName(k.issue) + "." + safeName(name) + ".md"
}

// safeName keeps a path segment to characters that mean the same thing on every
// filesystem, so an issue key or a comment id can never reach outside the
// drafts directory.
func safeName(s string) string {
	var b strings.Builder
	b.Grow(len(s))
	for _, r := range s {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9', r == '-', r == '_':
			b.WriteRune(r)
		default:
			b.WriteByte('_')
		}
	}
	if b.Len() == 0 {
		return "unnamed"
	}
	return b.String()
}

// drafts is where unsent text is kept between sessions. docs/UX.md principle 6
// asks that anything typed survive a failed request, a conflict and a crash,
// which means on disk rather than in the model.
type drafts struct {
	root string
}

// openDrafts finds the drafts directory. A session with nowhere to write —
// no home directory, an unwritable profile directory — gets a store that keeps
// nothing rather than a failure, because a comment nobody can save is still
// worth typing.
func openDrafts(deps kernel.Deps) *drafts {
	dir, err := deps.DraftRoot()
	if err != nil || strings.TrimSpace(dir) == "" {
		return &drafts{}
	}
	root := filepath.Join(dir, draftDirName)
	if deps.DraftsDir == "" {
		migrateOnce.Do(func() {
			if cache, err := config.CacheDir(); err == nil && strings.TrimSpace(cache) != "" {
				migrateDrafts(filepath.Join(cache, legacyDraftDirName), root)
			}
		})
	}
	return &drafts{root: root}
}

// legacyDraftDirName is where earlier builds kept comment drafts, under the
// cache directory a user is entitled to delete.
const legacyDraftDirName = "drafts"

var migrateOnce sync.Once

// migrateDrafts moves every draft under from into the same place under to. A
// draft already at the destination is newer than the one left behind, so it
// wins and the old one stays where it was rather than being lost.
func migrateDrafts(from, to string) {
	sites, err := os.ReadDir(from)
	if err != nil {
		return
	}
	for _, site := range sites {
		if !site.IsDir() {
			continue
		}
		files, err := os.ReadDir(filepath.Join(from, site.Name()))
		if err != nil {
			continue
		}
		for _, f := range files {
			if f.IsDir() || !strings.HasSuffix(f.Name(), ".md") {
				continue
			}
			moveDraft(filepath.Join(from, site.Name(), f.Name()), filepath.Join(to, site.Name(), f.Name()))
		}
		_ = os.Remove(filepath.Join(from, site.Name()))
	}
	_ = os.Remove(from)
}

func moveDraft(src, dst string) {
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

func (d *drafts) path(k draftKey) string {
	if d == nil || d.root == "" {
		return ""
	}
	return filepath.Join(d.root, safeName(k.site), k.file())
}

// read returns the draft kept for this key, and "" when there is none.
func (d *drafts) read(k draftKey) string {
	path := d.path(k)
	if path == "" {
		return ""
	}
	b, err := os.ReadFile(path) //nolint:gosec // the path is built from safeName segments under the drafts directory
	if err != nil {
		return ""
	}
	return string(b)
}

// write keeps the text, replacing whatever was there. The file is written
// beside its target and renamed over it, so a crash half way through leaves
// the previous draft rather than a truncated one.
func (d *drafts) write(k draftKey, text string) error {
	path := d.path(k)
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
	if _, err := tmp.WriteString(text); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(name, path)
}

// discard forgets a draft, which is what sending it does.
func (d *drafts) discard(k draftKey) {
	if path := d.path(k); path != "" {
		_ = os.Remove(path)
	}
}
