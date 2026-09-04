package kernel

import (
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"

	"github.com/varijkapil13/saral/internal/config"
)

// registerAppearanceSettings puts the same four settings init() registers into
// a freshly reset registry, so a test sees exactly what a running program
// would without depending on whether an earlier test in this package already
// reset the real one.
func registerAppearanceSettings() {
	RegisterSetting(themeSetting())
	RegisterSetting(glyphsSetting())
	RegisterSetting(schemeSetting())
	RegisterSetting(mouseSetting())
}

// TestSettings_EveryOneAnswersValueWithAnOptionThatExists is the gate
// docs/SETTINGS.md asks for: every registered setting's current value is one
// of the values it offers, for a session with no theme at all, a themed one,
// and one where NO_COLOR is set. Settings() scanning zero settings fails this
// on its own, so a registration that silently did not happen cannot pass by
// doing nothing.
func TestSettings_EveryOneAnswersValueWithAnOptionThatExists(t *testing.T) {
	resetRegistry()
	t.Cleanup(resetRegistry)
	registerAppearanceSettings()

	settings := Settings()
	if len(settings) == 0 {
		t.Fatal("Settings scanned zero settings; a registration failure would pass this test by doing nothing")
	}

	t.Run("no theme at all", func(t *testing.T) {
		checkSettingsAnswerTheirOwnOptions(t, settings, Deps{})
	})
	t.Run("a themed session", func(t *testing.T) {
		colourfulEnv(t)
		checkSettingsAnswerTheirOwnOptions(t, settings, Deps{
			Theme: NewTheme(ThemeDark, true, ASCIIGlyphs(), WithScheme(nordScheme)),
			Site:  "example.atlassian.net",
		})
	})
	t.Run("NO_COLOR is set", func(t *testing.T) {
		t.Setenv("NO_COLOR", "1")
		checkSettingsAnswerTheirOwnOptions(t, settings, Deps{
			Theme: NewTheme(ThemeNoColor, true, UnicodeGlyphs()),
		})
	})
}

func checkSettingsAnswerTheirOwnOptions(t *testing.T, settings []Setting, d Deps) {
	t.Helper()
	for i := range settings {
		s := &settings[i]
		if s.Kind != KindChoice && s.Kind != KindToggle {
			continue
		}
		value := s.Value(d)
		opts := s.Options(d)
		offered := make([]string, 0, len(opts))
		found := false
		for _, o := range opts {
			offered = append(offered, o.ID)
			if o.ID == value {
				found = true
			}
		}
		if !found {
			t.Errorf("%s: Value is %q, which Options does not offer: %v", s.ID, value, offered)
		}
	}
}

func TestSettings_OrderedBySectionThenOrderThenTitle(t *testing.T) {
	resetRegistry()
	t.Cleanup(resetRegistry)
	registerAppearanceSettings()

	var ids []string
	for _, s := range Settings() {
		if s.Section == appearanceSection {
			ids = append(ids, s.ID)
		}
	}
	want := []string{"appearance.theme", "appearance.scheme", "appearance.glyphs", "appearance.mouse"}
	if strings.Join(ids, ",") != strings.Join(want, ",") {
		t.Errorf("got %v, want %v", ids, want)
	}
}

func TestRegisterSetting_RecordsBadRegistrationsInsteadOfPanicking(t *testing.T) {
	choice := func(id string) Setting {
		return Setting{
			ID: id, Section: "Test", Title: "T", Kind: KindChoice,
			Options: func(Deps) []SettingOption { return nil },
			Value:   func(Deps) string { return "" },
			Set:     func(Deps, string) tea.Cmd { return nil },
		}
	}
	for name, tc := range map[string]struct {
		register func()
		want     string
	}{
		"no ID": {
			func() {
				RegisterSetting(Setting{Section: "Test", Title: "T", Kind: KindInfo, Value: func(Deps) string { return "" }})
			},
			"no ID",
		},
		"no section": {
			func() {
				RegisterSetting(Setting{ID: "x", Title: "T", Kind: KindInfo, Value: func(Deps) string { return "" }})
			},
			"no section",
		},
		"no title": {
			func() {
				RegisterSetting(Setting{ID: "x", Section: "Test", Kind: KindInfo, Value: func(Deps) string { return "" }})
			},
			"no title",
		},
		"choice with nothing to answer with": {
			func() { RegisterSetting(Setting{ID: "x", Section: "Test", Title: "T", Kind: KindChoice}) },
			"Options, Value or Set",
		},
		"info with no value": {
			func() { RegisterSetting(Setting{ID: "x", Section: "Test", Title: "T", Kind: KindInfo}) },
			"no Value",
		},
		"action with no run": {
			func() { RegisterSetting(Setting{ID: "x", Section: "Test", Title: "T", Kind: KindAction}) },
			"no Run",
		},
		"unknown kind": {
			func() { RegisterSetting(Setting{ID: "x", Section: "Test", Title: "T", Kind: SettingKind(99)}) },
			"unknown kind",
		},
		"duplicate ID": {
			func() { RegisterSetting(choice("x")); RegisterSetting(choice("x")) },
			"registered twice",
		},
	} {
		t.Run(name, func(t *testing.T) {
			resetSettings()
			t.Cleanup(resetSettings)
			tc.register()
			errs := RegistrationErrors()
			if len(errs) == 0 {
				t.Fatal("expected a recorded error")
			}
			if !strings.Contains(errs[len(errs)-1].Error(), tc.want) {
				t.Errorf("error %q does not mention %q", errs[len(errs)-1], tc.want)
			}
		})
	}
}

func TestResetSettings_LeavesViewsCommandsAndKeysAlone(t *testing.T) {
	resetRegistry()
	t.Cleanup(resetRegistry)
	RegisterView(spec("board", 1, "", &stubView{id: "board"}))
	RegisterCommand(Command{ID: "x", Title: "X", Run: func(Deps) tea.Cmd { return nil }})
	RegisterSetting(mouseSetting())

	resetSettings()

	if len(Settings()) != 0 {
		t.Error("resetSettings left a setting behind")
	}
	if _, ok := LookupView("board"); !ok {
		t.Error("resetSettings threw away a registered view")
	}
	if _, ok := LookupCommand("x"); !ok {
		t.Error("resetSettings threw away a registered command")
	}
}

// TestSwitchTheme_KeepsTheColourSchemeWhenTheModeChanges is the regression for
// the bug docs/SETTINGS.md names: SwitchTheme built NewTheme with no
// WithScheme, so choosing a mode silently reverted a Nord session to the
// default colours while the profile still said scheme = "nord".
func TestSwitchTheme_KeepsTheColourSchemeWhenTheModeChanges(t *testing.T) {
	colourfulEnv(t)
	writeConfig(t, profileWithEverything)

	d := Deps{Theme: NewTheme(ThemeDark, true, UnicodeGlyphs(), WithScheme(nordScheme)), Site: "example.atlassian.net"}
	msg := firstMsgOfType[ThemeMsg](t, SwitchTheme(d, ThemeLight))
	if msg.Theme.Scheme.ID() != "nord" {
		t.Errorf("switching mode changed the scheme to %q, want nord", msg.Theme.Scheme.ID())
	}
	if msg.Theme.Mode != ThemeLight {
		t.Errorf("got mode %v, want light", msg.Theme.Mode)
	}
}

// TestSchemeSetting_UnavailableWhenColourIsOff is the regression for the
// second bug: NewTheme ignores the scheme entirely under ThemeNoColor, so the
// row has to say colour is off rather than silently accepting a choice that
// changes nothing on screen.
func TestSchemeSetting_UnavailableWhenColourIsOff(t *testing.T) {
	s := schemeSetting()
	d := Deps{Theme: NewTheme(ThemeNoColor, true, UnicodeGlyphs())}
	if reason := s.Unavailable(d); !strings.Contains(reason, "colour is off") {
		t.Errorf("Unavailable with colour off is %q, does not say colour is off", reason)
	}
	if reason := s.Unavailable(Deps{Theme: NewTheme(ThemeDark, true, UnicodeGlyphs())}); reason != "" {
		t.Errorf("a coloured theme is unavailable: %q", reason)
	}
}

// TestSchemeSetting_SetDoesNotWriteWhenColourIsOff is the other half: picking
// a scheme with colour off must not write the profile, because the choice
// cannot take effect and the file would then disagree with the screen for no
// reason a user could see.
func TestSchemeSetting_SetDoesNotWriteWhenColourIsOff(t *testing.T) {
	path := writeConfig(t, profileWithEverything)
	s := schemeSetting()
	d := Deps{Theme: NewTheme(ThemeNoColor, true, UnicodeGlyphs()), Site: "example.atlassian.net"}

	cmd := s.Set(d, "nord")
	if _, ok := firstOfType[ThemeMsg](collect(cmd)); ok {
		t.Error("Set rebuilt the theme even though colour is off")
	}
	status, ok := firstOfType[StatusMsg](collect(cmd))
	if !ok || !strings.Contains(status.Text, "colour is off") {
		t.Errorf("Set did not say why it refused: %+v", status)
	}

	cfg, err := config.LoadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	got, err := cfg.Get("work")
	if err != nil {
		t.Fatal(err)
	}
	if got.Scheme != "" {
		t.Errorf("the profile's scheme changed to %q even though colour is off", got.Scheme)
	}
}

// TestThemeSetting_UnavailableReusesNoColorForced holds appearance.theme's
// Unavailable to the exact sentence noColorForced already computes, rather
// than a second copy of it drifting out of step.
func TestThemeSetting_UnavailableReusesNoColorForced(t *testing.T) {
	t.Setenv("NO_COLOR", "1")
	_, want := noColorForced()

	s := themeSetting()
	if got := s.Unavailable(Deps{}); got != want {
		t.Errorf("Unavailable is %q, want the exact sentence noColorForced computes: %q", got, want)
	}
}

func TestGlyphsSetting_ValueReadsWhetherTheThemeIsASCII(t *testing.T) {
	s := glyphsSetting()
	if got := s.Value(Deps{Theme: NewTheme(ThemeDark, true, ASCIIGlyphs())}); got != "ascii" {
		t.Errorf("got %q for an ASCII theme, want ascii", got)
	}
	if got := s.Value(Deps{Theme: NewTheme(ThemeDark, true, UnicodeGlyphs())}); got != "unicode" {
		t.Errorf("got %q for a unicode theme, want unicode", got)
	}
	if got := s.Value(Deps{}); got != "unicode" {
		t.Errorf("got %q with no theme at all, want the default unicode", got)
	}
}

func TestSwitchGlyphs_KeepsModeAndScheme(t *testing.T) {
	colourfulEnv(t)
	writeConfig(t, profileWithEverything)

	d := Deps{Theme: NewTheme(ThemeLight, false, UnicodeGlyphs(), WithScheme(nordScheme)), Site: "example.atlassian.net"}
	msg := firstMsgOfType[ThemeMsg](t, SwitchGlyphs(d, true))
	switch {
	case !msg.Theme.Glyphs.IsASCII():
		t.Error("the glyph set was not switched to ascii")
	case msg.Theme.Mode != ThemeLight:
		t.Errorf("got mode %v, want light", msg.Theme.Mode)
	case msg.Theme.Scheme.ID() != "nord":
		t.Errorf("got scheme %q, want nord", msg.Theme.Scheme.ID())
	}
}

func TestWriteGlyphs_ChangesGlyphsAndNothingElseInTheProfile(t *testing.T) {
	path := writeConfig(t, strings.Replace(profileWithEverything, `glyphs = "ascii"`, `theme = "dark"`, 1))

	if err := writeGlyphs("example.atlassian.net", ASCIIGlyphs()); err != nil {
		t.Fatalf("writeGlyphs: %v", err)
	}
	cfg, err := config.LoadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	got, err := cfg.Get("work")
	if err != nil {
		t.Fatal(err)
	}
	switch {
	case got.Glyphs != "ascii":
		t.Errorf("glyphs is %q, want ascii", got.Glyphs)
	case got.Theme != "dark":
		t.Errorf("the theme changed to %q", got.Theme)
	case len(got.Queries) != 1 || got.Queries[0].Slot != 2:
		t.Errorf("the saved queries did not survive: %+v", got.Queries)
	}
}

func TestMouseSetting_ValueReadsWhetherTheZoneManagerIsEnabled(t *testing.T) {
	s := mouseSetting()
	if got := s.Value(Deps{}); got != "off" {
		t.Errorf("got %q with no zone manager at all, want off", got)
	}
	m := newAt(t, testDeps(), 80, 24, WithMouse(true))
	if got := s.Value(m.deps); got != "on" {
		t.Errorf("got %q for a session with the mouse on, want on", got)
	}
}

func TestSwitchMouse_TurnsTheZoneManagerOffAndPersistsIt(t *testing.T) {
	resetRegistry()
	t.Cleanup(resetRegistry)
	RegisterView(spec("board", 1, "", &stubView{id: "board"}))
	path := writeConfig(t, profileWithEverything)

	m := newAt(t, testDeps(), 80, 24, WithMouse(true))
	if !m.deps.Zones.Enabled() {
		t.Fatal("the session did not start with the mouse on, so this proves nothing")
	}

	next, cmd := m.Update(SetMouseMsg{Enabled: false})
	m = next.(Model)
	if m.deps.Zones.Enabled() {
		t.Error("the zone manager is still enabled after the mouse was turned off")
	}
	if m.mouse {
		t.Error("m.mouse is still true after the mouse was turned off")
	}
	_ = cmd

	if err := writeMouse(false); err != nil {
		t.Fatalf("writeMouse: %v", err)
	}
	cfg, err := config.LoadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Mouse {
		t.Error("mouse is still true in the file")
	}
}

func TestSaveMouse_SaysSoWhenThereIsNoConfigToSaveTo(t *testing.T) {
	t.Setenv("SARAL_CONFIG_DIR", t.TempDir())

	status := firstMsgOfType[StatusMsg](t, saveMouse(false))
	if !strings.Contains(status.Text, "no config file") {
		t.Errorf("unhelpful message with nowhere to save: %q", status.Text)
	}
	if status.Level != LevelInfo {
		t.Errorf("a first run with no config is reported at level %v, which reads as a failure", status.Level)
	}
}

// TestGlobalKeys_SettingsOpensTheSettingsView covers both routes docs/UX.md
// promises: the bare chord from anywhere, and g then s once the prefix has
// latched. A view's own bare "s" — list.Save and sprint.Start both bind one —
// must still reach the view when g was never pressed.
func TestGlobalKeys_SettingsOpensTheSettingsView(t *testing.T) {
	resetRegistry()
	t.Cleanup(resetRegistry)
	RegisterView(spec("board", 1, "", &stubView{id: "board"}))
	RegisterView(ViewSpec{ID: SettingsViewID, Title: "Settings", New: func(Deps) View { return &stubView{id: SettingsViewID} }})

	for name, keys := range map[string][]string{
		"the bare chord": {"ctrl+,"},
		"g then s":       {"g", "s"},
	} {
		t.Run(name, func(t *testing.T) {
			m := newAt(t, testDeps(), 120, 30)
			m, _ = press(m, keys...)
			if got := m.Frame(); !strings.Contains(got, "settings body") {
				t.Errorf("%v did not open the settings view:\n%s", keys, got)
			}
		})
	}
}

func TestGlobalKeys_BareSStillReachesAViewsOwnBinding(t *testing.T) {
	resetRegistry()
	t.Cleanup(resetRegistry)
	board := &stubView{id: "board"}
	RegisterView(spec("board", 1, "", board))

	m := newAt(t, testDeps(), 120, 30)
	m, _ = press(m, "s")
	if !saw(board, "key:s") {
		t.Errorf("a bare s never reached the view: %v", board.seen)
	}
	if strings.Contains(m.Frame(), "settings body") {
		t.Error("a bare s opened settings, which steals the key list.Save and sprint.Start already bind")
	}
}

// TestGlobalKeys_SettingsOpensGracefullyWithNothingRegistered is the crash
// guard: a key that opens a view nothing has registered yet must fall back to
// the kernel's existing "not available" message rather than panic. S2
// registers SettingsViewID; this proves the kernel survives before it does.
func TestGlobalKeys_SettingsOpensGracefullyWithNothingRegistered(t *testing.T) {
	resetRegistry()
	t.Cleanup(resetRegistry)
	RegisterView(spec("board", 1, "", &stubView{id: "board"}))

	m := newAt(t, testDeps(), 120, 30)
	m, _ = press(m, "ctrl+,")
	if !strings.Contains(m.status, "not available") {
		t.Errorf("opening an unregistered settings view did not say so: %q", m.status)
	}
}

// Settings goes over what you were reading, so esc brings that back. It used to
// be opened as a root: a root switch throws the pushed stack away and leaves
// nothing for esc to pop, and the screen claims no footer slot, so a session
// that pressed ctrl+, had no way back to the issue it was on.
func TestGlobalKeys_EscLeavesSettingsForWhatWasUnderIt(t *testing.T) {
	resetRegistry()
	t.Cleanup(resetRegistry)
	RegisterView(spec("board", 1, "", &stubView{id: "board"}))
	RegisterView(ViewSpec{ID: SettingsViewID, Title: "Settings", New: func(Deps) View { return &stubView{id: SettingsViewID} }})

	for name, keys := range map[string][]string{
		"the bare chord": {"ctrl+,"},
		"g then s":       {"g", "s"},
	} {
		t.Run(name, func(t *testing.T) {
			m := newAt(t, testDeps(), 120, 30)
			m, _ = press(m, keys...)
			if got := m.Frame(); !strings.Contains(got, "settings body") {
				t.Fatalf("%v did not open settings:\n%s", keys, got)
			}
			m, _ = press(m, "esc")
			got := m.Frame()
			if strings.Contains(got, "settings body") {
				t.Errorf("esc left settings on screen:\n%s", got)
			}
			if !strings.Contains(got, "board body") {
				t.Errorf("esc did not come back to the view settings was opened over:\n%s", got)
			}
		})
	}
}

// The palette's own row reaches it the same way the key does. A command that
// switched roots while the key pushed would be two answers to one question.
func TestSettingsCommand_GoesOverTheViewItWasRunFrom(t *testing.T) {
	resetRegistry()
	t.Cleanup(resetRegistry)
	RegisterView(spec("board", 1, "", &stubView{id: "board"}))
	RegisterView(ViewSpec{ID: SettingsViewID, Title: "Settings", New: func(Deps) View { return &stubView{id: SettingsViewID} }})
	RegisterCommand(Command{
		ID: "settings.open", Title: "Settings", Group: "Session", Kind: KindSession,
		Run: func(d Deps) tea.Cmd { return Push(SettingsViewID, "Settings", &stubView{id: SettingsViewID}) },
	})

	m := newAt(t, testDeps(), 120, 30)
	next, cmd := m.Update(RunCommandMsg{ID: "settings.open"})
	m = deliver(t, next.(Model), cmd)
	if got := m.Frame(); !strings.Contains(got, "settings body") {
		t.Fatalf("the command did not open settings:\n%s", got)
	}
	m, _ = press(m, "esc")
	if got := m.Frame(); !strings.Contains(got, "board body") {
		t.Errorf("esc after the command did not come back to the board:\n%s", got)
	}
}
