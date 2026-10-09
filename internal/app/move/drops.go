package move

import (
	"cmp"
	"context"
	"errors"
	"maps"
	"regexp"
	"slices"
	"strings"

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

// ErrNoSourceShape is an issue handed over without the project or issue type
// its create screen is configured by, so there is no screen to compare with.
var ErrNoSourceShape = errors.New("an issue arrived without its project or issue type")

// ErrOddKey is an issue key JQL cannot name, so its values cannot be read.
var ErrOddKey = errors.New("an issue key is not one JQL can name")

// Dropped is one field the source issues hold a value in and the target's
// create screen has no place for, with how many of the leaving issues hold one.
// Meta is the first source screen's description of it, unsanitized.
type Dropped struct {
	Meta  jira.FieldMeta
	Count int
}

// Pair is the project and issue type a create screen is configured by.
type Pair struct {
	Project string
	TypeID  string
}

// Drops is what a move would lose. Sources are the source create screens read
// on the way, to be passed back in on the next check so none is asked for twice.
type Drops struct {
	Fields  []Dropped
	Leaving int
	Sources map[Pair]jira.Schema
}

// DropReader is what the check reads with: the create screens and the values.
type DropReader interface {
	jira.SchemaReader
	jira.Searcher
}

// LeavingIssues are the issues that change project. An issue already in the
// target keeps every field it has.
func LeavingIssues(issues []jira.Issue, target string) []jira.Issue {
	out := make([]jira.Issue, 0, len(issues))
	for i := range issues {
		if issues[i].Project.Key != target {
			out = append(out, issues[i])
		}
	}
	return out
}

// CheckDrops compares the leaving issues' create screens with the target's and
// counts who holds what the target has no place for. Source screens already
// read are passed in and not asked for again. Values are read only for the
// candidate fields and only on issues whose own read did not ask for them.
func CheckDrops(ctx context.Context, client DropReader, leaving []jira.Issue, target jira.Schema,
	known map[Pair]jira.Schema,
) (Drops, error) {
	sources := make(map[Pair]jira.Schema, len(known)+1)
	maps.Copy(sources, known)
	order := make([]Pair, 0, 2)
	for i := range leaving {
		p := Pair{Project: leaving[i].Project.Key, TypeID: leaving[i].Type.ID}
		if p.Project == "" || p.TypeID == "" {
			return Drops{}, ErrNoSourceShape
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
			if schema, err = client.CreateMeta(ctx, p.Project, p.TypeID); err != nil {
				return Drops{}, err
			}
			sources[p] = schema
		}
		screens = append(screens, schema)
	}
	cands := gaps(screens, target)
	if len(cands) == 0 {
		return Drops{Leaving: len(leaving), Sources: sources}, nil
	}
	read, err := readValues(ctx, client, unloaded(leaving, cands), cands)
	if err != nil {
		return Drops{}, err
	}
	counted := make([]jira.Issue, len(leaving))
	for i := range leaving {
		counted[i] = leaving[i]
		if fresh, ok := read[leaving[i].Key]; ok {
			counted[i] = fresh
		}
	}
	return Drops{Fields: dropsOf(cands, counted), Leaving: len(leaving), Sources: sources}, nil
}

// gaps are the fields on any source create screen that the target's has
// no place for, in the order the first screen naming each one sent them.
func gaps(sources []jira.Schema, target jira.Schema) []jira.FieldMeta {
	onTarget := make(map[string]bool, len(target.Fields))
	for i := range target.Fields {
		onTarget[target.Fields[i].Field.ID] = true
	}
	seen := make(map[string]bool, 8)
	out := make([]jira.FieldMeta, 0, 8)
	for s := range sources {
		for i := range sources[s].Fields {
			meta := &sources[s].Fields[i]
			id := meta.Field.ID
			if id == "" || onTarget[id] || keptOnMove[id] || seen[id] {
				continue
			}
			seen[id] = true
			out = append(out, *meta)
		}
	}
	return out
}

// dropsOf is what the move loses: each candidate at least one leaving issue
// holds a value in, most widely held first and otherwise in screen order.
func dropsOf(cands []jira.FieldMeta, issues []jira.Issue) []Dropped {
	out := make([]Dropped, 0, len(cands))
	for c := range cands {
		n := 0
		for i := range issues {
			if holds(&issues[i], cands[c].Field.ID) {
				n++
			}
		}
		if n > 0 {
			out = append(out, Dropped{Meta: cands[c], Count: n})
		}
	}
	slices.SortStableFunc(out, func(a, b Dropped) int { return cmp.Compare(b.Count, a.Count) })
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
func unloaded(issues []jira.Issue, cands []jira.FieldMeta) []jira.Issue {
	out := make([]jira.Issue, 0, len(issues))
	for i := range issues {
		for c := range cands {
			if !issues[i].Requested.Has(cands[c].Field.ID) {
				out = append(out, issues[i])
				break
			}
		}
	}
	return out
}

func readValues(ctx context.Context, client jira.Searcher, issues []jira.Issue, cands []jira.FieldMeta) (map[string]jira.Issue, error) {
	out := make(map[string]jira.Issue, len(issues))
	if len(issues) == 0 {
		return out, nil
	}
	fields := make([]string, 0, len(cands))
	for c := range cands {
		fields = append(fields, cands[c].Field.ID)
	}
	for chunk := range slices.Chunk(issues, dropChunk) {
		keys := make([]string, 0, len(chunk))
		for i := range chunk {
			if !issueKey.MatchString(chunk[i].Key) {
				return nil, ErrOddKey
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
