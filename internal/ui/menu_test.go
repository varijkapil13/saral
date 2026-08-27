package ui

import (
	"fmt"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/varijkapil13/saral/internal/ui/kernel"
	"github.com/varijkapil13/saral/internal/ui/list"
)

// clickableKernel is the program with one view on top and the mouse on, which is
// the only configuration the right-click menu exists in.
func clickableKernel(t *testing.T, id string, w, h int) kernel.Model {
	t.Helper()
	m, err := kernel.New(depsFor(t), kernel.WithSize(w, h),
		kernel.WithInitialView(list.ViewID), kernel.WithMouse(true))
	if err != nil {
		t.Fatalf("kernel.New: %v", err)
	}
	next, _ := m.Update(tea.WindowSizeMsg{Width: w, Height: h})
	m = next.(kernel.Model)
	if id != list.ViewID {
		next, _ = m.Update(kernel.PushMsg{ID: id, Title: id, View: build(t, id)})
		m = next.(kernel.Model)
	}
	return m
}

// menuOf is the body with the menu up, trimmed of the blank rows below it so
// that a change in a view's height does not rewrite this.
func menuOf(t *testing.T, id string, w, h int) string {
	t.Helper()
	m := clickableKernel(t, id, w, h)
	next, _ := m.Update(tea.MouseClickMsg{X: 4, Y: 4, Button: tea.MouseRight})
	rows := strings.Split(ansi.Strip(next.(kernel.Model).Frame()), "\n")
	kept := make([]string, 0, len(rows))
	for _, row := range rows[1 : len(rows)-1] {
		if strings.TrimSpace(row) != "" {
			kept = append(kept, strings.TrimRight(row, " "))
		}
	}
	return strings.Join(kept, "\n")
}

// TestContextMenu_EveryView is the menu over the real views rather than a stub.
// It is what makes the gesture worth having: every entry here is a view's own
// answer to "what can be done to the thing in front of you", which is the
// question kernel.Command cannot answer and the reason P3.3 cut this.
//
// A view taking typing is left out, and that is the behaviour rather than a gap
// in the sweep: the menu spends the arrows and enter, so a view mid-token keeps
// the keyboard and the right-click reaches it as a click.
func TestContextMenu_EveryView(t *testing.T) {
	sweepEnv(t)
	var b strings.Builder
	for _, id := range viewsUnderTest() {
		if c, ok := build(t, id).(kernel.KeyCapturer); ok && c.WantsRawKeys() {
			continue
		}
		fmt.Fprintf(&b, "%s\n%s\n\n", id, menuOf(t, id, 120, 38))
	}
	golden(t, "menu_120x38.golden", b.String())
}

// TestContextMenu_NamesEveryActionTheViewHas is what the menu is for over the
// row: the row folds what does not fit into a +N, and the menu names all of it.
// Principle 3 as an equality rather than a promise — an action that falls off an
// eighty-column row is still one gesture away, from the same one inventory.
func TestContextMenu_NamesEveryActionTheViewHas(t *testing.T) {
	sweepEnv(t)
	for _, id := range viewsUnderTest() {
		view := build(t, id)
		if c, ok := view.(kernel.KeyCapturer); ok && c.WantsRawKeys() {
			continue
		}
		set := kernel.KeysFor(id)
		if reporter, ok := view.(kernel.KeyReporter); ok {
			set, _ = reporter.LiveKeys()
		}
		acts := set.Acts
		if len(acts) == 0 {
			acts = set.Short
		}
		menu := menuOf(t, id, 120, 38)
		// A state with nothing of its own to offer says so by advertising nothing —
		// an attachment pane with nothing attached and no permission to attach is
		// the one in this build — and the gesture then says so rather than opening
		// an empty box.
		if len(acts) == 0 {
			if !strings.Contains(menu, "nothing to do to what is on this screen") {
				t.Errorf("%s offers no actions, and the right-click neither opened nor explained:\n%s", id, menu)
			}
			continue
		}
		for _, act := range acts {
			if !strings.Contains(menu, act.Help().Key) {
				t.Errorf("%s: the view offers %q and the menu does not name it:\n%s", id, act.Help().Key, menu)
			}
		}
	}
}

// TestContextMenu_ChoosingTheFirstEntryReachesTheViewThatNamedIt drives the whole
// gesture against a real view: a right-click, then enter, and the list has to
// have been handed the stroke its own first action advertises.
func TestContextMenu_ChoosingTheFirstEntryReachesTheViewThatNamedIt(t *testing.T) {
	sweepEnv(t)

	m := clickableKernel(t, list.ViewID, 120, 38)
	next, _ := m.Update(tea.MouseClickMsg{X: 4, Y: 4, Button: tea.MouseRight})
	m = next.(kernel.Model)
	menu := ansi.Strip(m.Frame())
	if !strings.Contains(menu, "What can be done here") {
		t.Fatalf("the right-click did not open a menu over the issue list:\n%s", menu)
	}

	next, _ = m.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	after := ansi.Strip(next.(kernel.Model).Frame())
	if strings.Contains(after, "What can be done here") {
		t.Errorf("enter left the menu up:\n%s", after)
	}
	// The list's first action opens the row under the cursor, and with no rows
	// loaded that is a refusal rather than a pane — either way the menu is gone
	// and the stroke was delivered to the view rather than swallowed here.
	if !strings.Contains(after, "Issues") {
		t.Errorf("the frame after choosing is not the issue list any more:\n%s", after)
	}
}
