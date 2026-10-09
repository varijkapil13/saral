package search

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"

	appquery "github.com/varijkapil13/saral/internal/app/query"
)

// MaxSavedSlot is the highest number key a saved query can be bound to.
const MaxSavedSlot = 9

// SavedQuery is a query a user keeps, optionally bound to a number key.
type SavedQuery struct {
	Name string
	JQL  string
	// Slot is the number key that runs this query, 1 to MaxSavedSlot, or zero
	// when it is only reachable by name.
	Slot int
	// Projection is the field set to fetch for it. One that asks for nothing
	// means the list projection, which is what a saved query is opened into.
	Projection appquery.Projection
}

func (q SavedQuery) projection() appquery.Projection {
	if len(q.Projection.IDs) == 0 && len(q.Projection.Names) == 0 && !q.Projection.Custom {
		return appquery.ListProjection()
	}
	return q.Projection
}

// SavedQueries is an ordered set of saved queries.
//
// It is immutable the way jira.FieldSet is: Add and Remove return a new set. A
// set travels by value into the search use case and out to whatever renders the
// list of them, and a shared slice behind a value type would mean that binding
// a key in one place rebinds it everywhere.
type SavedQueries struct {
	items []SavedQuery
}

// NewSavedQueries builds a set, adding the queries in order.
func NewSavedQueries(in ...SavedQuery) (SavedQueries, error) {
	var out SavedQueries
	for _, q := range in {
		var err error
		if out, err = out.Add(q); err != nil {
			return SavedQueries{}, err
		}
	}
	return out, nil
}

// Add returns a copy of the set carrying one more query.
//
// A query whose name is already in the set replaces it and keeps its position.
// A slot already bound elsewhere moves to the new query rather than being
// refused: binding a key is a user taking it, and answering "that key is taken"
// would only make them go and unbind the old one first.
func (q SavedQueries) Add(in SavedQuery) (SavedQueries, error) {
	in.Name = strings.TrimSpace(in.Name)
	in.JQL = strings.TrimSpace(in.JQL)
	switch {
	case in.Name == "":
		return q, errors.New("app: a saved query needs a name")
	case in.JQL == "":
		return q, fmt.Errorf("app: the saved query %q has no JQL to run", in.Name)
	case in.Slot < 0 || in.Slot > MaxSavedSlot:
		return q, fmt.Errorf("app: the saved query %q asks for key %d; the keys are 1 to %d, or 0 for none", in.Name, in.Slot, MaxSavedSlot)
	}

	items := slices.Clone(q.items)
	for i := range items {
		if in.Slot > 0 && items[i].Slot == in.Slot {
			items[i].Slot = 0
		}
	}
	if at := q.indexOf(in.Name); at >= 0 {
		items[at] = in
		return SavedQueries{items: items}, nil
	}
	return SavedQueries{items: append(items, in)}, nil
}

// Remove returns a copy of the set without the named query.
func (q SavedQueries) Remove(name string) SavedQueries {
	at := q.indexOf(name)
	if at < 0 {
		return q
	}
	return SavedQueries{items: slices.Delete(slices.Clone(q.items), at, at+1)}
}

// All returns the queries in the order they were added.
func (q SavedQueries) All() []SavedQuery { return slices.Clone(q.items) }

// Len reports how many queries are saved.
func (q SavedQueries) Len() int { return len(q.items) }

// ByName finds a query by name, case-insensitively.
func (q SavedQueries) ByName(name string) (SavedQuery, bool) {
	if at := q.indexOf(name); at >= 0 {
		return q.items[at], true
	}
	return SavedQuery{}, false
}

// Slots lists the number keys that have a query bound, in ascending order. It
// is what a footer showing only the keys that work right now is built from.
func (q SavedQueries) Slots() []int {
	out := make([]int, 0, len(q.items))
	for _, item := range q.items {
		if item.Slot > 0 {
			out = append(out, item.Slot)
		}
	}
	slices.Sort(out)
	return out
}

// BySlot finds the query bound to a number key.
func (q SavedQueries) BySlot(slot int) (SavedQuery, bool) {
	if slot <= 0 {
		return SavedQuery{}, false
	}
	for _, item := range q.items {
		if item.Slot == slot {
			return item, true
		}
	}
	return SavedQuery{}, false
}

func (q SavedQueries) indexOf(name string) int {
	wanted := strings.TrimSpace(name)
	for i := range q.items {
		if strings.EqualFold(q.items[i].Name, wanted) {
			return i
		}
	}
	return -1
}

// RunSaved runs a saved query by name.
func RunSaved(ctx context.Context, s *appquery.Search, saved SavedQueries, name string) (appquery.Result, error) {
	query, ok := saved.ByName(name)
	if !ok {
		return appquery.Result{}, fmt.Errorf("app: there is no saved query called %q", name)
	}
	return s.Run(ctx, appquery.Request{JQL: query.JQL, Projection: query.projection()})
}
