package ui

import (
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/varijkapil13/saral/internal/ui/backlog"
	"github.com/varijkapil13/saral/internal/ui/board"
	"github.com/varijkapil13/saral/internal/ui/comment"
	"github.com/varijkapil13/saral/internal/ui/issue"
	"github.com/varijkapil13/saral/internal/ui/kernel"
	"github.com/varijkapil13/saral/internal/ui/list"
	"github.com/varijkapil13/saral/internal/ui/release"
	"github.com/varijkapil13/saral/pkg/jira"
)

// The slot allocation only exists here: the kernel may not import a view and a
// view package sees only itself, so this is the only place the overlay behind the
// prefix can be drawn the way a user meets it.
func destinationsOf(t *testing.T, caps jira.Capabilities, w, h int) string {
	t.Helper()

	d := depsFor(t)
	d.Caps = caps
	m, err := kernel.New(d, kernel.WithSize(w, h),
		kernel.WithInitialView(list.ViewID), kernel.WithMouse(false))
	if err != nil {
		t.Fatalf("kernel.New: %v", err)
	}
	next, _ := m.Update(tea.WindowSizeMsg{Width: w, Height: h})
	next, _ = next.(kernel.Model).Update(tea.KeyPressMsg{Code: 'g', Text: "g"})
	return ansi.Strip(next.(kernel.Model).Frame())
}

func everything() jira.Capabilities {
	ok := jira.Capability{OK: true}
	return jira.Capabilities{
		Plans: ok, BulkMove: ok, Boards: ok, Attachments: ok, DeleteIssues: ok, People: ok,
		TimeZone: time.UTC,
	}
}

// Every view that claimed a digit is named, and its digit with it. The list is
// the registry's, so a view added to a slot appears here without this test being
// told about it — which is the half the footer gave up at eighty columns.
func TestDestinations_NameEveryViewThatClaimedADigit(t *testing.T) {
	sweepEnv(t)

	frame := destinationsOf(t, everything(), 120, 38)
	slots := 0
	for _, spec := range kernel.Views() {
		if spec.Slot == 0 {
			continue
		}
		slots++
		if !strings.Contains(frame, spec.Title) {
			t.Errorf("slot %d holds %q and the overlay does not name it:\n%s", spec.Slot, spec.Title, frame)
		}
		if gesture := kernel.SlotGesture(spec.Slot); !strings.Contains(frame, gesture) {
			t.Errorf("the overlay does not say %q reaches %q:\n%s", gesture, spec.Title, frame)
		}
	}
	if slots == 0 {
		t.Fatal("no view in this build claims a digit, so this is checking nothing")
	}
}

// A token that cannot see the boards is the common shape of this, and the answer
// is the site's own sentence on the row rather than a row that is not there.
func TestDestinations_CarryTheReasonAViewIsOutOfReach(t *testing.T) {
	sweepEnv(t)

	const reason = "Boards need a Jira Software project, and this token cannot see one"
	caps := everything()
	caps.Boards = jira.Capability{Reason: reason}

	frame := destinationsOf(t, caps, 120, 38)
	if !strings.Contains(frame, reason) {
		t.Errorf("the overlay does not carry the reason the boards are out of reach:\n%s", frame)
	}
	for _, spec := range kernel.Views() {
		if spec.Slot == 0 || spec.Requires != jira.CapBoards {
			continue
		}
		if !strings.Contains(frame, spec.Title) {
			t.Errorf("%q was left out rather than answered with a reason:\n%s", spec.Title, frame)
		}
	}
}

// A session that has probed nothing knows nothing about this site, which is a
// different answer from a probe that came back without the capability — and the
// overlay is where a user meets it.
func TestDestinations_SayWhenNothingHasBeenCheckedYet(t *testing.T) {
	sweepEnv(t)

	frame := destinationsOf(t, jira.Capabilities{}, 120, 38)
	if !strings.Contains(frame, "has been checked on this site yet") {
		t.Errorf("an unprobed session reads as a refusal rather than as an unknown:\n%s", frame)
	}
}

// The overlay adds three keys to the latched prefix, and it may only add ones no
// view answers behind it. A view that spelt a gesture on one of them would teach
// a stroke the overlay has since taken, so this fails the build rather than
// leaving the two to disagree quietly.
//
// A label lists the things one binding answers to — "↑/k", "G / g e" — so each
// alternative is asked separately.
func TestDestinations_TakeNoKeyAViewSpellsBehindThePrefix(t *testing.T) {
	lead := kernel.DefaultGlobalKeys().Go.Help().Key + " "
	taken := []string{"up", "down", "k", "j", "enter", "↑", "↓", "↑/k", "↓/j"}
	scopes := kernel.KeyScopes()
	if len(scopes) == 0 {
		t.Fatal("no view registered keys, so this is checking nothing")
	}
	for _, scope := range scopes {
		shown, _ := labelsOf(kernel.KeysFor(scope))
		for _, label := range shown {
			for alt := range strings.SplitSeq(label, "/") {
				rest, prefixed := strings.CutPrefix(strings.TrimSpace(alt), lead)
				if !prefixed {
					continue
				}
				if slices.Contains(taken, strings.TrimSpace(rest)) {
					t.Errorf("%s teaches %q, and the destination overlay answers %q behind the prefix itself",
						scope, label, strings.TrimSpace(rest))
				}
			}
		}
	}
}

// secondStrokes is what each view's own dispatcher answers behind the prefix,
// which only that view knows: the kernel sees a key set and not a switch. Every
// view that spells any gesture on the prefix has to appear, so the next one to
// spend g fails this until somebody has said which strokes it takes.
var secondStrokes = map[string][]string{
	list.ViewID:    {"g", "e"},
	issue.ViewID:   {"g", "e"},
	comment.ViewID: {"g", "e"},
	board.ViewID:   {"g", "e"},
	backlog.ViewID: {"g"},
	release.ViewID: {"g"},
}

// A gesture is taught only if a binding spells it, so one a view answers and
// nothing spells is a stroke neither this overlay nor ? can tell the user about.
// ge was that stroke in four views until each spelt it on the binding it lands on.
func TestDestinations_TeachTheSecondStrokeOfEveryGestureAViewAnswers(t *testing.T) {
	lead := kernel.DefaultGlobalKeys().Go.Help().Key + " "
	for _, scope := range kernel.KeyScopes() {
		shown, _ := labelsOf(kernel.KeysFor(scope))
		spelt := make(map[string]bool, len(shown))
		for _, label := range shown {
			for alt := range strings.SplitSeq(label, "/") {
				if rest, ok := strings.CutPrefix(strings.TrimSpace(alt), lead); ok {
					spelt[strings.TrimSpace(rest)] = true
				}
			}
		}
		second, named := secondStrokes[scope]
		if len(spelt) > 0 && !named {
			t.Errorf("%s spells a gesture on %q and secondStrokes does not say what it answers behind "+
				"the prefix, so nothing holds it to teaching them", scope, strings.TrimSpace(lead))
		}
		if !named {
			continue
		}
		if len(spelt) == 0 {
			t.Errorf("secondStrokes names %s, which spells no gesture on %q at all",
				scope, strings.TrimSpace(lead))
		}
		for _, stroke := range second {
			if !spelt[stroke] {
				t.Errorf("%s answers %q and no binding of its spells it, so neither the destination "+
					"overlay nor ? can teach it; it shows %v", scope, lead+stroke, shown)
			}
		}
	}
}

func TestDestinations_Golden(t *testing.T) {
	sweepEnv(t)

	caps := everything()
	denied := everything()
	denied.Boards = jira.Capability{Reason: "Boards need a Jira Software project, and this token cannot see one"}

	for _, tc := range []struct {
		name string
		caps jira.Capabilities
		w, h int
	}{
		{"everything within reach at 120", caps, 120, 38},
		{"everything within reach at 80", caps, 80, 20},
		{"the boards out of reach at 80", denied, 80, 20},
		{"nothing probed yet at 120", jira.Capabilities{}, 120, 38},
	} {
		t.Run(tc.name, func(t *testing.T) {
			name := "destinations_" + strings.ReplaceAll(tc.name, " ", "_") + "_" +
				strconv.Itoa(tc.w) + "x" + strconv.Itoa(tc.h) + ".golden"
			golden(t, name, destinationsOf(t, tc.caps, tc.w, tc.h))
		})
	}
}
