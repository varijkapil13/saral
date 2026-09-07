package main

import (
	"github.com/varijkapil13/saral/internal/config"
	"github.com/varijkapil13/saral/internal/ui/kernel"
)

// profileMemory is kernel.Memory backed by the cache directory's own
// ui.toml, scoped to one profile's site and account so a filter's ids are
// only ever read back for the token that could resolve them — see
// config.ProfileScope.
type profileMemory struct{ scope config.ProfileScope }

// newMemory builds a session's memory, scoped the same site-and-account way
// openCache scopes the cache.
func newMemory(site, account string) kernel.Memory {
	return profileMemory{scope: config.ProfileScope{Site: site, Account: account}}
}

func (m profileMemory) Recall(view, key string) (string, bool) {
	return config.RememberedState(m.scope, view, key)
}

// Keep takes no error: a session with nowhere to write remembers nothing for
// the next one, the same tolerance LoadUIState already gives a first run.
func (m profileMemory) Keep(view, key, value string) {
	_ = config.RememberState(m.scope, view, key, value)
}

func (m profileMemory) Forget() { _ = config.ForgetRemembered(m.scope) }

var _ kernel.Memory = profileMemory{}
