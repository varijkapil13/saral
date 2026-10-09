package search

import (
	"strings"

	appissueref "github.com/varijkapil13/saral/internal/app/issueref"
)

// Found is what one filter keystroke reaches among issues: the ranked cache hits
// and, when the text is an issue key or a Jira URL, the key to jump to.
type Found struct {
	Hits []Hit
	// Jump is a key to offer first, and "" when there is none or a hit already answers it.
	Jump string
	// Foreign is set when the text is a URL for a site other than the profile's.
	Foreign *ForeignSite
}

// ForeignSite names a pasted issue URL that belongs to another site than the profile's.
type ForeignSite struct {
	Key  string
	Host string
	Here string
}

// Find ranks cached issues against text and reads text as an issue key or URL.
// here is the profile's normalised site; hereOK is false when it could not be
// normalised, which skips the site check. Hits come back with the error, as the
// index returns them.
func Find(ix *Index, text string, limit int, here string, hereOK bool) (Found, error) {
	hits, err := ix.Search(text, limit)
	out := Found{Hits: hits}
	key, foreign := findJump(text, here, hereOK)
	out.Foreign = foreign
	if key != "" && !findHas(hits, key) {
		out.Jump = key
	}
	return out, err
}

func findJump(text, here string, hereOK bool) (string, *ForeignSite) {
	if key, ok := appissueref.ParseKey(text); ok {
		return key, nil
	}
	key, host, ok := appissueref.ParseIssueURL(text)
	if !ok {
		return "", nil
	}
	if hereOK && !strings.EqualFold(here, host) {
		return "", &ForeignSite{Key: key, Host: host, Here: here}
	}
	return key, nil
}

func findHas(hits []Hit, key string) bool {
	for i := range hits {
		if strings.EqualFold(hits[i].Key, key) {
			return true
		}
	}
	return false
}
