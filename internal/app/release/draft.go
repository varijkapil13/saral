package release

import (
	"errors"
	"strings"

	"github.com/varijkapil13/saral/pkg/jira"
)

// Draft is a version as typed: an id when it edits one, and four values.
type Draft struct {
	ID          string
	Name        string
	Description string
	Start       string
	Release     string
}

// DateField names which of a draft's two dates a DateError is about.
type DateField uint8

// The two dates.
const (
	StartDate DateField = iota
	ReleaseDate
)

// DateError is a typed date the port's own parser refused.
type DateError struct {
	Field DateField
	Typed string
}

func (e *DateError) Error() string { return "not a date: " + e.Typed }

var (
	// ErrNoName is a draft with no name.
	ErrNoName = errors.New("a version needs a name")
	// ErrReleasedBeforeStart is a release date earlier than the start date.
	ErrReleasedBeforeStart = errors.New("a version cannot be released before it starts")
)

// Input builds what the site is sent, or says why it cannot be. Both dates are
// read with the port's own parser, so a typed one is refused here rather than
// turned into a day somewhere east of the reader. A new version is created in
// project.
func (d Draft) Input(project string) (jira.VersionInput, error) {
	name := strings.TrimSpace(d.Name)
	if name == "" {
		return jira.VersionInput{}, ErrNoName
	}
	start, err := readDay(d.Start, StartDate)
	if err != nil {
		return jira.VersionInput{}, err
	}
	release, err := readDay(d.Release, ReleaseDate)
	if err != nil {
		return jira.VersionInput{}, err
	}
	if !start.IsZero() && !release.IsZero() && release.Before(start) {
		return jira.VersionInput{}, ErrReleasedBeforeStart
	}
	in := jira.VersionInput{
		ID:          d.ID,
		Name:        name,
		Description: strings.TrimSpace(d.Description),
		StartDate:   start,
		ReleaseDate: release,
	}
	if in.ID == "" {
		in.ProjectKey = project
	}
	return in, nil
}

func readDay(typed string, field DateField) (jira.Date, error) {
	typed = strings.TrimSpace(typed)
	if typed == "" {
		return jira.Date{}, nil
	}
	day, err := jira.ParseDate(typed)
	if err != nil {
		return jira.Date{}, &DateError{Field: field, Typed: typed}
	}
	return day, nil
}
