package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/signal"
	"slices"
	"strings"
	"text/tabwriter"

	"github.com/varijkapil13/saral/internal/app"
	"github.com/varijkapil13/saral/internal/config"
	"github.com/varijkapil13/saral/pkg/jira"
)

var scriptFlags = map[string]func() *flag.FlagSet{}

var scriptGroups = map[string][]string{}

func registerScriptFlags(path string, build func() *flag.FlagSet) {
	if _, dup := scriptFlags[path]; dup {
		panic("saral: flags for " + path + " are registered twice")
	}
	scriptFlags[path] = build
}

func scriptFlagSet(path string, opt *options) *flag.FlagSet {
	fs := flag.NewFlagSet("saral "+path, flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	fs.Usage = func() {}
	bindProfileFlags(fs, opt)
	return fs
}

func parseScript(inv *invocation, fs *flag.FlagSet, args []string, usage string) (positional []string, helped bool, err error) {
	for {
		if perr := fs.Parse(args); perr != nil {
			if isHelp(perr) {
				return nil, true, printScriptUsage(inv.stdout, fs, usage)
			}
			return nil, false, usageErrorf("%v; run %s --help", perr, fs.Name())
		}
		rest := fs.Args()
		if len(rest) == 0 {
			return positional, false, nil
		}
		if len(args) > len(rest) && args[len(args)-len(rest)-1] == "--" {
			return append(positional, rest...), false, nil
		}
		positional = append(positional, rest[0])
		args = rest[1:]
	}
}

func printScriptUsage(w io.Writer, fs *flag.FlagSet, usage string) error {
	tw := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
	_, _ = fmt.Fprintf(tw, "usage: %s %s\n\nflags:\n", fs.Name(), usage)
	fs.VisitAll(func(f *flag.Flag) {
		name, text := flag.UnquoteUsage(f)
		_, _ = fmt.Fprintf(tw, "  --%s\t%s\t%s\n", f.Name, name, text)
	})
	return tw.Flush()
}

type action struct {
	name string
	run  func(inv *invocation, args []string) error
}

func registerGroup(name, summary string, actions ...action) {
	words := make([]string, len(actions))
	for i := range actions {
		words[i] = actions[i].name
	}
	scriptGroups[name] = words
	registerSubcommand(subcommand{
		name:    name,
		summary: summary,
		run: func(inv *invocation, args []string) error {
			return dispatch(inv, name, actions, args)
		},
	})
}

func dispatch(inv *invocation, group string, actions []action, args []string) error {
	words := scriptGroups[group]
	if len(args) == 0 {
		return usageErrorf("saral %s needs one of: %s", group, strings.Join(words, ", "))
	}
	switch args[0] {
	case "-h", "-help", "--help", "help":
		_, err := fmt.Fprintf(inv.stdout, "usage: saral %s <%s> [flags]\nrun saral %s <%s> --help for each one's flags\n",
			group, strings.Join(words, "|"), group, strings.Join(words, "|"))
		return err
	}
	at := slices.IndexFunc(actions, func(a action) bool { return a.name == args[0] })
	if at < 0 {
		return usageErrorf("saral %s has no %q; it takes %s", group, args[0], strings.Join(words, ", "))
	}
	return actions[at].run(inv, args[1:])
}

type session struct {
	client  jira.SessionClient
	profile config.Profile
}

func withSession(inv *invocation, opt options, do func(ctx context.Context, s session) error) error {
	closeLog, err := openScriptLog(inv, &opt)
	if err != nil {
		return err
	}
	defer closeLog()
	profile, err := scriptProfile(opt)
	if err != nil {
		return err
	}
	client, err := scriptClient(opt, profile)
	if err != nil {
		return err
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()
	return do(ctx, session{client: client, profile: profile})
}

func openScriptLog(inv *invocation, opt *options) (func(), error) {
	if opt.logFile == "" || opt.logFile == inv.opt.logFile {
		return func() {}, nil
	}
	logger, closeLog, err := openLog(opt.logFile)
	if err != nil {
		return func() {}, err
	}
	opt.logger = logger
	return closeLog, nil
}

func scriptProfile(opt options) (config.Profile, error) {
	cfg, err := config.Load()
	switch {
	case errors.Is(err, config.ErrNoConfig):
		cfg = config.Config{}
	case err != nil:
		return config.Profile{}, withCode(exitConfig, err)
	}
	profile, _, err := resolveProfile(cfg, opt.profile)
	if err != nil {
		if errors.Is(err, config.ErrNoProfile) {
			err = fmt.Errorf("%w; run saral to set one up, or set %s, %s and %s", err, envSite, envEmail, envToken)
		}
		return config.Profile{}, withCode(exitConfig, err)
	}
	project, err := sessionProject(opt.project, profile.Project)
	if err != nil {
		return config.Profile{}, withCode(exitUsage, err)
	}
	profile.Project = project
	return profile, nil
}

func scriptClient(opt options, profile config.Profile) (jira.SessionClient, error) {
	if opt.connectVia != nil {
		client, err := opt.connectVia(profile)
		if err != nil {
			return nil, withCode(exitAuth, err)
		}
		return client, nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), tokenTimeout)
	defer cancel()
	token, err := profile.ResolveToken(ctx)
	if err != nil {
		return nil, withCode(exitAuth, fmt.Errorf("the token from %s: %w", profile.Token, err))
	}
	client, err := connectWith(opt.logger)(profile.Site, profile.Email, token)
	if err != nil {
		return nil, withCode(exitConfig, err)
	}
	return client, nil
}

func siteError(err error) error { return withCode(siteFailure(err), err) }

// A link to another site is refused: the same key here may be somebody else's issue.
func issueArg(arg, site string) (string, error) {
	if key, ok := app.ParseKey(arg); ok {
		return key, nil
	}
	if key, host, ok := app.ParseIssueURL(arg); ok {
		here, err := config.NormalizeSite(site)
		if err == nil && !strings.EqualFold(here, host) {
			return "", usageErrorf("%s is on %s and this profile is on %s", key, host, here)
		}
		return key, nil
	}
	return "", usageErrorf("%q is not an issue key such as PROJ-142, or a link to one", arg)
}

func readText(inv *invocation, flagName, path string) (string, error) {
	var (
		b   []byte
		err error
	)
	if path == "-" {
		b, err = io.ReadAll(inv.input())
	} else {
		b, err = os.ReadFile(path)
	}
	if err != nil {
		return "", usageErrorf("--%s %s: %v", flagName, path, err)
	}
	return string(b), nil
}

type multiFlag []string

func (m *multiFlag) String() string { return strings.Join(*m, ", ") }

func (m *multiFlag) Set(s string) error {
	*m = append(*m, s)
	return nil
}
