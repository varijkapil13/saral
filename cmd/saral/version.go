package main

import (
	"cmp"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime/debug"

	"github.com/varijkapil13/saral/internal/config"
	"github.com/varijkapil13/saral/internal/ui/kernel"
)

var (
	version = "dev"
	commit  = "none"
	date    = "unknown"
)

// fileNameTOML is what config.Path builds; --version names it without asking
// for the path, which would fail on a machine with no home directory.
const fileNameTOML = "config.toml"

type buildStamp struct{ version, commit, date string }

// `go install …@v0.x.y` sets no ldflags, but the build info carries the same answer.
func stamp() buildStamp {
	info, ok := debug.ReadBuildInfo()
	if !ok {
		info = nil
	}
	return stampFrom(buildStamp{version: version, commit: commit, date: date}, info)
}

func stampFrom(s buildStamp, info *debug.BuildInfo) buildStamp {
	if info == nil {
		return s
	}
	if s.version == "dev" && info.Main.Version != "" && info.Main.Version != "(devel)" {
		s.version = info.Main.Version
	}
	for _, kv := range info.Settings {
		switch {
		case kv.Key == "vcs.revision" && s.commit == "none":
			s.commit = kv.Value
			if len(s.commit) > 12 {
				s.commit = s.commit[:12]
			}
		case kv.Key == "vcs.time" && s.date == "unknown":
			s.date = kv.Value
		}
	}
	return s
}

// buildKind must stay the judgement config.Dir makes, or the label lies about the directory.
func buildKind() string {
	if config.IsDevBuild() {
		return "dev"
	}
	return "release"
}

func init() {
	registerSubcommand(subcommand{
		name:    "version",
		summary: "print the version, where the config lives, and what the terminal reports",
		run: func(inv *invocation, _ []string) error {
			return printVersion(inv.stdout, inv.opt)
		},
	})
}

func printVersion(stdout io.Writer, opt options) error {
	s := stamp()
	_, err := fmt.Fprintf(stdout, "saral %s (%s, %s) %s\n%s\nglyphs %s · TERM %s · TERM_PROGRAM %s\n",
		s.version, s.commit, s.date, buildKind(), configNote(), glyphTier(opt),
		cmp.Or(os.Getenv("TERM"), "unset"), cmp.Or(os.Getenv("TERM_PROGRAM"), "unset"))
	return err
}

func configNote() string {
	dir, err := config.Dir()
	if err != nil {
		return "config nowhere: " + err.Error()
	}
	note := "config " + filepath.Join(dir, fileNameTOML)
	if config.IsDevBuild() {
		note += " (kept apart from an installed copy)"
	}
	return note
}

func glyphTier(opt options) string {
	var stored string
	if cfg, err := config.Load(); err == nil {
		if p, _, err := resolveProfile(cfg, opt.profile); err == nil {
			stored = p.Glyphs
		}
	}
	return kernel.GlyphsFor(cmp.Or(opt.glyphs, stored)).Tier()
}
