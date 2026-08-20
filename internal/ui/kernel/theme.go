package kernel

import (
	"fmt"
	"strings"
	"sync/atomic"

	"charm.land/bubbles/v2/help"
	"charm.land/lipgloss/v2"
)

// ThemeMode is how colours are chosen.
type ThemeMode int

// The theme modes. Auto follows the terminal's background colour.
const (
	ThemeAuto ThemeMode = iota
	ThemeDark
	ThemeLight
	ThemeNoColor
)

// String names the mode as it is written in config.
func (m ThemeMode) String() string {
	switch m {
	case ThemeDark:
		return "dark"
	case ThemeLight:
		return "light"
	case ThemeNoColor:
		return "no-color"
	default:
		return "auto"
	}
}

// ParseThemeMode reads a theme mode from config. An empty string is Auto.
func ParseThemeMode(s string) (ThemeMode, error) {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "", "auto":
		return ThemeAuto, nil
	case "dark":
		return ThemeDark, nil
	case "light":
		return ThemeLight, nil
	case "no-color", "nocolor", "none", "mono":
		return ThemeNoColor, nil
	default:
		return ThemeAuto, fmt.Errorf("kernel: unknown theme %q, want auto, dark, light or no-color", s)
	}
}

// ThemeModeFromEnv resolves the mode a session should start in. NO_COLOR wins
// over configuration, because a user who set it meant it everywhere.
func ThemeModeFromEnv(env []string, configured string) ThemeMode {
	for _, kv := range env {
		name, value, ok := strings.Cut(kv, "=")
		switch {
		case !ok:
			continue
		case name == "NO_COLOR" && value != "":
			return ThemeNoColor
		case name == "TERM" && (value == "dumb" || value == ""):
			return ThemeNoColor
		}
	}
	mode, err := ParseThemeMode(configured)
	if err != nil {
		return ThemeAuto
	}
	return mode
}

// Glyphs is the icon set. Nothing here may assume a Nerd Font; the default set
// is plain Unicode and the fallback is ASCII.
type Glyphs struct {
	Bullet     string
	Arrow      string
	Check      string
	Cross      string
	Dot        string
	Ellipsis   string
	Stale      string
	Spinner    []string
	VLine      string
	HLine      string
	CornerTL   string
	CornerTR   string
	CornerBL   string
	CornerBR   string
	Separator  string
	Collapsed  string
	Expanded   string
	Diamond    string
	ProgressOn string
	ProgressNo string
}

// UnicodeGlyphs is the default set: box drawing and geometric shapes only.
func UnicodeGlyphs() Glyphs {
	return Glyphs{
		Bullet: "•", Arrow: "→", Check: "✓", Cross: "✗", Dot: "·",
		Ellipsis: "…", Stale: "◌", Spinner: []string{"⠋", "⠙", "⠹", "⠸", "⠼", "⠴", "⠦", "⠧", "⠇", "⠏"},
		VLine: "│", HLine: "─", CornerTL: "╭", CornerTR: "╮", CornerBL: "╰", CornerBR: "╯",
		Separator: "•", Collapsed: "▸", Expanded: "▾", Diamond: "◆",
		ProgressOn: "█", ProgressNo: "░",
	}
}

// ASCIIGlyphs is the fallback for terminals and fonts that cannot be trusted.
func ASCIIGlyphs() Glyphs {
	return Glyphs{
		Bullet: "*", Arrow: "->", Check: "+", Cross: "x", Dot: ".",
		Ellipsis: "...", Stale: "~", Spinner: []string{"|", "/", "-", "\\"},
		VLine: "|", HLine: "-", CornerTL: "+", CornerTR: "+", CornerBL: "+", CornerBR: "+",
		Separator: "|", Collapsed: ">", Expanded: "v", Diamond: "<>",
		ProgressOn: "#", ProgressNo: "-",
	}
}

// GlyphsFor picks a glyph set by name, falling back to Unicode.
func GlyphsFor(name string) Glyphs {
	if strings.EqualFold(strings.TrimSpace(name), "ascii") {
		return ASCIIGlyphs()
	}
	return UnicodeGlyphs()
}

// Theme holds every style the UI uses, built once so that nothing constructs a
// lipgloss.Style inside a render loop. Gen changes whenever the theme is
// rebuilt, which is what row memoization keys off.
type Theme struct {
	Gen    int
	Mode   ThemeMode
	Dark   bool
	Color  bool
	Glyphs Glyphs

	Base       lipgloss.Style
	Muted      lipgloss.Style
	Accent     lipgloss.Style
	Danger     lipgloss.Style
	Warning    lipgloss.Style
	Success    lipgloss.Style
	Title      lipgloss.Style
	Header     lipgloss.Style
	StatusBar  lipgloss.Style
	StatusWarn lipgloss.Style
	StatusFail lipgloss.Style
	Footer     lipgloss.Style
	SlotOn     lipgloss.Style
	SlotOff    lipgloss.Style
	SlotGone   lipgloss.Style
	HintKey    lipgloss.Style
	HintDesc   lipgloss.Style
	Selected   lipgloss.Style
	Badge      lipgloss.Style
	StaleBadge lipgloss.Style
	Overlay    lipgloss.Style
	Help       help.Styles

	// HelpModel is the configured help component. It lives here rather than in
	// the root model because it is presentation, and because a copy of it costs
	// more than the rest of the model put together.
	HelpModel help.Model
}

var themeGen atomic.Int64

// NewTheme builds the styles for a mode. dark is only consulted when the mode
// is Auto; it comes from the terminal's reported background colour.
func NewTheme(mode ThemeMode, dark bool, glyphs Glyphs) *Theme {
	t := &Theme{
		Gen:    int(themeGen.Add(1)),
		Mode:   mode,
		Dark:   dark,
		Color:  mode != ThemeNoColor,
		Glyphs: glyphs,
	}
	switch mode {
	case ThemeDark:
		t.Dark = true
	case ThemeLight:
		t.Dark = false
	case ThemeAuto, ThemeNoColor:
	}
	if t.Color {
		t.colored()
	} else {
		t.plain()
	}
	t.HelpModel = help.New()
	t.HelpModel.Styles = t.Help
	t.HelpModel.ShortSeparator = " " + t.Glyphs.Separator + " "
	t.HelpModel.FullSeparator = "    "
	t.HelpModel.Ellipsis = t.Glyphs.Ellipsis
	return t
}

// plain builds a theme that carries no colour at all. NO_COLOR asks for colour
// to go away, not for emphasis to go away, so bold and faint stay.
func (t *Theme) plain() {
	base := lipgloss.NewStyle()
	t.Base = base
	t.Muted = base.Faint(true)
	t.Accent = base.Bold(true)
	t.Danger = base.Bold(true)
	t.Warning = base.Bold(true)
	t.Success = base.Bold(true)
	t.Title = base.Bold(true)
	t.Header = base.Bold(true).Padding(0, 1)
	t.StatusBar = base.Padding(0, 1)
	t.StatusWarn = base.Bold(true)
	t.StatusFail = base.Bold(true)
	t.Footer = base.Faint(true)
	t.SlotOn = base.Bold(true).Reverse(true).Padding(0, 1)
	t.SlotOff = base.Padding(0, 1)
	t.SlotGone = base.Faint(true).Padding(0, 1)
	t.HintKey = base.Bold(true)
	t.HintDesc = base.Faint(true)
	t.Selected = base.Reverse(true)
	t.Badge = base.Padding(0, 1)
	t.StaleBadge = base.Faint(true).Padding(0, 1)
	t.Overlay = base.Border(lipgloss.NormalBorder()).Padding(0, 1)
	t.Help = help.Styles{
		Ellipsis:       t.Muted,
		ShortKey:       t.HintKey,
		ShortDesc:      t.HintDesc,
		ShortSeparator: t.Muted,
		FullKey:        t.HintKey,
		FullDesc:       t.HintDesc,
		FullSeparator:  t.Muted,
	}
}

func (t *Theme) colored() {
	pick := lipgloss.LightDark(t.Dark)
	var (
		fg       = pick(lipgloss.Color("#1f2328"), lipgloss.Color("#e6edf3"))
		muted    = pick(lipgloss.Color("#6e7781"), lipgloss.Color("#8b949e"))
		accent   = pick(lipgloss.Color("#0550ae"), lipgloss.Color("#79c0ff"))
		danger   = pick(lipgloss.Color("#cf222e"), lipgloss.Color("#ff7b72"))
		warning  = pick(lipgloss.Color("#9a6700"), lipgloss.Color("#d29922"))
		success  = pick(lipgloss.Color("#1a7f37"), lipgloss.Color("#3fb950"))
		surface  = pick(lipgloss.Color("#eaeef2"), lipgloss.Color("#161b22"))
		selected = pick(lipgloss.Color("#ddf4ff"), lipgloss.Color("#1f6feb"))
		onAccent = pick(lipgloss.Color("#0a3069"), lipgloss.Color("#f0f6fc"))
	)
	base := lipgloss.NewStyle().Foreground(fg)
	t.Base = base
	t.Muted = lipgloss.NewStyle().Foreground(muted)
	t.Accent = lipgloss.NewStyle().Foreground(accent)
	t.Danger = lipgloss.NewStyle().Foreground(danger)
	t.Warning = lipgloss.NewStyle().Foreground(warning)
	t.Success = lipgloss.NewStyle().Foreground(success)
	t.Title = lipgloss.NewStyle().Foreground(fg).Bold(true)
	t.Header = lipgloss.NewStyle().Foreground(fg).Background(surface).Bold(true).Padding(0, 1)
	t.StatusBar = lipgloss.NewStyle().Foreground(muted)
	t.StatusWarn = lipgloss.NewStyle().Foreground(warning)
	t.StatusFail = lipgloss.NewStyle().Foreground(danger)
	t.Footer = lipgloss.NewStyle().Foreground(muted).Background(surface)
	t.SlotOn = lipgloss.NewStyle().Foreground(onAccent).Background(selected).Bold(true).Padding(0, 1)
	t.SlotOff = lipgloss.NewStyle().Foreground(fg).Background(surface).Padding(0, 1)
	t.SlotGone = lipgloss.NewStyle().Foreground(muted).Background(surface).Faint(true).Padding(0, 1)
	t.HintKey = lipgloss.NewStyle().Foreground(accent).Background(surface)
	t.HintDesc = lipgloss.NewStyle().Foreground(muted).Background(surface)
	t.Selected = lipgloss.NewStyle().Foreground(onAccent).Background(selected)
	t.Badge = lipgloss.NewStyle().Foreground(onAccent).Background(surface).Padding(0, 1)
	t.StaleBadge = lipgloss.NewStyle().Foreground(warning).Padding(0, 1)
	t.Overlay = lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(muted).
		Foreground(fg).
		Padding(0, 1)
	t.Help = help.Styles{
		Ellipsis:       t.Muted,
		ShortKey:       lipgloss.NewStyle().Foreground(accent),
		ShortDesc:      t.Muted,
		ShortSeparator: t.Muted,
		FullKey:        lipgloss.NewStyle().Foreground(accent),
		FullDesc:       t.Muted,
		FullSeparator:  t.Muted,
	}
}
