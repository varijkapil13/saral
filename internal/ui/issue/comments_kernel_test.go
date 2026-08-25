package issue

import (
	"fmt"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/varijkapil13/saral/internal/ui/kernel"
	"github.com/varijkapil13/saral/pkg/jira"
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
}

func boot(t *testing.T, d kernel.Deps, seed jira.Issue, w, h int) *session {
	t.Helper()

	m, err := kernel.New(d, kernel.WithSize(w, h), kernel.WithInitialView(rowsViewID), kernel.WithMouse(false))
	if err != nil {
		t.Fatalf("kernel.New: %v", err)
	}
	s := &session{t: t, m: m}
	s.send(tea.WindowSizeMsg{Width: w, Height: h})
	s.run(s.m.Init())
	s.send(kernel.PushMsg{ID: ViewID, Title: seed.Key, View: New(d, seed)})
	return s
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

// The gesture through the whole program: the thread opens over the issue, and
// esc lands back on the issue rather than walking past it.
func TestSession_TheThreadOpensOverTheIssueAndEscComesBack(t *testing.T) {
	t.Parallel()

	f := newFake(12)
	addComment(t, f, "PROJ-2", "The conversation you came for.")
	s := boot(t, testDeps(f), seedOf(t, f, "PROJ-2"), 120, 30)
	mustContain(t, s.title(), "PROJ-2")

	s.press("c")
	mustContain(t, s.frame(), "The conversation you came for.")
	mustContain(t, s.title(), "PROJ-2")
	mustContain(t, s.footer(), "a write")

	s.press("esc")
	mustContain(t, s.frame(), "PROJ-2", "Comments (1)")
	mustContain(t, s.footer(), "c comment")
	mustNotContain(t, s.footer(), "a write")

	s.press("esc")
	mustContain(t, s.frame(), "the rows this issue was opened from")
}

// docs/UX.md principle 2: the footer names c where it opens the thread, and does
// not name it in the thread, where c is the thread's own key for writing one.
func TestSession_TheFooterNamesTheKeyOnlyWhereItOpensTheThread(t *testing.T) {
	t.Parallel()

	f := newFake(12)
	addComment(t, f, "PROJ-3", "Something to read.")
	s := boot(t, testDeps(f), seedOf(t, f, "PROJ-3"), 120, 30)
	mustContain(t, s.footer(), "c comment")

	s.press("c")
	mustContain(t, s.footer(), "a write")
	mustNotContain(t, s.footer(), "c comment")

	s.press("esc")
	mustContain(t, s.footer(), "c comment")
}

// The help overlay is the other half of the footer, and answers for the view it
// is covering.
func TestSession_TheHelpOverlayListsTheKeyForTheViewItIsCovering(t *testing.T) {
	t.Parallel()

	f := newFake(12)
	s := boot(t, testDeps(f), seedOf(t, f, "PROJ-4"), 120, 30)

	s.press("?")
	mustContain(t, s.frame(), "c comment")

	s.press("?", "c", "?")
	mustContain(t, s.frame(), "a write a comment")
	mustNotContain(t, s.frame(), "c comment")
}

// A comment written from the issue reaches the read-only thread the detail pane
// draws, because coming back refetches what the pane is showing.
func TestSession_WhatIsWrittenInTheThreadIsThereWhenYouComeBack(t *testing.T) {
	t.Parallel()

	f := newFake(12)
	s := boot(t, testDeps(f), seedOf(t, f, "PROJ-5"), 120, 30)
	mustContain(t, s.frame(), "Nobody has commented.")

	s.press("c")
	s.press("a")
	for _, r := range "Written without leaving the issue." {
		s.send(tea.KeyPressMsg{Code: r, Text: string(r)})
	}
	s.press("ctrl+s")
	s.press("esc")
	s.press("r")

	mustContain(t, s.frame(), "Comments (1)", "Written without leaving the issue.")
}

// The row is one line shared by three cells, and the smallest terminal
// docs/UX.md supports is where it has to give something up. Every action this pane
// offers survives there, and so does every way out. This build registers no
// palette, so ctrl+k is honestly absent from the globals.
func TestSession_TheSmallestTerminalStillTeachesThePanesKeys(t *testing.T) {
	t.Parallel()

	f := newFake(12)
	s := boot(t, testDeps(f), seedOf(t, f, "PROJ-7"), kernel.MinWidth, kernel.MinHeight)

	mustContain(t, s.footer(), "Issues", "e edit", "t status", "c comment", "? esc")
	mustNotContain(t, s.footer(), "…")
}

func TestSession_Frames(t *testing.T) {
	t.Parallel()

	for _, size := range []struct{ w, h int }{{120, 38}, {100, 28}, {80, 20}} {
		t.Run(fmt.Sprintf("%dx%d", size.w, size.h), func(t *testing.T) {
			t.Parallel()

			f := newFake(12)
			addComment(t, f, "PROJ-6", "A comment worth a line or two, and a second sentence to wrap.")
			s := boot(t, testDeps(f), seedOf(t, f, "PROJ-6"), size.w, size.h)
			golden(t, fmt.Sprintf("session_issue_%dx%d.golden", size.w, size.h), s.frame())

			s.press("c")
			golden(t, fmt.Sprintf("session_thread_%dx%d.golden", size.w, size.h), s.frame())
		})
	}
}
