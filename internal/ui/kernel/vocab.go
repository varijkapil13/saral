package kernel

import (
	"fmt"
	"slices"
	"sync"
)

// Action names one thing a key does, app-wide.
type Action string

// The canonical actions, one constant per row of the vocabulary in docs/UX.md.
const (
	ActUp           Action = "up"
	ActDown         Action = "down"
	ActLeft         Action = "left"
	ActRight        Action = "right"
	ActPageUp       Action = "page-up"
	ActPageDown     Action = "page-down"
	ActHalfUp       Action = "half-up"
	ActHalfDown     Action = "half-down"
	ActTop          Action = "top"
	ActBottom       Action = "bottom"
	ActNextPane     Action = "next-pane"
	ActPrevPane     Action = "prev-pane"
	ActOpen         Action = "open"
	ActBack         Action = "back"
	ActClearFilters Action = "clear-filters"
	ActEdit         Action = "edit"
	ActEditExternal Action = "edit-external"
	ActSave         Action = "save"
	ActRevert       Action = "revert"
	ActRevertAll    Action = "revert-all"
	ActStatus       Action = "status"
	ActAssign       Action = "assign"
	ActPriority     Action = "priority"
	ActLabels       Action = "labels"
	ActMove         Action = "move"
	ActComment      Action = "comment"
	ActAdd          Action = "add"
	ActCreate       Action = "create"
	ActDelete       Action = "delete"
	ActSort         Action = "sort"
	ActFilter       Action = "filter"
	ActFind         Action = "find"
	ActFindNext     Action = "find-next"
	ActFindPrev     Action = "find-prev"
	ActType         Action = "type"
	ActGroup        Action = "group"
	ActLook         Action = "look"
	ActFold         Action = "fold"
	ActFoldAll      Action = "fold-all"
	ActHidden       Action = "hidden"
	ActAdvance      Action = "advance"
	ActRefresh      Action = "refresh"
	ActRefreshAll   Action = "refresh-all"
	ActCopyKey      Action = "copy-key"
	ActCopyLink     Action = "copy-link"
	ActBrowser      Action = "browser"
	ActParent       Action = "parent"
	ActChildren     Action = "children"
	ActFull         Action = "full-list"
	ActLinks        Action = "links"
	ActLogTime      Action = "log-time"
	ActWatchers     Action = "watchers"
	ActToggle       Action = "toggle"
	ActSelectGroup  Action = "select-group"
	ActSelectNone   Action = "select-none"
	ActRankUp       Action = "rank-up"
	ActRankDown     Action = "rank-down"
	ActRankFirst    Action = "rank-first"
	ActRankLast     Action = "rank-last"
	ActShiftLeft    Action = "shift-left"
	ActShiftRight   Action = "shift-right"
	ActMine         Action = "mine"

	ActSortPrev   Action = "sort-prev"
	ActSortNext   Action = "sort-next"
	ActSortChoose Action = "sort-choose"
	ActSortCancel Action = "sort-cancel"

	ActQuit     Action = "quit"
	ActHelp     Action = "help"
	ActPalette  Action = "palette"
	ActGo       Action = "go"
	ActSlot     Action = "slot"
	ActSaved    Action = "saved-query"
	ActJump     Action = "jump"
	ActSettings Action = "settings"
	ActSearch   Action = "search"
)

// Canonical is one row of the vocabulary: the strokes an action answers to, how
// they are written in help, and what the action is called there.
type Canonical struct {
	Action Action
	Keys   []string
	Label  string
	Desc   string
	// Variant is the lowercase action this one is the capital of, if any.
	Variant Action
	// Aliases are the command-ID suffixes that name this action in the palette.
	Aliases []string
	// Prefixed are the Keys only pressed behind the go-to prefix, so they do not
	// own the bare stroke.
	Prefixed []string
	// Modal marks an action that only exists inside a Modal key set.
	Modal bool
}

var vocabulary = []Canonical{
	{Action: ActUp, Keys: []string{"k", "up"}, Label: "↑/k", Desc: "up"},
	{Action: ActDown, Keys: []string{"j", "down"}, Label: "↓/j", Desc: "down"},
	{Action: ActLeft, Keys: []string{"h", "left"}, Label: "←/h", Desc: "left"},
	{Action: ActRight, Keys: []string{"l", "right"}, Label: "→/l", Desc: "right"},
	{Action: ActPageUp, Keys: []string{"pgup", "ctrl+b"}, Label: "pgup", Desc: "page up"},
	{Action: ActPageDown, Keys: []string{"pgdown", "ctrl+f"}, Label: "pgdn", Desc: "page down"},
	{Action: ActHalfUp, Keys: []string{"ctrl+u"}, Label: "ctrl+u", Desc: "half page up"},
	{Action: ActHalfDown, Keys: []string{"ctrl+d"}, Label: "ctrl+d", Desc: "half page down"},
	{Action: ActTop, Keys: []string{"home"}, Label: "g g", Desc: "first row"},
	{Action: ActBottom, Keys: []string{"G", "end"}, Label: "G / g e", Desc: "last row", Variant: ActTop},
	{Action: ActNextPane, Keys: []string{"tab"}, Label: "tab", Desc: "next"},
	{Action: ActPrevPane, Keys: []string{"shift+tab"}, Label: "shift+tab", Desc: "previous"},
	{Action: ActOpen, Keys: []string{"enter"}, Label: "enter", Desc: "open"},
	{Action: ActBack, Keys: []string{"esc"}, Label: "esc", Desc: "back"},
	{Action: ActClearFilters, Keys: []string{"ctrl+g"}, Label: "ctrl+g", Desc: "clear filters", Aliases: []string{".clear-filter"}},
	{Action: ActEdit, Keys: []string{"e"}, Label: "e", Desc: "edit"},
	{Action: ActEditExternal, Keys: []string{"E"}, Label: "E", Desc: "edit in $EDITOR", Variant: ActEdit},
	{Action: ActSave, Keys: []string{"ctrl+s"}, Label: "ctrl+s", Desc: "save"},
	{Action: ActRevert, Keys: []string{"u"}, Label: "u", Desc: "revert"},
	{Action: ActRevertAll, Keys: []string{"U"}, Label: "U", Desc: "revert all", Variant: ActRevert},
	{Action: ActStatus, Keys: []string{"t"}, Label: "t", Desc: "status"},
	{Action: ActAssign, Keys: []string{"@"}, Label: "@", Desc: "assign", Aliases: []string{".assign"}},
	{Action: ActPriority, Keys: []string{"P"}, Label: "P", Desc: "priority"},
	{Action: ActLabels, Keys: []string{"#"}, Label: "#", Desc: "labels"},
	{Action: ActMove, Keys: []string{"m"}, Label: "m", Desc: "move to sprint", Aliases: []string{".move"}},
	{Action: ActComment, Keys: []string{"C"}, Label: "C", Desc: "comment"},
	{Action: ActAdd, Keys: []string{"a"}, Label: "a", Desc: "add"},
	{Action: ActCreate, Keys: []string{"c"}, Label: "c", Desc: "create", Aliases: []string{".new", ".create"}},
	{Action: ActDelete, Keys: []string{"d"}, Label: "d", Desc: "delete", Aliases: []string{".delete"}},
	{Action: ActSort, Keys: []string{"s"}, Label: "s", Desc: "sort", Aliases: []string{".sort"}},
	{Action: ActFilter, Keys: []string{"f"}, Label: "f", Desc: "filter", Aliases: []string{".filter-by"}},
	{Action: ActFind, Keys: []string{"/"}, Label: "/", Desc: "find", Aliases: []string{".find"}},
	{Action: ActFindNext, Keys: []string{"n"}, Label: "n", Desc: "next match"},
	{Action: ActFindPrev, Keys: []string{"N"}, Label: "N", Desc: "previous match", Variant: ActFindNext},
	{Action: ActType, Keys: []string{"i"}, Label: "i", Desc: "type"},
	{Action: ActGroup, Keys: []string{"v"}, Label: "v", Desc: "group"},
	{Action: ActLook, Keys: []string{"V"}, Label: "V", Desc: "look", Variant: ActGroup},
	{Action: ActFold, Keys: []string{"z"}, Label: "z", Desc: "fold"},
	{Action: ActFoldAll, Keys: []string{"Z"}, Label: "Z", Desc: "fold all", Variant: ActFold},
	{Action: ActHidden, Keys: []string{"."}, Label: ".", Desc: "hidden"},
	{Action: ActAdvance, Keys: []string{"!"}, Label: "!", Desc: "advance"},
	{Action: ActRefresh, Keys: []string{"r"}, Label: "r", Desc: "refresh"},
	{Action: ActRefreshAll, Keys: []string{"R"}, Label: "R", Desc: "refetch everything", Variant: ActRefresh},
	{Action: ActCopyKey, Keys: []string{"y"}, Label: "y", Desc: "copy key"},
	{Action: ActCopyLink, Keys: []string{"Y"}, Label: "Y", Desc: "copy link", Variant: ActCopyKey},
	{Action: ActBrowser, Keys: []string{"o"}, Label: "o", Desc: "open in browser"},
	{Action: ActParent, Keys: []string{"p"}, Label: "p", Desc: "parent"},
	{Action: ActChildren, Keys: []string{"]"}, Label: "]", Desc: "children"},
	{Action: ActFull, Keys: []string{"I"}, Label: "I", Desc: "send to full list"},
	{Action: ActLinks, Keys: []string{"&"}, Label: "&", Desc: "links"},
	{Action: ActLogTime, Keys: []string{"w"}, Label: "w", Desc: "log time"},
	{Action: ActWatchers, Keys: []string{"W"}, Label: "W", Desc: "watchers", Variant: ActLogTime},
	{Action: ActToggle, Keys: []string{"space"}, Label: "space", Desc: "toggle"},
	{Action: ActSelectGroup, Keys: []string{"*"}, Label: "*", Desc: "select group"},
	{Action: ActSelectNone, Keys: []string{"x"}, Label: "x", Desc: "select none"},
	{Action: ActRankUp, Keys: []string{"K"}, Label: "K", Desc: "rank up", Variant: ActUp},
	{Action: ActRankDown, Keys: []string{"J"}, Label: "J", Desc: "rank down", Variant: ActDown},
	{Action: ActRankFirst, Keys: []string{"{"}, Label: "{", Desc: "rank first"},
	{Action: ActRankLast, Keys: []string{"}"}, Label: "}", Desc: "rank last"},
	{Action: ActShiftLeft, Keys: []string{"H"}, Label: "H", Desc: "shift left", Variant: ActLeft},
	{Action: ActShiftRight, Keys: []string{"L"}, Label: "L", Desc: "shift right", Variant: ActRight},
	{Action: ActMine, Keys: []string{"M"}, Label: "M", Desc: "mine", Aliases: []string{".mine"}},

	{Action: ActSortPrev, Keys: []string{"left", "h"}, Label: "←/h", Desc: "previous field", Modal: true},
	{Action: ActSortNext, Keys: []string{"right", "l"}, Label: "→/l", Desc: "next field", Modal: true},
	{Action: ActSortChoose, Keys: []string{"enter"}, Label: "enter", Desc: "choose this order", Modal: true},
	{Action: ActSortCancel, Keys: []string{"esc"}, Label: "esc", Desc: "leave the order as it is", Modal: true},

	{Action: ActQuit, Keys: []string{"q", "ctrl+c"}, Label: "q", Desc: "quit"},
	{Action: ActHelp, Keys: []string{"?"}, Label: "?", Desc: "help"},
	{Action: ActPalette, Keys: []string{"ctrl+k"}, Label: "ctrl+k", Desc: "commands"},
	{Action: ActGo, Keys: []string{"g"}, Label: "g", Desc: "where to go"},
	{Action: ActSlot, Keys: slices.Clone(digits), Label: "g 1-9", Desc: "switch view", Prefixed: digits},
	{Action: ActSaved, Keys: slices.Clone(digits), Label: "1-9", Desc: "saved query"},
	{Action: ActJump, Keys: []string{"i"}, Label: "g i", Desc: "jump to an issue", Prefixed: []string{"i"}},
	{Action: ActSettings, Keys: []string{"ctrl+,", "s"}, Label: "ctrl+, / g s", Desc: "settings", Prefixed: []string{"s"}},
	{Action: ActSearch, Keys: []string{"/"}, Label: "g /", Desc: "search issues", Prefixed: []string{"/"}},
}

var byAction = func() map[Action]Canonical {
	m := make(map[Action]Canonical, len(vocabulary))
	for _, c := range vocabulary {
		m[c.Action] = c
	}
	return m
}()

// Vocabulary returns every canonical action, in the order docs/UX.md lists them.
func Vocabulary() []Canonical {
	out := make([]Canonical, len(vocabulary))
	copy(out, vocabulary)
	return out
}

// Mint records how a binding came to exist: as a canonical action, or as a key
// local to one view and declared as such.
type Mint struct {
	Action Action
	Local  bool
	Owner  string
	ID     string
}

// The mint table is keyed by the first element of a binding's key slice, which
// Bind keeps as given. A binding built by Bind from the same slice, as Terse does,
// is the same mint; one that merely spells the same keys is not.
var mints = struct {
	mu sync.RWMutex
	by map[*string]Mint
}{by: make(map[*string]Mint)}

func fail(err error) {
	reg.mu.Lock()
	reg.errs = append(reg.errs, err)
	reg.mu.Unlock()
}

func record(keys []string, m Mint) {
	if len(keys) == 0 {
		fail(fmt.Errorf("kernel: a binding minted as %+v has no keys", m))
		return
	}
	mints.mu.Lock()
	prev, held := mints.by[&keys[0]]
	if !held {
		mints.by[&keys[0]] = m
	}
	mints.mu.Unlock()
	if held && prev != m {
		fail(fmt.Errorf("kernel: keys %v are minted as both %+v and %+v", keys, prev, m))
	}
}

// MintOf reports how a binding was made, or false for one built with Bind alone.
func MintOf(b Binding) (Mint, bool) {
	keys := b.Keys()
	if len(keys) == 0 {
		return Mint{}, false
	}
	mints.mu.RLock()
	defer mints.mu.RUnlock()
	m, ok := mints.by[&keys[0]]
	return m, ok
}

// Canon is the binding for a canonical action, with the description overridden
// when the view says it more precisely. An unknown action is a startup error.
func Canon(a Action, desc ...string) Binding {
	c, ok := byAction[a]
	if !ok {
		fail(fmt.Errorf("kernel: Canon was asked for unknown action %q", a))
		return Binding{}
	}
	text := c.Desc
	if len(desc) > 0 {
		text = desc[0]
	}
	record(c.Keys, Mint{Action: a})
	return Bind(c.Keys, c.Label, text)
}

// Local is a key that belongs to one view alone and is declared as such.
func Local(owner, id string, keys []string, label, desc string) Binding {
	if owner == "" || id == "" {
		fail(fmt.Errorf("kernel: Local binding %q %q has no owner or id", label, desc))
	}
	record(keys, Mint{Local: true, Owner: owner, ID: id})
	return Bind(keys, label, desc)
}

// SortPickerKeys are the keys of the sort picker, in the order it draws them.
func SortPickerKeys() []Binding {
	return []Binding{Canon(ActSortPrev), Canon(ActSortNext), Canon(ActSortChoose), Canon(ActSortCancel)}
}
