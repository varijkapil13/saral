package kernel

import (
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/varijkapil13/saral/pkg/jira"
)

func withPalette(t *testing.T) Model {
	t.Helper()
	RegisterView(spec("board", 1, "", &stubView{id: "board"}))
	RegisterView(spec(PaletteViewID, 0, "", &stubView{id: PaletteViewID}))
	m := newAt(t, testDeps(), 120, 30)
	m, _ = press(m, "ctrl+k")
	return m
}

func TestRunCommand_RunsAgainstTheSessionAsItIsNowRatherThanAsThePaletteFoundIt(t *testing.T) {
	resetRegistry()
	t.Cleanup(resetRegistry)
	var scopes []string
	RegisterCommand(Command{ID: "issues.mine", Title: "My issues", Run: func(d Deps) tea.Cmd {
		scopes = append(scopes, d.Project)
		return nil
	}})

	m := withPalette(t)
	next, _ := m.Update(ProjectMsg{Project: "PROJ"})
	_, _ = next.(Model).Update(RunCommandMsg{ID: "issues.mine"})

	if len(scopes) != 1 || scopes[0] != "PROJ" {
		t.Errorf("the command ran with project %v, want [PROJ]: a search scoped from a stale copy of the deps runs over the whole site and looks like it worked", scopes)
	}
}

func TestRunCommand_ClosesThePaletteBeforeWhatItRunsTakesEffect(t *testing.T) {
	resetRegistry()
	t.Cleanup(resetRegistry)
	RegisterCommand(Command{ID: "issue.create", Title: "Create an issue", Run: func(Deps) tea.Cmd {
		return Push("form", "New issue", &stubView{id: "form", content: "form body"})
	}})

	RegisterView(spec("board", 1, "", &stubView{id: "board"}))
	RegisterView(spec(PaletteViewID, 0, "", &stubView{id: PaletteViewID}))
	m := newAt(t, testDeps(), 120, 30)
	next, _ := m.Update(PushMsg{View: &stubView{id: "detail", content: "PROJ-1 detail"}, ID: "issue", Title: "PROJ-1"})
	m = next.(Model)
	m, _ = press(m, "ctrl+k")

	after, cmd := m.Update(RunCommandMsg{ID: "issue.create"})
	m = deliver(t, after.(Model), cmd)
	if got := ansi.Strip(m.Frame()); !strings.Contains(got, "form body") {
		t.Fatalf("the command did not run:\n%s", got)
	}

	m, _ = press(m, "esc")
	if got := ansi.Strip(m.Frame()); !strings.Contains(got, "PROJ-1 detail") {
		t.Errorf("esc from what the command opened landed back on the palette rather than where it was run from:\n%s", got)
	}
}

func TestRunCommand_RefusesWithTheCapabilitysOwnWordsAndRunsNothing(t *testing.T) {
	resetRegistry()
	t.Cleanup(resetRegistry)
	ran := false
	RegisterCommand(Command{
		ID: "issue.delete", Title: "Delete this issue", Requires: jira.CapDeleteIssues,
		Run: func(Deps) tea.Cmd { ran = true; return nil },
	})
	RegisterView(spec("board", 1, "", &stubView{id: "board"}))
	RegisterView(spec(PaletteViewID, 0, "", &stubView{id: PaletteViewID}))

	d := testDeps()
	d.Caps.DeleteIssues = jira.Capability{Reason: "Deleting issues needs the Delete Issues permission, which this token does not have"}
	m := newAt(t, d, 120, 30)
	m, _ = press(m, "ctrl+k")

	after, _ := m.Update(RunCommandMsg{ID: "issue.delete"})
	if ran {
		t.Error("a command the site does not allow was run anyway")
	}
	if got := ansi.Strip(after.(Model).Frame()); !strings.Contains(got, "Delete Issues permission") {
		t.Errorf("the refusal did not reach the user in the capability's own words:\n%s", got)
	}
}

func TestRunCommand_SaysWhatRanAndWhichKeyReachesItWithoutThePalette(t *testing.T) {
	resetRegistry()
	t.Cleanup(resetRegistry)
	RegisterCommand(Command{ID: "issue.edit", Title: "Edit this issue", Keys: []string{"e"},
		Run: func(Deps) tea.Cmd { return nil }})

	board := &stubView{id: "board"}
	RegisterView(spec("board", 1, "", board))
	palette := &stubView{id: PaletteViewID}
	RegisterView(spec(PaletteViewID, 0, "", palette))

	m := newAt(t, testDeps(), 120, 30)
	m, _ = press(m, "ctrl+k")
	board.seen, palette.seen = nil, nil

	if _, _ = m.Update(RunCommandMsg{ID: "issue.edit"}); !saw(board, "ran:issue.edit:e") {
		t.Errorf("nothing was told what ran, so neither frecency nor the hint has anywhere to hook: %v", board.seen)
	}
	if !saw(palette, "ran:issue.edit:e") {
		t.Errorf("the palette did not hear the command it sent, and it is the one counting runs: %v", palette.seen)
	}
}

func TestRunCommand_SaysSoWhenNothingRegisteredTheID(t *testing.T) {
	resetRegistry()
	t.Cleanup(resetRegistry)
	m := withPalette(t)

	after, _ := m.Update(RunCommandMsg{ID: "issues.mine"})
	if got := ansi.Strip(after.(Model).Frame()); !strings.Contains(got, "issues.mine is not available in this build") {
		t.Errorf("an unknown command was swallowed:\n%s", got)
	}
}
