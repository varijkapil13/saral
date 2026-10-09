package release

import (
	"context"
	"errors"
	"slices"
	"strconv"

	"github.com/varijkapil13/saral/pkg/jira"
)

const (
	// Cap is the most issues one assignment reads and writes. A query matching
	// more is refused rather than cut short: the preview has to be the whole of
	// what will change.
	Cap = 1000
	// Chunk is how many issues one step of an assignment edits before the
	// caller hears how it went. There is no bulk edit in the port, so each is
	// a write of its own.
	Chunk = 25
)

// ErrTooMany is a query matching more than one assignment will write.
var ErrTooMany = &jira.ValidationError{Messages: []string{
	"the query matches more than " + strconv.Itoa(Cap) + " issues; narrow it and ask again",
}}

// Matches is what a query matched, split into the issues the write will
// change and how many already are the way it would leave them.
type Matches struct {
	Todo    []jira.Issue
	Skipped int
}

// ReadMatches runs the query for the one field the write turns on.
func ReadMatches(ctx context.Context, s jira.Searcher, jql, versionID string, remove bool) (Matches, error) {
	page, err := s.Search(ctx, jira.Query{JQL: jql, Fields: []string{"summary", "fixVersions"}})
	if err != nil {
		return Matches{}, err
	}
	var out Matches
	seen := 0
	for {
		for i := range page.Items {
			seen++
			if seen > Cap {
				return Matches{}, ErrTooMany
			}
			iss := page.Items[i]
			carries := slices.ContainsFunc(iss.FixVersions, func(v jira.Version) bool { return v.ID == versionID })
			if carries != remove {
				out.Skipped++
				continue
			}
			out.Todo = append(out.Todo, iss)
		}
		if !page.HasMore() {
			return out, nil
		}
		if page, err = page.Next(ctx); err != nil {
			return Matches{}, err
		}
	}
}

// Patch is the write: an add or a remove of the one version, never the issue's
// whole list, so a version somebody else put on an issue between the preview
// and the write survives it.
func Patch(versionID string, remove bool) jira.IssuePatch {
	if remove {
		return jira.IssuePatch{RemoveFixVersions: []string{versionID}}
	}
	return jira.IssuePatch{AddFixVersions: []string{versionID}}
}

// Failure is one issue the site refused, in its own words.
type Failure struct {
	Key    string
	Reason string
}

// Progress is where an assignment stands after a chunk. Failed and Pending
// are the whole run's so far; Pending is set once the run is Finished, and
// holds the issues never sent.
type Progress struct {
	Done     int
	Failed   []Failure
	Pending  []string
	Finished bool
}

// Assignment writes a patch to a list of issues a chunk at a time. The caller
// drains it with Next until a Progress comes back Finished; a context that
// ends stops the chunk where it is. Next is not safe to call concurrently.
type Assignment struct {
	w     jira.IssueWriter
	keys  []string
	patch jira.IssuePatch

	next    int
	done    int
	failed  []Failure
	pending []string
	over    bool
}

// NewAssignment is a run of patch over keys, nothing sent yet.
func NewAssignment(w jira.IssueWriter, keys []string, patch jira.IssuePatch) *Assignment {
	return &Assignment{w: w, keys: slices.Clone(keys), patch: patch}
}

// Next writes the next chunk issue by issue. A refusal is that issue's and the
// rest of the chunk still goes.
//
// A chunk in which nothing at all landed stops the run: whatever refused every
// issue in it — a permission, a rate limit that outlasted the retries, a
// connection — will refuse the next chunk too, and the rest are reported as not
// sent rather than as refused.
func (a *Assignment) Next(ctx context.Context) Progress {
	if a.over {
		return a.progress()
	}
	end := min(a.next+Chunk, len(a.keys))
	chunk := a.keys[a.next:end]
	a.next = end
	done, failed := 0, 0
	var stopped []string
	for i, key := range chunk {
		if ctx.Err() != nil {
			stopped = chunk[i:]
			break
		}
		err := a.w.UpdateIssue(ctx, key, a.patch)
		if err == nil {
			done++
			continue
		}
		if errors.Is(err, context.Canceled) {
			stopped = chunk[i:]
			break
		}
		reason, _ := jira.Reason(err)
		a.failed = append(a.failed, Failure{Key: key, Reason: reason})
		failed++
	}
	a.done += done
	stuck := done == 0 && failed > 0
	if len(stopped) > 0 || stuck || a.next >= len(a.keys) {
		a.pending = append(a.pending, stopped...)
		a.pending = append(a.pending, a.keys[a.next:]...)
		a.next = len(a.keys)
		a.over = true
	}
	return a.progress()
}

func (a *Assignment) progress() Progress {
	return Progress{
		Done:     a.done,
		Failed:   slices.Clone(a.failed),
		Pending:  slices.Clone(a.pending),
		Finished: a.over,
	}
}
