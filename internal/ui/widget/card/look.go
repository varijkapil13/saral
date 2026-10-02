// Package card draws an issue as a card of fixed height: a roomy card of
// five lines, a compact card of three, for the list, the backlog and the
// board to share. The one-line row each view already draws is the third look,
// and stays the view's own.
package card

import (
	"strings"
	"sync"
	"sync/atomic"

	tea "charm.land/bubbletea/v2"

	"github.com/varijkapil13/saral/internal/config"
	"github.com/varijkapil13/saral/internal/ui/kernel"
)

// Look is how a view draws its issues. The zero value is Roomy, the default.
type Look uint8

// The looks, in the order V cycles through them.
const (
	Roomy Look = iota
	Compact
	Lines
)

// Default is the look a machine that never chose one gets.
func Default() Look { return Roomy }

// Next is the look V moves to from this one.
func (l Look) Next() Look {
	switch l {
	case Roomy:
		return Compact
	case Compact:
		return Lines
	default:
		return Roomy
	}
}

// Lines is how many lines one issue takes in this look.
func (l Look) Lines() int {
	switch l {
	case Compact:
		return 3
	case Lines:
		return 1
	default:
		return 5
	}
}

// Cards reports whether this look is drawn by Render rather than by the
// view's own row.
func (l Look) Cards() bool { return l != Lines }

// Word is the look as ui.toml spells it.
func (l Look) Word() string {
	switch l {
	case Compact:
		return "compact"
	case Lines:
		return "lines"
	default:
		return "roomy"
	}
}

// Parse reads a look from its word, and answers Default for anything else.
func Parse(s string) Look {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "compact":
		return Compact
	case "lines":
		return Lines
	default:
		return Default()
	}
}

// LookMsg is broadcast when the look changes. Every root view that draws
// issues applies it, so a key, the palette and a click take one path.
type LookMsg struct{ Look Look }

// Binding is the key that cycles the look.
var Binding = kernel.Canon(kernel.ActLook, "roomy / compact / lines")

// RoomyFields are what a roomy card draws beyond a row's own fields. A view
// adds them to its search only while the look is Roomy.
var RoomyFields = []string{"duedate", "subtasks", "fixVersions", "labels"}

// CommandID is the palette command that cycles the look.
const CommandID = "cards.look"

func init() {
	kernel.RegisterCommand(kernel.Command{
		ID:    CommandID,
		Title: "Cycle the row look: roomy cards, compact cards, lines",
		Group: "Appearance",
		Keys:  []string{Binding.Help().Key},
		Run:   func(kernel.Deps) tea.Cmd { return Cycle(Recall()) },
	})
}

var recalled struct {
	sync.Mutex
	look Look
	read bool
}

// Recall is the look this machine last chose, or Default when it never chose
// one or chose one this build does not know. ui.toml is read once per process:
// every view calls Recall as it is built, and after that the look only moves
// through Cycle, which keeps it here before its save reaches the file.
func Recall() Look {
	recalled.Lock()
	defer recalled.Unlock()
	if !recalled.read {
		recalled.look, recalled.read = Parse(config.LoadUIState().Look()), true
	}
	return recalled.look
}

// ResetRecall makes the next Recall read ui.toml again. It is for tests that
// point the cache directory somewhere else.
func ResetRecall() {
	recalled.Lock()
	recalled.read = false
	recalled.Unlock()
}

var saveFailed atomic.Bool

// Cycle moves from l to the next look: it broadcasts LookMsg and writes the
// choice to ui.toml off the event loop. A failed write is reported once per
// process, and the look changes regardless.
func Cycle(l Look) tea.Cmd {
	next := l.Next()
	recalled.Lock()
	recalled.look, recalled.read = next, true
	recalled.Unlock()
	return tea.Batch(kernel.Broadcast(LookMsg{Look: next}), save(next))
}

func save(l Look) tea.Cmd {
	return func() tea.Msg {
		err := config.SaveLook(l.Word())
		if err == nil || !saveFailed.CompareAndSwap(false, true) {
			return nil
		}
		return kernel.StatusMsg{
			Text:  "the look changed for this session, but saving it failed: " + err.Error(),
			Level: kernel.LevelWarn,
		}
	}
}
