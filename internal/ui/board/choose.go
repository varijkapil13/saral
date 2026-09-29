package board

import (
	"context"
	"strconv"
	"strings"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/varijkapil13/saral/internal/ui/issue"
	"github.com/varijkapil13/saral/internal/ui/kernel"
	"github.com/varijkapil13/saral/internal/ui/widget"
	"github.com/varijkapil13/saral/pkg/jira"
)

const zoneChoice = "choice:"

func choiceZone(at int) string { return zoneChoice + strconv.Itoa(at) }

// movesInto is every transition landing in a column, in the order the site
// offered them. A column mapping several statuses has no way of preferring one
// that is not a guess, so more than one is a question for the user.
func (m *Model) movesInto(list []jira.Transition, col int) []jira.Transition {
	var out []jira.Transition
	for _, tr := range list {
		if at, mapped := m.plan.columnOf(tr.To.ID); mapped && at == col {
			out = append(out, tr)
		}
	}
	return out
}

// distinctTargets keeps the first transition to each status, which is what a
// set of cards is asked about: each card takes its own transition to the status
// chosen, and a transition id belongs to one issue's workflow.
func distinctTargets(list []jira.Transition) []jira.Transition {
	out := make([]jira.Transition, 0, len(list))
	seen := make(map[string]bool, len(list))
	for _, tr := range list {
		if seen[tr.To.ID] {
			continue
		}
		seen[tr.To.ID] = true
		out = append(out, tr)
	}
	return out
}

// choiceLabels are for display only: names are neither unique nor stable, so
// nothing reads a label back.
func choiceLabels(list []jira.Transition) []string {
	fold := func(s string) string { return strings.ToLower(strings.TrimSpace(widget.Sanitize(s))) }
	sameStatus := make(map[string]int, len(list))
	for _, tr := range list {
		sameStatus[fold(tr.To.Name)]++
	}
	out := make([]string, 0, len(list))
	for _, tr := range list {
		status := strings.TrimSpace(widget.Sanitize(tr.To.Name))
		name := strings.TrimSpace(widget.Sanitize(tr.Name))
		label := status
		switch {
		case status == "" && name != "":
			label = name
		case status == "":
			label = tr.To.ID
		case name != "" && (sameStatus[fold(status)] > 1 || fold(name) != fold(status)):
			label = status + " (" + name + ")"
		}
		out = append(out, label)
	}
	alike := make(map[string]int, len(out))
	for _, label := range out {
		alike[label]++
	}
	for i, label := range out {
		if alike[label] > 1 {
			out[i] = label + " [" + list[i].ID + "]"
		}
	}
	return out
}

func (m *Model) choosing() bool { return m.card != nil && len(m.card.choices) > 0 }

func (m *Model) land(key string, col int, tr jira.Transition) tea.Cmd {
	if needsScreen(tr) {
		iss := m.byKey(key)
		m.putBack()
		if iss == nil {
			return nil
		}
		return tea.Batch(
			kernel.Status(widget.Sanitize(tr.Name)+" needs more than a column, so it is being asked for"),
			kernel.Push(issue.ViewID, iss.Key, issue.New(m.deps, *iss, issue.WithTransition(tr.ID))),
		)
	}
	if m.deps.Jira == nil {
		m.putBack()
		return kernel.Warn("there is no Jira connection in this session")
	}
	name := m.plan.columns[col].name
	from := m.plan.columns[m.card.from].name
	m.card.choices, m.card.labels = nil, nil
	ctx, gen := m.beginMove()
	return kernel.Reply(apply(ctx, m.deps.Jira, key, tr, name, from, gen), m.addr)
}

func (m *Model) chooseKey(stroke string) tea.Cmd {
	c := m.card
	switch m.inChoice[stroke] {
	case actPrev:
		c.choice = max(c.choice-1, 0)
		m.forget()
	case actNext:
		c.choice = min(c.choice+1, len(c.choices)-1)
		m.forget()
	case actAccept:
		return m.land(c.key, c.target, c.choices[c.choice])
	case actCancel:
		m.putBack()
	default:
	}
	return nil
}

func (m *Model) choiceUnder(msg tea.MouseMsg, n int) (int, bool) {
	for i := range n {
		if m.zones.Hit(choiceZone(i), msg) {
			return i, true
		}
	}
	return 0, false
}

func (m *Model) clickChoice(msg tea.MouseMsg) tea.Cmd {
	at, on := m.choiceUnder(msg, len(m.card.choices))
	if !on {
		return nil
	}
	m.card.choice = at
	return m.land(m.card.key, m.card.target, m.card.choices[at])
}

// choicePrompt gives way on the hint before the options on a narrow screen.
func (m *Model) choicePrompt(lead string, labels []string, at int, hints []string) string {
	ell := m.deps.Theme.Glyphs.Ellipsis
	var b strings.Builder
	b.WriteString(m.styles.aimed.Render(lead))
	for i, label := range labels {
		text, style := " "+label+" ", m.styles.muted
		if i == at {
			text, style = "["+label+"]", m.styles.selected
		}
		b.WriteString(" ")
		b.WriteString(m.zones.Mark(choiceZone(i), style.Render(text)))
	}
	left := b.String()
	for _, hint := range hints {
		if pad := m.width - ansi.StringWidth(left) - ansi.StringWidth(hint) - 2; pad > 0 {
			return left + strings.Repeat(" ", pad) + m.styles.muted.Render(hint+"  ")
		}
	}
	return padCells(left, m.width, ell)
}

var choiceHints = func() []string {
	k := defaultKeys()
	return []string{
		k.ChoosePrev.Help().Key + " " + k.ChooseNext.Help().Key + " choose · " +
			k.Choose.Help().Key + " moves it · " + k.Cancel.Help().Key + " puts it back",
		k.Choose.Help().Key + " moves it · " + k.Cancel.Help().Key + " puts it back",
		k.Choose.Help().Key + " · " + k.Cancel.Help().Key,
	}
}()

var targetHints = func() []string {
	k := defaultKeys()
	return []string{
		k.ChoosePrev.Help().Key + " " + k.ChooseNext.Help().Key + " choose · " +
			k.Choose.Help().Key + " takes it · " + k.PutBack.Help().Key + " cancels",
		k.Choose.Help().Key + " takes it · " + k.PutBack.Help().Key + " cancels",
		k.Choose.Help().Key + " · " + k.PutBack.Help().Key,
	}
}()

// --- a picked set -------------------------------------------------------------

type targetsMsg struct {
	gen     int
	options []jira.Transition
	err     error
}

// askTarget reads the moves of the first card that has to move, so the
// question names statuses this site actually calls something. Each card still
// takes its own transition when the set runs.
func (m *Model) askTarget(b *bulk) tea.Cmd {
	probe := ""
	for _, key := range b.keys {
		iss := m.byKey(key)
		if iss == nil {
			continue
		}
		if at, mapped := m.plan.columnOf(iss.Status.ID); mapped && at == b.col {
			continue
		}
		probe = key
		break
	}
	if probe == "" {
		return nil
	}
	b.stage = stageAskStatus
	b.askGen++
	ctx, cancel := context.WithCancel(context.Background())
	b.askStop, b.searching = cancel, true
	mover, gen, col, p := m.deps.Jira, b.askGen, b.col, m.plan
	return kernel.Reply(withCancel(cancel, func() tea.Msg {
		list, err := mover.Transitions(ctx, probe)
		if err != nil {
			return targetsMsg{gen: gen, err: err}
		}
		var into []jira.Transition
		for _, tr := range list {
			if at, mapped := p.columnOf(tr.To.ID); mapped && at == col {
				into = append(into, tr)
			}
		}
		return targetsMsg{gen: gen, options: distinctTargets(into)}
	}), m.addr)
}

func (m *Model) tookTargets(msg targetsMsg) tea.Cmd {
	b := m.bulk
	if b == nil || b.stage != stageAskStatus || msg.gen != b.askGen {
		return nil
	}
	b.askStop, b.searching = nil, false
	m.forget()
	if msg.err != nil {
		m.bulk = nil
		return kernel.Fail(msg.err)
	}
	if len(msg.options) < 2 {
		b.stage = stageConfirm
		return nil
	}
	b.choices, b.labels, b.at = msg.options, statusLabels(msg.options), 0
	return nil
}

// statusLabels name a set's options by status alone: the transition belongs to
// the one card read, and every other card takes its own. Two statuses sharing a
// name are told apart by id.
func statusLabels(list []jira.Transition) []string {
	out := make([]string, 0, len(list))
	alike := make(map[string]int, len(list))
	for _, tr := range list {
		label := strings.TrimSpace(widget.Sanitize(tr.To.Name))
		if label == "" {
			label = tr.To.ID
		}
		out = append(out, label)
		alike[label]++
	}
	for i, label := range out {
		if alike[label] > 1 && label != list[i].To.ID {
			out[i] = label + " [" + list[i].To.ID + "]"
		}
	}
	return out
}

func (m *Model) chooseTarget(stroke string) tea.Cmd {
	b := m.bulk
	switch m.inChoice[stroke] {
	case actPrev:
		b.at = max(b.at-1, 0)
		m.forget()
	case actNext:
		b.at = min(b.at+1, max(len(b.choices)-1, 0))
		m.forget()
	case actAccept:
		return m.takeTarget(b.at)
	case actCancel:
		return m.dropBulk()
	default:
	}
	return nil
}

func (m *Model) takeTarget(at int) tea.Cmd {
	b := m.bulk
	if b.searching || at < 0 || at >= len(b.choices) {
		return nil
	}
	b.status, b.statusName = b.choices[at].To.ID, b.labels[at]
	b.choices, b.labels = nil, nil
	b.stage = stageConfirm
	m.forget()
	return nil
}

func (m *Model) targetPrompt() string {
	b := m.bulk
	ell := m.deps.Theme.Glyphs.Ellipsis
	lead := "move " + countCards(len(b.keys)) + " to " + widget.Sanitize(b.name)
	if b.searching {
		return padCells(m.styles.muted.Render("  "+lead+": asking the site which statuses it can take"+ell), m.width, ell)
	}
	return m.choicePrompt("  "+lead+" as", b.labels, b.at, targetHints)
}
