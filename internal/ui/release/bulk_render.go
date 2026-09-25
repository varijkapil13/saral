package release

import (
	"strconv"
	"strings"

	"github.com/charmbracelet/x/ansi"

	"github.com/varijkapil13/saral/internal/ui/widget"
	"github.com/varijkapil13/saral/pkg/jira"
)

const (
	bulkZoneToggle = "bulk:toggle"
	bulkZoneRun    = "bulk:run"
	bulkZoneEdit   = "bulk:edit"
	// bulkKeyWidth holds an issue key and the gap after it.
	bulkKeyWidth = 12
	// bulkChrome is the lines above and below the rows on a step with rows: the
	// title, the rule, the count, the blank before the keys and the keys.
	bulkChrome = 5
)

// bulkItem is one row: an issue that will change, or one the site refused and
// why.
type bulkItem struct {
	key  string
	text string
}

type bulkRowKey struct {
	item     bulkItem
	width    int
	selected bool
	gen      int
}

func (b *Bulk) rowsHeight() int { return max(b.height-bulkChrome, 1) }

func (b *Bulk) row(at int) string {
	k := bulkRowKey{item: b.items[at], width: b.width, selected: at == b.cursor, gen: b.styles.gen}
	if s, ok := b.rows.Get(k); ok {
		return s
	}
	ell := b.deps.Theme.Glyphs.Ellipsis
	line := strings.Repeat(" ", marker) + widget.PadTruncate(k.item.key, bulkKeyWidth, ell) +
		widget.PadTruncate(k.item.text, max(b.width-marker-bulkKeyWidth, 1), ell)
	if k.selected {
		line = b.styles.selected.Render(line)
	} else {
		line = b.styles.muted.Render(line)
	}
	b.rows.Put(k, line)
	return line
}

func (b *Bulk) verb() string {
	if b.remove {
		return "Take " + widget.Sanitize(b.version.Name) + " off issues"
	}
	return "Put " + widget.Sanitize(b.version.Name) + " on issues"
}

// bulkChromeKey is everything the lines around the rows are built from, so
// they are rebuilt when one of them moves and never once per frame.
type bulkChromeKey struct {
	state                                bulkState
	remove                               bool
	width, gen                           int
	todo, skipped, done, failed, pending int
}

// View draws the step that is up. Only the rows that fit are built, so a
// preview of a thousand issues costs what one of twenty costs.
func (b *Bulk) View() string {
	if b.width <= 0 || b.height <= 0 {
		return ""
	}
	chrome := b.chromeLines()
	lines := append(b.lines[:0], chrome[0], chrome[1])
	switch b.state {
	case bulkQuery, bulkReading:
		lines = b.appendQuery(lines, b.fit)
	case bulkPreview, bulkDone:
		lines = append(lines, chrome[2])
		lines = b.appendRows(lines)
		lines = append(lines, "", chrome[3])
	case bulkWorking:
		lines = append(lines, chrome[2])
	case bulkStates:
	}
	for len(lines) < b.height {
		lines = append(lines, "")
	}
	b.lines = lines[:b.height]
	return strings.Join(b.lines, "\n")
}

func (b *Bulk) fit(s string) string {
	return ansi.Truncate(s, max(b.width, 8), b.deps.Theme.Glyphs.Ellipsis)
}

func (b *Bulk) chromeLines() [4]string {
	key := bulkChromeKey{
		state: b.state, remove: b.remove, width: b.width, gen: b.styles.gen,
		todo: len(b.todo), skipped: b.skipped, done: b.done, failed: len(b.failed), pending: len(b.pending),
	}
	if b.chrome[0] != "" && key == b.chromeAt {
		return b.chrome
	}
	out := [4]string{
		b.styles.accent.Render(b.fit("  " + b.verb())),
		b.styles.muted.Render(strings.Repeat(b.deps.Theme.Glyphs.HLine, max(b.width, 1))),
	}
	switch b.state {
	case bulkPreview:
		out[2], out[3] = b.styles.name.Render(b.fit("  "+b.previewWords())), b.fit("  "+b.previewKeys())
	case bulkWorking:
		out[2] = b.styles.name.Render(b.fit("  " + b.workingWords()))
	case bulkDone:
		out[2], out[3] = b.styles.name.Render(b.fit("  "+b.outcome())), b.fit("  "+b.doneKeys())
	case bulkQuery, bulkReading, bulkStates:
	}
	b.chrome, b.chromeAt = out, key
	return out
}

func (b *Bulk) appendQuery(lines []string, fit func(string) string) []string {
	room := max(b.width-marker, 8)
	lines = append(lines,
		b.styles.muted.Render(fit("  Which issues, as JQL:")),
		fit("  "+b.input.View()),
		"",
	)
	if b.state == bulkReading {
		return append(lines, b.styles.muted.Render(fit("  Running the query"+b.deps.Theme.Glyphs.Ellipsis)))
	}
	if b.failure != nil {
		reason, _ := jira.Reason(b.failure)
		lines = append(lines, b.styles.danger.Render(fit("  "+whatQuery)))
		for _, line := range strings.Split(ansi.Wrap(reason, room, ""), "\n")[:1] {
			lines = append(lines, b.styles.muted.Render(fit("  "+line)))
		}
		lines = append(lines, "")
	}
	other := "takes it off instead"
	if b.remove {
		other = "puts it on instead"
	}
	return append(lines, fit("  "+
		b.styles.muted.Render(b.zones.Mark(bulkZoneRun, "enter shows what would change"))+
		b.styles.muted.Render(" · ")+
		b.styles.muted.Render(b.zones.Mark(bulkZoneToggle, "tab "+other))+
		b.styles.muted.Render(" · esc leaves")))
}

func (b *Bulk) appendRows(lines []string) []string {
	h := b.rowsHeight()
	end := min(b.top+h, len(b.items))
	for i := b.top; i < end; i++ {
		lines = append(lines, b.row(i))
	}
	for i := end - b.top; i < h; i++ {
		lines = append(lines, "")
	}
	return lines
}

func (b *Bulk) previewWords() string {
	name := widget.Sanitize(b.version.Name)
	n := len(b.todo)
	matched := n + b.skipped
	var s strings.Builder
	s.WriteString(plural(matched, "issue matches", "issues match"))
	if b.skipped > 0 {
		if b.remove {
			s.WriteString("; " + strconv.Itoa(b.skipped) + " do not carry " + name + " and are left alone")
		} else {
			s.WriteString("; " + strconv.Itoa(b.skipped) + " already carry " + name + " and are left alone")
		}
	}
	switch {
	case n == 0:
		s.WriteString(". Nothing would change.")
	case b.remove:
		s.WriteString(". " + name + " comes off " + plural(n, "issue:", "issues:"))
	default:
		s.WriteString(". " + name + " goes on " + plural(n, "issue:", "issues:"))
	}
	return s.String()
}

func (b *Bulk) previewKeys() string {
	edit := b.styles.muted.Render(b.zones.Mark(bulkZoneEdit, "e changes the query"))
	if len(b.todo) == 0 {
		return edit + b.styles.muted.Render(" · esc leaves")
	}
	what := "y puts it on " + plural(len(b.todo), "issue", "issues")
	if b.remove {
		what = "y takes it off " + plural(len(b.todo), "issue", "issues")
	}
	return b.styles.name.Render(b.zones.Mark(zoneConfirm, what)) + b.styles.muted.Render(" · ") + edit +
		b.styles.muted.Render(" · esc leaves")
}

func (b *Bulk) workingWords() string {
	sent := b.done + len(b.failed)
	return "Sent " + strconv.Itoa(sent) + " of " + plural(len(b.todo), "issue", "issues") +
		b.deps.Theme.Glyphs.Ellipsis
}

func (b *Bulk) doneKeys() string {
	edit := b.styles.muted.Render(b.zones.Mark(bulkZoneEdit, "e runs another query"))
	if len(b.failed) == 0 {
		return edit + b.styles.muted.Render(" · esc leaves")
	}
	return b.styles.muted.Render("the refusals are listed above · ") + edit + b.styles.muted.Render(" · esc leaves")
}
