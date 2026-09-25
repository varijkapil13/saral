package main

import (
	"bytes"
	"encoding/json"
	"io"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/varijkapil13/saral/internal/app"
	"github.com/varijkapil13/saral/internal/ui/kernel"
	"github.com/varijkapil13/saral/internal/ui/widget"
	"github.com/varijkapil13/saral/pkg/adf"
	"github.com/varijkapil13/saral/pkg/jira"
)

// present is omitted when its field was not read and null when it was read empty.
type present[T any] struct {
	asked bool
	value *T
}

func some[T any](v T) present[T] { return present[T]{asked: true, value: &v} }

func null[T any]() present[T] { return present[T]{asked: true} }

func (p present[T]) IsZero() bool { return !p.asked }

func (p present[T]) MarshalJSON() ([]byte, error) {
	if p.value == nil {
		return []byte("null"), nil
	}
	return marshalJSON(*p.value)
}

type projectJSON struct {
	Key  string `json:"key"`
	Name string `json:"name"`
}

type typeJSON struct {
	ID      string `json:"id"`
	Name    string `json:"name"`
	Subtask bool   `json:"subtask"`
}

type statusJSON struct {
	ID       string `json:"id"`
	Name     string `json:"name"`
	Category string `json:"category"`
}

type refJSON struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

type userJSON struct {
	AccountID   string `json:"accountId"`
	DisplayName string `json:"displayName"`
	Email       string `json:"email"`
}

type issueRefJSON struct {
	Key     string `json:"key"`
	Summary string `json:"summary"`
	Status  string `json:"status"`
}

type linkJSON struct {
	Type  string `json:"type"`
	Label string `json:"label"`
	Key   string `json:"key"`
}

type timeTrackingJSON struct {
	OriginalEstimate  int64 `json:"originalEstimate"`
	RemainingEstimate int64 `json:"remainingEstimate"`
	TimeSpent         int64 `json:"timeSpent"`
}

type optionJSON struct {
	ID       string       `json:"id"`
	Label    string       `json:"label"`
	Children []optionJSON `json:"children,omitempty"`
}

type customJSON struct {
	Name  string `json:"name"`
	Value any    `json:"value"`
}

type issueJSON struct {
	Key          string                    `json:"key"`
	ID           string                    `json:"id"`
	URL          string                    `json:"url"`
	Project      present[projectJSON]      `json:"project,omitzero"`
	Type         present[typeJSON]         `json:"type,omitzero"`
	Status       present[statusJSON]       `json:"status,omitzero"`
	Summary      present[string]           `json:"summary,omitzero"`
	Priority     present[refJSON]          `json:"priority,omitzero"`
	Resolution   present[refJSON]          `json:"resolution,omitzero"`
	Assignee     present[userJSON]         `json:"assignee,omitzero"`
	Reporter     present[userJSON]         `json:"reporter,omitzero"`
	Labels       present[[]string]         `json:"labels,omitzero"`
	Components   present[[]string]         `json:"components,omitzero"`
	FixVersions  present[[]string]         `json:"fixVersions,omitzero"`
	Parent       present[issueRefJSON]     `json:"parent,omitzero"`
	Subtasks     present[[]issueRefJSON]   `json:"subtasks,omitzero"`
	Links        present[[]linkJSON]       `json:"links,omitzero"`
	Due          present[string]           `json:"due,omitzero"`
	Created      present[string]           `json:"created,omitzero"`
	Updated      present[string]           `json:"updated,omitzero"`
	Resolved     present[string]           `json:"resolved,omitzero"`
	TimeTracking present[timeTrackingJSON] `json:"timeTracking,omitzero"`
	Description  present[string]           `json:"description,omitzero"`
	Fields       map[string]customJSON     `json:"fields,omitempty"`
}

type platformField struct {
	id, name string
	json     func(*issueJSON, *jira.Issue)
	text     func(*jira.Issue) string
}

var platformFields = []platformField{
	{id: "project", name: "project",
		json: func(o *issueJSON, i *jira.Issue) {
			o.Project = some(projectJSON{Key: i.Project.Key, Name: i.Project.Name})
		},
		text: func(i *jira.Issue) string { return i.Project.Key }},
	{id: "issuetype", name: "type",
		json: func(o *issueJSON, i *jira.Issue) {
			o.Type = some(typeJSON{ID: i.Type.ID, Name: i.Type.Name, Subtask: i.Type.Subtask})
		},
		text: func(i *jira.Issue) string { return i.Type.Name }},
	{id: "status", name: "status",
		json: func(o *issueJSON, i *jira.Issue) {
			o.Status = some(statusJSON{ID: i.Status.ID, Name: i.Status.Name, Category: categoryKey(i.Status.Category)})
		},
		text: func(i *jira.Issue) string { return i.Status.Name }},
	{id: "summary", name: "summary",
		json: func(o *issueJSON, i *jira.Issue) { o.Summary = some(i.Summary) },
		text: func(i *jira.Issue) string { return i.Summary }},
	{id: "priority", name: "priority",
		json: func(o *issueJSON, i *jira.Issue) {
			o.Priority = null[refJSON]()
			if i.Priority != nil {
				o.Priority = some(refJSON{ID: i.Priority.ID, Name: i.Priority.Name})
			}
		},
		text: func(i *jira.Issue) string {
			if i.Priority == nil {
				return ""
			}
			return i.Priority.Name
		}},
	{id: "resolution", name: "resolution",
		json: func(o *issueJSON, i *jira.Issue) {
			o.Resolution = null[refJSON]()
			if i.Resolution != nil {
				o.Resolution = some(refJSON{ID: i.Resolution.ID, Name: i.Resolution.Name})
			}
		},
		text: func(i *jira.Issue) string {
			if i.Resolution == nil {
				return ""
			}
			return i.Resolution.Name
		}},
	{id: "assignee", name: "assignee",
		json: func(o *issueJSON, i *jira.Issue) { o.Assignee = userOrNull(i.Assignee) },
		text: func(i *jira.Issue) string { return userName(i.Assignee) }},
	{id: "reporter", name: "reporter",
		json: func(o *issueJSON, i *jira.Issue) { o.Reporter = userOrNull(i.Reporter) },
		text: func(i *jira.Issue) string { return userName(i.Reporter) }},
	{id: "labels", name: "labels",
		json: func(o *issueJSON, i *jira.Issue) { o.Labels = some(append([]string{}, i.Labels...)) },
		text: func(i *jira.Issue) string { return strings.Join(i.Labels, ", ") }},
	{id: "components", name: "components",
		json: func(o *issueJSON, i *jira.Issue) { o.Components = some(componentNames(i.Components)) },
		text: func(i *jira.Issue) string { return strings.Join(componentNames(i.Components), ", ") }},
	{id: "fixVersions", name: "fixVersions",
		json: func(o *issueJSON, i *jira.Issue) { o.FixVersions = some(versionNames(i.FixVersions)) },
		text: func(i *jira.Issue) string { return strings.Join(versionNames(i.FixVersions), ", ") }},
	{id: "parent", name: "parent",
		json: func(o *issueJSON, i *jira.Issue) {
			o.Parent = null[issueRefJSON]()
			if i.Parent != nil {
				o.Parent = some(refOf(*i.Parent))
			}
		},
		text: func(i *jira.Issue) string {
			if i.Parent == nil {
				return ""
			}
			return i.Parent.Key
		}},
	{id: "subtasks", name: "subtasks",
		json: func(o *issueJSON, i *jira.Issue) {
			out := make([]issueRefJSON, len(i.Subtasks))
			for n := range i.Subtasks {
				out[n] = refOf(i.Subtasks[n])
			}
			o.Subtasks = some(out)
		},
		text: func(i *jira.Issue) string {
			keys := make([]string, len(i.Subtasks))
			for n := range i.Subtasks {
				keys[n] = i.Subtasks[n].Key
			}
			return strings.Join(keys, ", ")
		}},
	{id: "issuelinks", name: "links",
		json: func(o *issueJSON, i *jira.Issue) {
			out := make([]linkJSON, len(i.Links))
			for n, l := range i.Links {
				out[n] = linkJSON{Type: l.Type, Label: l.Label, Key: l.Other.Key}
			}
			o.Links = some(out)
		},
		text: func(i *jira.Issue) string {
			parts := make([]string, len(i.Links))
			for n, l := range i.Links {
				parts[n] = l.Label + " " + l.Other.Key
			}
			return strings.Join(parts, ", ")
		}},
	{id: "duedate", name: "due",
		json: func(o *issueJSON, i *jira.Issue) { o.Due = stringOrNull(i.Due.String()) },
		text: func(i *jira.Issue) string { return i.Due.String() }},
	{id: "created", name: "created",
		json: func(o *issueJSON, i *jira.Issue) { o.Created = stringOrNull(stamp3339(i.Created)) },
		text: func(i *jira.Issue) string { return stamp3339(i.Created) }},
	{id: "updated", name: "updated",
		json: func(o *issueJSON, i *jira.Issue) { o.Updated = stringOrNull(stamp3339(i.Updated)) },
		text: func(i *jira.Issue) string { return stamp3339(i.Updated) }},
	{id: "resolutiondate", name: "resolved",
		json: func(o *issueJSON, i *jira.Issue) { o.Resolved = stringOrNull(resolvedAt(i)) },
		text: resolvedAt},
	{id: "timetracking", name: "timeTracking",
		json: func(o *issueJSON, i *jira.Issue) {
			o.TimeTracking = null[timeTrackingJSON]()
			if t := i.TimeTracking; t != nil {
				o.TimeTracking = some(timeTrackingJSON{OriginalEstimate: t.OriginalEstimate, RemainingEstimate: t.RemainingEstimate, TimeSpent: t.TimeSpent})
			}
		},
		text: func(i *jira.Issue) string {
			t := i.TimeTracking
			if t == nil {
				return ""
			}
			return "original " + strconv.FormatInt(t.OriginalEstimate, 10) + "s, remaining " +
				strconv.FormatInt(t.RemainingEstimate, 10) + "s, spent " + strconv.FormatInt(t.TimeSpent, 10) + "s"
		}},
	{id: "description", name: "description",
		json: func(o *issueJSON, i *jira.Issue) { o.Description = stringOrNull(descriptionText(i.Description)) },
		text: func(i *jira.Issue) string { return descriptionText(i.Description) }},
}

func platformFieldByID(id string) (platformField, bool) {
	at := slices.IndexFunc(platformFields, func(f platformField) bool { return f.id == id })
	if at < 0 {
		return platformField{}, false
	}
	return platformFields[at], true
}

type issueView struct {
	issue  *jira.Issue
	asked  []string
	labels app.FieldLabels
	site   string
}

func (v issueView) json() issueJSON {
	i := v.issue
	out := issueJSON{Key: i.Key, ID: i.ID, URL: issueURL(v.site, i.Key)}
	for _, id := range v.asked {
		if p, ok := platformFieldByID(id); ok {
			p.json(&out, i)
			continue
		}
		value, ok := i.Fields.ByID(id)
		if !ok || value.Kind == jira.KindEmpty {
			continue
		}
		if out.Fields == nil {
			out.Fields = make(map[string]customJSON)
		}
		out.Fields[id] = customJSON{Name: v.fieldName(id), Value: valueJSON(value)}
	}
	return out
}

func (v issueView) text(id string) string {
	if p, ok := platformFieldByID(id); ok {
		return p.text(v.issue)
	}
	value, ok := v.issue.Fields.ByID(id)
	if !ok {
		return ""
	}
	return valueText(value)
}

func (v issueView) fieldName(id string) string {
	if name := v.labels.Name(id); name != "" {
		return name
	}
	return id
}

func issueURL(site, key string) string {
	link, err := kernel.IssueURL(site, key)
	if err != nil {
		return ""
	}
	return link
}

func categoryKey(c jira.StatusCategory) string {
	switch c {
	case jira.CategoryToDo:
		return "new"
	case jira.CategoryInProgress:
		return "indeterminate"
	case jira.CategoryDone:
		return "done"
	default:
		return "unknown"
	}
}

func userOf(u *jira.User) userJSON {
	return userJSON{AccountID: u.AccountID, DisplayName: u.DisplayName, Email: u.Email}
}

func userOrNull(u *jira.User) present[userJSON] {
	if u == nil {
		return null[userJSON]()
	}
	return some(userOf(u))
}

func userName(u *jira.User) string {
	if u == nil {
		return ""
	}
	return u.DisplayName
}

func componentNames(cs []jira.Component) []string {
	out := make([]string, len(cs))
	for i := range cs {
		out[i] = cs[i].Name
	}
	return out
}

func versionNames(vs []jira.Version) []string {
	out := make([]string, len(vs))
	for i := range vs {
		out[i] = vs[i].Name
	}
	return out
}

func refOf(r jira.IssueRef) issueRefJSON {
	return issueRefJSON{Key: r.Key, Summary: r.Summary, Status: r.Status.Name}
}

func stringOrNull(s string) present[string] {
	if s == "" {
		return null[string]()
	}
	return some(s)
}

func stamp3339(t time.Time) string {
	if t.IsZero() {
		return ""
	}
	return t.UTC().Format(time.RFC3339)
}

func resolvedAt(i *jira.Issue) string {
	if i.Resolved == nil {
		return ""
	}
	return stamp3339(*i.Resolved)
}

func descriptionText(d adf.Doc) string { return strings.TrimRight(adf.Markdown(d), "\n") }

func optionOf(o jira.Option) optionJSON {
	out := optionJSON{ID: o.ID, Label: o.Label}
	for _, c := range o.Children {
		out.Children = append(out.Children, optionOf(c))
	}
	return out
}

func valueJSON(v jira.FieldValue) any {
	switch v.Kind {
	case jira.KindText:
		return v.Text
	case jira.KindNumber:
		return v.Number
	case jira.KindBool:
		return v.Bool
	case jira.KindDate:
		return v.Date.String()
	case jira.KindTime:
		return stamp3339(v.Time)
	case jira.KindDoc:
		return descriptionText(v.Doc)
	case jira.KindOption:
		if len(v.Options) == 0 {
			return nil
		}
		return optionOf(v.Options[0])
	case jira.KindOptions:
		out := make([]optionJSON, len(v.Options))
		for i := range v.Options {
			out[i] = optionOf(v.Options[i])
		}
		return out
	case jira.KindUser:
		if len(v.Users) == 0 {
			return nil
		}
		return userOf(&v.Users[0])
	case jira.KindUsers:
		out := make([]userJSON, len(v.Users))
		for i := range v.Users {
			out[i] = userOf(&v.Users[i])
		}
		return out
	case jira.KindUnknown:
		if json.Valid([]byte(v.Text)) {
			return json.RawMessage(v.Text)
		}
		return v.Text
	default:
		return nil
	}
}

func valueText(v jira.FieldValue) string {
	switch v.Kind {
	case jira.KindText, jira.KindUnknown:
		return v.Text
	case jira.KindNumber:
		return strconv.FormatFloat(v.Number, 'f', -1, 64)
	case jira.KindBool:
		return strconv.FormatBool(v.Bool)
	case jira.KindDate:
		return v.Date.String()
	case jira.KindTime:
		return stamp3339(v.Time)
	case jira.KindDoc:
		return descriptionText(v.Doc)
	case jira.KindOption, jira.KindOptions:
		labels := make([]string, 0, len(v.Options))
		for _, o := range v.Options {
			labels = append(labels, optionLabel(o))
		}
		return strings.Join(labels, ", ")
	case jira.KindUser, jira.KindUsers:
		names := make([]string, len(v.Users))
		for i := range v.Users {
			names[i] = v.Users[i].DisplayName
		}
		return strings.Join(names, ", ")
	default:
		return ""
	}
}

func optionLabel(o jira.Option) string {
	if len(o.Children) == 0 {
		return o.Label
	}
	return o.Label + " / " + optionLabel(o.Children[0])
}

func marshalJSON(v any) ([]byte, error) {
	var b bytes.Buffer
	enc := json.NewEncoder(&b)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(v); err != nil {
		return nil, err
	}
	return bytes.TrimRight(b.Bytes(), "\n"), nil
}

func writeJSON(w io.Writer, v any) error {
	enc := json.NewEncoder(w)
	enc.SetEscapeHTML(false)
	enc.SetIndent("", "  ")
	return enc.Encode(v)
}

var lineBreaks = strings.NewReplacer("\r\n", " ", "\n", " ", "\r", " ")

func cell(s string) string { return widget.Sanitize(lineBreaks.Replace(s)) }

func writeRow(w io.Writer, cells ...string) error {
	for i := range cells {
		cells[i] = cell(cells[i])
	}
	_, err := io.WriteString(w, strings.Join(cells, "\t")+"\n")
	return err
}
