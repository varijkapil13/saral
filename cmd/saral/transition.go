package main

import (
	"context"
	"flag"
	"fmt"
	"strings"

	"github.com/varijkapil13/saral/pkg/jira"
)

func init() {
	registerSubcommand(subcommand{
		name:    "transition",
		summary: "move an issue through its workflow: transition KEY 'status or transition name'",
		run:     runTransition,
	})
	registerScriptFlags("transition", func() *flag.FlagSet { return transitionFlags(&options{}, &multiFlag{}) })
}

func transitionFlags(opt *options, fields *multiFlag) *flag.FlagSet {
	fs := scriptFlagSet("transition", opt)
	fs.Var(fields, "field", "a field the transition's screen asks for, as `Name=Value`; repeat for more")
	return fs
}

func runTransition(inv *invocation, args []string) error {
	opt := inv.opt
	var fields multiFlag
	fs := transitionFlags(&opt, &fields)
	positional, helped, err := parseScript(inv, fs, args, "KEY 'status or transition name' [flags]")
	if err != nil || helped {
		return err
	}
	if len(positional) < 2 {
		return usageErrorf("saral transition takes an issue key and where to move it")
	}
	target := strings.TrimSpace(strings.Join(positional[1:], " "))
	return withSession(inv, opt, func(ctx context.Context, s session) error {
		key, err := issueArg(positional[0], s.profile.Site)
		if err != nil {
			return err
		}
		moves, err := s.client.Transitions(ctx, key)
		if err != nil {
			return siteError(err)
		}
		move, err := pickTransition(key, moves, target)
		if err != nil {
			return err
		}
		patch, err := screenPatch(move, fields)
		if err != nil {
			return err
		}
		if err := s.client.Transition(ctx, key, move.ID, patch); err != nil {
			return siteError(err)
		}
		return writeRow(inv.stdout, key, move.To.Name)
	})
}

// A transition's own name wins: two transitions can lead to one status.
func pickTransition(key string, moves []jira.Transition, target string) (jira.Transition, error) {
	var byName, byStatus []jira.Transition
	for _, tr := range moves {
		switch {
		case tr.ID == target || strings.EqualFold(tr.Name, target):
			byName = append(byName, tr)
		case strings.EqualFold(tr.To.Name, target):
			byStatus = append(byStatus, tr)
		}
	}
	for _, found := range [][]jira.Transition{byName, byStatus} {
		switch len(found) {
		case 0:
			continue
		case 1:
			return found[0], nil
		default:
			return jira.Transition{}, usageErrorf("%q is %d moves on %s: %s; name the transition", target, len(found), key, describeMoves(found))
		}
	}
	if len(moves) == 0 {
		return jira.Transition{}, usageErrorf("%s has no moves available to this account right now", key)
	}
	return jira.Transition{}, usageErrorf("%s cannot move to %q from where it is; it can take %s", key, target, describeMoves(moves))
}

func describeMoves(moves []jira.Transition) string {
	parts := make([]string, len(moves))
	for i, tr := range moves {
		parts[i] = fmt.Sprintf("%q (to %s)", tr.Name, tr.To.Name)
	}
	return strings.Join(parts, ", ")
}

func screenPatch(move jira.Transition, given multiFlag) (jira.IssuePatch, error) {
	values := make(map[string]jira.FieldValue, len(given))
	for _, raw := range given {
		name, value, ok := strings.Cut(raw, "=")
		name, value = strings.TrimSpace(name), strings.TrimSpace(value)
		if !ok || name == "" || value == "" {
			return jira.IssuePatch{}, usageErrorf("--field %q is not Name=Value", raw)
		}
		meta, ok := screenField(move, name)
		if !ok {
			return jira.IssuePatch{}, usageErrorf("%q asks for no field called %s; its screen has %s", move.Name, name, screenFieldNames(move))
		}
		if len(meta.AllowedValues) == 0 {
			return jira.IssuePatch{}, usageErrorf("%s takes free text, which saral transition cannot fill; move the issue in saral instead", fieldLabel(&meta))
		}
		option, ok := allowedOption(meta, value)
		if !ok {
			return jira.IssuePatch{}, usageErrorf("%s cannot be %q; it takes %s", fieldLabel(&meta), value, optionLabels(&meta))
		}
		values[meta.Field.ID] = jira.FieldValue{Kind: jira.KindOption, Options: []jira.Option{option}}
	}
	for i := range move.Fields {
		meta := &move.Fields[i]
		if _, set := values[meta.Field.ID]; set || !meta.Required || meta.HasDefault {
			continue
		}
		if len(meta.AllowedValues) == 0 {
			return jira.IssuePatch{}, usageErrorf("%q needs %s, which takes free text that saral transition cannot fill", move.Name, fieldLabel(meta))
		}
		return jira.IssuePatch{}, usageErrorf("%q needs %s: pass --field '%s=<value>' with one of %s",
			move.Name, fieldLabel(meta), fieldLabel(meta), optionLabels(meta))
	}
	if len(values) == 0 {
		return jira.IssuePatch{}, nil
	}
	return jira.IssuePatch{Fields: jira.NewFieldSet(values)}, nil
}

func screenField(move jira.Transition, name string) (jira.FieldMeta, bool) {
	for i := range move.Fields {
		meta := &move.Fields[i]
		if strings.EqualFold(meta.Name, name) || strings.EqualFold(meta.Field.Name, name) || meta.Field.ID == name {
			return *meta, true
		}
	}
	return jira.FieldMeta{}, false
}

func screenFieldNames(move jira.Transition) string {
	if len(move.Fields) == 0 {
		return "no fields"
	}
	names := make([]string, len(move.Fields))
	for i := range move.Fields {
		names[i] = fieldLabel(&move.Fields[i])
	}
	return strings.Join(names, ", ")
}

func fieldLabel(meta *jira.FieldMeta) string {
	if meta.Name != "" {
		return meta.Name
	}
	if meta.Field.Name != "" {
		return meta.Field.Name
	}
	return meta.Field.ID
}

func allowedOption(meta jira.FieldMeta, value string) (jira.Option, bool) {
	for _, o := range meta.AllowedValues {
		if o.ID == value {
			return o, true
		}
	}
	for _, o := range meta.AllowedValues {
		if strings.EqualFold(o.Label, value) {
			return o, true
		}
	}
	return jira.Option{}, false
}

func optionLabels(meta *jira.FieldMeta) string {
	labels := make([]string, len(meta.AllowedValues))
	for i := range meta.AllowedValues {
		labels[i] = meta.AllowedValues[i].Label
	}
	return strings.Join(labels, ", ")
}
