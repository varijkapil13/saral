package release

import (
	"context"
	"errors"
	"slices"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/varijkapil13/saral/internal/ui/kernel"
	"github.com/varijkapil13/saral/pkg/jira"
	"github.com/varijkapil13/saral/pkg/jira/jiratest"
)

// refusingEdits is the fake with the edits of some issues refused, which is
// the only way to have a run in which some writes land and some do not.
type refusingEdits struct {
	*jiratest.Fake
	mu     sync.Mutex
	refuse map[string]error
	all    error
	sent   []jira.IssuePatch
}

func (r *refusingEdits) UpdateIssue(ctx context.Context, key string, in jira.IssuePatch) error {
	r.mu.Lock()
	r.sent = append(r.sent, in)
	err, all := r.refuse[key], r.all
	r.mu.Unlock()
	switch {
	case all != nil:
		return all
	case err != nil:
		return err
	}
	return r.Fake.UpdateIssue(ctx, key, in)
}

func bulkOf(t *testing.T, d kernel.Deps, v jira.Version, w, h int) *driver {
	t.Helper()
	return newDriver(t, NewBulk(d, v), w, h)
}

func (d *driver) bulk() *Bulk {
	d.t.Helper()
	b, ok := d.m.(*Bulk)
	if !ok {
		d.t.Fatalf("the view under test is a %T, not the assignment screen", d.m)
	}
	return b
}

func carrying(t *testing.T, f *jiratest.Fake, versionID string) []string {
	t.Helper()
	page, err := f.Search(t.Context(), jira.Query{JQL: `project = "PROJ"`, Fields: []string{"fixVersions"}, MaxResults: 1000})
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	var out []string
	for {
		for _, iss := range page.Items {
			if slices.ContainsFunc(iss.FixVersions, func(v jira.Version) bool { return v.ID == versionID }) {
				out = append(out, iss.Key)
			}
		}
		if !page.HasMore() {
			return out
		}
		if page, err = page.Next(t.Context()); err != nil {
			t.Fatalf("Next: %v", err)
		}
	}
}

func versionByID(t *testing.T, id string) jira.Version {
	t.Helper()
	for _, v := range seededVersions {
		if v.ID == id {
			return v
		}
	}
	t.Fatalf("no seeded version %s", id)
	return jira.Version{}
}

func TestBulk_PutsTheVersionOnWhatTheQueryMatchesAndLeavesTheRestOfTheirVersions(t *testing.T) {
	t.Parallel()

	f := newFake(12)
	had := carrying(t, f, twoOh)
	oneOhHolders := carrying(t, f, oneOh)
	if len(had) == 0 || len(had) == 12 || len(oneOhHolders) == 0 {
		t.Fatalf("the seed has %d of 12 on 2.0 and %d on 1.0, so this proves nothing", len(had), len(oneOhHolders))
	}
	dr := bulkOf(t, testDeps(f), versionByID(t, twoOh), 100, 24)
	dr.key("enter")

	b := dr.bulk()
	if b.state != bulkPreview || len(b.todo) != 12-len(had) || b.skipped != len(had) {
		t.Fatalf("the preview holds %d to change and %d left alone in state %d, want %d and %d",
			len(b.todo), b.skipped, b.state, 12-len(had), len(had))
	}
	if n := countCalls(f, "UpdateIssue"); n != 0 {
		t.Fatalf("the preview wrote %d times before anybody said y", n)
	}
	mustContain(t, dr.view(), "already carry 2.0", "y puts it on")

	dr.key("y")
	if got := carrying(t, f, twoOh); len(got) != 12 {
		t.Errorf("%d of 12 carry 2.0 after the run", len(got))
	}
	if got := carrying(t, f, oneOh); !slices.Equal(got, oneOhHolders) {
		t.Errorf("1.0 is on %v after putting 2.0 on, want it left on %v", got, oneOhHolders)
	}
	if n := countCalls(f, "UpdateIssue"); n != 12-len(had) {
		t.Errorf("the run wrote %d times, want one per issue that changes (%d)", n, 12-len(had))
	}
	if dr.bulk().state != bulkDone {
		t.Errorf("the screen is in state %d after the run", dr.bulk().state)
	}
	mustContain(t, dr.lastStatus().Text, "2.0 is on 9 of 9 issues")
}

func TestBulk_TakesTheVersionOffWhenSwitched(t *testing.T) {
	t.Parallel()

	f := newFake(12)
	had := carrying(t, f, twoOh)
	dr := bulkOf(t, testDeps(f), versionByID(t, twoOh), 100, 24)
	dr.key("tab")
	b := dr.bulk()
	if !b.remove || b.input.Value() != "fixVersion = "+twoOh {
		t.Fatalf("tab left remove=%t and the query %q", b.remove, b.input.Value())
	}
	dr.key("enter")
	if got := len(dr.bulk().todo); got != len(had) {
		t.Fatalf("the preview takes it off %d issues, want the %d that carry it", got, len(had))
	}
	dr.key("y")
	if got := carrying(t, f, twoOh); len(got) != 0 {
		t.Errorf("%v still carry 2.0", got)
	}
}

func TestBulk_AQueryTheReaderWroteSurvivesTheSwitch(t *testing.T) {
	t.Parallel()

	dr := bulkOf(t, testDeps(newFake(4)), versionByID(t, twoOh), 100, 24)
	b := dr.bulk()
	b.input.SetValue("")
	dr.typeText(`key in (PROJ-1, PROJ-2)`)
	dr.key("tab")
	if got := dr.bulk().input.Value(); got != `key in (PROJ-1, PROJ-2)` {
		t.Errorf("the switch replaced a written query with %q", got)
	}
}

// Each issue's refusal is its own: the rest of the run still goes, and the
// screen lists every refused issue with the site's reason.
func TestBulk_ARefusedIssueIsListedWithItsReasonAndTheRestStillGo(t *testing.T) {
	t.Parallel()

	for name, err := range map[string]error{
		"a 403":               &jira.CapabilityError{Capability: jira.CapBoards, Reason: "needs the Edit Issues permission"},
		"a 429":               &jira.RateLimitError{RetryAfter: time.Minute},
		"a transport failure": &jira.TransportError{Op: "PUT", Err: errors.New("connection reset")},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			f := newFake(12)
			r := &refusingEdits{Fake: f, refuse: map[string]error{"PROJ-2": err, "PROJ-3": err}}
			dr := bulkOf(t, testDeps(r), versionByID(t, twoOh), 100, 24)
			dr.key("enter", "y")

			b := dr.bulk()
			if len(b.failed) != 2 || b.done != len(b.todo)-2 {
				t.Fatalf("done=%d failed=%v of %d, want every issue but the two refused", b.done, b.failed, len(b.todo))
			}
			reason, _ := jira.Reason(err)
			frame := dr.view()
			mustContain(t, frame, "PROJ-2", "PROJ-3", "the site refused 2")
			mustContain(t, frame, reason[:min(len(reason), 20)])
			if st := dr.lastStatus(); st.Level != kernel.LevelWarn {
				t.Errorf("a run with refusals ended on %q at level %v, want a warning", st.Text, st.Level)
			}
		})
	}
}

// A chunk in which nothing landed stops the run, and the issues after it are
// reported as not sent rather than as refused.
func TestBulk_AChunkThatLandsNothingStopsTheRun(t *testing.T) {
	t.Parallel()

	f := newFake(80)
	r := &refusingEdits{Fake: f, all: &jira.RateLimitError{RetryAfter: time.Minute}}
	dr := bulkOf(t, testDeps(r), versionByID(t, twoOh), 100, 24)
	dr.key("enter")
	todo := len(dr.bulk().todo)
	if todo <= bulkChunk {
		t.Fatalf("%d issues is one chunk, so this proves nothing", todo)
	}
	dr.key("y")
	b := dr.bulk()
	if len(b.failed) != bulkChunk || len(b.pending) != todo-bulkChunk {
		t.Errorf("failed=%d pending=%d of %d, want one chunk refused and the rest not sent", len(b.failed), len(b.pending), todo)
	}
	r.mu.Lock()
	sent := len(r.sent)
	r.mu.Unlock()
	if sent != bulkChunk {
		t.Errorf("%d edits reached the site after a chunk that landed nothing, want %d", sent, bulkChunk)
	}
	mustContain(t, dr.lastStatus().Text, strconv.Itoa(todo-bulkChunk)+" were not sent")
}

func TestBulk_TheWriteIsAnAddOrARemoveAndNeverTheWholeList(t *testing.T) {
	t.Parallel()

	f := newFake(6)
	r := &refusingEdits{Fake: f}
	dr := bulkOf(t, testDeps(r), versionByID(t, twoOh), 100, 24)
	dr.key("enter", "y")
	r.mu.Lock()
	defer r.mu.Unlock()
	if len(r.sent) == 0 {
		t.Fatal("nothing was sent")
	}
	for _, p := range r.sent {
		if !slices.Equal(p.AddFixVersions, []string{twoOh}) || p.Fields.Len() != 0 || len(p.Clear) != 0 {
			t.Errorf("sent %+v, want an add of %s and nothing else", p, twoOh)
		}
	}
}

func TestBulk_AQueryTheSiteRefusesIsSaidOnTheQuery(t *testing.T) {
	t.Parallel()

	for name, err := range map[string]error{
		"a bad query": &jira.ValidationError{Messages: []string{"The value 'NOPE' does not exist for the field 'project'."}},
		"a 429":       &jira.RateLimitError{RetryAfter: time.Minute},
		"a transport": &jira.TransportError{Op: "POST", Err: errors.New("connection reset")},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			f := newFake(4)
			f.FailNext(err)
			dr := bulkOf(t, testDeps(f), versionByID(t, twoOh), 100, 24)
			dr.key("enter")
			if dr.bulk().state != bulkQuery {
				t.Fatalf("a refused query left the screen in state %d", dr.bulk().state)
			}
			mustContain(t, dr.view(), whatQuery)
			if n := countCalls(f, "UpdateIssue"); n != 0 {
				t.Errorf("a refused query wrote %d times", n)
			}
		})
	}
}

func TestBulk_AQueryMatchingMoreThanOneRunWritesIsRefused(t *testing.T) {
	t.Parallel()

	f := newFake(bulkCap + 1)
	dr := bulkOf(t, testDeps(f), versionByID(t, threeOh), 100, 24)
	dr.key("enter")
	b := dr.bulk()
	if b.state != bulkQuery || !errors.Is(b.failure, errTooMany) {
		t.Fatalf("state %d failure %v, want the query refused as too wide", b.state, b.failure)
	}
	mustContain(t, dr.view(), "narrow it")
}

func TestBulk_ARunInFlightRefusesToBeThrownAway(t *testing.T) {
	t.Parallel()

	dr := bulkOf(t, testDeps(newFake(4)), versionByID(t, twoOh), 100, 24)
	b := dr.bulk()
	if _, blocked := b.BlocksClose(); blocked {
		t.Error("an idle screen blocks closing")
	}
	b.state, b.todo = bulkWorking, make([]jira.Issue, 40)
	b.done = 10
	reason, blocked := b.BlocksClose()
	if !blocked {
		t.Fatal("a run in flight let itself be thrown away")
	}
	mustContain(t, reason, "10 of 40")
	if set, _ := b.LiveKeys(); len(set.Acts) != 0 {
		t.Errorf("a run in flight advertises %v", set.Acts)
	}
}

func TestBulk_EscLeavesFromTheQueryAndClaimsTheKeysOnlyThere(t *testing.T) {
	t.Parallel()

	dr := bulkOf(t, testDeps(newFake(4)), versionByID(t, twoOh), 100, 24)
	if !dr.bulk().WantsRawKeys() {
		t.Error("the query is not taking typing")
	}
	dr.typeText("q1")
	if got := dr.bulk().input.Value(); got[len(got)-2:] != "q1" {
		t.Errorf("typing reached the query as %q", got)
	}
	dr.key("esc")
	if dr.pops != 1 {
		t.Errorf("esc from the query popped %d times, want once", dr.pops)
	}
	dr.bulk().state = bulkPreview
	if dr.bulk().WantsRawKeys() {
		t.Error("the preview claims the keys, so the kernel's esc never reaches it")
	}
}

func TestBulk_ClickingTheAnswerRunsIt(t *testing.T) {
	t.Parallel()

	f := newFake(6)
	d := testDeps(f)
	dr := bulkOf(t, d, versionByID(t, twoOh), 100, 24)
	pressOn(t, d, dr, bulkZoneToggle)
	if !dr.bulk().remove {
		t.Fatal("clicking the switch did not switch")
	}
	pressOn(t, d, dr, bulkZoneToggle)
	pressOn(t, d, dr, bulkZoneRun)
	if dr.bulk().state != bulkPreview {
		t.Fatalf("clicking the run left state %d", dr.bulk().state)
	}
	pressOn(t, d, dr, zoneConfirm)
	if countCalls(f, "UpdateIssue") == 0 {
		t.Error("clicking the answer wrote nothing")
	}
}

func TestList_AssignOpensTheScreenOverTheVersionUnderTheCursor(t *testing.T) {
	t.Parallel()

	dr := listOf(t, testDeps(newFake(4)), 100, 20)
	dr.moveTo(twoOh)
	dr.key("b")
	push, ok := dr.pushed()
	if !ok || push.ID != BulkViewID {
		t.Fatalf("b pushed %+v, want the assignment screen", push)
	}
	b, isBulk := push.View.(*Bulk)
	if !isBulk || b.version.ID != twoOh {
		t.Errorf("the screen opened over %+v", push.View)
	}

	dr.list().versions[dr.list().cursor].Archived = true
	dr.key("b")
	if st := dr.lastStatus(); st.Level != kernel.LevelWarn {
		t.Errorf("an archived version opened the screen; status %q", st.Text)
	}
}

func TestBulk_Golden(t *testing.T) {
	t.Parallel()

	for name, tc := range map[string]struct {
		build  func(t *testing.T) *driver
		golden string
	}{
		"the query": {golden: "bulk_query_100x16.golden", build: func(t *testing.T) *driver {
			return bulkOf(t, testDeps(newFake(12)), versionByID(t, twoOh), 100, 16)
		}},
		"a refused query": {golden: "bulk_refused_100x16.golden", build: func(t *testing.T) *driver {
			f := newFake(12)
			f.FailNext(&jira.ValidationError{Messages: []string{"Field 'nope' does not exist or you do not have permission to view it."}})
			dr := bulkOf(t, testDeps(f), versionByID(t, twoOh), 100, 16)
			dr.key("enter")
			return dr
		}},
		"the preview": {golden: "bulk_preview_100x16.golden", build: func(t *testing.T) *driver {
			dr := bulkOf(t, testDeps(newFake(12)), versionByID(t, twoOh), 100, 16)
			dr.key("enter")
			return dr
		}},
		"a run with refusals": {golden: "bulk_done_100x16.golden", build: func(t *testing.T) *driver {
			f := newFake(12)
			r := &refusingEdits{Fake: f, refuse: map[string]error{
				"PROJ-2": &jira.CapabilityError{Capability: jira.CapBoards, Reason: "needs the Edit Issues permission"},
			}}
			dr := bulkOf(t, testDeps(r), versionByID(t, twoOh), 100, 16)
			dr.key("enter", "y")
			return dr
		}},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			golden(t, tc.golden, tc.build(t).view())
		})
	}
}

// Turning the mouse off mid-session drops every memoized line with a marker in
// it, on the list, the flow and the assignment screen alike.
func TestReleases_TurningTheMouseOffDropsTheMarkedLines(t *testing.T) {
	t.Parallel()

	for name, build := range map[string]func(d kernel.Deps) kernel.View{
		"the list": New,
		"the flow": func(d kernel.Deps) kernel.View {
			return NewFlow(d, versionByID(t, twoOh), 2, []jira.Version{versionByID(t, threeOh)})
		},
		"the assignment preview": func(d kernel.Deps) kernel.View { return NewBulk(d, versionByID(t, twoOh)) },
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			d := plainDeps(newFake(12))
			mgr := d.Zones
			t.Cleanup(mgr.Close)
			dr := newDriver(t, build(d), 100, 20)
			if _, ok := dr.m.(*Bulk); ok {
				dr.key("enter")
			}
			if !strings.ContainsRune(dr.m.View(), '\x1b') {
				t.Fatal("the mouse-on frame carries no marker, so this proves nothing")
			}
			mgr.SetEnabled(false)
			dr.send(kernel.SetMouseMsg{Enabled: false})
			if frame := dr.m.View(); strings.ContainsRune(frame, '\x1b') {
				t.Errorf("a marker survived turning the mouse off:\n%q", frame)
			}
		})
	}
}
