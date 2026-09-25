package main

import (
	"flag"
	"fmt"
	"io"
	"path/filepath"
	"text/tabwriter"

	"github.com/varijkapil13/saral/internal/config"
)

func init() {
	registerSubcommand(subcommand{
		name:    "help",
		summary: "print this help",
		run: func(inv *invocation, _ []string) error {
			return printHelp(inv.stdout, rootFlags(&options{}))
		},
	})
}

func printHelp(w io.Writer, fs *flag.FlagSet) error {
	tw := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
	p := func(format string, args ...any) { _, _ = fmt.Fprintf(tw, format, args...) }

	p("saral is a terminal client for Jira Cloud.\n\n")
	p("usage:\n")
	p("  saral [flags] [view | issue key | Jira URL]\n")
	p("  saral <command> [flags]\n\n")

	p("views:\n")
	for _, spec := range openableViews() {
		key := ""
		if spec.Slot > 0 {
			key = fmt.Sprintf("g %d", spec.Slot)
		}
		p("  %s\t%s\t%s\n", spec.ID, spec.Title, key)
	}
	p("\ncommands:\n")
	for _, name := range subcommandNames() {
		s, _ := lookupSubcommand(name)
		p("  %s\t%s\n", s.name, s.summary)
	}

	p("\nexamples:\n")
	p("  saral\topen where you left off, or setup on a first run\n")
	p("  saral board --project PROJ\tthe board, scoped to one project\n")
	p("  saral PROJ-142\tone issue; a pasted Jira URL works too\n")
	p("\ninside:\n")
	p("  ?\tevery key the view in front of you takes\n")
	p("  ctrl+k\tthe command palette: every action, view and issue on disk\n")

	p("\nflags:\n")
	fs.VisitAll(func(f *flag.Flag) {
		if hiddenFlags[f.Name] {
			return
		}
		name, usage := flag.UnquoteUsage(f)
		p("  --%s\t%s\t%s\n", f.Name, name, usage)
	})

	p("\nfiles:\n")
	if dir, err := config.Dir(); err == nil {
		p("  config\t%s\n", filepath.Join(dir, fileNameTOML))
	}
	if dir, err := config.CacheDir(); err == nil {
		p("  cache\t%s\n", filepath.Join(dir, cacheFile))
	}

	p("\nenvironment:\n")
	for _, row := range [][2]string{
		{"SARAL_CONFIG_DIR", "where config.toml lives"},
		{"SARAL_CACHE_DIR", "where the cache lives"},
		{envProfile, "the profile to use when --profile is not given"},
		{envSite + ", " + envEmail, "override the profile's site and email, or stand in for a config file"},
		{envToken, "the API token; read from the environment only, never written to the file"},
		{"HTTPS_PROXY, NO_PROXY", "the proxy requests go through"},
		{"NO_COLOR", "turn colour off whatever the theme says"},
	} {
		p("  %s\t%s\n", row[0], row[1])
	}
	p("  precedence: flags, then the environment, then config.toml\n")

	p("\nexit codes:\n")
	for _, row := range []struct {
		code int
		what string
	}{
		{exitOK, "success"},
		{exitOther, "anything else: the site could not be reached, a request failed"},
		{exitUsage, "a flag or argument was not understood"},
		{exitConfig, "config.toml or the profile is unusable"},
		{exitAuth, "the token could not be found, or the site refused it"},
	} {
		p("  %d\t%s\n", row.code, row.what)
	}
	if err := tw.Flush(); err != nil {
		return err
	}
	return nil
}
