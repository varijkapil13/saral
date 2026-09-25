package main

import (
	"context"
	"flag"
	"strings"

	"github.com/varijkapil13/saral/pkg/adf"
)

type commentArgs struct{ message, file string }

func init() {
	registerGroup("comment", "add a comment written in Markdown: comment add KEY (-m text | --file F)",
		action{name: "add", run: runCommentAdd},
	)
	registerScriptFlags("comment add", func() *flag.FlagSet { return commentFlags(&options{}, &commentArgs{}) })
}

func commentFlags(opt *options, c *commentArgs) *flag.FlagSet {
	fs := scriptFlagSet("comment add", opt)
	fs.StringVar(&c.message, "m", "", "the comment, in Markdown")
	fs.StringVar(&c.file, "file", "", "a Markdown file to post; - reads stdin")
	return fs
}

func runCommentAdd(inv *invocation, args []string) error {
	opt := inv.opt
	var c commentArgs
	fs := commentFlags(&opt, &c)
	positional, helped, err := parseScript(inv, fs, args, "KEY (-m text | --file F) [flags]")
	if err != nil || helped {
		return err
	}
	if len(positional) != 1 {
		return usageErrorf("saral comment add takes one issue key, got %d arguments", len(positional))
	}
	var text string
	switch {
	case c.message != "" && c.file != "":
		return usageErrorf("saral comment add takes -m or --file, not both")
	case c.file != "":
		if text, err = readText(inv, "file", c.file); err != nil {
			return err
		}
	default:
		text = c.message
	}
	if strings.TrimSpace(text) == "" {
		return usageErrorf("saral comment add needs something to say: -m text, or --file F")
	}
	body, err := adf.ParseMarkdown(text)
	if err != nil {
		return usageErrorf("the comment is not Markdown saral can send: %v", err)
	}
	return withSession(inv, opt, func(ctx context.Context, s session) error {
		key, err := issueArg(positional[0], s.profile.Site)
		if err != nil {
			return err
		}
		added, err := s.client.AddComment(ctx, key, body)
		if err != nil {
			return siteError(err)
		}
		return writeRow(inv.stdout, key, added.ID)
	})
}
