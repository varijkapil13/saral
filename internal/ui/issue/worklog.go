package issue

import (
	"context"
	"errors"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"

	appissue "github.com/varijkapil13/saral/internal/app/issue"
	"github.com/varijkapil13/saral/internal/ui/kernel"
	"github.com/varijkapil13/saral/internal/ui/widget"
	"github.com/varijkapil13/saral/pkg/adf"
	"github.com/varijkapil13/saral/pkg/jira"
)

type timeKind struct {
	step    int
	spent   time.Duration
	started time.Time
}

var timeKeys = newSheetKeys(sheetBind{kernel.Canon(kernel.ActAdd, "log time"), sheetAdd})

func (k *timeKind) keys() *sheetKeys { return timeKeys }

func (k *timeKind) load(s *sheet) tea.Cmd {
	key, loc := s.key, s.deps.Caps.Location()
	return s.read(&s.loads, func(ctx context.Context, c jira.SessionClient) func(*sheet) tea.Cmd {
		logs, tracking, err := appissue.TimeLogged(ctx, c, key)
		return func(s *sheet) tea.Cmd {
			if err != nil {
				return s.failed(err)
			}
			rows := make([]sheetRow, 0, len(logs))
			for i := len(logs) - 1; i >= 0; i-- {
				rows = append(rows, worklogRow(&logs[i], loc))
			}
			s.setRows(rows)
			s.note = timeTracking(tracking)
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
	now, loc := s.deps.Now(), s.deps.Caps.Location()
	switch k.step {
	case 0:
		if k.spent, err = appissue.ParseSpent(text); err == nil {
			k.step++
			return s.ask("When did it start? YYYY-MM-DD, with HH:MM if you like; empty is now", "", false)
		}
	case 1:
		if k.started, err = appissue.ParseStarted(text, now, loc); err == nil {
			k.step++
			return s.ask("What was it? Optional", "", false)
		}
	default:
		key, spent, started, said := s.key, k.spent, k.started, "logged "+duration(int64(k.spent/time.Second))+" on "+s.key
		s.endAsk()
		return s.write(func(ctx context.Context, c jira.SessionClient) (func(*sheet) tea.Cmd, error) {
			err := appissue.LogWork(ctx, c, key, spent, started, text)
			return func(s *sheet) tea.Cmd {
				return tea.Batch(k.load(s), s.changedIssue(), kernel.Status(said))
			}, err
		})
	}
	s.problem = worklogProblem(err, now.In(loc))
	return nil
}

func worklogProblem(err error, now time.Time) string {
	switch {
	case errors.Is(err, appissue.ErrNoLength):
		return "say how long, like 1h 30m"
	case errors.Is(err, appissue.ErrLengthInDays):
		return "give hours and minutes; a day here is whatever the site's working day is"
	case errors.Is(err, appissue.ErrNotLength):
		return "that is not a length of time; try 1h 30m"
	case errors.Is(err, appissue.ErrUnderMinute):
		return "log at least a minute"
	case errors.Is(err, appissue.ErrNotDate):
		return "that is not a date; try " + now.Format(time.DateOnly)
	case errors.Is(err, appissue.ErrNotYet):
		return "that has not happened yet"
	default:
		return err.Error()
	}
}
