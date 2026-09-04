package ui

import (
	"slices"
	"testing"

	"github.com/varijkapil13/saral/internal/ui/attach"
	"github.com/varijkapil13/saral/internal/ui/backlog"
	"github.com/varijkapil13/saral/internal/ui/board"
	"github.com/varijkapil13/saral/internal/ui/comment"
	"github.com/varijkapil13/saral/internal/ui/issue"
	"github.com/varijkapil13/saral/internal/ui/kernel"
	"github.com/varijkapil13/saral/internal/ui/list"
	"github.com/varijkapil13/saral/internal/ui/plan"
	"github.com/varijkapil13/saral/internal/ui/release"
	"github.com/varijkapil13/saral/internal/ui/sprint"
	"github.com/varijkapil13/saral/internal/ui/timeline"
)

// keyOwners says whose footer renders the key each command carries. The kernel
// cannot check this — it may not import a view — and a registrar checking itself
// is the drift, so the sweep lives here, above every view and below nothing.
//
// A command that reaches a view by its footer slot names the view; its key is
// the gesture that slot is reached by.
var keyOwners = map[string]string{
	// The destination overlay is the kernel's own chrome rather than a view, so
	// the gesture it teaches is in the global keymap and no view's footer shows
	// it. GlobalScope names that keymap.
	"views.switch": kernel.GlobalScope,
	// ctrl+, and g s are GlobalKeys.Settings, not a footer slot: settings.open
	// is reached the same way from every view, so no one view's footer shows it.
	"settings.open": kernel.GlobalScope,

	"comments.write":          comment.ViewID,
	"comments.edit":           comment.ViewID,
	"comments.delete":         comment.ViewID,
	"issue.comments":          issue.ViewID,
	"issue.edit":              issue.ViewID,
	"issue.transition":        issue.ViewID,
	"issue.split.sidebar":     issue.ViewID,
	"issue.split.description": issue.ViewID,
	"issue.split.reset":       issue.ViewID,
	"issues.filter-by":        list.ViewID,
	"issues.save-query":       list.ViewID,
	"issues.edit-query":       list.ViewID,
	"issues.all":              list.ViewID,
	"issues.open":             list.ViewID,

	// The pane holds no slot, so every one of these is a key of its own that the
	// pane's footer shows once an issue with files is on it.
	"attachments.show":   attach.ViewID,
	"attachments.open":   attach.ViewID,
	"attachments.upload": attach.ViewID,
	"attachments.delete": attach.ViewID,

	"backlog.move": backlog.ViewID,
	"backlog.open": backlog.ViewID,

	"board.move-issue": board.ViewID,
	"board.next":       board.ViewID,
	"board.open":       board.ViewID,

	"plans.open":    plan.ViewID,
	"plans.sources": plan.ViewID,

	"releases.archive": release.ViewID,
	"releases.edit":    release.ViewID,
	"releases.new":     release.ViewID,
	"releases.open":    release.ViewID,
	"releases.release": release.ViewID,

	"sprints.closed":   sprint.ViewID,
	"sprints.complete": sprint.ViewID,
	"sprints.edit":     sprint.ViewID,
	"sprints.new":      sprint.ViewID,
	"sprints.open":     sprint.ViewID,
	"sprints.start":    sprint.ViewID,

	"timeline.notes":    timeline.ViewID,
	"timeline.open":     timeline.ViewID,
	"timeline.today":    timeline.ViewID,
	"timeline.zoom-in":  timeline.ViewID,
	"timeline.zoom-out": timeline.ViewID,
}

func TestCommands_TeachTheKeyTheirViewActuallyShows(t *testing.T) {
	seen := make(map[string]bool, len(keyOwners))
	for _, cmd := range kernel.Commands() {
		if len(cmd.Keys) == 0 {
			continue
		}
		owner, named := keyOwners[cmd.ID]
		if !named {
			t.Errorf("command %q carries keys %v and this test does not know whose footer shows them; add it to keyOwners",
				cmd.ID, cmd.Keys)
			continue
		}
		seen[cmd.ID] = true
		for _, k := range cmd.Keys {
			checkKey(t, cmd.ID, owner, k)
		}
	}
	for id := range keyOwners {
		if !seen[id] {
			t.Errorf("keyOwners names %q, which registers no command with keys any more", id)
		}
	}
}

// checkKey holds a command's key against the two things a view will ever tell a
// user to press: the help label of one of its bindings, or the gesture that
// reaches its footer slot.
func checkKey(t *testing.T, id, owner, k string) {
	t.Helper()

	if owner == kernel.GlobalScope {
		if global, _ := labelsOf(kernel.DefaultGlobalKeys().KeySet()); !slices.Contains(global, k) {
			t.Errorf("command %q teaches %q, and the global keymap shows %v; a key the kernel does not "+
				"draw anywhere is one nobody can be told to press", id, k, global)
		}
		return
	}
	shown, matched := labelsOf(kernel.KeysFor(owner))
	if slices.Contains(shown, k) {
		return
	}
	if spec, ok := kernel.LookupView(owner); ok && spec.Slot > 0 && k == kernel.SlotGesture(spec.Slot) {
		return
	}
	if slices.Contains(matched, k) {
		t.Errorf("command %q teaches %q, which %s matches but never shows; its footer says %v, so a user is given two answers to one question",
			id, k, owner, shown)
		return
	}
	t.Errorf("command %q teaches %q, which %s neither shows nor binds; it shows %v", id, k, owner, shown)
}

// labelsOf splits a key set into what it tells the user to press and what it
// merely answers to. "a c" is one binding whose label is "a".
//
// Acts is walked as well as Short and Full, and it is the half that matters: it
// is the footer, so a command teaching a key nothing in Acts shows is teaching a
// key the row the user is looking at does not carry.
func labelsOf(set kernel.KeySet) (shown, matched []string) {
	each := func(b kernel.Binding) {
		shown = append(shown, b.Help().Key)
		matched = append(matched, b.Keys()...)
	}
	for _, b := range set.Acts {
		each(b)
	}
	for _, b := range set.Short {
		each(b)
	}
	for _, column := range set.Full {
		for _, b := range column {
			each(b)
		}
	}
	return shown, matched
}
