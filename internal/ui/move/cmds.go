package move

import (
	"context"
	"errors"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/varijkapil13/saral/internal/app"
	"github.com/varijkapil13/saral/pkg/jira"
)

// candidatesLimit is how many issues the project suggestions read. It is a page
// and not a walk: the answer is a handful of keys, and paging further only finds
// projects the account has not touched recently.
const candidatesLimit = 50

// pollCap is as long as the wizard ever waits between two questions about a
// task. A bulk move over a thousand issues takes minutes, and a poll a minute
// apart would report it finished long after it was.
const pollCap = 4 * time.Second

type candidatesMsg struct {
	gen  int
	keys []string
}

// vocabularyMsg carries the target project's issue types and the statuses each
// one's workflow reaches. It is also what proves the project key is real: a key
// this token cannot see answers with a refusal instead.
type vocabularyMsg struct {
	gen     int
	project string
	types   []jira.IssueTypeStatuses
}

type schemaMsg struct {
	gen    int
	schema jira.Schema
}

// submittedMsg carries the task the queue took the move under. The ref is
// carried whole, because the endpoint to poll is part of it and cannot be built
// from the id.
type submittedMsg struct {
	gen int
	ref jira.TaskRef
}

type taskMsg struct {
	gen    int
	status jira.TaskStatus
}

// failedMsg is a read or a write that brought nothing back. The error travels
// whole so that a refusal reaches the user in the words the site used, and at
// says which step was asking: a poll that comes back rate limited is a pause and
// every other refusal is the end of what was being asked.
type failedMsg struct {
	gen int
	at  step
	err error
}

// candidates reads the projects behind the account's own recent issues, which is
// the only way to offer a target: /project/search is not on the port, so nothing
// here can enumerate projects and a page of issues is what is left.
func candidates(ctx context.Context, client app.SearchClient, gen int) tea.Cmd {
	return func() tea.Msg {
		search := app.NewSearch(client)
		projection := app.Projection{Name: "move target", IDs: []string{"project"}}
		// The account's own work first, then anything this token can see at all:
		// a session whose user has nothing assigned would otherwise be offered
		// nothing and have to type a key from memory.
		for _, jql := range []string{"assignee = currentUser() ORDER BY updated DESC", "ORDER BY updated DESC"} {
			result, err := search.Run(ctx, app.Request{
				JQL:        jql,
				Projection: projection,
				MaxResults: candidatesLimit,
			})
			if err != nil {
				return failedMsg{gen: gen, at: stepTarget, err: err}
			}
			if keys := distinctProjects(result.Page.Items); len(keys) > 0 {
				return candidatesMsg{gen: gen, keys: keys}
			}
		}
		return candidatesMsg{gen: gen}
	}
}

// distinctProjects keeps the order the issues came back in, which is the order
// the query sorted them by and therefore the order worth offering.
func distinctProjects(issues []jira.Issue) []string {
	seen := make(map[string]bool, len(issues))
	out := make([]string, 0, 4)
	for i := range issues {
		key := issues[i].Project.Key
		if key == "" || seen[key] {
			continue
		}
		seen[key] = true
		out = append(out, key)
	}
	return out
}

func vocabulary(ctx context.Context, vocab jira.FilterVocabulary, project string, gen int) tea.Cmd {
	return func() tea.Msg {
		types, err := vocab.IssueTypeStatuses(ctx, project)
		if err != nil {
			return failedMsg{gen: gen, at: stepTarget, err: err}
		}
		return vocabularyMsg{gen: gen, project: project, types: types}
	}
}

// schemaOf reads what the target insists on for one issue type. Which fields are
// mandatory is site configuration, so it is asked for the project and type that
// were actually chosen and never assumed.
func schemaOf(ctx context.Context, reader jira.SchemaReader, project, typeID string, gen int) tea.Cmd {
	return func() tea.Msg {
		schema, err := reader.CreateMeta(ctx, project, typeID)
		if err != nil {
			return failedMsg{gen: gen, at: stepType, err: err}
		}
		return schemaMsg{gen: gen, schema: schema}
	}
}

// submit hands the move to the bulk queue. What comes back is a task and not an
// outcome: the issues have not moved when this returns.
func submit(ctx context.Context, mover jira.Relocator, in jira.MoveRequest, gen int) tea.Cmd {
	return func() tea.Msg {
		ref, err := mover.BulkMove(ctx, in)
		if err != nil {
			return failedMsg{gen: gen, at: stepConfirm, err: err}
		}
		return submittedMsg{gen: gen, ref: ref}
	}
}

// poll asks the queue about the task after waiting, which is the whole of the
// backoff. The ref is passed back to the port untouched: a bulk move is followed
// on its own queue, and an id put into the generic task path answers a body that
// does not decode as this one.
func poll(ctx context.Context, watcher jira.TaskWatcher, ref jira.TaskRef, wait waiter, after time.Duration, gen int) tea.Cmd {
	return func() tea.Msg {
		if err := wait(ctx, after); err != nil {
			return failedMsg{gen: gen, at: stepRunning, err: err}
		}
		status, err := watcher.Task(ctx, ref)
		if err != nil {
			return failedMsg{gen: gen, at: stepRunning, err: err}
		}
		return taskMsg{gen: gen, status: status}
	}
}

// waiter is how long the wizard waits before asking the queue again. It is a
// field on the model rather than a call to time.After so that a test can hold
// the backoff to account without spending it.
type waiter func(ctx context.Context, d time.Duration) error

// sleep waits, and gives up the moment the view is closed.
func sleep(ctx context.Context, d time.Duration) error {
	if d <= 0 {
		return ctx.Err()
	}
	timer := time.NewTimer(d)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}

// backoff is how long to wait before the next question about a task. The first
// is asked at once, because a small move is finished by the time a fixed delay
// would have elapsed, and the wait then doubles to a ceiling.
func backoff(attempt int) time.Duration {
	if attempt <= 0 {
		return 0
	}
	d := 250 * time.Millisecond
	for range attempt - 1 {
		d *= 2
		if d >= pollCap {
			return pollCap
		}
	}
	return d
}

// held is how long a rate limit asks for, which is a pause rather than a
// failure: the queue is still working and the wizard is only being told to stop
// asking so often.
func held(err error, attempt int) (time.Duration, bool) {
	var limited *jira.RateLimitError
	if !errors.As(err, &limited) {
		return 0, false
	}
	if limited.RetryAfter > 0 {
		return limited.RetryAfter, true
	}
	return backoff(attempt + 1), true
}
