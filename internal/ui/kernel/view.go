package kernel

import (
	"time"

	tea "charm.land/bubbletea/v2"
	zone "github.com/lrstanley/bubblezone/v2"

	"github.com/varijkapil13/saral/internal/app"
	"github.com/varijkapil13/saral/pkg/jira"
)

// View is one screen. It is a Bubble Tea model that renders into a string
// rather than a tea.View, because the kernel owns the frame: alt screen, mouse
// mode, cursor and window title are decided once, at the root.
//
// A view learns its size from the SizeMsg the kernel sends it, and keeps it.
// Nothing hands a size to View, so a view is free to memoize whatever it likes.
type View interface {
	Init() tea.Cmd
	Update(tea.Msg) (View, tea.Cmd)
	View() string
}

// ViewSpec is how a view package registers itself. It is declared in an init()
// inside the view's own package, so adding a view touches no shared file.
type ViewSpec struct {
	// ID is the view's stable name. It is what `saral <id>` opens and what
	// RegisterKeys scopes keys to.
	ID string
	// Title is the footer label.
	Title string
	// Slot is the footer position, 1-9, reached with g and the digit. Zero means
	// the view is reachable only by being pushed or opened by name. The
	// allocation is written down in docs/UX.md; the registry rejects a duplicate.
	Slot int
	// RunsQueries marks the view a saved query opens into. The kernel switches
	// to it and hands it a RunQueryMsg, which is as much as the kernel knows
	// about what a search is.
	RunsQueries bool
	// Requires names the capability this view needs. An empty key means the
	// view is always available; an absent capability hides it and its reason is
	// what the user sees instead.
	Requires jira.CapabilityKey
	// New builds an instance. It is called once, when the view is first opened.
	New func(Deps) View
}

// Command is an action for the command palette. Anything may register one.
type Command struct {
	// ID is the command's stable name, used by frecency ranking.
	ID string
	// Title is what the palette shows.
	Title string
	// Group buckets related commands in the palette.
	Group string
	// Requires names the capability the command needs, if any.
	Requires jira.CapabilityKey
	// Keys are the ways to reach this same action without the palette, each
	// spelt the way the view's own footer spells it: the help label of the
	// binding, not the list of strokes it matches. "a", not "a c"; "g1", not
	// "1". Two answers to one question in the same frame is what carrying the
	// match list gives.
	//
	// Empty means there is no key and nothing may guess one — an ID says nothing
	// about a keybinding, and one command's is byte-identical to a view ID whose
	// keys belong to something else entirely.
	Keys []string
	// Run performs the command.
	Run func(Deps) tea.Cmd
}

// Deps is what a view is built with. It carries no back-pointer to the kernel:
// a view affects the rest of the program by returning one of this package's
// commands, never by reaching for another model.
type Deps struct {
	// Jira is the port, narrowed to the roles the views in this build call. It
	// is never a concrete adapter here, and it is deliberately not the whole
	// jira.Client: an adapter that can serve every view that exists is wired in
	// now rather than when it can serve the ones that do not.
	Jira jira.SessionClient
	// Caps is the probe result, already resolved for Project.
	Caps jira.Capabilities
	// Project is the project this session is scoped to. Several capabilities
	// are per-project, so it is what the probe was run against.
	Project string
	// Theme holds the styles, built once per theme generation.
	Theme *Theme
	// Zones resolves mouse clicks to the element that was rendered there. It is
	// also how a view learns whether the mouse is on at all: a session started
	// with mouse = false disables the manager, so Zones.Enabled() answers that
	// question and Mark writes nothing into the frame.
	Zones *zone.Manager
	// Cache is what this session has already read from the site, or nil when it
	// has nowhere to keep one. Every view has to draw without it: a first run has
	// nothing on disk, and another copy of Saral may be holding the file.
	Cache app.Cache
	// Site is the host this session is talking to, for display only.
	Site string
	// Now is the clock. Tests inject a fixed one.
	Now func() time.Time
	// Saved are the queries the number keys run, as the profile last had them.
	Saved app.SavedQueries
	// SaveQueries persists a changed set. A session with nowhere to write them —
	// no profile yet — leaves it nil, and the kernel says so rather than
	// pretending the binding survived.
	SaveQueries func(app.SavedQueries) error
}

// KeySet is a view's keys, scoped to itself. Short is the footer hint line, in
// order; Full is the help overlay, one inner slice per column.
type KeySet struct {
	Short []Binding
	Full  [][]Binding
}

// IsZero reports whether the set carries no bindings.
func (k KeySet) IsZero() bool { return len(k.Short) == 0 && len(k.Full) == 0 }
