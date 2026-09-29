package list

import (
	"context"
	"errors"
	"slices"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/varijkapil13/saral/internal/config"
	"github.com/varijkapil13/saral/internal/ui/kernel"
	"github.com/varijkapil13/saral/internal/ui/widget/card"
	"github.com/varijkapil13/saral/pkg/jira"
	"github.com/varijkapil13/saral/pkg/jira/jiratest"
)

// recording is the fake with the fields of every search it was asked written
// down, which is how a test tells a read for a roomy card from a read for a row.
type recording struct {
	*jiratest.Fake
	mu     sync.Mutex
	fields [][]string
}

func (r *recording) Search(ctx context.Context, q jira.Query) (jira.Page[jira.Issue], error) {
	r.mu.Lock()
	r.fields = append(r.fields, slices.Clone(q.Fields))
	r.mu.Unlock()
	return r.Fake.Search(ctx, q)
}

// roomyReads counts the searches that asked for every field a roomy card draws.
func (r *recording) roomyReads() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	n := 0
	for _, fields := range r.fields {
		if hasAll(fields, card.RoomyFields) {
			n++
		}
	}
	return n
}

func (r *recording) searches() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return len(r.fields)
}

func hasAll(have, want []string) bool {
	for _, w := range want {
		if !slices.Contains(have, w) {
			return false
		}
	}
	return true
}

// cardIssues are the generated issues with what the generator leaves out: one
// with subtasks, one of them done, and one due before the harness's today.
func cardIssues(n int) []jira.Issue {
	issues := jiratest.Gen(n)
	issues[1].Subtasks = []jira.IssueRef{
		{Key: "PROJ-901", Status: jira.Status{Category: jira.CategoryDone}},
		{Key: "PROJ-902", Status: jira.Status{Category: jira.CategoryToDo}},
		{Key: "PROJ-903", Status: jira.Status{Category: jira.CategoryInProgress}},
	}
	issues[2].Due = jira.Date{Year: 2025, Month: time.March, Day: 1}
	return issues
}

func recordingFake(n int) *recording {
	return &recording{Fake: jiratest.New(
		jiratest.WithProject("PROJ", jiratest.Scrum),
		jiratest.WithIssues(cardIssues(n)),
	)}
}

// inLook is every issue in the project, drawn in a look.
func inLook(t *testing.T, look card.Look, n, w, h int) (*driver, *recording, kernel.Deps) {
	t.Helper()
	f := recordingFake(n)
	d := testDeps(f)
	dr := openAll(t, d, w, h)
	dr.send(card.LookMsg{Look: look})
	if dr.m.look != look {
		t.Fatalf("the list is in %s after being sent %s", dr.m.look.Word(), look.Word())
	}
	return dr, f, d
}

// pressV presses V and delivers what it broadcasts back to the view, the way
// the kernel hands a broadcast to every root.
func pressV(t *testing.T, dr *driver) {
	t.Helper()
	_, cmd := dr.m.Update(keyPress("V"))
	if cmd == nil {
		t.Fatal("V returned no command")
	}
	var look *card.LookMsg
	for queue := []tea.Cmd{cmd}; len(queue) > 0; queue = queue[1:] {
		if queue[0] == nil {
			continue
		}
		msg := queue[0]()
		if cmds, ok := unwrapCmds(msg); ok {
			queue = append(queue, cmds...)
			continue
		}
		if b, ok := msg.(kernel.BroadcastMsg); ok {
			if l, ok := b.Msg.(card.LookMsg); ok {
				look = &l
			}
		}
	}
	if look == nil {
		t.Fatal("V broadcast no card.LookMsg")
	}
	dr.send(*look)
}

// ownConfig points ui.toml at a directory of this test's own, so that a look
// saved here is not what every other model in the binary recalls.
func ownConfig(t *testing.T) {
	t.Helper()
	linesByDefault()
	dir := t.TempDir()
	t.Setenv("SARAL_CACHE_DIR", dir)
	t.Setenv("XDG_CACHE_HOME", dir)
}

func TestLook_TheHarnessDrawsLines(t *testing.T) {
	t.Parallel()
	if got := newModel(t).look; got != card.Lines {
		t.Fatalf("the harness builds a list in %s, want lines so the goldens stay the one-line rows", got.Word())
	}
}

func TestLook_AMachineThatNeverChoseOneGetsRoomy(t *testing.T) {
	ownConfig(t)
	if got := newModel(t).look; got != card.Roomy {
		t.Fatalf("a fresh model with no stored look is in %s, want roomy", got.Word())
	}
}

func TestCards_Golden(t *testing.T) {
	t.Parallel()
	for _, look := range []card.Look{card.Roomy, card.Compact} {
		for _, size := range []struct{ w, h int }{{80, 20}, {120, 40}} {
			name := look.Word() + "_" + strconv.Itoa(size.w) + "x" + strconv.Itoa(size.h)
			t.Run(name, func(t *testing.T) {
				t.Parallel()
				dr, _, _ := inLook(t, look, 12, size.w, size.h)
				dr.key("j")
				got := dr.view()
				golden(t, "cards_"+name+".golden", got)
				if strings.Contains(got, "SUMMARY") {
					t.Errorf("a %s list still draws the column captions:\n%s", look.Word(), got)
				}
				if lines := strings.Count(got, "\n") + 1; lines != size.h {
					t.Errorf("a %s frame is %d lines, want %d", look.Word(), lines, size.h)
				}
			})
		}
	}
}

func TestCards_ALookChangeKeepsTheIssueUnderTheCursor(t *testing.T) {
	t.Parallel()
	dr, _, _ := inLook(t, card.Lines, 60, 80, 20)
	for range 30 {
		dr.key("j")
	}
	want := dr.m.selectedKey()
	for _, look := range []card.Look{card.Roomy, card.Compact, card.Lines, card.Compact} {
		dr.send(card.LookMsg{Look: look})
		if got := dr.m.selectedKey(); got != want {
			t.Fatalf("in %s the cursor is on %q, want %q", look.Word(), got, want)
		}
		if dr.m.cursor < dr.m.top || dr.m.cursor >= dr.m.top+dr.m.itemsHeight() {
			t.Fatalf("in %s the cursor at %d is off the window [%d, %d)",
				look.Word(), dr.m.cursor, dr.m.top, dr.m.top+dr.m.itemsHeight())
		}
		mustContain(t, dr.view(), want+" ")
	}
}

func TestCards_PageDownMovesByWholeCards(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		look  card.Look
		items int
	}{
		// 20 lines less the summary line: 19, which is three roomy cards and six
		// compact ones.
		{card.Roomy, 3},
		{card.Compact, 6},
	} {
		t.Run(tc.look.Word(), func(t *testing.T) {
			t.Parallel()
			dr, _, _ := inLook(t, tc.look, 40, 80, 20)
			if got := dr.m.itemsHeight(); got != tc.items {
				t.Fatalf("%d cards fit, want %d", got, tc.items)
			}
			dr.key("pgdown")
			if dr.m.cursor != tc.items {
				t.Fatalf("page down moved the cursor to %d, want %d", dr.m.cursor, tc.items)
			}
			dr.key("pgdown")
			if dr.m.top != dr.m.cursor-tc.items+1 {
				t.Errorf("the second page left the top at %d under a cursor at %d", dr.m.top, dr.m.cursor)
			}
			lines := strings.Split(dr.view(), "\n")
			head := lines[1]
			if !strings.HasPrefix(head, "+") {
				t.Errorf("the first line under the summary is %q, want the head of a whole card", head)
			}
			for _, blank := range lines[1+tc.items*tc.look.Lines():] {
				if strings.TrimSpace(blank) != "" {
					t.Errorf("under the last whole card: %q, want blank", blank)
				}
			}
		})
	}
}

func TestCards_TheWheelMovesByCards(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		look card.Look
		step int
	}{
		{card.Roomy, 1},
		{card.Compact, 1},
		{card.Lines, 3},
	} {
		t.Run(tc.look.Word(), func(t *testing.T) {
			t.Parallel()
			dr, _, _ := inLook(t, tc.look, 40, 80, 20)
			dr.send(tea.MouseWheelMsg{Button: tea.MouseWheelDown})
			if dr.m.top != tc.step {
				t.Fatalf("a notch down scrolled to %d, want %d", dr.m.top, tc.step)
			}
			dr.send(tea.MouseWheelMsg{Button: tea.MouseWheelUp})
			if dr.m.top != 0 {
				t.Errorf("a notch back up left the top at %d", dr.m.top)
			}
		})
	}
}

func TestCards_ADoubleClickOnTheThirdCardOpensIt(t *testing.T) {
	t.Parallel()
	for _, look := range []card.Look{card.Roomy, card.Compact} {
		t.Run(look.Word(), func(t *testing.T) {
			t.Parallel()
			dr, _, d := inLook(t, look, 20, 120, 30)
			pressOn(t, d, dr, rowZone("PROJ-3"))
			if got := dr.m.selectedKey(); got != "PROJ-3" {
				t.Fatalf("clicking the third card left the cursor on %q", got)
			}
			pressOn(t, d, dr, rowZone("PROJ-3"))
			if len(dr.pushes) != 1 || dr.pushes[0].Title != "PROJ-3" {
				t.Fatalf("a double-click on the third card pushed %+v, want PROJ-3", dr.pushes)
			}
		})
	}
}

func TestCards_ClickingACardsStatusFiltersByIt(t *testing.T) {
	t.Parallel()
	for _, look := range []card.Look{card.Roomy, card.Compact} {
		t.Run(look.Word(), func(t *testing.T) {
			t.Parallel()
			dr, _, d := inLook(t, look, 20, 120, 30)
			pressOn(t, d, dr, statusZone("PROJ-2"))
			want := `project = "PROJ" AND status = "10203" ORDER BY updated DESC`
			if dr.m.jql != want {
				t.Fatalf("the click asked for %q, want %q", dr.m.jql, want)
			}
			if len(dr.pushes) != 0 {
				t.Errorf("a status click opened %d panes", len(dr.pushes))
			}
		})
	}
}

func TestCards_RoomyOverRowsWithoutItsFieldsReadsThemOnce(t *testing.T) {
	t.Parallel()
	dr, f, _ := inLook(t, card.Compact, 30, 80, 20)
	for range 7 {
		dr.key("j")
	}
	under, top := dr.m.selectedKey(), dr.m.top
	if f.roomyReads() != 0 {
		t.Fatalf("a compact list asked for the roomy fields %d times", f.roomyReads())
	}

	dr.send(card.LookMsg{Look: card.Roomy})

	if got := f.roomyReads(); got != 1 {
		t.Fatalf("moving to roomy read the roomy fields %d times, want once", got)
	}
	if got := dr.m.selectedKey(); got != under {
		t.Errorf("the revalidation moved the cursor from %q to %q", under, got)
	}
	// The window moves only as far as the taller cards need to keep the cursor's
	// whole, and the read that lands behind it moves it no further.
	if want := max(top, dr.m.cursor-dr.m.itemsHeight()+1); dr.m.top != want {
		t.Errorf("the window starts at %d over a cursor at %d, want %d", dr.m.top, dr.m.cursor, want)
	}
	if dr.m.lacksRoomy() {
		t.Error("the rows still lack the roomy fields after the read that brought them")
	}
	if got := subtaskCount(dr.m.issues[1].Subtasks); got != "1/3" {
		t.Errorf("PROJ-2's subtasks read %q after the roomy read, want 1/3", got)
	}

	before := f.searches()
	dr.send(card.LookMsg{Look: card.Compact})
	dr.send(card.LookMsg{Look: card.Roomy})
	if got := f.searches() - before; got != 0 {
		t.Errorf("moving away from roomy and back over rows that have its fields searched %d times", got)
	}
}

func TestCards_CompactOverRoomyRowsReadsNothing(t *testing.T) {
	t.Parallel()
	dr, f, _ := inLook(t, card.Roomy, 30, 80, 20)
	before := f.searches()
	dr.send(card.LookMsg{Look: card.Compact})
	dr.send(card.LookMsg{Look: card.Lines})
	if got := f.searches() - before; got != 0 {
		t.Errorf("moving away from roomy searched %d times, want none", got)
	}
}

func TestCards_AFailedRevalidationKeepsTheRowsAndBadgesThem(t *testing.T) {
	t.Parallel()
	for name, err := range map[string]error{
		"403":       &jira.CapabilityError{Capability: jira.CapBoards, Reason: "needs Browse Projects permission"},
		"429":       &jira.RateLimitError{RetryAfter: 30 * time.Second},
		"transport": &jira.TransportError{Op: "search", Err: errors.New("dial tcp: no such host")},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			dr, f, _ := inLook(t, card.Compact, 30, 80, 20)
			rows := len(dr.m.issues)
			f.FailNext(err)
			dr.send(card.LookMsg{Look: card.Roomy})
			if got := len(dr.m.issues); got != rows {
				t.Fatalf("a failed revalidation left %d of %d rows", got, rows)
			}
			if !dr.m.stale {
				t.Error("a failed revalidation did not mark the rows stale")
			}
			mustContain(t, dr.view(), staleLabel, "PROJ-1 ")
		})
	}
}

func TestCards_ALookChangeMidWalkRestartsIt(t *testing.T) {
	t.Parallel()
	f := recordingFake(30)
	dr := openAll(t, testDeps(f), 80, 20)
	was := dr.m.gen
	m := dr.m
	_ = m.refetch(whyBackground)
	if !m.loading {
		t.Fatal("the refetch did not start a walk")
	}
	walking := m.gen
	dr.send(card.LookMsg{Look: card.Roomy})
	if dr.m.gen == walking || dr.m.gen <= was {
		t.Fatalf("moving to roomy mid-walk kept generation %d, want a new one", dr.m.gen)
	}
	if got := f.roomyReads(); got != 1 {
		t.Errorf("the restarted walk read the roomy fields %d times, want once", got)
	}
}

func TestCards_VCyclesRoomyCompactLines(t *testing.T) {
	ownConfig(t)
	dr, _, _ := inLook(t, card.Roomy, 20, 80, 20)
	for _, want := range []card.Look{card.Compact, card.Lines, card.Roomy} {
		pressV(t, dr)
		if dr.m.look != want {
			t.Fatalf("V moved the list to %s, want %s", dr.m.look.Word(), want.Word())
		}
	}
}

func TestCards_VReachesTheListThroughTheKernel(t *testing.T) {
	ownConfig(t)
	if err := config.SaveLook(card.Lines.Word()); err != nil {
		t.Fatal(err)
	}
	m := startAll(t, testDeps(newFake(20)), 120, 30)
	mustContain(t, frame(m), "SUMMARY")
	m = send(t, m, keyPress("V"))
	got := frame(m)
	mustNotContain(t, got, "SUMMARY")
	mustContain(t, got, "+ > PROJ-1 ")
	if saved := card.Recall(); saved != card.Roomy {
		t.Errorf("V saved %s, want roomy", saved.Word())
	}
}
