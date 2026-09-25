package main

import (
	"context"
	"flag"
	"fmt"
	"slices"
	"strings"

	"github.com/varijkapil13/saral/internal/app"
	"github.com/varijkapil13/saral/pkg/adf"
	"github.com/varijkapil13/saral/pkg/jira"
)

func init() {
	registerGroup("issue", "print one issue, or create one: issue view KEY, issue create --project P --type T --summary S",
		action{name: "create", run: runIssueCreate},
		action{name: "view", run: runIssueView},
	)
	registerScriptFlags("issue view", func() *flag.FlagSet { return issueViewFlags(&options{}, new(bool)) })
	registerScriptFlags("issue create", func() *flag.FlagSet { return issueCreateFlags(&options{}, &createArgs{}) })
}

func issueViewFlags(opt *options, asJSON *bool) *flag.FlagSet {
	fs := scriptFlagSet("issue view", opt)
	fs.BoolVar(asJSON, "json", false, "print the issue as JSON (docs/CLI.md has the schema)")
	return fs
}

func runIssueView(inv *invocation, args []string) error {
	opt := inv.opt
	var asJSON bool
	fs := issueViewFlags(&opt, &asJSON)
	positional, helped, err := parseScript(inv, fs, args, "KEY [flags]")
	if err != nil || helped {
		return err
	}
	if len(positional) != 1 {
		return usageErrorf("saral issue view takes one issue key, got %d arguments", len(positional))
	}
	return withSession(inv, opt, func(ctx context.Context, s session) error {
		key, err := issueArg(positional[0], s.profile.Site)
		if err != nil {
			return err
		}
		view, err := readIssue(ctx, s, key)
		if err != nil {
			return err
		}
		if asJSON {
			return writeJSON(inv.stdout, view.json())
		}
		return writeIssueText(inv, view)
	})
}

func readIssue(ctx context.Context, s session, key string) (issueView, error) {
	iss, labels, err := app.NewSearch(s.client).ReadIssue(ctx, s.client, key, app.DetailProjection())
	if err != nil {
		return issueView{}, siteError(err)
	}
	return issueView{issue: &iss, asked: iss.Requested.IDs(), labels: labels, site: s.profile.Site}, nil
}

func writeIssueText(inv *invocation, v issueView) error {
	if err := writeRow(inv.stdout, "key", v.issue.Key); err != nil {
		return err
	}
	if err := writeRow(inv.stdout, "url", issueURL(v.site, v.issue.Key)); err != nil {
		return err
	}
	for _, p := range platformFields {
		if p.id == "description" || !slices.Contains(v.asked, p.id) {
			continue
		}
		if err := writeRow(inv.stdout, p.name, p.text(v.issue)); err != nil {
			return err
		}
	}
	type custom struct{ name, value string }
	var rows []custom
	for _, id := range v.asked {
		if _, platform := platformFieldByID(id); platform {
			continue
		}
		if text := v.text(id); text != "" {
			rows = append(rows, custom{name: v.fieldName(id), value: text})
		}
	}
	slices.SortFunc(rows, func(a, b custom) int { return strings.Compare(strings.ToLower(a.name), strings.ToLower(b.name)) })
	for _, r := range rows {
		if err := writeRow(inv.stdout, r.name, r.value); err != nil {
			return err
		}
	}
	description := descriptionText(v.issue.Description)
	if description == "" {
		return nil
	}
	var b strings.Builder
	b.WriteString("\n")
	for line := range strings.SplitSeq(description, "\n") {
		b.WriteString(cell(line))
		b.WriteString("\n")
	}
	_, err := fmt.Fprint(inv.stdout, b.String())
	return err
}

type createArgs struct {
	issueType, summary, descriptionFile, parent string
	asJSON                                      bool
}

func issueCreateFlags(opt *options, c *createArgs) *flag.FlagSet {
	fs := scriptFlagSet("issue create", opt)
	fs.StringVar(&c.issueType, "type", "", "the issue type, by name or ID as this project has it")
	fs.StringVar(&c.summary, "summary", "", "the summary")
	fs.StringVar(&c.descriptionFile, "description-file", "", "a Markdown file for the description; - reads stdin")
	fs.StringVar(&c.parent, "parent", "", "the parent issue's key, which a subtask type needs")
	fs.BoolVar(&c.asJSON, "json", false, "print the new issue's key, ID and URL as JSON")
	return fs
}

func runIssueCreate(inv *invocation, args []string) error {
	opt := inv.opt
	var c createArgs
	fs := issueCreateFlags(&opt, &c)
	positional, helped, err := parseScript(inv, fs, args, "--project P --type T --summary S [flags]")
	if err != nil || helped {
		return err
	}
	if len(positional) > 0 {
		return usageErrorf("saral issue create takes flags only, got %s", strings.Join(positional, " "))
	}
	c.summary = strings.TrimSpace(c.summary)
	switch {
	case c.summary == "":
		return usageErrorf("saral issue create needs --summary")
	case strings.TrimSpace(c.issueType) == "":
		return usageErrorf("saral issue create needs --type")
	}
	var in jira.IssueInput
	in.Summary = c.summary
	if c.parent != "" {
		parent, ok := app.ParseKey(c.parent)
		if !ok {
			return usageErrorf("--parent %q is not an issue key", c.parent)
		}
		in.ParentKey = parent
	}
	if c.descriptionFile != "" {
		text, err := readText(inv, "description-file", c.descriptionFile)
		if err != nil {
			return err
		}
		if in.Description, err = adf.ParseMarkdown(text); err != nil {
			return usageErrorf("--description-file %s: %v", c.descriptionFile, err)
		}
	}
	return withSession(inv, opt, func(ctx context.Context, s session) error {
		if s.profile.Project == "" {
			return usageErrorf("saral issue create needs --project, or a project on the profile")
		}
		in.ProjectKey = s.profile.Project
		typeID, err := issueTypeID(ctx, s.client, in.ProjectKey, c.issueType)
		if err != nil {
			return err
		}
		in.IssueTypeID = typeID
		created, err := s.client.CreateIssue(ctx, in)
		if err != nil {
			return siteError(err)
		}
		link := issueURL(s.profile.Site, created.Key)
		if c.asJSON {
			return writeJSON(inv.stdout, struct {
				Key string `json:"key"`
				ID  string `json:"id"`
				URL string `json:"url"`
			}{created.Key, created.ID, link})
		}
		return writeRow(inv.stdout, created.Key, link)
	})
}

func issueTypeID(ctx context.Context, vocab jira.FilterVocabulary, project, wanted string) (string, error) {
	byType, err := vocab.IssueTypeStatuses(ctx, project)
	if err != nil {
		return "", siteError(err)
	}
	wanted = strings.TrimSpace(wanted)
	var matches []jira.IssueType
	names := make([]string, 0, len(byType))
	for _, t := range byType {
		names = append(names, t.Type.Name)
		if t.Type.ID == wanted {
			return t.Type.ID, nil
		}
		if strings.EqualFold(t.Type.Name, wanted) {
			matches = append(matches, t.Type)
		}
	}
	switch len(matches) {
	case 1:
		return matches[0].ID, nil
	case 0:
		return "", usageErrorf("%s has no issue type %q; it has %s", project, wanted, strings.Join(names, ", "))
	default:
		ids := make([]string, len(matches))
		for i := range matches {
			ids[i] = matches[i].ID
		}
		return "", usageErrorf("%s has %d issue types called %q; pass one of these IDs to --type: %s",
			project, len(matches), wanted, strings.Join(ids, ", "))
	}
}
