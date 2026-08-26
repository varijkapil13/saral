package issue

import (
	"fmt"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/varijkapil13/saral/internal/ui/kernel"
	"github.com/varijkapil13/saral/pkg/jira"
	"github.com/varijkapil13/saral/pkg/jira/jiratest"
)

// rowsViewID is a root view standing in for the issue list. The list is what
// pushes the detail pane in the program, and this package may not import it, so
// the kernel is given a root to push over and to pop back to. It takes the slot
// docs/UX.md gives the issue list, so the footer these tests read is the one the
// program draws rather than one with nothing on its left.
const rowsViewID = "issue.rows"

type rowsView struct{}

func (rowsView) Init() tea.Cmd                         { return nil }
func (rowsView) Update(tea.Msg) (kernel.View, tea.Cmd) { return rowsView{}, nil }
func (rowsView) View() string                          { return "the rows this issue was opened from" }

func init() {
	kernel.RegisterView(kernel.ViewSpec{
		ID:    rowsViewID,
		Title: "Issues",
		Slot:  1,
		New:   func(kernel.Deps) kernel.View { return rowsView{} },
	})
}

// session drives the whole program: the kernel, the global keys, the detail pane
// pushed the way a list pushes it, and whatever that pane pushes in turn.
type session struct {
	t *testing.T
	m kernel.Model

	// held keeps what a command answered with instead of delivering it, which is
	// how a read still out when something is pushed over the view that asked for
	// it is arranged. Delivery is the kernel's decision and it has to be made
	// after the push, not before.
	holding bool
	held    []tea.Msg
}

func boot(t *testing.T, d kernel.Deps, seed jira.Issue, w, h int) *session {
	t.Helper()

	s := bootRoot(t, d, w, h)
	s.send(kernel.PushMsg{ID: ViewID, Title: seed.Key, View: New(d, seed)})
	return s
}

// bootRoot is the program with the list standing in and nothing pushed over it.
func bootRoot(t *testing.T, d kernel.Deps, w, h int) *session {
	t.Helper()

	m, err := kernel.New(d, kernel.WithSize(w, h), kernel.WithInitialView(rowsViewID), kernel.WithMouse(false))
	if err != nil {
		t.Fatalf("kernel.New: %v", err)
	}
	s := &session{t: t, m: m}
	s.send(tea.WindowSizeMsg{Width: w, Height: h})
	s.run(s.m.Init())
	return s
}

func (s *session) hold() { s.holding = true }

// release hands everything that was held to the kernel, in the order it came
// back, and lets the kernel decide where each of them goes.
func (s *session) release() {
	s.t.Helper()

	held := s.held
	s.holding, s.held = false, nil
	for _, msg := range held {
		s.send(msg)
	}
}

func (s *session) send(msg tea.Msg) {
	s.t.Helper()

	next, cmd := s.m.Update(msg)
	model, ok := next.(kernel.Model)
	if !ok {
		s.t.Fatal("the kernel stopped returning its own model")
	}
	s.m = model
	s.run(cmd)
}

func (s *session) run(cmd tea.Cmd) {
	s.t.Helper()

	queue := []tea.Cmd{cmd}
	for steps := 0; len(queue) > 0; steps++ {
		if steps > 4000 {
			s.t.Fatal("commands never settled")
		}
		next := queue[0]
		queue = queue[1:]
		if next == nil {
			continue
		}
		msg := next()
		if msg == nil {
			continue
		}
		if cmds, ok := unwrapCmds(msg); ok {
			queue = append(queue, cmds...)
			continue
		}
		if s.holding {
			s.held = append(s.held, msg)
			continue
		}
		updated, follow := s.m.Update(msg)
		model, ok := updated.(kernel.Model)
		if !ok {
			s.t.Fatal("the kernel stopped returning its own model")
		}
		s.m = model
		queue = append(queue, follow)
	}
}

func (s *session) press(keys ...string) {
	s.t.Helper()

	for _, k := range keys {
		s.send(stroke(k))
	}
}

func (s *session) frame() string { return ansi.Strip(s.m.Frame()) }

func (s *session) footer() string {
	lines := strings.Split(strings.TrimRight(s.frame(), "\n"), "\n")
	return lines[len(lines)-1]
}

func (s *session) title() string { return strings.SplitN(s.frame(), "\n", 2)[0] }

// cover stands in for anything the kernel pushes over the pane — the palette is
// the one a user meets, and this package may not import it. What matters is that
// it is on top and that it ignores every message a detail pane would have taken.
type cover struct{}

func (cover) Init() tea.Cmd                         { return nil }
func (cover) Update(tea.Msg) (kernel.View, tea.Cmd) { return cover{}, nil }
func (cover) View() string                          { return "something else entirely" }

// Through the real kernel: the pane asks, something is pushed over it while the
// answer is still out, and the answer arrives to find the pane no longer on top.
// It belongs to the pane either way.
//
// Nothing here hands the pane its own answer. The kernel is given the message
// the read produced and decides where it goes, which is the step that was
// missing when the pane recovered by asking again.
func TestSession_AnAnswerLandingWhileThePaneIsCoveredStillReachesThePane(t *testing.T) {
	t.Parallel()

	f := newFake(20)
	seed := seedOf(t, f, "PROJ-8")
	s := bootRoot(t, testDeps(f), 120, 30)

	s.hold()
	s.send(kernel.PushMsg{ID: ViewID, Title: seed.Key, View: New(testDeps(f), seed)})
	s.send(kernel.PushMsg{ID: "cover", Title: "Cover", View: cover{}})
	mustContain(t, s.frame(), "something else entirely")

	reads := countCalls(f, "Search")
	s.release()
	s.send(kernel.PopMsg{})

	// The description, and not the summary: the summary was in the row the pane
	// was built from and is on screen whether or not the read ever landed.
	mustContain(t, s.frame(), "Filed against PROJ-8.")
	if got := countCalls(f, "Search") - reads; got != 0 {
		t.Errorf("coming back read the issue again %d times; the answer to the first read was delivered", got)
	}
}

func countCalls(f *jiratest.Fake, name string) int {
	n := 0
	for _, call := range f.Calls() {
		if call == name {
			n++
		}
	}
	return n
}

// The gesture through the whole program: the thread takes the whole screen over
// the issue, and esc lands back on the issue rather than walking past it.
func TestSession_TheThreadOpensOverTheIssueAndEscComesBack(t *testing.T) {
	t.Parallel()

	f := newFake(12)
	addComment(t, f, "PROJ-2", "The conversation you came for.")
	s := boot(t, testDeps(f), seedOf(t, f, "PROJ-2"), 120, 30)
	mustContain(t, s.title(), "PROJ-2")
	// The sidebar already holds the thread, so the gesture is about the size it
	// can be written at rather than about opening it at all.
	mustContain(t, s.frame(), "The conversation you came for.")

	s.press("C")
	mustContain(t, s.frame(), "The conversation you came for.")
	mustContain(t, s.title(), "PROJ-2")
	mustContain(t, s.footer(), "a write")

	s.press("esc")
	mustContain(t, s.frame(), "PROJ-2", "The conversation you came for.")
	mustContain(t, s.footer(), "C comment")
	mustNotContain(t, s.footer(), "a write")

	s.press("esc")
	mustContain(t, s.frame(), "the rows this issue was opened from")
}

// docs/UX.md principle 2: the footer names C where it gives the thread the whole
// screen, and does not name it in the thread, where the keys are the thread's
// own.
func TestSession_TheFooterNamesTheKeyOnlyWhereItOpensTheThread(t *testing.T) {
	t.Parallel()

	f := newFake(12)
	addComment(t, f, "PROJ-3", "Something to read.")
	s := boot(t, testDeps(f), seedOf(t, f, "PROJ-3"), 120, 30)
	mustContain(t, s.footer(), "C comment")

	s.press("C")
	mustContain(t, s.footer(), "a write")
	mustNotContain(t, s.footer(), "C comment")

	s.press("esc")
	mustContain(t, s.footer(), "C comment")
}

// The help overlay is the other half of the footer, and answers for the view it
// is covering.
func TestSession_TheHelpOverlayListsTheKeyForTheViewItIsCovering(t *testing.T) {
	t.Parallel()

	f := newFake(12)
	s := boot(t, testDeps(f), seedOf(t, f, "PROJ-4"), 120, 30)

	// The overlay pads the key column to its widest entry, so what is asserted
	// here is the description only the overlay has room for.
	s.press("?")
	mustContain(t, s.frame(), "edit fields", "change status", "next pane")

	s.press("?", "C", "?")
	mustContain(t, s.frame(), "write a comment")
	mustNotContain(t, s.frame(), "edit fields", "next pane")
}

// A comment written full screen is in the sidebar when you come back, because
// the sidebar holds the very model that was written in.
func TestSession_WhatIsWrittenInTheThreadIsThereWhenYouComeBack(t *testing.T) {
	t.Parallel()

	f := newFake(12)
	s := boot(t, testDeps(f), seedOf(t, f, "PROJ-5"), 120, 30)
	mustContain(t, s.frame(), "Nobody has commented on PROJ-5")

	s.press("C")
	s.press("a")
	for _, r := range "Written without leaving the issue." {
		s.send(tea.KeyPressMsg{Code: r, Text: string(r)})
	}
	s.press("ctrl+s")
	s.press("esc")

	mustContain(t, s.frame(), "Written without leaving")
	mustNotContain(t, s.frame(), "Nobody has commented")
}

// The row is one line shared by three cells, and the smallest terminal
// docs/UX.md supports is where it has to give something up. Every action this pane
// offers survives there, and so does every way out. This build registers no
// palette, so ctrl+k is honestly absent from the globals.
func TestSession_TheSmallestTerminalStillTeachesThePanesKeys(t *testing.T) {
	t.Parallel()

	f := newFake(12)
	s := boot(t, testDeps(f), seedOf(t, f, "PROJ-7"), kernel.MinWidth, kernel.MinHeight)

	mustContain(t, s.footer(), "Issues", "tab pane", "e edit", "t status", "C comment", "? esc")
	mustNotContain(t, s.footer(), "…")
}

func TestSession_Frames(t *testing.T) {
	t.Parallel()

	// An unsent comment lives on disk, keyed by the issue it is about, so each
	// size composes on an issue of its own: four parallel subtests writing one
	// draft file would each be typing into the last one's.
	for _, size := range []struct {
		w, h int
		key  string
	}{{120, 38, "PROJ-9"}, {100, 28, "PROJ-10"}, {90, 28, "PROJ-11"}, {80, 20, "PROJ-12"}} {
		t.Run(fmt.Sprintf("%dx%d", size.w, size.h), func(t *testing.T) {
			t.Parallel()

			f := newFake(12)
			addComment(t, f, size.key, "A comment worth a line or two, and a second sentence to wrap.")
			s := boot(t, testDeps(f), seedOf(t, f, size.key), size.w, size.h)
			golden(t, fmt.Sprintf("session_issue_%dx%d.golden", size.w, size.h), s.frame())

			s.press("C")
			golden(t, fmt.Sprintf("session_thread_%dx%d.golden", size.w, size.h), s.frame())

			s.press("a")
			for _, r := range "Half of what I meant to say" {
				s.send(tea.KeyPressMsg{Code: r, Text: string(r)})
			}
			golden(t, fmt.Sprintf("session_compose_%dx%d.golden", size.w, size.h), s.frame())
		})
	}
}
