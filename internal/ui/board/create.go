package board

import (
	"context"
	"slices"

	tea "charm.land/bubbletea/v2"

	appboard "github.com/varijkapil13/saral/internal/app/board"
	"github.com/varijkapil13/saral/internal/ui/form"
	"github.com/varijkapil13/saral/internal/ui/issue"
	"github.com/varijkapil13/saral/internal/ui/kernel"
	"github.com/varijkapil13/saral/internal/ui/widget"
	"github.com/varijkapil13/saral/pkg/jira"
)

// creation is the column a create was started from, kept until the form says
// what it made.
type creation struct {
	board  int64
	col    int
	name   string
	sprint jira.Sprint
}

// landedMsg is how far a created issue got towards the column it was made in.
type landedMsg struct {
	gen    int
	key    string
	issue  jira.Issue
	read   bool
	step   appboard.LandStep
	err    error
	noMove bool
	screen *jira.Transition
}

// startCreate opens the create form for the column under the cursor: in this
// board's project unless the card there says otherwise, as the kind of issue
// that card is, and for the sprint on screen.
func (m *Model) startCreate() tea.Cmd {
	if m.deps.Jira == nil {
		return kernel.Warn("there is no Jira connection in this session")
	}
	if !m.drawable() {
		return kernel.Warn("there is no board on screen to create an issue in")
	}
	if m.moving || m.card != nil || m.bulk != nil {
		return nil
	}
	col := min(max(m.curCol, 0), len(m.plan.Columns)-1)
	opts := []form.Option{form.WithReport(m.addr)}
	if iss := m.issueAt(m.curCol, m.curRow); iss != nil {
		opts = append(opts, form.WithProject(iss.Project.Key))
		if iss.Type.ID != "" && !iss.Type.Subtask {
			opts = append(opts, form.WithIssueType(iss.Type.ID))
		}
	}
	if m.sprint.ID != 0 {
		opts = append(opts, form.WithSprint(m.sprint))
	}
	m.creating = &creation{board: m.plan.BoardID, col: col, name: m.plan.Columns[col].Name, sprint: m.sprint}
	return kernel.Push(form.ViewID, "New issue", form.NewWith(m.deps, opts...))
}

// created lands what the form made in the column it was made from: into the
// sprint on screen first, since the site creates every issue in the backlog,
// then through the workflow move into the column when it was created in
// another.
func (m *Model) created(msg form.CreatedMsg) tea.Cmd {
	c := m.creating
	m.creating = nil
	if c == nil || m.deps.Jira == nil || msg.Issue.Key == "" {
		return nil
	}
	m.stopLanding()
	m.landGen++
	ctx, cancel := context.WithCancel(context.Background())
	m.landStop = cancel
	job := landing{
		created: msg.Issue, sprint: msg.Sprint.ID, col: c.col, plan: m.plan, fields: slices.Clone(m.fields),
		onBoard: c.board == m.plan.BoardID,
	}
	m.landingTo = c
	return kernel.Reply(withCancel(cancel, job.run(ctx, m.deps.Jira, m.landGen)), m.addr)
}

func (m *Model) stopLanding() {
	if m.landStop != nil {
		m.landStop()
		m.landStop = nil
	}
}

type landing struct {
	created jira.Issue
	sprint  int64
	col     int
	plan    appboard.Plan
	fields  []string
	onBoard bool
}

func (l landing) run(ctx context.Context, client jira.SessionClient, gen int) tea.Cmd {
	return func() tea.Msg {
		got := appboard.Land(ctx, client, appboard.Landing{
			Created: l.created, Sprint: l.sprint, Col: l.col, Plan: l.plan, Fields: l.fields, OnBoard: l.onBoard,
		})
		return landedMsg{
			gen: gen, key: l.created.Key, issue: got.Issue, read: got.Read, step: got.Step, err: got.Err,
			noMove: got.NoMove, screen: got.Screen,
		}
	}
}

func (m *Model) tookLanding(msg landedMsg) tea.Cmd {
	if msg.gen != m.landGen || m.landingTo == nil {
		return nil
	}
	c := m.landingTo
	m.landingTo, m.landStop = nil, nil
	key, name := msg.key, widget.Sanitize(c.name)
	if msg.err != nil {
		reason, _ := jira.Reason(msg.err)
		text := key + " was created, but "
		switch msg.step {
		case appboard.LandSprint:
			text += "not moved into " + widget.Sanitize(c.sprint.Name) + ", so it waits in the backlog: " + reason
		case appboard.LandMove:
			text += "not moved into " + name + ": " + reason
		case appboard.LandRead:
			text += "reading it back failed, so it is not on the board yet: " + reason
		}
		return func() tea.Msg { return kernel.StatusMsg{Text: text, Level: kernel.LevelError} }
	}
	if c.board != m.plan.BoardID {
		return kernel.Status(key + " created")
	}
	var said tea.Cmd
	switch {
	case msg.screen != nil:
		said = tea.Batch(
			kernel.Status(key+" was created; "+msg.screen.Name+" needs more than a column, so it is being asked for"),
			kernel.Push(issue.ViewID, key, issue.New(m.deps, msg.issue, issue.WithTransition(msg.screen.ID))),
		)
	case msg.noMove:
		where := "which this board maps to no column"
		if at, mapped := m.plan.ColumnOf(msg.issue.Status.ID); mapped {
			where = "in " + widget.Sanitize(m.plan.Columns[at].Name)
		}
		said = kernel.Warn(key + " was created " + where + "; no workflow move takes it from " +
			widget.Sanitize(msg.issue.Status.Name) + " into " + name)
	default:
		said = kernel.Status(key + " created in " + name)
	}
	if !msg.read {
		return said
	}
	m.keepLanded(msg.issue)
	return tea.Batch(said, stored(m.pagePut([]jira.Issue{msg.issue}, false)))
}

// keepLanded puts a created issue on the board, and remembers it so a read
// the site's index has not caught up with yet does not take it off again.
func (m *Model) keepLanded(iss jira.Issue) {
	if at := m.indexOf(iss.Key); at >= 0 {
		m.issues[at] = iss
	} else {
		m.issues = append(m.issues, iss)
	}
	if !slices.Contains(m.landed, iss.Key) {
		m.landed = append(m.landed, iss.Key)
	}
	m.place()
	m.forget()
	m.restore(iss.Key)
}

// carryLanded keeps what this board created and a fresh first page lacks, and
// lets go of what the page has.
func (m *Model) carryLanded(was []jira.Issue) {
	if len(m.landed) == 0 {
		return
	}
	kept := m.landed[:0]
	for _, key := range m.landed {
		if m.indexOf(key) >= 0 {
			continue
		}
		at := slices.IndexFunc(was, func(iss jira.Issue) bool { return iss.Key == key })
		if at < 0 {
			continue
		}
		m.issues = append(m.issues, was[at])
		kept = append(kept, key)
	}
	m.landed = kept
}

// dropArrived takes a carried issue off the board when a later page of the read
// brings it, so the page's own copy is the one drawn.
func (m *Model) dropArrived(page []jira.Issue) {
	if len(m.landed) == 0 {
		return
	}
	kept := m.landed[:0]
	for _, key := range m.landed {
		if !slices.ContainsFunc(page, func(iss jira.Issue) bool { return iss.Key == key }) {
			kept = append(kept, key)
			continue
		}
		if at := m.indexOf(key); at >= 0 {
			m.issues = slices.Delete(m.issues, at, at+1)
		}
	}
	m.landed = kept
}
