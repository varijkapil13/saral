package issue

import (
	"context"
	"errors"
	"fmt"
	"os"
	"slices"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/varijkapil13/saral/internal/config"
	"github.com/varijkapil13/saral/internal/ui/kernel"
	"github.com/varijkapil13/saral/internal/ui/widget/sortpick"
	"github.com/varijkapil13/saral/pkg/jira"
	"github.com/varijkapil13/saral/pkg/jira/jiratest"
)

func useChildSort(t *testing.T, c sortpick.Choice) {
	t.Helper()
	childSortNow.Store(&c)
	t.Cleanup(func() {
		zero := sortpick.Choice{}
		childSortNow.Store(&zero)
		childSortSaveWarned.Store(false)
	})
}

func orderedKeys(issues []jira.Issue, c sortpick.Choice, o *childOrder) []string {
	idx := orderIndex(issues, c, o, nil)
	out := make([]string, len(idx))
	for i, at := range idx {
		out[i] = issues[at].Key
	}
	return out
}

func issueAt(key string, created int) jira.Issue {
	return jira.Issue{Key: key, Created: time.Date(2025, time.January, created, 9, 0, 0, 0, time.UTC)}
}

func TestChildSort_KeyIsNumericAware(t *testing.T) {
	t.Parallel()
	issues := []jira.Issue{{Key: "PROJ-10"}, {Key: "PROJ-2"}, {Key: "PROJ-1"}, {Key: "OTHER-3"}}
	got := orderedKeys(issues, sortpick.Choice{Field: "key"}, &childOrder{})
	if want := []string{"OTHER-3", "PROJ-1", "PROJ-2", "PROJ-10"}; !slices.Equal(got, want) {
		t.Errorf("by key: %v, want %v", got, want)
	}
	got = orderedKeys(issues, sortpick.Choice{Field: "key", Desc: true}, &childOrder{})
	if want := []string{"PROJ-10", "PROJ-2", "PROJ-1", "OTHER-3"}; !slices.Equal(got, want) {
		t.Errorf("by key descending: %v, want %v", got, want)
	}
}

func TestChildSort_StatusByCategoryThenName(t *testing.T) {
	t.Parallel()
	st := func(name string, c jira.StatusCategory) jira.Status { return jira.Status{Name: name, Category: c} }
	issues := []jira.Issue{
		{Key: "A-1", Status: st("Shipped", jira.CategoryDone)},
		{Key: "A-2", Status: st("Mystery", jira.CategoryUnknown)},
		{Key: "A-3", Status: st("Working", jira.CategoryInProgress)},
		{Key: "A-4", Status: st("Backlog", jira.CategoryToDo)},
		{Key: "A-5", Status: st("Accepted", jira.CategoryDone)},
		{Key: "A-6", Status: st("Zebra", jira.CategoryToDo)},
	}
	got := orderedKeys(issues, sortpick.Choice{Field: "status"}, &childOrder{})
	if want := []string{"A-4", "A-6", "A-3", "A-5", "A-1", "A-2"}; !slices.Equal(got, want) {
		t.Errorf("by status: %v, want %v", got, want)
	}
}

func TestChildSort_PriorityFollowsTheSiteOrderNotTheName(t *testing.T) {
	t.Parallel()
	f := newFake(1)
	list, err := f.Priorities(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	byName := slices.SortedFunc(slices.Values(list), func(a, b jira.Priority) int { return strings.Compare(a.Name, b.Name) })
	if slices.Equal(list, byName) {
		t.Fatal("the fake's priorities are already alphabetical, so this test proves nothing")
	}
	issues := make([]jira.Issue, 0, len(list))
	for i := range list {
		issues = append(issues, jira.Issue{Key: fmt.Sprintf("A-%d", i+1), Priority: &list[len(list)-1-i]})
	}
	done := childRead{
		key: "A-0", vocab: f, choice: sortpick.Choice{Field: fieldPriority}, bound: childrenSortBound,
	}.run(t.Context())
	if done.err != nil || done.warn != "" {
		t.Fatalf("reading the order: %v %q", done.err, done.warn)
	}
	got := orderedKeys(issues, sortpick.Choice{Field: fieldPriority}, &done.order)
	for i, key := range got {
		at := slices.IndexFunc(issues, func(c jira.Issue) bool { return c.Key == key })
		if issues[at].Priority.ID != list[i].ID {
			t.Errorf("position %d holds %s, the site ranks %s there", i, issues[at].Priority.Name, list[i].Name)
		}
	}
	desc := orderedKeys(issues, sortpick.Choice{Field: fieldPriority, Desc: true}, &done.order)
	slices.Reverse(got)
	if !slices.Equal(desc, got) {
		t.Errorf("descending is %v, want %v", desc, got)
	}
}

type failingVocab struct {
	jira.FilterVocabulary
	err   error
	calls int
}

func (v *failingVocab) Priorities(context.Context) ([]jira.Priority, error) {
	v.calls++
	return nil, v.err
}

func TestChildSort_PriorityReadFailsFallsBackToNameAndWarnsOnce(t *testing.T) {
	t.Parallel()
	for name, fail := range map[string]error{
		"403":       &jira.CapabilityError{Reason: "no Browse projects permission"},
		"429":       &jira.RateLimitError{RetryAfter: 30 * time.Second},
		"transport": errors.New("connection reset by peer"),
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			v := &failingVocab{err: fail}
			reason, _ := jira.Reason(fail)
			in := childRead{vocab: v, choice: sortpick.Choice{Field: fieldPriority}, bound: childrenSortBound}
			done := in.run(t.Context())
			if want := "priority order could not be read: " + reason + "; by name"; done.warn != want {
				t.Errorf("warning is %q, want %q", done.warn, want)
			}
			issues := []jira.Issue{
				{Key: "A-1", Priority: &jira.Priority{ID: "9", Name: "Zeta"}},
				{Key: "A-2", Priority: &jira.Priority{ID: "1", Name: "alpha"}},
				{Key: "A-3", Priority: &jira.Priority{ID: "5", Name: "Mid"}},
			}
			if got := orderedKeys(issues, sortpick.Choice{Field: fieldPriority}, &done.order); !slices.Equal(got, []string{"A-2", "A-3", "A-1"}) {
				t.Errorf("by name: %v", got)
			}

			kind := &childrenKind{}
			if cmd := kind.adopt(done); cmd == nil {
				t.Error("the first failure said nothing")
			}
			in.order = kind.order
			if again := in.run(t.Context()); v.calls != 1 || again.warn != "" {
				t.Errorf("a read that already failed was tried again: %d calls, warning %q", v.calls, again.warn)
			}
			in.order.prioTried = false
			again := in.run(t.Context())
			if v.calls != 2 || again.warn == "" {
				t.Fatalf("the second failure: %d calls, warning %q", v.calls, again.warn)
			}
			if cmd := kind.adopt(again); cmd != nil {
				t.Error("the second failure warned again")
			}
		})
	}
}

func TestChildSort_UndatedLastBothWays(t *testing.T) {
	t.Parallel()
	dated := func(key string, day int) jira.Issue {
		iss := issueAt(key, 1)
		iss.Due = jira.Date{Year: 2025, Month: time.June, Day: day}
		return iss
	}
	issues := []jira.Issue{issueAt("A-1", 1), dated("A-2", 20), dated("A-3", 5), issueAt("A-4", 2)}
	asc := orderedKeys(issues, sortpick.Choice{Field: "due"}, &childOrder{})
	if want := []string{"A-3", "A-2", "A-1", "A-4"}; !slices.Equal(asc, want) {
		t.Errorf("ascending: %v, want %v", asc, want)
	}
	desc := orderedKeys(issues, sortpick.Choice{Field: "due", Desc: true}, &childOrder{})
	if want := []string{"A-2", "A-3", "A-1", "A-4"}; !slices.Equal(desc, want) {
		t.Errorf("descending: %v, want %v", desc, want)
	}

	ada := &jira.User{DisplayName: "Ada"}
	who := []jira.Issue{{Key: "A-1"}, {Key: "A-2", Assignee: ada}, {Key: "A-3", Assignee: &jira.User{DisplayName: "bea"}}}
	for _, desc := range []bool{false, true} {
		got := orderedKeys(who, sortpick.Choice{Field: "assignee", Desc: desc}, &childOrder{})
		if got[len(got)-1] != "A-1" {
			t.Errorf("assignee desc=%v: %v puts the unassigned issue before an assigned one", desc, got)
		}
	}
}

func TestChildSort_TiesBreakByCreatedThenKey(t *testing.T) {
	t.Parallel()
	done := jira.Status{Name: "Done", Category: jira.CategoryDone}
	issues := []jira.Issue{
		{Key: "A-10", Status: done, Created: issueAt("", 3).Created},
		{Key: "A-2", Status: done, Created: issueAt("", 3).Created},
		{Key: "A-7", Status: done, Created: issueAt("", 1).Created},
	}
	for _, desc := range []bool{false, true} {
		got := orderedKeys(issues, sortpick.Choice{Field: "status", Desc: desc}, &childOrder{})
		if want := []string{"A-7", "A-2", "A-10"}; !slices.Equal(got, want) {
			t.Errorf("desc=%v: %v, want %v", desc, got, want)
		}
	}
}

func TestChildSort_SummaryAndTypeAreCaseFoldedAndByLevel(t *testing.T) {
	t.Parallel()
	issues := []jira.Issue{
		{Key: "A-1", Summary: "banana", Type: jira.IssueType{Name: "Task", HierarchyLevel: 0}},
		{Key: "A-2", Summary: "Apple", Type: jira.IssueType{Name: "Story", HierarchyLevel: 0}},
		{Key: "A-3", Summary: "cherry", Type: jira.IssueType{Name: "Epic", HierarchyLevel: 1}},
	}
	if got := orderedKeys(issues, sortpick.Choice{Field: "summary"}, &childOrder{}); !slices.Equal(got, []string{"A-2", "A-1", "A-3"}) {
		t.Errorf("by summary: %v", got)
	}
	if got := orderedKeys(issues, sortpick.Choice{Field: "type"}, &childOrder{}); !slices.Equal(got, []string{"A-2", "A-1", "A-3"}) {
		t.Errorf("by type: %v", got)
	}
}

func TestChildSort_RankOfferedOnlyWithOneLexoRankField(t *testing.T) {
	t.Parallel()
	rank := func(id string) jira.Field { return jira.Field{ID: id, Schema: jira.FieldSchema{Custom: lexoRankType}} }
	other := jira.Field{ID: "customfield_1", Schema: jira.FieldSchema{Custom: "x"}}
	for name, tc := range map[string]struct {
		fields []jira.Field
		want   string
	}{
		"none":  {[]jira.Field{other}, ""},
		"one":   {[]jira.Field{other, rank("customfield_7")}, "customfield_7"},
		"two":   {[]jira.Field{rank("customfield_7"), rank("customfield_8")}, ""},
		"empty": {nil, ""},
	} {
		if got := lexoRankID(tc.fields); got != tc.want {
			t.Errorf("%s: rank is %q, want %q", name, got, tc.want)
		}
	}

	o := childOrder{}
	if slices.ContainsFunc(o.fields(), func(f sortpick.Field) bool { return f.ID == fieldRank }) {
		t.Error("rank is offered with no rank field")
	}
	o.rankID = "customfield_7"
	if !slices.ContainsFunc(o.fields(), func(f sortpick.Field) bool { return f.ID == fieldRank }) {
		t.Error("rank is not offered with one rank field")
	}

	f, epic, _ := epicFake(t, 2)
	d := openChildrenSheet(t, f, epic)
	d.keys("s")
	if !d.s.picker.Open || !slices.ContainsFunc(d.s.picker.Fields, func(f sortpick.Field) bool { return f.ID == fieldRank }) {
		t.Errorf("the picker offers %v on a site with one rank field", d.s.picker.Fields)
	}
	mustContain(t, d.frame(), "rank")
}

func TestChildSort_RankOrdersByTheLexoValue(t *testing.T) {
	t.Parallel()
	set := func(key, rank string) jira.Issue {
		iss := jira.Issue{Key: key}
		if rank != "" {
			iss.Fields = jira.NewFieldSet(map[string]jira.FieldValue{"customfield_7": {Kind: jira.KindText, Text: rank}})
		}
		return iss
	}
	issues := []jira.Issue{set("A-1", "0|i00c"), set("A-2", ""), set("A-3", "0|i00a"), set("A-4", "0|i00b")}
	o := &childOrder{rankID: "customfield_7", rankTried: true}
	for _, desc := range []bool{false, true} {
		got := orderedKeys(issues, sortpick.Choice{Field: fieldRank, Desc: desc}, o)
		want := []string{"A-3", "A-4", "A-1", "A-2"}
		if desc {
			want = []string{"A-1", "A-4", "A-3", "A-2"}
		}
		if !slices.Equal(got, want) {
			t.Errorf("desc=%v: %v, want %v", desc, got, want)
		}
	}
}

func TestChildSort_SavedRankOnASiteWithoutOneDrawsDefaultAndKeepsFile(t *testing.T) {
	if err := config.SaveSort(childSortView, sortpick.Choice{Field: fieldRank, Desc: true}.Spec()); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = config.SaveSort(childSortView, config.SortSpec{}) })
	zero := sortpick.Choice{}
	childSortNow.Store(nil)
	t.Cleanup(func() { childSortNow.Store(&zero) })

	if got := currentChildSort(); got != (sortpick.Choice{Field: fieldRank, Desc: true}) {
		t.Fatalf("the saved choice read back as %+v", got)
	}
	d := syntheticSheet(t, 120, 30)
	if k := d.s.kind.(*childrenKind); k.applied.Chosen() {
		t.Errorf("a rank order on a site with no rank field drew %+v", d.s.kind.(*childrenKind).applied)
	}
	got := make([]string, len(d.s.rows))
	for i := range d.s.rows {
		got[i] = d.s.rows[i].key
	}
	if want := []string{"PROJ-31", "PROJ-32", "PROJ-33", "PROJ-34", "PROJ-35", "PROJ-36"}; !slices.Equal(got, want) {
		t.Errorf("rows are %v, want the site's order", got)
	}
	if spec, ok := config.LoadUIState().Sort(childSortView); !ok || spec.Field != fieldRank {
		t.Errorf("the file now holds %+v (%v), want the saved rank kept", spec, ok)
	}
}

func (c *sheetDriver) pickField(label string) {
	c.t.Helper()
	c.keys("s")
	for range len(c.s.picker.Fields) {
		if c.s.picker.Fields[c.s.picker.Cursor].ID == label {
			c.keys("enter")
			return
		}
		c.keys("right")
	}
	c.t.Fatalf("the picker offers no %q: %v", label, c.s.picker.Fields)
}

func (c *sheetDriver) rowKeys() []string {
	out := make([]string, len(c.s.rows))
	for i := range c.s.rows {
		out[i] = c.s.rows[i].key
	}
	return out
}

func TestChildrenSheet_SortReadsTheRestThenOrdersOnce(t *testing.T) {
	useChildSort(t, sortpick.Choice{})
	f, epic, _ := epicFake(t, 12, jiratest.WithPageSize(5))
	d := openChildrenSheet(t, f, epic)
	if len(d.s.rows) != 5 {
		t.Fatalf("the first page holds %d rows", len(d.s.rows))
	}
	before := countCalls(f, "Search")
	d.pickField("summary")

	if got := len(d.s.rows); got != 12 {
		t.Fatalf("after choosing, %d rows are held, want all 12", got)
	}
	if countCalls(f, "Search") == before {
		t.Error("choosing a field read nothing more")
	}
	want := make([]string, 0, 12)
	for _, n := range []int{1, 10, 11, 12, 2, 3, 4, 5, 6, 7, 8, 9} {
		want = append(want, fmt.Sprintf("PROJ-%d", firstChildNumber(d)+n-1))
	}
	if got := d.rowKeys(); !slices.Equal(got, want) {
		t.Errorf("rows are %v, want %v (summary order)", got, want)
	}
	mustContain(t, d.s.note, "12 children", "sort: summary ^")

	after := countCalls(f, "Search")
	d.pickField("key")
	if countCalls(f, "Search") != after {
		t.Error("a second order over rows already held read again")
	}
	if len(d.broadcasts) == 0 || d.broadcasts[len(d.broadcasts)-1] != (childSortMsg{}) {
		t.Errorf("no childSortMsg went out: %v", d.broadcasts)
	}
}

func firstChildNumber(d *childSheet) int {
	first := d.kind().issues[0].Key
	var n int
	_, _ = fmt.Sscanf(first[strings.LastIndexByte(first, '-')+1:], "%d", &n)
	return n
}

func TestChildrenSheet_SortStopsAtTheBoundAndSaysSo(t *testing.T) {
	useChildSort(t, sortpick.Choice{})
	f, epic, _ := epicFake(t, 12, jiratest.WithPageSize(5))
	d := openChildrenSheet(t, f, epic, func(k *childrenKind) { k.bound = 10 })
	d.pickField("summary")
	if got := len(d.s.rows); got != 10 {
		t.Fatalf("%d rows held, want the bound of 10", got)
	}
	mustContain(t, d.s.note, "10+ children", "sort: summary ^", "sorted over the first 10 of 10+")
	before := len(d.rowKeys())
	for range 12 {
		d.keys("j")
	}
	if len(d.s.rows) != before {
		t.Errorf("walking to the end paged a sorted sheet from %d to %d rows", before, len(d.s.rows))
	}
}

func TestChildrenSheet_CursorStaysOnItsKeyAcrossASort(t *testing.T) {
	useChildSort(t, sortpick.Choice{})
	d := syntheticSheet(t, 120, 30)
	d.keys("j", "j")
	if d.s.current().key != "PROJ-33" {
		t.Fatalf("cursor is on %s", d.s.current().key)
	}
	d.pickField("key")
	d.pickField("key")
	if d.s.current().key != "PROJ-33" {
		t.Errorf("the cursor moved to %s when the order changed", d.s.current().key)
	}
	if d.s.cursor != 3 {
		t.Errorf("the row for PROJ-33 sits at %d, want 3 in a descending key order", d.s.cursor)
	}
}

func TestChildrenSheet_ActOnSortedRowHitsTheRightIssue(t *testing.T) {
	useChildSort(t, sortpick.Choice{})
	f, epic, _ := epicFake(t, 4)
	d := openChildrenSheet(t, f, epic)
	d.pickField("key")
	d.pickField("key")
	if got := d.rowKeys(); !slices.IsSortedFunc(got, func(a, b string) int { return -compareIssueKeys(a, b) }) {
		t.Fatalf("rows are not in descending key order: %v", got)
	}
	for i := range d.s.rows {
		d.s.cursor = i
		if iss := d.kind().at(d.s); iss == nil || iss.Key != d.s.rows[i].key {
			t.Fatalf("row %d is %s and the action would hit %v", i, d.s.rows[i].key, iss)
		}
	}
	d.s.cursor = 0
	top := d.s.rows[0].key
	d.keys("@", "enter")
	if got := d.issue(top).Assignee; got == nil || got.AccountID != collabMe.AccountID {
		t.Errorf("%s is assigned to %+v after assigning the top row", top, got)
	}
	d.pushed = nil
	d.keys("enter")
	if len(d.pushed) != 1 || !strings.Contains(d.pushed[0].Title, top) {
		t.Errorf("enter on %s pushed %+v", top, d.pushed)
	}
}

func TestChildrenSheet_PickerEscLeavesTheOrderAndTheFileAlone(t *testing.T) {
	useChildSort(t, sortpick.Choice{})
	d := syntheticSheet(t, 120, 30)
	before := d.rowKeys()
	d.keys("s", "right", "right", "esc")
	if d.s.picker.Open {
		t.Error("esc left the picker open")
	}
	if !slices.Equal(d.rowKeys(), before) {
		t.Error("esc changed the order")
	}
	if got := currentChildSort(); got.Chosen() {
		t.Errorf("esc chose %+v", got)
	}
}

func TestChildrenSheet_PatchedChildKeepsSortFields(t *testing.T) {
	useChildSort(t, sortpick.Choice{Field: "updated", Desc: true})
	f, epic, kids := epicFake(t, 3)
	p := epicPane(t, f, f, epic, 50)
	m := p.editor()
	at := slices.IndexFunc(m.children, func(c jira.Issue) bool { return c.Key == kids[0] })
	created := m.children[at].Created
	if created.IsZero() {
		t.Fatal("the children were read without a creation time")
	}
	p.send(ChangedMsg{Key: kids[0]})
	at = slices.IndexFunc(m.children, func(c jira.Issue) bool { return c.Key == kids[0] })
	if !m.children[at].Created.Equal(created) {
		t.Errorf("a patched child lost its creation time: %v", m.children[at].Created)
	}
}

func TestChildrenInline_FollowsTheSheetsOrder(t *testing.T) {
	useChildSort(t, sortpick.Choice{})
	p := epicGolden(t, 120, 38)
	m := p.editor()
	inline := func() []string {
		g, _ := m.childGroup()
		out := make([]string, len(g.refs))
		for i := range g.refs {
			out[i] = g.refs[i].Key
		}
		return out
	}
	site := inline()

	childSortNow.Store(&sortpick.Choice{Field: "key", Desc: true})
	p.send(childSortMsg{})
	got := inline()
	if slices.Equal(got, site) {
		t.Fatal("the inline list did not move")
	}
	if !slices.IsSortedFunc(got, func(a, b string) int { return -compareIssueKeys(a, b) }) {
		t.Errorf("the first eight are %v, not the top of a descending key order", got)
	}
	mustContain(t, p.frame(), "sort: key v")
	golden(t, "children_inline_sorted_120x38.golden", p.frame())
}

func TestChildrenInline_DefaultOrderReadsNoMore(t *testing.T) {
	useChildSort(t, sortpick.Choice{})
	f, epic, _ := epicFake(t, 12, jiratest.WithPageSize(5))
	log := &searchLog{Client: f}
	p := epicPane(t, log, f, epic, 60)
	if got := len(log.seen()); got != 1 {
		t.Errorf("the pane made %d searches in the default order, want one", got)
	}
	p.send(childSortMsg{})
	if got := len(log.seen()); got != 1 {
		t.Errorf("a sort message in the default order cost %d searches", got)
	}
	if countCalls(f, "Priorities") != 0 {
		t.Error("the default order read the site's priorities")
	}
}

func TestChildrenInline_ANonDefaultOrderReadsTheRest(t *testing.T) {
	useChildSort(t, sortpick.Choice{})
	f, epic, _ := epicFake(t, 12, jiratest.WithPageSize(5))
	p := epicPane(t, f, f, epic, 60)
	m := p.editor()
	if len(m.children) != 5 {
		t.Fatalf("the first page holds %d", len(m.children))
	}
	childSortNow.Store(&sortpick.Choice{Field: "summary"})
	p.send(childSortMsg{})
	if len(m.children) != 12 {
		t.Errorf("the pane holds %d children after a non-default order, want all 12", len(m.children))
	}
}

func TestChildSort_SaveFailureWarnsOnce(t *testing.T) {
	useChildSort(t, sortpick.Choice{})
	blocker := t.TempDir() + "/file"
	if err := os.WriteFile(blocker, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("SARAL_CACHE_DIR", blocker+"/cache")

	warned := 0
	for _, c := range []sortpick.Choice{{Field: "key"}, {Field: "summary"}} {
		cmd := keepChildSort(c)
		if cmd == nil {
			continue
		}
		if status, ok := cmd().(kernel.StatusMsg); ok && status.Level == kernel.LevelWarn {
			warned++
			mustContain(t, status.Text, "will not survive a restart")
		}
	}
	if warned != 1 {
		t.Errorf("%d warnings for two failed saves, want one", warned)
	}
}

func TestChildSortSetting_SetSavesAndBroadcasts(t *testing.T) {
	useChildSort(t, sortpick.Choice{})
	var set kernel.Setting
	for _, s := range kernel.Settings() {
		if s.ID == childSortSetting {
			set = s
		}
	}
	if set.ID == "" {
		t.Fatal("the setting is not registered")
	}
	if set.Section != "Issue" || set.Scope != kernel.ScopeMachine || set.Kind != kernel.KindChoice {
		t.Errorf("the setting is %q, scope %v, kind %v", set.Section, set.Scope, set.Kind)
	}
	d := kernel.Deps{}
	if got := set.Value(d); got != "created:asc" {
		t.Errorf("the default reads as %q", got)
	}
	options := set.Options(d)
	if !slices.ContainsFunc(options, func(o kernel.SettingOption) bool { return o.ID == "rank:asc" && o.Note != "" }) {
		t.Errorf("the options do not offer rank with a note: %v", options)
	}

	var broadcast, status bool
	for _, msg := range drain(set.Set(d, "priority:desc")) {
		switch m := msg.(type) {
		case kernel.BroadcastMsg:
			broadcast = broadcast || m.Msg == (childSortMsg{})
		case kernel.StatusMsg:
			status = true
		}
	}
	if !broadcast || !status {
		t.Errorf("broadcast %v, status %v", broadcast, status)
	}
	if got := set.Value(d); got != "priority:desc" {
		t.Errorf("the value reads %q after setting", got)
	}
	spec, ok := config.LoadUIState().Sort(childSortView)
	if !ok || spec.Field != fieldPriority || !spec.Desc {
		t.Errorf("ui.toml holds %+v (%v)", spec, ok)
	}

	drain(set.Set(d, "created:asc"))
	if _, ok := config.LoadUIState().Sort(childSortView); ok {
		t.Error("the default order was written to the file instead of removing the entry")
	}
}

func TestChildrenSheet_SortedFrames(t *testing.T) {
	useChildSort(t, sortpick.Choice{})
	d := syntheticSheet(t, 120, 30)
	d.keys("s", "right", "right")
	golden(t, "sheet_children_sorting_120x30.golden", d.frame())

	ascii := syntheticSheet(t, 80, 20)
	ascii.keys("s")
	golden(t, "sheet_children_sorting_80x20_ascii.golden", ascii.frame())

	sorted := prioritySheet(t, 120, 30)
	sorted.pickField(fieldPriority)
	golden(t, "sheet_children_sorted_priority_120x30.golden", sorted.frame())
	k := sorted.kind()
	if !k.order.prioTried || k.order.prio == nil {
		t.Error("the priority order was not read from the site")
	}
}

func prioritySheet(t *testing.T, w, h int) *childSheet {
	t.Helper()
	f := newFake(2)
	list, err := f.Priorities(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	pick := func(i int) *jira.Priority { return &list[i] }
	story, task := jira.IssueType{ID: "10301", Name: "Story"}, jira.IssueType{ID: "10303", Name: "Task"}
	todo := jira.Status{Name: "To Do", Category: jira.CategoryToDo}
	issues := []jira.Issue{
		{ID: "1", Key: "PROJ-31", Summary: "Split the export job", Type: story, Status: todo, Priority: pick(2)},
		{ID: "2", Key: "PROJ-32", Summary: "Drop the cron entry", Type: task, Status: todo, Priority: pick(0)},
		{ID: "3", Key: "PROJ-33", Summary: "Write the migration", Type: story, Status: todo, Priority: pick(1)},
		{ID: "4", Key: "PROJ-34", Summary: "Backfill last year", Type: task, Status: todo},
		{ID: "5", Key: "PROJ-35", Summary: "Announce the cut-over", Type: story, Status: todo, Priority: pick(0)},
		{ID: "6", Key: "PROJ-36", Summary: "Remove the old endpoint", Type: task, Status: todo, Priority: pick(1)},
	}
	rec := record(f)
	kind := &childrenKind{seed: &childSeed{issues: issues}}
	s := newSheet(testDeps(t, rec), jira.Issue{Key: "PROJ-3", Summary: "Billing rewrite"}, kind)
	s.wait = 0
	dr := &sheetDriver{t: t, s: s}
	dr.send(kernel.SizeMsg{Width: w, Height: h})
	dr.run(s.Init())
	return &childSheet{sheetDriver: dr, rec: rec, f: f}
}

func drain(cmd tea.Cmd) []tea.Msg {
	var out []tea.Msg
	queue := []tea.Cmd{cmd}
	for len(queue) > 0 {
		next := queue[0]
		queue = queue[1:]
		if next == nil {
			continue
		}
		msg := next()
		if cmds, ok := unwrapCmds(msg); ok {
			queue = append(queue, cmds...)
			continue
		}
		out = append(out, msg)
	}
	return out
}
