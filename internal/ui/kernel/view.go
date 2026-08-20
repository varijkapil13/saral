package kernel

import (
	"time"

	tea "charm.land/bubbletea/v2"
	zone "github.com/lrstanley/bubblezone/v2"

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
	// Slot is the footer position, 1-9. Zero means the view is reachable only
	// by being pushed or opened by name.
	Slot int
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
	// Run performs the command.
	Run func(Deps) tea.Cmd
}

// Deps is what a view is built with. It carries no back-pointer to the kernel:
// a view affects the rest of the program by returning one of this package's
// commands, never by reaching for another model.
type Deps struct {
	// Jira is the port. It is never a concrete adapter here.
	Jira jira.Client
	// Caps is the probe result, already resolved.
	Caps jira.Capabilities
	// Theme holds the styles, built once per theme generation.
	Theme *Theme
	// Zones resolves mouse clicks to the element that was rendered there.
	Zones *zone.Manager
	// Site is the host this session is talking to, for display only.
	Site string
	// Now is the clock. Tests inject a fixed one.
	Now func() time.Time
}

// KeySet is a view's keys, scoped to itself. Short is the footer hint line, in
// order; Full is the help overlay, one inner slice per column.
type KeySet struct {
	Short []Binding
	Full  [][]Binding
}

// IsZero reports whether the set carries no bindings.
func (k KeySet) IsZero() bool { return len(k.Short) == 0 && len(k.Full) == 0 }
