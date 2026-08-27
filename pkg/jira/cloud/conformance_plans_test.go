package cloud

import (
	"errors"
	"net/http"
	"strconv"
	"strings"
	"testing"

	"github.com/varijkapil13/saral/pkg/jira"
	"github.com/varijkapil13/saral/pkg/jira/jiratest"
)

// One set of assertions about plans, run against both adapters through the port
// role that names the method. The two sites disagree about everything else: the
// fixture site holds a plan drawing on boards and a filter and one somebody
// threw away, and the fake holds one Active project-sourced plan per project. So
// the cases are properties — a plan is identified, a plan read from Jira is
// never Local, a source says what kind it is in the port's own words, and a
// refusal names CapPlans with a reason to show.
//
// One case fails against the fake on purpose. jiratest.Fake.Plans puts a project
// KEY in PlanSource.Value where the schema and plans_ok.json both put the numeric
// project ID, so a plan view written against the fake would render EX in every
// test and 10000 against a site. Weakening the case is not the fix; sending an id
// from pkg/jira/jiratest is.

type planBuilder func(*testing.T) jira.PlanReader

func planFromSite(t *testing.T, opts ...jiratest.ServerOption) jira.PlanReader {
	t.Helper()

	s := jiratest.NewServer(opts...)
	t.Cleanup(s.Close)
	c, _ := testClient(t, s.URL())
	return c
}

func TestPlans_BothAdaptersAnswerTheSameWay(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name   string
		cloud  planBuilder
		fake   planBuilder
		assert func(*testing.T, []jira.Plan, error)
	}{
		{
			name: "every plan is identified, named, and says what state it is in",
			cloud: func(t *testing.T) jira.PlanReader {
				return planFromSite(t, jiratest.WithFixture(http.MethodGet, planPath, "plans_ok.json"))
			},
			fake: func(t *testing.T) jira.PlanReader { return conformFake(t) },
			assert: func(t *testing.T, got []jira.Plan, err error) {
				t.Helper()
				if err != nil {
					t.Fatalf("reading the site's plans: %v", err)
				}
				if len(got) == 0 {
					t.Fatal("the site came back with no plans, and both sites are supposed to hold some")
				}
				for _, plan := range got {
					if strings.TrimSpace(plan.ID) == "" {
						t.Errorf("the plan %q has no id, so nothing can open it: %+v", plan.Name, plan)
					}
					if strings.TrimSpace(plan.Name) == "" {
						t.Errorf("the plan %s has no name, and a name is the whole row: %+v", plan.ID, plan)
					}
					if strings.TrimSpace(plan.Status) == "" {
						t.Errorf("the plan %s says nothing about its state: %+v", plan.ID, plan)
					}
				}
			},
		},
		{
			name: "a plan read from Jira is never one this client made up",
			cloud: func(t *testing.T) jira.PlanReader {
				return planFromSite(t, jiratest.WithFixture(http.MethodGet, planPath, "plans_ok.json"))
			},
			fake: func(t *testing.T) jira.PlanReader { return conformFake(t) },
			assert: func(t *testing.T, got []jira.Plan, err error) {
				t.Helper()
				if err != nil {
					t.Fatalf("reading the site's plans: %v", err)
				}
				for _, plan := range got {
					// Local is the plan view's own flag for what config
					// defines. An adapter setting it would put a plan nobody
					// can open behind the same badge as one nobody has to.
					if plan.Local {
						t.Errorf("plan %s (%s) came back Local from an adapter that read it off a site", plan.ID, plan.Name)
					}
				}
			},
		},
		{
			name: "an issue source says what it points at, in the port's own words",
			cloud: func(t *testing.T) jira.PlanReader {
				return planFromSite(t, jiratest.WithFixture(http.MethodGet, planPath, "plans_ok.json"))
			},
			fake: func(t *testing.T) jira.PlanReader { return conformFake(t) },
			assert: func(t *testing.T, got []jira.Plan, err error) {
				t.Helper()
				if err != nil {
					t.Fatalf("reading the site's plans: %v", err)
				}
				sourced := 0
				for _, plan := range got {
					sourced += len(plan.Sources)
					for _, source := range plan.Sources {
						if strings.TrimSpace(source.Value) == "" {
							t.Errorf("plan %s draws on a %q with no value: %+v", plan.ID, source.Type, source)
						}
						// Jira capitalises these and the port does not, so a
						// value passed through raw would match none of the
						// constants a view branches on.
						if string(source.Type) != strings.ToLower(string(source.Type)) {
							t.Errorf("plan %s has a source typed %q, want the port's own spelling", plan.ID, source.Type)
						}
					}
				}
				if sourced == 0 {
					t.Error("no plan on either site draws on anything, so this proves nothing about sources")
				}
			},
		},
		{
			name: "a project issue source carries the numeric project id the schema documents",
			cloud: func(t *testing.T) jira.PlanReader {
				return planFromSite(t, jiratest.WithFixture(http.MethodGet, planPath, "plans_ok.json"))
			},
			fake: func(t *testing.T) jira.PlanReader { return conformFake(t) },
			assert: func(t *testing.T, got []jira.Plan, err error) {
				t.Helper()
				if err != nil {
					t.Fatalf("reading the site's plans: %v", err)
				}
				projects := 0
				for _, plan := range got {
					for _, source := range plan.Sources {
						if source.Type != jira.PlanSourceProject {
							continue
						}
						projects++
						if _, err := strconv.ParseInt(source.Value, 10, 64); err != nil {
							t.Errorf("plan %s draws on project %q, and issueSources[].value is a project id: nothing in this port turns a key back into one, so a view rendering this shows a different thing against each adapter",
								plan.ID, source.Value)
						}
					}
				}
				if projects == 0 {
					t.Error("no plan on either site draws on a project, so this proves nothing about project sources")
				}
			},
		},
		{
			name: "a refusal names the capability and the reason to show instead",
			cloud: func(t *testing.T) jira.PlanReader {
				// No override: refusing is what the fixture server does with
				// this route by default, because it is what a site does.
				return planFromSite(t)
			},
			fake: func(t *testing.T) jira.PlanReader {
				return conformFake(t, jiratest.WithCapabilities(jiratest.NoPlans))
			},
			assert: func(t *testing.T, got []jira.Plan, err error) {
				t.Helper()
				var refused *jira.CapabilityError
				if !errors.As(err, &refused) {
					t.Fatalf("got %+v, %T (%v); want a *jira.CapabilityError", got, err, err)
				}
				if refused.Capability != jira.CapPlans {
					t.Errorf("Capability = %q, want %q so the view knows it has a fallback for this one",
						refused.Capability, jira.CapPlans)
				}
				if refused.Reason == "" {
					t.Error("the refusal carries no reason, and the reason is what is drawn beside the local plans")
				}
				if len(got) != 0 {
					t.Errorf("the refusal came back with %+v attached; an empty list would read as a site with no plans", got)
				}
			},
		},
	}

	for _, tt := range cases {
		for _, adapter := range []struct {
			name string
			open planBuilder
		}{
			{name: "cloud", open: tt.cloud},
			{name: "fake", open: tt.fake},
		} {
			t.Run(tt.name+"/"+adapter.name, func(t *testing.T) {
				t.Parallel()

				got, err := adapter.open(t).Plans(t.Context())
				tt.assert(t, got, err)
			})
		}
	}
}
