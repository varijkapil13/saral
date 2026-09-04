package kernel

import (
	"errors"
	"fmt"
	"slices"
	"sort"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"

	"github.com/varijkapil13/saral/internal/config"
	"github.com/varijkapil13/saral/pkg/jira"
)

// SettingKind is how a setting is drawn and what changing it means.
type SettingKind int

const (
	// KindChoice is one value out of a known set. It draws as radios when the
	// options fit the row and as a value with a picker behind it when they do
	// not.
	KindChoice SettingKind = iota
	// KindToggle is a choice of exactly two where one is the absence of the
	// other.
	KindToggle
	// KindInfo is state the program shows and cannot change here.
	KindInfo
	// KindAction is a button: it runs a command and holds no value.
	KindAction
)

// SettingScope is where a chosen value is kept, which is drawn beside the
// section so that nothing has to guess whether a choice survives the session.
type SettingScope int

// The scopes a setting's value can be kept in.
const (
	ScopeProfile SettingScope = iota // config.toml, the active profile
	ScopeFile                        // config.toml, shared by every profile
	ScopeMachine                     // the cache directory's ui.toml
	ScopeSession                     // this run only
)

// SettingOption is one value a KindChoice or KindToggle setting can take. It
// is not named Option: kernel.Option already names the functional option
// kernel.New's own callers pass, and the two are unrelated.
type SettingOption struct {
	ID    string
	Label string
	// Note is the half-line under or beside the label: what the value means, or
	// what is different about it.
	Note string
	// Style, when non-nil, draws this option's label. It is how the colour
	// schemes preview themselves in their own colours.
	Style func(*Theme) lipgloss.Style
}

// Setting is one piece of state the settings view can show and change.
type Setting struct {
	// ID is stable and dotted, "appearance.scheme". It is what a deep link
	// names and what the frecency table would key on if settings ever rank.
	ID string
	// Section buckets settings on screen. Order within a section is Order, then
	// Title.
	Section string
	Order   int
	Title   string
	// Summary is the one line under the title. It says what the setting
	// decides, never what the current value is — Value answers that.
	Summary string
	Kind    SettingKind
	Scope   SettingScope
	// Requires names the capability this setting needs, if any, and is refused
	// with the probe's own words exactly as Command.Requires is.
	Requires jira.CapabilityKey

	// Options are what a KindChoice or KindToggle offers. It is a function
	// because the answer can come from the site: the projects are read, not
	// registered.
	Options func(Deps) []SettingOption
	// Value is which option is in force right now, read from the live session
	// rather than from anything this registry stored. That is the whole
	// point: a mark computed from Deps.Theme cannot drift from what is on
	// screen, and one cached at registration can.
	Value func(Deps) string
	// Set changes it. It returns the command that both applies and persists,
	// which is what SwitchTheme and SwitchScheme already are.
	Set func(d Deps, optionID string) tea.Cmd
	// OpenPicker opens a picker of this setting's own rather than the generic
	// one the settings view builds from Options, Value and Set. A KindChoice
	// setting sets this when its real option list cannot be answered
	// synchronously — session.project's comes from the site, read inside the
	// picker project.switch already opens — so the owning package supplies
	// the picker itself instead of the settings view learning its ID.
	OpenPicker func(Deps) tea.Cmd

	// Unavailable is why this setting cannot be changed here, and "" when it
	// can. It is not the same question as Requires: NO_COLOR being exported is
	// not a capability, and neither is "colour is off, so a scheme changes
	// nothing". A setting that answers is drawn with its value and its reason,
	// never hidden — hiding it would leave a user looking for a control that
	// is there and inert.
	Unavailable func(Deps) string

	// Run is what a KindAction does.
	Run func(Deps) tea.Cmd
}

// RegisterSetting adds a setting to the registry. It is called from an init()
// in the package that owns the state, exactly as RegisterView and
// RegisterCommand are.
//
// A bad or duplicate registration is recorded rather than raised, for the same
// reason RegisterCommand records one: init() runs before anything can handle an
// error, and a panic in a library package is worse than a startup message.
func RegisterSetting(s Setting) {
	reg.mu.Lock()
	defer reg.mu.Unlock()
	switch {
	case s.ID == "":
		reg.errs = append(reg.errs, fmt.Errorf("kernel: a setting was registered with no ID"))
		return
	case s.Section == "":
		reg.errs = append(reg.errs, fmt.Errorf("kernel: setting %q registered with no section", s.ID))
		return
	case s.Title == "":
		reg.errs = append(reg.errs, fmt.Errorf("kernel: setting %q registered with no title", s.ID))
		return
	}
	switch s.Kind {
	case KindChoice, KindToggle:
		if s.Options == nil || s.Value == nil || s.Set == nil {
			reg.errs = append(reg.errs, fmt.Errorf(
				"kernel: setting %q is a choice or toggle with no Options, Value or Set", s.ID))
			return
		}
	case KindInfo:
		if s.Value == nil {
			reg.errs = append(reg.errs, fmt.Errorf("kernel: setting %q is info with no Value", s.ID))
			return
		}
	case KindAction:
		if s.Run == nil {
			reg.errs = append(reg.errs, fmt.Errorf("kernel: setting %q is an action with no Run", s.ID))
			return
		}
	default:
		reg.errs = append(reg.errs, fmt.Errorf("kernel: setting %q has an unknown kind %d", s.ID, s.Kind))
		return
	}
	if _, dup := reg.settings[s.ID]; dup {
		reg.errs = append(reg.errs, fmt.Errorf("kernel: setting %q is registered twice", s.ID))
		return
	}
	reg.settings[s.ID] = s
	if !slices.Contains(reg.settingSections, s.Section) {
		reg.settingSections = append(reg.settingSections, s.Section)
	}
}

// Settings returns every registered setting, ordered by section — in the order
// sections were first registered — then by Order, then by Title.
func Settings() []Setting {
	reg.mu.RLock()
	defer reg.mu.RUnlock()
	sections := reg.settingSections
	out := make([]Setting, 0, len(reg.settings))
	for id := range reg.settings {
		out = append(out, reg.settings[id])
	}
	sort.Slice(out, func(i, j int) bool {
		si, sj := slices.Index(sections, out[i].Section), slices.Index(sections, out[j].Section)
		if si != sj {
			return si < sj
		}
		if out[i].Order != out[j].Order {
			return out[i].Order < out[j].Order
		}
		if out[i].Title != out[j].Title {
			return out[i].Title < out[j].Title
		}
		return out[i].ID < out[j].ID
	})
	return out
}

// SettingSections names every section that has a setting in it, in the order
// they were first registered rather than alphabetically — the order the
// screen groups them in.
func SettingSections() []string {
	reg.mu.RLock()
	defer reg.mu.RUnlock()
	return append([]string(nil), reg.settingSections...)
}

// resetSettings clears every registered setting and every recorded
// registration error, leaving the views, commands and keys other packages
// registered alone. Tests use it; nothing else may.
func resetSettings() {
	reg.mu.Lock()
	defer reg.mu.Unlock()
	reg.settings = make(map[string]Setting)
	reg.settingSections = nil
	reg.errs = nil
}

const appearanceSection = "Appearance"

func init() { RegisterSetting(mouseSetting()) }

func mouseSetting() Setting {
	return Setting{
		ID:      "appearance.mouse",
		Section: appearanceSection,
		Order:   3,
		Title:   "Mouse",
		Summary: "clicking, dragging the split, the right-click menu",
		Kind:    KindToggle,
		Scope:   ScopeFile,
		Options: func(Deps) []SettingOption {
			return []SettingOption{{ID: "on", Label: "on"}, {ID: "off", Label: "off"}}
		},
		Value: func(d Deps) string {
			if d.Zones != nil && d.Zones.Enabled() {
				return "on"
			}
			return "off"
		},
		Set: func(d Deps, id string) tea.Cmd { return SwitchMouse(d, id == "on") },
	}
}

// SwitchMouse turns mouse reporting on or off for the rest of this run and
// every one after it: it emits the message the kernel re-enables the zone
// manager from, and persists the choice to the shared part of the config
// file, the same read-modify-write shape writeTheme and writeScheme use.
func SwitchMouse(d Deps, enabled bool) tea.Cmd {
	return tea.Batch(SetMouse(enabled), saveMouse(enabled))
}

// saveMouse writes the choice into config.toml. Mouse is Config.Mouse, shared
// by every profile rather than kept on one, so there is no site to check
// against and no profile that could be missing — only the file itself.
func saveMouse(enabled bool) tea.Cmd {
	return func() tea.Msg {
		switch err := writeMouse(enabled); {
		case err == nil:
			return nil
		case errors.Is(err, config.ErrNoConfig):
			return StatusMsg{
				Text:  "the mouse changed for this session; there is no config file to save it to",
				Level: LevelInfo,
			}
		default:
			return StatusMsg{
				Text:  "the mouse changed for this session, but saving it failed: " + err.Error(),
				Level: LevelWarn,
			}
		}
	}
}

func writeMouse(enabled bool) error {
	path, err := config.Path()
	if err != nil {
		return err
	}
	cfg, err := config.LoadFile(path)
	if err != nil {
		return err
	}
	if cfg.Mouse == enabled {
		return nil
	}
	cfg.Mouse = enabled
	return cfg.Save(path)
}
