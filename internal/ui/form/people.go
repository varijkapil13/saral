package form

import (
	"context"

	tea "charm.land/bubbletea/v2"

	"github.com/varijkapil13/saral/internal/ui/kernel"
	"github.com/varijkapil13/saral/pkg/jira"
)

// peopleLimit bounds one search: a person is found by typing more, not by
// paging, so this is a ceiling the picker never pages past.
const peopleLimit = 20

type peopleSearch struct {
	gen     int
	cancel  context.CancelFunc
	asked   string
	loading bool
	fail    string
}

type peopleFoundMsg struct {
	gen    int
	people []jira.User
}

type peopleFailedMsg struct {
	gen int
	err error
}

func findPeople(ctx context.Context, finder jira.PeopleFinder, project, match string, gen int) tea.Cmd {
	return func() tea.Msg {
		people, err := finder.FindPeople(ctx, jira.PeopleQuery{Match: match, Project: project, Limit: peopleLimit})
		if err != nil {
			return peopleFailedMsg{gen: gen, err: err}
		}
		return peopleFoundMsg{gen: gen, people: people}
	}
}

// findPeople asks the site for the accounts matching needle, giving up on the
// previous question. Every keystroke goes back to the site rather than
// narrowing what is held, because jira.PeopleQuery's matching cannot be
// reproduced locally: a longer needle can find someone a shorter one did not.
func (m *Model) findPeople(needle string) tea.Cmd {
	m.people.asked = needle
	if reason, blocked := m.peopleBlocked(); blocked {
		m.people.fail = reason
		return nil
	}
	m.stopPeople()
	m.people.gen++
	ctx, cancel := context.WithCancel(context.Background())
	m.people.cancel, m.people.loading, m.people.fail = cancel, true, ""
	return kernel.Reply(withCancel(cancel, findPeople(ctx, m.deps.Jira, m.project, needle, m.people.gen)), m.addr)
}

func (m *Model) stopPeople() {
	if m.people.cancel != nil {
		m.people.cancel()
		m.people.cancel = nil
	}
	m.people.loading = false
}

// peopleBlocked is why accounts cannot be looked up at all: no connection, or a
// token without the permission to browse users.
func (m *Model) peopleBlocked() (string, bool) {
	if m.deps.Jira == nil {
		return "there is no Jira connection in this session yet", true
	}
	got := m.deps.Caps.Capability(jira.CapPeople)
	if got.OK {
		return "", false
	}
	if got.Reason != "" {
		return got.Reason, true
	}
	return "this token cannot look accounts up", true
}

func (m *Model) choosingPeople() bool {
	return m.edit == editChoose && m.editing < len(m.fields) && m.fields[m.editing].kind.people()
}

func (m *Model) peopleFound(msg peopleFoundMsg) {
	if !m.choosingPeople() || msg.gen != m.people.gen {
		return
	}
	m.people.loading = false
	under := ""
	if visible := m.visibleChoices(); m.pick >= 0 && m.pick < len(visible) {
		under = m.choices[visible[m.pick]].value.ID
	}
	m.choices = m.userChoices(m.fields[m.editing], m.chosenOptions(), msg.people)
	m.pick, m.pickTop = 0, 0
	for i, at := range m.visibleChoices() {
		if m.choices[at].value.ID == under {
			m.pick = i
			break
		}
	}
	m.scrollChoices()
}

// peopleFailed says on the picker why the search did not answer. The filter
// keeps what was typed and the picker keeps what it already offered.
func (m *Model) peopleFailed(msg peopleFailedMsg) tea.Cmd {
	if !m.choosingPeople() || msg.gen != m.people.gen {
		return nil
	}
	m.people.loading = false
	m.people.fail, _ = jira.Reason(msg.err)
	return kernel.Fail(msg.err)
}

// userChoices are the accounts a person picker offers: the field's own list
// where it states one, this session's own account, whatever is chosen already,
// and what the site last answered for the typed name. The site's answers are
// marked found, so the local filter does not hide a match Jira made on
// initials or an email address.
func (m *Model) userChoices(f *field, on []jira.Option, found []jira.User) []choice {
	out := make([]choice, 0, len(f.meta.AllowedValues)+len(on)+len(found)+1)
	seen := make(map[string]int, cap(out))
	add := func(label string, option jira.Option, fromSite bool) {
		if option.ID == "" {
			return
		}
		if at, ok := seen[option.ID]; ok {
			out[at].found = out[at].found || fromSite
			return
		}
		seen[option.ID] = len(out)
		out = append(out, choice{label: label, value: option, found: fromSite})
	}
	for _, option := range f.meta.AllowedValues {
		add(option.Label, option, false)
	}
	if m.haveMe {
		me := userOption(m.me)
		add(me.Label+" (me)", me, false)
	}
	for _, option := range on {
		add(option.Label, option, false)
	}
	for _, user := range found {
		option := userOption(user)
		add(option.Label, option, true)
	}
	for i := range out {
		out[i].on = false
		for _, option := range on {
			if option.ID == out[i].value.ID {
				out[i].on = true
			}
		}
	}
	return out
}
