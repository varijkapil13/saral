package backlog

import (
	"context"
	"strings"
	"unicode/utf8"

	"charm.land/bubbles/v2/textinput"
	tea "charm.land/bubbletea/v2"

	"github.com/varijkapil13/saral/internal/ui/filter"
	"github.com/varijkapil13/saral/internal/ui/kernel"
	"github.com/varijkapil13/saral/internal/ui/widget"
	"github.com/varijkapil13/saral/pkg/jira"
)

// FindMsg opens the search and MineMsg toggles only-my-issues. Both are
// exported so the palette reaches the gesture the key does.
type FindMsg struct{}

// MineMsg is described with FindMsg.
type MineMsg struct{}

func newFindInput() textinput.Model {
	in := widget.NewInput()
	in.Prompt = "/"
	in.Placeholder = "a key or words of a summary"
	return in
}

func (m *Model) startFind() tea.Cmd {
	if m.mode != browsing {
		return nil
	}
	if len(m.rows) == 0 {
		return kernel.Warn("there are no issues in this backlog to search")
	}
	m.mode, m.findMiss, m.findFrom = finding, false, m.cursor
	m.find.Reset()
	m.find.SetValue(m.needle)
	m.find.CursorEnd()
	m.find.SetWidth(max(min(m.width-24, 60), 8))
	_ = m.find.Focus()
	m.keepVisible()
	return nil
}

func (m *Model) findKey(msg tea.KeyPressMsg) tea.Cmd {
	switch m.inFind[msg.String()] {
	case actFindKeep:
		m.endFind()
		if m.needle != "" && m.findMiss {
			return kernel.Warn("no issue in this backlog matches " + m.needle)
		}
		return nil
	case actFindCancel:
		m.needle = ""
		m.endFind()
		return m.moveTo(m.findFrom)
	default:
	}
	// The input's own command is a cursor blink, a timer this view would then
	// own for as long as the prompt is open.
	m.find, _ = m.find.Update(msg)
	q := strings.TrimSpace(m.find.Value())
	if q == m.needle {
		return nil
	}
	m.needle, m.findMiss = q, false
	if q == "" {
		return m.moveTo(m.findFrom)
	}
	if at, ok := m.nextMatch(m.findFrom, 1, true); ok {
		return m.moveTo(at)
	}
	m.findMiss = true
	return nil
}

func (m *Model) endFind() {
	m.mode = browsing
	m.find.Blur()
	m.keepVisible()
}

func (m *Model) findAgain(dir int) tea.Cmd {
	if m.needle == "" {
		return kernel.Warn("nothing is being searched for; " + defaultKeys().Find.Help().Key + " starts a search")
	}
	at, ok := m.nextMatch(m.cursor, dir, false)
	if !ok {
		return kernel.Warn("no issue in this backlog matches " + m.needle)
	}
	return m.moveTo(at)
}

// nextMatch is the first issue row from a row in the direction given that the
// needle matches, wrapping at either end.
func (m *Model) nextMatch(from, dir int, inclusive bool) (int, bool) {
	n := len(m.rows)
	if n == 0 {
		return 0, false
	}
	from = min(max(from, 0), n-1)
	start := 1
	if inclusive {
		start = 0
	}
	for step := start; step < n+start; step++ {
		i := ((from+dir*step)%n + n) % n
		if matchesNeedle(m.issueAt(i), m.needle) {
			return i, true
		}
	}
	return 0, false
}

func matchesNeedle(iss *jira.Issue, needle string) bool {
	return iss != nil && (containsFold(iss.Key, needle) || containsFold(iss.Summary, needle))
}

// containsFold is a case-insensitive strings.Contains that allocates nothing,
// which a search run on every keystroke over every row has to be.
func containsFold(s, sub string) bool {
	if sub == "" {
		return true
	}
	for i := 0; i+len(sub) <= len(s); {
		if strings.EqualFold(s[i:i+len(sub)], sub) {
			return true
		}
		_, size := utf8.DecodeRuneInString(s[i:])
		i += size
	}
	return false
}

func (m *Model) findPrompt() string {
	line := m.find.View()
	if m.findMiss {
		line += m.styles.warn.Render("  no issue matches")
	}
	return m.fit(line)
}

type meMsg struct {
	user jira.User
	err  error
}

func (m *Model) toggleMine() tea.Cmd {
	if m.me != nil {
		return m.setTerms(mineToggled(m.terms, *m.me))
	}
	if m.deps.Jira == nil {
		return kernel.Warn("there is no Jira connection in this session")
	}
	if m.askingMe {
		return nil
	}
	m.askingMe = true
	who := m.deps.Jira
	return kernel.Reply(func() tea.Msg {
		u, err := who.Me(context.Background())
		return meMsg{user: u, err: err}
	}, m.addr)
}

func (m *Model) tookMe(msg meMsg) tea.Cmd {
	m.askingMe = false
	if msg.err != nil {
		return kernel.Fail(msg.err)
	}
	if msg.user.AccountID == "" {
		return kernel.Warn("the site named no account for this session, so there is nobody to narrow the backlog to")
	}
	me := msg.user
	m.me = &me
	return m.setTerms(mineToggled(m.terms, me))
}

// mineToggled is the terms with the assignee facet set to exactly this account,
// or with it taken off when that is already all it holds.
func mineToggled(terms filter.Terms, me jira.User) filter.Terms {
	mine := filter.Term{Facet: filter.FacetAssignee, ID: me.AccountID, Label: widget.Sanitize(me.DisplayName)}
	if terms.Has(mine) && terms.Count(filter.FacetAssignee) == 1 {
		return terms.Without(filter.FacetAssignee)
	}
	return terms.Without(filter.FacetAssignee).Toggle(mine)
}
