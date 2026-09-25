package comment

import (
	tea "charm.land/bubbletea/v2"

	"github.com/varijkapil13/saral/internal/ui/kernel"
	"github.com/varijkapil13/saral/internal/ui/mention"
	"github.com/varijkapil13/saral/pkg/jira"
)

const (
	staleNote     = "this comment changed on the site after you wrote this draft; review it, and ctrl+s twice sends it over that change"
	staleSendNote = "this draft was written against an older version of the comment; ctrl+s again sends it anyway"
)

func (m *Model) mentionMsg(msg tea.Msg) tea.Cmd {
	search := mention.Search{Finder: m.deps.Jira, Project: m.deps.Project}
	switch got := m.deps.Caps.Capability(jira.CapPeople); {
	case m.deps.Jira == nil:
	case !got.OK && got.Reason != "":
		search.Blocked = got.Reason
	case !got.OK:
		search.Blocked = "this token cannot look accounts up"
	}
	cmd, handled := m.mention.Update(msg, search)
	if !handled {
		return nil
	}
	m.relayout()
	return kernel.Reply(cmd, m.addr, m.holder)
}

func (m *Model) mentionLook() mention.Look {
	g := m.deps.Theme.Glyphs
	return mention.Look{
		Row: m.styles.author, Selected: m.styles.selected, Muted: m.styles.muted,
		Arrow: g.Arrow, Ellipsis: g.Ellipsis,
	}
}

func join(a, b tea.Cmd) tea.Cmd {
	switch {
	case a == nil:
		return b
	case b == nil:
		return a
	default:
		return tea.Batch(a, b)
	}
}
