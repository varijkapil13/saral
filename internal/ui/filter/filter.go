// Package filter is the filter picker: choose a facet, then choose one of the
// values this site actually holds for it. It exists so that narrowing a search
// by a person, a status, a type, a priority or a label costs a keystroke rather
// than a JQL query typed from memory.
//
// It never resolves anything by display name. A value is carried as the id the
// site gave it, because a name is localised, is not unique on one site, and on
// the measured site one account answered to two of them on two endpoints within
// a minute.
package filter

import (
	"context"
	"strings"

	"charm.land/bubbles/v2/textinput"
	tea "charm.land/bubbletea/v2"

	"github.com/varijkapil13/saral/internal/app"
	"github.com/varijkapil13/saral/internal/ui/kernel"
	"github.com/varijkapil13/saral/internal/ui/widget"
	"github.com/varijkapil13/saral/pkg/jira"
)

// ViewID is the name this view's keys are registered under.
const ViewID = "filter"

// peopleLimit is how many accounts one search asks for. It is a ceiling and not
// a page size: the port does not page people, because a person is found by
// typing more rather than by paging.
const peopleLimit = 50

// thinAnswer is how few candidates the held accounts have to leave before a
// keystroke is worth a round trip. Jira's matching is neither substring nor
// fuzzy and is documented nowhere, so a needle can find an account that nothing
// local would have — but asking on every keystroke is slower than typing the
// JQL this picker exists to replace, so the site is asked when what is held
// stops answering rather than when a key is pressed.
const thinAnswer = 8

// maxLabels bounds the walk over a site's labels. The endpoint takes no query
// and ignores one sent anyway, so the only way to narrow them is to walk them,
// and a busy site has more than anybody scrolls.
const maxLabels = 2000

var (
	_ kernel.View        = (*Model)(nil)
	_ kernel.KeyCapturer = (*Model)(nil)
	_ kernel.Addressed   = (*Model)(nil)
)

// state is which of the picker's two screens is up.
type state uint8

const (
	pickFacet state = iota
	pickValue
)

// facetRow is one facet as the picker offers it, with why this site cannot
// answer for it.
type facetRow struct {
	facet  Facet
	reason string
}

// Option configures a picker at construction.
type Option func(*Model)

// WithTerms opens the picker over what is already in force, so that a value
// already chosen is marked and choosing it again takes it off.
func WithTerms(t Terms) Option {
	return func(m *Model) { m.terms = append(Terms(nil), t...) }
}

// WithEditKey names the key the view being filtered shows its search on, so
// that a facet this site refuses can point at the way round it. Empty means the
// caller has no such key and the sentence leaves it out rather than inventing
// one.
func WithEditKey(key string) Option {
	return func(m *Model) { m.editKey = key }
}

// Model is the picker.
type Model struct {
	deps    kernel.Deps
	acts    map[string]action
	inValue map[string]action

	state   state
	terms   Terms
	editKey string

	facets []facetRow
	facet  Facet

	input textinput.Model
	query string

	// all is every value the site has answered with for the facet on screen,
	// and shown is what the typed pattern leaves of it, best first.
	all   []value
	shown []int
	ranks []ranked

	// asked records the needles this site has already been asked for, so that a
	// keystroke never repeats a request, and complete records that it answered
	// with fewer accounts than it was allowed — which means what is held is the
	// whole directory and typing can be answered without it.
	asked    map[string]bool
	complete bool

	cursor, top   int
	width, height int

	loading bool
	failure error
	gen     int
	cancel  context.CancelFunc
	addr    kernel.Addr

	styles *styles
	memo   *rowCache
	lay    layout

	head     string
	headAt   headKey
	needle   string
	needleAt needleKey
	lines    []string

	zones widget.Zoner
}

// New builds the picker. It opens on the facets, which need nothing of the
// site, so its first frame costs no round trip.
func New(d kernel.Deps, opts ...Option) kernel.View {
	m := &Model{deps: d, input: newInput(), addr: kernel.NewAddr()}
	for _, opt := range opts {
		opt(m)
	}
	if m.deps.Theme == nil {
		m.deps.Theme = kernel.NewTheme(kernel.ThemeAuto, true, kernel.UnicodeGlyphs())
	}
	m.acts, m.inValue = defaultKeys().tables()
	m.styles = newStyles(m.deps.Theme)
	m.memo = newRowCache(rowMemoLimit)
	m.zones = widget.NewZoner(d.Zones)
	m.facets = m.buildFacets()
	m.lay = planLayout(m.width)
	return m
}

// buildFacets asks, once, what this session can answer for. A facet whose
// values cannot be read is still offered and still says why, because a facet
// that disappears is one nobody can find out about.
func (m *Model) buildFacets() []facetRow {
	out := make([]facetRow, 0, len(Facets))
	for _, f := range Facets {
		out = append(out, facetRow{facet: f, reason: m.refusal(f)})
	}
	return out
}

// refusal is why this session cannot offer a facet's values, and "" when it
// can. The words are the site's own wherever the site supplied any.
func (m *Model) refusal(f Facet) string {
	if m.deps.Jira == nil {
		return "there is no Jira connection in this session"
	}
	if f.people() && !m.deps.Caps.Allows(jira.CapPeople) {
		if reason := m.deps.Caps.Capability(jira.CapPeople).Reason; reason != "" {
			return reason
		}
		return "this token may not look accounts up on this site"
	}
	if (f == FacetStatus || f == FacetType) && strings.TrimSpace(m.deps.Project) == "" {
		return "statuses and types are per project, and this session is not scoped to one"
	}
	return ""
}

// WantsRawKeys is true while a value is being typed for. Without it the kernel
// matches its own bindings first, so the needle loses every digit, q quits the
// program out from under the typing, and esc never reaches the picker to take
// it back to the facets.
func (m *Model) WantsRawKeys() bool { return m.state == pickValue }

// Init has nothing to fetch: the facets are this build's, not the site's.
func (m *Model) Init() tea.Cmd { return nil }

// Update handles one message.
func (m *Model) Update(msg tea.Msg) (kernel.View, tea.Cmd) {
	var cmd tea.Cmd
	switch msg := msg.(type) {
	case kernel.SizeMsg:
		m.resize(msg.Width, msg.Height)

	case kernel.FocusMsg:
		m.focus(msg.Focused)

	case kernel.ThemeMsg:
		m.deps.Theme = msg.Theme
		m.styles = newStyles(msg.Theme)
		m.memo.reset()
		m.head, m.needle = "", ""

	case kernel.CapabilitiesMsg:
		m.deps.Caps = msg.Caps
		m.facets = m.buildFacets()
		m.memo.reset()

	case vocabularyMsg:
		cmd = m.tookVocabulary(msg)

	case peopleMsg:
		cmd = m.tookPeople(msg)

	case failedMsg:
		cmd = m.failed(msg)

	case tea.KeyPressMsg:
		cmd = m.key(msg)

	case tea.MouseClickMsg:
		cmd = m.click(msg)

	case tea.MouseWheelMsg:
		m.wheel(msg)
	}
	return m, cmd
}

func (m *Model) resize(w, h int) {
	if w == m.width && h == m.height {
		return
	}
	m.width, m.height = w, h
	m.lay = planLayout(w)
	m.input.SetWidth(max(w-inputChrome, 8))
	m.memo.reset()
	m.head, m.needle = "", ""
	m.clampScroll()
}

// focus keeps the needle's cursor out of a pane nobody is looking at. The line
// is drawn with the cursor in it, so the memo of it goes with the focus.
func (m *Model) focus(on bool) {
	m.needle = ""
	if on && m.state == pickValue {
		_ = m.input.Focus()
		return
	}
	m.input.Blur()
}

func newInput() textinput.Model {
	ti := widget.NewInput()
	ti.Prompt = "> "
	return ti
}

func (m *Model) key(msg tea.KeyPressMsg) tea.Cmd {
	if m.state == pickValue {
		return m.valueKey(msg)
	}
	switch m.acts[msg.String()] {
	case actUp:
		m.moveTo(m.cursor - 1)
	case actDown:
		m.moveTo(m.cursor + 1)
	case actPageUp:
		m.moveTo(m.cursor - m.rowsHeight())
	case actPageDown:
		m.moveTo(m.cursor + m.rowsHeight())
	case actTop:
		m.moveTo(0)
	case actBottom:
		m.moveTo(m.rowCount() - 1)
	case actChoose:
		return m.chooseFacet()
	case actNone, actBack:
	}
	return nil
}

// valueKey takes the keys while a needle is being typed. Everything the table
// does not claim is text, which is why the picker claims raw keys here.
func (m *Model) valueKey(msg tea.KeyPressMsg) tea.Cmd {
	switch m.inValue[msg.String()] {
	case actUp:
		m.moveTo(m.cursor - 1)
		return nil
	case actDown:
		m.moveTo(m.cursor + 1)
		return nil
	case actPageUp:
		m.moveTo(m.cursor - m.rowsHeight())
		return nil
	case actPageDown:
		m.moveTo(m.cursor + m.rowsHeight())
		return nil
	case actChoose:
		return m.chooseValue()
	case actBack:
		return m.backToFacets()
	case actNone, actTop, actBottom:
	}
	// The input's own command is a cursor blink, which is a timer this view
	// would then own for as long as it is up. Dropping it costs a blinking block
	// and keeps every frame reproducible.
	m.input, _ = m.input.Update(msg)
	if q := m.input.Value(); q != m.query {
		m.query = q
		return m.retype()
	}
	return nil
}

// chooseFacet opens the second state over one facet, or says why this site
// cannot answer for it.
func (m *Model) chooseFacet() tea.Cmd {
	if m.cursor < 0 || m.cursor >= len(m.facets) {
		return nil
	}
	row := m.facets[m.cursor]
	if row.reason != "" {
		return kernel.Warn(m.refusalSentence(row.reason))
	}
	m.state, m.facet = pickValue, row.facet
	m.all, m.shown, m.ranks = nil, m.shown[:0], m.ranks[:0]
	// Nobody is a value of the assignee facet like any other, and it is this
	// program's own row rather than one the site answers with, so it goes on
	// before anything is asked and survives a search that comes back refused.
	if row.facet == FacetAssignee {
		m.all = append(m.all, unassignedValue())
	}
	m.asked, m.complete = make(map[string]bool, 4), false
	m.query, m.failure = "", nil
	m.cursor, m.top = 0, 0
	m.input.Reset()
	m.input.Placeholder = "which " + row.facet.Label() + "?"
	m.input.SetWidth(max(m.width-inputChrome, 8))
	_ = m.input.Focus()
	m.memo.reset()
	m.head = ""
	// The rows this program supplies itself are on offer before the site has
	// answered anything, and they stay on offer if it refuses.
	m.rerank("")
	return m.fetch("")
}

// refusalSentence is the site's own words plus the way round them, which is the
// prompt that shows the search and takes an edited one.
func (m *Model) refusalSentence(reason string) string {
	if m.editKey == "" {
		return reason
	}
	return reason + "; " + m.editKey + " edits the search by hand"
}

// backToFacets drops the value state, including whatever is in flight for it.
func (m *Model) backToFacets() tea.Cmd {
	m.stop()
	m.state = pickFacet
	m.input.Blur()
	m.query, m.failure = "", nil
	m.all, m.shown = nil, m.shown[:0]
	m.cursor, m.top = m.facetAt(m.facet), 0
	m.facet = FacetNone
	m.memo.reset()
	m.head = ""
	m.clampScroll()
	return nil
}

func (m *Model) facetAt(f Facet) int {
	for i := range m.facets {
		if m.facets[i].facet == f {
			return i
		}
	}
	return 0
}

// chooseValue puts the value in force and closes. A value already in force
// comes off instead, which is the same gesture a second click on a cell makes.
//
// The picker names the value and never applies it: it is pushed over the view
// being filtered and holds no pointer to it, so the term travels as a broadcast
// and the view that owns the search decides what to do with it.
func (m *Model) chooseValue() tea.Cmd {
	v := m.selected()
	if v == nil {
		return nil
	}
	m.stop()
	return tea.Sequence(kernel.Pop(), kernel.Broadcast(ChosenMsg{Term: v.term}))
}

func (m *Model) selected() *value {
	if m.state != pickValue || m.cursor < 0 || m.cursor >= len(m.shown) {
		return nil
	}
	return &m.all[m.shown[m.cursor]]
}

// ChosenMsg carries the value the picker was closed on. It is a broadcast
// because the picker is pushed over whatever view is being filtered and never
// holds a pointer to one.
type ChosenMsg struct{ Term Term }

// --- fetching ---------------------------------------------------------------

// begin cancels whatever is in flight and opens a context for its replacement.
// The generation it returns is what a landing answer is checked against, so a
// reply to a question the user has already changed is dropped rather than drawn.
func (m *Model) begin() (ctx context.Context, gen int) {
	m.stop()
	m.gen++
	ctx, cancel := context.WithCancel(context.Background())
	m.cancel = cancel
	m.loading, m.failure = true, nil
	return ctx, m.gen
}

func (m *Model) stop() {
	if m.cancel != nil {
		m.cancel()
		m.cancel = nil
	}
	m.loading = false
}

// Addr is where the kernel delivers the vocabulary and the accounts this picker
// asked for, whatever has since been pushed over it.
func (m *Model) Addr() kernel.Addr { return m.addr }

// reply puts this picker's address on a command, so what it asked for comes
// back here rather than to whatever the stack has on top by then.
func (m *Model) reply(cmd tea.Cmd) tea.Cmd {
	return kernel.Reply(withCancel(m.cancel, cmd), m.addr)
}

// Close lets go of the vocabulary read and of an account search still out with
// the site. A picker that has been thrown away has nothing to rank.
func (m *Model) Close() { m.stop() }

// fetch asks the site for the facet on screen. needle is only ever non-empty
// for the accounts, which are the one vocabulary this site will not let anybody
// narrow locally.
func (m *Model) fetch(needle string) tea.Cmd {
	if m.deps.Jira == nil {
		return nil
	}
	ctx, gen := m.begin()
	facet := m.facet
	if facet.people() {
		m.asked[needle] = true
		return m.reply(findPeople(ctx, m.deps.Jira, facet, jira.PeopleQuery{
			Match: needle, Project: m.peopleProject(facet), Limit: peopleLimit,
		}, m.inForceIDs(facet), gen))
	}
	return m.reply(vocabulary(ctx, m.deps.Jira, facet, m.deps.Project, gen))
}

// inForceIDs are the accounts this facet is already being filtered by, which
// the search is asked to bring back whether or not it would have found them.
func (m *Model) inForceIDs(f Facet) []string {
	var out []string
	for _, term := range m.terms {
		if term.Facet == f && term.ID != "" {
			out = append(out, term.ID)
		}
	}
	return out
}

// peopleProject decides whether the search is the assignable one. Setting it
// switches Jira to the accounts that can be given work in a project, which
// drops the app accounts for free — ten of the eleven on the measured site.
// A reporter need not be assignable, though: an account that reported an issue
// and then lost the permission is still on those rows, so that search is the
// site-wide one and the app accounts it brings are badged and sunk instead.
func (m *Model) peopleProject(f Facet) string {
	if f == FacetAssignee {
		return strings.TrimSpace(m.deps.Project)
	}
	return ""
}

// withCancel makes a command release its context however it ends. The cancel is
// also held on the model so that the next request can cut this one short.
func withCancel(cancel context.CancelFunc, cmd tea.Cmd) tea.Cmd {
	return func() tea.Msg {
		defer cancel()
		return cmd()
	}
}

func (m *Model) current(gen int, f Facet) bool { return gen == m.gen && f == m.facet }

func (m *Model) tookVocabulary(msg vocabularyMsg) tea.Cmd {
	if !m.current(msg.gen, msg.facet) {
		return nil
	}
	m.loading, m.failure = false, nil
	under := m.underCursor()
	m.all, m.complete = msg.values, true
	m.memo.reset()
	m.rerank(under)
	return nil
}

// tookPeople merges an answer into what is held, keyed by account id. A later
// needle brings accounts an earlier one did not, and dropping the earlier ones
// would make the list flicker between two subsets of the same directory.
func (m *Model) tookPeople(msg peopleMsg) tea.Cmd {
	if !m.current(msg.gen, msg.facet) {
		return nil
	}
	m.loading, m.failure = false, nil
	if msg.needle == "" {
		m.complete = msg.complete
	}
	under := m.underCursor()
	held := make(map[string]bool, len(m.all)+len(msg.people))
	for i := range m.all {
		held[m.all[i].term.ID] = true
	}
	for i := range msg.people {
		if held[msg.people[i].AccountID] {
			continue
		}
		held[msg.people[i].AccountID] = true
		m.all = append(m.all, personValue(msg.facet, msg.people[i]))
	}
	sortPeople(m.all)
	m.memo.reset()
	m.rerank(under)
	return nil
}

// failed keeps the refusal in the pane as well as on the status line: a status
// line is overwritten by the next thing that happens, and a list that is empty
// because the site said no has to keep saying so.
func (m *Model) failed(msg failedMsg) tea.Cmd {
	if !m.current(msg.gen, msg.facet) {
		return nil
	}
	m.loading, m.failure = false, msg.err
	// A needle the site never answered is not one it has been asked, or the
	// same keystrokes would never reach it again.
	delete(m.asked, msg.needle)
	m.head = ""
	m.rerank(m.underCursor())
	return kernel.Fail(msg.err)
}

// retype ranks what is held against the needle, and goes back to the site only
// where what is held cannot answer: the accounts, whose matching this program
// cannot reproduce, and only once the local answer has run thin.
func (m *Model) retype() tea.Cmd {
	m.rerank(m.underCursor())
	needle := strings.TrimSpace(m.query)
	switch {
	case !m.facet.people(), needle == "", m.complete, m.asked[needle]:
		return nil
	case len(m.shown) >= thinAnswer:
		return nil
	}
	return m.fetch(needle)
}

// rerank recomputes the order without asking anything of the site and puts the
// cursor back on under, which is a value id and not a row number.
//
// It cannot read that row itself: shown holds indices into all, so an answer
// that appends to all and sorts it leaves them pointing at other values. Only
// the caller still knows which row the reader was looking at.
func (m *Model) rerank(under string) {
	m.shown, m.ranks = rank(m.all, app.NewPattern(strings.TrimSpace(m.query)), m.shown[:0], m.ranks[:0])
	m.cursor = 0
	if under != "" {
		for i, at := range m.shown {
			if m.all[at].term.ID == under {
				m.cursor = i
				break
			}
		}
	}
	m.scrollToCursor()
}

// underCursor names the value the cursor is on. It has to be read before
// anything appends to or replaces all.
func (m *Model) underCursor() string {
	if v := m.selected(); v != nil {
		return v.term.ID
	}
	return ""
}

// --- selection --------------------------------------------------------------

func (m *Model) rowCount() int {
	if m.state == pickFacet {
		return len(m.facets)
	}
	return len(m.shown)
}

func (m *Model) moveTo(at int) {
	n := m.rowCount()
	if n == 0 {
		m.cursor, m.top = 0, 0
		return
	}
	m.cursor = min(max(at, 0), n-1)
	m.scrollToCursor()
}

func (m *Model) scrollToCursor() {
	h := m.rowsHeight()
	if m.cursor < m.top {
		m.top = m.cursor
	}
	if m.cursor >= m.top+h {
		m.top = m.cursor - h + 1
	}
	m.clampScroll()
}

func (m *Model) clampScroll() {
	m.top = min(max(m.top, 0), max(m.rowCount()-m.rowsHeight(), 0))
}

// rowsHeight is how many rows fit under the head line and its rule, less the
// line a refusal keeps under them.
func (m *Model) rowsHeight() int {
	h := m.height - headHeight
	if m.refused() {
		h--
	}
	return max(h, 1)
}

// refused reports that the site said no and there are still rows to draw, which
// is the one state where the reason cannot be the pane's whole answer.
func (m *Model) refused() bool { return m.failure != nil && m.rowCount() > 0 }

// --- mouse ------------------------------------------------------------------

// click selects the row under the pointer, and a second click on the row
// already selected does what enter does — the gesture the palette and the issue
// list already answer to.
func (m *Model) click(msg tea.MouseClickMsg) tea.Cmd {
	if msg.Button != tea.MouseLeft {
		return nil
	}
	for i := m.top; i < min(m.top+m.rowsHeight(), m.rowCount()); i++ {
		if !m.zones.Hit(m.zoneOf(i), msg) {
			continue
		}
		if i == m.cursor {
			if m.state == pickFacet {
				return m.chooseFacet()
			}
			return m.chooseValue()
		}
		m.moveTo(i)
		return nil
	}
	return nil
}

// wheel scrolls the rows without moving the selection, which is what a wheel
// does everywhere else.
func (m *Model) wheel(msg tea.MouseWheelMsg) {
	switch msg.Button {
	case tea.MouseWheelUp:
		m.top -= 3
	case tea.MouseWheelDown:
		m.top += 3
	default:
		return
	}
	m.clampScroll()
}
