package issue

import (
	"context"
	"slices"
	"strconv"
	"strings"
	"sync/atomic"
	"unicode"
	"unicode/utf8"

	tea "charm.land/bubbletea/v2"

	"github.com/varijkapil13/saral/internal/app"
	"github.com/varijkapil13/saral/internal/config"
	"github.com/varijkapil13/saral/internal/ui/kernel"
	"github.com/varijkapil13/saral/internal/ui/widget/sortpick"
	"github.com/varijkapil13/saral/pkg/jira"
)

const (
	childSortView     = "issue.children"
	childSortSetting  = "issue.children.sort"
	childrenSortBound = 500

	fieldCreated  = "created"
	fieldPriority = "priority"
	fieldRank     = "rank"

	lexoRankType = "com.pyxis.greenhopper.jira:gh-lexo-rank"
)

type childSortMsg struct{}

type childField struct {
	sortpick.Field
	compare func(a, b *jira.Issue, o *childOrder) int
	missing func(*jira.Issue, *childOrder) bool
}

var childFields = []childField{
	{Field: sortpick.Field{ID: fieldCreated, Label: "created", Desc: true}, compare: compareCreated},
	{Field: sortpick.Field{ID: "updated", Label: "updated", Desc: true}, compare: func(a, b *jira.Issue, _ *childOrder) int {
		return a.Updated.Compare(b.Updated)
	}},
	{
		Field:   sortpick.Field{ID: "due", Label: "due", Desc: true},
		compare: func(a, b *jira.Issue, _ *childOrder) int { return compareDates(a.Due, b.Due) },
		missing: func(i *jira.Issue, _ *childOrder) bool { return i.Due.IsZero() },
	},
	{Field: sortpick.Field{ID: "key", Label: "key"}, compare: func(a, b *jira.Issue, _ *childOrder) int {
		return compareIssueKeys(a.Key, b.Key)
	}},
	{Field: sortpick.Field{ID: "summary", Label: "summary"}, compare: func(a, b *jira.Issue, _ *childOrder) int {
		return foldCompare(a.Summary, b.Summary)
	}},
	{Field: sortpick.Field{ID: "status", Label: "status"}, compare: compareStatus},
	{
		Field:   sortpick.Field{ID: fieldPriority, Label: "priority"},
		compare: comparePriority,
		missing: func(i *jira.Issue, _ *childOrder) bool { return i.Priority == nil },
	},
	{
		Field: sortpick.Field{ID: "assignee", Label: "assignee"},
		compare: func(a, b *jira.Issue, _ *childOrder) int {
			return foldCompare(a.Assignee.DisplayName, b.Assignee.DisplayName)
		},
		missing: func(i *jira.Issue, _ *childOrder) bool { return i.Assignee == nil },
	},
	{Field: sortpick.Field{ID: "type", Label: "type"}, compare: compareType},
	{
		Field:   sortpick.Field{ID: fieldRank, Label: "rank"},
		compare: compareRank,
		missing: func(i *jira.Issue, o *childOrder) bool {
			_, ok := i.Fields.Text(jira.FieldRef{ID: o.rankID})
			return !ok
		},
	},
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
		out = append(out, childFields[i].Field)
	}
	return out
}

func childFieldByID(id string) *childField {
	at := slices.IndexFunc(childFields, func(f childField) bool { return f.ID == id })
	if at < 0 {
		return nil
	}
	return &childFields[at]
}

func normalizeChoice(c sortpick.Choice) sortpick.Choice {
	if childFieldByID(c.Field) == nil || c == (sortpick.Choice{Field: fieldCreated}) {
		return sortpick.Choice{}
	}
	return c
}

type childOrder struct {
	prio       map[string]int
	prioTried  bool
	prioWarned bool
	rankID     string
	rankTried  bool
}

func (o *childOrder) effective(c sortpick.Choice) sortpick.Choice {
	c = normalizeChoice(c)
	if c.Field == fieldRank && o.rankTried && o.rankID == "" {
		return sortpick.Choice{}
	}
	return c
}

func (o *childOrder) fields() []sortpick.Field {
	if o.rankID != "" {
		return childPickFieldsRank
	}
	return childPickFields
}

func (o *childOrder) needs(raw sortpick.Choice, haveRank, restRead bool, page jira.Page[jira.Issue]) bool {
	raw = normalizeChoice(raw)
	switch {
	case raw.Field == fieldRank && (!o.rankTried || (o.rankID != "" && !haveRank)):
		return true
	case raw.Field == fieldPriority && !o.prioTried:
		return true
	}
	return o.effective(raw).Chosen() && page.HasMore() && !restRead
}

func (f *childField) comparer(desc bool, o *childOrder) func(a, b *jira.Issue) int {
	return func(a, b *jira.Issue) int {
		if f.missing != nil {
			am, bm := f.missing(a, o), f.missing(b, o)
			if am || bm {
				switch {
				case am && bm:
					return tieBreak(a, b)
				case am:
					return 1
				default:
					return -1
				}
			}
		}
		c := f.compare(a, b, o)
		if desc {
			c = -c
		}
		if c != 0 {
			return c
		}
		return tieBreak(a, b)
	}
}

func tieBreak(a, b *jira.Issue) int {
	if c := a.Created.Compare(b.Created); c != 0 {
		return c
	}
	return compareIssueKeys(a.Key, b.Key)
}

func orderIndex(issues []jira.Issue, c sortpick.Choice, o *childOrder, dst []int) []int {
	idx := dst[:0]
	for i := range issues {
		idx = append(idx, i)
	}
	f := childFieldByID(c.Field)
	if !c.Chosen() || f == nil {
		return idx
	}
	cmp := f.comparer(c.Desc, o)
	slices.SortStableFunc(idx, func(a, b int) int { return cmp(&issues[a], &issues[b]) })
	return idx
}

func compareIssueKeys(a, b string) int {
	ap, an, aok := splitKeySuffix(a)
	bp, bn, bok := splitKeySuffix(b)
	if aok && bok && ap == bp {
		return an - bn
	}
	return strings.Compare(a, b)
}

func splitKeySuffix(key string) (prefix string, n int, ok bool) {
	i := strings.LastIndexByte(key, '-')
	if i < 0 || i == len(key)-1 {
		return "", 0, false
	}
	num, err := strconv.Atoi(key[i+1:])
	if err != nil {
		return "", 0, false
	}
	return key[:i], num, true
}

func compareCreated(a, b *jira.Issue, _ *childOrder) int { return a.Created.Compare(b.Created) }

func compareDates(a, b jira.Date) int {
	switch {
	case a.Year != b.Year:
		return a.Year - b.Year
	case a.Month != b.Month:
		return int(a.Month) - int(b.Month)
	default:
		return a.Day - b.Day
	}
}

func statusRank(c jira.StatusCategory) int {
	switch c {
	case jira.CategoryToDo:
		return 0
	case jira.CategoryInProgress:
		return 1
	case jira.CategoryDone:
		return 2
	default:
		return 3
	}
}

func compareStatus(a, b *jira.Issue, _ *childOrder) int {
	if c := statusRank(a.Status.Category) - statusRank(b.Status.Category); c != 0 {
		return c
	}
	return foldCompare(a.Status.Name, b.Status.Name)
}

func comparePriority(a, b *jira.Issue, o *childOrder) int {
	if o.prio != nil {
		ap, aok := o.prio[a.Priority.ID]
		bp, bok := o.prio[b.Priority.ID]
		switch {
		case aok && bok && ap != bp:
			return ap - bp
		case aok && !bok:
			return -1
		case !aok && bok:
			return 1
		}
	}
	return foldCompare(a.Priority.Name, b.Priority.Name)
}

func compareType(a, b *jira.Issue, _ *childOrder) int {
	if c := a.Type.HierarchyLevel - b.Type.HierarchyLevel; c != 0 {
		return c
	}
	return foldCompare(a.Type.Name, b.Type.Name)
}

func compareRank(a, b *jira.Issue, o *childOrder) int {
	ref := jira.FieldRef{ID: o.rankID}
	at, _ := a.Fields.Text(ref)
	bt, _ := b.Fields.Text(ref)
	return strings.Compare(at, bt)
}

func foldCompare(a, b string) int {
	for a != "" && b != "" {
		ra, na := utf8.DecodeRuneInString(a)
		rb, nb := utf8.DecodeRuneInString(b)
		if ra != rb {
			if la, lb := unicode.ToLower(ra), unicode.ToLower(rb); la != lb {
				if la < lb {
					return -1
				}
				return 1
			}
		}
		a, b = a[na:], b[nb:]
	}
	switch {
	case a == "" && b == "":
		return 0
	case a == "":
		return -1
	default:
		return 1
	}
}

func lexoRankID(fields []jira.Field) string {
	id := ""
	for i := range fields {
		if fields[i].Schema.Custom != lexoRankType {
			continue
		}
		if id != "" {
			return ""
		}
		id = fields[i].ID
	}
	return id
}

func childProjection(rankID string) app.Projection {
	p := app.ListProjection()
	p.IDs = append(p.IDs, "created", "duedate")
	if rankID != "" {
		p.IDs = append(p.IDs, rankID)
	}
	return p
}

type childRead struct {
	key      string
	search   *app.Search
	vocab    jira.FilterVocabulary
	choice   sortpick.Choice
	order    childOrder
	page     jira.Page[jira.Issue]
	loaded   bool
	haveRank bool
	restRead bool
	bound    int
}

type childReadDone struct {
	order    childOrder
	page     jira.Page[jira.Issue]
	haveRank bool
	restRead bool
	warn     string
	err      error
}

func (r childRead) run(ctx context.Context) childReadDone {
	d := childReadDone{order: r.order, page: r.page, haveRank: r.haveRank, restRead: r.restRead}
	raw := normalizeChoice(r.choice)

	if raw.Field == fieldRank && !d.order.rankTried {
		d.order.rankTried = true
		if r.search != nil {
			fields, err := r.search.Fields(ctx)
			d.order.rankTried = err == nil
			if err == nil {
				d.order.rankID = lexoRankID(fields)
			}
		}
	}
	if raw.Field == fieldPriority && !d.order.prioTried {
		d.order.prioTried = true
		if r.vocab != nil {
			list, err := r.vocab.Priorities(ctx)
			if ctx.Err() != nil {
				d.err = ctx.Err()
				return d
			}
			if err != nil {
				reason, _ := jira.Reason(err)
				d.warn = "priority order could not be read: " + reason + "; by name"
			} else {
				d.order.prio = make(map[string]int, len(list))
				for i := range list {
					d.order.prio[list[i].ID] = i
				}
			}
		}
	}

	rankWanted := raw.Field == fieldRank && d.order.rankID != ""
	if r.search != nil && (!r.loaded || (rankWanted && !d.haveRank)) {
		rankID := ""
		if rankWanted {
			rankID = d.order.rankID
		}
		res, err := r.search.Run(ctx, app.Request{
			JQL: childrenJQL(r.key), Projection: childProjection(rankID), MaxResults: childrenPage,
		})
		if err != nil {
			d.err = err
			return d
		}
		d.page, d.haveRank, d.restRead = res.Page, rankWanted, false
	}

	if d.order.effective(raw).Chosen() && d.page.HasMore() && !d.restRead {
		bound := r.bound
		if bound <= 0 {
			bound = childrenSortBound
		}
		page := d.page
		items := slices.Clone(page.Items)
		for page.HasMore() && len(items) < bound {
			next, err := page.Next(ctx)
			if err != nil {
				d.err = err
				return d
			}
			items = append(items, next.Items...)
			page = next
		}
		page.Items = items
		d.page, d.restRead = page, true
	}
	return d
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
