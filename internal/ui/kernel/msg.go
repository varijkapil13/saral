package kernel

import (
	tea "charm.land/bubbletea/v2"

	"github.com/varijkapil13/saral/internal/app"
	"github.com/varijkapil13/saral/pkg/jira"
)

// PushMsg puts a view on top of the stack. ID scopes its keys in the registry
// and Title names it in the header; both may be empty for a throwaway pane.
type PushMsg struct {
	View  View
	ID    string
	Title string
	// Lent says the pusher goes on holding this view after the stack drops it,
	// so taking it off is not discarding it and the kernel must not close it.
	// The default is the common case: a view built for the push, which the
	// kernel then owns.
	Lent bool
}

// PopMsg takes the top view off the stack.
type PopMsg struct{}

// OpenMsg switches to a registered root view by ID.
type OpenMsg struct{ ID string }

// BroadcastMsg carries a message to every view on the stack, which is how one
// view tells another that something changed without holding a pointer to it.
type BroadcastMsg struct{ Msg tea.Msg }

// ReplyMsg carries a view's own answer back to the view that asked for it.
//
// To is that view first and whatever holds it after, so a view the kernel cannot
// see — the comment thread inside the issue pane's sidebar is one — is reached
// through the entry that can, which passes it on. The kernel delivers Msg to the
// first address it can resolve and drops it when it can resolve none.
type ReplyMsg struct {
	To  []Addr
	Msg tea.Msg
}

// RunCommandMsg asks the kernel to run a registered command, named by ID. It is
// how the palette runs one: the kernel holds the deps a command is given, so
// they are current as of the keypress rather than as of whenever the palette was
// built, and one place decides whether a capability allows the command at all.
type RunCommandMsg struct{ ID string }

// CommandRanMsg says which command just ran and which keys reach it without the
// palette, so that a view can count what gets used and another can offer the key
// for it.
type CommandRanMsg struct {
	ID   string
	Keys []string
}

// RunQueryMsg carries a search to the view that registered RunsQueries. The
// kernel sends it when a number key runs a saved query; the view turns it into
// whatever retargeting means for it.
type RunQueryMsg struct {
	JQL   string
	Title string
}

// BindQueryMsg asks for a query to be bound to a number key and kept. A view
// sends it for the search it is showing, because the kernel does not know what
// is on screen; the kernel owns the set, because it is what dispatches the key.
type BindQueryMsg struct {
	Name string
	JQL  string
	Slot int
}

// SavedQueriesMsg carries the saved queries after one changed, so that a view
// offering to bind another can say what a key already runs.
type SavedQueriesMsg struct{ Queries app.SavedQueries }

// SizeMsg tells a view the box it has been given. It is not the terminal size:
// the kernel has already taken the header, status line and footer out of it.
type SizeMsg struct{ Width, Height int }

// FocusMsg tells a view whether it is the one receiving keys.
type FocusMsg struct{ Focused bool }

// RefreshMsg asks the focused view to refetch. Purge means throw the cache away
// first rather than revalidate it.
type RefreshMsg struct{ Purge bool }

// ThemeMsg carries a rebuilt theme after the terminal reported its background
// colour or the user switched themes.
type ThemeMsg struct{ Theme *Theme }

// SetMouseMsg turns mouse reporting on or off. The kernel is the only place
// that holds whether the mouse is on, so this is where appearance.mouse's
// choice lands.
type SetMouseMsg struct{ Enabled bool }

// CapabilitiesMsg carries a fresh probe result.
type CapabilitiesMsg struct{ Caps jira.Capabilities }

// StatusLevel is how prominently a status line is drawn.
type StatusLevel int

// The status levels.
const (
	LevelInfo StatusLevel = iota
	LevelWarn
	LevelError
)

// StatusMsg sets the status line.
type StatusMsg struct {
	Text  string
	Level StatusLevel
}

// Push returns a command that pushes a view onto the stack. The kernel takes it
// over: popping it discards it, and a view implementing Closer is told so.
func Push(id, title string, v View) tea.Cmd {
	return func() tea.Msg { return PushMsg{View: v, ID: id, Title: title} }
}

// Lend returns a command that puts a view the caller keeps on the stack. The
// kernel draws it and routes keys to it like any other entry and drops it on
// esc without closing it, because the caller is still holding it — the issue
// pane hands over the very thread its sidebar draws, and closing that would
// cancel a read the sidebar is still waiting for. The lender owes the view a
// Close of its own when it is discarded in turn.
func Lend(id, title string, v View) tea.Cmd {
	return func() tea.Msg { return PushMsg{View: v, ID: id, Title: title, Lent: true} }
}

// Reply wraps a command so that what it comes back with is delivered to the view
// that issued it, wherever that view has got to by then, rather than to whatever
// is on top of the stack when it lands.
//
// A view addresses itself first and names whatever holds it after, so that a
// view the kernel never sees is still reachable through the one it does. An
// answer for a view that has been discarded resolves to nothing and is dropped.
//
// It is for a view's own answers and not for everything it returns. A kernel
// command — a push, a pop, a status line — is addressed to the kernel and must
// not be wrapped, and neither must a widget's own tick: a cursor blink belongs to
// whoever is being looked at, which is exactly where the top of the stack puts
// it.
func Reply(cmd tea.Cmd, to ...Addr) tea.Cmd {
	if cmd == nil {
		return nil
	}
	return func() tea.Msg { return ReplyTo(cmd(), to...) }
}

// ReplyTo addresses a message a view has already produced. It is what a view
// wraps a callback's answer in where Reply cannot reach — the editor handoff
// hands tea.ExecProcess a function rather than a command, and wrapping the
// command itself would put an envelope round the exec the runtime has to see.
func ReplyTo(msg tea.Msg, to ...Addr) tea.Msg {
	if msg == nil {
		return nil
	}
	return ReplyMsg{To: to, Msg: msg}
}

// Pop returns a command that goes back one view.
func Pop() tea.Cmd { return func() tea.Msg { return PopMsg{} } }

// Open returns a command that switches to a registered root view.
func Open(id string) tea.Cmd { return func() tea.Msg { return OpenMsg{ID: id} } }

// SetMouse returns a command that turns mouse reporting on or off.
func SetMouse(enabled bool) tea.Cmd { return func() tea.Msg { return SetMouseMsg{Enabled: enabled} } }

// RunCommand returns a command that runs a registered command by ID.
func RunCommand(id string) tea.Cmd {
	return func() tea.Msg { return RunCommandMsg{ID: id} }
}

// Broadcast returns a command that delivers a message to every view on the
// stack.
func Broadcast(msg tea.Msg) tea.Cmd {
	return func() tea.Msg { return BroadcastMsg{Msg: msg} }
}

// Refresh returns a command that asks the focused view to refetch.
func Refresh(purge bool) tea.Cmd {
	return func() tea.Msg { return RefreshMsg{Purge: purge} }
}

// BindQuery returns a command that binds a query to a number key and persists
// it.
func BindQuery(name, jql string, slot int) tea.Cmd {
	return func() tea.Msg { return BindQueryMsg{Name: name, JQL: jql, Slot: slot} }
}

// Status returns a command that shows a plain status message.
func Status(text string) tea.Cmd {
	return func() tea.Msg { return StatusMsg{Text: text} }
}

// Warn returns a command that shows a warning in the status line.
func Warn(text string) tea.Cmd {
	return func() tea.Msg { return StatusMsg{Text: text, Level: LevelWarn} }
}

// Fail returns a command that shows an error in the status line, using the
// wording the error itself carries. A capability's reason reaches the user
// verbatim; nothing rephrases a 403 into "something went wrong".
func Fail(err error) tea.Cmd {
	if err == nil {
		return nil
	}
	text, _ := jira.Reason(err)
	return func() tea.Msg { return StatusMsg{Text: text, Level: LevelError} }
}
