package backlog

import (
	"context"
	"slices"
	"strconv"
	"strings"

	tea "charm.land/bubbletea/v2"

	"github.com/varijkapil13/saral/internal/app"
	"github.com/varijkapil13/saral/internal/ui/form"
	"github.com/varijkapil13/saral/internal/ui/kernel"
	"github.com/varijkapil13/saral/pkg/jira"
)

// madeLimit bounds how many created issues are carried over a re-read that
// has not caught up with them. The index trails a write by seconds, so only
// the last few creates can still be missing from it.
const madeLimit = 16

// CreateMsg opens the create form for the section under the cursor. It is
// exported for the same reason NextBoardMsg is.
type CreateMsg struct{}

// createdMsg is a created issue once it has been moved into the sprint it was
// created for and read back.
type createdMsg struct {
	board   int64
	key     string
	sprint  jira.Sprint
	moveErr error
	read    bool
	issue   jira.Issue
	readErr error
}

func (m *Model) createRefused() string {
	switch {
	case m.deps.Jira == nil || m.mover == nil:
		return "there is no Jira connection in this session to create an issue with"
	case m.busy():
		return "this move is still going; an issue can be created once it has finished"
	case m.mode != browsing:
		return "finish what is on screen first; an issue can be created once it is answered"
	case !m.loaded || len(m.boards) == 0 || m.config.BoardID == 0:
		return "this backlog has no board loaded yet, so there is no section to create an issue in"
	}
	return ""
}

// startCreate opens the form already answered with what the section under the
// cursor knows: the project and type of the issue there, and the sprint.
func (m *Model) startCreate() tea.Cmd {
	if refused := m.createRefused(); refused != "" {
		return kernel.Warn(refused)
	}
	project, issueType := m.deps.Project, ""
	if iss := m.issueAt(m.cursor); iss != nil {
		if key := strings.TrimSpace(iss.Project.Key); key != "" {
			project = key
		}
		if !iss.Type.Subtask {
			issueType = iss.Type.ID
		}
	}
	opts := []form.Option{form.WithProject(project), form.WithReport(m.addr)}
	if issueType != "" {
		opts = append(opts, form.WithIssueType(issueType))
	}
	if sp, ok := m.sprintUnderCursor(); ok {
		opts = append(opts, form.WithSprint(sp))
	}
	m.createOn = m.config.BoardID
	return kernel.Push(form.ViewID, "New issue", form.NewWith(m.deps, opts...))
}

func (m *Model) sprintUnderCursor() (jira.Sprint, bool) {
	if m.cursor < 0 || m.cursor >= len(m.rows) {
		return jira.Sprint{}, false
	}
	id := m.groups[m.rows[m.cursor].group].id
	if id == 0 {
		return jira.Sprint{}, false
	}
	at := slices.IndexFunc(m.sprints, func(sp jira.Sprint) bool { return sp.ID == id })
	if at < 0 {
		return jira.Sprint{}, false
	}
	return m.sprints[at], true
}

// created settles what the form reported. The site creates every issue in the
// backlog, so one meant for a sprint is moved there, and it is read back only
// when the board it was started on is still the one on screen.
func (m *Model) created(msg form.CreatedMsg) tea.Cmd {
	key := strings.TrimSpace(msg.Issue.Key)
	if key == "" || m.mover == nil {
		return nil
	}
	board := m.createOn
	var reader jira.IssueReader
	if board != 0 && board == m.config.BoardID && m.deps.Jira != nil {
		reader = m.deps.Jira
	}
	return kernel.Reply(settle(m.mover, reader, m.search, m.fieldIDs, projectionOf(m.field, m.config),
		board, key, msg.Sprint), m.addr)
}

// settle runs on a context of its own: a re-read of the board cancels the read
// in flight, and the move into the sprint is what the user asked for.
func settle(mover jira.SprintManager, reader jira.IssueReader, search *app.Search, fields []string,
	want app.Projection, board int64, key string, sp jira.Sprint,
) tea.Cmd {
	return func() tea.Msg {
		ctx := context.Background()
		out := createdMsg{board: board, key: key, sprint: sp}
		if sp.ID != 0 {
			out.moveErr = mover.MoveToSprint(ctx, sp.ID, []string{key})
		}
		if reader == nil {
			return out
		}
		out.read = true
		if len(fields) == 0 && search != nil {
			wanted, err := search.Resolve(ctx, want)
			if err != nil {
				out.readErr = err
				return out
			}
			fields = wanted.IDs
		}
		out.issue, out.readErr = reader.IssueFields(ctx, key, fields)
		return out
	}
}

func (m *Model) settled(msg createdMsg) tea.Cmd {
	moved := msg.sprint.ID != 0 && msg.moveErr == nil
	where := backlogName
	if moved {
		where = msg.sprint.Name
	}
	said := ""
	if msg.moveErr != nil {
		reason, _ := jira.Reason(msg.moveErr)
		said = msg.key + " was created but is still in the backlog: " + reason
	}
	if !msg.read || msg.board != m.config.BoardID || !m.loaded {
		if said != "" {
			return failSay(said)
		}
		return kernel.Status(msg.key + " created in " + where + ", on a board this backlog is no longer showing")
	}
	if msg.readErr != nil {
		reason, _ := jira.Reason(msg.readErr)
		if said != "" {
			return failSay(said + "; reading it back failed too: " + reason)
		}
		return failSay(msg.key + " was created in " + where + " but reading it back failed: " + reason)
	}
	iss := msg.issue
	if moved && m.field.ID != "" {
		iss.Fields = iss.Fields.With(m.field, jira.FieldValue{
			Kind:    jira.KindOptions,
			Options: []jira.Option{{ID: strconv.FormatInt(msg.sprint.ID, 10), Label: msg.sprint.Name}},
		})
	}
	m.insertMade(iss)
	kept := stored(m.movedPut([]string{iss.Key}))
	if said != "" {
		return tea.Batch(kept, failSay(said))
	}
	return tea.Batch(kept, kernel.Status(msg.key+" created in "+where))
}

func failSay(text string) tea.Cmd {
	return func() tea.Msg { return kernel.StatusMsg{Text: text, Level: kernel.LevelError} }
}

func (m *Model) insertMade(iss jira.Issue) {
	if at, held := m.byKey[iss.Key]; held {
		m.issues[at] = iss
	} else {
		m.issues = append(m.issues, iss)
		m.byKey[iss.Key] = len(m.issues) - 1
	}
	m.made = slices.DeleteFunc(m.made, func(key string) bool { return key == iss.Key })
	m.made = append(m.made, iss.Key)
	if len(m.made) > madeLimit {
		m.made = slices.Delete(m.made, 0, len(m.made)-madeLimit)
	}
	m.relayout()
	m.regroup()
	m.restore(iss.Key)
}

// carryMade is a fresh first page with the issues this view created and the
// page does not have yet put back on the end. A key the page has is dropped
// from made: the index has caught up with it.
func (m *Model) carryMade(board int64, items []jira.Issue) []jira.Issue {
	if len(m.made) == 0 {
		return items
	}
	if board != m.config.BoardID {
		m.made = nil
		return items
	}
	out, kept := items, m.made[:0]
	for _, key := range m.made {
		at, held := m.byKey[key]
		if !held || slices.ContainsFunc(items, func(iss jira.Issue) bool { return iss.Key == key }) {
			continue
		}
		if len(kept) == 0 {
			out = slices.Clone(items)
		}
		out = append(out, m.issues[at])
		kept = append(kept, key)
	}
	m.made = kept
	return out
}

// dropMade takes out of the issues in hand every carried one a later page
// brings, so the page's own copy is the one kept.
func (m *Model) dropMade(items []jira.Issue) []jira.Issue {
	if len(m.made) == 0 {
		return m.issues
	}
	arrived := func(key string) bool {
		return slices.ContainsFunc(items, func(iss jira.Issue) bool { return iss.Key == key })
	}
	if !slices.ContainsFunc(m.made, arrived) {
		return m.issues
	}
	gone := make(map[string]bool, len(m.made))
	m.made = slices.DeleteFunc(m.made, func(key string) bool {
		if arrived(key) {
			gone[key] = true
			return true
		}
		return false
	})
	return slices.DeleteFunc(slices.Clone(m.issues), func(iss jira.Issue) bool { return gone[iss.Key] })
}
