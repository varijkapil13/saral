package issue

import (
	"context"
	"errors"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/varijkapil13/saral/internal/ui/kernel"
	"github.com/varijkapil13/saral/internal/ui/widget"
	"github.com/varijkapil13/saral/pkg/adf"
	"github.com/varijkapil13/saral/pkg/jira"
)

const worklogLimit = 200

type timeKind struct {
	step    int
	spent   time.Duration
	started time.Time
}

var timeKeys = newSheetKeys(sheetBind{kernel.Bind([]string{"a", "w"}, "a", "log time"), sheetAdd})

func (k *timeKind) keys() *sheetKeys { return timeKeys }

func (k *timeKind) load(s *sheet) tea.Cmd {
	key, loc := s.key, s.deps.Caps.Location()
	return s.read(&s.loads, func(ctx context.Context, c jira.SessionClient) func(*sheet) tea.Cmd {
		page, err := c.Worklogs(ctx, key)
		var logs []jira.Worklog
		if err == nil {
			logs, err = jira.Collect(ctx, page, worklogLimit)
		}
		var iss jira.Issue
		if err == nil {
			iss, err = c.IssueFields(ctx, key, []string{"timetracking"})
		}
		return func(s *sheet) tea.Cmd {
			if err != nil {
				return s.failed(err)
			}
			rows := make([]sheetRow, 0, len(logs))
			for i := len(logs) - 1; i >= 0; i-- {
				rows = append(rows, worklogRow(&logs[i], loc))
			}
			s.setRows(rows)
			s.note = timeTracking(iss.TimeTracking)
			if s.note == "" {
				s.note = "nothing logged yet"
			}
			return nil
		}
	})
}

func worklogRow(w *jira.Worklog, loc *time.Location) sheetRow {
	text := formatWhen(w.Started, loc) + "  " + duration(int64(w.Spent/time.Second)) + "  " + widget.Sanitize(w.Author.DisplayName)
	if note, _, _ := strings.Cut(strings.TrimSpace(adf.Markdown(w.Comment)), "\n"); note != "" {
		text += "  " + widget.Sanitize(note)
	}
	return sheetRow{text: text, id: w.ID}
}

func (k *timeKind) act(s *sheet, a sheetAct) tea.Cmd {
	if a != sheetAdd {
		return nil
	}
	k.step = 0
	return s.ask("How long? Hours and minutes, like 1h 30m", "", false)
}

func (k *timeKind) changed(*sheet, string) tea.Cmd { return nil }

func (k *timeKind) answered(s *sheet, text string, _ *sheetRow) tea.Cmd {
	var err error
	switch k.step {
	case 0:
		if k.spent, err = parseSpent(text); err == nil {
			k.step++
			return s.ask("When did it start? YYYY-MM-DD, with HH:MM if you like; empty is now", "", false)
		}
	case 1:
		if k.started, err = parseStarted(text, s.deps.Now(), s.deps.Caps.Location()); err == nil {
			k.step++
			return s.ask("What was it? Optional", "", false)
		}
	default:
		in := jira.WorklogInput{Spent: k.spent, Started: k.started}
		if text != "" {
			in.Comment = adf.NewDoc(adf.NewNode("paragraph", adf.NewText(text)))
		}
		key, said := s.key, "logged "+duration(int64(k.spent/time.Second))+" on "+s.key
		s.endAsk()
		return s.write(func(ctx context.Context, c jira.SessionClient) (func(*sheet) tea.Cmd, error) {
			_, err := c.AddWorklog(ctx, key, in)
			return func(s *sheet) tea.Cmd {
				return tea.Batch(k.load(s), s.changedIssue(), kernel.Status(said))
			}, err
		})
	}
	s.problem = err.Error()
	return nil
}

// A day or a week is refused: its length is the site's working day, which this
// client never reads.
func parseSpent(text string) (time.Duration, error) {
	t := strings.ToLower(strings.Join(strings.Fields(text), ""))
	switch {
	case t == "":
		return 0, errors.New("say how long, like 1h 30m")
	case strings.ContainsAny(t, "dw"):
		return 0, errors.New("give hours and minutes; a day here is whatever the site's working day is")
	}
	d, err := time.ParseDuration(t)
	switch {
	case err != nil:
		return 0, errors.New("that is not a length of time; try 1h 30m")
	case d < time.Minute:
		return 0, errors.New("log at least a minute")
	}
	return d, nil
}

func parseStarted(text string, now time.Time, loc *time.Location) (time.Time, error) {
	now = now.In(loc)
	if text == "" {
		return now, nil
	}
	at, err := time.ParseInLocation("2006-01-02 15:04", text, loc)
	if err != nil {
		day, dayErr := time.ParseInLocation(time.DateOnly, text, loc)
		if dayErr != nil {
			return time.Time{}, errors.New("that is not a date; try " + now.Format(time.DateOnly))
		}
		at = time.Date(day.Year(), day.Month(), day.Day(), now.Hour(), now.Minute(), 0, 0, loc)
	}
	if at.After(now) {
		return time.Time{}, errors.New("that has not happened yet")
	}
	return at, nil
}
