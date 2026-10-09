package connect

import (
	"context"
	"slices"
	"strings"

	appquery "github.com/varijkapil13/saral/internal/app/query"
)

// PinnableField is one field a profile can pin: the id pinning writes and the
// name to draw.
type PinnableField struct{ ID, Label string }

// PinnableFields reads the site's field catalogue and keeps every field with an
// id, named the way this site spells it, and sorted by that name — the only
// order there is before anything is pinned.
func PinnableFields(ctx context.Context, search *appquery.Search) ([]PinnableField, error) {
	fields, err := search.Fields(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]PinnableField, 0, len(fields))
	for i := range fields {
		f := &fields[i]
		if f.ID == "" {
			continue
		}
		label := f.Name
		if strings.TrimSpace(label) == "" {
			label = f.ID
		}
		out = append(out, PinnableField{ID: f.ID, Label: label})
	}
	slices.SortFunc(out, func(a, b PinnableField) int { return strings.Compare(a.Label, b.Label) })
	return out, nil
}
