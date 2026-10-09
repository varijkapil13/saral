package issue

import (
	"fmt"

	appissue "github.com/varijkapil13/saral/internal/app/issue"
	"github.com/varijkapil13/saral/internal/ui/kernel"
)

// newDraftStore locates the draft directory beside the profile rather than in
// the cache directory: a cache is something a user is entitled to delete, and
// the one thing in here is text nobody else has a copy of.
func newDraftStore(d kernel.Deps) (appissue.Drafts, error) {
	dir, err := d.DraftRoot()
	if err != nil {
		return appissue.Drafts{}, fmt.Errorf("locating the drafts directory: %w", err)
	}
	return appissue.NewDrafts(dir), nil
}
