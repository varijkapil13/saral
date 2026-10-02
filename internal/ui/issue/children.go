package issue

import (
	"context"
	"slices"
	"strconv"

	tea "charm.land/bubbletea/v2"

	"github.com/varijkapil13/saral/internal/app"
	"github.com/varijkapil13/saral/internal/ui/kernel"
	"github.com/varijkapil13/saral/pkg/jira"
)

const (
	childrenPage   = 50
	childrenInline = 8
	childrenGroup  = "Children"
	moreRowID      = "children:more"
)

type childrenMsg struct {
	gen  int
	page jira.Page[jira.Issue]
	err  error
}

type childPatchedMsg struct {
	gen   int
	issue jira.Issue
	err   error
}

func childrenJQL(key string) string { return "parent = " + key + " ORDER BY created ASC" }

func rollup(children []jira.Issue) (n, done int) {
	for i := range children {
		if children[i].Status.Category == jira.CategoryDone {
			done++
		}
	}
	return len(children), done
}

func projectOfKey(key string) string {
	for i := len(key) - 1; i > 0; i-- {
		if key[i] == '-' {
			return key[:i]
		}
	}
	return ""
}

func (m *Model) wantsChildren() bool {
	return m.loadedIssue && m.issue.Type.HierarchyLevel >= 1 && jira.IsIssueRef(m.issue.Key) &&
		m.search != nil && m.deps.Jira != nil
}

func loadChildren(ctx context.Context, search *app.Search, key string, gen int) tea.Cmd {
	return func() tea.Msg {
		res, err := search.Run(ctx, app.Request{
			JQL: childrenJQL(key), Projection: app.ListProjection(), MaxResults: childrenPage,
		})
		return childrenMsg{gen: gen, page: res.Page, err: err}
	}
}

func (m *Model) fetchChildren() tea.Cmd {
	if !m.wantsChildren() {
		return nil
	}
	m.childGen++
	m.childAsked = true
	return kernel.Reply(loadChildren(m.childContext(), m.search, m.issue.Key, m.childGen), m.addr)
}

func (m *Model) childContext() context.Context {
	if m.childCtx == nil {
		m.childCtx, m.childCancel = context.WithCancel(context.Background())
	}
	return m.childCtx
}

func (m *Model) childrenArrived(msg childrenMsg) {
	if msg.gen != m.childGen {
		return
	}
	m.childRead, m.childErr = true, msg.err
	if msg.err == nil {
		m.children, m.childPage = msg.page.Items, msg.page
	}
	m.dataGen++
}

func (m *Model) hasChild(key string) bool {
	return slices.ContainsFunc(m.children, func(c jira.Issue) bool { return c.Key == key }) ||
		slices.ContainsFunc(m.issue.Subtasks, func(c jira.IssueRef) bool { return c.Key == key })
}

func (m *Model) childChanged(key string) tea.Cmd {
	if m.deps.Jira == nil || !m.hasChild(key) {
		return nil
	}
	ctx, gen, reader := m.childContext(), m.childGen, m.deps.Jira
	return kernel.Reply(func() tea.Msg {
		iss, err := reader.IssueFields(ctx, key, app.ListProjection().IDs)
		return childPatchedMsg{gen: gen, issue: iss, err: err}
	}, m.addr)
}

func (m *Model) childPatched(msg childPatchedMsg) {
	if msg.gen != m.childGen || msg.err != nil {
		return
	}
	key := msg.issue.Key
	if at := slices.IndexFunc(m.children, func(c jira.Issue) bool { return c.Key == key }); at >= 0 {
		m.children = slices.Clone(m.children)
		m.children[at] = msg.issue
	}
	if at := slices.IndexFunc(m.issue.Subtasks, func(c jira.IssueRef) bool { return c.Key == key }); at >= 0 {
		m.issue.Subtasks = slices.Clone(m.issue.Subtasks)
		m.issue.Subtasks[at].Summary, m.issue.Subtasks[at].Status = msg.issue.Summary, msg.issue.Status
	}
	m.dataGen++
}

func (m *Model) childGroup() (refGroup, bool) {
	switch {
	case !m.wantsChildren() || !m.childAsked:
		return refGroup{}, false
	case m.childErr != nil:
		reason, _ := jira.Reason(m.childErr)
		return refGroup{label: childrenGroup, note: "children could not be read: " + reason}, true
	case !m.childRead:
		return refGroup{label: childrenGroup, note: "reading" + m.deps.Theme.Glyphs.Ellipsis}, true
	case len(m.children) == 0:
		return refGroup{}, false
	}
	n, done := rollup(m.children)
	more := m.childPage.HasMore()
	title := childrenGroup + " · " + strconv.Itoa(n) + " · " + strconv.Itoa(done) + " done"
	if more {
		title = childrenGroup + " · " + strconv.Itoa(n) + "+ · " + strconv.Itoa(done) + " done so far"
	}
	shown := min(n, childrenInline)
	refs := make([]jira.IssueRef, shown)
	for i := range shown {
		refs[i] = refOf(&m.children[i])
	}
	g := refGroup{label: childrenGroup, title: title, refs: refs}
	if rest := n - shown; rest > 0 || more {
		g.more = "+" + strconv.Itoa(rest) + " more"
		if more {
			g.more = "+" + strconv.Itoa(rest) + "+ more"
		}
		g.more += " · ] lists them all"
	}
	return g, true
}

func refOf(iss *jira.Issue) jira.IssueRef {
	return jira.IssueRef{ID: iss.ID, Key: iss.Key, Summary: iss.Summary, Status: iss.Status, Type: iss.Type}
}

func (m *Model) openChildren() tea.Cmd {
	switch {
	case m.issue.Key == "" || m.stage != sideBrowse:
		return nil
	case !m.read("subtasks"):
		return kernel.Warn(m.issue.Key + " has not been read yet")
	case len(m.issue.Subtasks) == 0 && m.issue.Type.HierarchyLevel < 1:
		return kernel.Warn(m.issue.Key + " has no children")
	case m.childRead && m.childErr == nil && len(m.children) == 0 && len(m.issue.Subtasks) == 0:
		return kernel.Warn(m.issue.Key + " has no children")
	}
	kind := &childrenKind{search: m.search}
	if m.childRead && m.childErr == nil {
		kind.seed = &childSeed{issues: m.children, page: m.childPage}
	}
	sh := newSheet(m.deps, m.issue, kind)
	sh.trail = m.trailBeneath()
	return kernel.Push("issue.sheet", m.issue.Key+" children", sh)
}

func (m *Model) showChildrenInList() tea.Cmd {
	if !jira.IsIssueRef(m.issue.Key) {
		return nil
	}
	for _, spec := range kernel.Views() {
		if spec.RunsQueries {
			return kernel.OpenThen(spec.ID, kernel.RunQueryMsg{JQL: childrenJQL(m.issue.Key), Title: m.issue.Key + " children"})
		}
	}
	return kernel.Warn("there is no issue list to show them in")
}
