package kernel

import (
	"errors"
	"fmt"
	"os"
	"strings"
	"sync/atomic"
	"unicode/utf8"

	"charm.land/bubbles/v2/help"
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"

	"github.com/varijkapil13/saral/internal/config"
	"github.com/varijkapil13/saral/pkg/jira"
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

// Glyphs is the icon set. A Nerd Font is assumed here as a tier, not as a
// floor: NerdGlyphs is the default, and the two tiers under it are kept whole
// rather than deleted — the settings screen's Glyphs row is how somebody
// without the font gets out of the tofu it draws.
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

	// The five icons docs/FILTERS.md names for an issue's place in the
	// hierarchy. TypeGlyph can only ever reach TypeSubtask today —
	// pkg/jira.IssueType carries the subtask flag and nothing that places the
	// rest in the hierarchy — the other four wait for the port amendment that
	// will.
	TypeEpic    string
	TypeStory   string
	TypeTask    string
	TypeBug     string
	TypeSubtask string

	// Keyed by jira.StatusCategory rather than by a status name.
	CategoryToDo       string
	CategoryInProgress string
	CategoryDone       string
	CategoryUnknown    string
}

// UnicodeGlyphs is the mid tier: box drawing and geometric shapes only.
func UnicodeGlyphs() Glyphs {
	return Glyphs{
		Bullet: "•", Arrow: "→", Check: "✓", Cross: "✗", Dot: "·",
		Ellipsis: "…", Stale: "◌", Spinner: []string{"⠋", "⠙", "⠹", "⠸", "⠼", "⠴", "⠦", "⠧", "⠇", "⠏"},
		VLine: "│", HLine: "─", CornerTL: "╭", CornerTR: "╮", CornerBL: "╰", CornerBR: "╯",
		Separator: "•", Collapsed: "▸", Expanded: "▾", Diamond: "◆",
		ProgressOn: "█", ProgressNo: "░",
		TypeEpic: "◆", TypeStory: "●", TypeTask: "■", TypeBug: "▲", TypeSubtask: "▪",
		CategoryToDo: "○", CategoryInProgress: "◐", CategoryDone: "●", CategoryUnknown: "◌",
	}
}

// ASCIIGlyphs is the floor: the fallback for terminals and fonts that cannot
// be trusted with anything past plain ASCII.
func ASCIIGlyphs() Glyphs {
	return Glyphs{
		Bullet: "*", Arrow: "->", Check: "+", Cross: "x", Dot: ".",
		Ellipsis: "...", Stale: "~", Spinner: []string{"|", "/", "-", "\\"},
		VLine: "|", HLine: "-", CornerTL: "+", CornerTR: "+", CornerBL: "+", CornerBR: "+",
		Separator: "|", Collapsed: ">", Expanded: "v", Diamond: "<>",
		ProgressOn: "#", ProgressNo: "-",
		TypeEpic: "<>", TypeStory: "*", TypeTask: "#", TypeBug: "!", TypeSubtask: "-",
		CategoryToDo: "o", CategoryInProgress: "~", CategoryDone: "x", CategoryUnknown: ".",
	}
}

// NerdGlyphs is the top tier, assumed by default: every icon a Nerd Font
// patches in over the box-drawing and geometric shapes UnicodeGlyphs already
// carries, which a Nerd Font renders as well as any other font does.
func NerdGlyphs() Glyphs {
	g := UnicodeGlyphs()
	g.Check = ""              // nf-fa-check
	g.Cross = ""              // nf-fa-times
	g.Arrow = ""              // nf-fa-arrow_right
	g.Stale = ""              // nf-fa-clock_o
	g.Collapsed = ""          // nf-fa-caret_right
	g.Expanded = ""           // nf-fa-caret_down
	g.Diamond = ""            // nf-fa-diamond
	g.TypeEpic = ""           // nf-fa-bolt
	g.TypeStory = ""          // nf-fa-bookmark
	g.TypeTask = ""           // nf-fa-tasks
	g.TypeBug = ""            // nf-fa-bug
	g.TypeSubtask = ""        // nf-fa-level_down
	g.CategoryToDo = ""       // nf-fa-circle_o
	g.CategoryInProgress = "" // nf-fa-clock_o
	g.CategoryDone = ""       // nf-fa-check_circle
	g.CategoryUnknown = ""    // nf-fa-question
	return g
}

// GlyphsFor picks a glyph set by name: "nerd", "unicode" or "ascii", falling
// back to nerd, the default tier.
func GlyphsFor(name string) Glyphs {
	switch strings.ToLower(strings.TrimSpace(name)) {
	case "ascii":
		return ASCIIGlyphs()
	case "unicode":
		return UnicodeGlyphs()
	default:
		return NerdGlyphs()
	}
}

// IsASCII reports whether these are the ASCII fallback glyphs rather than one
// of the other two tiers. Glyphs holds a slice field, so it is not comparable
// with ==; Bullet is enough to tell ASCII apart from the other two, which both
// keep it as the plain bullet.
func (g Glyphs) IsASCII() bool { return g.Bullet == ASCIIGlyphs().Bullet }

// Tier names which of the three sets this is. Cross differs across all three,
// which is what makes one field enough to tell them apart.
func (g Glyphs) Tier() string {
	switch g.Cross {
	case NerdGlyphs().Cross:
		return "nerd"
	case ASCIIGlyphs().Cross:
		return "ascii"
	default:
		return "unicode"
	}
}

// TypeGlyph resolves the icon for an issue type from what the site's own type
// carries, never from its name. pkg/jira.IssueType has no hierarchy level,
// only the subtask flag, so everything else falls back to the type's own
// first letter rather than to a hardcoded guess like "Bug".
func (g Glyphs) TypeGlyph(it jira.IssueType) string {
	if it.Subtask {
		return g.TypeSubtask
	}
	return firstLetterGlyph(it.Name)
}

// CategoryGlyph resolves the icon for a status category, which is the one
// status property the same on every site.
func (g Glyphs) CategoryGlyph(c jira.StatusCategory) string {
	switch c {
	case jira.CategoryToDo:
		return g.CategoryToDo
	case jira.CategoryInProgress:
		return g.CategoryInProgress
	case jira.CategoryDone:
		return g.CategoryDone
	default:
		return g.CategoryUnknown
	}
}

// PriorityGlyph resolves a priority's icon. pkg/jira.Priority carries only an
// ID and a name, nothing to rank it by, so it falls back to the same letter
// TypeGlyph does.
func (g Glyphs) PriorityGlyph(p jira.Priority) string { return firstLetterGlyph(p.Name) }

// firstLetterGlyph is the fallback every unresolved glyph shares: the first
// rune of a name, decoded rather than byte-sliced so a non-ASCII name still
// gives back one whole letter.
func firstLetterGlyph(name string) string {
	name = strings.TrimSpace(name)
	if name == "" {
		return "?"
	}
	r, _ := utf8.DecodeRuneInString(name)
	return strings.ToUpper(string(r))
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

// themeModes is every mode the theme setting offers, in the order it draws
// them.
var themeModes = []ThemeMode{ThemeAuto, ThemeDark, ThemeLight, ThemeNoColor}

// The theme and the glyph set are both switchable while the program runs,
// registered as settings: state that is, and stays that way until changed,
// rather than a verb the palette runs once. docs/SETTINGS.md is the design.
func init() {
	RegisterSetting(themeSetting())
	RegisterSetting(glyphsSetting())
}

func themeSetting() Setting {
	return Setting{
		ID:      "appearance.theme",
		Section: appearanceSection,
		Order:   0,
		Title:   "Theme",
		Summary: "how colours are chosen",
		Kind:    KindChoice,
		Scope:   ScopeProfile,
		Options: func(Deps) []SettingOption {
			out := make([]SettingOption, len(themeModes))
			for i, mode := range themeModes {
				out[i] = SettingOption{ID: mode.String(), Label: mode.label()}
			}
			return out
		},
		Value: func(d Deps) string {
			if d.Theme == nil {
				return ThemeAuto.String()
			}
			return d.Theme.Mode.String()
		},
		Set: func(d Deps, id string) tea.Cmd {
			mode, err := ParseThemeMode(id)
			if err != nil {
				return nil
			}
			return SwitchTheme(d, mode)
		},
		// The sentence is noColorForced's own, not a second copy of it: SwitchTheme
		// refuses a colour mode with exactly these words, and the two must not drift
		// apart.
		Unavailable: func(Deps) string {
			if forced, why := noColorForced(); forced {
				return why
			}
			return ""
		},
	}
}

func glyphsSetting() Setting {
	return Setting{
		ID:      "appearance.glyphs",
		Section: appearanceSection,
		Order:   2,
		Title:   "Glyphs",
		Summary: "Nerd Font icons, plain box drawing, or ASCII for a font you cannot trust",
		Kind:    KindChoice,
		Scope:   ScopeProfile,
		Options: func(Deps) []SettingOption {
			return []SettingOption{
				{ID: "nerd", Label: "nerd font"},
				{ID: "unicode", Label: "unicode"},
				{ID: "ascii", Label: "ascii"},
			}
		},
		Value: func(d Deps) string {
			if d.Theme == nil {
				return "nerd"
			}
			return d.Theme.Glyphs.Tier()
		},
		Set: SwitchGlyphs,
	}
}

// label is the mode as a settings row names it, which is a value and not an
// instruction: the row already says it is the theme, and "Theme: use the dark
// theme" reads as a command left over from the palette this moved out of.
func (m ThemeMode) label() string {
	switch m {
	case ThemeDark:
		return "dark"
	case ThemeLight:
		return "light"
	case ThemeNoColor:
		return "no colour"
	default:
		return "auto"
	}
}

// SwitchTheme rebuilds the styles in a new mode and keeps the choice. The
// glyph set and the colour scheme both come along unchanged: the glyphs answer
// a question about the font rather than about colour, and a session that fell
// back to ASCII must not quietly get box drawing back — and the scheme answers
// a question about which colours, so switching mode without being told a
// scheme must not silently revert a Nord session to the default palette while
// the profile still says otherwise.
//
// Auto asks the terminal for its background again rather than reusing the answer
// the old theme settled on, because that is the whole of what auto means and a
// terminal that has been changed since is the likely reason for switching to it.
func SwitchTheme(d Deps, mode ThemeMode) tea.Cmd {
	if forced, why := noColorForced(); forced && mode != ThemeNoColor {
		return func() tea.Msg { return StatusMsg{Text: why, Level: LevelWarn} }
	}
	dark, glyphs, scheme := true, NerdGlyphs(), DefaultScheme
	if d.Theme != nil {
		dark, glyphs, scheme = d.Theme.Dark, d.Theme.Glyphs, d.Theme.Scheme
	}
	next := NewTheme(mode, dark, glyphs, WithScheme(scheme))
	cmds := []tea.Cmd{
		func() tea.Msg { return ThemeMsg{Theme: next} },
		saveTheme(d.Site, mode),
	}
	if mode == ThemeAuto {
		cmds = append(cmds, tea.RequestBackgroundColor)
	}
	return tea.Batch(cmds...)
}

// SwitchGlyphs rebuilds the styles in a new glyph tier and keeps the choice.
// The mode and the scheme both come along unchanged: glyphs answer a question
// about the font, not about light, dark or which colours. tier is resolved
// through GlyphsFor, so an id this build does not know falls back to the same
// default a first run gets.
func SwitchGlyphs(d Deps, tier string) tea.Cmd {
	mode, dark, scheme := ThemeAuto, true, DefaultScheme
	if d.Theme != nil {
		mode, dark, scheme = d.Theme.Mode, d.Theme.Dark, d.Theme.Scheme
	}
	glyphs := GlyphsFor(tier)
	next := NewTheme(mode, dark, glyphs, WithScheme(scheme))
	return tea.Batch(
		func() tea.Msg { return ThemeMsg{Theme: next} },
		saveGlyphs(d.Site, glyphs),
	)
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

// saveGlyphs writes the glyph set into the profile it came from, the same
// shape saveTheme already answers in.
func saveGlyphs(site string, g Glyphs) tea.Cmd {
	return func() tea.Msg {
		switch err := writeGlyphs(site, g); {
		case err == nil:
			return nil
		case errors.Is(err, config.ErrNoConfig), errors.Is(err, config.ErrNoProfile):
			return StatusMsg{
				Text:  "the glyphs changed for this session; there is no profile to save it to",
				Level: LevelInfo,
			}
		default:
			return StatusMsg{
				Text:  "the glyphs changed for this session, but saving it failed: " + err.Error(),
				Level: LevelWarn,
			}
		}
	}
}

// writeGlyphs reads the whole file and writes it back with one field changed,
// for the reason writeTheme already does: Save writes the profile it is
// handed and nothing else, so a fresh Profile built from what is on screen
// would drop the saved queries, the timeline field names and the theme.
func writeGlyphs(site string, g Glyphs) error {
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
	if site != "" && profile.Site != site {
		return fmt.Errorf("this session is on %s and the active profile %q is on %s, so nothing was written",
			site, profile.Name, profile.Site)
	}
	// Nerd is the absence of a glyph set in the file rather than a value, so
	// that a profile that never chose and a profile that chose nerd read the
	// same.
	value := g.Tier()
	if value == "nerd" {
		value = ""
	}
	if profile.Glyphs == value {
		return nil
	}
	profile.Glyphs = value
	cfg.Profiles[profile.Name] = profile
	return cfg.Save(path)
}
