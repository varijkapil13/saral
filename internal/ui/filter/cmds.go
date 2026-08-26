package filter

import (
	"context"

	tea "charm.land/bubbletea/v2"

	"github.com/varijkapil13/saral/pkg/jira"
)

// vocabularyMsg carries the values of a facet the site answers in one read.
type vocabularyMsg struct {
	gen    int
	facet  Facet
	values []value
}

// peopleMsg carries the accounts a search brought back. The needle travels with
// them so that one is never asked for twice, and complete says the site had
// fewer accounts than it was allowed to send — which is what makes typing
// answerable from what is already held.
type peopleMsg struct {
	gen      int
	facet    Facet
	needle   string
	people   []jira.User
	complete bool
}

// failedMsg is a read that brought nothing back. The error travels whole so
// that a refusal reaches the user in the words the site used, and the needle
// travels with it so that a question this site never answered can be asked
// again rather than being remembered as asked.
type failedMsg struct {
	gen    int
	facet  Facet
	needle string
	err    error
}

// findPeople searches the site's accounts, and then draws back any account
// already in force that the search did not answer with.
//
// Jira's matching is undocumented and is neither substring nor fuzzy, so the
// answer is what the site found and never the order it is offered in.
//
// The second read is not belt and braces. The assignable search drops the app
// accounts, and a directory can be longer than one search may hand back, so an
// account already being filtered by can be missing from what came back — and a
// value in force that is not on the list is a filter that cannot be taken off
// here. It is only made for the ids the search itself did not cover.
func findPeople(ctx context.Context, finder jira.PeopleFinder, f Facet, q jira.PeopleQuery, inForce []string, gen int) tea.Cmd {
	return func() tea.Msg {
		people, err := finder.FindPeople(ctx, q)
		if err != nil {
			return failedMsg{gen: gen, facet: f, needle: q.Match, err: err}
		}
		complete := len(people) < q.Limit
		if missing := absent(inForce, people); len(missing) > 0 {
			also, err := finder.People(ctx, missing)
			if err != nil {
				return failedMsg{gen: gen, facet: f, needle: q.Match, err: err}
			}
			people = append(people, also...)
		}
		return peopleMsg{gen: gen, facet: f, needle: q.Match, people: people, complete: complete}
	}
}

// absent is the account ids the search did not answer with. An id this site
// does not know comes back from the bulk read as nothing at all rather than as
// a blank row, so asking for one costs an absence and never a wrong name.
func absent(want []string, got []jira.User) []string {
	if len(want) == 0 {
		return nil
	}
	have := make(map[string]bool, len(got))
	for i := range got {
		have[got[i].AccountID] = true
	}
	out := make([]string, 0, len(want))
	for _, id := range want {
		if id != "" && !have[id] {
			out = append(out, id)
		}
	}
	return out
}

// vocabulary reads the values of a facet that is not a person.
func vocabulary(ctx context.Context, vocab jira.FilterVocabulary, f Facet, project string, gen int) tea.Cmd {
	return func() tea.Msg {
		values, err := readVocabulary(ctx, vocab, f, project)
		if err != nil {
			return failedMsg{gen: gen, facet: f, err: err}
		}
		return vocabularyMsg{gen: gen, facet: f, values: values}
	}
}

func readVocabulary(ctx context.Context, vocab jira.FilterVocabulary, f Facet, project string) ([]value, error) {
	switch f {
	case FacetStatus, FacetType:
		byType, err := vocab.IssueTypeStatuses(ctx, project)
		if err != nil {
			return nil, err
		}
		if f == FacetType {
			return typeValues(byType), nil
		}
		return statusValues(byType), nil
	case FacetPriority:
		priorities, err := vocab.Priorities(ctx)
		if err != nil {
			return nil, err
		}
		return priorityValues(priorities), nil
	case FacetLabel:
		labels, err := walkLabels(ctx, vocab)
		if err != nil {
			return nil, err
		}
		return labelValues(labels), nil
	case FacetNone, FacetAssignee, FacetReporter:
	}
	return nil, nil
}

// walkLabels reads as many labels as the picker will offer. The endpoint takes
// no query and ignores one sent anyway, so narrowing them means walking them,
// and the walk is bounded because a busy site has more labels than anybody
// scrolls past.
func walkLabels(ctx context.Context, vocab jira.FilterVocabulary) ([]string, error) {
	page, err := vocab.Labels(ctx)
	if err != nil {
		return nil, err
	}
	out := append([]string(nil), page.Items...)
	for page.HasMore() && len(out) < maxLabels {
		page, err = page.Next(ctx)
		if err != nil {
			return nil, err
		}
		out = append(out, page.Items...)
	}
	if len(out) > maxLabels {
		out = out[:maxLabels]
	}
	return out, nil
}
