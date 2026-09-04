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
	"path/filepath"
	"slices"
	"strings"
	"time"
	"unicode"

	tea "charm.land/bubbletea/v2"

	"github.com/varijkapil13/saral/internal/app"
	"github.com/varijkapil13/saral/internal/config"
	"github.com/varijkapil13/saral/internal/store"
	_ "github.com/varijkapil13/saral/internal/ui"
	"github.com/varijkapil13/saral/internal/ui/issue"
	"github.com/varijkapil13/saral/internal/ui/kernel"
	"github.com/varijkapil13/saral/internal/ui/list"
	"github.com/varijkapil13/saral/internal/ui/onboarding"
	"github.com/varijkapil13/saral/pkg/jira"
	"github.com/varijkapil13/saral/pkg/jira/cloud"
)

// cacheFile is the bbolt database inside config.CacheDir().
const cacheFile = "cache.db"

// fileNameTOML is what config.Path builds; --version names it without asking
// for the path, which would fail on a machine with no home directory.
const fileNameTOML = "config.toml"

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
	profile string
	project string
	// arg is the positional argument, which names a view, an issue or a Jira URL.
	arg        string
	theme      string
	scheme     string
	glyphs     string
	poll       time.Duration
	mouse      bool
	mouseSet   bool
	benchPaint bool
	showVer    bool
}

// printVersion names the build and where it keeps its files. The path is not
// decoration: a checkout and an installed copy are two programs on one machine,
// and the first question about a profile that did not save is which of them
// wrote it.
func printVersion(stdout io.Writer) error {
	if _, err := fmt.Fprintf(stdout, "saral %s (%s, %s)\n", version, commit, date); err != nil {
		return err
	}
	_, err := fmt.Fprintln(stdout, configNote())
	return err
}

// configNote is where this build keeps its profile, and why that is not where
// another copy keeps its. A machine with no home directory has nowhere to put
// one, which is worth saying rather than leaving the line off.
func configNote() string {
	dir, err := config.Dir()
	if err != nil {
		return "config nowhere: " + err.Error()
	}
	kind := "release"
	if config.IsDevBuild() {
		kind = "development build, kept apart from an installed copy"
	}
	return "config " + filepath.Join(dir, fileNameTOML) + " (" + kind + ")"
}

func run(args []string, stdout, stderr io.Writer) error {
	var opt options
	fs := flag.NewFlagSet("saral", flag.ContinueOnError)
	fs.SetOutput(stderr)
	fs.StringVar(&opt.project, "project", "", "project key to scope the session to; several capabilities are per-project")
	fs.StringVar(&opt.profile, "profile", "", "profile to use (default: the active one)")
	fs.StringVar(&opt.theme, "theme", "", "auto, dark, light or no-color")
	fs.StringVar(&opt.scheme, "scheme", "", "default, nord, dracula, solarized or gruvbox")
	fs.StringVar(&opt.glyphs, "glyphs", "", "nerd, unicode or ascii; nerd is the default and assumes a Nerd Font")
	fs.DurationVar(&opt.poll, "poll", 0, "re-read the focused view this often; off by default, and pauses when Jira rate-limits")
	fs.BoolVar(&opt.mouse, "mouse", true, "enable mouse reporting")
	fs.BoolVar(&opt.benchPaint, "bench-first-paint", false, "render one frame, print how long it took, and exit")
	fs.BoolVar(&opt.showVer, "version", false, "print the version and exit")
	fs.Usage = func() {
		_, _ = fmt.Fprint(stderr, "usage: saral [flags] [view | issue key | Jira URL]\n\nflags:\n")
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
		return printVersion(stdout)
	}
	switch rest := fs.Args(); len(rest) {
	case 0:
	case 1:
		opt.arg = rest[0]
	default:
		return fmt.Errorf("saral opens one thing: %s. To scope a session to a project, use --project",
			strings.Join(rest, " and "))
	}

	if errs := kernel.RegistrationErrors(); len(errs) > 0 {
		return fmt.Errorf("%d view(s) failed to register: %w", len(errs), errors.Join(errs...))
	}

	deps, kopts, notice, closeCache, err := build(opt)
	if err != nil {
		return err
	}
	defer closeCache()
	if opt.benchPaint {
		return benchFirstPaint(stdout, stderr, deps, kopts, notice)
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
//
// The fourth releases the cache file. bbolt holds an exclusive lock on it for as
// long as it is open, so the run that took it gives it back rather than leaving
// the next copy of Saral without one.
func build(opt options) (deps kernel.Deps, kopts []kernel.Option, notice string, releaseCache func(), err error) {
	releaseCache = func() {}
	cfg, err := config.Load()
	switch {
	case errors.Is(err, config.ErrNoConfig):
		cfg = config.Config{Mouse: true}
	case err != nil:
		return deps, nil, notice, releaseCache, err
	}

	// A config file that exists but points nowhere is a mistake worth naming: the
	// alternative is a session that silently talks to no site at all.
	profile, perr := profileFor(cfg, opt.profile)
	if perr != nil && (opt.profile != "" || len(cfg.Profiles) > 0) {
		return deps, nil, notice, releaseCache, perr
	}
	deps.Site = profile.Site
	project, err := sessionProject(opt.project, profile.Project)
	if err != nil {
		return deps, nil, notice, releaseCache, err
	}
	deps.Project = project

	saved, err := app.NewSavedQueries(profile.Queries...)
	if err != nil {
		return deps, nil, notice, releaseCache, err
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

	if !firstRun {
		client, cerr := clientFor(profile)
		if cerr != nil {
			notice = cerr.Error()
		} else {
			deps.Jira = client
		}
		// Opened whether or not the client was: rows from the last session are
		// worth drawing even in a session that cannot reach the site at all.
		var cacheNote string
		deps.Cache, releaseCache, cacheNote = openCache(profile)
		if notice == "" {
			notice = cacheNote
		}
	}

	list.SetPollInterval(opt.poll)

	theme := opt.theme
	if theme == "" {
		theme = profile.Theme
	}
	mode := kernel.ThemeModeFromEnv(os.Environ(), theme)
	scheme := opt.scheme
	if scheme == "" {
		scheme = profile.Scheme
	}
	// An unrecognised scheme falls back to the default the same way an
	// unrecognised theme falls back to auto: silently, at the flag, with the
	// error surfaced instead when a profile tries to save one.
	resolvedScheme, _ := kernel.ParseScheme(scheme)
	glyphs := opt.glyphs
	if glyphs == "" {
		glyphs = profile.Glyphs
	}
	deps.Theme = kernel.NewTheme(mode, true, kernel.GlyphsFor(glyphs), kernel.WithScheme(resolvedScheme))

	mouse := cfg.Mouse
	if opt.mouseSet {
		mouse = opt.mouse
	}
	kopts = []kernel.Option{kernel.WithMouse(mouse)}
	switch {
	case opt.arg != "":
		opened, argNotice, aerr := argument(opt.arg, profile.Site)
		if aerr != nil {
			return deps, nil, notice, releaseCache, aerr
		}
		kopts = append(kopts, opened...)
		// Both, when there are both: one says why nothing will reach the site and
		// the other why the issue that was asked for is not on screen, and
		// dropping either leaves a question the status line could have answered.
		if argNotice != "" {
			notice = strings.TrimPrefix(notice+" · "+argNotice, " · ")
		}
	case firstRun:
		kopts = append(kopts, kernel.WithInitialView(kernel.SetupViewID))
	}
	return deps, kopts, notice, releaseCache, nil
}

// argument resolves the positional argument: an issue key, a Jira URL, or the ID
// of a registered view.
//
// Anything else is an error rather than nothing. The argument used to be handed
// to the kernel as a view ID and dropped where no view had it, so `saral PROJ-142`
// and `saral bord` both opened whichever view held the first footer slot and said
// nothing about the word that was typed.
//
// A URL for another site is named rather than opened: the key would be read
// against this profile's site, where it is at best a 404 and at worst somebody
// else's issue with the same key.
func argument(arg, site string) (opts []kernel.Option, notice string, err error) {
	if key, ok := app.ParseKey(arg); ok {
		return []kernel.Option{openIssue(key)}, "", nil
	}
	if key, host, ok := app.ParseIssueURL(arg); ok {
		here, serr := config.NormalizeSite(site)
		if serr == nil && !strings.EqualFold(here, host) {
			return nil, fmt.Sprintf("%s is on %s and this profile is on %s, so it was not opened", key, host, here), nil
		}
		return []kernel.Option{openIssue(key)}, "", nil
	}
	if _, ok := kernel.LookupView(arg); ok {
		return []kernel.Option{kernel.WithInitialView(arg)}, "", nil
	}
	return nil, "", fmt.Errorf("%q is not an issue key, a Jira URL, or one of the views in this build (%s)",
		arg, strings.Join(viewIDs(), ", "))
}

// openIssue is the detail pane over whichever root opens, seeded with the key
// and nothing else: the pane reads the issue itself, and says so in its own
// words when the site has no such key.
func openIssue(key string) kernel.Option {
	return kernel.WithInitialPush(issue.ViewID, key, func(d kernel.Deps) kernel.View {
		return issue.New(d, jira.Issue{Key: key})
	})
}

func viewIDs() []string {
	specs := kernel.Views()
	ids := make([]string, 0, len(specs))
	for _, spec := range specs {
		ids = append(ids, spec.ID)
	}
	slices.Sort(ids)
	return ids
}

// openCache opens the profile's cache, and says in words when it could not.
//
// Nothing here is fatal: a session that keeps no cache fetches everything it
// shows, which still works. Another copy of Saral holding the file is the
// ordinary way that happens, not a mistake, and so is an unwritable home.
func openCache(p config.Profile) (cache app.Cache, release func(), notice string) {
	release = func() {}
	dir, err := config.CacheDir()
	if err != nil {
		return nil, release, "nothing will be cached this session: " + err.Error()
	}
	db, err := store.Open(filepath.Join(dir, cacheFile))
	switch {
	case errors.Is(err, store.ErrLocked):
		return nil, release, "another copy of Saral has the cache open, so this session keeps none of its own"
	case err != nil:
		return nil, release, "nothing will be cached this session: " + err.Error()
	}
	// The account is the profile's email rather than its Jira account ID: the ID
	// takes a round trip to learn, the first frame is drawn before one could have
	// answered, and two profiles on one site differ by email anyway.
	return app.NewCache(db, store.Scope{Site: p.Site, Account: p.Email}), func() { _ = db.Close() }, ""
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
//
// There is no status line on this path, so the startup notice goes to stderr
// instead: a run measuring a session with no client is measuring something else,
// and stdout stays the single number a script reads.
func benchFirstPaint(stdout, stderr io.Writer, deps kernel.Deps, kopts []kernel.Option, notice string) error {
	if notice != "" {
		_, _ = fmt.Fprintln(stderr, "saral: "+notice)
	}
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
