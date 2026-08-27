package release

import (
	"strings"

	"github.com/charmbracelet/x/ansi"

	"github.com/varijkapil13/saral/internal/ui/kernel"
	"github.com/varijkapil13/saral/pkg/jira"
)

const (
	// flowHead is the title line and the rule under it.
	flowHead = 2
	// flowLead is the sentence above the rows and the blank line under it.
	flowLead = 2
	// flowNoteWidth keeps the note beside the label rather than at the far edge
	// of a wide terminal.
	flowNoteWidth = 34
	// flowMinLabel is how far the label gives way to a refusal. A refusal is a
	// sentence and needs the room, but a row whose label has been cut to nothing
	// no longer says what is being refused.
	flowMinLabel = 30
	// flowMemoLimit is a screenful of rows in both forms, a few widths deep. A
	// limit under a screenful is worse than none: the map is cleared on every
	// frame and the memo never hits.
	flowMemoLimit = 256
)

// zoneConfirm is the click target on the line that answers the confirm. It is
// its own name rather than a row, because it is the one element on the screen
// and there is nothing to select first.
const zoneConfirm = "confirm"

// flowRowKey is what makes two renderings of a flow row the same rendering.
type flowRowKey struct {
	label    string
	note     string
	refusal  string
	width    int
	selected bool
	gen      int
}

// zoneOf is the click target one row is marked with. A choice is named by the
// policy it stands for and a target by the version's id, both stable for the
// life of the screen.
func (f *Flow) zoneOf(at int) string {
	switch f.state {
	case flowChoosing:
		if at < 0 || at >= len(f.choices) {
			return ""
		}
		return f.choices[at].zone
	case flowPicking:
		if at < 0 || at >= len(f.targets) {
			return ""
		}
		return f.targetRows[at].zone
	case flowConfirming, flowWorking, flowStuck:
	}
	return ""
}

func (f *Flow) rowsHeight() int {
	return max(f.height-flowHead-flowLead, 1)
}

func (f *Flow) row(at int) string {
	var k flowRowKey
	switch f.state {
	case flowChoosing:
		row := f.choices[at]
		k = flowRowKey{label: row.label, note: row.note, refusal: row.refusal}
	case flowPicking:
		row := f.targetRows[at]
		k = flowRowKey{label: row.label, note: row.note}
	case flowConfirming, flowWorking, flowStuck:
		return ""
	}
	k.width, k.selected, k.gen = f.width, at == f.cursor, f.styles.gen
	if s, ok := f.rows.get(k); ok {
		return s
	}
	s := f.zones.Mark(f.zoneOf(at), renderFlowRow(k, f.styles, f.deps.Theme))
	f.rows.put(k, s)
	return s
}

// buildTargets draws the versions the open issues could move to out once, for
// the same reason the choices are: a frame asks for a row at a time and a date
// rendered per frame is an allocation per row per frame.
//
// A version with no release date is not a worse target than one with a date, so
// the note says which it is rather than leaving the row bare.
func (f *Flow) buildTargets() []choice {
	out := make([]choice, 0, len(f.targets))
	for _, v := range f.targets {
		note := "no release date"
		if !v.ReleaseDate.IsZero() {
			note = "releases " + v.ReleaseDate.String()
		}
		out = append(out, choice{label: v.Name, note: note, zone: "target:" + v.ID})
	}
	return out
}

func renderFlowRow(k flowRowKey, st *styles, t *kernel.Theme) string {
	ell := t.Glyphs.Ellipsis
	var b strings.Builder
	b.Grow(k.width + 32)
	if k.selected {
		b.WriteString(padTruncate(t.Glyphs.Collapsed, marker, ell))
	} else {
		b.WriteString(strings.Repeat(" ", marker))
	}
	note, room := k.note, flowNoteWidth
	width := max(k.width-marker-gap-flowNoteWidth, minName)
	if k.refusal != "" {
		note = k.refusal
		width = max(min(width, flowMinLabel), minName)
		room = max(k.width-marker-gap-width, 8)
	}
	label := padTruncate(k.label, width, ell)
	switch {
	case k.selected:
		b.WriteString(label)
	case k.refusal != "":
		b.WriteString(st.muted.Render(label))
	default:
		b.WriteString(st.name.Render(label))
	}
	b.WriteString(strings.Repeat(" ", gap))
	cell := padTruncate(note, room, ell)
	if k.selected {
		b.WriteString(cell)
	} else {
		b.WriteString(st.muted.Render(cell))
	}
	line := padTruncate(b.String(), k.width, ell)
	if k.selected {
		return st.selected.Render(line)
	}
	return line
}

// chromeKey is everything the three lines above the rows are built from, so that
// they are rebuilt when one of them moves and never once per frame.
type chromeKey struct {
	state      flowState
	width, gen int
	open       int
	policy     jira.UnresolvedPolicy
	target     string
}

func (f *Flow) chromeKey() chromeKey {
	return chromeKey{
		state: f.state, width: f.width, gen: f.styles.gen,
		open: f.open, policy: f.policy, target: f.target.ID,
	}
}

// chrome is the title, the rule and the lead, built together because they are
// built from the same handful of facts.
func (f *Flow) chrome() (title, rule, lead string) {
	key := f.chromeKey()
	if f.head[0] != "" && key == f.headAt {
		return f.head[0], f.head[1], f.head[2]
	}
	f.head = [3]string{f.title(), f.rule(), f.lead()}
	f.headAt = key
	return f.head[0], f.head[1], f.head[2]
}

// title names the version and the screen it is on, so that a reader who came
// back to the terminal knows what the y they are about to press does.
func (f *Flow) title() string {
	switch f.state {
	case flowPicking:
		return f.styles.accent.Render(f.fit1("  Move the open issues on " + f.version.Name + " to"))
	case flowConfirming:
		return f.styles.accent.Render(f.fit1("  Release " + f.version.Name + "?"))
	case flowWorking:
		return f.styles.accent.Render(f.fit1("  Releasing " + f.version.Name +
			f.deps.Theme.Glyphs.Ellipsis))
	case flowStuck:
		return f.styles.danger.Render(f.fit1("  " + f.version.Name + " was not released"))
	case flowChoosing:
	}
	return f.styles.accent.Render(f.fit1("  Release " + f.version.Name))
}

func (f *Flow) rule() string {
	return f.styles.muted.Render(strings.Repeat(f.deps.Theme.Glyphs.HLine, max(f.width, 1)))
}

// lead is the sentence above the rows: what is open, which is the fact the whole
// screen exists for.
func (f *Flow) lead() string {
	if f.state == flowPicking {
		return f.styles.muted.Render(f.fit1("  " +
			plural(f.open, "open issue", "open issues") + " will move."))
	}
	return f.styles.warning.Render(f.fit1("  " +
		plural(f.open, "issue is", "issues are") + " still open on " + f.version.Name + "."))
}

// appendConfirm is the screen the write is behind. It says the count, what will
// happen to those issues and what will happen to the version, in that order,
// because the count is the thing a reader is being asked about.
func (f *Flow) appendConfirm(lines []string) []string {
	said := []string{}
	switch {
	case f.open == 0:
		said = append(said, "  Nothing is open on "+f.version.Name+".")
	case f.policy == jira.MoveUnresolved:
		said = append(said, "  "+plural(f.open, "open issue", "open issues")+
			" will move from "+f.version.Name+" to "+f.target.Name+".")
	case f.policy == jira.StripUnresolved:
		said = append(said, "  "+f.version.Name+" will come off "+
			plural(f.open, "open issue", "open issues")+".")
	default:
		said = append(said, "  "+plural(f.open, "open issue", "open issues")+
			" will stay on "+f.version.Name+".")
	}
	said = append(said,
		"  "+f.version.Name+" will be marked released, dated today.",
		"  Releasing a version cannot be undone from here.",
	)
	for _, line := range said {
		lines = append(lines, f.styles.name.Render(f.fit1(line)))
	}
	answer := f.zones.Mark(zoneConfirm, f.styles.danger.Render(f.fit1("  "+confirmHint)))
	return append(lines, "", answer)
}

func (f *Flow) appendWorking(lines []string) []string {
	return append(lines,
		f.styles.muted.Render(f.fit1("  The site is being asked to release "+f.version.Name+".")),
		f.styles.muted.Render(f.fit1("  Nothing else here answers until it does.")),
	)
}

// appendStuck keeps the site's own words on the screen. A release that failed
// part way through says how far it got, and that sentence is the error's, so it
// is wrapped rather than cut.
func (f *Flow) appendStuck(lines []string) []string {
	reason, _ := jira.Reason(f.failure)
	room := max(f.width-marker, 8)
	said := strings.Split(ansi.Wrap(reason, room, ""), "\n")
	for _, line := range said[:min(len(said), reasonLines)] {
		lines = append(lines, f.styles.muted.Render(f.fit1("  "+line)))
	}
	return append(lines, "",
		f.styles.muted.Render(f.fit1("  "+againHint)))
}

// The two sentences that name a key, spelt from the bindings rather than written
// out, so the screen cannot teach a stroke it does not answer.
var (
	confirmHint = defaultFlowKeys().Confirm.Help().Key + " releases it. " +
		kernel.DefaultGlobalKeys().Back.Help().Key + " leaves it alone."
	againHint = defaultFlowKeys().Again.Help().Key + " goes back to the choice. " +
		kernel.DefaultGlobalKeys().Back.Help().Key + " leaves it alone."
)

func (f *Flow) fit1(s string) string {
	return ansi.Truncate(s, max(f.width, 1), f.deps.Theme.Glyphs.Ellipsis)
}

// View draws the screen the flow is on. Only the visible rows are built, which
// costs nothing here and is what keeps the shape the same as every other list in
// this program.
func (f *Flow) View() string {
	if f.width <= 0 || f.height <= 0 {
		return ""
	}
	title, rule, lead := f.chrome()
	lines := f.lines[:0]
	lines = append(lines, title, rule)
	switch f.state {
	case flowChoosing, flowPicking:
		lines = append(lines, lead, "")
		h, n := f.rowsHeight(), f.rowCount()
		end := min(f.top+h, n)
		for i := f.top; i < end; i++ {
			lines = append(lines, f.row(i))
		}
		for i := end - f.top; i < h; i++ {
			lines = append(lines, "")
		}
	case flowConfirming:
		lines = f.appendConfirm(lines)
	case flowWorking:
		lines = f.appendWorking(lines)
	case flowStuck:
		lines = f.appendStuck(lines)
	}
	for len(lines) < f.height {
		lines = append(lines, "")
	}
	lines = lines[:f.height]
	f.lines = lines
	return strings.Join(lines, "\n")
}
