package kernel

import (
	"errors"
	"fmt"
	"image/color"
	"strings"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"

	"github.com/varijkapil13/saral/internal/config"
)

// Scheme is a named set of the colours every themed style draws from — the
// same nine roles Theme's colored() has always built from literals, now
// swappable as a whole rather than fixed to the one this program shipped
// with. It is a colour scheme and deliberately never called a "palette" in
// code or in anything this program says: that word already means the command
// palette everywhere else here, and a second meaning for it would be read as
// the first one.
type Scheme struct {
	id, title   string
	light, dark schemeColors
}

// schemeColors is the nine roles every themed style is built from, resolved
// for one of light or dark. Nine rather than one per Theme field, because a
// Header's background and a Badge's background are both surface, and giving
// every style its own literal would let two things meant to match drift apart
// one scheme edit at a time.
type schemeColors struct {
	fg, muted, accent, danger, warning, success, surface, selected, onAccent color.Color
}

func (s Scheme) colors(dark bool) schemeColors {
	if dark {
		return s.dark
	}
	return s.light
}

// ID is the name a config file, a flag or the command palette names this
// scheme by.
func (s Scheme) ID() string { return s.id }

// Title is the sentence the command palette offers this scheme with.
func (s Scheme) Title() string { return s.title }

// hexColors builds a schemeColors from nine hex strings in field order, so a
// scheme's table reads as the colours it names rather than as nine calls to
// lipgloss.Color repeated for every one of five schemes and two modes.
func hexColors(fg, muted, accent, danger, warning, success, surface, selected, onAccent string) schemeColors {
	return schemeColors{
		fg: lipgloss.Color(fg), muted: lipgloss.Color(muted), accent: lipgloss.Color(accent),
		danger: lipgloss.Color(danger), warning: lipgloss.Color(warning), success: lipgloss.Color(success),
		surface: lipgloss.Color(surface), selected: lipgloss.Color(selected), onAccent: lipgloss.Color(onAccent),
	}
}

// DefaultScheme is what a session starts in when nothing configured
// otherwise, and is the colours this program has always drawn: every other
// scheme is additive, not a replacement of what colour meant before it
// existed.
var DefaultScheme = Scheme{
	id: "default", title: "Use the default colours",
	light: hexColors("#1f2328", "#6e7781", "#0550ae", "#cf222e", "#9a6700", "#1a7f37", "#eaeef2", "#ddf4ff", "#0a3069"),
	dark:  hexColors("#e6edf3", "#8b949e", "#79c0ff", "#ff7b72", "#d29922", "#3fb950", "#161b22", "#1f6feb", "#f0f6fc"),
}

var nordScheme = Scheme{
	id: "nord", title: "Use the Nord colour scheme",
	dark:  hexColors("#eceff4", "#4c566a", "#88c0d0", "#bf616a", "#ebcb8b", "#a3be8c", "#3b4252", "#5e81ac", "#eceff4"),
	light: hexColors("#2e3440", "#4c566a", "#5e81ac", "#bf616a", "#b48111", "#4c7a3d", "#e5e9f0", "#88c0d0", "#2e3440"),
}

var draculaScheme = Scheme{
	id: "dracula", title: "Use the Dracula colour scheme",
	dark:  hexColors("#f8f8f2", "#6272a4", "#bd93f9", "#ff5555", "#f1fa8c", "#50fa7b", "#44475a", "#6272a4", "#282a36"),
	light: hexColors("#282a36", "#6272a4", "#6b46c1", "#d63031", "#b8860b", "#2e7d32", "#f4f4f8", "#e0d4f7", "#282a36"),
}

var solarizedScheme = Scheme{
	id: "solarized", title: "Use the Solarized colour scheme",
	dark:  hexColors("#839496", "#586e75", "#268bd2", "#dc322f", "#b58900", "#859900", "#073642", "#268bd2", "#fdf6e3"),
	light: hexColors("#657b83", "#93a1a1", "#268bd2", "#dc322f", "#b58900", "#859900", "#eee8d5", "#268bd2", "#fdf6e3"),
}

var gruvboxScheme = Scheme{
	id: "gruvbox", title: "Use the Gruvbox colour scheme",
	dark:  hexColors("#ebdbb2", "#928374", "#83a598", "#fb4934", "#fabd2f", "#b8bb26", "#3c3836", "#458588", "#282828"),
	light: hexColors("#3c3836", "#928374", "#458588", "#cc241d", "#d79921", "#98971a", "#ebdbb2", "#83a598", "#fbf1c7"),
}

// Schemes is every scheme this build offers, in the order the command palette
// and an unknown-scheme error list them in.
var Schemes = []Scheme{DefaultScheme, nordScheme, draculaScheme, solarizedScheme, gruvboxScheme}

// ParseScheme resolves a scheme by the name a config file or a flag gives it.
// Empty means the default, the same way an unset theme means auto.
func ParseScheme(s string) (Scheme, error) {
	name := strings.ToLower(strings.TrimSpace(s))
	if name == "" || name == "default" {
		return DefaultScheme, nil
	}
	for i := range Schemes {
		if Schemes[i].id == name {
			return Schemes[i], nil
		}
	}
	names := make([]string, len(Schemes))
	for i := range Schemes {
		names[i] = Schemes[i].id
	}
	return DefaultScheme, fmt.Errorf("kernel: unknown colour scheme %q, want one of %s", s, strings.Join(names, ", "))
}

// The scheme is switchable while the program runs, registered as commands for
// the same reason the theme's modes are — there is no letter left that would
// not also be a letter somebody types into a field.
func init() { registerSchemeCommands() }

func registerSchemeCommands() {
	for i := range Schemes {
		RegisterCommand(Command{
			ID:    "scheme." + Schemes[i].id,
			Title: Schemes[i].title,
			Group: "Appearance",
			Run:   func(d Deps) tea.Cmd { return SwitchScheme(d, Schemes[i]) },
		})
	}
}

// SwitchScheme rebuilds the styles in a new scheme and keeps the choice. The
// mode and the glyph set both come along unchanged: a scheme answers a
// question about which colours, not about light, dark or the font.
func SwitchScheme(d Deps, s Scheme) tea.Cmd {
	mode, dark, glyphs := ThemeAuto, true, UnicodeGlyphs()
	if d.Theme != nil {
		mode, dark, glyphs = d.Theme.Mode, d.Theme.Dark, d.Theme.Glyphs
	}
	next := NewTheme(mode, dark, glyphs, WithScheme(s))
	return tea.Batch(
		func() tea.Msg { return ThemeMsg{Theme: next} },
		saveScheme(d.Site, s),
	)
}

// saveScheme writes the scheme into the profile it came from. The scheme has
// already changed by the time this runs, so a failure is reported rather than
// undoing the switch — the same shape saveTheme already answers in.
func saveScheme(site string, s Scheme) tea.Cmd {
	return func() tea.Msg {
		switch err := writeScheme(site, s); {
		case err == nil:
			return nil
		case errors.Is(err, config.ErrNoConfig), errors.Is(err, config.ErrNoProfile):
			return StatusMsg{
				Text:  "the colour scheme changed for this session; there is no profile to save it to",
				Level: LevelInfo,
			}
		default:
			return StatusMsg{
				Text:  "the colour scheme changed for this session, but saving it failed: " + err.Error(),
				Level: LevelWarn,
			}
		}
	}
}

// writeScheme reads the whole file and writes it back with one field changed,
// for the reason writeTheme already does: Save writes the profile it is
// handed and nothing else, so a fresh Profile built from what is on screen
// would drop the saved queries, the timeline field names and the glyph set.
func writeScheme(site string, s Scheme) error {
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
	// Default is the absence of a scheme in the file rather than a value, so
	// that a profile that never chose and a profile that chose default read
	// the same.
	value := s.id
	if s.id == DefaultScheme.id {
		value = ""
	}
	if profile.Scheme == value {
		return nil
	}
	profile.Scheme = value
	cfg.Profiles[profile.Name] = profile
	return cfg.Save(path)
}
