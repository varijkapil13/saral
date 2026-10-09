package plan

import (
	"errors"
	"strings"
	"testing"

	appplan "github.com/varijkapil13/saral/internal/app/plan"
	"github.com/varijkapil13/saral/pkg/jira"
)

const browseRefusal = "You must have browse project rights in order to view versions."

func projectSources(refs ...string) []jira.PlanSource {
	out := make([]jira.PlanSource, 0, len(refs))
	for _, ref := range refs {
		out = append(out, jira.PlanSource{Type: jira.PlanSourceProject, Value: ref})
	}
	return out
}

func TestRefusedReason_IsTheSitesWordsWithoutTheRef(t *testing.T) {
	t.Parallel()

	for name, tc := range map[string]struct {
		err  error
		want string
	}{
		"a project the token cannot browse": {&jira.ValidationError{Messages: []string{browseRefusal}}, browseRefusal},
		"a board the site explains":         {&jira.NotFoundError{Kind: "board", ID: "17", Detail: "The requested board cannot be viewed."}, "The requested board cannot be viewed."},
		"a missing project":                 {&jira.NotFoundError{Kind: "project", ID: "10011"}, "it does not exist, or you cannot see it"},
		"a refused licence":                 {&jira.CapabilityError{Capability: jira.CapBoards, Reason: "Jira Software is not licensed on this site."}, "Jira Software is not licensed on this site."},
		"a board that is not an id":         {appplan.ErrNotBoardID, "the site did not name it by a board id"},
		"a board with no project":           {appplan.ErrBoardEmpty, "the site named no project behind it"},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			got := refusedReason(tc.err)
			if !strings.Contains(got, tc.want) || strings.Contains(got, "10011") {
				t.Errorf("refusedReason = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestPlansRefused_FallsBackToItsOwnWordsOnlyForThePlansRefusal(t *testing.T) {
	t.Parallel()

	if reason, ok := plansRefused(&jira.CapabilityError{Capability: jira.CapPlans}); !ok || !strings.Contains(reason, "Administer Jira") {
		t.Errorf("plansRefused = %q, %v", reason, ok)
	}
	if _, ok := plansRefused(errors.New("boom")); ok {
		t.Error("a plain error was taken as the plans refusal")
	}
}

func TestPlans_ARefusedProjectIsNamedBesideTheReleasesThatWereRead(t *testing.T) {
	t.Parallel()

	f := newFake(5)
	dr := newDriver(t, testDeps(f), 160, 30)
	dr.send(plansMsg{gen: dr.m.gen, plans: []jira.Plan{{
		ID: "42", Name: "Delivery", Status: "Active",
		Sources: projectSources("10021", "10011"),
	}}})
	dr.key("enter")
	dr.send(releasesMsg{gen: dr.m.gen, plan: "42", got: appplan.Releases{
		Versions: []jira.Version{{ID: "1", Name: "Spring drop"}},
		Names:    map[string]string{"10021": "EX", "10011": "OPS"},
		Refused:  []appplan.Refusal{{Kind: appplan.RefusedProject, Ref: "10011", Err: &jira.ValidationError{Messages: []string{browseRefusal}}}},
		Read:     []string{"10021"},
	}})

	frame := dr.view()
	mustContain(t, frame, "1 in EX - enter browses", "project OPS left out", "browse project rights")
}
