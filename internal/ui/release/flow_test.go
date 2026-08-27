package release

import (
	"context"
	"errors"
	"strconv"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	zone "github.com/lrstanley/bubblezone/v2"

	"github.com/varijkapil13/saral/internal/ui/kernel"
	"github.com/varijkapil13/saral/pkg/jira"
	"github.com/varijkapil13/saral/pkg/jira/jiratest"
)

// twoOhVersion and threeOhVersion are the fake's own versions as a release
// screen is handed them.
func twoOhVersion() jira.Version {
	return jira.Version{ID: twoOh, Name: "2.0", Description: "The one being worked on"}
}

func threeOhVersion() jira.Version {
	return jira.Version{ID: threeOh, Name: "3.0", Description: "Not planned yet"}
}

func openFlow(t *testing.T, client jira.SessionClient, open int, targets ...jira.Version) *driver {
	t.Helper()
	return flowOf(t, testDeps(client), twoOhVersion(), open, targets, 100, 20)
}

func TestFlow_OpensOnTheDecisionAndSaysWhatIsOpen(t *testing.T) {
	t.Parallel()

	dr := openFlow(t, newFake(4), 12, threeOhVersion())
	if got := dr.flow().state; got != flowChoosing {
		t.Fatalf("the flow opened in state %d, want the choice", got)
	}
	mustContain(t, dr.view(),
		"12 issues are still open on 2.0.",
		"Release 2.0 anyway",
		"Move them to another version",
		"Take 2.0 off the open issues",
	)
}

// Nothing open is nothing to decide about, and it is still not nothing to
// confirm.
func TestFlow_AVersionWithNothingOpenStillGetsAConfirm(t *testing.T) {
	t.Parallel()

	dr := openFlow(t, newFake(4), 0)
	if got := dr.flow().state; got != flowConfirming {
		t.Fatalf("a version with nothing open opened in state %d, want the confirm", got)
	}
	mustContain(t, dr.view(), "Nothing is open on 2.0.", "Release 2.0?", confirmHint)
}

// The confirm is the only way into the write. Every stroke the flow answers is
// pressed on both list screens, and none of them may reach the site.
func TestFlow_NothingReachesTheWriteWithoutTheConfirm(t *testing.T) {
	t.Parallel()

	strokes := []string{"enter", "y", "j", "k", "up", "down", "Y", "n", "e", "A", "tab", "ctrl+s"}
	for name, reach := range map[string]func(*driver){
		"on the choice":            func(*driver) {},
		"having chosen to move":    func(dr *driver) { dr.key("j", "enter") },
		"back on the choice again": func(dr *driver) { dr.key("j", "enter") },
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			fake := newFake(4)
			watch := watching(fake)
			dr := openFlow(t, watch, 7, threeOhVersion())
			reach(dr)
			if dr.flow().state == flowConfirming {
				t.Fatal("getting to this screen already reached the confirm, so this proves nothing")
			}
			for _, stroke := range strokes {
				if dr.flow().state == flowConfirming {
					break
				}
				dr.key(stroke)
			}
			if got := watch.released(); len(got) != 0 {
				t.Errorf("the site was asked to release the version without a confirm: %+v", got)
			}
		})
	}
}

// Each choice sends the policy it named, and a move sends the version it was
// pointed at rather than the one being released.
func TestFlow_EachChoiceSendsThePolicyItNamed(t *testing.T) {
	t.Parallel()

	for name, tc := range map[string]struct {
		reach  func(*driver)
		policy jira.UnresolvedPolicy
		target string
	}{
		"release it anyway": {
			reach:  func(dr *driver) { dr.key("enter") },
			policy: jira.ReleaseAnyway,
		},
		"move the open issues": {
			reach:  func(dr *driver) { dr.key("j", "enter", "enter") },
			policy: jira.MoveUnresolved,
			target: threeOh,
		},
		"strip the version off them": {
			reach:  func(dr *driver) { dr.key("j", "j", "enter") },
			policy: jira.StripUnresolved,
		},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			watch := watching(newFake(0, jiratest.WithIssues(openOn(twoOh, 3))))
			dr := openFlow(t, watch, 3, threeOhVersion())
			tc.reach(dr)

			if got := dr.flow().state; got != flowConfirming {
				t.Fatalf("choosing left the flow in state %d, want the confirm", got)
			}
			dr.key("y")

			sent := watch.released()
			if len(sent) != 1 {
				t.Fatalf("the site was asked to release %d times, want once", len(sent))
			}
			if sent[0].id != twoOh {
				t.Errorf("it released %q, want the version the screen was opened over", sent[0].id)
			}
			if sent[0].in.Unresolved != tc.policy {
				t.Errorf("it sent policy %d, want %d", sent[0].in.Unresolved, tc.policy)
			}
			if sent[0].in.MoveToVersionID != tc.target {
				t.Errorf("it sent MoveToVersionID %q, want %q", sent[0].in.MoveToVersionID, tc.target)
			}
			if dr.pops != 1 {
				t.Errorf("a release closed the screen %d times, want once", dr.pops)
			}
			if _, told := dr.released(); !told {
				t.Error("nothing was told the version had been released, so the list still shows the old row")
			}
		})
	}
}

// A choice this project cannot offer stays on the list with the reason beside
// it: a row that disappeared would be one nobody could find out about.
func TestFlow_MovingIsRefusedWhenThereIsNowhereToMoveTo(t *testing.T) {
	t.Parallel()

	dr := openFlow(t, newFake(4), 4)
	mustContain(t, dr.view(), "no other unreleased version to move them to")

	dr.key("j", "enter")
	if got := dr.flow().state; got != flowChoosing {
		t.Errorf("a refused choice moved the flow to state %d", got)
	}
	mustContain(t, dr.lastStatus().Text, "no other unreleased version")
}

func TestFlow_TheConfirmSaysWhatWillHappenToTheOpenIssues(t *testing.T) {
	t.Parallel()

	for name, tc := range map[string]struct {
		reach func(*driver)
		want  []string
	}{
		"release anyway": {
			reach: func(dr *driver) { dr.key("enter") },
			want:  []string{"9 open issues will stay on 2.0."},
		},
		"move them": {
			reach: func(dr *driver) { dr.key("j", "enter", "enter") },
			want:  []string{"9 open issues will move from 2.0 to 3.0."},
		},
		"strip them": {
			reach: func(dr *driver) { dr.key("j", "j", "enter") },
			want:  []string{"2.0 will come off 9 open issues."},
		},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			dr := openFlow(t, newFake(4), 9, threeOhVersion())
			tc.reach(dr)
			mustContain(t, dr.view(), append(tc.want,
				"2.0 will be marked released, dated today.",
				"cannot be undone",
				confirmHint)...)
		})
	}
}

// The port hands back what the release left open on the version. A move or a
// strip that did not reach every issue comes back released with a count still on
// it, and that is a sweep that stopped part way rather than a release to report.
func TestFlow_ASweepThatDidNotFinishIsReportedRatherThanCalledARelease(t *testing.T) {
	t.Parallel()

	client := &partial{Fake: newFake(0, jiratest.WithIssues(openOn(twoOh, 8))), left: 3}
	dr := openFlow(t, client, 8, threeOhVersion())
	dr.key("j", "enter", "enter", "y")

	status := dr.lastStatus()
	if status.Level != kernel.LevelWarn {
		t.Errorf("a half-finished move was reported at level %d, want a warning", status.Level)
	}
	mustContain(t, status.Text, "2.0 was released, but 3 of the 8 open issues still carry it",
		"the move did not finish")
	if dr.pops != 1 {
		t.Errorf("the screen closed %d times over a half-finished move, want once", dr.pops)
	}
}

// A release anyway leaves the open issues where they are on purpose, so the
// count coming back is the answer rather than a sweep that stopped.
func TestFlow_ReleasingAnywayReportsTheIssuesLeftOnItWithoutCallingItAFailure(t *testing.T) {
	t.Parallel()

	client := &partial{Fake: newFake(0, jiratest.WithIssues(openOn(twoOh, 8))), left: 8}
	dr := openFlow(t, client, 8)
	dr.key("enter", "y")

	status := dr.lastStatus()
	if status.Level != kernel.LevelInfo {
		t.Errorf("releasing anyway was reported at level %d, want plain information", status.Level)
	}
	mustContain(t, status.Text, "2.0 released.", "8 open issues left on it.")
}

// An answer that does not say the version is released is not read as one. The
// port flips released last, so an answer without it is a release that may not
// have happened.
func TestFlow_AnAnswerThatDoesNotSayReleasedIsNotTreatedAsOne(t *testing.T) {
	t.Parallel()

	client := &unreleased{Fake: newFake(4)}
	dr := openFlow(t, client, 0)
	dr.key("y")

	if got := dr.flow().state; got != flowStuck {
		t.Errorf("the flow is in state %d after an answer that never said released", got)
	}
	if dr.pops != 0 {
		t.Error("the screen closed over an answer that never said the version was released")
	}
	mustContain(t, dr.view(), "2.0 was not released", "may not be", againHint)
}

func TestFlow_ARefusedReleaseKeepsTheSitesOwnWords(t *testing.T) {
	t.Parallel()

	for name, tc := range map[string]struct {
		err  error
		want string
	}{
		"a token that may not release": {
			err:  &jira.CapabilityError{Reason: "you need Administer Projects on PROJ"},
			want: "you need Administer Projects on PROJ",
		},
		"a site that is rate limiting": {
			err:  &jira.RateLimitError{},
			want: "rate limited by Jira",
		},
		"a transport that failed": {
			err:  &jira.TransportError{Op: "PUT /rest/api/3/version/10001", Status: 503},
			want: "failed with HTTP 503",
		},
		// A release refused for permissions arrives as a 400 with no field on it,
		// so a screen that draws only field errors says nothing at all.
		"a permission refused as a bare validation message": {
			err: &jira.ValidationError{Messages: []string{
				"You do not have permission to edit this version.",
			}},
			want: "You do not have permission to edit this version.",
		},
		"a sweep that stopped part way": {
			err: errors.New("cloud: the fix version was moved on 4 of 9 open issues, and then " +
				"PROJ-8 failed, so version 10001 was not released: nope"),
			want: "moved on 4 of 9 open issues",
		},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			fake := newFake(4)
			fake.FailNext(tc.err)
			dr := openFlow(t, fake, 0)
			dr.key("y")

			if got := dr.flow().state; got != flowStuck {
				t.Fatalf("a refused release left the flow in state %d", got)
			}
			mustContain(t, dr.view(), tc.want, againHint)
			if got := dr.lastStatus().Text; !strings.Contains(got, tc.want) {
				t.Errorf("the status line says %q, want the site's own words", got)
			}
			if dr.pops != 0 {
				t.Error("the screen closed over a release that did not happen")
			}
		})
	}
}

// Starting again goes back to the decision and asks the site for nothing.
func TestFlow_StartingAgainGoesBackToTheDecisionAndWritesNothing(t *testing.T) {
	t.Parallel()

	fake := newFake(4)
	fake.FailNext(&jira.RateLimitError{})
	watch := watching(fake)
	dr := openFlow(t, watch, 5, threeOhVersion())
	dr.key("j", "enter", "enter", "y")
	if got := dr.flow().state; got != flowStuck {
		t.Fatalf("the release was not refused; the flow is in state %d", got)
	}

	dr.key("enter")

	if got := dr.flow().state; got != flowChoosing {
		t.Errorf("starting again left the flow in state %d, want the choice", got)
	}
	if got := dr.flow().policy; got != jira.ReleaseAnyway {
		t.Errorf("starting again kept policy %d from the attempt that failed", got)
	}
	if got := len(watch.released()); got != 1 {
		t.Errorf("starting again asked the site to release %d times in all", got)
	}
}

// Being discarded lets go of a release still out with the site, so the answer is
// not drawn into a screen the stack has thrown away.
func TestFlow_BeingDiscardedLetsGoOfTheRelease(t *testing.T) {
	t.Parallel()

	dr := openFlow(t, newFake(4), 0)
	view, cmd := dr.m.Update(keyPress("y"))
	f, ok := view.(*Flow)
	if !ok {
		t.Fatalf("Update returned a %T", view)
	}
	if f.state != flowWorking {
		t.Fatalf("y left the flow in state %d, want the write in flight", f.state)
	}

	f.Close()
	msg := answer(cmd)
	failed, ok := msg.(failedMsg)
	if !ok {
		t.Fatalf("the release came back as %T after the screen was discarded", msg)
	}
	if !errors.Is(failed.err, context.Canceled) {
		t.Errorf("the release came back with %v, want the cancelled context", failed.err)
	}
}

func TestFlow_FitsTheBoxItIsGiven(t *testing.T) {
	t.Parallel()

	for _, size := range []struct{ w, h int }{{80, 20}, {100, 28}, {120, 30}, {200, 60}} {
		for name, reach := range map[string]func(*driver){
			"choosing":   func(*driver) {},
			"picking":    func(dr *driver) { dr.key("j", "enter") },
			"confirming": func(dr *driver) { dr.key("enter") },
			"working": func(dr *driver) {
				dr.flow().state = flowWorking
			},
			"stuck": func(dr *driver) {
				f := dr.flow()
				f.state, f.failure = flowStuck, errors.New(strings.Repeat("a long reason ", 40))
			},
		} {
			dr := flowOf(t, testDeps(newFake(4)), twoOhVersion(), 6,
				[]jira.Version{threeOhVersion()}, size.w, size.h)
			reach(dr)
			lines := strings.Split(dr.view(), "\n")
			if len(lines) != size.h {
				t.Errorf("%dx%d %s: %d lines, want %d", size.w, size.h, name, len(lines), size.h)
			}
			for i, line := range lines {
				if got := len([]rune(line)); got > size.w {
					t.Errorf("%dx%d %s: line %d is %d columns wide", size.w, size.h, name, i, got)
				}
			}
		}
	}
}

func TestFlow_ClickingAChoiceSelectsItAndClickingItAgainTakesIt(t *testing.T) {
	t.Parallel()

	d := testDeps(newFake(4))
	dr := flowOf(t, d, twoOhVersion(), 6, []jira.Version{threeOhVersion()}, 120, 20)

	pressOn(t, d, dr, "choice:"+strconv.Itoa(int(jira.StripUnresolved)))
	if dr.flow().state != flowChoosing {
		t.Fatal("one click took the choice; it should only have selected it")
	}
	pressOn(t, d, dr, "choice:"+strconv.Itoa(int(jira.StripUnresolved)))
	if got := dr.flow().state; got != flowConfirming {
		t.Fatalf("a second click left the flow in state %d", got)
	}
	if got := dr.flow().policy; got != jira.StripUnresolved {
		t.Errorf("the click chose policy %d", got)
	}
}

// The confirm takes one click, because it is already the second screen: the row
// that led here was the first.
func TestFlow_ClickingTheConfirmReleasesIt(t *testing.T) {
	t.Parallel()

	d := testDeps(newFake(4))
	watch := watching(newFake(4))
	d.Jira = watch
	dr := flowOf(t, d, twoOhVersion(), 0, nil, 120, 20)

	pressOn(t, d, dr, zoneConfirm)
	if got := len(watch.released()); got != 1 {
		t.Errorf("clicking the confirm asked the site to release %d times", got)
	}
}

func TestFlow_WithTheMouseOffTheFrameCarriesNoMarker(t *testing.T) {
	t.Parallel()

	off := zone.New()
	t.Cleanup(off.Close)
	off.SetEnabled(false)

	d := plainDeps(newFake(4))
	d.Zones = off
	dr := flowOf(t, d, twoOhVersion(), 6, []jira.Version{threeOhVersion()}, 120, 20)

	frame := dr.m.View()
	if strings.ContainsRune(frame, '\x1b') {
		t.Errorf("an escape survived a frame drawn with the mouse off:\n%q", frame)
	}
	if !strings.Contains(frame, "Release 2.0") {
		t.Fatalf("the flow did not draw at all:\n%q", frame)
	}
}

// answer is what the kernel hands a view: the command's own reply with the
// envelope the kernel addresses it by taken off.
func answer(cmd tea.Cmd) tea.Msg {
	msg := cmd()
	if reply, addressed := msg.(kernel.ReplyMsg); addressed {
		return reply.Msg
	}
	return msg
}

// partial is a site whose release leaves issues on the version. The fake's own
// sweep always clears them, and the real adapter's does not have to: it reports
// what it could not reach, and this is the answer that shape produces.
type partial struct {
	*jiratest.Fake
	left int
}

func (p *partial) ReleaseVersion(ctx context.Context, id string, in jira.ReleaseInput) (jira.Version, error) {
	v, err := p.Fake.ReleaseVersion(ctx, id, in)
	if err != nil {
		return v, err
	}
	left := p.left
	v.Unresolved = &left
	return v, nil
}

// unreleased is a site that answers a release without saying the version is
// released.
type unreleased struct {
	*jiratest.Fake
}

func (u *unreleased) ReleaseVersion(ctx context.Context, id string, in jira.ReleaseInput) (jira.Version, error) {
	v, err := u.Fake.ReleaseVersion(ctx, id, in)
	if err != nil {
		return v, err
	}
	v.Released = false
	return v, nil
}
