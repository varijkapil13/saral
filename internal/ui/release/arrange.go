package release

import (
	"strconv"
	"strings"

	tea "charm.land/bubbletea/v2"

	"github.com/varijkapil13/saral/internal/ui/kernel"
	"github.com/varijkapil13/saral/internal/ui/widget"
	"github.com/varijkapil13/saral/pkg/jira"
)

type arrangement uint8

const (
	arrCross arrangement = iota
	arrProject
	arrNone
	arrangements
)

var arrangementNames = [arrangements]string{"cross-space", "project", "none"}

var arrangementLabels = [arrangements]string{"by cross-space release", "by project", "ungrouped"}

const (
	arrangeMemoryKey = "arrange"
	ungroupedName    = "not in a cross-space release"
)

func recallArrange(d kernel.Deps) arrangement {
	name, ok := kernel.Recall(d, SetViewID, arrangeMemoryKey)
	if ok {
		for a := range arrangements {
			if arrangementNames[a] == name {
				return a
			}
		}
	}
	return arrCross
}

type head struct {
	name     string
	key      string
	zone     string
	total    int
	kept     int
	projects string
	span     string
	released string
	text     string

	shipped                 int
	first, last             jira.Date
	owners                  []string
	ungrouped, wantsProject bool
}

func (m *Model) cycleArrange() tea.Cmd {
	s := m.set
	if s == nil || m.saving || m.mode != browsing {
		return nil
	}
	next := s.arrange
	for range arrangements {
		next = (next + 1) % arrangements
		if next == arrCross && len(s.src.Groups) == 0 {
			continue
		}
		break
	}
	if next == s.arrange {
		return nil
	}
	under, head := m.selectedID(), m.headKeyAtCursor()
	s.arrange = next
	kernel.Keep(m.deps, SetViewID, arrangeMemoryKey, arrangementNames[next])
	m.sum = ""
	m.regroup()
	m.reorder()
	m.moveOnto(under)
	if under == "" {
		m.moveOntoHead(head)
	}
	return nil
}

func (m *Model) headKeyAtCursor() string {
	if m.set == nil || m.cursor < 0 || m.cursor >= len(m.order) || m.order[m.cursor].v >= 0 {
		return ""
	}
	return m.set.heads[m.order[m.cursor].g].key
}

func (m *Model) moveOntoHead(key string) {
	if key == "" {
		m.moveTo(m.cursor)
		return
	}
	for at, sl := range m.order {
		if sl.v < 0 && m.set.heads[sl.g].key == key {
			m.moveTo(at)
			return
		}
	}
	m.moveTo(m.cursor)
}

func (m *Model) foldHeader(at int) tea.Cmd {
	if m.set == nil || at < 0 || at >= len(m.order) || m.order[at].v >= 0 {
		return nil
	}
	key := m.set.heads[m.order[at].g].key
	if m.set.folded[key] {
		delete(m.set.folded, key)
	} else {
		m.set.folded[key] = true
	}
	m.reorder()
	m.moveOntoHead(key)
	return nil
}

// regroup runs when the versions or the arrangement change, never per frame.
func (m *Model) regroup() {
	s := m.set
	s.memberGroup = s.memberGroup[:0]
	s.heads = s.heads[:0]
	if s.arrange == arrNone {
		return
	}
	prefix := arrangementNames[s.arrange] + "\x00"
	switch s.arrange {
	case arrCross:
		for gi, g := range s.src.Groups {
			s.heads = append(s.heads, head{name: widget.Sanitize(g.Name), key: prefix + g.Name})
			s.heads[gi].zone = "group:" + strconv.Itoa(gi)
		}
		first := make([]int32, len(m.versions))
		for i := range first {
			first[i] = -1
		}
		at := make(map[string]int, len(m.versions))
		for i := range m.versions {
			at[m.versions[i].ID] = i
		}
		for gi, g := range s.src.Groups {
			for _, id := range g.VersionIDs {
				if i, ok := at[id]; ok && first[i] < 0 {
					first[i] = int32(gi)
				}
			}
		}
		s.memberGroup = append(s.memberGroup, first...)
	case arrProject:
		index := make(map[string]int32, len(s.owned))
		for gi, o := range s.owned {
			index[o.Ref] = int32(gi)
			s.heads = append(s.heads, head{name: widget.Sanitize(o.Label), key: prefix + o.Ref, wantsProject: true})
			s.heads[gi].zone = "group:" + strconv.Itoa(gi)
		}
		for i := range m.versions {
			s.memberGroup = append(s.memberGroup, index[s.owners[i].Ref])
		}
	case arrNone:
	}
	tail := len(s.heads)
	s.heads = append(s.heads, head{
		name: ungroupedName, key: prefix + "\x00", zone: "group:" + strconv.Itoa(tail), ungrouped: true,
	})
	for i := range m.versions {
		g := s.memberGroup[i]
		if g < 0 {
			g = int32(tail)
			s.memberGroup[i] = g
		}
		s.heads[g].add(&m.versions[i], s.owners[i].Label)
	}
	for g := range s.heads {
		s.heads[g].finish(m.day)
	}
}

func (h *head) add(v *jira.Version, owner string) {
	h.total++
	if v.Released {
		h.shipped++
	}
	if !v.StartDate.IsZero() && (h.first.IsZero() || v.StartDate.Before(h.first)) {
		h.first = v.StartDate
	}
	if !v.ReleaseDate.IsZero() && (h.last.IsZero() || h.last.Before(v.ReleaseDate)) {
		h.last = v.ReleaseDate
	}
	for _, o := range h.owners {
		if o == owner {
			return
		}
	}
	h.owners = append(h.owners, owner)
}

func (h *head) finish(today jira.Date) {
	h.projects = strings.Join(h.owners, ", ")
	switch {
	case h.first.IsZero() && h.last.IsZero():
		h.span = ""
	case h.last.IsZero():
		h.span = "from " + h.first.String()
	case h.first.IsZero():
		h.span = "to " + h.last.String()
	default:
		h.span = h.first.String() + " to " + h.last.String()
	}
	switch {
	case h.total == 0:
		h.released = ""
	case h.shipped == h.total:
		h.released = "all released"
	case !h.last.IsZero() && !today.IsZero() && h.last.Before(today):
		h.released = "overdue"
	default:
		h.released = strconv.Itoa(h.shipped) + " of " + strconv.Itoa(h.total) + " released"
	}
}

func (h *head) line() string {
	var b strings.Builder
	b.WriteString(strconv.Itoa(h.kept))
	b.WriteString(" of ")
	b.WriteString(strconv.Itoa(h.total))
	if h.ungrouped {
		return b.String()
	}
	for _, part := range []string{h.projectsIf(), h.span, h.released} {
		if part == "" {
			continue
		}
		b.WriteString(" · ")
		b.WriteString(part)
	}
	return b.String()
}

func (h *head) projectsIf() string {
	if h.wantsProject {
		return ""
	}
	return h.projects
}

func (m *Model) arrangeSlots() {
	s := m.set
	s.keptIdx = s.keptIdx[:0]
	for _, i := range m.sorted {
		if m.keeps(i) {
			s.keptIdx = append(s.keptIdx, int32(i))
		}
	}
	s.shown = len(s.keptIdx)
	if s.arrange == arrNone || len(s.heads) == 0 {
		for _, i := range s.keptIdx {
			m.order = append(m.order, slot{v: i, g: -1})
		}
		return
	}
	n := len(s.heads)
	for len(s.buckets) < n {
		s.buckets = append(s.buckets, nil)
	}
	for g := range n {
		s.buckets[g] = s.buckets[g][:0]
		s.heads[g].kept = 0
	}
	s.seq = s.seq[:0]
	tail := int32(n - 1)
	for _, i := range s.keptIdx {
		g := s.memberGroup[i]
		if len(s.buckets[g]) == 0 && g != tail {
			s.seq = append(s.seq, g)
		}
		s.buckets[g] = append(s.buckets[g], i)
	}
	if len(s.buckets[tail]) > 0 {
		s.seq = append(s.seq, tail)
	}
	for _, g := range s.seq {
		h := &s.heads[g]
		h.kept = len(s.buckets[g])
		h.text = h.line()
		m.order = append(m.order, slot{v: -1, g: g})
		if s.folded[h.key] {
			continue
		}
		for _, i := range s.buckets[g] {
			m.order = append(m.order, slot{v: i, g: g})
		}
	}
}

func (m *Model) keeps(i int) bool {
	if !m.filter.keeps(m.cells[i].state) {
		return false
	}
	s := m.set
	if s.excluded[i] && !s.showExcluded {
		return false
	}
	if s.pick != "" && s.owners[i].Ref != s.pick {
		return false
	}
	return s.needle == "" || strings.Contains(s.folds[i], s.needle)
}

func (m *Model) toggleExcluded() tea.Cmd {
	s := m.set
	if s == nil || m.saving || m.mode != browsing {
		return nil
	}
	if s.excludedN == 0 {
		return kernel.Status("the plan leaves no version out")
	}
	under, head := m.selectedID(), m.headKeyAtCursor()
	s.showExcluded = !s.showExcluded
	m.sum = ""
	m.reorder()
	m.moveOnto(under)
	if under == "" {
		m.moveOntoHead(head)
	}
	return nil
}

func (m *Model) cyclePick() tea.Cmd {
	s := m.set
	if s == nil || m.saving || m.mode != browsing {
		return nil
	}
	if len(s.owned) < 2 {
		return kernel.Status("this list holds versions of one project")
	}
	next := ""
	if s.pick == "" {
		next = s.owned[0].Ref
	} else {
		for i, o := range s.owned {
			if o.Ref == s.pick && i+1 < len(s.owned) {
				next = s.owned[i+1].Ref
			}
		}
	}
	under, head := m.selectedID(), m.headKeyAtCursor()
	s.pick = next
	m.sum = ""
	m.reorder()
	m.moveOnto(under)
	if under == "" {
		m.moveOntoHead(head)
	}
	return nil
}

func (s *setView) pickLabel() string {
	for _, o := range s.owned {
		if o.Ref == s.pick {
			return o.Label
		}
	}
	return ""
}
