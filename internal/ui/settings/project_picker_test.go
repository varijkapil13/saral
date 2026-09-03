package settings

import (
	"context"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"
	zone "github.com/lrstanley/bubblezone/v2"

	"github.com/varijkapil13/saral/internal/ui/kernel"
	"github.com/varijkapil13/saral/pkg/jira"
	"github.com/varijkapil13/saral/pkg/jira/jiratest"
)

// failsToSearch is a site whose one call the project picker makes is broken,
// the same shape palette's own refusesToSearch is — duplicated here rather
// than exported from palette, since a fake built for one package's tests is
// not part of its API.
type failsToSearch struct {
	*jiratest.Fake
	err error
}

func (f failsToSearch) Search(context.Context, jira.Query) (jira.Page[jira.Issue], error) {
	return jira.Page[jira.Issue]{}, f.err
}

// viewDriver drives any kernel.View generically, through the interface alone
// — what project_picker_test.go needs to prove is that the failure reaches
// this session, not the internals of palette's own view.
//
// It stands in for the kernel rather than for the view: a ReplyMsg is
// addressed and goes back into the view exactly as kernel.reply delivers one,
// and everything else — a status line, a pop — is what the kernel itself
// would consume, so it is recorded rather than handed to the view a second
// time.
type viewDriver struct {
	t    *testing.T
	v    kernel.View
	msgs []tea.Msg
}

func driveOpen(t *testing.T, v kernel.View, w, h int) *viewDriver {
	t.Helper()
	d := &viewDriver{t: t, v: v}
	d.send(kernel.SizeMsg{Width: w, Height: h})
	d.send(kernel.FocusMsg{Focused: true})
	d.run(v.Init())
	return d
}

func (d *viewDriver) send(msg tea.Msg) {
	d.t.Helper()
	v, cmd := d.v.Update(msg)
	d.v = v
	d.run(cmd)
}

func (d *viewDriver) run(cmd tea.Cmd) {
	d.t.Helper()
	queue := []tea.Cmd{cmd}
	for steps := 0; len(queue) > 0; steps++ {
		if steps > 500 {
			d.t.Fatal("commands never settled")
		}
		next := queue[0]
		queue = queue[1:]
		if next == nil {
			continue
		}
		msg := next()
		if msg == nil {
			continue
		}
		if cmds, ok := unwrapCmds(msg); ok {
			queue = append(queue, cmds...)
			continue
		}
		if reply, ok := msg.(kernel.ReplyMsg); ok {
			d.send(reply.Msg)
			continue
		}
		d.msgs = append(d.msgs, msg)
	}
}

func (d *viewDriver) frame() string { return ansi.Strip(d.v.View()) }

func (d *viewDriver) statuses() []string {
	out := []string{}
	for _, msg := range d.msgs {
		if status, ok := msg.(kernel.StatusMsg); ok {
			out = append(out, status.Text)
		}
	}
	return out
}

func openProjectFrom(t *testing.T, client jira.SessionClient) *viewDriver {
	t.Helper()
	d := kernel.Deps{
		Jira:  client,
		Theme: defaultTheme(),
		Zones: zone.New(),
		Now:   func() time.Time { return clockAt },
	}
	view := customPickers["session.project"](d)
	// customPickers returns a tea.Cmd (the push), not the view itself: run it
	// once to get at the PushMsg's own View, the same way the kernel would.
	msg := view()
	push, ok := msg.(kernel.PushMsg)
	if !ok {
		t.Fatalf("session.project's opener did not push a view: %#v", msg)
	}
	return driveOpen(t, push.View, 120, 24)
}

func siteWithProjects(t *testing.T) *jiratest.Fake {
	t.Helper()
	return jiratest.New(
		jiratest.WithProject("PROJ", jiratest.Scrum),
		jiratest.WithMe(jira.User{AccountID: "acct-1", DisplayName: "Nobody", TimeZone: time.UTC, Active: true}),
		jiratest.WithIssues(jiratest.GenFor("PROJ", 2)),
	)
}

func TestSessionProject_A403ReachesTheStatusLineInTheSitesWords(t *testing.T) {
	t.Parallel()
	client := failsToSearch{Fake: siteWithProjects(t), err: &jira.CapabilityError{Reason: "you do not have permission to search issues"}}
	d := openProjectFrom(t, client)
	// The picker is never left with nothing to pick: the whole site and the
	// scope in force are offered without the site's help.
	mustContain(t, d.frame(), "The whole site")
	mustContain(t, strings.Join(d.statuses(), " | "), "permission")
}

func TestSessionProject_A429ReachesTheStatusLineInTheSitesWords(t *testing.T) {
	t.Parallel()
	client := failsToSearch{Fake: siteWithProjects(t), err: &jira.RateLimitError{RetryAfter: 30 * time.Second}}
	d := openProjectFrom(t, client)
	mustContain(t, strings.Join(d.statuses(), " | "), "rate limited")
}

func TestSessionProject_ATransportFailureReachesTheStatusLine(t *testing.T) {
	t.Parallel()
	client := failsToSearch{Fake: siteWithProjects(t), err: &jira.TransportError{Op: "search", Err: context.DeadlineExceeded}}
	d := openProjectFrom(t, client)
	mustContain(t, strings.Join(d.statuses(), " | "), "search failed")
}
