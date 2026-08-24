package onboarding

import (
	"errors"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/zalando/go-keyring"

	"github.com/varijkapil13/saral/internal/config"
	"github.com/varijkapil13/saral/internal/ui/kernel"
	"github.com/varijkapil13/saral/pkg/jira"
	"github.com/varijkapil13/saral/pkg/jira/jiratest"
)

// TestFlow_WritesTheProfileOnlyAfterEverythingHasBeenChecked is the happy path,
// and the assertion that matters is where the write happens: at the end, once.
func TestFlow_WritesTheProfileOnlyAfterEverythingHasBeenChecked(t *testing.T) {
	keyring.MockInit()
	t.Cleanup(keyring.MockInit)

	fake := testFake()
	d := newDriver(t, fake)

	d.credentials()
	d.atStep(stepStorage)
	d.nothingWritten()

	d.press("enter")
	d.atStep(stepProject)
	d.typeIn("PROJ")
	d.press("enter")

	d.atStep(stepReview)
	d.mustContain("What this token can do in PROJ")
	d.nothingWritten()

	d.press("enter")
	d.atStep(stepDone)

	written := d.written()
	for _, want := range []string{
		`active = "example"`,
		`site    = "example.atlassian.net"`,
		`email   = "you@example.com"`,
		`project = "PROJ"`,
		`token   = { keychain = "saral:example" }`,
	} {
		if !strings.Contains(written, want) {
			t.Errorf("the config does not hold %q:\n%s", want, written)
		}
	}
	d.noTokenAnywhere()

	stored, err := keyring.Get("saral", "example")
	if err != nil {
		t.Fatalf("reading the keychain back: %v", err)
	}
	if stored != testToken {
		t.Errorf("the keychain holds %q, want the token that was verified", stored)
	}

	cfg, err := config.LoadFile(d.path)
	if err != nil {
		t.Fatalf("loading back what onboarding wrote: %v", err)
	}
	profile, err := cfg.Current()
	if err != nil {
		t.Fatalf("the written config has no usable profile: %v", err)
	}
	if profile.Project != "PROJ" {
		t.Errorf("the profile remembers project %q, want PROJ", profile.Project)
	}
	if got, _ := profile.ResolveToken(t.Context()); got != testToken {
		t.Errorf("the written profile resolves to %q, so the next start would fail", got)
	}
}

func TestFlow_ARejectedTokenReturnsToTheTokenFieldWithEverythingStillThere(t *testing.T) {
	t.Parallel()

	fake := testFake()
	fake.FailNext(&jira.AuthError{Reason: "the token is not valid for you@example.com"})
	d := newDriver(t, fake)

	d.credentials()

	d.atStep(stepToken)
	d.mustContain("the token is not valid for you@example.com")
	if got := d.model().value(fieldSite); got != testSite {
		t.Errorf("the site was lost: %q", got)
	}
	if got := d.model().value(fieldEmail); got != testEmail {
		t.Errorf("the email was lost: %q", got)
	}
	if got := d.model().input[fieldToken].Value(); got != testToken {
		t.Error("the token field was emptied by the refusal")
	}
	d.nothingWritten()
	d.noTokenAnywhere()

	// The second attempt goes through, which is what makes the first one a
	// refusal rather than a dead end.
	d.press("enter")
	d.atStep(stepStorage)
}

func TestFlow_AnUnreachableSiteReturnsToTheSiteFieldAndSaysWhichProblemItIs(t *testing.T) {
	t.Parallel()

	fake := testFake()
	fake.FailNext(&jira.TransportError{
		Op:  "GET /rest/api/3/myself",
		Err: errors.New("dial tcp: lookup exmaple.atlassian.net: no such host"),
	})
	d := newDriver(t, fake)

	d.credentials()

	d.atStep(stepSite)
	d.mustContain("no such host")
	d.mustContain("Nothing was written")
	if got := d.model().input[fieldToken].Value(); got != testToken {
		t.Error("going back to the site field emptied the token")
	}
	d.nothingWritten()
	d.noTokenAnywhere()
}

// The whole point of asking who the token belongs to is that an answer proves
// the three fields go together. An answer that names nobody proves nothing, so
// it has to stop the flow rather than reach the review screen as an account with
// no name.
func TestFlow_AnAnswerThatNamesNobodyIsNotProofTheCredentialsGoTogether(t *testing.T) {
	t.Parallel()

	d := newDriver(t, testFake(jiratest.WithMe(jira.User{})))

	d.credentials()

	d.atStep(stepSite)
	d.mustContain("names no account")
	if strings.Contains(d.frame(), "verified") {
		t.Errorf("an answer naming nobody read as a verified credential:\n%s", d.frame())
	}
	d.nothingWritten()
	d.noTokenAnywhere()
}

func TestFlow_AConnectorThatCannotBuildAClientIsReportedOnTheTokenStep(t *testing.T) {
	t.Parallel()

	d := newDriverWith(t, func(string, string, string) (jira.SessionClient, error) {
		return nil, errors.New("cloud: an API token is required")
	})
	d.credentials()

	d.atStep(stepToken)
	d.mustContain("an API token is required")
}

func TestFlow_AProjectTheTokenCannotSeeIsRefusedWithoutLeavingTheStep(t *testing.T) {
	t.Parallel()

	d := newDriver(t, testFake())
	d.credentials()
	d.press("enter")

	d.typeIn("NOPE")
	d.press("enter")

	d.atStep(stepProject)
	d.mustContain("does not exist, or you cannot see it")
	d.nothingWritten()

	d.clearField()
	d.typeIn("PROJ")
	d.press("enter")
	d.atStep(stepReview)
}

func TestFlow_NoProjectIsAnAnswerAndTheProbeSaysWhatItCostsYou(t *testing.T) {
	t.Parallel()

	d := newDriver(t, testFake())
	d.credentials()
	d.press("enter", "enter")

	d.atStep(stepReview)
	d.mustContain("What this token can do on this site")
	d.mustContain("no project is selected, so per-project permissions are unknown")
}

func TestFlow_TheProbeReasonsAreShownInTheProbesOwnWords(t *testing.T) {
	t.Parallel()

	fake := testFake(jiratest.WithCapabilities(jiratest.NoBulkMove, jiratest.NoPlans))
	d := newDriver(t, fake)
	d.credentials()
	d.press("enter")
	d.typeIn("PROJ")
	d.press("enter")

	d.atStep(stepReview)
	d.mustContain("needs Bulk Change permission")
	d.mustContain("the Plans API needs Administer Jira")
}

func TestFlow_AKeychainThatRefusesTheWriteLeavesNothingBehind(t *testing.T) {
	keyring.MockInitWithError(errors.New("the keychain is locked"))
	t.Cleanup(keyring.MockInit)

	d := newDriver(t, testFake())
	d.credentials()
	d.press("enter")
	d.typeIn("PROJ")
	d.press("enter")
	d.press("enter")

	d.atStep(stepReview)
	d.mustContain("the keychain is locked")
	d.nothingWritten()
	d.noTokenAnywhere()
}

// TestFlow_AnEnvironmentSourceIsReportedWhenTheVariableIsNotSet is the other
// half of "saved and then broken on every screen": the file is right, and the
// place it points at is empty.
func TestFlow_AnEnvironmentSourceIsReportedWhenTheVariableIsNotSet(t *testing.T) {
	d := newDriver(t, testFake())
	d.credentials()
	d.press("down")
	d.clearField()
	d.typeIn("SARAL_TEST_TOKEN_UNSET")
	d.press("enter")
	d.typeIn("PROJ")
	d.press("enter")
	d.press("enter")

	d.atStep(stepDone)
	d.mustContain("SARAL_TEST_TOKEN_UNSET is empty or not set")
	if !strings.Contains(d.written(), `token   = { env = "SARAL_TEST_TOKEN_UNSET" }`) {
		t.Errorf("the config does not name the environment variable:\n%s", d.written())
	}
	d.noTokenAnywhere()
}

func TestFlow_AnEnvironmentSourceThatResolvesIsNotComplainedAbout(t *testing.T) {
	t.Setenv("SARAL_TEST_TOKEN", testToken)

	d := newDriver(t, testFake())
	d.credentials()
	d.press("down")
	d.clearField()
	d.typeIn("SARAL_TEST_TOKEN")
	d.press("enter")
	d.press("enter")
	d.press("enter")

	d.atStep(stepDone)
	if got := d.model().problem; got != "" {
		t.Errorf("a working environment source was reported as a problem: %q", got)
	}
	d.noTokenAnywhere()
}

func TestStorage_EverySourceIsOfferedAndValidatedInItsOwnWords(t *testing.T) {
	t.Parallel()

	tests := map[string]struct {
		down  int
		value string
		want  string
	}{
		"an unnamed keychain entry": {down: 0, value: "", want: "name the keychain entry"},
		"a variable with a space":   {down: 1, value: "JIRA TOKEN", want: "name the environment variable"},
		"a pipeline as a command":   {down: 2, value: "pass jira | head -1", want: "never run through a shell"},
		"an empty command":          {down: 2, value: "", want: "name the command"},
	}

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			d := newDriver(t, testFake())
			d.credentials()
			for range tc.down {
				d.press("down")
			}
			d.clearField()
			d.typeIn(tc.value)
			d.press("enter")

			d.atStep(stepStorage)
			d.mustContain(tc.want)
		})
	}
}

func TestStorage_SwitchingSourceKeepsWhatWasTypedForEachOne(t *testing.T) {
	t.Parallel()

	d := newDriver(t, testFake())
	d.credentials()
	d.atStep(stepStorage)

	d.clearField()
	d.typeIn("saral:mine")
	d.press("down")
	if got := d.model().input[fieldSecret].Value(); got != "JIRA_TOKEN" {
		t.Errorf("the environment source starts at %q, want the usual default", got)
	}
	d.press("up")
	if got := d.model().input[fieldSecret].Value(); got != "saral:mine" {
		t.Errorf("switching back lost the entry name: %q", got)
	}
}

func TestProjects_AreOfferedFromTheAccountsOwnRecentWorkFirst(t *testing.T) {
	t.Parallel()

	fake := jiratest.New(
		jiratest.WithProject("PROJ", jiratest.Scrum),
		jiratest.WithIssues(jiratest.Gen(6)),
		jiratest.WithIssues(jiratest.GenFor("OTHER", 3)),
		jiratest.WithMe(jira.User{AccountID: "acct-ada", DisplayName: "Ada Lovelace", TimeZone: time.UTC, Active: true}),
	)
	d := newDriver(t, fake)
	d.credentials()
	d.press("enter")

	d.atStep(stepProject)
	if got := d.model().suggested; len(got) == 0 {
		t.Fatal("no project was suggested from the account's own issues")
	}
	d.mustContain("Recently worked in")

	d.press("down")
	if got := d.model().value(fieldProject); got == "" {
		t.Error("choosing a suggestion did not fill the field")
	}
}

func TestProjects_FallBackToWhatTheAccountCanSeeWhenNothingIsAssignedToIt(t *testing.T) {
	t.Parallel()

	d := newDriver(t, testFake())
	d.credentials()
	d.press("enter")

	if got := d.model().suggested; len(got) != 1 || got[0] != "PROJ" {
		t.Errorf("suggestions are %v, want the one project the account can see", got)
	}
}

func TestProjects_AFailedLookupIsANoteAndNotARefusal(t *testing.T) {
	t.Parallel()

	d := newDriver(t, refusesToSearch{
		Fake: testFake(),
		err:  &jira.RateLimitError{RetryAfter: 30 * time.Second, Endpoint: "/search/jql"},
	})
	d.credentials()

	d.atStep(stepStorage)
	d.press("enter")
	d.atStep(stepProject)
	d.mustContain("rate limited by Jira")

	d.typeIn("PROJ")
	d.press("enter")
	d.atStep(stepReview)
}

func TestBack_KeepsEverythingThatWasTyped(t *testing.T) {
	t.Parallel()

	d := newDriver(t, testFake())
	d.credentials()
	d.atStep(stepStorage)

	d.press("shift+tab")
	d.atStep(stepToken)
	if got := d.model().input[fieldToken].Value(); got != testToken {
		t.Error("going back emptied the token field")
	}
	d.press("shift+tab", "shift+tab")
	d.atStep(stepSite)
	if got := d.model().value(fieldSite); got != testSite {
		t.Errorf("going back to the start lost the site: %q", got)
	}
	d.press("shift+tab")
	d.atStep(stepSite)
}

func TestSite_AcceptsWhatPeopleActuallyPasteAndRefusesWhatCannotBeASite(t *testing.T) {
	t.Parallel()

	tests := map[string]struct {
		typed string
		want  string
		stay  bool
	}{
		"a pasted url":       {typed: "https://example.atlassian.net/jira/software", want: "", stay: true},
		"a bare host":        {typed: "Example.Atlassian.NET", want: ""},
		"an empty site":      {typed: "", want: "site is required", stay: true},
		"an insecure scheme": {typed: "http://example.atlassian.net", want: "must be reached over https", stay: true},
	}

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			d := newDriver(t, testFake())
			d.typeIn(tc.typed)
			d.press("enter")

			if tc.want != "" {
				d.atStep(stepSite)
				d.mustContain(tc.want)
				return
			}
			d.atStep(stepEmail)
			if got := d.model().value(fieldSite); got != testSite {
				t.Errorf("the site was normalised to %q, want %q", got, testSite)
			}
		})
	}
}

func TestEmail_MustBeAnAddressBecauseJiraPairsItWithTheToken(t *testing.T) {
	t.Parallel()

	d := newDriver(t, testFake())
	d.typeIn(testSite)
	d.press("enter")
	d.typeIn("you")
	d.press("enter")

	d.atStep(stepEmail)
	d.mustContain("pairs the account email with the API token")
}

func TestBlocksClose_RefusesToThrowAwayAHalfFinishedSetup(t *testing.T) {
	t.Parallel()

	d := newDriver(t, testFake())
	if _, blocked := d.model().BlocksClose(); blocked {
		t.Error("an untouched form blocks quitting")
	}

	d.typeIn(testSite)
	reason, blocked := d.model().BlocksClose()
	if !blocked {
		t.Fatal("a form with something typed in it does not block quitting")
	}
	if !strings.Contains(reason, "ctrl+c") {
		t.Errorf("the reason %q does not say how to leave anyway", reason)
	}
}

func TestBlocksClose_StopsBlockingOnceTheProfileIsWritten(t *testing.T) {
	keyring.MockInit()
	t.Cleanup(keyring.MockInit)

	d := newDriver(t, testFake())
	d.credentials()
	d.press("enter")
	d.typeIn("PROJ")
	d.press("enter", "enter")

	d.atStep(stepDone)
	if _, blocked := d.model().BlocksClose(); blocked {
		t.Error("a finished setup still blocks quitting")
	}
	d.press("enter")
	if !d.quit {
		t.Error("enter on the summary did not leave, so nothing tells the user to restart")
	}
}

func TestSave_RefusesToWriteOverAConfigFileItCannotRead(t *testing.T) {
	t.Parallel()

	d := newDriver(t, testFake())
	d.send(configLoadedMsg{path: d.path, err: errors.New("unknown key profiles.work.colour")})

	d.credentials()
	d.press("enter")
	d.typeIn("PROJ")
	d.press("enter", "enter")

	d.atStep(stepReview)
	d.mustContain("refusing to write over a config file")
	d.nothingWritten()
}

func TestSave_AddsToTheProfilesThatAreAlreadyThereAndBecomesTheActiveOne(t *testing.T) {
	keyring.MockInit()
	t.Cleanup(keyring.MockInit)

	d := newDriver(t, testFake())
	d.send(configLoadedMsg{path: d.path, cfg: config.Config{
		Active: "work",
		Mouse:  true,
		Profiles: map[string]config.Profile{"work": {
			Name:  "work",
			Site:  "other.atlassian.net",
			Email: "you@example.com",
			Token: config.TokenSource{Env: "JIRA_TOKEN"},
		}},
	}})

	d.credentials()
	d.press("enter")
	d.typeIn("PROJ")
	d.press("enter", "enter")

	d.atStep(stepDone)
	cfg, err := config.LoadFile(d.path)
	if err != nil {
		t.Fatalf("loading back: %v", err)
	}
	if len(cfg.Profiles) != 2 {
		t.Errorf("the file holds %d profiles, want the old one and the new one", len(cfg.Profiles))
	}
	if cfg.Active != "example" {
		t.Errorf("active is %q, want the profile that was just set up", cfg.Active)
	}
}

func TestSave_NamesASecondProfileForTheSameSiteWithoutOverwritingTheFirst(t *testing.T) {
	t.Parallel()

	d := newDriver(t, testFake())
	d.send(configLoadedMsg{path: d.path, cfg: config.Config{
		Mouse:    true,
		Profiles: map[string]config.Profile{"example": {Name: "example", Site: testSite, Email: "old@example.com", Token: config.TokenSource{Env: "JIRA_TOKEN"}}},
	}})
	d.credentials()

	d.atStep(stepStorage)
	if got := d.model().profileName(); got != "example-2" {
		t.Errorf("the new profile is called %q, which would replace the one already there", got)
	}
}

func TestFocus_CancelsWhatIsInTheAirWhenTheViewStopsBeingLookedAt(t *testing.T) {
	t.Parallel()

	fake := testFake()
	fake.Delay(time.Hour)
	d := newDriver(t, testFake())

	d.typeIn(testSite)
	d.press("enter")
	d.typeIn(testEmail)
	d.press("enter")
	d.typeIn(testToken)

	m := d.model()
	m.client = fake
	m.busy, m.seq = busyProbe, 7
	cancelled := false
	m.cancel = func() { cancelled = true }
	m.stop()

	if !cancelled {
		t.Error("the in-flight call was not cancelled")
	}
	if m.busy != busyNone {
		t.Error("the view still thinks something is in the air")
	}
}

func TestRefresh_RunsTheLastThingThatFailedAgain(t *testing.T) {
	t.Parallel()

	fake := testFake()
	fake.FailNext(&jira.TransportError{Op: "GET /rest/api/3/myself", Err: errors.New("connection reset by peer")})
	d := newDriver(t, fake)
	d.credentials()

	d.atStep(stepSite)
	d.send(kernel.RefreshMsg{})
	d.atStep(stepStorage)
}

func TestTheme_IsRebuiltWithoutLosingAnythingTyped(t *testing.T) {
	t.Parallel()

	d := newDriver(t, testFake())
	d.typeIn(testSite)
	d.send(kernel.ThemeMsg{Theme: kernel.NewTheme(kernel.ThemeDark, true, kernel.UnicodeGlyphs())})

	if got := d.model().value(fieldSite); got != testSite {
		t.Errorf("a theme change lost the site: %q", got)
	}
	if !strings.Contains(d.frame(), testSite) {
		t.Errorf("the retheme dropped the field:\n%s", d.frame())
	}
}

func TestPaste_ReachesTheField(t *testing.T) {
	t.Parallel()

	d := newDriver(t, testFake())
	d.send(tea.PasteMsg{Content: testSite})
	if got := d.model().value(fieldSite); got != testSite {
		t.Errorf("a pasted site landed as %q", got)
	}
}

func TestInit_ReadsTheConfigDirectoryFromTheEnvironment(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("SARAL_CONFIG_DIR", dir)

	msg, ok := loadConfig().(configLoadedMsg)
	if !ok {
		t.Fatalf("loadConfig returned a %T", msg)
	}
	if msg.err != nil {
		t.Errorf("a first run reported an error: %v", msg.err)
	}
	if !strings.HasPrefix(msg.path, dir) {
		t.Errorf("the config path is %q, which is not under %q", msg.path, dir)
	}
	if !msg.cfg.Mouse {
		t.Error("a first run turned the mouse off")
	}
}
