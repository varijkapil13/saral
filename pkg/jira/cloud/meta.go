package cloud

import (
	"cmp"
	"context"
	"net/http"
	"strings"

	"github.com/varijkapil13/saral/pkg/jira"
)

// The create screen is read through a pair of endpoints. The single
// ?expand=projects.issuetypes.fields call they replaced is deprecated and
// answers a body this client deliberately cannot decode.
const (
	createMetaPrefix = "/rest/api/3/issue/createmeta/"
	createMetaTypes  = "/issuetypes"
)

// createMetaPageSize is how many entries one page asks for. Both endpoints
// page, and both default to a size the site chooses, so the request says what
// it wants rather than inheriting whatever the site is configured for.
const createMetaPageSize = 50

// CreateMeta reports what a project and issue type require in order to create
// an issue, through the pair of endpoints that replaced the deprecated
// ?expand=projects.issuetypes.fields form of /issue/createmeta.
//
// The issue types the project offers are read first, so that an issue type this
// project does not have comes back named rather than as a 404 on a URL, and so
// that the schema carries the type as the project defines it. The fields for
// that one type are read second, and both walks page.
//
// Nothing about the answer is interpreted here. Which fields exist, which are
// required, what each one may hold and which values it allows are the site's
// own answer, and a form built from it is correct on a site this code has never
// seen — including one whose field, status and priority names are translated,
// since nothing is matched on a display name.
func (c *Client) CreateMeta(ctx context.Context, projectKey, issueTypeID string) (jira.Schema, error) {
	project := strings.TrimSpace(projectKey)
	typeID := strings.TrimSpace(issueTypeID)
	if err := createMetaCheck(project, typeID); err != nil {
		return jira.Schema{}, err
	}

	issueType, err := c.createMetaIssueType(ctx, project, typeID)
	if err != nil {
		return jira.Schema{}, err
	}
	fields, err := c.createMetaFields(ctx, project, typeID)
	if err != nil {
		return jira.Schema{}, err
	}

	schema := jira.Schema{
		Project:   createMetaProject(fields, project),
		IssueType: issueType,
		Fields:    make([]jira.FieldMeta, 0, len(fields)),
	}
	for i := range fields {
		schema.Fields = append(schema.Fields, fields[i].meta)
	}
	return schema, nil
}

// createMetaCheck refuses what would silently become a request for something
// else. A slash in either identifier would move the request to another route.
func createMetaCheck(project, typeID string) error {
	var bad []jira.FieldError
	switch {
	case project == "":
		bad = append(bad, jira.FieldError{Field: "project", Message: "a create screen belongs to a project, so one has to be named"})
	case strings.Contains(project, "/"):
		bad = append(bad, jira.FieldError{Field: "project", Message: "a project key cannot contain a slash"})
	}
	switch {
	case typeID == "":
		bad = append(bad, jira.FieldError{Field: "issuetype", Message: "a create screen belongs to one issue type, so one has to be named by id"})
	case strings.Contains(typeID, "/"):
		bad = append(bad, jira.FieldError{Field: "issuetype", Message: "an issue type id cannot contain a slash"})
	}
	if len(bad) == 0 {
		return nil
	}
	return &jira.ValidationError{Fields: bad}
}

// createMetaIssueType finds one issue type among those the project offers,
// stopping at the page that carries it rather than walking the rest.
func (c *Client) createMetaIssueType(ctx context.Context, project, typeID string) (jira.IssueType, error) {
	path := createMetaPrefix + project + createMetaTypes
	op := http.MethodGet + " " + path
	build := func(startAt int) request {
		return request{
			method: http.MethodGet,
			path:   path,
			query:  pagedQuery(nil, startAt, createMetaPageSize),
			kind:   "project",
			id:     project,
		}
	}
	page, err := offsetPages(ctx, c, build, func(resp *response) ([]jira.IssueType, int, bool, error) {
		var body apiCreateMetaTypes
		if err := resp.decode(op, &body); err != nil {
			return nil, -1, false, err
		}
		out := make([]jira.IssueType, 0, len(body.IssueTypes))
		for _, t := range body.IssueTypes {
			out = append(out, t.domain())
		}
		return out, body.total(), false, nil
	})
	if err != nil {
		return jira.IssueType{}, err
	}
	for {
		for _, t := range page.Items {
			if t.ID == typeID {
				return t, nil
			}
		}
		if !page.HasMore() {
			return jira.IssueType{}, &jira.NotFoundError{Kind: "issue type", ID: typeID + " in project " + project}
		}
		if page, err = page.Next(ctx); err != nil {
			return jira.IssueType{}, err
		}
	}
}

// createMetaFields reads every page of one issue type's create screen.
func (c *Client) createMetaFields(ctx context.Context, project, typeID string) ([]createMetaField, error) {
	path := createMetaPrefix + project + createMetaTypes + "/" + typeID
	op := http.MethodGet + " " + path
	build := func(startAt int) request {
		return request{
			method: http.MethodGet,
			path:   path,
			query:  pagedQuery(nil, startAt, createMetaPageSize),
			kind:   "issue type",
			id:     typeID + " in project " + project,
		}
	}
	page, err := offsetPages(ctx, c, build, func(resp *response) ([]createMetaField, int, bool, error) {
		var body apiCreateMetaFields
		if err := resp.decode(op, &body); err != nil {
			return nil, -1, false, err
		}
		out := make([]createMetaField, 0, len(body.Fields))
		for i := range body.Fields {
			out = append(out, body.Fields[i].domain())
		}
		return out, body.total(), false, nil
	})
	if err != nil {
		return nil, err
	}
	return jira.Collect(ctx, page, 0)
}

// createMetaProject reads the project out of its own field's allowed values,
// which is where the create screen states the id and name. The key is the one
// the screen was asked for, because that is what identifies it.
func createMetaProject(fields []createMetaField, projectKey string) jira.ProjectRef {
	out := jira.ProjectRef{Key: projectKey}
	for i := range fields {
		if fields[i].meta.Field.Schema.System != "project" || len(fields[i].allowed) != 1 {
			continue
		}
		only := fields[i].allowed[0]
		out.ID = string(only.ID)
		out.Name = only.label()
		out.Key = cmp.Or(only.Key, projectKey)
		return out
	}
	return out
}

// createMetaField is one field of a create screen, kept beside the allowed
// values it was decoded from: jira.Option carries an id and a label, and the
// project's own entry also carries the key that names it.
type createMetaField struct {
	meta    jira.FieldMeta
	allowed []apiCreateMetaOption
}

// apiCreateMetaTypes is one page of the issue types a project offers. It pages
// by startAt like the Agile API and names its array issueTypes, so the shared
// envelope in paginate.go cannot read it.
type apiCreateMetaTypes struct {
	Total      *int           `json:"total"`
	IssueTypes []apiIssueType `json:"issueTypes"`
}

func (p apiCreateMetaTypes) total() int {
	if p.Total == nil {
		return -1
	}
	return *p.Total
}

// apiCreateMetaFields is one page of one issue type's fields.
type apiCreateMetaFields struct {
	Total  *int                 `json:"total"`
	Fields []apiCreateMetaField `json:"fields"`
}

func (p apiCreateMetaFields) total() int {
	if p.Total == nil {
		return -1
	}
	return *p.Total
}

type apiCreateMetaField struct {
	Required        bool                  `json:"required"`
	Schema          apiFieldSchema        `json:"schema"`
	Name            string                `json:"name"`
	Key             string                `json:"key"`
	FieldID         string                `json:"fieldId"`
	HasDefaultValue bool                  `json:"hasDefaultValue"`
	Operations      []string              `json:"operations"`
	AllowedValues   []apiCreateMetaOption `json:"allowedValues"`
	AutoCompleteURL string                `json:"autoCompleteUrl"`
}

func (f apiCreateMetaField) domain() createMetaField {
	schema := f.Schema.domain()
	id := cmp.Or(f.FieldID, f.Key)
	meta := jira.FieldMeta{
		Field:           jira.FieldRef{ID: id, Name: f.Name, Schema: schema},
		Name:            f.Name,
		Required:        f.Required,
		HasDefault:      f.HasDefaultValue,
		Operations:      append([]string(nil), f.Operations...),
		AutoCompleteURL: f.AutoCompleteURL,
	}
	for _, allowed := range f.AllowedValues {
		if option, ok := allowed.domain(); ok {
			meta.AllowedValues = append(meta.AllowedValues, option)
		}
	}
	return createMetaField{meta: meta, allowed: f.AllowedValues}
}

// apiCreateMetaOption is an allowed value. A cascading select nests its second
// level under children, which is a different shape from the child a stored
// value of the same field arrives in, so this cannot reuse the option type the
// issue decoder uses.
type apiCreateMetaOption struct {
	ID       flexString            `json:"id"`
	Value    string                `json:"value"`
	Name     string                `json:"name"`
	Key      string                `json:"key"`
	Children []apiCreateMetaOption `json:"children"`
}

// label is whichever spelling arrived: a custom option carries a value, while a
// priority, version, project or issue type carries a name.
func (o apiCreateMetaOption) label() string { return cmp.Or(o.Value, o.Name) }

func (o apiCreateMetaOption) domain() (jira.Option, bool) {
	if o.label() == "" {
		return jira.Option{}, false
	}
	out := jira.Option{ID: string(o.ID), Label: o.label()}
	for _, child := range o.Children {
		if option, ok := child.domain(); ok {
			out.Children = append(out.Children, option)
		}
	}
	return out, true
}
