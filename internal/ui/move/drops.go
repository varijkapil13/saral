package move

import (
	"cmp"
	"context"
	"errors"
	"maps"
	"regexp"
	"slices"
	"strings"

	tea "charm.land/bubbletea/v2"

	"github.com/varijkapil13/saral/internal/ui/widget"
	"github.com/varijkapil13/saral/pkg/jira"
)

// keptOnMove are platform field ids a move never drops for want of a place on
// the target's create screen: the ones it supplies or maps itself, the ones that
// travel with an issue rather than being a value on it, and the two a create
// screen shows or hides by the caller's permissions rather than by the target's
// configuration.
var keptOnMove = map[string]bool{
	"project":    true,
	"issuetype":  true,
	"status":     true,
	"summary":    true,
	"resolution": true,
	"reporter":   true,
	"assignee":   true,
	"attachment": true,
	"issuelinks": true,
	"comment":    true,
	"worklog":    true,
	"subtasks":   true,
}

// dropChunk is how many keys one read of the source values names. It keeps the
// JQL short enough for a query string and one page long.
const dropChunk = 100

var issueKey = regexp.MustCompile(`^[A-Za-z][A-Za-z0-9_]*-\d+$`)

type dropState uint8

const (
	// dropNone is a move with nothing to check: every issue is already in the
	// target project, or no target schema has been asked for yet.
	dropNone dropState = iota
	dropPending
	dropDone
	dropFailed
)

// dropped is one field the source issues hold a value in and the target's
// create screen has no place for, with how many of the leaving issues hold one.
type dropped struct {
	id    string
	name  string
	count int
}

type candidate struct {
	id   string
	name string
}

type pair struct {
	project string
	typeID  string
}

type droppedMsg struct {
	gen     int
	fields  []dropped
	leaving int
	sources map[pair]jira.Schema
	err     error
}

// errNoSourceShape is an issue handed over without the project or issue type
// its create screen is configured by, so there is no screen to compare with.
var errNoSourceShape = errors.New("some of these issues arrived without their project or issue type, " +
	"so which fields the move drops could not be checked")

var errOddKey = errors.New("an issue key here is not one JQL can name, so which fields the move drops could not be checked")

// leavingIssues are the issues that change project. An issue already in the
// target keeps every field it has.
func leavingIssues(issues []jira.Issue, target string) []jira.Issue {
	out := make([]jira.Issue, 0, len(issues))
	for i := range issues {
		if issues[i].Project.Key != target {
			out = append(out, issues[i])
		}
	}
	return out
}

// gaps are the fields on any source create screen that the target's has
// no place for, in the order the first screen naming each one sent them. Names
// are the site's own, sanitized here because they are drawn later.
func gaps(sources []jira.Schema, target jira.Schema) []candidate {
	onTarget := make(map[string]bool, len(target.Fields))
	for i := range target.Fields {
		onTarget[target.Fields[i].Field.ID] = true
	}
	seen := make(map[string]bool, 8)
	out := make([]candidate, 0, 8)
	for s := range sources {
		for i := range sources[s].Fields {
			meta := &sources[s].Fields[i]
			id := meta.Field.ID
			if id == "" || onTarget[id] || keptOnMove[id] || seen[id] {
				continue
			}
			seen[id] = true
			out = append(out, candidate{id: id, name: fieldName(meta)})
		}
	}
	return out
}

func fieldName(meta *jira.FieldMeta) string {
	for _, n := range []string{meta.Name, meta.Field.Name} {
		if s := strings.TrimSpace(widget.Sanitize(n)); s != "" {
			return s
		}
	}
	return widget.Sanitize(meta.Field.ID)
}

// dropsOf is what the move loses: each candidate at least one leaving issue
// holds a value in, most widely held first and otherwise in screen order.
func dropsOf(cands []candidate, issues []jira.Issue) []dropped {
	out := make([]dropped, 0, len(cands))
	for _, c := range cands {
		n := 0
		for i := range issues {
			if holds(&issues[i], c.id) {
				n++
			}
		}
		if n > 0 {
			out = append(out, dropped{id: c.id, name: c.name, count: n})
		}
	}
	slices.SortStableFunc(out, func(a, b dropped) int { return cmp.Compare(b.count, a.count) })
	return out
}

// holds reports whether an issue carries a value in a field. The system fields
// the adapter lifts onto the Issue struct are read there; everything else is in
// the field set.
func holds(iss *jira.Issue, id string) bool {
	switch id {
	case "description":
		return !iss.Description.IsEmpty()
	case "priority":
		return iss.Priority != nil
	case "labels":
		return len(iss.Labels) > 0
	case "components":
		return len(iss.Components) > 0
	case "fixVersions":
		return len(iss.FixVersions) > 0
	case "parent":
		return iss.Parent != nil
	case "duedate":
		return !iss.Due.IsZero()
	case "timetracking":
		return iss.TimeTracking != nil && *iss.TimeTracking != jira.TimeTracking{}
	}
	v, ok := iss.Fields.ByID(id)
	return ok && !emptyValue(v)
}

func emptyValue(v jira.FieldValue) bool {
	switch v.Kind {
	case jira.KindEmpty:
		return true
	case jira.KindText, jira.KindUnknown:
		return strings.TrimSpace(v.Text) == ""
	case jira.KindDoc:
		return v.Doc.IsEmpty()
	case jira.KindOption, jira.KindOptions:
		return len(v.Options) == 0
	case jira.KindUser, jira.KindUsers:
		return len(v.Users) == 0
	case jira.KindDate:
		return v.Date.IsZero()
	case jira.KindTime:
		return v.Time.IsZero()
	case jira.KindNumber, jira.KindBool:
	}
	return false
}

// unloaded are the issues whose read did not ask for every candidate, so an
// absent value on them is not yet an answer.
func unloaded(issues []jira.Issue, cands []candidate) []jira.Issue {
	out := make([]jira.Issue, 0, len(issues))
	for i := range issues {
		for _, c := range cands {
			if !issues[i].Requested.Has(c.id) {
				out = append(out, issues[i])
				break
			}
		}
	}
	return out
}

type dropReader interface {
	jira.SchemaReader
	jira.Searcher
}

// checkDrops compares the leaving issues' create screens with the target's and
// counts who holds what the target has no place for. Source screens already
// read are passed in and not asked for again. Values are read only for the
// candidate fields and only on issues whose own read did not ask for them.
func checkDrops(ctx context.Context, client dropReader, leaving []jira.Issue, target jira.Schema,
	known map[pair]jira.Schema, gen int,
) tea.Cmd {
	return func() tea.Msg {
		fail := func(err error) tea.Msg { return droppedMsg{gen: gen, err: err} }
		sources := make(map[pair]jira.Schema, len(known)+1)
		maps.Copy(sources, known)
		order := make([]pair, 0, 2)
		for i := range leaving {
			p := pair{project: leaving[i].Project.Key, typeID: leaving[i].Type.ID}
			if p.project == "" || p.typeID == "" {
				return fail(errNoSourceShape)
			}
			if !slices.Contains(order, p) {
				order = append(order, p)
			}
		}
		screens := make([]jira.Schema, 0, len(order))
		for _, p := range order {
			schema, ok := sources[p]
			if !ok {
				var err error
				if schema, err = client.CreateMeta(ctx, p.project, p.typeID); err != nil {
					return fail(err)
				}
				sources[p] = schema
			}
			screens = append(screens, schema)
		}
		cands := gaps(screens, target)
		if len(cands) == 0 {
			return droppedMsg{gen: gen, leaving: len(leaving), sources: sources}
		}
		read, err := readValues(ctx, client, unloaded(leaving, cands), cands)
		if err != nil {
			return fail(err)
		}
		counted := make([]jira.Issue, len(leaving))
		for i := range leaving {
			counted[i] = leaving[i]
			if fresh, ok := read[leaving[i].Key]; ok {
				counted[i] = fresh
			}
		}
		return droppedMsg{gen: gen, fields: dropsOf(cands, counted), leaving: len(leaving), sources: sources}
	}
}

func readValues(ctx context.Context, client jira.Searcher, issues []jira.Issue, cands []candidate) (map[string]jira.Issue, error) {
	out := make(map[string]jira.Issue, len(issues))
	if len(issues) == 0 {
		return out, nil
	}
	fields := make([]string, 0, len(cands))
	for _, c := range cands {
		fields = append(fields, c.id)
	}
	for chunk := range slices.Chunk(issues, dropChunk) {
		keys := make([]string, 0, len(chunk))
		for i := range chunk {
			if !issueKey.MatchString(chunk[i].Key) {
				return nil, errOddKey
			}
			keys = append(keys, `"`+chunk[i].Key+`"`)
		}
		page, err := client.Search(ctx, jira.Query{
			JQL:        "key in (" + strings.Join(keys, ", ") + ")",
			Fields:     fields,
			MaxResults: len(chunk),
		})
		if err != nil {
			return nil, err
		}
		got, err := jira.Collect(ctx, page, len(chunk))
		if err != nil {
			return nil, err
		}
		for i := range got {
			out[got[i].Key] = got[i]
		}
	}
	return out, nil
}
