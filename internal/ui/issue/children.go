package issue

import (
	"context"
	"slices"
	"strconv"

	tea "charm.land/bubbletea/v2"

	appissue "github.com/varijkapil13/saral/internal/app/issue"
	"github.com/varijkapil13/saral/internal/ui/kernel"
	"github.com/varijkapil13/saral/pkg/jira"
)

const (
	childrenInline = 8
	childrenGroup  = "Children"
	moreRowID      = "children:more"
)

type childrenMsg struct {
	gen      int
	page     jira.Page[jira.Issue]
	err      error
	order    appissue.ChildOrder
	haveRank bool
	restRead bool
	warn     string
}

type childPatchedMsg struct {
	gen   int
	issue jira.Issue
	err   error
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

func loadChildren(ctx context.Context, in appissue.ChildRead, gen int) tea.Cmd {
	return func() tea.Msg {
		d := in.Run(ctx)
		return childrenMsg{
			gen: gen, page: d.Page, err: d.Err, order: d.Order,
			haveRank: d.HaveRank, restRead: d.RestRead, warn: priorityWarning(d.PriorityErr),
		}
	}
}

func (m *Model) childRequest() appissue.ChildRead {
	return appissue.ChildRead{
		Key: m.issue.Key, Search: m.search, Vocab: m.deps.Jira, Choice: appissue.ChildSort(currentChildSort()),
		Order: m.childOrd.ChildOrder, Bound: appissue.ChildrenSortBound,
	}
}

func (m *Model) fetchChildren() tea.Cmd {
	if !m.wantsChildren() {
		return nil
	}
	m.childGen++
	m.childAsked = true
	return kernel.Reply(loadChildren(m.childContext(), m.childRequest(), m.childGen), m.addr)
}

func (m *Model) resortChildren() tea.Cmd {
	if !m.wantsChildren() || !m.childAsked {
		return nil
	}
	if !m.childRead || m.childErr != nil {
		return m.fetchChildren()
	}
	if !m.childOrd.needs(currentChildSort(), m.childRank, m.childRest, m.childPage) {
		m.reorderChildren()
		m.dataGen++
		return nil
	}
	m.childGen++
	in := m.childRequest()
	in.Page, in.Loaded, in.HaveRank, in.RestRead = m.childPage, true, m.childRank, m.childRest
	in.Page.Items = m.children
	return kernel.Reply(loadChildren(m.childContext(), in, m.childGen), m.addr)
}

func (m *Model) reorderChildren() {
	m.childApplied = m.childOrd.effective(currentChildSort())
	m.childIdx = orderIndex(m.children, m.childApplied, &m.childOrd, m.childIdx)
}

func (m *Model) rankID() string {
	if m.childRank {
		return m.childOrd.RankID
	}
	return ""
}

func (m *Model) childContext() context.Context {
	if m.childCtx == nil {
		m.childCtx, m.childCancel = context.WithCancel(context.Background())
	}
	return m.childCtx
}

func (m *Model) childrenArrived(msg childrenMsg) tea.Cmd {
	if msg.gen != m.childGen {
		return nil
	}
	m.childRead, m.childErr = true, msg.err
	var cmd tea.Cmd
	if msg.err == nil {
		m.children, m.childPage = msg.page.Items, msg.page
		m.childOrd.ChildOrder, m.childRank, m.childRest = msg.order, msg.haveRank, msg.restRead
		if msg.warn != "" && !m.childOrd.prioWarned {
			m.childOrd.prioWarned = true
			cmd = kernel.Warn(msg.warn)
		}
		m.reorderChildren()
	}
	m.dataGen++
	return cmd
}

func (m *Model) hasChild(key string) bool {
	return slices.ContainsFunc(m.children, func(c jira.Issue) bool { return c.Key == key }) ||
		slices.ContainsFunc(m.issue.Subtasks, func(c jira.IssueRef) bool { return c.Key == key })
}

func (m *Model) childChanged(key string) tea.Cmd {
	if m.deps.Jira == nil || !m.hasChild(key) {
		return nil
	}
	ctx, gen, reader, ids := m.childContext(), m.childGen, m.deps.Jira, appissue.ChildProjection(m.rankID()).IDs
	return kernel.Reply(func() tea.Msg {
		iss, err := appissue.ReadFields(ctx, reader, key, ids)
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
	m.reorderChildren()
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
	n, done := appissue.Rollup(m.children)
	more := m.childPage.HasMore()
	title := childrenGroup + " · " + strconv.Itoa(n) + " · " + strconv.Itoa(done) + " done"
	if more {
		title = childrenGroup + " · " + strconv.Itoa(n) + "+ · " + strconv.Itoa(done) + " done so far"
	}
	if m.childApplied.Chosen() {
		title += " · sort: " + sortLabel(m.childApplied, m.deps.Theme.Glyphs)
	}
	shown := min(n, childrenInline)
	refs := make([]jira.IssueRef, shown)
	for i := range shown {
		at := i
		if len(m.childIdx) == n {
			at = m.childIdx[i]
		}
		refs[i] = refOf(&m.children[at])
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
	kind := &childrenKind{search: m.search, order: m.childOrd}
	if m.childRead && m.childErr == nil {
		kind.seed = &childSeed{issues: m.children, page: m.childPage, haveRank: m.childRank, restRead: m.childRest}
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
			return kernel.OpenThen(spec.ID, kernel.RunQueryMsg{JQL: appissue.ChildrenJQL(m.issue.Key), Title: m.issue.Key + " children"})
		}
	}
	return kernel.Warn("there is no issue list to show them in")
}
