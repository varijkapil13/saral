package comment

import (
	"strconv"
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"

	"github.com/varijkapil13/saral/internal/ui/kernel"
	"github.com/varijkapil13/saral/pkg/adf"
	"github.com/varijkapil13/saral/pkg/jira"
	"github.com/varijkapil13/saral/pkg/jira/jiratest"
)

// boxes are the three boxes this view has to be the same code in: a sidebar
// narrow enough that a comment body has thirty cells, the half-screen a divider
// usually lands on, and a whole screen.
var boxes = []struct {
	name          string
	width, height int
}{
	{name: "a sidebar", width: 34, height: 24},
	{name: "half a screen", width: 60, height: 24},
	{name: "a whole screen", width: 120, height: 24},
}

// richBody is a comment holding the things markdown would put on screen as
// punctuation: a heading, a bold run, a link, and a code block wider than any of
// the boxes.
func richBody() adf.Doc {
	return adf.NewDoc(
		adf.NewNode("heading", adf.NewText("What the log said")).WithAttrs(adf.Attrs{"level": 2}),
		adf.NewNode("paragraph",
			adf.NewText("The total is ").WithMarks(adf.Mark{Type: "strong"}),
			adf.NewText("wrong").WithMarks(adf.Mark{Type: "strong"}),
			adf.NewText(" past the rounding step, see "),
			adf.NewText("the note").WithMarks(adf.Mark{
				Type:  "link",
				Attrs: adf.Attrs{"href": "https://example.atlassian.net/wiki/x"},
			}),
			adf.NewText("."),
		),
		adf.NewNode("codeBlock", adf.NewText(
			"func total(rows []Row) Money { return sum(rows).Round(2) } // the rounding that lies",
		)).WithAttrs(adf.Attrs{"language": "go"}),
	)
}

func withComment(t *testing.T, body adf.Doc) *jiratest.Fake {
	t.Helper()

	f := newFake(3)
	if _, err := f.AddComment(t.Context(), "PROJ-1", body); err != nil {
		t.Fatalf("AddComment: %v", err)
	}
	return f
}

// One layout, at any box a sidebar or a screen can be: every frame is exactly as
// tall as the box, no line is wider than it, and the thread is above whatever
// answers it rather than replaced by it.
func TestThread_TheSameModelLaysOutASidebarAndAWholeScreen(t *testing.T) {
	t.Parallel()

	for _, box := range boxes {
		for _, state := range []struct {
			name string
			keys []string
		}{
			{name: "reading"},
			{name: "composing", keys: []string{"a"}},
			{name: "confirming a delete", keys: []string{"d"}},
		} {
			t.Run(box.name+", "+state.name, func(t *testing.T) {
				t.Parallel()

				f := newFake(3)
				comment(t, f, "PROJ-1", "Reproduced on staging.")
				dr := newDriver(t, testDeps(t, f), "PROJ-1", box.width, box.height)
				dr.key(state.keys...)

				lines := strings.Split(dr.view(), "\n")
				if len(lines) != box.height {
					t.Errorf("the frame is %d lines in a box %d tall", len(lines), box.height)
				}
				for i, line := range lines {
					if got := ansi.StringWidth(line); got > box.width {
						t.Errorf("line %d is %d cells wide in a box %d wide: %q",
							i, got, box.width, line)
					}
				}
				lay := dr.m.layout()
				if lay.head+lay.thread+lay.prompt+lay.composer != box.height {
					t.Errorf("%+v does not divide a box of %d", lay, box.height)
				}
				if !strings.Contains(lines[0], "PROJ-1") {
					t.Errorf("the box does not open by saying which issue it is about: %q", lines[0])
				}
				if lay.thread == 0 {
					t.Error("nothing of the thread is on screen, so a comment is being written blind")
				}
				if at := strings.Index(dr.view(), "Reproduced on staging."); at < 0 {
					t.Error("the comment being answered is not on screen")
				}
			})
		}
	}
}

// The composer's height rule is what makes a sidebar and a screen the same
// layout, so it is checked through the model rather than only on the arithmetic:
// the draft that fits in a 120-cell row wraps three times in a 34-cell one, and
// the composer grows by exactly that much.
func TestThread_TheComposerGrowsWithTheDraftAtEveryWidth(t *testing.T) {
	t.Parallel()

	const draft = "A reply long enough that it wraps more than once in a narrow sidebar and keeps going."

	for _, box := range boxes {
		t.Run(box.name, func(t *testing.T) {
			t.Parallel()

			f := newFake(3)
			comment(t, f, "PROJ-1", "Reproduced on staging.")
			dr := newDriver(t, testDeps(t, f), "PROJ-1", box.width, box.height)
			dr.key("a")
			dr.typeText(draft)

			lay := dr.m.layout()
			if want := composerHeight(dr.m.composerLines, box.height); lay.composer != want {
				t.Errorf("the composer is %d rows, want composerHeight(%d, %d) = %d",
					lay.composer, dr.m.composerLines, box.height, want)
			}
			if lay.composer > max(box.height/2, 3) {
				t.Errorf("the composer took %d of a box %d tall", lay.composer, box.height)
			}
			if lay.editor < dr.m.editor.Height() {
				t.Errorf("the composer draws %d rows of a draft the widget lays out in %d, so the cursor can sit off screen",
					lay.editor, dr.m.editor.Height())
			}
			// Whatever the row count, the whole draft is readable: the words at
			// the start of it are on screen along with the words at the end.
			mustContain(t, dr.view(), "A reply long enough", "keeps going.")
		})
	}
}

// A draft longer than the box stops at half of it and gives the rest back to the
// thread, at every width, which is what stops a comment being written on a
// screen that hides what it answers.
func TestThread_ALongDraftStopsAtHalfTheBox(t *testing.T) {
	t.Parallel()

	for _, box := range boxes {
		t.Run(box.name, func(t *testing.T) {
			t.Parallel()

			f := newFake(3)
			comment(t, f, "PROJ-1", "Reproduced on staging.")
			dr := newDriver(t, testDeps(t, f), "PROJ-1", box.width, box.height)
			dr.key("a")
			for i := range 40 {
				dr.typeText("line " + strconv.Itoa(i))
				dr.send(keyPress("enter"))
			}

			lay := dr.m.layout()
			if want := max(box.height/2, 3); lay.composer != want {
				t.Errorf("a forty-line draft took %d rows of a box %d tall, want %d",
					lay.composer, box.height, want)
			}
			if lay.thread < box.height/2-2 {
				t.Errorf("the thread was left %d rows of a box %d tall", lay.thread, box.height)
			}
			if got := len(strings.Split(dr.view(), "\n")); got != box.height {
				t.Errorf("the frame is %d lines in a box %d tall", got, box.height)
			}
		})
	}
}

// A body richtext lays out wider than the box — a code block is never wrapped,
// because wrapping corrupts what a reader is about to copy — is cut at the box
// and says so, and the pan keys reach the rest of it. Nothing is cut silently.
func TestThread_AWideCodeBlockIsCutWhereItSaysSoAndPannedTo(t *testing.T) {
	t.Parallel()

	f := withComment(t, richBody())
	dr := newDriver(t, testDeps(t, f), "PROJ-1", 34, 14)

	ell := dr.m.deps.Theme.Glyphs.Ellipsis
	first := dr.view()
	if !strings.Contains(first, "func total(rows []Row)") {
		t.Error("the code block does not start on screen")
	}
	if !strings.Contains(first, ell) {
		t.Errorf("a line wider than the box was cut with no marker saying so:\n%s", first)
	}
	if b := dr.m.blockAt(0); b.wide <= b.width {
		t.Errorf("the block reports a widest line of %d in a box %d wide, so nothing is pannable",
			b.wide, b.width)
	}

	dr.key("l")
	if dr.m.pan == 0 {
		t.Fatal("the pan key moved nothing")
	}
	// Panning stops at the widest line in the window rather than running off
	// into empty cells, so the whole of the code block is reachable and no more.
	var panned string
	for steps := 0; ; steps++ {
		at := dr.m.pan
		panned = dr.view()
		dr.key("l")
		if dr.m.pan == at {
			break
		}
		if steps > 20 {
			t.Fatal("panning right never stopped")
		}
	}
	if !strings.Contains(panned, "the rounding that lies") {
		t.Errorf("panning did not reach the end of the code block:\n%s", panned)
	}
	// The prose around it does not slide: it fits, and moving it would take the
	// author and the date of the comment being read off the left edge.
	mustContain(t, panned, "Sam Tester", "What the log said")

	for steps := 0; dr.m.pan > 0; steps++ {
		dr.key("h")
		if steps > 20 {
			t.Fatal("panning left never came home")
		}
	}
	if got := dr.view(); got != first {
		t.Errorf("panning out and back did not come home:\n--- was ---\n%s\n--- now ---\n%s", first, got)
	}
	for i, line := range strings.Split(panned, "\n") {
		if got := ansi.StringWidth(line); got > 34 {
			t.Errorf("panned line %d is %d cells wide in a 34 column box", i, got)
		}
	}
}

// A body written out as markdown puts ## and ** and [text](url) on screen as
// punctuation. It is drawn as styled text instead, at every width.
func TestThread_DrawsARichBodyAsStyledTextAndNotAsMarkdown(t *testing.T) {
	t.Parallel()

	for _, box := range boxes {
		t.Run(box.name, func(t *testing.T) {
			t.Parallel()

			f := withComment(t, richBody())
			dr := newDriver(t, testDeps(t, f), "PROJ-1", box.width, box.height)

			got := dr.view()
			mustContain(t, got, "What the log said", "wrong")
			mustNotContain(t, got, "**", "## ", "](https://")
			// The styling itself survives: in the no-colour theme emphasis is
			// bold rather than a colour, and it is still there.
			if !strings.Contains(dr.m.View(), "\x1b[1m") {
				t.Error("nothing in the body is emphasised, so the marks were dropped rather than drawn")
			}
		})
	}
}

// The confirmation quotes the comment back through the same renderer, so a
// heading is not quoted with its hashes on.
func TestThread_TheDeleteConfirmationQuotesWordsAndNotMarkdown(t *testing.T) {
	t.Parallel()

	for _, box := range boxes {
		t.Run(box.name, func(t *testing.T) {
			t.Parallel()

			f := withComment(t, richBody())
			dr := newDriver(t, testDeps(t, f), "PROJ-1", box.width, box.height)
			dr.key("d")

			if dr.m.mode != confirming {
				t.Fatal("d did not ask")
			}
			prompt := strings.Join(dr.m.prompt, "\n")
			mustContain(t, ansi.Strip(prompt), "delete")
			mustNotContain(t, ansi.Strip(prompt), "**", "## ")
			// The key that goes ahead is named wherever the box put it.
			mustContain(t, ansi.Strip(prompt), "y")
		})
	}
}

// The box changes size when the divider moves, when the terminal resizes and
// when this same instance goes from a sidebar to the whole screen. Every memo
// keyed on width has to invalidate, and the text has to still be there.
func TestThread_AResizeMidComposeKeepsTheTextAndRelaysTheBox(t *testing.T) {
	t.Parallel()

	const draft = "Half a thought that has to survive the divider moving."

	f := withComment(t, richBody())
	dr := newDriver(t, testDeps(t, f), "PROJ-1", 34, 24)
	dr.key("a")
	dr.typeText(draft)

	narrow := dr.m.composerLines
	if narrow < 2 {
		t.Fatalf("the draft occupies %d rows in a 34 column box, expected it to wrap", narrow)
	}

	for _, box := range boxes {
		dr.send(kernel.SizeMsg{Width: box.width, Height: box.height})

		if got := dr.m.editor.Value(); got != draft {
			t.Fatalf("in %s the composer holds %q", box.name, got)
		}
		if dr.m.mode != writing {
			t.Fatalf("in %s the composer closed", box.name)
		}
		lines := strings.Split(dr.view(), "\n")
		if len(lines) != box.height {
			t.Errorf("in %s the frame is %d lines, want %d", box.name, len(lines), box.height)
		}
		for i, line := range lines {
			if got := ansi.StringWidth(line); got > box.width {
				t.Errorf("in %s line %d is %d cells wide: %q", box.name, i, got, line)
			}
		}
		if want := composerHeight(dr.m.composerLines, box.height); dr.m.layout().composer != want {
			t.Errorf("in %s the composer is %d rows, want %d", box.name, dr.m.layout().composer, want)
		}
		mustContain(t, dr.view(), "Half a thought")
	}

	// Widening it unwraps the draft, which is the memo that would have gone
	// stale: the same text takes fewer rows in a wider box.
	if wide := dr.m.composerLines; wide >= narrow {
		t.Errorf("the draft takes %d rows at 120 columns and %d at 34; the width memo did not move",
			wide, narrow)
	}
	// The body memo moved with it: the code block that had to be cut at 34
	// columns fits a 120 column box whole.
	if b := dr.m.blockAt(0); b.width != 120 {
		t.Errorf("the comment is still laid out for a box %d wide", b.width)
	}
}

// A comment is typed with the keys the kernel would otherwise spend on itself. A
// view that does not claim the keyboard reports success over text with its
// digits eaten.
func TestThread_ACommentKeepsTheKeysTheKernelWouldHaveTaken(t *testing.T) {
	t.Parallel()

	const draft = "q quits, 1 and 2 pick views, and ? opens help — none of that belongs here."

	f := newFake(3)
	dr := newDriver(t, testDeps(t, f), "PROJ-1", 34, 24)
	dr.key("a")
	if !dr.m.WantsRawKeys() {
		t.Fatal("the composer does not claim the keyboard while it has focus")
	}
	dr.typeText(draft)

	if got := dr.m.editor.Value(); got != draft {
		t.Errorf("the composer holds %q, want %q", got, draft)
	}
	dr.key("ctrl+s")

	stored, err := jira.Collect(t.Context(), mustComments(t, f, "PROJ-1"), 0)
	if err != nil {
		t.Fatalf("reading back: %v", err)
	}
	if len(stored) != 1 {
		t.Fatalf("the site holds %d comments, want 1", len(stored))
	}
	if got := adf.Markdown(stored[0].Body); got != draft {
		t.Errorf("the site holds %q, want %q", got, draft)
	}
}

// A thread built before an issue is known must not ask the site for one. A pane
// that embeds this builds it first and names the issue after.
func TestThread_InitAsksForNothingUntilThereIsAnIssue(t *testing.T) {
	t.Parallel()

	f := newFake(3)
	comment(t, f, "PROJ-1", "Reproduced on staging.")
	m := Thread(testDeps(t, f), "")
	if cmd := m.Init(); cmd != nil {
		t.Error("a thread with no issue asked the site for one anyway")
	}
	if got := countCalls(f, "Comments"); got != 0 {
		t.Errorf("the site was asked for comments %d times before an issue was named", got)
	}

	dr := &driver{t: t, m: m}
	dr.send(kernel.SizeMsg{Width: 34, Height: 24})
	dr.send(ThreadMsg{Key: "PROJ-1"})

	if !dr.m.loaded {
		t.Fatal("naming the issue did not read the thread")
	}
	mustContain(t, dr.view(), "Reproduced on staging.")

	// Being named the same issue again does not throw the reader's place away.
	before := countCalls(f, "Comments")
	dr.send(ThreadMsg{Key: "PROJ-1"})
	if got := countCalls(f, "Comments"); got != before {
		t.Errorf("the thread was read again on being told the issue it already had (%d then %d)",
			before, got)
	}
}

func TestThread_GoldenAtEveryBox(t *testing.T) {
	t.Parallel()

	for _, box := range boxes {
		for _, state := range []struct {
			name string
			keys []string
		}{
			{name: "reading"},
			{name: "composing", keys: []string{"a"}},
			{name: "confirming", keys: []string{"d"}},
		} {
			name := "box_" + state.name + "_" + strconv.Itoa(box.width) + "x" +
				strconv.Itoa(box.height) + ".golden"
			t.Run(name, func(t *testing.T) {
				t.Parallel()

				f := withComment(t, richBody())
				comment(t, f, "PROJ-1", "Fix is behind a flag until the migration lands.")
				dr := newDriver(t, testDeps(t, f), "PROJ-1", box.width, box.height)
				dr.key(state.keys...)
				if len(state.keys) > 0 && state.keys[0] == "a" {
					dr.typeText("Rounding is done in the adapter, not here.")
				}

				golden(t, name, dr.view())
			})
		}
	}
}
