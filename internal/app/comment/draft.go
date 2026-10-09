package comment

import (
	"os"
	"path/filepath"
	"strings"
	"sync"

	"github.com/varijkapil13/saral/pkg/adf"
)

// draftDirName is where unsent comments live under the drafts directory. They
// are the user's own words rather than anything fetched, so nothing in the
// program deletes one except sending it or clearing it by hand.
const draftDirName = "comments"

// DraftKey names one draft: a new comment on an issue when Comment is empty,
// or an edit of one comment. The two are separate drafts, because abandoning an
// edit must not take the half-written new comment beside it with it.
type DraftKey struct {
	Site    string
	Issue   string
	Comment string
}

func (k DraftKey) file() string {
	name := k.Comment
	if name == "" {
		name = "new"
	}
	return safeName(k.Issue) + "." + safeName(name) + ".md"
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

// Drafts is where unsent text is kept between sessions. docs/UX.md principle 6
// asks that anything typed survive a failed request, a conflict and a crash,
// which means on disk rather than in the model.
type Drafts struct {
	root string
}

// OpenDrafts keeps comment drafts under dir. A session with nowhere to write
// gets a store that keeps nothing rather than a failure, because a comment
// nobody can save is still worth typing. legacy is the cache directory earlier
// builds kept drafts under, moved out once per process; "" moves nothing.
func OpenDrafts(dir, legacy string) *Drafts {
	if strings.TrimSpace(dir) == "" {
		return &Drafts{}
	}
	root := filepath.Join(dir, draftDirName)
	if strings.TrimSpace(legacy) != "" {
		migrateOnce.Do(func() { migrateDrafts(filepath.Join(legacy, legacyDraftDirName), root) })
	}
	return &Drafts{root: root}
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

func (d *Drafts) path(k DraftKey) string {
	if d == nil || d.root == "" {
		return ""
	}
	return filepath.Join(d.root, safeName(k.Site), k.file())
}

// Read returns the draft kept for this key, and "" when there is none.
func (d *Drafts) Read(k DraftKey) string {
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

// basePath is where the fingerprint of the body an edit's draft was written
// against is kept, beside the draft.
func (d *Drafts) basePath(k DraftKey) string {
	path := d.path(k)
	if path == "" {
		return ""
	}
	return strings.TrimSuffix(path, ".md") + ".base"
}

// ReadBase is the fingerprint kept with a draft, "" when none was.
func (d *Drafts) ReadBase(k DraftKey) string {
	path := d.basePath(k)
	if path == "" {
		return ""
	}
	b, err := os.ReadFile(path) //nolint:gosec // the path is built from safeName segments under the drafts directory
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(b))
}

// Write keeps the text, replacing whatever was there. The file is written
// beside its target and renamed over it, so a crash half way through leaves
// the previous draft rather than a truncated one.
func (d *Drafts) Write(k DraftKey, text, base string) error {
	if err := d.writeFile(d.path(k), text); err != nil {
		return err
	}
	if base == "" {
		if path := d.basePath(k); path != "" {
			_ = os.Remove(path)
		}
		return nil
	}
	return d.writeFile(d.basePath(k), base)
}

// Keep writes the text, or forgets the draft when nothing but whitespace is left.
func (d *Drafts) Keep(k DraftKey, text, base string) error {
	if strings.TrimSpace(text) == "" {
		d.Discard(k)
		return nil
	}
	return d.Write(k, text, base)
}

func (d *Drafts) writeFile(path, text string) error {
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

// Discard forgets a draft, which is what sending it does.
func (d *Drafts) Discard(k DraftKey) {
	if path := d.path(k); path != "" {
		_ = os.Remove(path)
		_ = os.Remove(d.basePath(k))
	}
}

// Opening is what an editor starts with for one key.
type Opening struct {
	// Text is the draft left for this key, or the body rendered for editing.
	Text string
	// Base is the fingerprint to keep with the draft from here on.
	Base string
	// Restored is a draft that differs from what the site holds.
	Restored bool
	// Stale is a restored edit whose base the site has since moved, which
	// sends only once the author has been told.
	Stale bool
}

// Open seeds an editor for k: a new comment when k.Comment is empty and body is
// the zero document, or an edit of the comment whose body is given.
func (d *Drafts) Open(k DraftKey, body adf.Doc) Opening {
	seeded := Markdown(body)
	restored := d.Read(k)
	o := Opening{Text: seeded}
	if k.Comment != "" {
		o.Base = Fingerprint(body)
	}
	if restored == "" {
		return o
	}
	o.Text = restored
	o.Restored = restored != seeded
	if k.Comment != "" && o.Restored {
		current := o.Base
		o.Base = d.ReadBase(k)
		o.Stale = o.Base != current
	}
	return o
}
