package board

import (
	"cmp"
	"slices"
	"strconv"
	"strings"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/varijkapil13/saral/internal/ui/kernel"
	"github.com/varijkapil13/saral/internal/ui/widget"
	"github.com/varijkapil13/saral/pkg/jira"
)

// laneMode is what the board's rows are grouped by. The parent is the issue's
// own parent field, which is where a company-managed epic and a team-managed
// parent both arrive: no epic-link custom field is read.
type laneMode uint8

const (
	lanesOff laneMode = iota
	lanesByAssignee
	lanesByParent
	laneModes
)

var laneWords = [...]string{lanesOff: "none", lanesByAssignee: "assignee", lanesByParent: "parent"}

func parseLaneMode(s string) laneMode {
	for i, w := range laneWords {
		if w == s {
			return laneMode(i)
		}
	}
	return lanesOff
}

const zoneLane = "lane:"

// laneZone names a lane's header by what the lane is, so a lane that moves down
// the screen keeps the id it was first drawn with.
func laneZone(mode laneMode, key string) string {
	return zoneLane + laneWords[mode] + ":" + key
}

// lane is one swimlane: the cards that share an assignee or a parent, laid out
// per column. at and n are, per column, where the lane's cards start in that
// column's slice of cols and how many there are.
type lane struct {
	key    string
	label  string
	order  string
	cards  int
	at, n  []int
	height int
	// start is the line of the lane's header within the whole run of lanes.
	start  int
	folded bool

	head    string
	headW   int
	headGen int
}

func (l *lane) lines() int {
	if l.folded {
		return 1
	}
	return 1 + l.height
}

func laneMemoryKey(boardID int64) string { return "lanes:" + strconv.FormatInt(boardID, 10) }

func (m *Model) lanesOn() bool { return m.laneMode != lanesOff && m.ready }

// recallLanes loads the grouping this board was last drawn with, once per
// board.
func (m *Model) recallLanes() {
	if m.laneFor == m.plan.boardID && m.laneKnown {
		return
	}
	m.laneFor, m.laneKnown = m.plan.boardID, true
	m.laneMode, m.foldedLanes, m.laneTop = lanesOff, nil, 0
	if enc, ok := kernel.Recall(m.deps, ViewID, laneMemoryKey(m.plan.boardID)); ok {
		m.laneMode = parseLaneMode(enc)
	}
}

// laneOfIssue is the lane an issue belongs to under the mode in force.
func (m *Model) laneOfIssue(iss *jira.Issue) (key, label string) {
	switch m.laneMode {
	case lanesByAssignee:
		if iss.Assignee == nil || iss.Assignee.AccountID == "" {
			return "", "Unassigned"
		}
		name := widget.Sanitize(iss.Assignee.DisplayName)
		if strings.TrimSpace(name) == "" {
			name = iss.Assignee.AccountID
		}
		return iss.Assignee.AccountID, name
	case lanesByParent:
		if iss.Parent == nil || iss.Parent.Key == "" {
			return "", "No parent"
		}
		label := widget.Sanitize(iss.Parent.Key)
		if summary := strings.TrimSpace(widget.Sanitize(iss.Parent.Summary)); summary != "" {
			label += " " + summary
		}
		return iss.Parent.Key, label
	case lanesOff, laneModes:
	}
	return "", ""
}

// beginLanes readies the per-issue lane index for a place that groups.
func (m *Model) beginLanes() map[string]int {
	if cap(m.laneOf) < len(m.issues) {
		m.laneOf = make([]int, len(m.issues))
	}
	m.laneOf = m.laneOf[:len(m.issues)]
	m.lanes = m.lanes[:0]
	return make(map[string]int, 16)
}

// joinLane puts issue i in its lane, opening the lane the first time one of its
// cards is seen.
func (m *Model) joinLane(seen map[string]int, i int) {
	key, label := m.laneOfIssue(&m.issues[i])
	at, ok := seen[key]
	if !ok {
		at = len(m.lanes)
		seen[key] = at
		order := strings.ToLower(label)
		if key == "" {
			order = ""
		}
		m.lanes = append(m.lanes, lane{key: key, label: label, order: order, folded: m.foldedLanes[key]})
	}
	m.lanes[at].cards++
	m.laneOf[i] = at
}

// layLanes orders the lanes, sorts each column by lane with the board's own
// order kept inside a lane, takes the cards of folded lanes out of the columns
// the cursor walks, and records where each lane sits in each column.
//
// People are ordered by name and parents by the first card of theirs the board
// ranks, and the lane of nobody's cards comes last either way.
func (m *Model) layLanes() {
	perm := make([]int, len(m.lanes))
	for i := range perm {
		perm[i] = i
	}
	byName := m.laneMode == lanesByAssignee
	slices.SortStableFunc(perm, func(a, b int) int {
		la, lb := &m.lanes[a], &m.lanes[b]
		if (la.key == "") != (lb.key == "") {
			if la.key == "" {
				return 1
			}
			return -1
		}
		if byName {
			return cmp.Compare(la.order, lb.order)
		}
		return 0
	})
	rank := make([]int, len(m.lanes))
	sorted := make([]lane, len(m.lanes))
	for to, from := range perm {
		rank[from] = to
		sorted[to] = m.lanes[from]
	}
	m.lanes = sorted
	cols := len(m.cols)
	for L := range m.lanes {
		m.lanes[L].at, m.lanes[L].n = make([]int, cols), make([]int, cols)
	}
	if len(m.folded) != cols {
		m.folded = make([][]int, cols)
	}
	for c := range m.cols {
		col := m.cols[c]
		for _, i := range col {
			m.laneOf[i] = rank[m.laneOf[i]]
		}
		slices.SortStableFunc(col, func(a, b int) int { return m.laneOf[a] - m.laneOf[b] })
		kept, folded := col[:0], m.folded[c][:0]
		for _, i := range col {
			if m.lanes[m.laneOf[i]].folded {
				folded = append(folded, i)
				continue
			}
			l := &m.lanes[m.laneOf[i]]
			if l.n[c] == 0 {
				l.at[c] = len(kept)
			}
			l.n[c]++
			kept = append(kept, i)
		}
		m.cols[c], m.folded[c] = kept, folded
	}
	line := 0
	for L := range m.lanes {
		l := &m.lanes[L]
		for c := range l.n {
			l.height = max(l.height, l.n[c])
		}
		l.start = line
		line += l.lines()
	}
	m.laneLines = line
}

// dropLanes is place's other half for a board drawn without lanes.
func (m *Model) dropLanes() {
	m.lanes, m.laneLines = m.lanes[:0], 0
	for c := range m.folded {
		m.folded[c] = m.folded[c][:0]
	}
}

func (m *Model) foldedIn(col int) int {
	if col < 0 || col >= len(m.folded) {
		return 0
	}
	return len(m.folded[col])
}

// laneAt is the lane the card at (col, row) is in, or -1.
func (m *Model) laneAt(col, row int) int {
	if !m.lanesOn() || col < 0 || col >= len(m.cols) || row < 0 || row >= len(m.cols[col]) {
		return -1
	}
	return m.laneOf[m.cols[col][row]]
}

// laneSpan is the stretch of a column the card at row shares a lane with: the
// whole column when there are no lanes. A rank never leaves it.
func (m *Model) laneSpan(col, row int) (lo, hi int) {
	L := m.laneAt(col, row)
	if L < 0 {
		return 0, m.columnLen(col)
	}
	l := &m.lanes[L]
	return l.at[col], l.at[col] + l.n[col]
}

// cursorLine is the line the cursor's card is drawn on within the run of
// lanes, and whether it is drawn at all.
func (m *Model) cursorLine() (line int, first, ok bool) {
	L := m.laneAt(m.curCol, m.curRow)
	if L < 0 {
		return 0, false, false
	}
	l := &m.lanes[L]
	r := m.curRow - l.at[m.curCol]
	return l.start + 1 + r, r == 0, true
}

// followLanes scrolls the run of lanes as little as keeps the cursor's card on
// screen, bringing its lane's header along when the card is the lane's first.
func (m *Model) followLanes() {
	h := m.rowsHeight()
	if line, first, ok := m.cursorLine(); ok {
		top := line
		if first {
			top--
		}
		switch {
		case top < m.laneTop:
			m.laneTop = top
		case line >= m.laneTop+h:
			m.laneTop = line - h + 1
		}
	}
	m.clampLanes()
}

func (m *Model) clampLanes() {
	m.laneTop = min(max(m.laneTop, 0), max(m.laneLines-m.rowsHeight(), 0))
}

// laneFrom is the lane whose lines include line.
func (m *Model) laneFrom(line int) int {
	at, _ := slices.BinarySearchFunc(m.lanes, line, func(l lane, line int) int {
		switch {
		case l.start+l.lines() <= line:
			return -1
		case l.start > line:
			return 1
		}
		return 0
	})
	return at
}

// laneGrid is the window of lines a board with lanes draws: each lane's header
// and, unless it is folded, its rows.
func (m *Model) laneGrid(lines []string, h int) []string {
	L := m.laneFrom(m.laneTop)
	for row := range h {
		line := m.laneTop + row
		for L < len(m.lanes) && m.lanes[L].start+m.lanes[L].lines() <= line {
			L++
		}
		if L >= len(m.lanes) {
			lines = append(lines, m.blankRow)
			continue
		}
		l := &m.lanes[L]
		if line == l.start {
			lines = append(lines, m.laneHead(l))
			continue
		}
		lines = append(lines, m.composeLaneRow(l, line-l.start-1))
	}
	return lines
}

func (m *Model) composeLaneRow(l *lane, r int) string {
	end := min(m.colTop+m.lay.cols, len(m.cols))
	cells := m.rowCells[:0]
	for c := m.colTop; c < end; c++ {
		if r < l.n[c] {
			cells = append(cells, m.cell(c, l.at[c]+r))
			continue
		}
		cells = append(cells, m.blank)
	}
	m.rowCells = cells
	return m.joinCells(cells)
}

// laneHead is a lane's header: whether it is folded, what it is and how many
// cards are in it. It is one zone the whole width of the board, which is what a
// click folds the lane with.
func (m *Model) laneHead(l *lane) string {
	if l.head != "" && l.headW == m.width && l.headGen == m.styles.gen {
		return l.head
	}
	g := m.deps.Theme.Glyphs
	mark := g.Expanded
	if l.folded {
		mark = g.Collapsed
	}
	count := strconv.Itoa(l.cards)
	room := max(m.width-ansi.StringWidth(mark)-ansi.StringWidth(count)-4, 1)
	name := ansi.Truncate(l.label, room, g.Ellipsis)
	line := m.styles.muted.Render(mark) + " " + m.styles.title.Render(name) + "  " + m.styles.muted.Render(count)
	l.head = m.zones.Mark(laneZone(m.laneMode, l.key), padCells(line, m.width, g.Ellipsis))
	l.headW, l.headGen = m.width, m.styles.gen
	return l.head
}

// cycleLanes steps the grouping to the next mode and remembers it for this
// board.
func (m *Model) cycleLanes() tea.Cmd {
	if !m.drawable() {
		return kernel.Warn("there is no board on screen to draw in lanes")
	}
	under := m.selectedKey()
	m.laneMode = (m.laneMode + 1) % laneModes
	m.foldedLanes, m.laneTop = nil, 0
	kernel.Keep(m.deps, ViewID, laneMemoryKey(m.plan.boardID), laneWords[m.laneMode])
	m.place()
	m.forget()
	m.restore(under)
	if m.laneMode == lanesOff {
		return kernel.Status("swimlanes off")
	}
	return kernel.Status("swimlanes by " + laneWords[m.laneMode])
}

// foldLane folds or unfolds the lane of the card under the cursor.
func (m *Model) foldLane() tea.Cmd {
	if !m.lanesOn() {
		return kernel.Warn("the board is not drawn in lanes; " + defaultKeys().Lanes.Help().Key + " draws it in lanes")
	}
	L := m.laneAt(m.curCol, m.curRow)
	if L < 0 {
		return m.foldAll()
	}
	return m.setFold(m.lanes[L].key, !m.lanes[L].folded)
}

// foldAll folds every lane while any is open, and opens every lane otherwise.
func (m *Model) foldAll() tea.Cmd {
	if !m.lanesOn() {
		return kernel.Warn("the board is not drawn in lanes; " + defaultKeys().Lanes.Help().Key + " draws it in lanes")
	}
	open := false
	for i := range m.lanes {
		open = open || !m.lanes[i].folded
	}
	under := m.selectedKey()
	m.foldedLanes = make(map[string]bool, len(m.lanes))
	for i := range m.lanes {
		m.foldedLanes[m.lanes[i].key] = open
	}
	m.place()
	m.forget()
	m.restore(under)
	return nil
}

func (m *Model) setFold(key string, folded bool) tea.Cmd {
	if m.foldedLanes == nil {
		m.foldedLanes = make(map[string]bool, len(m.lanes))
	}
	under := m.selectedKey()
	m.foldedLanes[key] = folded
	m.place()
	m.forget()
	m.restore(under)
	return nil
}

// clickLane folds or unfolds the lane whose header was clicked.
func (m *Model) clickLane(msg tea.MouseMsg) (tea.Cmd, bool) {
	if !m.lanesOn() {
		return nil, false
	}
	h := m.rowsHeight()
	for L := m.laneFrom(m.laneTop); L < len(m.lanes) && m.lanes[L].start < m.laneTop+h; L++ {
		if m.zones.Hit(laneZone(m.laneMode, m.lanes[L].key), msg) {
			return m.setFold(m.lanes[L].key, !m.lanes[L].folded), true
		}
	}
	return nil, false
}

// eachDrawn calls fn for every card position on screen, and stops when it says
// so.
func (m *Model) eachDrawn(fn func(col, row int) bool) {
	h := m.rowsHeight()
	last := min(m.colTop+m.lay.cols, len(m.cols))
	if !m.lanesOn() {
		for c := m.colTop; c < last; c++ {
			top := m.rowTopAt(c)
			for r := top; r < min(top+h, m.columnLen(c)); r++ {
				if fn(c, r) {
					return
				}
			}
		}
		return
	}
	for L := m.laneFrom(m.laneTop); L < len(m.lanes); L++ {
		l := &m.lanes[L]
		if l.start >= m.laneTop+h {
			return
		}
		if l.folded {
			continue
		}
		for r := range l.height {
			line := l.start + 1 + r
			if line < m.laneTop || line >= m.laneTop+h {
				continue
			}
			for c := m.colTop; c < last; c++ {
				if r < l.n[c] && fn(c, l.at[c]+r) {
					return
				}
			}
		}
	}
}

func (m *Model) laneTitle() string {
	if !m.lanesOn() {
		return ""
	}
	return "lanes by " + laneWords[m.laneMode]
}
