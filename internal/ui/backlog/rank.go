package backlog

import (
	"context"
	"errors"
	"slices"
	"strings"

	tea "charm.land/bubbletea/v2"

	"github.com/varijkapil13/saral/internal/ui/kernel"
	"github.com/varijkapil13/saral/pkg/jira"
)

// ranking is one issue whose rank has been changed on screen ahead of the site,
// the way board.ranking is: next is the issue that followed it in the read and
// value its own rank before the first step, which is what a refusal puts back.
// A section is ordered by the rank field's value, so a step gives the issue its
// anchor's value and puts it beside the anchor in m.issues: the stable sort by
// value then keeps the two side by side, in that order.
type ranking struct {
	key       string
	next      string
	value     jira.FieldValue
	hadValue  bool
	sent      string
	sentValue jira.FieldValue
	sentHad   bool
	dirty     bool
	anchor    string
	after     bool
}

type rankMsg struct {
	gen int
	key string
	err error
}

type rankWhere uint8

const (
	rankUp rankWhere = iota
	rankDown
	rankTop
	rankBottom
)

// RankMsg ranks the issue under the cursor within its section. It is exported
// so the palette reaches the gesture its key does.
type RankMsg struct{ Where rankWhere }

func rankIssue(ctx context.Context, r jira.Ranker, key string, at jira.RankPosition, gen int) tea.Cmd {
	return func() tea.Msg {
		return rankMsg{gen: gen, key: key, err: r.RankIssues(ctx, []string{key}, at)}
	}
}

func (m *Model) rankRef() jira.FieldRef { return jira.FieldRef{ID: m.config.RankFieldID} }

// rankRefused is why no issue here can be ranked, or "".
func (m *Model) rankRefused() string {
	switch {
	case m.deps.Jira == nil:
		return "there is no Jira connection in this session"
	case m.config.Ordering() != jira.OrderRank:
		return "this board has no rank field, so its backlog has no rank to change"
	case m.sort.chosen():
		return "the rows are sorted by " + m.sort.plain(m.deps.Theme.Glyphs) +
			", so a change of rank would not show where it went"
	case m.busy():
		return "this move is still going; ranks can change once it has finished"
	}
	return ""
}

func (m *Model) reorder(where rankWhere) tea.Cmd {
	if m.mode != browsing {
		return nil
	}
	iss := m.issueAt(m.cursor)
	if iss == nil {
		return nil
	}
	g := &m.groups[m.rows[m.cursor].group]
	pos := slices.Index(g.issues, m.byKey[iss.Key])
	n := len(g.issues)
	var anchor int
	var after bool
	switch where {
	case rankUp, rankTop:
		if pos == 0 {
			return kernel.Status(iss.Key + " is already first in " + g.name)
		}
		anchor = pos - 1
		if where == rankTop {
			anchor = 0
		}
	case rankDown, rankBottom:
		if where == rankBottom && m.page.HasMore() {
			return kernel.Warn("the rest of this backlog is still loading, so the last issue in " + g.name + " is not known yet")
		}
		if pos == n-1 {
			return kernel.Status(iss.Key + " is already last in " + g.name)
		}
		anchor, after = pos+1, true
		if where == rankBottom {
			anchor = n - 1
		}
	}
	return m.rankNextTo(iss.Key, m.issues[g.issues[anchor]].Key, after)
}

func (m *Model) rankNextTo(key, anchor string, after bool) tea.Cmd {
	if refused := m.rankRefused(); refused != "" {
		return kernel.Warn(refused)
	}
	if m.inFlight != nil && m.inFlight.key != key {
		return kernel.Warn(m.inFlight.key + " is still being ranked; " + key + " can move once the site has answered")
	}
	from, ok := m.byKey[key]
	to, known := m.byKey[anchor]
	if !ok || !known {
		return nil
	}
	pending := m.inFlight != nil
	if !pending {
		m.inFlight = &ranking{key: key}
		m.inFlight.value, m.inFlight.hadValue = m.issues[from].Fields.Get(m.rankRef())
		if from+1 < len(m.issues) {
			m.inFlight.next = m.issues[from+1].Key
		}
	}
	value, has := m.issues[to].Fields.Get(m.rankRef())
	m.setRank(from, value, has)
	m.issues = shiftIssue(m.issues, from, anchor, after)
	m.reindex()
	m.regroup()
	m.restore(key)
	if pending {
		m.inFlight.dirty = true
		return nil
	}
	return m.sendRank()
}

func (m *Model) setRank(at int, value jira.FieldValue, has bool) {
	if has {
		m.issues[at].Fields = m.issues[at].Fields.With(m.rankRef(), value)
		return
	}
	m.issues[at].Fields = m.issues[at].Fields.Without(m.rankRef())
}

// sendRank names the position the issue has on screen by its neighbour in its
// own section.
func (m *Model) sendRank() tea.Cmd {
	if m.inFlight == nil || m.deps.Jira == nil {
		return nil
	}
	at, ok := m.byKey[m.inFlight.key]
	if !ok {
		m.inFlight = nil
		return nil
	}
	var pos jira.RankPosition
	for g := range m.groups {
		issues := m.groups[g].issues
		p := slices.Index(issues, at)
		if p < 0 {
			continue
		}
		if p+1 < len(issues) {
			pos = jira.RankBefore(m.issues[issues[p+1]].Key)
		} else if p > 0 {
			pos = jira.RankAfter(m.issues[issues[p-1]].Key)
		}
		break
	}
	if pos.Before == "" && pos.After == "" {
		m.inFlight = nil
		return nil
	}
	pos.FieldID = m.config.RankFieldID
	m.inFlight.anchor, m.inFlight.after = pos.Anchor()
	m.inFlight.sent = ""
	if at+1 < len(m.issues) {
		m.inFlight.sent = m.issues[at+1].Key
	}
	m.inFlight.sentValue, m.inFlight.sentHad = m.issues[at].Fields.Get(m.rankRef())
	m.stopRank()
	m.rankGen++
	ctx, cancel := context.WithCancel(context.Background())
	m.rankStop = cancel
	return kernel.Reply(withCancel(cancel, rankIssue(ctx, m.deps.Jira, m.inFlight.key, pos, m.rankGen)), m.addr)
}

func (m *Model) stopRank() {
	if m.rankStop != nil {
		m.rankStop()
		m.rankStop = nil
	}
}

func (m *Model) dropRank() {
	m.stopRank()
	m.rankGen++
	m.inFlight = nil
}

func (m *Model) ranked(msg rankMsg) tea.Cmd {
	if msg.gen != m.rankGen || m.inFlight == nil || m.inFlight.key != msg.key {
		return nil
	}
	m.rankStop = nil
	if msg.err != nil && !rankedAnyway(msg.err, msg.key) {
		m.putRankBack()
		return kernel.Fail(msg.err)
	}
	if m.inFlight.dirty {
		m.inFlight.dirty = false
		m.inFlight.next, m.inFlight.value, m.inFlight.hadValue = m.inFlight.sent, m.inFlight.sentValue, m.inFlight.sentHad
		return m.sendRank()
	}
	anchor, after := m.inFlight.anchor, m.inFlight.after
	m.inFlight = nil
	where := " above "
	if after {
		where = " below "
	}
	return tea.Batch(kernel.Status(msg.key+" now sits"+where+anchor), stored(m.pagePut(m.issues, true)))
}

func rankedAnyway(err error, key string) bool {
	var partial *jira.PartialRankError
	return errors.As(err, &partial) && slices.Contains(partial.Ranked, key)
}

func (m *Model) putRankBack() {
	r := m.inFlight
	m.inFlight = nil
	from, ok := m.byKey[r.key]
	if !ok {
		return
	}
	under := m.under()
	m.setRank(from, r.value, r.hadValue)
	iss := m.issues[from]
	m.issues = slices.Delete(m.issues, from, from+1)
	at := len(m.issues)
	if i := slices.IndexFunc(m.issues, func(i jira.Issue) bool { return i.Key == r.next }); r.next != "" && i >= 0 {
		at = i
	}
	m.issues = slices.Insert(m.issues, at, iss)
	m.reindex()
	m.regroup()
	m.restore(under)
}

func shiftIssue(issues []jira.Issue, from int, anchor string, after bool) []jira.Issue {
	iss := issues[from]
	issues = slices.Delete(issues, from, from+1)
	at := slices.IndexFunc(issues, func(i jira.Issue) bool { return i.Key == anchor })
	if at < 0 {
		return slices.Insert(issues, from, iss)
	}
	if after {
		at++
	}
	return slices.Insert(issues, at, iss)
}

// dropWithin ends a drag released over another issue of the same section: the
// dragged issue is ranked beside it.
func (m *Model) dropWithin(grabbed string, msg tea.MouseMsg) tea.Cmd {
	key, isRow := strings.CutPrefix(grabbed, "row:")
	if !isRow {
		return nil
	}
	from := -1
	for i := range m.rows {
		if !m.rows[i].head && m.issues[m.rows[i].issue].Key == key {
			from = i
			break
		}
	}
	if from < 0 {
		return nil
	}
	for i := m.top; i < min(m.top+m.rowsHeight(), len(m.rows)); i++ {
		if m.rows[i].head || i == from || !m.zones.Hit(m.zoneOf(i), msg) {
			continue
		}
		if m.rows[i].group != m.rows[from].group {
			return nil
		}
		m.cursor = from
		return m.rankNextTo(key, m.issues[m.rows[i].issue].Key, i > from)
	}
	return nil
}
