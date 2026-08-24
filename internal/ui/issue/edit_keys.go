package issue

import "github.com/varijkapil13/saral/internal/ui/kernel"

// EditViewID and MoveViewID are the scopes the two pushed panes register their
// keys under. Neither is a footer slot: both are opened with the issue they are
// about, and a registry constructor has no issue to open them with.
const (
	EditViewID = "issue.edit"
	MoveViewID = "issue.move"
)

// editOpenKeys are the two bindings the detail pane hangs off. They live here
// rather than in the detail pane's keymap so that everything about editing an
// issue is in one place, and are named in that keymap so the footer and the
// help overlay advertise them.
func editBinding() kernel.Binding {
	return kernel.Bind([]string{"e"}, "e", "edit fields")
}

func moveBinding() kernel.Binding {
	return kernel.Bind([]string{"t"}, "t", "change status")
}

type editKeyMap struct {
	Up      kernel.Binding
	Down    kernel.Binding
	Act     kernel.Binding
	Prev    kernel.Binding
	Next    kernel.Binding
	Clear   kernel.Binding
	Save    kernel.Binding
	Discard kernel.Binding
	Accept  kernel.Binding
	Cancel  kernel.Binding
	Yes     kernel.Binding
}

func defaultEditKeys() editKeyMap {
	return editKeyMap{
		Up:      kernel.Bind([]string{"k", "up"}, "↑/k", "up"),
		Down:    kernel.Bind([]string{"j", "down"}, "↓/j", "down"),
		Act:     kernel.Bind([]string{"enter"}, "enter", "change this field"),
		Prev:    kernel.Bind([]string{"h", "left"}, "←/h", "previous value"),
		Next:    kernel.Bind([]string{"l", "right"}, "→/l", "next value"),
		Clear:   kernel.Bind([]string{"delete"}, "del", "empty this field"),
		Save:    kernel.Bind([]string{"ctrl+s"}, "ctrl+s", "save"),
		Discard: kernel.Bind([]string{"X"}, "X", "discard changes"),
		Accept:  kernel.Bind([]string{"enter"}, "enter", "keep this value"),
		Cancel:  kernel.Bind([]string{"esc"}, "esc", "leave the value alone"),
		Yes:     kernel.Bind([]string{"y"}, "y", "go ahead"),
	}
}

func (k editKeyMap) keySet() kernel.KeySet {
	return kernel.KeySet{
		Short: []kernel.Binding{k.Down, k.Up, k.Act, k.Save},
		Full: [][]kernel.Binding{
			{k.Down, k.Up, k.Act, k.Clear},
			{k.Prev, k.Next, k.Accept, k.Cancel},
			{k.Save, k.Discard, k.Yes},
		},
	}
}

type moveKeyMap struct {
	Up     kernel.Binding
	Down   kernel.Binding
	Act    kernel.Binding
	Prev   kernel.Binding
	Next   kernel.Binding
	Yes    kernel.Binding
	Cancel kernel.Binding
}

func defaultMoveKeys() moveKeyMap {
	return moveKeyMap{
		Up:     kernel.Bind([]string{"k", "up"}, "↑/k", "up"),
		Down:   kernel.Bind([]string{"j", "down"}, "↓/j", "down"),
		Act:    kernel.Bind([]string{"enter"}, "enter", "choose"),
		Prev:   kernel.Bind([]string{"h", "left"}, "←/h", "previous value"),
		Next:   kernel.Bind([]string{"l", "right"}, "→/l", "next value"),
		Yes:    kernel.Bind([]string{"y"}, "y", "go ahead"),
		Cancel: kernel.Bind([]string{"esc"}, "esc", "back"),
	}
}

func (k moveKeyMap) keySet() kernel.KeySet {
	return kernel.KeySet{
		Short: []kernel.Binding{k.Down, k.Up, k.Act},
		Full: [][]kernel.Binding{
			{k.Down, k.Up, k.Act},
			{k.Prev, k.Next, k.Yes, k.Cancel},
		},
	}
}
