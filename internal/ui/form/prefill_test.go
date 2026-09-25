package form

import (
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/varijkapil13/saral/internal/ui/kernel"
	"github.com/varijkapil13/saral/pkg/jira"
)

func prefilled(t *testing.T, d kernel.Deps, w, h int, opts ...Option) *driver {
	t.Helper()

	m := newWith(d, newSchemaCache(schemaTTL, time.Now))
	for _, opt := range opts {
		opt(m)
	}
	dr := &driver{t: t, m: m}
	dr.send(kernel.SizeMsg{Width: w, Height: h})
	dr.send(kernel.FocusMsg{Focused: true})
	dr.run(dr.m.Init())
	return dr
}

func (d *driver) typeSummary(text string) {
	d.t.Helper()

	d.focus("summary")
	d.key("enter")
	d.typeText(text)
	d.key("enter")
}

func TestPrefill_OpensStraightOntoTheIssueTypeAndNamesTheSprint(t *testing.T) {
	t.Parallel()

	sprint := jira.Sprint{ID: 7, Name: "Sprint 7", State: jira.SprintActive}
	dr := prefilled(t, testDeps(t, newFake(20)), 100, 24,
		WithIssueType(fakeStory), WithSprint(sprint), WithReport(kernel.NewAddr()))

	if dr.m.stage != stageFields {
		t.Fatalf("the form is still picking an issue type; it noted %q", dr.m.note)
	}
	if dr.m.chosen.ID != fakeStory {
		t.Errorf("the form opened on type %q, want %q", dr.m.chosen.ID, fakeStory)
	}
	golden(t, "prefilled_sprint_100x24.golden", dr.view())
}

func TestPrefill_CreatesInTheProjectItWasGiven(t *testing.T) {
	t.Parallel()

	c := newFake(20)
	d := testDeps(t, c)
	d.Project = "ELSEWHERE"
	dr := prefilled(t, d, 100, 24, WithProject("PROJ"), WithIssueType(fakeStory))
	if dr.m.stage != stageFields {
		t.Fatalf("the form is still picking an issue type; it noted %q", dr.m.note)
	}
	dr.typeSummary("Made from a board column")
	dr.submitRow()
	dr.key("enter")

	key := strings.Fields(dr.lastStatus().Text)
	if len(key) == 0 || !strings.HasPrefix(key[0], "PROJ-") {
		t.Fatalf("the status line says %q, want an issue created in PROJ", dr.lastStatus().Text)
	}
	if len(dr.elsewhere) != 0 {
		t.Errorf("a form nobody asked a report of sent %d answers elsewhere", len(dr.elsewhere))
	}
}

func TestPrefill_ReportsTheCreatedIssueToTheViewThatAsked(t *testing.T) {
	t.Parallel()

	c := newFake(20)
	to := kernel.NewAddr()
	sprint := jira.Sprint{ID: 7, Name: "Sprint 7", State: jira.SprintActive}
	dr := prefilled(t, testDeps(t, c), 100, 24, WithIssueType(fakeStory), WithSprint(sprint), WithReport(to))
	dr.typeSummary("Reported back")
	dr.submitRow()
	dr.key("enter")

	if len(dr.elsewhere) != 1 {
		t.Fatalf("%d answers went to another view, want the one report", len(dr.elsewhere))
	}
	reply := dr.elsewhere[0]
	if len(reply.To) != 1 || reply.To[0] != to {
		t.Errorf("the report went to %v, want %v", reply.To, to)
	}
	got, ok := reply.Msg.(CreatedMsg)
	if !ok {
		t.Fatalf("the report is a %T, want a CreatedMsg", reply.Msg)
	}
	if !strings.HasPrefix(got.Issue.Key, "PROJ-") || got.Issue.Summary != "Reported back" {
		t.Errorf("the report carries %+v, want the new issue", got.Issue)
	}
	if got.Sprint.ID != sprint.ID {
		t.Errorf("the report names sprint %d, want %d", got.Sprint.ID, sprint.ID)
	}
	if dr.pops != 1 || len(dr.casts) != 1 {
		t.Errorf("popped %d times and broadcast %d, want the form closed and what is behind it refreshed once each",
			dr.pops, len(dr.casts))
	}
}

func TestPrefill_ReportsNothingWhenTheCreateIsRefused(t *testing.T) {
	t.Parallel()

	for name, err := range map[string]error{
		"a 403":               &jira.CapabilityError{Reason: "You do not have permission to create issues in this project."},
		"a rate limit":        &jira.RateLimitError{RetryAfter: 30 * time.Second},
		"a transport failure": &jira.TransportError{Op: "POST /issue", Err: errors.New("connection reset")},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			c := newFake(20)
			dr := prefilled(t, testDeps(t, c), 100, 24, WithIssueType(fakeStory), WithReport(kernel.NewAddr()))
			dr.typeSummary("Never made")
			c.FailNext(err)
			dr.submitRow()
			dr.key("enter")

			if len(dr.elsewhere) != 0 {
				t.Errorf("a refused create reported %d issues", len(dr.elsewhere))
			}
			if dr.pops != 0 || dr.m.busy {
				t.Errorf("the form closed or stayed busy after a refusal: pops %d, busy %v", dr.pops, dr.m.busy)
			}
			if got := dr.lastStatus().Level; got != kernel.LevelError {
				t.Errorf("the refusal was reported at level %v", got)
			}
		})
	}
}
