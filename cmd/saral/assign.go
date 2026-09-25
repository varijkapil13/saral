package main

import (
	"context"
	"flag"
	"fmt"
	"strings"
	"unicode"

	"github.com/varijkapil13/saral/internal/app"
	"github.com/varijkapil13/saral/pkg/jira"
)

const (
	assignMe     = "me"
	assignNobody = "none"
	maxNamed     = 10
)

func init() {
	registerSubcommand(subcommand{
		name:    "assign",
		summary: "assign an issue: assign KEY (me | none | account ID | a name or email to search for)",
		run:     runAssign,
	})
	registerScriptFlags("assign", func() *flag.FlagSet { return scriptFlagSet("assign", &options{}) })
}

func runAssign(inv *invocation, args []string) error {
	opt := inv.opt
	fs := scriptFlagSet("assign", &opt)
	positional, helped, err := parseScript(inv, fs, args, "KEY (me | none | accountId | name or email)")
	if err != nil || helped {
		return err
	}
	if len(positional) < 2 {
		return usageErrorf("saral assign takes an issue key and who to assign it to: me, none, an account ID, or a name to search for")
	}
	who := strings.TrimSpace(strings.Join(positional[1:], " "))
	return withSession(inv, opt, func(ctx context.Context, s session) error {
		key, err := issueArg(positional[0], s.profile.Site)
		if err != nil {
			return err
		}
		user, err := assignee(ctx, s.client, key, who)
		if err != nil {
			return err
		}
		id := ""
		if user != nil {
			id = user.AccountID
		}
		if err := app.SaveIssue(ctx, s.client, key, app.EditBase{}, jira.IssuePatch{Assignee: &id}); err != nil {
			return siteError(err)
		}
		if user == nil {
			return writeRow(inv.stdout, key, "unassigned")
		}
		return writeRow(inv.stdout, key, user.DisplayName, user.AccountID)
	})
}

type assignClient interface {
	jira.Identifier
	jira.PeopleFinder
}

// assignee is nil for nobody.
func assignee(ctx context.Context, client assignClient, key, who string) (*jira.User, error) {
	switch strings.ToLower(who) {
	case assignMe:
		me, err := client.Me(ctx)
		if err != nil {
			return nil, siteError(err)
		}
		return &me, nil
	case assignNobody:
		return nil, nil
	}
	if accountShaped(who) {
		found, err := client.People(ctx, []string{who})
		if err != nil {
			return nil, siteError(err)
		}
		if len(found) == 1 {
			return &found[0], nil
		}
	}
	project, _, _ := strings.Cut(key, "-")
	found, err := client.FindPeople(ctx, jira.PeopleQuery{Match: who, Project: project})
	if err != nil {
		return nil, siteError(err)
	}
	var exact []jira.User
	for _, u := range found {
		if strings.EqualFold(u.Email, who) || strings.EqualFold(u.DisplayName, who) {
			exact = append(exact, u)
		}
	}
	switch {
	case len(found) == 1:
		return &found[0], nil
	case len(exact) == 1:
		return &exact[0], nil
	case len(found) == 0:
		return nil, usageErrorf("nobody who can be assigned on %s matches %q", project, who)
	default:
		return nil, usageErrorf("%q matches %d people on %s: %s; pass an account ID", who, len(found), project, describePeople(found))
	}
}

// Atlassian account IDs carry digits or a colon; names and emails rarely do.
func accountShaped(s string) bool {
	if strings.ContainsFunc(s, unicode.IsSpace) || strings.Contains(s, "@") {
		return false
	}
	return strings.ContainsFunc(s, func(r rune) bool { return unicode.IsDigit(r) || r == ':' })
}

func describePeople(people []jira.User) string {
	parts := make([]string, 0, min(len(people), maxNamed))
	for i := range people {
		if i == maxNamed {
			parts = append(parts, fmt.Sprintf("and %d more", len(people)-maxNamed))
			break
		}
		parts = append(parts, fmt.Sprintf("%s (%s)", people[i].DisplayName, people[i].AccountID))
	}
	return strings.Join(parts, ", ")
}
