package list

import (
	"strconv"
	"strings"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/varijkapil13/saral/internal/ui/filter"
	"github.com/varijkapil13/saral/internal/ui/kernel"
	"github.com/varijkapil13/saral/pkg/jira"
)

// FacetMsg narrows the rows by one cell of one row, or drops every term. It is
// exported so the palette reaches the gesture the mouse does rather than a
// second implementation of it: an empty Value means the row under the cursor,
// which is the only row a command with no pointer can mean.
type FacetMsg struct {
	Kind  filter.Facet
	Value string
}

// OpenFilterMsg opens the picker over the search on screen. It is exported so
// that the palette reaches the same gesture the key does.
type OpenFilterMsg struct{}

// unassigned is what an issue nobody owns draws in the assignee column. It is
// this program's word rather than the site's, which is exactly why it may only
// ever be drawn: the term behind it is the empty account id.
const unassigned = "unassigned"

// termOf turns a cell of a row into the term that would narrow to it. The id is
// the site's own and the name is only ever drawn — a status name is localised,
// two statuses on one project can share one, and one account answered to two
// names on two endpoints in a minute.
func termOf(iss *jira.Issue, kind filter.Facet) (filter.Term, bool) {
	switch kind {
	case filter.FacetStatus:
		if iss.Status.ID == "" {
			return filter.Term{}, false
		}
		return filter.Term{Facet: kind, ID: iss.Status.ID, Label: iss.Status.Name}, true
	case filter.FacetType:
		if iss.Type.ID == "" {
			return filter.Term{}, false
		}
		return filter.Term{Facet: kind, ID: iss.Type.ID, Label: iss.Type.Name}, true
	case filter.FacetAssignee:
		if iss.Assignee == nil || iss.Assignee.AccountID == "" {
			return filter.Term{Facet: kind, Label: unassigned}, true
		}
		return filter.Term{Facet: kind, ID: iss.Assignee.AccountID, Label: assigneeName(iss, unassigned)}, true
	case filter.FacetNone, filter.FacetReporter, filter.FacetPriority, filter.FacetLabel:
	}
	return filter.Term{}, false
}

// The zones a row carries, one per clickable cell per issue. They are stable
// for the life of the view — the prefix is minted once and the rest is the
// issue's own key — which is as close to bounded as the manager allows: it
// frees no id it has ever been handed.
func rowZone(key string) string    { return "row:" + key }
func statusZone(key string) string { return "status:" + key }
func typeZone(key string) string   { return "type:" + key }
func whoZone(key string) string    { return "who:" + key }

// termZone is one chip on the line that says what is in force. Clicking it
// drops that term, which is the pointer's half of the same toggle the picker
// makes with enter.
func termZone(at int) string { return "term:" + strconv.Itoa(at) }

// --- the terms in force ------------------------------------------------------

// applyTerm puts a value in force or takes it off again, and re-runs the search
// rather than narrowing the rows already loaded. Narrowing locally could not
// reach an issue that had not been fetched, and it matched on a display name,
// which is neither unique nor stable.
func (m *Model) applyTerm(term filter.Term) tea.Cmd {
	return m.applyTerms(m.terms.Toggle(term))
}

func (m *Model) applyTerms(next filter.Terms) tea.Cmd {
	jql, title := termQuery(m.deps.Project, next)
	cmd := m.setQuery(jql, title, false)
	m.terms, m.termsGen = next, m.termsGen+1
	return cmd
}

// termQuery composes what the terms ask the site for. With no terms it is
// exactly the search a is bound to, which is why there is no second way to
// clear a filter: dropping the last term lands on it.
func termQuery(project string, terms filter.Terms) (jql, title string) {
	if len(terms) == 0 {
		return everyIssue.at(project)
	}
	jql = strings.TrimSpace(scoped(project, terms.Clause()) + " " + everyIssue.order)
	title = terms.Words()
	if p := strings.TrimSpace(project); p != "" {
		title += " in " + p
	}
	return jql, title
}

// facetMsg answers the palette. It toggles the row under the cursor the way a
// click on that cell does, and an empty Kind drops every term at once.
func (m *Model) facetMsg(msg FacetMsg) tea.Cmd {
	if msg.Kind == filter.FacetNone {
		if len(m.terms) == 0 {
			return nil
		}
		return m.applyTerms(nil)
	}
	iss := m.selectedIssue()
	if iss == nil {
		return kernel.Warn("there is no row here to narrow these rows to")
	}
	term, ok := termOf(iss, msg.Kind)
	if !ok {
		return kernel.Warn("this row has no " + msg.Kind.Label() + " to narrow by")
	}
	return m.applyTerm(term)
}

// clickFacet narrows to the cell under the pointer. The cell zones are drawn
// inside the row's own, so they are asked about first: a click on the status of
// a row is in bounds for both.
func (m *Model) clickFacet(msg tea.MouseClickMsg, iss *jira.Issue) (tea.Cmd, bool) {
	for _, cell := range [...]struct {
		zone string
		kind filter.Facet
	}{
		{statusZone(iss.Key), filter.FacetStatus},
		{typeZone(iss.Key), filter.FacetType},
		{whoZone(iss.Key), filter.FacetAssignee},
	} {
		if !m.zones.Hit(cell.zone, msg) {
			continue
		}
		term, ok := termOf(iss, cell.kind)
		if !ok {
			return nil, true
		}
		return m.applyTerm(term), true
	}
	return nil, false
}

func (m *Model) clickTerm(msg tea.MouseClickMsg) (tea.Cmd, bool) {
	for i := range m.terms {
		if m.zones.Hit(termZone(i), msg) {
			return m.applyTerm(m.terms[i]), true
		}
	}
	return nil, false
}

// openFilter pushes the picker over this view, with what is already in force so
// that a value on screen can be taken off there too, and with the key this view
// shows its search on so that a facet the site refuses can point at it.
func (m *Model) openFilter() tea.Cmd {
	keys := defaultKeys()
	return kernel.Push(filter.ViewID, "Filter",
		filter.New(m.deps, filter.WithTerms(m.terms), filter.WithEditKey(keys.Edit.Help().Key)))
}

// --- the line that says what is in force -------------------------------------

// chipKey is everything the line under the rows is built from, so that
// scrolling under one costs what scrolling without one costs.
type chipKey struct {
	query      string
	terms      int
	width, gen int
	mouse      bool
}

func (m *Model) chipKey() chipKey {
	return chipKey{
		query: m.query, terms: m.termsGen, width: m.width,
		gen: m.styles.gen, mouse: m.zones.Enabled(),
	}
}

// termsLine names every term the search is narrowed by, each marked so that a
// click drops it, and says how to change them without a pointer. A filter that
// is not on screen is one nobody can get off.
func (m *Model) termsLine() string {
	key := m.chipKey()
	if m.chip != "" && key == m.chipAt {
		return m.chip
	}
	hint := "  " + defaultKeys().FilterBy.Help().Key + " changes them"
	if m.zones.Enabled() {
		hint = "  click one to drop it"
	}
	room, used := max(m.width-ansi.StringWidth(hint), 8), 0
	ell := m.deps.Theme.Glyphs.Ellipsis
	var b strings.Builder
	// Each chip is cut before it is marked. Truncating the joined line instead
	// would cut inside a zone marker and leave a click resolving to the chip
	// before it.
	for i, term := range m.terms {
		sep := ""
		if i > 0 {
			sep = "  " + m.deps.Theme.Glyphs.Separator + " "
		}
		left := room - used - ansi.StringWidth(sep)
		if left <= 0 {
			b.WriteString(m.styles.muted.Render(" " + ell))
			break
		}
		text := term.Facet.Label() + " " + strconv.Quote(term.Label)
		if ansi.StringWidth(text) > left {
			text = ansi.Truncate(text, left, ell)
		}
		b.WriteString(m.styles.muted.Render(sep))
		b.WriteString(m.zones.Mark(termZone(i), m.styles.prompt.Render(text)))
		used += ansi.StringWidth(sep) + ansi.StringWidth(text)
	}
	m.chip = b.String() + m.styles.muted.Render(hint)
	m.chipAt = key
	return m.chip
}

// filterChip names the filter the rows are being narrowed by, and the key that
// takes it off — the same answer termsLine gives for a term, and for the same
// reason.
func (m *Model) filterChip() string {
	key := m.chipKey()
	if m.filterLine != "" && key == m.filterAt {
		return m.filterLine
	}
	label := "only rows matching " + strconv.Quote(m.query)
	room := max(m.width-ansi.StringWidth(clearHint), 8)
	m.filterLine = m.styles.prompt.Render(ansi.Truncate(label, room, m.deps.Theme.Glyphs.Ellipsis)) +
		m.styles.muted.Render(clearHint)
	m.filterAt = key
	return m.filterLine
}

// filtered reports whether the rows on screen are fewer than the answer the
// site gave, which is only ever the local filter now: a term changes the search
// itself, so its rows are the whole of the answer rather than part of it.
func (m *Model) filtered() bool { return m.query != "" }
