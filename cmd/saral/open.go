package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"os/exec"
	"runtime"

	"github.com/varijkapil13/saral/internal/ui/kernel"
)

var openInBrowser = func(ctx context.Context, link string) error {
	var cmd *exec.Cmd
	switch runtime.GOOS {
	case "darwin":
		cmd = exec.CommandContext(ctx, "open", link)
	case "windows":
		cmd = exec.CommandContext(ctx, "rundll32", "url.dll,FileProtocolHandler", link)
	case "linux", "freebsd", "netbsd", "openbsd", "dragonfly":
		cmd = exec.CommandContext(ctx, "xdg-open", link)
	default:
		return errors.New("saral does not know how to open a link on " + runtime.GOOS)
	}
	return cmd.Run()
}

func init() {
	registerSubcommand(subcommand{
		name:    "open",
		summary: "open an issue in the browser and print its link: open KEY [--print]",
		run:     runOpen,
	})
	registerScriptFlags("open", func() *flag.FlagSet { return openFlags(&options{}, new(bool)) })
}

func openFlags(opt *options, printOnly *bool) *flag.FlagSet {
	fs := scriptFlagSet("open", opt)
	fs.BoolVar(printOnly, "print", false, "print the link without opening it")
	return fs
}

func runOpen(inv *invocation, args []string) error {
	opt := inv.opt
	var printOnly bool
	fs := openFlags(&opt, &printOnly)
	positional, helped, err := parseScript(inv, fs, args, "KEY [flags]")
	if err != nil || helped {
		return err
	}
	if len(positional) != 1 {
		return usageErrorf("saral open takes one issue key, got %d arguments", len(positional))
	}
	profile, err := scriptProfile(opt)
	if err != nil {
		return err
	}
	key, err := issueArg(positional[0], profile.Site)
	if err != nil {
		return err
	}
	link, err := kernel.IssueURL(profile.Site, key)
	if err != nil {
		return withCode(exitConfig, err)
	}
	if err := writeRow(inv.stdout, link); err != nil {
		return err
	}
	if printOnly {
		return nil
	}
	if err := openInBrowser(context.Background(), link); err != nil {
		return withCode(exitOther, fmt.Errorf("could not open a browser: %w", err))
	}
	return nil
}
