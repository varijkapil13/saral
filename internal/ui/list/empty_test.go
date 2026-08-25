package list

import (
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/charmbracelet/x/ansi"

	"github.com/varijkapil13/saral/internal/ui/kernel"
	"github.com/varijkapil13/saral/pkg/jira"
)

// refusedConnection is the shape the Cloud adapter gives a site that is not
// listening: the endpoint in Op, and the reason on its own.
func refusedConnection() error {
	return &jira.TransportError{
		Op:  "POST /rest/api/3/search/jql",
		Err: errors.New("dial tcp 127.0.0.1:62630: connect: connection refused"),
	}
}

// sized builds the view and gives it a box, and stops there — which is the state
// kernel.FirstPaint renders, since it never calls Init.
func sized(t *testing.T, d kernel.Deps, w, h int) *Model {
	t.Helper()
	m, ok := New(d).(*Model)
	if !ok {
		t.Fatal("New did not return a *Model")
	}
	next, _ := m.Update(kernel.SizeMsg{Width: w, Height: h})
	sized, ok := next.(*Model)
	if !ok {
		t.Fatal("Update did not return a *Model")
	}
	return sized
}

// emptyPanes are the four answers a pane with no rows has to be able to give.
// Each is built the way a session reaches it rather than by setting the fields.
var emptyPanes = []struct {
	name  string
	file  string
	build func(t *testing.T, w, h int) *Model
	says  []string
}{
	{
		name:  "nothing has been asked yet",
		file:  "unasked",
		build: func(t *testing.T, w, h int) *Model { return sized(t, testDeps(newFake(0)), w, h) },
		says:  []string{"Nothing has been asked of Jira yet."},
	},
	{
		name: "a search in flight",
		file: "searching",
		build: func(t *testing.T, w, h int) *Model {
			m := sized(t, testDeps(newFake(0)), w, h)
			// Init has run begin(); the command it returned has not, so this is
			// the frame between the question and the answer.
			_ = m.Init()
			t.Cleanup(m.stop)
			return m
		},
		says: []string{"Searching", "searching"},
	},
	{
		name:  "an answer with no rows in it",
		file:  "none",
		build: func(t *testing.T, w, h int) *Model { return newDriver(t, testDeps(newFake(0)), w, h).m },
		// The search named here is the whole project: a default that found nothing
		// assigned to the account widens to it before anybody sees this pane.
		says: []string{"Nothing matches this search.", `project = "PROJ" ORDER BY updated DESC`, "0 issues"},
	},
	{
		name: "a search that failed",
		file: "failed",
		build: func(t *testing.T, w, h int) *Model {
			f := newFake(0)
			f.FailNext(refusedConnection())
			return newDriver(t, testDeps(f), w, h).m
		},
		says: []string{"The search failed.", "connection refused", "no answer", retryHint},
	},
}

func TestEmptyPane_SaysWhichKindOfEmptyItIs(t *testing.T) {
	t.Parallel()

	for _, pane := range emptyPanes {
		t.Run(pane.name, func(t *testing.T) {
			t.Parallel()

			m := pane.build(t, 120, 30)
			mustContain(t, ansi.Strip(m.View()), pane.says...)
		})
	}
}

// Four states that read the same are one state with four names.
func TestEmptyPane_NoTwoOfThemReadTheSame(t *testing.T) {
	seen := make(map[string]string, len(emptyPanes))
	for _, pane := range emptyPanes {
		body := ansi.Strip(pane.build(t, 120, 30).View())
		if other, clash := seen[body]; clash {
			t.Errorf("%q and %q draw the same pane:\n%s", pane.name, other, body)
		}
		seen[body] = pane.name
	}
}

func TestEmptyPane_Golden(t *testing.T) {
	t.Parallel()

	for _, size := range []struct{ w, h int }{{120, 30}, {80, 20}} {
		for _, pane := range emptyPanes {
			t.Run(fmt.Sprintf("%s at %dx%d", pane.name, size.w, size.h), func(t *testing.T) {
				t.Parallel()

				m := pane.build(t, size.w, size.h)
				name := fmt.Sprintf("list_empty_%s_%dx%d.golden", pane.file, size.w, size.h)
				golden(t, name, ansi.Strip(m.View()))
			})
		}
	}
}

// The status line is transient by design: the next thing that happens writes
// over it, so the pane has to carry the reason itself.
func TestFailedSearch_KeepsSayingWhyAfterAKeypress(t *testing.T) {
	t.Parallel()

	f := newFake(0)
	f.FailNext(refusedConnection())
	dr := newDriver(t, testDeps(f), 120, 30)

	mustContain(t, dr.view(), "The search failed.", "connection refused")
	mustNotContain(t, dr.view(), "Searching")

	dr.key("j", "k", "G", "g", "g")
	mustContain(t, dr.view(), "The search failed.", "connection refused")
}

// A retarget drops the rows before the new search is issued, so a search that
// then fails leaves the pane with nothing to badge.
func TestFailedRetarget_SaysSoRatherThanLookingLikeASearchStillRunning(t *testing.T) {
	t.Parallel()

	f := newFake(3)
	dr := openAll(t, testDeps(f), 120, 30)
	if len(dr.m.issues) == 0 {
		t.Fatal("no rows were on screen, so this proves nothing about dropping them")
	}

	refused := `the project "NOPE" does not exist`
	f.FailNext(&jira.ValidationError{Fields: []jira.FieldError{{Field: "jql", Message: refused}}})
	dr.send(QueryMsg{JQL: `project = "NOPE" ORDER BY key`, Title: "Nope"})

	mustNotContain(t, dr.view(), "Searching")
	mustContain(t, dr.view(), "The search failed.", refused)

	dr.key("j")
	mustContain(t, dr.view(), refused)
}

// r is the kernel's refresh, which the pane names, so after a failure it has to
// run the search again.
func TestFailedSearch_RefreshRunsItAgainAndTheRowsComeBack(t *testing.T) {
	t.Parallel()

	f := newFake(6)
	dr := openAll(t, testDeps(f), 120, 30)
	want := len(dr.m.issues)

	f.FailNext(refusedConnection())
	dr.send(QueryMsg{JQL: allJQL, Title: "All issues"})
	mustContain(t, dr.view(), "The search failed.")

	before := countCalls(f, "Search")
	dr.send(kernel.RefreshMsg{})

	if got := countCalls(f, "Search") - before; got != 1 {
		t.Errorf("the retry made %d searches, want exactly one", got)
	}
	if dr.m.failure != nil {
		t.Errorf("the failure outlived a search that worked: %v", dr.m.failure)
	}
	if got := len(dr.m.issues); got != want {
		t.Errorf("the retry brought back %d rows, want the %d the search holds", got, want)
	}
	mustNotContain(t, dr.view(), "The search failed.")
}

func TestFailedPane_ReportsWhatTheErrorItselfSays(t *testing.T) {
	t.Parallel()

	tests := map[string]struct {
		err  error
		want string
	}{
		// docs/UX.md: a 403 reaches the user as the capability's Reason, whole.
		"a permission the token does not have": {
			err: &jira.CapabilityError{
				Capability: jira.CapBoards,
				Reason:     "You need the Browse Projects permission for this project",
			},
			want: "You need the Browse Projects permission for this project",
		},
		"the rate limiter": {
			err:  &jira.RateLimitError{RetryAfter: 30 * time.Second},
			want: "rate limited by Jira, retry in 30s",
		},
		"a query the site refused": {
			err:  &jira.ValidationError{Fields: []jira.FieldError{{Field: "jql", Message: "unknown field 'projec'"}}},
			want: "jql: unknown field 'projec'",
		},
		"a host that is not listening": {
			err:  refusedConnection(),
			want: "dial tcp 127.0.0.1:62630: connect: connection refused",
		},
	}

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			f := newFake(0)
			f.FailNext(tc.err)
			dr := newDriver(t, testDeps(f), 120, 30)

			mustContain(t, dr.view(), tc.want)
			dr.key("j")
			mustContain(t, dr.view(), tc.want)
		})
	}
}

// The sentence led with the method, the path and the whole URL, so on a terminal
// somebody actually has, the part saying what went wrong was the part cut off.
func TestFailedPane_SaysWhyInsideEightyColumns(t *testing.T) {
	t.Parallel()

	f := newFake(0)
	f.FailNext(refusedConnection())
	dr := newDriver(t, testDeps(f), 80, 20)

	mustContain(t, dr.view(), "connection refused")
	for i, line := range strings.Split(dr.view(), "\n") {
		if got := ansi.StringWidth(line); got > 80 {
			t.Errorf("line %d is %d columns wide: %q", i, got, line)
		}
	}
}

func TestFilter_IsNamedUnderTheRowsAndClearedFromBrowsing(t *testing.T) {
	t.Parallel()

	dr := openAll(t, testDeps(newFake(40)), 120, 30)
	dr.key("/")
	dr.typeText("login")
	dr.key("enter")

	if narrowed := len(dr.m.view); narrowed == 0 || narrowed == len(dr.m.issues) {
		t.Fatalf("the filter leaves %d of %d rows, so this proves nothing", narrowed, len(dr.m.issues))
	}
	mustContain(t, dr.view(), `only rows matching "login"`, "ctrl+g")

	set, gen := dr.m.LiveKeys()
	if gen != int(keysNarrowed) {
		t.Errorf("a kept filter reports key state %d, want %d", gen, keysNarrowed)
	}
	if !strings.Contains(shortOf(set), "clear filter") {
		t.Errorf("the footer of a narrowed list does not offer the key that widens it: %s", shortOf(set))
	}

	dr.key("ctrl+g")

	if got := len(dr.m.view); got != len(dr.m.issues) {
		t.Errorf("%d of %d rows came back", got, len(dr.m.issues))
	}
	mustNotContain(t, dr.view(), "only rows matching")
	if state, _ := dr.m.LiveKeys(); shortOf(state) != shortOf(liveSets[keysBrowsing]) {
		t.Errorf("the footer still advertises a filter there is none of: %s", shortOf(state))
	}
}

func TestFilter_KeptGolden(t *testing.T) {
	t.Parallel()

	dr := openAll(t, testDeps(newFake(40)), 120, 30)
	dr.key("/")
	dr.typeText("login")
	dr.key("enter")

	golden(t, "list_kept_filter_120x30.golden", dr.view())
}

// The palette reaches the same gesture the key does, which is what makes the
// filter clearable without one.
func TestClearFilterCommand_IsRegisteredAndClearsWhatWasTyped(t *testing.T) {
	t.Parallel()

	var unfilter kernel.Command
	for _, cmd := range kernel.Commands() {
		if cmd.ID == "issues.clear-filter" {
			unfilter = cmd
		}
	}
	if unfilter.ID == "" {
		t.Fatal("issues.clear-filter is not in the registry, so the palette cannot reach it")
	}

	var sent bool
	for _, msg := range collect(unfilter.Run(kernel.Deps{})) {
		if got, ok := msg.(kernel.BroadcastMsg); ok {
			_, sent = got.Msg.(ClearFilterMsg)
		}
	}
	if !sent {
		t.Fatal("the command broadcasts no ClearFilterMsg")
	}

	dr := openAll(t, testDeps(newFake(40)), 120, 30)
	dr.key("/")
	dr.typeText("login")
	dr.key("enter")
	dr.send(ClearFilterMsg{})

	if dr.m.query != "" {
		t.Errorf("the filter still reads %q", dr.m.query)
	}
	if got := len(dr.m.view); got != len(dr.m.issues) {
		t.Errorf("%d of %d rows came back", got, len(dr.m.issues))
	}
}
