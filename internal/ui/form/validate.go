package form

import (
	"errors"
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/varijkapil13/saral/pkg/adf"
	"github.com/varijkapil13/saral/pkg/jira"
)

// dateTimeLayouts are what a date-and-time field accepts, in the order they are
// tried. The first two carry their own offset; the rest are read in the account
// timezone, because a time typed with no offset is the one on the user's clock.
var dateTimeLayouts = []string{
	"2006-01-02T15:04:05.000-0700",
	time.RFC3339,
	"2006-01-02 15:04:05",
	"2006-01-02 15:04",
	"2006-01-02T15:04:05",
	"2006-01-02T15:04",
}

// issueKey is the shape every Jira issue key has: a project key, a hyphen and a
// number. It is not a list of the site's projects, which no endpoint on the
// port answers.
var issueKey = regexp.MustCompile(`^[A-Za-z][A-Za-z0-9_]*-\d+$`)

func parseDateTime(text string, loc *time.Location) (time.Time, error) {
	if loc == nil {
		loc = time.UTC
	}
	trimmed := strings.TrimSpace(text)
	for i, layout := range dateTimeLayouts {
		var (
			at  time.Time
			err error
		)
		if i < 2 {
			at, err = time.Parse(layout, trimmed)
		} else {
			at, err = time.ParseInLocation(layout, trimmed, loc)
		}
		if err == nil {
			return at, nil
		}
	}
	return time.Time{}, fmt.Errorf("form: %q is not a date and time", trimmed)
}

// validate answers what is wrong with this field's value, and "" when nothing
// is. Every rule comes from what the site said about the field: whether it is
// required, what it holds, and which values it allows.
func (f *field) validate() string {
	if f.empty() {
		if f.meta.Required && !f.meta.HasDefault {
			return "this field is required"
		}
		return ""
	}
	text := strings.TrimSpace(f.text)
	switch f.kind {
	case kindNumber:
		if _, err := strconv.ParseFloat(text, 64); err != nil {
			return strconv.Quote(text) + " is not a number"
		}
	case kindDate:
		if _, err := jira.ParseDate(text); err != nil {
			return strconv.Quote(text) + " is not a date; write it as 2026-03-27"
		}
	case kindDateTime:
		if _, err := parseDateTime(text, f.loc); err != nil {
			return strconv.Quote(text) + " is not a date and time; write it as 2026-03-27 09:30"
		}
	case kindIssueKey:
		if !issueKey.MatchString(text) {
			return strconv.Quote(text) + " is not an issue key; write it as PROJ-142"
		}
	case kindDoc:
		if _, err := f.document(); err != nil {
			return docProblem(err)
		}
	case kindUser, kindUsers:
		for _, option := range f.picked {
			if option.ID == "" {
				return strconv.Quote(option.Label) + " has no account id, so Jira cannot be told who it is"
			}
		}
		return f.outsideAllowed()
	case kindSelect, kindMultiSelect, kindCascade:
		return f.outsideAllowed()
	case kindText, kindLabels, kindOther:
	}
	return ""
}

// outsideAllowed reports a chosen value the site does not allow for this field.
// A picker only ever offers what the schema stated, so this catches a value
// that arrived some other way — a restored draft against a screen that has
// since changed, or a person picker with no list to check against.
func (f *field) outsideAllowed() string {
	if len(f.meta.AllowedValues) == 0 {
		return ""
	}
	for _, option := range f.picked {
		if !allows(f.meta.AllowedValues, option) {
			return strconv.Quote(cascadeLabel(option)) + " is not one of the values this field allows"
		}
	}
	return ""
}

// allows reports whether one chosen value is among those stated, following the
// second level of a cascading select.
func allows(allowed []jira.Option, option jira.Option) bool {
	for _, candidate := range allowed {
		if candidate.ID != option.ID {
			continue
		}
		if len(option.Children) == 0 {
			return true
		}
		for _, child := range candidate.Children {
			if child.ID == option.Children[0].ID {
				return true
			}
		}
		return false
	}
	return false
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
