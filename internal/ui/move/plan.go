package move

import (
	"strconv"
	"strings"

	"github.com/varijkapil13/saral/pkg/jira"
)

// maxKeys is what one bulk move takes. The endpoint refuses more, and refusing
// here is the honest half: a wizard that quietly moved the first thousand of
// twelve hundred would report a success over four hundred issues left behind.
//
// Subtasks travel with their parents and count towards it as well, so the site
// can still refuse a selection this accepts. Nothing here knows how many there
// are, which is why the confirm screen says so rather than guessing.
const maxKeys = 1000

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

// remap is one source status and the target status the issues on it will land
// on. Both are held by id, because a display name is neither unique nor stable:
// one measured site had four pairs of distinct status ids sharing a name, and a
// German site translates every one of them.
type remap struct {
	from  jira.Status
	count int
	// to indexes into the target statuses, which is what the left and right keys
	// move. A target with no statuses at all leaves it at -1.
	to int
}

// pending is one field the target insists on. chosen is -1 while the field is
// keeping whatever the source issue holds, which is what a move does with every
// mandatory field until one of them is given a value.
type pending struct {
	meta    jira.FieldMeta
	options []jira.Option
	chosen  int
}

func (p *pending) retains() bool { return p.chosen < 0 }

func (p *pending) fillable() bool { return len(p.options) > 0 }

func (p *pending) value() jira.Option {
	if p.retains() || p.chosen >= len(p.options) {
		return jira.Option{}
	}
	return p.options[p.chosen]
}

// name is what this site calls the field, falling back to its id. The id is the
// only half that is the same on two sites, so it is what shows when the site
// sent no label at all.
func (p *pending) name() string {
	if n := strings.TrimSpace(p.meta.Name); n != "" {
		return n
	}
	if n := strings.TrimSpace(p.meta.Field.Name); n != "" {
		return n
	}
	return p.meta.Field.ID
}

// sourceStatuses are the distinct statuses the chosen issues are on, in the
// order they were first seen, each with how many issues sit on it. Distinct by
// id: two statuses that share a display name are two rows, which is the whole
// reason the remap exists.
func sourceStatuses(issues []jira.Issue) []remap {
	out := make([]remap, 0, 4)
	at := make(map[string]int, 4)
	for i := range issues {
		st := issues[i].Status
		if j, seen := at[st.ID]; seen {
			out[j].count++
			continue
		}
		at[st.ID] = len(out)
		out = append(out, remap{from: st, count: 1, to: -1})
	}
	return out
}

// statusesFor is the workflow the target issue type runs. It is looked up by
// type id: the same project answers with a different set per type, and one of
// them can share a display name with a status of another id.
func statusesFor(vocab []jira.IssueTypeStatuses, typeID string) []jira.Status {
	for i := range vocab {
		if vocab[i].Type.ID == typeID {
			return vocab[i].Statuses
		}
	}
	return nil
}

// defaultRemap points each source status at a target status of the same
// category. The category is the one thing about a status that is not site
// configuration, so it is the only defensible guess; where the target workflow
// has nothing in that category the first status is offered instead, and the row
// says so by being on screen for the user to change.
func defaultRemap(rows []remap, targets []jira.Status) []remap {
	for i := range rows {
		rows[i].to = -1
		if len(targets) == 0 {
			continue
		}
		rows[i].to = 0
		for j := range targets {
			if targets[j].Category == rows[i].from.Category {
				rows[i].to = j
				break
			}
		}
	}
	return rows
}

// mandatory is every field the target insists on, less the four a move supplies
// for itself. It is the whole group and not the interesting part of it: naming
// one value on this endpoint stops all the others being kept from the source, so
// the set has to be complete or empty and the wizard cannot know which fields
// matter without the whole list.
func mandatory(schema jira.Schema) []pending {
	out := make([]pending, 0, len(schema.Fields))
	required := schema.Required()
	for i := range required {
		if suppliedFields[required[i].Field.ID] {
			continue
		}
		out = append(out, pending{meta: required[i], options: required[i].AllowedValues, chosen: -1})
	}
	return out
}

// halfAnswered is why a set of mandatory fields cannot be submitted: one of them
// has been given a value and another has nothing to give. Sending it would write
// the one and blank the rest, because a value anywhere in the group opts the
// whole group out of keeping what the source issues hold.
func halfAnswered(fields []pending) (string, bool) {
	written := false
	for i := range fields {
		if !fields[i].retains() {
			written = true
			break
		}
	}
	if !written {
		return "", false
	}
	for i := range fields {
		if !fields[i].retains() {
			continue
		}
		if !fields[i].fillable() {
			return "setting " + fields[i].name() + " is not possible here — this site offered no values " +
				"for it — and naming any other value stops it being kept from the source; leave them all alone " +
				"or move these in the browser", true
		}
		return fields[i].name() + " has to be given a value too: naming one mandatory field stops every " +
			"other one being kept from the source", true
	}
	return "", false
}

// written reports whether any mandatory field is being set rather than kept,
// which is the one fact the whole group's behaviour turns on.
func written(fields []pending) bool {
	for i := range fields {
		if !fields[i].retains() {
			return true
		}
	}
	return false
}

// request is the move as it will be submitted, built from what is on the confirm
// screen and from nothing else.
func (m *Model) request() jira.MoveRequest {
	targets := m.targetStatuses()
	maps := make([]jira.StatusMapping, 0, len(m.remaps))
	for i := range m.remaps {
		to := m.remaps[i].to
		if to < 0 || to >= len(targets) {
			continue
		}
		maps = append(maps, jira.StatusMapping{
			FromStatusID: m.remaps[i].from.ID,
			ToStatusID:   targets[to].ID,
		})
	}
	// Either every mandatory field carries a value or none of them does. A
	// partly filled group writes what it names and blanks the rest, which is why
	// confirmed refuses one rather than sending it.
	values := make(map[string]jira.FieldValue, len(m.fields))
	if written(m.fields) {
		for i := range m.fields {
			f := &m.fields[i]
			values[f.meta.Field.ID] = jira.FieldValue{Kind: jira.KindOption, Options: []jira.Option{f.value()}}
		}
	}
	in := jira.MoveRequest{
		Keys:              m.issueKeys(),
		TargetProjectKey:  m.target,
		TargetIssueTypeID: m.targetType().ID,
		StatusMap:         maps,
		Notify:            m.notify,
	}
	if len(values) > 0 {
		in.Fields = jira.NewFieldSet(values)
	}
	return in
}

func (m *Model) issueKeys() []string {
	out := make([]string, 0, len(m.issues))
	for i := range m.issues {
		out = append(out, m.issues[i].Key)
	}
	return out
}

// tooMany is why a selection cannot be submitted at all, in a sentence carrying
// both numbers: the count is the fact the answer turns on.
func tooMany(n int) (string, bool) {
	if n <= maxKeys {
		return "", false
	}
	return "Jira takes " + strconv.Itoa(maxKeys) + " issues in one move and this is " +
		strconv.Itoa(n) + "; move them in smaller batches", true
}
