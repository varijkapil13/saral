package cloud

import (
	"context"
	"net/http"

	"github.com/varijkapil13/saral/pkg/jira"
)

const fieldPath = "/rest/api/3/field"

// Fields returns the site's field catalogue.
//
// It is what turns a field written down by name — in a profile, a saved query, a
// board's estimation setting — into the ID to ask this site for. A custom field
// ID is site-specific, so there is no other way to reach one that does not mean
// writing a customfield_NNNNN into the source and being wrong on every other
// site.
//
// The endpoint returns every field the site defines in one unpaged array, which
// on a large site is a few hundred entries. Callers cache it; it changes when
// somebody edits the site's configuration.
func (c *Client) Fields(ctx context.Context) ([]jira.Field, error) {
	var body []apiField
	err := c.doJSON(ctx, request{
		method: http.MethodGet,
		path:   fieldPath,
		kind:   "the field catalogue",
		id:     fieldPath,
	}, &body)
	if err != nil {
		return nil, err
	}
	out := make([]jira.Field, len(body))
	for i := range body {
		out[i] = body[i].domain()
	}
	return out, nil
}

type apiField struct {
	ID               string         `json:"id"`
	Key              string         `json:"key"`
	Name             string         `json:"name"`
	UntranslatedName string         `json:"untranslatedName"`
	Custom           bool           `json:"custom"`
	Navigable        bool           `json:"navigable"`
	Searchable       bool           `json:"searchable"`
	Orderable        bool           `json:"orderable"`
	ClauseNames      []string       `json:"clauseNames"`
	Schema           apiFieldSchema `json:"schema"`
}

func (f apiField) domain() jira.Field {
	return jira.Field{
		ID:               f.ID,
		Key:              f.Key,
		Name:             f.Name,
		UntranslatedName: f.UntranslatedName,
		Custom:           f.Custom,
		Navigable:        f.Navigable,
		Searchable:       f.Searchable,
		Orderable:        f.Orderable,
		ClauseNames:      f.ClauseNames,
		Schema:           f.Schema.domain(),
	}
}
