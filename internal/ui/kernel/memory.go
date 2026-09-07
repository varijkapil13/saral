package kernel

import tea "charm.land/bubbletea/v2"

// Memory is what a session remembers between runs, scoped to whichever
// profile it is on: which root view it last opened, and whatever a view kept
// for itself under a key of its own naming — a filter's encoding, a board's
// active quick filters. The kernel may not import internal/ui/filter to make
// sense of a term (docs/ARCHITECTURE.md's layering), so everything kept here
// is opaque text a view encodes and decodes for itself.
//
// A session with nowhere to write — no profile yet, an unwritable cache
// directory — leaves this nil, and every method on it below is written to
// cope: Recall answers false and Keep does nothing, the same "remembers
// nothing and says nothing" LoadUIState already gives a first run.
type Memory interface {
	// Recall reads what was kept under a view and a key of its own naming,
	// and whether anything ever was.
	Recall(view, key string) (value string, ok bool)
	// Keep remembers a value under a view and a key of its own naming. It
	// takes no error: a session with nowhere to write remembers nothing for
	// the next one and says nothing about it to this one, the same way a
	// stale stored capability answer is used instead of refused.
	Keep(view, key, value string)
	// Forget drops everything this profile has ever kept — the root view and
	// every view's own state alike — which is what asking to forget the
	// remembered view and filters means.
	Forget()
}

// Recall reads a session's memory, answering false whenever there is nowhere
// one could have been kept.
func Recall(d Deps, view, key string) (string, bool) {
	if d.Memory == nil {
		return "", false
	}
	return d.Memory.Recall(view, key)
}

// Keep remembers a value in a session's memory. Nil-safe: a session with no
// memory to write to does nothing.
func Keep(d Deps, view, key, value string) {
	if d.Memory == nil {
		return
	}
	d.Memory.Keep(view, key, value)
}

// memoryScope is the reserved view name the kernel keeps its own state under
// — the root view a session last had open — rather than a view's. It is not a
// registered ViewSpec.ID, so nothing a view keeps for itself can collide
// with it.
const memoryScope = "kernel"

// rootViewKey is the key the kernel's own root-view memory is kept under.
const rootViewKey = "view"

// rememberRoot records which root view is now on screen, so that the next
// session — with no explicit view named on the command line and nothing from
// onboarding — can land on the same one instead of reopening the first
// footer slot and asking to be told again.
func (m Model) rememberRoot(id string) { Keep(m.deps, memoryScope, rootViewKey, id) }

// recalledRoot is the root view the last session left this profile on, and
// whether one is on record: registered still, a root and not merely
// reachable by being pushed, and available under the capabilities this
// session actually has.
func (m Model) recalledRoot() (ViewSpec, bool) {
	id, ok := Recall(m.deps, memoryScope, rootViewKey)
	if !ok {
		return ViewSpec{}, false
	}
	spec, found := LookupView(id)
	if !found || spec.Slot <= 0 || !m.available(spec) {
		return ViewSpec{}, false
	}
	return spec, true
}

func init() { RegisterSetting(forgetMemorySetting()) }

// forgetMemorySetting is the settings row docs/FILTERS.md and docs/SETTINGS.md
// ask for: a way to see that something is remembered and to clear it. It is
// KindAction rather than KindInfo with a Run, the same shape
// session.onboarding already is, because there is no value to show — only
// something to do.
func forgetMemorySetting() Setting {
	return Setting{
		ID:      "session.memory",
		Section: "Session",
		Order:   3,
		Title:   "Forget the remembered view and filters",
		Summary: "the view this profile opens on next time, and every view's kept filters",
		Kind:    KindAction,
		Scope:   ScopeMachine,
		Run: func(d Deps) tea.Cmd {
			if d.Memory == nil {
				return Warn("there is nowhere this session's memory is kept")
			}
			d.Memory.Forget()
			return Status("forgotten — next time opens on the default view with no filters in force")
		},
	}
}
