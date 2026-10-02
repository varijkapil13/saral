package search

import (
	"github.com/varijkapil13/saral/internal/ui/issue"
	"github.com/varijkapil13/saral/internal/ui/kernel"
	"github.com/varijkapil13/saral/internal/ui/widget"
)

var _ kernel.KeyReporter = (*Model)(nil)

type keyMap struct {
	Up       kernel.Binding
	Down     kernel.Binding
	PageUp   kernel.Binding
	PageDown kernel.Binding
	HalfUp   kernel.Binding
	HalfDown kernel.Binding
	Go       kernel.Binding
	Top      kernel.Binding
	Bottom   kernel.Binding
	Open     kernel.Binding
	Results  kernel.Binding
	Typing   kernel.Binding
	Scope    kernel.Binding
	InList   kernel.Binding
	Retry    kernel.Binding
	Close    kernel.Binding
}

func defaultKeys() keyMap {
	return keyMap{
		Up:       kernel.Bind([]string{"k", "up"}, "↑/k", "up"),
		Down:     kernel.Bind([]string{"j", "down"}, "↓/j", "down"),
		PageUp:   kernel.Bind([]string{"pgup", "ctrl+b"}, "pgup", "page up"),
		PageDown: kernel.Bind([]string{"pgdown", "ctrl+f", "space"}, "pgdn", "page down"),
		HalfUp:   kernel.Bind([]string{"ctrl+u"}, "ctrl+u", "half page up"),
		HalfDown: kernel.Bind([]string{"ctrl+d"}, "ctrl+d", "half page down"),
		Go:       kernel.Bind([]string{"g"}, "g", "go to"),
		Top:      kernel.Bind([]string{"home"}, "g g", "first row"),
		Bottom:   kernel.Bind([]string{"G", "end"}, "G / g e", "last row"),
		Open:     kernel.Bind([]string{"enter"}, "enter", "open"),
		Results:  kernel.Bind([]string{"enter", "down", "ctrl+n"}, "enter", "results"),
		Typing:   kernel.Bind([]string{"/", "i"}, "/", "search"),
		Scope:    kernel.Bind([]string{"tab"}, "tab", "search this project or every project"),
		InList:   kernel.Bind([]string{"L"}, "L", "in list"),
		Retry:    kernel.Bind([]string{"r"}, "r", "retry"),
		Close:    kernel.Bind([]string{"esc"}, "esc", "close"),
	}
}

type keyState int

const (
	keysTypingIdle keyState = iota
	keysTypingRows
	keysBrowseRows
	keysBrowseEmpty
	keyStatesPerScope
)

var liveSets = func() [2 * keyStatesPerScope]kernel.KeySet {
	k := defaultKeys()
	tab := kernel.Terse(k.Scope, "scope")
	var sets [2 * keyStatesPerScope]kernel.KeySet
	for withScope := range 2 {
		at := func(s keyState) int { return withScope*int(keyStatesPerScope) + int(s) }
		scoped := func(b kernel.Binding, rest ...kernel.Binding) []kernel.Binding {
			out := []kernel.Binding{}
			if b.Enabled() {
				out = append(out, b)
			}
			return append(out, rest...)
		}
		var terse, spelt kernel.Binding
		if withScope == 1 {
			terse, spelt = tab, k.Scope
		}
		motions := [][]kernel.Binding{
			{k.Down, k.Up, k.PageDown, k.PageUp},
			{k.HalfDown, k.HalfUp, k.Top, k.Bottom},
		}

		typing := func(lead []kernel.Binding) kernel.KeySet {
			acts := append(append([]kernel.Binding{}, lead...), scoped(terse, k.Close)...)
			full := append(append([]kernel.Binding{}, lead...), scoped(spelt, k.Close)...)
			return kernel.KeySet{Acts: acts, Full: [][]kernel.Binding{full, {widget.KillLine}}}
		}
		sets[at(keysTypingIdle)] = typing(nil)
		sets[at(keysTypingRows)] = typing([]kernel.Binding{k.Results})

		actions := append(append([]kernel.Binding{k.Open, k.Typing}, scoped(spelt, k.InList)...), issue.ShareBindings...)
		sets[at(keysBrowseRows)] = kernel.KeySet{
			Acts: append([]kernel.Binding{k.Open, k.Typing}, scoped(terse, k.InList)...),
			Menu: issue.ShareBindings,
			Full: append(append([][]kernel.Binding{}, motions...), actions),
		}
		sets[at(keysBrowseEmpty)] = kernel.KeySet{
			Acts: append([]kernel.Binding{k.Typing, k.Retry}, scoped(terse)...),
			Full: [][]kernel.Binding{append([]kernel.Binding{k.Typing, k.Retry}, scoped(spelt, k.Close)...)},
		}
	}
	return sets
}()

func (k keyMap) keySet() kernel.KeySet { return liveSets[int(keysBrowseRows)+int(keyStatesPerScope)] }

// LiveKeys reports the keys that work in the state the view is in.
func (m *Model) LiveKeys() (set kernel.KeySet, gen int) {
	state := keysBrowseEmpty
	switch {
	case !m.browsing && len(m.rows) > 0:
		state = keysTypingRows
	case !m.browsing:
		state = keysTypingIdle
	case len(m.rows) > 0:
		state = keysBrowseRows
	}
	at := int(state)
	if m.canScope() {
		at += int(keyStatesPerScope)
	}
	return liveSets[at], at
}

type action uint8

const (
	actNone action = iota
	actDown
	actUp
	actPageDown
	actPageUp
	actHalfDown
	actHalfUp
	actGo
	actTop
	actBottom
	actOpen
	actTyping
	actScope
	actInList
)

func (k keyMap) browseTable() map[string]action {
	out := make(map[string]action, 24)
	for _, e := range []struct {
		b kernel.Binding
		a action
	}{
		{k.Down, actDown}, {k.Up, actUp}, {k.PageDown, actPageDown}, {k.PageUp, actPageUp},
		{k.HalfDown, actHalfDown}, {k.HalfUp, actHalfUp}, {k.Go, actGo}, {k.Top, actTop},
		{k.Bottom, actBottom}, {k.Open, actOpen}, {k.Typing, actTyping}, {k.Scope, actScope},
		{k.InList, actInList},
	} {
		for _, stroke := range e.b.Keys() {
			out[stroke] = e.a
		}
	}
	return out
}
