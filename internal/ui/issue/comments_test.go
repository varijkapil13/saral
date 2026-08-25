package issue

import (
	"errors"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/varijkapil13/saral/internal/ui/comment"
	"github.com/varijkapil13/saral/internal/ui/kernel"
	"github.com/varijkapil13/saral/pkg/adf"
	"github.com/varijkapil13/saral/pkg/jira"
	"github.com/varijkapil13/saral/pkg/jira/jiratest"
)

// threadOf is the view the detail pane asked the kernel to push, checked against
// the issue it was opened from. The push is the assertion: a thread pushed under
// another issue's key is a thread about the wrong conversation.
func threadOf(t *testing.T, p *panel, key string) kernel.View {
	t.Helper()

	if len(p.pushes) != 1 {
		t.Fatalf("the pane asked for %d views to be pushed, want one", len(p.pushes))
	}
	got := p.pushes[0]
	if got.ID != comment.ViewID {
		t.Errorf("the pushed view is %q, want %q", got.ID, comment.ViewID)
	}
	if got.Title != key {
		t.Errorf("the thread was pushed titled %q, want the issue it is about, %q", got.Title, key)
	}
	if got.View == nil {
		t.Fatal("the push carried no view")
	}
	return got.View
}

func threadPanel(t *testing.T, p *panel, key string, w, h int) *panel {
	t.Helper()
	return newPanel(t, threadOf(t, p, key), w, h)
}

func TestComments_TheKeyOpensTheThreadForTheIssueOnScreen(t *testing.T) {
	t.Parallel()

	f := newFake(12)
	addComment(t, f, "PROJ-4", "What we agreed in the end.")
	addComment(t, f, "PROJ-5", "Said on an issue nobody opened.")
	p := newPanel(t, New(testDeps(f), seedOf(t, f, "PROJ-4")), 100, 24)

	p.keys("C")

	thread := threadPanel(t, p, "PROJ-4", 100, 24)
	mustContain(t, thread.frame(), "PROJ-4", "What we agreed in the end.")
	mustNotContain(t, thread.frame(), "Said on an issue nobody opened.")
}

// The palette knows which command ran and never which issue is on screen, so the
// command has to arrive as a broadcast and be answered by the pane that holds
// one.
func TestComments_ThePaletteOpensTheSameThreadTheKeyDoes(t *testing.T) {
	t.Parallel()

	f := newFake(12)
	addComment(t, f, "PROJ-6", "Reached without touching the keyboard shortcut.")
	p := newPanel(t, New(testDeps(f), seedOf(t, f, "PROJ-6")), 100, 24)

	p.send(CommentsMsg{})

	thread := threadPanel(t, p, "PROJ-6", 100, 24)
	mustContain(t, thread.frame(), "PROJ-6", "Reached without touching the keyboard shortcut.")
}

// Nothing retargets a thread that is already on the stack, because the kernel
// never reuses a pushed view: two panes open two threads, each about its own
// issue.
func TestComments_EachPaneOpensTheThreadOfItsOwnIssue(t *testing.T) {
	t.Parallel()

	f := newFake(12)
	addComment(t, f, "PROJ-1", "The first conversation.")
	addComment(t, f, "PROJ-2", "The second conversation.")
	d := testDeps(f)

	first := newPanel(t, New(d, seedOf(t, f, "PROJ-1")), 100, 24)
	first.keys("C")
	second := newPanel(t, New(d, seedOf(t, f, "PROJ-2")), 100, 24)
	second.keys("C")

	one := threadPanel(t, first, "PROJ-1", 100, 24)
	two := threadPanel(t, second, "PROJ-2", 100, 24)
	mustContain(t, one.frame(), "The first conversation.")
	mustNotContain(t, one.frame(), "The second conversation.")
	mustContain(t, two.frame(), "The second conversation.")
	mustNotContain(t, two.frame(), "The first conversation.")
}

// The whole point of the packet: a comment written, changed and removed without
// leaving the issue, and the fake agreeing at every step.
func TestComments_WriteEditAndDeleteReachTheSiteFromTheDetailPane(t *testing.T) {
	t.Parallel()

	f := newFake(12)
	p := newPanel(t, New(testDeps(f), seedOf(t, f, "PROJ-7")), 100, 24)
	p.keys("C")
	thread := threadPanel(t, p, "PROJ-7", 100, 24)

	thread.keys("a")
	thread.typed("Written from the issue.")
	thread.keys("ctrl+s")
	mustContain(t, thread.statusText(), "the comment has been added")
	if got := bodiesOn(t, f, "PROJ-7"); !slices.Contains(got, "Written from the issue.") {
		t.Fatalf("the site holds %v, not the comment that was written", got)
	}

	thread.keys("e")
	thread.typed(" Changed my mind.")
	thread.keys("ctrl+s")
	mustContain(t, thread.statusText(), "the comment has been changed")
	if got := bodiesOn(t, f, "PROJ-7"); !slices.Contains(got, "Written from the issue. Changed my mind.") {
		t.Fatalf("the site holds %v, not the edit", got)
	}

	thread.keys("d", "y")
	mustContain(t, thread.statusText(), "the comment is gone")
	if got := bodiesOn(t, f, "PROJ-7"); len(got) != 0 {
		t.Errorf("the site still holds %v after the deletion", got)
	}
}

// The confirmation is unskippable from here too: d alone leaves the comment
// where it is.
func TestComments_DeletingStillWaitsForTheAnswerItAsksFor(t *testing.T) {
	t.Parallel()

	f := newFake(12)
	addComment(t, f, "PROJ-8", "Not going anywhere.")
	p := newPanel(t, New(testDeps(f), seedOf(t, f, "PROJ-8")), 100, 24)
	p.keys("C")
	thread := threadPanel(t, p, "PROJ-8", 100, 24)

	thread.keys("d")
	mustContain(t, thread.frame(), "Not going anywhere.")
	if got := bodiesOn(t, f, "PROJ-8"); len(got) != 1 {
		t.Fatalf("the site holds %v; d on its own deleted a comment", got)
	}

	thread.keys("esc")
	if got := bodiesOn(t, f, "PROJ-8"); len(got) != 1 {
		t.Errorf("the site holds %v after the confirmation was refused", got)
	}
}

func TestComments_ReadingTheThreadThatFailsSaysWhyRatherThanShowingAnEmptyOne(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name string
		err  error
		want string
	}{
		{
			name: "a refusal",
			err:  &jira.CapabilityError{Reason: "you need the Browse Projects permission to read this issue"},
			want: "Browse Projects permission",
		},
		{
			name: "a rate limit",
			err:  &jira.RateLimitError{RetryAfter: 45 * time.Second},
			want: "retry in 45s",
		},
		{
			name: "a transport failure",
			err:  &jira.TransportError{Op: "GET /comment", Err: errors.New("connection refused")},
			want: "connection refused",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			f := newFake(12)
			addComment(t, f, "PROJ-3", "Never read.")
			p := newPanel(t, New(testDeps(f), seedOf(t, f, "PROJ-3")), 100, 24)
			p.keys("C")
			thread := newPanel(t, threadOf(t, p, "PROJ-3"), 100, 24)

			// A reread is what can fail after the first one worked, and it is the
			// same read: the thread fetches its own comments now that the pane
			// holds the thread itself rather than a copy of its rows.
			f.FailNext(tc.err)
			thread.send(kernel.RefreshMsg{})

			if got := thread.statusText(); !strings.Contains(got, tc.want) {
				t.Errorf("the status line says %q, want it to carry %q", got, tc.want)
			}
			mustContain(t, thread.frame(), "Never read.")
			mustNotContain(t, thread.frame(), "Nobody has commented")
		})
	}
}

// A pane built with no issue has nothing to comment on, and answers by doing
// nothing rather than by pushing a thread that says so.
func TestComments_APaneWithNoIssueOpensNothing(t *testing.T) {
	t.Parallel()

	p := newPanel(t, New(testDeps(newFake(2)), jira.Issue{}), 100, 24)

	p.keys("C")
	p.send(CommentsMsg{})

	if len(p.pushes) != 0 {
		t.Errorf("a pane with no issue pushed %d views", len(p.pushes))
	}
}

// C had to be free, and taking it must not have cost the pane a gesture it
// already spends: g g and g e still walk the description.
func TestComments_TakingCLeftTheOtherGesturesAlone(t *testing.T) {
	t.Parallel()

	f := newFake(12)
	seed := seedOf(t, f, "PROJ-9")
	seed.Description = longDoc(60)
	p := newPanel(t, New(testDeps(f), seed), 100, 12)
	p.send(loadedMsg{gen: p.pane(t).gen, issue: seed})

	p.keys("g", "e")
	if len(p.pushes) != 0 {
		t.Fatalf("g then e pushed %d views; it is the gesture that goes to the end of the issue", len(p.pushes))
	}
	if p.pane(t).tops[regionDesc] == 0 {
		t.Error("g then e did not reach the end of the description")
	}

	p.keys("g", "g")
	if p.pane(t).tops[regionDesc] != 0 {
		t.Error("g then g did not come back to the top of the description")
	}
}

func TestComments_RegisterTheKeyTheFooterShowsAndThePaletteEntryThatReachesIt(t *testing.T) {
	t.Parallel()

	if errs := kernel.RegistrationErrors(); len(errs) > 0 {
		t.Fatalf("registration went wrong: %v", errs)
	}

	var advertised []string
	set := kernel.KeysFor(ViewID)
	for _, row := range append([][]kernel.Binding{set.Short}, set.Full...) {
		for _, binding := range row {
			advertised = append(advertised, binding.Help().Key)
		}
	}
	if !slices.Contains(advertised, "C") {
		t.Errorf("the detail pane does not advertise %q; the footer only shows keys that work: %v", "C", advertised)
	}

	cmd, ok := kernel.LookupCommand("issue.comments")
	if !ok {
		t.Fatal("the palette has no issue.comments; every action is reachable a key and a command")
	}
	if cmd.Group != "Issue" {
		t.Errorf("issue.comments is grouped under %q, want it beside the other things done to an issue", cmd.Group)
	}
	if !slices.Equal(cmd.Keys, []string{"C"}) {
		t.Errorf("issue.comments teaches %v, want the key the detail pane shows", cmd.Keys)
	}

	if _, gone := kernel.LookupCommand("comments.open"); gone {
		t.Error("comments.open is still registered; it switched to a thread with no issue, which nothing can then satisfy")
	}
}

// bodiesOn is what the site holds for an issue, as plain text, so a test can say
// what was written rather than what the view thinks it wrote.
func bodiesOn(t *testing.T, f *jiratest.Fake, key string) []string {
	t.Helper()

	page, err := f.Comments(t.Context(), key)
	if err != nil {
		t.Fatalf("Comments: %v", err)
	}
	all, err := jira.Collect(t.Context(), page, 200)
	if err != nil {
		t.Fatalf("Collect: %v", err)
	}
	out := make([]string, 0, len(all))
	for i := range all {
		out = append(out, strings.TrimSpace(adf.Markdown(all[i].Body)))
	}
	return out
}
