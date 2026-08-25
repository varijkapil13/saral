package list

import (
	"errors"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/varijkapil13/saral/internal/ui/kernel"
	"github.com/varijkapil13/saral/pkg/jira"
	"github.com/varijkapil13/saral/pkg/jira/jiratest"
)

// movingClock is a clock a test can wind on, so that what a refresh leaves in
// the summary line can be held against what was there before it.
func movingClock(start time.Time) (now func() time.Time, wind func(time.Duration)) {
	at := new(atomic.Int64)
	at.Store(start.UnixNano())
	return func() time.Time { return time.Unix(0, at.Load()).UTC() },
		func(d time.Duration) { at.Add(int64(d)) }
}

func TestRefresh_SaysNothingChangedRatherThanNothingAtAll(t *testing.T) {
	t.Parallel()

	dr := newDriver(t, testDeps(asAda(40)), 120, 30)
	dr.key("j", "j", "j")
	under, cursor, top := dr.m.selectedKey(), dr.m.cursor, dr.m.top

	dr.send(kernel.RefreshMsg{})

	status := dr.lastStatus()
	if status.Level != kernel.LevelInfo {
		t.Errorf("a refresh that worked was reported at level %d", status.Level)
	}
	mustContain(t, status.Text, "refreshed", "nothing has changed")
	if !strings.Contains(status.Text, "issues") {
		t.Errorf("the line reads %q and does not say how much came back", status.Text)
	}
	switch {
	case dr.m.cursor != cursor:
		t.Errorf("the refresh moved the cursor to %d, want %d", dr.m.cursor, cursor)
	case dr.m.top != top:
		t.Errorf("the refresh moved the scroll to %d, want %d", dr.m.top, top)
	case dr.m.selectedKey() != under:
		t.Errorf("the refresh left the cursor on %s, want %s", dr.m.selectedKey(), under)
	}
}

func TestRefresh_SaysWhatChangedWhenSomethingDid(t *testing.T) {
	t.Parallel()

	f := asAda(40)
	dr := newDriver(t, testDeps(f), 120, 30)
	under := dr.m.selectedKey()
	if under == "" {
		t.Fatal("there is no row to change")
	}

	summary := "Renamed by somebody else"
	if err := f.UpdateIssue(t.Context(), under, jira.IssuePatch{Summary: &summary}); err != nil {
		t.Fatalf("UpdateIssue: %v", err)
	}
	dr.send(kernel.RefreshMsg{})

	status := dr.lastStatus()
	mustContain(t, status.Text, "refreshed", "1 changed")
	mustNotContain(t, status.Text, "nothing has changed")
	mustContain(t, dr.view(), summary)
}

func TestRefresh_ReportsAFailureAsAFailedRefreshAndKeepsTheRows(t *testing.T) {
	t.Parallel()

	f := asAda(40)
	dr := newDriver(t, testDeps(f), 120, 30)
	before := len(dr.m.issues)

	f.FailNext(&jira.TransportError{Op: "search", Err: errors.New("dial tcp: no such host")})
	dr.send(kernel.RefreshMsg{})

	status := dr.lastStatus()
	if status.Level != kernel.LevelError {
		t.Errorf("a refusal was reported at level %d, want an error", status.Level)
	}
	mustContain(t, status.Text, "the refresh failed", "no such host")
	if got := len(dr.m.issues); got != before {
		t.Errorf("a failed refresh left %d of %d rows", got, before)
	}
	mustContain(t, dr.view(), staleLabel)
}

func TestRefresh_APurgeSaysSomethingDifferentFromARefresh(t *testing.T) {
	t.Parallel()

	dr := newDriver(t, testDeps(asAda(40)), 120, 30)

	dr.send(kernel.RefreshMsg{})
	plain := dr.lastStatus().Text
	dr.send(kernel.RefreshMsg{Purge: true})
	purged := dr.lastStatus().Text

	if plain == purged {
		t.Fatalf("r and R both say %q, so one cannot be told from the other", plain)
	}
	mustContain(t, plain, "refreshed")
	mustContain(t, purged, "refetched")
}

func TestRefresh_APurgeThatFailsSaysWhichOfTheTwoItWas(t *testing.T) {
	t.Parallel()

	f := asAda(40)
	dr := newDriver(t, testDeps(f), 120, 30)

	f.FailNextN(4, &jira.RateLimitError{RetryAfter: 30 * time.Second})
	dr.send(kernel.RefreshMsg{Purge: true})

	status := dr.lastStatus()
	if status.Level != kernel.LevelError {
		t.Errorf("a refused purge was reported at level %d, want an error", status.Level)
	}
	mustContain(t, status.Text, "the refetch failed", "retry in 30s")
}

func TestRefresh_LeavesTheTimeItLandedInTheSummaryLine(t *testing.T) {
	t.Parallel()

	now, wind := movingClock(time.Date(2025, time.March, 5, 9, 0, 0, 0, time.UTC))
	d := testDeps(asAda(12))
	d.Now = now
	dr := newDriver(t, d, 120, 30)

	mustContain(t, dr.view(), "checked 09:00")

	wind(37 * time.Minute)
	dr.send(kernel.RefreshMsg{})

	mustContain(t, dr.view(), "checked 09:37")
	mustNotContain(t, dr.view(), "checked 09:00")
}

func TestRefresh_RowsOffDiskAreNotClaimedToHaveBeenChecked(t *testing.T) {
	t.Parallel()

	cache := newFakeCache()
	deps := withCache(testDeps(asAda(12)), cache)
	jql, _ := defaultQuery(deps.Project)
	cache.hold(jql, storedRows(4), false, false)

	view, ok := New(deps).(*Model)
	if !ok {
		t.Fatal("New did not return a *Model")
	}
	next, _ := view.Update(kernel.SizeMsg{Width: 120, Height: 20})
	m, _ := next.(*Model)

	mustNotContain(t, m.View(), "checked")
}

// Paging and polling are not refreshes, and a line that narrated them would be
// noise on every scroll and on every tick.
func TestRefresh_ScrollingOntoAnotherPageSaysNothing(t *testing.T) {
	t.Parallel()

	dr := openAll(t, testDeps(newFake(60, jiratest.WithPageSize(20))), 120, 30)
	dr.statuses = nil

	for range 12 {
		dr.key("j")
	}

	if len(dr.m.issues) <= 20 {
		t.Fatal("nothing was paged, so this proves nothing")
	}
	for _, status := range dr.statuses {
		if strings.Contains(status.Text, "refreshed") || strings.Contains(status.Text, "refetched") {
			t.Errorf("paging reported itself as a refresh: %q", status.Text)
		}
	}
}

func TestRefresh_APollSaysNothing(t *testing.T) {
	t.Parallel()

	dr := newDriver(t, testDeps(asAda(40)), 120, 30)
	dr.statuses = nil

	dr.send(pollMsg{gen: dr.m.gen})

	for _, status := range dr.statuses {
		if strings.Contains(status.Text, "refreshed") || strings.Contains(status.Text, "refetched") {
			t.Errorf("a poll nobody asked for reported itself: %q", status.Text)
		}
	}
	if dr.m.checked.IsZero() {
		t.Error("a poll that landed left no record of when it did")
	}
}

func TestRefresh_RevalidatingRowsOffDiskSaysNothing(t *testing.T) {
	t.Parallel()

	cache := newFakeCache()
	deps := withCache(testDeps(asAda(12)), cache)
	jql, _ := defaultQuery(deps.Project)
	cache.hold(jql, storedRows(4), true, false)

	dr := newDriver(t, deps, 120, 20)

	for _, status := range dr.statuses {
		if strings.Contains(status.Text, "refreshed") {
			t.Errorf("a revalidation nobody asked for reported itself: %q", status.Text)
		}
	}
}

func TestRefreshed_DistinguishesEveryOutcomeItCanReport(t *testing.T) {
	t.Parallel()

	at := time.Date(2025, time.March, 5, 9, 0, 0, 0, time.UTC)
	rows := func(spec ...string) []jira.Issue {
		out := make([]jira.Issue, 0, len(spec))
		for i, s := range spec {
			key, when := s, at
			if k, bump, ok := strings.Cut(s, "@"); ok {
				key = k
				when = at.Add(time.Duration(len(bump)) * time.Hour)
			}
			out = append(out, jira.Issue{Key: key, Updated: when, Summary: strconv.Itoa(i)})
		}
		return out
	}

	tests := []struct {
		name         string
		w            why
		before       []jira.Issue
		after        []jira.Issue
		want, unwant string
	}{
		{
			name: "nothing moved", w: whyRefresh,
			before: rows("A", "B"), after: rows("A", "B"),
			want: "refreshed: nothing has changed, still 2 issues",
		},
		{
			name: "one row was touched", w: whyRefresh,
			before: rows("A", "B"), after: rows("A", "B@x"),
			want: "refreshed: 1 changed, now 2 issues",
		},
		{
			name: "one arrived and one left", w: whyRefresh,
			before: rows("A", "B"), after: rows("A", "C"),
			want: "refreshed: 1 new, 1 gone, now 2 issues",
		},
		{
			name: "all three at once", w: whyRefresh,
			before: rows("A", "B"), after: rows("A@x", "C", "D"),
			want: "refreshed: 2 new, 1 changed, 1 gone, now 3 issues",
		},
		{
			name: "the answer is still nothing", w: whyRefresh,
			before: nil, after: nil,
			want: "refreshed: still nothing matches this search",
		},
		{
			name: "an answer where there was none", w: whyRefresh,
			before: nil, after: rows("A"),
			want: "refreshed: 1 issue",
		},
		{
			name: "everything went away", w: whyRefresh,
			before: rows("A", "B"), after: nil,
			want: "refreshed: still nothing matches this search",
		},
		{
			name: "a purge names itself", w: whyPurge,
			before: rows("A"), after: rows("A"),
			want: "refetched from scratch: nothing has changed, still 1 issue",
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			cmd := refreshed(tc.w, tc.before, tc.after)
			if cmd == nil {
				t.Fatalf("%s reported nothing at all", tc.name)
			}
			status, ok := cmd().(kernel.StatusMsg)
			if !ok {
				t.Fatalf("the report is a %T, not a status line", cmd())
			}
			if status.Text != tc.want {
				t.Errorf("the line reads %q, want %q", status.Text, tc.want)
			}
		})
	}
}

// The frame a refresh that changed nothing leaves behind, which is the frame
// that read as a broken key: the status line names the outcome and the summary
// line keeps the time it was checked.
func TestRefresh_QuietOutcomeGolden(t *testing.T) {
	t.Parallel()

	for name, size := range map[string]struct{ w, h int }{
		"120x30": {120, 30},
		"80x20":  {80, 20},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			m := start(t, testDeps(asAda(12)), size.w, size.h)
			m = keys(t, m, "r")
			golden(t, "list_refreshed_"+name+".golden", frame(m))
		})
	}
}

func TestRefreshed_SaysNothingForAFetchNobodyAskedFor(t *testing.T) {
	t.Parallel()

	rows := []jira.Issue{{Key: "A"}}
	for _, w := range []why{whyOpen, whyPage, whyBackground} {
		if cmd := refreshed(w, nil, rows); cmd != nil {
			t.Errorf("a fetch of kind %d narrated itself: %v", w, cmd())
		}
	}
}
