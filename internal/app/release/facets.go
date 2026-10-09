package release

import (
	"strings"

	"github.com/varijkapil13/saral/pkg/jira"
)

// State is which of four states a version is in. It is derived from the port's
// own booleans and dates and never from anything the site can rename. The
// order is the state sort's: what still needs doing first.
type State uint8

// The four states.
const (
	Overdue State = iota
	Unreleased
	Released
	Archived
)

// StateOf is which state a version is in on the reader's date today. Archived
// beats released because an archived version is out of the way whatever else
// is true of it, and overdue is a release date in the past on something
// unreleased.
func StateOf(v jira.Version, today jira.Date) State {
	switch {
	case v.Archived:
		return Archived
	case v.Released:
		return Released
	case !v.ReleaseDate.IsZero() && !today.IsZero() && v.ReleaseDate.Before(today):
		return Overdue
	default:
		return Unreleased
	}
}

// CompareStates puts what still needs doing first: an overdue version, then
// one still to ship, then the shipped ones and last whatever has been put away.
func CompareStates(a, b State) int { return int(a) - int(b) }

// CompareNames orders two names regardless of case.
func CompareNames(a, b string) int {
	return strings.Compare(strings.ToLower(a), strings.ToLower(b))
}

// CompareDated orders two dates, putting an undated one last whichever way
// the order runs: an undated version has not been planned, and reversing the
// list should not bring the unplanned ones to the top.
func CompareDated(a, b jira.Date, desc bool) int {
	switch {
	case a.IsZero() && b.IsZero():
		return 0
	case a.IsZero():
		return 1
	case b.IsZero():
		return -1
	}
	c := 0
	switch {
	case a.Before(b):
		c = -1
	case b.Before(a):
		c = 1
	}
	if desc {
		return -c
	}
	return c
}

// Filter narrows a list to one kind of version. It is local: the site is read
// unfiltered because the same list feeds the flow's choice of where open work
// can move.
type Filter uint8

// The filters, in the order they cycle.
const (
	FilterAll Filter = iota
	FilterUnreleased
	FilterReleased
	FilterArchived
	FilterCount
)

// filterNames are words rather than numbers so that a reordered enum reads a
// remembered value back as what it was.
var filterNames = [FilterCount]string{"all", "unreleased", "released", "archived"}

// Name is the filter's word, which is what a remembered filter is kept as.
func (f Filter) Name() string { return filterNames[f] }

// Next is the filter after this one, wrapping round to all.
func (f Filter) Next() Filter { return (f + 1) % FilterCount }

// Step is the filter dir places along the cycle.
func (f Filter) Step(dir int) Filter {
	return (f + Filter(int(FilterCount)+dir)) % FilterCount
}

// Keeps is whether a version in state belongs under this filter. An overdue
// version is still unreleased, and an archived one is only ever archived,
// whatever else is true of it.
func (f Filter) Keeps(state State) bool {
	switch f {
	case FilterUnreleased:
		return state == Unreleased || state == Overdue
	case FilterReleased:
		return state == Released
	case FilterArchived:
		return state == Archived
	default:
		return true
	}
}

// FilterNamed is the filter a remembered word names, or all for a word this
// build does not know.
func FilterNamed(name string) (Filter, bool) {
	for f := range FilterCount {
		if filterNames[f] == name {
			return f, true
		}
	}
	return FilterAll, false
}

// Fold is the text a version is found by: its name and, separated so that a
// needle cannot match across the two, the names of the groups it is in.
func Fold(name string, groups ...string) string {
	if len(groups) == 0 {
		return strings.ToLower(name)
	}
	return strings.ToLower(name + "\x00" + strings.Join(groups, ""))
}

// Needle is what typed text is matched as.
func Needle(typed string) string { return strings.ToLower(strings.TrimSpace(typed)) }

// Found is whether a folded version holds needle. An empty needle finds every one.
func Found(fold, needle string) bool { return needle == "" || strings.Contains(fold, needle) }
