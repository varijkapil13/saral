package comment

import (
	"bytes"
	"errors"
	"strconv"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/varijkapil13/saral/internal/ui/kernel"
	"github.com/varijkapil13/saral/pkg/adf"
	"github.com/varijkapil13/saral/pkg/jira"
	"github.com/varijkapil13/saral/pkg/jira/jiratest"
)

func TestThread_ReadsTheThreadAndLandsOnTheNewestComment(t *testing.T) {
	t.Parallel()

	f := newFake(3)
	comment(t, f, "PROJ-1", "The first thing anybody said.")
	comment(t, f, "PROJ-1", "The last thing anybody said.")

	dr := newDriver(t, testDeps(t, f), "PROJ-1", 100, 24)

	if got := len(dr.m.comments); got != 2 {
		t.Fatalf("the thread holds %d comments, want 2", got)
	}
	if dr.m.cursor != 1 {
		t.Errorf("the cursor landed on %d, want the newest comment", dr.m.cursor)
	}
	mustContain(t, dr.view(), "PROJ-1", "2 comments", "The last thing anybody said.", "Sam Tester")
}

func TestThread_WithoutAnIssueSaysSoRatherThanDrawingAnEmptyThread(t *testing.T) {
	t.Parallel()

	dr := newDriver(t, testDeps(t, newFake(3)), "", 100, 24)

	mustContain(t, dr.view(), "has not been told which")
	if dr.m.loaded {
		t.Error("a view with no issue went and read something anyway")
	}
}

func TestThread_PagesAsTheCursorReachesWhatIsLoaded(t *testing.T) {
	t.Parallel()

	f := newFake(3, jiratest.WithPageSize(5))
	for i := range 23 {
		comment(t, f, "PROJ-1", "comment number "+strconv.Itoa(i))
	}
	dr := newDriver(t, testDeps(t, f), "PROJ-1", 100, 24)

	// Opening reads a page, and the cursor landing on its last comment is
	// already inside the lookahead, so the page after it follows. Nothing more
	// is read until the cursor asks for it.
	if got := len(dr.m.comments); got != 10 {
		t.Fatalf("opening the thread read %d comments, want a page and the one behind it", got)
	}
	for range 10 {
		if !dr.m.page.HasMore() {
			break
		}
		dr.key("G")
	}
	if got := len(dr.m.comments); got != 23 {
		t.Fatalf("walking to the end stopped at %d comments, want all 23", got)
	}
	if dr.m.page.HasMore() {
		t.Error("the last page still claims another one after it")
	}
	dr.key("G")
	if dr.m.cursor != len(dr.m.comments)-1 {
		t.Errorf("the cursor is on %d after paging, want the newest comment", dr.m.cursor)
	}
}

func TestThread_MovesByCommentAndJumpsToEitherEnd(t *testing.T) {
	t.Parallel()

	f := newFake(3)
	for i := range 8 {
		comment(t, f, "PROJ-1", "comment number "+strconv.Itoa(i))
	}
	dr := newDriver(t, testDeps(t, f), "PROJ-1", 100, 24)

	dr.key("g", "g")
	if dr.m.cursor != 0 || dr.m.top != 0 {
		t.Errorf("g g left the cursor at %d and the window at %d", dr.m.cursor, dr.m.top)
	}
	dr.key("j")
	if dr.m.cursor != 1 {
		t.Errorf("j left the cursor at %d", dr.m.cursor)
	}
	dr.key("G")
	if want := len(dr.m.comments) - 1; dr.m.cursor != want {
		t.Errorf("G left the cursor at %d, want %d", dr.m.cursor, want)
	}
	dr.key("k")
	if want := len(dr.m.comments) - 2; dr.m.cursor != want {
		t.Errorf("k left the cursor at %d, want %d", dr.m.cursor, want)
	}
}

func TestThread_DrawsOnlyTheCommentsTheWindowHolds(t *testing.T) {
	t.Parallel()

	f := newFake(3)
	for i := range 60 {
		comment(t, f, "PROJ-1", "comment number "+strconv.Itoa(i))
	}
	dr := newDriver(t, testDeps(t, f), "PROJ-1", 100, 24)
	dr.key("g", "g")
	_ = dr.view()

	// Opening the thread drew the newest screenful and g g drew the oldest, so
	// two screens have been rendered out of sixty comments — and the third
	// screen the assertion below draws costs nothing, because the window has
	// not moved.
	drawn := len(dr.m.blocks.made)
	if drawn > 24 {
		t.Errorf("two screens rendered %d comments out of %d", drawn, len(dr.m.comments))
	}
	_ = dr.view()
	if got := len(dr.m.blocks.made); got != drawn {
		t.Errorf("drawing the same screen again rendered %d more comments", got-drawn)
	}
	mustNotContain(t, dr.view(), "comment number 40")
}

func TestThread_WheelScrollsTheThreadWithoutMovingTheSelection(t *testing.T) {
	t.Parallel()

	f := newFake(3)
	for i := range 30 {
		comment(t, f, "PROJ-1", "comment number "+strconv.Itoa(i))
	}
	dr := newDriver(t, testDeps(t, f), "PROJ-1", 100, 24)
	dr.key("g", "g")
	under := dr.m.cursor

	dr.send(tea.MouseWheelMsg{Button: tea.MouseWheelDown})

	if dr.m.top == 0 && dr.m.skip == 0 {
		t.Error("the wheel did not scroll")
	}
	if dr.m.cursor != under {
		t.Errorf("the wheel moved the selection to %d, want it left on %d", dr.m.cursor, under)
	}
}

// --- writing ----------------------------------------------------------------

func TestThread_WritesACommentTheSiteThenHolds(t *testing.T) {
	t.Parallel()

	f := newFake(3)
	dr := newDriver(t, testDeps(t, f), "PROJ-1", 100, 24)

	dr.key("a")
	if dr.m.mode != writing {
		t.Fatal("a did not open the editor")
	}
	if !dr.m.WantsRawKeys() {
		t.Error("the editor does not claim the keyboard, so q would quit out from under the typing")
	}
	dr.typeText("Reproduced on staging.")
	dr.key("ctrl+s")

	stored, err := jira.Collect(t.Context(), mustComments(t, f, "PROJ-1"), 0)
	if err != nil {
		t.Fatalf("reading back: %v", err)
	}
	if len(stored) != 1 {
		t.Fatalf("the site holds %d comments, want 1", len(stored))
	}
	if got := adf.Markdown(stored[0].Body); got != "Reproduced on staging." {
		t.Errorf("the site holds %q", got)
	}
	if dr.m.mode != browsing {
		t.Error("the editor is still open after the comment was sent")
	}
	if got := dr.m.cursor; got != 0 {
		t.Errorf("the cursor is on %d, want the comment that was just written", got)
	}
	mustContain(t, dr.view(), "Reproduced on staging.")
}

func TestThread_RefusesToSendNothing(t *testing.T) {
	t.Parallel()

	f := newFake(3)
	dr := newDriver(t, testDeps(t, f), "PROJ-1", 100, 24)

	dr.key("a")
	dr.typeText("   ")
	dr.key("ctrl+s")

	if dr.m.mode != writing {
		t.Error("the editor closed on a comment with nothing in it")
	}
	mustContain(t, dr.statusText(), "nothing here to send")
	if calls := countCalls(f, "AddComment"); calls != 0 {
		t.Errorf("the site was asked to store %d empty comments", calls)
	}
}

func TestThread_EditKeepsWhatMarkdownCannotCarry(t *testing.T) {
	t.Parallel()

	f := newFake(3)
	original := adf.NewDoc(
		adf.NewNode("paragraph",
			adf.NewText("thanks "),
			adf.NewNode("mention").WithAttrs(adf.Attrs{"id": "acct-someone", "text": "@Someone"}),
			adf.NewText(" for the fix"),
		),
		adf.NewNode("paragraph", adf.NewText("The second paragraph is the one being rewritten.")),
	)
	if _, err := f.AddComment(t.Context(), "PROJ-1", original); err != nil {
		t.Fatalf("AddComment: %v", err)
	}
	dr := newDriver(t, testDeps(t, f), "PROJ-1", 100, 24)

	dr.key("e")
	if dr.m.mode != writing || dr.m.editing == "" {
		t.Fatal("e did not open the editor on the comment under the cursor")
	}
	seeded := dr.m.editor.Value()
	if !strings.Contains(seeded, "@Someone") {
		t.Fatalf("the editor was seeded with %q", seeded)
	}
	dr.m.editor.SetValue(strings.Replace(seeded,
		"The second paragraph is the one being rewritten.",
		"The second paragraph has now been rewritten.", 1))
	dr.key("ctrl+s")

	stored, err := jira.Collect(t.Context(), mustComments(t, f, "PROJ-1"), 0)
	if err != nil {
		t.Fatalf("reading back: %v", err)
	}
	if len(stored) != 1 {
		t.Fatalf("the site holds %d comments, want 1", len(stored))
	}
	if got := adf.Markdown(stored[0].Body); !strings.Contains(got, "has now been rewritten") {
		t.Fatalf("the edit did not land: %q", got)
	}

	var mention adf.Node
	stored[0].Body.Walk(func(n adf.Node) bool {
		if n.Type == "mention" {
			mention = n
		}
		return true
	})
	if mention.Type == "" {
		t.Fatal("the mention was rewritten as prose by the edit")
	}
	if got := mention.Attrs["id"]; got != "acct-someone" {
		t.Errorf("the mention came back with id %v; markdown has no room for one, so the original must supply it", got)
	}
}

func TestThread_AnUntouchedEditGoesBackByteForByte(t *testing.T) {
	t.Parallel()

	f := newFake(3)
	original := adf.NewDoc(
		adf.NewNode("paragraph",
			adf.NewText("a lozenge: "),
			adf.NewNode("status").WithAttrs(adf.Attrs{"text": "DONE", "color": "green", "localId": "abc"}),
		),
		adf.NewNode("someNodeTypeThisClientHasNeverHeardOf").WithAttrs(adf.Attrs{"weight": 3}),
	)
	if _, err := f.AddComment(t.Context(), "PROJ-1", original); err != nil {
		t.Fatalf("AddComment: %v", err)
	}
	before, err := adf.Marshal(original)
	if err != nil {
		t.Fatalf("marshalling the original: %v", err)
	}
	dr := newDriver(t, testDeps(t, f), "PROJ-1", 100, 24)

	dr.key("e")
	dr.key("ctrl+s")

	stored, err := jira.Collect(t.Context(), mustComments(t, f, "PROJ-1"), 0)
	if err != nil {
		t.Fatalf("reading back: %v", err)
	}
	after, err := adf.Marshal(stored[0].Body)
	if err != nil {
		t.Fatalf("marshalling what was stored: %v", err)
	}
	if !bytes.Equal(before, after) {
		t.Errorf("an edit that changed nothing rewrote the document\n--- before ---\n%s\n--- after ---\n%s", before, after)
	}
}

func TestThread_SaysWhatAnEditWillOnlyKeepInThePartsNobodyTouches(t *testing.T) {
	t.Parallel()

	f := newFake(3)
	body := adf.NewDoc(adf.NewNode("paragraph",
		adf.NewText("ask "),
		adf.NewNode("mention").WithAttrs(adf.Attrs{"id": "acct-someone", "text": "@Someone"}),
	))
	if _, err := f.AddComment(t.Context(), "PROJ-1", body); err != nil {
		t.Fatalf("AddComment: %v", err)
	}
	dr := newDriver(t, testDeps(t, f), "PROJ-1", 100, 24)

	dr.key("e")

	if got := dr.statusText(); !strings.Contains(got, "mention") {
		t.Errorf("opening the editor said %q, want a warning naming what markdown cannot carry", got)
	}
}

func TestThread_SaysARestrictionSurvivesTheEdit(t *testing.T) {
	t.Parallel()

	dr := newDriver(t, testDeps(t, newFake(3)), "PROJ-1", 130, 24)
	dr.send(loadedMsg{gen: dr.m.gen, page: jira.NewPage([]jira.Comment{{
		ID:         "10701",
		Author:     jira.User{DisplayName: "Another User"},
		Body:       doc("Behind a flag."),
		Created:    time.Date(2026, time.February, 11, 9, 38, 0, 0, time.UTC),
		Updated:    time.Date(2026, time.February, 11, 9, 38, 0, 0, time.UTC),
		Visibility: &jira.Visibility{Type: "role", Value: "Developers"},
	}}, nil)})

	mustContain(t, dr.view(), "only the Developers role")

	dr.key("e")
	mustContain(t, dr.view(), "only the Developers role, and it stays that way")
}

// --- deleting ---------------------------------------------------------------

func TestThread_DeleteCannotBeReachedWithoutTheConfirmation(t *testing.T) {
	t.Parallel()

	f := newFake(3)
	comment(t, f, "PROJ-1", "The one that must not vanish.")
	dr := newDriver(t, testDeps(t, f), "PROJ-1", 100, 24)

	dr.key("d")
	if dr.m.mode != confirming {
		t.Fatal("d did not put the confirmation up")
	}
	if calls := countCalls(f, "DeleteComment"); calls != 0 {
		t.Fatalf("d deleted the comment before anybody confirmed it")
	}

	// Every key that is not the one the prompt names keeps the comment, and
	// leaves nothing to press twice by accident.
	for _, stroke := range []string{"n", "esc", "enter", "d", "x", "Y", "j"} {
		dr.key("d")
		dr.key(stroke)
		if got := countCalls(f, "DeleteComment"); got != 0 {
			t.Fatalf("%q went through with the delete", stroke)
		}
		if dr.m.mode != browsing {
			t.Fatalf("%q left the confirmation up", stroke)
		}
	}

	dr.key("d", "y")
	if got := countCalls(f, "DeleteComment"); got != 1 {
		t.Fatalf("y after the confirmation deleted %d comments, want 1", got)
	}
	if len(dr.m.comments) != 0 {
		t.Errorf("the thread still shows %d comments", len(dr.m.comments))
	}
}

func TestThread_DeleteConfirmationNamesWhatWillGo(t *testing.T) {
	t.Parallel()

	f := newFake(3)
	comment(t, f, "PROJ-1", "Reproduced on staging, then again on production.")
	dr := newDriver(t, testDeps(t, f), "PROJ-1", 100, 24)

	dr.key("d")

	mustContain(t, dr.view(), "delete Sam Tester's comment", "Reproduced on staging", "y deletes it")
}

func TestThread_DeleteThatIsRefusedSaysWhyAndKeepsTheComment(t *testing.T) {
	t.Parallel()

	f := newFake(3)
	comment(t, f, "PROJ-1", "Somebody else wrote this.")
	dr := newDriver(t, testDeps(t, f), "PROJ-1", 100, 24)

	f.FailNext(&jira.CapabilityError{Reason: "you need Delete All Comments to remove somebody else's"})
	dr.key("d", "y")

	if got := dr.lastStatus(); got.Level != kernel.LevelError ||
		!strings.Contains(got.Text, "Delete All Comments") {
		t.Errorf("the refusal reached the user as %+v, want the site's own sentence", got)
	}
	if len(dr.m.comments) != 1 {
		t.Errorf("a refused delete took the comment off the screen anyway")
	}
}

// --- failure paths ----------------------------------------------------------

func TestThread_EveryRequestReportsWhatWentWrongInItsOwnWords(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name string
		err  error
		want string
	}{
		{
			name: "a refusal",
			err:  &jira.CapabilityError{Reason: "you may not edit somebody else's comment"},
			want: "may not edit somebody else's comment",
		},
		{
			name: "a rate limit",
			err:  &jira.RateLimitError{RetryAfter: 30 * time.Second},
			want: "retry in 30s",
		},
		{
			name: "a transport failure",
			err:  &jira.TransportError{Op: "POST /comment", Err: errors.New("connection reset")},
			want: "connection reset",
		},
		{
			name: "a conflict",
			err:  &jira.ConflictError{Resource: "comment 10701", Detail: "somebody edited it first"},
			want: "somebody edited it first",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			f := newFake(3)
			comment(t, f, "PROJ-1", "The comment being written over.")
			dr := newDriver(t, testDeps(t, f), "PROJ-1", 100, 24)

			dr.key("a")
			dr.typeText("A comment that will not land.")
			f.FailNext(tc.err)
			dr.key("ctrl+s")

			if got := dr.lastStatus(); !strings.Contains(got.Text, tc.want) {
				t.Errorf("the status line says %q, want it to carry %q", got.Text, tc.want)
			}
			if dr.m.mode != writing {
				t.Fatal("a failed send closed the editor, taking the text with it")
			}
			if got := dr.m.editor.Value(); got != "A comment that will not land." {
				t.Errorf("the editor holds %q after the failure", got)
			}
		})
	}
}

func TestThread_ReadingAThreadThatFailsSaysSoRatherThanShowingAnEmptyOne(t *testing.T) {
	t.Parallel()

	f := newFake(3)
	comment(t, f, "PROJ-1", "unreachable")
	f.FailNext(&jira.RateLimitError{RetryAfter: 12 * time.Second})

	dr := newDriver(t, testDeps(t, f), "PROJ-1", 100, 24)

	if got := dr.lastStatus(); got.Level != kernel.LevelError || !strings.Contains(got.Text, "retry in 12s") {
		t.Errorf("reading a rate-limited thread reported %+v", got)
	}
	if dr.m.loaded {
		t.Error("a thread that never arrived is marked as loaded")
	}
}

// --- drafts -----------------------------------------------------------------

func TestThread_KeepsTheTextWhenTheEditorIsPutAside(t *testing.T) {
	t.Parallel()

	f := newFake(3)
	d := testDeps(t, f)
	dr := newDriver(t, d, "PROJ-2", 100, 24)

	dr.key("a")
	dr.typeText("Half a thought.")
	dr.key("esc")

	if dr.m.mode != browsing {
		t.Fatal("esc did not put the editor away")
	}
	dr.key("a")
	if got := dr.m.editor.Value(); got != "Half a thought." {
		t.Errorf("reopening the editor found %q", got)
	}
}

func TestThread_ADraftOutlivesTheSessionThatTypedIt(t *testing.T) {
	t.Parallel()

	f := newFake(3)
	d := testDeps(t, f)
	first := newDriver(t, d, "PROJ-3", 100, 24)
	first.key("a")
	first.typeText("Typed just before the crash.")

	// A second view of the same issue on the same site is what comes back after
	// the program stops without anybody pressing anything.
	second := newDriver(t, testDeps(t, f), "PROJ-3", 100, 24)
	second.key("a")

	if got := second.m.editor.Value(); got != "Typed just before the crash." {
		t.Errorf("the new session found %q in the editor", got)
	}
	mustContain(t, second.statusText(), "the draft you left")
}

func TestThread_DraftsForTwoIssuesDoNotReachEachOther(t *testing.T) {
	t.Parallel()

	f := newFake(4)
	one := newDriver(t, testDeps(t, f), "PROJ-4", 100, 24)
	one.key("a")
	one.typeText("about four")

	two := newDriver(t, testDeps(t, f), "PROJ-2", 100, 24)
	two.key("a")

	if got := two.m.editor.Value(); got != "" {
		t.Errorf("the editor on another issue was seeded with %q", got)
	}
}

func TestThread_ASentCommentLeavesNoDraftBehind(t *testing.T) {
	t.Parallel()

	f := newFake(5)
	dr := newDriver(t, testDeps(t, f), "PROJ-5", 100, 24)

	dr.key("a")
	dr.typeText("This one lands.")
	dr.key("ctrl+s")

	dr.key("a")
	if got := dr.m.editor.Value(); got != "" {
		t.Errorf("a comment that was sent came back as a draft: %q", got)
	}
}

func TestThread_HoldsOntoTheViewWhileTextIsUnsent(t *testing.T) {
	t.Parallel()

	dr := newDriver(t, testDeps(t, newFake(3)), "PROJ-1", 100, 24)

	if _, blocked := dr.m.BlocksClose(); blocked {
		t.Error("the view blocks closing with nothing typed")
	}
	dr.key("a")
	dr.typeText("unsent")

	reason, blocked := dr.m.BlocksClose()
	if !blocked {
		t.Fatal("the view lets itself be closed over unsent text")
	}
	if !strings.Contains(reason, "ctrl+s") {
		t.Errorf("the reason is %q, and does not say what to press", reason)
	}
}

// --- rendering --------------------------------------------------------------

func TestThread_RendersTheThreadAtSeveralWidths(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name          string
		width, height int
	}{
		{name: "thread_80x20.golden", width: 80, height: 20},
		{name: "thread_120x30.golden", width: 120, height: 30},
		{name: "thread_60x12.golden", width: 60, height: 12},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			f := newFake(3)
			comment(t, f, "PROJ-1", "Reproduced on staging.", "The stack trace points at the total.")
			comment(t, f, "PROJ-1", "Fix is behind a flag until the migration lands.")
			dr := newDriver(t, testDeps(t, f), "PROJ-1", tc.width, tc.height)

			golden(t, tc.name, dr.view())
		})
	}
}

func TestThread_RendersTheEditorAndTheConfirmation(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name string
		keys []string
	}{
		{name: "editor_100x24.golden", keys: []string{"a"}},
		{name: "confirm_100x24.golden", keys: []string{"d"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			f := newFake(3)
			comment(t, f, "PROJ-1", "Reproduced on staging.")
			dr := newDriver(t, testDeps(t, f), "PROJ-1", 100, 24)
			dr.key(tc.keys...)

			golden(t, tc.name, dr.view())
		})
	}
}

func TestThread_DrawsNothingBeforeItHasBeenGivenASize(t *testing.T) {
	t.Parallel()

	view, ok := Thread(testDeps(t, newFake(3)), "PROJ-1").(*Model)
	if !ok {
		t.Fatal("Thread did not return a *Model")
	}
	if got := view.View(); got != "" {
		t.Errorf("a view with no size drew %q", got)
	}
}

func TestThread_KeepsEveryLineInsideTheWidthItWasGiven(t *testing.T) {
	t.Parallel()

	f := newFake(3)
	comment(t, f, "PROJ-1",
		"A paragraph long enough that no terminal this narrow could hold it on one line, which is the "+
			"case a comment thread meets on the first day.")
	dr := newDriver(t, testDeps(t, f), "PROJ-1", 40, 14)

	for _, line := range strings.Split(dr.view(), "\n") {
		if got := len([]rune(line)); got > 40 {
			t.Errorf("a line is %d columns wide in a 40 column terminal: %q", got, line)
		}
	}
}

// --- registration -----------------------------------------------------------

func TestRegistration_PutsTheViewItsKeysAndItsCommandsInTheRegistry(t *testing.T) {
	t.Parallel()

	spec, ok := kernel.LookupView(ViewID)
	if !ok {
		t.Fatal("the comment view is not registered")
	}
	if spec.Slot != 0 {
		t.Errorf("the comment view claims footer slot %d; docs/UX.md keeps the digits for root views", spec.Slot)
	}
	if kernel.KeysFor(ViewID).IsZero() {
		t.Error("the comment view registered no keys, so the footer can say nothing about it")
	}
	want := map[string]bool{
		"comments.write": false, "comments.edit": false, "comments.delete": false,
	}
	for _, cmd := range kernel.Commands() {
		if _, ours := want[cmd.ID]; ours {
			want[cmd.ID] = true
		}
		if cmd.ID == "comments.open" {
			t.Error("something registers comments.open again; it switched to this view with no issue " +
				"behind it, which is a pane nothing can then satisfy")
		}
	}
	for id, found := range want {
		if !found {
			t.Errorf("%s is not in the command palette", id)
		}
	}
	for _, err := range kernel.RegistrationErrors() {
		t.Errorf("registration: %v", err)
	}
}

func TestRegistration_ThePaletteReachesTheSameGesturesTheKeysDo(t *testing.T) {
	t.Parallel()

	f := newFake(3)
	comment(t, f, "PROJ-1", "The comment the palette acts on.")
	dr := newDriver(t, testDeps(t, f), "PROJ-1", 100, 24)

	dr.send(WriteMsg{})
	if dr.m.mode != writing || dr.m.editing != "" {
		t.Error("the write command did not open the editor on a new comment")
	}
	dr.key("esc")

	dr.send(EditMsg{})
	if dr.m.mode != writing || dr.m.editing == "" {
		t.Error("the edit command did not open the editor on the comment under the cursor")
	}
	dr.key("esc")

	dr.send(DeleteMsg{})
	if dr.m.mode != confirming {
		t.Error("the delete command did not put the confirmation up")
	}
	if calls := countCalls(f, "DeleteComment"); calls != 0 {
		t.Error("the delete command deleted a comment without a confirmation")
	}
}

func TestThread_ARefreshKeepsTheReaderWhereTheyWere(t *testing.T) {
	t.Parallel()

	f := newFake(3)
	for i := range 12 {
		comment(t, f, "PROJ-1", "comment number "+strconv.Itoa(i))
	}
	dr := newDriver(t, testDeps(t, f), "PROJ-1", 100, 24)
	dr.key("g", "g")
	dr.key("j", "j")
	under := dr.m.comments[dr.m.cursor].ID

	dr.send(kernel.RefreshMsg{})

	if got := dr.m.comments[dr.m.cursor].ID; got != under {
		t.Errorf("a refresh moved the cursor from comment %s to %s", under, got)
	}
}

func TestThread_RetargetsAtAnotherIssueWithoutKeepingTheOldThread(t *testing.T) {
	t.Parallel()

	f := newFake(3)
	comment(t, f, "PROJ-1", "about one")
	comment(t, f, "PROJ-2", "about two")
	dr := newDriver(t, testDeps(t, f), "PROJ-1", 100, 24)

	dr.send(ThreadMsg{Key: "PROJ-2"})

	if dr.m.issue != "PROJ-2" {
		t.Fatalf("the view is still on %s", dr.m.issue)
	}
	mustContain(t, dr.view(), "about two")
	mustNotContain(t, dr.view(), "about one")
}

func mustComments(t *testing.T, f *jiratest.Fake, key string) jira.Page[jira.Comment] {
	t.Helper()

	page, err := f.Comments(t.Context(), key)
	if err != nil {
		t.Fatalf("Comments: %v", err)
	}
	return page
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
