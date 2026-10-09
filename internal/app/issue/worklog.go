package issue

import (
	"context"
	"errors"
	"strings"
	"time"

	"github.com/varijkapil13/saral/pkg/adf"
	"github.com/varijkapil13/saral/pkg/jira"
)

const worklogLimit = 200

// TimeReader is what reading an issue's logged time runs on.
type TimeReader interface {
	jira.Worklogger
	jira.IssueReader
}

// TimeLogged is the time logged on an issue, up to a bound, with its time
// tracking.
func TimeLogged(ctx context.Context, c TimeReader, key string) ([]jira.Worklog, *jira.TimeTracking, error) {
	page, err := c.Worklogs(ctx, key)
	if err != nil {
		return nil, nil, err
	}
	logs, err := jira.Collect(ctx, page, worklogLimit)
	if err != nil {
		return nil, nil, err
	}
	iss, err := c.IssueFields(ctx, key, []string{"timetracking"})
	if err != nil {
		return nil, nil, err
	}
	return logs, iss.TimeTracking, nil
}

// LogWork logs time on an issue, with note as its comment when there is one.
func LogWork(ctx context.Context, w jira.Worklogger, key string, spent time.Duration, started time.Time, note string) error {
	in := jira.WorklogInput{Spent: spent, Started: started}
	if note != "" {
		in.Comment = adf.NewDoc(adf.NewNode("paragraph", adf.NewText(note)))
	}
	_, err := w.AddWorklog(ctx, key, in)
	return err
}

// What ParseSpent and ParseStarted refuse.
var (
	ErrNoLength     = errors.New("no length of time")
	ErrLengthInDays = errors.New("a length in days or weeks")
	ErrNotLength    = errors.New("not a length of time")
	ErrUnderMinute  = errors.New("less than a minute")
	ErrNotDate      = errors.New("not a date")
	ErrNotYet       = errors.New("not happened yet")
)

// ParseSpent reads a length of time in hours and minutes. A day or a week is
// refused: its length is the site's working day, which this client never reads.
func ParseSpent(text string) (time.Duration, error) {
	t := strings.ToLower(strings.Join(strings.Fields(text), ""))
	switch {
	case t == "":
		return 0, ErrNoLength
	case strings.ContainsAny(t, "dw"):
		return 0, ErrLengthInDays
	}
	d, err := time.ParseDuration(t)
	switch {
	case err != nil:
		return 0, ErrNotLength
	case d < time.Minute:
		return 0, ErrUnderMinute
	}
	return d, nil
}

// ParseStarted reads when logged work started: a day, with a time of day if
// given, in loc; empty is now. A start in the future is refused.
func ParseStarted(text string, now time.Time, loc *time.Location) (time.Time, error) {
	now = now.In(loc)
	if text == "" {
		return now, nil
	}
	at, err := time.ParseInLocation("2006-01-02 15:04", text, loc)
	if err != nil {
		day, dayErr := time.ParseInLocation(time.DateOnly, text, loc)
		if dayErr != nil {
			return time.Time{}, ErrNotDate
		}
		at = time.Date(day.Year(), day.Month(), day.Day(), now.Hour(), now.Minute(), 0, 0, loc)
	}
	if at.After(now) {
		return time.Time{}, ErrNotYet
	}
	return at, nil
}
