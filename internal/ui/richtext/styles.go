package richtext

import (
	"image/color"
	"strings"

	"charm.land/lipgloss/v2"
)

// Palette is the theme, handed in by the caller. The renderer never reads a
// theme itself: the views that use it hold one, this package holds none, and
// the goldens are then a property of the document rather than of whichever
// theme was loaded when they were written.
type Palette struct {
	Base    lipgloss.Style
	Muted   lipgloss.Style
	Title   lipgloss.Style
	Accent  lipgloss.Style
	Danger  lipgloss.Style
	Warning lipgloss.Style
	Success lipgloss.Style
	Badge   lipgloss.Style

	// Color is false for the no-colour theme, where emphasis must survive as
	// bold, faint, italic or reverse rather than disappearing: NO_COLOR asks for
	// colour to go away, not for emphasis to.
	Color bool
}

// PanelStyles is one style per panel kind. Custom is the one the site coloured
// itself, whose colour arrives with the document.
type PanelStyles struct {
	Info    lipgloss.Style
	Note    lipgloss.Style
	Warning lipgloss.Style
	Success lipgloss.Style
	Error   lipgloss.Style
	Custom  lipgloss.Style
}

// StatusStyles is one style per value of the colour enum a status lozenge
// carries. The theme has one accent, so blue and purple share it; the words in
// the lozenge are what tell those two apart, and inventing a colour outside the
// palette would break both the no-colour theme and the theme-independence of
// the goldens.
type StatusStyles struct {
	Neutral lipgloss.Style
	Blue    lipgloss.Style
	Purple  lipgloss.Style
	Red     lipgloss.Style
	Yellow  lipgloss.Style
	Green   lipgloss.Style
}

// Styles is one style per construct, built once per theme so that nothing
// constructs a lipgloss.Style inside a render loop.
type Styles struct {
	Body  lipgloss.Style
	Muted lipgloss.Style

	H1 lipgloss.Style
	H2 lipgloss.Style
	H3 lipgloss.Style

	Strong    lipgloss.Style
	Em        lipgloss.Style
	Underline lipgloss.Style
	Strike    lipgloss.Style
	Code      lipgloss.Style
	Link      lipgloss.Style
	URL       lipgloss.Style

	Bullet   lipgloss.Style
	Number   lipgloss.Style
	Task     lipgloss.Style
	TaskDone lipgloss.Style
	Decision lipgloss.Style

	Quote    lipgloss.Style
	QuoteBar lipgloss.Style

	CodeBlock lipgloss.Style
	CodeLang  lipgloss.Style
	CodeBar   lipgloss.Style

	Panel PanelStyles

	Rule lipgloss.Style

	TableBorder lipgloss.Style
	TableHeader lipgloss.Style
	TableCell   lipgloss.Style

	FoldMark  lipgloss.Style
	FoldTitle lipgloss.Style

	Media   lipgloss.Style
	Mention lipgloss.Style
	Date    lipgloss.Style
	Card    lipgloss.Style

	Status StatusStyles

	Unknown lipgloss.Style
	Cont    lipgloss.Style

	// Color mirrors Palette.Color. A textColor mark has nothing to render in a
	// theme that has no colour, and must not silently become emphasis it never
	// carried.
	Color bool
}

// NewStyles derives every construct's style from the palette.
func NewStyles(p Palette) Styles {
	s := Styles{Color: p.Color}
	s.Body = p.Base
	s.Muted = p.Muted

	// Levels 1 and 2 are told apart by an attribute rather than a colour, so
	// that they stay distinguishable in the no-colour theme; 3 and below are
	// told apart by the indent the renderer gives them.
	s.H1 = p.Title.Bold(true).Underline(true)
	s.H2 = p.Title.Bold(true)
	s.H3 = p.Accent.Bold(true)

	s.Strong = p.Base.Bold(true)
	s.Em = p.Base.Italic(true)
	s.Underline = p.Base.Underline(true)
	s.Strike = p.Base.Strikethrough(true)
	s.Code = p.Badge
	if !p.Color {
		s.Code = p.Base.Reverse(true)
	}
	s.Link = p.Accent.Underline(true)
	if !p.Color {
		s.Link = p.Base.Underline(true)
	}
	s.URL = p.Muted

	s.Bullet = p.Muted
	s.Number = p.Muted
	s.Task = p.Accent
	s.TaskDone = p.Success
	s.Decision = p.Accent

	s.Quote = p.Muted
	s.QuoteBar = p.Muted

	s.CodeBlock = p.Base
	s.CodeLang = p.Muted
	s.CodeBar = p.Muted

	s.Panel = PanelStyles{
		Info:    p.Accent,
		Note:    p.Accent,
		Warning: p.Warning,
		Success: p.Success,
		Error:   p.Danger,
		Custom:  p.Accent,
	}

	s.Rule = p.Muted

	s.TableBorder = p.Muted
	s.TableHeader = p.Title.Bold(true)
	s.TableCell = p.Base

	s.FoldMark = p.Accent
	s.FoldTitle = p.Title.Bold(true)

	s.Media = p.Accent
	s.Mention = p.Accent
	s.Date = p.Accent
	s.Card = p.Accent

	s.Status = StatusStyles{
		Neutral: p.Muted,
		Blue:    p.Accent,
		Purple:  p.Accent,
		Red:     p.Danger,
		Yellow:  p.Warning,
		Green:   p.Success,
	}
	if !p.Color {
		bold := p.Base.Bold(true)
		s.Status = StatusStyles{Neutral: bold, Blue: bold, Purple: bold, Red: bold, Yellow: bold, Green: bold}
	}

	s.Unknown = p.Warning.Bold(true)
	s.Cont = p.Muted
	return s
}

// status picks the style for the colour a lozenge carries. An unknown value is
// neutral rather than dropped: the wording is still the author's.
func (s *Styles) status(name string) *lipgloss.Style {
	switch name {
	case "blue":
		return &s.Status.Blue
	case "purple":
		return &s.Status.Purple
	case "red":
		return &s.Status.Red
	case "yellow":
		return &s.Status.Yellow
	case "green":
		return &s.Status.Green
	default:
		return &s.Status.Neutral
	}
}

// panelStyle picks the style and marker for a panel.
func (s *Styles) panelStyle(kind string, m Markers) (style *lipgloss.Style, marker, label string) {
	switch kind {
	case "info":
		return &s.Panel.Info, m.Info, "INFO"
	case "note":
		return &s.Panel.Note, m.Note, "NOTE"
	case "success", "tip":
		return &s.Panel.Success, m.Success, strings.ToUpper(sanitize(kind))
	case "warning":
		return &s.Panel.Warning, m.Warning, "WARNING"
	case "error":
		return &s.Panel.Error, m.Error, "ERROR"
	case "", "custom":
		return &s.Panel.Custom, m.Panel, "PANEL"
	default:
		return &s.Panel.Custom, m.Panel, strings.ToUpper(sanitize(kind))
	}
}

// marks is the set of inline annotations on one text node, gathered before any
// of them is applied: ADF hands them over in an order that is byte-significant
// and means nothing, so strong inside em has to render as em inside strong.
type marks struct {
	strong    bool
	em        bool
	underline bool
	strike    bool
	code      bool
	sub       bool
	sup       bool
	href      string
	fg        string
	bg        string
}

func (m marks) any() bool {
	return m.strong || m.em || m.underline || m.strike || m.code ||
		m.href != "" || m.fg != "" || m.bg != ""
}

// apply builds the style for a run of marked text on top of the style its block
// gives it — a quote's text stays a quote's text under a bold mark.
func (s *Styles) apply(ctx lipgloss.Style, m marks) lipgloss.Style {
	out := ctx
	switch {
	case m.code:
		out = s.Code
		if m.href != "" {
			out = out.Underline(true)
		}
	case m.href != "":
		out = s.Link
	}
	if m.strong {
		out = out.Bold(true)
	}
	if m.em {
		out = out.Italic(true)
	}
	if m.underline {
		out = out.Underline(true)
	}
	if m.strike {
		out = out.Strikethrough(true)
	}
	if s.Color {
		if c, ok := wireColor(m.fg); ok {
			out = out.Foreground(c)
		}
		if c, ok := wireColor(m.bg); ok {
			out = out.Background(c)
		}
	}
	return out
}

// wireColor reads a colour off the wire. Anything but a hex triple is refused
// rather than handed on, because lipgloss renders an unparseable colour as no
// colour and a caller cannot tell that from a document that asked for none.
func wireColor(v string) (color.Color, bool) {
	if len(v) != 7 && len(v) != 4 {
		return nil, false
	}
	if v[0] != '#' {
		return nil, false
	}
	for i := 1; i < len(v); i++ {
		switch c := v[i]; {
		case c >= '0' && c <= '9', c >= 'a' && c <= 'f', c >= 'A' && c <= 'F':
		default:
			return nil, false
		}
	}
	return lipgloss.Color(v), true
}
