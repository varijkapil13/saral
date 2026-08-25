package list

import (
	"strconv"
	"strings"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/varijkapil13/saral/internal/ui/kernel"
	"github.com/varijkapil13/saral/pkg/jira"
)

// Facet is a cell of a row the rows can be narrowed to: the status, the type or
// the assignee a row draws. docs/UX.md asks that clicking one filters by it and
// clicking it again clears the filter.
type Facet uint8

// The facets a row draws. Nothing here is a Jira field id or an English status
// name: a facet holds whatever the row it was clicked on was showing.
const (
	FacetNone Facet = iota
	FacetStatus
	FacetType
	FacetAssignee
)

// FacetMsg narrows the rows to one cell of one row, or clears the narrowing. It
// is exported so the palette reaches the gesture the mouse does rather than a
// second implementation of it: an empty Value means the row under the cursor,
// which is the only row a command with no pointer can mean.
type FacetMsg struct {
	Kind  Facet
	Value string
}

// unassigned is what an issue nobody owns draws in the assignee column. It is
// this program's word rather than the site's, so the same word has to be what
// the facet matches on.
const unassigned = "unassigned"

// facet is what the rows are narrowed to right now.
type facet struct {
	kind  Facet
	value string
}

func (f facet) on() bool { return f.kind != FacetNone }

// matches reports whether a row survives the narrowing. It compares the value
// the row draws, so a row that reads "unassigned" is found by clicking another
// row that reads the same.
func (f facet) matches(iss *jira.Issue) bool {
	if !f.on() {
		return true
	}
	return facetValue(iss, f.kind) == f.value
}

func facetValue(iss *jira.Issue, kind Facet) string {
	switch kind {
	case FacetStatus:
		return iss.Status.Name
	case FacetType:
		return iss.Type.Name
	case FacetAssignee:
		return assigneeName(iss, unassigned)
	case FacetNone:
		return ""
	}
	return ""
}

// label is the word for the facet in the line that says what is being shown.
func (f Facet) label() string {
	switch f {
	case FacetStatus:
		return "status"
	case FacetType:
		return "type"
	case FacetAssignee:
		return "assignee"
	case FacetNone:
		return ""
	}
	return ""
}

// The zones a row carries, one per clickable cell per issue. They are stable
// for the life of the view — the prefix is minted once and the rest is the
// issue's own key — which is as close to bounded as the manager allows: it
// frees no id it has ever been handed.
func rowZone(key string) string    { return "row:" + key }
func statusZone(key string) string { return "status:" + key }
func typeZone(key string) string   { return "type:" + key }
func whoZone(key string) string    { return "who:" + key }

// facetPrompt is the line under the rows while the list is narrowed to a cell.
// It says what is being left out and how to stop leaving it out, because esc in
// a root view never reaches this view and no key holds the narrowing.
func (m *Model) facetPrompt() string {
	label := "only " + m.facet.kind.label() + " " + strconv.Quote(m.facet.value)
	hint := "  clear it from the palette"
	if m.zones.Enabled() {
		hint = "  click it again to clear"
	}
	room := max(m.width-ansi.StringWidth(hint), 8)
	return m.styles.prompt.Render(ansi.Truncate(label, room, m.deps.Theme.Glyphs.Ellipsis)) +
		m.styles.muted.Render(hint)
}

// filtered reports whether anything is being left out, by the local filter or
// by a facet. Both empty the screen the same way, and both make the count a
// fraction rather than a total.
func (m *Model) filtered() bool { return m.query != "" || m.facet.on() }

// filterWords names everything being left out, for the line that says nothing
// matched.
func (m *Model) filterWords() string {
	parts := make([]string, 0, 2)
	if m.facet.on() {
		parts = append(parts, m.facet.kind.label()+" "+strconv.Quote(m.facet.value))
	}
	if m.query != "" {
		parts = append(parts, strconv.Quote(m.query))
	}
	return strings.Join(parts, " and ")
}

// facetMsg answers the palette. It sets the narrowing rather than toggling it:
// a command named after what it shows must not clear it every second time.
func (m *Model) facetMsg(msg FacetMsg) tea.Cmd {
	if msg.Kind == FacetNone {
		return m.applyFacet(facet{})
	}
	value := msg.Value
	if value == "" {
		iss := m.selectedIssue()
		if iss == nil {
			return kernel.Warn("there is no row here to narrow these rows to")
		}
		value = facetValue(iss, msg.Kind)
	}
	return m.applyFacet(facet{kind: msg.Kind, value: value})
}

// toggleFacet is the click: the same cell twice means the user is done with it.
func (m *Model) toggleFacet(kind Facet, value string) tea.Cmd {
	next := facet{kind: kind, value: value}
	if m.facet == next {
		next = facet{}
	}
	return m.applyFacet(next)
}

func (m *Model) applyFacet(f facet) tea.Cmd {
	if f == m.facet {
		return nil
	}
	m.facet = f
	m.refilter()
	m.scrollToCursor()
	return m.pageAheadIfNeeded()
}

// clickFacet narrows to the cell under the pointer. The cell zones are drawn
// inside the row's own, so they are asked about first: a click on the status of
// a row is in bounds for both.
func (m *Model) clickFacet(msg tea.MouseClickMsg, iss *jira.Issue) (tea.Cmd, bool) {
	for _, cell := range [...]struct {
		zone string
		kind Facet
	}{
		{statusZone(iss.Key), FacetStatus},
		{typeZone(iss.Key), FacetType},
		{whoZone(iss.Key), FacetAssignee},
	} {
		if m.zones.Hit(cell.zone, msg) {
			return m.toggleFacet(cell.kind, facetValue(iss, cell.kind)), true
		}
	}
	return nil, false
}
