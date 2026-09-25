package release

import "github.com/varijkapil13/saral/internal/ui/kernel"

var _ kernel.KeyReporter = (*Bulk)(nil)

// bulkKeyMap is what the assignment screen answers to. While the query is
// being typed every letter is text, so the switch is tab and leaving is esc;
// once there is a preview the kernel has esc back, and it pops the screen.
type bulkKeyMap struct {
	Run      kernel.Binding
	Toggle   kernel.Binding
	Leave    kernel.Binding
	Apply    kernel.Binding
	Edit     kernel.Binding
	Up       kernel.Binding
	Down     kernel.Binding
	PageUp   kernel.Binding
	PageDown kernel.Binding
}

func defaultBulkKeys() bulkKeyMap {
	return bulkKeyMap{
		Run:      kernel.Bind([]string{"enter"}, "enter", "show what would change"),
		Toggle:   kernel.Bind([]string{"tab"}, "tab", "switch between putting it on and taking it off"),
		Leave:    kernel.Bind([]string{"esc"}, "esc", "leave"),
		Apply:    kernel.Bind([]string{"y"}, "y", "go ahead"),
		Edit:     kernel.Bind([]string{"e"}, "e", "change the query"),
		Up:       kernel.Bind([]string{"k", "up"}, "↑/k", "up"),
		Down:     kernel.Bind([]string{"j", "down"}, "↓/j", "down"),
		PageUp:   kernel.Bind([]string{"pgup", "ctrl+b"}, "pgup", "page up"),
		PageDown: kernel.Bind([]string{"pgdown", "ctrl+f"}, "pgdn", "page down"),
	}
}

func (k bulkKeyMap) keySet() kernel.KeySet { return bulkSets[bulkQuery] }

var bulkSets = func() [bulkStates]kernel.KeySet {
	k := defaultBulkKeys()
	motions := []kernel.Binding{k.Down, k.Up, k.PageDown, k.PageUp}
	var sets [bulkStates]kernel.KeySet
	sets[bulkQuery] = kernel.KeySet{
		Acts: []kernel.Binding{kernel.Terse(k.Run, "preview"), kernel.Terse(k.Toggle, "on/off"), k.Leave},
		Full: [][]kernel.Binding{{k.Run, k.Toggle, k.Leave}},
	}
	sets[bulkReading] = kernel.KeySet{}
	sets[bulkPreview] = kernel.KeySet{
		Acts: []kernel.Binding{k.Apply, kernel.Terse(k.Edit, "query")},
		Full: [][]kernel.Binding{motions, {k.Apply, k.Edit}},
	}
	// Chunks in flight answer nothing: the ones sent have changed, and a key
	// that looked like it stopped the rest would be a claim about a race.
	sets[bulkWorking] = kernel.KeySet{}
	sets[bulkDone] = kernel.KeySet{
		Acts: []kernel.Binding{kernel.Terse(k.Edit, "another query")},
		Full: [][]kernel.Binding{motions, {k.Edit}},
	}
	return sets
}()

// LiveKeys reports the keys that work on the step that is up.
func (b *Bulk) LiveKeys() (set kernel.KeySet, gen int) { return bulkSets[b.state], int(b.state) }

type bulkAction uint8

const (
	bulkNone bulkAction = iota
	bulkRun
	bulkToggle
	bulkLeave
	bulkApply
	bulkEdit
	bulkUp
	bulkDown
	bulkPageUp
	bulkPageDown
)

func (k bulkKeyMap) table() map[string]bulkAction {
	return table(
		binding[bulkAction]{k.Run, bulkRun}, binding[bulkAction]{k.Toggle, bulkToggle},
		binding[bulkAction]{k.Leave, bulkLeave}, binding[bulkAction]{k.Apply, bulkApply},
		binding[bulkAction]{k.Edit, bulkEdit},
		binding[bulkAction]{k.Up, bulkUp}, binding[bulkAction]{k.Down, bulkDown},
		binding[bulkAction]{k.PageUp, bulkPageUp}, binding[bulkAction]{k.PageDown, bulkPageDown},
	)
}
