package sprint

import (
	"context"
	"strconv"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/varijkapil13/saral/pkg/jira"
)

// issueCap bounds the walk over one sprint's issues. A sprint holding more is
// counted as far as the cap and says so.
const issueCap = 1000

// progress is how much of one sprint is done, by the board's own measure: an
// issue is done when it sits in the board's last column with a status mapped to
// it (docs/API-NOTES.md), and points are the board's estimation field.
type progress struct {
	total, done        int
	points, donePoints float64
	estimated          bool
	// open are the keys not done, in the board's order. They are what a
	// completion moves somewhere other than the backlog.
	open   []string
	capped bool
	err    error
}

// progressMsg is every running sprint's progress as one pass read it.
type progressMsg struct {
	gen int
	of  map[int64]progress
}

// issueReader is what a progress read needs: the board's columns and estimation
// field, and the board's view of a sprint.
type issueReader interface {
	jira.BoardReader
	jira.SprintIssueReader
}

// readAllProgress reads each running sprint in turn. A refusal on one is kept
// on that sprint rather than failing the others.
func readAllProgress(ctx context.Context, r issueReader, sprints []jira.Sprint, gen int) tea.Cmd {
	return func() tea.Msg {
		out := make(map[int64]progress, len(sprints))
		for _, sp := range sprints {
			if ctx.Err() != nil {
				return nil
			}
			p, err := readProgress(ctx, r, sp.BoardID, sp.ID)
			p.err = err
			out[sp.ID] = p
		}
		return progressMsg{gen: gen, of: out}
	}
}

func readProgress(ctx context.Context, r issueReader, boardID, sprintID int64) (progress, error) {
	cfg, err := r.BoardConfig(ctx, boardID)
	if err != nil {
		return progress{}, err
	}
	done := doneStatuses(cfg)
	fields := []string{"status"}
	var est jira.FieldRef
	if cfg.Estimation != nil && cfg.Estimation.Type == jira.EstimationField && strings.TrimSpace(cfg.Estimation.Field.ID) != "" {
		est = cfg.Estimation.Field
		fields = append(fields, est.ID)
	}
	page, err := r.SprintIssues(ctx, boardID, sprintID, jira.BoardQuery{Fields: fields})
	if err != nil {
		return progress{}, err
	}
	p := progress{estimated: est.ID != ""}
	for {
		for i := range page.Items {
			if p.total == issueCap {
				p.capped = true
				return p, nil
			}
			p.count(&page.Items[i], done, est)
		}
		if !page.HasMore() {
			return p, nil
		}
		if page, err = page.Next(ctx); err != nil {
			return progress{}, err
		}
	}
}

func (p *progress) count(iss *jira.Issue, done map[string]bool, est jira.FieldRef) {
	p.total++
	finished := done[iss.Status.ID]
	if finished {
		p.done++
	} else {
		p.open = append(p.open, iss.Key)
	}
	if est.ID == "" {
		return
	}
	if n, ok := iss.Fields.Number(est); ok {
		p.points += n
		if finished {
			p.donePoints += n
		}
	}
}

// doneStatuses is the board's last column that has a status in it. A trailing
// column with nothing mapped is a column nothing can be in.
func doneStatuses(cfg jira.BoardConfig) map[string]bool {
	for i := len(cfg.Columns) - 1; i >= 0; i-- {
		if ids := cfg.Columns[i].StatusIDs; len(ids) > 0 {
			out := make(map[string]bool, len(ids))
			for _, id := range ids {
				out[id] = true
			}
			return out
		}
	}
	return nil
}

// words is the progress line for a running sprint.
func (p progress) words() string {
	var b strings.Builder
	b.WriteString(strconv.Itoa(p.done))
	b.WriteString(" of ")
	b.WriteString(strconv.Itoa(p.total))
	if p.total == 1 {
		b.WriteString(" issue done")
	} else {
		b.WriteString(" issues done")
	}
	if p.estimated {
		b.WriteString(" · ")
		b.WriteString(points(p.donePoints))
		b.WriteString(" of ")
		b.WriteString(points(p.points))
		b.WriteString(" points done")
	}
	if p.capped {
		b.WriteString(" (the first " + strconv.Itoa(issueCap) + " issues only)")
	}
	return b.String()
}

func points(n float64) string { return strconv.FormatFloat(n, 'f', -1, 64) }

// daysLeft counts calendar days in the account's zone, so a sprint that ends
// tonight reads "ends today" wherever the machine is.
func daysLeft(end, now time.Time, loc *time.Location) string {
	e, n := jira.DateOf(end.In(loc)), jira.DateOf(now.In(loc))
	days := int(time.Date(e.Year, e.Month, e.Day, 0, 0, 0, 0, time.UTC).
		Sub(time.Date(n.Year, n.Month, n.Day, 0, 0, 0, 0, time.UTC)).Hours() / 24)
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
