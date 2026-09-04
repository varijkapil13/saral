package settings

import (
	"strings"
	"testing"

	"github.com/varijkapil13/saral/internal/config"
	"github.com/varijkapil13/saral/internal/ui/kernel"
)

// useConfig points config.Path at a fresh directory for one test, writes cfg
// there if it is not the zero value, and never touches the shared TestMain
// directory the rest of this package's tests rely on being empty.
func useConfig(t *testing.T, cfg *config.Config) string {
	t.Helper()
	dir := t.TempDir()
	t.Setenv("SARAL_CONFIG_DIR", dir)
	if cfg == nil {
		return dir
	}
	path, err := config.Path()
	if err != nil {
		t.Fatal(err)
	}
	if err := cfg.Save(path); err != nil {
		t.Fatal(err)
	}
	return dir
}

func TestReadProfile_NoConfigFileAnswersNotSetUp(t *testing.T) {
	useConfig(t, nil)
	p := readProfile()
	if p.value() != "not set up yet" {
		t.Errorf("Value() = %q, want the not-set-up sentence", p.value())
	}
	if p.multi() {
		t.Error("a config with no file at all reports more than one profile")
	}
}

func TestReadProfile_OneProfileNamesItSiteAccountAndTokenSource(t *testing.T) {
	useConfig(t, &config.Config{
		Active: "work",
		Profiles: map[string]config.Profile{
			"work": {Site: "example.atlassian.net", Email: "me@example.com", Token: config.TokenSource{Env: "JIRA_TOKEN"}},
		},
	})
	p := readProfile()
	want := "work · example.atlassian.net · me@example.com · token from environment variable JIRA_TOKEN"
	if got := p.value(); got != want {
		t.Errorf("Value() = %q, want %q", got, want)
	}
	if p.multi() {
		t.Error("one profile reports as more than one")
	}
	if strings.Contains(p.value(), "JIRA_TOKEN=") {
		t.Error("the value string leaked something that looks like a token assignment")
	}
}

func TestReadProfile_MultipleProfilesReportsMulti(t *testing.T) {
	useConfig(t, &config.Config{
		Active: "work",
		Profiles: map[string]config.Profile{
			"work": {Site: "example.atlassian.net", Email: "me@example.com", Token: config.TokenSource{Env: "JIRA_TOKEN"}},
			"home": {Site: "home.atlassian.net", Email: "me@home.com", Token: config.TokenSource{Env: "JIRA_TOKEN_HOME"}},
		},
	})
	p := readProfile()
	if !p.multi() {
		t.Fatal("two profiles did not report as multi")
	}
	if len(p.names) != 2 {
		t.Errorf("names = %v, want both profiles", p.names)
	}
}

func TestOpenProfile_OneProfileRepeatsItsValueRatherThanOpeningAPicker(t *testing.T) {
	useConfig(t, &config.Config{
		Active:   "work",
		Profiles: map[string]config.Profile{"work": {Site: "example.atlassian.net", Email: "me@example.com", Token: config.TokenSource{Env: "JIRA_TOKEN"}}},
	})
	d := settingsDeps(defaultTheme())
	p := fly(t, d, []kernel.Setting{profileSetting()}, []string{sessionSection}, 100, 20)
	p.press("enter")
	if len(p.pushed()) != 0 {
		t.Fatalf("a single profile opened a picker: %v", p.pushed())
	}
	if len(p.statuses()) == 0 || !strings.Contains(p.statuses()[0], "work") {
		t.Errorf("enter on a single profile did not name it in the status line: %v", p.statuses())
	}
}

func TestOpenProfile_MultipleProfilesOpenAPickerThatWritesActive(t *testing.T) {
	useConfig(t, &config.Config{
		Active: "work",
		Profiles: map[string]config.Profile{
			"work": {Site: "example.atlassian.net", Email: "me@example.com", Token: config.TokenSource{Env: "JIRA_TOKEN"}},
			"home": {Site: "home.atlassian.net", Email: "me@home.com", Token: config.TokenSource{Env: "JIRA_TOKEN_HOME"}},
		},
	})
	d := settingsDeps(defaultTheme())
	p := fly(t, d, []kernel.Setting{profileSetting()}, []string{sessionSection}, 100, 20)
	p.press("enter")
	pushed := p.pushed()
	if len(pushed) != 1 || !strings.HasPrefix(pushed[0], pickerViewID+":") {
		t.Fatalf("more than one profile did not open the generic picker: %v", pushed)
	}
}

func TestSetActiveProfile_WritesActiveAndSaysItTakesEffectNextRun(t *testing.T) {
	useConfig(t, &config.Config{
		Active: "work",
		Profiles: map[string]config.Profile{
			"work": {Site: "example.atlassian.net", Email: "me@example.com", Token: config.TokenSource{Env: "JIRA_TOKEN"}},
			"home": {Site: "home.atlassian.net", Email: "me@home.com", Token: config.TokenSource{Env: "JIRA_TOKEN_HOME"}},
		},
	})
	cmd := setActiveProfile(kernel.Deps{}, "home")
	msg, ok := cmd().(kernel.StatusMsg)
	if !ok {
		t.Fatalf("setActiveProfile did not answer with a StatusMsg: %#v", cmd())
	}
	if !strings.Contains(msg.Text, "home") || !strings.Contains(msg.Text, "next time") {
		t.Errorf("status text is %q, want it to name home and say it takes effect next run", msg.Text)
	}
	path, err := config.Path()
	if err != nil {
		t.Fatal(err)
	}
	cfg, err := config.LoadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Active != "home" {
		t.Errorf("active is %q after the switch, want home", cfg.Active)
	}
}

func TestSetActiveProfile_ReportsAFailureRatherThanPanicking(t *testing.T) {
	useConfig(t, nil)
	cmd := setActiveProfile(kernel.Deps{}, "nobody")
	msg, ok := cmd().(kernel.StatusMsg)
	if !ok || msg.Level != kernel.LevelWarn {
		t.Fatalf("switching to a profile with no config file did not warn: %#v", cmd())
	}
}

func TestOnboardingSetting_AsksTheKernelToRunOnboardingRunByID(t *testing.T) {
	t.Parallel()
	cmd := onboardingSetting().Run(kernel.Deps{})
	if cmd == nil {
		t.Fatal("Run returned a nil command")
	}
	msg, ok := cmd().(kernel.RunCommandMsg)
	if !ok || msg.ID != "onboarding.run" {
		t.Fatalf("Run did not ask the kernel to run onboarding.run: %#v", cmd())
	}
}
