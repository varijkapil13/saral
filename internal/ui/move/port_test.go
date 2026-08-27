package move

import (
	"context"
	"testing"
	"time"

	"github.com/varijkapil13/saral/pkg/jira"
	"github.com/varijkapil13/saral/pkg/jira/jiratest"
)

// watcher records the TaskRef the wizard polls with. It exists because the fake
// looks a task up by ref.ID and never reads ref.URL, so a view that kept the id
// and threw the endpoint away would be green against the fake for its whole
// life — and a bulk move is followed on its own queue, whose body does not decode
// as the generic task registry's.
type watcher struct {
	jira.SessionClient
	polled []jira.TaskRef
}

func (w *watcher) Task(ctx context.Context, ref jira.TaskRef) (jira.TaskStatus, error) {
	w.polled = append(w.polled, ref)
	return w.SessionClient.Task(ctx, ref)
}

func TestMove_PollsWithTheWholeRefAndNotJustTheId(t *testing.T) {
	t.Parallel()
	f, iss := twoProjects(t)
	spy := &watcher{SessionClient: f}
	w := &immediate{}
	dr := newDriver(t, testDeps(spy), 100, 24, WithIssues(iss), withWaiter(w.wait))
	dr.walkTo("OTHER")
	dr.key("y")

	if len(spy.polled) == 0 {
		t.Fatal("the queue was never asked about the task")
	}
	for i, ref := range spy.polled {
		if ref.URL == "" {
			t.Errorf("poll %d passed a ref with no endpoint on it; the submit answers a bare id and the "+
				"progress endpoint is the client's to keep", i)
		}
		if ref.ID != dr.m.ref.ID || ref.URL != dr.m.ref.URL {
			t.Errorf("poll %d used %+v rather than the ref the submit answered with, %+v", i, ref, dr.m.ref)
		}
	}
}

// The queue keys its failures by numeric issue id while a fixture keys them by
// issue key, so the outcome has to recognise both and fall back to what came
// back rather than drawing nothing.
func TestMove_NamesTheIssuesAQueueFailedByIdOrByKey(t *testing.T) {
	t.Parallel()
	for name, failed := range map[string][]string{
		"keyed by issue key": {"PROJ-2"},
		"keyed by issue id":  {"20002"},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			f, iss := twoProjects(t)
			w := &immediate{}
			dr := newDriver(t, testDeps(f), 100, 24, WithIssues(iss), withWaiter(w.wait))
			dr.walkTo("OTHER")
			dr.running()
			dr.send(taskMsg{gen: dr.m.gen, status: jira.TaskStatus{
				State: jira.TaskComplete, Progress: 100, Failed: failed,
			}})
			mustContain(t, dr.view(), "PROJ-2", "did not move")
		})
	}
}

// An id the selection does not hold is drawn as it came back: inventing a key for
// it would be worse than showing the number the queue used.
func TestMove_DrawsAFailureItCannotNameAsItCameBack(t *testing.T) {
	t.Parallel()
	f, iss := twoProjects(t)
	w := &immediate{}
	dr := newDriver(t, testDeps(f), 100, 24, WithIssues(iss), withWaiter(w.wait))
	dr.walkTo("OTHER")
	dr.running()
	dr.send(taskMsg{gen: dr.m.gen, status: jira.TaskStatus{
		State: jira.TaskComplete, Progress: 100, Failed: []string{"999999"},
	}})
	mustContain(t, dr.view(), "999999")
}

// The pause between two questions is a timer in the binary and injected in every
// other test here, so this is the one place the real one runs.
func TestSleep_GivesUpTheMomentTheViewIsClosed(t *testing.T) {
	t.Parallel()
	if err := sleep(context.Background(), 0); err != nil {
		t.Errorf("waiting for no time at all answered %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := sleep(ctx, time.Hour); err == nil {
		t.Error("a closed wizard waited out the backoff")
	}
	if err := sleep(context.Background(), time.Microsecond); err != nil {
		t.Errorf("a pause that elapsed answered %v", err)
	}
}

// The wizard must not be pushed under an id whose keys belong to something else:
// the kernel looks a pushed view's keys up under the id it was pushed with.
func TestRegister_TheKeysAreRegisteredUnderTheIdTheViewIsPushedWith(t *testing.T) {
	t.Parallel()
	if ViewID != "move" {
		t.Errorf("the view id is %q", ViewID)
	}
	if Requires != jira.CapBulkMove {
		t.Errorf("the wizard declares %q rather than the bulk-move capability", Requires)
	}
	var _ jira.SessionClient = (*jiratest.Fake)(nil)
}
