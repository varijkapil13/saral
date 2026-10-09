package board

import (
	"slices"
	"strings"

	tea "charm.land/bubbletea/v2"

	appboard "github.com/varijkapil13/saral/internal/app/board"
	"github.com/varijkapil13/saral/internal/ui/kernel"
	"github.com/varijkapil13/saral/pkg/jira"
)

type rankMsg appboard.RankAnswer

type rankWhere uint8

const (
	rankUp rankWhere = iota
	rankDown
	rankTop
	rankBottom
)

// rankRefused is why no card on this board can be ranked, or "".
func (m *Model) rankRefused() string {
	switch {
	case m.deps.Jira == nil:
		return "there is no Jira connection in this session"
	case m.plan.Ordering != jira.OrderRank:
		return "this board is ordered by its filter, so its cards have no rank to change"
	}
	return ""
}

// reorder ranks the card under the cursor within its column.
func (m *Model) reorder(where rankWhere) tea.Cmd {
	if m.moving || m.card != nil || m.bulk != nil {
		return nil
	}
	iss := m.issueAt(m.curCol, m.curRow)
	if iss == nil {
		return nil
	}
	col, row := m.curCol, m.curRow
	lo, hi := m.laneSpan(col, row)
	name := m.plan.Columns[col].Name
	if m.lanesOn() {
		name += " in this lane"
	}
	var anchor int
	var after bool
	switch where {
	case rankUp, rankTop:
		if row == lo {
			return kernel.Status(iss.Key + " is already first in " + name)
		}
		anchor = row - 1
		if where == rankTop {
			anchor = lo
		}
	case rankDown, rankBottom:
		if where == rankBottom && m.more {
			return kernel.Warn("the rest of this board is still loading, so the last card in " + name + " is not known yet")
		}
		if row == hi-1 {
			return kernel.Status(iss.Key + " is already last in " + name)
		}
		anchor, after = row+1, true
		if where == rankBottom {
			anchor = hi - 1
		}
	}
	return m.rankNextTo(iss.Key, m.issues[m.cols[col][anchor]].Key, after)
}

// rankNextTo moves key to just before or just after anchor on screen and asks
// the site to do the same. The board's order is the order of m.issues, so the
// card moves there.
func (m *Model) rankNextTo(key, anchor string, after bool) tea.Cmd {
	if refused := m.rankRefused(); refused != "" {
		return kernel.Warn(refused)
	}
	if k := m.rank.Key(); k != "" && k != key {
		return kernel.Warn(k + " is still being ranked; " + key + " can move once the site has answered")
	}
	from := m.indexOf(key)
	if from < 0 || m.indexOf(anchor) < 0 {
		return nil
	}
	next := ""
	if from+1 < len(m.issues) {
		next = m.issues[from+1].Key
	}
	start := m.rank.Step(key, next, struct{}{})
	m.issues = appboard.ShiftIssue(m.issues, from, anchor, after)
	m.place()
	m.forget()
	m.restore(key)
	if start != appboard.RankFresh {
		return nil
	}
	return m.sendRank()
}

// sendRank asks the site for the position the card has on screen now, named by
// the card beside it in its own column.
func (m *Model) sendRank() tea.Cmd {
	key := m.rank.Key()
	if key == "" || m.deps.Jira == nil {
		return nil
	}
	col, row, ok := m.locate(key)
	if !ok {
		m.rank.Drop()
		return nil
	}
	lo, hi := m.laneSpan(col, row)
	var prev, next string
	if row+1 < hi {
		next = m.issueAt(col, row+1).Key
	}
	if row > lo {
		prev = m.issueAt(col, row-1).Key
	}
	at, ok := appboard.RankBeside(prev, next, m.rawConfig.RankFieldID)
	if !ok {
		m.rank.Drop()
		return nil
	}
	sent := ""
	if i := m.indexOf(key); i >= 0 && i+1 < len(m.issues) {
		sent = m.issues[i+1].Key
	}
	run, cancel := m.rank.Send(m.deps.Jira, at, sent, struct{}{})
	return kernel.Reply(withCancel(cancel, func() tea.Msg { return rankMsg(run()) }), m.addr)
}

func (m *Model) ranked(msg rankMsg) tea.Cmd {
	res := m.rank.Answer(appboard.RankAnswer(msg))
	switch res.Outcome {
	case appboard.RankRefused:
		m.putRankBack(res.Key, res.Next)
		return kernel.Fail(msg.Err)
	case appboard.RankResend:
		return m.sendRank()
	case appboard.RankDone:
		where := " above "
		if res.After {
			where = " below "
		}
		return tea.Batch(kernel.Status(res.Key+" now sits"+where+res.Anchor), stored(m.pagePut(m.issues, true)))
	}
	return nil
}

// putRankBack returns the card to where the read had it, before the card that
// followed it then.
func (m *Model) putRankBack(key, next string) {
	from := m.indexOf(key)
	if from < 0 {
		return
	}
	under := m.selectedKey()
	m.issues = appboard.PutBack(m.issues, from, next)
	m.place()
	m.forget()
	m.restore(under)
}

func (m *Model) indexOf(key string) int {
	return slices.IndexFunc(m.issues, func(i jira.Issue) bool { return i.Key == key })
}

// locate is where a card is drawn, by key.
func (m *Model) locate(key string) (col, row int, ok bool) {
	for c := range m.cols {
		for r, at := range m.cols[c] {
			if m.issues[at].Key == key {
				return c, r, true
			}
		}
	}
	return 0, 0, false
}

// dropWithin ends a drag that never left the card's own column: released over
// another card of that column, it ranks the dragged card beside it.
func (m *Model) dropWithin(grabbed string, msg tea.MouseMsg) tea.Cmd {
	key, isCard := strings.CutPrefix(grabbed, zoneCard)
	if !isCard {
		return nil
	}
	col, row, on := m.cardUnder(msg)
	if !on {
		return nil
	}
	fromCol, fromRow, ok := m.locate(key)
	if !ok || fromCol != col || fromRow == row {
		return nil
	}
	if lo, hi := m.laneSpan(col, fromRow); row < lo || row >= hi {
		return kernel.Warn("a card is ranked within its own lane; move it to another lane by changing what the lane is grouped by")
	}
	return m.rankNextTo(key, m.issueAt(col, row).Key, row > fromRow)
}

// shiftCard lands the card under the cursor in the column beside it: the same
// pick-up, aim and drop the m gesture makes, in one stroke.
func (m *Model) shiftCard(by int) tea.Cmd {
	if m.moving || m.card != nil || m.bulk != nil {
		return nil
	}
	iss := m.issueAt(m.curCol, m.curRow)
	if iss == nil {
		return nil
	}
	to := m.curCol + by
	if to < 0 || to >= len(m.plan.Columns) {
		side := "last"
		if by < 0 {
			side = "first"
		}
		return kernel.Status(iss.Key + " is already in the " + side + " column")
	}
	if cmd := m.pickUp(); m.card == nil {
		return cmd
	}
	m.aim(to)
	return m.drop()
}
