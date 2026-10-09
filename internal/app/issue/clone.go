package issue

import (
	"context"
	"strings"

	"github.com/varijkapil13/saral/pkg/jira"
)

// CloneReader is what reading an issue to copy runs on.
type CloneReader interface {
	jira.IssueReader
	jira.SchemaReader
	jira.Linker
}

// CloneSource is an issue to copy and what its copy would be.
type CloneSource struct {
	Src     jira.Issue
	Input   jira.IssueInput
	Carried []string
	Types   []jira.LinkType
}

// ReadClone reads an issue, its type's create screen, and the kinds of link
// when it has links to copy. The kinds are best effort: without them the links
// are simply not offered.
func ReadClone(ctx context.Context, c CloneReader, key string) (CloneSource, error) {
	src, err := c.Issue(ctx, key)
	if err != nil {
		return CloneSource{}, err
	}
	schema, err := c.CreateMeta(ctx, src.Project.Key, src.Type.ID)
	if err != nil {
		return CloneSource{}, err
	}
	var types []jira.LinkType
	if len(src.Links) > 0 {
		types, _ = c.IssueLinkTypes(ctx)
	}
	in, names := CloneInput(src, schema)
	return CloneSource{Src: src, Input: in, Carried: names, Types: types}, nil
}

// CloneInput is the copy's create request, and the names of what it carries.
// Only fields the create screen names go in: anything else is refused by the
// site, and the rest of the copy with it.
func CloneInput(src jira.Issue, schema jira.Schema) (in jira.IssueInput, names []string) {
	in = jira.IssueInput{ProjectKey: src.Project.Key, IssueTypeID: src.Type.ID}
	fields := jira.FieldSet{}
	for i := range schema.Fields {
		meta := &schema.Fields[i]
		v, carried := typedValue(&src, meta.Field.ID, &in)
		switch {
		case carried:
		case v.Kind != jira.KindEmpty:
			fields = fields.With(meta.Field, v)
			carried = true
		default:
			if custom, ok := src.Fields.ByID(meta.Field.ID); ok && copyable(custom.Kind) {
				fields = fields.With(meta.Field, custom)
				carried = true
			}
		}
		if carried {
			names = append(names, firstNonEmpty(meta.Name, meta.Field.Name, meta.Field.ID))
		}
	}
	in.Fields = fields
	return in, names
}

func firstNonEmpty(s ...string) string {
	for _, v := range s {
		if strings.TrimSpace(v) != "" {
			return v
		}
	}
	return ""
}

func typedValue(src *jira.Issue, id string, in *jira.IssueInput) (jira.FieldValue, bool) {
	var v jira.FieldValue
	switch id {
	case "description":
		in.Description = src.Description
		return v, !src.Description.IsZero()
	case "labels":
		in.Labels = src.Labels
		return v, len(src.Labels) > 0
	case "assignee":
		if src.Assignee != nil {
			in.Assignee = src.Assignee.AccountID
		}
		return v, src.Assignee != nil
	case "parent":
		if src.Parent != nil {
			in.ParentKey = src.Parent.Key
		}
		return v, src.Parent != nil
	case "summary", "project", "issuetype", "reporter", "issuelinks", "attachment", "timetracking":
		return v, false
	case "priority":
		if src.Priority != nil {
			v = jira.FieldValue{Kind: jira.KindOption, Options: []jira.Option{{ID: src.Priority.ID}}}
		}
	case "components":
		for _, c := range src.Components {
			v.Options = append(v.Options, jira.Option{ID: c.ID})
		}
	case "fixVersions":
		for i := range src.FixVersions {
			v.Options = append(v.Options, jira.Option{ID: src.FixVersions[i].ID})
		}
	case "duedate":
		if !src.Due.IsZero() {
			v = jira.FieldValue{Kind: jira.KindDate, Date: src.Due}
		}
	}
	if v.Kind == jira.KindEmpty && len(v.Options) > 0 {
		v.Kind = jira.KindOptions
	}
	return v, false
}

// copyable is a value kind that is written back the way it was read. An untyped
// value is not: a sprint reads as an object and is written as a number.
func copyable(k jira.FieldKind) bool {
	switch k {
	case jira.KindText, jira.KindNumber, jira.KindBool, jira.KindDate, jira.KindTime,
		jira.KindDoc, jira.KindOption, jira.KindOptions, jira.KindUser, jira.KindUsers:
		return true
	default:
		return false
	}
}

// Cloner is what creating a copy, and copying its links, runs on.
type Cloner interface {
	jira.IssueWriter
	jira.Linker
}

// Clone creates the copy, and copies src's links onto it when links is set.
// failed is how many links could not be copied.
func Clone(ctx context.Context, c Cloner, in jira.IssueInput, src jira.Issue, types []jira.LinkType, links bool) (made jira.Issue, failed int, err error) {
	made, err = c.CreateIssue(ctx, in)
	if err != nil {
		return jira.Issue{}, 0, err
	}
	if links {
		failed = CopyLinks(ctx, c, src.Links, types, made.Key)
	}
	return made, failed, nil
}
