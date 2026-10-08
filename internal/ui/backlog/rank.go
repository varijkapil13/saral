package backlog

import (
	"slices"
	"strings"

	tea "charm.land/bubbletea/v2"

	"github.com/varijkapil13/saral/internal/app"
	"github.com/varijkapil13/saral/internal/ui/kernel"
	"github.com/varijkapil13/saral/pkg/jira"
)

// rankValue is the rank field an issue had, which a refusal puts back. A
// section is ordered by the rank field's value, so a step gives the issue its
// anchor's value and puts it beside the anchor in m.issues: the stable sort by
// value then keeps the two side by side, in that order.
type rankValue struct {
	value jira.FieldValue
	has   bool
}

type rankMsg app.RankAnswer

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
	if k := m.ranking.Key(); k != "" && k != key {
		return kernel.Warn(k + " is still being ranked; " + key + " can move once the site has answered")
	}
	from, ok := m.byKey[key]
	to, known := m.byKey[anchor]
	if !ok || !known {
		return nil
	}
	next := ""
	if from+1 < len(m.issues) {
		next = m.issues[from+1].Key
	}
	start := m.ranking.Step(key, next, m.rankValue(from))
	value, has := m.issues[to].Fields.Get(m.rankRef())
	m.setRank(from, value, has)
	m.issues = app.ShiftIssue(m.issues, from, anchor, after)
	m.reindex()
	m.regroup()
	m.restore(key)
	if start != app.RankFresh {
		return nil
	}
	return m.sendRank()
}

func (m *Model) rankValue(at int) rankValue {
	value, has := m.issues[at].Fields.Get(m.rankRef())
	return rankValue{value: value, has: has}
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
	key := m.ranking.Key()
	if key == "" || m.deps.Jira == nil {
		return nil
	}
	at, ok := m.byKey[key]
	if !ok {
		m.ranking.Drop()
		return nil
	}
	var prev, next string
	for g := range m.groups {
		issues := m.groups[g].issues
		p := slices.Index(issues, at)
		if p < 0 {
			continue
		}
		if p+1 < len(issues) {
			next = m.issues[issues[p+1]].Key
		}
		if p > 0 {
			prev = m.issues[issues[p-1]].Key
		}
		break
	}
	pos, ok := app.RankBeside(prev, next, m.config.RankFieldID)
	if !ok {
		m.ranking.Drop()
		return nil
	}
	sent := ""
	if at+1 < len(m.issues) {
		sent = m.issues[at+1].Key
	}
	run, cancel := m.ranking.Send(m.deps.Jira, pos, sent, m.rankValue(at))
	return kernel.Reply(withCancel(cancel, func() tea.Msg { return rankMsg(run()) }), m.addr)
}

func (m *Model) ranked(msg rankMsg) tea.Cmd {
	res := m.ranking.Answer(app.RankAnswer(msg))
	switch res.Outcome {
	case app.RankRefused:
		m.putRankBack(res.Key, res.Next, res.Snap)
		return kernel.Fail(msg.Err)
	case app.RankResend:
		return m.sendRank()
	case app.RankDone:
		where := " above "
		if res.After {
			where = " below "
		}
		return tea.Batch(kernel.Status(res.Key+" now sits"+where+res.Anchor), stored(m.pagePut(m.issues, true)))
	}
	return nil
}

func (m *Model) putRankBack(key, next string, was rankValue) {
	from, ok := m.byKey[key]
	if !ok {
		return
	}
	under := m.under()
	m.setRank(from, was.value, was.has)
	m.issues = app.PutBack(m.issues, from, next)
	m.reindex()
	m.regroup()
	m.restore(under)
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
	for i, end := m.top, m.visibleEnd(); i < end; i++ {
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
