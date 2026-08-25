package jiratest

import (
	"context"
	"slices"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/varijkapil13/saral/pkg/jira"
)

// fakePeopleLimit is how many accounts a search hands back when the caller names
// no ceiling, which is what the cloud adapter does with the same input.
const fakePeopleLimit = 50

// FindPeople searches the fake site's directory.
//
// The matching rule here is **not** Jira's. Jira's is undocumented, is neither
// substring nor fuzzy, and was measured taking word prefixes, initials and part
// of an email address in one pass with no way to tell which fired. Nothing local
// reproduces it, so this one is written down instead: a needle matches when it
// begins a word of the display name, spells the initials, or appears in the
// email address.
//
// The order is deliberately meaningless — by account id, which is not relevance
// and not the alphabet — because Jira's order is not a ranking either, and a
// picker that presents whatever arrived as its own ranking has to look wrong
// somewhere. This is where.
func (f *Fake) FindPeople(ctx context.Context, q jira.PeopleQuery) ([]jira.User, error) {
	if err := f.fakeBegin(ctx, "FindPeople"); err != nil {
		return nil, err
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if err := f.caps.Require(jira.CapPeople); err != nil {
		return nil, err
	}

	assignable := false
	if project := strings.TrimSpace(q.Project); project != "" {
		if _, ok := f.projects[project]; !ok {
			return nil, fakeNotFound("project", project)
		}
		// The assignable endpoint answers only accounts that can be given work,
		// which is how a real site drops its app accounts without being asked.
		assignable = true
	}

	limit := q.Limit
	if limit <= 0 {
		limit = fakePeopleLimit
	}
	out := make([]jira.User, 0, min(limit, len(f.people)))
	for _, u := range f.people {
		if len(out) >= limit {
			break
		}
		if assignable && u.Kind != jira.AccountPerson {
			continue
		}
		if !fakePersonMatches(u, q.Match) {
			continue
		}
		out = append(out, u)
	}
	return out, nil
}

// People resolves account ids to accounts, leaving out the ids this site does
// not know rather than answering a blank row for one — which is what a real
// bulk read's JSON null decodes into if nothing drops it.
func (f *Fake) People(ctx context.Context, accountIDs []string) ([]jira.User, error) {
	if len(accountIDs) == 0 {
		// A real bulk read with no ids is a 400 rather than an empty answer, so
		// nothing asks for one. No call is recorded, because none is made.
		return nil, nil
	}
	if err := f.fakeBegin(ctx, "People"); err != nil {
		return nil, err
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if err := f.caps.Require(jira.CapPeople); err != nil {
		return nil, err
	}

	out := make([]jira.User, 0, len(accountIDs))
	seen := make(map[string]struct{}, len(accountIDs))
	for _, id := range accountIDs {
		wanted := strings.TrimSpace(id)
		if wanted == "" {
			continue
		}
		if _, dup := seen[wanted]; dup {
			continue
		}
		seen[wanted] = struct{}{}
		for _, u := range f.people {
			if u.AccountID == wanted {
				out = append(out, u)
				break
			}
		}
	}
	return out, nil
}

// fakePersonMatches is the rule described on FindPeople. An empty needle matches
// everybody, which is what an empty query does on a real site.
func fakePersonMatches(u jira.User, match string) bool {
	needle := strings.ToLower(strings.TrimSpace(match))
	if needle == "" {
		return true
	}
	if strings.Contains(strings.ToLower(u.Email), needle) {
		return true
	}
	name := strings.ToLower(u.DisplayName)
	var initials strings.Builder
	for _, word := range strings.FieldsFunc(name, unicode.IsSpace) {
		if strings.HasPrefix(word, needle) {
			return true
		}
		first, _ := utf8.DecodeRuneInString(word)
		initials.WriteRune(first)
	}
	return initials.Len() > 0 && strings.HasPrefix(initials.String(), needle)
}

// IssueTypeStatuses lists the fake project's issue types with the statuses each
// one's workflow reaches. The subtask type runs a different workflow from the
// rest, and one of its statuses shares a display name with a status of another
// id — which is what a team-managed project mints on a real site, and the reason
// this answer carries ids at all.
func (f *Fake) IssueTypeStatuses(ctx context.Context, projectKey string) ([]jira.IssueTypeStatuses, error) {
	if err := f.fakeBegin(ctx, "IssueTypeStatuses"); err != nil {
		return nil, err
	}
	f.mu.Lock()
	defer f.mu.Unlock()

	project := strings.TrimSpace(projectKey)
	if project == "" {
		return nil, fakeInvalid("projectKey",
			"a project is required: statuses are per project, and there is no site-wide answer to give")
	}
	if _, ok := f.projects[project]; !ok {
		return nil, fakeNotFound("project", project)
	}

	out := make([]jira.IssueTypeStatuses, 0, len(fakeIssueTypes))
	for _, typ := range fakeIssueTypes {
		statuses := fakeStatuses
		if typ.Subtask {
			statuses = fakeSubtaskStatuses
		}
		out = append(out, jira.IssueTypeStatuses{Type: typ, Statuses: slices.Clone(statuses)})
	}
	return out, nil
}

// Priorities lists the site's priorities in ranking order, which is the order
// they are declared in and not the alphabet.
func (f *Fake) Priorities(ctx context.Context) ([]jira.Priority, error) {
	if err := f.fakeBegin(ctx, "Priorities"); err != nil {
		return nil, err
	}
	return slices.Clone(fakePriorities), nil
}

// Labels pages the labels in use on the fake site: the seeded pool, whatever the
// stored issues carry, and two that are not ASCII — a label is whatever somebody
// typed, and a column measured with len() over one is wrong.
func (f *Fake) Labels(ctx context.Context) (jira.Page[string], error) {
	return jira.Offset(ctx, func(ctx context.Context, startAt int) ([]string, int, bool, error) {
		if err := f.fakeBegin(ctx, "Labels"); err != nil {
			return nil, -1, false, err
		}
		f.mu.Lock()
		defer f.mu.Unlock()

		all := f.fakeSiteLabels()
		if startAt > len(all) {
			startAt = len(all)
		}
		end := min(startAt+f.pageSize, len(all))
		return slices.Clone(all[startAt:end]), len(all), end >= len(all), nil
	})
}

// fakeSiteLabels is every label the site knows, sorted and deduplicated the way
// the endpoint answers them.
func (f *Fake) fakeSiteLabels() []string {
	seen := make(map[string]struct{}, len(fakeSiteLabelPool))
	out := make([]string, 0, len(fakeSiteLabelPool))
	add := func(label string) {
		if label == "" {
			return
		}
		if _, dup := seen[label]; dup {
			return
		}
		seen[label] = struct{}{}
		out = append(out, label)
	}
	for _, label := range fakeSiteLabelPool {
		add(label)
	}
	for _, key := range f.issueKeys {
		for _, label := range f.issues[key].Labels {
			add(label)
		}
	}
	slices.Sort(out)
	return out
}
