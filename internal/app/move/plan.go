// Package move is the bulk move of issues to another project: the plan a
// wizard builds (target, status remap, mandatory fields, what the move drops)
// and the task the queue runs it under, followed until it finishes.
//
// Nothing here is matched by display name. A status is remapped by id and an
// issue type is chosen by id, because a name is translated on a site that is not
// in English and is not unique even on one that is.
package move

import (
	"strings"

	"github.com/varijkapil13/saral/pkg/jira"
)

// MaxKeys is what one bulk move takes. The endpoint refuses more, and refusing
// here is the honest half: a wizard that quietly moved the first thousand of
// twelve hundred would report a success over four hundred issues left behind.
//
// Subtasks travel with their parents and count towards it as well, so the site
// can still refuse a selection this accepts. Nothing here knows how many there
// are, which is why the confirm screen says so rather than guessing.
const MaxKeys = 1000

// TooMany reports whether n issues are more than one move takes.
func TooMany(n int) bool { return n > MaxKeys }

// suppliedFields are the platform field ids a move supplies for itself, so a
// target project insisting on them is not insisting on anything a user has to
// answer. They are platform ids and not display names: a name is translated and
// an id is not.
var suppliedFields = map[string]bool{
	"project":   true,
	"issuetype": true,
	"summary":   true,
	"status":    true,
}

// Remap is one source status and the target status the issues on it will land
// on. Both are held by id, because a display name is neither unique nor stable:
// one measured site had four pairs of distinct status ids sharing a name, and a
// German site translates every one of them.
type Remap struct {
	From  jira.Status
	Count int
	// To indexes into the target statuses. A target with no statuses at all
	// leaves it at -1.
	To int
}

// Cycle moves the landing status by steps over n target statuses, wrapping.
func (r *Remap) Cycle(by, n int) {
	if n <= 0 {
		return
	}
	r.To = (max(r.To, 0) + by + n) % n
}

// Pending is one field the target insists on. Chosen is -1 while the field is
// keeping whatever the source issue holds, which is what a move does with every
// mandatory field until one of them is given a value.
type Pending struct {
	Meta    jira.FieldMeta
	Options []jira.Option
	Chosen  int
}

// Retains reports whether the field keeps what the source issue holds.
func (p *Pending) Retains() bool { return p.Chosen < 0 }

// Fillable reports whether the site offered any value to set the field to.
func (p *Pending) Fillable() bool { return len(p.Options) > 0 }

// Value is the option chosen, or the zero option while the field is kept.
func (p *Pending) Value() jira.Option {
	if p.Retains() || p.Chosen >= len(p.Options) {
		return jira.Option{}
	}
	return p.Options[p.Chosen]
}

// Cycle moves the chosen value by steps. The values run from -1, which keeps
// what the source issue holds, so a field can always be put back to being left
// alone.
func (p *Pending) Cycle(by int) {
	if !p.Fillable() {
		return
	}
	p.Chosen = (p.Chosen+by+2+len(p.Options))%(len(p.Options)+1) - 1
}

// Name is what this site calls the field, falling back to its id. The id is the
// only half that is the same on two sites, so it is what shows when the site
// sent no label at all.
func (p *Pending) Name() string {
	if n := strings.TrimSpace(p.Meta.Name); n != "" {
		return n
	}
	if n := strings.TrimSpace(p.Meta.Field.Name); n != "" {
		return n
	}
	return p.Meta.Field.ID
}

// SourceStatuses are the distinct statuses the chosen issues are on, in the
// order they were first seen, each with how many issues sit on it. Distinct by
// id: two statuses that share a display name are two rows, which is the whole
// reason the remap exists.
func SourceStatuses(issues []jira.Issue) []Remap {
	out := make([]Remap, 0, 4)
	at := make(map[string]int, 4)
	for i := range issues {
		st := issues[i].Status
		if j, seen := at[st.ID]; seen {
			out[j].Count++
			continue
		}
		at[st.ID] = len(out)
		out = append(out, Remap{From: st, Count: 1, To: -1})
	}
	return out
}

// StatusesFor is the workflow the target issue type runs. It is looked up by
// type id: the same project answers with a different set per type, and one of
// them can share a display name with a status of another id.
func StatusesFor(vocab []jira.IssueTypeStatuses, typeID string) []jira.Status {
	for i := range vocab {
		if vocab[i].Type.ID == typeID {
			return vocab[i].Statuses
		}
	}
	return nil
}

// DefaultRemap points each source status at a target status of the same
// category. The category is the one thing about a status that is not site
// configuration, so it is the only defensible guess; where the target workflow
// has nothing in that category the first status is offered instead, and the row
// says so by being on screen for the user to change.
func DefaultRemap(rows []Remap, targets []jira.Status) []Remap {
	for i := range rows {
		rows[i].To = -1
		if len(targets) == 0 {
			continue
		}
		rows[i].To = 0
		for j := range targets {
			if targets[j].Category == rows[i].From.Category {
				rows[i].To = j
				break
			}
		}
	}
	return rows
}

// Unmapped finds the first row with nowhere to land, which is a move Jira
// refuses. The row is -1 when the target reaches no status at all.
func Unmapped(rows []Remap, targets []jira.Status) (row int, blocked bool) {
	if len(targets) == 0 {
		return -1, true
	}
	for i := range rows {
		if rows[i].To < 0 || rows[i].To >= len(targets) {
			return i, true
		}
	}
	return 0, false
}

// Mandatory is every field the target insists on, less the four a move supplies
// for itself. It is the whole group and not the interesting part of it: naming
// one value on this endpoint stops all the others being kept from the source, so
// the set has to be complete or empty and the wizard cannot know which fields
// matter without the whole list.
func Mandatory(schema jira.Schema) []Pending {
	out := make([]Pending, 0, len(schema.Fields))
	required := schema.Required()
	for i := range required {
		if suppliedFields[required[i].Field.ID] {
			continue
		}
		out = append(out, Pending{Meta: required[i], Options: required[i].AllowedValues, Chosen: -1})
	}
	return out
}

// Written reports whether any mandatory field is being set rather than kept,
// which is the one fact the whole group's behaviour turns on.
func Written(fields []Pending) bool {
	for i := range fields {
		if !fields[i].Retains() {
			return true
		}
	}
	return false
}

// HalfAnswered finds the field that stops a set of mandatory fields being
// submitted: one of them has been given a value and this one still keeps the
// source's. Sending it would write the one and blank the rest, because a value
// anywhere in the group opts the whole group out of keeping what the source
// issues hold.
func HalfAnswered(fields []Pending) (row int, blocked bool) {
	if !Written(fields) {
		return 0, false
	}
	for i := range fields {
		if fields[i].Retains() {
			return i, true
		}
	}
	return 0, false
}

// Plan is the move as it is on the confirm screen.
type Plan struct {
	Issues  []jira.Issue
	Target  string
	TypeID  string
	Targets []jira.Status
	Remaps  []Remap
	Fields  []Pending
	Notify  bool
}

// Request is the move as it will be submitted, built from the plan and from
// nothing else.
func (p *Plan) Request() jira.MoveRequest {
	maps := make([]jira.StatusMapping, 0, len(p.Remaps))
	for i := range p.Remaps {
		to := p.Remaps[i].To
		if to < 0 || to >= len(p.Targets) {
			continue
		}
		maps = append(maps, jira.StatusMapping{
			FromStatusID: p.Remaps[i].From.ID,
			ToStatusID:   p.Targets[to].ID,
		})
	}
	// Either every mandatory field carries a value or none of them does. A
	// partly filled group writes what it names and blanks the rest, which is why
	// HalfAnswered refuses one rather than sending it.
	values := make(map[string]jira.FieldValue, len(p.Fields))
	if Written(p.Fields) {
		for i := range p.Fields {
			f := &p.Fields[i]
			values[f.Meta.Field.ID] = jira.FieldValue{Kind: jira.KindOption, Options: []jira.Option{f.Value()}}
		}
	}
	keys := make([]string, 0, len(p.Issues))
	for i := range p.Issues {
		keys = append(keys, p.Issues[i].Key)
	}
	in := jira.MoveRequest{
		Keys:              keys,
		TargetProjectKey:  p.Target,
		TargetIssueTypeID: p.TypeID,
		StatusMap:         maps,
		Notify:            p.Notify,
	}
	if len(values) > 0 {
		in.Fields = jira.NewFieldSet(values)
	}
	return in
}
