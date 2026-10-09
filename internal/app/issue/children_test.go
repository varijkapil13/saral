package issue

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
	"testing"
	"time"

	appquery "github.com/varijkapil13/saral/internal/app/query"
	"github.com/varijkapil13/saral/pkg/jira"
	"github.com/varijkapil13/saral/pkg/jira/jiratest"
)

func orderedKeys(issues []jira.Issue, c ChildSort, o *ChildOrder) []string {
	idx := OrderIndex(issues, c, o, nil)
	out := make([]string, len(idx))
	for i, at := range idx {
		out[i] = issues[at].Key
	}
	return out
}

func issueAt(key string, created int) jira.Issue {
	return jira.Issue{Key: key, Created: time.Date(2025, time.January, created, 9, 0, 0, 0, time.UTC)}
}

func TestRollup(t *testing.T) {
	t.Parallel()
	done := jira.Issue{Status: jira.Status{Category: jira.CategoryDone}}
	open := jira.Issue{Status: jira.Status{Category: jira.CategoryToDo}}
	if n, d := Rollup([]jira.Issue{done, open, done}); n != 3 || d != 2 {
		t.Errorf("rollup is %d of %d", d, n)
	}
	if n, d := Rollup(nil); n != 0 || d != 0 {
		t.Errorf("rollup of nothing is %d of %d", d, n)
	}
}

func TestChildSort_KeyIsNumericAware(t *testing.T) {
	t.Parallel()
	issues := []jira.Issue{{Key: "PROJ-10"}, {Key: "PROJ-2"}, {Key: "PROJ-1"}, {Key: "OTHER-3"}}
	got := orderedKeys(issues, ChildSort{Field: "key"}, &ChildOrder{})
	if want := []string{"OTHER-3", "PROJ-1", "PROJ-2", "PROJ-10"}; !slices.Equal(got, want) {
		t.Errorf("by key: %v, want %v", got, want)
	}
	got = orderedKeys(issues, ChildSort{Field: "key", Desc: true}, &ChildOrder{})
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
	got := orderedKeys(issues, ChildSort{Field: "status"}, &ChildOrder{})
	if want := []string{"A-4", "A-6", "A-3", "A-5", "A-1", "A-2"}; !slices.Equal(got, want) {
		t.Errorf("by status: %v, want %v", got, want)
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
	asc := orderedKeys(issues, ChildSort{Field: "due"}, &ChildOrder{})
	if want := []string{"A-3", "A-2", "A-1", "A-4"}; !slices.Equal(asc, want) {
		t.Errorf("ascending: %v, want %v", asc, want)
	}
	desc := orderedKeys(issues, ChildSort{Field: "due", Desc: true}, &ChildOrder{})
	if want := []string{"A-2", "A-3", "A-1", "A-4"}; !slices.Equal(desc, want) {
		t.Errorf("descending: %v, want %v", desc, want)
	}

	ada := &jira.User{DisplayName: "Ada"}
	who := []jira.Issue{{Key: "A-1"}, {Key: "A-2", Assignee: ada}, {Key: "A-3", Assignee: &jira.User{DisplayName: "bea"}}}
	for _, desc := range []bool{false, true} {
		got := orderedKeys(who, ChildSort{Field: "assignee", Desc: desc}, &ChildOrder{})
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
		got := orderedKeys(issues, ChildSort{Field: "status", Desc: desc}, &ChildOrder{})
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
	if got := orderedKeys(issues, ChildSort{Field: "summary"}, &ChildOrder{}); !slices.Equal(got, []string{"A-2", "A-1", "A-3"}) {
		t.Errorf("by summary: %v", got)
	}
	if got := orderedKeys(issues, ChildSort{Field: "type"}, &ChildOrder{}); !slices.Equal(got, []string{"A-2", "A-1", "A-3"}) {
		t.Errorf("by type: %v", got)
	}
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
	o := &ChildOrder{RankID: "customfield_7", RankTried: true}
	for _, desc := range []bool{false, true} {
		got := orderedKeys(issues, ChildSort{Field: SortRank, Desc: desc}, o)
		want := []string{"A-3", "A-4", "A-1", "A-2"}
		if desc {
			want = []string{"A-1", "A-4", "A-3", "A-2"}
		}
		if !slices.Equal(got, want) {
			t.Errorf("desc=%v: %v, want %v", desc, got, want)
		}
	}
}

func TestChildSort_PriorityFollowsTheSiteOrderNotTheName(t *testing.T) {
	t.Parallel()
	f := testFake(1)
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
	done := ChildRead{
		Key: "A-0", Vocab: f, Choice: ChildSort{Field: SortPriority}, Bound: ChildrenSortBound,
	}.Run(t.Context())
	if done.Err != nil || done.PriorityErr != nil {
		t.Fatalf("reading the order: %v %v", done.Err, done.PriorityErr)
	}
	got := orderedKeys(issues, ChildSort{Field: SortPriority}, &done.Order)
	for i, key := range got {
		at := slices.IndexFunc(issues, func(c jira.Issue) bool { return c.Key == key })
		if issues[at].Priority.ID != list[i].ID {
			t.Errorf("position %d holds %s, the site ranks %s there", i, issues[at].Priority.Name, list[i].Name)
		}
	}
	desc := orderedKeys(issues, ChildSort{Field: SortPriority, Desc: true}, &done.Order)
	slices.Reverse(got)
	if !slices.Equal(desc, got) {
		t.Errorf("descending is %v, want %v", desc, got)
	}
}

func TestLexoRankID_OnlyWithOneLexoRankField(t *testing.T) {
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
		if got := LexoRankID(tc.fields); got != tc.want {
			t.Errorf("%s: rank is %q, want %q", name, got, tc.want)
		}
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

func TestChildRead_APriorityOrderThatFailsFallsBackToNameAndIsNotAskedAgain(t *testing.T) {
	t.Parallel()
	for name, fail := range failures() {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			v := &failingVocab{err: fail}
			in := ChildRead{Vocab: v, Choice: ChildSort{Field: SortPriority}, Bound: ChildrenSortBound}
			done := in.Run(t.Context())
			if !errors.Is(done.PriorityErr, fail) || done.Err != nil {
				t.Fatalf("priority err %v, err %v; want the site's refusal kept apart", done.PriorityErr, done.Err)
			}
			issues := []jira.Issue{
				{Key: "A-1", Priority: &jira.Priority{ID: "9", Name: "Zeta"}},
				{Key: "A-2", Priority: &jira.Priority{ID: "1", Name: "alpha"}},
				{Key: "A-3", Priority: &jira.Priority{ID: "5", Name: "Mid"}},
			}
			if got := orderedKeys(issues, ChildSort{Field: SortPriority}, &done.Order); !slices.Equal(got, []string{"A-2", "A-3", "A-1"}) {
				t.Errorf("by name: %v", got)
			}
			in.Order = done.Order
			if again := in.Run(t.Context()); v.calls != 1 || again.PriorityErr != nil {
				t.Errorf("a read that already failed was tried again: %d calls, err %v", v.calls, again.PriorityErr)
			}
		})
	}
}

func TestChildRead_ReadsThePageThenTheRestForAnOrderOfItsOwn(t *testing.T) {
	t.Parallel()
	f := jiratest.New(
		jiratest.WithProject("PROJ", jiratest.Scrum),
		jiratest.WithIssues(childrenOf("PROJ-1", 12)),
		jiratest.WithPageSize(5),
	)
	done := ChildRead{Key: "PROJ-1", Search: appquery.NewSearch(f), Choice: ChildSort{Field: "key", Desc: true}}.Run(t.Context())
	if done.Err != nil {
		t.Fatal(done.Err)
	}
	if len(done.Page.Items) != 12 || !done.RestRead || done.Page.HasMore() {
		t.Errorf("read %d children, rest read %v, more %v; want all 12", len(done.Page.Items), done.RestRead, done.Page.HasMore())
	}
	plain := ChildRead{Key: "PROJ-1", Search: appquery.NewSearch(f)}.Run(t.Context())
	if plain.Err != nil || len(plain.Page.Items) != 5 || plain.RestRead {
		t.Errorf("the default order read %d (rest %v, err %v), want one page", len(plain.Page.Items), plain.RestRead, plain.Err)
	}
}

func TestChildRead_AFailedSearchIsTheReadsError(t *testing.T) {
	t.Parallel()
	for name, fail := range failures() {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			f := testFake(2)
			f.FailNext(fail)
			done := ChildRead{Key: "PROJ-1", Search: appquery.NewSearch(f)}.Run(t.Context())
			if !errors.Is(done.Err, fail) {
				t.Errorf("err = %v, want %v", done.Err, fail)
			}
		})
	}
}

func TestRankField(t *testing.T) {
	t.Parallel()
	rank := jira.Field{ID: "customfield_7", Name: "Rank", Schema: jira.FieldSchema{Custom: lexoRankType}}
	f := jiratest.New(jiratest.WithFields([]jira.Field{rank}))
	if id, err := RankField(t.Context(), f); err != nil || id != "customfield_7" {
		t.Errorf("rank field %q, %v", id, err)
	}
	for name, fail := range failures() {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			f := jiratest.New(jiratest.WithFields([]jira.Field{rank}))
			f.FailNext(fail)
			if _, err := RankField(t.Context(), f); !errors.Is(err, fail) {
				t.Errorf("err = %v, want %v", err, fail)
			}
		})
	}
}

func TestChildSort_NormalizeAndEffective(t *testing.T) {
	t.Parallel()
	if got := NormalizeChildSort(ChildSort{Field: SortCreated}); got.Chosen() {
		t.Errorf("created ascending normalised to %+v, want the default", got)
	}
	if got := NormalizeChildSort(ChildSort{Field: "nonsense"}); got.Chosen() {
		t.Errorf("an unknown field normalised to %+v", got)
	}
	o := ChildOrder{RankTried: true}
	if got := o.Effective(ChildSort{Field: SortRank}); got.Chosen() {
		t.Errorf("rank on a site without one is %+v", got)
	}
	if !o.Needs(ChildSort{Field: SortPriority}, false, false, jira.Page[jira.Issue]{}) {
		t.Error("a priority order not yet read needs nothing")
	}
}

func childrenOf(parent string, n int) []jira.Issue {
	out := make([]jira.Issue, 0, n+1)
	out = append(out, jira.Issue{Key: parent, Summary: "Parent", Type: jira.IssueType{ID: "1", Name: "Epic", HierarchyLevel: 1}})
	for i := range n {
		out = append(out, jira.Issue{
			Key:     fmt.Sprintf("PROJ-%d", i+2),
			Summary: strings.Repeat("x", i+1),
			Parent:  &jira.IssueRef{Key: parent},
			Created: time.Date(2025, time.January, i+1, 9, 0, 0, 0, time.UTC),
		})
	}
	return out
}
