package main

import (
	"context"
	"errors"
	"flag"
	"slices"
	"strings"

	"github.com/varijkapil13/saral/internal/app"
	"github.com/varijkapil13/saral/pkg/jira"
)

const (
	defaultSearchLimit = 50
	searchPageSize     = 100
)

var searchColumns = []string{"issuetype", "status", "priority", "assignee", "updated", "summary"}

type searchArgs struct {
	asJSON bool
	limit  int
	fields string
}

func init() {
	registerSubcommand(subcommand{
		name:    "search",
		summary: "run JQL and print one issue per line, or JSON: search 'JQL' [--fields F,G] [--limit N]",
		run:     runSearch,
	})
	registerScriptFlags("search", func() *flag.FlagSet { return searchFlags(&options{}, &searchArgs{}) })
}

func searchFlags(opt *options, a *searchArgs) *flag.FlagSet {
	fs := scriptFlagSet("search", opt)
	fs.BoolVar(&a.asJSON, "json", false, "print the issues as JSON (docs/CLI.md has the schema)")
	fs.IntVar(&a.limit, "limit", defaultSearchLimit, "print at most this many issues")
	fs.StringVar(&a.fields, "fields", "", "comma-separated fields to print, by ID or by the name this site gives them")
	return fs
}

func runSearch(inv *invocation, args []string) error {
	opt := inv.opt
	var a searchArgs
	fs := searchFlags(&opt, &a)
	positional, helped, err := parseScript(inv, fs, args, "'JQL' [flags]")
	if err != nil || helped {
		return err
	}
	if len(positional) != 1 {
		return usageErrorf("saral search takes the JQL as one argument, quoted, or - to read it from stdin; got %d arguments", len(positional))
	}
	if a.limit <= 0 {
		return usageErrorf("--limit %d: it has to be at least 1", a.limit)
	}
	jql := positional[0]
	if jql == "-" {
		if jql, err = readText(inv, "jql", "-"); err != nil {
			return err
		}
	}
	jql = strings.TrimSpace(jql)
	if jql == "" {
		return usageErrorf("saral search needs a JQL query")
	}
	return withSession(inv, opt, func(ctx context.Context, s session) error {
		search := app.NewSearch(s.client)
		ids, labels, err := searchFields(ctx, search, a.fields)
		if err != nil {
			return err
		}
		issues, more, err := collectIssues(ctx, search, jql, ids, a.limit)
		if err != nil {
			return siteError(err)
		}
		if a.asJSON {
			out := struct {
				Issues []issueJSON `json:"issues"`
				More   bool        `json:"more"`
			}{Issues: make([]issueJSON, len(issues)), More: more}
			for i := range issues {
				out.Issues[i] = issueView{issue: &issues[i], asked: ids, labels: labels, site: s.profile.Site}.json()
			}
			return writeJSON(inv.stdout, out)
		}
		row := make([]string, 0, len(ids)+1)
		for i := range issues {
			view := issueView{issue: &issues[i], asked: ids, labels: labels, site: s.profile.Site}
			row = append(row[:0], issues[i].Key)
			for _, id := range ids {
				row = append(row, view.text(id))
			}
			if err := writeRow(inv.stdout, row...); err != nil {
				return err
			}
		}
		return nil
	})
}

func searchFields(ctx context.Context, search *app.Search, spec string) ([]string, app.FieldLabels, error) {
	var wanted []string
	for part := range strings.SplitSeq(spec, ",") {
		if part = strings.TrimSpace(part); part != "" && !strings.EqualFold(part, "key") {
			wanted = append(wanted, part)
		}
	}
	if len(wanted) == 0 {
		return slices.Clone(searchColumns), app.FieldLabels{}, nil
	}
	ids := make([]string, 0, len(wanted))
	var catalogue []jira.Field
	for _, name := range wanted {
		if _, ok := platformFieldByID(name); ok {
			ids = appendNew(ids, name)
			continue
		}
		if catalogue == nil {
			fields, err := search.Fields(ctx)
			if err != nil {
				return nil, app.FieldLabels{}, siteError(err)
			}
			catalogue = fields
		}
		if at := slices.IndexFunc(catalogue, func(f jira.Field) bool { return f.ID == name }); at >= 0 {
			ids = appendNew(ids, name)
			continue
		}
		field, err := jira.ResolveField(catalogue, name)
		if err != nil {
			var named *jira.FieldNameError
			if errors.As(err, &named) {
				return nil, app.FieldLabels{}, usageErrorf("--fields: %v", err)
			}
			return nil, app.FieldLabels{}, err
		}
		ids = appendNew(ids, field.ID)
	}
	return ids, app.NewFieldLabels(catalogue, ids), nil
}

func appendNew(ids []string, id string) []string {
	if slices.Contains(ids, id) {
		return ids
	}
	return append(ids, id)
}

func collectIssues(ctx context.Context, search *app.Search, jql string, ids []string, limit int) (issues []jira.Issue, more bool, err error) {
	result, err := search.Run(ctx, app.Request{
		JQL:        jql,
		Projection: app.Projection{Name: "search", IDs: ids},
		MaxResults: min(limit, searchPageSize),
	})
	if err != nil {
		return nil, false, err
	}
	page := result.Page
	for {
		issues = append(issues, page.Items...)
		if len(issues) >= limit {
			return issues[:limit], len(issues) > limit || page.HasMore(), nil
		}
		if !page.HasMore() {
			return issues, false, nil
		}
		if page, err = page.Next(ctx); err != nil {
			return nil, false, err
		}
	}
}
