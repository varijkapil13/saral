package issue

import (
	tea "charm.land/bubbletea/v2"

	"github.com/varijkapil13/saral/internal/ui/kernel"
	"github.com/varijkapil13/saral/internal/ui/mention"
)

// mentionMsg answers the @-autocomplete's own timer and search. The thread's
// composer runs one of its own, and its answers pass through here to it.
func (m *Model) mentionMsg(msg tea.Msg) tea.Cmd {
	search := mention.Search{Finder: m.deps.Jira, Project: m.deps.Project}
	if reason, blocked := m.peopleBlocked(); blocked {
		search.Blocked = reason
	}
	cmd, handled := m.mention.Update(msg, search)
	if !handled {
		return nil
	}
	m.editGen++
	return kernel.Reply(cmd, m.addr)
}

func (m *Model) mentionLook() mention.Look {
	g := m.deps.Theme.Glyphs
	return mention.Look{
		Row: m.styles.value, Selected: m.styles.selected, Muted: m.styles.muted,
		Arrow: g.Arrow, Ellipsis: g.Ellipsis,
	}
}
