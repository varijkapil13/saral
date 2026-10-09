package sprint

import (
	"context"
	"strings"

	"github.com/varijkapil13/saral/pkg/jira"
)

// IssueCap bounds the walk over one sprint's issues. A sprint holding more is
// counted as far as the cap and says so.
const IssueCap = 1000

// IssueReader is what a progress read needs: the board's columns and
// estimation field, and the board's view of a sprint.
type IssueReader interface {
	jira.BoardReader
	jira.SprintIssueReader
}

// Progress is how much of one sprint is done, by the board's own measure: an
// issue is done when it sits in the board's last column with a status mapped to
// it (docs/API-NOTES.md), and points are the board's estimation field.
type Progress struct {
	Total, Done        int
	Points, DonePoints float64
	Estimated          bool
	// Open are the keys not done, in the board's order. They are what a
	// completion moves somewhere other than the backlog.
	Open   []string
	Capped bool
}

// Count is one sprint's progress, or why it could not be read.
type Count struct {
	Progress
	Err error
}

// ReadAllProgress reads each sprint in turn. A refusal on one is kept on that
// sprint rather than failing the others; the error is the context's, once it
// is cancelled.
func ReadAllProgress(ctx context.Context, r IssueReader, sprints []jira.Sprint) (map[int64]Count, error) {
	out := make(map[int64]Count, len(sprints))
	for _, sp := range sprints {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		p, err := ReadProgress(ctx, r, sp.BoardID, sp.ID)
		out[sp.ID] = Count{Progress: p, Err: err}
	}
	return out, nil
}

// ReadProgress counts one sprint's issues against its board.
func ReadProgress(ctx context.Context, r IssueReader, boardID, sprintID int64) (Progress, error) {
	cfg, err := r.BoardConfig(ctx, boardID)
	if err != nil {
		return Progress{}, err
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
		return Progress{}, err
	}
	p := Progress{Estimated: est.ID != ""}
	for {
		for i := range page.Items {
			if p.Total == IssueCap {
				p.Capped = true
				return p, nil
			}
			p.count(&page.Items[i], done, est)
		}
		if !page.HasMore() {
			return p, nil
		}
		if page, err = page.Next(ctx); err != nil {
			return Progress{}, err
		}
	}
}

func (p *Progress) count(iss *jira.Issue, done map[string]bool, est jira.FieldRef) {
	p.Total++
	finished := done[iss.Status.ID]
	if finished {
		p.Done++
	} else {
		p.Open = append(p.Open, iss.Key)
	}
	if est.ID == "" {
		return
	}
	if n, ok := iss.Fields.Number(est); ok {
		p.Points += n
		if finished {
			p.DonePoints += n
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
