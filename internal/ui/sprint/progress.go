package sprint

import (
	"context"
	"strconv"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"

	appsprint "github.com/varijkapil13/saral/internal/app/sprint"
	"github.com/varijkapil13/saral/pkg/jira"
)

// progressMsg is every running sprint's progress as one pass read it.
type progressMsg struct {
	gen int
	of  map[int64]appsprint.Count
}

func readAllProgress(ctx context.Context, r appsprint.IssueReader, sprints []jira.Sprint, gen int) tea.Cmd {
	return func() tea.Msg {
		of, err := appsprint.ReadAllProgress(ctx, r, sprints)
		if err != nil {
			return nil
		}
		return progressMsg{gen: gen, of: of}
	}
}

// progressWords is the progress line for a running sprint.
func progressWords(p appsprint.Progress) string {
	var b strings.Builder
	b.WriteString(strconv.Itoa(p.Done))
	b.WriteString(" of ")
	b.WriteString(strconv.Itoa(p.Total))
	if p.Total == 1 {
		b.WriteString(" issue done")
	} else {
		b.WriteString(" issues done")
	}
	if p.Estimated {
		b.WriteString(" · ")
		b.WriteString(points(p.DonePoints))
		b.WriteString(" of ")
		b.WriteString(points(p.Points))
		b.WriteString(" points done")
	}
	if p.Capped {
		b.WriteString(" (the first " + strconv.Itoa(appsprint.IssueCap) + " issues only)")
	}
	return b.String()
}

func points(n float64) string { return strconv.FormatFloat(n, 'f', -1, 64) }

// daysLeft counts calendar days in the account's zone, so a sprint that ends
// tonight reads "ends today" wherever the machine is.
func daysLeft(end, now time.Time, loc *time.Location) string {
	days := appsprint.DaysLeft(end, now, loc)
	switch {
	case days > 1:
		return strconv.Itoa(days) + " days left"
	case days == 1:
		return "1 day left"
	case days == 0:
		return "ends today"
	case days == -1:
		return "ended yesterday and is still running"
	}
	return "ended " + strconv.Itoa(-days) + " days ago and is still running"
}
