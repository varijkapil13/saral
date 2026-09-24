package main

import (
	"errors"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/varijkapil13/saral/internal/config"
	"github.com/varijkapil13/saral/internal/ui/kernel"
	"github.com/varijkapil13/saral/pkg/jira"
)

var glyphTiers = []string{"unicode", "nerd", "ascii"}

// hiddenFlags are CI measuring tools, left out of --help.
var hiddenFlags = map[string]bool{"bench-first-paint": true}

type options struct {
	profile    string
	project    string
	arg        string
	theme      string
	scheme     string
	glyphs     string
	poll       time.Duration
	logFile    string
	mouse      bool
	mouseSet   bool
	benchPaint bool
	showVer    bool
	fake       bool

	// connectVia, when set, replaces resolving a token and dialling the site.
	connectVia func(config.Profile) (jira.SessionClient, error)
	logger     *slog.Logger
}

type pollFlag struct{ d *time.Duration }

func (p pollFlag) String() string {
	if p.d == nil || *p.d == 0 {
		return ""
	}
	return p.d.String()
}

func (p pollFlag) Set(s string) error {
	s = strings.TrimSpace(s)
	if n, err := strconv.ParseFloat(s, 64); err == nil {
		if n == 0 {
			*p.d = 0
			return nil
		}
		return fmt.Errorf("%q has no unit (did you mean %ss?)", s, s)
	}
	d, err := time.ParseDuration(s)
	if err != nil {
		return fmt.Errorf("%q is not a duration such as 30s or 2m", s)
	}
	if d < 0 {
		return fmt.Errorf("%q is negative", s)
	}
	*p.d = d
	return nil
}

func bindProfileFlags(fs *flag.FlagSet, opt *options) {
	fs.StringVar(&opt.profile, "profile", opt.profile, "profile to use (default: $SARAL_PROFILE, then the active one)")
	fs.StringVar(&opt.project, "project", opt.project, "project key to scope the session to; several capabilities are per-project")
	fs.StringVar(&opt.logFile, "log", opt.logFile, "write a debug log to this file; tokens and emails are redacted")
}

func rootFlags(opt *options) *flag.FlagSet {
	fs := flag.NewFlagSet("saral", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	fs.Usage = func() {}
	bindProfileFlags(fs, opt)
	fs.StringVar(&opt.theme, "theme", "", "auto, dark, light or no-color")
	fs.StringVar(&opt.scheme, "scheme", "", "default, nord, dracula, solarized or gruvbox")
	fs.StringVar(&opt.glyphs, "glyphs", "", "unicode, nerd or ascii; unicode is the default, nerd needs a Nerd Font")
	fs.Var(pollFlag{&opt.poll}, "poll", "re-read the focused view every `duration`, e.g. 30s; off by default, and pauses when Jira rate-limits")
	fs.BoolVar(&opt.mouse, "mouse", true, "enable mouse reporting")
	fs.BoolVar(&opt.fake, "fake", false, "demo mode: synthetic issues in memory, no site, no token, nothing saved")
	fs.BoolVar(&opt.showVer, "version", false, "print the version and exit")
	fs.BoolVar(&opt.benchPaint, "bench-first-paint", false, "render one frame, print how long it took, and exit")
	return fs
}

// parseRoot stops at a subcommand's name so the subcommand parses the rest.
func parseRoot(fs *flag.FlagSet, args []string) (positional, subArgs []string, sub *subcommand, err error) {
	for {
		if err := fs.Parse(args); err != nil {
			return positional, nil, nil, err
		}
		rest := fs.Args()
		if len(rest) == 0 {
			return positional, nil, nil, nil
		}
		if len(positional) == 0 {
			if s, ok := lookupSubcommand(rest[0]); ok {
				return nil, rest[1:], &s, nil
			}
		}
		consumedDashes := len(args) > len(rest) && args[len(args)-len(rest)-1] == "--"
		if consumedDashes {
			return append(positional, rest...), nil, nil, nil
		}
		positional = append(positional, rest[0])
		args = rest[1:]
	}
}

func (opt *options) validate() error {
	if opt.theme != "" {
		if _, err := kernel.ParseThemeMode(opt.theme); err != nil {
			return usageErrorf("--theme %q is not one of auto, dark, light, no-color", opt.theme)
		}
	}
	if opt.scheme != "" {
		if _, err := kernel.ParseScheme(opt.scheme); err != nil {
			names := make([]string, len(kernel.Schemes))
			for i := range kernel.Schemes {
				names[i] = kernel.Schemes[i].ID()
			}
			return usageErrorf("--scheme %q is not one of %s", opt.scheme, strings.Join(names, ", "))
		}
	}
	if opt.glyphs != "" && !slices.Contains(glyphTiers, strings.ToLower(strings.TrimSpace(opt.glyphs))) {
		return usageErrorf("--glyphs %q is not one of %s", opt.glyphs, strings.Join(glyphTiers, ", "))
	}
	return nil
}

func isHelp(err error) bool { return errors.Is(err, flag.ErrHelp) }

func flagError(err error) error {
	return usageErrorf("%v; run saral --help for the flags, views and commands", err)
}
