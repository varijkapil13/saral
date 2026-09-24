package main

import (
	"cmp"
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/varijkapil13/saral/internal/config"
	"github.com/varijkapil13/saral/internal/store"
	"github.com/varijkapil13/saral/pkg/jira"
)

const doctorTimeout = 20 * time.Second

var doctorCaps = []jira.CapabilityKey{
	jira.CapBoards, jira.CapPlans, jira.CapBulkMove, jira.CapAttachments, jira.CapDeleteIssues, jira.CapPeople,
}

func init() {
	registerSubcommand(subcommand{
		name:    "doctor",
		summary: "check the config, the token, the site and the cache; the output is safe to paste",
		run:     runDoctor,
	})
}

// report's code is the exit code of the first check that failed.
type report struct {
	rows []reportRow
	code int
}

type reportRow struct {
	name, value string
	failed      bool
}

func (r *report) ok(name, format string, args ...any) {
	r.rows = append(r.rows, reportRow{name: name, value: scrub(fmt.Sprintf(format, args...))})
}

func (r *report) fail(code int, name, format string, args ...any) {
	r.rows = append(r.rows, reportRow{name: name, value: scrub(fmt.Sprintf(format, args...)), failed: true})
	if r.code == exitOK {
		r.code = code
	}
}

func (r *report) write(w io.Writer) error {
	tw := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
	for _, row := range r.rows {
		mark := "ok"
		if row.failed {
			mark = "FAIL"
		}
		if _, err := fmt.Fprintf(tw, "%s\t%s\t%s\n", mark, row.name, row.value); err != nil {
			return err
		}
	}
	return tw.Flush()
}

func scrub(s string) string { return emailShape.ReplaceAllStringFunc(s, redactEmail) }

func runDoctor(inv *invocation, args []string) error {
	opt := inv.opt
	fs := flag.NewFlagSet("saral doctor", flag.ContinueOnError)
	fs.SetOutput(inv.stderr)
	bindProfileFlags(fs, &opt)
	if err := fs.Parse(args); err != nil {
		if isHelp(err) {
			return nil
		}
		return withCode(exitUsage, errAlreadyReported)
	}
	if fs.NArg() > 0 {
		return usageErrorf("saral doctor takes no arguments, got %s", strings.Join(fs.Args(), " "))
	}
	if opt.logFile != "" && opt.logFile != inv.opt.logFile {
		l, closeLog, err := openLog(opt.logFile)
		if err != nil {
			return err
		}
		defer closeLog()
		opt.logger = l
	}

	var r report
	s := stamp()
	r.ok("version", "%s (%s)", s.version, buildKind())
	profile, ok := doctorProfile(&r, opt)
	if ok {
		doctorSite(&r, profile, opt)
		doctorCache(&r, profile)
		doctorProxy(&r, profile)
	}
	r.ok("glyphs", "%s", glyphTier(opt))
	r.ok("terminal", "TERM=%s TERM_PROGRAM=%s COLORTERM=%s NO_COLOR=%s",
		cmp.Or(os.Getenv("TERM"), "unset"), cmp.Or(os.Getenv("TERM_PROGRAM"), "unset"),
		cmp.Or(os.Getenv("COLORTERM"), "unset"), setOrUnset("NO_COLOR"))

	if err := r.write(inv.stdout); err != nil {
		return err
	}
	if r.code != exitOK {
		return withCode(r.code, errAlreadyReported)
	}
	return nil
}

func setOrUnset(name string) string {
	if _, ok := os.LookupEnv(name); ok {
		return "set"
	}
	return "unset"
}

func doctorProfile(r *report, opt options) (config.Profile, bool) {
	path, err := config.Path()
	if err != nil {
		r.fail(exitConfig, "config", "%v", err)
		return config.Profile{}, false
	}
	cfg, err := config.LoadFile(path)
	switch {
	case errors.Is(err, config.ErrNoConfig):
		r.ok("config", "%s (not written yet)", path)
	case err != nil:
		r.fail(exitConfig, "config", "%s: %v", path, err)
		return config.Profile{}, false
	default:
		r.ok("config", "%s", path)
	}
	for _, w := range cfg.Warnings {
		r.ok("config", "warning: %s", w)
	}

	profile, fromEnv, err := resolveProfile(cfg, opt.profile)
	if err != nil {
		if errors.Is(err, config.ErrNoProfile) {
			err = fmt.Errorf("%w; run saral to set one up, or set %s and %s", err, envSite, envEmail)
		}
		r.fail(exitConfig, "profile", "%v", err)
		return config.Profile{}, false
	}
	source := "config.toml"
	if fromEnv {
		source = envSite + " and " + envEmail
	}
	project, err := sessionProject(opt.project, profile.Project)
	if err != nil {
		r.fail(exitUsage, "profile", "%v", err)
		return config.Profile{}, false
	}
	profile.Project = project
	r.ok("profile", "%s (from %s): site %s, email %s, project %s",
		profile.Name, source, profile.Site, redactEmail(profile.Email), cmp.Or(project, "none"))
	return profile, true
}

func doctorSite(r *report, profile config.Profile, opt options) {
	ctx, cancel := context.WithTimeout(context.Background(), doctorTimeout)
	defer cancel()

	var client jira.SessionClient
	if opt.connectVia != nil {
		c, err := opt.connectVia(profile)
		if err != nil {
			r.fail(exitAuth, "token", "%v", err)
			return
		}
		r.ok("token", "not needed: this run's client is in process")
		client = c
	} else {
		token, err := profile.ResolveToken(ctx)
		if err != nil {
			r.fail(exitAuth, "token", "from %s: does not resolve: %v", profile.Token, err)
			return
		}
		r.ok("token", "from %s: resolves", profile.Token)
		c, err := connectWith(opt.logger)(profile.Site, profile.Email, token)
		if err != nil {
			r.fail(exitConfig, "site", "%v", err)
			return
		}
		client = c
	}

	if _, err := client.Me(ctx); err != nil {
		r.fail(siteFailure(err), "/myself", "%v", err)
		return
	}
	r.ok("/myself", "the site accepts the token")

	if reader, ok := client.(jira.ServerInfoReader); ok {
		if info, err := reader.ServerInfo(ctx); err != nil {
			r.fail(siteFailure(err), "deployment", "%v", err)
		} else {
			r.ok("deployment", "%s, version %s", cmp.Or(string(info.DeploymentType), "unknown"), cmp.Or(info.Version, "unknown"))
		}
	}

	caps, err := client.Capabilities(ctx, profile.Project)
	if err != nil {
		r.fail(siteFailure(err), "capabilities", "%v", err)
		return
	}
	parts := make([]string, 0, len(doctorCaps))
	for _, k := range doctorCaps {
		c := caps.Capability(k)
		switch {
		case c.OK:
			parts = append(parts, string(k)+" yes")
		case c.Reason != "":
			parts = append(parts, fmt.Sprintf("%s no (%s)", k, c.Reason))
		default:
			parts = append(parts, string(k)+" no")
		}
	}
	r.ok("capabilities", "%s", strings.Join(parts, ", "))
}

func siteFailure(err error) int {
	var auth *jira.AuthError
	if errors.As(err, &auth) {
		return exitAuth
	}
	return exitOther
}

// doctorCache must not create the file it reports on.
func doctorCache(r *report, _ config.Profile) {
	dir, err := config.CacheDir()
	if err != nil {
		r.fail(exitOther, "cache", "%v", err)
		return
	}
	path := filepath.Join(dir, cacheFile)
	info, err := os.Stat(path)
	switch {
	case errors.Is(err, os.ErrNotExist):
		r.ok("cache", "%s (not written yet)", path)
		return
	case err != nil:
		r.fail(exitOther, "cache", "%s: %v", path, err)
		return
	}
	size := fmt.Sprintf("%.1f MiB", float64(info.Size())/(1<<20))
	db, err := store.Open(path, store.WithLockTimeout(100*time.Millisecond))
	switch {
	case errors.Is(err, store.ErrLocked):
		r.ok("cache", "%s, %s, open in another copy of Saral", path, size)
	case err != nil:
		r.fail(exitOther, "cache", "%s, %s: %v", path, size, err)
	default:
		state := "readable"
		if aside := db.Recovered(); aside != "" {
			state = "unreadable, moved to " + aside
		}
		_ = db.Close()
		r.ok("cache", "%s, %s, %s", path, size, state)
	}
}

func doctorProxy(r *report, profile config.Profile) {
	req, err := http.NewRequest(http.MethodGet, profile.BaseURL()+"/", http.NoBody)
	if err != nil {
		r.ok("proxy", "unknown: %v", err)
		return
	}
	proxy, err := http.ProxyFromEnvironment(req)
	switch {
	case err != nil:
		r.fail(exitConfig, "proxy", "%v", err)
	case proxy == nil:
		r.ok("proxy", "none")
	default:
		r.ok("proxy", "%s", redactProxy(proxy))
	}
}

func redactProxy(u *url.URL) string {
	clean := *u
	if clean.User != nil {
		clean.User = url.User(redacted)
	}
	return clean.String()
}
