package sprint

import (
	"errors"
	"strings"
	"time"

	"github.com/varijkapil13/saral/pkg/jira"
)

// DateLayout is how a sprint's date is typed and drawn. It is the one format
// that is the same in every locale: the site's own dates arrive as instants and
// go back out in the layout the adapter writes.
const DateLayout = "2006-01-02"

// ErrBadDate is a date not written in DateLayout.
var ErrBadDate = errors.New("a date is not written " + DateLayout)

// ParseDate reads a date as it is typed. An empty one is not a bad date: it is
// a date that has not been given, which is what a planned sprint has.
func ParseDate(s string, loc *time.Location) (*time.Time, error) {
	trimmed := strings.TrimSpace(s)
	if trimmed == "" {
		return nil, nil
	}
	at, err := time.ParseInLocation(DateLayout, trimmed, loc)
	if err != nil {
		return nil, ErrBadDate
	}
	return &at, nil
}

// Field is one of the values a sprint is planned or edited with.
type Field uint8

// The fields in the order a form shows them, and how many there are.
const (
	FieldName Field = iota
	FieldGoal
	FieldStart
	FieldEnd
	FieldCount
)

// FieldOf maps the API's own field names onto the fields. A name that is not
// one of them reports false.
func FieldOf(name string) (Field, bool) {
	switch name {
	case "name":
		return FieldName, true
	case "goal":
		return FieldGoal, true
	case "startDate":
		return FieldStart, true
	case "endDate":
		return FieldEnd, true
	}
	return FieldCount, false
}

// Draft is each field as typed.
type Draft [FieldCount]string

// Problem is what is wrong with one field before anything is sent.
type Problem uint8

// The problems the port would refuse locally anyway.
const (
	Fine Problem = iota
	NoName
	BadDate
	EndsBeforeStart
	// Cleared is a date that was set and has been emptied, which is a request to
	// unset one, and the port has no way to send that: a nil field in a patch
	// means leave it alone, and an empty string would be a date of nothing.
	Cleared
)

// Problems are a draft's problems by field.
type Problems [FieldCount]Problem

// First is the first field with a problem, or false when there is none.
func (p Problems) First() (Field, bool) {
	for i := range p {
		if p[i] != Fine {
			return Field(i), true
		}
	}
	return FieldCount, false
}

// Validate is what is wrong with typed, given was, what the fields held before
// any of it was typed; a new sprint's was is empty.
func Validate(typed, was Draft, loc *time.Location) Problems {
	var out Problems
	if strings.TrimSpace(typed[FieldName]) == "" {
		out[FieldName] = NoName
	}
	start, startErr := ParseDate(typed[FieldStart], loc)
	end, endErr := ParseDate(typed[FieldEnd], loc)
	if startErr != nil {
		out[FieldStart] = BadDate
	}
	if endErr != nil {
		out[FieldEnd] = BadDate
	}
	if start != nil && end != nil && end.Before(*start) {
		out[FieldEnd] = EndsBeforeStart
	}
	for _, at := range [...]Field{FieldStart, FieldEnd} {
		if was[at] != "" && strings.TrimSpace(typed[at]) == "" {
			out[at] = Cleared
		}
	}
	return out
}

// Input is a validated draft as a sprint to create on a board.
func Input(boardID int64, typed Draft, loc *time.Location) jira.SprintInput {
	start, _ := ParseDate(typed[FieldStart], loc)
	end, _ := ParseDate(typed[FieldEnd], loc)
	return jira.SprintInput{
		BoardID: boardID,
		Name:    strings.TrimSpace(typed[FieldName]),
		Goal:    strings.TrimSpace(typed[FieldGoal]),
		Start:   start,
		End:     end,
	}
}

// Patch is the fields that have changed from was, each as a pointer, and
// reports whether any has. Everything nil is what leaves a field alone; the
// endpoint underneath nulls whatever it is not sent. A closed sprint takes only
// its name and goal, so closed leaves its dates out.
func Patch(typed, was Draft, closed bool, loc *time.Location) (jira.SprintPatch, bool) {
	var out jira.SprintPatch
	named := false
	if name := strings.TrimSpace(typed[FieldName]); name != strings.TrimSpace(was[FieldName]) {
		out.Name, named = &name, true
	}
	if goal := typed[FieldGoal]; goal != was[FieldGoal] {
		out.Goal, named = &goal, true
	}
	if closed {
		return out, named
	}
	if typed[FieldStart] != was[FieldStart] {
		if at, err := ParseDate(typed[FieldStart], loc); err == nil && at != nil {
			out.Start, named = at, true
		}
	}
	if typed[FieldEnd] != was[FieldEnd] {
		if at, err := ParseDate(typed[FieldEnd], loc); err == nil && at != nil {
			out.End, named = at, true
		}
	}
	return out, named
}
