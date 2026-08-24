// Command saral is a terminal client for Jira Cloud.
package main

import (
	"cmp"
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"strings"
	"time"
	"unicode"

	tea "charm.land/bubbletea/v2"

	"github.com/varijkapil13/saral/internal/app"
	"github.com/varijkapil13/saral/internal/config"
	_ "github.com/varijkapil13/saral/internal/ui"
	"github.com/varijkapil13/saral/internal/ui/kernel"
	"github.com/varijkapil13/saral/internal/ui/onboarding"
	"github.com/varijkapil13/saral/pkg/jira"
	"github.com/varijkapil13/saral/pkg/jira/cloud"
)

// tokenTimeout bounds resolving the token, which happens before the first frame
// and cannot be cancelled by anyone watching it. internal/config gives a command
// source 15s plus a 2s wait delay of its own, so this sits above that and lets
// the resolver's better-worded failure win; what it really caps is a keychain
// prompt nobody is at the machine to answer.
const tokenTimeout = 20 * time.Second

var (
	version = "dev"
	commit  = "none"
	date    = "unknown"
)

// errAlreadyReported means the message has been printed already, so main exits
// without printing a second one.
var errAlreadyReported = errors.New("")

func main() {
	err := run(os.Args[1:], os.Stdout, os.Stderr)
	switch {
	case err == nil:
		return
	case errors.Is(err, errAlreadyReported):
	default:
		fmt.Fprintln(os.Stderr, "saral: "+err.Error())
	}
	os.Exit(1)
}

type options struct {
	profile    string
	project    string
	view       string
	theme      string
	mouse      bool
	mouseSet   bool
	benchPaint bool
	showVer    bool
}

func run(args []string, stdout, stderr io.Writer) error {
	var opt options
	fs := flag.NewFlagSet("saral", flag.ContinueOnError)
	fs.SetOutput(stderr)
	fs.StringVar(&opt.project, "project", "", "project key to scope the session to; several capabilities are per-project")
	fs.StringVar(&opt.profile, "profile", "", "profile to use (default: the active one)")
	fs.StringVar(&opt.theme, "theme", "", "auto, dark, light or no-color")
	fs.BoolVar(&opt.mouse, "mouse", true, "enable mouse reporting")
	fs.BoolVar(&opt.benchPaint, "bench-first-paint", false, "render one frame, print how long it took, and exit")
	fs.BoolVar(&opt.showVer, "version", false, "print the version and exit")
	fs.Usage = func() {
		_, _ = fmt.Fprint(stderr, "usage: saral [flags] [view]\n\nflags:\n")
		fs.PrintDefaults()
	}
	if len(args) > 0 && args[0] == "version" {
		args = []string{"--version"}
	}
	if err := fs.Parse(args); err != nil {
		// --help is a request, not a failure, and flag has already printed the
		// usage and the error for anything else.
		if errors.Is(err, flag.ErrHelp) {
			return nil
		}
		return errAlreadyReported
	}
	fs.Visit(func(f *flag.Flag) {
		if f.Name == "mouse" {
			opt.mouseSet = true
		}
	})
	if opt.showVer {
		_, err := fmt.Fprintf(stdout, "saral %s (%s, %s)\n", version, commit, date)
		return err
	}
	if rest := fs.Args(); len(rest) > 0 {
		opt.view = rest[0]
	}

	if errs := kernel.RegistrationErrors(); len(errs) > 0 {
		return fmt.Errorf("%d view(s) failed to register: %w", len(errs), errors.Join(errs...))
	}

	deps, kopts, notice, err := build(opt)
	if err != nil {
		return err
	}
	if opt.benchPaint {
		return benchFirstPaint(stdout, deps, kopts)
	}
	return start(deps, kopts, notice)
}

// build turns flags and config into the kernel's dependencies. A missing config
// file is not an error: there is nothing to onboard with yet, so the UI opens
// with no site and says so.
//
// The third result is a sentence to put in front of the user once the program is
// running. Resolving a token can fail for reasons that are nobody's mistake — a
// locked keychain, a helper command that is not installed on this machine — and
// none of them is a reason to refuse to open.
func build(opt options) (kernel.Deps, []kernel.Option, string, error) {
	deps := kernel.Deps{}
	cfg, err := config.Load()
	switch {
	case errors.Is(err, config.ErrNoConfig):
		cfg = config.Config{Mouse: true}
	case err != nil:
		return deps, nil, "", err
	}

	// A config file that exists but points nowhere is a mistake worth naming: the
	// alternative is a session that silently talks to no site at all.
	profile, perr := profileFor(cfg, opt.profile)
	if perr != nil && (opt.profile != "" || len(cfg.Profiles) > 0) {
		return deps, nil, "", perr
	}
	deps.Site = profile.Site
	project, err := sessionProject(opt.project, profile.Project)
	if err != nil {
		return deps, nil, "", err
	}
	deps.Project = project

	saved, err := app.NewSavedQueries(profile.Queries...)
	if err != nil {
		return deps, nil, "", err
	}
	deps.Saved = saved
	deps.SaveQueries = queryWriter(profile.Name)

	// Nothing configured means the first thing to show is the thing that
	// configures it. Without this the kernel opens whichever view claimed the
	// first footer slot, and setup is reachable only by someone who already
	// knows its name — which is nobody on their first run.
	firstRun := perr != nil || profile.Site == ""

	// A first run is exactly the path with no token yet, and onboarding is what
	// gets one, so this goes on whether or not there is a profile to open with.
	onboarding.SetConnector(connect)

	var notice string
	if !firstRun {
		client, cerr := clientFor(profile)
		if cerr != nil {
			notice = cerr.Error()
		} else {
			deps.Jira = client
		}
	}

	theme := opt.theme
	if theme == "" {
		theme = profile.Theme
	}
	mode := kernel.ThemeModeFromEnv(os.Environ(), theme)
	deps.Theme = kernel.NewTheme(mode, true, kernel.GlyphsFor(profile.Glyphs))

	mouse := cfg.Mouse
	if opt.mouseSet {
		mouse = opt.mouse
	}
	kopts := []kernel.Option{kernel.WithMouse(mouse)}
	switch {
	case opt.view != "":
		kopts = append(kopts, kernel.WithInitialView(opt.view))
	case firstRun:
		kopts = append(kopts, kernel.WithInitialView(kernel.SetupViewID))
	}
	return deps, kopts, notice, nil
}

// connect opens a client for credentials the caller already holds, which is what
// onboarding does with three fields that have never been saved.
//
// The error path returns a nil interface deliberately. cloud.New returns a
// *cloud.Client, and returning that straight into the result would hand back a
// non-nil jira.SessionClient wrapping a nil pointer — every `== nil` check
// downstream would pass and the first call would panic.
func connect(site, email, token string) (jira.SessionClient, error) {
	client, err := cloud.New(site, email, token, cloud.WithUserAgent("saral/"+version))
	if err != nil {
		return nil, err
	}
	return client, nil
}

// clientFor resolves the profile's token and opens a client with it.
//
// Nothing is probed here. The kernel probes on Init once deps.Jira is set, so
// the first frame is drawn from what is already on disk (docs/UX.md).
func clientFor(p config.Profile) (jira.SessionClient, error) {
	ctx, cancel := context.WithTimeout(context.Background(), tokenTimeout)
	defer cancel()

	token, err := p.ResolveToken(ctx)
	if err != nil {
		return nil, err
	}
	return connect(p.Site, p.Email, token)
}

// sessionProject scopes the session. The flag overrides the profile for one run
// rather than replacing it, and reaches JQL the way a stored key does, so it is
// held to the rule internal/config enforces on one.
func sessionProject(fromFlag, stored string) (string, error) {
	key := cmp.Or(strings.TrimSpace(fromFlag), stored)
	if strings.ContainsFunc(key, unicode.IsSpace) {
		return "", fmt.Errorf("--project %q is not a project key", key)
	}
	return key, nil
}

// queryWriter persists a changed set of saved queries back into the profile
// they came from. It re-reads the file rather than writing back the copy this
// session started with, because onboarding may have written one since; an empty
// name is the first run, where the profile to write into is whichever one is
// active by the time a key is bound.
func queryWriter(name string) func(app.SavedQueries) error {
	return func(saved app.SavedQueries) error {
		path, err := config.Path()
		if err != nil {
			return err
		}
		cfg, err := config.LoadFile(path)
		if err != nil {
			return err
		}
		profile, err := profileFor(cfg, name)
		if err != nil {
			return err
		}
		profile.Queries = saved.All()
		cfg.Profiles[profile.Name] = profile
		return cfg.Save(path)
	}
}

func profileFor(cfg config.Config, name string) (config.Profile, error) {
	if name != "" {
		return cfg.Get(name)
	}
	return cfg.Current()
}

// benchFirstPaint measures the budget in docs/PERFORMANCE.md that is otherwise
// unmeasurable: how long it takes to put the first frame on the screen from
// what is already on disk.
func benchFirstPaint(stdout io.Writer, deps kernel.Deps, kopts []kernel.Option) error {
	took, _, err := kernel.FirstPaint(deps, 120, 40, kopts...)
	if err != nil {
		return err
	}
	_, err = fmt.Fprintf(stdout, "%d\n", took.Microseconds())
	return err
}

func start(deps kernel.Deps, kopts []kernel.Option, notice string) error {
	m, err := kernel.New(deps, kopts...)
	if err != nil {
		return err
	}
	if _, err := tea.NewProgram(withNotice(m, notice)).Run(); err != nil {
		return err
	}
	return nil
}

// withNotice puts a startup message on the status line before the program runs.
//
// The alt screen wipes anything printed ahead of it, so the status line is the
// only surface left, and Update is a pure function on a value, so the message
// goes onto the model rather than into a running event loop. The resize command
// it returns is dropped: no size is known yet, and the WindowSizeMsg that
// arrives at startup does the same work.
func withNotice(m kernel.Model, notice string) tea.Model {
	if notice == "" {
		return m
	}
	next, _ := m.Update(kernel.StatusMsg{Text: notice, Level: kernel.LevelWarn})
	return next
}
