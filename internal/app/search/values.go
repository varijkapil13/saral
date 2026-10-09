package search

import (
	"context"
	"slices"
	"strings"

	appmatch "github.com/varijkapil13/saral/internal/app/match"
	appterm "github.com/varijkapil13/saral/internal/app/term"
	"github.com/varijkapil13/saral/pkg/jira"
)

// notePenalty is what finding a value by its second column rather than by its
// name costs. It is match.Pattern's ranking step nine times over, which is the
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

// Value is one thing on offer in the filter picker's second state.
type Value struct {
	Term appterm.Term
	// Note is the second column: what tells two values of one name apart, and
	// what is worth knowing about a row beyond what it is called. It is a
	// status's issue types, an account's email, or the badge that says an
	// account is not a person.
	Note string
	// Sink is where the value sits before anything is typed. It orders the
	// accounts, whose arrival order is the site's and is not a ranking.
	Sink int
}

// Match is the best of the two ways a value can be found. The name answers
// first, so that a person is never found only through an email nobody can see
// on the row.
func (v *Value) Match(p appmatch.Pattern) (int, bool) {
	best, ok := p.Score(v.Term.Label)
	if score, hit := p.Score(v.Note); hit && (!ok || score-notePenalty > best) {
		best, ok = score-notePenalty, true
	}
	return best, ok
}

// ValueRank is one value's place in the order.
type ValueRank struct {
	At    int
	Score int
}

// RankValues orders what is on offer against what has been typed. The pattern decides
// and the vocabulary's own order settles the ties, so Jira's order is never
// presented as a ranking and a site's priority order is never re-alphabetised.
//
// Both slices are reused so that a keystroke costs no allocation of its own.
func RankValues(all []Value, p appmatch.Pattern, shown []int, ranks []ValueRank) ([]int, []ValueRank) {
	for i := range all {
		score, ok := all[i].Match(p)
		if !ok {
			continue
		}
		ranks = append(ranks, ValueRank{At: i, Score: score})
	}
	slices.SortFunc(ranks, func(a, b ValueRank) int {
		if a.Score != b.Score {
			return b.Score - a.Score
		}
		return a.At - b.At
	})
	for _, r := range ranks {
		shown = append(shown, r.At)
	}
	return shown, ranks
}

// PersonValue is one account as the picker offers it, held by its account id.
func PersonValue(f appterm.Facet, u jira.User) Value {
	return Value{
		Term: appterm.Term{Facet: f, ID: u.AccountID, Label: u.DisplayName},
		Note: personNote(u),
		Sink: personSink(u),
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

// UnassignedValue is the row for an issue nobody is on. It is a value of the
// assignee facet like any other, held as the empty id, which is what makes it
// compose with the rest rather than needing a filter of its own.
func UnassignedValue() Value {
	return Value{Term: appterm.Term{Facet: appterm.FacetAssignee, Label: "unassigned"}, Note: "nobody is on it", Sink: sinkEmpty}
}

// SortPeople puts the accounts in the order they are offered in before anything
// is typed: people first, then the accounts that are not people, and inactive
// accounts below their own kind. The site's own order is arrival order and says
// nothing, so it cannot be the one on screen.
func SortPeople(all []Value) {
	slices.SortFunc(all, func(a, b Value) int {
		if a.Sink != b.Sink {
			return a.Sink - b.Sink
		}
		if c := strings.Compare(a.Term.Label, b.Term.Label); c != 0 {
			return c
		}
		return strings.Compare(a.Term.ID, b.Term.ID)
	})
}

// StatusValues is the union of every issue type's workflow, keyed by status id.
// Two ids can answer to one display name — a team-managed project mints
// project-scoped statuses that reuse the stock names — so the row names the
// issue types the status belongs to, which is the only thing that tells them
// apart on screen.
func StatusValues(in []jira.IssueTypeStatuses) []Value {
	out := make([]Value, 0, len(in)*4)
	at := make(map[string]int, len(in)*4)
	types := make([][]string, 0, len(in)*4)
	for _, its := range in {
		for _, s := range its.Statuses {
			i, seen := at[s.ID]
			if !seen {
				i = len(out)
				at[s.ID] = i
				out = append(out, Value{Term: appterm.Term{Facet: appterm.FacetStatus, ID: s.ID, Label: s.Name}})
				types = append(types, nil)
			}
			types[i] = append(types[i], its.Type.Name)
		}
	}
	for i := range out {
		out[i].Note = strings.Join(types[i], ", ")
	}
	return out
}

// TypeValues is the project's issue types, in the order the site listed them.
func TypeValues(in []jira.IssueTypeStatuses) []Value {
	out := make([]Value, 0, len(in))
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
		out = append(out, Value{Term: appterm.Term{Facet: appterm.FacetType, ID: its.Type.ID, Label: its.Type.Name}, Note: note})
	}
	return out
}

// PriorityValues keeps the site's own order, which is the order they rank in
// and is not alphabetical.
func PriorityValues(in []jira.Priority) []Value {
	out := make([]Value, 0, len(in))
	for _, p := range in {
		out = append(out, Value{Term: appterm.Term{Facet: appterm.FacetPriority, ID: p.ID, Label: p.Name}})
	}
	return out
}

// LabelValues holds the label as its own id: a label is the string somebody
// typed, and the site keeps no other name for it.
func LabelValues(in []string) []Value {
	out := make([]Value, 0, len(in))
	for _, label := range in {
		if label == "" {
			continue
		}
		out = append(out, Value{Term: appterm.Term{Facet: appterm.FacetLabel, ID: label, Label: label}})
	}
	return out
}

// PeopleLimit is how many accounts one search asks for. It is a ceiling and not
// a page size: the port does not page people, because a person is found by
// typing more rather than by paging.
const PeopleLimit = 50

// ThinAnswer is how few candidates the held accounts have to leave before a
// keystroke is worth a round trip. Jira's matching is neither substring nor
// fuzzy and is documented nowhere, so a needle can find an account that nothing
// local would have — but asking on every keystroke is slower than typing the
// JQL this picker exists to replace, so the site is asked when what is held
// stops answering rather than when a key is pressed.
const ThinAnswer = 8

// MaxLabels bounds the walk over a site's labels. The endpoint takes no query
// and ignores one sent anyway, so the only way to narrow them is to walk them,
// and a busy site has more than anybody scrolls.
const MaxLabels = 2000

// ValueRefusal is why a session cannot offer a facet's values.
type ValueRefusal uint8

// The reasons a facet cannot be offered.
const (
	ValueOffered ValueRefusal = iota
	ValueNoConnection
	ValueNoPeople
	ValueNoProject
)

// OfferFacet says whether this session can answer for a facet's values. When
// the kind is ValueNoPeople the reason is the site's own words for it,
// which may be empty.
func OfferFacet(f appterm.Facet, connected bool, caps jira.Capabilities, project string) (kind ValueRefusal, reason string) {
	if !connected {
		return ValueNoConnection, ""
	}
	if f.People() && !caps.Allows(jira.CapPeople) {
		return ValueNoPeople, caps.Capability(jira.CapPeople).Reason
	}
	if (f == appterm.FacetStatus || f == appterm.FacetType) && strings.TrimSpace(project) == "" {
		return ValueNoProject, ""
	}
	return ValueOffered, ""
}

// LookedUpByTyping reports that a facet's values are found by asking the site
// as the needle changes, which is true of accounts and of nothing else.
func LookedUpByTyping(f appterm.Facet) bool { return f.People() }

// AskSiteFor reports whether a needle is worth a round trip: only for accounts,
// only when it says something, and only once what is held has run thin.
func AskSiteFor(f appterm.Facet, needle string, complete, asked bool, shown int) bool {
	switch {
	case !f.People(), needle == "", complete, asked:
		return false
	}
	return shown < ThinAnswer
}

// PeopleProject is the project an account search is narrowed to. Setting it
// switches Jira to the accounts that can be given work in a project, which
// drops the app accounts for free — ten of the eleven on the measured site.
// A reporter need not be assignable, though: an account that reported an issue
// and then lost the permission is still on those rows, so that search is the
// site-wide one and the app accounts it brings are badged and sunk instead.
func PeopleProject(f appterm.Facet, project string) string {
	if f == appterm.FacetAssignee {
		return strings.TrimSpace(project)
	}
	return ""
}

// People is what an account search brought back. Complete says the site had
// fewer accounts than it was allowed to send, which is what makes typing
// answerable from what is already held.
type People struct {
	Users    []jira.User
	Complete bool
}

// FindAccounts searches the site's accounts, and then draws back any account
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
func FindAccounts(ctx context.Context, finder jira.PeopleFinder, q jira.PeopleQuery, inForce []string) (People, error) {
	users, err := finder.FindPeople(ctx, q)
	if err != nil {
		return People{}, err
	}
	complete := len(users) < q.Limit
	if missing := absent(inForce, users); len(missing) > 0 {
		also, err := finder.People(ctx, missing)
		if err != nil {
			return People{}, err
		}
		users = append(users, also...)
	}
	return People{Users: users, Complete: complete}, nil
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

// Vocabulary reads the values of a facet that is not a person.
func Vocabulary(ctx context.Context, vocab jira.FilterVocabulary, f appterm.Facet, project string) ([]Value, error) {
	switch f {
	case appterm.FacetStatus, appterm.FacetType:
		byType, err := vocab.IssueTypeStatuses(ctx, project)
		if err != nil {
			return nil, err
		}
		if f == appterm.FacetType {
			return TypeValues(byType), nil
		}
		return StatusValues(byType), nil
	case appterm.FacetPriority:
		priorities, err := vocab.Priorities(ctx)
		if err != nil {
			return nil, err
		}
		return PriorityValues(priorities), nil
	case appterm.FacetLabel:
		labels, err := walkLabels(ctx, vocab)
		if err != nil {
			return nil, err
		}
		return LabelValues(labels), nil
	case appterm.FacetNone, appterm.FacetAssignee, appterm.FacetReporter:
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
	for page.HasMore() && len(out) < MaxLabels {
		page, err = page.Next(ctx)
		if err != nil {
			return nil, err
		}
		out = append(out, page.Items...)
	}
	if len(out) > MaxLabels {
		out = out[:MaxLabels]
	}
	return out, nil
}
