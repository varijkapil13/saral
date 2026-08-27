package ui

import (
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"
	zone "github.com/lrstanley/bubblezone/v2"

	"github.com/varijkapil13/saral/internal/ui/attach"
	"github.com/varijkapil13/saral/internal/ui/kernel"
	"github.com/varijkapil13/saral/internal/ui/list"
	"github.com/varijkapil13/saral/internal/ui/move"
	"github.com/varijkapil13/saral/pkg/adf"
	"github.com/varijkapil13/saral/pkg/jira"
	"github.com/varijkapil13/saral/pkg/jira/jiratest"
)

// program is the whole thing running: the kernel, every view this build
// registered, and the real command palette on ctrl+k.
//
// Nothing here hands a view a message. Everything goes to the kernel and the
// kernel decides where it belongs, which is the step these tests are about — the
// step a test that delivers an answer to the view itself skips over.
type program struct {
	t *testing.T
	m kernel.Model

	// deps is what the program was built with, kept so a case can push a view the
	// registry cannot build: one needs an issue, the other the issues to move.
	deps kernel.Deps

	// held keeps a view's own answers back instead of delivering them. An answer
	// that has already landed cannot be covered by anything, so this is the only
	// way to arrange the case: the answer waits, ctrl+k puts the palette over the
	// view that asked, and only then is the kernel given the message.
	//
	// What the kernel acts on itself goes through, because those are the gestures
	// that build the situation rather than the answers under test.
	holding bool
	held    []tea.Msg
}

func boot(t *testing.T, d kernel.Deps, root string, w, h int) *program {
	t.Helper()

	m, err := kernel.New(d, kernel.WithSize(w, h), kernel.WithInitialView(root), kernel.WithMouse(false))
	if err != nil {
		t.Fatalf("kernel.New: %v", err)
	}
	p := &program{t: t, m: m, deps: d}
	p.send(tea.WindowSizeMsg{Width: w, Height: h})
	return p
}

func (p *program) hold() { p.holding = true }

// release hands the kernel everything that was held, in the order it came back.
func (p *program) release() {
	p.t.Helper()

	held := p.held
	p.holding, p.held = false, nil
	for _, msg := range held {
		p.send(msg)
	}
}

// gestures are what the kernel acts on rather than what a view is waiting for.
// Holding a push would mean the view under test never opens.
func gesture(msg tea.Msg) bool {
	switch msg.(type) {
	case kernel.PushMsg, kernel.PopMsg, kernel.OpenMsg, kernel.RunCommandMsg,
		kernel.BroadcastMsg, kernel.StatusMsg:
		return true
	default:
		return false
	}
}

func (p *program) send(msg tea.Msg) {
	p.t.Helper()

	next, cmd := p.m.Update(msg)
	m, ok := next.(kernel.Model)
	if !ok {
		p.t.Fatal("the kernel stopped returning its own model")
	}
	p.m = m
	p.run(cmd)
}

func (p *program) run(cmd tea.Cmd) {
	p.t.Helper()

	queue := []tea.Cmd{cmd}
	for steps := 0; len(queue) > 0; steps++ {
		if steps > 8000 {
			p.t.Fatal("commands never settled")
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
		if batch, ok := msg.(tea.BatchMsg); ok {
			queue = append(queue, batch...)
			continue
		}
		if p.holding && !gesture(msg) {
			p.held = append(p.held, msg)
			continue
		}
		updated, follow := p.m.Update(msg)
		m, ok := updated.(kernel.Model)
		if !ok {
			p.t.Fatal("the kernel stopped returning its own model")
		}
		p.m = m
		queue = append(queue, follow)
	}
}

func (p *program) press(keys ...string) {
	p.t.Helper()

	for _, k := range keys {
		p.send(stroke(k))
	}
}

func (p *program) frame() string { return ansi.Strip(p.m.Frame()) }

func stroke(s string) tea.KeyPressMsg {
	switch s {
	case "enter":
		return tea.KeyPressMsg{Code: tea.KeyEnter}
	case "esc":
		return tea.KeyPressMsg{Code: tea.KeyEscape}
	case "ctrl+k":
		return tea.KeyPressMsg{Code: 'k', Mod: tea.ModCtrl}
	default:
		return tea.KeyPressMsg{Code: rune(s[0]), Text: s}
	}
}

// replyFake is a project with a thread on the issue these tests open, so that
// the sidebar has an answer of its own to wait for.
func replyFake(t *testing.T) *jiratest.Fake {
	t.Helper()

	f := jiratest.New(
		jiratest.WithProject("PROJ", jiratest.Scrum),
		jiratest.WithIssues(jiratest.Gen(12)),
	)
	body := adf.NewDoc(adf.NewNode("paragraph", adf.NewText(threadLine)))
	if _, err := f.AddComment(t.Context(), "PROJ-12", body); err != nil {
		t.Fatalf("seeding the thread: %v", err)
	}
	return f
}

// threadLine is what the sidebar draws once the thread's own read has landed,
// and nothing draws before it.
const threadLine = "The conversation you came for."

func replyDeps(f *jiratest.Fake) kernel.Deps {
	ok := jira.Capability{OK: true}
	return kernel.Deps{
		Jira: f,
		Caps: jira.Capabilities{
			Plans: ok, BulkMove: ok, Boards: ok, Attachments: ok,
			DeleteIssues: ok, People: ok, TimeZone: time.UTC,
		},
		Project: "PROJ",
		Theme:   kernel.NewTheme(kernel.ThemeNoColor, true, kernel.ASCIIGlyphs()),
		Zones:   zone.New(),
		Site:    "example.atlassian.net",
		Now:     func() time.Time { return time.Date(2026, time.March, 5, 9, 0, 0, 0, time.UTC) },
	}
}

// palettePrompt is what the palette draws where nothing has been typed, and how
// these tests know it really is the palette that is covering the view.
const palettePrompt = "what do you want to do"

// A view's own answer, through the kernel and the real palette. Every one of
// these views asks the site for something and can have ctrl+k pressed over it
// while the answer is still out. The answer belongs to whichever view asked.
//
// arrive is driven while the answers are held, so the view opens with its read
// genuinely in flight. Every case asserts the view has nothing yet, so that a
// read which quietly completed early cannot make one of these pass.
func TestReply_AnAnswerLandingUnderThePaletteStillReachesTheViewThatAskedForIt(t *testing.T) {
	for _, tc := range []struct {
		name string
		// settle gets the program to where the gesture can be made, with every
		// answer up to that point delivered.
		settle func(*program)
		arrive func(*program)
		want   []string
		gone   []string
	}{
		{
			name:   "the issue list, which is a root and is covered where it stands",
			settle: func(*program) {},
			arrive: func(p *program) { p.run(p.m.Init()) },
			want:   []string{"PROJ-12", "Retire the nightly export"},
		},
		{
			name:   "the detail pane, pushed over the list",
			settle: func(p *program) { p.run(p.m.Init()) },
			arrive: func(p *program) { p.press("enter") },
			want:   []string{"Filed against PROJ-12."},
		},
		{
			name:   "the field editor, pushed over the pane",
			settle: func(p *program) { p.run(p.m.Init()) },
			arrive: func(p *program) { p.press("enter", "e") },
			want:   []string{"2025-03-16"},
			gone:   []string{"not read"},
		},
		{
			name:   "the transition picker, pushed over the pane",
			settle: func(p *program) { p.run(p.m.Init()) },
			arrive: func(p *program) { p.press("enter", "t") },
			want:   []string{"Move to Building"},
		},
		{
			name:   "the filter picker, pushed over the list",
			settle: func(p *program) { p.run(p.m.Init()) },
			arrive: func(p *program) { p.press("f", "enter") },
			want:   []string{"Ada Lovelace"},
		},
		{
			name:   "the create form, pushed by a command",
			settle: func(p *program) { p.run(p.m.Init()) },
			arrive: func(p *program) { p.send(kernel.RunCommandMsg{ID: "issue.create"}) },
			want:   []string{"Defect"},
		},
		{
			// The thread the sidebar draws is a model inside the pane and never an
			// entry on the stack, so the kernel cannot deliver to it at all. Its
			// answer names the pane after itself, and the pane hands it on.
			name:   "the comment thread the pane draws beside the description",
			settle: func(p *program) { p.run(p.m.Init()) },
			arrive: func(p *program) { p.press("enter") },
			want:   []string{threadLine},
		},
		{
			// The same thread, lent to the kernel for the whole screen. Now it is
			// an entry, and the answer goes straight to it rather than through the
			// pane underneath.
			name:   "the comment thread lent to the kernel for the whole screen",
			settle: func(p *program) { p.run(p.m.Init()) },
			arrive: func(p *program) { p.press("enter", "C") },
			want:   []string{threadLine},
		},
		{
			// The six views a footer slot reaches are switched to as roots rather
			// than pushed, so each is covered where it stands and the palette is
			// the only thing on the stack above it.
			name:   "the board, switched to as a root",
			settle: func(p *program) { p.run(p.m.Init()) },
			arrive: func(p *program) { p.send(kernel.RunCommandMsg{ID: "board.open"}) },
			want:   []string{"In Progress", "3 columns"},
			gone:   []string{"Asking which boards"},
		},
		{
			name:   "the backlog, switched to as a root",
			settle: func(p *program) { p.run(p.m.Init()) },
			arrive: func(p *program) { p.send(kernel.RunCommandMsg{ID: "backlog.open"}) },
			want:   []string{"2 open sprints"},
			gone:   []string{"Reading the board"},
		},
		{
			name:   "the sprints, switched to as a root",
			settle: func(p *program) { p.run(p.m.Init()) },
			arrive: func(p *program) { p.send(kernel.RunCommandMsg{ID: "sprints.open"}) },
			want:   []string{"Make it usable"},
			gone:   []string{"Asking the site"},
		},
		{
			name:   "the versions list, switched to as a root",
			settle: func(p *program) { p.run(p.m.Init()) },
			arrive: func(p *program) { p.send(kernel.RunCommandMsg{ID: "releases.open"}) },
			want:   []string{"The one being worked on"},
			gone:   []string{"Reading the versions"},
		},
		{
			name:   "the timeline, switched to as a root",
			settle: func(p *program) { p.run(p.m.Init()) },
			arrive: func(p *program) { p.send(kernel.RunCommandMsg{ID: "timeline.open"}) },
			want:   []string{"12 of 12 dated"},
			gone:   []string{"Reading the fields"},
		},
		{
			// The plans view draws the profile's own before it asks, so what proves
			// the site's answer landed is the row saying where it came from.
			name:   "the plans, switched to as a root",
			settle: func(p *program) { p.run(p.m.Init()) },
			arrive: func(p *program) { p.send(kernel.RunCommandMsg{ID: "plans.open"}) },
			want:   []string{"read from the site"},
			gone:   []string{"Asking the site for its plans"},
		},
		{
			// Neither of the last two registers a view spec, so the case pushes it
			// the way the view holding the issue does.
			name:   "the attachment pane, pushed with the issue it is about",
			settle: func(p *program) { p.run(p.m.Init()) },
			arrive: func(p *program) {
				p.send(kernel.PushMsg{ID: attach.ViewID, Title: "Files",
					View: attach.New(p.deps, attach.WithIssue("PROJ-12"))})
			},
			want: []string{"Nothing is attached to PROJ-12."},
			gone: []string{"Reading what is attached"},
		},
		{
			name:   "the move wizard, pushed with the issues it is about",
			settle: func(p *program) { p.run(p.m.Init()) },
			arrive: func(p *program) {
				p.send(kernel.PushMsg{ID: move.ViewID, Title: "Move",
					View: move.New(p.deps, move.WithIssues([]jira.Issue{{ID: "10012", Key: "PROJ-12"}}))})
			},
			want: []string{"which project"},
			gone: []string{"Asking the site"},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			sweepEnv(t)

			p := boot(t, replyDeps(replyFake(t)), list.ViewID, 120, 40)
			tc.settle(p)

			p.hold()
			tc.arrive(p)
			mustLack(t, p.frame(), tc.want...)

			p.press("ctrl+k")
			mustHave(t, p.frame(), palettePrompt)

			p.release()
			p.press("esc")

			mustHave(t, p.frame(), tc.want...)
			mustLack(t, p.frame(), tc.gone...)
		})
	}
}

// The other half: an answer for a view that has been thrown away goes nowhere.
// The kernel has no view to give it to, and handing it to whatever is on top is
// what put an issue's fields into somebody else's pane.
func TestReply_AnAnswerForAViewThatIsGoneIsDroppedRatherThanGivenToTheTop(t *testing.T) {
	sweepEnv(t)

	p := boot(t, replyDeps(replyFake(t)), list.ViewID, 120, 40)
	p.run(p.m.Init())

	p.hold()
	p.press("enter", "t")
	p.press("esc", "esc")
	mustHave(t, p.frame(), "Retire the nightly export")

	p.release()
	mustLack(t, p.frame(), "Move to Building")
	mustHave(t, p.frame(), "Retire the nightly export")
}

func mustHave(t *testing.T, frame string, want ...string) {
	t.Helper()

	for _, w := range want {
		if !strings.Contains(frame, w) {
			t.Errorf("the frame does not carry %q:\n%s", w, frame)
		}
	}
}

func mustLack(t *testing.T, frame string, gone ...string) {
	t.Helper()

	for _, g := range gone {
		if strings.Contains(frame, g) {
			t.Errorf("the frame still carries %q:\n%s", g, frame)
		}
	}
}
