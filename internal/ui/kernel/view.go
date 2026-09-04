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
	// Title names the view in the header, and in the footer when it is the root
	// the stack sits on.
	Title string
	// Slot is the digit g reaches this view with, 1-9. Zero means
	// the view is reachable only by being pushed or opened by name. The
	// allocation is written down in docs/UX.md; the registry rejects a duplicate.
	Slot int
	// RunsQueries marks the view a saved query opens into. The kernel switches
	// to it and hands it a RunQueryMsg, which is as much as the kernel knows
	// about what a search is.
	RunsQueries bool
	// Filters marks a view whose Update handles filter.ChosenMsg and draws
	// internal/ui/widget/filterbar's bar under whatever it shows whenever a
	// term is in force. The kernel may not import either package to check this
	// itself, so the view says so — the same self-registration RunsQueries
	// already is — and a sweep in internal/ui holds every one of them to it.
	Filters bool
	// Requires names the capability this view needs. An empty key means the
	// view is always available; an absent capability hides it and its reason is
	// what the user sees instead.
	Requires jira.CapabilityKey
	// New builds an instance. It is called once, when the view is first opened.
	New func(Deps) View
}

// CommandKind ranks a command in the palette's unfiltered list. It is not a
// table of group names: AGENTS.md forbids the central switch that would be.
type CommandKind int

// The command kinds, in the order the unfiltered palette heads them. KindVerb
// is the default and is drawn last; see rank below.
const (
	// KindVerb is the default; not KindAction, which SettingKind already uses
	// in this package.
	KindVerb    CommandKind = iota
	KindGoTo                // a destination
	KindSearch              // a search to run
	KindSession             // scope: the project, the settings screen
)

// lastNamedKind must move down if a Kind is ever added after KindSession.
const lastNamedKind = KindSession

// rank sorts KindVerb last despite being the zero value; every other Kind
// sorts by its own ordinal.
func (k CommandKind) rank() int {
	if k == KindVerb {
		return int(lastNamedKind) + 1
	}
	return int(k)
}

// Command is an action for the command palette. Anything may register one.
type Command struct {
	// ID is the command's stable name, used by frecency ranking.
	ID string
	// Title is what the palette shows.
	Title string
	// Group buckets related commands in the palette.
	Group string
	// Kind ranks this command ahead of Group and Title in the unfiltered list.
	Kind CommandKind
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
	// Site is what the profile calls the site this session is talking to. It is
	// drawn in the header and it is what IssueURL builds a browse link from, so
	// it is not display-only — but nothing after onboarding checks its shape, so
	// it may carry a scheme, a port or a context path and anything reading it has
	// to cope with all three rather than concatenating.
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

// KeySet is a view's keys, scoped to itself.
//
// Acts is the inventory of what can be done to the thing on screen, most-used
// first, and it is the footer's middle cell. Full is the help overlay, one inner
// slice per column, and it is where the motions and the whole sentence live.
//
// The two are not the same list written twice. The footer is one row shared with
// the root cell and the globals, so a label there is two or three words — "edit",
// not "edit fields" — and it names an action rather than a way of moving around.
// The overlay has the room to say what a key really does, and it leads with Acts
// so that what a user came to do is above how to scroll.
//
// Short is the one-line form bubbles/help renders, and the footer falls back to
// it for a set that names no Acts. Every view in this build names Acts; the
// fallback is what keeps a key set that has not been partitioned yet from
// drawing an empty footer.
type KeySet struct {
	Acts  []Binding
	Short []Binding
	Full  [][]Binding
}

// IsZero reports whether the set carries no bindings.
func (k KeySet) IsZero() bool {
	return len(k.Acts) == 0 && len(k.Short) == 0 && len(k.Full) == 0
}

// KeyReporter is the optional interface a view implements when the keys that
// work in it depend on what it is doing. RegisterKeys is init-time and refuses a
// second call, so the registry holds one set per view for the whole run: the
// resting state. A list with its filter open, a comment being written, a
// transition waiting to be confirmed and an onboarding step all answer
// differently, and docs/UX.md asks the footer to show only what works right now.
//
// Gen must change whenever the set does. The chrome is memoized on a comparable
// key and a KeySet holds slices, so the number is what tells the footer to
// repaint; a view that returns a constant one is right on the first frame and
// stale forever. Returning the index of the state the set belongs to is enough.
//
// The set must be stored rather than built: this is called on every frame, and
// a KeySet assembled per call puts its allocations under every keystroke.
type KeyReporter interface {
	LiveKeys() (set KeySet, gen int)
}
