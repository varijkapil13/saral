package release

import (
	"strings"

	tea "charm.land/bubbletea/v2"
)

// While the filter is open every key is text, so q, j and digits are typed.
func (m *Model) startFind() tea.Cmd {
	s := m.set
	if s == nil || m.saving || m.mode != browsing {
		return nil
	}
	m.mode = finding
	s.find.SetWidth(max(m.width-len(s.find.Prompt)-inputChrome, 8))
	s.find.SetValue(s.rawNeedle)
	s.find.CursorEnd()
	_ = s.find.Focus()
	m.sum = ""
	m.clampScroll()
	return nil
}

func (m *Model) findKey(msg tea.KeyPressMsg) tea.Cmd {
	s := m.set
	switch msg.String() {
	case "enter":
		m.endFind()
		return nil
	case "esc":
		s.find.SetValue("")
		m.setNeedle("")
		m.endFind()
		return nil
	}
	s.find, _ = s.find.Update(msg)
	if raw := s.find.Value(); raw != s.rawNeedle {
		m.setNeedle(raw)
	}
	return nil
}

func (m *Model) endFind() {
	m.mode = browsing
	m.set.find.Blur()
	m.sum = ""
	m.scrollToCursor()
}

func (m *Model) setNeedle(raw string) {
	s := m.set
	under, head := m.selectedID(), m.headKeyAtCursor()
	s.rawNeedle = raw
	s.needle = strings.ToLower(strings.TrimSpace(raw))
	m.sum = ""
	m.reorder()
	m.moveOnto(under)
	if under == "" {
		m.moveOntoHead(head)
	}
}
