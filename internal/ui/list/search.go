package list

import (
	"strconv"
	"strings"

	"charm.land/bubbles/v2/textinput"
	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/varijkapil13/saral/internal/ui/kernel"
	"github.com/varijkapil13/saral/internal/ui/widget"
)

// search is one of the searches this view offers by name: the predicate half,
// which is empty for every issue there is, and the order to read the answer in.
// The two are kept apart because a search with no predicate has no AND to hang
// an ORDER BY off, and "every issue in this project" is exactly that search.
type search struct {
	id   string
	name string
	// command is what the palette calls it, where that is not the name the view
	// puts on screen.
	command string
	where   string
	order   string
}

// The searches every session offers. Nothing here names a project, a status or
// a field: the project is whatever the session is scoped to and goes in at run
// time.
var (
	everyIssue = search{
		id: "issues.all", name: "All issues", command: "Every issue in this project",
		order: "ORDER BY updated DESC",
	}
	myIssues = search{
		id: "issues.mine", name: "My issues",
		where: "assignee = currentUser()", order: "ORDER BY updated DESC",
	}
	iReported = search{
		id: "issues.reported", name: "Issues I reported",
		where: "reporter = currentUser()", order: "ORDER BY created DESC",
	}
	nobodysIssues = search{
		id: "issues.unassigned", name: "Unassigned issues",
		where: "assignee IS EMPTY", order: "ORDER BY created DESC",
	}
)

var searches = []search{everyIssue, myIssues, iReported, nobodysIssues}

// palette is what the command palette calls the search.
func (s search) palette() string {
	if s.command != "" {
		return s.command
	}
	return s.name
}

// at composes the search for the project the session is on, and names it after
// what it is about to put on screen.
func (s search) at(project string) (jql, title string) {
	jql = strings.TrimSpace(scoped(project, s.where) + " " + s.order)
	if p := strings.TrimSpace(project); p != "" {
		return jql, s.name + " in " + p
	}
	return jql, s.name
}

// scoped narrows a clause to the session's project when there is one. An empty
// clause is every issue in that project, so there is nothing to put an AND
// between. The key is whatever the session was opened against; nothing about it
// is written down.
func scoped(project, clause string) string {
	p, c := strings.TrimSpace(project), strings.TrimSpace(clause)
	switch {
	case p == "":
		return c
	case c == "":
		return "project = " + quote(p)
	}
	return "project = " + quote(p) + " AND " + c
}

func quote(s string) string {
	return strconv.Quote(strings.ReplaceAll(s, `"`, ""))
}

// defaultQuery is what a session opens on: the account's own work, narrowed to
// the project the session is scoped to. An account with nothing of its own in
// that project meets the project itself instead — see widen.
func defaultQuery(project string) (jql, title string) { return myIssues.at(project) }

// showEverything runs the whole of the session's project. It is the one search
// the three that were here could not express: they all narrow by who you are,
// and a token that is nobody in particular matches none of them.
func (m *Model) showEverything() tea.Cmd {
	jql, title := everyIssue.at(m.deps.Project)
	if jql == m.jql {
		return kernel.Status(title + " is already what is on screen")
	}
	return m.setQuery(jql, title, false)
}

// widen answers the first load of the search this view chose itself coming back
// with nothing. An account with nothing assigned to it in a project met an empty
// screen and read it as a broken program, so it meets the project instead —
// once, and told why. A search the user ran is never widened, and a project that
// is itself empty says so rather than being asked again.
func (m *Model) widen() tea.Cmd {
	project := strings.TrimSpace(m.deps.Project)
	if !m.defaulted || m.widened || len(m.issues) > 0 || project == "" {
		return nil
	}
	jql, title := everyIssue.at(project)
	if jql == m.jql {
		return nil
	}
	m.widened = true
	return tea.Batch(
		kernel.Status("nothing in "+project+" is assigned to you, so this is every issue in it"),
		m.setQuery(jql, title, true),
	)
}

// --- the search on screen, shown and edited where it is ---------------------

// EditQueryMsg opens the prompt that shows the search on screen and runs an
// edited one. It is exported so that the palette reaches the same gesture the
// key does rather than a second implementation of it.
type EditQueryMsg struct{}

// askPrefix is the prompt's own label. It names what is being typed, which is
// half of why the prompt exists: until it is opened, the only account of the
// search on screen is the title's summary of it.
const askPrefix = "jql "

const askHint = "  enter runs it, esc keeps this one"

func newAskInput() textinput.Model {
	ti := widget.NewInput()
	ti.Prompt = askPrefix
	ti.Placeholder = "a search to run"
	return ti
}

// askRoom is how wide the prompt line's editable half is, leaving the hint the
// columns it needs. The search gives way on a narrow terminal, never the keys
// that answer.
func (m *Model) askRoom() int { return max(m.width-ansi.StringWidth(askHint), 8) }

// askWidth is what the text input itself is given, so that the prompt's label
// and its editable half together fill the room left over.
func (m *Model) askWidth() int { return max(m.askRoom()-ansi.StringWidth(askPrefix), 4) }

func (m *Model) startAsk() tea.Cmd {
	m.asking = true
	m.ask.SetValue(m.jql)
	// The width goes on before the cursor moves, so that a search too long for
	// the room ends up windowed on the end somebody is about to type at rather
	// than on its start with the cursor off screen.
	m.ask.SetWidth(m.askWidth())
	m.ask.CursorEnd()
	_ = m.ask.Focus()
	m.clampScroll()
	return nil
}

func (m *Model) closeAsk() {
	m.asking = false
	m.ask.Blur()
	m.clampScroll()
}

// askKey takes the prompt's keys. Everything the prompt's own table does not
// claim is text, which is why the view claims raw keys while it is open.
func (m *Model) askKey(msg tea.KeyPressMsg, stroke string) tea.Cmd {
	switch m.inAsk[stroke] {
	case actRun:
		asked := strings.TrimSpace(m.ask.Value())
		m.closeAsk()
		return m.runAsked(asked)
	case actKeep:
		m.closeAsk()
		return nil
	default:
	}
	// The text input's own command is a cursor blink, dropped here for the same
	// reason the filter drops it: it is a timer this view would then own.
	m.ask, _ = m.ask.Update(msg)
	return nil
}

// runAsked runs what the prompt was left holding. An edited search is titled
// after itself, because the words that named the old one no longer describe what
// is on screen.
func (m *Model) runAsked(jql string) tea.Cmd {
	switch jql {
	case "":
		return kernel.Warn("an empty search is not one, so " + strconv.Quote(m.title) + " is still on screen")
	case m.jql:
		return nil
	}
	return m.setQuery(jql, jql, false)
}

// askPrompt is the line the prompt puts under the rows: the search itself, as
// the site will be asked it, and what enter will do with it.
//
// The text input pads itself to its own width, so what is cut here to make room
// for the hint is that padding — the input has already windowed a search too
// long to fit, and cutting with no ellipsis is what keeps one out of the middle
// of a row of spaces.
func (m *Model) askPrompt() string {
	room, line := m.askRoom(), m.ask.View()
	if w := ansi.StringWidth(line); w < room {
		line += strings.Repeat(" ", room-w)
	} else {
		line = ansi.Truncate(line, room, "")
	}
	return line + m.styles.muted.Render(askHint)
}

// titleZone is the summary line's own name. Clicking what says which search is
// on screen opens the prompt that changes it, so the search is reachable by
// pointer as well as by key and from the palette.
const titleZone = "title"
