package cloud

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"

	"github.com/varijkapil13/saral/pkg/jira"
)

const editMetaSuffix = "/editmeta"

// EditMeta reads the fields on one issue's screen right now: the editable
// ones, resolved through the screen scheme, the field configuration and each
// custom field's context.
//
// The endpoint's fields object has the same per-field shape a transition
// screen sends — required, schema, name, key or fieldId, operations,
// allowedValues — so this decodes each entry with apiTransitionField rather
// than a shape of its own.
func (c *Client) EditMeta(ctx context.Context, key string) (jira.EditMeta, error) {
	id, err := issueKey(key)
	if err != nil {
		return jira.EditMeta{}, err
	}
	r := request{
		method: http.MethodGet,
		path:   issuePath + "/" + id + editMetaSuffix,
		kind:   "issue",
		id:     id,
	}
	resp, err := c.do(ctx, r)
	if err != nil {
		return jira.EditMeta{}, err
	}
	var envelope struct {
		Fields json.RawMessage `json:"fields"`
	}
	if err := resp.decode(r.op(), &envelope); err != nil {
		return jira.EditMeta{}, err
	}
	fields, err := decodeEditMetaFields(envelope.Fields)
	if err != nil {
		return jira.EditMeta{}, &jira.TransportError{
			Op: r.op(), Status: resp.status,
			Err: fmt.Errorf("the fields object is not the JSON this client expected: %w", err),
		}
	}
	return jira.EditMeta{Fields: fields}, nil
}

// decodeEditMetaFields reads the fields object key by key, in the order Jira
// sent it: a screen's own field order is drawn from that order, and decoding
// into a map — the way every other field lookup in this package does — would
// throw it away.
func decodeEditMetaFields(raw json.RawMessage) ([]jira.FieldMeta, error) {
	trimmed := bytes.TrimSpace(raw)
	if len(trimmed) == 0 || string(trimmed) == "null" {
		return nil, nil
	}
	if trimmed[0] != '{' {
		return nil, errors.New("fields is not a JSON object")
	}
	dec := json.NewDecoder(bytes.NewReader(trimmed))
	if _, err := dec.Token(); err != nil {
		return nil, err
	}
	var out []jira.FieldMeta
	for dec.More() {
		keyToken, err := dec.Token()
		if err != nil {
			return nil, err
		}
		mapKey, _ := keyToken.(string)
		var f apiTransitionField
		if err := dec.Decode(&f); err != nil {
			return nil, err
		}
		out = append(out, f.domain(mapKey))
	}
	return out, nil
}
