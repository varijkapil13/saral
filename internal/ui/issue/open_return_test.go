package issue

import (
	"strings"
	"testing"

	"github.com/varijkapil13/saral/internal/ui/kernel"
	"github.com/varijkapil13/saral/pkg/jira"
	"github.com/varijkapil13/saral/pkg/jira/jiratest"
)

const rowsText = "the rows this issue was opened from"

func loopFake(t *testing.T) *jiratest.Fake {
	t.Helper()
	f := newFake(6)
	linkOn(t, f, "PROJ-1", "PROJ-2")
	linkOn(t, f, "PROJ-2", "PROJ-3")
	linkOn(t, f, "PROJ-3", "PROJ-1")
	return f
}

func (s *session) follow(key string) {
	s.t.Helper()
	s.walkToRef(key)
	s.press("enter")
	mustContain(s.t, s.title(), key)
}

func (s *session) topPane() *Model {
	s.t.Helper()
	m, ok := s.m.Top().(*Model)
	if !ok {
		s.t.Fatalf("the top of the stack is a %T, not an issue pane", s.m.Top())
	}
	return m
}

func TestOpenRelated_ALoopTwoDeepReturnsToTheFirstPane(t *testing.T) {
	t.Parallel()
	f := loopFake(t)
	s := boot(t, testDeps(t, f), seedOf(t, f, "PROJ-1"), 120, 30)
	s.follow("PROJ-2")
	s.follow("PROJ-3")

	s.follow("PROJ-1")
	s.press("esc")
	mustContain(t, s.frame(), rowsText)
}

func TestOpenRelated_ABackAndForthReturnsToTheFirstPane(t *testing.T) {
	t.Parallel()
	f := linkedFake(t)
	s := boot(t, testDeps(t, f), seedOf(t, f, "PROJ-1"), 120, 30)
	s.follow("PROJ-2")
	s.follow("PROJ-1")
	s.press("esc")
	mustContain(t, s.frame(), rowsText)
}

func TestOpenRelated_ThroughASheetCollapsesPastIt(t *testing.T) {
	t.Parallel()
	f := linkedFake(t)
	s := boot(t, testDeps(t, f), seedOf(t, f, "PROJ-1"), 120, 30)
	s.follow("PROJ-2")

	s.press("&")
	mustContain(t, s.frame(), "PROJ-1")
	s.press("enter")
	mustContain(t, s.title(), "PROJ-1")
	s.press("esc")
	mustContain(t, s.frame(), rowsText)
}

func TestOpenRelated_ANewKeyStillPushes(t *testing.T) {
	t.Parallel()
	f := loopFake(t)
	s := boot(t, testDeps(t, f), seedOf(t, f, "PROJ-1"), 120, 30)
	s.follow("PROJ-2")
	s.follow("PROJ-3")

	s.press("esc")
	mustContain(t, s.title(), "PROJ-2")
	s.press("esc")
	mustContain(t, s.title(), "PROJ-1")
	s.press("esc")
	mustContain(t, s.frame(), rowsText)
}

func TestOpenRelated_ADirtyPaneAboveTheTargetIsAskedAbout(t *testing.T) {
	t.Parallel()
	f := loopFake(t)
	allEditable(f, "PROJ-2")
	s := boot(t, testDeps(t, f), seedOf(t, f, "PROJ-1"), 120, 30)
	s.follow("PROJ-2")

	b := s.topPane()
	b.focus = regionDetails
	for i, cr := range b.sideRows {
		if cr.id == "summary" {
			b.cursor = i
		}
	}
	s.press("enter")
	b.input.SetValue("Renamed")
	s.press("enter")
	if _, blocked := b.BlocksClose(); !blocked {
		t.Fatal("the edit did not leave the pane dirty")
	}

	for i, cr := range b.sideRows {
		if cr.kind == rkRef && cr.ref.Key == "PROJ-1" {
			b.cursor = i
		}
	}
	s.press("enter")
	mustContain(t, s.title(), "PROJ-2")
	if _, blocked := b.BlocksClose(); !blocked {
		t.Error("the edit was thrown away without asking")
	}
	s.press("n")
	mustContain(t, s.title(), "PROJ-1")
	s.press("esc")
	mustContain(t, s.frame(), rowsText)
}

func TestIssuePushTitleIsItsKey(t *testing.T) {
	t.Parallel()
	d := testDeps(t, newFake(4))
	ref := jira.IssueRef{ID: "30012", Key: "PROJ-12", Summary: "x"}

	push, ok := openIssue(d, ref, nil)().(kernel.PushMsg)
	if !ok {
		t.Fatal("opening a new key did not push")
	}
	if push.ID != ViewID || push.Title != "PROJ-12" {
		t.Errorf("pushed %q under %q; kernel.PopTo finds a pane by that pair", push.ID, push.Title)
	}

	back, ok := openIssue(d, ref, []string{"PROJ-12"})().(kernel.PopToMsg)
	if !ok || back.ID != push.ID || back.Title != push.Title {
		t.Errorf("returning asked for %+v, want the pair a push uses: %q %q", back, push.ID, push.Title)
	}
	if strings.TrimSpace(push.Title) == "" {
		t.Error("an empty title would be the title beneath it")
	}
}
