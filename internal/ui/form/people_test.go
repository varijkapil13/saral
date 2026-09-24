package form

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/varijkapil13/saral/internal/ui/kernel"
	"github.com/varijkapil13/saral/pkg/jira"
	"github.com/varijkapil13/saral/pkg/jira/jiratest"
)

// asking records every question the form puts to FindPeople.
type asking struct {
	*jiratest.Fake
	mu    sync.Mutex
	asked []jira.PeopleQuery
}

func (a *asking) FindPeople(ctx context.Context, q jira.PeopleQuery) ([]jira.User, error) {
	a.mu.Lock()
	a.asked = append(a.asked, q)
	a.mu.Unlock()
	return a.Fake.FindPeople(ctx, q)
}

func (a *asking) queries() []jira.PeopleQuery {
	a.mu.Lock()
	defer a.mu.Unlock()
	return append([]jira.PeopleQuery(nil), a.asked...)
}

func choiceLabels(m *Model) []string {
	out := make([]string, 0, len(m.choices))
	for _, at := range m.visibleChoices() {
		out = append(out, m.choices[at].label)
	}
	return out
}

func openAssignee(t *testing.T, d kernel.Deps, w, h int) *driver {
	t.Helper()

	dr := openOn(t, d, w, h, fakeStory)
	dr.focus("assignee")
	dr.key("enter")
	if !dr.m.choosingPeople() {
		t.Fatal("the assignee did not open a person picker")
	}
	return dr
}

func TestPeople_TypingANameAsksTheSite(t *testing.T) {
	t.Parallel()

	c := &asking{Fake: newFake(20)}
	dr := openAssignee(t, testDeps(t, c), 100, 24)
	dr.typeText("gr")

	asked := c.queries()
	if len(asked) == 0 {
		t.Fatal("typing a name asked the site nothing")
	}
	last := asked[len(asked)-1]
	if last.Match != "gr" || last.Project != "PROJ" || last.Limit <= 0 {
		t.Errorf("the last search was %+v, want the typed name scoped to the session's project with a ceiling", last)
	}
	if got := choiceLabels(dr.m); !containsLabel(got, "Grace Hopper") {
		t.Errorf("the picker offers %v, want the account the site answered with", got)
	}
}

func TestPeople_OffersWhatTheSiteMatchedEvenWhereTheLocalFilterWouldNot(t *testing.T) {
	t.Parallel()

	dr := openAssignee(t, testDeps(t, newFake(20)), 100, 24)
	dr.typeText("gh")

	if got := choiceLabels(dr.m); !containsLabel(got, "Grace Hopper") {
		t.Errorf("the picker offers %v; the site matched Grace Hopper on her initials", got)
	}
}

func TestPeople_ChoosingOneSetsTheAccountID(t *testing.T) {
	t.Parallel()

	dr := openAssignee(t, testDeps(t, newFake(20)), 100, 24)
	dr.typeText("grace")
	dr.key("enter")

	assignee := dr.field("assignee")
	if len(assignee.picked) != 1 || assignee.picked[0].ID != "acct-grace" {
		t.Fatalf("the assignee is %+v, want Grace by account id", assignee.picked)
	}
	if in := dr.m.issueInput(); in.Assignee != "acct-grace" {
		t.Errorf("the create would assign %q, want the account id", in.Assignee)
	}
}

func TestPeople_KeepsTheAuthenticatedAccountOnOffer(t *testing.T) {
	t.Parallel()

	c := newFake(20)
	me, err := c.Me(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	dr := openAssignee(t, testDeps(t, c), 100, 24)
	if len(dr.m.choices) == 0 || dr.m.choices[0].value.ID != me.AccountID {
		t.Fatalf("the picker opens on %v, want the authenticated account first", choiceLabels(dr.m))
	}
	if !strings.HasSuffix(dr.m.choices[0].label, "(me)") {
		t.Errorf("the account is offered as %q, which does not say it is this session's own", dr.m.choices[0].label)
	}
	seen := 0
	for _, choice := range dr.m.choices {
		if choice.value.ID == me.AccountID {
			seen++
		}
	}
	if seen != 1 {
		t.Errorf("the authenticated account is offered %d times", seen)
	}
}

func TestPeople_AFailedSearchIsSaidAndTheTypingKept(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name  string
		err   error
		wants string
	}{
		{"a token that may not browse users", &jira.CapabilityError{Capability: jira.CapPeople, Reason: "needs the Browse users and groups permission"}, "Browse users"},
		{"a site that is rate limiting", &jira.RateLimitError{RetryAfter: 30 * time.Second}, "retry in 30s"},
		{"a site that could not be reached", &jira.TransportError{Op: "GET user/assignable/search", Err: errors.New("connection refused")}, "connection refused"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			c := newFake(20)
			dr := openAssignee(t, testDeps(t, c), 100, 24)
			c.FailNext(tt.err)
			dr.typeText("g")

			if got := dr.m.filter.Value(); got != "g" {
				t.Errorf("the filter reads %q after the search failed, want what was typed", got)
			}
			if got := dr.lastStatus(); got.Level != kernel.LevelError || !strings.Contains(got.Text, tt.wants) {
				t.Errorf("the status line says %+v, want %q", got, tt.wants)
			}
			mustContain(t, dr.view(), tt.wants)
			if dr.m.edit != editChoose {
				t.Error("a failed search closed the picker")
			}

			dr.typeText("r")
			if dr.m.people.fail != "" {
				t.Errorf("a search that answered left the failure up: %q", dr.m.people.fail)
			}
		})
	}
}

func TestPeople_IgnoresAnAnswerToANameTypedOver(t *testing.T) {
	t.Parallel()

	dr := openAssignee(t, testDeps(t, newFake(20)), 100, 24)
	_, stale := dr.m.Update(keyPress("a"))
	staleGen := dr.m.people.gen
	dr.typeText("d")
	if dr.m.people.gen == staleGen {
		t.Fatal("typing more did not start a new search")
	}

	dr.send(peopleFoundMsg{gen: staleGen, people: []jira.User{{AccountID: "acct-stale", DisplayName: "Stale Answer"}}})
	if containsLabel(choiceLabels(dr.m), "Stale Answer") {
		t.Error("an answer to a name typed over was drawn")
	}
	dr.send(peopleFailedMsg{gen: staleGen, err: errors.New("too late")})
	if dr.m.people.fail != "" {
		t.Errorf("a failure for a name typed over was drawn: %q", dr.m.people.fail)
	}

	if msg := answer(stale); msg != nil {
		dr.send(msg)
	}
	if !containsLabel(choiceLabels(dr.m), "Ada Lovelace") {
		t.Errorf("the picker offers %v, want the answer to what is typed now", choiceLabels(dr.m))
	}
}

func TestPeople_StopsAskingWhenThePickerCloses(t *testing.T) {
	t.Parallel()

	dr := openAssignee(t, testDeps(t, newFake(20)), 100, 24)
	view, cmd := dr.m.Update(keyPress("g"))
	dr.m = view.(*Model)
	dr.key("esc")

	failed, ok := answer(cmd).(peopleFailedMsg)
	if !ok || !errors.Is(failed.err, context.Canceled) {
		t.Errorf("the search in flight answered %+v after the picker closed, want it cancelled", answer(cmd))
	}
}

func TestPeople_SaysWhyItCannotAskAndStillOffersTheAccount(t *testing.T) {
	t.Parallel()

	c := &asking{Fake: newFake(20)}
	d := testDeps(t, c)
	d.Caps.People = jira.Capability{Reason: "needs the Browse users and groups permission"}
	dr := openAssignee(t, d, 100, 24)
	dr.typeText("gr")

	if n := len(c.queries()); n != 0 {
		t.Errorf("the site was asked %d times by a token that may not look anyone up", n)
	}
	mustContain(t, dr.view(), "Browse users and groups")
	if len(dr.m.choices) == 0 {
		t.Error("the authenticated account is no longer offered")
	}
}

func TestPeople_CleansADisplayNameBeforeOfferingIt(t *testing.T) {
	t.Parallel()

	c := newFake(20, jiratest.WithPeople([]jira.User{
		{AccountID: "acct-odd", DisplayName: "Odd\x1b[31m Name\x07", Active: true, Kind: jira.AccountPerson},
	}))
	dr := openAssignee(t, testDeps(t, c), 100, 24)
	dr.typeText("odd")

	for _, choice := range dr.m.choices {
		if strings.ContainsAny(choice.label+choice.value.Label, "\x1b\x07") {
			t.Errorf("a display name reached the picker with its control bytes: %q", choice.label)
		}
	}
}

func TestPeople_KeepsWhatIsPickedAcrossSearches(t *testing.T) {
	t.Parallel()

	m := newWith(testDeps(t, nil), newSchemaCache(schemaTTL, time.Now))
	f := newField(meta("customfield_8", "Reviewers", jira.FieldSchema{Type: "array", Items: "user", Custom: "x:people"}), time.UTC)
	on := []jira.Option{{ID: "acct-ada", Label: "Ada Lovelace"}}

	got := m.userChoices(f, on, []jira.User{{AccountID: "acct-grace", DisplayName: "Grace Hopper"}})
	if len(got) != 2 || got[0].value.ID != "acct-ada" || !got[0].on || got[1].on || !got[1].found {
		t.Errorf("the choices are %+v, want Ada still picked and Grace found", got)
	}
}

func TestRender_DrawsThePersonPickerAtEveryWidth(t *testing.T) {
	t.Parallel()

	sizes := []struct {
		name string
		w, h int
	}{
		{"people_100x24.golden", 100, 24},
		{"people_140x30.golden", 140, 30},
		{"people_48x14.golden", 48, 14},
	}
	for _, size := range sizes {
		t.Run(size.name, func(t *testing.T) {
			t.Parallel()

			dr := openAssignee(t, testDeps(t, newFake(20)), size.w, size.h)
			dr.typeText("a")
			golden(t, size.name, dr.view())
		})
	}
}

func TestRender_DrawsAFailedPersonSearch(t *testing.T) {
	t.Parallel()

	c := newFake(20)
	dr := openAssignee(t, testDeps(t, c), 100, 24)
	c.FailNext(&jira.RateLimitError{RetryAfter: 30 * time.Second})
	dr.typeText("g")
	golden(t, "people_failed_100x24.golden", dr.view())
}

func TestRender_DrawsTheLeavePrompt(t *testing.T) {
	t.Parallel()

	dr := openOn(t, testDeps(t, newFake(20)), 100, 24, fakeStory)
	dr.fill("summary", "Not yet created")
	dr.run(dr.m.AskClose())
	golden(t, "leave_100x24.golden", dr.view())
}

func containsLabel(labels []string, want string) bool {
	for _, l := range labels {
		if strings.Contains(l, want) {
			return true
		}
	}
	return false
}
