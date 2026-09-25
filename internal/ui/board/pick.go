package board

import (
	"context"
	"errors"
	"slices"
	"strconv"
	"strings"

	"charm.land/bubbles/v2/textinput"
	tea "charm.land/bubbletea/v2"

	"github.com/varijkapil13/saral/internal/app"
	"github.com/varijkapil13/saral/internal/ui/kernel"
	"github.com/varijkapil13/saral/internal/ui/widget"
	"github.com/varijkapil13/saral/pkg/jira"
)

// peopleLimit bounds one search for a person: somebody is found by typing
// more, not by paging.
const peopleLimit = 20

type bulkKind uint8

const (
	bulkAssign bulkKind = iota
	bulkMoveTo
	bulkLabel
)

type bulkStage uint8

const (
	stageAskPerson bulkStage = iota
	stageAskLabel
	stageConfirm
	stageRunning
)

// bulk is one change to many cards, from the question that shapes it through
// the confirmation to the run, which asks the site one card at a time so that
// a refusal part way names exactly which cards changed.
type bulk struct {
	kind  bulkKind
	stage bulkStage
	keys  []string

	who   jira.User
	col   int
	name  string
	label string

	input     textinput.Model
	found     []jira.User
	at        int
	asked     string
	askGen    int
	askStop   context.CancelFunc
	searching bool
	askFail   string

	next    int
	changed []string
	already []string
	failed  []bulkMiss
	halt    bool
	leave   bool
	runGen  int
	runStop context.CancelFunc
}

type bulkMiss struct {
	key string
	err error
}

func (b *bulk) keyState() keyState {
	switch b.stage {
	case stageAskPerson:
		return keysAskingPerson
	case stageAskLabel:
		return keysAskingLabel
	case stageConfirm:
		return keysConfirming
	case stageRunning:
		return keysRunning
	}
	return keysBrowsing
}

func (b *bulk) stopAsking() {
	if b.askStop != nil {
		b.askStop()
		b.askStop = nil
	}
	b.searching = false
}

func (b *bulk) stopRun() {
	if b.runStop != nil {
		b.runStop()
		b.runStop = nil
	}
}

type peopleMsg struct {
	gen    int
	people []jira.User
	err    error
}

type bulkStepMsg struct {
	gen    int
	key    string
	status jira.Status
	moved  bool
	err    error
}

// errNoMove is a card no workflow move takes into the column the set is going
// to; errScreen is one whose move needs a field only the issue pane can fill.
var (
	errNoMove = errors.New("no workflow move takes it into that column")
	errScreen = errors.New("its move needs a field filled in, which the issue pane asks for")
)

// --- picking ------------------------------------------------------------------

func (m *Model) togglePick() tea.Cmd {
	iss := m.issueAt(m.curCol, m.curRow)
	if iss == nil {
		return nil
	}
	m.setPicked(iss.Key, !m.picked[iss.Key])
	m.moveTo(m.curCol, m.curRow+1)
	return nil
}

// pickColumn picks every card in the cursor's column, or unpicks them all when
// every one of them is picked already.
func (m *Model) pickColumn() tea.Cmd {
	col := m.curCol
	if m.columnLen(col) == 0 {
		return nil
	}
	all := true
	for _, at := range m.cols[col] {
		if !m.picked[m.issues[at].Key] {
			all = false
			break
		}
	}
	for _, at := range m.cols[col] {
		m.setPicked(m.issues[at].Key, !all)
	}
	return nil
}

func (m *Model) setPicked(key string, on bool) {
	if on {
		if m.picked == nil {
			m.picked = make(map[string]bool, 8)
		}
		m.picked[key] = true
	} else {
		delete(m.picked, key)
	}
	m.dataGen++
	m.forget()
}

func (m *Model) unpickAll() tea.Cmd {
	if len(m.picked) == 0 {
		return nil
	}
	m.picked = nil
	m.dataGen++
	m.forget()
	return nil
}

// prunePicked lets go of a picked card that is no longer on the board: a read
// that no longer returns it, or a term that hides it.
func (m *Model) prunePicked() {
	if len(m.picked) == 0 {
		return
	}
	placed := make(map[string]bool, len(m.picked))
	for c := range m.cols {
		for _, at := range m.cols[c] {
			if key := m.issues[at].Key; m.picked[key] {
				placed[key] = true
			}
		}
	}
	for c := range m.folded {
		for _, at := range m.folded[c] {
			if key := m.issues[at].Key; m.picked[key] {
				placed[key] = true
			}
		}
	}
	m.picked = placed
}

// selection is what a bulk change acts on: the picked cards in reading order,
// or the card under the cursor when nothing is picked.
func (m *Model) selection() []string {
	if len(m.picked) == 0 {
		if iss := m.issueAt(m.curCol, m.curRow); iss != nil {
			return []string{iss.Key}
		}
		return nil
	}
	out := make([]string, 0, len(m.picked))
	for c := range m.cols {
		for _, at := range m.cols[c] {
			if key := m.issues[at].Key; m.picked[key] {
				out = append(out, key)
			}
		}
		if c < len(m.folded) {
			for _, at := range m.folded[c] {
				if key := m.issues[at].Key; m.picked[key] {
					out = append(out, key)
				}
			}
		}
	}
	return out
}

func countCards(n int) string {
	if n == 1 {
		return "1 card"
	}
	return strconv.Itoa(n) + " cards"
}

// --- starting a bulk change --------------------------------------------------

func (m *Model) bulkRefused() string {
	switch {
	case m.deps.Jira == nil:
		return "there is no Jira connection in this session"
	case m.moving || m.card != nil || m.finding || m.bulk != nil:
		return "finish what is under way first"
	}
	return ""
}

func (m *Model) newAsk(placeholder string) textinput.Model {
	in := widget.NewInput()
	in.Prompt = ""
	in.Placeholder = placeholder
	in.SetWidth(max(min(m.width/3, 40), 8))
	_ = in.Focus()
	return in
}

// startAssign asks who the cards go to. Nobody typed is this session's own
// account and nobody at all; anything typed is asked of the site.
func (m *Model) startAssign() tea.Cmd {
	if why := m.bulkRefused(); why != "" {
		return kernel.Warn(why)
	}
	keys := m.selection()
	if len(keys) == 0 {
		return kernel.Warn("there is no card under the cursor and nothing is picked")
	}
	if got := m.deps.Caps.Capability(jira.CapPeople); !got.OK {
		reason := got.Reason
		if reason == "" {
			reason = "this token cannot look accounts up"
		}
		return kernel.Warn(reason)
	}
	m.bulk = &bulk{kind: bulkAssign, stage: stageAskPerson, keys: keys, input: m.newAsk("a name to look up")}
	m.bulk.found = m.personSeed()
	m.forget()
	return nil
}

func (m *Model) personSeed() []jira.User {
	out := make([]jira.User, 0, 2)
	if m.me != nil {
		out = append(out, *m.me)
	}
	return append(out, jira.User{})
}

func (m *Model) startLabel() tea.Cmd {
	if why := m.bulkRefused(); why != "" {
		return kernel.Warn(why)
	}
	keys := m.selection()
	if len(keys) == 0 {
		return kernel.Warn("there is no card under the cursor and nothing is picked")
	}
	m.bulk = &bulk{kind: bulkLabel, stage: stageAskLabel, keys: keys, input: m.newAsk("a label")}
	m.forget()
	return nil
}

// pickUpSet takes every picked card in hand at once. Aiming it is the same
// left and right a single card takes, and landing it asks for confirmation.
func (m *Model) pickUpSet() tea.Cmd {
	if why := m.bulkRefused(); why != "" {
		return kernel.Warn(why)
	}
	if len(m.plan.columns) < 2 {
		return kernel.Warn("this board has one column, so there is nowhere to move these cards to")
	}
	iss := m.issueAt(m.curCol, m.curRow)
	key := ""
	if iss != nil {
		key = iss.Key
	}
	m.card = &held{key: key, from: m.curCol, row: m.curRow, target: m.curCol, set: true}
	m.forget()
	return nil
}

// dropSet ends the aim of a picked set on the confirmation for it.
func (m *Model) dropSet() tea.Cmd {
	target := m.card.target
	m.putBack()
	keys := m.selection()
	if len(keys) == 0 {
		return nil
	}
	m.bulk = &bulk{kind: bulkMoveTo, stage: stageConfirm, keys: keys, col: target, name: m.plan.columns[target].name}
	m.forget()
	return nil
}

// --- answering the questions --------------------------------------------------

func (m *Model) bulkKey(msg tea.KeyPressMsg) tea.Cmd {
	b := m.bulk
	stroke := msg.String()
	switch b.stage {
	case stageRunning:
		if m.inRun[stroke] == actHalt {
			b.halt = true
			m.forget()
			return kernel.Status("stopping once the card in flight has answered")
		}
		return nil
	case stageConfirm:
		switch m.inSure[stroke] {
		case actRun:
			return m.runBulk()
		case actDecline:
			return m.dropBulk()
		default:
		}
		return nil
	case stageAskPerson, stageAskLabel:
	}
	switch m.inAsk[stroke] {
	case actAccept:
		return m.answer()
	case actDecline:
		return m.dropBulk()
	case actPrev:
		if b.stage == stageAskPerson {
			b.at = max(b.at-1, 0)
			m.forget()
		}
		return nil
	case actNext:
		if b.stage == stageAskPerson {
			b.at = min(b.at+1, max(len(b.found)-1, 0))
			m.forget()
		}
		return nil
	default:
	}
	b.input, _ = b.input.Update(msg)
	m.forget()
	if b.stage != stageAskPerson {
		return nil
	}
	needle := strings.TrimSpace(b.input.Value())
	if needle == b.asked {
		return nil
	}
	return m.findPeople(needle)
}

// findPeople asks the site for the accounts matching needle, giving up on the
// question before it: a longer needle can find someone a shorter one did not,
// so nothing is narrowed locally.
func (m *Model) findPeople(needle string) tea.Cmd {
	b := m.bulk
	b.asked, b.at, b.askFail = needle, 0, ""
	b.stopAsking()
	if needle == "" {
		b.found = m.personSeed()
		return nil
	}
	b.askGen++
	ctx, cancel := context.WithCancel(context.Background())
	b.askStop, b.searching = cancel, true
	finder, project, gen := m.deps.Jira, m.deps.Project, b.askGen
	return kernel.Reply(withCancel(cancel, func() tea.Msg {
		people, err := finder.FindPeople(ctx, jira.PeopleQuery{Match: needle, Project: project, Limit: peopleLimit})
		return peopleMsg{gen: gen, people: people, err: err}
	}), m.addr)
}

func (m *Model) tookPeople(msg peopleMsg) tea.Cmd {
	b := m.bulk
	if b == nil || b.stage != stageAskPerson || msg.gen != b.askGen {
		return nil
	}
	b.askStop, b.searching = nil, false
	m.forget()
	if msg.err != nil {
		b.askFail, _ = jira.Reason(msg.err)
		b.found = nil
		return kernel.Fail(msg.err)
	}
	b.found = msg.people
	b.at = 0
	return nil
}

// answer takes what the prompt holds and moves on to the confirmation.
func (m *Model) answer() tea.Cmd {
	b := m.bulk
	switch b.stage {
	case stageAskPerson:
		if b.at < 0 || b.at >= len(b.found) {
			return kernel.Warn("nobody is chosen yet")
		}
		b.who = b.found[b.at]
	case stageAskLabel:
		label := strings.TrimSpace(b.input.Value())
		switch {
		case label == "":
			return kernel.Warn("type the label to add first")
		case strings.ContainsAny(label, " \t"):
			return kernel.Warn("a label cannot contain a space")
		}
		b.label = label
	case stageConfirm, stageRunning:
		return nil
	}
	b.stopAsking()
	b.input.Blur()
	b.stage = stageConfirm
	m.forget()
	return nil
}

func (m *Model) dropBulk() tea.Cmd {
	if m.bulk == nil {
		return nil
	}
	m.bulk.stopAsking()
	m.bulk.stopRun()
	m.bulk = nil
	m.forget()
	return nil
}

func personName(u jira.User) string {
	if u.AccountID == "" {
		return "nobody"
	}
	if name := strings.TrimSpace(widget.Sanitize(u.DisplayName)); name != "" {
		return name
	}
	return u.AccountID
}

// what is the change in words, which the confirmation and the report share.
func (b *bulk) what(n int) string {
	switch b.kind {
	case bulkAssign:
		if b.who.AccountID == "" {
			return "unassign " + countCards(n)
		}
		return "assign " + countCards(n) + " to " + personName(b.who)
	case bulkMoveTo:
		return "move " + countCards(n) + " to " + widget.Sanitize(b.name)
	case bulkLabel:
		return "add the label " + b.label + " to " + countCards(n)
	}
	return ""
}

func (b *bulk) done(n int) string {
	switch b.kind {
	case bulkAssign:
		if b.who.AccountID == "" {
			return "unassigned " + countCards(n)
		}
		return "assigned " + countCards(n) + " to " + personName(b.who)
	case bulkMoveTo:
		return "moved " + countCards(n) + " to " + widget.Sanitize(b.name)
	case bulkLabel:
		return "added the label " + b.label + " to " + countCards(n)
	}
	return ""
}

// --- running ------------------------------------------------------------------

func (m *Model) runBulk() tea.Cmd {
	b := m.bulk
	if m.deps.Jira == nil {
		m.bulk = nil
		m.forget()
		return kernel.Warn("there is no Jira connection in this session")
	}
	b.stage, b.next = stageRunning, 0
	m.forget()
	return m.bulkNext()
}

// bulkNext sends the next card, or reports once none are left.
func (m *Model) bulkNext() tea.Cmd {
	b := m.bulk
	for b.next < len(b.keys) && !b.halt {
		key := b.keys[b.next]
		iss := m.byKey(key)
		if iss == nil {
			b.failed = append(b.failed, bulkMiss{key: key, err: errors.New("it is no longer on this board")})
			b.next++
			continue
		}
		if b.kind == bulkMoveTo {
			if at, mapped := m.plan.columnOf(iss.Status.ID); mapped && at == b.col {
				b.already = append(b.already, key)
				b.next++
				continue
			}
		}
		b.stopRun()
		b.runGen++
		ctx, cancel := context.WithCancel(context.Background())
		b.runStop = cancel
		step := bulkJob{kind: b.kind, who: b.who.AccountID, col: b.col, label: b.label, plan: m.plan, issue: *iss}
		return kernel.Reply(withCancel(cancel, step.run(ctx, m.deps.Jira, b.runGen)), m.addr)
	}
	return m.endBulk()
}

// bulkJob is what one card's step needs, copied off the model so that the
// command running it reads nothing the update loop writes.
type bulkJob struct {
	kind  bulkKind
	who   string
	col   int
	label string
	plan  plan
	issue jira.Issue
}

func (j bulkJob) run(ctx context.Context, client jira.SessionClient, gen int) tea.Cmd {
	key := j.issue.Key
	return func() tea.Msg {
		out := bulkStepMsg{gen: gen, key: key}
		switch j.kind {
		case bulkAssign:
			who := j.who
			out.err = app.SaveIssue(ctx, client, key, app.BaseOf(j.issue, "assignee"), jira.IssuePatch{Assignee: &who})
		case bulkLabel:
			out.err = app.SaveIssue(ctx, client, key, app.EditBase{}, jira.IssuePatch{AddLabels: []string{j.label}})
		case bulkMoveTo:
			out.status, out.moved, out.err = j.transition(ctx, client)
		}
		return out
	}
}

func (j bulkJob) transition(ctx context.Context, mover jira.Mover) (jira.Status, bool, error) {
	list, err := mover.Transitions(ctx, j.issue.Key)
	if err != nil {
		return jira.Status{}, false, err
	}
	for _, tr := range list {
		if at, mapped := j.plan.columnOf(tr.To.ID); !mapped || at != j.col {
			continue
		}
		if needsScreen(tr) {
			return jira.Status{}, false, errScreen
		}
		if err := mover.Transition(ctx, j.issue.Key, tr.ID, jira.IssuePatch{}); err != nil {
			return jira.Status{}, false, err
		}
		return tr.To, true, nil
	}
	return jira.Status{}, false, errNoMove
}

func (m *Model) bulkStepped(msg bulkStepMsg) tea.Cmd {
	b := m.bulk
	if b == nil || b.stage != stageRunning || msg.gen != b.runGen {
		return nil
	}
	b.runStop = nil
	b.next++
	if msg.err != nil {
		b.failed = append(b.failed, bulkMiss{key: msg.key, err: msg.err})
		m.forget()
		return m.bulkNext()
	}
	b.changed = append(b.changed, msg.key)
	var put tea.Cmd
	if iss := m.byKey(msg.key); iss != nil {
		switch b.kind {
		case bulkAssign:
			if b.who.AccountID == "" {
				iss.Assignee = nil
			} else {
				who := b.who
				iss.Assignee = &who
			}
		case bulkLabel:
			if !slices.Contains(iss.Labels, b.label) {
				iss.Labels = append(slices.Clone(iss.Labels), b.label)
			}
		case bulkMoveTo:
			if msg.moved {
				iss.Status = msg.status
			}
		}
		put = stored(m.pagePut([]jira.Issue{*iss}, false))
		under := m.selectedKey()
		m.place()
		m.forget()
		m.restore(under)
	}
	return tea.Batch(put, m.bulkNext())
}

// endBulk reports what changed. The cards that did not stay picked, so the same
// gesture tries them again; the ones that did are let go.
func (m *Model) endBulk() tea.Cmd {
	b := m.bulk
	m.bulk = nil
	b.stopRun()
	for _, key := range b.changed {
		delete(m.picked, key)
	}
	for _, key := range b.already {
		delete(m.picked, key)
	}
	if len(b.failed) > 0 || b.halt {
		if m.picked == nil {
			m.picked = make(map[string]bool, len(b.failed))
		}
		for _, miss := range b.failed {
			m.picked[miss.key] = true
		}
		for _, key := range b.keys[b.next:] {
			m.picked[key] = true
		}
	}
	m.dataGen++
	m.forget()
	said := m.bulkReport(b)
	if b.leave {
		return tea.Sequence(said, kernel.Proceed())
	}
	return said
}

func (m *Model) bulkReport(b *bulk) tea.Cmd {
	total := len(b.keys)
	var parts []string
	switch {
	case len(b.changed) == total-len(b.already) && len(b.changed) > 0:
		parts = append(parts, b.done(len(b.changed)))
	case len(b.changed) > 0:
		parts = append(parts, b.done(len(b.changed))+" of the "+strconv.Itoa(total))
	}
	if len(b.already) > 0 {
		parts = append(parts, countCards(len(b.already))+" already in "+widget.Sanitize(b.name))
	}
	if len(b.failed) > 0 {
		keys := make([]string, 0, len(b.failed))
		for _, miss := range b.failed {
			keys = append(keys, miss.key)
		}
		reason, _ := jira.Reason(b.failed[0].err)
		parts = append(parts, strings.Join(keys, ", ")+" did not change: "+reason)
	}
	if left := total - b.next; b.halt && left > 0 {
		parts = append(parts, "stopped with "+countCards(left)+" not sent")
	}
	text := strings.Join(parts, "; ")
	if text == "" {
		text = "nothing changed"
	}
	if len(b.failed) > 0 {
		return func() tea.Msg { return kernel.StatusMsg{Text: text, Level: kernel.LevelError} }
	}
	if b.halt {
		return kernel.Warn(text)
	}
	return kernel.Status(text)
}

// BlocksClose refuses to throw away a bulk change part way through: the cards
// already sent have changed and the rest have not.
func (m *Model) BlocksClose() (string, bool) {
	if m.bulk == nil || m.bulk.stage != stageRunning {
		return "", false
	}
	return strconv.Itoa(m.bulk.next) + " of " + countCards(len(m.bulk.keys)) +
		" have been sent and the rest are still going", true
}

// AskClose stops the run after the card in flight and leaves once it has
// answered, with the report of what changed, by sending kernel.Proceed.
func (m *Model) AskClose() tea.Cmd {
	if m.bulk == nil || m.bulk.stage != stageRunning {
		return kernel.Proceed()
	}
	m.bulk.halt, m.bulk.leave = true, true
	m.forget()
	return kernel.Status("leaving once the card in flight has answered")
}

// bulkPrompt is the line a bulk change takes under the grid.
func (m *Model) bulkPrompt() string {
	b := m.bulk
	keys := defaultKeys()
	ell := m.deps.Theme.Glyphs.Ellipsis
	n := len(b.keys)
	switch b.stage {
	case stageAskPerson:
		left := "  assign " + countCards(n) + " to " + b.input.View()
		right := ""
		switch {
		case b.askFail != "":
			right = b.askFail
		case b.searching:
			right = "looking" + ell
		case len(b.found) == 0:
			right = "nobody matches"
		default:
			right = "→ " + personName(b.found[b.at]) + " (" + strconv.Itoa(b.at+1) + " of " + strconv.Itoa(len(b.found)) + ")"
		}
		return m.twoCells(left, right+"  ", m.styles.aimed, m.styles.muted)
	case stageAskLabel:
		return padCells(m.styles.aimed.Render("  add a label to "+countCards(n)+": ")+b.input.View(), m.width, ell)
	case stageConfirm:
		hint := keys.Run.Help().Key + " go ahead · " + keys.Decline.Help().Key + " leave them as they are  "
		return m.twoCells("  "+b.what(n)+"?", hint, m.styles.aimed, m.styles.muted)
	case stageRunning:
		said := "  " + strconv.Itoa(b.next+1) + " of " + strconv.Itoa(n) + ": " + b.what(n) + ell
		hint := keys.Halt.Help().Key + " stops after this one  "
		if b.halt {
			hint = "stopping after this one  "
		}
		return m.twoCells(said, hint, m.styles.aimed, m.styles.muted)
	}
	return strings.Repeat(" ", m.width)
}
