package issue

import (
	"slices"
	"strings"
	"sync/atomic"

	tea "charm.land/bubbletea/v2"

	appissue "github.com/varijkapil13/saral/internal/app/issue"
	"github.com/varijkapil13/saral/internal/config"
	"github.com/varijkapil13/saral/internal/ui/kernel"
	"github.com/varijkapil13/saral/internal/ui/widget/sortpick"
	"github.com/varijkapil13/saral/pkg/jira"
)

const (
	childSortView    = "issue.children"
	childSortSetting = "issue.children.sort"

	fieldCreated  = appissue.SortCreated
	fieldPriority = appissue.SortPriority
	fieldRank     = appissue.SortRank
)

type childSortMsg struct{}

var childFields = []sortpick.Field{
	{ID: fieldCreated, Label: "created", Desc: true},
	{ID: "updated", Label: "updated", Desc: true},
	{ID: "due", Label: "due", Desc: true},
	{ID: "key", Label: "key"},
	{ID: "summary", Label: "summary"},
	{ID: "status", Label: "status"},
	{ID: fieldPriority, Label: "priority"},
	{ID: "assignee", Label: "assignee"},
	{ID: "type", Label: "type"},
	{ID: fieldRank, Label: "rank"},
}

var (
	childPickFields     = pickFields(false)
	childPickFieldsRank = pickFields(true)
)

func pickFields(withRank bool) []sortpick.Field {
	out := make([]sortpick.Field, 0, len(childFields))
	for i := range childFields {
		if childFields[i].ID == fieldRank && !withRank {
			continue
		}
		out = append(out, childFields[i])
	}
	return out
}

func childFieldByID(id string) *sortpick.Field {
	at := slices.IndexFunc(childFields, func(f sortpick.Field) bool { return f.ID == id })
	if at < 0 {
		return nil
	}
	return &childFields[at]
}

func normalizeChoice(c sortpick.Choice) sortpick.Choice {
	return sortpick.Choice(appissue.NormalizeChildSort(appissue.ChildSort(c)))
}

// childOrder is what the children's order has learnt from the site, and
// whether the user has been told the priority order could not be read.
type childOrder struct {
	appissue.ChildOrder
	prioWarned bool
}

func (o *childOrder) effective(c sortpick.Choice) sortpick.Choice {
	return sortpick.Choice(o.Effective(appissue.ChildSort(c)))
}

func (o *childOrder) fields() []sortpick.Field {
	if o.RankID != "" {
		return childPickFieldsRank
	}
	return childPickFields
}

func (o *childOrder) needs(raw sortpick.Choice, haveRank, restRead bool, page jira.Page[jira.Issue]) bool {
	return o.Needs(appissue.ChildSort(raw), haveRank, restRead, page)
}

func orderIndex(issues []jira.Issue, c sortpick.Choice, o *childOrder, dst []int) []int {
	return appissue.OrderIndex(issues, appissue.ChildSort(c), &o.ChildOrder, dst)
}

func priorityWarning(err error) string {
	if err == nil {
		return ""
	}
	reason, _ := jira.Reason(err)
	return "priority order could not be read: " + reason + "; by name"
}

var (
	childSortNow        atomic.Pointer[sortpick.Choice]
	childSortSaveWarned atomic.Bool
)

func loadChildSort() sortpick.Choice {
	spec, ok := config.LoadUIState().Sort(childSortView)
	if !ok {
		return sortpick.Choice{}
	}
	c, ok := sortpick.FromSpec(spec, childPickFieldsRank)
	if !ok {
		return sortpick.Choice{}
	}
	return normalizeChoice(c)
}

func currentChildSort() sortpick.Choice {
	if p := childSortNow.Load(); p != nil {
		return *p
	}
	c := loadChildSort()
	childSortNow.CompareAndSwap(nil, &c)
	return *childSortNow.Load()
}

func setChildSort(c sortpick.Choice) tea.Cmd {
	c = normalizeChoice(c)
	childSortNow.Store(&c)
	return tea.Batch(keepChildSort(c), kernel.Broadcast(childSortMsg{}))
}

func keepChildSort(c sortpick.Choice) tea.Cmd {
	if childSortSaveWarned.Load() {
		return nil
	}
	spec := c.Spec()
	return func() tea.Msg {
		err := config.SaveSort(childSortView, spec)
		if err == nil || !childSortSaveWarned.CompareAndSwap(false, true) {
			return nil
		}
		return kernel.Warn("the order of children on screen will not survive a restart: " + err.Error())()
	}
}

func sortLabel(c sortpick.Choice, g kernel.Glyphs) string {
	f := childFieldByID(c.Field)
	if f == nil {
		return ""
	}
	return f.Label + " " + sortpick.Arrow(c.Desc, g)
}

func init() { kernel.RegisterSetting(childSortSettingSpec()) }

func childSortOptionID(c sortpick.Choice) string {
	field, dir := c.Field, "asc"
	if field == "" {
		field = fieldCreated
	}
	if c.Desc {
		dir = "desc"
	}
	return field + ":" + dir
}

func childSortSettingSpec() kernel.Setting {
	return kernel.Setting{
		ID:      childSortSetting,
		Section: "Issue",
		Title:   "Order of an epic's children",
		Summary: "the order the children sheet and the pane's list read an epic's children in",
		Kind:    kernel.KindChoice,
		Scope:   kernel.ScopeMachine,
		Options: func(d kernel.Deps) []kernel.SettingOption {
			g := kernel.UnicodeGlyphs()
			if d.Theme != nil {
				g = d.Theme.Glyphs
			}
			out := make([]kernel.SettingOption, 0, 2*len(childPickFieldsRank))
			for _, f := range childPickFieldsRank {
				for _, desc := range [...]bool{false, true} {
					o := kernel.SettingOption{
						ID:    childSortOptionID(sortpick.Choice{Field: f.ID, Desc: desc}),
						Label: f.Label + " " + sortpick.Arrow(desc, g),
					}
					if f.ID == fieldRank {
						o.Note = "only where the site has a rank field"
					}
					out = append(out, o)
				}
			}
			return out
		},
		Value: func(kernel.Deps) string { return childSortOptionID(currentChildSort()) },
		Set: func(_ kernel.Deps, id string) tea.Cmd {
			field, dir, _ := strings.Cut(id, ":")
			c := sortpick.Choice{Field: field, Desc: dir == "desc"}
			if childFieldByID(field) == nil {
				return kernel.Warn("there is no order called " + id)
			}
			return tea.Batch(setChildSort(c), kernel.Status("children are now ordered by "+sortLabel(c, kernel.UnicodeGlyphs())))
		},
	}
}
