package issue

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/varijkapil13/saral/internal/ui/kernel"
	"github.com/varijkapil13/saral/pkg/adf"
	"github.com/varijkapil13/saral/pkg/jira"
)

// The four sizes are the smallest terminal docs/UX.md supports, the breakpoint
// exactly, and one either side of it — and each one is drawn with the keyboard
// in the description and again with it in the fields, because the gutter rail is
// the only thing that says which.
func TestIssue_Golden(t *testing.T) {
	t.Parallel()

	for _, size := range []struct{ w, h int }{{80, 20}, {90, 28}, {100, 28}, {120, 38}} {
		name := fmt.Sprintf("%dx%d", size.w, size.h)
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			f := newFake(20)
			addComment(t, f, "PROJ-12", "Reproduced on staging, twice.", "The fix is in the shared client.")
			addComment(t, f, "PROJ-12", "Agreed. I will pick this up on Monday.")
			dr := newDriver(t, testDeps(f), seedOf(t, f, "PROJ-12"), size.w, size.h)

			golden(t, "issue_"+name+".golden", dr.view())

			dr.key("tab")
			golden(t, "issue_fields_"+name+".golden", dr.view())

			dr.key("tab")
			golden(t, "issue_thread_"+name+".golden", dr.view())
		})
	}
}

// Every frame is exactly as tall as the box the kernel gave it. A pane one line
// short leaves the previous frame's row on screen and one line long pushes the
// footer off it.
func TestIssue_TheFrameIsExactlyAsTallAsItsBox(t *testing.T) {
	t.Parallel()

	f := newFake(20)
	for _, size := range []struct{ w, h int }{{80, 20}, {90, 28}, {100, 28}, {120, 38}, {200, 60}} {
		dr := newDriver(t, testDeps(f), seedOf(t, f, "PROJ-12"), size.w, size.h)
		for _, focus := range []string{"", "tab", "tab"} {
			if focus != "" {
				dr.key(focus)
			}
			lines := strings.Split(dr.view(), "\n")
			if len(lines) != size.h {
				t.Errorf("%dx%d draws %d lines, want %d", size.w, size.h, len(lines), size.h)
			}
			for i, line := range lines {
				if got := ansi.StringWidth(line); got > size.w {
					t.Errorf("%dx%d line %d is %d cells wide, want at most %d: %q",
						size.w, size.h, i, got, size.w, line)
				}
			}
		}
	}
}

func TestIssue_DrawsTheRowItWasOpenedWithBeforeAnythingIsFetched(t *testing.T) {
	t.Parallel()

	f := newFake(20)
	seed := seedOf(t, f, "PROJ-7")
	f.Delay(time.Hour) // nothing will arrive during this test

	view, ok := New(testDeps(f), seed).(*Model)
	if !ok {
		t.Fatal("New did not return a *Model")
	}
	next, _ := view.Update(kernel.SizeMsg{Width: 120, Height: 30})
	m, _ := next.(*Model)

	mustContain(t, m.View(), seed.Key, seed.Summary, seed.Status.Name)
}

func TestIssue_ShowsTheCommentThreadOldestFirst(t *testing.T) {
	t.Parallel()

	f := newFake(20)
	addComment(t, f, "PROJ-3", "First thing said.")
	addComment(t, f, "PROJ-3", "Second thing said.")
	dr := newDriver(t, testDeps(f), seedOf(t, f, "PROJ-3"), 120, 40)

	got := dr.view()
	mustContain(t, got, "2 comments", "Sam Tester", "First thing said.", "Second thing said.")
	if strings.Index(got, "First thing said.") > strings.Index(got, "Second thing said.") {
		t.Errorf("the thread is not in the order it was written:\n%s", got)
	}
}

func TestIssue_SaysSoWhenNobodyHasCommented(t *testing.T) {
	t.Parallel()

	f := newFake(20)
	dr := newDriver(t, testDeps(f), seedOf(t, f, "PROJ-5"), 120, 40)

	mustContain(t, dr.view(), "Nobody has commented on PROJ-5")
}

// The description goes through the display renderer, so what is on screen is
// styled text rather than the markdown pkg/adf serialises for an editor. That
// markdown is what this pane used to draw, and it put ##, ** and [text](url) in
// front of the reader.
func TestIssue_RendersTheDescriptionAsStyledTextRatherThanMarkdown(t *testing.T) {
	t.Parallel()

	f := newFake(20)
	seed := seedOf(t, f, "PROJ-4")
	seed.Description = richDoc()
	dr := newDriver(t, testDeps(f), seed, 120, 40)
	dr.send(loadedMsg{gen: dr.m.gen, issue: seed})

	// The same document is asserted through the markdown serialisation first, so
	// that the markers being absent from the frame means something.
	markdown := adf.Markdown(seed.Description)
	mustContain(t, markdown, "## ", "**", "](")

	got := dr.view()
	mustContain(t, got, "What broke", "regressed", "the migration note", "the shared client")
	mustNotContain(t, got, "## ", "**", "](", "bulletList", "listItem")

	// And it is styled rather than merely stripped: the bold run comes back with
	// a sequence around it.
	if raw := dr.m.View(); !strings.Contains(raw, "\x1b[") {
		t.Error("the description carries no escape sequence at all, so nothing was styled")
	}
}

// richDoc is a description with the constructs the markdown serialisation would
// have spelt out: a heading, a bold run, a link and a code fence.
func richDoc() adf.Doc {
	bold := adf.NewText("regressed")
	bold.Marks = []adf.Mark{{Type: "strong"}}
	link := adf.NewText("the migration note")
	link.Marks = []adf.Mark{{Type: "link", Attrs: adf.Attrs{"href": "https://example.atlassian.net/wiki/x/1"}}}
	code := adf.NewNode("codeBlock", adf.NewText("return c.do(ctx, http.MethodPost, \"/export/\"+tenant, nil)"))
	code.Attrs = adf.Attrs{"language": "go"}
	heading := adf.NewNode("heading", adf.NewText("What broke"))
	heading.Attrs = adf.Attrs{"level": 2}
	return adf.NewDoc(
		heading,
		adf.NewNode("paragraph", adf.NewText("The export "), bold, adf.NewText(" after "), link, adf.NewText(".")),
		code,
		adf.NewNode("bulletList", adf.NewNode("listItem", adf.NewNode("paragraph", adf.NewText("It touches the shared client.")))),
	)
}

func TestIssue_ReadsTheIssueWithANarrowFieldSetRatherThanTheWholeThing(t *testing.T) {
	t.Parallel()

	f := newFake(20)
	seed := seedOf(t, f, "PROJ-6")
	before := len(f.Calls())
	newDriver(t, testDeps(f), seed, 120, 40)

	for _, call := range f.Calls()[before:] {
		if call == "Issue" {
			t.Error("the detail pane used the field-blind issue read; a search with a projection is the narrow one")
		}
	}
}

func TestIssue_ReportsWhatTheErrorItselfSays(t *testing.T) {
	t.Parallel()

	tests := map[string]struct {
		err  error
		want string
	}{
		"a permission it does not have": {
			err:  &jira.CapabilityError{Capability: jira.CapDeleteIssues, Reason: "needs Browse Projects permission"},
			want: "needs Browse Projects permission",
		},
		"the rate limiter": {
			err:  &jira.RateLimitError{RetryAfter: 45 * time.Second},
			want: "retry in 45s",
		},
		"the network": {
			err:  &jira.TransportError{Op: "search", Err: errors.New("connection reset")},
			want: "connection reset",
		},
	}

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			f := newFake(20)
			seed := seedOf(t, f, "PROJ-2")
			f.FailNextN(2, tc.err)
			dr := newDriver(t, testDeps(f), seed, 120, 30)

			if status := dr.lastStatus(); !strings.Contains(status.Text, tc.want) {
				t.Errorf("the status line reads %q, want it to carry %q", status.Text, tc.want)
			}
			mustContain(t, dr.view(), seed.Key, seed.Summary)
		})
	}
}

func TestIssue_SaysSoWhenTheIssueIsGone(t *testing.T) {
	t.Parallel()

	f := newFake(5)
	dr := newDriver(t, testDeps(f), jira.Issue{Key: "PROJ-999", Summary: "deleted while you were reading"}, 120, 30)

	if got := dr.lastStatus().Text; !strings.Contains(got, "PROJ-999") {
		t.Errorf("the status line reads %q, want it to name the missing issue", got)
	}
}

// The pane is blurred whenever anything is pushed over it — the palette, the
// editor, the thread on the whole screen — and none of those is the pane being
// thrown away. The read carries on, and it carries this pane's address, so the
// kernel has somewhere to deliver it other than whatever ends up on top.
//
// Coming back therefore asks for nothing: there is an answer on its way. That
// the answer really arrives is asserted through the kernel, in
// TestSession_AnAnswerLandingWhileThePaneIsCoveredStillReachesThePane — here it
// would be this test handing the pane its own message, which is exactly the step
// the kernel used not to take.
func TestIssue_LosingTheKeyboardDoesNotGiveUpTheRead(t *testing.T) {
	t.Parallel()

	f := newFake(20)
	seed := seedOf(t, f, "PROJ-8")
	f.Delay(50 * time.Millisecond)

	view, ok := New(testDeps(f), seed).(*Model)
	if !ok {
		t.Fatal("New did not return a *Model")
	}
	next, _ := view.Update(kernel.SizeMsg{Width: 120, Height: 30})
	m, _ := next.(*Model)
	cmd := m.Init()

	next, _ = m.Update(kernel.FocusMsg{})
	m, _ = next.(*Model)

	msgs := collect(cmd)
	if !readTheIssue(msgs, m.Addr()) {
		t.Errorf("the read did not survive the pane losing the keyboard, addressed to it: %+v", msgs)
	}

	gen := m.gen
	dr := &driver{t: t, m: m}
	dr.send(kernel.FocusMsg{Focused: true})
	if dr.m.gen != gen {
		t.Errorf("coming back asked a second time while the first read was still out: generation %d, was %d",
			dr.m.gen, gen)
	}
}

// A pane that really has been discarded stops, and takes the thread it holds
// with it: the sidebar is the only thing drawing that model.
func TestIssue_ClosingStopsTheReadAndTheThreadItHolds(t *testing.T) {
	t.Parallel()

	f := newFake(20)
	seed := seedOf(t, f, "PROJ-8")
	f.Delay(50 * time.Millisecond)

	view, ok := New(testDeps(f), seed).(*Model)
	if !ok {
		t.Fatal("New did not return a *Model")
	}
	next, _ := view.Update(kernel.SizeMsg{Width: 120, Height: 30})
	m, _ := next.(*Model)
	cmd := m.fetch()

	thread := &closeSpy{}
	m.thread = thread
	m.Close()

	if msgs := answers(collect(cmd)); !allCancelled(msgs) {
		t.Errorf("a discarded pane went on reading: %+v", msgs)
	}
	if thread.closed != 1 {
		t.Errorf("the thread was closed %d times, want once", thread.closed)
	}
}

// closeSpy stands in for the thread, which answers with a message type this
// package cannot see into.
type closeSpy struct{ closed int }

func (s *closeSpy) Init() tea.Cmd                         { return nil }
func (s *closeSpy) Update(tea.Msg) (kernel.View, tea.Cmd) { return s, nil }
func (s *closeSpy) View() string                          { return "" }
func (s *closeSpy) Close()                                { s.closed++ }

// readTheIssue holds the read to two things at once: that it answered with the
// issue rather than with a cancellation, and that it named this pane on the way
// out. An answer with no address is one the kernel can only give to the top of
// the stack, which is the whole of the bug.
func readTheIssue(msgs []tea.Msg, addr kernel.Addr) bool {
	for _, msg := range msgs {
		reply, sent := msg.(kernel.ReplyMsg)
		if !sent || !slices.Contains(reply.To, addr) {
			continue
		}
		if _, ok := reply.Msg.(loadedMsg); ok {
			return true
		}
	}
	return false
}

// answers is what the kernel hands a view: the envelope off, the message inside.
func answers(msgs []tea.Msg) []tea.Msg {
	out := make([]tea.Msg, 0, len(msgs))
	for _, msg := range msgs {
		if reply, sent := msg.(kernel.ReplyMsg); sent {
			msg = reply.Msg
		}
		out = append(out, msg)
	}
	return out
}

func collect(cmd tea.Cmd) []tea.Msg {
	var out []tea.Msg
	queue := []tea.Cmd{cmd}
	for len(queue) > 0 {
		next := queue[0]
		queue = queue[1:]
		if next == nil {
			continue
		}
		msg := next()
		if cmds, ok := unwrapCmds(msg); ok {
			queue = append(queue, cmds...)
			continue
		}
		out = append(out, msg)
	}
	return out
}

// allCancelled holds this pane's own read to having been given up. The thread
// reads its own comments and answers with a message of its own, and whether that
// one was cancelled is a question for the package that owns it.
func allCancelled(msgs []tea.Msg) bool {
	seen := 0
	for _, msg := range msgs {
		failed, ok := msg.(failedMsg)
		if !ok {
			continue
		}
		seen++
		if !errors.Is(failed.err, context.Canceled) {
			return false
		}
	}
	return seen > 0
}

func TestIssue_RendersDatesInTheAccountsTimezoneAndNotTheMachines(t *testing.T) {
	t.Parallel()

	kolkata, err := time.LoadLocation("Asia/Kolkata")
	if err != nil {
		t.Skipf("no timezone database here: %v", err)
	}

	f := newFake(20)
	addComment(t, f, "PROJ-9", "Said something.")
	seed := seedOf(t, f, "PROJ-9")

	utc := newDriver(t, testDeps(f), seed, 120, 40)
	d := testDeps(f)
	d.Caps.TimeZone = kolkata
	shifted := newDriver(t, d, seed, 120, 40)

	mustContain(t, utc.view(), "02 Mar 2026 09:00")
	mustContain(t, shifted.view(), "02 Mar 2026 14:30")
}

func TestIssue_StillRendersWhenTheCapabilityProbeFoundNothing(t *testing.T) {
	t.Parallel()

	f := newFake(20)
	addComment(t, f, "PROJ-10", "Still readable.")
	d := testDeps(f)
	d.Caps = jira.Capabilities{}
	dr := newDriver(t, d, seedOf(t, f, "PROJ-10"), 120, 30)

	golden(t, "issue_no_caps_120x30.golden", dr.view())
}

func TestIssue_ScrollsTheBodyAndKeepsTheIdentityLinesPut(t *testing.T) {
	t.Parallel()

	f := newFake(20)
	seed := seedOf(t, f, "PROJ-11")
	seed.Description = longDoc(30)
	dr := newDriver(t, testDeps(f), seed, 100, 14)
	dr.send(loadedMsg{gen: dr.m.gen, issue: seed})

	top := dr.view()
	dr.key("G")
	bottom := dr.view()

	if top == bottom {
		t.Error("the description did not move")
	}
	head := strings.SplitN(top, "\n", 2)[0]
	if !strings.HasPrefix(bottom, head) {
		t.Errorf("the identity line scrolled away with the description:\n%s", bottom)
	}

	dr.key("g", "g")
	if dr.view() != top {
		t.Error("g g did not go back to the top")
	}
}

// A motion is aimed at the region that has the keyboard, and the thread is a
// view rather than a list of lines, so it is handed the stroke that means the
// same thing in its own keymap.
func TestIssue_AMotionMovesTheRegionThatHasTheKeyboard(t *testing.T) {
	t.Parallel()

	f := newFake(20)
	for i := range 12 {
		addComment(t, f, "PROJ-11", "Comment number "+strconv.Itoa(i+1)+", worth a line or two of somebody's afternoon.")
	}
	seed := seedOf(t, f, "PROJ-11")
	seed.Description = longDoc(30)
	dr := newDriver(t, testDeps(f), seed, 120, 30)
	dr.send(loadedMsg{gen: dr.m.gen, issue: seed})

	before := dr.view()
	dr.key("tab", "tab")
	if dr.m.focus != regionComments {
		t.Fatalf("two tabs left the keyboard on region %d, want the thread", dr.m.focus)
	}
	dr.key("G")
	if dr.m.tops[regionDesc] != 0 {
		t.Error("G moved the description while the thread had the keyboard")
	}
	if dr.view() == before {
		t.Error("G with the thread focused moved nothing at all")
	}
}

func TestIssue_AResizeReflowsWithoutLosingThePlace(t *testing.T) {
	t.Parallel()

	f := newFake(20)
	for range 20 {
		addComment(t, f, "PROJ-13", "Something worth several lines when the pane is narrow.")
	}
	seed := seedOf(t, f, "PROJ-13")
	seed.Description = longDoc(40)
	dr := newDriver(t, testDeps(f), seed, 120, 16)
	dr.send(loadedMsg{gen: dr.m.gen, issue: seed})
	dr.key("ctrl+d", "ctrl+d")
	at := dr.m.tops[regionDesc]
	if at == 0 {
		t.Fatal("the description never scrolled, so this proves nothing")
	}

	dr.send(kernel.SizeMsg{Width: 110, Height: 16})
	if got := dr.m.tops[regionDesc]; got != at {
		t.Errorf("the resize moved the description to line %d, want %d", got, at)
	}
}

func TestIssue_ARefreshRereadsTheIssueAndTheThread(t *testing.T) {
	t.Parallel()

	f := newFake(20)
	dr := newDriver(t, testDeps(f), seedOf(t, f, "PROJ-14"), 120, 40)
	addComment(t, f, "PROJ-14", "Added while you were reading.")

	dr.send(kernel.RefreshMsg{})

	mustContain(t, dr.view(), "Added while you were reading.")
}

func TestIssue_RegistersItsKeysUnderItsOwnScopeAndNoFooterSlot(t *testing.T) {
	t.Parallel()

	if set := kernel.KeysFor(ViewID); set.IsZero() {
		t.Error("the detail pane registered no keys, so the footer cannot advertise it")
	}
	if _, ok := kernel.LookupView(ViewID); ok {
		t.Error("the detail pane registered a view spec, but it cannot be built without an issue")
	}
}
