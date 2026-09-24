package filterbar

import (
	tea "charm.land/bubbletea/v2"

	"github.com/varijkapil13/saral/internal/ui/filter"
	"github.com/varijkapil13/saral/internal/ui/kernel"
)

// memoryKey is per project: a status or type id means nothing on another.
func memoryKey(project string) string { return "terms:" + project }

// Recall reads the terms kept for the project d is scoped to.
func Recall(d kernel.Deps, view string) (filter.Terms, bool) {
	enc, ok := kernel.Recall(d, view, memoryKey(d.Project))
	if !ok {
		return nil, false
	}
	return filter.DecodeTerms(enc)
}

// Keep keeps terms for the project d is scoped to; an empty set clears them.
func Keep(d kernel.Deps, view string, terms filter.Terms) {
	kernel.Keep(d, view, memoryKey(d.Project), terms.Encode())
}

// Reproject takes held terms off on a switch from project was, says so, and
// puts on whatever was kept for the project d is now scoped to.
func Reproject(d kernel.Deps, view, was string, held filter.Terms) (filter.Terms, tea.Cmd) {
	next, _ := Recall(d, view)
	if len(held) == 0 {
		return next, nil
	}
	said := "the filters were about " + was + ", so they came off with it"
	if len(next) > 0 {
		said += "; the ones last used on " + d.Project + " are back"
	}
	return next, kernel.Status(said)
}
