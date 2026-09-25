package backlog

import (
	"context"
	"errors"
	"slices"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/varijkapil13/saral/internal/ui/form"
	"github.com/varijkapil13/saral/internal/ui/kernel"
	"github.com/varijkapil13/saral/pkg/jira"
	"github.com/varijkapil13/saral/pkg/jira/jiratest"
)

// createSite refuses the single-issue read back on request and can hide one
// issue from the board's read, the way an index that has not caught up with a
// create does.
type createSite struct {
	*jiratest.Fake

	mu       sync.Mutex
	readFail error
	hide     string
}

func (c *createSite) IssueFields(ctx context.Context, key string, fields []string) (jira.Issue, error) {
	c.mu.Lock()
	err := c.readFail
	c.mu.Unlock()
	if err != nil {
		return jira.Issue{}, err
	}
	return c.Fake.IssueFields(ctx, key, fields)
}

func (c *createSite) BoardIssues(ctx context.Context, boardID int64, q jira.BoardQuery) (jira.Page[jira.Issue], error) {
	page, err := c.Fake.BoardIssues(ctx, boardID, q)
	c.mu.Lock()
	hide := c.hide
	c.mu.Unlock()
	if hide != "" {
		page.Items = slices.DeleteFunc(page.Items, func(iss jira.Issue) bool { return iss.Key == hide })
	}
	return page, err
}

func (c *createSite) set(fn func()) {
	c.mu.Lock()
	defer c.mu.Unlock()
	fn()
}

const storyType = "10301"

func createOnSite(t *testing.T, f *jiratest.Fake) jira.Issue {
	t.Helper()
	iss, err := f.CreateIssue(context.Background(), jira.IssueInput{
		ProjectKey: "PROJ", IssueTypeID: storyType, Summary: "Made from a backlog section",
	})
	if err != nil {
		t.Fatal(err)
	}
	return iss
}

// cursorOnSection puts the cursor on the head of the first sprint section, or of
// the backlog when sprint is false, and hands back the sprint it is for. held
// asks for a section with an issue in it.
func (d *driver) cursorOnSection(sprint, held bool) jira.Sprint {
	d.t.Helper()
	for g := range d.m.groups {
		if (d.m.groups[g].id != 0) != sprint || (held && len(d.m.groups[g].issues) == 0) {
			continue
		}
		d.cursorTo("head:" + strconv.Itoa(g))
		if !sprint {
			return jira.Sprint{}
		}
		at := slices.IndexFunc(d.m.sprints, func(sp jira.Sprint) bool { return sp.ID == d.m.groups[g].id })
		return d.m.sprints[at]
	}
	d.t.Fatalf("the fixture has no section with sprint=%v", sprint)
	return jira.Sprint{}
}

func (d *driver) onCursor() string {
	if iss := d.m.issueAt(d.m.cursor); iss != nil {
		return iss.Key
	}
	return ""
}

func sprintHolds(t *testing.T, f *jiratest.Fake, m *Model, key string, sprintID int64) bool {
	t.Helper()
	iss, err := f.Issue(context.Background(), key)
	if err != nil {
		t.Fatal(err)
	}
	return slices.Contains(m.sprintsOn(&iss), sprintID)
}

// formView drives a pushed form until it settles and returns its frame, which is
// the only place the answers it was opened with can be read from outside it.
func formView(t *testing.T, v kernel.View) string {
	t.Helper()
	queue := []tea.Cmd{}
	step := func(msg tea.Msg) {
		next, cmd := v.Update(msg)
		v = next
		queue = append(queue, cmd)
	}
	step(kernel.SizeMsg{Width: 100, Height: 24})
	step(kernel.FocusMsg{Focused: true})
	queue = append(queue, v.Init())
	for steps := 0; len(queue) > 0; steps++ {
		if steps > 2000 {
			t.Fatal("the form's commands never settled")
		}
		cmd := queue[0]
		queue = queue[1:]
		if cmd == nil {
			continue
		}
		msg := cmd()
		if cmds, ok := unwrapCmds(msg); ok {
			queue = append(queue, cmds...)
			continue
		}
		if reply, ok := msg.(kernel.ReplyMsg); ok {
			msg = reply.Msg
		}
		switch msg.(type) {
		case nil, kernel.StatusMsg, kernel.BroadcastMsg:
			continue
		}
		step(msg)
	}
	return ansi.Strip(v.View())
}

func TestCreate_OpensTheFormAnsweredWithTheSectionUnderTheCursor(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name    string
		sprint  bool
		subtask bool
		heading string
	}{
		{"an issue in a sprint names its type and the sprint", true, false, "for "},
		{"an issue in the backlog names its type and no sprint", false, false, ""},
		{"a subtask leaves the type to the picker", true, true, ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			f := newFake(12)
			dr := newDriver(t, testDeps(f), 120, 30)
			sp := dr.cursorOnSection(tc.sprint, false)
			if tc.sprint {
				if err := f.MoveToSprint(context.Background(), sp.ID, []string{"PROJ-1"}); err != nil {
					t.Fatal(err)
				}
				dr.send(kernel.RefreshMsg{})
			}
			want := backlogName
			if tc.sprint {
				want = sp.Name
			}
			if got := dr.groupOf("PROJ-1"); got != want {
				t.Fatalf("PROJ-1 is drawn in %q, want %q", got, want)
			}
			dr.cursorTo("row:PROJ-1")
			at := dr.m.byKey["PROJ-1"]
			if tc.subtask {
				dr.m.issues[at].Type = jira.IssueType{ID: "10305", Name: "Offshoot", Subtask: true}
			} else {
				dr.m.issues[at].Type = jira.IssueType{ID: storyType, Name: "Story"}
			}
			dr.key("c")
			if len(dr.pushes) != 1 {
				t.Fatalf("c pushed %d views, want the form", len(dr.pushes))
			}
			push := dr.pushes[0]
			if push.ID != form.ViewID {
				t.Fatalf("c pushed %q, want %q", push.ID, form.ViewID)
			}
			if _, ok := push.View.(*form.Model); !ok {
				t.Fatalf("c pushed a %T, want the create form", push.View)
			}
			heading := strings.SplitN(formView(t, push.View), "\n", 2)[0]
			switch {
			case tc.subtask:
				if strings.HasPrefix(heading, "New Offshoot") {
					t.Errorf("the form opened on the subtask type: %q", heading)
				}
			case tc.sprint:
				mustContain(t, heading, "New Story in PROJ", "for "+sp.Name)
			default:
				mustContain(t, heading, "New Story in PROJ")
				mustNotContain(t, heading, " for ")
			}
			if dr.m.createOn != dr.m.config.BoardID {
				t.Errorf("the create was started on board %d, want %d", dr.m.createOn, dr.m.config.BoardID)
			}
		})
	}
}

func TestCreate_RefusesWhenThereIsNothingToCreateIn(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name  string
		setup func(t *testing.T) *driver
		want  string
	}{
		{"no Jira connection", func(t *testing.T) *driver {
			return newDriver(t, testDeps(nil), 100, 20)
		}, "no Jira connection"},
		{"a backlog whose board never loaded", func(t *testing.T) *driver {
			f := newFake(8)
			f.FailNext(&jira.TransportError{Op: "GET /board", Err: errors.New("connection reset")})
			return newDriver(t, testDeps(f), 100, 20)
		}, "no board loaded"},
		{"a move still going", func(t *testing.T) *driver {
			dr := newDriver(t, testDeps(newFake(8)), 100, 20)
			dr.m.mode = movingIssues
			return dr
		}, "this move is still going"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			dr := tc.setup(t)
			dr.send(CreateMsg{})
			if len(dr.pushes) != 0 {
				t.Fatalf("the form was opened anyway")
			}
			got := dr.lastStatus()
			if got.Level != kernel.LevelWarn || !strings.Contains(got.Text, tc.want) {
				t.Errorf("the status says %q at level %v, want a warning naming %q", got.Text, got.Level, tc.want)
			}
		})
	}
}

func TestCreate_AnIssueForASprintIsMovedThereAndDrawnInIt(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name   string
		sprint bool
	}{
		{"a sprint section", true},
		{"the backlog section", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			f := newFake(12)
			dr := newDriver(t, testDeps(f), 120, 30)
			sp := dr.cursorOnSection(tc.sprint, false)
			dr.key("c")
			iss := createOnSite(t, f)
			moves := countCalls(f, "MoveToSprint")

			dr.send(form.CreatedMsg{Issue: iss, Sprint: sp})

			where := backlogName
			if tc.sprint {
				where = sp.Name
				if !sprintHolds(t, f, dr.m, iss.Key, sp.ID) {
					t.Errorf("the site does not have %s in %s", iss.Key, sp.Name)
				}
				if got := countCalls(f, "MoveToSprint"); got != moves+1 {
					t.Errorf("the create made %d moves, want 1", got-moves)
				}
			} else if got := countCalls(f, "MoveToSprint"); got != moves {
				t.Errorf("an issue for the backlog was moved %d times; the site creates it there", got-moves)
			}
			if got := dr.groupOf(iss.Key); got != where {
				t.Errorf("%s is drawn in %q, want %q", iss.Key, got, where)
			}
			if got := dr.onCursor(); got != iss.Key {
				t.Errorf("the cursor is on %q, want the new issue %s", got, iss.Key)
			}
			want := iss.Key + " created in " + where
			if got := dr.lastStatus(); got.Text != want || got.Level != kernel.LevelInfo {
				t.Errorf("the status says %q at level %v, want %q", got.Text, got.Level, want)
			}
		})
	}
}

func TestCreate_EachFailureSaysWhichHalfHappened(t *testing.T) {
	t.Parallel()
	refusals := []struct {
		name string
		err  error
	}{
		{"403", &jira.CapabilityError{Capability: jira.CapBoards, Reason: "this token may not schedule issues"}},
		{"429", &jira.RateLimitError{RetryAfter: 30 * time.Second}},
		{"transport", &jira.TransportError{Op: "POST /sprint", Err: errors.New("connection reset")}},
	}
	for _, r := range refusals {
		t.Run("the move is refused with a "+r.name, func(t *testing.T) {
			t.Parallel()
			f := newFake(12)
			dr := newDriver(t, testDeps(f), 120, 30)
			sp := dr.cursorOnSection(true, false)
			dr.key("c")
			iss := createOnSite(t, f)
			f.FailNext(r.err)

			dr.send(form.CreatedMsg{Issue: iss, Sprint: sp})

			reason, _ := jira.Reason(r.err)
			want := iss.Key + " was created but is still in the backlog: " + reason
			if got := dr.lastStatus(); got.Text != want || got.Level != kernel.LevelError {
				t.Errorf("the status says %q at level %v, want %q", got.Text, got.Level, want)
			}
			if sprintHolds(t, f, dr.m, iss.Key, sp.ID) {
				t.Fatal("the fake moved the issue despite the refusal")
			}
			if got := dr.groupOf(iss.Key); got != backlogName {
				t.Errorf("%s is drawn in %q; it is in the backlog", iss.Key, got)
			}
			claimsSuccess(t, dr, iss.Key)
		})
		t.Run("the read back is refused with a "+r.name, func(t *testing.T) {
			t.Parallel()
			site := &createSite{Fake: newFake(12)}
			dr := newDriver(t, testDeps(site), 120, 30)
			sp := dr.cursorOnSection(true, false)
			dr.key("c")
			iss := createOnSite(t, site.Fake)
			site.set(func() { site.readFail = r.err })

			dr.send(form.CreatedMsg{Issue: iss, Sprint: sp})

			reason, _ := jira.Reason(r.err)
			want := iss.Key + " was created in " + sp.Name + " but reading it back failed: " + reason
			if got := dr.lastStatus(); got.Text != want || got.Level != kernel.LevelError {
				t.Errorf("the status says %q at level %v, want %q", got.Text, got.Level, want)
			}
			if !sprintHolds(t, site.Fake, dr.m, iss.Key, sp.ID) {
				t.Error("a refused read back undid the move")
			}
			if _, drawn := dr.m.byKey[iss.Key]; drawn {
				t.Error("an issue nobody could read back is drawn anyway")
			}
			claimsSuccess(t, dr, iss.Key)
		})
	}
}

func claimsSuccess(t *testing.T, dr *driver, key string) {
	t.Helper()
	for _, s := range dr.statuses {
		if strings.HasPrefix(s.Text, key+" created in ") {
			t.Errorf("the status line claimed success: %q", s.Text)
		}
	}
}

// The form's refresh re-reads the board, and the index behind that read trails
// the create by seconds.
func TestCreate_ARereadTheIndexHasNotCaughtUpWithKeepsTheNewIssue(t *testing.T) {
	t.Parallel()
	site := &createSite{Fake: newFake(12)}
	dr := newDriver(t, testDeps(site), 120, 30)
	sp := dr.cursorOnSection(true, false)
	dr.key("c")
	iss := createOnSite(t, site.Fake)
	site.set(func() { site.hide = iss.Key })
	dr.send(form.CreatedMsg{Issue: iss, Sprint: sp})

	dr.send(kernel.RefreshMsg{})
	if got := dr.groupOf(iss.Key); got != sp.Name {
		t.Fatalf("after a re-read that lacks it, %s is drawn in %q, want %q", iss.Key, got, sp.Name)
	}

	site.set(func() { site.hide = "" })
	dr.send(kernel.RefreshMsg{})
	held := 0
	for i := range dr.m.issues {
		if dr.m.issues[i].Key == iss.Key {
			held++
		}
	}
	if held != 1 {
		t.Errorf("once the read has it, %s is held %d times", iss.Key, held)
	}
	if len(dr.m.made) != 0 {
		t.Errorf("the view still carries %v after a read that has them", dr.m.made)
	}
	if got := dr.groupOf(iss.Key); got != sp.Name {
		t.Errorf("%s is drawn in %q, want %q", iss.Key, got, sp.Name)
	}
}

func TestCreate_AReportAfterTheBoardChangedMovesTheIssueButDrawsNothing(t *testing.T) {
	t.Parallel()
	f := newFake(12, jiratest.WithProject("OTHER", jiratest.Scrum))
	dr := newDriver(t, testDeps(f), 120, 30)
	sp := dr.cursorOnSection(true, false)
	dr.key("c")
	iss := createOnSite(t, f)
	dr.send(kernel.ProjectMsg{Project: "OTHER"})
	if !dr.m.loaded || dr.m.config.BoardID == dr.m.createOn {
		t.Fatalf("the switch left board %d on screen, the one the create started on", dr.m.config.BoardID)
	}
	reads := countCalls(f, "IssueFields")

	dr.send(form.CreatedMsg{Issue: iss, Sprint: sp})

	if !sprintHolds(t, f, dr.m, iss.Key, sp.ID) {
		t.Errorf("the site does not have %s in %s", iss.Key, sp.Name)
	}
	if _, drawn := dr.m.byKey[iss.Key]; drawn {
		t.Error("the issue was drawn on a board it does not belong to")
	}
	if got := countCalls(f, "IssueFields"); got != reads {
		t.Errorf("the issue was read back %d times for a board no longer on screen", got-reads)
	}
	mustContain(t, dr.lastStatus().Text, iss.Key+" created in "+sp.Name, "no longer showing")
}

func TestCreate_IsAKeyAndAPaletteCommand(t *testing.T) {
	t.Parallel()
	browse, _, _, _, _ := defaultKeys().tables()
	if browse["c"] != actCreate {
		t.Error("c does not create in the browsing state")
	}
	if !slices.ContainsFunc(liveSets[keysBrowsing].Acts, func(b kernel.Binding) bool {
		return b.Help().Key == defaultKeys().Create.Help().Key
	}) {
		t.Error("the resting footer does not advertise c")
	}
	cmd, ok := kernel.LookupCommand("backlog.create")
	if !ok {
		t.Fatal("backlog.create is not registered")
	}
	if !slices.Contains(cmd.Keys, "c") || cmd.Requires != jira.CapBoards {
		t.Errorf("backlog.create carries keys %v and requires %v", cmd.Keys, cmd.Requires)
	}
}
