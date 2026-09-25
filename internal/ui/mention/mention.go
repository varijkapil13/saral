// Package mention is @-autocomplete for a markdown textarea: it watches the
// word under the cursor, asks the site for people once typing pauses, and
// replaces "@name" with the mention markdown pkg/adf parses into a real mention
// node.
package mention

import (
	"context"
	"strings"
	"sync/atomic"
	"time"
	"unicode"

	"charm.land/bubbles/v2/textarea"
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/varijkapil13/saral/internal/ui/widget"
	"github.com/varijkapil13/saral/pkg/adf"
	"github.com/varijkapil13/saral/pkg/jira"
)

// Delay is how long typing has to pause before the site is asked. Every
// keystroke would otherwise be a request, and PeopleQuery's matching cannot be
// narrowed locally.
const Delay = 250 * time.Millisecond

const (
	limit    = 8
	maxShown = 5
	maxQuery = 40
)

// DueMsg says typing paused on the query of the generation it carries.
type DueMsg struct {
	owner uint64
	gen   int
}

// FoundMsg is the site's answer to one query.
type FoundMsg struct {
	owner  uint64
	gen    int
	people []jira.User
	err    error
}

// Search is what a host lets the state ask: a finder and the project to scope
// the search to, or why there is nothing to ask.
type Search struct {
	Finder  jira.PeopleFinder
	Project string
	Blocked string
}

// Look is how the suggestion lines are drawn.
type Look struct {
	Row, Selected, Muted lipgloss.Style
	Arrow, Ellipsis      string
}

var owners atomic.Uint64

// State is one textarea's autocomplete. The zero value is ready to use.
type State struct {
	owner uint64
	gen   int

	open      bool
	line, at  int
	query     string
	asked     string
	dismissed [2]int

	people  []jira.User
	cursor  int
	loading bool
	fail    string
	cancel  context.CancelFunc
}

// Open reports whether suggestions are showing.
func (s *State) Open() bool { return s.open }

// Close puts the suggestions away and gives up on any question in flight.
func (s *State) Close() {
	if s.cancel != nil {
		s.cancel()
		s.cancel = nil
	}
	s.open, s.query, s.asked, s.people, s.cursor, s.loading, s.fail = false, "", "", nil, 0, false, ""
	s.gen++
}

// Area is what the state reads of a textarea: its text and where the cursor is.
type Area interface {
	Value() string
	Line() int
	Column() int
}

// token finds the "@query" the cursor sits at the end of. An @ inside a word
// is an email address, and "@[" is a mention already written.
func token(ta Area) (line, at int, query string, ok bool) {
	line = ta.Line()
	text := ta.Value()
	for range line {
		i := strings.IndexByte(text, '\n')
		if i < 0 {
			return 0, 0, "", false
		}
		text = text[i+1:]
	}
	if i := strings.IndexByte(text, '\n'); i >= 0 {
		text = text[:i]
	}
	runes := []rune(text)
	col := min(ta.Column(), len(runes))
	for i := col - 1; i >= 0 && col-i <= maxQuery+1; i-- {
		r := runes[i]
		if r == '@' {
			if i > 0 && !unicode.IsSpace(runes[i-1]) && runes[i-1] != '(' {
				return 0, 0, "", false
			}
			q := string(runes[i+1 : col])
			if strings.HasPrefix(q, "[") {
				return 0, 0, "", false
			}
			return line, i, q, true
		}
		if unicode.IsSpace(r) || strings.ContainsRune("[]()", r) {
			return 0, 0, "", false
		}
	}
	return 0, 0, "", false
}

// Track re-reads the word under the cursor after the text or the cursor moved.
// It answers with the timer that asks the site once typing pauses, which the
// host addresses back to itself, or nil when there is nothing new to ask.
func (s *State) Track(ta Area, after func(time.Duration, func() tea.Msg) tea.Cmd) tea.Cmd {
	if s.owner == 0 {
		s.owner = owners.Add(1)
	}
	line, at, query, ok := token(ta)
	if !ok || s.dismissed == [2]int{line + 1, at + 1} {
		if s.open {
			s.Close()
		}
		if !ok {
			s.dismissed = [2]int{}
		}
		return nil
	}
	s.open, s.line, s.at = true, line, at
	if query == s.query {
		return nil
	}
	s.query = query
	s.gen++
	if strings.TrimSpace(query) == "" {
		s.people, s.cursor, s.loading, s.fail = nil, 0, false, ""
		return nil
	}
	owner, gen := s.owner, s.gen
	return after(Delay, func() tea.Msg { return DueMsg{owner: owner, gen: gen} })
}

// Update takes a DueMsg or a FoundMsg meant for this state. handled is false
// for anything else, including another textarea's answers.
func (s *State) Update(msg tea.Msg, search Search) (cmd tea.Cmd, handled bool) {
	switch msg := msg.(type) {
	case DueMsg:
		if msg.owner != s.owner || s.owner == 0 {
			return nil, false
		}
		if msg.gen != s.gen || !s.open || s.query == s.asked {
			return nil, true
		}
		return s.ask(search), true
	case FoundMsg:
		if msg.owner != s.owner || s.owner == 0 {
			return nil, false
		}
		if msg.gen != s.gen || !s.open {
			return nil, true
		}
		s.loading = false
		if msg.err != nil {
			s.fail, _ = jira.Reason(msg.err)
			s.people = nil
			return nil, true
		}
		s.fail, s.people, s.cursor = "", msg.people, 0
		return nil, true
	}
	return nil, false
}

func (s *State) ask(search Search) tea.Cmd {
	s.asked = s.query
	if search.Finder == nil || search.Blocked != "" {
		s.fail = search.Blocked
		if s.fail == "" {
			s.fail = "there is no Jira connection in this session"
		}
		return nil
	}
	if s.cancel != nil {
		s.cancel()
	}
	ctx, cancel := context.WithCancel(context.Background())
	s.cancel, s.loading, s.fail = cancel, true, ""
	owner, gen, q := s.owner, s.gen, jira.PeopleQuery{Match: s.query, Project: search.Project, Limit: limit}
	finder := search.Finder
	return func() tea.Msg {
		defer cancel()
		people, err := finder.FindPeople(ctx, q)
		return FoundMsg{owner: owner, gen: gen, people: people, err: err}
	}
}

// Key answers a key while suggestions are showing: up and down move, enter and
// tab write the mention, esc puts the list away until the next @. false means
// the key belongs to the textarea.
func (s *State) Key(msg tea.KeyPressMsg, ta *textarea.Model) bool {
	if !s.open {
		return false
	}
	switch msg.String() {
	case "esc":
		s.dismissed = [2]int{s.line + 1, s.at + 1}
		s.Close()
		return true
	case "up", "ctrl+p":
		if len(s.people) == 0 {
			return false
		}
		s.cursor = max(s.cursor-1, 0)
		return true
	case "down", "ctrl+n":
		if len(s.people) == 0 {
			return false
		}
		s.cursor = min(s.cursor+1, min(len(s.people), maxShown)-1)
		return true
	case "enter", "tab":
		if len(s.people) == 0 {
			return false
		}
		s.insert(ta, s.people[s.cursor])
		return true
	}
	return false
}

// insert replaces "@query" with the mention's markdown and a space after it.
func (s *State) insert(ta *textarea.Model, who jira.User) {
	back := tea.KeyPressMsg{Code: tea.KeyBackspace}
	for range []rune("@" + s.query) {
		*ta, _ = ta.Update(back)
	}
	ta.InsertString(adf.MentionMarkdown(who.DisplayName, who.AccountID) + " ")
	s.Close()
}

// Height is how many lines Lines draws.
func (s *State) Height() int {
	if !s.open {
		return 0
	}
	switch {
	case strings.TrimSpace(s.query) == "", s.loading, s.fail != "", len(s.people) == 0:
		return 1
	default:
		return min(len(s.people), maxShown) + 1
	}
}

// Lines draws the suggestions at one width.
func (s *State) Lines(width int, look Look) []string {
	n := s.Height()
	if n == 0 {
		return nil
	}
	out := make([]string, 0, n)
	note := func(text string) []string {
		return append(out, look.Muted.Render(ansi.Truncate("  "+text, max(width, 1), look.Ellipsis)))
	}
	switch {
	case strings.TrimSpace(s.query) == "":
		return note("type a name to mention someone")
	case s.loading:
		return note("looking for people" + look.Ellipsis)
	case s.fail != "":
		return note(s.fail)
	case len(s.people) == 0:
		return note("nobody matches @" + widget.Sanitize(s.query))
	}
	for i, p := range s.people[:min(len(s.people), maxShown)] {
		name := widget.Sanitize(strings.TrimSpace(p.DisplayName))
		if name == "" {
			name = widget.Sanitize(p.AccountID)
		}
		line, style := "    @"+name, look.Row
		if i == s.cursor {
			line, style = "  "+look.Arrow+" @"+name, look.Selected
		}
		out = append(out, style.Render(ansi.Truncate(line, max(width, 1), look.Ellipsis)))
	}
	return note("enter or tab mentions, esc closes")
}
