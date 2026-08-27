package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/varijkapil13/saral/internal/app"
	"github.com/varijkapil13/saral/internal/config"
	"github.com/varijkapil13/saral/internal/store"
	"github.com/varijkapil13/saral/internal/ui/kernel"
	"github.com/varijkapil13/saral/internal/ui/list"
	"github.com/varijkapil13/saral/internal/ui/onboarding"
	"github.com/varijkapil13/saral/pkg/jira"
	"github.com/varijkapil13/saral/pkg/jira/jiratest"
)

func TestRun_Version(t *testing.T) {
	for _, args := range [][]string{{"version"}, {"--version"}, {"-version"}} {
		var out, errOut bytes.Buffer
		if err := run(args, &out, &errOut); err != nil {
			t.Fatalf("%v: %v", args, err)
		}
		if !strings.HasPrefix(out.String(), "saral dev (none, unknown)") {
			t.Errorf("%v printed %q", args, out.String())
		}
	}
}

func TestRun_BenchFirstPaintPrintsMicroseconds(t *testing.T) {
	t.Setenv("SARAL_CONFIG_DIR", t.TempDir())
	t.Setenv("SARAL_CACHE_DIR", t.TempDir())

	var out, errOut bytes.Buffer
	if err := run([]string{"--bench-first-paint"}, &out, &errOut); err != nil {
		t.Fatalf("run: %v (stderr %q)", err, errOut.String())
	}
	got := strings.TrimSpace(out.String())
	if _, err := strconv.Atoi(got); err != nil {
		t.Errorf("expected a microsecond count, got %q", got)
	}
}

func TestRun_BenchFirstPaintStillSaysWhyThereIsNoClient(t *testing.T) {
	writeProfile(t)
	t.Setenv("SARAL_TEST_TOKEN", "")

	var out, errOut bytes.Buffer
	if err := run([]string{"--bench-first-paint"}, &out, &errOut); err != nil {
		t.Fatalf("run: %v (stderr %q)", err, errOut.String())
	}
	if !strings.Contains(errOut.String(), "SARAL_TEST_TOKEN") {
		t.Errorf("stderr is %q, want the reason the session has no client", errOut.String())
	}
	if _, err := strconv.Atoi(strings.TrimSpace(out.String())); err != nil {
		t.Errorf("stdout is %q, want only the microsecond count", out.String())
	}
}

func TestRun_UnknownProfileIsAnError(t *testing.T) {
	t.Setenv("SARAL_CONFIG_DIR", t.TempDir())
	t.Setenv("SARAL_CACHE_DIR", t.TempDir())

	var out, errOut bytes.Buffer
	if err := run([]string{"--profile", "nope", "--bench-first-paint"}, &out, &errOut); err == nil {
		t.Error("an explicitly named missing profile should fail loudly")
	}
}

func TestRun_MissingConfigStillStarts(t *testing.T) {
	t.Setenv("SARAL_CONFIG_DIR", t.TempDir())
	t.Setenv("SARAL_CACHE_DIR", t.TempDir())

	deps, opts, _, _, err := build(options{benchPaint: true})
	if err != nil {
		t.Fatalf("build: %v", err)
	}
	if deps.Theme == nil {
		t.Error("no theme was built")
	}
	if _, _, err := kernel.FirstPaint(deps, 100, 30, opts...); err != nil {
		t.Errorf("FirstPaint: %v", err)
	}
}

func TestBuild_NoColorEnvironmentWinsOverTheFlag(t *testing.T) {
	t.Setenv("SARAL_CONFIG_DIR", t.TempDir())
	t.Setenv("SARAL_CACHE_DIR", t.TempDir())
	t.Setenv("NO_COLOR", "1")

	deps, _, _, _, err := build(options{theme: "dark"})
	if err != nil {
		t.Fatalf("build: %v", err)
	}
	if deps.Theme.Color {
		t.Error("NO_COLOR was ignored")
	}
}

func TestBuild_AFirstRunStartsAtSetup(t *testing.T) {
	t.Setenv("SARAL_CONFIG_DIR", t.TempDir())
	t.Setenv("SARAL_CACHE_DIR", t.TempDir())

	// Nothing configured. The kernel would otherwise open whichever view claimed
	// the first footer slot, leaving setup reachable only by someone who already
	// knows its name — which is nobody on their first run.
	_, opts, _, _, err := build(options{})
	if err != nil {
		t.Fatalf("build: %v", err)
	}
	if got := initialView(opts); got != kernel.SetupViewID {
		t.Errorf("a first run opens %q, want %q", got, kernel.SetupViewID)
	}
}

func TestBuild_AnExplicitViewStillWinsOnAFirstRun(t *testing.T) {
	t.Setenv("SARAL_CONFIG_DIR", t.TempDir())
	t.Setenv("SARAL_CACHE_DIR", t.TempDir())

	_, opts, _, _, err := build(options{view: "board"})
	if err != nil {
		t.Fatalf("build: %v", err)
	}
	if got := initialView(opts); got != "board" {
		t.Errorf("saral board opened %q, want board", got)
	}
}

func TestBuild_AConfiguredProfileDoesNotStartAtSetup(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("SARAL_CONFIG_DIR", dir)
	t.Setenv("SARAL_CACHE_DIR", t.TempDir())
	const cfg = `active = "work"

[profiles.work]
site  = "example.atlassian.net"
email = "you@example.com"
token = { env = "JIRA_TOKEN" }
`
	if err := os.WriteFile(filepath.Join(dir, "config.toml"), []byte(cfg), 0o600); err != nil {
		t.Fatal(err)
	}

	_, opts, _, _, err := build(options{})
	if err != nil {
		t.Fatalf("build: %v", err)
	}
	if got := initialView(opts); got != "" {
		t.Errorf("a configured profile opened %q, want the registered default", got)
	}
}

func TestBuild_TheProjectFlagOverridesTheProfileRatherThanReplacingIt(t *testing.T) {
	tests := map[string]struct {
		stored string
		flag   string
		want   string
		fails  bool
	}{
		"the profile alone scopes the session":   {stored: "EX", want: "EX"},
		"the flag alone scopes the session":      {flag: "OTHER", want: "OTHER"},
		"the flag wins over the profile":         {stored: "EX", flag: "OTHER", want: "OTHER"},
		"neither leaves the session unscoped":    {},
		"a blank flag falls back to the profile": {stored: "EX", flag: "   ", want: "EX"},
		"a padded flag is taken trimmed":         {flag: "  OTHER  ", want: "OTHER"},
		"a flag with a space in it is refused":   {flag: "two words", fails: true},
	}

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			dir := t.TempDir()
			t.Setenv("SARAL_CONFIG_DIR", dir)
			t.Setenv("SARAL_CACHE_DIR", t.TempDir())
			t.Setenv("SARAL_CACHE_DIR", t.TempDir())
			cfg := "active = \"work\"\n\n[profiles.work]\nsite  = \"example.atlassian.net\"\n" +
				"email = \"you@example.com\"\ntoken = { env = \"JIRA_TOKEN\" }\n"
			if tc.stored != "" {
				cfg += "project = " + strconv.Quote(tc.stored) + "\n"
			}
			if err := os.WriteFile(filepath.Join(dir, "config.toml"), []byte(cfg), 0o600); err != nil {
				t.Fatal(err)
			}

			deps, _, _, _, err := build(options{project: tc.flag})
			switch {
			case tc.fails && err == nil:
				t.Fatalf("--project %q was accepted and reached JQL", tc.flag)
			case tc.fails:
				if !strings.Contains(err.Error(), tc.flag) {
					t.Errorf("error %q does not quote the offending value", err)
				}
				return
			case err != nil:
				t.Fatalf("build: %v", err)
			}
			if deps.Project != tc.want {
				t.Errorf("the session is scoped to %q, want %q", deps.Project, tc.want)
			}
		})
	}
}

func initialView(opts []kernel.Option) string { return kernel.InitialViewOf(opts...) }

// writeProfile puts a config file naming an env-resolved token in a temporary
// config directory. Nothing here reaches a keychain: a test that prompted for
// one would hang on somebody's machine and pass on nobody's.
func writeProfile(t *testing.T) {
	t.Helper()

	dir := t.TempDir()
	t.Setenv("SARAL_CONFIG_DIR", dir)
	t.Setenv("SARAL_CACHE_DIR", t.TempDir())
	cfg := "active = \"work\"\n\n[profiles.work]\nsite  = \"example.atlassian.net\"\n" +
		"email = \"you@example.com\"\ntoken = { env = \"SARAL_TEST_TOKEN\" }\n"
	if err := os.WriteFile(filepath.Join(dir, "config.toml"), []byte(cfg), 0o600); err != nil {
		t.Fatal(err)
	}
}

func TestBuild_AResolvableTokenProducesAClientTheSessionCanUse(t *testing.T) {
	writeProfile(t)
	t.Setenv("SARAL_TEST_TOKEN", "a-token")

	deps, _, notice, _, err := build(options{})
	if err != nil {
		t.Fatalf("build: %v", err)
	}
	if deps.Jira == nil {
		t.Fatal("the session has no Jira client, so every read in it is dead on arrival")
	}
	if notice != "" {
		t.Errorf("a resolvable token still produced the notice %q", notice)
	}
	// Nothing was asked of the site. A probe before the first frame is the one
	// thing docs/UX.md rules out, and the kernel runs its own on Init.
	if deps.Caps != (jira.Capabilities{}) {
		t.Errorf("capabilities read %+v at build time, so something probed before the first frame", deps.Caps)
	}
}

func TestBuild_AnUnresolvableTokenStillStartsAndSaysWhy(t *testing.T) {
	writeProfile(t)
	t.Setenv("SARAL_TEST_TOKEN", "")

	deps, opts, notice, _, err := build(options{})
	if err != nil {
		t.Fatalf("a token that would not resolve stopped the program: %v", err)
	}
	if deps.Jira != nil {
		t.Error("a client was built from a token that does not exist")
	}
	if !strings.Contains(notice, "SARAL_TEST_TOKEN") {
		t.Errorf("the notice is %q, want the resolver's own sentence naming what it looked for", notice)
	}

	// The program opens, and the sentence is on the status line rather than
	// behind the alt screen that would have wiped it.
	m, err := kernel.New(deps, append(opts, kernel.WithSize(100, 30))...)
	if err != nil {
		t.Fatalf("kernel.New: %v", err)
	}
	started, ok := withNotice(m, notice).(kernel.Model)
	if !ok {
		t.Fatal("the notice replaced the kernel model with something else")
	}
	if !strings.Contains(started.Frame(), "SARAL_TEST_TOKEN") {
		t.Errorf("the first frame does not carry the reason:\n%s", started.Frame())
	}
}

func TestWithNotice_LeavesTheModelAloneWhenThereIsNothingToSay(t *testing.T) {
	t.Setenv("SARAL_CONFIG_DIR", t.TempDir())
	t.Setenv("SARAL_CACHE_DIR", t.TempDir())

	deps, opts, _, _, err := build(options{})
	if err != nil {
		t.Fatalf("build: %v", err)
	}
	m, err := kernel.New(deps, append(opts, kernel.WithSize(100, 30))...)
	if err != nil {
		t.Fatalf("kernel.New: %v", err)
	}
	plain, ok := withNotice(m, "").(kernel.Model)
	if !ok {
		t.Fatal("an empty notice replaced the kernel model with something else")
	}
	if plain.Frame() != m.Frame() {
		t.Error("an empty notice still changed what the first frame says")
	}
}

func TestConnect_ReturnsANilInterfaceRatherThanANilPointerInOne(t *testing.T) {
	t.Parallel()

	client, err := connect("", "you@example.com", "a-token")
	if err == nil {
		t.Fatal("a site of nothing was accepted")
	}
	if client != nil {
		t.Errorf("the failure came back as a non-nil %T, which passes every == nil check downstream", client)
	}
}

func TestConnect_BuildsAClientForCredentialsThatWereNeverSaved(t *testing.T) {
	t.Parallel()

	client, err := connect("example.atlassian.net", "you@example.com", "a-token")
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	if client == nil {
		t.Fatal("no client came back and no error did either")
	}
}

// A first run has no token and no profile, and onboarding is the path that gets
// one. The connector has to be registered before the view opens, or the flow
// refuses the credentials it has just collected.
func TestBuild_AFirstRunWiresTheConnectorBeforeOnboardingOpens(t *testing.T) {
	t.Setenv("SARAL_CONFIG_DIR", t.TempDir())
	t.Setenv("SARAL_CACHE_DIR", t.TempDir())

	deps, _, _, _, err := build(options{})
	if err != nil {
		t.Fatalf("build: %v", err)
	}

	// Built through onboarding.New, which takes whatever this build registered.
	// NewWith would prove nothing about it.
	view := onboarding.New(deps)
	view, _ = view.Update(kernel.SizeMsg{Width: 100, Height: 30})

	// The commands that come back are dropped rather than run. Reaching the
	// connect attempt is the whole assertion; running it would put a request on
	// the wire, and the site typed here is not one this test may talk to.
	for _, typed := range []string{"example.atlassian.net", "you@example.com", "a-token"} {
		for _, r := range typed {
			view, _ = view.Update(tea.KeyPressMsg{Code: r, Text: string(r)})
		}
		view, _ = view.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	}

	frame := view.View()
	if strings.Contains(frame, "onboarding.SetConnector") {
		t.Fatalf("the flow still refuses to open a connection:\n%s", frame)
	}
	if !strings.Contains(frame, "Checking the site, the email and the token") {
		t.Errorf("the flow did not reach a connection attempt:\n%s", frame)
	}
}

func TestBuild_OpensTheCacheForAConfiguredProfile(t *testing.T) {
	writeProfile(t)
	t.Setenv("SARAL_TEST_TOKEN", "a-token")

	deps, _, notice, closeCache, err := build(options{})
	if err != nil {
		t.Fatalf("build: %v", err)
	}
	defer closeCache()

	if deps.Cache == nil {
		t.Fatal("a configured session has nowhere to keep what it reads")
	}
	if notice != "" {
		t.Errorf("opening the cache said %q", notice)
	}
	dir, err := config.CacheDir()
	if err != nil {
		t.Fatalf("CacheDir: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, cacheFile)); err != nil {
		t.Errorf("the cache file is not on disk: %v", err)
	}
}

func TestBuild_AFirstRunHasNoCacheToOpen(t *testing.T) {
	t.Setenv("SARAL_CONFIG_DIR", t.TempDir())
	cacheDir := t.TempDir()
	t.Setenv("SARAL_CACHE_DIR", cacheDir)

	deps, _, _, closeCache, err := build(options{})
	if err != nil {
		t.Fatalf("build: %v", err)
	}
	defer closeCache()

	if deps.Cache != nil {
		t.Error("a run with no profile opened a cache for a site it does not know")
	}
	if _, err := os.Stat(filepath.Join(cacheDir, cacheFile)); err == nil {
		t.Error("a first run took the cache file's lock before it knew whose cache it was")
	}
}

// A second copy of Saral cannot have the cache file, because bbolt holds it
// exclusively. It has to run without one rather than refuse to start.
func TestBuild_AnotherCopyHoldingTheCacheStillStarts(t *testing.T) {
	writeProfile(t)
	t.Setenv("SARAL_TEST_TOKEN", "a-token")

	dir, err := config.CacheDir()
	if err != nil {
		t.Fatalf("CacheDir: %v", err)
	}
	held, err := store.Open(filepath.Join(dir, cacheFile))
	if err != nil {
		t.Fatalf("taking the cache first: %v", err)
	}
	t.Cleanup(func() {
		if err := held.Close(); err != nil {
			t.Errorf("Close: %v", err)
		}
	})

	deps, opts, notice, closeCache, err := build(options{})
	if err != nil {
		t.Fatalf("build refused to start with the cache held elsewhere: %v", err)
	}
	defer closeCache()

	if deps.Cache != nil {
		t.Error("two copies of Saral both hold the cache")
	}
	if deps.Jira == nil {
		t.Error("a session with no cache also lost its connection to the site")
	}
	if !strings.Contains(notice, "another copy of Saral") {
		t.Errorf("the run says %q, want it to say why nothing is being cached", notice)
	}
	if _, _, err := kernel.FirstPaint(deps, 100, 30, opts...); err != nil {
		t.Errorf("FirstPaint with no cache: %v", err)
	}
}

func TestBuild_ACacheFileThatCannotBeOpenedIsNotFatal(t *testing.T) {
	writeProfile(t)
	t.Setenv("SARAL_TEST_TOKEN", "a-token")

	dir, err := config.CacheDir()
	if err != nil {
		t.Fatalf("CacheDir: %v", err)
	}
	// A directory where the file goes is the portable way to make opening it
	// fail; a read-only parent does not stop root, which is who CI runs as.
	if err := os.MkdirAll(filepath.Join(dir, cacheFile), 0o700); err != nil {
		t.Fatalf("blocking the cache file: %v", err)
	}

	deps, _, notice, closeCache, err := build(options{})
	if err != nil {
		t.Fatalf("build refused to start without a usable cache: %v", err)
	}
	defer closeCache()

	if deps.Cache != nil {
		t.Error("a cache was built over a file that could not be opened")
	}
	if !strings.Contains(notice, "nothing will be cached") {
		t.Errorf("the run says %q, want it to say that nothing is being cached", notice)
	}
}

func TestBuild_ThePollFlagIsOffUnlessItIsGiven(t *testing.T) {
	writeProfile(t)
	t.Setenv("SARAL_TEST_TOKEN", "a-token")
	t.Cleanup(func() { list.SetPollInterval(0) })

	_, _, _, closeCache, err := build(options{})
	if err != nil {
		t.Fatalf("build: %v", err)
	}
	closeCache()
	if got := list.PollInterval(); got != 0 {
		t.Errorf("a run nobody asked to poll polls every %s", got)
	}

	_, _, _, closeCache, err = build(options{poll: 90 * time.Second})
	if err != nil {
		t.Fatalf("build: %v", err)
	}
	closeCache()
	if got := list.PollInterval(); got != 90*time.Second {
		t.Errorf("--poll 90s produced %s", got)
	}
}

// TestFirstPaint_DrawsRowsOutOfTheRealCacheFile is the one test that runs the
// whole path: bbolt on disk, through app.Cache, onto kernel.Deps, into the list's
// constructor, and out as a frame. Everything above the store is otherwise tested
// against a cache in a map, because internal/ui may not import internal/store.
func TestFirstPaint_DrawsRowsOutOfTheRealCacheFile(t *testing.T) {
	writeProfile(t)
	t.Setenv("SARAL_TEST_TOKEN", "a-token")

	// The query the list opens on, which is internal/ui/list's own default for a
	// session scoped to a project. Nothing exports it, so if it changes this test
	// stops finding rows and says so rather than passing on a different question.
	const opensOn = `project = "PROJ" AND assignee = currentUser() ORDER BY updated DESC`

	deps, _, _, releaseCache, err := build(options{project: "PROJ"})
	if err != nil {
		t.Fatalf("build: %v", err)
	}
	defer releaseCache()
	if deps.Cache == nil {
		t.Fatal("there is no cache to write into")
	}

	rows := jiratest.Gen(5)
	mask := jira.NewFieldMask(app.ListProjection().IDs)
	for i := range rows {
		rows[i].Requested = mask
	}
	if err := deps.Cache.PutRows(opensOn, rows, false); err != nil {
		t.Fatalf("PutRows: %v", err)
	}

	// A second session over the same file, which is what the next run of the
	// program is. The first has to let the lock go for this to open at all.
	releaseCache()
	deps, opts, _, releaseCache, err := build(options{project: "PROJ"})
	if err != nil {
		t.Fatalf("second build: %v", err)
	}
	defer releaseCache()

	_, frame, err := kernel.FirstPaint(deps, 120, 40, opts...)
	if err != nil {
		t.Fatalf("FirstPaint: %v", err)
	}
	for i := range rows {
		if !strings.Contains(frame, rows[i].Key) {
			t.Fatalf("the first frame does not show %s, so it was not drawn from the file:\n%s", rows[i].Key, frame)
		}
		if !strings.Contains(frame, rows[i].Summary) {
			t.Errorf("%s was drawn without its summary", rows[i].Key)
		}
	}
}
