package issue

import (
	"context"
	"strconv"

	tea "charm.land/bubbletea/v2"

	"github.com/varijkapil13/saral/internal/ui/kernel"
	"github.com/varijkapil13/saral/internal/ui/widget"
	"github.com/varijkapil13/saral/pkg/jira"
)

const clonePrefix = "CLONE - "

type cloneKind struct {
	src   jira.Issue
	in    jira.IssueInput
	types []jira.LinkType
}

var cloneKeys = newSheetKeys(sheetBind{kernel.Bind([]string{"enter", "a"}, "enter", "clone it"), sheetAdd})

func (k *cloneKind) keys() *sheetKeys { return cloneKeys }

func (k *cloneKind) load(s *sheet) tea.Cmd {
	key := s.key
	return s.read(&s.loads, func(ctx context.Context, c jira.SessionClient) func(*sheet) tea.Cmd {
		src, err := c.Issue(ctx, key)
		var schema jira.Schema
		if err == nil {
			schema, err = c.CreateMeta(ctx, src.Project.Key, src.Type.ID)
		}
		var types []jira.LinkType
		if err == nil && len(src.Links) > 0 {
			types, _ = c.IssueLinkTypes(ctx)
		}
		return func(s *sheet) tea.Cmd {
			if err != nil {
				return s.failed(err)
			}
			var names []string
			k.src, k.types = src, types
			k.in, names = cloneInput(src, schema)
			rows := []sheetRow{{text: "Carried over", head: true}}
			for _, n := range names {
				rows = append(rows, sheetRow{text: widget.Sanitize(n)})
			}
			s.setRows(rows)
			s.note = "into " + src.Project.Key + " as " + widget.Sanitize(src.Type.Name)
			return k.act(s, sheetAdd)
		}
	})
}

// cloneInput is the copy's create request, and the names of what it carries.
// Only fields the create screen names go in: anything else is refused by the
// site, and the rest of the copy with it.
func cloneInput(src jira.Issue, schema jira.Schema) (in jira.IssueInput, names []string) {
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

func (k *cloneKind) act(s *sheet, a sheetAct) tea.Cmd {
	if a != sheetAdd || k.src.Key == "" {
		return nil
	}
	return s.ask("Summary of the copy", clonePrefix+k.src.Summary, false)
}

func (k *cloneKind) changed(*sheet, string) tea.Cmd { return nil }

func (k *cloneKind) answered(s *sheet, text string, _ *sheetRow) tea.Cmd {
	if text == "" {
		s.problem = "the copy needs a summary"
		return nil
	}
	s.endAsk()
	in := k.in
	in.Summary = text
	if len(k.src.Links) == 0 || len(k.types) == 0 {
		return k.create(s, in, false)
	}
	s.confirm("Copy its "+count(len(k.src.Links), "link")+" as well?",
		func() tea.Cmd { return k.create(s, in, true) },
		func() tea.Cmd { return k.create(s, in, false) })
	return nil
}

func (k *cloneKind) create(s *sheet, in jira.IssueInput, links bool) tea.Cmd {
	src, types, d := k.src, k.types, s.deps
	return s.write(func(ctx context.Context, c jira.SessionClient) (func(*sheet) tea.Cmd, error) {
		made, err := c.CreateIssue(ctx, in)
		if err != nil {
			return nil, err
		}
		said := "cloned " + src.Key + " as " + made.Key
		if links {
			if failed := copyLinks(ctx, c, src.Links, types, made.Key); failed > 0 {
				said += "; " + strconv.Itoa(failed) + " of its links could not be copied"
			}
		}
		return func(*sheet) tea.Cmd {
			return tea.Sequence(kernel.Pop(), openIssue(d, jira.IssueRef{ID: made.ID, Key: made.Key, Summary: made.Summary}), kernel.Status(said))
		}, nil
	})
}

func copyLinks(ctx context.Context, c jira.Linker, links []jira.IssueLink, types []jira.LinkType, to string) int {
	failed := 0
	for i := range links {
		l := &links[i]
		in := jira.LinkInput{From: to, To: l.Other.Key}
		if l.Direction == jira.LinkInward {
			in.From, in.To = l.Other.Key, to
		}
		for _, t := range types {
			if t.Name == l.Type {
				in.TypeID = t.ID
			}
		}
		if in.TypeID == "" || c.LinkIssues(ctx, in) != nil {
			failed++
		}
	}
	return failed
}
