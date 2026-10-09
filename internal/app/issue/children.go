package issue

import (
	"context"
	"slices"
	"strconv"
	"strings"
	"unicode"
	"unicode/utf8"

	appquery "github.com/varijkapil13/saral/internal/app/query"
	"github.com/varijkapil13/saral/pkg/jira"
)

// The children read: one page inline, and how far a sort reads before it
// stops and orders what it has.
const (
	ChildrenPage      = 50
	ChildrenSortBound = 500
)

// The sort fields the children read treats specially.
const (
	SortCreated  = "created"
	SortPriority = "priority"
	SortRank     = "rank"
)

const lexoRankType = "com.pyxis.greenhopper.jira:gh-lexo-rank"

// ChildrenJQL is the search for an issue's children, oldest first.
func ChildrenJQL(key string) string { return "parent = " + key + " ORDER BY created ASC" }

// Rollup counts children and how many of them are done.
func Rollup(children []jira.Issue) (n, done int) {
	for i := range children {
		if children[i].Status.Category == jira.CategoryDone {
			done++
		}
	}
	return len(children), done
}

// ChildProjection is the field set a child is read with; rankID is "" when
// no rank is wanted.
func ChildProjection(rankID string) appquery.Projection {
	p := appquery.ListProjection()
	p.IDs = append(p.IDs, "created", "duedate")
	if rankID != "" {
		p.IDs = append(p.IDs, rankID)
	}
	return p
}

// ChildSort is an order for an epic's children. The zero value is the default,
// created ascending.
type ChildSort struct {
	Field string
	Desc  bool
}

// Chosen reports an order other than the default.
func (c ChildSort) Chosen() bool { return c.Field != "" }

type childSort struct {
	id      string
	compare func(a, b *jira.Issue, o *ChildOrder) int
	missing func(*jira.Issue, *ChildOrder) bool
}

var childSorts = []childSort{
	{id: SortCreated, compare: compareCreated},
	{id: "updated", compare: func(a, b *jira.Issue, _ *ChildOrder) int {
		return a.Updated.Compare(b.Updated)
	}},
	{
		id:      "due",
		compare: func(a, b *jira.Issue, _ *ChildOrder) int { return compareDates(a.Due, b.Due) },
		missing: func(i *jira.Issue, _ *ChildOrder) bool { return i.Due.IsZero() },
	},
	{id: "key", compare: func(a, b *jira.Issue, _ *ChildOrder) int {
		return CompareIssueKeys(a.Key, b.Key)
	}},
	{id: "summary", compare: func(a, b *jira.Issue, _ *ChildOrder) int {
		return foldCompare(a.Summary, b.Summary)
	}},
	{id: "status", compare: compareStatus},
	{
		id:      SortPriority,
		compare: comparePriority,
		missing: func(i *jira.Issue, _ *ChildOrder) bool { return i.Priority == nil },
	},
	{
		id: "assignee",
		compare: func(a, b *jira.Issue, _ *ChildOrder) int {
			return foldCompare(a.Assignee.DisplayName, b.Assignee.DisplayName)
		},
		missing: func(i *jira.Issue, _ *ChildOrder) bool { return i.Assignee == nil },
	},
	{id: "type", compare: compareType},
	{
		id:      SortRank,
		compare: compareRank,
		missing: func(i *jira.Issue, o *ChildOrder) bool {
			_, ok := i.Fields.Text(jira.FieldRef{ID: o.RankID})
			return !ok
		},
	},
}

// ChildSortFields are the fields children can be ordered by, in the order they
// are offered.
func ChildSortFields() []string {
	out := make([]string, len(childSorts))
	for i := range childSorts {
		out[i] = childSorts[i].id
	}
	return out
}

func childSortByID(id string) *childSort {
	at := slices.IndexFunc(childSorts, func(f childSort) bool { return f.id == id })
	if at < 0 {
		return nil
	}
	return &childSorts[at]
}

// KnownChildSort reports a field children can be ordered by.
func KnownChildSort(id string) bool { return childSortByID(id) != nil }

// NormalizeChildSort folds an unknown field and the default into the zero value.
func NormalizeChildSort(c ChildSort) ChildSort {
	if childSortByID(c.Field) == nil || c == (ChildSort{Field: SortCreated}) {
		return ChildSort{}
	}
	return c
}

// ChildOrder is what ordering children has learnt from the site: the
// priority order and the rank field, each asked for at most once.
type ChildOrder struct {
	Prio      map[string]int
	PrioTried bool
	RankID    string
	RankTried bool
}

// Effective is the order actually applied: a rank order on a site found to
// have no rank field is the default.
func (o *ChildOrder) Effective(c ChildSort) ChildSort {
	c = NormalizeChildSort(c)
	if c.Field == SortRank && o.RankTried && o.RankID == "" {
		return ChildSort{}
	}
	return c
}

// Needs reports whether ordering by raw has to ask the site anything first.
func (o *ChildOrder) Needs(raw ChildSort, haveRank, restRead bool, page jira.Page[jira.Issue]) bool {
	raw = NormalizeChildSort(raw)
	switch {
	case raw.Field == SortRank && (!o.RankTried || (o.RankID != "" && !haveRank)):
		return true
	case raw.Field == SortPriority && !o.PrioTried:
		return true
	}
	return o.Effective(raw).Chosen() && page.HasMore() && !restRead
}

func (f *childSort) comparer(desc bool, o *ChildOrder) func(a, b *jira.Issue) int {
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
	return CompareIssueKeys(a.Key, b.Key)
}

// OrderIndex is the order of issues under c as indexes into issues, reusing dst.
func OrderIndex(issues []jira.Issue, c ChildSort, o *ChildOrder, dst []int) []int {
	idx := dst[:0]
	for i := range issues {
		idx = append(idx, i)
	}
	f := childSortByID(c.Field)
	if !c.Chosen() || f == nil {
		return idx
	}
	cmp := f.comparer(c.Desc, o)
	slices.SortStableFunc(idx, func(a, b int) int { return cmp(&issues[a], &issues[b]) })
	return idx
}

// CompareIssueKeys orders keys of one project by number, and anything else as
// text.
func CompareIssueKeys(a, b string) int {
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

func compareCreated(a, b *jira.Issue, _ *ChildOrder) int { return a.Created.Compare(b.Created) }

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

func compareStatus(a, b *jira.Issue, _ *ChildOrder) int {
	if c := statusRank(a.Status.Category) - statusRank(b.Status.Category); c != 0 {
		return c
	}
	return foldCompare(a.Status.Name, b.Status.Name)
}

func comparePriority(a, b *jira.Issue, o *ChildOrder) int {
	if o.Prio != nil {
		ap, aok := o.Prio[a.Priority.ID]
		bp, bok := o.Prio[b.Priority.ID]
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

func compareType(a, b *jira.Issue, _ *ChildOrder) int {
	if c := a.Type.HierarchyLevel - b.Type.HierarchyLevel; c != 0 {
		return c
	}
	return foldCompare(a.Type.Name, b.Type.Name)
}

func compareRank(a, b *jira.Issue, o *ChildOrder) int {
	ref := jira.FieldRef{ID: o.RankID}
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

// LexoRankID is the site's one rank field, or "" when it has none or more than
// one.
func LexoRankID(fields []jira.Field) string {
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

// RankField looks the site's rank field up.
func RankField(ctx context.Context, catalogue jira.FieldCatalogue) (string, error) {
	fields, err := catalogue.Fields(ctx)
	if err != nil {
		return "", err
	}
	return LexoRankID(fields), nil
}

// ChildRead is one read of an issue's children in an order: what is already
// held, and what the order needs from the site.
type ChildRead struct {
	Key      string
	Search   *appquery.Search
	Vocab    jira.FilterVocabulary
	Choice   ChildSort
	Order    ChildOrder
	Page     jira.Page[jira.Issue]
	Loaded   bool
	HaveRank bool
	RestRead bool
	Bound    int
}

// ChildReadDone is what a read came back with. PriorityErr is a priority order
// that could not be read, which falls back to ordering by name.
type ChildReadDone struct {
	Order       ChildOrder
	Page        jira.Page[jira.Issue]
	HaveRank    bool
	RestRead    bool
	PriorityErr error
	Err         error
}

// Run reads what the order needs: the rank field, the priority order, the
// first page, and the rest up to the bound when the order is not the site's.
func (r ChildRead) Run(ctx context.Context) ChildReadDone {
	d := ChildReadDone{Order: r.Order, Page: r.Page, HaveRank: r.HaveRank, RestRead: r.RestRead}
	raw := NormalizeChildSort(r.Choice)

	if raw.Field == SortRank && !d.Order.RankTried {
		d.Order.RankTried = true
		if r.Search != nil {
			id, err := RankField(ctx, r.Search)
			d.Order.RankTried = err == nil
			if err == nil {
				d.Order.RankID = id
			}
		}
	}
	if raw.Field == SortPriority && !d.Order.PrioTried {
		d.Order.PrioTried = true
		if r.Vocab != nil {
			list, err := r.Vocab.Priorities(ctx)
			if ctx.Err() != nil {
				d.Err = ctx.Err()
				return d
			}
			if err != nil {
				d.PriorityErr = err
			} else {
				d.Order.Prio = make(map[string]int, len(list))
				for i := range list {
					d.Order.Prio[list[i].ID] = i
				}
			}
		}
	}

	rankWanted := raw.Field == SortRank && d.Order.RankID != ""
	if r.Search != nil && (!r.Loaded || (rankWanted && !d.HaveRank)) {
		rankID := ""
		if rankWanted {
			rankID = d.Order.RankID
		}
		res, err := r.Search.Run(ctx, appquery.Request{
			JQL: ChildrenJQL(r.Key), Projection: ChildProjection(rankID), MaxResults: ChildrenPage,
		})
		if err != nil {
			d.Err = err
			return d
		}
		d.Page, d.HaveRank, d.RestRead = res.Page, rankWanted, false
	}

	if d.Order.Effective(raw).Chosen() && d.Page.HasMore() && !d.RestRead {
		bound := r.Bound
		if bound <= 0 {
			bound = ChildrenSortBound
		}
		page := d.Page
		items := slices.Clone(page.Items)
		for page.HasMore() && len(items) < bound {
			next, err := page.Next(ctx)
			if err != nil {
				d.Err = err
				return d
			}
			items = append(items, next.Items...)
			page = next
		}
		page.Items = items
		d.Page, d.RestRead = page, true
	}
	return d
}
