package issue

import (
	tea "charm.land/bubbletea/v2"

	"github.com/varijkapil13/saral/internal/ui/kernel"
)

// ShareAct is one way of taking an issue out of Saral.
type ShareAct uint8

// The share acts, in the order ShareBindings lists them.
const (
	ShareNone ShareAct = iota
	ShareKey
	ShareLink
	ShareBrowser
)

// ShareMsg is the palette's copy and open commands. It is a broadcast, so
// whichever view on top holds an issue answers it and the rest leave it.
type ShareMsg struct{ Act ShareAct }

// ShareBindings are the keys every view holding an issue answers the same way.
var ShareBindings = []kernel.Binding{
	kernel.Canon(kernel.ActCopyKey, "copy the key"),
	kernel.Canon(kernel.ActCopyLink, "copy the link"),
	kernel.Canon(kernel.ActBrowser),
}

// ShareStroke is the act a stroke asks for, if it is one of ShareBindings.
func ShareStroke(stroke string) ShareAct {
	for i := range ShareBindings {
		for _, k := range ShareBindings[i].Keys() {
			if k == stroke {
				return ShareAct(i + 1)
			}
		}
	}
	return ShareNone
}

// Share copies an issue's key or link, or opens it in the browser.
func Share(d kernel.Deps, act ShareAct, key string) tea.Cmd {
	if act == ShareNone {
		return nil
	}
	if key == "" {
		return kernel.Warn("there is no issue here")
	}
	if act == ShareKey {
		return kernel.Copy(key, key)
	}
	link, err := kernel.IssueURL(d.Site, key)
	if err != nil {
		return kernel.Warn(err.Error())
	}
	if act == ShareLink {
		return kernel.Copy(link, "the link to "+key)
	}
	return kernel.OpenURL(link)
}

type collabAct uint8

const (
	collabNone collabAct = iota
	collabLinks
	collabTime
	collabWatchers
	collabClone
	collabParent
	collabChildren
	collabChildList
)

// CollabMsg is the palette's way to the sheets the keys open.
type CollabMsg struct{ Open collabAct }

// ChangedMsg is broadcast whenever a write lands against an issue this pane
// holds — a transition, a dirty-set save, or a sheet's own change (a link,
// a worklog, a watcher). A list, board or backlog row showing the same key
// listens for it to revalidate that one row, rather than reloading whole.
type ChangedMsg struct{ Key string }

var collabBindings = [...]kernel.Binding{
	collabLinks:    kernel.Canon(kernel.ActLinks),
	collabTime:     kernel.Canon(kernel.ActLogTime),
	collabWatchers: kernel.Canon(kernel.ActWatchers),
}

var collabStrokes = func() map[string]collabAct {
	out := map[string]collabAct{}
	for at, b := range collabBindings {
		for _, k := range b.Keys() {
			out[k] = collabAct(at)
		}
	}
	return out
}()

var collabKeys = append(append([]kernel.Binding{}, ShareBindings...),
	collabBindings[collabLinks], collabBindings[collabTime], collabBindings[collabWatchers])

func init() {
	for _, c := range []struct {
		id, title string
		act       kernel.Action
		b         *kernel.Binding
		msg       tea.Msg
	}{
		{"issue.copyKey", "Copy this issue's key", kernel.ActCopyKey, &ShareBindings[0], ShareMsg{ShareKey}},
		{"issue.copyLink", "Copy the link to this issue", kernel.ActCopyLink, &ShareBindings[1], ShareMsg{ShareLink}},
		{"issue.openBrowser", "Open this issue in the browser", kernel.ActBrowser, &ShareBindings[2], ShareMsg{ShareBrowser}},
		{"issue.links", "Link this issue to another", kernel.ActLinks, &collabBindings[collabLinks], CollabMsg{collabLinks}},
		{"issue.worklog", "Log time on this issue", kernel.ActLogTime, &collabBindings[collabTime], CollabMsg{collabTime}},
		{"issue.watchers", "Watch this issue, or see who does", kernel.ActWatchers, &collabBindings[collabWatchers], CollabMsg{collabWatchers}},
		{"issue.clone", "Clone this issue", "", nil, CollabMsg{collabClone}},
		{"issue.parent", "Go to this issue's parent", kernel.ActParent, &parentKey, CollabMsg{collabParent}},
		{"issue.children", "List this issue's children", kernel.ActChildren, &childrenKey, CollabMsg{collabChildren}},
		{"issue.childrenInList", "Show this issue's children in the issue list", "", nil, CollabMsg{collabChildList}},
	} {
		var keys []string
		if c.b != nil {
			keys = []string{c.b.Help().Key}
		}
		kernel.RegisterCommand(kernel.Command{
			ID: c.id, Title: c.title, Group: "Issue", Action: c.act, Keys: keys,
			Run: func(kernel.Deps) tea.Cmd { return kernel.Broadcast(c.msg) },
		})
	}
}

var parentKey, childrenKey = parentBinding(), childrenBinding()

func (m *Model) collabKey(stroke string) (tea.Cmd, bool) {
	if act := ShareStroke(stroke); act != ShareNone {
		return Share(m.deps, act, m.issue.Key), true
	}
	if at := collabStrokes[stroke]; at != collabNone {
		return m.openSheet(at), true
	}
	return nil, false
}

func (m *Model) collabMsg(msg tea.Msg) tea.Cmd {
	switch msg := msg.(type) {
	case ShareMsg:
		if !m.inactive {
			return Share(m.deps, msg.Act, m.issue.Key)
		}
	case CollabMsg:
		if !m.inactive {
			return m.openSheet(msg.Open)
		}
	case ChangedMsg:
		if msg.Key == m.issue.Key {
			return m.fetch()
		}
		return m.childChanged(msg.Key)
	}
	return nil
}

func (m *Model) openSheet(at collabAct) tea.Cmd {
	if m.issue.Key == "" || m.stage != sideBrowse {
		return nil
	}
	var kind sheetKind
	title := m.issue.Key + " "
	switch at {
	case collabParent:
		return m.openParent()
	case collabChildren:
		return m.openChildren()
	case collabChildList:
		return m.showChildrenInList()
	case collabLinks:
		kind, title = &linksKind{}, title+"links"
	case collabTime:
		kind, title = &timeKind{}, title+"time"
	case collabWatchers:
		kind, title = &watchKind{}, title+"watchers"
	case collabClone:
		kind, title = &cloneKind{}, "Clone "+m.issue.Key
	default:
		return nil
	}
	sh := newSheet(m.deps, m.issue, kind)
	sh.trail = m.trailBeneath()
	return kernel.Push("issue.sheet", title, sh)
}
