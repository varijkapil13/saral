package connect

import (
	"context"
	"errors"

	appcache "github.com/varijkapil13/saral/internal/app/cache"
	"github.com/varijkapil13/saral/pkg/jira"
)

// ErrNoClient is a probe asked of a session with no Jira connection.
var ErrNoClient = errors.New("there is no Jira connection to ask what this token can do")

// Caps asks the site what a token may do in a project, and keeps the answer for
// the next run when the session has somewhere to keep it.
type Caps struct {
	prober jira.Prober
	store  appcache.CapsCache
}

// NewCaps builds the probe over a client and a cache, either of which may be nil.
func NewCaps(prober jira.Prober, cache appcache.Cache) Caps {
	store, _ := cache.(appcache.CapsCache)
	return Caps{prober: prober, store: store}
}

// CanProbe reports whether there is a client to ask.
func (c Caps) CanProbe() bool { return c.prober != nil }

// Probe asks what the token may do in project. A failure is an error and never
// an all-negative answer, which is what makes a stored answer safe to keep.
func (c Caps) Probe(ctx context.Context, project string) (jira.Capabilities, error) {
	if c.prober == nil {
		return jira.Capabilities{}, ErrNoClient
	}
	return c.prober.Capabilities(ctx, project)
}

// Stored is what the last run learnt about project, whatever its age.
func (c Caps) Stored(project string) (appcache.CapsSnapshot, bool) {
	if c.store == nil {
		return appcache.CapsSnapshot{}, false
	}
	return c.store.Caps(project)
}

// Keeps reports whether there is anywhere to keep an answer.
func (c Caps) Keeps() bool { return c.store != nil }

// Keep stores a probe answer for project. With nowhere to keep it, it does
// nothing.
func (c Caps) Keep(project string, caps jira.Capabilities) error {
	if c.store == nil {
		return nil
	}
	return c.store.PutCaps(project, caps)
}
