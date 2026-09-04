package list

import (
	"strings"
	"testing"

	"github.com/varijkapil13/saral/internal/app"
	"github.com/varijkapil13/saral/internal/ui/kernel"
	"github.com/varijkapil13/saral/pkg/jira"
)

const shippedJQL = `project = "PROJ" AND status = "Shipped" ORDER BY key`

// keys drives the whole program rather than the view, because what these tests
// are about is which of the two got the keystroke.
func keys(t *testing.T, m kernel.Model, strokes ...string) kernel.Model {
	t.Helper()
	for _, stroke := range strokes {
		m = send(t, m, keyPress(stroke))
	}
	return m
}

// selectedKey reads the issue under the cursor out of the drawn frame, which is
// the only place a test above the view can see it.
func selectedKey(frame string) string {
	for _, line := range strings.Split(frame, "\n") {
		if !strings.HasPrefix(line, "> ") {
			continue
		}
		if fields := strings.Fields(line); len(fields) > 1 {
			return fields[1]
		}
	}
	return ""
}

func savedDeps(t *testing.T, client jira.Client, queries ...app.SavedQuery) (kernel.Deps, *[]app.SavedQuery) {
	t.Helper()
	saved, err := app.NewSavedQueries(queries...)
	if err != nil {
		t.Fatalf("NewSavedQueries: %v", err)
	}
	d := testDeps(client)
	d.Saved = saved
	written := new([]app.SavedQuery)
	d.SaveQueries = func(q app.SavedQueries) error {
		*written = q.All()
		return nil
	}
	return d, written
}

func TestList_KeepsItsOwnGGesturesUnderTheKernelsPrefix(t *testing.T) {
	t.Parallel()

	m := startAll(t, testDeps(newFake(40)), 120, 30)
	if got := selectedKey(frame(m)); got != "PROJ-1" {
		t.Fatalf("the list opened on %q, want PROJ-1", got)
	}

	m = keys(t, m, "g", "5")
	if got := frame(m); !strings.Contains(got, "nothing is bound to 5") {
		t.Fatalf("g 5 was not the kernel's:\n%s", got)
	}

	m = keys(t, m, "g", "e")
	last := selectedKey(frame(m))
	if last == "PROJ-1" || last == "" {
		t.Errorf("g e left the cursor on %q; the view saw half a gesture the kernel had eaten", last)
	}

	m = keys(t, m, "g", "g")
	if got := selectedKey(frame(m)); got != "PROJ-1" {
		t.Errorf("g g left the cursor on %q, want PROJ-1", got)
	}
}

func TestList_RunsTheQueryTheKernelDispatchesFromANumberKey(t *testing.T) {
	t.Parallel()

	d, _ := savedDeps(t, newFake(20), app.SavedQuery{Name: "Shipped work", JQL: shippedJQL, Slot: 2})
	m := start(t, d, 120, 30)

	m = keys(t, m, "2")
	got := frame(m)
	mustContain(t, got, "Shipped work")
	if selectedKey(got) == "" {
		t.Errorf("the saved query brought no rows:\n%s", got)
	}
}

func TestList_BindsTheQueryOnScreenToANumberKeyAndKeepsIt(t *testing.T) {
	t.Parallel()

	d, written := savedDeps(t, newFake(20))
	m := startAll(t, d, 120, 30)

	m = keys(t, m, "S")
	mustContain(t, frame(m), `bind "All issues" to a key`, "any other key cancels")

	m = keys(t, m, "4")
	mustContain(t, frame(m), `4 runs "All issues"`)
	if len(*written) != 1 {
		t.Fatalf("the profile was written with %d queries, want 1", len(*written))
	}
	if q := (*written)[0]; q.Slot != 4 || q.JQL != allJQL || q.Name != "All issues" {
		t.Errorf("wrote %+v, want the query on screen bound to 4", q)
	}

	if footer := frame(m); !strings.Contains(footer, "saved query") {
		t.Errorf("the footer does not offer the key that was just bound:\n%s", footer)
	}
}

func TestList_ConfirmsBeforeTakingAKeyAnotherQueryHolds(t *testing.T) {
	t.Parallel()

	d, written := savedDeps(t, newFake(20), app.SavedQuery{Name: "Shipped work", JQL: shippedJQL, Slot: 2})
	m := startAll(t, d, 120, 30)

	m = keys(t, m, "S", "2")
	got := frame(m)
	mustContain(t, got, `2 runs "Shipped work"`, "y replaces it", "All issues")
	if len(*written) != 0 {
		t.Fatalf("the key changed hands before the confirmation: %+v", *written)
	}

	m = keys(t, m, "n")
	if len(*written) != 0 {
		t.Errorf("refusing the confirmation still rebound the key: %+v", *written)
	}

	m = keys(t, m, "S", "2", "y")
	if len(*written) != 2 {
		t.Fatalf("the profile was written with %d queries, want 2", len(*written))
	}
	mustContain(t, frame(m), `2 runs "All issues" instead of "Shipped work"`)
	for _, q := range *written {
		if q.Name == "Shipped work" && q.Slot != 0 {
			t.Errorf("the query that lost the key kept it: %+v", q)
		}
	}
}

func TestList_TakesTheDigitItselfWhileItIsPickingAKey(t *testing.T) {
	t.Parallel()

	d, _ := savedDeps(t, newFake(20), app.SavedQuery{Name: "Shipped work", JQL: shippedJQL, Slot: 2})
	m := startAll(t, d, 120, 30)

	m = keys(t, m, "S", "2")
	if got := frame(m); !strings.Contains(got, "All issues") {
		t.Errorf("the digit that was picking a key ran the query bound to it instead:\n%s", got)
	}
}

func TestList_TheSaveGestureIsReachableFromThePaletteAsWell(t *testing.T) {
	t.Parallel()

	d, written := savedDeps(t, newFake(20))
	m := startAll(t, d, 120, 30)

	m = send(t, m, kernel.BroadcastMsg{Msg: SaveQueryMsg{}})
	mustContain(t, frame(m), `bind "All issues" to a key`)

	keys(t, m, "6")
	if len(*written) != 1 || (*written)[0].Slot != 6 {
		t.Errorf("the command did not reach the same gesture the key does: %+v", *written)
	}
}

func TestList_ARejectedKeyEndsTheGestureRatherThanTrappingTheUser(t *testing.T) {
	t.Parallel()

	d, written := savedDeps(t, newFake(20))
	m := startAll(t, d, 120, 30)

	m = keys(t, m, "S", "x")
	if got := frame(m); strings.Contains(got, "to a key") {
		t.Errorf("a key that binds nothing left the gesture open:\n%s", got)
	}
	m = keys(t, m, "j")
	if got := selectedKey(frame(m)); got != "PROJ-2" {
		t.Errorf("the cursor is on %q, want PROJ-2: j was swallowed by a gesture nobody could see", got)
	}
	if len(*written) != 0 {
		t.Errorf("a cancelled gesture wrote something: %+v", *written)
	}
}

func TestList_BindPrompt_Golden(t *testing.T) {
	t.Parallel()

	for name, strokes := range map[string][]string{
		"pick":    {"S"},
		"confirm": {"S", "2"},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			d, _ := savedDeps(t, newFake(12), app.SavedQuery{Name: "Shipped work", JQL: shippedJQL, Slot: 2})
			dr := openAll(t, d, 120, 20)
			dr.key(strokes...)
			golden(t, "list_bind_"+name+"_120x20.golden", dr.view())
		})
	}
}
