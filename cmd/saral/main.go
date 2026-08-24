// Command saral is a terminal client for Jira Cloud.
package main

import (
	"errors"
	"flag"
	"fmt"
	"io"
	"os"

	tea "charm.land/bubbletea/v2"

	"github.com/varijkapil13/saral/internal/app"
	"github.com/varijkapil13/saral/internal/config"
	_ "github.com/varijkapil13/saral/internal/ui"
	"github.com/varijkapil13/saral/internal/ui/kernel"
)

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

	deps, kopts, err := build(opt)
	if err != nil {
		return err
	}
	if opt.benchPaint {
		return benchFirstPaint(stdout, deps, kopts)
	}
	return start(deps, kopts)
}

// build turns flags and config into the kernel's dependencies. A missing config
// file is not an error: there is nothing to onboard with yet, so the UI opens
// with no site and says so.
func build(opt options) (kernel.Deps, []kernel.Option, error) {
	deps := kernel.Deps{}
	cfg, err := config.Load()
	switch {
	case errors.Is(err, config.ErrNoConfig):
		cfg = config.Config{Mouse: true}
	case err != nil:
		return deps, nil, err
	}

	// A config file that exists but points nowhere is a mistake worth naming: the
	// alternative is a session that silently talks to no site at all.
	profile, perr := profileFor(cfg, opt.profile)
	if perr != nil && (opt.profile != "" || len(cfg.Profiles) > 0) {
		return deps, nil, perr
	}
	deps.Site = profile.Site
	deps.Project = opt.project

	saved, err := app.NewSavedQueries(profile.Queries...)
	if err != nil {
		return deps, nil, err
	}
	deps.Saved = saved
	deps.SaveQueries = queryWriter(profile.Name)

	// Nothing configured means the first thing to show is the thing that
	// configures it. Without this the kernel opens whichever view claimed the
	// first footer slot, and setup is reachable only by someone who already
	// knows its name — which is nobody on their first run.
	firstRun := perr != nil || profile.Site == ""

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
	return deps, kopts, nil
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

func start(deps kernel.Deps, kopts []kernel.Option) error {
	m, err := kernel.New(deps, kopts...)
	if err != nil {
		return err
	}
	if _, err := tea.NewProgram(m).Run(); err != nil {
		return err
	}
	return nil
}
