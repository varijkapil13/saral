package kernel

import (
	"errors"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/varijkapil13/saral/internal/app"
)

func querySpec(id string, slot int, v *stubView) ViewSpec {
	s := spec(id, slot, "", v)
	s.RunsQueries = true
	return s
}

func savedQueries(t *testing.T, in ...app.SavedQuery) app.SavedQueries {
	t.Helper()
	q, err := app.NewSavedQueries(in...)
	if err != nil {
		t.Fatalf("NewSavedQueries: %v", err)
	}
	return q
}

func twoQueries(t *testing.T) app.SavedQueries {
	t.Helper()
	return savedQueries(t,
		app.SavedQuery{Name: "Blockers", JQL: "priority = Highest ORDER BY updated DESC", Slot: 1},
		app.SavedQuery{Name: "Mine", JQL: "assignee = currentUser() ORDER BY updated DESC", Slot: 3},
	)
}

// run executes a command and returns the messages it produced, flattening the
// batches the kernel builds out of several of them.
func run(cmd tea.Cmd) []tea.Msg {
	if cmd == nil {
		return nil
	}
	msg := cmd()
	switch msg := msg.(type) {
	case nil:
		return nil
	case tea.BatchMsg:
		var out []tea.Msg
		for _, c := range msg {
			out = append(out, run(c)...)
		}
		return out
	default:
		return []tea.Msg{msg}
	}
}

func TestDigits_RunTheQueryBoundToThemInARootView(t *testing.T) {
	resetRegistry()
	t.Cleanup(resetRegistry)
	issues := &stubView{id: "issues"}
	RegisterView(querySpec("issues", 1, issues))

	d := testDeps()
	d.Saved = twoQueries(t)
	m := newAt(t, d, 120, 30)
	issues.seen = nil

	if _, _ = press(m, "3"); !saw(issues, "query:assignee = currentUser() ORDER BY updated DESC") {
		t.Errorf("3 did not run the query bound to it: %v", issues.seen)
	}
}

func TestDigits_RunAQueryInTheViewThatRunsThemWhateverIsOnScreen(t *testing.T) {
	resetRegistry()
	t.Cleanup(resetRegistry)
	board := &stubView{id: "board"}
	issues := &stubView{id: "issues"}
	RegisterView(spec("board", 1, "", board))
	RegisterView(querySpec("issues", 2, issues))

	d := testDeps()
	d.Saved = twoQueries(t)
	m := newAt(t, d, 120, 30)
	if got := ansi.Strip(m.Frame()); !strings.Contains(got, "board body") {
		t.Fatalf("did not start on the board:\n%s", got)
	}

	m, _ = press(m, "1")
	if got := ansi.Strip(m.Frame()); !strings.Contains(got, "issues body") {
		t.Errorf("a saved query did not switch to the view that runs one:\n%s", got)
	}
	if !saw(issues, "query:priority = Highest ORDER BY updated DESC") {
		t.Errorf("the query never reached the view: %v", issues.seen)
	}
}

func TestDigits_SayNothingIsBoundRatherThanRunningSomethingElse(t *testing.T) {
	resetRegistry()
	t.Cleanup(resetRegistry)
	issues := &stubView{id: "issues"}
	RegisterView(querySpec("issues", 1, issues))

	d := testDeps()
	d.Saved = twoQueries(t)
	m := newAt(t, d, 120, 30)
	issues.seen = nil

	m, _ = press(m, "5")
	if got := ansi.Strip(m.Frame()); !strings.Contains(got, "no saved query is bound to 5") {
		t.Errorf("an unbound digit was silent:\n%s", got)
	}
	for _, s := range issues.seen {
		if strings.HasPrefix(s, "query:") {
			t.Errorf("an unbound digit ran a query anyway: %v", issues.seen)
		}
	}
}

func TestDigits_InAPushedViewReachTheViewRatherThanASavedQuery(t *testing.T) {
	resetRegistry()
	t.Cleanup(resetRegistry)
	issues := &stubView{id: "issues"}
	RegisterView(querySpec("issues", 1, issues))

	d := testDeps()
	d.Saved = twoQueries(t)
	m := newAt(t, d, 120, 30)
	detail := &stubView{id: "detail"}
	next, _ := m.Update(PushMsg{View: detail, ID: "issue", Title: "PROJ-1"})
	m = next.(Model)
	issues.seen, detail.seen = nil, nil

	m, _ = press(m, "3")
	if saw(issues, "query:assignee = currentUser() ORDER BY updated DESC") {
		t.Error("a digit under a pushed view ran a saved query")
	}
	if !saw(detail, "key:3") {
		t.Errorf("the digit never reached the pushed view: %v", detail.seen)
	}
	if got := ansi.Strip(m.Frame()); !strings.Contains(got, "detail body") {
		t.Errorf("a digit under a pushed view changed the screen:\n%s", got)
	}
}

func TestRunSaved_SaysSoWhenNothingInTheBuildRunsQueries(t *testing.T) {
	resetRegistry()
	t.Cleanup(resetRegistry)
	RegisterView(spec("board", 1, "", &stubView{id: "board"}))

	d := testDeps()
	d.Saved = twoQueries(t)
	m := newAt(t, d, 120, 30)

	m, _ = press(m, "1")
	if got := ansi.Strip(m.Frame()); !strings.Contains(got, "nothing in this build can run a saved query") {
		t.Errorf("a bound digit with nowhere to run was silent:\n%s", got)
	}
}

func TestGoPrefix_SwitchesViewFromAnywhereAndIsBufferedUntilItIsSpent(t *testing.T) {
	tests := map[string]struct {
		keys     []string
		wantBody string
		wantSeen []string
		wantGone []string
	}{
		"a digit switches to that slot": {
			keys:     []string{"g", "2"},
			wantBody: "backlog body",
			wantGone: []string{"key:g", "key:2"},
		},
		"a second g reaches the view as both keys, in order": {
			keys:     []string{"g", "g"},
			wantBody: "board body",
			wantSeen: []string{"key:g", "key:g"},
		},
		"a letter reaches the view behind the buffered prefix": {
			keys:     []string{"g", "e"},
			wantBody: "board body",
			wantSeen: []string{"key:g", "key:e"},
		},
		"esc throws the gesture away and forwards nothing": {
			keys:     []string{"g", "esc"},
			wantBody: "board body",
			wantGone: []string{"key:g", "key:esc"},
		},
		"a global key is the view's while the prefix is buffered": {
			keys:     []string{"g", "r"},
			wantBody: "board body",
			wantSeen: []string{"key:g", "key:r"},
			wantGone: []string{"refresh"},
		},
	}

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			resetRegistry()
			t.Cleanup(resetRegistry)
			board := &stubView{id: "board"}
			RegisterView(spec("board", 1, "", board))
			RegisterView(spec("backlog", 2, "", &stubView{id: "backlog"}))

			m := newAt(t, testDeps(), 120, 30)
			board.seen = nil
			m, _ = press(m, tc.keys...)

			if got := ansi.Strip(m.Frame()); !strings.Contains(got, tc.wantBody) {
				t.Errorf("frame does not show %q:\n%s", tc.wantBody, got)
			}
			for _, want := range tc.wantSeen {
				if !saw(board, want) {
					t.Errorf("the view was not handed %q: %v", want, board.seen)
				}
			}
			for _, gone := range tc.wantGone {
				if saw(board, gone) {
					t.Errorf("the view was handed %q, which the kernel had taken: %v", gone, board.seen)
				}
			}
			if len(tc.wantSeen) > 1 && !equalOrder(board.seen, tc.wantSeen) {
				t.Errorf("the keys arrived as %v, want %v in that order", board.seen, tc.wantSeen)
			}
		})
	}
}

func TestGoPrefix_OpensASlotFromAPushedView(t *testing.T) {
	resetRegistry()
	t.Cleanup(resetRegistry)
	RegisterView(spec("board", 1, "", &stubView{id: "board"}))
	RegisterView(spec("backlog", 2, "", &stubView{id: "backlog"}))

	m := newAt(t, testDeps(), 120, 30)
	next, _ := m.Update(PushMsg{View: &stubView{id: "detail"}, ID: "issue"})
	m = next.(Model)

	m, _ = press(m, "g", "2")
	if got := ansi.Strip(m.Frame()); !strings.Contains(got, "backlog body") {
		t.Errorf("g 2 did not switch view from a pushed one:\n%s", got)
	}
}

func TestGoPrefix_DoesNotLatchWhileAViewIsTakingTyping(t *testing.T) {
	resetRegistry()
	t.Cleanup(resetRegistry)
	form := &stubView{id: "form", capturing: true}
	RegisterView(spec("form", 1, "", form))
	RegisterView(spec("backlog", 2, "", &stubView{id: "backlog"}))

	d := testDeps()
	d.Saved = twoQueries(t)
	m := newAt(t, d, 120, 30)
	form.seen = nil

	m, _ = press(m, "g", "2", "1")
	for _, want := range []string{"key:g", "key:2", "key:1"} {
		if !saw(form, want) {
			t.Errorf("a view taking typing did not get %q: %v", want, form.seen)
		}
	}
	if got := ansi.Strip(m.Frame()); !strings.Contains(got, "form body") {
		t.Errorf("a gesture latched under a view that was taking typing:\n%s", got)
	}
}

func TestFooter_FollowsTheLatchedPrefixAndTheBoundDigits(t *testing.T) {
	resetRegistry()
	t.Cleanup(resetRegistry)
	RegisterView(querySpec("issues", 1, &stubView{id: "issues"}))

	d := testDeps()
	d.Saved = twoQueries(t)
	m := newAt(t, d, 140, 30)

	footer := lastLine(ansi.Strip(m.Frame()))
	if !strings.Contains(footer, "1/3") || !strings.Contains(footer, "saved query") {
		t.Errorf("the footer does not offer the bound digits:\n%s", footer)
	}

	m, _ = press(m, "g")
	latched := lastLine(ansi.Strip(m.Frame()))
	if !strings.Contains(latched, "switch view") || !strings.Contains(latched, "cancel") {
		t.Errorf("the footer did not repaint for the latched prefix:\n%s", latched)
	}
	if strings.Contains(latched, "saved query") {
		t.Errorf("the footer still offers keys the prefix has taken:\n%s", latched)
	}
}

func TestHelp_NamesEveryQueryTheDigitsRun(t *testing.T) {
	resetRegistry()
	t.Cleanup(resetRegistry)
	RegisterView(querySpec("issues", 1, &stubView{id: "issues"}))

	d := testDeps()
	d.Saved = twoQueries(t)
	m := newAt(t, d, 140, 30)
	m, _ = press(m, "?")

	overlay := ansi.Strip(m.Frame())
	for _, want := range []string{"Blockers", "Mine", "switch view", "saved query"} {
		if !strings.Contains(overlay, want) {
			t.Errorf("the help overlay does not mention %q:\n%s", want, overlay)
		}
	}
}

func TestFooter_OffersNoDigitsWhenNothingIsBound(t *testing.T) {
	resetRegistry()
	t.Cleanup(resetRegistry)
	RegisterView(querySpec("issues", 1, &stubView{id: "issues"}))

	m := newAt(t, testDeps(), 140, 30)
	if footer := lastLine(ansi.Strip(m.Frame())); strings.Contains(footer, "saved query") {
		t.Errorf("the footer offers a saved query on a profile that has none:\n%s", footer)
	}
}

func TestFooter_RepaintsWhenAQueryTakesAKey(t *testing.T) {
	resetRegistry()
	t.Cleanup(resetRegistry)
	RegisterView(querySpec("issues", 1, &stubView{id: "issues"}))

	m := newAt(t, testDeps(), 140, 30)
	_ = m.Frame()

	next, _ := m.Update(BindQueryMsg{Name: "Blockers", JQL: "priority = Highest", Slot: 2})
	m = next.(Model)
	if footer := lastLine(ansi.Strip(m.Frame())); !strings.Contains(footer, "saved query") {
		t.Errorf("the footer did not repaint after a key was bound:\n%s", footer)
	}
	if !strings.Contains(ansi.Strip(m.Frame()), `2 runs "Blockers"`) {
		t.Errorf("binding a key said nothing:\n%s", ansi.Strip(m.Frame()))
	}
}

func TestBindQuery_TakesTheKeyFromWhateverHeldItAndSaysSo(t *testing.T) {
	resetRegistry()
	t.Cleanup(resetRegistry)
	issues := &stubView{id: "issues"}
	RegisterView(querySpec("issues", 1, issues))

	var written []app.SavedQuery
	d := testDeps()
	d.Saved = twoQueries(t)
	d.SaveQueries = func(q app.SavedQueries) error {
		written = q.All()
		return nil
	}
	m := newAt(t, d, 140, 30)

	next, cmd := m.Update(BindQueryMsg{Name: "Shipped", JQL: `status = "Shipped"`, Slot: 3})
	m = next.(Model)
	for _, msg := range run(cmd) {
		if status, ok := msg.(StatusMsg); ok && status.Level == LevelError {
			t.Errorf("saving reported %q", status.Text)
		}
	}

	if got := ansi.Strip(m.Frame()); !strings.Contains(got, `3 runs "Shipped" instead of "Mine"`) {
		t.Errorf("the user was not told what the key stopped doing:\n%s", got)
	}
	if len(written) != 3 {
		t.Fatalf("the profile was written with %d queries, want 3", len(written))
	}
	if q, ok := m.deps.Saved.BySlot(3); !ok || q.Name != "Shipped" {
		t.Errorf("3 runs %+v, want the query just bound", q)
	}
	for _, q := range written {
		if q.Name == "Mine" && q.Slot != 0 {
			t.Errorf("the query that lost the key kept it: %+v", q)
		}
	}

	issues.seen = nil
	m, _ = press(m, "3")
	if !saw(issues, `query:status = "Shipped"`) {
		t.Errorf("the key does not run what was just bound to it: %v", issues.seen)
	}
}

func TestBindQuery_ReportsWhatWentWrongWithoutTakingTheKeyAway(t *testing.T) {
	tests := map[string]struct {
		save    func(app.SavedQueries) error
		bind    BindQueryMsg
		want    string
		bound   bool
		isError bool
	}{
		"a session with no profile keeps the key for itself": {
			bind:  BindQueryMsg{Name: "Shipped", JQL: `status = "Shipped"`, Slot: 2},
			want:  "there is no profile to save it to",
			bound: true,
		},
		"a failed write is reported, not swallowed": {
			save:    func(app.SavedQueries) error { return errors.New("the config file is read-only") },
			bind:    BindQueryMsg{Name: "Shipped", JQL: `status = "Shipped"`, Slot: 2},
			want:    "the config file is read-only",
			bound:   true,
			isError: true,
		},
		"a key outside the range is refused in app's own words": {
			bind: BindQueryMsg{Name: "Shipped", JQL: `status = "Shipped"`, Slot: 12},
			want: "the keys are 1 to 9",
		},
		"a query saved on no key at all says so": {
			bind: BindQueryMsg{Name: "Shipped", JQL: `status = "Shipped"`},
			want: `"Shipped" is saved, on no key`,
		},
		"a query with no JQL is refused": {
			bind: BindQueryMsg{Name: "Shipped", Slot: 2},
			want: "has no JQL to run",
		},
	}

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			resetRegistry()
			t.Cleanup(resetRegistry)
			RegisterView(querySpec("issues", 1, &stubView{id: "issues"}))

			d := testDeps()
			d.SaveQueries = tc.save
			m := newAt(t, d, 140, 30)

			next, cmd := m.Update(tc.bind)
			m = next.(Model)
			text := ansi.Strip(m.Frame())
			for _, msg := range run(cmd) {
				if status, ok := msg.(StatusMsg); ok {
					text += "\n" + status.Text
					if tc.isError && status.Level != LevelError {
						t.Errorf("a failed write was reported at level %v", status.Level)
					}
				}
			}
			if !strings.Contains(text, tc.want) {
				t.Errorf("nothing said %q:\n%s", tc.want, text)
			}
			if _, bound := m.deps.Saved.BySlot(tc.bind.Slot); bound != tc.bound {
				t.Errorf("the key is bound=%v, want %v", bound, tc.bound)
			}
		})
	}
}

func TestBindQuery_TellsEveryViewTheSetChanged(t *testing.T) {
	resetRegistry()
	t.Cleanup(resetRegistry)
	issues := &stubView{id: "issues"}
	board := &stubView{id: "board"}
	RegisterView(querySpec("issues", 1, issues))
	RegisterView(spec("board", 2, "", board))

	m := newAt(t, testDeps(), 140, 30)
	m, _ = press(m, "g", "2")
	m, _ = press(m, "g", "1")
	issues.seen, board.seen = nil, nil

	_, _ = m.Update(BindQueryMsg{Name: "Shipped", JQL: `status = "Shipped"`, Slot: 2})
	if !saw(issues, "saved:1") {
		t.Errorf("the focused view was not told: %v", issues.seen)
	}
	if !saw(board, "saved:1") {
		t.Errorf("a parked root view was not told: %v", board.seen)
	}
}

func equalOrder(seen, want []string) bool {
	at := 0
	for _, s := range seen {
		if at < len(want) && s == want[at] {
			at++
		}
	}
	return at == len(want)
}
