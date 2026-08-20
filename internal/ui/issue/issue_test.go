package issue

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/varijkapil13/saral/internal/ui/kernel"
	"github.com/varijkapil13/saral/pkg/jira"
	"github.com/varijkapil13/saral/pkg/jira/jiratest"
)

func TestIssue_Golden(t *testing.T) {
	t.Parallel()

	for name, size := range map[string]struct{ w, h int }{
		"120x38": {120, 38},
		"100x28": {100, 28},
		"80x18":  {80, 18},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			f := newFake(20)
			comment(t, f, "PROJ-12", "Reproduced on staging, twice.", "The fix is in the shared client.")
			comment(t, f, "PROJ-12", "Agreed. I will pick this up on Monday.")
			dr := newDriver(t, testDeps(f), seedOf(t, f, "PROJ-12"), size.w, size.h)

			golden(t, "issue_"+name+".golden", dr.view())
		})
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
	comment(t, f, "PROJ-3", "First thing said.")
	comment(t, f, "PROJ-3", "Second thing said.")
	dr := newDriver(t, testDeps(f), seedOf(t, f, "PROJ-3"), 120, 40)

	got := dr.view()
	mustContain(t, got, "Comments (2)", "Sam Tester", "First thing said.", "Second thing said.")
	if strings.Index(got, "First thing said.") > strings.Index(got, "Second thing said.") {
		t.Errorf("the thread is not in the order it was written:\n%s", got)
	}
}

func TestIssue_SaysSoWhenNobodyHasCommented(t *testing.T) {
	t.Parallel()

	f := newFake(20)
	dr := newDriver(t, testDeps(f), seedOf(t, f, "PROJ-5"), 120, 40)

	mustContain(t, dr.view(), "Comments (0)", "Nobody has commented.")
}

func TestIssue_RendersTheDescriptionThroughTheMarkdownRenderer(t *testing.T) {
	t.Parallel()

	f := newFake(20)
	dr := newDriver(t, testDeps(f), seedOf(t, f, "PROJ-4"), 120, 40)

	// jiratest.Gen writes every description as a paragraph followed by a bullet
	// list, so the markers are what prove ADF was rendered rather than dropped.
	mustContain(t, dr.view(), "- Filed against PROJ-4.")
	mustNotContain(t, dr.view(), "bulletList", "listItem")
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

func TestIssue_LosingFocusStopsTheWorkAndGettingItBackStartsItAgain(t *testing.T) {
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
	if msgs := collect(cmd); !allCancelled(msgs) {
		t.Errorf("the in-flight requests survived the pane losing focus: %+v", msgs)
	}

	f.Delay(0)
	dr := &driver{t: t, m: m}
	dr.send(kernel.FocusMsg{Focused: true})
	if !dr.m.loadedIssue || !dr.m.loadedComments {
		t.Error("getting focus back did not start the work again")
	}
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

func allCancelled(msgs []tea.Msg) bool {
	if len(msgs) == 0 {
		return false
	}
	for _, msg := range msgs {
		failed, ok := msg.(failedMsg)
		if !ok || !errors.Is(failed.err, context.Canceled) {
			return false
		}
	}
	return true
}

func TestIssue_RendersDatesInTheAccountsTimezoneAndNotTheMachines(t *testing.T) {
	t.Parallel()

	kolkata, err := time.LoadLocation("Asia/Kolkata")
	if err != nil {
		t.Skipf("no timezone database here: %v", err)
	}

	f := newFake(20)
	comment(t, f, "PROJ-9", "Said something.")
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
	comment(t, f, "PROJ-10", "Still readable.")
	d := testDeps(f)
	d.Caps = jira.Capabilities{}
	dr := newDriver(t, d, seedOf(t, f, "PROJ-10"), 120, 30)

	golden(t, "issue_no_caps_120x30.golden", dr.view())
}

func TestIssue_ScrollsTheBodyAndKeepsTheIdentityLinesPut(t *testing.T) {
	t.Parallel()

	f := newFake(20)
	for i := range 30 {
		comment(t, f, "PROJ-11", "Comment number "+strings.Repeat("x", i%5+1))
	}
	dr := newDriver(t, testDeps(f), seedOf(t, f, "PROJ-11"), 100, 14)

	top := dr.view()
	dr.key("G")
	bottom := dr.view()

	if top == bottom {
		t.Error("the pager did not move")
	}
	head := strings.SplitN(top, "\n", 2)[0]
	if !strings.HasPrefix(bottom, head) {
		t.Errorf("the identity line scrolled away with the body:\n%s", bottom)
	}

	dr.key("g", "g")
	if dr.view() != top {
		t.Error("g g did not go back to the top")
	}
}

func TestIssue_AResizeReflowsWithoutLosingThePlace(t *testing.T) {
	t.Parallel()

	f := newFake(20)
	for range 20 {
		comment(t, f, "PROJ-13", "Something worth several lines when the pane is narrow.")
	}
	dr := newDriver(t, testDeps(f), seedOf(t, f, "PROJ-13"), 120, 16)
	dr.key("ctrl+d", "ctrl+d")
	at := dr.m.pager.YOffset()
	if at == 0 {
		t.Fatal("the pager never scrolled, so this proves nothing")
	}

	dr.send(kernel.SizeMsg{Width: 70, Height: 16})
	if got := dr.m.pager.YOffset(); got != at {
		t.Errorf("the resize moved the pager to line %d, want %d", got, at)
	}
}

func TestIssue_ARefreshRereadsTheIssueAndTheThread(t *testing.T) {
	t.Parallel()

	f := newFake(20)
	dr := newDriver(t, testDeps(f), seedOf(t, f, "PROJ-14"), 120, 40)
	comment(t, f, "PROJ-14", "Added while you were reading.")

	dr.send(kernel.RefreshMsg{})

	mustContain(t, dr.view(), "Added while you were reading.", "Comments (1)")
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

func BenchmarkIssueView(b *testing.B) {
	f := jiratest.New(
		jiratest.WithProject("PROJ", jiratest.Scrum),
		jiratest.WithIssues(jiratest.Gen(20)),
	)
	full, err := f.Issue(b.Context(), "PROJ-12")
	if err != nil {
		b.Fatal(err)
	}
	d := kernel.Deps{
		Caps:  jira.Capabilities{TimeZone: time.UTC},
		Theme: kernel.NewTheme(kernel.ThemeDark, true, kernel.UnicodeGlyphs()),
		Now:   func() time.Time { return time.Date(2025, time.March, 5, 9, 0, 0, 0, time.UTC) },
	}
	view, ok := New(d, full).(*Model)
	if !ok {
		b.Fatal("New did not return a *Model")
	}
	next, _ := view.Update(kernel.SizeMsg{Width: 120, Height: 40})
	m, _ := next.(*Model)
	next, _ = m.Update(loadedMsg{gen: m.gen, issue: full})
	m, _ = next.(*Model)
	next, _ = m.Update(commentsMsg{gen: m.gen})
	m, _ = next.(*Model)
	_ = m.View()

	b.ReportAllocs()
	b.ResetTimer()
	for range b.N {
		_ = m.View()
	}
}
