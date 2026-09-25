package board

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

func newFindInput() textinput.Model {
	in := widget.NewInput()
	in.Prompt = "/"
	in.Placeholder = "a key or words of a summary"
	return in
}

// startFind opens the prompt. The cards it walks are the ones drawn, in
// reading order: down the first column, then down the next.
func (m *Model) startFind() tea.Cmd {
	if m.moving || m.card != nil {
		return nil
	}
	if !m.drawable() || len(m.issues) == 0 {
		return kernel.Warn("there are no cards on this board to search")
	}
	m.finding, m.findMiss = true, false
	m.findCol, m.findRow = m.curCol, m.curRow
	m.find.Reset()
	m.find.SetValue(m.needle)
	m.find.CursorEnd()
	m.find.SetWidth(max(min(m.width-24, 60), 8))
	_ = m.find.Focus()
	m.follow()
	m.forget()
	return nil
}

func (m *Model) findKey(msg tea.KeyPressMsg) tea.Cmd {
	switch m.inFind[msg.String()] {
	case actFindKeep:
		m.endFind()
		if m.needle != "" && m.findMiss {
			return kernel.Warn("no card on this board matches " + m.needle)
		}
		return nil
	case actFindCancel:
		m.needle = ""
		m.endFind()
		m.moveTo(m.findCol, m.findRow)
		return nil
	default:
	}
	// The input's own command is a cursor blink, a timer this view would then
	// own for as long as the prompt is open.
	m.find, _ = m.find.Update(msg)
	if q := strings.TrimSpace(m.find.Value()); q != m.needle {
		m.needle = q
		m.findMiss = false
		if q == "" {
			m.moveTo(m.findCol, m.findRow)
		} else if col, row, ok := m.nextMatch(m.findCol, m.findRow, 1, true); ok {
			m.moveTo(col, row)
		} else {
			m.findMiss = true
		}
		m.forget()
	}
	return nil
}

func (m *Model) endFind() {
	m.finding = false
	m.find.Blur()
	m.follow()
	m.forget()
}

// findAgain walks to the next card the kept search matches, or the one before,
// wrapping at either end.
func (m *Model) findAgain(dir int) tea.Cmd {
	if m.moving || m.card != nil {
		return nil
	}
	if m.needle == "" {
		return kernel.Warn("nothing is being searched for; " + defaultKeys().Find.Help().Key + " starts a search")
	}
	col, row, ok := m.nextMatch(m.curCol, m.curRow, dir, false)
	if !ok {
		return kernel.Warn("no card on this board matches " + m.needle)
	}
	m.moveTo(col, row)
	return nil
}

// nextMatch is the first card from (col, row) in the direction given that the
// needle matches, the start itself included when inclusive.
func (m *Model) nextMatch(col, row, dir int, inclusive bool) (atCol, atRow int, found bool) {
	total, at := 0, -1
	for c := range m.cols {
		if c == col {
			at = total + min(row, max(len(m.cols[c])-1, 0))
		}
		total += len(m.cols[c])
	}
	if total == 0 {
		return 0, 0, false
	}
	at = max(at, 0)
	start := 1
	if inclusive {
		start = 0
	}
	for step := start; step < total+start; step++ {
		i := ((at+dir*step)%total + total) % total
		c, r := m.cardAt(i)
		if matchesNeedle(m.issueAt(c, r), m.needle) {
			return c, r, true
		}
	}
	return 0, 0, false
}

// cardAt is the i-th card in reading order.
func (m *Model) cardAt(i int) (col, row int) {
	for c := range m.cols {
		if i < len(m.cols[c]) {
			return c, i
		}
		i -= len(m.cols[c])
	}
	return 0, 0
}

func matchesNeedle(iss *jira.Issue, needle string) bool {
	return iss != nil && (containsFold(iss.Key, needle) || containsFold(iss.Summary, needle))
}

// containsFold is a case-insensitive strings.Contains that allocates nothing,
// which a search run on every keystroke over every card has to be.
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

// findPrompt is the line the prompt takes under the grid.
func (m *Model) findPrompt() string {
	ell := m.deps.Theme.Glyphs.Ellipsis
	line := m.find.View()
	if m.findMiss {
		line += m.styles.warning.Render("  no card matches")
	}
	return padCells(line, m.width, ell)
}

type meMsg struct {
	user jira.User
	err  error
}

// toggleMine narrows the board to the account this session is signed in as,
// or takes that narrowing off again. The account is asked for once.
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
		return kernel.Warn("the site named no account for this session, so there is nobody to narrow the board to")
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
