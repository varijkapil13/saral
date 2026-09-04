package settings

import (
	"sort"

	tea "charm.land/bubbletea/v2"

	"github.com/varijkapil13/saral/internal/config"
	"github.com/varijkapil13/saral/internal/ui/kernel"
)

// sessionSection is the section name palette.project.go's session.project
// registers under too — kernel.Settings groups by this string, not by a
// shared symbol, the same way two packages both naming "Appearance" already
// share a section without importing one another.
const sessionSection = "Session"

// session.profile and the onboarding action are registered here rather than
// from internal/config or internal/ui/onboarding. config cannot: the kernel
// package it would need already imports config, and a setting is a
// kernel.Setting. onboarding could host them architecturally — it already
// owns the profile's whole lifecycle — but it is not a path this packet owns,
// and every one of these functions only reads internal/config directly and
// runs an already-registered command by ID, so nothing here needs a second
// package's internals to do it from here instead.
func init() {
	kernel.RegisterSetting(profileSetting())
	kernel.RegisterSetting(onboardingSetting())
}

// profileState is config.toml read fresh: the active profile is state the
// program keeps on disk and nowhere in kernel.Deps, so a Setting.Value here
// has no live field to read the way appearance.theme's does.
type profileState struct {
	err   error
	name  string
	site  string
	email string
	token string
	names []string
}

func (p profileState) multi() bool { return len(p.names) > 1 }

func readProfile() profileState {
	path, err := config.Path()
	if err != nil {
		return profileState{err: err}
	}
	cfg, err := config.LoadFile(path)
	if err != nil {
		return profileState{err: err}
	}
	current, err := cfg.Current()
	if err != nil {
		return profileState{err: err, names: cfg.Names()}
	}
	names := append([]string(nil), cfg.Names()...)
	sort.Strings(names)
	return profileState{
		name: current.Name, site: current.Site, email: current.Email,
		token: current.Token.String(), names: names,
	}
}

func (p profileState) value() string {
	if p.name == "" {
		return "not set up yet"
	}
	return p.name + " · " + p.site + " · " + p.email + " · token from " + p.token
}

func profileSetting() kernel.Setting {
	return kernel.Setting{
		ID:      "session.profile",
		Section: sessionSection,
		Order:   1,
		Title:   "Profile",
		Summary: "site, account and where the token comes from; changing it needs a restart",
		Kind:    kernel.KindInfo,
		Scope:   kernel.ScopeFile,
		Value:   func(kernel.Deps) string { return readProfile().value() },
		Run:     openProfile,
	}
}

// openProfile is enter on the Profile row: a single profile has nothing to
// switch to, so it repeats what the row already says, and more than one opens
// a picker that writes which is active — a hot swap is not this packet's, per
// docs/SETTINGS.md: run() builds the token, the client, the cache and the
// theme before kernel.New, so switching here cannot reach any of them.
func openProfile(d kernel.Deps) tea.Cmd {
	p := readProfile()
	if !p.multi() {
		return kernel.Status(p.value())
	}
	opts := make([]kernel.SettingOption, len(p.names))
	for i, name := range p.names {
		note := ""
		if name == p.name {
			note = "current"
		}
		opts[i] = kernel.SettingOption{ID: name, Label: name, Note: note}
	}
	return openOptionsPicker(d, "Profile", opts, p.name, setActiveProfile)
}

// setActiveProfile writes active = "<name>" and says, in the status line,
// that it takes effect next run — the same honesty writeTheme's own status
// messages already default to when a write cannot apply to what is on screen.
func setActiveProfile(_ kernel.Deps, name string) tea.Cmd {
	return func() tea.Msg {
		if err := writeActiveProfile(name); err != nil {
			return kernel.StatusMsg{Text: "the active profile could not be changed: " + err.Error(), Level: kernel.LevelWarn}
		}
		return kernel.StatusMsg{Text: name + " is now active, and takes effect the next time saral starts", Level: kernel.LevelInfo}
	}
}

func writeActiveProfile(name string) error {
	path, err := config.Path()
	if err != nil {
		return err
	}
	cfg, err := config.LoadFile(path)
	if err != nil {
		return err
	}
	if _, err := cfg.Get(name); err != nil {
		return err
	}
	if cfg.Active == name {
		return nil
	}
	cfg.Active = name
	return cfg.Save(path)
}

// onboardingSetting is a KindAction: a verb, but one that belongs on the
// Profile section rather than in the palette, per docs/SETTINGS.md. Run
// reaches onboarding.run by ID rather than by importing the package that
// registers it, the same way a command's own Run never imports another
// command's package.
func onboardingSetting() kernel.Setting {
	return kernel.Setting{
		ID:      "session.onboarding",
		Section: sessionSection,
		Order:   2,
		Title:   "Set up a profile again",
		Summary: "re-runs the questions onboarding asks",
		Kind:    kernel.KindAction,
		Scope:   kernel.ScopeSession,
		Run:     func(kernel.Deps) tea.Cmd { return kernel.RunCommand("onboarding.run") },
	}
}
