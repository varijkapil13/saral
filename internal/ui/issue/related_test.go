package issue

import (
	"errors"
	"fmt"
	"regexp"
	"slices"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/varijkapil13/saral/internal/ui/kernel"
	"github.com/varijkapil13/saral/internal/ui/uitest"
	"github.com/varijkapil13/saral/pkg/jira"
	"github.com/varijkapil13/saral/pkg/jira/jiratest"
)

func navIssue() jira.Issue {
	todo := jira.Status{Name: "To Do", Category: jira.CategoryToDo}
	doing := jira.Status{Name: "In Progress", Category: jira.CategoryInProgress}
	done := jira.Status{Name: "Done", Category: jira.CategoryDone}
	return jira.Issue{
		ID: "30012", Key: "PROJ-12", Summary: "Rewrite the invoice export",
		Project: jira.ProjectRef{Key: "PROJ", Name: "Billing"},
		Type:    jira.IssueType{ID: "10301", Name: "Story"},
		Status:  doing,
		Parent:  &jira.IssueRef{ID: "30003", Key: "PROJ-3", Summary: "Billing rewrite", Status: doing, Type: jira.IssueType{Name: "Epic"}},
		Subtasks: []jira.IssueRef{
			{ID: "30031", Key: "PROJ-31", Summary: "Split the export job", Status: done},
			{ID: "30032", Key: "PROJ-32", Summary: "Drop the cron entry", Status: todo},
		},
		Links: []jira.IssueLink{
			{ID: "1", Type: "Blocks", Label: "blocks", Direction: jira.LinkOutward,
				Other: jira.IssueRef{ID: "30040", Key: "PROJ-40", Summary: "Ship 2.0", Status: todo}},
			{ID: "2", Type: "Blocks", Label: "is blocked by", Direction: jira.LinkInward,
				Other: jira.IssueRef{ID: "40007", Key: "OPS-7", Summary: "Rotate the keys", Status: jira.Status{Name: "In Review", Category: jira.CategoryInProgress}}},
		},
		Requested: jira.AllFields(),
	}
}

func navPanel(t *testing.T, w, h int, opts ...modelOption) (*panel, kernel.Deps) {
	t.Helper()
	d := testDeps(t, newFake(4))
	iss := navIssue()
	p := newPanel(t, New(d, jira.Issue{Key: iss.Key, Summary: iss.Summary}, opts...), w, h)
	p.send(loadedMsg{gen: p.editor().gen, issue: iss})
	p.editor().focus = regionDetails
	p.send(kernel.FocusMsg{Focused: true})
	return p, d
}

func (p *panel) cursorOnRef(key string) {
	p.t.Helper()
	m := p.editor()
	for i := range m.sideRows {
		if m.sideRows[i].kind == rkRef && m.sideRows[i].ref.Key == key {
			m.cursor = i
			m.focus = regionDetails
			p.send(kernel.FocusMsg{Focused: true})
			return
		}
	}
	p.t.Fatalf("no related issue %s on the sidebar cursor", key)
}

func (p *panel) onlyPush() kernel.PushMsg {
	p.t.Helper()
	if len(p.pushes) != 1 {
		p.t.Fatalf("want one push, got %d", len(p.pushes))
	}
	return p.pushes[0]
}

func pushedPane(t *testing.T, msg kernel.PushMsg) *Model {
	t.Helper()
	m, ok := msg.View.(*Model)
	if !ok {
		t.Fatalf("pushed a %T, not an issue pane", msg.View)
	}
	return m
}

func TestRelated_RefRowsAreOnTheCursor(t *testing.T) {
	t.Parallel()
	p, _ := navPanel(t, 120, 38)
	var got []string
	for _, row := range p.editor().sideRows {
		if row.kind == rkRef {
			got = append(got, row.ref.Key)
		}
	}
	want := []string{"PROJ-3", "PROJ-31", "PROJ-32", "PROJ-40", "OPS-7"}
	if !slices.Equal(got, want) {
		t.Fatalf("cursor rows are %v, want %v", got, want)
	}

	p.cursorOnRef("PROJ-31")
	arrow := regexp.MustCompile(`->\s+PROJ-31 `)
	if !arrow.MatchString(p.frame()) {
		t.Errorf("the cursor row does not carry the arrow:\n%s", p.frame())
	}
	p.keys("j")
	if got := p.editor().currentCursorRow(); got == nil || got.ref.Key != "PROJ-32" {
		t.Errorf("j did not step to the next related issue: %+v", got)
	}
}

func TestRelated_EnterOpensRefWithSeed(t *testing.T) {
	t.Parallel()
	for _, stroke := range []string{"enter"} {
		t.Run(stroke, func(t *testing.T) {
			t.Parallel()
			p, _ := navPanel(t, 120, 38)
			p.cursorOnRef("PROJ-3")
			p.keys(stroke)

			push := p.onlyPush()
			if push.ID != ViewID || push.Title != "PROJ-3" {
				t.Errorf("pushed %q titled %q", push.ID, push.Title)
			}
			opened := pushedPane(t, push)
			if got := opened.issue; got.Key != "PROJ-3" || got.ID != "30003" || got.Summary != "Billing rewrite" ||
				got.Status.Name != "In Progress" || got.Type.Name != "Epic" {
				t.Errorf("the seed is %+v", got)
			}
			if !slices.Equal(opened.trail, []string{"PROJ-12"}) {
				t.Errorf("the trail is %v", opened.trail)
			}
		})
	}
}

func TestRelated_EditKeyLeavesARefRowAlone(t *testing.T) {
	t.Parallel()
	p, _ := navPanel(t, 120, 38)
	p.cursorOnRef("PROJ-3")
	p.keys("e")
	if len(p.pushes) != 0 {
		t.Errorf("e on a reference row pushed %d views", len(p.pushes))
	}
}

func TestRelated_ClickKeyOpens(t *testing.T) {
	t.Parallel()
	p, d := navPanel(t, 120, 38)
	m := p.editor()
	id := refRowID("Subtasks", "PROJ-32")
	at := uitest.Zone(t, d.Zones, p.view.View, m.zones.ID(refZone(id)), m.zones.ID(zoneNames[regionDetails]))
	p.send(tea.MouseClickMsg{X: at.StartX + 1, Y: at.StartY, Button: tea.MouseLeft})
	if got := pushedPane(t, p.onlyPush()).issue.Key; got != "PROJ-32" {
		t.Errorf("a click on the key opened %s", got)
	}
}

func TestRelated_DoubleClickRowOpens(t *testing.T) {
	t.Parallel()
	p, d := navPanel(t, 120, 38)
	m := p.editor()
	row := fieldRowZone(refRowID("blocks", "PROJ-40"))
	at := uitest.Zone(t, d.Zones, p.view.View, m.zones.ID(row), m.zones.ID(zoneNames[regionDetails]))
	click := tea.MouseClickMsg{X: at.StartX + 1, Y: at.StartY, Button: tea.MouseLeft}

	p.send(click)
	if len(p.pushes) != 0 {
		t.Fatal("one click on the row opened it")
	}
	if got := p.editor().currentCursorRow(); got == nil || got.ref.Key != "PROJ-40" {
		t.Fatalf("the click did not select the row: %+v", got)
	}
	p.send(click)
	if got := pushedPane(t, p.onlyPush()).issue.Key; got != "PROJ-40" {
		t.Errorf("a double-click opened %s", got)
	}
}

func TestParent_POpensParent(t *testing.T) {
	t.Parallel()
	p, _ := navPanel(t, 120, 38)
	p.keys("p")
	opened := pushedPane(t, p.onlyPush())
	if opened.issue.Key != "PROJ-3" || !slices.Equal(opened.trail, []string{"PROJ-12"}) {
		t.Errorf("p opened %s with trail %v", opened.issue.Key, opened.trail)
	}
}

func TestParent_PWithoutParentSaysSo(t *testing.T) {
	t.Parallel()
	f := newFake(6)
	d := testDeps(t, f)
	p := newPanel(t, New(d, readIssue(t, f, "PROJ-3")), 120, 30)
	p.send(loadedMsg{gen: p.editor().gen, issue: readIssue(t, f, "PROJ-3")})
	p.keys("p")
	if len(p.pushes) != 0 {
		t.Error("p pushed a pane for an issue with no parent")
	}
	if got := p.lastStatus().Text; got != "PROJ-3 has no parent" {
		t.Errorf("status is %q", got)
	}
}

func TestTrail_OpeningIssueBeneathReturnsToIt(t *testing.T) {
	t.Parallel()
	p, _ := navPanel(t, 120, 38, fromTrail([]string{"PROJ-3"}))
	p.cursorOnRef("PROJ-3")
	p.keys("enter")
	if len(p.pushes) != 0 || !slices.Equal(p.popTos, []kernel.PopToMsg{{ID: ViewID, Title: "PROJ-3"}}) {
		t.Errorf("pushes %d, returns %v; want the pane beneath to be returned to", len(p.pushes), p.popTos)
	}
}

func TestTrail_DeeperCycleReturnsToo(t *testing.T) {
	t.Parallel()
	p, _ := navPanel(t, 120, 38, fromTrail([]string{"PROJ-3", "PROJ-40"}))
	p.cursorOnRef("PROJ-3")
	p.keys("enter")
	if len(p.pushes) != 0 || !slices.Equal(p.popTos, []kernel.PopToMsg{{ID: ViewID, Title: "PROJ-3"}}) {
		t.Errorf("pushes %d, returns %v; want the first PROJ-3 to be returned to", len(p.pushes), p.popTos)
	}
}

func TestTrail_DirtyPaneStillBlocksItsOwnClose(t *testing.T) {
	t.Parallel()
	f := newFake(6)
	p, _ := openEditable(t, f, "PROJ-2", withDrafts(tempDrafts(t)), fromTrail([]string{"PROJ-1"}))
	iss := readIssue(t, f, "PROJ-2")
	iss.Parent = &jira.IssueRef{Key: "PROJ-1", Summary: "Above"}
	p.send(loadedMsg{gen: p.editor().gen, issue: iss})
	p.editor().focus = regionDetails
	rowAt(t, p, "summary")
	p.keys("enter")
	p.editor().input.SetValue("Renamed")
	p.keys("enter")
	if _, blocked := p.editor().BlocksClose(); !blocked {
		t.Fatal("the pane with an unsaved edit does not block its own pop")
	}
	p.keys("p")
	if len(p.popTos) != 1 || len(p.pushes) != 0 {
		t.Errorf("p on the pane beneath: returns %d, pushes %d", len(p.popTos), len(p.pushes))
	}
}

func rowOfRef(s *session, key string) bool {
	return regexp.MustCompile(`->\s+` + regexp.QuoteMeta(key) + ` `).MatchString(s.frame())
}

func (s *session) walkToRef(key string) {
	s.t.Helper()
	s.press("tab")
	for range 60 {
		if rowOfRef(s, key) {
			return
		}
		s.press("j")
	}
	s.t.Fatalf("the cursor never reached %s:\n%s", key, s.frame())
}

func linkedFake(t *testing.T) *jiratest.Fake {
	t.Helper()
	f := newFake(6)
	linkOn(t, f, "PROJ-1", "PROJ-2")
	return f
}

func TestTrail_EscRestoresScrollAndCursor(t *testing.T) {
	t.Parallel()
	f := linkedFake(t)
	s := boot(t, testDeps(t, f), seedOf(t, f, "PROJ-1"), 120, 30)
	s.walkToRef("PROJ-2")
	before := s.frame()

	s.press("enter")
	mustContain(t, s.title(), "PROJ-2")
	s.press("esc")
	if got := s.frame(); got != before {
		t.Errorf("coming back changed the pane\n--- before ---\n%s\n--- after ---\n%s", before, got)
	}
}

func TestTrail_FollowingALinkBackReturnsInsteadOfStacking(t *testing.T) {
	t.Parallel()
	f := linkedFake(t)
	s := boot(t, testDeps(t, f), seedOf(t, f, "PROJ-1"), 120, 30)
	s.walkToRef("PROJ-2")
	s.press("enter")
	mustContain(t, s.title(), "PROJ-2")

	s.walkToRef("PROJ-1")
	s.press("enter")
	mustContain(t, s.title(), "PROJ-1")
	s.press("esc")
	mustContain(t, s.frame(), "the rows this issue was opened from")
}

func TestLinks_BothDirectionsGroupedByPhrase(t *testing.T) {
	t.Parallel()
	f := linkedFake(t)
	for key, want := range map[string]struct{ has, not string }{
		"PROJ-1": {"holds up", "is held up by"},
		"PROJ-2": {"is held up by", "holds up"},
	} {
		dr := newDriver(t, testDeps(t, f), seedOf(t, f, key), 120, 50)
		got := dr.view()
		mustContain(t, got, want.has)
		if strings.Contains(strings.ReplaceAll(got, "is held up by", ""), want.not) {
			t.Errorf("%s draws the other direction's phrase %q:\n%s", key, want.not, got)
		}
	}
}

func TestLinks_EmptyLabelKeepsDirectionsApart(t *testing.T) {
	t.Parallel()
	iss := navIssue()
	other := func(key string) jira.IssueRef { return jira.IssueRef{Key: key, Summary: "x"} }
	iss.Links = []jira.IssueLink{
		{ID: "1", Type: "Blocks", Direction: jira.LinkOutward, Other: other("PROJ-40")},
		{ID: "2", Type: "Blocks", Direction: jira.LinkInward, Other: other("OPS-7")},
		{ID: "3", Type: "Blocks", Direction: jira.LinkOutward, Other: other("PROJ-41")},
	}
	dr := newDriver(t, testDeps(t, newFake(2)), jira.Issue{Key: iss.Key}, 120, 38)
	dr.send(loadedMsg{gen: dr.m.gen, issue: iss})
	groups := dr.m.refGroups()
	got := make([]string, 0, len(groups))
	for _, g := range groups {
		got = append(got, g.label)
	}
	if want := []string{"Parent", "Subtasks", "Blocks", "Blocks (inward)"}; !slices.Equal(got, want) {
		t.Errorf("groups are %v, want %v", got, want)
	}
}

func TestLinks_SheetOpensWithTrailAndNeverPopsToItself(t *testing.T) {
	t.Parallel()
	f := collabFake()
	linkOn(t, f, "PROJ-1", "PROJ-2")
	d := openSheet(t, f, "PROJ-1", &linksKind{})
	d.s.trail = []string{"PROJ-9", "PROJ-1"}
	d.keys("enter")
	if len(d.pushed) != 1 || d.pops != 0 {
		t.Fatalf("pushed %d, popped %d", len(d.pushed), d.pops)
	}
	if got := pushedPane(t, d.pushed[0]).trail; !slices.Equal(got, []string{"PROJ-9", "PROJ-1", ""}) {
		t.Errorf("the trail is %v; the sheet sits between the panes and must not be mistaken for one", got)
	}
}

func TestLiveKeys_RefRow(t *testing.T) {
	t.Parallel()
	p, _ := navPanel(t, 120, 38)
	p.cursorOnRef("PROJ-31")
	m := p.editor()
	set, gen := m.LiveKeys()
	if gen != lkBrowseRef {
		t.Fatalf("a ref row answers for state %d", gen)
	}
	if got := actsOf(set); !strings.Contains(got, "enter open") {
		t.Errorf("the footer says %q", got)
	}
	m.cursor = 0
	if _, gen := m.LiveKeys(); gen == lkBrowseRef {
		t.Error("the summary row answers as a related issue")
	}
}

func TestOpen_NotFoundSaysCannotBrowse(t *testing.T) {
	t.Parallel()
	d := testDeps(t, newFake(4))
	dr := newDriver(t, d, jira.Issue{Key: "PROJ-99", Summary: "Retire the old exporter"}, 100, 28)
	want := "PROJ-99 could not be opened: it does not exist, or this account cannot browse its project."
	if got := dr.lastStatus(); got.Text != want || got.Level != kernel.LevelError {
		t.Errorf("status is %+v", got)
	}
	mustContain(t, dr.view(), "PROJ-99", "Retire the old exporter", "could not be opened")
	golden(t, "open_notfound_100x28.golden", dr.view())
}

func TestOpen_RateLimited(t *testing.T) {
	t.Parallel()
	f := newFake(4)
	seed := seedOf(t, f, "PROJ-1")
	f.FailNextN(4, &jira.RateLimitError{RetryAfter: 30 * time.Second})
	dr := newDriver(t, testDeps(t, f), seed, 100, 28)
	reason, _ := jira.Reason(&jira.RateLimitError{RetryAfter: 30 * time.Second})
	if got := dr.lastStatus().Text; got != reason {
		t.Errorf("status is %q, want %q", got, reason)
	}
	mustContain(t, dr.view(), reason[:20])
}

func TestOpen_TransportFailure(t *testing.T) {
	t.Parallel()
	f := newFake(4)
	err := &jira.TransportError{Op: "issue", Err: errors.New("connection reset")}
	seed := seedOf(t, f, "PROJ-1")
	f.FailNextN(4, err)
	dr := newDriver(t, testDeps(t, f), seed, 100, 28)
	if got := dr.lastStatus().Text; got != err.Error() {
		t.Errorf("status is %q, want %q", got, err.Error())
	}
	mustContain(t, dr.view(), "connection reset")
}

func TestRelated_Frames(t *testing.T) {
	t.Parallel()
	for _, size := range []struct{ w, h int }{{120, 38}, {80, 20}} {
		t.Run(fmt.Sprintf("%dx%d", size.w, size.h), func(t *testing.T) {
			t.Parallel()
			p, _ := navPanel(t, size.w, size.h)
			p.cursorOnRef("PROJ-32")
			golden(t, fmt.Sprintf("related_%dx%d.golden", size.w, size.h), p.frame())
		})
	}
}
