package kernel

import (
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/charmbracelet/x/ansi"

	"github.com/varijkapil13/saral/internal/app"
	"github.com/varijkapil13/saral/pkg/jira"
)

// capsStore is an app.Cache that keeps a probe answer in memory. The real one is
// bbolt-backed and belongs to internal/app; what the kernel needs from it is the
// two methods of app.CapsCache and nothing else.
type capsStore struct {
	held    map[string]app.CapsSnapshot
	written map[string]jira.Capabilities
	order   []string
	err     error
}

var (
	_ app.Cache     = (*capsStore)(nil)
	_ app.CapsCache = (*capsStore)(nil)
)

func newCapsStore() *capsStore {
	return &capsStore{held: map[string]app.CapsSnapshot{}, written: map[string]jira.Capabilities{}}
}

func (c *capsStore) store(project string, snap app.CapsSnapshot) *capsStore {
	c.held[project] = snap
	return c
}

func (c *capsStore) Caps(project string) (app.CapsSnapshot, bool) {
	snap, ok := c.held[project]
	return snap, ok
}

func (c *capsStore) PutCaps(project string, caps jira.Capabilities) error {
	if c.err != nil {
		return c.err
	}
	c.written[project] = caps
	c.order = append(c.order, project)
	return nil
}

func (c *capsStore) Rows(string) (app.Snapshot, bool)                        { return app.Snapshot{}, false }
func (c *capsStore) PutRows(string, []jira.Issue, bool) error                { return nil }
func (c *capsStore) Forget(string) error                                     { return nil }
func (c *capsStore) EachIssue(func(jira.Issue, time.Time) bool) (int, error) { return 0, nil }
func (c *capsStore) Generation() uint64                                      { return 0 }

// storedAnswer is what a previous run left behind: boards allowed here, plans
// refused with the site's own sentence.
func storedAnswer(at time.Time, stale bool) app.CapsSnapshot {
	return app.CapsSnapshot{
		Caps: jira.Capabilities{
			Boards: jira.Capability{OK: true},
			Plans:  jira.Capability{Reason: "Plans need Administer Jira, which this token does not have"},
		},
		StoredAt: at,
		Stale:    stale,
	}
}

// TestCaps_TheFirstFrameIsDrawnFromTheStoredAnswer is the whole point of keeping
// it: a gated view is on screen and reachable before the probe has answered.
func TestCaps_TheFirstFrameIsDrawnFromTheStoredAnswer(t *testing.T) {
	resetRegistry()
	t.Cleanup(resetRegistry)
	RegisterView(spec("board", 1, jira.CapBoards, &stubView{id: "board"}))

	rec := newProbeRecorder("ONE")
	d := scopedDeps(rec, "ONE")
	d.Cache = newCapsStore().store("ONE", storedAnswer(d.Now().Add(-5*time.Minute), false))

	m := newAt(t, d, 120, 30)
	if m.capsProbed {
		t.Fatal("a stored answer is not a probe, and the kernel says it probed")
	}
	if !m.deps.Caps.Allows(jira.CapBoards) {
		t.Fatal("the stored answer was not installed, so every gated view is hidden")
	}
	frame := ansi.Strip(m.Frame())
	if !strings.Contains(frame, "board body") {
		t.Errorf("the view the stored answer allows did not open:\n%s", frame)
	}
	// Fresh enough that this is the same situation as a second after a live
	// probe, so there is nothing to say about it.
	if strings.Contains(frame, "last checked") {
		t.Errorf("a current stored answer explained itself:\n%s", frame)
	}
}

// TestCaps_AViewIsOpenedByNameOnTheFirstFrame covers what the zero Capabilities
// did quietly: `saral board` fell through to whatever held the first slot,
// because the view it named was gated on an answer nothing had yet.
func TestCaps_AViewIsOpenedByNameOnTheFirstFrame(t *testing.T) {
	resetRegistry()
	t.Cleanup(resetRegistry)
	RegisterView(spec("issues", 1, "", &stubView{id: "issues"}))
	RegisterView(spec("board", 2, jira.CapBoards, &stubView{id: "board"}))

	rec := newProbeRecorder("ONE")
	d := scopedDeps(rec, "ONE")
	d.Cache = newCapsStore().store("ONE", storedAnswer(d.Now(), false))

	m := newAt(t, d, 120, 30, WithInitialView("board"))
	if got := ansi.Strip(m.Frame()); !strings.Contains(got, "board body") {
		t.Errorf("the named view did not open:\n%s", got)
	}
}

// TestCaps_AStoredAnswerIsRevalidatedWhateverItsAge is what makes serving one
// safe. Nothing checks the TTL before asking again.
func TestCaps_AStoredAnswerIsRevalidatedWhateverItsAge(t *testing.T) {
	resetRegistry()
	t.Cleanup(resetRegistry)
	RegisterView(spec("board", 1, jira.CapBoards, &stubView{id: "board"}))

	rec := newProbeRecorder("ONE")
	d := scopedDeps(rec, "ONE")
	d.Cache = newCapsStore().store("ONE", storedAnswer(d.Now(), false))

	m := newAt(t, d, 120, 30)
	probeAnswer(t, m.Init())
	if got := rec.probes(); len(got) != 1 || got[0] != "ONE" {
		t.Errorf("the probes were %v, want one for ONE — a stored answer that is never re-asked is just stale", got)
	}
}

// TestCaps_AStaleStoredAnswerSaysSoUntilTheProbeAnswers holds docs/UX.md
// principle 2 to the one case where the footer can be a run behind: past the TTL
// the row is drawn from an answer old enough to be worth naming.
func TestCaps_AStaleStoredAnswerSaysSoUntilTheProbeAnswers(t *testing.T) {
	resetRegistry()
	t.Cleanup(resetRegistry)
	RegisterView(spec("board", 1, jira.CapBoards, &stubView{id: "board"}))

	rec := newProbeRecorder("ONE")
	d := scopedDeps(rec, "ONE")
	d.Cache = newCapsStore().store("ONE", storedAnswer(d.Now().Add(-50*time.Hour), true))

	m := newAt(t, d, 120, 30)
	if got := ansi.Strip(m.Frame()); !strings.Contains(got, "last checked 2 days ago") {
		t.Errorf("a two-day-old answer is drawn with nothing saying so:\n%s", got)
	}

	m = deliver(t, m, m.Init())
	frame := ansi.Strip(m.Frame())
	if strings.Contains(frame, "last checked") {
		t.Errorf("the notice outlived the answer that settled it:\n%s", frame)
	}
	if !m.capsProbed {
		t.Error("the probe answered and the kernel still says nothing has been checked")
	}
}

// TestCaps_TheProbesOwnAnswerIsKeptForTheNextRun is the write half.
func TestCaps_TheProbesOwnAnswerIsKeptForTheNextRun(t *testing.T) {
	resetRegistry()
	t.Cleanup(resetRegistry)
	RegisterView(spec("board", 1, "", &stubView{id: "board"}))

	rec := newProbeRecorder("ONE")
	held := newCapsStore()
	d := scopedDeps(rec, "ONE")
	d.Cache = held

	m := newAt(t, d, 120, 30)
	deliver(t, m, m.Init())

	kept, ok := held.written["ONE"]
	if !ok {
		t.Fatalf("the probe answered and nothing was kept: %v", held.order)
	}
	if kept.Plans.Reason != answeredFor("ONE") {
		t.Errorf("what was kept says %q, want the answer for the project that was probed", kept.Plans.Reason)
	}
}

// TestCaps_AFailedProbeLeavesTheStoredAnswerStanding is the safety the decision
// rests on: the probe reports a rejected credential, a rate limit and an
// unreachable host as errors rather than as an answer, so a stored positive is
// never replaced by a failure.
func TestCaps_AFailedProbeLeavesTheStoredAnswerStanding(t *testing.T) {
	resetRegistry()
	t.Cleanup(resetRegistry)
	RegisterView(spec("board", 1, jira.CapBoards, &stubView{id: "board"}))

	rec := newProbeRecorder("ONE")
	held := newCapsStore().store("ONE", storedAnswer(time.Time{}, false))
	d := scopedDeps(rec, "ONE")
	d.Cache = held

	m := newAt(t, d, 120, 30)
	next, _ := m.Update(capsFailedMsg{seq: m.capsSeq, err: &jira.AuthError{Reason: "the token has been revoked"}})
	m = next.(Model)

	if !m.deps.Caps.Allows(jira.CapBoards) {
		t.Error("a failed probe took the stored answer away")
	}
	if len(held.written) != 0 {
		t.Errorf("a failed probe wrote %v to disk", held.written)
	}
	frame := ansi.Strip(m.Frame())
	if !strings.Contains(frame, "revoked") {
		t.Errorf("the failure is not on screen:\n%s", frame)
	}
	if !strings.Contains(frame, "board body") {
		t.Errorf("the view the stored answer allows was closed by a failure:\n%s", frame)
	}
}

// TestCaps_AProjectSwitchKeepsTheAnswerUnderTheProjectItIsAbout covers the key
// the entry is stored under: three of these capabilities are per-project, so one
// entry for the whole profile would answer for the wrong project.
func TestCaps_AProjectSwitchKeepsTheAnswerUnderTheProjectItIsAbout(t *testing.T) {
	resetRegistry()
	t.Cleanup(resetRegistry)
	RegisterView(spec("board", 1, "", &stubView{id: "board"}))

	rec := newProbeRecorder("ONE", "TWO")
	held := newCapsStore()
	d := scopedDeps(rec, "ONE")
	d.Cache = held

	m := newAt(t, d, 120, 30)
	m = deliver(t, m, m.Init())
	m, cmd := switchTo(t, m, "TWO")
	deliver(t, m, cmd)

	if got := held.written["ONE"].Plans.Reason; got != answeredFor("ONE") {
		t.Errorf("the entry for ONE says %q", got)
	}
	if got := held.written["TWO"].Plans.Reason; got != answeredFor("TWO") {
		t.Errorf("the entry for TWO says %q", got)
	}
}

// TestCaps_ACacheThatWillNotWriteIsSaidOnceAndNotFatal keeps a cache failure
// where every other one in this program is: on the status line, with the session
// carrying on.
func TestCaps_ACacheThatWillNotWriteIsSaidOnceAndNotFatal(t *testing.T) {
	resetRegistry()
	t.Cleanup(resetRegistry)
	RegisterView(spec("board", 1, "", &stubView{id: "board"}))

	rec := newProbeRecorder("ONE")
	held := newCapsStore()
	held.err = errors.New("the cache file is read-only")
	d := scopedDeps(rec, "ONE")
	d.Cache = held

	m := newAt(t, d, 120, 30)
	m = deliver(t, m, m.Init())

	if !m.capsProbed {
		t.Fatal("a cache that would not write cost the session its probe answer")
	}
	if got := ansi.Strip(m.Frame()); !strings.Contains(got, "read-only") {
		t.Errorf("the write failure was swallowed:\n%s", got)
	}
}

// TestCaps_ASessionWithNoCacheStillDraws is the ordinary first run: nothing on
// disk, nowhere to put anything, and the same program.
func TestCaps_ASessionWithNoCacheStillDraws(t *testing.T) {
	resetRegistry()
	t.Cleanup(resetRegistry)
	RegisterView(spec("board", 1, jira.CapBoards, &stubView{id: "board"}))

	rec := newProbeRecorder("ONE")
	m := newAt(t, scopedDeps(rec, "ONE"), 120, 30)
	if m.capsStored {
		t.Fatal("a session with no cache claims a stored answer")
	}
	m, _ = press(m, "g", "1")
	if got := ansi.Strip(m.Frame()); !strings.Contains(got, "nothing has been checked on this site yet") {
		t.Errorf("a session that has asked nothing does not say so:\n%s", got)
	}
	m = deliver(t, m, m.Init())
	if got := ansi.Strip(m.Frame()); !strings.Contains(got, "board body") {
		t.Errorf("the probe answered and the view it allows did not open:\n%s", got)
	}
}
