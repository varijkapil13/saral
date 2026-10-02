package release

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/varijkapil13/saral/internal/config"
	"github.com/varijkapil13/saral/internal/ui/kernel"
	"github.com/varijkapil13/saral/pkg/jira"
	"github.com/varijkapil13/saral/pkg/jira/jiratest"
)

const (
	spring = "Spring launch"
	summer = "Summer launch"
)

func owner(ref string) Owner { return Owner{Ref: ref, Label: ref} }

// Today is 2026-03-05 in testDeps.
func planSet() Set {
	v := func(id, name, pid string, start, release jira.Date, mods ...func(*jira.Version)) jira.Version {
		out := jira.Version{ID: id, Name: name, ProjectID: pid, StartDate: start, ReleaseDate: release}
		for _, mod := range mods {
			mod(&out)
		}
		return out
	}
	released := func(x *jira.Version) { x.Released = true }
	archived := func(x *jira.Version) { x.Archived = true }
	described := func(text string) func(*jira.Version) {
		return func(x *jira.Version) { x.Description = text }
	}
	none := jira.Date{}
	return Set{
		Title:   "Delivery: 11 releases from EX, OPS, WEB",
		Explain: "cross-space release = one release across several projects (\"spaces\")",
		Members: []Member{
			{v("1001", "2.4.0", "11", day(2026, 2, 1), day(2026, 3, 1), described("checkout rewrite")), owner("EX")},
			{v("1002", "2.3.0", "11", day(2026, 2, 1), day(2026, 2, 20), released), owner("EX")},
			{v("2001", "ops-2026.3", "22", day(2026, 2, 10), day(2026, 4, 15)), owner("OPS")},
			{v("3001", "web-7", "33", day(2026, 2, 15), day(2026, 4, 30)), owner("WEB")},
			{v("1003", "2.5.0", "11", day(2026, 5, 1), day(2026, 6, 15)), owner("EX")},
			{v("1004", "2.6.0", "11", day(2026, 6, 1), day(2026, 7, 31)), owner("EX")},
			{v("2002", "ops-2026.4", "22", day(2026, 5, 10), day(2026, 7, 20)), owner("OPS")},
			{v("3002", "web-8", "33", day(2026, 5, 20), day(2026, 8, 15)), owner("WEB")},
			{v("4001", "1.9.x-hotfix", "44", none, day(2026, 3, 10)), Owner{Ref: "10400", Label: "id 10400"}},
			{v("2003", "ops-2026.1", "22", none, day(2026, 1, 20), released), owner("OPS")},
			{v("3003", "web-hotfix", "33", none, none, archived), owner("WEB")},
		},
		Groups: []Group{
			{Name: spring, VersionIDs: []string{"1001", "1002", "2001", "3001"}},
			{Name: summer, VersionIDs: []string{"1003", "1004", "2002"}},
		},
		Excluded: map[string]bool{"2003": true, "3003": true},
		Notes:    []string{"board 18 left out: the site named no project behind it"},
	}
}

func setOf(t *testing.T, d kernel.Deps, s Set, w, h int) *driver {
	t.Helper()
	return newDriver(t, NewSet(d, s), w, h)
}

func drawn(m *Model) []string {
	out := make([]string, 0, len(m.order))
	for _, sl := range m.order {
		if sl.v < 0 {
			out = append(out, "# "+m.set.heads[sl.g].name)
			continue
		}
		out = append(out, m.versions[sl.v].Name)
	}
	return out
}

func TestSet_GroupsByCrossSpaceReleaseThenProjectThenNone(t *testing.T) {
	t.Parallel()

	dr := setOf(t, testDeps(nil), planSet(), 120, 30)
	m := dr.list()
	for _, step := range []struct {
		name string
		want []string
	}{
		{"by cross-space release", []string{
			"# " + spring, "2.4.0", "2.3.0", "ops-2026.3", "web-7",
			"# " + summer, "2.5.0", "2.6.0", "ops-2026.4",
			"# " + ungroupedName, "web-8", "1.9.x-hotfix",
		}},
		{"by project", []string{
			"# EX", "2.4.0", "2.3.0", "2.5.0", "2.6.0",
			"# OPS", "ops-2026.3", "ops-2026.4",
			"# WEB", "web-7", "web-8",
			"# id 10400", "1.9.x-hotfix",
		}},
		{"ungrouped", []string{
			"2.4.0", "2.3.0", "ops-2026.3", "web-7", "2.5.0", "2.6.0", "ops-2026.4", "web-8", "1.9.x-hotfix",
		}},
	} {
		if got := drawn(m); !slices.Equal(got, step.want) {
			t.Errorf("%s draws\n%v\nwant\n%v", step.name, got, step.want)
		}
		mustContain(t, dr.view(), step.name)
		dr.key("v")
	}
	if m.set.arrange != arrCross {
		t.Errorf("a third v left the arrangement at %q, want it back at cross-space", arrangementNames[m.set.arrange])
	}
}

func TestSet_UngroupedVersionsFallUnderTheirOwnHeader(t *testing.T) {
	t.Parallel()

	dr := setOf(t, testDeps(nil), planSet(), 120, 30)
	got := drawn(dr.list())
	at := slices.Index(got, "# "+ungroupedName)
	if at < 0 || at != len(got)-3 {
		t.Fatalf("the versions in no release are not drawn last under a header of their own: %v", got)
	}
	mustContain(t, dr.view(), ungroupedName+"   2 of 4")
}

func TestSet_AVersionNamedByTwoGroupsIsDrawnUnderTheFirst(t *testing.T) {
	t.Parallel()

	s := planSet()
	s.Groups = append(s.Groups, Group{Name: "Autumn launch", VersionIDs: []string{"1001", "1003"}})
	dr := setOf(t, testDeps(nil), s, 120, 30)
	got := drawn(dr.list())

	if n := strings.Count(strings.Join(got, "|"), "2.4.0"); n != 1 {
		t.Errorf("a version two groups name is drawn %d times: %v", n, got)
	}
	if slices.Contains(got, "# Autumn launch") {
		t.Errorf("a group whose every version is drawn under an earlier one has a header: %v", got)
	}
	if i, j := slices.Index(got, "# "+spring), slices.Index(got, "2.4.0"); j < i || j > slices.Index(got, "# "+summer) {
		t.Errorf("2.4.0 is not under the first group that names it: %v", got)
	}
}

func TestSet_HeaderSaysCountProjectsSpanAndReleasedState(t *testing.T) {
	t.Parallel()

	allShipped := planSet()
	for i := range allShipped.Members {
		switch allShipped.Members[i].Version.ID {
		case "1003", "1004", "2002":
			allShipped.Members[i].Version.Released = true
		}
	}
	for name, tc := range map[string]struct {
		set  Set
		now  time.Time
		want []string
	}{
		"some of it shipped": {
			set: planSet(), now: time.Date(2026, 3, 5, 9, 0, 0, 0, time.UTC),
			want: []string{
				spring + "   4 of 4 · EX, OPS, WEB · 2026-02-01 to 2026-04-30 · 1 of 4 released",
				summer + "   3 of 3 · EX, OPS · 2026-05-01 to 2026-07-31 · 0 of 3 released",
			},
		},
		"all of it shipped": {
			set: allShipped, now: time.Date(2026, 3, 5, 9, 0, 0, 0, time.UTC),
			want: []string{summer + "   3 of 3 · EX, OPS · 2026-05-01 to 2026-07-31 · all released"},
		},
		"past its last release date": {
			set: planSet(), now: time.Date(2026, 9, 1, 9, 0, 0, 0, time.UTC),
			want: []string{
				spring + "   4 of 4 · EX, OPS, WEB · 2026-02-01 to 2026-04-30 · overdue",
				summer + "   3 of 3 · EX, OPS · 2026-05-01 to 2026-07-31 · overdue",
			},
		},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			d := testDeps(nil)
			d.Now = func() time.Time { return tc.now }
			dr := setOf(t, d, tc.set, 140, 30)
			mustContain(t, dr.view(), tc.want...)
		})
	}
}

func TestSet_GroupsFollowTheSortAndMembersSortWithinThem(t *testing.T) {
	t.Parallel()

	dr := setOf(t, testDeps(nil), planSet(), 120, 30)
	m := dr.list()
	m.sort = sortChoice{field: "release", desc: true}
	m.reorder()

	want := []string{
		"# " + summer, "2.6.0", "ops-2026.4", "2.5.0",
		"# " + spring, "web-7", "ops-2026.3", "2.4.0", "2.3.0",
		"# " + ungroupedName, "web-8", "1.9.x-hotfix",
	}
	if got := drawn(m); !slices.Equal(got, want) {
		t.Errorf("by release date, newest first, the set draws\n%v\nwant\n%v", got, want)
	}
}

func TestSet_ExcludedAreHiddenUntilDotThenMarked(t *testing.T) {
	t.Parallel()

	dr := setOf(t, testDeps(nil), planSet(), 120, 30)
	m := dr.list()
	mustNotContain(t, dr.view(), "ops-2026.1", "web-hotfix")
	mustContain(t, dr.view(), "2 excluded by the plan, . shows", "9 of 11 versions")

	dr.key(".")
	frame := dr.view()
	mustContain(t, frame, "ops-2026.1", "web-hotfix", "excluded by the plan, shown, . hides", "11 versions")
	marked := 0
	for _, line := range strings.Split(frame, "\n") {
		if strings.HasSuffix(strings.TrimRight(line, " "), excludedCell) {
			marked++
		}
	}
	if marked != 2 {
		t.Errorf("%d rows are marked as excluded by the plan, want the 2 shown", marked)
	}
	mustContain(t, frame, ungroupedName+"   4 of 4")

	dr.key(".")
	if got := drawn(m); slices.Contains(got, "web-hotfix") {
		t.Errorf("a second . did not hide the excluded versions again: %v", got)
	}
}

func TestSet_ProjectFacetStepsThroughTheSetsProjects(t *testing.T) {
	t.Parallel()

	dr := setOf(t, testDeps(nil), planSet(), 120, 30)
	m := dr.list()
	for _, want := range []struct {
		pick     string
		versions []string
	}{
		{"EX", []string{"2.4.0", "2.3.0", "2.5.0", "2.6.0"}},
		{"OPS", []string{"ops-2026.3", "ops-2026.4"}},
		{"WEB", []string{"web-7", "web-8"}},
		{"10400", []string{"1.9.x-hotfix"}},
		{"", nil},
	} {
		dr.key("f", "tab", "l", "enter")
		if m.set.pick != want.pick {
			t.Fatalf("the project facet moved the project filter to %q, want %q", m.set.pick, want.pick)
		}
		if want.pick == "" {
			if m.set.shown != 9 {
				t.Errorf("the last step did not bring every project back: %d versions are drawn", m.set.shown)
			}
			continue
		}
		var got []string
		for _, name := range drawn(m) {
			if !strings.HasPrefix(name, "# ") {
				got = append(got, name)
			}
		}
		if !slices.Equal(got, want.versions) {
			t.Errorf("project %s draws %v, want %v", want.pick, got, want.versions)
		}
		mustContain(t, dr.view(), "project "+m.set.pickLabel())
	}
}

func TestSet_TextFilterMatchesVersionAndGroupCaseFolded(t *testing.T) {
	t.Parallel()

	dr := setOf(t, testDeps(nil), planSet(), 120, 30)
	m := dr.list()
	dr.key("/")
	dr.typeText("SPRING")
	want := []string{"# " + spring, "2.4.0", "2.3.0", "ops-2026.3", "web-7"}
	if got := drawn(m); !slices.Equal(got, want) {
		t.Errorf("a group's name, in capitals, draws %v, want its versions %v", got, want)
	}

	for range "SPRING" {
		dr.key("backspace")
	}
	dr.typeText("OPS-2026.4")
	if got, want := drawn(m), []string{"# " + summer, "ops-2026.4"}; !slices.Equal(got, want) {
		t.Errorf("a version's name, in capitals, draws %v, want %v", got, want)
	}

	dr.key("enter")
	if m.WantsRawKeys() {
		t.Error("enter kept the text and the list still claims every key")
	}
	if got, want := drawn(m), []string{"# " + summer, "ops-2026.4"}; !slices.Equal(got, want) {
		t.Errorf("enter lost the filter: %v", got)
	}
	mustContain(t, dr.view(), `matching "OPS-2026.4"`)

	dr.key("/", "esc")
	if m.find.needle != "" || m.find.rawNeedle != "" {
		t.Errorf("esc kept the text %q", m.find.rawNeedle)
	}
	if len(drawn(m)) != 12 {
		t.Errorf("esc did not bring every version back: %v", drawn(m))
	}
}

func TestSet_TypingTheFilterTakesEveryKeyAsText(t *testing.T) {
	t.Parallel()

	dr := setOf(t, testDeps(nil), planSet(), 120, 30)
	m := dr.list()
	if m.WantsRawKeys() {
		t.Fatal("a set claims raw keys while it is only being read")
	}
	dr.key("j", "j")
	dr.key("/")
	if !m.WantsRawKeys() {
		t.Fatal("the text filter does not claim raw keys, so q quits and a digit switches view")
	}
	dr.typeText("q1jk2g")
	if got := m.find.input.Value(); got != "q1jk2g" {
		t.Errorf("q, 1, j, k, 2 and g typed into the filter left %q", got)
	}
	if dr.pops != 0 || len(dr.pushes) != 0 || len(dr.statuses) != 0 {
		t.Errorf("typing into the filter asked the kernel for something: %d pops, %d pushes, %d statuses",
			dr.pops, len(dr.pushes), len(dr.statuses))
	}
	mustContain(t, dr.view(), "/ q1jk2g")
	if _, blocked := m.BlocksClose(); blocked {
		t.Error("a filter being typed blocks closing the list")
	}
}

func TestSet_EnterFoldsAHeaderAndOpensTheFlowOnAVersion(t *testing.T) {
	t.Parallel()

	dr := setOf(t, testDeps(nil), planSet(), 120, 30)
	m := dr.list()
	dr.key("home", "enter")
	if got := drawn(m); slices.Contains(got, "2.4.0") || !slices.Contains(got, "# "+spring) {
		t.Errorf("enter on a header did not fold it: %v", got)
	}
	mustContain(t, dr.view(), ">")
	dr.key("enter")
	if got := drawn(m); !slices.Contains(got, "2.4.0") {
		t.Errorf("a second enter did not open the header: %v", got)
	}
	if m.cursor != 0 {
		t.Errorf("folding moved the cursor off its header to row %d", m.cursor)
	}
}

func TestSet_ClickingAHeaderFoldsIt(t *testing.T) {
	t.Parallel()

	d := testDeps(nil)
	dr := setOf(t, d, planSet(), 120, 30)
	pressOn(t, d, dr, "group:0")
	if got := drawn(dr.list()); slices.Contains(got, "2.4.0") {
		t.Errorf("a click on the header did not fold it: %v", got)
	}
	pressOn(t, d, dr, "group:0")
	if got := drawn(dr.list()); !slices.Contains(got, "2.4.0") {
		t.Errorf("a second click did not open it: %v", got)
	}
}

func twoProjectFake() (*jiratest.Fake, Set) {
	fake := jiratest.New(
		jiratest.WithProject("EX", jiratest.Scrum),
		jiratest.WithProject("OPS", jiratest.Scrum),
	)
	var members []Member
	for _, p := range []string{"EX", "OPS"} {
		versions := jiratest.VersionsFor(p)
		for i := range versions {
			members = append(members, Member{Version: versions[i], Project: owner(p)})
		}
	}
	return fake, Set{Title: "Two projects", Members: members}
}

func TestSet_FlowIsOfferedOnlyThatProjectsVersions(t *testing.T) {
	t.Parallel()

	fake, s := twoProjectFake()
	dr := setOf(t, testDeps(fake), s, 120, 30)
	m := dr.list()
	dr.key("j")
	if v, _ := m.selected(); v.Name != "1.0" {
		t.Fatalf("the cursor is on %q, want the first of EX's versions", v.Name)
	}
	dr.key("j")
	v, _ := m.selected()
	if v.Name != "2.0" || v.ProjectID != jiratest.VersionsFor("EX")[1].ProjectID {
		t.Fatalf("the cursor is on %+v, want EX's 2.0", v)
	}
	dr.key("!")

	push, ok := dr.pushed()
	if !ok || push.ID != FlowViewID {
		t.Fatalf("enter on a version pushed %q, want the release flow", push.ID)
	}
	flow, _ := push.View.(*Flow)
	if flow == nil || len(flow.targets) != 1 || flow.targets[0].ID != jiratest.VersionsFor("EX")[2].ID {
		t.Errorf("the flow was offered %+v, want EX's 3.0 and nothing from OPS", flow.targets)
	}
}

func TestSet_NewIsRefusedWithASentenceAndNotAdvertised(t *testing.T) {
	t.Parallel()

	dr := setOf(t, testDeps(nil), planSet(), 120, 30)
	dr.key("c")
	if dr.list().mode != browsing {
		t.Error("n opened an editor over a set, which has no project to create a version in")
	}
	if got := dr.lastStatus().Text; got != createRefusal {
		t.Errorf("n said %q, want %q", got, createRefusal)
	}
	for state, set := range setSets {
		for _, b := range set.Acts {
			if slices.Contains(b.Keys(), "c") {
				t.Errorf("state %d advertises n in its footer", state)
			}
		}
		for _, column := range set.Full {
			for _, b := range column {
				if slices.Contains(b.Keys(), "c") {
					t.Errorf("state %d advertises n in its help", state)
				}
			}
		}
	}
}

func TestSet_AssignStartsFromTheVersionsProject(t *testing.T) {
	t.Parallel()

	fake, _ := twoProjectFake()
	s := planSet()
	dr := setOf(t, testDeps(fake), s, 120, 30)
	m := dr.list()
	dr.key("v", "v")
	for m.selectedID() != "4001" {
		dr.key("j")
	}
	dr.key("B")

	push, ok := dr.pushed()
	if !ok || push.ID != BulkViewID {
		t.Fatalf("B pushed %q, want the assignment screen", push.ID)
	}
	bulk, _ := push.View.(*Bulk)
	if bulk == nil || bulk.deps.Project != "10400" {
		t.Fatalf("the assignment was scoped to %+v, want the owner's reference 10400", bulk)
	}
	if got := bulk.input.Value(); got != `project = "10400"` {
		t.Errorf("the query starts as %q", got)
	}
}

func TestSet_IgnoresAProjectSwitch(t *testing.T) {
	t.Parallel()

	fake := newFake(4)
	dr := setOf(t, testDeps(fake), planSet(), 120, 30)
	before := drawn(dr.list())
	dr.send(kernel.ProjectMsg{Project: "ELSEWHERE"})

	if got := drawn(dr.list()); !slices.Equal(got, before) {
		t.Errorf("a project switch changed the set: %v", got)
	}
	if dr.list().deps.Project != "PROJ" {
		t.Errorf("the set took the project %q", dr.list().deps.Project)
	}
	if n := countCalls(fake, "Versions"); n != 0 {
		t.Errorf("a set read the versions of a project %d times", n)
	}
}

func TestSet_ReloadKeepsTheCursorOnItsVersion(t *testing.T) {
	t.Parallel()

	s := planSet()
	reloaded := planSet()
	slices.Reverse(reloaded.Members)
	reloaded.Members = append(reloaded.Members, Member{
		Version: jira.Version{ID: "9001", Name: "brand-new", ProjectID: "11"}, Project: owner("EX"),
	})
	s.Reload = func(context.Context) (Set, error) { return reloaded, nil }

	dr := setOf(t, testDeps(nil), s, 120, 30)
	m := dr.list()
	for m.selectedID() != "3001" {
		dr.key("j")
	}
	dr.send(kernel.RefreshMsg{})

	if got := m.selectedID(); got != "3001" {
		t.Errorf("after a reload the cursor is on %q, want it still on web-7 (3001)", got)
	}
	if !slices.Contains(drawn(m), "brand-new") {
		t.Errorf("the reload did not bring the new version in: %v", drawn(m))
	}
	if m.stale {
		t.Error("a reload that worked left the list marked stale")
	}
}

func TestSet_AReloadThatFailsKeepsTheRowsAndSaysWhy(t *testing.T) {
	t.Parallel()

	for name, tc := range map[string]struct {
		err  error
		want string
	}{
		"a token that may not read them": {
			err:  &jira.CapabilityError{Capability: jira.CapPlans, Reason: "you need Browse Projects on EX"},
			want: "you need Browse Projects on EX",
		},
		"a site that is rate limiting": {
			err:  &jira.RateLimitError{Endpoint: "/rest/api/3/plans/plan/7"},
			want: "rate limited by Jira",
		},
		"a transport that failed": {
			err:  &jira.TransportError{Op: "GET /rest/api/3/plans/plan/7", Status: 502},
			want: "failed with HTTP 502",
		},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			s := planSet()
			s.Reload = func(context.Context) (Set, error) { return Set{}, tc.err }
			dr := setOf(t, testDeps(nil), s, 120, 30)
			before := drawn(dr.list())
			dr.send(kernel.RefreshMsg{})

			if got := drawn(dr.list()); !slices.Equal(got, before) {
				t.Errorf("a refused reload changed the rows: %v", got)
			}
			if !dr.list().stale {
				t.Error("a refused reload did not mark the rows stale")
			}
			mustContain(t, dr.view(), staleLabel)
			if got := dr.lastStatus().Text; !strings.Contains(got, tc.want) {
				t.Errorf("the status line says %q, want the site's own words %q", got, tc.want)
			}
		})
	}
}

func TestSet_ARefreshWithNothingToReadAgainSaysSo(t *testing.T) {
	t.Parallel()

	dr := setOf(t, testDeps(nil), planSet(), 120, 30)
	dr.send(kernel.RefreshMsg{})
	if got := dr.lastStatus(); got.Level != kernel.LevelWarn {
		t.Errorf("a refresh with no Reload said %+v, want a warning", got)
	}
}

func TestSet_SortAndArrangementAreKeptApartFromTheProjectList(t *testing.T) {
	t.Cleanup(func() { _ = config.SaveSort(SetViewID, config.SortSpec{}) })

	mem := newFakeMemory()
	d := testDeps(nil)
	d.Memory = mem
	dr := setOf(t, d, planSet(), 120, 30)
	m := dr.list()
	dr.key("v", "f", "l", "enter")
	dr.key("s", "l", "l", "enter")

	if got := loadSort(SetViewID); got.fieldID() != "name" {
		t.Errorf("the set kept the order %+v under its own name, want name", got)
	}
	if got := loadSort(ViewID); got.chosen() {
		t.Errorf("the project list inherited the set's order %+v", got)
	}
	if got, _ := mem.Recall(SetViewID, arrangeMemoryKey); got != "project" {
		t.Errorf("the set kept the arrangement %q, want project", got)
	}
	if _, kept := mem.Recall(ViewID, arrangeMemoryKey); kept {
		t.Error("the project list was given an arrangement")
	}
	if got := recallFilter(d, ViewID); got != filterAll {
		t.Errorf("the project list inherited the set's state filter %q", got.name())
	}
	if got := recallFilter(d, SetViewID); got != filterUnreleased {
		t.Errorf("the set did not keep its state filter: %q", got.name())
	}

	again := setOf(t, d, planSet(), 120, 30).list()
	if again.set.arrange != arrProject || again.filter != filterUnreleased || again.sort.fieldID() != "name" {
		t.Errorf("a set opened again looks at its versions as %v, %q, %q", again.set.arrange, again.filter.name(), again.sort.fieldID())
	}
	_ = m
}

func TestSet_NoGroupsMeansArrangedByProjectAndSkipsCrossSpace(t *testing.T) {
	t.Parallel()

	s := planSet()
	s.Groups = nil
	dr := setOf(t, testDeps(nil), s, 120, 30)
	m := dr.list()
	if m.set.arrange != arrProject {
		t.Fatalf("a set with no cross-space releases opens arranged %q, want by project", arrangementNames[m.set.arrange])
	}
	dr.key("v", "v")
	if m.set.arrange != arrProject {
		t.Errorf("v cycled through cross-space, which has nothing to show: %q", arrangementNames[m.set.arrange])
	}
}

func TestSet_ASortCanOrderByProject(t *testing.T) {
	t.Parallel()

	dr := setOf(t, testDeps(nil), planSet(), 120, 30)
	m := dr.list()
	dr.key("v", "v")
	m.sort = sortChoice{field: "owner", desc: true}
	m.reorder()
	if got := drawn(m); got[0] != "web-7" {
		t.Errorf("by project, descending, the set starts with %q", got[0])
	}
	if f := setSortFields[0]; f.label != "plan order" {
		t.Errorf("the set's own order is called %q", f.label)
	}
}

func TestSet_Golden(t *testing.T) {
	t.Parallel()

	for name, tc := range map[string]struct {
		width, height int
		after         func(*driver)
		golden        string
	}{
		"grouped by cross-space release": {width: 120, height: 30, golden: "set_grouped_120x30.golden"},
		"grouped by project": {
			width: 120, height: 30, golden: "set_by_project_120x30.golden",
			after: func(dr *driver) { dr.key("v") },
		},
		"ungrouped on a narrow terminal": {
			width: 80, height: 20, golden: "set_flat_80x20.golden",
			after: func(dr *driver) { dr.key("v", "v") },
		},
		"the excluded shown": {
			width: 120, height: 30, golden: "set_excluded_shown_120x30.golden",
			after: func(dr *driver) { dr.key(".") },
		},
		"a filter nothing matches": {
			width: 120, height: 20, golden: "set_filtered_empty_120x20.golden",
			after: func(dr *driver) {
				dr.key("/")
				dr.typeText("nothing like this")
				dr.key("enter", "f", "l", "l", "l", "enter")
			},
		},
		"typing a filter": {
			width: 120, height: 20, golden: "set_finding_120x20.golden",
			after: func(dr *driver) {
				dr.key("/")
				dr.typeText("ops")
			},
		},
		"a header folded": {
			width: 120, height: 20, golden: "set_folded_120x20.golden",
			after: func(dr *driver) { dr.key("home", "enter") },
		},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			dr := setOf(t, testDeps(nil), planSet(), tc.width, tc.height)
			if tc.after != nil {
				tc.after(dr)
			}
			golden(t, tc.golden, dr.view())
		})
	}
}

func TestSet_ARefusedReloadWithNothingOnScreenSaysWhy(t *testing.T) {
	t.Parallel()

	s := Set{Title: "Empty"}
	dr := setOf(t, testDeps(nil), s, 100, 14)
	mustContain(t, dr.view(), "This set holds no versions.")

	m := dr.list()
	m.failure, m.what = errors.New("the site is not answering"), whatSet
	m.sum = ""
	mustContain(t, dr.view(), whatSet, "the site is not answering")
}

func TestSet_KeysAreHeldAgainstTheStateTheyBelongTo(t *testing.T) {
	t.Parallel()

	dr := setOf(t, testDeps(nil), planSet(), 120, 30)
	m := dr.list()
	seen := map[int]bool{}
	for name, enter := range map[string]func(){
		"reading":  func() {},
		"finding":  func() { m.mode = finding },
		"faceting": func() { m.mode = faceting },
		"sorting":  func() { m.mode = sorting },
		"editing":  func() { m.mode = editing },
		"counting": func() { m.mode, m.counting = browsing, "1001" },
		"saving":   func() { m.mode, m.counting, m.saving = browsing, "", true },
	} {
		m.mode, m.counting, m.saving = browsing, "", false
		enter()
		_, gen := m.LiveKeys()
		if seen[gen] {
			t.Errorf("%s shares key generation %d with another state", name, gen)
		}
		seen[gen] = true
	}
	if len(seen) != int(setKeyStates) {
		t.Errorf("the test reached %d of the set's %d key states", len(seen), setKeyStates)
	}
}

func TestSetKeys_EveryStateGolden(t *testing.T) {
	t.Parallel()
	named := []struct {
		name  string
		state setKeyState
	}{
		{"reading the set", setBrowsing},
		{"counting what is open on one", setCounting},
		{"typing a version", setEditing},
		{"a save in flight", setSaving},
		{"choosing an order", setSorting},
		{"typing the text filter", setFinding},
		{"choosing the filters", setFaceting},
	}
	if len(named) != int(setKeyStates) {
		t.Fatalf("a set has %d key states and this test names %d", setKeyStates, len(named))
	}
	var b strings.Builder
	for _, s := range named {
		fmt.Fprintf(&b, "%s\n", s.name)
		writeKeySet(&b, setSets[s.state])
	}
	golden(t, "set_keys.golden", b.String())
}

func TestSet_FOpensStateAndProjectTogetherAndEscKeepsThem(t *testing.T) {
	t.Parallel()

	dr := setOf(t, testDeps(nil), planSet(), 120, 30)
	m := dr.list()
	dr.key("f")
	if m.mode != faceting || !m.WantsRawKeys() {
		t.Fatalf("f left the list in mode %v, want the filters open and claiming keys", m.mode)
	}
	mustContain(t, dr.view(), "filter by:", "[state all]", "project all")

	dr.key("l")
	if m.filter != filterUnreleased {
		t.Errorf("l moved the state to %q, want unreleased", m.filter.name())
	}
	dr.key("tab", "h")
	if m.set.pick != "10400" {
		t.Errorf("h on project moved it to %q, want the last project", m.set.pick)
	}
	mustContain(t, dr.view(), "[project")

	dr.key("esc")
	if m.mode != browsing || m.filter != filterUnreleased || m.set.pick != "10400" {
		t.Errorf("esc left mode %v, state %q, project %q, want both kept", m.mode, m.filter.name(), m.set.pick)
	}
}
