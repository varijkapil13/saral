package settings

import (
	"strings"

	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/varijkapil13/saral/internal/ui/kernel"
	"github.com/varijkapil13/saral/pkg/jira"
)

const (
	marker     = 2
	titleWidth = 22
	gap        = 2
	symWidth   = 2
	// rowMemoLimit is generous next to how many settings a build can register:
	// past a few dozen this is a build nobody shipped, and the palette's own
	// limit is the same order of magnitude for the same reason.
	rowMemoLimit = 256
)

// shape is which of the five row shapes docs/SETTINGS.md draws a setting as.
type shape int

const (
	shapeRadios shape = iota
	shapePicker
	shapeToggle
	shapeInfo
	shapeAction
)

func shapeOf(s kernel.Setting, d kernel.Deps) shape {
	switch s.Kind {
	case kernel.KindToggle:
		return shapeToggle
	case kernel.KindInfo:
		return shapeInfo
	case kernel.KindAction:
		return shapeAction
	case kernel.KindChoice:
		if customPickers[s.ID] != nil {
			return shapePicker
		}
		if fitsInline(s.Options(d)) {
			return shapeRadios
		}
		return shapePicker
	default:
		return shapeInfo
	}
}

// fitsInline is docs/SETTINGS.md's "roughly ≤4 short labels": four or fewer
// options whose "( ) label" markers, joined with two spaces, clear a row
// without crowding the value column a wider terminal still leaves it.
const inlineBudget = 44

func fitsInline(opts []kernel.SettingOption) bool {
	if len(opts) == 0 || len(opts) > 4 {
		return false
	}
	width := 0
	for i, o := range opts {
		if i > 0 {
			width += 2
		}
		width += 4 + ansi.StringWidth(o.Label)
	}
	return width <= inlineBudget
}

// refusal is why a setting cannot be shown here at all, and "" when it can.
// It is Requires answered in the probe's own words, exactly as
// palette.refusal does it — a separate question from Setting.Unavailable,
// which is drawn rather than hidden.
func refusal(caps jira.Capabilities, needs jira.CapabilityKey, title string) string {
	if needs == "" || caps.Allows(needs) {
		return ""
	}
	if reason := caps.Capability(needs).Reason; reason != "" {
		return reason
	}
	return title + " is not available on this site"
}

type layout struct {
	width int
	value int
}

func planLayout(width int) layout {
	value := width - marker - titleWidth - gap - gap - symWidth
	if value < 10 {
		value = 10
	}
	return layout{width: width, value: value}
}

type styles struct {
	gen                                     int
	title, value, muted, warn, header, note lipgloss.Style
	selected                                lipgloss.Style
}

func newStyles(t *kernel.Theme) *styles {
	return &styles{
		gen:      t.Gen,
		title:    t.Base,
		value:    t.Base,
		muted:    t.Muted,
		warn:     t.Warning,
		header:   t.Title,
		note:     t.Muted,
		selected: t.Selected,
	}
}

// rowKey is what makes two renderings of a setting the same rendering. The
// current value is part of it deliberately: a radio that only keyed off the
// setting's ID would not repaint when the value moved, which is exactly the
// bug the whole screen exists to make impossible.
type rowKey struct {
	id          string
	value       string
	unavailable string
	sel         bool
	gen         int
	width       int
}

type renderedRow struct{ ctrl, detail string }

// rowCache is a bounded memo of rendered rows, the same shape
// palette.rowCache is: past its limit it is emptied rather than evicted one
// at a time, since a resize or a theme change invalidates a screenful anyway.
type rowCache struct {
	rows  map[rowKey]renderedRow
	limit int
}

func newRowCache(limit int) *rowCache {
	return &rowCache{rows: make(map[rowKey]renderedRow, limit), limit: limit}
}

func (c *rowCache) get(k rowKey) (renderedRow, bool) {
	r, ok := c.rows[k]
	return r, ok
}

func (c *rowCache) put(k rowKey, r renderedRow) {
	if len(c.rows) >= c.limit {
		clear(c.rows)
	}
	c.rows[k] = r
}

func (c *rowCache) reset() { clear(c.rows) }

func writeMarker(b *strings.Builder, sel bool, t *kernel.Theme) {
	if !sel {
		b.WriteString(strings.Repeat(" ", marker))
		return
	}
	b.WriteString(t.Glyphs.Collapsed)
	b.WriteString(strings.Repeat(" ", max(marker-ansi.StringWidth(t.Glyphs.Collapsed), 0)))
}

func writeCell(b *strings.Builder, text string, sel bool, style lipgloss.Style) {
	if sel {
		b.WriteString(text)
		return
	}
	b.WriteString(style.Render(text))
}

// renderControl draws a setting's first line: the marker, its title, the
// value in whatever shape its kind calls for, and the trailing symbol a
// picker, an action or an actionable info row ends in.
func renderControl(s kernel.Setting, sp shape, d kernel.Deps, value string, sel bool, lay layout, st *styles) string {
	t := d.Theme
	var b strings.Builder
	b.Grow(lay.width + 16)
	writeMarker(&b, sel, t)

	if sp == shapeAction {
		span := titleWidth + gap + lay.value
		writeCell(&b, padTruncate(s.Title, span, t.Glyphs.Ellipsis), sel, st.title)
	} else {
		writeCell(&b, padTruncate(s.Title, titleWidth, t.Glyphs.Ellipsis), sel, st.title)
		b.WriteString(strings.Repeat(" ", gap))
		writeCell(&b, padTruncate(controlText(s, sp, d, value, t), lay.value, t.Glyphs.Ellipsis), sel, controlStyle(sp, st))
	}

	b.WriteString(strings.Repeat(" ", gap))
	sym := symbolFor(sp, s, t)
	writeCell(&b, padLeft(sym, symWidth, ""), sel, st.muted)

	if sel {
		return st.selected.Render(b.String())
	}
	return b.String()
}

func controlStyle(sp shape, st *styles) lipgloss.Style {
	if sp == shapeInfo {
		return st.muted
	}
	return st.value
}

func controlText(s kernel.Setting, sp shape, d kernel.Deps, value string, t *kernel.Theme) string {
	switch sp {
	case shapeRadios:
		return renderRadios(s.Options(d), value, t)
	case shapeToggle:
		if value == "on" {
			return "[" + t.Glyphs.Check + "] on"
		}
		return "[ ] off"
	case shapePicker:
		return optionLabel(s.Options(d), value)
	default:
		return value
	}
}

// renderRadios draws every option inline, the highlighted one marked with the
// theme's own bullet glyph so a scheme with no colour still shows which value
// is in force.
func renderRadios(opts []kernel.SettingOption, current string, t *kernel.Theme) string {
	parts := make([]string, len(opts))
	for i, o := range opts {
		mark := "( )"
		if o.ID == current {
			mark = "(" + t.Glyphs.Bullet + ")"
		}
		parts[i] = mark + " " + o.Label
	}
	return strings.Join(parts, "  ")
}

// optionLabel is the label the current value's own option offers, and the raw
// value when nothing offers it — a setting whose Options only ever answers
// with the value already in force, such as session.project, always finds one.
func optionLabel(opts []kernel.SettingOption, value string) string {
	for _, o := range opts {
		if o.ID == value {
			return o.Label
		}
	}
	return value
}

func symbolFor(sp shape, s kernel.Setting, t *kernel.Theme) string {
	switch sp {
	case shapePicker:
		return t.Glyphs.Collapsed
	case shapeAction:
		return t.Glyphs.Arrow
	case shapeInfo:
		if s.Run != nil {
			return t.Glyphs.Collapsed
		}
	}
	return ""
}

// renderDetail draws a setting's second line: its summary, replaced with
// Unavailable's own reason when it answers one — drawn, never hidden, because
// the control above it is real and the user is looking straight at it.
func renderDetail(text string, warn, sel bool, width int, ell string, st *styles) string {
	indent := strings.Repeat(" ", marker+titleWidth+gap)
	body := padTruncate(indent+text, width, ell)
	style := st.note
	if warn {
		style = st.warn
	}
	if sel {
		return st.selected.Render(body)
	}
	return style.Render(body)
}

// scopeText says where a scope's value lives, in the same voice
// saveTheme/saveMouse already answer a failed write in.
func scopeText(scope kernel.SettingScope, p profileState) string {
	switch scope {
	case kernel.ScopeProfile:
		if p.name == "" {
			return "this session only; no profile to save to"
		}
		return `saved to profile "` + p.name + `"`
	case kernel.ScopeFile:
		if p.err != nil {
			return "this session only; no config file yet"
		}
		return "saved to config.toml"
	case kernel.ScopeMachine:
		return "saved on this machine"
	default:
		return "this session only"
	}
}

// padTruncate makes a string exactly width columns wide, counting grapheme
// clusters rather than bytes.
func padTruncate(s string, width int, ellipsis string) string {
	if width <= 0 {
		return ""
	}
	got := ansi.StringWidth(s)
	switch {
	case got == width:
		return s
	case got < width:
		return s + strings.Repeat(" ", width-got)
	}
	out := ansi.Truncate(s, width, ellipsis)
	if pad := width - ansi.StringWidth(out); pad > 0 {
		out += strings.Repeat(" ", pad)
	}
	return out
}

func padLeft(s string, width int, ellipsis string) string {
	if width <= 0 {
		return ""
	}
	got := ansi.StringWidth(s)
	if got < width {
		return strings.Repeat(" ", width-got) + s
	}
	return padTruncate(s, width, ellipsis)
}
