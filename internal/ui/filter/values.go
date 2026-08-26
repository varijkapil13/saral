package filter

import (
	"slices"
	"strings"

	"github.com/varijkapil13/saral/internal/app"
	"github.com/varijkapil13/saral/pkg/jira"
)

// notePenalty is what finding a value by its second column rather than by its
// name costs. It is app.Pattern's ranking step nine times over, which is the
// calibration the command palette already uses: a name match has to beat a
// prefix of something the row only mentions, and an email or a workflow's issue
// type is exactly that.
const notePenalty = 9 * 256

// The order a vocabulary is offered in before anything is typed. An app account
// is assigned work and reports issues exactly as a person does, so it is
// offered — but the measured site was ten robots and one human, which is
// unreadable unless the robots are last.
const (
	sinkEmpty    = -1
	sinkPerson   = 0
	sinkCustomer = 2
	sinkApp      = 4
	sinkInactive = 1
)

// value is one thing on offer in the picker's second state.
type value struct {
	term Term
	// note is the second column: what tells two values of one name apart, and
	// what is worth knowing about a row beyond what it is called. It is a
	// status's issue types, an account's email, or the badge that says an
	// account is not a person.
	note string
	// sink is where the value sits before anything is typed. It orders the
	// accounts, whose arrival order is the site's and is not a ranking.
	sink int
}

// match is the best of the two ways a value can be found. The name answers
// first, so that a person is never found only through an email nobody can see
// on the row.
func (v *value) match(p app.Pattern) (int, bool) {
	best, ok := p.Score(v.term.Label)
	if score, hit := p.Score(v.note); hit && (!ok || score-notePenalty > best) {
		best, ok = score-notePenalty, true
	}
	return best, ok
}

// ranked is one value's place in the order.
type ranked struct {
	at    int
	score int
}

// rank orders what is on offer against what has been typed. The pattern decides
// and the vocabulary's own order settles the ties, so Jira's order is never
// presented as a ranking and a site's priority order is never re-alphabetised.
//
// Both slices are reused so that a keystroke costs no allocation of its own.
func rank(all []value, p app.Pattern, shown []int, ranks []ranked) ([]int, []ranked) {
	for i := range all {
		score, ok := all[i].match(p)
		if !ok {
			continue
		}
		ranks = append(ranks, ranked{at: i, score: score})
	}
	slices.SortFunc(ranks, func(a, b ranked) int {
		if a.score != b.score {
			return b.score - a.score
		}
		return a.at - b.at
	})
	for _, r := range ranks {
		shown = append(shown, r.at)
	}
	return shown, ranks
}

// personValue is one account as the picker offers it, held by its account id.
func personValue(f Facet, u jira.User) value {
	return value{
		term: Term{Facet: f, ID: u.AccountID, Label: u.DisplayName},
		note: personNote(u),
		sink: personSink(u),
	}
}

func personNote(u jira.User) string {
	parts := make([]string, 0, 2)
	if kind := u.Kind.String(); kind != "" && u.Kind != jira.AccountPerson {
		parts = append(parts, kind)
	}
	if !u.Active {
		parts = append(parts, "inactive")
	}
	if len(parts) == 0 {
		return u.Email
	}
	return strings.Join(parts, ", ")
}

func personSink(u jira.User) int {
	sink := sinkPerson
	switch u.Kind {
	case jira.AccountApp:
		sink = sinkApp
	case jira.AccountCustomer:
		sink = sinkCustomer
	case jira.AccountPerson, jira.AccountUnknown:
	}
	if !u.Active {
		sink += sinkInactive
	}
	return sink
}

// unassignedValue is the row for an issue nobody is on. It is a value of the
// assignee facet like any other, held as the empty id, which is what makes it
// compose with the rest rather than needing a filter of its own.
func unassignedValue() value {
	return value{term: Term{Facet: FacetAssignee, Label: "unassigned"}, note: "nobody is on it", sink: sinkEmpty}
}

// sortPeople puts the accounts in the order they are offered in before anything
// is typed: people first, then the accounts that are not people, and inactive
// accounts below their own kind. The site's own order is arrival order and says
// nothing, so it cannot be the one on screen.
func sortPeople(all []value) {
	slices.SortFunc(all, func(a, b value) int {
		if a.sink != b.sink {
			return a.sink - b.sink
		}
		if c := strings.Compare(a.term.Label, b.term.Label); c != 0 {
			return c
		}
		return strings.Compare(a.term.ID, b.term.ID)
	})
}

// statusValues is the union of every issue type's workflow, keyed by status id.
// Two ids can answer to one display name — a team-managed project mints
// project-scoped statuses that reuse the stock names — so the row names the
// issue types the status belongs to, which is the only thing that tells them
// apart on screen.
func statusValues(in []jira.IssueTypeStatuses) []value {
	out := make([]value, 0, len(in)*4)
	at := make(map[string]int, len(in)*4)
	types := make([][]string, 0, len(in)*4)
	for _, its := range in {
		for _, s := range its.Statuses {
			i, seen := at[s.ID]
			if !seen {
				i = len(out)
				at[s.ID] = i
				out = append(out, value{term: Term{Facet: FacetStatus, ID: s.ID, Label: s.Name}})
				types = append(types, nil)
			}
			types[i] = append(types[i], its.Type.Name)
		}
	}
	for i := range out {
		out[i].note = strings.Join(types[i], ", ")
	}
	return out
}

// typeValues is the project's issue types, in the order the site listed them.
func typeValues(in []jira.IssueTypeStatuses) []value {
	out := make([]value, 0, len(in))
	seen := make(map[string]bool, len(in))
	for _, its := range in {
		if its.Type.ID == "" || seen[its.Type.ID] {
			continue
		}
		seen[its.Type.ID] = true
		note := ""
		if its.Type.Subtask {
			note = "subtask"
		}
		out = append(out, value{term: Term{Facet: FacetType, ID: its.Type.ID, Label: its.Type.Name}, note: note})
	}
	return out
}

// priorityValues keeps the site's own order, which is the order they rank in
// and is not alphabetical.
func priorityValues(in []jira.Priority) []value {
	out := make([]value, 0, len(in))
	for _, p := range in {
		out = append(out, value{term: Term{Facet: FacetPriority, ID: p.ID, Label: p.Name}})
	}
	return out
}

// labelValues holds the label as its own id: a label is the string somebody
// typed, and the site keeps no other name for it.
func labelValues(in []string) []value {
	out := make([]value, 0, len(in))
	for _, label := range in {
		if label == "" {
			continue
		}
		out = append(out, value{term: Term{Facet: FacetLabel, ID: label, Label: label}})
	}
	return out
}
