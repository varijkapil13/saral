package kernel

import (
	"errors"
	"fmt"
	"os"
	"strings"
	"sync/atomic"

	"charm.land/bubbles/v2/help"
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"

	"github.com/varijkapil13/saral/internal/config"
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
	// Scheme is which set of colours Base through Overlay below were built
	// from. It travels with the theme so that switching mode (SwitchTheme)
	// without being told a scheme keeps the one already in force, the same way
	// switching scheme keeps the mode.
	Scheme Scheme

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

// ThemeOption configures NewTheme beyond mode, dark and glyphs. It exists so
// that the scheme is optional at every one of NewTheme's existing call
// sites — a caller that never heard of one still gets DefaultScheme, which is
// the colours this program always drew.
type ThemeOption func(*themeConfig)

type themeConfig struct {
	scheme Scheme
}

// WithScheme sets which named set of colours the theme is built from. Not
// given, a theme is built from DefaultScheme.
func WithScheme(s Scheme) ThemeOption {
	return func(c *themeConfig) { c.scheme = s }
}

// NewTheme builds the styles for a mode. dark is only consulted when the mode
// is Auto; it comes from the terminal's reported background colour.
func NewTheme(mode ThemeMode, dark bool, glyphs Glyphs, opts ...ThemeOption) *Theme {
	cfg := themeConfig{scheme: DefaultScheme}
	for _, opt := range opts {
		opt(&cfg)
	}
	t := &Theme{
		Gen:    int(themeGen.Add(1)),
		Mode:   mode,
		Dark:   dark,
		Color:  mode != ThemeNoColor,
		Glyphs: glyphs,
		Scheme: cfg.scheme,
	}
	switch mode {
	case ThemeDark:
		t.Dark = true
	case ThemeLight:
		t.Dark = false
	case ThemeAuto, ThemeNoColor:
	}
	if t.Color {
		t.colored(cfg.scheme.colors(t.Dark))
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

// colored builds the styles from one scheme's nine roles, already resolved
// for this theme's mode by the caller — colored itself never asks whether it
// is drawing light or dark, so a scheme with the two reversed cannot make it
// draw the wrong one.
func (t *Theme) colored(c schemeColors) {
	fg, muted, accent, danger, warning, success, surface, selected, onAccent :=
		c.fg, c.muted, c.accent, c.danger, c.warning, c.success, c.surface, c.selected, c.onAccent
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

// The theme is switchable while the program runs. It registers commands rather
// than keys: there is no letter left that would not also be a letter somebody
// types into a field, and docs/UX.md points at the palette for everything that
// has no key of its own.
func init() { registerThemeCommands() }

func registerThemeCommands() {
	for _, mode := range []ThemeMode{ThemeAuto, ThemeDark, ThemeLight, ThemeNoColor} {
		RegisterCommand(Command{
			ID:    "theme." + mode.String(),
			Title: mode.title(),
			Group: "Appearance",
			Run:   func(d Deps) tea.Cmd { return SwitchTheme(d, mode) },
		})
	}
}

// title is how the palette offers the mode.
func (m ThemeMode) title() string {
	switch m {
	case ThemeDark:
		return "Use the dark theme"
	case ThemeLight:
		return "Use the light theme"
	case ThemeNoColor:
		return "Turn colour off"
	default:
		return "Follow the terminal's own colours"
	}
}

// SwitchTheme rebuilds the styles in a new mode and keeps the choice. The glyph
// set comes along unchanged: it answers a question about the font rather than
// about colour, and a session that fell back to ASCII must not quietly get box
// drawing back.
//
// Auto asks the terminal for its background again rather than reusing the answer
// the old theme settled on, because that is the whole of what auto means and a
// terminal that has been changed since is the likely reason for switching to it.
func SwitchTheme(d Deps, mode ThemeMode) tea.Cmd {
	if forced, why := noColorForced(); forced && mode != ThemeNoColor {
		return func() tea.Msg { return StatusMsg{Text: why, Level: LevelWarn} }
	}
	dark, glyphs := true, UnicodeGlyphs()
	if d.Theme != nil {
		dark, glyphs = d.Theme.Dark, d.Theme.Glyphs
	}
	next := NewTheme(mode, dark, glyphs)
	cmds := []tea.Cmd{
		func() tea.Msg { return ThemeMsg{Theme: next} },
		saveTheme(d.Site, mode),
	}
	if mode == ThemeAuto {
		cmds = append(cmds, tea.RequestBackgroundColor)
	}
	return tea.Batch(cmds...)
}

// noColorForced reports whether the environment has already said no colour, in
// which case a mode with colour in it is refused rather than quietly overriding
// what the user exported. ThemeModeFromEnv is asked with nothing configured, so
// only the environment can answer no-colour here.
func noColorForced() (forced bool, why string) {
	if ThemeModeFromEnv(os.Environ(), "") != ThemeNoColor {
		return false, ""
	}
	return true, "this environment asks for no colour — NO_COLOR or TERM says so, and that beats a theme"
}

// saveTheme writes the mode into the profile it came from. The theme has already
// changed by the time this runs, so a failure is reported rather than undoing
// the switch.
func saveTheme(site string, mode ThemeMode) tea.Cmd {
	return func() tea.Msg {
		switch err := writeTheme(site, mode); {
		case err == nil:
			return nil
		case errors.Is(err, config.ErrNoConfig), errors.Is(err, config.ErrNoProfile):
			return StatusMsg{
				Text:  "the theme changed for this session; there is no profile to save it to",
				Level: LevelInfo,
			}
		default:
			return StatusMsg{
				Text:  "the theme changed for this session, but saving it failed: " + err.Error(),
				Level: LevelWarn,
			}
		}
	}
}

// writeTheme reads the whole file and writes it back with one field changed.
// Save writes the profile it is handed and nothing else, so a fresh Profile
// built from what is on screen would drop the saved queries, the timeline field
// names and the glyph set.
func writeTheme(site string, mode ThemeMode) error {
	path, err := config.Path()
	if err != nil {
		return err
	}
	cfg, err := config.LoadFile(path)
	if err != nil {
		return err
	}
	profile, err := cfg.Current()
	if err != nil {
		return err
	}
	// The kernel is told which site it is talking to and never which profile was
	// named on the command line, so a session started with --profile would
	// otherwise write the choice onto whichever profile is active instead.
	if site != "" && profile.Site != site {
		return fmt.Errorf("this session is on %s and the active profile %q is on %s, so nothing was written",
			site, profile.Name, profile.Site)
	}
	// Auto is the absence of a theme in the file rather than a value, so that a
	// profile that never chose and a profile that chose auto read the same.
	value := mode.String()
	if mode == ThemeAuto {
		value = ""
	}
	if profile.Theme == value {
		return nil
	}
	profile.Theme = value
	cfg.Profiles[profile.Name] = profile
	return cfg.Save(path)
}
