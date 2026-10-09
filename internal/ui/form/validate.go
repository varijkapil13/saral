package form

import (
	"errors"
	"fmt"
	"strconv"
	"strings"

	appissue "github.com/varijkapil13/saral/internal/app/issue"
	"github.com/varijkapil13/saral/pkg/adf"
	"github.com/varijkapil13/saral/pkg/jira"
)

// validate answers what is wrong with this field's value, and "" when nothing
// is.
func (f *field) validate() string {
	e := f.entry()
	return problemText(e.Check())
}

func problemText(p appissue.Problem) string {
	switch p.Kind {
	case appissue.ProblemRequired:
		return "this field is required"
	case appissue.ProblemNotNumber:
		return strconv.Quote(p.Text) + " is not a number"
	case appissue.ProblemNotDate:
		return strconv.Quote(p.Text) + " is not a date; write it as 2026-03-27"
	case appissue.ProblemNotDateTime:
		return strconv.Quote(p.Text) + " is not a date and time; write it as 2026-03-27 09:30"
	case appissue.ProblemNotIssueKey:
		return strconv.Quote(p.Text) + " is not an issue key; write it as PROJ-142"
	case appissue.ProblemDocument:
		return docProblem(p.Err)
	case appissue.ProblemNoAccount:
		return strconv.Quote(p.Option.Label) + " has no account id, so Jira cannot be told who it is"
	case appissue.ProblemNotAllowed:
		return strconv.Quote(cascadeLabel(p.Option)) + " is not one of the values this field allows"
	default:
		return ""
	}
}

// docProblem words a markdown the parser will not turn into a document, at the
// line it stopped on.
func docProblem(err error) string {
	var stopped *adf.ParseError
	if errors.As(err, &stopped) {
		return fmt.Sprintf("line %d: %v", stopped.Line, stopped.Err)
	}
	return err.Error()
}

// validateAll re-checks every field and reports how many are wrong.
func (m *Model) validateAll() int {
	bad := 0
	for _, f := range m.fields {
		problem := f.validate()
		if problem != f.problem {
			f.problem, f.rev = problem, f.rev+1
		}
		if problem != "" {
			bad++
		}
	}
	return bad
}

// firstProblem is the index of the first field with something wrong with it.
func (m *Model) firstProblem() int {
	for i, f := range m.fields {
		if f.problem != "" {
			return i
		}
	}
	return -1
}

// applyValidationError puts Jira's own words on the fields they are about.
//
// The messages are keyed by field id, which is what the create endpoint sends.
// A key that matches no field on this form has nowhere to go inline, and is
// shown above the form rather than dropped: docs/TESTING.md asks for the first
// case, and the second is the one that silently loses a refusal.
func (m *Model) applyValidationError(invalid *jira.ValidationError) {
	m.banner = nil
	for _, entry := range invalid.Fields {
		if f := m.fieldFor(entry.Field); f != nil {
			f.problem, f.rev = entry.Message, f.rev+1
			continue
		}
		m.banner = append(m.banner, entry.Field+": "+entry.Message)
	}
	for _, message := range invalid.Messages {
		if strings.TrimSpace(message) != "" {
			m.banner = append(m.banner, message)
		}
	}
	if at := m.firstProblem(); at >= 0 {
		m.moveTo(at)
	}
}

// fieldFor finds the field a message is about. The id is what Jira sends and
// the only thing that is the same on every site; a display name is tried only
// when no id matched, because a translated site sends translated names and a
// form keyed off one works on exactly one site.
func (m *Model) fieldFor(name string) *field {
	for _, f := range m.fields {
		if f.id() == name {
			return f
		}
	}
	for _, f := range m.fields {
		if strings.EqualFold(f.meta.Name, name) {
			return f
		}
	}
	return nil
}
