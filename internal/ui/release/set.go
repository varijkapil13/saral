package release

import (
	"context"
	"strings"

	tea "charm.land/bubbletea/v2"

	"github.com/varijkapil13/saral/internal/ui/kernel"
	"github.com/varijkapil13/saral/internal/ui/widget"
	"github.com/varijkapil13/saral/pkg/jira"
)

// Owner is the project a version belongs to: the reference a query names it by and the label a row draws.
type Owner struct{ Ref, Label string }

// Member is one version of a set and the project it belongs to.
type Member struct {
	Version jira.Version
	Project Owner
}

// Group is one cross-space release and the versions that make it up.
type Group struct {
	Name       string
	VersionIDs []string
}

// Set is the versions of several projects that the release list browses.
type Set struct {
	Title, Explain string
	Members        []Member
	Groups         []Group
	Excluded       map[string]bool
	Notes          []string
	Reload         func(context.Context) (Set, error)
}

const (
	createRefusal = "a version belongs to one project; open Releases (g 5) on it to create one"
	whatSet       = "The releases could not be read again."
	excludedCell  = "excluded by the plan"
)

// slot is one drawn row; v is -1 for a group's header.
type slot struct{ v, g int32 }

type setReadMsg struct {
	gen int
	set Set
}

type setView struct {
	src       Set
	owners    []Owner
	excluded  []bool
	excludedN int

	arrange      arrangement
	pick         string
	showExcluded bool
	folded       map[string]bool

	acts map[string]setAct

	// memberGroup is -1 for a version in no group, and heads has one more entry than there are groups, for them.
	projects    []string
	rows        *widget.RowCache[setKey, string]
	memberGroup []int32
	heads       []head
	owned       []Owner
	buckets     [][]int32
	seq         []int32
	keptIdx     []int32
	shown       int

	title string
}

// NewSet builds the release list over a set of projects.
func NewSet(d kernel.Deps, s Set) kernel.View {
	return SetList{newSetModel(d, s)}
}

// SetList is NewSet's view; it owes the kernel a Close that stops a reload in flight.
type SetList struct{ *Model }

var _ kernel.Closer = SetList{}

// Update handles one message.
func (s SetList) Update(msg tea.Msg) (kernel.View, tea.Cmd) {
	_, cmd := s.Model.Update(msg)
	return s, cmd
}

// Close stops a reload still in flight.
func (s SetList) Close() { s.stop() }

func newSetModel(d kernel.Deps, s Set) *Model {
	m := newModel(d)
	m.set = &setView{
		folded: map[string]bool{},
		rows:   widget.NewRowCache[setKey, string](rowCacheLimit),
		acts:   defaultSetKeys().table(),
	}
	m.find.input.Placeholder = "a version or a release"
	m.sort = loadSort(SetViewID)
	m.filter = recallFilter(d, SetViewID)
	m.set.arrange = recallArrange(d)
	m.install(s)
	m.loaded, m.checked = true, m.now()
	m.relayout()
	m.rebuildCells()
	return m
}

func (s *setView) headZone(g int32) string { return s.heads[g].zone }

func (s *setView) ownerOf(i int) Owner {
	if i < 0 || i >= len(s.owners) {
		return Owner{}
	}
	return s.owners[i]
}

func (m *Model) install(src Set) {
	s := m.set
	counted := make(map[string]*int, len(m.versions))
	for i := range m.versions {
		if m.versions[i].Unresolved != nil {
			counted[m.versions[i].ID] = m.versions[i].Unresolved
		}
	}
	s.src = src
	m.versions = make([]jira.Version, len(src.Members))
	s.owners = make([]Owner, len(src.Members))
	s.excluded = make([]bool, len(src.Members))
	s.excludedN = 0
	s.owned = s.owned[:0]
	for i := range src.Members {
		mem := &src.Members[i]
		v := mem.Version
		if v.Unresolved == nil {
			v.Unresolved = counted[v.ID]
		}
		m.versions[i], s.owners[i] = v, mem.Project
		if src.Excluded[v.ID] {
			s.excluded[i] = true
			s.excludedN++
		}
		if !s.hasOwner(mem.Project.Ref) {
			s.owned = append(s.owned, mem.Project)
		}
	}
	if s.pick != "" && !s.hasOwner(s.pick) {
		s.pick = ""
	}
	if s.arrange == arrCross && len(src.Groups) == 0 {
		s.arrange = arrProject
	}
}

func (s *setView) hasOwner(ref string) bool {
	for _, o := range s.owned {
		if o.Ref == ref {
			return true
		}
	}
	return false
}

func (m *Model) decorate() {
	s := m.set
	named := s.groupNames(m.versions)
	s.projects, m.find.folds = s.projects[:0], m.find.folds[:0]
	for i := range m.cells {
		s.projects = append(s.projects, widget.Sanitize(s.owners[i].Label))
		m.find.folds = append(m.find.folds, strings.ToLower(m.cells[i].name+"\x00"+named[i]))
	}
	m.regroup()
}

func (s *setView) groupNames(versions []jira.Version) []string {
	out := make([]string, len(versions))
	if len(s.src.Groups) == 0 {
		return out
	}
	at := make(map[string]int, len(versions))
	for i := range versions {
		at[versions[i].ID] = i
	}
	for _, g := range s.src.Groups {
		for _, id := range g.VersionIDs {
			if i, ok := at[id]; ok {
				out[i] += g.Name + "\x00"
			}
		}
	}
	return out
}

func (m *Model) reloadSet() tea.Cmd {
	reload := m.set.src.Reload
	if reload == nil {
		return kernel.Warn("this list was read once and has nothing to read again; open it again to refresh it")
	}
	ctx, gen := m.begin()
	m.loading, m.failure = true, nil
	m.sum = ""
	return m.reply(func() tea.Msg {
		next, err := reload(ctx)
		if err != nil {
			return failedMsg{gen: gen, what: whatSet, err: err}
		}
		return setReadMsg{gen: gen, set: next}
	})
}

func (m *Model) tookSet(msg setReadMsg) tea.Cmd {
	if m.set == nil || !m.current(msg.gen) {
		return nil
	}
	under := m.selectedID()
	m.loading, m.loaded, m.failure, m.stale = false, true, nil, false
	m.checked = m.now()
	m.install(msg.set)
	m.head, m.sum = "", ""
	m.relayout()
	m.rebuildCells()
	m.moveOnto(under)
	return nil
}
