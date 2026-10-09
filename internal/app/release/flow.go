package release

import (
	"context"
	"errors"

	"github.com/varijkapil13/saral/pkg/jira"
)

// ErrNoTarget is a move of the open issues with nowhere chosen to move them.
var ErrNoTarget = errors.New("nothing has been chosen to move the open issues to")

// Outcome is a version the site answered a release with, read against the
// decision it was released under and the count that decision was made on.
type Outcome struct {
	Version jira.Version
	Policy  jira.UnresolvedPolicy
	Asked   int
}

// Released is whether the site says the version is released. An answer that
// does not may mean it is not.
func (o Outcome) Released() bool { return o.Version.Released }

// Left is how many issues the release left open on the version.
func (o Outcome) Left() int {
	if o.Version.Unresolved == nil {
		return 0
	}
	return *o.Version.Unresolved
}

// Unfinished is a move or a strip that did not reach every issue: the version
// comes back released with a count still on it, a sweep that stopped part way.
func (o Outcome) Unfinished() bool {
	return o.Policy != jira.ReleaseAnyway && o.Left() > 0
}

// Release ships a version under policy. target is the version a move puts the
// open issues on, and asked the count the decision was made against.
func Release(ctx context.Context, w jira.Releaser, id string, policy jira.UnresolvedPolicy, target string, asked int) (Outcome, error) {
	in := jira.ReleaseInput{Unresolved: policy}
	if policy == jira.MoveUnresolved {
		if target == "" {
			return Outcome{}, ErrNoTarget
		}
		in.MoveToVersionID = target
	}
	v, err := w.ReleaseVersion(ctx, id, in)
	if err != nil {
		return Outcome{}, err
	}
	return Outcome{Version: v, Policy: policy, Asked: asked}, nil
}
