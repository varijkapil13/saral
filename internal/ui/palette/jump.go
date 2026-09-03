package palette

import (
	"fmt"
	"strings"

	tea "charm.land/bubbletea/v2"

	"github.com/varijkapil13/saral/internal/app"
	"github.com/varijkapil13/saral/internal/config"
	"github.com/varijkapil13/saral/internal/ui/kernel"
)

// jumpLabel marks a hit this session has not read, so it never reads like a
// cached issue whose age happened to come back blank.
const jumpLabel = "not cached — opens with a fetch"

// jumpHit resolves what was typed as an issue key or a Jira URL — the two
// shapes docs/UX.md promises reach an issue with no round trip — using the
// same two parsers K5 gave the CLI argument. It is what g then k answers to:
// opening the palette already armed with what gets typed, rather than a
// fourth place that reads a key, and it is why a key search does not need to
// already be cached to be offered here.
//
// A URL for another site is named as the mistake it is, in the same words the
// CLI argument answers with, rather than read against this profile's site.
func (m *Model) jumpHit(text string) (h *hit, warn tea.Cmd) {
	if key, ok := app.ParseKey(text); ok {
		return &hit{key: key, text: key + "  " + jumpLabel}, nil
	}
	key, host, ok := app.ParseIssueURL(text)
	if !ok {
		return nil, nil
	}
	if here, err := config.NormalizeSite(m.deps.Site); err == nil && !strings.EqualFold(here, host) {
		return nil, kernel.Warn(fmt.Sprintf("%s is on %s and this profile is on %s, so it was not opened", key, host, here))
	}
	return &hit{key: key, text: key + "  " + jumpLabel}, nil
}

// alreadyFound reports whether a cache hit already answers this key, so a
// valid key already ranked by the index is not offered a second time under a
// different row.
func (m *Model) alreadyFound(key string) bool {
	for i := range m.hits {
		if strings.EqualFold(m.hits[i].key, key) {
			return true
		}
	}
	return false
}
