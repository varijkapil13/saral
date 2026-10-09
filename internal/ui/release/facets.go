package release

import (
	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/varijkapil13/saral/internal/ui/kernel"
)

const (
	facetState = iota
	facetProject
	facetCount
)

func (m *Model) startFacets(facet int) tea.Cmd {
	if m.set == nil || m.saving || m.mode != browsing {
		return nil
	}
	if facet == facetProject && len(m.set.owned) < 2 {
		return kernel.Status("this list holds versions of one project")
	}
	m.set.facet = facet
	m.mode = faceting
	m.sum = ""
	m.clampScroll()
	return nil
}

func (m *Model) stepFacet(dir int) {
	under, head := m.selectedID(), m.headKeyAtCursor()
	s := m.set
	if s.facet == facetProject {
		s.stepPick(dir)
		m.sum = ""
		m.reorder()
	} else {
		m.filter = m.filter.Step(dir)
		m.sum = ""
		m.rememberFilter()
		m.reorder()
	}
	m.moveOnto(under)
	if under == "" {
		m.moveOntoHead(head)
	}
}

func (s *setView) stepPick(dir int) {
	refs := make([]string, 0, len(s.owned)+1)
	refs = append(refs, "")
	for _, o := range s.owned {
		refs = append(refs, o.Ref)
	}
	at := 0
	for i, ref := range refs {
		if ref == s.pick {
			at = i
		}
	}
	s.pick = refs[(at+dir+len(refs))%len(refs)]
}

func (m *Model) facetsPrompt() string {
	s := m.set
	state := "state " + m.filter.Name()
	project := "project all"
	if s.pick != "" {
		project = "project " + s.pickLabel()
	}
	if s.facet == facetState {
		state = "[" + state + "]"
	} else {
		project = "[" + project + "]"
	}
	hint := "  tab switches, ←/→ change, enter keeps"
	if m.deps.Theme.Glyphs.IsASCII() {
		hint = "  tab switches, left/right change, enter keeps"
	}
	label := "  filter by:  " + state + "  " + project
	if ansi.StringWidth(label)+ansi.StringWidth(hint) > m.width {
		hint = ""
	}
	ell := m.deps.Theme.Glyphs.Ellipsis
	return m.styles.accent.Render(ansi.Truncate(label, max(m.width, 1), ell)) + m.styles.muted.Render(hint)
}
