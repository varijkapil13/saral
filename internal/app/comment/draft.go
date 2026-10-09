package comment

import (
	"path/filepath"
	"strings"
	"sync"

	appdraft "github.com/varijkapil13/saral/internal/app/draft"
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

func safeName(s string) string { return appdraft.SafeName(s, "") }

// Drafts is where unsent comments are kept between sessions.
type Drafts struct {
	store appdraft.Store
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
		migrateOnce.Do(func() { appdraft.Migrate(filepath.Join(legacy, legacyDraftDirName), root, ".md") })
	}
	return &Drafts{store: appdraft.Open(root)}
}

// legacyDraftDirName is where earlier builds kept comment drafts, under the
// cache directory a user is entitled to delete.
const legacyDraftDirName = "drafts"

var migrateOnce sync.Once

func (d *Drafts) path(k DraftKey) string {
	if d == nil {
		return ""
	}
	return d.store.Path(safeName(k.Site), k.file())
}

func (d *Drafts) read(path string) string {
	if d == nil {
		return ""
	}
	b, _, _ := d.store.Read(path)
	return string(b)
}

// Read returns the draft kept for this key, and "" when there is none.
func (d *Drafts) Read(k DraftKey) string { return d.read(d.path(k)) }

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
func (d *Drafts) ReadBase(k DraftKey) string { return strings.TrimSpace(d.read(d.basePath(k))) }

// Write keeps the text, replacing whatever was there.
func (d *Drafts) Write(k DraftKey, text, base string) error {
	if d == nil {
		return nil
	}
	if err := d.store.Write(d.path(k), []byte(text)); err != nil {
		return err
	}
	if base == "" {
		_ = d.store.Remove(d.basePath(k))
		return nil
	}
	return d.store.Write(d.basePath(k), []byte(base))
}

// Keep writes the text, or forgets the draft when nothing but whitespace is left.
func (d *Drafts) Keep(k DraftKey, text, base string) error {
	if strings.TrimSpace(text) == "" {
		d.Discard(k)
		return nil
	}
	return d.Write(k, text, base)
}

// Discard forgets a draft, which is what sending it does.
func (d *Drafts) Discard(k DraftKey) {
	if path := d.path(k); path != "" {
		_ = d.store.Remove(path)
		_ = d.store.Remove(d.basePath(k))
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
