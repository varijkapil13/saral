package search

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"testing"
	"time"

	appmatch "github.com/varijkapil13/saral/internal/app/match"
	appterm "github.com/varijkapil13/saral/internal/app/term"
	"github.com/varijkapil13/saral/pkg/jira"
	"github.com/varijkapil13/saral/pkg/jira/jiratest"
)

func valuesUser(id, name, email string, kind jira.AccountKind, active bool) jira.User {
	return jira.User{AccountID: id, DisplayName: name, Email: email, Kind: kind, Active: active, TimeZone: time.UTC}
}

func valuesLabels(n int) []string {
	out := make([]string, 0, n)
	for i := range n {
		out = append(out, fmt.Sprintf("team-%d-service-%d", i%37, i))
	}
	return out
}

func valuesFailures() map[string]error {
	return map[string]error{
		"403":       &jira.CapabilityError{Capability: jira.CapPeople, Reason: "needs Browse users and groups"},
		"429":       &jira.RateLimitError{RetryAfter: 30 * time.Second},
		"transport": &jira.TransportError{Op: "reading", Err: errors.New("connection refused")},
	}
}

func TestRankValues_ANameBeatsAnEmailAndTiesKeepTheVocabularyOrder(t *testing.T) {
	all := []Value{
		{Term: appterm.Term{ID: "1", Label: "Zed"}, Note: "ada@example.com"},
		{Term: appterm.Term{ID: "2", Label: "Ada"}, Note: ""},
		{Term: appterm.Term{ID: "3", Label: "Bob"}},
		{Term: appterm.Term{ID: "4", Label: "Bob"}},
	}
	shown, _ := RankValues(all, appmatch.NewPattern("ada"), nil, nil)
	if !slices.Equal(shown, []int{1, 0}) {
		t.Errorf("ada ranked %v, want the name (1) before the email (0)", shown)
	}
	shown, _ = RankValues(all, appmatch.NewPattern("bob"), nil, nil)
	if !slices.Equal(shown, []int{2, 3}) {
		t.Errorf("a tie ranked %v, want the vocabulary's order", shown)
	}
	shown, _ = RankValues(all, appmatch.NewPattern(""), nil, nil)
	if len(shown) != len(all) {
		t.Errorf("an empty pattern showed %d of %d", len(shown), len(all))
	}
}

func TestRankValues_ReusesItsBuffers(t *testing.T) {
	all := LabelValues(valuesLabels(500))
	pattern := appmatch.NewPattern("serv")
	shown, ranks := make([]int, 0, len(all)), make([]ValueRank, 0, len(all))
	shown, ranks = RankValues(all, pattern, shown, ranks)
	if got := testing.AllocsPerRun(20, func() {
		shown, ranks = RankValues(all, pattern, shown[:0], ranks[:0])
	}); got != 0 {
		t.Errorf("ranking allocates %.1f times, want none", got)
	}
}

func TestPersonValue_BadgesWhatIsNotAnActivePersonAndSinksIt(t *testing.T) {
	for _, tc := range []struct {
		name string
		user jira.User
		note string
		sink int
	}{
		{"person", valuesUser("a", "Ada", "ada@example.com", jira.AccountPerson, true), "ada@example.com", sinkPerson},
		{"inactive", valuesUser("a", "Ada", "ada@example.com", jira.AccountPerson, false), "inactive", sinkPerson + sinkInactive},
		{"app", valuesUser("b", "Bot", "", jira.AccountApp, true), jira.AccountApp.String(), sinkApp},
		{"inactive customer", valuesUser("c", "Cy", "", jira.AccountCustomer, false), jira.AccountCustomer.String() + ", inactive", sinkCustomer + sinkInactive},
	} {
		t.Run(tc.name, func(t *testing.T) {
			v := PersonValue(appterm.FacetAssignee, tc.user)
			if v.Note != tc.note || v.Sink != tc.sink {
				t.Errorf("got note %q sink %d, want %q %d", v.Note, v.Sink, tc.note, tc.sink)
			}
			if v.Term.ID != tc.user.AccountID || v.Term.Label != tc.user.DisplayName || v.Term.Facet != appterm.FacetAssignee {
				t.Errorf("term %+v does not carry the account", v.Term)
			}
		})
	}
}

func TestSortPeople_PeopleFirstThenOtherKindsAndInactiveBelowTheirOwn(t *testing.T) {
	all := []Value{
		PersonValue(appterm.FacetAssignee, valuesUser("1", "Bot", "", jira.AccountApp, true)),
		PersonValue(appterm.FacetAssignee, valuesUser("2", "Zoe", "", jira.AccountPerson, false)),
		PersonValue(appterm.FacetAssignee, valuesUser("3", "Yan", "", jira.AccountPerson, true)),
		PersonValue(appterm.FacetAssignee, valuesUser("4", "Ann", "", jira.AccountPerson, true)),
		UnassignedValue(),
	}
	SortPeople(all)
	got := make([]string, 0, len(all))
	for _, v := range all {
		got = append(got, v.Term.Label)
	}
	if want := []string{"unassigned", "Ann", "Yan", "Zoe", "Bot"}; !slices.Equal(got, want) {
		t.Errorf("order %v, want %v", got, want)
	}
}

func TestUnassignedValue_IsTheAssigneeFacetsEmptyID(t *testing.T) {
	v := UnassignedValue()
	if v.Term.Facet != appterm.FacetAssignee || v.Term.ID != "" || v.Term.Label != "unassigned" || v.Note != "nobody is on it" {
		t.Errorf("got %+v", v)
	}
}

func TestStatusValues_NamesTheIssueTypesThatTellTwoIdsOfOneNameApart(t *testing.T) {
	in := []jira.IssueTypeStatuses{
		{Type: jira.IssueType{ID: "1", Name: "Bug"}, Statuses: []jira.Status{{ID: "10", Name: "Done"}, {ID: "11", Name: "Open"}}},
		{Type: jira.IssueType{ID: "2", Name: "Task"}, Statuses: []jira.Status{{ID: "10", Name: "Done"}, {ID: "12", Name: "Done"}}},
	}
	got := StatusValues(in)
	if len(got) != 3 {
		t.Fatalf("got %d statuses, want 3 keyed by id", len(got))
	}
	if got[0].Note != "Bug, Task" || got[1].Note != "Bug" || got[2].Note != "Task" {
		t.Errorf("notes %q %q %q", got[0].Note, got[1].Note, got[2].Note)
	}
	if got[0].Term.Facet != appterm.FacetStatus {
		t.Errorf("facet %v", got[0].Term.Facet)
	}
}

func TestTypeValues_KeepsTheSitesOrderDropsRepeatsAndMarksSubtasks(t *testing.T) {
	in := []jira.IssueTypeStatuses{
		{Type: jira.IssueType{ID: "2", Name: "Task"}},
		{Type: jira.IssueType{ID: "1", Name: "Sub", Subtask: true}},
		{Type: jira.IssueType{ID: "2", Name: "Task"}},
		{Type: jira.IssueType{Name: "No id"}},
	}
	got := TypeValues(in)
	if len(got) != 2 || got[0].Term.ID != "2" || got[1].Term.ID != "1" || got[1].Note != "subtask" || got[0].Note != "" {
		t.Errorf("got %+v", got)
	}
}

func TestPriorityAndLabelValues_KeepTheirOrderAndSkipBlankLabels(t *testing.T) {
	p := PriorityValues([]jira.Priority{{ID: "2", Name: "Low"}, {ID: "1", Name: "High"}})
	if len(p) != 2 || p[0].Term.Label != "Low" || p[1].Term.ID != "1" {
		t.Errorf("priorities %+v", p)
	}
	l := LabelValues([]string{"b", "", "a"})
	if len(l) != 2 || l[0].Term.ID != "b" || l[0].Term.Label != "b" || l[1].Term.ID != "a" {
		t.Errorf("labels %+v", l)
	}
}

func TestAbsent_NamesOnlyTheIDsTheSearchDidNotAnswerWith(t *testing.T) {
	got := absent([]string{"a", "", "b", "c"}, []jira.User{{AccountID: "b"}})
	if !slices.Equal(got, []string{"a", "c"}) {
		t.Errorf("got %v", got)
	}
	if absent(nil, []jira.User{{AccountID: "b"}}) != nil {
		t.Error("nothing wanted should ask for nothing")
	}
}

func TestOfferFacet_GatesOnConnectionCapabilityAndProject(t *testing.T) {
	ok := jira.Capabilities{People: jira.Capability{OK: true}}
	denied := jira.Capabilities{People: jira.Capability{Reason: "no browse"}}
	for _, tc := range []struct {
		name      string
		f         appterm.Facet
		connected bool
		caps      jira.Capabilities
		project   string
		kind      ValueRefusal
		why       string
	}{
		{"no client", appterm.FacetLabel, false, ok, "P", ValueNoConnection, ""},
		{"label offered", appterm.FacetLabel, true, jira.Capabilities{}, "", ValueOffered, ""},
		{"people denied", appterm.FacetAssignee, true, denied, "P", ValueNoPeople, "no browse"},
		{"people denied silently", appterm.FacetReporter, true, jira.Capabilities{}, "P", ValueNoPeople, ""},
		{"people allowed", appterm.FacetAssignee, true, ok, "", ValueOffered, ""},
		{"status needs a project", appterm.FacetStatus, true, ok, "  ", ValueNoProject, ""},
		{"type needs a project", appterm.FacetType, true, ok, "", ValueNoProject, ""},
		{"status with a project", appterm.FacetStatus, true, ok, "P", ValueOffered, ""},
		{"priority needs none", appterm.FacetPriority, true, ok, "", ValueOffered, ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			kind, why := OfferFacet(tc.f, tc.connected, tc.caps, tc.project)
			if kind != tc.kind || why != tc.why {
				t.Errorf("got %v %q, want %v %q", kind, why, tc.kind, tc.why)
			}
		})
	}
}

func TestAskSiteFor_OnlyAccountsOnlyWithANeedleOnlyWhenHeldRunsThin(t *testing.T) {
	for _, tc := range []struct {
		name     string
		f        appterm.Facet
		needle   string
		complete bool
		asked    bool
		shown    int
		want     bool
	}{
		{"thin", appterm.FacetAssignee, "ad", false, false, ThinAnswer - 1, true},
		{"enough held", appterm.FacetAssignee, "ad", false, false, ThinAnswer, false},
		{"not a person", appterm.FacetLabel, "ad", false, false, 0, false},
		{"empty needle", appterm.FacetAssignee, "", false, false, 0, false},
		{"complete", appterm.FacetReporter, "ad", true, false, 0, false},
		{"asked already", appterm.FacetAssignee, "ad", false, true, 0, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := AskSiteFor(tc.f, tc.needle, tc.complete, tc.asked, tc.shown); got != tc.want {
				t.Errorf("got %v, want %v", got, tc.want)
			}
		})
	}
	if !LookedUpByTyping(appterm.FacetReporter) || LookedUpByTyping(appterm.FacetStatus) {
		t.Error("only accounts are looked up by typing")
	}
}

func TestPeopleProject_OnlyTheAssigneeSearchIsAssignable(t *testing.T) {
	if got := PeopleProject(appterm.FacetAssignee, " PROJ "); got != "PROJ" {
		t.Errorf("assignee got %q", got)
	}
	if got := PeopleProject(appterm.FacetReporter, "PROJ"); got != "" {
		t.Errorf("reporter got %q", got)
	}
}

func TestFindAccounts_ReturnsWhatTheSiteFoundAndSaysWhenThatIsEverybody(t *testing.T) {
	f := jiratest.New(jiratest.WithProject("PROJ", jiratest.Scrum), jiratest.WithPeople([]jira.User{
		valuesUser("a", "Ada", "", jira.AccountPerson, true),
		valuesUser("b", "Bob", "", jira.AccountPerson, true),
	}))
	got, err := FindAccounts(context.Background(), f, jira.PeopleQuery{Limit: PeopleLimit}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(got.Users) != 2 || !got.Complete {
		t.Errorf("got %d users complete=%v", len(got.Users), got.Complete)
	}
	got, err = FindAccounts(context.Background(), f, jira.PeopleQuery{Limit: 2}, nil)
	if err != nil || got.Complete {
		t.Errorf("a full answer is not the whole directory: complete=%v err=%v", got.Complete, err)
	}
}

func TestFindAccounts_BackFillsAnAccountInForceTheSearchDidNotReturn(t *testing.T) {
	f := jiratest.New(jiratest.WithProject("PROJ", jiratest.Scrum), jiratest.WithPeople([]jira.User{
		valuesUser("a", "Ada", "", jira.AccountPerson, true),
		valuesUser("bot", "Bot", "", jira.AccountApp, true),
	}))
	q := jira.PeopleQuery{Project: "PROJ", Limit: PeopleLimit}
	got, err := FindAccounts(context.Background(), f, q, []string{"a", "bot", "ghost", ""})
	if err != nil {
		t.Fatal(err)
	}
	ids := make([]string, 0, len(got.Users))
	for _, u := range got.Users {
		ids = append(ids, u.AccountID)
	}
	if !slices.Equal(ids, []string{"a", "bot"}) {
		t.Errorf("got %v, want the search's account then the back-filled app account, and nothing for an unknown id", ids)
	}
	if !got.Complete {
		t.Error("completeness is about the search, not the back-fill")
	}
	if calls := f.Calls(); !slices.Equal(calls, []string{"FindPeople", "People"}) {
		t.Errorf("calls %v", calls)
	}
}

func TestFindAccounts_AsksNothingMoreWhenEveryAccountInForceCameBack(t *testing.T) {
	f := jiratest.New(jiratest.WithPeople([]jira.User{valuesUser("a", "Ada", "", jira.AccountPerson, true)}))
	if _, err := FindAccounts(context.Background(), f, jira.PeopleQuery{Limit: 10}, []string{"a"}); err != nil {
		t.Fatal(err)
	}
	if calls := f.Calls(); !slices.Equal(calls, []string{"FindPeople"}) {
		t.Errorf("calls %v", calls)
	}
}

func TestFindAccounts_PassesEachReadsFailureThroughUntouched(t *testing.T) {
	people := []jira.User{valuesUser("bot", "Bot", "", jira.AccountApp, true)}
	q := jira.PeopleQuery{Project: "PROJ", Limit: 10}
	for name, fail := range valuesFailures() {
		t.Run(name+"/search", func(t *testing.T) {
			f := jiratest.New(jiratest.WithProject("PROJ", jiratest.Scrum), jiratest.WithPeople(people))
			f.FailNext(fail)
			if _, err := FindAccounts(context.Background(), f, q, []string{"bot"}); !errors.Is(err, fail) {
				t.Errorf("got %v, want %v", err, fail)
			}
		})
		t.Run(name+"/back-fill", func(t *testing.T) {
			f := jiratest.New(jiratest.WithProject("PROJ", jiratest.Scrum), jiratest.WithPeople(people))
			finder := valuesBackFillFails{PeopleFinder: f, err: fail}
			if _, err := FindAccounts(context.Background(), finder, q, []string{"bot"}); !errors.Is(err, fail) {
				t.Errorf("got %v, want %v", err, fail)
			}
		})
	}
}

type valuesBackFillFails struct {
	jira.PeopleFinder
	err error
}

func (s valuesBackFillFails) People(context.Context, []string) ([]jira.User, error) {
	return nil, s.err
}

func TestVocabulary_ReadsEachKindOfFacet(t *testing.T) {
	f := jiratest.New(jiratest.WithProject("PROJ", jiratest.Scrum))
	ctx := context.Background()

	statuses, err := Vocabulary(ctx, f, appterm.FacetStatus, "PROJ")
	if err != nil || len(statuses) == 0 || statuses[0].Term.Facet != appterm.FacetStatus {
		t.Errorf("statuses %v %v", statuses, err)
	}
	types, err := Vocabulary(ctx, f, appterm.FacetType, "PROJ")
	if err != nil || len(types) == 0 || types[0].Term.Facet != appterm.FacetType {
		t.Errorf("types %v %v", types, err)
	}
	priorities, err := Vocabulary(ctx, f, appterm.FacetPriority, "")
	if err != nil || len(priorities) != 3 || priorities[0].Term.Label != "Urgent" {
		t.Errorf("priorities %v %v", priorities, err)
	}
	labels, err := Vocabulary(ctx, f, appterm.FacetLabel, "")
	if err != nil || len(labels) == 0 || labels[0].Term.Facet != appterm.FacetLabel {
		t.Errorf("labels %v %v", labels, err)
	}
	none, err := Vocabulary(ctx, f, appterm.FacetAssignee, "")
	if err != nil || none != nil {
		t.Errorf("a person facet has no vocabulary: %v %v", none, err)
	}
}

type valuesLabelSite struct {
	jira.FilterVocabulary
	n, page int
}

func (s valuesLabelSite) Labels(ctx context.Context) (jira.Page[string], error) {
	all := valuesLabels(s.n)
	return jira.Offset(ctx, func(_ context.Context, startAt int) ([]string, int, bool, error) {
		end := min(startAt+s.page, len(all))
		return all[startAt:end], len(all), end >= len(all), nil
	})
}

func TestVocabulary_WalksLabelsUpToTheBoundAndNoFurther(t *testing.T) {
	for _, tc := range []struct{ n, page, want int }{
		{n: 130, page: 50, want: 130},
		{n: MaxLabels + 500, page: 300, want: MaxLabels},
		{n: MaxLabels + 500, page: MaxLabels + 500, want: MaxLabels},
	} {
		got, err := Vocabulary(context.Background(), valuesLabelSite{n: tc.n, page: tc.page}, appterm.FacetLabel, "")
		if err != nil {
			t.Fatal(err)
		}
		if len(got) != tc.want {
			t.Errorf("%d labels in pages of %d gave %d, want %d", tc.n, tc.page, len(got), tc.want)
		}
	}
}

func TestVocabulary_PassesAFailureThroughOnEveryRead(t *testing.T) {
	for name, fail := range valuesFailures() {
		for _, facet := range []appterm.Facet{appterm.FacetStatus, appterm.FacetType, appterm.FacetPriority, appterm.FacetLabel} {
			t.Run(name+"/"+facet.Label(), func(t *testing.T) {
				f := jiratest.New(jiratest.WithProject("PROJ", jiratest.Scrum))
				f.FailNext(fail)
				if _, err := Vocabulary(context.Background(), f, facet, "PROJ"); !errors.Is(err, fail) {
					t.Errorf("got %v, want %v", err, fail)
				}
			})
		}
	}
}

func TestVocabulary_ALaterLabelPageFailingFailsTheWalk(t *testing.T) {
	for name, fail := range valuesFailures() {
		t.Run(name, func(t *testing.T) {
			f := jiratest.New(jiratest.WithProject("PROJ", jiratest.Scrum), jiratest.WithPageSize(3))
			site := valuesLabelsThenFail{Fake: f, err: fail}
			if _, err := Vocabulary(context.Background(), site, appterm.FacetLabel, ""); !errors.Is(err, fail) {
				t.Errorf("got %v, want %v", err, fail)
			}
		})
	}
}

type valuesLabelsThenFail struct {
	*jiratest.Fake
	err error
}

func (s valuesLabelsThenFail) Labels(ctx context.Context) (jira.Page[string], error) {
	page, err := s.Fake.Labels(ctx)
	if err == nil && page.HasMore() {
		s.FailNext(s.err)
	}
	return page, err
}
