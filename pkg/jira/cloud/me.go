package cloud

import (
	"context"
	"errors"
	"net/http"

	"github.com/varijkapil13/saral/pkg/jira"
)

// Me returns the account the token authenticates as.
//
// It is the cheapest proof that a site, an email and a token belong together,
// which is why onboarding asks it first, and the account it names is the one a
// create screen offers as the default assignee.
//
// The capability probe reads the same endpoint and keeps its own decode. It owes
// the user a sentence about a zone it could not load; here the same zone is a
// rendering detail on an otherwise complete account and is dropped silently. One
// decode cannot do both without being told which was wanted.
func (c *Client) Me(ctx context.Context) (jira.User, error) {
	r := request{
		method: http.MethodGet,
		path:   capsMyselfPath,
		kind:   "account",
		id:     "the authenticated account",
	}
	var body apiUser
	if err := c.doJSON(ctx, r, &body); err != nil {
		return jira.User{}, err
	}
	// Onboarding reads a successful answer here as proof the credentials go
	// together, so a 200 that names nobody must not read as one.
	if body.AccountID == "" {
		return jira.User{}, &jira.TransportError{
			Op:     r.op(),
			Status: http.StatusOK,
			Err:    errors.New("the answer names no account"),
		}
	}
	return body.domain(), nil
}
