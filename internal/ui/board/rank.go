package board

import (
	"context"
	"errors"
	"slices"
	"strings"

	tea "charm.land/bubbletea/v2"

	"github.com/varijkapil13/saral/internal/ui/kernel"
	"github.com/varijkapil13/saral/pkg/jira"
)

// ranking is one card whose rank has been changed on screen ahead of the site.
// next is the card that followed it in the read before the first step, which is
// where a refusal puts it back; later steps taken while one is out only mark it
// dirty, and the position the card has on screen once the site answers is what
// is sent next, so two quick steps cannot reach the site out of order. sent is
// the card that followed it when the step now out was sent, which becomes next
// once the site accepts that step.
type ranking struct {
	key    string
	next   string
	sent   string
	dirty  bool
	anchor string
	after  bool
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

func rankIssue(ctx context.Context, r jira.Ranker, key string, at jira.RankPosition, gen int) tea.Cmd {
	return func() tea.Msg {
		return rankMsg{gen: gen, key: key, err: r.RankIssues(ctx, []string{key}, at)}
	}
}

// rankRefused is why no card on this board can be ranked, or "".
func (m *Model) rankRefused() string {
	switch {
	case m.deps.Jira == nil:
		return "there is no Jira connection in this session"
	case m.plan.ordering != jira.OrderRank:
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
	name := m.plan.columns[col].name
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
	if m.rank != nil && m.rank.key != key {
		return kernel.Warn(m.rank.key + " is still being ranked; " + key + " can move once the site has answered")
	}
	from := m.indexOf(key)
	if from < 0 || m.indexOf(anchor) < 0 {
		return nil
	}
	pending := m.rank != nil
	if !pending {
		next := ""
		if from+1 < len(m.issues) {
			next = m.issues[from+1].Key
		}
		m.rank = &ranking{key: key, next: next}
	}
	m.issues = shiftIssue(m.issues, from, anchor, after)
	m.place()
	m.forget()
	m.restore(key)
	if pending {
		m.rank.dirty = true
		return nil
	}
	return m.sendRank()
}

// sendRank asks the site for the position the card has on screen now, named by
// the card beside it in its own column.
func (m *Model) sendRank() tea.Cmd {
	if m.rank == nil || m.deps.Jira == nil {
		return nil
	}
	col, row, ok := m.locate(m.rank.key)
	if !ok {
		m.rank = nil
		return nil
	}
	lo, hi := m.laneSpan(col, row)
	var at jira.RankPosition
	switch {
	case row+1 < hi:
		at = jira.RankBefore(m.issueAt(col, row+1).Key)
	case row > lo:
		at = jira.RankAfter(m.issueAt(col, row-1).Key)
	default:
		m.rank = nil
		return nil
	}
	at.FieldID = m.rawConfig.RankFieldID
	m.rank.anchor, m.rank.after = at.Anchor()
	m.rank.sent = ""
	if i := m.indexOf(m.rank.key); i >= 0 && i+1 < len(m.issues) {
		m.rank.sent = m.issues[i+1].Key
	}
	m.stopRank()
	m.rankGen++
	ctx, cancel := context.WithCancel(context.Background())
	m.rankStop = cancel
	return kernel.Reply(withCancel(cancel, rankIssue(ctx, m.deps.Jira, m.rank.key, at, m.rankGen)), m.addr)
}

func (m *Model) stopRank() {
	if m.rankStop != nil {
		m.rankStop()
		m.rankStop = nil
	}
}

// dropRank forgets a rank in flight without putting anything back, for a board
// whose cards have been replaced wholesale.
func (m *Model) dropRank() {
	m.stopRank()
	m.rankGen++
	m.rank = nil
}

func (m *Model) ranked(msg rankMsg) tea.Cmd {
	if msg.gen != m.rankGen || m.rank == nil || m.rank.key != msg.key {
		return nil
	}
	m.rankStop = nil
	if msg.err != nil && !rankedAnyway(msg.err, msg.key) {
		m.putRankBack()
		return kernel.Fail(msg.err)
	}
	if m.rank.dirty {
		m.rank.dirty, m.rank.next = false, m.rank.sent
		return m.sendRank()
	}
	anchor, after := m.rank.anchor, m.rank.after
	m.rank = nil
	where := " above "
	if after {
		where = " below "
	}
	return tea.Batch(kernel.Status(msg.key+" now sits"+where+anchor), stored(m.pagePut(m.issues, true)))
}

// rankedAnyway reads a partial answer for the one card sent: a PartialRankError
// that names it among the ranked is a success.
func rankedAnyway(err error, key string) bool {
	var partial *jira.PartialRankError
	return errors.As(err, &partial) && slices.Contains(partial.Ranked, key)
}

// putRankBack returns the card to where the read had it, before the card that
// followed it then.
func (m *Model) putRankBack() {
	r := m.rank
	m.rank = nil
	from := m.indexOf(r.key)
	if from < 0 {
		return
	}
	under := m.selectedKey()
	iss := m.issues[from]
	m.issues = slices.Delete(m.issues, from, from+1)
	at := len(m.issues)
	if r.next != "" {
		if i := m.indexOf(r.next); i >= 0 {
			at = i
		}
	}
	m.issues = slices.Insert(m.issues, at, iss)
	m.place()
	m.forget()
	m.restore(under)
}

// shiftIssue moves issues[from] to just before or just after the issue keyed
// anchor, in place.
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
	if to < 0 || to >= len(m.plan.columns) {
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
