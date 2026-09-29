package board

import (
	tea "charm.land/bubbletea/v2"

	"github.com/varijkapil13/saral/internal/ui/widget/card"
)

var recallLook = card.Recall

// setLook draws the board in another look. The cursor, each column's own
// offset and the lane at the top of the window are kept in cards rather than
// lines, so they survive the change. Moving to roomy over cards read without
// what a roomy card draws reads them again, through the same staged swap as
// any revalidation, and so does a walk in flight that was not asking for it;
// moving away from roomy reads nothing.
func (m *Model) setLook(l card.Look) tea.Cmd {
	if l == m.look {
		return nil
	}
	was := m.look
	lane, row, marked := m.laneMark()
	m.look = l
	m.stackLanes()
	if marked {
		m.toLaneMark(lane, row)
	}
	m.forget()
	m.relayout()
	m.follow()
	if l != card.Roomy || was == card.Roomy {
		return nil
	}
	if m.walking() || m.lacksRoomy() {
		return m.readCards(false)
	}
	return nil
}

func (m *Model) walking() bool {
	return m.loading && (m.step == stepIssues || m.step == stepSprints) && !m.qfWait
}

func (m *Model) lacksRoomy() bool {
	for i := range m.issues {
		asked := m.issues[i].Requested
		if asked.Wide() {
			continue
		}
		for _, id := range card.RoomyFields {
			if !asked.Has(id) {
				return true
			}
		}
	}
	return false
}
